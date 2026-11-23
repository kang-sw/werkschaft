package wsfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FileIntent is the durable recovery evidence for one admitted file mutation.
// A nil Prior explicitly represents an absent file.
type FileIntent struct {
	Target   string  `json:"target"`
	Prior    *string `json:"prior"`
	Expected string  `json:"expected"`
}

// Outcome checks the recorded target. Contents already equal to the expected
// bytes satisfy the mutation, including when the prior and expected match.
func (i FileIntent) Outcome() string {
	if !filepath.IsAbs(i.Target) || !sameResolvedPath(i.Target) {
		return "outcome unknown"
	}
	current, _, err := inspectFile(i.Target, false)
	if err != nil || !sameResolvedPath(i.Target) {
		return "outcome unknown"
	}
	if current.digest != nil && *current.digest == i.Expected {
		return "applied"
	}
	switch {
	case current.digest == nil && i.Prior == nil:
		return "not applied"
	case current.digest != nil && i.Prior != nil && *current.digest == *i.Prior:
		return "not applied"
	default:
		return "outcome unknown"
	}
}

type fileSnapshot struct {
	info   fs.FileInfo
	digest *string
}

func inspectFile(path string, keepData bool) (fileSnapshot, []byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileSnapshot{}, nil, nil
	}
	if err != nil {
		return fileSnapshot{}, nil, err
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, nil, fmt.Errorf("%s is not a regular file", path)
	}
	file, err := openMutationFile(path, os.O_RDONLY)
	if err != nil {
		return fileSnapshot{}, nil, err
	}
	defer file.Close()
	if err := verifyFile(file, info); err != nil {
		return fileSnapshot{}, nil, err
	}
	var data []byte
	var digest string
	if keepData {
		data, err = io.ReadAll(file)
		if err == nil {
			digest = contentHash(data)
		}
	} else {
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		if err == nil {
			digest = hex.EncodeToString(hash.Sum(nil))
		}
	}
	if err != nil {
		return fileSnapshot{}, nil, err
	}
	if err := verifyFile(file, info); err != nil {
		return fileSnapshot{}, nil, err
	}
	return fileSnapshot{info: info, digest: &digest}, data, nil
}

func verifyFile(file *os.File, prior fs.FileInfo) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(prior, info) || prior.Size() != info.Size() || !prior.ModTime().Equal(info.ModTime()) {
		return fmt.Errorf("%s changed while preparing the mutation", file.Name())
	}
	return nil
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sameResolvedPath(path string) bool {
	resolved, err := realPath(path)
	return err == nil && resolved == path
}

func (f *Files) commitFile(ctx context.Context, path string, prior fileSnapshot, data []byte) error {
	expected := contentHash(data)
	if f.RecordMutation != nil {
		if err := f.RecordMutation(ctx, FileIntent{Target: path, Prior: prior.digest, Expected: expected}); err != nil {
			return fmt.Errorf("record file mutation intent: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !sameResolvedPath(path) {
		return fmt.Errorf("%s changed while preparing the mutation", path)
	}
	flags := os.O_WRONLY
	if prior.info == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if !sameResolvedPath(path) {
			return fmt.Errorf("%s changed while preparing the mutation", path)
		}
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := openMutationFile(path, flags)
	if err != nil {
		return err
	}
	defer file.Close()
	if prior.info != nil {
		if !sameResolvedPath(path) {
			return fmt.Errorf("%s changed while preparing the mutation", path)
		}
		if err := verifyFile(file, prior.info); err != nil {
			return err
		}
		if *prior.digest == expected {
			return nil
		}
		if err := file.Truncate(0); err != nil {
			return err
		}
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Close()
}
