//go:build !darwin

package wsfs

import "os"

func mutationStagingLocation(parent *os.File, _ string, _ bool) (*os.File, string, func(), error) {
	return siblingMutationStagingLocation(parent)
}

func recoverMutationStage(*os.File, string) error { return nil }
