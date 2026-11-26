// Collapsed turn tool summary projection and shared DOM contract (Decision 1, 3, 5).

package wsdashboard

import (
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TurnSummaryView is the shared presentation model for a collapsed turn
// summary row. Completed tool turns, orphan tool spans, active live turns,
// and folded plain response turns all render through the same template and
// DOM contract ([data-conv-turn-summary], [data-conv-turn-drawer],
// [data-conv-turn-count], [data-conv-turn-fails], [data-conv-turn-tokens]).
type TurnSummaryView struct {
	Key         string
	EntryKey    string
	At          int64
	AtRFC       string
	TurnAtRFC   string
	Live        bool
	Completed   bool
	CustomLabel string
	Calls       int
	Failed      int
	DurationMs  int64
	AddedTokens int64
	EstTokens   int64
	Origin      string
	WakeTurn    bool
	Reason      string
	Gaps        []TurnGapItem
	Lang        string
}

// TurnGapItem is one RingTools or live ToolGap slice rendered inside
// [data-conv-turn-drawer] in chronological order.
type TurnGapItem struct {
	EntryKey string
	At       int64
	AtRFC    string
	Tools    *wslive.ToolSummary
	Live     bool
}

// NewFoldedPlainTurnSummary constructs a TurnSummaryView for a 0-tool folded
// plain response turn (reusable entry point for Child 2) so it uses the exact
// same summary model, template, token formatting, and DOM contract without
// creating a second container.
func NewFoldedPlainTurnSummary(key, entryKey string, turn wslive.RingEntry, at int64, atRFC string, replyBytes int64, label string) *TurnSummaryView {
	v := &TurnSummaryView{
		Key:         key,
		EntryKey:    entryKey,
		At:          at,
		AtRFC:       atRFC,
		TurnAtRFC:   atRFC,
		Completed:   true,
		CustomLabel: label,
		Calls:       turn.Done + turn.Failed,
		Failed:      turn.Failed,
		DurationMs:  turn.DurationMs,
		AddedTokens: turn.AddedTokens,
		Origin:      turn.Origin,
		WakeTurn:    turn.WakeTurn,
		Reason:      turn.Reason,
	}
	if v.AddedTokens <= 0 && replyBytes > 0 {
		v.EstTokens = estTurnTokens(0, replyBytes)
	}
	return v
}

// CountText returns the primary count label (or CustomLabel when set).
func (v *TurnSummaryView) CountText() string {
	if v == nil {
		return ""
	}
	if v.CustomLabel != "" {
		return v.CustomLabel
	}
	return wsi18n.Plural(v.Lang, "conv.turn.tools", v.Calls)
}

// TokenText formats the single per-turn token delta: exact "+Nk tok" when
// AddedTokens > 0, render-time byte-estimate fallback "~+Nk tok" when
// AddedTokens == 0 and EstTokens > 0, or empty when both are 0.
func (v *TurnSummaryView) TokenText() string {
	if v == nil {
		return ""
	}
	if v.AddedTokens > 0 {
		return "+" + compactTokens(v.AddedTokens) + " tok"
	}
	if v.EstTokens > 0 {
		return "~+" + compactTokens(v.EstTokens) + " tok"
	}
	return ""
}

// TokenEstimated reports whether TokenText is a render-time byte-estimate fallback.
func (v *TurnSummaryView) TokenEstimated() bool {
	return v != nil && v.AddedTokens <= 0 && v.EstTokens > 0
}

// TokenTitle returns a localized hover explanation for the token delta.
func (v *TurnSummaryView) TokenTitle() string {
	if v == nil {
		return ""
	}
	if v.AddedTokens > 0 {
		return wsi18n.T(v.Lang, "conv.turn.tokens.exact", commafy(v.AddedTokens))
	}
	if v.EstTokens > 0 {
		return wsi18n.T(v.Lang, "conv.turn.tokens.est", commafy(v.EstTokens))
	}
	return ""
}

// CacheFingerprint encodes every rendered field of the summary so entryCache
// invalidates when an anchor transitions from open/orphan/live to completed
// or absorbs additional gaps.
func (v *TurnSummaryView) CacheFingerprint() string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\x00ts:")
	b.WriteString(v.Key)
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(v.At, 10))
	b.WriteByte('|')
	b.WriteString(v.TurnAtRFC)
	b.WriteByte('|')
	b.WriteString(strconv.FormatBool(v.Completed))
	b.WriteByte('|')
	b.WriteString(v.CustomLabel)
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(v.Calls))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(v.Failed))
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(v.DurationMs, 10))
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(v.AddedTokens, 10))
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(v.EstTokens, 10))
	b.WriteByte('|')
	b.WriteString(v.Origin)
	b.WriteByte('|')
	b.WriteString(strconv.FormatBool(v.WakeTurn))
	b.WriteByte('|')
	b.WriteString(v.Reason)
	for _, g := range v.Gaps {
		b.WriteByte(',')
		b.WriteString(g.EntryKey)
		if g.Tools != nil {
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(g.Tools.Calls))
			b.WriteByte('/')
			b.WriteString(strconv.Itoa(g.Tools.Failed))
			b.WriteByte('/')
			b.WriteString(strconv.FormatInt(g.Tools.DurationMs, 10))
		}
	}
	return b.String()
}

