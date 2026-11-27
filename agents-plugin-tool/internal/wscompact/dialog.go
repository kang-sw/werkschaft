package wscompact

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// ToolRunMaxLines caps verbatim tool lines kept in a consecutive run before
	// earlier calls are folded into a single summary line.
	ToolRunMaxLines = 8
	// ToolArgsThresholdBytes is the minimum byte length before middle-eliding
	// a single tool call's argument preview in a dialog excerpt.
	ToolArgsThresholdBytes = 300
	// ToolArgsKeepBytes is the head and tail byte length preserved when
	// middle-eliding a tool call's argument preview.
	ToolArgsKeepBytes = 150

	dialogOpenTag     = "<dialog>"
	dialogCloseTag    = "</dialog>"
	toolGroupOpenTag  = "<tool-calls>"
	toolGroupCloseTag = "</tool-calls>"
)

var (
	dialogWrapperBytes    = len(dialogOpenTag) + 1 + len(dialogCloseTag) + 1
	toolGroupWrapperBytes = len(toolGroupOpenTag) + 1 + len(toolGroupCloseTag) + 1

	xmlAttrReplacer = strings.NewReplacer(
		"\r\n", " ",
		"\r", " ",
		"\n", " ",
		"&", "&amp;",
		`"`, "&quot;",
		"<", "&lt;",
		">", "&gt;",
	)

	// framingCloseTagRE matches closing tags for compaction/restore XML framing
	// elements (case-insensitively) so untrusted conversation, summary, or file
	// payloads cannot terminate an enclosing framing block. Domain tags such as
	// <wb:*> or <skill> are intentionally excluded.
	framingCloseTagRE = regexp.MustCompile(`(?i)</\s*(ws-restore|session-state|work-target|direct-children|recent-mail|held-mail|compaction-checkpoint|session|task|lead-summary|worker-summary|children|exec-jobs|rereads|file|dialog|message|tool-calls|tool-call|tool-fold|mail|wake|command)\s*>`)
)

// EscapeXMLAttr escapes text for safe inclusion inside a double-quoted XML
// attribute value and collapses line breaks to spaces.
func EscapeXMLAttr(s string) string {
	return xmlAttrReplacer.Replace(s)
}

// EscapeFramingCloseTags neutralizes closing tags of ws compaction and restore
// XML framing elements inside untrusted or historical text payloads.
func EscapeFramingCloseTags(s string) string {
	return framingCloseTagRE.ReplaceAllStringFunc(s, func(match string) string {
		return "&lt;" + match[1:len(match)-1] + "&gt;"
	})
}

// ElideMiddle preserves up to keepBytes from the start and end of text on valid
// UTF-8 boundaries when text exceeds thresholdBytes, replacing the middle with
// an explicit omitted-byte count.
func ElideMiddle(text string, thresholdBytes, keepBytes int, join string) string {
	if len(text) <= thresholdBytes || keepBytes <= 0 || len(text) <= keepBytes*2 {
		return text
	}
	headEnd := keepBytes
	for headEnd > 0 && !utf8.RuneStart(text[headEnd]) {
		headEnd--
	}
	tailStart := len(text) - keepBytes
	for tailStart < len(text) && !utf8.RuneStart(text[tailStart]) {
		tailStart++
	}
	omitted := tailStart - headEnd
	if omitted <= 0 {
		return text
	}
	elided := text[:headEnd] + join + fmt.Sprintf("… [%d bytes elided] …", omitted) + join + text[tailStart:]
	if len(elided) >= len(text) {
		return text
	}
	return elided
}

// DialogItemKind identifies the provenance of one entry in a compaction dialog.
type DialogItemKind string

const (
	DialogUser      DialogItemKind = "user"
	DialogAssistant DialogItemKind = "assistant"
	DialogTool      DialogItemKind = "tool"
	DialogFold      DialogItemKind = "fold"
	DialogMail      DialogItemKind = "mail"
	DialogWake      DialogItemKind = "wake"
	DialogCommand   DialogItemKind = "command"
)

// ToolFoldCount records one tool's call count inside a folded tool summary line.
type ToolFoldCount struct {
	Name  string
	Count int
}

// DialogItem is a harness-agnostic conversation event rendered into <dialog>.
type DialogItem struct {
	Kind       DialogItemKind
	Timestamp  time.Time
	Truncated  bool
	Text       string
	ToolName   string
	ToolArgs   string
	ToolFailed bool
	// ToolCalls carries a tally count (>1) when per-call previews are unavailable.
	ToolCalls  int
	FoldTotal  int
	FoldCounts []ToolFoldCount
	Direction  string
	Peer       string
	Title      string
	Cause      string
	Source     string
	Caption    string
	Command    string
}

