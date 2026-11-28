package wsreview

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// SizeThresholdCommits is the legacy commit-count cutoff cap preserved when a
// project's local config supplies only a `staleness: N commits` override.
const SizeThresholdCommits = 20

var localOnlyGitEnv = []string{
	"GIT_NO_LAZY_FETCH=1",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_TERMINAL_PROMPT=0",
}

// CheckpointNudge computes the read-only review-watermark checkpoint advisory.
//
// It is pure, root-in/string-out, and fail-open — it NEVER spawns a review
// and NEVER appends to the ledger; it only reads (wsreview.Frontier, never
// wsreview.Bootstrap).
//
// Composition:
//  1. Resolve (never Bootstrap) the ledger's clearing frontier entry; no entry
//     found returns the baseline-missing advisory with no write, even on a
//     trackless repository.
//  2. Resolve the review-track branch (ResolveTrackWith) using local objects
//     only; failure silently skips the behind-track advisory ("").
//  3. Resolve merge-base(frontier.Head, track) as the comparison boundary; an
//     unresolved merge base gives no advisory.
//  4. Read `## Checkpoint Nudge` from ai-docs/_review.local.md:
//     - Legacy-only override (`staleness: N commits` with no valid `merges:`
//       or `lines:`): count all-ancestry commits over `<base>..<track>` and
//       fire once at `min(N, SizeThresholdCommits)`.
//     - Metric mode (default, or any valid `merges:` / `lines:`): count
//       eligible first-parent merges on `<track>` outside `<base>`'s reachable
//       ancestry and net changed text lines over `<base>..<track>`, both
//       excluding `ai-docs/` and configured generated-path pathspecs, and emit
//       one advisory once either threshold is reached.
func CheckpointNudge(ctx context.Context, root string) string {
	return CheckpointNudgeWith(ctx, wsgit.ExecRunner{}, root)
}

// CheckpointNudgeWith is CheckpointNudge running git through runner (a
// hosted server's runner carries its caller's environment).
func CheckpointNudgeWith(ctx context.Context, runner wsgit.Runner, root string) string {
	entry, found, err := Frontier(root)
	if err != nil {
		return ""
	}
	if !found {
		return "no review ledger yet for this project; run a sweep (lead-review range: <base>..<head>) to establish a baseline"
	}

	// The status helper owns the composition; the advisory is its text.
	return checkpointStatusFound(ctx, localOnlyGitRunner(runner), root, entry).NudgeText
}

func localOnlyGitRunner(runner wsgit.Runner) wsgit.Runner {
	switch r := runner.(type) {
	case nil:
		return wsgit.ExecRunner{Env: slices.Clone(localOnlyGitEnv)}
	case wsgit.ExecRunner:
		env := make([]string, 0, len(r.Env)+len(localOnlyGitEnv))
		env = append(env, r.Env...)
		env = append(env, localOnlyGitEnv...)
		r.Env = env
		return r
	case *wsgit.ExecRunner:
		if r == nil {
			return wsgit.ExecRunner{Env: slices.Clone(localOnlyGitEnv)}
		}
		cp := *r
		env := make([]string, 0, len(cp.Env)+len(localOnlyGitEnv))
		env = append(env, cp.Env...)
		env = append(env, localOnlyGitEnv...)
		cp.Env = env
		return cp
	default:
		return runner
	}
}

