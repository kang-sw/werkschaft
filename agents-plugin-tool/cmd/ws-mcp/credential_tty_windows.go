//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// disableEcho turns console echo off on f and returns the function that
// turns it back on; terminal is false (and nothing changes) when f is not a
// console. Line input stays on, so the key still ends at Enter.
func disableEcho(f *os.File) (restore func(), terminal bool, err error) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return nil, false, nil
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, true, fmt.Errorf("turn off console echo: %w", err)
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }, true, nil
}