// TurnRev returns a compact revision token for data-turn-rev so client
// mergeEntries replaces an existing anchor node when counts, token delta,
// completion state, or gaps change, while skipping unchanged rows.
func (v *TurnSummaryView) TurnRev() string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	if v.Completed {
		b.WriteByte('1')
	} else {
		b.WriteByte('0')
	}
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(v.Calls))
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(v.Failed))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(v.DurationMs, 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(v.AddedTokens, 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(v.EstTokens, 10))
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(len(v.Gaps)))
	if v.WakeTurn {
		b.WriteString(":w")
	}
	if v.Reason != "" {
		b.WriteByte(':')
		b.WriteString(v.Reason)
	}
	return b.String()
}

// leadTurnRunning reports whether the lead currently has an active turn with
// observed live turn state. Idle sessions (Phase empty or "idle") never
// render a live turn summary header, and spans with TurnStartedAt == 0 and no
// open ToolGap keep their orphan RingTools in r-entries (Decision 5).
func leadTurnRunning(lead wslive.Lead) bool {
	if lead.Phase == "" || lead.Phase == wslive.PhaseIdle {
		return false
	}
	return lead.TurnStartedAt > 0 || lead.ToolGap != nil
}

// estTurnTokens estimates context tokens added from tool and reply byte sizes:
// (toolBytes + replyBytes) / 4, floored at 1 when total > 0, 0 when total <= 0.
func estTurnTokens(toolBytes, replyBytes int64) int64 {
	total := max(toolBytes, int64(0)) + max(replyBytes, int64(0))
	if total <= 0 {
		return 0
	}
	return max(int64(1), total/4)
}

// groupTurnSummaries groups conversation ring entries into turn spans bounded
// by RingTurn entries (Decision 5). Completed turns with >= 1 RingTools
// collapse all RingTools in the span into one summary anchored at the first
// RingTools position and absorb the closing RingTurn (preserving any
// SummaryHistory reasoning summary host at turn-end). Active open spans with
// >= 1 flushed RingTools or Lead.ToolGap return a single live TurnSummaryView
// for r-convlive while marking open-span RingTools as live-absorbed so there
// is never a second container. Idle open spans with orphan RingTools fall back
// to a summary derived from those RingTools entries.
func groupTurnSummaries(lead wslive.Lead, entries []ConvEntry) *TurnSummaryView {
	spanStart := 0
	for i := range entries {
		if entries[i].Kind != wslive.RingTurn {
			continue
		}
		collapseCompletedTurnSpan(entries, spanStart, i)
		spanStart = i + 1
	}
	return collapseOpenTurnSpan(lead, entries, spanStart)
}

func collapseCompletedTurnSpan(entries []ConvEntry, spanStart, turnIdx int) {
	var toolIdxs []int
	var replyBytes int64
	for j := spanStart; j < turnIdx; j++ {
		switch entries[j].Kind {
		case wslive.RingTools:
			if entries[j].Tools != nil {
				toolIdxs = append(toolIdxs, j)
			}
		case wslive.RingReply:
			replyBytes += int64(len(entries[j].Text))
		}
	}
	if len(toolIdxs) == 0 {
		// 0-tool turns keep their existing .turnline unchanged unless Child 2 folds them.
		return
	}

	turn := entries[turnIdx]
	anchorIdx := toolIdxs[0]
	anchor := entries[anchorIdx]

	var gaps []TurnGapItem
	var gapCalls, gapFailed int
	var gapDurMs, toolBytes int64
	for _, idx := range toolIdxs {
		e := entries[idx]
		gaps = append(gaps, TurnGapItem{
			EntryKey: e.Key,
			At:       e.At,
			AtRFC:    e.AtRFC,
			Tools:    e.Tools,
		})
		gapCalls += e.Tools.Calls
		gapFailed += e.Tools.Failed
		gapDurMs += e.Tools.DurationMs
		toolBytes += int64(max(e.Tools.InBytes, 0)) + int64(max(e.Tools.OutBytes, 0))
	}

	calls := turn.Done + turn.Failed
	failed := turn.Failed
	if calls == 0 {
		calls = gapCalls
		failed = gapFailed
	}
	durMs := turn.DurationMs
	if durMs <= 0 {
		durMs = gapDurMs
	}
	at := turn.At
	if at <= 0 {
		at = anchor.At
	}

	summary := &TurnSummaryView{
		Key:         "turn-" + anchor.Key,
		EntryKey:    anchor.Key,
		At:          at,
		AtRFC:       anchor.AtRFC,
		TurnAtRFC:   turn.AtRFC,
		Completed:   true,
		Calls:       calls,
		Failed:      failed,
		DurationMs:  durMs,
		AddedTokens: turn.AddedTokens,
		Origin:      turn.Origin,
		WakeTurn:    turn.WakeTurn,
		Reason:      turn.Reason,
		Gaps:        gaps,
	}
	if summary.AddedTokens <= 0 {
		summary.EstTokens = estTurnTokens(toolBytes, replyBytes)
	}

	entries[anchorIdx].TurnSummary = summary
	for _, idx := range toolIdxs[1:] {
		entries[idx].TurnAbsorbed = true
	}
	if entries[turnIdx].Summary() != nil {
		// Preserve .turn-with-summary reasoning-summary host at turn-end position
		// without duplicating the .turnline footer.
		entries[turnIdx].TurnReasoningOnly = true
	} else {
		entries[turnIdx].TurnAbsorbed = true
	}
}

