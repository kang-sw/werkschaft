package mcp

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsstate"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func historyProbe(src *DashboardSessionSource) (builds, hits int, renders map[string]int) {
	src.history.mu.Lock()
	defer src.history.mu.Unlock()
	return src.history.builds, src.history.hits, maps.Clone(src.history.renders)
}

// uncachedTopicHistory is the pre-memo ReadTopicHistory: the whole view
// built and every named topic rendered on each call.
func uncachedTopicHistory(t *testing.T, src *DashboardSessionSource, reg wsdashboard.Registration, names []string) []wswhiteboard.TopicFragment {
	t.Helper()
	_, _, record, ok, err := src.lookupRecord(reg)
	if !ok || err != nil {
		t.Fatalf("lookup: %v", err)
	}
	view := wswhiteboard.BuildView(record.Whiteboard, servedViewInput(record, reg.ReplyID))
	frags, err := wswhiteboard.RenderTopicFragments(view, names)
	if err != nil {
		t.Fatal(err)
	}
	return frags
}

func historyHTML(t *testing.T, src *DashboardSessionSource, reg wsdashboard.Registration, names ...string) map[string]string {
	t.Helper()
	frags, ok := src.ReadTopicHistory(reg, "", names, "")
	if !ok {
		t.Fatal("history not read")
	}
	out := map[string]string{}
	for _, f := range frags {
		out[f.Name] = string(f.HTML)
	}
	return out
}

