package wsdashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

type holdBoardSource struct {
	mu     sync.Mutex
	boards map[string]*wswhiteboard.BoardDB
}

func (s *holdBoardSource) ReadSession(reg Registration) (SessionData, bool, error) {
	s.mu.Lock()
	b := s.boards[reg.ReplyID]
	s.mu.Unlock()
	if b == nil {
		return SessionData{Fingerprint: "noboard"}, true, nil
	}
	st, rev, held := b.SnapshotWithHolds()
	fp := fmt.Sprintf("rev=%d;seq=%d;held=%v", rev, st.Seq, held)
	return SessionData{
		HasBoard:           true,
		CountPolicy:        st.OpenAskCountExcluding(held),
		OpenAskIDs:         st.OpenAskIDs(),
		NonLiveFingerprint: fp,
		Fingerprint:        fp,
	}, true, nil
}

func (s *holdBoardSource) ReadPage(reg Registration) (PageContent, bool, error) {
	s.mu.Lock()
	b := s.boards[reg.ReplyID]
	s.mu.Unlock()
	if b == nil {
		return PageContent{}, true, nil
	}
	st, _, held := b.SnapshotWithHolds()
	view := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{
		Layout:  wswhiteboard.LayoutFull,
		ReplyID: reg.ReplyID,
		Held:    held,
	})
	regions, err := wswhiteboard.RenderRegions(view)
	if err != nil {
		return PageContent{}, false, err
	}
	return PageContent{
		HasBoard:         true,
		Seq:              st.Seq,
		BoardIncarnation: st.LoadedAt,
		Board:            regions,
	}, true, nil
}

func (s *holdBoardSource) SetBoardHold(reg Registration, itemID int, held bool) ([]int, bool, error) {
	s.mu.Lock()
	b := s.boards[reg.ReplyID]
	s.mu.Unlock()
	if b == nil {
		return nil, false, nil
	}
	ids, err := b.SetHold(itemID, held)
	return ids, true, err
}

