package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type fakeTokenCall struct {
	path, auth string
	body       map[string]any
}

func setWorkerForwardEndpoint(t *testing.T, s *Server, endpoint string) {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(endpoint, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.dashboard.stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(wsdashboard.Endpoint{SchemaVersion: 1, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "endpoint.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerForwardRediscoversEndpointOnEachCall(t *testing.T) {
	setupMailboxTestEnv(t)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"endpoint":"first"}`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			http.Error(w, "wrong capability", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"endpoint":"second"}`)
	}))
	defer second.Close()
	t.Setenv(wsagent.EnvPiToken, "tok123")
	t.Setenv(wsagent.EnvPiEndpoint, first.URL)
	s := NewServer(t.TempDir(), "test")
	defer s.Close()
	call := func(want string) {
		t.Helper()
		var answer struct{ Endpoint string }
		if err := s.workerCall(context.Background(), "agent.list", map[string]any{}, time.Second, &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Endpoint != want {
			t.Fatalf("forwarded to %q, want %q", answer.Endpoint, want)
		}
	}
	call("first") // No discovery file: retain the inherited endpoint fallback.
	setWorkerForwardEndpoint(t, s, second.URL)
	call("second")
	setWorkerForwardEndpoint(t, s, first.URL)
	call("first")
}

type routeBackend struct {
	mu     sync.Mutex
	starts []wsagent.StartSpec
	procs  []*routeProc
}

func (b *routeBackend) Start(_ context.Context, spec wsagent.StartSpec) (wsagent.Process, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := &routeProc{events: make(chan wsagent.Event, 64), sent: make(chan string, 16)}
	b.starts = append(b.starts, spec)
	b.procs = append(b.procs, p)
	return p, nil
}

// waitStart waits for the i-th process start.
func (b *routeBackend) waitStart(t *testing.T, i int) (wsagent.StartSpec, *routeProc) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		if len(b.starts) > i {
			spec, p := b.starts[i], b.procs[i]
			b.mu.Unlock()
			return spec, p
		}
		b.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process %d did not start", i)
	return wsagent.StartSpec{}, nil
}

type routeProc struct {
	events chan wsagent.Event
	sent   chan string
	once   sync.Once
}

func (p *routeProc) Events() <-chan wsagent.Event { return p.events }
func (p *routeProc) Send(text string) error       { p.sent <- text; return nil }
func (p *routeProc) Interrupt() error             { return nil }
func (p *routeProc) SetModel(string) error        { return wsagent.ErrUnsupported }
func (p *routeProc) SetEffort(string) error       { return wsagent.ErrUnsupported }
func (p *routeProc) Close() error {
	p.once.Do(func() { close(p.events) })
	return nil
}

// reply sends a final reply and ends the turn.
func (p *routeProc) reply(text string) {
	p.events <- wsagent.Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: text, Final: true}}
	p.events <- wsagent.Event{TurnEnd: true}
}

func (p *routeProc) nextSent(t *testing.T) string {
	t.Helper()
	select {
	case s := <-p.sent:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("no turn was sent")
		return ""
	}
}

type routeKeys struct {
	mu sync.Mutex
	n  int
}

func (k *routeKeys) Mint(string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.n++
	return fmt.Sprintf("route-test-key-%d", k.n), nil
}

func (k *routeKeys) Ensure(string, string) error { return nil }

// routeLog records the token route's answers: each call's op, status, and
// body, and announces each call as it arrives.
