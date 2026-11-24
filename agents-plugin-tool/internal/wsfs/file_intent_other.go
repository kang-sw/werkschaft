//go:build !unix && !windows

package wsfs

import (
	"errors"
	"os"
)

func openMutationTemp(*os.File, string) (*os.File, error) { return nil, errors.ErrUnsupported }

func setMutationTempMode(*os.File, os.FileMode) error { return errors.ErrUnsupported }

func publishMutationTemp(*os.File, *os.File, string, bool) error { return errors.ErrUnsupported }

func discardMutationTemp(*os.File, *os.File) {}

func openMutationDir(_ *os.File, _ string, _ bool) (*os.File, error) {
	return nil, errors.ErrUnsupported
}

func openMutationFile(_ *os.File, _ string, _ int) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
