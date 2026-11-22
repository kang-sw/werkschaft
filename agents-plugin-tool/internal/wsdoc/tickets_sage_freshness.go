package wsdoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sageReviewFreshness struct {
	Stages      []string
	Baseline    string
	Instruction string
}

// sageGateFreshnessResult reports whether the completed stage(s) are stale and,
// when they are, resolves the freshness question with the supplied answer. The
// second return value ("consumed") is true whenever the freshness question was
// posed at all (i.e. at least one stage is stale); the caller uses it to know
// the answer was spent on the freshness question and must be reset before any
// remaining pending stage is resolved. The three post-check states are:
//   - not stale: consumed=false, zero result — caller proceeds with the answer.
//   - stale + answer "yes": consumed=true, a non-waivable `run` result over the
//     stale stages (combined when two are listed, standalone when one) — caller
//     returns it immediately.
//   - stale + answer "no": consumed=true, zero-action result and nothing written
//     — caller resets the answer and proceeds to resolve remaining stages.
//   - stale + answer "" (unanswered): consumed=true, the `check_review_required`
//     result — caller returns it immediately.
func sageGateFreshnessResult(env []string, root, ticketRel string, stages []string, answer string) (SageGateResult, bool, error) {
	freshness, err := sageReviewFreshnessCheck(env, root, ticketRel, stages)
	if err != nil || len(freshness.Stages) == 0 {
		return SageGateResult{}, false, err
	}
	switch answer {
	case "yes":
		// Accepted: the stale stage(s) rerun, carrying the non-waivable
		// advisory like every other run. Mode follows the stage count.
		mode := "standalone"
		if len(freshness.Stages) == 2 {
			mode = "combined"
		}
		out := stageOutcome{action: "run", advisory: sageReviewNonWaivableAdvisory}
		return gateResultFromStageReviewers(out, freshness.Stages, mode), true, nil
	case "no":
		// Declined: write nothing — posture stays completed and the recorded
		// digest stays stale, so tickets.verify keeps warning until a fresh
		// sage_stamp. Return a zero-action result so the caller falls through
		// to resolve any remaining pending stage (with the answer reset).
		return SageGateResult{}, true, nil
	default:
		// Unanswered: surface the freshness question for the caller to relay.
		return SageGateResult{
			Action:            "check_review_required",
			FreshnessStages:   freshness.Stages,
			ReviewBaseline:    freshness.Baseline,
			ReviewInstruction: freshness.Instruction,
		}, true, nil
	}
}

func sageReviewFreshnessCheck(env []string, root, ticketRel string, stages []string) (sageReviewFreshness, error) {
	ticketRel = filepath.ToSlash(filepath.Clean(ticketRel))
	if ticketRel == "." || ticketRel == "" || !strings.HasPrefix(ticketRel, "ai-docs/tickets/") {
		return sageReviewFreshness{}, nil
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return sageReviewFreshness{}, nil
	}
	currentRaw, err := sageReviewCurrentTicketContent(env, root, ticketRel)
	if err != nil {
		return sageReviewFreshness{}, nil
	}
	currentClean := normalizeTicketForSageFreshness(string(currentRaw))
	currentDigest := sageReviewBodyDigest(string(currentRaw))
	currentFullDigest := sageReviewFullBodyDigest(string(currentRaw))
	currentFM := frontmatterFromText(string(currentRaw))
	var affected []string
	var baselines []string
	for _, stage := range uniqueSageStages(stages) {
		field := "sage-review-" + stage
		if recorded, ok := currentFM[field+"-reviewed"].(string); ok && strings.TrimSpace(recorded) != "" {
			// Primary path: compare the recorded digest against the current body
			// digest directly. No git walk on this path.
			if recorded = strings.TrimSpace(recorded); recorded == currentDigest || recorded == currentFullDigest {
				continue
			}
			// Mismatch only: a stamp written by an older daemon digests the
			// whole body, Results included, so a later Result append matches
			// neither current digest. The git cost is paid here and nowhere
			// on the match path above.
			if sageLegacyStampOnlyGainedResults(env, root, ticketRel, recorded, currentClean) {
				continue
			}
			affected = append(affected, stage)
			baselines = append(baselines, "the last recorded digest")
			continue
		}
		// Fallback (legacy, no recorded digest): git-history baseline corrected
		// to the latest completed-transition, body-only compared.
		baseline, baselineRaw, ok, err := sageReviewStageBaseline(env, root, ticketRel, stage)
		if err != nil {
			return sageReviewFreshness{}, err
		}
		if !ok {
			continue
		}
		if normalizeTicketForSageFreshness(baselineRaw) == currentClean {
			continue
		}
		affected = append(affected, stage)
		baselines = append(baselines, shortCommit(baseline))
	}
	if len(affected) == 0 {
		return sageReviewFreshness{}, nil
	}
	baseline := strings.Join(uniqueStrings(baselines), ", ")
	return sageReviewFreshness{
		Stages:      affected,
		Baseline:    baseline,
		Instruction: fmt.Sprintf("Inspect the ticket diff against %s and decide whether to rerun sage %s review.", baseline, strings.Join(affected, " and ")),
	}, nil
}

