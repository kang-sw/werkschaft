package mcp

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestDashboardBoardHoldFingerprintNoNudgeAndMemoInvalidation(t *testing.T) {
	f := newWhiteboardFixture(t)
	var changedCalls atomic.Int32
	f.s.sessions.boards.SetChanged(func(_, _ string) {
		changedCalls.Add(1)
	})
	src := dashboardPageSource(t, nil, f.s.sessions.boards)
	reg := wsdashboard.Registration{ReplyID: f.replyID(), Project: f.root}

	f.update(t, `
<wb:topic name="alpha">
  <wb:log>older log entry</wb:log>
  <wb:log>newer log entry</wb:log>
  <wb:add class="policy"><wb:title>Pick cache</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="check"><wb:title>Verify backup</wb:title></wb:add>
  <wb:add class="technical"><wb:title>Ack note</wb:title></wb:add>
</wb:topic>
<wb:topic name="beta">
  <wb:log>untouched topic log</wb:log>
  <wb:add class="policy"><wb:title>Keep untouched</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
`)
	changedCalls.Store(0)

	// Baseline ReadSession + ReadPageLang + ReadTopicHistory (warm memos).
	s0, ok, err := src.ReadSession(reg)
	if err != nil || !ok {
		t.Fatalf("ReadSession baseline = (%v, %v)", ok, err)
	}
	if s0.CountPolicy != 3 || s0.OpenAskIDs != "1 2 4" {
		t.Fatalf("baseline CountPolicy=%d OpenAskIDs=%q, want 3 / \"1 2 4\"", s0.CountPolicy, s0.OpenAskIDs)
	}
	p0 := readPageOK(t, src, reg)
	if p0.Board.CountPolicy != s0.CountPolicy {
		t.Fatalf("baseline Board.CountPolicy=%d != SessionData.CountPolicy=%d", p0.Board.CountPolicy, s0.CountPolicy)
	}
	// Second ReadPageLang stamps watch list and warms the board memo; third hits memo.
	_ = readPageOK(t, src, reg)
	rendersBeforeNoHoldHit := boardRenders(src)
	_ = readPageOK(t, src, reg)
	if got := boardRenders(src); got != rendersBeforeNoHoldHit {
		t.Fatalf("no-hold board missed memo: renders %d -> %d", rendersBeforeNoHoldHit, got)
	}

	// Warm ReadTopicHistory fast-path memo and verify a no-hold board hits it.
	fr0, ok := src.ReadTopicHistory(reg, "", []string{"alpha"}, "")
	if !ok || len(fr0) != 1 || strings.Contains(string(fr0[0].HTML), `wb-held`) {
		t.Fatalf("baseline ReadTopicHistory unexpected: ok=%v len=%d", ok, len(fr0))
	}
	hitsBefore := src.history.hits
	fr0Hit, ok := src.ReadTopicHistory(reg, "", []string{"alpha"}, "")
	if !ok || len(fr0Hit) != 1 || src.history.hits != hitsBefore+1 {
		t.Fatalf("no-hold ReadTopicHistory did not hit fast-path memo: hits %d -> %d", hitsBefore, src.history.hits)
	}

	// Track per-topic renders across the hold mutation.
	src.boards.mu.Lock()
	src.boards.topicRenders = map[string]int{}
	src.boards.mu.Unlock()

	// Hold item 1 (in topic alpha): must NOT invoke WhiteboardStores.SetChanged!
	held, hasBoard, err := src.SetBoardHold(reg, 1, true)
	if err != nil || !hasBoard || !slices.Equal(held, []int{1}) {
		t.Fatalf("SetBoardHold(1, true) = (%v, %v, %v), want ([1], true, nil)", held, hasBoard, err)
	}
	if got := changedCalls.Load(); got != 0 {
		t.Fatalf("SetBoardHold triggered WhiteboardStores.changed %d times, want 0 (never nudge agent)", got)
	}

	// ReadSession: NonLiveFingerprint changes, CountPolicy drops from 3 to 2, OpenAskIDs stays "1 2 4".
	s1, ok, err := src.ReadSession(reg)
	if err != nil || !ok {
		t.Fatalf("ReadSession after hold = (%v, %v)", ok, err)
	}
	if s1.NonLiveFingerprint == s0.NonLiveFingerprint {
		t.Fatalf("NonLiveFingerprint did not change on hold toggle")
	}
	if s1.CountPolicy != 2 || s1.OpenAskIDs != "1 2 4" {
		t.Fatalf("after hold: CountPolicy=%d OpenAskIDs=%q, want 2 / \"1 2 4\"", s1.CountPolicy, s1.OpenAskIDs)
	}

	// Board render memo invalidates for the held board, renders alpha (affected),
	// and reuses beta's memoized TopicUnit without re-rendering beta!
	p1 := readPageOK(t, src, reg)
	if p1.Board.CountPolicy != s1.CountPolicy {
		t.Fatalf("after hold: Board.CountPolicy=%d != SessionData.CountPolicy=%d", p1.Board.CountPolicy, s1.CountPolicy)
	}
	if !strings.Contains(string(p1.Board.Board), `class="card policy wb-held" id="item-1"`) {
		t.Fatalf("rendered board after hold lacks .wb-held on item-1")
	}
	src.boards.mu.Lock()
	alphaRenders := src.boards.topicRenders["alpha"]
	betaRenders := src.boards.topicRenders["beta"]
	src.boards.mu.Unlock()
	if alphaRenders != 1 || betaRenders != 0 {
		t.Fatalf("topic renders after hold in alpha: alpha=%d beta=%d, want alpha=1 beta=0", alphaRenders, betaRenders)
	}

	// ReadTopicHistory fast-path memo must invalidate on hold write despite unchanged generation.
	buildsBefore := src.history.builds
	fr1, ok := src.ReadTopicHistory(reg, "", []string{"alpha"}, "")
	if !ok || len(fr1) != 1 {
		t.Fatalf("ReadTopicHistory after hold failed")
	}
	if src.history.builds != buildsBefore+1 {
		t.Fatalf("ReadTopicHistory served stale memo after hold write: builds %d -> %d", buildsBefore, src.history.builds)
	}
	if !strings.Contains(string(fr1[0].HTML), `class="card policy wb-held" id="item-1"`) {
		t.Fatalf("ReadTopicHistory after hold lacks .wb-held on item-1")
	}

	// No-op patch on item 1 keeps its hold; content-changing patch clears it.
	f.update(t, `<wb:patch id="1"><wb:title>Pick cache</wb:title></wb:patch>`)
	sNoOp, ok, err := src.ReadSession(reg)
	if err != nil || !ok || sNoOp.CountPolicy != 2 {
		t.Fatalf("after no-op patch: CountPolicy=%d ok=%v err=%v, want 2", sNoOp.CountPolicy, ok, err)
	}
	f.update(t, `<wb:patch id="1"><wb:title>Pick cache (revised)</wb:title></wb:patch>`)
	sPatched, ok, err := src.ReadSession(reg)
	if err != nil || !ok || sPatched.CountPolicy != 3 {
		t.Fatalf("after content patch: CountPolicy=%d ok=%v err=%v, want 3", sPatched.CountPolicy, ok, err)
	}
	pPatched := readPageOK(t, src, reg)
	if pPatched.Board.CountPolicy != 3 || strings.Contains(string(pPatched.Board.Board), `wb-held`) {
		t.Fatalf("after content patch: Board.CountPolicy=%d or stale wb-held remains", pPatched.Board.CountPolicy)
	}
}
