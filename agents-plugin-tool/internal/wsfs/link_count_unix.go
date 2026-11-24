//go:build unix

package wsfs

import (
	"errors"
	"os"
	"syscall"
)

// HasMultipleLinks reports whether the opened file has another directory entry.
func HasMultipleLinks(_ *os.File, info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errors.ErrUnsupported
	}
	return stat.Nlink > 1, nil
}
