package mcp

import (
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wslive"
)

// startWorkerFeed subscribes terminal lead snapshots to generic hosted
// worker changes. Hosted lead snapshots are written by the runner itself.
func (s *Server) startWorkerFeed() {
	if s.agents == nil {
		return
	}
	s.stopWorkerWatch = s.agents.Watch(s.workerRowChanged)
}

// workerRowChanged applies one descendant's change to its owning lead's live sessions
// in this server. It runs on the child's conversation loop.
func (s *Server) workerRowChanged(st wsagent.State) {
	lead := wsagent.SpawnLead(st, s.agents.Lineage)
	if lead == "" {
		return
	}
	sessions := s.live.byReplyID(lead)
	if len(sessions) == 0 {
		return
	}
	rows := wsagent.SpawnAgentEvents(st, s.workerRowKey(st.ID))
	for _, ls := range sessions {
		ls.applyAgentRows(rows)
	}
}

// seedWorkerRows gives a live session the rows of its open hosted worker tree.
func (s *Server) seedWorkerRows(ls *liveSession) {
	if s.agents == nil {
		return
	}
	var rows []wslive.Event
	for _, st := range wsagent.SpawnTree(wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: ls.replyID}, s.agents.Children) {
		if st.Status != wsagent.StatusClosed {
			rows = append(rows, wsagent.SpawnAgentEvents(st, s.workerRowKey(st.ID))...)
		}
	}
	if len(rows) > 0 {
		ls.applyAgentRows(rows)
	}
}

// workerRowKey is a child's ws session key, which labels its row with the
// child's reply-id (its conversation page); "" when its record cannot be
// read, which leaves the row unlinked.
func (s *Server) workerRowKey(id string) string {
	s.workerRowKeys.mu.Lock()
	key, ok := s.workerRowKeys.keys[id]
	s.workerRowKeys.mu.Unlock()
	if ok {
		return key
	}
	dir, err := s.dashboard.stateDir()
	if err != nil {
		return ""
	}
	rec, err := wsagent.ReadRecord(dir, id)
	if err != nil || rec.WSKey == "" {
		return ""
	}
	s.workerRowKeys.mu.Lock()
	if s.workerRowKeys.keys == nil {
		s.workerRowKeys.keys = map[string]string{}
	}
	s.workerRowKeys.keys[id] = rec.WSKey
	s.workerRowKeys.mu.Unlock()
	return rec.WSKey
}

// byReplyID is the hub's running live sessions of reply-id replyID.
func (h *liveHub) byReplyID(replyID string) []*liveSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*liveSession
	for _, ls := range h.sessions {
		if ls.replyID == replyID {
			out = append(out, ls)
		}
	}
	return out
}

// applyAgentRows applies agent events the server derived itself and
// schedules the snapshot write as a subagent's change.
func (ls *liveSession) applyAgentRows(events []wslive.Event) {
	seams := currentLiveSeams()
	now := seams.now().UnixMilli()
	ls.mu.Lock()
	if ls.stopped {
		ls.mu.Unlock()
		return
	}
	var observed []wslive.ObservedRecord
	changed := false
	for _, ev := range events {
		res, err := ls.state.Apply(ev, now)
		if err != nil {
			ls.debug("dashboard.worker_row_error", map[string]any{"agent": ev.AgentID, "error": err.Error()})
			continue
		}
		changed = changed || res.Changed
		observed = append(observed, res.Observed...)
	}
	if changed {
		ls.dirty = true
		ls.scheduleLocked(seams.debounceAgent)
	}
	ls.ringMu.Lock()
	ls.mu.Unlock()
	if err := wslive.AppendObserved(ls.dir, observed); err != nil {
		ls.debug("dashboard.model_evidence_write_error", map[string]any{"error": err.Error()})
	}
	ls.ringMu.Unlock()
}
