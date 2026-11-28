package wsagent

import (
	"encoding/json"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsaudit"
)

// werkAudit maps the builtin harness's output lines (werk.Line) to audit
// envelopes through the capture tap (audit_capture.go), so a werk worker is
// captured from its first release like every other hosted session.
//
// Each line is one record and yields its own events; the adapter carries only
// the model request chain (Decision 7 of the audit capture ticket):
//
//	init, turn_start, turn_end, retry, steer_ack,
//	user_taken, notice, status      -> system, subkind the line type
//	user                            -> user (reminder text is a system_reminder)
//	reply (final)                   -> assistant_text
//	reasoning                       -> thinking (an opaque item: signature_only)
//	tool_start                      -> tool_call (subkind the model's tool name,
//	                                   call_id the provider's call id, in the
//	                                   arguments as the model wrote them)
//	tool_end                        -> tool_result (out the bounded result the
//	                                   model saw, meta.failed / meta.truncated)
//	usage                           -> system (the figures and the cost)
//
// A streamed, non-final reply is recognized and dropped: the final line
// carries the whole body. An event of a response (a usage, reasoning, reply
// or tool_start line names its response id) carries meta.request, the chain
// of the session's model requests with the context the response reported.
// Committed checkpoints are persisted separately from droppable lines.
type werkAudit struct {
	session string
	chain   wsaudit.RequestChain
}

func newWerkAudit(session string) *werkAudit { return &werkAudit{session: session} }

var _ auditSource = (*werkMapper)(nil)

func (*werkMapper) newAudit(sessionID string) (string, auditAdapter) {
	return BackendWerk, newWerkAudit(sessionID)
}

func (a *werkAudit) env(kind wsaudit.Kind, at time.Time) wsaudit.Envelope {
	ev, _ := wsaudit.NewEnvelope(a.session, BackendWerk, at, kind)
	return ev
}

func (a *werkAudit) checkpoint(l werk.Line) wsaudit.Envelope {
	ev := a.env(wsaudit.KindCompaction, time.UnixMilli(l.At))
	ev.Out = l.Text
	setMeta(&ev, "checkpoint_id", l.ID)
	return ev
}

func (a *werkAudit) mapLine(raw []byte, at time.Time) (bool, []auditEvent, []auditEvent) {
	l, err := werk.ParseLine(raw)
	if err != nil || l.T == "" {
		return false, nil, nil
	}
	if l.At > 0 {
		at = time.UnixMilli(l.At)
	}
	request := a.request(l)
	switch l.T {
	case werk.LineCompactFailed:
		return true, nil, nil
	case werk.LineUser:
		ev := a.env(wsaudit.KindUser, at)
		ev.Out = l.Text
		if isReminder(l.Text) {
			ev.Kind = wsaudit.KindSystemReminder
		}
		return true, nil, slotted([]wsaudit.Envelope{ev})
	case werk.LineReply:
		if !l.Final {
			return true, nil, nil
		}
		ev := a.env(wsaudit.KindAssistantText, at)
		ev.Out = l.Text
		withRequest(&ev, request)
		return true, nil, slotted([]wsaudit.Envelope{ev})
	case werk.LineReasoning:
		ev := a.env(wsaudit.KindThinking, at)
		ev.Out = l.Text
		if l.Encrypted || l.Text == "" {
			setMeta(&ev, wsaudit.MetaSignatureOnly, true)
		}
		withRequest(&ev, request)
		return true, nil, slotted([]wsaudit.Envelope{ev})
	case werk.LineToolStart:
		ev := a.env(wsaudit.KindToolCall, at)
		ev.Subkind, ev.CallID, ev.In = l.Name, l.Call, l.Args
		if l.Truncated {
			setMeta(&ev, "truncated", true)
		}
		withRequest(&ev, request)
		return true, nil, slotted([]wsaudit.Envelope{ev})
	case werk.LineToolEnd:
		ev := a.env(wsaudit.KindToolResult, at)
		ev.Subkind, ev.CallID, ev.Out = l.Name, l.Call, l.Output
		if l.Failed {
			setMeta(&ev, "failed", true)
		}
		if l.Truncated {
			setMeta(&ev, "truncated", true)
		}
		return true, nil, slotted([]wsaudit.Envelope{ev})
	case werk.LineInit, werk.LineTurnStart, werk.LineTurnEnd, werk.LineRetry, werk.LineSteerAck,
		werk.LineUserTaken, werk.LineNotice, werk.LineStatus, werk.LineUsage:
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind = l.T
		ev.Out = compactJSON(scrubbedLine(l, raw))
		withRequest(&ev, request)
		return true, nil, slotted([]wsaudit.Envelope{ev})
	}
	return false, nil, nil
}

// request advances the chain for a line that names its response and returns
// the chain's current meta; nil for a line of no response or an out-of-band
// compaction summary call.
func (a *werkAudit) request(l werk.Line) map[string]any {
	if l.Response == "" || l.Compaction {
		return nil
	}
	if l.T == werk.LineUsage {
		a.chain.See(l.Response, l.InputTokens, true)
	} else {
		a.chain.See(l.Response, 0, false)
	}
	return a.chain.Meta()
}

func (a *werkAudit) markLoss(uint64, string) bool { return false }

func (a *werkAudit) flush(time.Time) []auditEvent { return nil }

// scrubbedLine is a system line as recorded: the free text a provider error
// or a notice can carry (an error body echoing a key or a bearer credential)
// is masked; a line with nothing to mask is recorded as it arrived.
func scrubbedLine(l werk.Line, raw []byte) []byte {
	errText, text := werk.Scrub(l.Error), werk.Scrub(l.Text)
	if errText == l.Error && text == l.Text {
		return raw
	}
	l.Error, l.Text = errText, text
	if masked, err := json.Marshal(l); err == nil {
		return masked
	}
	return raw
}
