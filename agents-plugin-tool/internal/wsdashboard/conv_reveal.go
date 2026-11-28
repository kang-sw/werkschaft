package wsdashboard

import (
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"
)

// ConvReveal is a dashboard UI highlight/jump event pushed over the live rail
// WebSocket stream when conv.reveal targets a session (and optionally a ring
// entry). Non-intrusive rail highlight is the default; Focus asks an active
// tab to navigate immediately to /s/<replyId>#entry=<entryId>.
type ConvReveal struct {
	ReplyID string `json:"replyId"`
	EntryID string `json:"entryId,omitempty"`
	Focus   bool   `json:"focus,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

const (
	convRevealEvent       = "conv-reveal"
	convRevealReasonRunes = 200
	convRevealEntryLimit  = 128
	convRevealRingCap     = 16
)

type convRevealRecord struct {
	seq     uint64
	payload []byte
}

// convRevealHub broadcasts conv-reveal events to open rail streams. A small
// ring retains recent publishes so back-to-back calls are not coalesced away
// before a stream wakes up; streams only receive events published after they
// connected.
type convRevealHub struct {
	mu   sync.Mutex
	seq  uint64
	ring []convRevealRecord
	wake chan struct{}
}

func (h *convRevealHub) current() (uint64, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	return h.seq, h.wake
}

func (h *convRevealHub) since(after uint64) (uint64, [][]byte, <-chan struct{}) {
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

func (h *convRevealHub) publish(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	h.ring = append(h.ring, convRevealRecord{seq: h.seq, payload: payload})
	if len(h.ring) > convRevealRingCap {
		h.ring = h.ring[len(h.ring)-convRevealRingCap:]
	}
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
}

// PublishConvReveal validates and broadcasts ev to every open rail stream.
func (d *Daemon) PublishConvReveal(ev ConvReveal) bool {
	if d == nil {
		return false
	}
	ev.ReplyID = strings.TrimSpace(ev.ReplyID)
	if !ValidReplyID(ev.ReplyID) {
		return false
	}
	ev.EntryID = strings.TrimSpace(ev.EntryID)
	if len(ev.EntryID) > convRevealEntryLimit {
		cut := convRevealEntryLimit
		for cut > 0 && !utf8.RuneStart(ev.EntryID[cut]) {
			cut--
		}
		ev.EntryID = ev.EntryID[:cut]
	}
	ev.Reason = strings.TrimSpace(ev.Reason)
	if runes := []rune(ev.Reason); len(runes) > convRevealReasonRunes {
		ev.Reason = string(runes[:convRevealReasonRunes])
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return false
	}
	d.convReveals.publish(payload)
	return true
}