// TestDashboardTopicHistoryMemo pins the history memo: an unchanged board
// answers from memory (no view build, no render), a board write re-renders
// only the topics it changed, ticket moves and image edits invalidate the
// fragments that show them, and every answer equals an uncached render.
func TestDashboardTopicHistoryMemo(t *testing.T) {
	t.Setenv("WS_MCP_NO_DASHBOARD", "1")
	f := newWhiteboardFixture(t)
	src := dashboardPageSource(t, nil, f.s.sessions.boards)
	src.history.renders = map[string]int{}
	reg := wsdashboard.Registration{ReplyID: f.replyID(), Project: f.root}
	writeTicket(t, f.root, "todo", "261126-feat-memo", "Memo")
	mustWrite(t, f.root, "shots/a.png", "image-one")
	f.update(t, `<wb:topic name="A" tickets="261126-feat-memo"><wb:body>![shot](shots/a.png)</wb:body><wb:panel recap="plan">a-first</wb:panel><wb:log>a-log</wb:log></wb:topic><wb:topic name="B"><wb:panel recap="plan">b-first</wb:panel><wb:log>b-log</wb:log></wb:topic>`)
	f.update(t, `<wb:topic name="A"><wb:panel id="p1" recap="plan">a-second</wb:panel></wb:topic><wb:topic name="B"><wb:log>b-later</wb:log></wb:topic>`)

	same := func(label string, names ...string) map[string]string {
		t.Helper()
		got := historyHTML(t, src, reg, names...)
		for _, fr := range uncachedTopicHistory(t, src, reg, names) {
			if got[fr.Name] != string(fr.HTML) {
				t.Fatalf("%s: memo answer for %s differs from an uncached render", label, fr.Name)
			}
		}
		return got
	}
	first := same("first read", "A", "B")
	if !strings.Contains(first["A"], "a-first") || !strings.Contains(first["B"], "b-first") {
		t.Fatal("full history missing earlier versions")
	}
	builds, hits, renders := historyProbe(src)
	if builds != 1 || renders["A"] != 1 || renders["B"] != 1 {
		t.Fatalf("first read: builds=%d renders=%v", builds, renders)
	}
	for i := 0; i < 5; i++ {
		frags, ok := src.ReadTopicHistory(reg, "", []string{"B", "A", "B", "nosuch"}, "")
		if !ok || len(frags) != 2 || frags[0].Name != "B" || frags[1].Name != "A" || string(frags[1].HTML) != first["A"] {
			t.Fatalf("memo hit answer = %v", frags)
		}
	}
	if b, h, r := historyProbe(src); b != builds || h != hits+5 || !maps.Equal(r, renders) {
		t.Fatalf("unchanged board rebuilt or rendered: builds %d->%d hits %d->%d renders %v->%v", builds, b, hits, h, renders, r)
	}

	// A write to B alone: one build, B renders, A is reused by revision.
	f.update(t, `<wb:topic name="B"><wb:log>b-newest</wb:log></wb:topic>`)
	got := same("after B write", "A", "B")
	if !strings.Contains(got["B"], "b-newest") {
		t.Fatal("board write hidden by the memo")
	}
	builds, hits, renders = historyProbe(src)
	if renders["A"] != 1 || renders["B"] != 2 {
		t.Fatalf("a write to B rendered %v", renders)
	}

	// A ticket move: a session nudge makes it visible at once.
	status := regexp.MustCompile(`data-ticket="261126-feat-memo" data-status="([a-z]+)"`)
	if m := status.FindStringSubmatch(got["A"]); m == nil || m[1] != "todo" {
		t.Fatalf("ticket badge before the move = %v", m)
	}
	move := func(from, to string) {
		t.Helper()
		dir := filepath.Join(f.root, "ai-docs", "tickets")
		if err := os.MkdirAll(filepath.Join(dir, to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, from, "261126-feat-memo.md"), filepath.Join(dir, to, "261126-feat-memo.md")); err != nil {
			t.Fatal(err)
		}
	}
	move("todo", "ready")
	src.invalidateSession(reg)
	if m := status.FindStringSubmatch(same("after nudged ticket move", "A")["A"]); m == nil || m[1] != "ready" {
		t.Fatalf("nudged ticket move hidden: %v", m)
	}
	// Without a nudge the move shows once the listing's window passes.
	move("ready", ".done")
	waitForDashboardValidation(t)
	if m := status.FindStringSubmatch(same("after silent ticket move", "A")["A"]); m == nil || m[1] != "done" {
		t.Fatalf("silent ticket move hidden: %v", m)
	}

	// An image edit: the relay version moves, and only A renders again.
	version := regexp.MustCompile(`shots%2Fa\.png&(?:amp;)*v=([0-9a-z.]+)`)
	before := version.FindStringSubmatch(historyHTML(t, src, reg, "A")["A"])
	if before == nil {
		t.Fatal("history carries no image version")
	}
	_, _, renders = historyProbe(src)
	path := filepath.Join(f.root, "shots", "a.png")
	mustWrite(t, f.root, "shots/a.png", "replacement-image-two")
	at := time.Now().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	waitForDashboardValidation(t)
	after := same("after image edit", "A", "B")
	if next := version.FindStringSubmatch(after["A"]); next == nil || next[1] == before[1] {
		t.Fatal("image edit hidden by the memo")
	}
	if _, _, r := historyProbe(src); r["A"] != renders["A"]+1 || r["B"] != renders["B"] {
		t.Fatalf("image edit renders %v -> %v", renders, r)
	}

	// Forgetting the session drops its records.
	src.ForgetSession(reg.ReplyID)
	src.history.mu.Lock()
	left := len(src.history.views)
	src.history.mu.Unlock()
	if left != 0 {
		t.Fatalf("forgotten session kept %d history records", left)
	}
	_, _, renders = historyProbe(src)
	historyHTML(t, src, reg, "A")
	if _, _, r := historyProbe(src); r["A"] != renders["A"]+1 {
		t.Fatal("a forgotten session answered from memory")
	}
}

