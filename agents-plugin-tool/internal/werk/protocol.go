// Package werk runs the builtin Go agent harness inside the ws daemon.
// It sends Responses requests to the provider, calls tools through the
// session's authenticated MCP connection, and journals conversation items
// in an append-only log. The daemon feeds it typed commands and maps its
// output events to the common runner and audit contracts.
package werk

import (
	"encoding/json"

	"github.com/kang-sw/devenv/internal/wsfs"
)

// ProtocolVersion is the harness version the process reports in its init
// line. It versions this line protocol and the loop's behavior, not the ws
// release, so a ws upgrade never moves a recorded catalog range.
const ProtocolVersion = "1.0.0"

// Authority is the filesystem permission assigned to a session.
type Authority = wsfs.Authority

const (
	AuthorityReadOnly     = wsfs.AuthorityReadOnly
	AuthorityWorkdirWrite = wsfs.AuthorityWorkdirWrite
	AuthorityFull         = wsfs.AuthorityFull
)

// Command types, daemon to process.
const (
	// CmdUser starts a turn with Text.
	CmdUser = "user"
	// CmdSteer carries Text into the running turn at its next model request;
	// the process answers it with a LineSteerAck carrying ID, and reports the
	// request that took it with a LineUserTaken.
	CmdSteer = "steer"
	// CmdClearSteered drops the steered messages no request took.
	CmdClearSteered = "clear_steered"
	// CmdInterrupt stops the running turn; the turn still ends with a
	// LineTurnEnd.
	CmdInterrupt = "interrupt"
	// CmdStatus asks whether a turn runs and what the last one ended with; the
	// process answers with a LineStatus carrying ID.
	CmdStatus = "status"
	// CmdSet changes Model and, when Effort is present, the reasoning effort
	// for the next request.
	CmdSet = "set"
)

// Steer is an admitted mid-turn message. ID is stable across daemon openings.
type Steer struct {
	ID   string
	Text string
}

// Command is a request from the daemon to the session loop.
type Command struct {
	Type   string  `json:"type"`
	TurnID string  `json:"-"`
	ID     string  `json:"id,omitempty"`
	Text   string  `json:"text,omitempty"`
	Model  string  `json:"model,omitempty"`
	Effort *string `json:"effort,omitempty"`
	Steers []Steer `json:"-"`
}

// Line types, process to daemon.
const (
	LineInit      = "init"
	LineUser      = "user"
	LineTurnStart = "turn_start"
	LineReply     = "reply"
	LineReasoning = "reasoning"
	LineToolStart = "tool_start"
	LineToolEnd   = "tool_end"
	LineUsage     = "usage"
	LineRetry     = "retry"
	LineTurnEnd   = "turn_end"
	LineSteerAck  = "steer_ack"
	LineUserTaken = "user_taken"
	LineNotice    = "notice"
	LineStatus    = "status"
)

// Turn end reasons.
const (
	ReasonDone    = "done"
	ReasonAborted = "aborted"
	ReasonError   = "error"
)

// Line is one line of the process's standard output. Which fields a line
// carries follows its type; T is always set. Free text is bounded by the
// process (ToolOutputBytes for a tool result).
type Line struct {
	T string `json:"t"`
	// At is the line's wall time, epoch milliseconds.
	At int64 `json:"at,omitempty"`

	// init
	Session string `json:"session,omitempty"`
	Version string `json:"version,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	// Resumed counts the conversation items an init recovered from the
	// session's persisted deltas.
	Resumed int `json:"resumed,omitempty"`

	// Turn is the turn counter (1-based) a turn-scoped line belongs to.
	Turn   int    `json:"turn,omitempty"`
	TurnID string `json:"-"`
	// Response is the provider's id of the model response a usage, reasoning,
	// reply or tool_start line comes from: the model request of the audit
	// chain.
	Response string `json:"response,omitempty"`

	// reply, reasoning, user, user_taken, notice, turn_end
	Item  string `json:"item,omitempty"`
	Text  string `json:"text,omitempty"`
	Final bool   `json:"final,omitempty"`
	// Encrypted marks a reasoning item that carries opaque content with no
	// readable summary.
	Encrypted bool `json:"encrypted,omitempty"`

	// tool_start, tool_end
	Call string `json:"call,omitempty"`
	Name string `json:"name,omitempty"`
	// Args is the call's argument JSON as the model wrote it.
	Args string `json:"args,omitempty"`
	// Output is a tool result, bounded; Truncated says it was cut.
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Failed    bool   `json:"failed,omitempty"`

	// usage: one model request's figures. CostUSD is the provider's own
	// figure when the response carried one.
	InputTokens     int64    `json:"input_tokens,omitempty"`
	OutputTokens    int64    `json:"output_tokens,omitempty"`
	CachedTokens    int64    `json:"cached_tokens,omitempty"`
	ReasoningTokens int64    `json:"reasoning_tokens,omitempty"`
	CostUSD         *float64 `json:"cost_usd,omitempty"`
	// Context is the request's input size in tokens, the context window's
	// occupancy; Window is the model's window when the process knows it.
	Context int64 `json:"context,omitempty"`
	Window  int64 `json:"window,omitempty"`

	// retry
	Attempt    int   `json:"attempt,omitempty"`
	MaxRetries int   `json:"max_retries,omitempty"`
	DelayMS    int64 `json:"delay_ms,omitempty"`
	Status     int   `json:"status,omitempty"`

	// turn_end
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`

	// steer_ack, status
	ID      string `json:"id,omitempty"`
	Refusal string `json:"refusal,omitempty"`

	// status: Running is whether a turn runs; Text, Reason and Error describe
	// the last turn's final reply and end; Taken lists the steered messages
	// that turn took.
	Running bool     `json:"running,omitempty"`
	Taken   []string `json:"taken,omitempty"`
}

// ParseLine decodes one output line.
func ParseLine(raw []byte) (Line, error) {
	var l Line
	err := json.Unmarshal(raw, &l)
	return l, err
}
