package wsauditstore

import (
	"errors"
	"io"
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

// readHead reads at most n bytes from the start of path.
var readHead = func(path string, n int) ([]byte, error) {
	f, err := wsstate.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer closeFile(f)
	buf := make([]byte, n)
	k, err := io.ReadFull(f, buf)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		err = nil
	}
	return buf[:k], err
}

func openAppend(path string) (*os.File, error) {
	return wsstate.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
}

var createSegment = func(path string) (*os.File, error) {
	return wsstate.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o600)
}

func mkdirPrivate(dir string) error {
	wsstate.CountFileIO("mkdir")
	return os.MkdirAll(dir, 0o700)
}

var closeFile = func(f *os.File) error {
	wsstate.CountFileIO("close")
	return f.Close()
}

var removeFile = func(path string) error {
	wsstate.CountFileIO("remove")
	return os.Remove(path)
}

var removeAll = func(path string) error {
	wsstate.CountFileIO("remove_all")
	return os.RemoveAll(path)
}

func truncateFile(path string, size int64) error {
	wsstate.CountFileIO("truncate")
	return os.Truncate(path, size)
}
