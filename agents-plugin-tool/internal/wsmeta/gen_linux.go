package wsmeta

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// dirGeneration identifies one incarnation of a session directory: device,
// inode and, where the file system records it (statx STATX_BTIME: ext4,
// xfs, btrfs, recent tmpfs), birth time, so a directory pruned and recreated
// under the same reply-id reads as another generation even when its inode is
// reused. Without a birth time a missed recreation is harmless: every capture
// row is still re-validated against its live file and a fence is proven only
// by the migration's sentinel (fenceProven).
func dirGeneration(path string, info os.FileInfo) string {
	var stx unix.Statx_t
	wsstate.CountFileIO("stat")
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME, &stx); err == nil {
		gen := fmt.Sprintf("ino:%d:%d:%d", stx.Dev_major, stx.Dev_minor, stx.Ino)
		if stx.Mask&unix.STATX_BTIME != 0 {
			gen += fmt.Sprintf(":btime:%d.%09d", stx.Btime.Sec, stx.Btime.Nsec)
		}
		return gen
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st != nil {
		return fmt.Sprintf("ino:%d:%d", uint64(st.Dev), uint64(st.Ino))
	}
	return "mtime:" + info.ModTime().UTC().Format("20060102T150405.000000000")
}
