package wsreview

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultStalenessCommits is the legacy checkpoint-nudge staleness knob's
// built-in default when StalenessKnob is read directly.
const DefaultStalenessCommits = 10

const (
	// Shipped metric-mode defaults: 10 eligible first-parent merges or 5,000
	// net changed text lines since the clearing review frontier.
	defaultMergesThreshold = 10
	defaultLinesThreshold  = 5000
)

// checkpointNudgeSectionRE isolates a `## Checkpoint Nudge` section's body
// out of ai-docs/_review.local.md, stopping at the next level-1 or level-2
// heading or end of file.
var checkpointNudgeSectionRE = regexp.MustCompile(`(?ms)^## Checkpoint Nudge\s*\n(.*?)(?:\n#{1,2} |\z)`)

// stalenessLineRE matches a `staleness: <N> commits` line inside the
// Checkpoint Nudge section.
var stalenessLineRE = regexp.MustCompile(`^staleness:\s*([0-9]+)\s*commits?\s*$`)

// mergesLineRE matches a `merges: <N>` positive integer threshold line.
var mergesLineRE = regexp.MustCompile(`^merges:\s*([0-9]+)\s*$`)

// linesLineRE matches a `lines: <N>` positive integer threshold line.
var linesLineRE = regexp.MustCompile(`^lines:\s*([0-9]+)\s*$`)

// excludeLineRE matches an `exclude: <pathspec>` generated-path exclusion line.
var excludeLineRE = regexp.MustCompile(`^exclude:\s*(.+?)\s*$`)

type checkpointConfig struct {
	// legacyOnly is true iff the section declares a valid `staleness: N commits`
	// threshold and neither a valid `merges: N` nor a valid `lines: N`.
	legacyOnly    bool
	legacyCommits int
	merges        int
	lines         int
	excludes      []string
}

// StalenessKnob reads the legacy checkpoint-nudge staleness commit-count knob
// from ai-docs/_review.local.md's `## Checkpoint Nudge` section, e.g.:
//
//	## Checkpoint Nudge
//	staleness: 15 commits
//
// Never errors: a missing file, a missing section, or a malformed value all
// fail open to DefaultStalenessCommits.
func StalenessKnob(root string) int {
	raw, err := os.ReadFile(filepath.Join(root, "ai-docs", "_review.local.md"))
	if err != nil {
		return DefaultStalenessCommits
	}
	_, staleness, hasStaleness := parseCheckpointConfigRaw(string(raw))
	if !hasStaleness {
		return DefaultStalenessCommits
	}
	return staleness
}

// readCheckpointConfig reads `## Checkpoint Nudge` from
// ai-docs/_review.local.md. A missing file, missing section, or malformed key
// fails open to the shipped metric defaults.
func readCheckpointConfig(root string) checkpointConfig {
	raw, err := os.ReadFile(filepath.Join(root, "ai-docs", "_review.local.md"))
	if err != nil {
		return defaultCheckpointConfig()
	}
	cfg, _, _ := parseCheckpointConfigRaw(string(raw))
	return cfg
}

func defaultCheckpointConfig() checkpointConfig {
	return checkpointConfig{
		merges: defaultMergesThreshold,
		lines:  defaultLinesThreshold,
	}
}

func parseCheckpointConfig(content string) checkpointConfig {
	cfg, _, _ := parseCheckpointConfigRaw(content)
	return cfg
}

func parseCheckpointConfigRaw(content string) (cfg checkpointConfig, staleness int, hasStaleness bool) {
	cfg = defaultCheckpointConfig()

	section := checkpointNudgeSectionRE.FindStringSubmatch(content)
	if section == nil {
		return cfg, 0, false
	}

	var (
		hasMerges    bool
		hasLines     bool
		seenExcludes = make(map[string]bool)
	)

	for _, rawLine := range strings.Split(section[1], "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if m := mergesLineRE.FindStringSubmatch(line); m != nil {
			if !hasMerges {
				if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
					cfg.merges = n
					hasMerges = true
				}
			}
			continue
		}
		if m := linesLineRE.FindStringSubmatch(line); m != nil {
			if !hasLines {
				if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
					cfg.lines = n
					hasLines = true
				}
			}
			continue
		}
		if m := stalenessLineRE.FindStringSubmatch(line); m != nil {
			if !hasStaleness {
				if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
					staleness = n
					hasStaleness = true
				}
			}
			continue
		}
		if m := excludeLineRE.FindStringSubmatch(line); m != nil {
			spec := strings.TrimSpace(m[1])
			if validExcludePathspec(spec) && !seenExcludes[spec] {
				seenExcludes[spec] = true
				cfg.excludes = append(cfg.excludes, spec)
			}
		}
	}

	if !hasMerges && !hasLines && hasStaleness {
		cfg.legacyOnly = true
		cfg.legacyCommits = staleness
	}
	return cfg, staleness, hasStaleness
}

// validExcludePathspec accepts a repo-relative, positive Git pathspec and
// rejects pathspec magic, absolute paths, drive-qualified paths, backslashes,
// control characters, and parent-directory traversals.
func validExcludePathspec(spec string) bool {
	if spec == "" {
		return false
	}
	if strings.HasPrefix(spec, ":") || strings.HasPrefix(spec, "/") || strings.ContainsRune(spec, '\\') {
		return false
	}
	if len(spec) >= 2 && ((spec[0] >= 'a' && spec[0] <= 'z') || (spec[0] >= 'A' && spec[0] <= 'Z')) && spec[1] == ':' {
		return false
	}
	for _, r := range spec {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, seg := range strings.Split(spec, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
