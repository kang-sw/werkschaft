//go:build !windows

package wspiruntime

import "os"

func renameDirWithRetry(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func removeAllWithRetry(path string) error {
	return os.RemoveAll(path)
}
