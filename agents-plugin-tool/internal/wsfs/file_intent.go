package wsfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FileCall correlates the daemon's recovery evidence with the model request
// that authorized the mutation. Only the intent is retained on disk.
type FileCall struct {
	ID, Tool  string
	Arguments json.RawMessage
	Authority Authority
	Intent    FileIntent
}

// FileWriteSession is the daemon-issued session binding for file calls.
// CurrentAuthority returns the live level, or empty after revocation. Record
// validates the active model call and persists its intent before it returns.
type FileWriteSession struct {
	Root, Cwd        string
	Authority        Authority
	CurrentAuthority func() Authority
	// LiveAuthority is set when the harness applies mode changes in the current process.
	LiveAuthority bool
	// HoldAuthority orders preparation and mutation against posture changes.
	HoldAuthority func() (release func())
	// Approve waits for the operator when a scoped model call needs a grant.
	// Call it outside HoldAuthority before retrying admission.
	Approve func(context.Context, FileCall) error
	Record  func(context.Context, FileCall) error
}

// AuthorityForCall snapshots the permission level to pin for one file operation.
func (s *FileWriteSession) AuthorityForCall() Authority {
	if s.CurrentAuthority == nil {
		return s.Authority
	}
	current := s.CurrentAuthority()
	if s.LiveAuthority || current == s.Authority {
		return current
	}
	return ""
}

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
	if !filepath.IsAbs(i.Target) {
		return "outcome unknown"
	}
	target, err := pinMutationTarget(i.Target)
	if err != nil {
		return "outcome unknown"
	}
	defer target.close()
	current, _, err := target.inspect(false, nil)
	if err != nil || target.checkParent() != nil {
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

func (t *mutationTarget) inspect(keepData bool, protection FileProtection) (fileSnapshot, []byte, error) {
	if len(t.missing) != 0 {
		return fileSnapshot{}, nil, nil
	}
	file, err := openMutationFile(t.parent, filepath.Base(t.path), os.O_RDONLY)
	if errors.Is(err, fs.ErrNotExist) {
		return fileSnapshot{}, nil, nil
	}
	if err != nil {
		return fileSnapshot{}, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fileSnapshot{}, nil, err
	}
	if protection != nil {
		if err := protection.CheckOpened(file, info); err != nil {
			return fileSnapshot{}, nil, err
		}
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, nil, fmt.Errorf("%s is not a regular file", t.path)
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
	if _, err := verifyFile(file, info); err != nil {
		return fileSnapshot{}, nil, err
	}
	return fileSnapshot{info: info, digest: &digest}, data, nil
}

func verifyFile(file *os.File, prior fs.FileInfo) (fs.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(prior, info) || prior.Size() != info.Size() || !prior.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("%s changed while preparing the mutation", file.Name())
	}
	return info, nil
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (f *Files) commitFile(ctx context.Context, target *mutationTarget, prior fileSnapshot, data []byte) error {
	expected := contentHash(data)
	if f.RecordMutation != nil {
		if err := f.RecordMutation(ctx, FileIntent{Target: target.path, Prior: prior.digest, Expected: expected}); err != nil {
			return fmt.Errorf("record file mutation intent: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := target.checkParent(); err != nil {
		return err
	}
	var protection FileProtection
	if f.Protection != nil {
		// The credential file may have been linked or replaced while the
		// durable mutation intent was recorded.
		protection = f.Protection()
		if err := protection.CheckPath(target.path); err != nil {
			return err
		}
		if err := target.checkProtectedParent(protection); err != nil {
			return err
		}
	}
	if f.Authority == AuthorityWorkdirWrite && (prior.digest == nil || *prior.digest != expected) {
		return target.commitReplacement(ctx, prior, data, protection, f.beforeReplacementWrite)
	}
	flags := os.O_WRONLY
	if prior.info == nil {
		if err := target.createParents(); err != nil {
			return err
		}
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := openMutationFile(target.parent, filepath.Base(target.path), flags)
	if err != nil {
		return err
	}
	defer file.Close()
	if prior.info != nil {
		if _, err := verifyMutationDestination(file, prior.info, protection); err != nil {
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

func verifyMutationDestination(file *os.File, prior fs.FileInfo, protection FileProtection) (fs.FileInfo, error) {
	info, err := verifyFile(file, prior)
	if err == nil && protection != nil {
		err = protection.CheckOpened(file, info)
	}
	return info, err
}

// A scoped mutation publishes a complete new file in the pinned parent.
// Hard links to the previous entry continue to refer to the previous inode.
func (t *mutationTarget) commitReplacement(ctx context.Context, prior fileSnapshot, data []byte, protection FileProtection, beforeWrite func(*os.File)) error {
	if err := t.createParents(); err != nil {
		return err
	}
	name := filepath.Base(t.path)
	var permissions *mutationPermissions
	if prior.info != nil {
		file, err := openMutationFile(t.parent, name, os.O_WRONLY)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := lockMutationDestination(file); err != nil {
			return err
		}
		info, err := verifyMutationDestination(file, prior.info, protection)
		if err != nil {
			return err
		}
		permissions, err = readMutationPermissions(file, info)
		if err != nil {
			return fmt.Errorf("read replacement permissions for %s: %w", t.path, err)
		}
	}
	stagingParent, tempName, cleanup, err := mutationStagingLocation(t.parent, permissions != nil)
	if err != nil {
		return err
	}
	defer cleanup()
	tmp, err := openMutationTemp(stagingParent, tempName, permissions)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			discardMutationTemp(stagingParent, tmp)
		}
		_ = tmp.Close()
	}()
	if permissions != nil {
		if err := permissions.prepare(tmp); err != nil {
			return fmt.Errorf("prepare replacement permissions for %s: %w", t.path, err)
		}
	}
	if beforeWrite != nil {
		beforeWrite(tmp)
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.checkParent(); err != nil {
		return err
	}
	if permissions != nil {
		if err := permissions.verify(tmp, nil); err != nil {
			return fmt.Errorf("verify staged replacement permissions for %s: %w", t.path, err)
		}
		file, err := openMutationFile(t.parent, name, os.O_WRONLY)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := verifyMutationDestination(file, prior.info, protection)
		if err != nil {
			return err
		}
		if err := permissions.verify(file, info); err != nil {
			return fmt.Errorf("recheck destination permissions for %s: %w", t.path, err)
		}
	}
	if err := publishMutationTemp(t.parent, stagingParent, tmp, name, prior.info != nil); err != nil {
		return err
	}
	published = true
	return tmp.Close()
}

func siblingMutationStagingLocation(parent *os.File) (*os.File, string, func(), error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", nil, err
	}
	return parent, ".ws-write-" + hex.EncodeToString(random[:]), func() {}, nil
}
