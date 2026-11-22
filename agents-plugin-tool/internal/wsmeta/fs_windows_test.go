package wsmeta

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestPlatformFileSystemNamesTheVolume(t *testing.T) {
	dir := t.TempDir()
	name, err := platformFileSystem(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name == "" {
		t.Fatalf("no file-system name for %s", dir)
	}
	t.Logf("%s is on %s (cutover supported: %v)", dir, name, supportedFileSystem(name))
	if _, err := platformFileSystem(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing directory = %v, want fs.ErrNotExist", err)
	}
}
