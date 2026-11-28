package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompanionBrainRouteAuthAndResponses(t *testing.T) {
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerCompanionBrainRoute(mux)

	// 1. Unbound callback returns 404 upgrade_required.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4321"+companionBrainRoute, nil)
	req.RemoteAddr = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unbound brain route status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}

	// 2. Non-GET rejected with 405.
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4321"+companionBrainRoute, nil)
	req.RemoteAddr = "127.0.0.1:4321"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST brain route status = %d, want 405", rec.Code)
	}

	// 3. Bound callback returns 200 JSON payload for local viewer.
	d.SetCompanionBrain(func() (CompanionBrainInfo, error) {
		return CompanionBrainInfo{
			ReplyID:          "brain123",
			WSKey:            "ws-brain-key",
			Harness:          "claude",
			Model:            "claude-sonnet",
			Tier:             "medium",
			PersonaName:      "Aria",
			Ready:            true,
			SeededFromLegacy: true,
		}, nil
	})
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4321"+companionBrainRoute, nil)
	req.RemoteAddr = "127.0.0.1:4321"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bound brain route status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var info CompanionBrainInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal brain info: %v", err)
	}
	if !info.Ready || info.ReplyID != "brain123" || info.PersonaName != "Aria" || !info.SeededFromLegacy {
		t.Fatalf("brain info = %+v", info)
	}

	// 4. Remote (non-local) viewer forbidden with 403.
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4321"+companionBrainRoute, nil)
	req.RemoteAddr = "192.168.1.55:9999"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote brain route status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}

	// 5. Callback error returns 503 with ready=false.
	d.SetCompanionBrain(func() (CompanionBrainInfo, error) {
		return CompanionBrainInfo{PersonaName: "Aria", Ready: false}, errors.New("no harness")
	})
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4321"+companionBrainRoute, nil)
	req.RemoteAddr = "127.0.0.1:4321"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("error brain route status = %d, want 503", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal error brain info: %v", err)
	}
	if info.Ready || info.Error != "no harness" {
		t.Fatalf("error brain info = %+v", info)
	}
}

func TestCompanionBrainReplyIDHandoffAndLegacySeed(t *testing.T) {
	dir := t.TempDir()
	ringDir := filepath.Join(dir, companionRingDirName)
	if err := os.MkdirAll(ringDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapRec := companionMessengerRecord{
		Type: companionRingSnapshot,
		Messages: []CompanionPhoneMessage{
			{ID: "m1", Sender: "user", Text: "remember the blue kite"},
			{ID: "m2", Sender: "companion", Text: "got it, blue kite noted"},
		},
	}
	rawSnap, err := json.Marshal(snapRec)
	if err != nil {
		t.Fatal(err)
	}
	segPath := filepath.Join(ringDir, companionRingSegmentName(1))
	if err := os.WriteFile(segPath, append(rawSnap, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	statBefore, err := os.Stat(segPath)
	if err != nil {
		t.Fatal(err)
	}

	d := &Daemon{dir: dir}
	seq0, _ := d.companionBrainStates.current()
	d.SetCompanionBrainReplyID("brain-a")
	if got := d.CompanionBrainReplyID(); got != "brain-a" {
		t.Fatalf("CompanionBrainReplyID = %q, want brain-a", got)
	}
	// Setting the same ID again does not emit a duplicate handoff.
	d.SetCompanionBrainReplyID("brain-a")
	d.SetCompanionBrainReplyID("brain-b")
	_, payloads, _ := d.companionBrainStates.since(seq0)
	if len(payloads) != 2 {
		t.Fatalf("handoff events = %d, want 2", len(payloads))
	}
	var ev CompanionBrainStateEvent
	if err := json.Unmarshal(payloads[1], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "handoff" || ev.ReplyID != "brain-b" || ev.Seq <= 0 {
		t.Fatalf("second handoff event = %+v", ev)
	}

	// Legacy seed reads <=10 messages bounded to 4 KiB without modifying the file.
	seed := d.CompanionLegacyMessengerSeed(10, 4096)
	if !strings.Contains(seed, "remember the blue kite") || !strings.Contains(seed, "blue kite noted") {
		t.Fatalf("legacy seed = %q", seed)
	}
	statAfter, err := os.Stat(segPath)
	if err != nil {
		t.Fatal(err)
	}
	if statBefore.Size() != statAfter.Size() || !statBefore.ModTime().Equal(statAfter.ModTime()) {
		t.Fatalf("legacy seed unexpectedly modified segment file")
	}
}
