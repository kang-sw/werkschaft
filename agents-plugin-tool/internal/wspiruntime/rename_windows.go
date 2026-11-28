//go:build windows

package wspiruntime

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func renameDirWithRetry(oldpath, newpath string) error {
	return retryTransientDirOp(func() error {
		return os.Rename(oldpath, newpath)
	})
}

func removeAllWithRetry(path string) error {
	return retryTransientDirOp(func() error {
		return os.RemoveAll(path)
	})
}

// retryTransientDirOp retries brief Windows AV/scanner holds
// (ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION) with exponential backoff,
// matching wsstate/replace_file_windows.go.
func retryTransientDirOp(op func() error) error {
	deadline := time.Now().Add(2 * time.Second)
	backoff := 5 * time.Millisecond
	for {
		err := op()
		if err == nil || !isTransientWindowsDirErr(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(backoff)
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
}

func isTransientWindowsDirErr(err error) bool {
	var errno windows.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_SHARING_VIOLATION
}
