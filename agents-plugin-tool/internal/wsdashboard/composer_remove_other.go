//go:build !windows

package wsdashboard

// composerDeletePending is Windows' delete-pending state; no other platform
// has one, so no error means it.
func composerDeletePending(error) bool { return false }
