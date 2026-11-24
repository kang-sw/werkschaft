package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

func TestWerkLeadSteersAndInterrupts(t *testing.T) {
	started, release, third := make(chan struct{}), make(chan struct{}), make(chan struct{})
	next := make(chan werk.Request, 1)
	var reads atomic.Int32
	continuation := func(ctx context.Context, req werk.Request) (*werk.Response, error) {
		raw, err := json.Marshal(req.Input)
		if err != nil {
			return nil, err
		}
		switch {
		case bytes.Contains(raw, []byte("continue the fixture")):
			return werkMessage("continued")(ctx, req)
		case bytes.Contains(raw, []byte("interrupt this")):
			close(third)
			<-ctx.Done()
			return nil, ctx.Err()
		case bytes.Contains(raw, []byte("steer this input")):
			next <- req
			resp, err := werkMessage("steer received")(ctx, req)
			resp.Output = append([]json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"reason","encrypted_content":"opaque","summary":[{"text":"Provider summary"}]}`)}, resp.Output...)
			return resp, err
		default:
			// The durable record is committed before the steer is handed to the
			// loop. Keep the turn at tool boundaries while that handoff completes.
			id := fmt.Sprintf("read-%d", reads.Add(1))
			return &werk.Response{Output: []json.RawMessage{json.RawMessage(werkCall(id, "fs_read", `{"path":"fixture"}`))}}, nil
		}
	}
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
			}
			return &werk.Response{Output: []json.RawMessage{json.RawMessage(werkCall("read", "fs_read", `{"path":"fixture"}`))}}, nil
		},
	}}
	for range 16 {
		model.steps = append(model.steps, continuation)
	}
	h, ws, _ := werkHarness(t, model)
	// Drain MCP tool observations so the HTTP handler can respond while the
	// model keeps calling tools until the steer arrives.
	// go.mod targets Go 1.23, which lacks testing.T.Context.
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	t.Cleanup(cancelDrain)
	go func() {
		for {
			select {
			case <-ws.calls:
			case <-drainCtx.Done():
				return
			}
		}
	}()
	st, err := h.runner.CreateLead(BackendWerk, h.root, LeadOptions{Model: "vendor/fixture", PermissionLevel: PermissionReadOnly, permissionUser: true})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(4 * time.Second):
			t.Fatalf("turn did not start: %+v", h.state(st.ID))
		}
	}
	h.send(st.ID, "first request")
	wait(started)
	if hello := ws.nextBinding(t); hello.FileWrites.CurrentAuthority() != "read-only" || hello.Conversation != st.ID {
		t.Fatal("lead MCP binding lost read-only authority")
	}
	c := h.runner.conv(st.ID)
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{"steer this input"}, Steer: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.runner.InboxChanged(st.ID)
	eventually(t, "lead durable steer", func() bool { return len(c.record().DurableSteers) == 1 })
	close(release)
	select {
	case req := <-next:
		raw, _ := json.Marshal(req.Input)
		if !strings.Contains(string(raw), "steer this input") || !strings.Contains(req.Instructions, "You are a ws lead") {
			t.Fatal("the lead did not send its composed instructions and steer")
		}
	case <-time.After(4 * time.Second):
		t.Fatalf("steer did not reach the model: %+v", h.state(st.ID))
	}
	eventually(t, "lead idle", func() bool { return h.state(st.ID).Status == StatusIdle })
	eventually(t, "provider summary in common turn history", func() bool {
		ring, err := h.runner.Transcript(st.ID)
		if err != nil {
			t.Fatal(err)
		}
		written, err := json.Marshal(ring)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(written, []byte("opaque")) {
			t.Fatal("the common turn history exposed encrypted reasoning")
		}
		return bytes.Contains(written, []byte("Provider summary"))
	})
	h.send(st.ID, "interrupt this")
	wait(third)
	if _, _, err := h.runner.Interrupt(st.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "interrupted lead idle", func() bool { return h.state(st.ID).Status == StatusIdle })
	h.send(st.ID, "continue the fixture")
	result, err := h.runner.Wait(context.Background(), st.ID, 4*time.Second)
	if err != nil || result.Status != TurnDone || result.Message != "continued" {
		t.Fatalf("lead did not continue after interrupt: %s, %v", result.Status, err)
	}
}
