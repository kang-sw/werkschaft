//go:build unix && !darwin && !linux

package wsfs

import (
	"errors"
	"os"
)

func readMutationACL(*os.File) ([]byte, error) { return nil, errors.ErrUnsupported }

func setMutationACL(*os.File, []byte) error { return errors.ErrUnsupported }
