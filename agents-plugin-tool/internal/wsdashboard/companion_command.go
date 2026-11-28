package wsdashboard

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
)

// CompanionCommand is one agent-director or Puppeteer instruction for the
// companion of the session it names. The daemon broadcasts it on every rail
// stream; each shell acts only on the session it shows (or "*" for Brain /
// wildcard commands) and validates every ID against its active pack, dropping
// an unknown field. Quip text is rendered as text only.
type CompanionCommand struct {
	Seq        int64  `json:"seq,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Source     string `json:"source,omitempty"`
	SessionID  string `json:"sessionId"`
	Scenario   string `json:"scenario,omitempty"`
	Expression string `json:"expression,omitempty"`
	Gesture    string `json:"gesture,omitempty"`
	Prop       string `json:"prop,omitempty"`
	FX         string `json:"fx,omitempty"`
	Quip       string `json:"quip,omitempty"`
	TTLms      int    `json:"ttl_ms"`
}

const (
	CompanionCommandMinTTL  = 1000
	CompanionCommandMaxTTL  = 15000
	CompanionQuipMaxRunes   = 140
	companionCommandEvent   = "companion-command"
	companionCommandIDLimit = 64
)

// companionCommandHub keeps the newest published command. A newer command
// replaces an older one, so a stream that falls behind sends only the newest
// (the shell would replace the older one anyway). A stream sends only what
// was published after it connected.
type companionCommandHub struct {
	seqCounter atomic.Int64
	mu         sync.Mutex
	seq        uint64
	payload    []byte
	wake       chan struct{}
}

// current returns the newest sequence, its payload, and the channel closed
// by the next publish.
func (h *companionCommandHub) current() (uint64, []byte, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	return h.seq, h.payload, h.wake
}

func (h *companionCommandHub) publish(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	h.payload = payload
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
}

// PublishCompanionCommand clamps the TTL, bounds the quip, stamps a monotonic
// sequence number, and broadcasts the command to every open shell rail stream.
// A command without a session is dropped.
func (d *Daemon) PublishCompanionCommand(cmd CompanionCommand) bool {
	cmd, ok := normalizeCompanionCommand(cmd)
	if !ok {
		return false
	}
	if cmd.Seq <= 0 {
		cmd.Seq = d.companionCommands.seqCounter.Add(1)
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return false
	}
	d.companionCommands.publish(payload)
	return true
}

// normalizeCompanionCommand applies the payload bounds the shell enforces
// again: TTL within 1-15 s, a quip of at most 140 code points, normalized
// priority/source tags, and bounded ID strings (an overlong ID drops that field).
func normalizeCompanionCommand(cmd CompanionCommand) (CompanionCommand, bool) {
	if cmd.SessionID == "" {
		return cmd, false
	}
	cmd.TTLms = min(max(cmd.TTLms, CompanionCommandMinTTL), CompanionCommandMaxTTL)
	switch strings.ToLower(strings.TrimSpace(cmd.Priority)) {
	case "high", "normal", "low":
		cmd.Priority = strings.ToLower(strings.TrimSpace(cmd.Priority))
	default:
		cmd.Priority = ""
	}
	cmd.Source = strings.TrimSpace(cmd.Source)
	if len(cmd.Source) > companionCommandIDLimit {
		cmd.Source = ""
	}
	for _, id := range []*string{&cmd.Scenario, &cmd.Expression, &cmd.Gesture, &cmd.Prop, &cmd.FX} {
		if len(*id) > companionCommandIDLimit {
			*id = ""
		}
	}
	if runes := []rune(cmd.Quip); len(runes) > CompanionQuipMaxRunes {
		cmd.Quip = string(runes[:CompanionQuipMaxRunes])
	}
	return cmd, true
}
