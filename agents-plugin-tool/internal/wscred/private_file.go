package wscred

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// GenerationPaths returns the committed, retiring, and staged names in that
// order. The last two are private to the store's single writer.
func GenerationPaths(path string) (current, retiring, staged string) {
	return path, path + ".retiring", path + ".staged"
}

// OpenCredentialFile opens a credential generation for inspection.
// On Windows the handle permits concurrent reads, writes, and replacement.
func OpenCredentialFile(path string) (*os.File, error) {
	return openCredentialFile(path, false)
}

type credentialWriteStep int

const (
	credentialStaged credentialWriteStep = iota + 1
	credentialLinked
	credentialPublished
	credentialTruncated
	credentialRetired
)

// writePrivateFile stages owner-only bytes before protecting the existing
// generation with a store-owned link. Once published, the replacement is
// committed even if scrubbing needs a retry.
func writePrivateFile(path string, data []byte, replace func(string, string) error, step func(credentialWriteStep)) (published bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	if err := restrictDir(dir); err != nil {
		return false, err
	}
	_, retiring, staged := GenerationPaths(path)
	tmp, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer func() {
		if !published {
			err = errors.Join(err, scrubCredentialFile(staged, removeStagedFile))
		}
	}()
	err = restrictFile(tmp, staged)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err := errors.Join(err, tmp.Close()); err != nil {
		return false, err
	}
	if step != nil {
		step(credentialStaged)
	}
	var old *os.File
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("current credential generation is not a regular file")
		}
		if err := os.Link(path, retiring); err != nil {
			return false, fmt.Errorf("link retiring credential generation: %w", err)
		}
		if step != nil {
			step(credentialLinked)
		}
		// Acquire write/delete sharing and an identity-checked handle before
		// publication. A reader that blocks scrubbing must also block commit.
		old, err = openCredentialForScrub(retiring)
		if err != nil {
			return false, errors.Join(err, wsstate.RemoveFile(retiring))
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := replace(staged, path); err != nil {
		if old != nil {
			err = errors.Join(err, old.Close(), wsstate.RemoveFile(retiring))
		}
		return false, err
	}
	if step != nil {
		step(credentialPublished)
	}
	if old == nil {
		return true, nil
	}
	defer old.Close()
	return true, scrubOpenedCredentialFile(old, removeRetiringFile, step)
}

func openCredentialForScrub(path string) (*os.File, error) {
	file, err := openCredentialFile(path, true)
	if err != nil {
		return nil, err
	}
	if _, err := regularCredentialIdentity(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func scrubCredentialFile(path string, remove func(*os.File) error) error {
	file, err := openCredentialForScrub(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	return scrubOpenedCredentialFile(file, remove, nil)
}

func scrubOpenedCredentialFile(file *os.File, remove func(*os.File) error, step func(credentialWriteStep)) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if step != nil {
		step(credentialTruncated)
	}
	if err := remove(file); err != nil {
		return err
	}
	if step != nil {
		step(credentialRetired)
	}
	return nil
}

func removeStagedFile(file *os.File) error {
	return errors.Join(file.Close(), wsstate.RemoveFile(file.Name()))
}

func credentialIdentity(path string) (os.FileInfo, error) {
	file, err := OpenCredentialFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return regularCredentialIdentity(file)
}

func regularCredentialIdentity(file *os.File) (os.FileInfo, error) {
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("credential generation is not a regular file")
	}
	return info, err
}

func recoverPrivateFile(path string) error {
	_, retiring, staged := GenerationPaths(path)
	retired, err := credentialIdentity(retiring)
	if err == nil {
		current, err := credentialIdentity(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// Equal identities mean the interrupted writer never published.
		if current != nil && os.SameFile(current, retired) {
			err = wsstate.RemoveFile(retiring)
		} else {
			err = scrubCredentialFile(retiring, removeRetiringFile)
		}
		if err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return scrubCredentialFile(staged, removeStagedFile)
}
