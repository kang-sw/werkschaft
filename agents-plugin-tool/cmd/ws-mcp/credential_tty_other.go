//go:build !darwin && !linux && !windows

package main

import "os"

// disableEcho has no terminal control on this platform: stdin is read as is.
func disableEcho(*os.File) (restore func(), terminal bool, err error) {
	return nil, false, nil
}
