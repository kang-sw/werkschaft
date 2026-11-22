package wsmeta

import "github.com/kang-sw/devenv/internal/wsstate"

// readRegularFile reads path, retrying a sharing violation. Discovery has
// already refused a non-regular file or link target; Windows has no FIFO in
// the file namespace.
func readRegularFile(path string) ([]byte, error) {
	return wsstate.ReadFile(path)
}
