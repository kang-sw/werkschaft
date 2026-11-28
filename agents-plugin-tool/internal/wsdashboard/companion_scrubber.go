package wsdashboard

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ScrubAction is the deterministic 2-layer guardrail outcome for a Soul write.
type ScrubAction string

const (
	// ScrubAllow permits writing the content directly to Portable Soul.
	ScrubAllow ScrubAction = "allow"
	// ScrubRejectSecret rejects secret-like tokens/keys outright (never persisted
	// to Portable Soul shards or the approval queue).
	ScrubRejectSecret ScrubAction = "reject_secret"
	// ScrubHoldPending holds suspicious PII/location content (filesystem paths,
	// emails, URLs) in the machine-local soul_pending approval queue.
	ScrubHoldPending ScrubAction = "hold_pending"
	// ScrubDowngradeEpisode redirects project-specific details (ticket stems,
	// active project/repo names) to the machine-local Episodes store.
	ScrubDowngradeEpisode ScrubAction = "downgrade_episode"
)

// ScrubCategory classifies a deterministic guardrail match.
type ScrubCategory string

const (
	ScrubCategorySecret      ScrubCategory = "secret"
	ScrubCategoryPath        ScrubCategory = "path"
	ScrubCategoryEmail       ScrubCategory = "email"
	ScrubCategoryURL         ScrubCategory = "url"
	ScrubCategoryTicketStem  ScrubCategory = "ticket_stem"
	ScrubCategoryProjectName ScrubCategory = "project_name"
)

// ScrubFinding describes one matched rule without leaking raw secret values.
type ScrubFinding struct {
	Category ScrubCategory `json:"category"`
	Rule     string        `json:"rule"`
	Sample   string        `json:"sample,omitempty"`
}

// ScrubResult summarizes the deterministic scrubber verdict for a candidate write.
type ScrubResult struct {
	Action         ScrubAction    `json:"action"`
	Findings       []ScrubFinding `json:"findings,omitempty"`
	MatchedProject string         `json:"matchedProject,omitempty"`
}

type namedPattern struct {
	rule string
	re   *regexp.Regexp
}

var (
	scrubSecretPatterns = []namedPattern{
		{rule: "pem_private_key", re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
		{rule: "openai_anthropic_key", re: regexp.MustCompile(`\bsk-(?:proj-|ant-|live-|test-)?[A-Za-z0-9_-]{20,}\b`)},
		{rule: "github_token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)},
		{rule: "gitlab_token", re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}\b`)},
		{rule: "slack_token", re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
		{rule: "aws_access_key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
		{rule: "google_api_key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`)},
		{rule: "stripe_key", re: regexp.MustCompile(`\b(?:pk|rk)_live_[A-Za-z0-9]{16,}\b`)},
		{rule: "huggingface_token", re: regexp.MustCompile(`\bhf_[A-Za-z0-9]{20,}\b`)},
		{rule: "npm_token", re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{20,}\b`)},
		{rule: "pypi_token", re: regexp.MustCompile(`\bpypi-[A-Za-z0-9_-]{20,}\b`)},
		{rule: "jwt_token", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
		{rule: "bearer_token", re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9_\-.+/=]{16,}\b`)},
		{rule: "secret_assignment", re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|secret[_-]?key|auth[_-]?token|refresh[_-]?token|client[_-]?secret|password|passwd|private[_-]?key)\s*[:=]\s*['"]?[A-Za-z0-9_\-./+=]{12,}['"]?`)},
	}

	scrubURLPatterns = []namedPattern{
		{rule: "url_scheme", re: regexp.MustCompile(`\b(?:https?|ftp|ssh|git|ws|wss)://[^\s"'` + "`" + `<>)]+`)},
		{rule: "git_scp_url", re: regexp.MustCompile(`\bgit@[A-Za-z0-9.-]+:[A-Za-z0-9._/\-]+`)},
	}

	scrubEmailPattern = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)

	scrubPathPatterns = []namedPattern{
		{rule: "unix_system_path", re: regexp.MustCompile(`(?:^|[\s("'` + "`" + `=:])(/(?:Users|home|var|etc|tmp|opt|srv|mnt|Volumes|root|Library|Applications|private|proc|sys|usr)(?:/[^\s"'` + "`" + `<>|;,)]+)+)`)},
		{rule: "unix_deep_path", re: regexp.MustCompile(`(?:^|[\s("'` + "`" + `=:])(/(?:[A-Za-z0-9._-]+/){2,}[A-Za-z0-9._-]+)`)},
		{rule: "home_relative_path", re: regexp.MustCompile(`(?:^|[\s("'` + "`" + `=:])(~/[^\s"'` + "`" + `<>|;,)]+)`)},
		{rule: "windows_drive_path", re: regexp.MustCompile(`\b[A-Za-z]:\\[^\s"'` + "`" + `<>|;,)]+`)},
		{rule: "windows_unc_path", re: regexp.MustCompile(`\\\\[A-Za-z0-9._-]+\\[^\s"'` + "`" + `<>|;,)]+`)},
	}

	scrubTicketStemPattern = regexp.MustCompile(`\b\d{6}-(?:feat|bug|refactor|chore|research|epic|workset)-[a-z0-9][a-z0-9-]*\b`)
)