func newTestBoardDB(t *testing.T, cmds string) *wswhiteboard.BoardDB {
	t.Helper()
	st := &wswhiteboard.State{}
	_, probs := wswhiteboard.Apply(st, cmds, wswhiteboard.Env{Now: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)})
	if len(probs) != 0 {
		t.Fatalf("Apply: %v", probs)
	}
	b, err := wswhiteboard.CreateBoardDB(filepath.Join(t.TempDir(), "board"), st, wswhiteboard.BoardDBOptions{})
	if err != nil {
		t.Fatalf("CreateBoardDB: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func startHoldDaemonFixture(t *testing.T, boards map[string]*wswhiteboard.BoardDB) (*Daemon, string, string) {
	t.Helper()
	dir := t.TempDir()
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	src := &holdBoardSource{boards: boards}
	now := time.Now()
	for rid := range boards {
		register(t, dir, rid, project, now)
	}
	// Also register one session without a board.
	register(t, dir, "noboardsess", project, now)
	d := startIndexedDaemon(t, dir, src, time.Hour, func(o *DaemonOptions) { o.noGitBadge = true })
	base := endpointURL(d.Endpoint(), "")
	for rid := range boards {
		waitFor(t, "index to load "+rid, func() bool {
			s, ok := d.Index().Session(rid)
			return d.Index().Loaded() && ok && s.HasBoard
		})
	}
	waitFor(t, "index to load noboardsess", func() bool {
		_, ok := d.Index().Session("noboardsess")
		return d.Index().Loaded() && ok
	})
	return d, base, project
}

func TestBoardHoldRouteContractAndRailDot(t *testing.T) {
	b1 := newTestBoardDB(t, `
<wb:topic name="design">
  <wb:add class="policy"><wb:title>Pick cache</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="check"><wb:title>Verify backup</wb:title></wb:add>
  <wb:add class="technical"><wb:title>Ack policy</wb:title></wb:add>
  <wb:add class="policy"><wb:title>Already answered</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
<wb:answer id="4" value="a">4=a</wb:answer>
`)
	b2 := newTestBoardDB(t, `
<wb:topic name="ops">
  <wb:add class="policy"><wb:title>Second session ask</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
`)
	d, base, _ := startHoldDaemonFixture(t, map[string]*wswhiteboard.BoardDB{
		"sessone": b1,
		"sesstwo": b2,
	})

	holdURL := func(rid string) string { return base + "/s/" + rid + "/board/hold" }

	// Method gate: GET -> 405.
	if code, _ := get(t, holdURL("sessone")); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET hold = %d, want 405", code)
	}
	// Same-origin gate: foreign Origin -> 403.
	if code, _, _ := postJSON(t, holdURL("sessone"), "http://evil.example", `{"id":"1","held":true}`); code != http.StatusForbidden {
		t.Fatalf("cross-origin hold = %d, want 403", code)
	}
	// Unknown session -> 404.
	if code, _, _ := postJSON(t, holdURL("nosuchsession"), base, `{"id":"1","held":true}`); code != http.StatusNotFound {
		t.Fatalf("unknown session = %d, want 404", code)
	}
	// Session without a board -> 404.
	if code, _, _ := postJSON(t, holdURL("noboardsess"), base, `{"id":"1","held":true}`); code != http.StatusNotFound {
		t.Fatalf("no-board session = %d, want 404", code)
	}
	// Oversized body -> 413.
	if code, _, _ := postJSON(t, holdURL("sessone"), base, `{"id":"`+strings.Repeat("1", boardHoldBodyLimit)+`","held":true}`); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized hold body = %d, want 413", code)
	}
	// Malformed bodies -> 400.
	for _, bad := range []string{
		`{`,
		`{}`,
		`{"id":"1"}`,
		`{"held":true}`,
		`{"id":"","held":true}`,
		`{"id":"abc","held":true}`,
		`{"id":"0","held":true}`,
		`{"id":"-1","held":true}`,
		`{"id":0,"held":true}`,
		`{"id":1.5,"held":true}`,
		`{"id":"1","held":"yes"}`,
	} {
		if code, _, raw := postJSON(t, holdURL("sessone"), base, bad); code != http.StatusBadRequest {
			t.Errorf("bad body %s = %d %q, want 400", bad, code, raw)
		}
	}
	// Unknown item ID -> 404.
	if code, _, _ := postJSON(t, holdURL("sessone"), base, `{"id":"999","held":true}`); code != http.StatusNotFound {
		t.Fatalf("unknown item = %d, want 404", code)
	}
	// Non-open item -> 409.
	if code, _, _ := postJSON(t, holdURL("sessone"), base, `{"id":"4","held":true}`); code != http.StatusConflict {
		t.Fatalf("non-open item = %d, want 409", code)
	}

	decodeHeld := func(raw string) []string {
		t.Helper()
		var resp boardHoldResponse
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		return resp.Held
	}

	// Clearing an unheld open item is a success no-op returning [].
	code, hdr, raw := postJSON(t, holdURL("sessone"), base, `{"id":"1","held":false}`)
	if code != http.StatusOK || hdr.Get("Cache-Control") != "no-store" || strings.TrimSpace(raw) != `{"held":[]}` {
		t.Fatalf("clear unheld = %d %q %q, want 200 no-store {\"held\":[]}", code, hdr.Get("Cache-Control"), raw)
	}

	// Initial rail: sessone has 2 open asks (policy 1 + check 2), sesstwo has 1 -> total 3.
	railRowRe := func(rid string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)<div class="rs[^"]*" data-rid="` + rid + `"[^>]*>.*?<template class="shp-source"`)
	}
	_, shellHTML := get(t, base+"/s/sesstwo")
	row1 := railRowRe("sessone").FindString(shellHTML)
	if !strings.Contains(row1, `data-asks="1 2"`) || !strings.Contains(row1, `class="qdot"`) {
		t.Fatalf("initial sessone rail row lacks data-asks or qdot:\n%s", row1)
	}
	if !strings.Contains(shellHTML, `class="chip policy"`) {
		t.Fatalf("initial shell lacks rail header policy chip")
	}

	// Hold policy (1, also testing zero-padded DOM data-id "01"), check (2), and ack (3); repeat set on 1 is idempotent.
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"id":"01","held":true}`, []string{"1"}},
		{`{"id":"1","held":true}`, []string{"1"}},
		{`{"id":"3","held":true}`, []string{"1", "3"}},
		{`{"id":"2","held":true}`, []string{"1", "2", "3"}},
	} {
		code, _, raw := postJSON(t, holdURL("sessone"), base, tc.body)
		if code != http.StatusOK {
			t.Fatalf("POST %s = %d %q, want 200", tc.body, code, raw)
		}
		got := decodeHeld(raw)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("POST %s held = %v, want %v", tc.body, got, tc.want)
		}
	}

	// Wait for index Reread to reflect CountPolicy == 0 while OpenAskIDs remains "1 2".
	waitFor(t, "sessone CountPolicy to drop to 0", func() bool {
		s, ok := d.Index().Session("sessone")
		return ok && s.CountPolicy == 0 && s.OpenAskIDs == "1 2"
	})

	// Shell rendered from sesstwo: sessone's .qdot is hidden while data-asks="1 2" stays!
	_, shellHTML = get(t, base+"/s/sesstwo")
	row1 = railRowRe("sessone").FindString(shellHTML)
	if !strings.Contains(row1, `data-asks="1 2"`) {
		t.Fatalf("held sessone rail row changed data-asks:\n%s", row1)
	}
	if strings.Contains(row1, `class="qdot"`) {
		t.Fatalf("held sessone rail row still shows .qdot when all asks are held:\n%s", row1)
	}

	// Session frame for sessone renders .wb-held and aria-pressed="true" on items 1, 2, 3.
	_, frameHTML := get(t, base+"/s/sessone?frame=1")
	for _, want := range []string{
		`class="card policy wb-held" id="item-1"`,
		`class="ckrow wb-held" id="item-2"`,
		`class="arow wb-held" id="item-3"`,
		`aria-pressed="true"`,
	} {
		if !strings.Contains(frameHTML, want) {
			t.Errorf("held session frame lacks %q", want)
		}
	}

	// Unhold item 2: CountPolicy returns to 1, OpenAskIDs stays "1 2", .qdot reappears.
	code, _, raw = postJSON(t, holdURL("sessone"), base, `{"id":"2","held":false}`)
	if code != http.StatusOK || strings.TrimSpace(raw) != `{"held":["1","3"]}` {
		t.Fatalf("unhold 2 = %d %q, want 200 [1,3]", code, raw)
	}
	waitFor(t, "sessone CountPolicy to return to 1", func() bool {
		s, ok := d.Index().Session("sessone")
		return ok && s.CountPolicy == 1 && s.OpenAskIDs == "1 2"
	})
	_, shellHTML = get(t, base+"/s/sesstwo")
	row1 = railRowRe("sessone").FindString(shellHTML)
	if !strings.Contains(row1, `data-asks="1 2"`) || !strings.Contains(row1, `class="qdot"`) {
		t.Fatalf("unheld sessone rail row lacks data-asks or qdot:\n%s", row1)
	}
}

// TestBoardHoldLiveMultiTabSSEPropagation verifies live multi-tab propagation:
// holding an ask via HTTP updates both the active session's board SSE stream
// (.wb-held) and a second tab's rail SSE stream (inactive session's .qdot
// hides and rail header total decrements) while preserving data-asks.
func TestBoardHoldLiveMultiTabSSEPropagation(t *testing.T) {
	b1 := newTestBoardDB(t, `