func commitsAheadFromBase(ctx context.Context, runner wsgit.Runner, root, base, track string) (int, bool) {
	out, err := runner.RunGit(ctx, root, "rev-list", "--count", base+".."+track)
	if err != nil {
		return 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return count, true
}

func reviewPathspecs(excludes []string) []string {
	specs := make([]string, 0, 2+len(excludes))
	specs = append(specs, ".", ":(top,exclude)ai-docs/")
	for _, ex := range excludes {
		if ex == "ai-docs/" {
			continue
		}
		specs = append(specs, ":(top,exclude)"+ex)
	}
	return specs
}

// eligibleFirstParentMerges counts merges on track's first-parent chain outside
// base's reachable ancestry whose delta against their first parent touches at
// least one non-excluded path (including binary, mode-only, and gitlink-only
// changes). Uses --full-history --sparse so a merge whose tree matches an
// uninteresting second parent (when base sits on a side branch) is never pruned
// before its first-parent diff is inspected. Runs in one Git subprocess.
func eligibleFirstParentMerges(ctx context.Context, runner wsgit.Runner, root, base, track string, pathspecs []string) (int, error) {
	args := make([]string, 0, 14+len(pathspecs))
	args = append(args,
		"log",
		"--first-parent",
		"--merges",
		"--full-history",
		"--sparse",
		"--diff-merges=first-parent",
		"--raw",
		"--no-renames",
		"--no-ext-diff",
		"--no-textconv",
		"-z",
		"--format=format:COMMIT%x00%H%x00",
		base+".."+track,
		"--",
	)
	args = append(args, pathspecs...)

	out, err := runner.RunGit(ctx, root, args...)
	if err != nil {
		return 0, err
	}
	return parseEligibleFirstParentMerges(out)
}

func parseEligibleFirstParentMerges(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	tokens := bytes.Split(out, []byte{0})
	count := 0
	for i := 0; i < len(tokens); {
		if len(tokens[i]) == 0 {
			i++
			continue
		}
		if string(tokens[i]) != "COMMIT" {
			return 0, fmt.Errorf("unexpected git log token %q", string(tokens[i]))
		}
		if i+1 >= len(tokens) {
			return 0, fmt.Errorf("truncated git log commit header")
		}
		i += 2
		hasDiff := false
		for i < len(tokens) {
			tok := tokens[i]
			if len(tok) == 0 {
				i++
				break
			}
			if tok[0] == '\n' {
				tok = tok[1:]
			}
			if len(tok) == 0 || tok[0] != ':' {
				if string(tokens[i]) == "COMMIT" {
					break
				}
				return 0, fmt.Errorf("unexpected git log raw record %q", string(tokens[i]))
			}
			hasDiff = true
			// With --no-renames, each raw record is followed by one path token.
			if i+1 >= len(tokens) {
				return 0, fmt.Errorf("truncated git log raw path")
			}
			i += 2
		}
		if hasDiff {
			count++
		}
	}
	return count, nil
}

// netChangedLines computes added + deleted text lines in the net diff between
// base and track over pathspecs. Combining --raw and --numstat in one Git
// subprocess identifies gitlink (mode 160000) transitions so synthetic
// submodule SHA lines are excluded while real text changes (including the text
// side of a file/gitlink typechange) are kept. Binary and mode-only changes
// naturally contribute zero lines.
func netChangedLines(ctx context.Context, runner wsgit.Runner, root, base, track string, pathspecs []string) (int, error) {
	args := make([]string, 0, 11+len(pathspecs))
	args = append(args,
		"diff",
		"--raw",
		"--numstat",
		"-z",
		"--no-ext-diff",
		"--no-textconv",
		"--submodule=short",
		base,
		track,
		"--",
	)
	args = append(args, pathspecs...)

	out, err := runner.RunGit(ctx, root, args...)
	if err != nil {
		return 0, err
	}
	return parseNetChangedLines(out)
}

type rawDiffModes struct {
	oldMode string
	newMode string
}

func parseNetChangedLines(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	trimmed := bytes.TrimSuffix(out, []byte{0})
	if len(trimmed) == 0 {
		return 0, nil
	}
	tokens := bytes.Split(trimmed, []byte{0})

	var rawEntries []rawDiffModes
	i := 0
	for i < len(tokens) && len(tokens[i]) > 0 && tokens[i][0] == ':' {
		fields := strings.Fields(string(tokens[i][1:]))
		if len(fields) < 5 {
			return 0, fmt.Errorf("malformed git diff raw header %q", string(tokens[i]))
		}
		status := fields[4]
		pathTokens := 1
		if len(status) > 0 && (status[0] == 'R' || status[0] == 'C') {
			pathTokens = 2
		}
		if i+pathTokens >= len(tokens) {
			return 0, fmt.Errorf("truncated git diff raw path for %q", string(tokens[i]))
		}
		rawEntries = append(rawEntries, rawDiffModes{
			oldMode: fields[0],
			newMode: fields[1],
		})
		i += 1 + pathTokens
	}

	total := 0
	idx := 0
	for i < len(tokens) {
		if idx >= len(rawEntries) {
			return 0, fmt.Errorf("git diff numstat count exceeds raw entries (%d)", len(rawEntries))
		}
		parts := strings.SplitN(string(tokens[i]), "\t", 3)
		if len(parts) != 3 {
			return 0, fmt.Errorf("malformed git diff numstat record %q", string(tokens[i]))
		}
		i++
		if parts[2] == "" {
			// Rename/copy numstat record is followed by two NUL-terminated paths.
			if i+2 > len(tokens) {
				return 0, fmt.Errorf("truncated git diff numstat rename paths")
			}
			i += 2
		}
		modes := rawEntries[idx]
		idx++

		if parts[0] == "-" || parts[1] == "-" {
			continue
		}
		added, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, fmt.Errorf("parse git diff added lines %q: %w", parts[0], err)
		}
		deleted, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, fmt.Errorf("parse git diff deleted lines %q: %w", parts[1], err)
		}
		if modes.oldMode == "160000" {
			deleted = 0
		}
		if modes.newMode == "160000" {
			added = 0
		}
		total += added + deleted
	}

	if idx != len(rawEntries) {
		return 0, fmt.Errorf("git diff raw/numstat count mismatch: raw=%d numstat=%d", len(rawEntries), idx)
	}
	return total, nil
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
