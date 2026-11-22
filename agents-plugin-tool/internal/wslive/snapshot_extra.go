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
	// Agents holds the context scale and compaction history of every
	// subagent row that is not running (AgentScales), by row ID. A v0.51.0
	// writer seeds such a row without those members (they are unknown to its
	// fixed Agent struct) and replaces the snapshot, and a finished row
	// without a linked session gets no later report to bring them back.
	Agents map[string]AgentScale `json:"agents,omitempty"`
}

// AgentScale is what a subagent row carries beyond v0.51.0's fixed Agent:
// the context scale its runner held for the bar (ContextWindow,
// ContextCompaction) and its compaction history (Compactions,
// CompactOrigin). Compacting is live state and is not kept.
type AgentScale struct {
	ContextWindow     int64            `json:"contextWindow,omitempty"`
	ContextCompaction *UsageCompaction `json:"contextCompaction,omitempty"`
	Compactions       int              `json:"compactions,omitempty"`
	CompactOrigin     string           `json:"compactOrigin,omitempty"`
}

func (a AgentScale) zero() bool {
	return a.ContextWindow == 0 && a.ContextCompaction == nil && a.Compactions == 0 && a.CompactOrigin == ""
}

// AgentScales is the scale of each row of agents that is not running and
// carries any. A running row is republished from its runner's memory.
func AgentScales(agents []Agent) map[string]AgentScale {
	var out map[string]AgentScale
	for _, a := range agents {
		scale := AgentScale{ContextWindow: a.ContextWindow, ContextCompaction: a.ContextCompaction, Compactions: a.Compactions, CompactOrigin: a.CompactOrigin}
		if a.Status == StatusRunning || scale.zero() {
			continue
		}
		if out == nil {
			out = map[string]AgentScale{}
		}
		out[a.ID] = scale
	}
	return out
}

// OverlayAgentScales gives each non-running row of s that carries no scale
// of its own the scale scales kept for its ID: the rows a v0.51.0 writer
// left without it. A row that carries any member keeps its own.
func (s *Snapshot) OverlayAgentScales(scales map[string]AgentScale) {
	for i := range s.Agents {
		a := &s.Agents[i]
		scale, ok := scales[a.ID]
		if !ok || a.Status == StatusRunning || !(AgentScale{ContextWindow: a.ContextWindow, ContextCompaction: a.ContextCompaction, Compactions: a.Compactions, CompactOrigin: a.CompactOrigin}).zero() {
			continue
		}
		a.ContextWindow, a.ContextCompaction, a.Compactions, a.CompactOrigin = scale.ContextWindow, scale.ContextCompaction, scale.Compactions, scale.CompactOrigin
	}
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