// FoldToolRuns collapses the leading calls of any consecutive run of more than
// ToolRunMaxLines tool calls into one DialogFold summary item, preserving the
// most recent ToolRunMaxLines calls verbatim.
func FoldToolRuns(items []DialogItem) []DialogItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]DialogItem, 0, len(items))
	for i := 0; i < len(items); {
		if items[i].Kind != DialogTool {
			out = append(out, items[i])
			i++
			continue
		}
		end := i + 1
		for end < len(items) && items[end].Kind == DialogTool {
			end++
		}
		run := items[i:end]
		if len(run) <= ToolRunMaxLines {
			out = append(out, run...)
		} else {
			folded := run[:len(run)-ToolRunMaxLines]
			kept := run[len(run)-ToolRunMaxLines:]
			counts := make(map[string]int, len(folded))
			total := 0
			for _, item := range folded {
				name := strings.TrimSpace(item.ToolName)
				if name == "" {
					continue
				}
				n := max(1, item.ToolCalls)
				counts[name] += n
				total += n
			}
			if total > 0 {
				sorted := make([]ToolFoldCount, 0, len(counts))
				for name, count := range counts {
					sorted = append(sorted, ToolFoldCount{Name: name, Count: count})
				}
				slices.SortFunc(sorted, func(a, b ToolFoldCount) int {
					if a.Count != b.Count {
						return b.Count - a.Count
					}
					return strings.Compare(a.Name, b.Name)
				})
				out = append(out, DialogItem{
					Kind:       DialogFold,
					FoldTotal:  total,
					FoldCounts: sorted,
				})
			}
			out = append(out, kept...)
		}
		i = end
	}
	return out
}

type renderedDialogEntry struct {
	text       string
	toolFamily bool
}

// BuildDialog renders conversation items into a bounded ## Dialog section with
// XML-framed entries (<message>, <tool-calls>, <mail>, <wake>, <command>)
// inside <dialog>...</dialog>. Selection runs newest-first by whole item so
// the <dialog>...</dialog> block stays within budgetBytes.
func BuildDialog(pointer string, items []DialogItem, budgetBytes int, secrets ...string) string {
	folded := FoldToolRuns(items)
	innerBudget := max(0, budgetBytes-dialogWrapperBytes)

	rendered := make([]renderedDialogEntry, 0, len(folded))
	for _, item := range folded {
		entry, ok := renderDialogItem(item, innerBudget, secrets)
		if !ok {
			continue
		}
		rendered = append(rendered, entry)
	}

	// Select newest-first whole items, charging one <tool-calls> wrapper per
	// maximal adjacent tool/fold run (matching Pi e1721a4c).
	selected := make([]renderedDialogEntry, 0, len(rendered))
	used := 0
	prevToolFamily := false
	for i := len(rendered) - 1; i >= 0; i-- {
		entry := rendered[i]
		cost := len(entry.text) + 1 // trailing newline
		if entry.toolFamily && !prevToolFamily {
			cost += toolGroupWrapperBytes
		}
		if used+cost > innerBudget {
			break
		}
		selected = append(selected, entry)
		used += cost
		prevToolFamily = entry.toolFamily
	}
	slices.Reverse(selected)

	var out strings.Builder
	out.WriteString("## Dialog\n")
	if ptr := strings.TrimSpace(pointer); ptr != "" {
		out.WriteString(EscapeFramingCloseTags(Scrub(ptr, secrets...)))
		out.WriteString("\n")
	}
	out.WriteString(dialogOpenTag)
	out.WriteString("\n")
	inToolRun := false
	for _, entry := range selected {
		if entry.toolFamily && !inToolRun {
			out.WriteString(toolGroupOpenTag)
			out.WriteString("\n")
			inToolRun = true
		} else if !entry.toolFamily && inToolRun {
			out.WriteString(toolGroupCloseTag)
			out.WriteString("\n")
			inToolRun = false
		}
		out.WriteString(entry.text)
		out.WriteString("\n")
	}
	if inToolRun {
		out.WriteString(toolGroupCloseTag)
		out.WriteString("\n")
	}
	out.WriteString(dialogCloseTag)
	out.WriteString("\n")
	return out.String()
}

