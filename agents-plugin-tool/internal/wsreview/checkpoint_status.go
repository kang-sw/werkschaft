package wsreview

import (
	"context"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// The closed set of CheckpointStatus.State values.
const (
	// CheckpointOK: a ledger frontier exists and no advisory is due.
	CheckpointOK = "ok"
	// CheckpointWarn: a ledger frontier exists and an advisory is due.
	CheckpointWarn = "warn"
	// CheckpointMissing: the repository has no clearing ledger entry yet.
	CheckpointMissing = "missing"
	// CheckpointNoRepo: the root is not a git repository, or no review-track
	// resolves (unconfigured projects land here too).
	CheckpointNoRepo = "norepo"
)

// CheckpointFrontier is the ledger's clearing frontier entry.
type CheckpointFrontier struct {
	Base    string `json:"base"`
	Head    string `json:"head"`
	Verdict string `json:"verdict"`
	Ref     string `json:"ref"`
}

// CheckpointStatus is the read-only review-watermark standing of a project: the
// data CheckpointNudgeWith renders as one advisory sentence, kept structured
// for a UI. For State missing and norepo every other field is its zero value.
// An uncomputable behind count (a git failure after the track resolved) leaves
// the counts 0 and State ok; it is never an error.
type CheckpointStatus struct {
	State     string             `json:"state"`
	HasLedger bool               `json:"has_ledger"`
	Frontier  CheckpointFrontier `json:"frontier"`
	Track     string             `json:"track"`
	// LegacyMode reports a `staleness: N commits`-only override: the metric
	// fields and thresholds are then 0 and the legacy ones carry the count.
	LegacyMode          bool   `json:"legacy_mode"`
	MergesBehind        int    `json:"merges_behind"`
	LinesBehind         int    `json:"lines_behind"`
	MergesThreshold     int    `json:"merges_threshold"`
	LinesThreshold      int    `json:"lines_threshold"`
	LegacyCommitsBehind int    `json:"legacy_commits_behind"`
	LegacyThreshold     int    `json:"legacy_threshold"`
	Nudge               bool   `json:"nudge"`
	NudgeText           string `json:"nudge_text"`
}

// CheckpointStatusWith computes CheckpointStatus for root, running git through
// runner with the local-only environment (no lazy fetch, no optional locks, no
// prompts). Like CheckpointNudgeWith it only reads: it never calls Bootstrap
// and never appends to the ledger.
func CheckpointStatusWith(ctx context.Context, runner wsgit.Runner, root string) CheckpointStatus {
	localRunner := localOnlyGitRunner(runner)
	entry, found, err := Frontier(root)
	if err != nil {
		return CheckpointStatus{State: CheckpointMissing}
	}
	if !found {
		if _, err := localRunner.RunGit(ctx, root, "rev-parse", "--git-dir"); err != nil {
			return CheckpointStatus{State: CheckpointNoRepo}
		}
		return CheckpointStatus{State: CheckpointMissing}
	}
	return checkpointStatusFound(ctx, localRunner, root, entry)
}

// checkpointStatusFound evaluates a project whose frontier entry is known.
// localRunner already carries the local-only environment.
func checkpointStatusFound(ctx context.Context, localRunner wsgit.Runner, root string, entry Entry) CheckpointStatus {
	track, err := ResolveTrackWith(ctx, localRunner, root)
	if err != nil {
		return CheckpointStatus{State: CheckpointNoRepo}
	}
	cfg := readCheckpointConfig(root)
	st := CheckpointStatus{
		State:     CheckpointOK,
		HasLedger: true,
		Frontier:  CheckpointFrontier{Base: entry.Base, Head: entry.Head, Verdict: entry.Verdict, Ref: entry.Ref},
		Track:     track,
	}
	if cfg.legacyOnly {
		st.LegacyMode = true
		st.LegacyThreshold = min(cfg.legacyCommits, SizeThresholdCommits)
	} else {
		st.MergesThreshold, st.LinesThreshold = cfg.merges, cfg.lines
	}

	mb, err := wsgit.Client{Runner: localRunner}.MergeBase(ctx, root, entry.Head, track)
	if err != nil {
		return st
	}
	base := strings.TrimSpace(mb.MergeBase)
	if base == "" {
		return st
	}

	if cfg.legacyOnly {
		count, ok := commitsAheadFromBase(ctx, localRunner, root, base, track)
		if !ok {
			return st
		}
		st.LegacyCommitsBehind = count
		if count >= st.LegacyThreshold {
			st.nudged(fmt.Sprintf(
				"review watermark is %d %s behind %s (threshold: %d %s); consider running a sweep (lead-review range: %s..%s).",
				count, pluralize(count, "commit", "commits"), track,
				st.LegacyThreshold, pluralize(st.LegacyThreshold, "commit", "commits"),
				entry.Head, track,
			))
		}
		return st
	}

	pathspecs := reviewPathspecs(cfg.excludes)
	merges, err := eligibleFirstParentMerges(ctx, localRunner, root, base, track, pathspecs)
	if err != nil {
		return st
	}
	lines, err := netChangedLines(ctx, localRunner, root, base, track, pathspecs)
	if err != nil {
		return st
	}
	st.MergesBehind, st.LinesBehind = merges, lines
	if merges >= cfg.merges || lines >= cfg.lines {
		st.nudged(fmt.Sprintf(
			"review watermark is %d %s, %d net %s behind %s (thresholds: %d %s / %d %s); consider running a sweep (lead-review range: %s..%s).",
			merges, pluralize(merges, "merge", "merges"),
			lines, pluralize(lines, "line", "lines"),
			track,
			cfg.merges, pluralize(cfg.merges, "merge", "merges"),
			cfg.lines, pluralize(cfg.lines, "line", "lines"),
			entry.Head, track,
		))
	}
	return st
}

func (s *CheckpointStatus) nudged(text string) {
	s.State, s.Nudge, s.NudgeText = CheckpointWarn, true, text
}
