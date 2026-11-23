package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wslive"
)

// werkProtocol writes the loop's commands (werk.Command) as JSON lines.
type werkProtocol struct{}

func (werkProtocol) userLine(text string) any {
	return werk.Command{Type: werk.CmdUser, Text: text}
}

// requestLine sends a command that the process answers with a line carrying
// the same id (the status ask).
func (werkProtocol) requestLine(id string, request map[string]any) any {
	typ, _ := request["type"].(string)
	return werk.Command{Type: typ, ID: id}
}

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
//   - steer_ack, user_taken: the answers a worker's steered reports need;
//   - status: the answer to a status ask (recovery after a ring gap).
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

func (m *werkMapper) mapLine(raw []byte, replies lineReplies) []Event {
	l, err := werk.ParseLine(raw)
	if err != nil || l.T == "" {
		return nil
	}
	if l.T == werk.LineStatus {
		replies.answer(l.ID, controlAnswer{ok: true, body: json.RawMessage(raw)})
		return nil
	}
	return m.mapEvent(l)
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

// werkProcess is a worker's process under its holder.
type werkProcess struct {
	*streamProcess
	m      *werkMapper
	seq    atomic.Uint64
	model  string
	effort string

	// asked is the answer to the status ask a rejoin's TurnRunning made, which
	// the recoveryReply right after it reads instead of asking again.
	askedMu sync.Mutex
	asked   *werk.Line
}

func (p *werkProcess) processControls() processControls {
	return processControls{Process: p, steer: p, reports: p, turns: p, recovery: p}
}

func (p *werkProcess) Send(text string) error {
	return p.writeLine(werk.Command{Type: werk.CmdUser, Text: text})
}

// Steer queues text for the running turn; the process answers with a
// steer_ack line (the next unanswered steer, in order), and a user_taken line
// when a request took it.
func (p *werkProcess) Steer(text string) error {
	id := "wsc/steer/" + strconv.FormatUint(p.seq.Add(1), 10)
	return p.writeLine(werk.Command{Type: werk.CmdSteer, ID: id, Text: text})
}

// ClearSteered drops the steered messages no request took, so the runner can
// send them as a turn instead.
func (p *werkProcess) ClearSteered() error {
	return p.writeLine(werk.Command{Type: werk.CmdClearSteered})
}

func (p *werkProcess) Interrupt() error {
	return p.writeLine(werk.Command{Type: werk.CmdInterrupt})
}

func (p *werkProcess) SetModel(model string) error {
	if model == "" {
		return ErrUnsupported
	}
	p.model = model
	return p.writeLine(werk.Command{Type: werk.CmdSet, Model: model})
}

func (p *werkProcess) SetEffort(effort string) error {
	if effort == "" {
		return ErrUnsupported
	}
	p.effort = effort
	return p.writeLine(werk.Command{Type: werk.CmdSet, Effort: &effort})
}

// werkStatusTimeout bounds the wait for the process's answer to a status ask.
var werkStatusTimeout = 10 * time.Second

// status asks the process whether a turn runs and what its last turn ended
// with.
func (p *werkProcess) status() (werk.Line, error) {
	id := "wsc/status/" + strconv.FormatUint(p.seq.Add(1), 10)
	body, err := p.requestBodyID(id, "status", map[string]any{"type": werk.CmdStatus}, werkStatusTimeout)
	if err != nil {
		return werk.Line{}, err
	}
	l, err := werk.ParseLine(body)
	if err != nil {
		return werk.Line{}, fmt.Errorf("the status answer is unreadable: %w", err)
	}
	return l, nil
}

// TurnRunning asks the process, which knows, after a rejoin whose replay lost
// records.
func (p *werkProcess) TurnRunning() (bool, error) {
	l, err := p.status()
	if err != nil {
		return false, err
	}
	p.askedMu.Lock()
	p.asked = &l
	p.askedMu.Unlock()
	return l.Running, nil
}

// recoveryReply is what the last turn ended with, which a rejoin that lost
// the turn's records reports in their place.
func (p *werkProcess) recoveryReply(int64) (message, turnErr string, taken []string, err error) {
	p.askedMu.Lock()
	asked := p.asked
	p.asked = nil
	p.askedMu.Unlock()
	var l werk.Line
	if asked != nil {
		l = *asked
	} else if l, err = p.status(); err != nil {
		return "", "", nil, err
	}
	if l.Running {
		return "", "", nil, errors.New("the process reports a turn running")
	}
	if l.Reason == werk.ReasonError {
		turnErr = l.Error
	}
	return l.Text, turnErr, l.Taken, nil
}