// sageLegacyStampOnlyGainedResults reports whether recorded is the full-body
// digest of a committed revision of the ticket whose Result-stripped body
// equals currentClean: the only edits since the stamp are Results and
// Editions. A stamp matching no committed revision, or one whose reviewed
// content changed, reports false and keeps the stale advisory.
func sageLegacyStampOnlyGainedResults(env []string, root, ticketRel, recorded, currentClean string) bool {
	history, err := sageReviewTicketHistory(env, root, ticketRel)
	if err != nil {
		return false
	}
	for _, entry := range history {
		raw, err := sageGitOutput(env, root, "show", entry.Commit+":"+entry.Path)
		if err != nil || sageReviewFullBodyDigest(string(raw)) != recorded {
			continue
		}
		if normalizeTicketForSageFreshness(string(raw)) == currentClean {
			return true
		}
	}
	return false
}

func sageReviewStageBaseline(env []string, root, ticketRel, stage string) (commit, text string, ok bool, err error) {
	history, err := sageReviewTicketHistory(env, root, ticketRel)
	if err != nil {
		return "", "", false, nil
	}
	if len(history) == 0 {
		return "", "", false, nil
	}
	field := "sage-review-" + stage

	// Precompute each entry's stage posture once (history[0] = newest).
	postures := make([]string, len(history))
	raws := make([]string, len(history))
	have := make([]bool, len(history))
	for i, entry := range history {
		raw, showErr := sageGitOutput(env, root, "show", entry.Commit+":"+entry.Path)
		if showErr != nil {
			continue
		}
		design, completeness := effectiveSageReviewPostures(frontmatterFromText(string(raw)))
		current := design
		if field == "sage-review-completeness" {
			current = completeness
		}
		postures[i] = current
		raws[i] = string(raw)
		have[i] = true
	}

	// Scan newest -> oldest for the latest completed-transition edge: the
	// first index i where postures[i] == "completed" and the next-older
	// entry (postures[i+1], or the "" sentinel when i is the oldest index)
	// is != "completed". This generalizes the prior "born completed"
	// sentinel trick to the newest-first direction, so a single-transition
	// ticket still resolves to its sole edge while a multi-transition ticket
	// (reset -> re-stamp) resolves to the latest edge instead of the
	// earliest.
	for i := 0; i < len(history); i++ {
		if !have[i] || postures[i] != "completed" {
			continue
		}
		next := ""
		if i+1 < len(history) && have[i+1] {
			next = postures[i+1]
		}
		if next != "completed" {
			return history[i].Commit, raws[i], true, nil
		}
	}
	return "", "", false, nil
}

type sageReviewHistoryEntry struct {
	Commit string
	Path   string
}

