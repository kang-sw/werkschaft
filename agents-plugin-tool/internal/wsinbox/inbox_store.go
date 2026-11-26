// Inbox entry naming, journaled persistence, sending, steering, and cancellation.
package wsinbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kang-sw/devenv/internal/wsstate"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// stampNow is the entry name clock (a test seam).
var stampNow = time.Now

var stampClock struct {
	sync.Mutex
	last time.Time
}

// nextStamp is strictly increasing within this process, so an entry written
// later (a send, or a steer's rewrite) always sorts after those before it.
func nextStamp() time.Time {
	stampClock.Lock()
	defer stampClock.Unlock()
	now := stampNow().UTC()
	if !now.After(stampClock.last) {
		now = stampClock.last.Add(time.Nanosecond)
	}
	stampClock.last = now
	return now
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// writeEntry commits entry under a fresh name in dir through temp+rename, so
// no reader sees a partial entry.
func writeEntry(dir string, entry Entry) (string, error) {
	return writeEntryJournaled(dir, entry, false)
}

func writeEntryJournaled(dir string, entry Entry, journal bool) (string, error) {
	stamp := nextStamp().Format(stampLayout)
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	suffix, err := randomHex(4)
	if err != nil {
		return "", err
	}
	name := entryFileName(stamp, suffix, entry)
	tmp, err := wsstate.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create inbox temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		remove(tmpPath)
		return "", fmt.Errorf("write inbox entry: %w", err)
	}
	if journal {
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			remove(tmpPath)
			return "", fmt.Errorf("sync inbox entry: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		remove(tmpPath)
		return "", fmt.Errorf("write inbox entry: %w", err)
	}
	var pending string
	if journal {
		journalDir := filepath.Join(dir, "answer-pending")
		if err := os.MkdirAll(journalDir, 0o700); err != nil {
			remove(tmpPath)
			return "", err
		}
		// Persist both the stage name and the journal directory before the
		// journal can be considered committed by the later rename.
		if err := syncInboxDir(dir); err != nil {
			remove(tmpPath)
			return "", fmt.Errorf("sync inbox directory: %w", err)
		}
		// Retain a copy even if the entry is drained or cancelled. The name
		// identifies the staging file: its disappearance is the atomic commit
		// point. A partial journal still has its stage and is discarded on replay.
		pending = filepath.Join(journalDir, name+"--"+filepath.Base(tmpPath))
		journalFile, err := wsstate.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			remove(tmpPath)
			return "", fmt.Errorf("journal inbox answer: %w", err)
		}
		_, writeErr := journalFile.Write(raw)
		if writeErr == nil {
			writeErr = journalFile.Sync()
		}
		if closeErr := journalFile.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			discardUncommitted(pending, tmpPath)
			return "", fmt.Errorf("journal inbox answer: %w", writeErr)
		}
		if err := syncInboxDir(journalDir); err != nil {
			discardUncommitted(pending, tmpPath)
			return "", fmt.Errorf("sync answer journal: %w", err)
		}
	}
	commit := rename
	if journal {
		commit = wsstate.AtomicReplaceFile
	}
	err = commit(tmpPath, filepath.Join(dir, name))
	bumpNames()
	if err != nil {
		// Keep the stage if removing the journal fails: recovery must still
		// recognize this as an uncommitted send.
		if pending == "" {
			remove(tmpPath)
		} else {
			discardUncommitted(pending, tmpPath)
		}
		return "", fmt.Errorf("commit inbox entry: %w", err)
	}
	if journal {
		// The write is already visible; a sync error cannot safely be reported
		// as a failed send without prompting a duplicate submission.
		if err := syncInboxDir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "wsinbox: sync published inbox entry: %v\n", err)
		}
	}
	return name, nil
}

func discardUncommitted(pending, stage string) {
	if remove(pending) == nil && syncInboxDir(filepath.Dir(pending)) == nil {
		remove(stage)
	}
}

func syncInboxDir(path string) error {
	// Windows uses a write-through publication; directory Sync is unsupported.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := wsstate.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readEntry(path string) (Entry, error) {
	raw, err := readFile(path)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Entry{}, fmt.Errorf("parse inbox entry: %w", err)
	}
	return provenCron(path, e)
}

// claim atomically takes the entry name in dir away from the inbox by
// renaming it to a hidden claim file. It returns ErrGone when another claim
// (a drain, a steer, a cancel) already took it.
func claim(dir, name, op string) (string, error) {
	suffix, err := randomHex(4)
	if err != nil {
		return "", err
	}
	claimed := filepath.Join(dir, "."+name+"."+op+"-"+suffix)
	if err := rename(filepath.Join(dir, name), claimed); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrGone
		}
		return "", err
	}
	return claimed, nil
}

// Send holds add in the inbox at dir as a new entry and returns its name;
// merged is always false (a send never joins a held entry, so every message
// keeps its own place in the queue). add is normalized first (Normalize);
// its SentAt is set from now when zero.
func Send(dir string, add Entry, now time.Time) (name string, merged bool, err error) {
	return sendEntry(dir, add, now, false)
}

// SendJournaled retains an answer until its board revision has been recorded.
func SendJournaled(dir string, add Entry, now time.Time) (name string, merged bool, err error) {
	return sendEntry(dir, add, now, true)
}

