package wsagent

import (
	"encoding/json"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// replayCompaction reconstructs only an unfinished lifecycle. Finished
// historical compactions must not queue another restore on every rejoin.
func (c *conv) replayCompaction(ev wslive.Event) {
	switch ev.Phase {
	case wslive.CompactStart:
		c.compacting = true
	case wslive.CompactEnd, wslive.CompactFailed:
		c.compacting = false
	}
}

// drainCompaction retains an already dispatched request's volatile resume
// intent for a bounded portion of graceful shutdown. It starts no pending
// requests and leaves a slow harness running under its holder.
func (c *conv) drainCompaction() {
	if !c.compactCommand || c.events == nil {
		return
	}
	// Leave room in the daemon's five-second successor lock budget for its
	// proxy drain and the existing one-second partial-line flush.
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for c.compactCommand || c.compacting {
		select {
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			c.handle(ev)
		case <-timer.C:
			return
		}
	}
	if !c.turn && c.compactResume && c.compactNote != "" {
		c.compactIdle()
	}
}

const claudeRecoveryContextAsk = "ws-recovery-context"

// Query only harnesses with a documented usage request. The answer, not
// replayed usage, becomes the first live observation after reattachment.
// A Claude worker asks too: its process runs the same control channel, and
// a worker resumed or rejoined at idle above its hard point compacts before
// its next input instead of after one more turn.
func (c *conv) queryRecoveryUsage() {
	var p *claudeProcess
	switch proc := c.proc.(type) {
	case claudeProcess:
		p = &proc
	case *claudeProcess:
		p = proc
	}
	if p == nil {
		return
	}
	c.compactUsageFresh = false
	p.send(claudeProtocol{}.requestLine(claudeRecoveryContextAsk, map[string]any{"subtype": "get_context_usage"}))
}

func (m *claudeMapper) recoveryContextUsage(l claudeLine) []Event {
	var d struct {
		TotalTokens  *int64 `json:"totalTokens"`
		RawMaxTokens int64  `json:"rawMaxTokens"`
	}
	if l.Response.Subtype != "success" || json.Unmarshal(l.Response.Response, &d) != nil || d.TotalTokens == nil || *d.TotalTokens < 0 || d.RawMaxTokens <= 0 {
		return nil
	}
	m.compactWindow, m.compactModel = d.RawMaxTokens, m.model
	m.context = &wslive.UsageContext{Tokens: d.TotalTokens}
	return []Event{m.status()}
}
