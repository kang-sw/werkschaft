package wscompact

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsredact"
)

const (
	// DialogBytes bounds the recent-dialog excerpt appended to a checkpoint.
	DialogBytes = 40 << 10
	// RereadBytes bounds the total inlined worktree text files in a checkpoint.
	RereadBytes = 40 << 10
)

// CompactionState describes current scheduler state supplied by the owner at
// the quiescent boundary. Entries are text pointers to durable records.
type CompactionState struct {
	ChildrenInFlight []string
	ChildrenFinished []string
	ExecJobs         []string
}

// CheckpointInput supplies the harness-agnostic inputs for BuildCheckpoint.
type CheckpointInput struct {
	Lead         bool
	Ticket       string
	Phase        string
	Playbook     string
	State        CompactionState
	OmitChildren bool
	Prose        []string
	Rereads      string
	Dialog       string
	Secrets      []string
}

// TicketRefRE extracts a ticket path or stem reference from text or tool args.
var TicketRefRE = regexp.MustCompile(`ai-docs/tickets/[A-Za-z._-]+/[0-9]{6}-[a-z0-9][a-z0-9-]*\.md|\b[0-9]{6}-[a-z]+-[a-z0-9][a-z0-9-]*\b`)

// SkillRE extracts a leading skill name from a user prompt.
var SkillRE = regexp.MustCompile(`^<skill name="([^"]+)"`)

