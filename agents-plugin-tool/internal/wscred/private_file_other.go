//go:build !windows

package wscred

import (
	"os"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// CredentialNameIsLink checks a credential name for a symbolic link.
func CredentialNameIsLink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

// restrictFile makes the still-empty temp file f mode 0600 (creation
// already does; this states it rather than relying on that).
func restrictFile(f *os.File, _ string) error { return f.Chmod(0o600) }

// restrictDir makes the credential directory mode 0700, also when an
// earlier run or the user created it wider.
func restrictDir(dir string) error { return os.Chmod(dir, 0o700) }

func replaceCredentialFile(staged, current string) error {
	return wsstate.AtomicReplaceFile(staged, current)
}

func removeRetiringFile(file *os.File) error { return wsstate.RemoveFile(file.Name()) }
