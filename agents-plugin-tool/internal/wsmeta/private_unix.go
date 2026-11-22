//go:build !windows

package wsmeta

import (
	"errors"
	"io/fs"
	"os"
)

// makePrivate creates the database file when missing and leaves it and any
// existing side file owner-only, also on reopen: an earlier file or a
// restored copy may carry a wider mode. The unix VFS gives a WAL or journal
// it creates later the main file's mode, so those stay private as well.
// Agent records hold session keys and capability secrets.
func makePrivate(dbPath string) error {
	f, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-journal"} {
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return &fs.PathError{Op: "open", Path: p, Err: errNotRegular}
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(p, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}
