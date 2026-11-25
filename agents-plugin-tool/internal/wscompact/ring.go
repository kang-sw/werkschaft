package wscompact

import (
	"fmt"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// RingDialogResult holds the recent-dialog excerpt and metadata derived from a
// conversation's wslive ring entries.
type RingDialogResult struct {
	Dialog        string
	Ticket        string
	Playbook      string
	LastCompactAt time.Time
}

// BuildRingDialog projects wslive ring records (oldest first) into the
// bounded ## Dialog section for a ws-managed compaction checkpoint, starting
// strictly after the newest successful RingCompact record.
func BuildRingDialog(records []wslive.RingRecord, ringRef string, secrets ...string) RingDialogResult {
	var out RingDialogResult
	start := 0
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Kind == wslive.RingCompact && !records[i].CompactFailed {
			start = i + 1
			out.LastCompactAt = records[i].At
			break
		}
	}
	var lines []string
	total := 0
	appendLine := func(role, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		line := "\n" + role + ": " + BoundedText(Scrub(body, secrets...), DialogBytes-16) + "\n"
		lines = append(lines, line)
		total += len(line)
		for total > DialogBytes && len(lines) > 0 {
			total -= len(lines[0])
			lines[0] = ""
			lines = lines[1:]
		}
	}
	appendDialog := func(role string, e wslive.RingEntry) {
		body := strings.TrimSpace(e.Text)
		if body != "" && e.Truncated {
			body += " [truncated]"
		}
		appendLine(role, body)
	}
	observeText := func(text string, checkSkill bool) {
		if checkSkill {
			if skill := SkillRE.FindStringSubmatch(text); len(skill) > 0 {
				if pb := NormalizePlaybook(skill[1]); pb != "" {
					out.Playbook = pb
				}
			}
		}
		if ticket := TicketRefRE.FindString(text); ticket != "" {
			out.Ticket = ticket
		}
	}

	for _, rec := range records[start:] {
		e := rec.RingEntry
		switch e.Kind {
		case wslive.RingUser:
			observeText(e.Text, true)
			appendDialog("user", e)
		case wslive.RingReply:
			observeText(e.Text, false)
			appendDialog("assistant", e)
		case wslive.RingTools:
			if summary := formatRingTools(e.Tools); summary != "" {
				observeText(summary, false)
				appendLine("tools", summary)
			}
		case wslive.RingMail:
			var parts []string
			if e.Direction != "" || e.Peer != "" {
				parts = append(parts, strings.TrimSpace(e.Direction+" "+e.Peer))
			}
			if e.Title != "" {
				parts = append(parts, e.Title)
			}
			if e.Text != "" {
				parts = append(parts, e.Text)
			}
			body := strings.Join(parts, ": ")
			observeText(body, false)
			appendLine("mail", body)
		case wslive.RingWake:
			var parts []string
			if e.Cause != "" {
				parts = append(parts, e.Cause)
			}
			if e.Source != "" {
				parts = append(parts, e.Source)
			}
			if e.Caption != "" {
				parts = append(parts, e.Caption)
			}
			if e.Text != "" {
				parts = append(parts, e.Text)
			}
			body := strings.Join(parts, " · ")
			observeText(body, false)
			appendLine("wake", body)
		case wslive.RingCommand:
			body := strings.TrimSpace(e.Command)
			if text := strings.TrimSpace(e.Text); text != "" {
				if body != "" {
					body += "\n" + text
				} else {
					body = text
				}
			}
			observeText(body, false)
			appendLine("command", body)
		// Skip UI / lifecycle-only rows: RingTurn, RingCompact, RingCard, RingUI,
		// RingWhiteboardEffects, notice, and unknown kinds.
		}
	}
	header := "## Dialog\n"
	if strings.TrimSpace(ringRef) != "" {
		header += "Conversation ring (relative to the session directory): " + ringRef + "\n"
	}
	out.Dialog = header + strings.Join(lines, "")
	return out
}

func formatRingTools(ts *wslive.ToolSummary) string {
	if ts == nil {
		return ""
	}
	var parts []string
	for _, batch := range ts.Batches {
		for _, call := range batch.CallArgs {
			name := strings.TrimSpace(call.Name)
			if name == "" {
				continue
			}
			entry := name
			if p := strings.TrimSpace(call.ArgsPreview); p != "" {
				entry += "(" + p + ")"
			}
			if call.Failed {
				entry += " [failed]"
			}
			parts = append(parts, entry)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	for _, tally := range ts.Tools {
		name := strings.TrimSpace(tally.Name)
		if name == "" {
			continue
		}
		if tally.Calls > 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", name, tally.Calls))
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}
