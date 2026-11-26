package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

var _ wsdashboard.AgentMetadataHost = (*Runner)(nil)

// SetMeta binds the daemon's shared SQLite metadata store and startup snapshot
// before Start. Once bound, conversation records, permissions, hiring marks,
// durable agent records, and retired Pi purges persist exclusively through
// SQLite without writing legacy session sidecars. The daemon owns closing store.
func (r *Runner) SetMeta(store *wsmeta.Store, snap *wsmeta.Snapshot) {
	if r == nil || store == nil || snap == nil {
		return
	}
	r.meta = store
	r.metaSnap = snap
	r.durableRecords = store
	r.piPurgeMu.Lock()
	r.piPurgeStore = store
	r.piPurgeMu.Unlock()

	r.recordBaseMu.Lock()
	r.recordBase = make(map[string][]byte, len(snap.Records))
	for id, ar := range snap.Records {
		r.recordBase[id] = bytes.Clone(ar.JSON)
	}
	r.recordBaseMu.Unlock()

	for id, p := range snap.Permissions {
		slot := &permissionSelection{loaded: true, explicit: true}
		if p.State == "valid" {
			level := PermissionLevel(p.Level)
			if level.rank() == 0 {
				slot.err = fmt.Errorf("invalid permission_level for %s", id)
			} else {
				slot.level = level
				slot.delegated = p.Delegated
				slot.delegatedSet = p.DelegatedSet
				slot.rawBody = bytes.Clone(p.Body)
			}
		} else {
			msg := strings.TrimSpace(p.Error)
			if msg == "" {
				msg = fmt.Sprintf("invalid permission selection for %s", id)
			}
			slot.err = errors.New(msg)
		}
		r.perms.Store(id, slot)
	}

	r.hiredByReqMu.Lock()
	r.hiredByReq = make(map[string]string)
	for id, h := range snap.Hiring {
		if h.Hired && h.RequestID != "" {
			r.hiredByReq[h.RequestID] = id
		}
	}
	r.hiredByReqMu.Unlock()
}

func (r *Runner) persistSQLiteRecord(dir string, rec Record) error {
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = recordSchemaVersion
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	r.recordBaseMu.Lock()
	defer r.recordBaseMu.Unlock()
	if r.recordBase == nil {
		r.recordBase = map[string][]byte{}
	}
	ctx := context.Background()
	if prev, have := r.recordBase[rec.ReplyID]; have && len(prev) > 0 {
		patch, err := wsmeta.DiffJSONObjects(prev, raw)
		if err != nil {
			return err
		}
		if len(patch) == 0 {
			return nil
		}
		err = r.meta.PatchRecord(ctx, rec.ReplyID, patch)
		if errors.Is(err, wsmeta.ErrNoRecord) {
			err = r.meta.PutRecord(ctx, rec.ReplyID, "", raw)
		}
		if err != nil {
			return err
		}
	} else {
		if err := r.meta.PutRecord(ctx, rec.ReplyID, "", raw); err != nil {
			return err
		}
	}
	r.recordBase[rec.ReplyID] = bytes.Clone(raw)
	if observe := recordWritten.Load(); observe != nil {
		(*observe)(dir, rec)
	}
	return nil
}

func (r *Runner) persistSQLiteHolderSeq(id, holderID string, seq uint64) error {
	if err := r.meta.SetHolderCursor(context.Background(), id, holderID, seq); err != nil {
		return err
	}
	r.recordBaseMu.Lock()
	if prev, ok := r.recordBase[id]; ok && len(prev) > 0 {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(prev, &members); err == nil && members != nil {
			if seq == 0 {
				delete(members, "holder_seq")
			} else if rawSeq, err := json.Marshal(seq); err == nil {
				members["holder_seq"] = rawSeq
			}
			if updated, err := json.Marshal(members); err == nil {
				r.recordBase[id] = updated
			}
		}
	}
	r.recordBaseMu.Unlock()
	return nil
}

func (r *Runner) deleteMetaSession(id string) {
	if r.meta == nil || id == "" {
		return
	}
	r.recordBaseMu.Lock()
	delete(r.recordBase, id)
	r.recordBaseMu.Unlock()
	r.perms.Delete(id)
	r.hiredByReqMu.Lock()
	for reqID, rid := range r.hiredByReq {
		if rid == id {
			delete(r.hiredByReq, reqID)
		}
	}
	r.hiredByReqMu.Unlock()
	if err := r.meta.DeleteSession(context.Background(), id); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: delete session metadata: %v\n", id, err)
	}
}

func (r *Runner) markHired(replyID, requestID, label string) error {
	if r.meta == nil {
		return wsdashboard.MarkHired(r.opts.Dir, replyID, requestID, label)
	}
	cleanLabel := strings.TrimSpace(label)
	if err := r.meta.MarkHired(context.Background(), replyID, requestID, cleanLabel); err != nil {
		return err
	}
	r.hiredByReqMu.Lock()
	if r.hiredByReq == nil {
		r.hiredByReq = map[string]string{}
	}
	r.hiredByReq[requestID] = replyID
	r.hiredByReqMu.Unlock()
	if r.opts.OnHired != nil {
		r.opts.OnHired(replyID, requestID, cleanLabel)
	}
	return nil
}

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
