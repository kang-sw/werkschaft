package wsholder

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Stopping holders: Stop, StopAll, and the checks that a process is still its holder.

// Stop ends holder id and its child. An attached holder is asked through
// the protocol. One the daemon cannot attach to is killed with its process
// group (POSIX) or process tree (Windows), but only while the process under its PID still matches the start
// time its descriptor records, which guards against a reused PID;
// ErrStartTimeMismatch refuses the kill otherwise.
func (m *Manager) Stop(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.stopHolder(id)
}

// stopHolder is Stop under opMu. Stops of different holders may run at
// once (StopAll): each touches only its own entry under mu.
func (m *Manager) stopHolder(id string) error {
	m.mu.Lock()
	e := m.entries[id]
	incomplete := m.incomplete
	var desc Descriptor
	var client *Client
	if e != nil {
		desc, client = e.desc, e.client
	}
	m.mu.Unlock()
	if e == nil {
		if incomplete {
			return ErrUnknownHolderIncomplete
		}
		return ErrUnknownHolder
	}
	if client != nil {
		if err := client.Stop(); err == nil {
			select {
			case <-client.Done():
			case <-time.After(stopWait):
				client.Close()
			}
			if m.waitGone(desc.PID) {
				if err := m.cleanup(desc, "stop"); err != nil {
					return err
				}
				return nil
			}
		}
		// The holder did not stop through the protocol; fall through.
	}
	if !m.opts.alive(desc.PID) {
		return m.cleanup(desc, "unknown")
	}
	if err := m.sameProcess(desc); err != nil {
		return err
	}
	if err := m.opts.kill(desc.PID); err != nil {
		m.logf("kill holder %s (pid %d): %v", id, desc.PID, err)
	}
	if !m.waitGone(desc.PID) {
		return fmt.Errorf("holder process %d did not end", desc.PID)
	}
	if err := m.cleanup(desc, "killed"); err != nil {
		return err
	}
	m.logf("killed holder %s (pid %d) and its child", id, desc.PID)
	return nil
}

// stopAllRounds bounds StopAll's rescans for holders that appeared while
// it stopped the ones it knew.
const stopAllRounds = 3

// StopFailure is a holder StopAll left running, and why.
type StopFailure struct {
	Info
	Error string `json:"error"`
}

// StopAllResult is StopAll's outcome: the holders it ended, and every
// holder whose process still runs afterwards. An empty Remaining is a
// complete stop.
type StopAllResult struct {
	Stopped   []string      `json:"stopped"`
	Remaining []StopFailure `json:"remaining"`
}

// StopAll ends every holder and its child, for a full quit. From its call
// no launch begins (ErrStopping), including one already queued behind
// opMu; a launch already running finishes first, since both hold opMu, so
// its holder is stopped too. Each round
// scans the holders directory, so a holder this manager is not attached to
// is found as well, and stops every holder not tried yet at once (Stop's
// protocol stop, or its guarded kill); a later round takes only the
// holders that appeared meanwhile. Remaining is then read from the
// directory and the process table, not from memory: every holder whose
// process still lives, with its stop's error when one was returned. After
// a complete stop launches stay refused, since the caller closes the
// manager next; with any holder remaining they are allowed again.
func (m *Manager) StopAll() StopAllResult {
	// Set before the wait, so a launch queued behind a running one is
	// refused, and again after it: a concurrent StopAll that left holders
	// may have allowed launches meanwhile.
	m.setStopping(true)
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.setStopping(true)
	tried := map[string]error{}
	for round := 0; round < stopAllRounds; round++ {
		m.scan()
		var ids []string
		m.mu.Lock()
		for id := range m.entries {
			if _, done := tried[id]; !done {
				ids = append(ids, id)
			}
		}
		m.mu.Unlock()
		if len(ids) == 0 {
			break
		}
		sort.Strings(ids)
		errs := make([]error, len(ids))
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = m.stopHolder(id)
			}()
		}
		wg.Wait()
		for i, id := range ids {
			tried[id] = errs[i]
			if errs[i] != nil {
				m.logf("full stop: holder %s: %v", id, errs[i])
			}
		}
	}
	result := StopAllResult{Stopped: []string{}, Remaining: []StopFailure{}}
	left := map[string]bool{}
	for _, info := range m.survivors() {
		failure := StopFailure{Info: info, Error: "the holder still runs after the stop"}
		if err := tried[info.ID]; err != nil {
			failure.Error = err.Error()
		}
		left[info.ID] = true
		result.Remaining = append(result.Remaining, failure)
	}
	// A holder whose process is gone is stopped even when its stop reported
	// an error after the end (its exit record or directory cleanup failed,
	// logged above): the survivors, not the errors, decide what remains.
	for id := range tried {
		if !left[id] {
			result.Stopped = append(result.Stopped, id)
		}
	}
	sort.Strings(result.Stopped)
	if len(result.Remaining) > 0 {
		m.setStopping(false)
		m.logf("full stop incomplete: %d holder(s) remain", len(result.Remaining))
	}
	return result
}

func (m *Manager) setStopping(on bool) {
	m.mu.Lock()
	m.stopping = on
	m.mu.Unlock()
}

// survivors are the holders whose process lives, by id: every known entry
// and every descriptor in the holders directory whose PID is alive. It
// reads the directory itself, so a closed manager (whose scan does
// nothing) still answers truthfully.
func (m *Manager) survivors() []Info {
	byID := map[string]Info{}
	m.mu.Lock()
	for id, e := range m.entries {
		byID[id] = e.info()
	}
	m.mu.Unlock()
	dirs, err := os.ReadDir(m.opts.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logf("full stop: read %s: %v", m.opts.Dir, err)
	}
	for _, d := range dirs {
		id := d.Name()
		if _, known := byID[id]; known || !d.IsDir() || !ValidID(id) {
			continue
		}
		if desc, err := ReadDescriptor(filepath.Join(m.opts.Dir, id)); err == nil {
			byID[id] = (&entry{desc: desc}).info()
		}
	}
	out := []Info{}
	for _, info := range byID {
		if m.opts.alive(info.PID) {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// sameProcess returns nil when the process under desc.PID is the holder
// that wrote desc, and the reason a kill is refused otherwise. The boot-clock
// start decides where the holder recorded one and the OS reports one; the
// wall-clock start, within startTimeTolerance, decides everywhere else.
func (m *Manager) sameProcess(desc Descriptor) error {
	if desc.StartTicks != 0 {
		ticks, err := m.opts.procTicks(desc.PID)
		if err != nil {
			return fmt.Errorf("the start time of process %d could not be read, so it was not killed: %w", desc.PID, err)
		}
		if ticks != 0 {
			if ticks != desc.StartTicks {
				return ErrStartTimeMismatch
			}
			return nil
		}
	}
	started, err := m.opts.procStart(desc.PID)
	if err != nil {
		return fmt.Errorf("the start time of process %d could not be read, so it was not killed: %w", desc.PID, err)
	}
	recorded := time.Unix(0, int64(desc.StartedAt*float64(time.Second)))
	if diff := started.Sub(recorded); math.Abs(float64(diff)) > float64(startTimeTolerance) {
		return ErrStartTimeMismatch
	}
	return nil
}

// waitGone waits for pid to end, up to stopWait.
func (m *Manager) waitGone(pid int) bool {
	deadline := time.Now().Add(stopWait)
	for m.opts.alive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
	return true
}
