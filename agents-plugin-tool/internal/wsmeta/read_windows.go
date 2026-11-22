package wsmeta

import (
	"errors"

	"golang.org/x/sys/windows"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// readRegularFile reads path, retrying a sharing violation. Discovery has
// already refused a non-regular file or link target; Windows has no FIFO in
// the file namespace.
func readRegularFile(path string) ([]byte, error) {
	return wsstate.ReadFile(path)
}

// platformReadErrorClass: a sharing or lock violation (a scanner, an
// indexer) always clears. Access denied is also what a pending delete
// returns, so it is only confirmable: it reads as a permanent denial once it
// persists across passes (see confirmUnread). Anything else may clear.
func platformReadErrorClass(err error) readClass {
	switch {
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION), errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return classTransient
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return classConfirmable
	}
	return classTransient
}