func renderDialogItem(item DialogItem, innerBudget int, secrets []string) (renderedDialogEntry, bool) {
	switch item.Kind {
	case DialogUser, DialogAssistant:
		body := strings.TrimSpace(Scrub(item.Text, secrets...))
		if body == "" {
			return renderedDialogEntry{}, false
		}
		var open strings.Builder
		fmt.Fprintf(&open, `<message role="%s"`, item.Kind)
		if !item.Timestamp.IsZero() {
			fmt.Fprintf(&open, ` timestamp="%s"`, EscapeXMLAttr(item.Timestamp.UTC().Format(time.RFC3339)))
		}
		if item.Truncated {
			open.WriteString(` truncated="true"`)
		}
		open.WriteString(">")
		text := formatXMLBlock(open.String(), "</message>", body, innerBudget)
		return renderedDialogEntry{text: text}, true

	case DialogTool:
		name := normalizeInlineWhitespace(Scrub(item.ToolName, secrets...))
		if name == "" {
			return renderedDialogEntry{}, false
		}
		args := normalizeInlineWhitespace(Scrub(item.ToolArgs, secrets...))
		var line strings.Builder
		line.WriteString("→ ")
		line.WriteString(name)
		if args != "" {
			line.WriteString(" ")
			line.WriteString(ElideMiddle(args, ToolArgsThresholdBytes, ToolArgsKeepBytes, " "))
		} else if item.ToolCalls > 1 {
			fmt.Fprintf(&line, " ×%d", item.ToolCalls)
		}
		if item.ToolFailed {
			line.WriteString(" ✗failed")
		}
		maxLine := max(0, innerBudget-toolGroupWrapperBytes-1)
		text := boundEscapedBody(line.String(), maxLine)
		return renderedDialogEntry{text: text, toolFamily: true}, true

	case DialogFold:
		if item.FoldTotal <= 0 || len(item.FoldCounts) == 0 {
			return renderedDialogEntry{}, false
		}
		parts := make([]string, 0, len(item.FoldCounts))
		for _, fc := range item.FoldCounts {
			name := normalizeInlineWhitespace(Scrub(fc.Name, secrets...))
			if name == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s×%d", name, fc.Count))
		}
		if len(parts) == 0 {
			return renderedDialogEntry{}, false
		}
		raw := fmt.Sprintf("→ (+%d more: %s)", item.FoldTotal, strings.Join(parts, ", "))
		maxLine := max(0, innerBudget-toolGroupWrapperBytes-1)
		text := boundEscapedBody(raw, maxLine)
		return renderedDialogEntry{text: text, toolFamily: true}, true

	case DialogMail:
		dir := strings.TrimSpace(Scrub(item.Direction, secrets...))
		peer := strings.TrimSpace(Scrub(item.Peer, secrets...))
		title := strings.TrimSpace(Scrub(item.Title, secrets...))
		body := strings.TrimSpace(Scrub(item.Text, secrets...))
		if dir == "" && peer == "" && title == "" && body == "" {
			return renderedDialogEntry{}, false
		}
		var attrs strings.Builder
		appendAttr(&attrs, "direction", dir)
		appendAttr(&attrs, "peer", peer)
		appendAttr(&attrs, "title", title)
		if !item.Timestamp.IsZero() {
			appendAttr(&attrs, "timestamp", item.Timestamp.UTC().Format(time.RFC3339))
		}
		if body == "" {
			return renderedDialogEntry{text: "<mail" + attrs.String() + "/>"}, true
		}
		text := formatXMLBlock("<mail"+attrs.String()+">", "</mail>", body, innerBudget)
		return renderedDialogEntry{text: text}, true

	case DialogWake:
		cause := strings.TrimSpace(Scrub(item.Cause, secrets...))
		source := strings.TrimSpace(Scrub(item.Source, secrets...))
		caption := strings.TrimSpace(Scrub(item.Caption, secrets...))
		body := strings.TrimSpace(Scrub(item.Text, secrets...))
		if cause == "" && source == "" && caption == "" && body == "" {
			return renderedDialogEntry{}, false
		}
		var attrs strings.Builder
		appendAttr(&attrs, "cause", cause)
		appendAttr(&attrs, "source", source)
		appendAttr(&attrs, "caption", caption)
		if !item.Timestamp.IsZero() {
			appendAttr(&attrs, "timestamp", item.Timestamp.UTC().Format(time.RFC3339))
		}
		if body == "" {
			return renderedDialogEntry{text: "<wake" + attrs.String() + "/>"}, true
		}
		text := formatXMLBlock("<wake"+attrs.String()+">", "</wake>", body, innerBudget)
		return renderedDialogEntry{text: text}, true

	case DialogCommand:
		cmd := strings.TrimSpace(Scrub(item.Command, secrets...))
		body := strings.TrimSpace(Scrub(item.Text, secrets...))
		if cmd == "" && body == "" {
			return renderedDialogEntry{}, false
		}
		var attrs strings.Builder
		appendAttr(&attrs, "cmd", cmd)
		if !item.Timestamp.IsZero() {
			appendAttr(&attrs, "timestamp", item.Timestamp.UTC().Format(time.RFC3339))
		}
		if body == "" {
			return renderedDialogEntry{text: "<command" + attrs.String() + "/>"}, true
		}
		text := formatXMLBlock("<command"+attrs.String()+">", "</command>", body, innerBudget)
		return renderedDialogEntry{text: text}, true

	default:
		return renderedDialogEntry{}, false
	}
}

func appendAttr(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, ` %s="%s"`, name, EscapeXMLAttr(value))
}

func normalizeInlineWhitespace(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
	return strings.TrimSpace(s)
}

func formatXMLBlock(openTag, closeTag, body string, innerBudget int) string {
	// Reserve openTag + "\n" + "\n" + closeTag + trailing "\n" for the entry.
	overhead := len(openTag) + len(closeTag) + 3
	maxBody := max(0, innerBudget-overhead)
	escaped := boundEscapedBody(body, maxBody)
	return openTag + "\n" + escaped + "\n" + closeTag
}

// boundEscapedBody escapes framing closing tags and bounds the result to
// maxBytes without splitting an escaped tag sequence or creating a raw closing
// framing tag at the truncation boundary.
func boundEscapedBody(body string, maxBytes int) string {
	escaped := EscapeFramingCloseTags(body)
	if len(escaped) <= maxBytes {
		return escaped
	}
	return EscapeFramingCloseTags(BoundedText(escaped, maxBytes))
}