// bearerToken matches an Authorization-style credential outside a header line,
// which wsredact's rules do not cover.
var bearerToken = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{6,}`)

// Scrub masks credentials in text before it enters a checkpoint, error, or log.
func Scrub(s string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) >= 8 {
			s = strings.ReplaceAll(s, secret, wsredact.Mask)
		}
	}
	s = wsredact.Secrets(s)
	return bearerToken.ReplaceAllString(s, "${1}"+wsredact.Mask)
}

// NormalizePlaybook strips any namespace prefix and ignores lead-revive.
func NormalizePlaybook(name string) string {
	if _, suffix, found := strings.Cut(name, ":"); found {
		name = suffix
	}
	if name == "" || name == "lead-revive" {
		return ""
	}
	return BoundedText(Scrub(name), 128)
}

// BoundedText truncates text to at most limit bytes on a valid UTF-8 boundary.
func BoundedText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= len("…") {
		return "…"[:limit]
	}
	text = text[:limit-len("…")]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + "…"
}

// FormatRereads formats the lead's requested reread files within RereadBytes,
// inlining the smallest readable files first and deferring the rest.
func FormatRereads(paths []string, files map[string]string, secrets ...string) string {
	if len(paths) == 0 {
		return ""
	}
	type candidate struct {
		name, body string
		ok, taken  bool
	}
	all := make([]candidate, 0, len(paths))
	seen := make(map[string]bool)
	for _, name := range paths {
		if seen[name] {
			continue
		}
		seen[name] = true
		text, ok := files[name]
		all = append(all, candidate{
			name: Scrub(name, secrets...),
			body: Scrub(text, secrets...),
			ok:   ok,
		})
	}
	ordered := make([]*candidate, 0, len(all))
	for i := range all {
		if all[i].ok {
			ordered = append(ordered, &all[i])
		}
	}
	slices.SortStableFunc(ordered, func(a, b *candidate) int { return len(a.body) - len(b.body) })
	left := RereadBytes
	for _, item := range ordered {
		if len(item.body) > left {
			break
		}
		item.taken = true
		left -= len(item.body)
	}
	var inlined, deferred strings.Builder
	for _, item := range all {
		if item.taken {
			// Escape closing framing tags so file contents cannot terminate the
			// enclosing <file>, <rereads>, or checkpoint block.
			body := EscapeFramingCloseTags(strings.TrimSuffix(item.body, "\n"))
			fmt.Fprintf(&inlined, "<file path=\"%s\">\n%s\n</file>\n", EscapeXMLAttr(item.name), body)
		} else {
			fmt.Fprintf(&deferred, "- %s\n", EscapeFramingCloseTags(item.name))
		}
	}
	if inlined.Len() == 0 && deferred.Len() == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("\n<rereads>\n")
	if inlined.Len() > 0 {
		out.WriteString("## Required re-reads (inlined)\n")
		out.WriteString(inlined.String())
	}
	if deferred.Len() > 0 {
		if inlined.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString("## Required re-reads (to read first)\n")
		out.WriteString(deferred.String())
	}
	out.WriteString("</rereads>\n")
	return out.String()
}

// BuildCheckpoint assembles the harness-agnostic compaction checkpoint with
// XML-framed provenance blocks so machine-generated state, agent-authored
// summary prose, inlined files, and historical dialog cannot spoof boundaries.
func BuildCheckpoint(in CheckpointInput) string {
	var out strings.Builder
	role := "worker"
	proseTag := "worker-summary"
	proseHeading := "Carried forward"
	if in.Lead {
		role = "lead"
		proseTag = "lead-summary"
		proseHeading = "Carried forward by the lead"
	}
	fmt.Fprintf(&out, "<compaction-checkpoint role=\"%s\">\n", role)
	fmt.Fprintf(&out, "Compacted %s conversation. Use the ticket and ws tooling for durable state.\n\n", role)

	ticket, playbook := in.Ticket, in.Playbook
	if ticket == "" {
		ticket = "(none recorded)"
	}
	if in.Phase != "" {
		ticket += ", phase " + BoundedText(Scrub(in.Phase, in.Secrets...), 128)
	}
	if playbook == "" {
		playbook = "(none recorded)"
	}
	out.WriteString("<session>\n## Session\n")
	fmt.Fprintf(&out, "- Active ticket: %s\n- Active playbook: %s\n", EscapeFramingCloseTags(ticket), EscapeFramingCloseTags(playbook))
	out.WriteString("</session>\n\n")

	fmt.Fprintf(&out, "<%s>\n## %s\n", proseTag, proseHeading)
	for i, field := range ProseFields() {
		body := ""
		if i < len(in.Prose) {
			body = strings.TrimSpace(in.Prose[i])
		}
		if body == "" {
			body = "(none)"
		} else {
			body = EscapeFramingCloseTags(body)
		}
		fmt.Fprintf(&out, "\n### %s\n%s\n", field.Heading, body)
	}
	fmt.Fprintf(&out, "</%s>\n", proseTag)

	if !in.OmitChildren {
		out.WriteString("\n<children>")
		writeCompactSection(&out, "Child agents in flight", in.State.ChildrenInFlight, in.Secrets)
		writeCompactSection(&out, "Child agents finished since the previous compaction", in.State.ChildrenFinished, in.Secrets)
		out.WriteString("</children>\n")
	}
	out.WriteString("\n<exec-jobs>")
	writeCompactSection(&out, "Exec jobs", in.State.ExecJobs, in.Secrets)
	out.WriteString("</exec-jobs>\n")
	if rereads := strings.TrimSpace(in.Rereads); rereads != "" {
		out.WriteString("\n")
		out.WriteString(rereads)
		out.WriteString("\n")
	}
	if dialog := strings.TrimSpace(in.Dialog); dialog != "" {
		out.WriteString("\n")
		out.WriteString(dialog)
		out.WriteString("\n")
	}
	out.WriteString("</compaction-checkpoint>\n")
	return out.String()
}

func writeCompactSection(out *strings.Builder, heading string, entries, secrets []string) {
	fmt.Fprintf(out, "\n## %s\n", heading)
	if len(entries) == 0 {
		out.WriteString("(none)\n")
	}
	for _, entry := range entries[:min(len(entries), 32)] {
		fmt.Fprintf(out, "- %s\n", EscapeFramingCloseTags(BoundedText(Scrub(entry, secrets...), 256)))
	}
}
