package wsagent

import (
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wslive"
)

// werkMapper turns the loop's output lines (werk.Line) into events of the
// common contract. It keeps only what a replay rebuilds from the lines
// alone: the running batch, the session's cost, and the open turn.
//
// Lines map as follows:
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
// The runner owns user-message rendering. The lead reasoning widget gets
// only provider-exposed summary text.
type werkMapper struct {
	session string
	batches batchTracker
	// resp counts the model responses of the session: a batch is one
	// response's tool calls.
	resp int
	// cost is the session's accumulated cost in USD; hasCost says the
	// provider reported one.
	cost     float64
	hasCost  bool
	turn     int
	turnID   string
	provider string
	summary  *wslive.ReasoningSummary
}

func newWerkMapper(session string) *werkMapper {
	return &werkMapper{session: session, batches: batchTracker{epoch: newBatchTracker("werk").epoch, callIDs: true}}
}

func (m *werkMapper) restoreUsage(usage *wslive.Usage) {
	if usage != nil && usage.Cost != nil {
		m.cost, m.hasCost = usage.Cost.USD, true
	}
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
		m.turn, m.turnID, m.summary = l.Turn, l.TurnID, nil
		return []Event{{TurnStart: true, TurnID: l.TurnID}}
	case werk.LineReply:
		ev := &wslive.Event{Kind: wslive.KindReply, Text: l.Text}
		if l.Final {
			ev.Final = true
			ev.Source = m.replySource(l)
		}
		return []Event{{Live: ev, TurnID: m.turnID}}
	case werk.LineReasoning:
		return m.reasoning(l)
	case werk.LineToolStart:
		return m.batches.addCall(m.group(), l.Call, l.Name, l.Args, len(l.Args), now)
	case werk.LineToolEnd:
		return m.batches.endCall(l.Call, l.Failed, len(l.Output), now)
	case werk.LineCheckpoint:
		if l.Replayed {
			return nil
		}
		return []Event{{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd}, compactCommandDone: true}}
	case werk.LineCompactFailed:
		return []Event{{CommandError: "compaction: " + l.Error}}
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
			if l.UsageLimit {
				out = append(out, Event{
					Alert: &wslive.Notice{
						Kind:     wslive.NoticeUsageLimit,
						Text:     end.TurnError,
						ResetsAt: l.ResetsAt,
					},
					AlertManualOnly: l.PlanPaused,
					TurnID:          l.TurnID,
				})
			}
		}
		return append(out, Event{RetryEnd: true}, end)
	case werk.LineSteerAck:
		return []Event{{SteerAnswer: &SteerAnswer{Refusal: l.Refusal}}}
	case werk.LineUserTaken:
		if l.TurnID != "" {
			return []Event{{TurnID: l.TurnID, durableSteerID: l.ID}}
		}
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

func (m *werkMapper) reasoning(l werk.Line) []Event {
	if m.provider == "" || l.Text == "" || l.Model == "" || m.turn <= 0 || m.resp <= 0 {
		return nil
	}
	var next wslive.ReasoningSummary
	if m.summary == nil {
		next = wslive.ReasoningSummary{V: 1, Producer: uuid.NewString(), Turn: int64(m.turn)}
	} else {
		next = *m.summary
		next.Requests = slices.Clone(next.Requests)
	}
	request := int64(m.resp)
	if len(next.Requests) == 0 || next.Requests[len(next.Requests)-1].Request != request {
		if len(next.Requests) == wslive.SummaryAttemptCap {
			next.Requests = next.Requests[1:]
			next.Truncated = true
		}
		next.Requests = append(next.Requests, wslive.SummaryAttempt{
			Request: request, Attempt: 1, Provider: m.provider, API: "openai-responses",
			Model: l.Model, Status: wslive.SummaryComplete,
		})
	}
	remaining, parts := wslive.SummaryTextBytes, 0
	for _, a := range next.Requests {
		for _, p := range a.Parts {
			remaining -= len(p.Text)
			parts++
		}
	}
	attempt := &next.Requests[len(next.Requests)-1]
	if parts < wslive.SummaryPartCap && remaining > 0 {
		text := l.Text[:min(len(l.Text), remaining)]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		truncated := len(text) < len(l.Text)
		attempt.Truncated = attempt.Truncated || truncated
		attempt.Parts = append(slices.Clone(attempt.Parts), wslive.SummaryPart{
			Output: int64(len(attempt.Parts)), Text: text, ObservedBytes: int64(len(l.Text)),
			RetainedBytes: int64(len(text)), Truncated: truncated, Status: wslive.SummaryComplete,
		})
	} else {
		attempt.Truncated = true
	}
	next.Truncated = next.Truncated || attempt.Truncated
	next.Revision++
	if wslive.ValidateReasoningSummary(&next) != nil {
		return nil
	}
	m.summary = &next
	return []Event{{ReasoningSummary: m.summary, TurnID: m.turnID}}
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
