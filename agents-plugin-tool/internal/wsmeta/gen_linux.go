package wsmeta

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// dirGeneration identifies one incarnation of a session directory: device,
// inode and, where the file system records it, birth time. Birth time comes
// from statx STATX_BTIME (ext4, xfs, btrfs, recent tmpfs). So a directory
// pruned and recreated under the same reply-id reads as another generation,
// even when its inode is reused.
//
// The token's form must not change between passes for the same directory,
// or the session would read as recreated and lose its journal rows. So a
// statx call that fails is not answered with a weaker token. A kernel or
// sandbox without statx (ENOSYS, or EPERM from a seccomp filter) always gets
// the device-and-inode token. Any other failure is returned as inconclusive.
// Without a birth time, a missed recreation is harmless: every capture row
// is still re-validated against its live file, and a fence is proven only by
// the migration's sentinel (fenceProven).
func dirGeneration(path string, _ os.FileInfo) (string, error) {
	var stx unix.Statx_t
	wsstate.CountFileIO("stat")
	err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME, &stx)
	switch {
	case err == nil:
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
		return "", fs.ErrNotExist
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EPERM):
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); err != nil {
			if errors.Is(err, unix.ENOENT) {
				return "", fs.ErrNotExist
			}
			return "", err
		}
		return fmt.Sprintf("ino:%d:%d", uint64(st.Dev), uint64(st.Ino)), nil
	default:
		return "", err
	}
	gen := fmt.Sprintf("ino:%d:%d:%d", stx.Dev_major, stx.Dev_minor, stx.Ino)
	if stx.Mask&unix.STATX_BTIME != 0 {
		gen += fmt.Sprintf(":btime:%d.%09d", stx.Btime.Sec, stx.Btime.Nsec)
	}
	return gen, nil
}
