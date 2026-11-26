package wsagent

import (
	"encoding/json"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

var _ wsdashboard.AgentMetadataHost = (*Runner)(nil)

// RecordByReplyID returns replyID's in-memory conversation record (open or
// closed) without touching agent.json on disk.
func (r *Runner) RecordByReplyID(replyID string) (Record, bool) {
	if r == nil || replyID == "" {
		return Record{}, false
	}
	return r.recordOf(replyID)
}

// AgentRecord implements wsdashboard.AgentMetadataHost using the runner's
// in-memory conversation records.
func (r *Runner) AgentRecord(replyID string) (wsdashboard.AgentRecordSnapshot, bool) {
	rec, ok := r.RecordByReplyID(replyID)
	if !ok {
		return wsdashboard.AgentRecordSnapshot{}, false
	}
	snap := wsdashboard.AgentRecordSnapshot{
		ReplyID:     rec.ReplyID,
		WSKey:       rec.WSKey,
		Profile:     rec.Profile,
		Backend:     rec.Backend,
		Root:        rec.Root,
		Model:       rec.Model,
		Effort:      rec.Effort,
		Lead:        rec.Lead,
		ClosedAt:    rec.ClosedAt,
		HandedOffAt: rec.HandedOffAt,
		HolderID:    rec.HolderID,
		HostPID:     rec.HostPID,
		Tier:        rec.Tier,
	}
	if rec.ResolvedDefinition != nil {
		if raw, err := json.Marshal(rec.ResolvedDefinition); err == nil {
			snap.ResolvedDefinition = raw
		}
	}
	if rec.Parent != nil {
		snap.Parent = &wsdashboard.AgentRecordParent{
			Kind:    rec.Parent.Kind,
			ReplyID: rec.Parent.ReplyID,
		}
	}
	return snap, true
}
