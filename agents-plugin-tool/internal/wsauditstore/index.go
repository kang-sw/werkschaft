package wsauditstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// The store index is a small append-only log of each ring's recorded
// last-seen time, kept beside the rings so retention ages a ring without
// reading it, even after the registry pruned the session (Decision 6). It is
// itself a two-file ring: rotation writes a full snapshot as the new file's
// first line and deletes the old file, so recovery reads one file.

var indexPattern = regexp.MustCompile(`^index-([0-9]{16})\.jsonl$`)

const indexRotateBytes = 1 << 20

func indexName(seq uint64) string { return fmt.Sprintf("index-%016d.jsonl", seq) }

type indexEntry struct {
	Dir     string `json:"dir"`
	Session string `json:"session,omitempty"`
	Seen    string `json:"seen"`
}

type indexLine struct {
	indexEntry
	Snap []indexEntry `json:"snap,omitempty"`
}

type indexLog struct {
	root string
	file *os.File
	seq  uint64
	size int64
}

// openIndex reads the index files once, oldest first, keeping each ring's
// latest recorded time. One file is the steady state; more mean a rotation
// was interrupted (its new file may be torn or empty), so the merged entries
// are written to a fresh snapshot file and the older files are deleted only
// once that snapshot is on disk.
func openIndex(root string) (*indexLog, map[string]indexEntry, error) {
	entries, err := readDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	var seqs []uint64
	for _, e := range entries {
		if m := indexPattern.FindStringSubmatch(e.Name()); m != nil && !e.IsDir() {
			if seq, err := strconv.ParseUint(m[1], 10, 64); err == nil && seq > 0 {
				seqs = append(seqs, seq)
			}
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	log := &indexLog{root: root}
	recorded := map[string]indexEntry{}
	if len(seqs) == 0 {
		return log, recorded, nil
	}
	keep := func(e indexEntry) {
		if old, ok := recorded[e.Dir]; ok && parseSeen(old.Seen).After(parseSeen(e.Seen)) {
			return
		}
		recorded[e.Dir] = e
	}
	var newestGood, newestLen int64
	for i, seq := range seqs {
		raw, err := readFile(filepath.Join(root, indexName(seq)))
		if err != nil {
			if i == len(seqs)-1 {
				return nil, nil, err
			}
			continue
		}
		pos := 0
		for pos < len(raw) {
			nl := bytes.IndexByte(raw[pos:], '\n')
			if nl < 0 {
				break
			}
			var ln indexLine
			if json.Unmarshal(raw[pos:pos+nl], &ln) == nil {
				for _, e := range ln.Snap {
					keep(e)
				}
				if ln.Dir != "" {
					keep(ln.indexEntry)
				}
			}
			pos += nl + 1
		}
		newestGood, newestLen = int64(pos), int64(len(raw))
	}
	newest := seqs[len(seqs)-1]
	if len(seqs) > 1 {
		log.seq = newest
		snap := make([]indexEntry, 0, len(recorded))
		for _, e := range recorded {
			snap = append(snap, e)
		}
		sort.Slice(snap, func(i, j int) bool { return snap[i].Dir < snap[j].Dir })
		if log.rotate(snap) != nil {
			// Keep every file; the next record retries the snapshot.
			return log, recorded, nil
		}
		for _, old := range seqs {
			_ = removeFile(filepath.Join(root, indexName(old)))
		}
		return log, recorded, nil
	}
	path := filepath.Join(root, indexName(newest))
	if newestGood < newestLen {
		_ = truncateFile(path, newestGood)
	}
	f, err := openAppend(path)
	if err != nil {
		return nil, nil, err
	}
	log.file, log.seq, log.size = f, newest, newestGood
	return log, recorded, nil
}

func parseSeen(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// record appends one entry, rotating to a snapshot file first when the log
// outgrew its bound. snapshot returns the entries to carry over.
func (l *indexLog) record(e indexEntry, snapshot func() []indexEntry) error {
	raw, err := json.Marshal(indexLine{indexEntry: e})
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if l.file == nil || l.size+int64(len(raw)) > indexRotateBytes {
		return l.rotate(snapshot())
	}
	n, err := writeFile(l.file, raw)
	l.size += int64(n)
	return err
}

func (l *indexLog) rotate(snap []indexEntry) error {
	raw, err := json.Marshal(indexLine{Snap: snap})
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := mkdirPrivate(l.root); err != nil {
		return err
	}
	f, err := createSegment(filepath.Join(l.root, indexName(l.seq+1)))
	if err != nil {
		return err
	}
	n, err := writeFile(f, raw)
	if err != nil {
		// A torn snapshot must not outrank the file it replaces.
		_ = closeFile(f)
		_ = removeFile(filepath.Join(l.root, indexName(l.seq+1)))
		return err
	}
	if l.file != nil {
		_ = closeFile(l.file)
		_ = removeFile(filepath.Join(l.root, indexName(l.seq)))
	}
	l.file, l.seq, l.size = f, l.seq+1, int64(n)
	return nil
}

func (l *indexLog) close() {
	if l.file != nil {
		_ = closeFile(l.file)
		l.file = nil
	}
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
