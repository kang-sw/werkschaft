//go:build !windows

package wsagent

import (
	"os/exec"
	"syscall"
)

// configureOneShotCmd gives the child its own process group so execjob.KillTree
// ends everything it started.
func configureOneShotCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
