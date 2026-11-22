package werk

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// store.go is the conversation's persistence. The live transcript is the
// process's memory; the store only makes it recoverable (a process restart
// that resumes the session, Backend.Start with a session id).
//
// Storage type 1 of ai-docs/manuals/performance.md (an append-only log, read
// back by replay): one JSONL file per session, opened once and held, one
// write() per change, never read again after startup. The first line is a
// header, so a segment is self-describing; every later line is one transcript
// item as the provider sent or the harness built it. Recovery trims a torn
// last line instead of relying on fsync. There is no rewrite: a compaction
// that shortens the transcript starts a new segment whose first line is the
// snapshot of what it keeps, and deletes the old one whole (Rotate).

// storeDirName is the directory under a session's directory that holds the
// conversation segments.
const storeDirName = "werk"

// storeVersion is the segment format version.
const storeVersion = 1

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{16,64}$`)

// NewSessionID draws a fresh session id.
func NewSessionID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic("werk: no random source: " + err.Error())
	}
	return hex.EncodeToString(raw)
}

// ValidSessionID reports whether id is a session id this package drew.
func ValidSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

type segmentHeader struct {
	K       string `json:"k"`
	V       int    `json:"v"`
	Session string `json:"session"`
	At      int64  `json:"at"`
	// Snapshot is how many items follow as the segment's opening snapshot
	// (a rotated segment); 0 for a first segment.
	Snapshot int `json:"snapshot,omitempty"`
}

type itemLine struct {
	K    string          `json:"k"`
	Item json.RawMessage `json:"item"`
}

// Store is one session's conversation file.
type Store struct {
	mu      sync.Mutex
	dir     string
	session string
	path    string
	f       *os.File
	now     func() int64
}

func segmentPath(dir, session string) string {
	return filepath.Join(dir, storeDirName, session+".jsonl")
}

// OpenStore opens session's file under dir and returns the items it holds. A
// session with no file starts one. A torn last line is dropped, and the file
// truncated to its last whole line, so the next append starts clean.
func OpenStore(dir, session string, now func() int64) (*Store, []json.RawMessage, error) {
	if !ValidSessionID(session) {
		return nil, nil, fmt.Errorf("%q is not a werk session id", session)
	}
	if now == nil {
		now = func() int64 { return 0 }
	}
	path := segmentPath(dir, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	s := &Store{dir: dir, session: session, path: path, now: now}
	items, keep, err := readSegment(path, session)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if keep >= 0 {
		if err := f.Truncate(keep); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, nil, err
	}
	s.f = f
	if keep <= 0 {
		if err := s.writeLocked(segmentHeader{K: "head", V: storeVersion, Session: session, At: now()}); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	return s, items, nil
}

// readSegment returns the items of the file at path and the length of its
// whole-line prefix (-1 when the file does not exist).
func readSegment(path, session string) ([]json.RawMessage, int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, -1, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var items []json.RawMessage
	var good int64
	rest := data
	first := true
	for len(rest) > 0 {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		line := rest[:nl]
		if first {
			var h segmentHeader
			if err := json.Unmarshal(line, &h); err != nil || h.K != "head" {
				return nil, 0, fmt.Errorf("%s is not a werk conversation segment", filepath.Base(path))
			}
			if h.V > storeVersion {
				return nil, 0, fmt.Errorf("%s was written by a newer werk (segment version %d)", filepath.Base(path), h.V)
			}
			if h.Session != session {
				return nil, 0, fmt.Errorf("%s belongs to session %s", filepath.Base(path), h.Session)
			}
			first = false
		} else {
			var l itemLine
			if err := json.Unmarshal(line, &l); err != nil || l.K != "item" || len(l.Item) == 0 {
				break
			}
			items = append(items, append(json.RawMessage(nil), l.Item...))
		}
		good += int64(nl + 1)
		rest = rest[nl+1:]
	}
	return items, good, nil
}

func (s *Store) writeLocked(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.f.Write(append(raw, '\n'))
	return err
}

// Append adds one transcript item: one line, one write.
func (s *Store) Append(item json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return errors.New("the conversation store is closed")
	}
	return s.writeLocked(itemLine{K: "item", Item: item})
}

// Rotate starts a new segment holding items as its snapshot and deletes the
// old one whole. The session id is kept; the new segment takes the file's
// name once the old one is gone.
func (s *Store) Rotate(items []json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return errors.New("the conversation store is closed")
	}
	tmp := s.path + ".next"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	head, _ := json.Marshal(segmentHeader{K: "head", V: storeVersion, Session: s.session, At: s.now(), Snapshot: len(items)})
	buf.Write(head)
	buf.WriteByte('\n')
	for _, it := range items {
		line, _ := json.Marshal(itemLine{K: "item", Item: it})
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// The old file is closed before the rename: Windows will not replace a
	// file that is open.
	s.f.Close()
	s.f = nil
	renameErr := os.Rename(tmp, s.path)
	if renameErr != nil {
		os.Remove(tmp)
	}
	nf, err := os.OpenFile(s.path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	s.f = nf
	return renameErr
}

// Close releases the file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}
