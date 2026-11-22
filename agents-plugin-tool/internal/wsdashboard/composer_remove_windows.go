//go:build windows

package wsdashboard

import (
	"errors"
	"syscall"
)

// errorDeletePending is ERROR_DELETE_PENDING: the removal was accepted and the
// name goes away when the last open handle closes (a filesystem without POSIX
// delete reports it on the next access).
const errorDeletePending syscall.Errno = 303

// composerDeletePending reports whether err says the file is delete-pending.
// Access denied (5) and a sharing violation (32) are not it: the file is still
// there.
func composerDeletePending(err error) bool {
	return errors.Is(err, errorDeletePending)
}
