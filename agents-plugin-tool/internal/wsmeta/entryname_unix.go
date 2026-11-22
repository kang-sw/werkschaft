//go:build !windows

package wsmeta

// platformEntryName returns name: a POSIX directory entry has no other
// spelling (a hard link is an independent entry).
func platformEntryName(_, name string) (string, error) { return name, nil }
