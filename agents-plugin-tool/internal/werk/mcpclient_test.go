package werk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcpframe"
)

type rpcServer struct {
	t       *testing.T
	methods []string
	auth    string
	onCall  func(w http.ResponseWriter, params json.RawMessage)
}

func (s *rpcServer) handler(w http.ResponseWriter, r *http.Request) {
	s.auth = r.Header.Get("Authorization")
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	s.methods = append(s.methods, req.Method)
	write := func(result string) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"result":`+result+`}`)
	}
	switch req.Method {
	case "initialize":
		write(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"ws-mcp"}}`)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		write(`{"tools":[{"name":"exec.shell","description":"run","inputSchema":{"type":"object"}}]}`)
	case "tools/call":
		if s.onCall != nil {
			s.onCall(w, req.Params)
			return
		}
		write(`{"content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}],"isError":false}`)
	}
}

func TestMCPHandshakeListAndCall(t *testing.T) {
	s := &rpcServer{t: t}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	defer srv.Close()
	m := &MCP{URL: srv.URL, Token: "bearer-secret", Version: "1.0.0"}
	tools, err := m.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "exec.shell" {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	res, err := m.Call(context.Background(), "exec.shell", json.RawMessage(`{"cmd":"ls"}`))
	if err != nil || res.Text != "line1\nline2" || res.IsError {
		t.Fatalf("call = %+v, %v", res, err)
	}
	if got := strings.Join(s.methods, ","); got != "initialize,notifications/initialized,tools/list,tools/call" {
		t.Errorf("methods = %s", got)
	}
	if s.auth != "Bearer bearer-secret" {
		t.Errorf("authorization = %q", s.auth)
	}
	if strings.Contains(m.String(), "bearer-secret") {
		t.Error("the printed client shows its bearer")
	}
}

func TestMCPCallToolErrorAndRPCError(t *testing.T) {
	s := &rpcServer{t: t}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	defer srv.Close()
	m := &MCP{URL: srv.URL, Token: "t"}
	s.onCall = func(w http.ResponseWriter, _ json.RawMessage) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"refused"}],"isError":true}}`)
	}
	res, err := m.Call(context.Background(), "exec.shell", nil)
	if err != nil || !res.IsError || res.Text != "refused" {
		t.Fatalf("tool error = %+v, %v", res, err)
	}
	s.onCall = func(w http.ResponseWriter, _ json.RawMessage) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":3,"error":{"code":-32601,"message":"no such tool"}}`)
	}
	if _, err := m.Call(context.Background(), "nope", nil); err == nil || !strings.Contains(err.Error(), "no such tool") || errors.Is(err, ErrDeliveryUnknown) {
		t.Fatalf("rpc error = %v", err)
	}
}

func TestMCPRefusedTokenSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	m := &MCP{URL: srv.URL, Token: "t", ConnectWait: time.Millisecond}
	_, err := m.ListTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the session token") {
		t.Fatalf("err = %v", err)
	}
}

// A call made while the daemon is replaced waits for it and runs once it is
// back, with no second handshake: the route serves a session from the token.
func TestMCPCallWaitsOutAnUnreachableDaemon(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	m := &MCP{URL: "http://" + addr, Token: "t", ConnectWait: 5 * time.Second}
	m.inited = true

	var calls atomic.Int32
	go func() {
		time.Sleep(400 * time.Millisecond)
		l2, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"back"}]}}`)
		})}
		go srv.Serve(l2)
		time.AfterFunc(3*time.Second, func() { srv.Close() })
	}()
	res, err := m.Call(context.Background(), "exec.shell", nil)
	if err != nil || res.Text != "back" || calls.Load() != 1 {
		t.Fatalf("call = %+v, %v, server calls %d", res, err, calls.Load())
	}
}

// A connection that drops after the request was sent leaves delivery
// unknown: the client does not send the call again.
func TestMCPDroppedConnectionIsNotRetried(t *testing.T) {
	var seen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		hj := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()
	m := &MCP{URL: srv.URL, Token: "t"}
	m.inited = true
	_, err := m.Call(context.Background(), "exec.shell", nil)
	if !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatalf("err = %v", err)
	}
	if seen.Load() != 1 {
		t.Errorf("the dropped call was sent %d times", seen.Load())
	}
}

func TestMCPCorrelatedCallDoesNotChangeArguments(t *testing.T) {
	captured := make(chan json.RawMessage, 1)
	server := &rpcServer{onCall: func(w http.ResponseWriter, params json.RawMessage) {
		captured <- params
		io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
	}}
	endpoint := httptest.NewServer(http.HandlerFunc(server.handler))
	defer endpoint.Close()
	client := &MCP{URL: endpoint.URL, Token: "fixture"}
	if _, err := client.CallCorrelated(context.Background(), "fs.write", json.RawMessage(`{"path":"test","content":"data"}`), "call_fixture"); err != nil {
		t.Fatal(err)
	}
	var args struct {
		Arguments map[string]string `json:"arguments"`
		Meta      map[string]string `json:"_meta"`
	}
	if err := json.Unmarshal(<-captured, &args); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args.Arguments, map[string]string{"path": "test", "content": "data"}) || args.Meta[mcpframe.ToolCallIDMeta] != "call_fixture" {
		t.Fatalf("call params = %+v", args)
	}
}

func TestMCPDoesNotFollowToolCallRedirect(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	server := &rpcServer{onCall: func(w http.ResponseWriter, _ json.RawMessage) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}}
	endpoint := httptest.NewServer(http.HandlerFunc(server.handler))
	defer endpoint.Close()
	client := &MCP{URL: endpoint.URL, Token: "fixture"}
	if _, err := client.Call(context.Background(), "fs.write", nil); !errors.Is(err, ErrDeliveryUnknown) || redirected.Load() {
		t.Fatalf("call error = %v, followed redirect = %v", err, redirected.Load())
	}
}
