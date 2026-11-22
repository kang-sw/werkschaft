package wscred

import (
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// writePrivateFile replaces path with data by atomic replace (a temp file in
// the same directory, then a rename), owner-only before the first byte is
// written: the temp file is restricted (restrictFile) while still empty, and
// the rename keeps its permissions. The directory is created owner-only.
func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := restrictDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := restrictFile(tmp, name); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := wsstate.AtomicReplaceFile(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
