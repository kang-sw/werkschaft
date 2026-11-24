//go:build !darwin

package wsfs

import "os"

func mutationStagingLocation(parent *os.File, _ bool) (*os.File, string, func(), error) {
	return siblingMutationStagingLocation(parent)
}

func lockMutationDestination(*os.File) error { return nil }
