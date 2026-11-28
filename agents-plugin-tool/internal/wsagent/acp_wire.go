package wsagent

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// acp_wire.go holds the Agent Client Protocol (v1: JSON-RPC 2.0 over stdio,
// one message per line) wire shapes the shared ACP core reads and writes.
// Every request ws writes carries an id that names its kind (acpIDKind), so
// a later daemon replaying a holder's ring tells a prompt's response from a
// control's.

// acpProtocol writes ACP requests. A user turn needs the session id, so
// acpProcess.Send writes it; userLine is never used.
type acpProtocol struct{}

func (acpProtocol) userLine(string) any { return nil }

func (acpProtocol) requestLine(id string, request map[string]any) any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": request["method"], "params": request["params"]}
}

// Request kinds, the second field of a request id ws wrote.
const (
	acpKindInit    = "init"
	acpKindNew     = "new"
	acpKindResume  = "resume"
	acpKindClose   = "close"
	acpKindConfig  = "cfg"
	acpKindMode    = "mode"
	acpKindPrompt  = "p"
	acpRequestMark = "ws"
)

// acpIDKind is the kind of a request id ws wrote ("ws-<kind>-<epoch>-<n>"),
// false for any other id.
func acpIDKind(id string) (string, bool) {
	parts := strings.Split(id, "-")
	if len(parts) != 4 || parts[0] != acpRequestMark {
		return "", false
	}
	return parts[1], true
}

// acpConfigValue is option's currentValue in a configOptions answer.
func acpConfigValue(body json.RawMessage, option string) (string, bool) {
	var r struct {
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(body, &r) != nil {
		return "", false
	}
	for _, o := range r.ConfigOptions {
		if o.ID == option {
			return o.CurrentValue, true
		}
	}
	return "", false
}

type acpConfigOption struct {
	ID           string `json:"id"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
	} `json:"options"`
}

// acpApproval is a pending permission request: the raw id its answer
// echoes and the options it offered.
type acpApproval struct {
	raw        json.RawMessage
	toolCallID string
	options    []acpOption
}

type acpOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}

type acpMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error"`
}

func compactRaw(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return slices.Clone(raw)
	}
	return b.Bytes()
}

type acpSessionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	// Content is a message chunk's content block, but a tool call
	// update's content list: decoding it as either would fail the other.
	Content           json.RawMessage   `json:"content"`
	ToolCallID        string            `json:"toolCallId"`
	Name              string            `json:"name"`
	Title             string            `json:"title"`
	Status            string            `json:"status"`
	Args              json.RawMessage   `json:"args"`
	RawInput          json.RawMessage   `json:"rawInput"`
	RawOutput         json.RawMessage   `json:"rawOutput"`
	Used              *int64            `json:"used"`
	Size              int64             `json:"size"`
	CurrentModeID     string            `json:"currentModeId"`
	AvailableCommands []json.RawMessage `json:"availableCommands"`
}

// params2configOptions lifts a config_option_update's configOptions to the
// shape of a set_config_option answer.
func params2configOptions(params json.RawMessage) json.RawMessage {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	return p.Update
}

// acpAuditBody is a tool call's input or output as compact JSON: raw, else
// fallback when raw is absent.
func acpAuditBody(raw, fallback json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		raw = fallback
	}
	if string(raw) == "null" {
		return ""
	}
	return compactJSON(raw)
}