func collapseOpenTurnSpan(lead wslive.Lead, entries []ConvEntry, spanStart int) *TurnSummaryView {
	var toolIdxs []int
	var replyBytes int64
	for j := spanStart; j < len(entries); j++ {
		switch entries[j].Kind {
		case wslive.RingTools:
			if entries[j].Tools != nil {
				toolIdxs = append(toolIdxs, j)
			}
		case wslive.RingReply:
			replyBytes += int64(len(entries[j].Text))
		}
	}

	if leadTurnRunning(lead) {
		if len(toolIdxs) == 0 && lead.ToolGap == nil {
			return nil
		}
		var gaps []TurnGapItem
		var calls, failed int
		var durMs, toolBytes int64
		var at int64
		for _, idx := range toolIdxs {
			e := entries[idx]
			entries[idx].TurnLiveAbsorbed = true
			gaps = append(gaps, TurnGapItem{
				EntryKey: e.Key,
				At:       e.At,
				AtRFC:    e.AtRFC,
				Tools:    e.Tools,
			})
			calls += e.Tools.Calls
			failed += e.Tools.Failed
			durMs += e.Tools.DurationMs
			toolBytes += int64(max(e.Tools.InBytes, 0)) + int64(max(e.Tools.OutBytes, 0))
			if e.At > at {
				at = e.At
			}
		}
		if lead.ToolGap != nil {
			gaps = append(gaps, TurnGapItem{Tools: lead.ToolGap, Live: true})
			calls += lead.ToolGap.Calls
			failed += lead.ToolGap.Failed
			durMs += lead.ToolGap.DurationMs
			toolBytes += int64(max(lead.ToolGap.InBytes, 0)) + int64(max(lead.ToolGap.OutBytes, 0))
		}
		if at == 0 && lead.TurnStartedAt > 0 {
			at = lead.TurnStartedAt
		}
		if lead.Reply != nil {
			replyBytes += int64(len(lead.Reply.Text))
		}
		live := &TurnSummaryView{
			Key:         "turn-live",
			At:          at,
			Live:        true,
			Calls:       calls,
			Failed:      failed,
			DurationMs:  durMs,
			AddedTokens: lead.TurnAddedTokens,
			Gaps:        gaps,
		}
		if live.AddedTokens <= 0 {
			live.EstTokens = estTurnTokens(toolBytes, replyBytes)
		}
		return live
	}

	// Idle session or saved history without a closing RingTurn: never emit a
	// live header; fall back to a gap-derived summary for orphan RingTools.
	if len(toolIdxs) == 0 {
		return nil
	}
	anchorIdx := toolIdxs[0]
	anchor := entries[anchorIdx]
	var gaps []TurnGapItem
	var calls, failed int
	var durMs, toolBytes int64
	var at int64
	for _, idx := range toolIdxs {
		e := entries[idx]
		gaps = append(gaps, TurnGapItem{
			EntryKey: e.Key,
			At:       e.At,
			AtRFC:    e.AtRFC,
			Tools:    e.Tools,
		})
		calls += e.Tools.Calls
		failed += e.Tools.Failed
		durMs += e.Tools.DurationMs
		toolBytes += int64(max(e.Tools.InBytes, 0)) + int64(max(e.Tools.OutBytes, 0))
		if e.At > at {
			at = e.At
		}
	}
	orphan := &TurnSummaryView{
		Key:        "turn-" + anchor.Key,
		EntryKey:   anchor.Key,
		At:         at,
		AtRFC:      anchor.AtRFC,
		Calls:      calls,
		Failed:     failed,
		DurationMs: durMs,
		EstTokens:  estTurnTokens(toolBytes, replyBytes),
		Gaps:       gaps,
	}
	entries[anchorIdx].TurnSummary = orphan
	for _, idx := range toolIdxs[1:] {
		entries[idx].TurnAbsorbed = true
	}
	return nil
}

