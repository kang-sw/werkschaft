package werk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mcpclient.go is the harness's ws tool client: MCP Streamable HTTP with JSON
// responses against the daemon's per-session route (wsdashboard
// proxy_http.go), authenticated by the session bearer. Each POST carries one
// JSON-RPC message and a request is answered in the response body. The daemon
// serves a session from the token alone and replays an initialize for a
// client that initialized against an earlier daemon, so a call after a daemon
// replacement needs no new handshake.

// mcpProtocolVersion is the protocol version the client asks for.
const mcpProtocolVersion = "2025-06-18"

// MCPTool is one ws tool as the daemon lists it.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCP is a ws tool client. It is safe for concurrent calls.
type MCP struct {
	URL   string
	Token string
	HTTP  *http.Client
	// Version is the clientInfo version of the handshake.
	Version string

	seq    atomic.Int64
	initMu sync.Mutex
	inited bool
	// ConnectWait bounds how long a call that cannot reach the daemon keeps
	// trying: a daemon replacement leaves the route down for a few seconds.
	ConnectWait time.Duration
}

// DefaultConnectWait is how long a call waits for an unreachable route.
const DefaultConnectWait = 30 * time.Second

// maxMCPBody bounds a route response.
const maxMCPBody = 64 << 20

// String keeps the bearer out of any printed client.
func (m *MCP) String() string   { return "werk.MCP(" + m.URL + ")" }
func (m *MCP) GoString() string { return m.String() }

func (m *MCP) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return http.DefaultClient
}

// rpcError is a JSON-RPC error answer.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("ws-mcp: %s (%d)", e.Message, e.Code) }

// ErrNotDelivered marks a request that never reached the daemon (the
// connection failed before the request was written), so it ran nothing and
// may be sent again.
var ErrNotDelivered = errors.New("the request did not reach the daemon")

// post sends one message. A request (withID) returns the result; a
// notification returns nil.
func (m *MCP) post(ctx context.Context, method string, params any, withID bool) (json.RawMessage, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	var id int64
	if withID {
		id = m.seq.Add(1)
		msg["id"] = id
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+m.Token)
	resp, err := m.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if notDelivered(err) {
			return nil, fmt.Errorf("%w: %v", ErrNotDelivered, urlFree(err))
		}
		return nil, fmt.Errorf("the connection to the daemon failed: %v", urlFree(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPBody))
	if err != nil {
		return nil, fmt.Errorf("reading the daemon's answer: %v", urlFree(err))
	}
	if resp.StatusCode == http.StatusAccepted && !withID {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("the daemon refused the session token (%d)", resp.StatusCode)
	}
	if !withID {
		return nil, nil
	}
	var answer struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &answer); err != nil {
		return nil, fmt.Errorf("the daemon answered %d with no JSON-RPC answer", resp.StatusCode)
	}
	if answer.Error != nil {
		return nil, answer.Error
	}
	return answer.Result, nil
}

// notDelivered reports a failure before any request byte was written: a
// refused or unreachable connection.
func notDelivered(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// urlFree is err's text without the request URL a transport error repeats.
func urlFree(err error) string {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if inner := ue.Unwrap(); inner != nil {
			return inner.Error()
		}
	}
	return err.Error()
}

// retryConnect runs f, trying again while the daemon is unreachable.
func (m *MCP) retryConnect(ctx context.Context, f func() (json.RawMessage, error)) (json.RawMessage, error) {
	wait := m.ConnectWait
	if wait <= 0 {
		wait = DefaultConnectWait
	}
	deadline := time.Now().Add(wait)
	delay := 250 * time.Millisecond
	for {
		out, err := f()
		if !errors.Is(err, ErrNotDelivered) || time.Now().After(deadline) {
			return out, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// Initialize opens the session: initialize, then the initialized
// notification. It is idempotent, and a failed attempt is tried again by the
// next call.
func (m *MCP) Initialize(ctx context.Context) error {
	m.initMu.Lock()
	defer m.initMu.Unlock()
	if m.inited {
		return nil
	}
	if _, err := m.retryConnect(ctx, func() (json.RawMessage, error) {
		return m.post(ctx, "initialize", map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "ws-werk", "version": m.Version},
		}, true)
	}); err != nil {
		return err
	}
	if _, err := m.post(ctx, "notifications/initialized", nil, false); err != nil {
		return err
	}
	m.inited = true
	return nil
}

// ListTools lists the ws tools the session may call.
func (m *MCP) ListTools(ctx context.Context) ([]MCPTool, error) {
	if err := m.Initialize(ctx); err != nil {
		return nil, err
	}
	var out []MCPTool
	cursor := ""
	for range 20 {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := m.retryConnect(ctx, func() (json.RawMessage, error) { return m.post(ctx, "tools/list", params, true) })
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []MCPTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("tools/list answered an unreadable list")
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// CallResult is a tools/call answer.
type CallResult struct {
	Text    string
	IsError bool
}

// ErrDeliveryUnknown marks a call whose connection failed after the request
// was sent: the tool may or may not have run, so the caller must not send it
// again.
var ErrDeliveryUnknown = errors.New("the connection failed while the call ran")

// Call runs a ws tool. A call that never reached the daemon is retried while
// the daemon is down; one whose delivery is unknown is not, and answers
// ErrDeliveryUnknown.
func (m *MCP) Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if err := m.Initialize(ctx); err != nil {
		return CallResult{}, err
	}
	var arguments any = map[string]any{}
	if len(bytes.TrimSpace(args)) > 0 {
		arguments = json.RawMessage(args)
	}
	raw, err := m.retryConnect(ctx, func() (json.RawMessage, error) {
		return m.post(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, true)
	})
	if err != nil {
		var rpc *rpcError
		switch {
		case errors.As(err, &rpc), errors.Is(err, ErrNotDelivered), ctx.Err() != nil:
			return CallResult{}, err
		}
		return CallResult{}, fmt.Errorf("%w: %v", ErrDeliveryUnknown, err)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, fmt.Errorf("tools/call answered an unreadable result")
	}
	var parts []string
	for _, c := range res.Content {
		if c.Type == "text" || c.Type == "" {
			parts = append(parts, c.Text)
		}
	}
	return CallResult{Text: strings.Join(parts, "\n"), IsError: res.IsError}, nil
}
