//go:build !windows

package wsmeta

import (
	"fmt"
	"os"
	"syscall"
)

// dirGeneration identifies one incarnation of a session directory: its
// device and inode, so a directory pruned and recreated under the same
// reply-id reads as a different generation (inode reuse aside).
func dirGeneration(info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		return fmt.Sprintf("ino:%d:%d", uint64(st.Dev), uint64(st.Ino))
	}
	return "mtime:" + info.ModTime().UTC().Format("20060102T150405.000000000")
}