// toolFamilyCategory maps a tool name to one of the six calm category dot
// families matching the approved v10 mockup: exec, tickets, agent, git, file,
// or think.
func toolFamilyCategory(name string) string {
	short := strings.ToLower(strings.TrimSpace(toolName(name)))
	if short == "" {
		return "think"
	}
	switch {
	case short == "bash" || short == "shell" || short == "command" || short == "terminal" ||
		short == "run_command" || strings.HasPrefix(short, "exec_") || strings.HasPrefix(short, "exec."):
		return "exec"
	case strings.HasPrefix(short, "git_") || strings.HasPrefix(short, "git.") ||
		strings.HasPrefix(short, "worktree_") || strings.HasPrefix(short, "worktree.") ||
		strings.HasPrefix(short, "rationale_") || strings.HasPrefix(short, "rationale."):
		return "git"
	case strings.HasPrefix(short, "tickets_") || strings.HasPrefix(short, "tickets.") ||
		strings.HasPrefix(short, "ticket") ||
		strings.HasPrefix(short, "todo_") || strings.HasPrefix(short, "todo.") ||
		strings.HasPrefix(short, "agenda_") || strings.HasPrefix(short, "agenda.") ||
		strings.HasPrefix(short, "note_") || strings.HasPrefix(short, "note.") ||
		strings.HasPrefix(short, "route_") || strings.HasPrefix(short, "route.") ||
		strings.HasPrefix(short, "whiteboard_") || strings.HasPrefix(short, "whiteboard.") ||
		strings.HasPrefix(short, "review_") || strings.HasPrefix(short, "review.") ||
		strings.HasPrefix(short, "playbook_") || strings.HasPrefix(short, "playbook."):
		return "tickets"
	case short == "agent" || short == "task" || short == "swarm" ||
		strings.HasPrefix(short, "agent_") || strings.HasPrefix(short, "agent.") ||
		strings.HasPrefix(short, "mailbox_") || strings.HasPrefix(short, "mailbox.") ||
		strings.HasPrefix(short, "subagent") || strings.HasPrefix(short, "dispatch"):
		return "agent"
	case short == "read" || short == "edit" || short == "write" || short == "grep" ||
		short == "glob" || short == "multiedit" || short == "notebookedit" ||
		short == "ls" || short == "find" || short == "view_file" ||
		short == "edit_file" || short == "write_file" || short == "grep_search" ||
		short == "find_by_name" || short == "list_dir" ||
		strings.HasPrefix(short, "read_") || strings.HasPrefix(short, "write_") ||
		strings.HasPrefix(short, "list_") || strings.HasPrefix(short, "search_") ||
		strings.HasPrefix(short, "path_") || strings.HasPrefix(short, "path.") ||
		strings.HasPrefix(short, "file"):
		return "file"
	default:
		return "think"
	}
}

func turnBatchCategory(b wslive.ToolBatch) string {
	if len(b.Tools) > 0 {
		return toolFamilyCategory(b.Tools[0].Name)
	}
	if len(b.Names) > 0 {
		return toolFamilyCategory(b.Names[0])
	}
	return "think"
}

// turnBatchNums formats trailing numbers for a legacy batch row inside
// [data-conv-turn-drawer]: unnamed call count, duration, and failed count only.
// Per-tool rows never fabricate per-call token deltas (Decision 2).
func turnBatchNums(lang string, b wslive.ToolBatch) []batchNum {
	var nums []batchNum
	add := func(sep, text, title, class string) {
		if len(nums) == 0 {
			sep = ""
		}
		nums = append(nums, batchNum{Sep: sep, Text: text, Title: title, Class: class})
	}
	named := 0
	for _, t := range b.Tools {
		named += t.Calls
	}
	if named < b.Calls {
		add(" · ", "×"+strconv.Itoa(b.Calls), wsi18n.Plural(lang, "batch.calls", b.Calls), "")
	}
	if b.DurationMs > 0 {
		add(" · ", batchSpanText(b.DurationMs), wsi18n.T(lang, "batch.duration", commafy(b.DurationMs)), "")
	}
	if b.Failed > 0 {
		add(" · ", "✗"+strconv.Itoa(b.Failed), wsi18n.T(lang, "count.failed", b.Failed), "bf")
	}
	return nums
}
