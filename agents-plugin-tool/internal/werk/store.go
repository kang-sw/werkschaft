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

	"github.com/gofrs/flock"

	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// store.go is the conversation's persistence. The live transcript is the
// process's memory; the store only makes it recoverable (a process restart
// that resumes the session, Backend.Start with a session id).
//
// Storage type 1 of ai-docs/manuals/performance.md (an append-only log, read
// back by replay): one JSONL file per session, opened once and held, one
// write() per change. Reads occur at startup and when compacting the raw
// history at an idle boundary. The first line is a
// header, so a segment is self-describing. Each later line is a transcript
// item, a complete model response, or recovery evidence for a mutation. Recovery discards a torn tail so it
// cannot mistake part of a response for committed model output.

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

type responseLine struct {
	K        string    `json:"k"`
	Response *Response `json:"response"`
}

type fileIntentLine struct {
	K      string           `json:"k"`
	CallID string           `json:"call_id"`
	Intent *wsfs.FileIntent `json:"intent"`
}

type execJobLine struct {
	K      string `json:"k"`
	CallID string `json:"call_id"`
	JobID  string `json:"job_id"`
}

// Store is one session's conversation file.
type Store struct {
	mu              sync.Mutex
	dir             string
	session         string
	path            string
	f               *os.File
	lock            *flock.Flock
	now             func() int64
	fileIntents     map[string]wsfs.FileIntent
	execJobs        map[string]string
	pendingApproval map[string]bool
	turnJournal     turnJournal
	checkpoint      *checkpointLine
}

func segmentPath(dir, session string) string {
	return filepath.Join(dir, wsstate.WerkJournalDirName, session+".jsonl")
}

// OpenStore opens session's file under dir and returns the items it holds. A
// session with no file starts one. The writer lease covers replay and repair
// as well as appends: a successor cannot trim a predecessor's live log.
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
	// The lease has its own inode because rotation replaces the segment.
	lock := flock.New(path + ".lock")
	acquired, err := lock.TryLock()
	if err != nil {
		return nil, nil, fmt.Errorf("acquire conversation writer: %w", err)
	}
	if !acquired {
		return nil, nil, errors.New("the conversation already has a writer")
	}
	s, items, err := openStore(dir, session, path, now)
	if err != nil {
		return nil, nil, errors.Join(err, lock.Close())
	}
	s.lock = lock
	return s, items, nil
}

