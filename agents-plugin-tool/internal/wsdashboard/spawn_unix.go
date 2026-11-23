//go:build !windows

package wsdashboard

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetached starts exe in a new session, so it has no controlling
// terminal and no hangup or job-control signal aimed at the caller reaches it.
func startDetachedWithPipes(exe string, args []string, dir string, stdin, stdout *os.File, _ bool) (*exec.Cmd, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cmd.Start()
}

func startDetached(exe string, args []string, dir string) (*exec.Cmd, error) {
	return startDetachedWithPipes(exe, args, dir, nil, nil, false)
}
