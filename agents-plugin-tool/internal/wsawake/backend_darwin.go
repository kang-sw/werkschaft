//go:build darwin

package wsawake

import (
	"io"
	"os/exec"
	"strconv"
)

// caffeinatePath is the system helper; an absolute path keeps a PATH entry
// from substituting another program.
const caffeinatePath = "/usr/bin/caffeinate"

func platformBackend(_ io.Writer, pid int) Backend {
	return &caffeinate{path: caffeinatePath, pid: pid}
}

// caffeinate holds the idle-system-sleep assertion through the system
// caffeinate helper, run as `caffeinate -i -w <daemon pid>`: -i prevents idle
// system sleep, and -w ends the helper with the daemon, so a daemon that dies
// without Release leaves no assertion behind. Display sleep and sleep the user
// asks for (closing the lid, the Apple menu) stay under the OS's control.
//
// Only this helper's own lifecycle is managed: it is started on Acquire, and
// Release ends it and reaps it before another may start.
type caffeinate struct {
	path string
	pid  int
	cmd  *exec.Cmd
	// exited closes once the helper was reaped.
	exited chan struct{}
}

func (b *caffeinate) Acquire() error {
	// A helper still on record is ended and reaped first, so at most one
	// ever runs.
	if err := b.Release(); err != nil {
		return err
	}
	cmd := exec.Command(b.path, "-i", "-w", strconv.Itoa(b.pid))
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	b.cmd, b.exited = cmd, exited
	return nil
}

func (b *caffeinate) Release() error {
	if b.cmd == nil {
		return nil
	}
	cmd, exited := b.cmd, b.exited
	b.cmd, b.exited = nil, nil
	select {
	case <-exited:
		// The helper already ended on its own: the assertion went with it.
		return nil
	default:
	}
	// A kill that races the helper's own exit fails harmlessly; either way
	// the helper is reaped before Release returns, and its assertion ended
	// with it.
	_ = cmd.Process.Kill()
	<-exited
	return nil
}
