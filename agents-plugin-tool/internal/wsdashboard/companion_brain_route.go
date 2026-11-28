package wsdashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// companionBrainRoute lazily attaches or launches the Mobile Full Lead
// (Brain, companion-work) session embedded inside the Companion Messenger
// compact iframe (/s/<brainRid>?frame=1&compact=1).
const (
	companionBrainRoute         = "/api/companion/brain"
	companionBrainEvent         = "companion-brain-state"
	companionBrainStateRingCap  = 24
)

// CompanionBrainInfo is the JSON payload returned by GET /api/companion/brain.
type CompanionBrainInfo struct {
	ReplyID          string `json:"replyId"`
	WSKey            string `json:"wsKey,omitempty"`
	Harness          string `json:"harness,omitempty"`
	Model            string `json:"model,omitempty"`
	Tier             string `json:"tier,omitempty"`
	PersonaName      string `json:"personaName,omitempty"`
	Ready            bool   `json:"ready"`
	SeededFromLegacy bool   `json:"seededFromLegacy,omitempty"`
	Error            string `json:"error,omitempty"`
}

// CompanionBrainStateEvent is the additive T0 rail SSE payload published under
// event: companion-brain-state whenever Brain transitions state or hands off
// to a new replyId (e.g. user_message, brain_work_start, subagent_event,
// brain_reply, brain_idle, handoff).
type CompanionBrainStateEvent struct {
	Kind    string `json:"kind"`
	ReplyID string `json:"replyId"`
	Seq     int64  `json:"seq"`
}

type companionBrainStateRecord struct {
	seq     int64
	payload []byte
}

// companionBrainStateHub broadcasts companion-brain-state events to open rail
// streams using a small ring so back-to-back transitions (e.g. handoff or
// user_message -> brain_work_start) are never coalesced away before flush.
type companionBrainStateHub struct {
	mu   sync.Mutex
	seq  int64
	ring []companionBrainStateRecord
	wake chan struct{}
}

func (h *companionBrainStateHub) current() (int64, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	return h.seq, h.wake
}

func (h *companionBrainStateHub) since(after int64) (int64, [][]byte, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	var out [][]byte
	for _, rec := range h.ring {
		if rec.seq > after {
			out = append(out, rec.payload)
		}
	}
	return h.seq, out, h.wake
}

func (h *companionBrainStateHub) publish(kind, replyID string) int64 {
	h.mu.Lock()
	h.seq++
	seq := h.seq
	ev := CompanionBrainStateEvent{
		Kind:    kind,
		ReplyID: replyID,
		Seq:     seq,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		h.mu.Unlock()
		return 0
	}
	h.ring = append(h.ring, companionBrainStateRecord{seq: seq, payload: payload})
	if len(h.ring) > companionBrainStateRingCap {
		h.ring = h.ring[len(h.ring)-companionBrainStateRingCap:]
	}
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
	h.mu.Unlock()
	return seq
}

var companionBrainUnavailable = configError{404, "upgrade_required", "This daemon has no companion brain endpoint."}

// SetCompanionBrain installs the callback backing GET /api/companion/brain;
// nil removes it.
func (d *Daemon) SetCompanionBrain(fn func() (CompanionBrainInfo, error)) {
	if fn == nil {
		d.companionBrain.Store(nil)
		return
	}
	d.companionBrain.Store(&fn)
}

// SetCompanionBrainReplyID records the active Companion Brain (companion-work)
// replyID atomically (zero lock contention on read) so the Puppeteer director
// observes it directly regardless of tab focus. When rid changes to a new
// non-empty ID, it publishes a "companion-brain-state" { kind: "handoff" }
// rail SSE event so open Messenger popups reload onto the new Brain session.
func (d *Daemon) SetCompanionBrainReplyID(rid string) {
	if d == nil {
		return
	}
	rid = strings.TrimSpace(rid)
	var nextPtr *string
	if rid != "" {
		nextPtr = &rid
	}
	prevPtr := d.companionBrainReplyID.Swap(nextPtr)
	prev := ""
	if prevPtr != nil {
		prev = *prevPtr
	}
	if rid != prev {
		d.director.resetBrainFor(rid)
	}
	if rid != "" && rid != prev {
		d.PublishCompanionBrainState("handoff", rid)
	}
}

// CompanionBrainReplyID returns the currently registered Companion Brain
// replyID, or "" when none is active.
func (d *Daemon) CompanionBrainReplyID() string {
	if d == nil {
		return ""
	}
	if p := d.companionBrainReplyID.Load(); p != nil {
		return *p
	}
	return ""
}

// PublishCompanionBrainState broadcasts an additive T0 "companion-brain-state"
// event on every open shell rail SSE stream and returns its monotonic seq.
func (d *Daemon) PublishCompanionBrainState(kind, replyID string) int64 {
	if d == nil {
		return 0
	}
	kind = strings.TrimSpace(kind)
	replyID = strings.TrimSpace(replyID)
	if kind == "" || replyID == "" {
		return 0
	}
	return d.companionBrainStates.publish(kind, replyID)
}

func (d *Daemon) registerCompanionBrainRoute(mux *http.ServeMux) {
	mux.HandleFunc(companionBrainRoute, d.handleCompanionBrain)
}

func (d *Daemon) handleCompanionBrain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !configRouteMethod(w, r, http.MethodGet) {
		return
	}
	if !localBrowseViewer(r) {
		writeConfigError(w, configForbidden)
		return
	}
	fn := d.companionBrain.Load()
	if fn == nil || *fn == nil {
		writeConfigError(w, companionBrainUnavailable)
		return
	}
	info, err := (*fn)()
	if err != nil {
		info.Ready = false
		if info.Error == "" {
			info.Error = err.Error()
		}
		writeJSON(w, http.StatusServiceUnavailable, info)
		return
	}
	writeJSON(w, http.StatusOK, info)
}
