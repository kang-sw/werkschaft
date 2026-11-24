//go:build unix

package wsfs

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

func openMutationTemp(parent *os.File, name string) (*os.File, error) {
	return openMutationFile(parent, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
}

func setMutationTempMode(file *os.File, mode os.FileMode) error {
	return file.Chmod(mode.Perm() | mode&os.ModeSticky)
}

func publishMutationTemp(parent, tmp *os.File, name string, replace bool) error {
	fd := int(parent.Fd())
	old := filepath.Base(tmp.Name())
	var err error
	if replace {
		err = unix.Renameat(fd, old, fd, name)
	} else {
		// Link the fully written file into a new entry. Linkat refuses an
		// entry created by someone else since admission, like O_EXCL.
		err = unix.Linkat(fd, old, fd, name, 0)
		if err == nil {
			err = unix.Unlinkat(fd, old, 0)
		}
	}
	runtime.KeepAlive(parent)
	if err != nil {
		return &os.PathError{Op: "publish", Path: filepath.Join(parent.Name(), name), Err: err}
	}
	return nil
}

func discardMutationTemp(parent, tmp *os.File) {
	_ = unix.Unlinkat(int(parent.Fd()), filepath.Base(tmp.Name()), 0)
	runtime.KeepAlive(parent)
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
