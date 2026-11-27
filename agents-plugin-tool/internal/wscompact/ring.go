package wscompact

import (
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

	items := make([]DialogItem, 0, len(records)-start)
	for _, rec := range records[start:] {
		e := rec.RingEntry
		switch e.Kind {
		case wslive.RingUser:
			observeText(e.Text, true)
			if strings.TrimSpace(e.Text) != "" {
				items = append(items, DialogItem{
					Kind:      DialogUser,
					Timestamp: rec.At,
					Truncated: e.Truncated,
					Text:      e.Text,
				})
			}
		case wslive.RingReply:
			observeText(e.Text, false)
			if strings.TrimSpace(e.Text) != "" {
				items = append(items, DialogItem{
					Kind:      DialogAssistant,
					Timestamp: rec.At,
					Truncated: e.Truncated,
					Text:      e.Text,
				})
			}
		case wslive.RingTools:
			toolItems := ringToolItems(e.Tools, observeText)
			items = append(items, toolItems...)
		case wslive.RingMail:
			observeText(e.Peer, false)
			observeText(e.Title, false)
			observeText(e.Text, false)
			items = append(items, DialogItem{
				Kind:      DialogMail,
				Timestamp: rec.At,
				Direction: string(e.Direction),
				Peer:      e.Peer,
				Title:     e.Title,
				Text:      e.Text,
			})
		case wslive.RingWake:
			observeText(e.Cause, false)
			observeText(e.Source, false)
			observeText(e.Caption, false)
			observeText(e.Text, false)
			items = append(items, DialogItem{
				Kind:      DialogWake,
				Timestamp: rec.At,
				Cause:     e.Cause,
				Source:    e.Source,
				Caption:   e.Caption,
				Text:      e.Text,
			})
		case wslive.RingCommand:
			observeText(e.Command, false)
			observeText(e.Text, false)
			items = append(items, DialogItem{
				Kind:      DialogCommand,
				Timestamp: rec.At,
				Command:   e.Command,
				Text:      e.Text,
			})
		// Skip UI / lifecycle-only rows: RingTurn, RingCompact, RingCard, RingUI,
		// RingWhiteboardEffects, notice, and unknown kinds.
		}
	}
	pointer := ""
	if ref := strings.TrimSpace(ringRef); ref != "" {
		pointer = "Conversation ring (relative to the session directory): " + ref
	}
	out.Dialog = BuildDialog(pointer, items, DialogBytes, secrets...)
	return out
}

func ringToolItems(ts *wslive.ToolSummary, observe func(string, bool)) []DialogItem {
	if ts == nil {
		return nil
	}
	var items []DialogItem
	for _, batch := range ts.Batches {
		for _, call := range batch.CallArgs {
			name := strings.TrimSpace(call.Name)
			if name == "" {
				continue
			}
			observe(name+" "+call.ArgsPreview, false)
			items = append(items, DialogItem{
				Kind:       DialogTool,
				ToolName:   name,
				ToolArgs:   call.ArgsPreview,
				ToolFailed: call.Failed,
			})
		}
	}
	if len(items) > 0 {
		return items
	}
	for _, tally := range ts.Tools {
		name := strings.TrimSpace(tally.Name)
		if name == "" {
			continue
		}
		observe(name, false)
		items = append(items, DialogItem{
			Kind:      DialogTool,
			ToolName:  name,
			ToolCalls: tally.Calls,
		})
	}
	return items
}
