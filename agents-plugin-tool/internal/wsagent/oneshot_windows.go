//go:build windows

package wsagent

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureOneShotCmd hides the console window and gives the child its own
// process group so execjob.KillTree ends everything it started.
func configureOneShotCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
