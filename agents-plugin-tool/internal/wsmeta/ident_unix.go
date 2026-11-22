//go:build !windows

package wsmeta

import "github.com/kang-sw/devenv/internal/wsstate"

// entryIdentity is the generation token of the entry at p (gen_*.go): device
// and inode, with the birth time where the platform has one. A rename keeps
// all of them.
func entryIdentity(p string) (string, error) {
	info, err := wsstate.Lstat(p)
	if err != nil {
		return "", err
	}
	return dirGeneration(p, info)
}
