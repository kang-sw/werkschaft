//go:build windows

package wsdashboard

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// isSecretSharingViolation reports whether err is a Windows sharing violation
// from a concurrent secret-file replacement or scanner hold. It intentionally
// excludes ERROR_ACCESS_DENIED so real permission failures fail immediately.
func isSecretSharingViolation(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == windows.ERROR_SHARING_VIOLATION
}
