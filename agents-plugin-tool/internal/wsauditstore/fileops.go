package wsauditstore

import (
	"os"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// File operations go through these seams so each touch is counted
// (wsstate.FileIOCounters) and tests can inject failures. Files are 0600 and
// directories 0700, like the display ring.

var (
	readDir  = wsstate.ReadDir
	readFile = wsstate.ReadFileOnce

	writeFile = func(f *os.File, b []byte) (int, error) {
		wsstate.CountFileIO("write")
		return f.Write(b)
	}
)

func openAppend(path string) (*os.File, error) {
	return wsstate.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
}

func createSegment(path string) (*os.File, error) {
	return wsstate.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o600)
}

func mkdirPrivate(dir string) error {
	wsstate.CountFileIO("mkdir")
	return os.MkdirAll(dir, 0o700)
}

func closeFile(f *os.File) error {
	wsstate.CountFileIO("close")
	return f.Close()
}

func removeFile(path string) error {
	wsstate.CountFileIO("remove")
	return os.Remove(path)
}

func removeAll(path string) error {
	wsstate.CountFileIO("remove_all")
	return os.RemoveAll(path)
}

func truncateFile(path string, size int64) error {
	wsstate.CountFileIO("truncate")
	return os.Truncate(path, size)
}
