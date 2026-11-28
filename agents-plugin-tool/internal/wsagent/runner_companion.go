package wsagent

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

// CompanionHandoffCheckpoint builds a carry-forward checkpoint for companion
// conversation id from its own ring dialog, with residual (the Messenger
// digest) as the residual-details prose. It reads only id's ring and leaves
// the conversation untouched: a cross-harness handoff passes the result to
// the replacement session's launch prompt and then closes id.
func (r *Runner) CompanionHandoffCheckpoint(id, residual string) (string, error) {
	rec, ok := r.recordOf(id)
	if !ok || rec.CompanionRole == "" {
		return "", fmt.Errorf("%s is not a companion conversation", id)
	}
	var records []wslive.RingRecord
	if page, err := wslive.ReadRingPage(filepath.Join(wsdashboard.SessionDir(r.opts.Dir, id), "ring"), "", wslive.RingHistoryCap); err == nil {
		records = page.Records
	}
	dialog := wscompact.BuildRingDialog(records, "ring")
	fields := wscompact.ProseFields()
	prose := make([]string, len(fields))
	prose[len(prose)-1] = wscompact.BoundedText(wscompact.Scrub(residual), wscompact.ProseFieldBytes)
	return wscompact.BuildCheckpoint(wscompact.CheckpointInput{
		Lead:         true,
		OmitChildren: true,
		Prose:        prose,
		Dialog:       dialog.Dialog,
	}), nil
}

// CompanionSessions lists the open companion conversations carrying role,
// newest first (CreatedUnixNano when both records carry it, else CreatedAt,
// then ReplyID). Open covers a live conversation and
// a parked one (rehydrated on its next delivery); closed and handed-off
// records are left out. The companion runtime uses it at start to reattach
// the single companion-body session a previous daemon left running instead
// of launching another.
func (r *Runner) CompanionSessions(role string) []Record {
	if role == "" {
		return nil
	}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	var out []Record
	for id := range r.parked {
		if rec, ok := r.closed[id]; ok && r.convs[id] == nil && rec.CompanionRole == role && !rec.handedOff() {
			out = append(out, rec)
		}
	}
	r.mu.Unlock()
	for _, c := range convs {
		c.mu.Lock()
		rec, closed := c.rec, c.closed
		c.mu.Unlock()
		if closed || rec.CompanionRole != role || rec.ClosedAt != "" || rec.handedOff() {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].CreatedUnixNano, out[j].CreatedUnixNano; a != 0 && b != 0 && a != b {
			return a > b
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ReplyID > out[j].ReplyID
	})
	return out
}

// IsConversationOpen reports whether conversation id is live or parked: a
// delivery reaches it either way. IsConversationLive is the live-only test.
func (r *Runner) IsConversationOpen(id string) bool {
	if r.IsConversationLive(id) {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.parked[id] && r.convs[id] == nil
}

// RotateCompanion rotates companion session wsKey onto a fresh harness
// session at its next idle boundary, carrying a checkpoint rebuilt from its
// ring dialog (no prose round trip: a companion has no ws/compact tool). The
// rotation does not resume the conversation; the next delivery starts the
// fresh session.
func (r *Runner) RotateCompanion(wsKey string) CompactResult {
	resume := false
	return r.CompactWithSummary(wsKey, "", &resume, emptyWorkerSummary())
}
