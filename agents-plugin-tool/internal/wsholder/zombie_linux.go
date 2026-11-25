//go:build linux

package wsholder

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// A zombie cannot answer requests or hold a child, even if kill(pid, 0)
// succeeds until its parent reaps it. The comm field may contain parentheses.
// If the process is reaped between kill(pid, 0) and reading /proc/<pid>/stat,
// ReadFile returns ENOENT/ESRCH and a follow-up kill(pid, 0) returns ESRCH;
// treat that as not alive rather than misreporting a just-reaped process.
func processZombie(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		}
		return false
	}
	return zombieStat(string(raw))
}

func zombieStat(stat string) bool {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(stat[end+1:])
	return len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
}
