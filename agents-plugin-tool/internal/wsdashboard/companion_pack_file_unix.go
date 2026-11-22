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
