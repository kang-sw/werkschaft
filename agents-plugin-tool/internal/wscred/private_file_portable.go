//go:build !unix && !windows

package wscred

import "os"

func openCredentialFile(path string, write bool) (*os.File, error) {
	flags := os.O_RDONLY
	if write {
		flags = os.O_WRONLY
	}
	return os.OpenFile(path, flags, 0)
}
