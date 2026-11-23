package wsagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

func TestWerkLeadSteersAndInterrupts(t *testing.T) {
	started, release, third := make(chan struct{}), make(chan struct{}), make(chan struct{})
	next := make(chan werk.Request, 1)
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
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			next <- req
			resp, err := werkMessage("steer received")(ctx, req)
			resp.Output = append([]json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"reason","encrypted_content":"opaque","summary":[{"text":"Provider summary"}]}`)}, resp.Output...)
			return resp, err
		},
		func(ctx context.Context, _ werk.Request) (*werk.Response, error) {
			close(third)
			<-ctx.Done()
			return nil, ctx.Err()
		},
		werkMessage("continued"),
	}}
	h, ws, _ := werkHarness(t, model)
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
	ring, err := h.runner.Transcript(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	written, _ := json.Marshal(ring)
	if !strings.Contains(string(written), "Provider summary") || strings.Contains(string(written), "opaque") {
		t.Fatal("the common turn history did not retain only the provider summary")
	}
	h.send(st.ID, "interrupt this")
	wait(third)
	if _, _, err := h.runner.Interrupt(st.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "interrupted lead idle", func() bool { return h.state(st.ID).Status == StatusIdle })
	h.send(st.ID, "continue")
	eventually(t, "lead continued", func() bool { return model.requestCount() == 4 && h.state(st.ID).Status == StatusIdle })
}
