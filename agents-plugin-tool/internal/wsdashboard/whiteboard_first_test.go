package wsdashboard

import (
	"net/http"
	"testing"
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
