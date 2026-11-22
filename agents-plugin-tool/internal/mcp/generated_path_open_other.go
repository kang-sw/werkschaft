//go:build !windows

package mcp

import "os"

// holdGeneratedPathGrant is true where POSIX semantics apply: the grant keeps
// its descriptor from path.generate to path.write, so path.write performs no
// further open.
const holdGeneratedPathGrant = true

// openGeneratedPathGrant opens the held path.write descriptor. POSIX
// unlink/rename never conflicts with an open descriptor, so a caller that
// saves the generated file by replace is unaffected by the held grant.
func openGeneratedPathGrant(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}

// lstatGeneratedPath is the identity probe paired with
// openGeneratedPathGrant: it inspects the same path form the grant opens.
func lstatGeneratedPath(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}
