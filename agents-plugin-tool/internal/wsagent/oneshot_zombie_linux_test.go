//go:build linux

package wsagent

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// oneShotProcessZombie mirrors internal/wsholder/zombie_linux.go: a killed
// grandchild may remain a Linux zombie (Z/X) until reaped, during which
// kill(pid, 0) still succeeds.
func oneShotProcessZombie(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		}
		return false
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(string(raw)[end+1:])
	return len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
}
