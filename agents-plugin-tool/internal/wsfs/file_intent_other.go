//go:build !unix

package wsfs

import "os"

func openMutationFile(path string, flags int) (*os.File, error) {
	return os.OpenFile(path, flags, 0o644)
}
