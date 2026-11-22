//go:build linux

package main

import (
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal pair: the master as a raw descriptor (the
// test types into it) and the slave as the *os.File the CLI reads.
func openPTY(t *testing.T) (master int, slave *os.File) {
	t.Helper()
	m, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(m) })
	if err := unix.IoctlSetPointerInt(m, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(m, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return m, s
}
