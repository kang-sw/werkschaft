package wsmeta

import (
	"fmt"
	"os"
	"syscall"
)

// dirGeneration identifies one incarnation of a session directory: device,
// inode and birth time, so a directory pruned and recreated under the same
// reply-id reads as another generation even when its inode is reused.
func dirGeneration(_ string, info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		return fmt.Sprintf("ino:%d:%d:btime:%d.%09d", uint64(st.Dev), uint64(st.Ino), st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
	}
	return "mtime:" + info.ModTime().UTC().Format("20060102T150405.000000000")
}
