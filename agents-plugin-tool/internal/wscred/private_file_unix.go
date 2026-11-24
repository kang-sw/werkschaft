//go:build unix

package wscred

import (
	"os"

	"golang.org/x/sys/unix"
)

func openCredentialFile(path string, write bool) (*os.File, error) {
	flags := unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if write {
		flags |= unix.O_WRONLY
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
