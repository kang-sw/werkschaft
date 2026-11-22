//go:build !windows && !darwin && !linux

package wsmeta

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// dirGeneration identifies one incarnation of a session directory by its
// device and inode. A directory pruned and recreated under the same reply-id
// can reuse the inode here. A missed recreation is harmless: every capture
// row is still re-validated against its live file, and a fence is proven
// only by the migration's sentinel (fenceProven).
func dirGeneration(_ string, info os.FileInfo) (string, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return "", errors.New("no stat data")
	}
	return fmt.Sprintf("ino:%d:%d", uint64(st.Dev), uint64(st.Ino)), nil
}
