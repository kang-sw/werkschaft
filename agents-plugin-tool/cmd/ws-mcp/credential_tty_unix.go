//go:build darwin || linux

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// disableEcho turns terminal echo off on f and returns the function that
// turns it back on; terminal is false (and nothing changes) when f is not a
// terminal. Canonical line input stays on, so the key still ends at Enter.
func disableEcho(f *os.File) (restore func(), terminal bool, err error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, false, nil
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	quiet.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return nil, true, fmt.Errorf("turn off terminal echo: %w", err)
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }, true, nil
}
