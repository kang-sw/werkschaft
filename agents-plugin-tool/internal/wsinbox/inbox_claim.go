package wsinbox

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// CronClaimDisposition records the background decision for a staged input.
type CronClaimDisposition string

const (
	CronClaimAuthorized CronClaimDisposition = "authorized"
	CronClaimRefused    CronClaimDisposition = "refused"
)

const cronClaimField = "runnerCronClaim"

func claimDir(dir string) string { return filepath.Join(dir, "runner-claims") }

// VisitTurnInputPaths visits held and claimed inputs a durable runner can
// restore, without opening their contents.
func VisitTurnInputPaths(dir string, visit func(string) error) error {
	for _, folder := range []string{dir, claimDir(dir)} {
		names, err := entryNames(folder)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := visit(filepath.Join(folder, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// DiscardClaims drops staging for a closed conversation. The runner must
// acknowledge committed claims first and stop admitting claims before this.
func DiscardClaims(dir string) error { return os.RemoveAll(claimDir(dir)) }

// PendingClaims reads inbox entries a durable runner took before it could
// commit their input queue. The private queue record lists the names from
// its last committed claim, which are already represented in that queue.
func PendingClaims(dir string, committed []string) ([]Record, error) {
	claims := claimDir(dir)
	names, err := Names(claims)
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, name := range names {
		if slices.Contains(committed, name) {
			continue
		}
		path := filepath.Join(claims, name)
		raw, err := readFile(path)
		if err != nil {
			continue
		}
		var body struct {
			Entry
			Disposition CronClaimDisposition `json:"runnerCronClaim"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			continue
		}
		entry, err := provenCron(path, body.Entry)
		if err != nil {
			continue
		}
		rec := Record{Name: name, Entry: entry}
		if entry.Cron != nil {
			switch body.Disposition {
			case CronClaimAuthorized, CronClaimRefused:
				rec.CronClaim = body.Disposition
			}
		}
		records = append(records, rec)
	}
	return records, nil
}

// RecordCronClaim atomically augments the claimed entry before the runner
// commits it. A crash before publication leaves an unclassified claim.
func RecordCronClaim(dir string, rec Record) error {
	if rec.Entry.Cron == nil || !entryNamePattern.MatchString(rec.Name) || !isCronFile(rec.Name) ||
		(rec.CronClaim != CronClaimAuthorized && rec.CronClaim != CronClaimRefused) {
		return ErrInvalid
	}
	claims := claimDir(dir)
	path := filepath.Join(claims, rec.Name)
	raw, err := readFile(path)
	if err != nil {
		return err
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return err
	}
	if entry == nil {
		return ErrInvalid
	}
	admission, err := json.Marshal(rec.CronClaim)
	if err != nil {
		return err
	}
	entry[cronClaimField] = admission
	raw, err = json.Marshal(entry)
	if err != nil {
		return err
	}
	tmp, err := wsstate.CreateTemp(claims, ".tmp-*")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = remove(tmp.Name())
		}
	}()
	_, err = tmp.Write(raw)
	if err == nil {
		err = tmp.Sync()
	}
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmp.Name(), path); err != nil {
		return err
	}
	published = true
	return syncInboxDir(claims)
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
			if rec.CronClaim != "" {
				if err := RecordCronClaim(dir, rec); err != nil && firstErr == nil {
					firstErr = err
				}
			}
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
