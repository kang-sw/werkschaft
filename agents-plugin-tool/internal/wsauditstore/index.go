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

// openIndex reads the newest index file once and opens it for appends.
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
	newest := seqs[len(seqs)-1]
	path := filepath.Join(root, indexName(newest))
	raw, err := readFile(path)
	if err != nil {
		return nil, nil, err
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
				recorded[e.Dir] = e
			}
			if ln.Dir != "" {
				recorded[ln.Dir] = ln.indexEntry
			}
		}
		pos += nl + 1
	}
	if pos < len(raw) {
		_ = truncateFile(path, int64(pos))
	}
	for _, old := range seqs[:len(seqs)-1] {
		_ = removeFile(filepath.Join(root, indexName(old)))
	}
	f, err := openAppend(path)
	if err != nil {
		return nil, nil, err
	}
	log.file, log.seq, log.size = f, newest, int64(pos)
	return log, recorded, nil
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
		_ = closeFile(f)
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