func openStore(dir, session, path string, now func() int64) (*Store, []json.RawMessage, error) {
	s := &Store{dir: dir, session: session, path: path, now: now}
	s.fileIntents = make(map[string]wsfs.FileIntent)
	s.execJobs = make(map[string]string)
	items, keep, err := s.readSegment(nil)
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
func (s *Store) readSegment(archive *compactArchive) ([]json.RawMessage, int64, error) {
	path, session := s.path, s.session
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
			var l struct {
				fileIntentLine
				TurnID   string          `json:"turn_id"`
				JobID    string          `json:"job_id"`
				SteerID  string          `json:"steer_id"`
				User     json.RawMessage `json:"user"`
				End      *durableEnd     `json:"end"`
				Item     json.RawMessage `json:"item"`
				Response *Response       `json:"response"`
				Summary  string          `json:"summary"`
				ID       string          `json:"id"`
				At       int64           `json:"at"`
				Sequence uint64          `json:"sequence"`
			}
			if err := json.Unmarshal(line, &l); err != nil {
				break
			}
			switch {
			case l.K == "item" && len(l.Item) > 0:
				items = append(items, l.Item)
				archive.observe(l.Item)
			case l.K == "response" && l.Response != nil:
				clear(s.pendingApproval)
				items = append(items, l.Response.Output...)
				for _, item := range l.Response.Output {
					archive.observe(item)
				}
				if s.turnJournal.id != "" && s.turnJournal.end == nil {
					s.turnJournal.response = l.Response
					s.turnJournal.responseEnd = len(items)
					s.turnJournal.searchesUsed += l.Response.webSearchesUsed()
					if l.Response.UsageMissing {
						s.turnJournal.usageMissing = true
					}
				}
			case l.K == "turn_start" && l.TurnID != "" && len(l.User) > 0:
				if s.turnJournal.id != "" && s.turnJournal.end == nil {
					return nil, 0, errors.New("a durable turn started before its predecessor finished")
				}
				s.turnJournal = turnJournal{id: l.TurnID, number: s.turnJournal.number + 1, input: len(items), user: l.User}
				clear(s.fileIntents)
				clear(s.execJobs)
				clear(s.pendingApproval)
				items = append(items, l.User)
				archive.observe(l.User)
			case l.K == "turn_steer" && l.SteerID != "" && len(l.User) > 0:
				if s.turnJournal.id != l.TurnID || s.turnJournal.end != nil {
					return nil, 0, errors.New("the durable steer has no active turn")
				}
				s.turnJournal.steers = append(s.turnJournal.steers, turnSteerRecord{K: l.K, TurnID: l.TurnID, ID: l.SteerID, User: l.User})
				items = append(items, l.User)
				archive.observe(l.User)
			case l.K == "turn_end" && l.TurnID != "" && l.End != nil:
				if s.turnJournal.id != l.TurnID || s.turnJournal.end != nil {
					return nil, 0, errors.New("the durable turn result has no active turn")
				}
				s.turnJournal.end = l.End
			case l.K == "checkpoint":
				if !ValidSessionID(l.ID) || l.Summary == "" || l.Sequence != s.checkpoint.next() || (s.turnJournal.id != "" && s.turnJournal.end == nil) {
					return nil, 0, errors.New("invalid conversation checkpoint")
				}
				s.checkpoint = &checkpointLine{K: l.K, ID: l.ID, At: l.At, Sequence: l.Sequence, Summary: l.Summary}
				items = []json.RawMessage{userItem(l.Summary)}
				s.turnJournal.selectCheckpoint()
				clear(s.fileIntents)
				clear(s.execJobs)
				clear(s.pendingApproval)
			case l.K == "approval_pending" && l.CallID != "":
				if s.pendingApproval == nil {
					s.pendingApproval = make(map[string]bool)
				}
				s.pendingApproval[l.CallID] = true
			case l.K == "approval_dispatch" && l.CallID != "":
				delete(s.pendingApproval, l.CallID)
			case l.K == "file_intent" && l.CallID != "" && l.Intent != nil:
				s.fileIntents[l.CallID] = *l.Intent
			case l.K == "exec_job" && l.CallID != "" && l.JobID != "":
				s.execJobs[l.CallID] = l.JobID
			default:
				return items, good, nil
			}
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

// AppendResponse saves the completed output as a unit, including empty
// responses. Tool calls are eligible to run only after their intent is saved.
func (s *Store) AppendResponse(response *Response) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return errors.New("the conversation store is closed")
	}
	return s.writeLocked(responseLine{K: "response", Response: response})
}

// appendBarrier commits the ordered log prefix for a turn or a tool intent.
func (s *Store) appendBarrier(record any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return errors.New("the conversation store is closed")
	}
	offset := int64(-1)
	if _, checkpoint := record.(*checkpointLine); checkpoint {
		var err error
		offset, err = s.f.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
	}
	err := s.writeLocked(record)
	if err == nil {
		err = s.f.Sync()
	}
	if err != nil && offset >= 0 {
		// Keep the preceding model input selected on replay when a failed
		// append or sync can be rolled back. The writer then stops.
		err = errors.Join(err, s.f.Truncate(offset), s.f.Sync())
	}
	return err
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

// Close releases the file and its writer lease.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.f != nil {
		err = s.f.Close()
		s.f = nil
	}
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
		s.lock = nil
	}
	return err
}
