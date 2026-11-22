package wslive

import (
	"encoding/json"
	"path/filepath"
)

// SnapshotExtraName is the additive session-local file that keeps what the
// recovery snapshot (snapshot.json) holds for a reader that knows its newer
// members, in storage a v0.51.0 writer never replaces: that writer decodes
// the whole snapshot into its fixed struct, drops every member it does not
// know, and replaces the file. v0.51.0 neither reads, writes nor prunes this
// file.
const SnapshotExtraName = "snapshot-extra.json"

// SnapshotExtraPath is the extra file in sessionDir.
func SnapshotExtraPath(sessionDir string) string {
	return filepath.Join(sessionDir, SnapshotExtraName)
}

// SnapshotExtra is the file's content.
type SnapshotExtra struct {
	// Coverage is set once a writer that records holder coverage
	// (Snapshot.HolderSeq) wrote the session's recovery snapshot. A snapshot
	// that then lacks coverage was replaced by a writer that predates it,
	// which drops agy native rows it does not seed (running ones) and the
	// coverage: no absence in that file shows a row evicted by retention.
	Coverage bool `json:"coverage,omitempty"`
}

// ReadSnapshotExtra reads the session's extra file; found is false for a
// missing or unreadable one (no writer of it ever ran, or its content is
// lost), which leaves readers on their legacy behavior.
func ReadSnapshotExtra(dir string) (extra SnapshotExtra, found bool) {
	raw, err := readReplacedFile(SnapshotExtraPath(dir))
	if err != nil || json.Unmarshal(raw, &extra) != nil {
		return SnapshotExtra{}, false
	}
	return extra, true
}