// ScrubSoulContent inspects candidate Portable Soul text and returns a
// deterministic guardrail verdict. Priority order:
//  1. Secret-like tokens/keys -> ScrubRejectSecret (never persisted).
//  2. Filesystem paths, emails, URLs -> ScrubHoldPending (approval queue).
//  3. Ticket stems, active project/repo names -> ScrubDowngradeEpisode.
//  4. Clean -> ScrubAllow.
func ScrubSoulContent(text string, activeProjects []string) ScrubResult {
	if strings.TrimSpace(text) == "" {
		return ScrubResult{Action: ScrubAllow}
	}

	var findings []ScrubFinding
	hasSecret := false
	hasHold := false
	hasDowngrade := false
	matchedProject := ""

	for _, p := range scrubSecretPatterns {
		if loc := p.re.FindString(text); loc != "" {
			hasSecret = true
			findings = append(findings, ScrubFinding{
				Category: ScrubCategorySecret,
				Rule:     p.rule,
				Sample:   redactSecretSample(loc),
			})
		}
	}

	// Collect URLs first and strip them from a working copy so path/email
	// patterns do not double-flag URL components (such as https://host/a/b/c
	// or git@host:org/repo).
	urlStripped := text
	for _, p := range scrubURLPatterns {
		if m := p.re.FindString(urlStripped); m != "" {
			hasHold = true
			findings = append(findings, ScrubFinding{
				Category: ScrubCategoryURL,
				Rule:     p.rule,
				Sample:   truncateScrubSample(m, 64),
			})
			urlStripped = p.re.ReplaceAllString(urlStripped, " ")
		}
	}

	if m := scrubEmailPattern.FindString(urlStripped); m != "" {
		hasHold = true
		findings = append(findings, ScrubFinding{
			Category: ScrubCategoryEmail,
			Rule:     "email_address",
			Sample:   truncateScrubSample(m, 64),
		})
	}

	for _, p := range scrubPathPatterns {
		if sub := p.re.FindStringSubmatch(urlStripped); len(sub) > 0 {
			sample := sub[0]
			if len(sub) > 1 && sub[1] != "" {
				sample = sub[1]
			}
			hasHold = true
			findings = append(findings, ScrubFinding{
				Category: ScrubCategoryPath,
				Rule:     p.rule,
				Sample:   truncateScrubSample(strings.TrimSpace(sample), 64),
			})
			break
		}
	}

	if m := scrubTicketStemPattern.FindString(text); m != "" {
		hasDowngrade = true
		findings = append(findings, ScrubFinding{
			Category: ScrubCategoryTicketStem,
			Rule:     "ticket_stem",
			Sample:   truncateScrubSample(m, 64),
		})
	}

	if proj := matchActiveProject(text, activeProjects); proj != "" {
		hasDowngrade = true
		matchedProject = proj
		findings = append(findings, ScrubFinding{
			Category: ScrubCategoryProjectName,
			Rule:     "active_project_name",
			Sample:   truncateScrubSample(proj, 64),
		})
	}

	action := ScrubAllow
	switch {
	case hasSecret:
		action = ScrubRejectSecret
	case hasHold:
		action = ScrubHoldPending
	case hasDowngrade:
		action = ScrubDowngradeEpisode
	}

	return ScrubResult{
		Action:         action,
		Findings:       findings,
		MatchedProject: matchedProject,
	}
}

func redactSecretSample(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) <= 8 {
		return "[redacted]"
	}
	return trimmed[:4] + "…[redacted]"
}

func truncateScrubSample(raw string, maxRunes int) string {
	if utf8.RuneCountInString(raw) <= maxRunes {
		return raw
	}
	runes := []rune(raw)
	return string(runes[:maxRunes-1]) + "…"
}

func matchActiveProject(text string, activeProjects []string) string {
	if len(activeProjects) == 0 {
		return ""
	}
	lowerText := strings.ToLower(text)
	seen := make(map[string]struct{}, len(activeProjects))
	for _, raw := range activeProjects {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		// Extract the repo/folder basename if a full path was supplied.
		if idx := strings.LastIndexAny(name, `/\`); idx >= 0 {
			name = name[idx+1:]
		}
		name = strings.TrimSpace(name)
		if utf8.RuneCountInString(name) < 3 {
			continue
		}
		lowerName := strings.ToLower(name)
		if _, dup := seen[lowerName]; dup {
			continue
		}
		seen[lowerName] = struct{}{}
		if containsWordToken(lowerText, lowerName) {
			return name
		}
	}
	return ""
}

func containsWordToken(haystack, needle string) bool {
	if needle == "" || len(needle) > len(haystack) {
		return false
	}
	start := 0
	for {
		idx := strings.Index(haystack[start:], needle)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(needle)
		leftOK := pos == 0 || !isProjectWordRune(lastRune(haystack[:pos]))
		rightOK := end == len(haystack) || !isProjectWordRune(firstRune(haystack[end:]))
		if leftOK && rightOK {
			return true
		}
		start = pos + 1
	}
}

func isProjectWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}
