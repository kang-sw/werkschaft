package wsaudit

import "time"

// SessionClass identifies how a session is hosted for audit capture gating.
type SessionClass string

const (
	// SessionHostedLead is a lead session hosted by the daemon's agent runner.
	SessionHostedLead SessionClass = "hosted_lead"
	// SessionHostedChild is a ws/agent.* child session hosted by the daemon's
	// agent runner. Its audit events land under its own session ID.
	SessionHostedChild SessionClass = "hosted_child"
	// SessionCLI is a non-hosted CLI-tier session (for example a standalone
	// Claude CLI plugin or Pi extension session).
	SessionCLI SessionClass = "cli"
)

// IsDaemonHosted reports whether c is a daemon-hosted session class.
func (c SessionClass) IsDaemonHosted() bool {
	return c == SessionHostedLead || c == SessionHostedChild
}

// Session describes the session a candidate audit event belongs to. Capture
// keys solely on whether the session is daemon-hosted, with no per-session or
// per-workspace toggle.
type Session struct {
	ID    string
	Class SessionClass
}

// DaemonHosted reports whether s is audited (a daemon-hosted lead or
// ws/agent.* child).
func (s Session) DaemonHosted() bool {
	return s.Class.IsDaemonHosted()
}

// ShouldCapture reports whether an audit event of kind is captured for session.
//
// Every daemon-hosted session (leads and ws/agent.* children) captures tool
// calls, hidden harness events, and unrecognized-line other events. Ordinary
// visible conversation kinds (user and assistant_text) and invalid kinds are
// always refused, and non-hosted sessions are refused for every kind.
func ShouldCapture(session Session, kind Kind) bool {
	if !session.DaemonHosted() {
		return false
	}
	return kind.InCaptureScope()
}

// FilterEvents returns a new slice containing only the normalized events in
// events that ShouldCapture admits for session, preserving order. It does not
// mutate the input slice.
func FilterEvents(session Session, events []Envelope) []Envelope {
	if !session.DaemonHosted() || len(events) == 0 {
		return nil
	}
	var out []Envelope
	for _, ev := range events {
		if ShouldCapture(session, ev.Kind) {
			out = append(out, ev)
		}
	}
	return out
}

// CaptureLine applies the capture policy to one decoded harness stream line.
//
// For non-hosted sessions it always returns nil. For daemon-hosted sessions:
//   - If recognized is false, the line becomes one KindOther envelope carrying
//     rawLine verbatim in Raw.
//   - If recognized is true, mapped is filtered through ShouldCapture so
//     in-scope events (for example, tool calls or tool results mixed into a
//     conversation line) are kept and out-of-scope ordinary text is dropped. A
//     recognized line whose events are all out of scope yields nil, never
//     KindOther.
func CaptureLine(session Session, harness string, at time.Time, rawLine string, recognized bool, mapped []Envelope) []Envelope {
	if !session.DaemonHosted() {
		return nil
	}
	if !recognized {
		return []Envelope{NewOtherEnvelope(session.ID, harness, at, rawLine)}
	}
	return FilterEvents(session, mapped)
}
