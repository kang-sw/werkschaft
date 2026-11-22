//go:build !windows

package wscred

import "os"

// restrictFile makes the still-empty temp file f mode 0600 (os.CreateTemp
// already does; this states it rather than relying on that).
func restrictFile(f *os.File, _ string) error { return f.Chmod(0o600) }

// restrictDir makes the credential directory mode 0700, also when an
// earlier run or the user created it wider.
func restrictDir(dir string) error { return os.Chmod(dir, 0o700) }
