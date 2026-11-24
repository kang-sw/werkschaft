package wsinbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

func claimDir(dir string) string { return filepath.Join(dir, "runner-claims") }

// DiscardClaims drops staging for a closed conversation. The runner must
// acknowledge committed claims first and stop admitting claims before this.
func DiscardClaims(dir string) error { return os.RemoveAll(claimDir(dir)) }

// PendingClaims reads inbox entries a durable runner took before it could
// commit their input queue. The private queue record lists the names from
// its last committed claim, which are already represented in that queue.
func PendingClaims(dir string, committed []string) ([]Record, error) {
	records, err := readAll(claimDir(dir))
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(records, func(rec Record) bool {
		return slices.Contains(committed, rec.Name)
	}), nil
}

// ClaimInputs moves held messages into the durable runner's staging area.
// Cancel and steer still race the claim's rename as they do for Drain. The
// caller supplies the held snapshot it has already read, since claim never
// needs to reread an immutable entry after winning its rename.
func ClaimInputs(dir string, held []Record) ([]Record, error) {
	if len(held) == 0 {
		return nil, nil
	}
	claims := claimDir(dir)
	if err := os.MkdirAll(claims, 0o755); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(held))
	var firstErr error
	for _, rec := range held {
		if !entryNamePattern.MatchString(rec.Name) || rec.Entry.Interrupt {
			continue
		}
		err := rename(filepath.Join(dir, rec.Name), filepath.Join(claims, rec.Name))
		switch {
		case err == nil:
			out = append(out, rec)
		case !errors.Is(err, fs.ErrNotExist) && firstErr == nil:
			firstErr = err
		}
	}
	notifyCronRemoved(recordEntries(out)...)
	return out, firstErr
}

// AcknowledgeClaims moves committed claims to the regular drained history.
// It is safe to repeat after a partial move or daemon replacement.
func AcknowledgeClaims(dir string, committed []string) error {
	if len(committed) == 0 {
		return nil
	}
	drained := DrainedDir(dir)
	if err := os.MkdirAll(drained, 0o755); err != nil {
		return err
	}
	claims := claimDir(dir)
	for _, name := range committed {
		if !entryNamePattern.MatchString(name) {
			return ErrInvalid
		}
		err := rename(filepath.Join(claims, name), filepath.Join(drained, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	trimDrained(dir)
	return nil
}
