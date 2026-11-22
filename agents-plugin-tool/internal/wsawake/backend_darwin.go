//go:build darwin

package wsawake

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// caffeinatePath is the system helper; an absolute path keeps a PATH entry
// from substituting another program.
const caffeinatePath = "/usr/bin/caffeinate"

// helperStderrLimit bounds the helper's stderr kept for the exit log.
const helperStderrLimit = 2048

func platformBackend(log io.Writer, pid int) Backend {
	return &caffeinate{path: caffeinatePath, pid: pid, log: log}
}

// caffeinate holds the idle-system-sleep assertion through the system
// caffeinate helper, run as `caffeinate -i -w <daemon pid>`: -i prevents idle
// system sleep, and -w ends the helper with the daemon, so a daemon that dies
// without Release leaves no assertion behind. Display sleep and sleep the user
// asks for (closing the lid, the Apple menu) stay under the OS's control.
//
// Only this helper's own lifecycle is managed: it is started on Acquire, and
// Release ends it and reaps it before another may start. A goroutine reaps
// the helper as soon as it exits and logs an exit Release did not ask for,
// with its status and the start of its stderr; Lost then reports the
// assertion gone, and Core takes it again at the next change.
type caffeinate struct {
	path string
	pid  int
	log  io.Writer
	cmd  *exec.Cmd
	// exited closes once the helper was reaped; released is set before
	// Release kills it, so that exit is not logged as unexpected.
	exited   chan struct{}
	released *atomic.Bool
}

func (b *caffeinate) Acquire() error {
	// A helper still on record is ended and reaped first, so at most one
	// ever runs.
	if err := b.Release(); err != nil {
		return err
	}
	cmd := exec.Command(b.path, "-i", "-w", strconv.Itoa(b.pid))
	stderr := &boundedBuffer{limit: helperStderrLimit}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	exited, released := make(chan struct{}), &atomic.Bool{}
	go func() {
		err := cmd.Wait()
		unexpected := !released.Load()
		// Reaped: close first, so Lost already holds by the time the exit
		// line can be seen in the log.
		close(exited)
		if unexpected && b.log != nil {
			status := "exit status 0"
			if err != nil {
				status = err.Error()
			}
			msg := fmt.Sprintf("keep-awake: %s exited unexpectedly (%s); the idle-sleep assertion ended with it", b.path, status)
			if out := strings.TrimSpace(stderr.String()); out != "" {
				msg += "; stderr: " + out
			}
			// One write, so the line does not interleave with other logging.
			_, _ = io.WriteString(b.log, msg+"\n")
		}
	}()
	b.cmd, b.exited, b.released = cmd, exited, released
	return nil
}

func (b *caffeinate) Release() error {
	if b.cmd == nil {
		return nil
	}
	cmd, exited, released := b.cmd, b.exited, b.released
	b.cmd, b.exited, b.released = nil, nil, nil
	select {
	case <-exited:
		// The helper already ended on its own (the reaper logs that exit):
		// the assertion went with it.
		return nil
	default:
	}
	// A kill that races the helper's own exit fails harmlessly; either way
	// the helper is reaped before Release returns, and its assertion ended
	// with it.
	released.Store(true)
	_ = cmd.Process.Kill()
	<-exited
	return nil
}

// Lost reports a helper on record that already exited: the assertion it
// held is gone although no Release asked for that.
func (b *caffeinate) Lost() bool {
	if b.cmd == nil {
		return false
	}
	select {
	case <-b.exited:
		return true
	default:
		return false
	}
}

// boundedBuffer keeps the first limit bytes written to it and drops the
// rest, so a chatty helper cannot grow the daemon's memory. exec copies the
// helper's stderr into it from its own goroutine.
type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.limit - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (w *boundedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
