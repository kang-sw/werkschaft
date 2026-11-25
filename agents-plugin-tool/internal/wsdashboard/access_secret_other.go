//go:build !windows

package wsdashboard

func isSecretSharingViolation(error) bool { return false }
