package wsholder

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Starting a holder: the interpreter, LaunchSpec, Launch, and pruning staged scripts.

// python resolves the interpreter a holder runs under.
func (m *Manager) python() (string, error) {
	if m.opts.Python != "" {
		return m.opts.Python, nil
	}
	path, err := m.opts.lookPath("python3")
	if err != nil {
		return "", fmt.Errorf("no interpreter for the session holder: %s is unset and python3 is not on PATH: %w", PythonEnv, err)
	}
	return path, nil
}

// LaunchSpec is one holder start.
type LaunchSpec struct {
	// Session is a JSON object the holder records in its descriptor, nil
	// for none.
	Session json.RawMessage
	// RingBytes is the retained size of this holder's output ring; zero is
	// Options.RingBytes.
	RingBytes int
	// RingCeilingBytes caps the ring with output waiting to be sent. Zero
	// keeps the ring fixed at its retained size.
	RingCeilingBytes int
	// RingSkip is a regular expression (the syntax Go and Python share):
	// the pipe child's whole stdout lines it matches from their start stay
	// out of the ring, so they take no sequence and reach no client. Empty
	// keeps every line.
	RingSkip string
	// RingPulse is a content-free line emitted at a bounded rate while
	// RingSkip drops matching lines. Empty emits nothing.
	RingPulse string
	// ExitCopy is a file that also receives the holder's exit record (the
	// exit.json object) when the holder exits gracefully. It belongs
	// outside the holder directory: the generic cleanup removes that
	// directory and never touches this file, so a terminal status survives
	// a daemon that consumes and discards exit.json. The caller owns and
	// removes it. Empty writes no copy.
	ExitCopy string
}

// Launch starts a new holder for session (a JSON object the holder records
// in its descriptor, nil for none), waits for its descriptor, and attaches.
// A failure is also kept for the listing.
func (m *Manager) Launch(session json.RawMessage) (string, error) {
	return m.LaunchWith(LaunchSpec{Session: session})
}

// LaunchWith is Launch for a holder the spec tunes. A failure that comes
// with an id is a holder that registered but could not be attached: it runs
// (listed unattached), and stopping it is the caller's.
func (m *Manager) LaunchWith(spec LaunchSpec) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	id, err := m.launch(spec)
	if err != nil {
		m.logf("holder start failed: %v", err)
		m.mu.Lock()
		m.failures = append(m.failures, StartFailure{At: time.Now().UnixMilli(), Message: err.Error(), Session: spec.Session})
		if len(m.failures) > maxFailures {
			m.failures = m.failures[len(m.failures)-maxFailures:]
		}
		m.mu.Unlock()
		return id, err
	}
	return id, nil
}

func (m *Manager) launch(spec LaunchSpec) (string, error) {
	if m.isClosed() {
		return "", errors.New("the holder manager is closed")
	}
	if m.isStopping() {
		return "", ErrStopping
	}
	session := spec.Session
	if len(session) == 0 {
		session = json.RawMessage("{}")
	}
	python, err := m.python()
	if err != nil {
		return "", err
	}
	scriptPath, err := StageScript(m.opts.Dir)
	if err != nil {
		return "", fmt.Errorf("stage the holder script: %w", err)
	}
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := "h" + hex.EncodeToString(raw)
	dir := filepath.Join(m.opts.Dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	args := []string{scriptPath, "--dir", dir, "--id", id, "--session", string(session)}
	ring := spec.RingBytes
	if ring <= 0 {
		ring = m.opts.RingBytes
	}
	if ring > 0 {
		args = append(args, "--ring-bytes", strconv.Itoa(ring))
	}
	if spec.RingCeilingBytes > 0 {
		args = append(args, "--ring-ceiling-bytes", strconv.Itoa(spec.RingCeilingBytes))
	}
	if spec.RingSkip != "" {
		args = append(args, "--ring-skip", spec.RingSkip)
	}
	if spec.RingPulse != "" {
		args = append(args, "--ring-pulse", spec.RingPulse)
	}
	if spec.ExitCopy != "" {
		args = append(args, "--exit-copy", spec.ExitCopy)
	}
	args = append(args, m.opts.holderArgs...)
	// The holder runs in the holders directory, not its own, so it can
	// remove its own directory when it exits.
	cmd, err := m.opts.start(python, args, m.opts.Dir, *m.opts.breakaway)
	if err != nil && *m.opts.breakaway {
		// The starter's job forbids breakaway: the holder still starts,
		// inside the job, and records that it may end with it.
		cmd, err = m.opts.start(python, append(args, "--restart-unsafe"), m.opts.Dir, false)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("start the session holder with %s: %w", python, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(m.opts.startTimeout)
	var desc Descriptor
	for {
		if desc, err = ReadDescriptor(dir); err == nil {
			break
		}
		select {
		case waitErr := <-exited:
			detail := logTail(dir)
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("the session holder (%s) ended before it registered: %v%s", python, exitText(waitErr), detail)
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			detail := logTail(dir)
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("the session holder (%s) wrote no descriptor within %s%s", python, m.opts.startTimeout, detail)
		}
	}
	if err := m.attach(desc); err != nil {
		// The holder registered and runs: its id goes back with the error,
		// so the caller can stop it.
		return id, fmt.Errorf("attach to the new session holder %s: %w", id, err)
	}
	return id, nil
}

func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// logTail is the end of a holder's own log, for a start failure's message.
func logTail(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, logName))
	if err != nil || len(raw) == 0 {
		return ""
	}
	if len(raw) > 1024 {
		raw = raw[len(raw)-1024:]
	}
	return ": " + strings.TrimSpace(string(raw))
}

// PruneScripts removes the staged scripts that no live holder's descriptor
// names. Run calls it once after its first scan.
func (m *Manager) PruneScripts() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	keep := map[string]bool{}
	m.mu.Lock()
	for _, e := range m.entries {
		if e.desc.ScriptHash != "" {
			keep[e.desc.ScriptHash] = true
		}
	}
	m.mu.Unlock()
	pruneStagedScripts(m.opts.Dir, keep, time.Now())
}
