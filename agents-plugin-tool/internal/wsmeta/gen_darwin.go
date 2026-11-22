package wsmeta

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// dirGeneration identifies one incarnation of a session directory: device,
// inode and birth time, so a directory pruned and recreated under the same
// reply-id reads as another generation even when its inode is reused. The
// stat already carries all three, so there is no separate lookup to fail.
func dirGeneration(_ string, info os.FileInfo) (string, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return "", errors.New("no stat data")
	}
	return fmt.Sprintf("ino:%d:%d:btime:%d.%09d", uint64(st.Dev), uint64(st.Ino), st.Birthtimespec.Sec, st.Birthtimespec.Nsec), nil
}
