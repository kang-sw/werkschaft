//go:build !windows

package wsmeta

import (
	"io"
	"os"
	"syscall"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// readRegularFile reads path only when the opened file is a regular file.
// The non-blocking open keeps a FIFO (or a link to one) swapped in after
// discovery's stat from blocking the import while it holds the store.
func readRegularFile(path string) ([]byte, error) {
	f, err := wsstate.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return io.ReadAll(f)
}
