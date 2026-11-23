package wsagent

import (
	"strconv"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wslive"
)

// werkMapper turns the loop's output lines (werk.Line) into events of the
// common contract. It keeps only what a replay rebuilds from the lines
// alone: the running batch, the session's cost, and the open turn.
//
// A worker's lines map as follows (the lead role is Phase 2):
//   - init: the backend session id and the harness report (its version);
//   - turn_start, turn_end: TurnStart and TurnEnd (an aborted turn names
//     that reason; an error turn carries its text);
//   - reply: the streamed text, and the final message with a source key that
//     keeps a replayed message from being written twice;
//   - tool_start, tool_end: the batch list, one batch per model response;
//   - usage: the context figure and the session's accumulated cost;
//   - retry: the request retry the dashboard shows;
//   - steer_ack, user_taken: the answers a worker's steered reports need.
//
// user, reasoning and notice lines are not conversation content: the runner
// shows user messages itself, and the provider-exposed summary is Phase 2's
// lead surface.
type werkMapper struct {
	session string
	batches batchTracker
	// resp counts the model responses of the session: a batch is one
	// response's tool calls.
	resp int
	// cost is the session's accumulated cost in USD; hasCost says the
	// provider reported one.
	cost    float64
	hasCost bool
	turn    int
	turnID  string
}

func newWerkMapper(session string) *werkMapper {
	return &werkMapper{session: session, batches: batchTracker{epoch: newBatchTracker("werk").epoch, callIDs: true}}
}

// werkReplySource is the dedup key of a final reply: the session, the turn
// and the message item.
func (m *werkMapper) replySource(l werk.Line) string {
	turn := m.turnID
	if turn == "" {
		turn = strconv.Itoa(l.Turn)
	}
	return "werk/" + m.session + "/" + turn + "/" + l.Item
}

func lineTime(l werk.Line) int64 {
	if l.At > 0 {
		return l.At
	}
	return time.Now().UnixMilli()
}

func (m *werkMapper) mapEvent(l werk.Line) []Event {
	now := lineTime(l)
	switch l.T {
	case werk.LineInit:
		if l.Session != "" {
			m.session = l.Session
		}
		return []Event{
			{SessionID: l.Session},
			{Harness: &HarnessReport{Version: l.Version}},
		}
	case werk.LineTurnStart:
		m.turn, m.turnID = l.Turn, l.TurnID
		return []Event{{TurnStart: true, TurnID: l.TurnID}}
	case werk.LineReply:
		ev := &wslive.Event{Kind: wslive.KindReply, Text: l.Text}
		if l.Final {
			ev.Final = true
			ev.Source = m.replySource(l)
		}
		return []Event{{Live: ev, TurnID: m.turnID}}
	case werk.LineToolStart:
		return m.batches.addCall(m.group(), l.Call, l.Name, l.Args, now)
	case werk.LineToolEnd:
		return m.batches.endCall(l.Call, l.Failed, now)
	case werk.LineUsage:
		return m.usage(l)
	case werk.LineRetry:
		return []Event{{Retry: &wslive.Retry{Attempt: l.Attempt, MaxRetries: l.MaxRetries, Error: l.Error, Status: l.Status}}}
	case werk.LineTurnEnd:
		out := []Event{}
		if ev := m.batches.finish(now, true); ev != nil {
			out = append(out, *ev)
		}
		end := Event{TurnEnd: true, TurnID: l.TurnID}
		switch l.Reason {
		case werk.ReasonAborted:
			end.TurnReason = wslive.TurnAborted
		case werk.ReasonError:
			end.TurnError = l.Error
			if end.TurnError == "" {
				end.TurnError = "the model request failed"
			}
		}
		return append(out, Event{RetryEnd: true}, end)
	case werk.LineSteerAck:
		return []Event{{SteerAnswer: &SteerAnswer{Refusal: l.Refusal}}}
	case werk.LineUserTaken:
		if l.Text == "" {
			return nil
		}
		return []Event{{UserTaken: l.Text}}
	case werk.LineNotice:
		if l.Text != "" {
			return []Event{{Notice: l.Text}}
		}
	}
	return nil
}

// group is the batch group of the response whose calls are running.
func (m *werkMapper) group() string { return "t" + strconv.Itoa(m.turn) + "/" + strconv.Itoa(m.resp) }

// usage maps a response's figures: the usage line comes after the response's
// items and before its tool calls, so the response counter moves here and the
// calls that follow belong to it.
func (m *werkMapper) usage(l werk.Line) []Event {
	m.resp++
	if l.CostUSD != nil {
		m.cost += *l.CostUSD
		m.hasCost = true
	}
	// The event is handed to the runner's goroutine: it shares nothing with
	// the mapper's state.
	tokens := l.Context
	ctx := &wslive.UsageContext{Tokens: &tokens, Window: l.Window}
	if l.Window > 0 {
		percent := float64(l.Context) * 100 / float64(l.Window)
		ctx.Percent = &percent
	}
	if l.InputTokens > 0 {
		hit := float64(l.CachedTokens) * 100 / float64(l.InputTokens)
		ctx.CacheHit = &hit
	}
	ev := wslive.Event{Kind: wslive.KindStatus, Context: ctx}
	if m.hasCost {
		ev.Cost = &wslive.UsageCost{USD: m.cost}
	}
	return []Event{{Live: &ev}}
}
