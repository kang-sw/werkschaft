//go:build !windows

package wsconvidx

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

var errNotRegular = errors.New("not a regular file")

// makePrivate creates the database file when missing and leaves it and any
// existing side files owner-only on every open. Conversation history contains
// private session content.
//
// Every file is opened with O_NOFOLLOW|O_NONBLOCK and checked and chmodded
// through the open descriptor, so a symlink or FIFO swapped in at the path can
// neither redirect the chmod nor block the open.
func makePrivate(dbPath string) error {
	if err := privateOpen(dbPath, os.O_RDWR|os.O_CREATE); err != nil {
		return err
	}
	for _, p := range []string{dbPath + "-wal", dbPath + "-journal"} {
		if err := privateOpen(p, os.O_RDONLY); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func privateOpen(path string, flag int) error {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return &fs.PathError{Op: "open", Path: path, Err: errNotRegular}
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(0o600); err != nil {
			return err
		}
	}
	return nil
}
