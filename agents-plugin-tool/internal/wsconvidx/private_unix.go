//go:build !windows

package wsconvidx

import (
	"errors"
	"io/fs"
	"os"
)

var errNotRegular = errors.New("not a regular file")

// makePrivate creates the database file when missing and leaves it and any
// existing side files owner-only on every open. Conversation history contains
// private session content.
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
