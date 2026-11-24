//go:build !unix && !windows

package wsfs

import (
	"errors"
	"os"
)

func HasMultipleLinks(*os.File, os.FileInfo) (bool, error) {
	return false, errors.ErrUnsupported
}