func sendEntry(dir string, add Entry, now time.Time, journal bool) (name string, merged bool, err error) {
	if add.SentAt == 0 {
		add.SentAt = now.UnixMilli()
	}
	add, err = Normalize(add)
	if err != nil {
		return "", false, err
	}
	// Every chip carries this send's time, whatever the caller put there.
	for i := range add.Chips {
		add.Chips[i].SentAt = add.SentAt
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("create inbox dir: %w", err)
	}
	if journal {
		if err := syncInboxDir(filepath.Dir(dir)); err != nil {
			return "", false, fmt.Errorf("sync inbox parent: %w", err)
		}
	}
	name, err = writeEntryJournaled(dir, add, journal)
	return name, false, err
}

// Steer marks the held entry name at dir for steering: it claims the entry
// and writes the same entry with Steer set under a fresh name, so entry files
// stay never rewritten. The fresh name's stamp is now, so the entry moves to
// the end of the steer queue. It returns the steered entry's name; an entry already
// steered is left as it is and its own name returned. A command entry is
// steered as text: its typed line (Command.Line) becomes the entry's one
// part and the command is dropped, so the adapter injects it mid-turn as a
// message the agent reads. It returns ErrGone when the entry is no longer
// held, or is an interrupt entry, which it leaves in place.
func Steer(dir, name string) (string, error) {
	return SteerWithAdmission(dir, name, nil)
}

// SteerWithAdmission checks the already-read, exclusively claimed entry before
// rewriting it. A refusal restores the original entry. Admission adds no read.
func SteerWithAdmission(dir, name string, admit func(Entry) error) (string, error) {
	if !entryNamePattern.MatchString(name) {
		return "", ErrGone
	}
	claimed, err := claim(dir, name, "steer")
	if err != nil {
		return "", err
	}
	entry, err := readEntry(claimed)
	if err == nil && entry.Interrupt {
		_ = rename(claimed, filepath.Join(dir, name))
		return "", ErrGone
	}
	if err == nil && admit != nil {
		if err := admit(entry); err != nil {
			_ = rename(claimed, filepath.Join(dir, name))
			return "", err
		}
	}
	if err != nil || entry.Steer {
		// Leave an unreadable or already steered entry held as it was.
		_ = rename(claimed, filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		return name, nil
	}
	entry.Steer = true
	if entry.Command != nil {
		entry.Parts = []string{entry.Command.Line()}
		entry.Command = nil
	}
	steered, err := writeEntry(dir, entry)
	if err != nil {
		// Put the claimed entry back so the message is not lost.
		_ = rename(claimed, filepath.Join(dir, name))
		return "", err
	}
	_ = remove(claimed)
	return steered, nil
}

// SteerAllWithAdmission atomically promotes every un-steered user held entry
// in dir (skipping interrupt, exec-notice, and already-steered entries) to
// steering. If no entries qualify, it performs zero file writes and returns
// (0, nil). admit runs after all qualifying entries are exclusively claimed
// and before any steered entry is persisted; an error restores every claimed
// entry with zero partial writes.
func SteerAllWithAdmission(dir string, admit func(entries []Entry) error) (int, error) {
	names, err := entryNames(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	eligible := func(e Entry) bool {
		if e.Interrupt || e.Steer {
			return false
		}
		_, isExec := e.ExecNotice()
		return !isExec
	}
	names = filterNames(dir, names, eligible)
	if len(names) == 0 {
		return 0, nil
	}
	type claimedItem struct {
		name    string
		claimed string
		entry   Entry
	}
	items := make([]claimedItem, 0, len(names))
	putBack := func() {
		for _, it := range items {
			_ = rename(it.claimed, filepath.Join(dir, it.name))
		}
	}
	for _, n := range names {
		claimed, err := claim(dir, n, "steer")
		if errors.Is(err, ErrGone) {
			continue
		}
		if err != nil {
			putBack()
			return 0, err
		}
		entry, err := readEntry(claimed)
		if err != nil || !eligible(entry) {
			_ = rename(claimed, filepath.Join(dir, n))
			continue
		}
		items = append(items, claimedItem{name: n, claimed: claimed, entry: entry})
	}
	if len(items) == 0 {
		return 0, nil
	}
	if admit != nil {
		entries := make([]Entry, len(items))
		for i, it := range items {
			entries[i] = it.entry
		}
		if err := admit(entries); err != nil {
			putBack()
			return 0, err
		}
	}
	written := make([]string, 0, len(items))
	for _, it := range items {
		e := it.entry
		e.Steer = true
		if e.Command != nil {
			e.Parts = []string{e.Command.Line()}
			e.Command = nil
		}
		steered, err := writeEntry(dir, e)
		if err != nil {
			for _, w := range written {
				_ = remove(filepath.Join(dir, w))
			}
			putBack()
			return 0, err
		}
		written = append(written, steered)
	}
	for _, it := range items {
		_ = remove(it.claimed)
	}
	return len(items), nil
}

// Cancel takes the held entry name out of the inbox at dir and returns it.
// It returns ErrGone when the entry is no longer held, or is an interrupt
// entry, which it leaves in place.
func Cancel(dir, name string) (Entry, error) {
	if !entryNamePattern.MatchString(name) {
		return Entry{}, ErrGone
	}
	claimed, err := claim(dir, name, "cancel")
	if err != nil {
		return Entry{}, err
	}
	entry, err := readEntry(claimed)
	if err != nil {
		// Leave an unreadable entry held rather than lose it.
		_ = rename(claimed, filepath.Join(dir, name))
		return Entry{}, err
	}
	if entry.Interrupt {
		_ = rename(claimed, filepath.Join(dir, name))
		return Entry{}, ErrGone
	}
	_ = remove(claimed)
	notifyCronRemoved(entry)
	return entry, nil
}