func sageReviewTicketHistory(env []string, root, ticketRel string) ([]sageReviewHistoryEntry, error) {
	logOut, err := sageGitOutput(env, root, "log", "--follow", "--find-renames", "--format=%H", "--name-status", "--", ticketRel)
	if err != nil {
		return nil, err
	}
	var entries []sageReviewHistoryEntry
	currentPath := ticketRel
	var currentCommit string
	var commitPath string
	var olderPath string
	flush := func() {
		if currentCommit == "" {
			return
		}
		if commitPath == "" {
			commitPath = currentPath
		}
		entries = append(entries, sageReviewHistoryEntry{Commit: currentCommit, Path: commitPath})
		if olderPath != "" {
			currentPath = olderPath
		}
	}
	for _, line := range strings.Split(string(logOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isFullCommitHash(line) {
			flush()
			currentCommit = line
			commitPath = ""
			olderPath = ""
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		if (strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")) && len(fields) >= 3 {
			oldPath := filepath.ToSlash(fields[1])
			newPath := filepath.ToSlash(fields[2])
			if newPath == currentPath {
				commitPath = newPath
				olderPath = oldPath
			}
			continue
		}
		path := filepath.ToSlash(fields[len(fields)-1])
		if path == currentPath {
			commitPath = path
		}
	}
	flush()
	return entries, nil
}

func sageReviewCurrentTicketContent(env []string, root, ticketRel string) ([]byte, error) {
	if staged, err := sageGitOutput(env, root, "diff", "--cached", "--name-only", "--", ticketRel); err == nil && strings.TrimSpace(string(staged)) != "" {
		return sageGitOutput(env, root, "show", ":"+ticketRel)
	}
	if unstaged, err := sageGitOutput(env, root, "diff", "--name-only", "--", ticketRel); err == nil && strings.TrimSpace(string(unstaged)) != "" {
		return os.ReadFile(filepath.Join(root, filepath.FromSlash(ticketRel)))
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(ticketRel)))
}

// ticketBodyBelowFrontmatter returns the markdown body below the frontmatter
// fence, trimmed. The whole frontmatter block is excluded (not just
// sage-review* keys) so any frontmatter-only edit (title, related,
// sage-review* postures/digests, ...) never perturbs the freshness/digest
// comparison. When the text has no leading `---` fence, or the closing fence
// is missing, the whole trimmed text is returned instead — a graceful
// degrade for malformed input rather than collapsing to "" (which would
// falsely equate all malformed tickets as identical).
func ticketBodyBelowFrontmatter(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return strings.TrimSpace(text)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return strings.TrimSpace(text)
}

// normalizeTicketForSageFreshness returns the body below the frontmatter with
// every `### Result` section and every `#### Edition` block removed, trimmed.
// A worker appending its Result to a stamped ticket therefore never makes the
// stamp stale. A Result section runs to the next `### ` or `## ` heading and
// takes its Edition blocks with it; a stray Edition block runs to the next
// heading of level 4 or higher. Headings inside fenced code are ignored.
func normalizeTicketForSageFreshness(text string) string {
	return stripTicketResultSections(ticketBodyBelowFrontmatter(text))
}

func stripTicketResultSections(body string) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	var fenceChar byte
	fenceLen := 0
	// skip is "" when keeping, "result" inside a Result section, "edition"
	// inside a stray Edition block.
	skip := ""
	for _, line := range lines {
		// Fences follow CommonMark, as ticketPhaseHeadingProblems does: the
		// opener's length and character bind its closer, and a line with an
		// info string never closes.
		if char, n, info := markdownFence(line); n > 0 {
			if fenceLen == 0 {
				fenceChar, fenceLen = char, n
			} else if char == fenceChar && n >= fenceLen && info == "" {
				fenceLen = 0
			}
			if skip == "" {
				kept = append(kept, line)
			}
			continue
		}
		if fenceLen > 0 {
			if skip == "" {
				kept = append(kept, line)
			}
			continue
		}
		if level, title, ok := sageHeading(line); ok {
			switch {
			case level <= 3 && skip != "":
				skip = ""
			case level == 4 && skip == "edition":
				skip = ""
			}
			if skip == "" {
				switch {
				case level == 3 && sageHeadingNamed(title, "Result"):
					skip = "result"
				case level == 4 && sageHeadingNamed(title, "Edition"):
					skip = "edition"
				}
			}
			// A heading of level 4+ never ends a Result section; a level-4
			// non-Edition heading ends a stray Edition block.
		}
		if skip == "" {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func sageHeadingNamed(title, name string) bool {
	return title == name || strings.HasPrefix(title, name+" ")
}

// sageHeading parses an ATX heading line of level 1-6 and returns its level
// and trimmed title.
func sageHeading(line string) (int, string, bool) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0, "", false
	}
	return n, strings.TrimSpace(line[n+1:]), true
}

// sageReviewBodyDigest computes the sha256 of the body-only normalized text,
// truncated to a fixed 16-hex-char prefix. Shared by the write path
// (tickets_sage.go, on every completed stamp) and the freshness read path
// (sageReviewFreshnessCheck's primary digest comparison) so both agree on
// what "the digest" means.
func sageReviewBodyDigest(text string) string {
	return sageDigestOf(normalizeTicketForSageFreshness(text))
}

// sageReviewFullBodyDigest digests the whole body below the frontmatter,
// Result sections included: the digest older binaries wrote. The read side
// accepts it next to the stripped digest so existing stamps stay fresh.
func sageReviewFullBodyDigest(text string) string {
	return sageDigestOf(ticketBodyBelowFrontmatter(text))
}

func sageDigestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:16]
}

// sageReviewCurrentBodyDigest reads the ticket file on disk and returns its
// body digest. This is the entry point tickets_sage.go's write path calls
// when stamping a stage completed.
func sageReviewCurrentBodyDigest(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sageReviewBodyDigest(string(raw)), nil
}

func sageGitOutput(env []string, root string, args ...string) ([]byte, error) {
	cmd := gitCommand(env, root, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %v\n%s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}

func isFullCommitHash(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func uniqueSageStages(stages []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, stage := range stages {
		stage = strings.TrimSpace(stage)
		if (stage == "design" || stage == "completeness") && !seen[stage] {
			seen[stage] = true
			out = append(out, stage)
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func shortCommit(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
