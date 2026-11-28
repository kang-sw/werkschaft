package wsdashboard

import (
	"net/http"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestMessageRouteCarriesWhiteboardFirstOnTheEntryOnly: the optional
// whiteboard_first field is held on the inbox entry, survives a steer rewrite,
// and never changes the entry's text; a send that omits it or sends false is
// unflagged.
func TestMessageRouteCarriesWhiteboardFirstOnTheEntryOnly(t *testing.T) {
	f := startPageFixture(t)
	on, _ := f.send(t, `{"text":"flagged","whiteboard_first":true}`)
	off, _ := f.send(t, `{"text":"explicit off","whiteboard_first":false}`)
	omitted, _ := f.send(t, `{"text":"legacy page"}`)
	flags := map[string]bool{}
	for _, rec := range f.held(t) {
		flags[rec.ID()] = rec.Entry.WhiteboardFirst
		if got := inboxBody(t, rec.Entry); got == "" || len(rec.Entry.Parts) != 1 {
			t.Fatalf("entry %s text = %q parts %v", rec.ID(), got, rec.Entry.Parts)
		}
	}
	if !flags[on] || flags[off] || flags[omitted] {
		t.Fatalf("flags = %v (on %s off %s omitted %s)", flags, on, off, omitted)
	}
	code, _, raw := postJSON(t, f.base+"/s/livesession/steer", f.base, `{"id":"`+on+`"}`)
	if code != http.StatusOK {
		t.Fatalf("steer = %d: %s", code, raw)
	}
	steered := 0
	for _, rec := range f.held(t) {
		if rec.Entry.Steer {
			steered++
			if !rec.Entry.WhiteboardFirst || rec.Entry.Parts[0] != "flagged" {
				t.Fatalf("steer rewrite lost the flag or changed text: %+v", rec.Entry)
			}
		}
	}
	if steered != 1 {
		t.Fatalf("steered entries = %d, want 1", steered)
	}
}

func TestMessageRouteStampsUserOnLocalAndLANMessages(t *testing.T) {
	f := startPageFixture(t)
	f.withCommands(t, wslive.SlashCommand{Name: "compact"})
	now := time.Now()
	dir := f.inbox("livesession")

	code, _ := f.d.messageWrite(dir, "livesession", []byte(`{"text":"local msg"}`), true, now)
	if code != http.StatusOK {
		t.Fatalf("local messageWrite = %d", code)
	}
	code, _ = f.d.messageWrite(dir, "livesession", []byte(`{"text":"lan msg","whiteboard_first":true}`), false, now.Add(time.Millisecond))
	if code != http.StatusOK {
		t.Fatalf("LAN messageWrite = %d", code)
	}
	code, _ = f.d.messageWrite(dir, "livesession", []byte(`{"command":"compact"}`), true, now.Add(2*time.Millisecond))
	if code != http.StatusOK {
		t.Fatalf("command messageWrite = %d", code)
	}

	held := f.held(t)
	if len(held) != 3 {
		t.Fatalf("held = %d, want 3", len(held))
	}
	if !held[0].Entry.Operator || !held[0].Entry.User {
		t.Fatalf("local message entry = %+v, want Operator=true User=true", held[0].Entry)
	}
	if held[1].Entry.Operator || !held[1].Entry.User || !held[1].Entry.WhiteboardFirst {
		t.Fatalf("LAN message entry = %+v, want Operator=false User=true WhiteboardFirst=true", held[1].Entry)
	}
	if held[2].Entry.Command == nil || held[2].Entry.User {
		t.Fatalf("command entry = %+v, want Command!=nil User=false", held[2].Entry)
	}
}
