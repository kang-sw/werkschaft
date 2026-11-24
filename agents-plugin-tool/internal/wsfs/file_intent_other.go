//go:build !unix && !windows

package wsfs

import (
	"errors"
	"os"
)

func hasSingleLink(*os.File, os.FileInfo) (bool, error) {
	return false, errors.ErrUnsupported
}

func openMutationDir(_ *os.File, _ string, _ bool) (*os.File, error) {
	return nil, errors.ErrUnsupported
}

func openMutationFile(_ *os.File, _ string, _ int) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