// TestDashboardTopicHistoryMemoFileIO counts the file operations a warm
// history read takes: the old path stat'ed every linked ticket in up to
// five status directories and every image on each request; a memo hit
// within the validation window takes none, and a cold read lists the five
// status directories once however many stems the board links.
func TestDashboardTopicHistoryMemoFileIO(t *testing.T) {
	t.Setenv("WS_MCP_NO_DASHBOARD", "1")
	f := newWhiteboardFixture(t)
	src := dashboardPageSource(t, nil, f.s.sessions.boards)
	reg := wsdashboard.Registration{ReplyID: f.replyID(), Project: f.root}
	var cmds strings.Builder
	var names []string
	for i := 0; i < 40; i++ {
		stem := fmt.Sprintf("261126-feat-io-%02d", i)
		writeTicket(t, f.root, []string{"todo", ".done"}[i%2], stem, "IO")
		fmt.Fprintf(&cmds, `<wb:topic name="t%02d" tickets="%s"><wb:panel recap="plan">v1</wb:panel><wb:log>line</wb:log></wb:topic>`, i, stem)
		names = append(names, fmt.Sprintf("t%02d", i))
	}
	f.update(t, cmds.String())
	f.update(t, strings.ReplaceAll(cmds.String(), ">v1<", ">v2<"))

	ours := func() int64 {
		var n int64
		for site, c := range wsstate.FileIOCounters() {
			if strings.Contains(site, "/dashboard_topic_history.go:") {
				n += c
			}
		}
		return n
	}
	counting := &countingTickets{root: f.root}
	_, _, record, _, _ := src.lookupRecord(reg)
	in := servedViewInput(record, reg.ReplyID)
	in.Tickets = counting
	wswhiteboard.BuildView(record.Whiteboard, in)

	start := ours()
	historyHTML(t, src, reg, names[:4]...)
	cold := ours() - start
	src.ticketDirs.mu.Lock()
	lists := src.ticketDirs.lists
	src.ticketDirs.mu.Unlock()
	start = ours()
	began := time.Now()
	for i := 0; i < 20; i++ {
		historyHTML(t, src, reg, names[:4]...)
	}
	warm := ours() - start
	// Each validation window that starts during the reads may list once.
	allowed := int64(5 * (1 + int(time.Since(began)/historyValidation)))
	_, hits, _ := historyProbe(src)
	t.Logf("history read file operations: before (per request) %d TicketStatus lookups = up to %d stats; after: cold %d (listing passes %d), 20 warm reads %d, memo hits %d", counting.calls, 5*counting.calls, cold, lists, warm, hits)
	if lists != 1 || cold > 5 {
		t.Fatalf("cold read: %d listing passes, %d file operations; want one pass of at most 5 listings", lists, cold)
	}
	if hits < 20 || warm > allowed {
		t.Fatalf("warm reads: %d hits, %d file operations", hits, warm)
	}
}

type countingTickets struct {
	root  string
	calls int
}

func (c *countingTickets) TicketStatus(stem string) (string, bool) {
	c.calls++
	return (&whiteboardTickets{root: c.root}).TicketStatus(stem)
}

// TestDashboardTopicHistoryMemoConcurrent reads history from several
// goroutines while the board changes (run under -race): every answer is
// whole, and once writes stop the memo settles to the current board.
func TestDashboardTopicHistoryMemoConcurrent(t *testing.T) {
	t.Setenv("WS_MCP_NO_DASHBOARD", "1")
	f := newWhiteboardFixture(t)
	src := dashboardPageSource(t, nil, f.s.sessions.boards)
	reg := wsdashboard.Registration{ReplyID: f.replyID(), Project: f.root}
	f.update(t, `<wb:topic name="A"><wb:panel recap="plan">a0</wb:panel><wb:log>x</wb:log></wb:topic><wb:topic name="B"><wb:panel recap="plan">b0</wb:panel><wb:log>y</wb:log></wb:topic>`)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			names := [][]string{{"A", "B"}, {"B"}, {"A"}}[g%3]
			for {
				select {
				case <-stop:
					return
				default:
				}
				frags, ok := src.ReadTopicHistory(reg, []string{"", "ko"}[g%2], names, "")
				if !ok || len(frags) != len(names) {
					t.Errorf("concurrent read = %d fragments, %t", len(frags), ok)
					return
				}
				for _, fr := range frags {
					if !strings.HasPrefix(string(fr.HTML), "<section ") {
						t.Errorf("torn fragment for %s", fr.Name)
						return
					}
				}
			}
		}(g)
	}
	for i := 1; i <= 6; i++ {
		f.update(t, fmt.Sprintf(`<wb:topic name="%s"><wb:panel recap="plan">rev-%d</wb:panel></wb:topic>`, []string{"A", "B"}[i%2], i))
	}
	close(stop)
	wg.Wait()
	got := historyHTML(t, src, reg, "A", "B")
	for _, fr := range uncachedTopicHistory(t, src, reg, []string{"A", "B"}) {
		if got[fr.Name] != string(fr.HTML) {
			t.Fatalf("settled memo answer for %s differs from the current board", fr.Name)
		}
	}
	if !strings.Contains(got["A"], "rev-6") || !strings.Contains(got["B"], "rev-5") {
		t.Fatal("settled memo missed the last writes")
	}
}

