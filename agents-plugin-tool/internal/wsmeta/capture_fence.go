package wsmeta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// Fence application: ApplyFence, ApplyRemovalFence, the fence plan, and proof that a fence is in place.

// ApplyFence replaces the legacy file at rel, a staged capture, with fenced:
// one of the cutover's one-time fences (2125). It follows the fence
// lifecycle above. The record (the fenced digest and a sentinel modification
// time) is durable first. Then fenced goes to a temporary file stamped with
// the sentinel, its data and time are synced, it atomically replaces rel,
// and the directory is synced before the fence is marked landed. The
// sentinel is a random even second in 2000-2009. Coarse file-system
// timestamps keep it, and no legacy writer produces it, because each writes
// the current time. So content a legacy writer wrote is never taken for the
// fence, even when it is byte-identical (2135).
//
// Re-applying after an interruption is safe. If the publication is already
// in place, ApplyFence publishes the same bytes under the same sentinel
// again, durably, and marks it landed; otherwise it publishes over the
// staged original. A live file that is neither
// returns ErrFenceStale. The directory must exist: a fence never recreates a
// pruned session. A session's directory must also be the one a pass staged:
// one marked missing or replaced since returns ErrFenceStale until a pass
// confirms it.
func (s *Store) ApplyFence(ctx context.Context, rel string, fenced []byte) error {
	s.cutover.Lock()
	defer s.cutover.Unlock()
	abs := filepath.Join(s.dir, filepath.FromSlash(rel))
	plan, err := s.planFence(ctx, rel, fenceReplace, contentDigest(fenced), "")
	if err != nil || plan.landed {
		return err
	}
	if err := s.hit("fence-prepared"); err != nil {
		return err
	}
	// A proven but unlanded publication is published again, identically:
	// its own write-through rename is the durability the platform documents,
	// where a barrier after another process's rename would only be inferred.
	if err := s.publishFile(abs, fenced, time.Unix(plan.mtime, 0)); err != nil {
		return err
	}
	if err := s.hit("fence-replaced"); err != nil {
		return err
	}
	return s.markFenceLanded(ctx, rel, fenceReplace, contentDigest(fenced), plan.mtime, "")
}

// ApplyRemovalFence removes the legacy file at rel, a staged capture with a
// fixed bundled name, after publishing its staged original bytes at
// witness: a file name in rel's directory, the new-daemon-only input path,
// fixed for rel once chosen (owner decision, board 2162). The publication is
// a replacement's (ApplyFence): the record (the original's digest and a
// sentinel mtime) is durable first, the bytes go to witness under the
// sentinel, and only then is rel deleted durably. A legacy symbolic link is
// read through, so the witness becomes a regular copy of its target; the
// link itself is deleted and its target left alone. Aliases changed during
// the cutover (hard links, swapped links) are not fenced; the staged
// original keeps their bytes.
//
// When rel was staged absent, nothing is published: the witness path is
// emptied, durably, of the migration's own earlier publication, so an
// owner's deletion is never resurrected through it. An unreadable original
// has no bytes to publish and refuses the cutover (ErrRemovalUnreadable).
// The witness path holding anything the migration did not publish refuses
// the removal (ErrWitnessOccupied).
//
// Right before the deletion rel is read again; a version other than the
// planned one is kept and the removal returns ErrFenceStale (stage, then
// apply again).
//
// A pass proves the removal only while rel is absent and the witness holds
// the publication, so an owner's own deletion of rel is never taken for the
// fence, and the import never undoes it (2135). Re-applying follows
// ApplyFence.
func (s *Store) ApplyRemovalFence(ctx context.Context, rel, witness string) error {
	if err := checkWitnessName(rel, witness); err != nil {
		return err
	}
	if !fixedBundledPath(rel) {
		// A path found by enumeration loses its row once absent, and with
		// it the fence obligation.
		return fmt.Errorf("wsmeta: %q has no fixed bundled name to remove", rel)
	}
	s.cutover.Lock()
	defer s.cutover.Unlock()
	witnessRel := path.Join(path.Dir(rel), witness)
	abs := filepath.Join(s.dir, filepath.FromSlash(rel))
	wabs := filepath.Join(s.dir, filepath.FromSlash(witnessRel))
	plan, err := s.planFence(ctx, rel, fenceRemove, "", witnessRel)
	if err != nil || plan.landed {
		return err
	}
	if err := s.hit("fence-prepared"); err != nil {
		return err
	}
	if plan.state == CapturePresent {
		// Published again when already proven, as ApplyFence does.
		if err := s.publishFile(wabs, plan.bytes, time.Unix(plan.mtime, 0)); err != nil {
			return err
		}
	}
	switch _, err := wsstate.Lstat(abs); {
	case err == nil:
		// Compare before delete: only the planned version, the one published
		// at the witness, is retired. An owner's save since then is kept and
		// the fence goes stale, so a pass stages the newer version. The
		// window between this read and the deletion is not fenced.
		now := readLegacy(abs, s.opts.ReadFile)
		if now.transient {
			return fmt.Errorf("%w: %s: %s", ErrLegacyUnread, rel, now.err)
		}
		if now.state != plan.state || now.digest != plan.digest {
			return fmt.Errorf("%w: %s", ErrFenceStale, rel)
		}
		if err := s.removeDurably(abs); err != nil {
			return err
		}
		if err := s.hit("fence-deleted"); err != nil {
			return err
		}
	case errors.Is(err, fs.ErrNotExist):
		// An interrupted attempt deleted it: the barrier makes that
		// deletion durable.
		if err := s.syncDir(filepath.Dir(abs)); err != nil {
			return err
		}
	default:
		return err
	}
	if err := s.hit("fence-replaced"); err != nil {
		return err
	}
	return s.markFenceLanded(ctx, rel, fenceRemove, plan.digest, plan.mtime, witnessRel)
}

