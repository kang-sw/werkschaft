package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type mainFakeBackend struct {
	mu    sync.Mutex
	specs []wsagent.StartSpec
	sends []string
}

func (b *mainFakeBackend) Start(_ context.Context, spec wsagent.StartSpec) (wsagent.Process, error) {
	b.mu.Lock()
	b.specs = append(b.specs, spec)
	b.mu.Unlock()
	return &mainFakeProc{backend: b, events: make(chan wsagent.Event, 8)}, nil
}

type mainFakeProc struct {
	backend   *mainFakeBackend
	events    chan wsagent.Event
	closeOnce sync.Once
}

func (p *mainFakeProc) Events() <-chan wsagent.Event { return p.events }
func (p *mainFakeProc) Send(text string) error {
	p.backend.mu.Lock()
	p.backend.sends = append(p.backend.sends, text)
	p.backend.mu.Unlock()
	go func() {
		p.events <- wsagent.Event{
			Live: &wslive.Event{Kind: wslive.KindReply, Text: `{"quip":"ok"}`, Final: true},
		}
		p.events <- wsagent.Event{TurnEnd: true}
	}()
	return nil
}
func (p *mainFakeProc) Interrupt() error       { return nil }
func (p *mainFakeProc) SetModel(string) error  { return nil }
func (p *mainFakeProc) SetEffort(string) error { return nil }
func (p *mainFakeProc) Close() error {
	p.closeOnce.Do(func() { close(p.events) })
	return nil
}

func TestCompanionPoolAdapterBridgesModesAndLifecycle(t *testing.T) {
	if (companionPoolAdapter{}).SupportsHarness(wsagent.BackendClaude) {
		t.Fatal("nil pool should not report harness support")
	}

	fb := &mainFakeBackend{}
	pool := wsagent.NewEphemeralPool(wsagent.EphemeralPoolOptions{
		Backends:      map[string]wsagent.Backend{wsagent.BackendClaude: fb},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	adapter := companionPoolAdapter{pool: pool}
	if !adapter.SupportsHarness(wsagent.BackendClaude) {
		t.Fatal("adapter should report registered claude backend")
	}
	if adapter.SupportsHarness("nonexistent-harness") {
		t.Fatal("adapter should reject unknown harness")
	}

	req := wsdashboard.CompanionEphemeralRequest{
		Harness:      wsagent.BackendClaude,
		Model:        "haiku",
		Effort:       "low",
		Mode:         wsdashboard.CompanionModeBoundedRotation,
		MaxTurns:     20,
		IdleTTL:      5 * time.Minute,
		SystemPrompt: "companion",
		Timeout:      5 * time.Second,
		Prompt:       "tick-1",
	}
	for i := range 3 {
		req.Prompt = "tick"
		reply, err := adapter.Execute(context.Background(), req)
		if err != nil || reply != `{"quip":"ok"}` {
			t.Fatalf("call %d = (%q, %v)", i, reply, err)
		}
	}
	fb.mu.Lock()
	starts := len(fb.specs)
	sends := len(fb.sends)
	var firstSpec wsagent.StartSpec
	if starts > 0 {
		firstSpec = fb.specs[0]
	}
	fb.mu.Unlock()
	if starts != 1 || sends != 3 {
		t.Fatalf("BoundedRotation starts=%d sends=%d, want 1 and 3", starts, sends)
	}
	if !firstSpec.Ephemeral || firstSpec.Model != "haiku" || firstSpec.Effort != "low" || firstSpec.SystemPrompt != "companion" {
		t.Fatalf("unexpected StartSpec: %+v", firstSpec)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("pool.Close(): %v", err)
	}
	if _, err := adapter.Execute(context.Background(), req); err == nil {
		t.Fatal("expected error after pool.Close()")
	}
}
