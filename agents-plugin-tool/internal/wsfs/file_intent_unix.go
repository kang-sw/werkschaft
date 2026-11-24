//go:build unix

package wsfs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func hasSingleLink(_ *os.File, info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errors.ErrUnsupported
	}
	return stat.Nlink == 1, nil
}

func openMutationDir(parent *os.File, name string, create bool) (*os.File, error) {
	if create {
		err := unix.Mkdirat(int(parent.Fd()), name, 0o755)
		runtime.KeepAlive(parent)
		if err != nil {
			return nil, &os.PathError{Op: "mkdir", Path: filepath.Join(parent.Name(), name), Err: err}
		}
	}
	return openMutationAt(parent, name, unix.O_RDONLY|unix.O_DIRECTORY)
}

func openMutationFile(parent *os.File, name string, flags int) (*os.File, error) {
	return openMutationAt(parent, name, flags)
}

func openMutationAt(parent *os.File, name string, flags int) (*os.File, error) {
	dirFD, path := unix.AT_FDCWD, name
	if parent != nil {
		dirFD, path = int(parent.Fd()), filepath.Join(parent.Name(), name)
	}
	// A replacement may be a symlink or a FIFO.
	fd, err := unix.Openat(dirFD, name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o644)
	runtime.KeepAlive(parent)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