// checkWitnessName accepts a plain file name, local on every platform. It
// must not be a dot name (the temp and barrier namespace), a bundled file
// name, or rel's own name.
func checkWitnessName(rel, witness string) error {
	bad := witness == "" || strings.ContainsAny(witness, `/\:`) || strings.HasPrefix(witness, ".") ||
		!filepath.IsLocal(witness) || witness == path.Base(rel)
	for _, f := range sessionFiles {
		bad = bad || witness == f.name
	}
	for _, f := range dashboardFiles {
		bad = bad || witness == f.name
	}
	for _, d := range dashboardDirs {
		bad = bad || witness == d.name
	}
	if bad {
		return fmt.Errorf("wsmeta: invalid removal witness %q", witness)
	}
	return nil
}

// fixedBundledPath reports whether rel is a fixed-name bundled file: one a
// pass captures whether or not it exists.
func fixedBundledPath(rel string) bool {
	for _, f := range dashboardFiles {
		if rel == f.name {
			return true
		}
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[0] == sessionsDirName && replyIDPattern.MatchString(parts[1]) {
		for _, f := range sessionFiles {
			if parts[2] == f.name {
				return true
			}
		}
	}
	return false
}

// fencePlan is what a fence application does after its record is settled.
type fencePlan struct {
	state  string // the staged original's state
	digest string // the publication's digest
	bytes  []byte // a removal's published bytes: the staged original
	mtime  int64  // the publication's sentinel
	proven bool   // the publication is already live
	landed bool   // ...and marked landed: nothing left to do
}

// planFence settles the record of a fence on rel inside one durable
// transaction (invariants 2 and 4). A proven publication keeps its record;
// a live file equal to the staged original gets a fresh record; anything
// else is refused.
func (s *Store) planFence(ctx context.Context, rel, kind, digest, witness string) (fencePlan, error) {
	var plan fencePlan
	err := s.write(ctx, durable, func(tx *sql.Tx) error {
		state, _, err := importState(ctx, tx)
		if err != nil {
			return err
		}
		if state == importComplete {
			return ErrImportComplete
		}
		m, ok, err := loadCaptureMeta(ctx, tx, rel)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("wsmeta: no staged capture %q to fence", rel)
		}
		if err := s.checkStagedSession(ctx, tx, rel); err != nil {
			return err
		}
		if kind == fenceRemove && m.fenceWitness != "" && m.fenceWitness != witness {
			return fmt.Errorf("%w: %s is published at %s", ErrFenceConflict, rel, m.fenceWitness)
		}
		abs := filepath.Join(s.dir, filepath.FromSlash(rel))
		// Before the record: a fence that could not be made durable is never
		// recorded or published.
		if reason := barrierRefusal(filepath.Dir(abs), true); reason != nil {
			return s.refuseCutover(ctx, tx, reason)
		}
		if kind == fenceRemove {
			if m.state == CaptureUnreadable {
				return s.refuseCutover(ctx, tx, fmt.Errorf("%w: %s", ErrRemovalUnreadable, rel))
			}
			digest = m.digest
		}
		got := readLegacy(abs, s.opts.ReadFile)
		if got.confirmable && m.state == CaptureUnreadable {
			// A denial a pass confirmed is the original's state while it
			// persists (stagePass), so the fence plans over it; a denial
			// over an original read before still refuses below.
			got = legacyRead{state: CaptureUnreadable, err: got.err}
		}
		if got.transient {
			return fmt.Errorf("%w: %s: %s", ErrLegacyUnread, rel, got.err)
		}
		proven, unread := s.fenceProven(m, got)
		if unread {
			return fmt.Errorf("%w: witness of %s", ErrLegacyUnread, rel)
		}
		plan = fencePlan{state: m.state, digest: digest, mtime: m.fenceMtime, proven: proven}
		if kind == fenceRemove && m.state == CapturePresent {
			if err := tx.QueryRowContext(ctx, `SELECT bytes FROM legacy_captures WHERE path = ?`, rel).Scan(&plan.bytes); err != nil {
				return err
			}
		}
		if proven {
			if m.fenceKind != kind || kind == fenceReplace && m.fenceDigest != digest {
				return fmt.Errorf("%w: %s", ErrFenceConflict, rel)
			}
			plan.landed = m.fenceState == FenceLanded
			return nil
		}
		if got.state != m.state || got.digest != m.digest {
			return fmt.Errorf("%w: %s", ErrFenceStale, rel)
		}
		if kind == fenceReplace {
			plan.mtime = distinctFenceSentinel(abs).Unix()
			if err := markCutoverStarted(ctx, tx); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE legacy_captures SET fence_state = 'intent', fence_kind = ?, fence_digest = ?, fence_mtime = ?, fence_witness = '', fence_required = 1 WHERE path = ?`,
				kind, digest, plan.mtime, rel)
			return err
		}
		// The witness path is durably empty before the record, so the
		// sticky witness columns always name what the path can hold, and no
		// earlier publication proves the new record.
		wabs := filepath.Join(s.dir, filepath.FromSlash(witness))
		own, err := s.witnessOwned(m, wabs)
		if err != nil {
			return err
		}
		if own {
			if err := s.removeDurably(wabs); err != nil {
				return err
			}
		}
		if m.state == CapturePresent {
			plan.mtime = fenceSentinel().Unix()
		}
		if err := markCutoverStarted(ctx, tx); err != nil {
			return err
		}
		wdigest := ""
		if m.state == CapturePresent {
			wdigest = digest
		}
		_, err = tx.ExecContext(ctx, `UPDATE legacy_captures SET fence_state = 'intent', fence_kind = ?, fence_digest = ?, fence_mtime = ?, fence_witness = ?, fence_required = 1, witness_digest = ?, witness_mtime = ? WHERE path = ?`,
			kind, digest, plan.mtime, witness, wdigest, plan.mtime, rel)
		return err
	})
	return plan, err
}

// checkStagedSession refuses a fence on a session row until a pass has
// confirmed the live directory: the session not marked missing, and its
// directory of the row's generation. A row kept for a missing session, or
// one of a directory since replaced under the same reply id, is not the live
// directory's original (ErrFenceStale: stage, then fence again).
func (s *Store) checkStagedSession(ctx context.Context, tx *sql.Tx, rel string) error {
	var id, gen string
	var missing bool
	if err := tx.QueryRowContext(ctx, `SELECT c.reply_id, c.session_gen, COALESCE(ls.missing, 1)
		FROM legacy_captures c LEFT JOIN legacy_sessions ls ON ls.reply_id = c.reply_id WHERE c.path = ?`, rel).Scan(&id, &gen, &missing); err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	cur, ok, err := s.sessionGeneration(id)
	if err != nil {
		return err
	}
	if missing || !ok || !sameGeneration(cur, gen) {
		return fmt.Errorf("%w: session %s is not the directory a pass staged", ErrFenceStale, id)
	}
	return nil
}

// witnessOwned reports whether the witness path wabs holds the migration's
// last removal publication there (invariant 3): a regular file with
// witness_digest under exactly witness_mtime. An empty path is not owned
// and free; any other entry refuses the removal.
func (s *Store) witnessOwned(m captureMeta, wabs string) (bool, error) {
	info, err := wsstate.Lstat(wabs)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: witness %s: %v", ErrLegacyUnread, filepath.Base(wabs), err)
	}
	if info.Mode().IsRegular() && m.witnessMtime != 0 {
		w := readLegacy(wabs, s.opts.ReadFile)
		if w.transient {
			return false, fmt.Errorf("%w: witness %s: %s", ErrLegacyUnread, filepath.Base(wabs), w.err)
		}
		if publishedAs(w, m.witnessDigest, m.witnessMtime) {
			return true, nil
		}
	}
	return false, fmt.Errorf("%w: %s", ErrWitnessOccupied, filepath.Base(wabs))
}

// publishedAs reports whether a read is the migration's publication of
// digest under the sentinel mtime.
func publishedAs(r legacyRead, digest string, mtime int64) bool {
	return r.state == CapturePresent && r.digest == digest &&
		r.mtime.Unix() == mtime && r.mtime.Nanosecond() == 0
}

// fenceProven reports whether the live state is the publication of the
// row's fence record (invariant 2): the record's digest under its exact
// sentinel time, at the path for a replacement, at the witness with the path
// absent for a removal. A removal of an absent original is proven by both
// absent. unread is set when the witness could not be read for a transient
// reason.
func (s *Store) fenceProven(prev captureMeta, got legacyRead) (proven, unread bool) {
	if prev.fenceState == FenceNone {
		return false, false
	}
	if prev.fenceKind != fenceRemove {
		return publishedAs(got, prev.fenceDigest, prev.fenceMtime), false
	}
	if got.state != CaptureAbsent {
		return false, false
	}
	w := readLegacy(filepath.Join(s.dir, filepath.FromSlash(prev.fenceWitness)), s.opts.ReadFile)
	if w.transient {
		return false, true
	}
	if prev.fenceDigest == absentDigest {
		return w.state == CaptureAbsent, false
	}
	return publishedAs(w, prev.fenceDigest, prev.fenceMtime), false
}
