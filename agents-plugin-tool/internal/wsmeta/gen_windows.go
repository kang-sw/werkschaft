package wsmeta

import (
	"fmt"
	"os"
	"syscall"
)

// dirGeneration identifies one incarnation of a session directory by its
// creation time, the identity wsstate.SameFileMetadata uses on Windows,
// read from the directory entry with no extra file operation.
func dirGeneration(info os.FileInfo) string {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && d != nil {
		return fmt.Sprintf("ctime:%d:%d", d.CreationTime.HighDateTime, d.CreationTime.LowDateTime)
	}
	return "mtime:" + info.ModTime().UTC().Format("20060102T150405.000000000")
}

// stableReadError reports a read error a retry will not clear. On Windows an
// access-denied error is also what a pending delete or a scanner's exclusive
// open returns, so no read error is treated as stable.
func stableReadError(error) bool { return false }