<wb:topic name="design">
  <wb:add class="policy"><wb:title>Pick cache</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
`)
	b2 := newTestBoardDB(t, `
<wb:topic name="ops">
  <wb:add class="policy"><wb:title>Second session ask</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
`)
	_, base, _ := startHoldDaemonFixture(t, map[string]*wswhiteboard.BoardDB{
		"sessone": b1,
		"sesstwo": b2,
	})

	boardStream := openSSE(t, base+"/s/sessone/events?frame=1")
	railStream := openSSE(t, base+"/rail/events")

	// Drain initial frames on both streams.
	waitRegion := func(ch <-chan sseEvent, wantName string, pred func(string) bool, desc string) string {
		t.Helper()
		deadline := time.After(8 * time.Second)
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					t.Fatalf("stream closed waiting for %s", desc)
				}
				if ev.name != "region" {
					continue
				}
				var r regionEvent
				if json.Unmarshal([]byte(ev.data), &r) != nil || r.Name != wantName {
					continue
				}
				html := string(r.HTML)
				if pred(html) {
					return html
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", desc)
			}
		}
	}

	waitRegion(boardStream, regionBoard, func(h string) bool {
		return strings.Contains(h, `id="item-1"`) && !strings.Contains(h, `wb-held`)
	}, "initial unheld board frame")
	waitRegion(railStream, regionRail, func(h string) bool {
		return strings.Contains(h, `data-rid="sessone"`) && strings.Contains(h, `class="qdot"`)
	}, "initial rail frame with qdot")

	// Hold item 1 of sessone from a second tab via POST /s/sessone/board/hold.
	if code, _, raw := postJSON(t, base+"/s/sessone/board/hold", base, `{"id":"1","held":true}`); code != http.StatusOK {
		t.Fatalf("POST hold = %d %q", code, raw)
	}

	// Tab 1's active board SSE stream receives the held markup.
	waitRegion(boardStream, regionBoard, func(h string) bool {
		return strings.Contains(h, `class="card policy wb-held" id="item-1"`)
	}, "board SSE update with wb-held")

	// Tab 2's rail SSE stream hides sessone's .qdot while keeping data-asks="1".
	railRowRe := regexp.MustCompile(`(?s)<div class="rs[^"]*" data-rid="sessone"[^>]*>.*?<template class="shp-source"`)
	waitRegion(railStream, regionRail, func(h string) bool {
		row := railRowRe.FindString(h)
		return row != "" && strings.Contains(row, `data-asks="1"`) && !strings.Contains(row, `class="qdot"`)
	}, "rail SSE update hiding sessone .qdot")
}
