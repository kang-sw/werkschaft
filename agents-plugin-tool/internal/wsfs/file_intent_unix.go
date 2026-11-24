//go:build unix

package wsfs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func openMutationTemp(parent *os.File, name string, permissions *mutationPermissions) (*os.File, error) {
	mode := uint32(0o644)
	if permissions != nil {
		mode = 0o600
	}
	return openMutationAt(parent, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}

const mutationModeBits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

type mutationPermissions struct {
	mode     os.FileMode
	uid, gid uint32
	acl      []byte
}

func readMutationPermissions(file *os.File, info os.FileInfo) (*mutationPermissions, error) {
	if info == nil {
		var err error
		info, err = file.Stat()
		if err != nil {
			return nil, err
		}
	}
	stat := info.Sys().(*syscall.Stat_t)
	acl, err := readMutationACL(file)
	if err != nil {
		return nil, fmt.Errorf("read access ACL: %w", err)
	}
	return &mutationPermissions{mode: info.Mode() & mutationModeBits, uid: stat.Uid, gid: stat.Gid, acl: acl}, nil
}

func (p *mutationPermissions) prepare(tmp *os.File) error {
	initial, err := readMutationPermissions(tmp, nil)
	if err != nil {
		return err
	}
	if p.uid != initial.uid || p.gid != initial.gid {
		if err := tmp.Chown(int(p.uid), int(p.gid)); err != nil {
			return fmt.Errorf("preserve owner and group: %w", err)
		}
	}
	// Clear any inherited ACL before opening up the group mode bits.
	if !bytes.Equal(initial.acl, p.acl) {
		if err := setMutationACL(tmp, p.acl); err != nil {
			return fmt.Errorf("preserve access ACL: %w", err)
		}
	}
	if p.mode != initial.mode {
		if err := tmp.Chmod(p.mode); err != nil {
			return fmt.Errorf("preserve mode: %w", err)
		}
	}
	return p.verify(tmp, nil)
}

func (p *mutationPermissions) verify(file *os.File, info os.FileInfo) error {
	actual, err := readMutationPermissions(file, info)
	if err != nil {
		return err
	}
	if actual.uid != p.uid || actual.gid != p.gid {
		return fmt.Errorf("owner or group differs")
	}
	if actual.mode != p.mode {
		return fmt.Errorf("mode differs")
	}
	if !bytes.Equal(actual.acl, p.acl) {
		return fmt.Errorf("access ACL differs")
	}
	return nil
}

func publishMutationTemp(parent, stagingParent, tmp *os.File, name string, replace bool) error {
	fd := int(parent.Fd())
	stagingFD := int(stagingParent.Fd())
	old := filepath.Base(tmp.Name())
	var err error
	if replace {
		err = unix.Renameat(stagingFD, old, fd, name)
	} else {
		// Link the fully written file into a new entry. Linkat refuses an
		// entry created by someone else since admission, like O_EXCL.
		err = unix.Linkat(stagingFD, old, fd, name, 0)
		if err == nil {
			err = unix.Unlinkat(stagingFD, old, 0)
		}
	}
	runtime.KeepAlive(parent)
	runtime.KeepAlive(stagingParent)
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
	return openMutationAt(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
}

func openMutationFile(parent *os.File, name string, flags int) (*os.File, error) {
	return openMutationAt(parent, name, flags, 0o644)
}

func openMutationAt(parent *os.File, name string, flags int, mode uint32) (*os.File, error) {
	dirFD, path := unix.AT_FDCWD, name
	if parent != nil {
		dirFD, path = int(parent.Fd()), filepath.Join(parent.Name(), name)
	}
	// A replacement may be a symlink or a FIFO.
	fd, err := unix.Openat(dirFD, name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	runtime.KeepAlive(parent)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
