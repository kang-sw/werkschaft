//go:build !windows

package wsmeta

// platformFileSystem needs no file-system name on POSIX: the barrier is a
// directory fsync, which the platform documents for every file system.
func platformFileSystem(string) (string, error) { return "", nil }
