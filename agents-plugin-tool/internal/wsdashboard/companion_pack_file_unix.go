//go:build unix

package wsdashboard

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// Open children relative to the pinned directory to prevent a concurrent
// rename or symlink replacement from redirecting a manifest or asset read.
func openCompanionPackEntry(parent companionPackFile, name string, directory bool) (companionPackFile, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	dirFD, path := unix.AT_FDCWD, name
	if parent != nil {
		dirFD, path = int(parent.Fd()), filepath.Join(parent.Name(), name)
	}
	fd, err := unix.Openat(dirFD, name, flags, 0)
	runtime.KeepAlive(parent)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// The clip write path below resolves no path string after its pins: every
// create, rename and unlink is relative to a directory descriptor opened
// with O_NOFOLLOW|O_DIRECTORY, so a symlink planted at the pack directory,
// clips/ or a target name refuses the write instead of redirecting it.

func companionAtPath(parent *os.File, name string) (int, string) {
	if parent == nil {
		return unix.AT_FDCWD, name
	}
	return int(parent.Fd()), filepath.Join(parent.Name(), name)
}

// openCompanionDirAt opens a directory for mutation relative to parent (or
// name itself when parent is nil), refusing a symlink.
func openCompanionDirAt(parent *os.File, name string) (*os.File, error) {
	dirFD, path := companionAtPath(parent, name)
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	runtime.KeepAlive(parent)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func mkdirCompanionAt(parent *os.File, name string) error {
	dirFD, path := companionAtPath(parent, name)
	err := unix.Mkdirat(dirFD, name, 0o755)
	runtime.KeepAlive(parent)
	if err != nil {
		return &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	return nil
}

// createCompanionTempAt creates a new file exclusively in dir with perm
// (subject to the umask), so a rewrite keeps the replaced file's mode.
func createCompanionTempAt(dir *os.File, name string, perm os.FileMode) (*os.File, error) {
	dirFD, path := companionAtPath(dir, name)
	fd, err := unix.Openat(dirFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_WRONLY|unix.O_CLOEXEC, uint32(perm.Perm()))
	runtime.KeepAlive(dir)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// renameCompanionAt atomically replaces name with the temp file in dir.
func renameCompanionAt(dir, _ *os.File, tmpName, name string) error {
	dirFD, path := companionAtPath(dir, name)
	err := unix.Renameat(dirFD, tmpName, dirFD, name)
	runtime.KeepAlive(dir)
	if err != nil {
		return &os.PathError{Op: "rename", Path: path, Err: err}
	}
	return nil
}

func removeCompanionAt(dir *os.File, name string) error {
	dirFD, path := companionAtPath(dir, name)
	err := unix.Unlinkat(dirFD, name, 0)
	runtime.KeepAlive(dir)
	if err != nil {
		return &os.PathError{Op: "remove", Path: path, Err: err}
	}
	return nil
}