// TestDashboardLookupRecordSkipsBoardJSON pins that a page or history read
// builds no board JSON (raw is nil), while the index's ReadSession still
// fingerprints the board's bytes.
func TestDashboardLookupRecordSkipsBoardJSON(t *testing.T) {
	t.Setenv("WS_MCP_NO_DASHBOARD", "1")
	f := newWhiteboardFixture(t)
	src := dashboardPageSource(t, nil, f.s.sessions.boards)
	reg := wsdashboard.Registration{ReplyID: f.replyID(), Project: f.root}
	f.update(t, `<wb:topic name="A"><wb:log>one</wb:log></wb:topic>`)
	_, raw, record, ok, err := src.lookupRecord(reg)
	if !ok || err != nil || record.Whiteboard == nil || raw != nil {
		t.Fatalf("lookupRecord = raw %d bytes, board %v, %t, %v", len(raw), record.Whiteboard != nil, ok, err)
	}
	_, raw, _, ok, err = src.lookupRecordWithActivity(reg, false, true)
	if !ok || err != nil || !strings.Contains(string(raw), "\x00board\x00") {
		t.Fatal("ReadSession's lookup lost the board bytes it fingerprints")
	}
	first, _, _ := src.ReadSession(reg)
	f.update(t, `<wb:topic name="A"><wb:log>two</wb:log></wb:topic>`)
	if next, _, _ := src.ReadSession(reg); next.Fingerprint == first.Fingerprint {
		t.Fatal("a board write did not move the session fingerprint")
	}
}

// TestDashboardTopicHistoryMemoTiming logs the per-request cost of the old
// uncached history path against a memo hit on a 120-topic board.
func TestDashboardTopicHistoryMemoTiming(t *testing.T) {
	t.Setenv("WS_MCP_NO_DASHBOARD", "1")
	f := newWhiteboardFixture(t)
	src := dashboardPageSource(t, nil, f.s.sessions.boards)
	reg := wsdashboard.Registration{ReplyID: f.replyID(), Project: f.root}
	var cmds strings.Builder
	for i := 0; i < 120; i++ {
		writeTicket(t, f.root, "todo", fmt.Sprintf("261126-feat-time-%03d", i), "Time")
		fmt.Fprintf(&cmds, "<wb:topic name=\"t%03d\" tickets=\"261126-feat-time-%03d\"><wb:body>## Topic %d\n\n%s</wb:body><wb:panel recap=\"plan\">- one\n- two\n- three</wb:panel><wb:log>line</wb:log></wb:topic>", i, i, i, strings.Repeat("Some *markdown* text with `code`. ", 20))
	}
	f.update(t, cmds.String())
	f.update(t, strings.ReplaceAll(cmds.String(), "- three", "- three\n- four"))
	names := []string{"t000", "t001", "t002", "t003"}
	const n = 20
	start := time.Now()
	for i := 0; i < n; i++ {
		uncachedTopicHistory(t, src, reg, names)
	}
	old := time.Since(start) / n
	historyHTML(t, src, reg, names...)
	start = time.Now()
	for i := 0; i < n; i++ {
		historyHTML(t, src, reg, names...)
	}
	hit := time.Since(start) / n
	t.Logf("4-topic history request on a 120-topic board: uncached %v, memo hit %v (%.1fx)", old, hit, float64(old)/float64(max(hit, 1)))
	if _, hits, _ := historyProbe(src); hits < n {
		t.Fatalf("timed reads were not memo hits: %d", hits)
	}
}
