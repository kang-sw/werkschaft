//go:build !unix && !windows

package wsfs

import (
	"errors"
	"os"
)

type mutationPermissions struct{}

func readMutationPermissions(*os.File, os.FileInfo) (*mutationPermissions, error) {
	return nil, errors.ErrUnsupported
}

func (*mutationPermissions) prepare(*os.File) error { return errors.ErrUnsupported }

func (*mutationPermissions) verify(*os.File, os.FileInfo) error { return errors.ErrUnsupported }

func openMutationTemp(*os.File, string, *mutationPermissions) (*os.File, error) {
	return nil, errors.ErrUnsupported
}

func publishMutationTemp(*os.File, *os.File, *os.File, string, bool) error {
	return errors.ErrUnsupported
}

func discardMutationTemp(*os.File, *os.File) {}

func openMutationDir(_ *os.File, _ string, _ bool) (*os.File, error) {
	return nil, errors.ErrUnsupported
}

func openMutationFile(_ *os.File, _ string, _ int) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
