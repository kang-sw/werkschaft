//go:build unix

package werk

import (
	"os"

	"golang.org/x/sys/unix"
)

func openMutationFile(path string, flags int) (*os.File, error) {
	// A replacement between Lstat and open may be a FIFO or a symlink.
	fd, err := unix.Open(path, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
