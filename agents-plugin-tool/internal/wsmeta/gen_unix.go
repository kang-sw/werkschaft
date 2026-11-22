//go:build !windows && !darwin && !linux

package wsmeta

import (
	"fmt"
	"os"
	"syscall"
)

// dirGeneration identifies one incarnation of a session directory by its
// device and inode. A directory pruned and recreated under the same reply-id
// can reuse the inode here; a missed recreation is harmless because every
// capture row is still re-validated against its live file and a fence is
// proven only by the migration's sentinel (fenceProven).
func dirGeneration(_ string, info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		return fmt.Sprintf("ino:%d:%d", uint64(st.Dev), uint64(st.Ino))
	}
	return "mtime:" + info.ModTime().UTC().Format("20060102T150405.000000000")
}
