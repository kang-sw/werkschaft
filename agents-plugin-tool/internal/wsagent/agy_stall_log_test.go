package wsagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the repeated-hour fixture's zone, on any host
)

// Synthetic agy worker log lines in agy's glog shape. No real log content.

func agyGlog(at time.Time, goroutine int, file, msg string) string {
	return agyGlogWall(at.Local(), goroutine, file, msg)
}

// agyGlogWall writes at's wall clock in its own location.
func agyGlogWall(at time.Time, goroutine int, file, msg string) string {
	return fmt.Sprintf("I%02d%02d %02d:%02d:%02d.%06d %7d %s] %s\n", int(at.Month()), at.Day(), at.Hour(), at.Minute(), at.Second(), at.Nanosecond()/1000, goroutine, file, msg)
}

func agyGlogStart(at time.Time, goroutine int) string {
	return agyGlog(at, goroutine, "server.go:100", "agent ready")
}

// agyGlogRequest is agy's per-request ACP server line for method.
func agyGlogRequest(at time.Time, goroutine int, method string) string {
	return agyGlog(at, goroutine, "server.go:213", "ACP Request received: "+method)
}

// agyGlogPrompt is what agy logs on receiving a session/prompt: the request
// line, then (for some prompts only) the user-message line naming the
// session.
func agyGlogPrompt(at time.Time, goroutine int, session string) string {
	return agyGlogRequest(at, goroutine, "session/prompt") + agyGlogUserMessage(at, goroutine, session)
}

func agyGlogUserMessage(at time.Time, goroutine int, session string) string {
	return agyGlog(at, goroutine, "server.go:1883", "Sending user message to conversation "+session+" (items=1, media=0)")
}

func agyGlogHeader(at time.Time, goroutine int) string {
	return agyGlog(at, goroutine, "http_helpers.go:760", "URL: https://model.example.invalid/v1internal:streamGenerateContent?alt=sse Trace: 0x1 ResponseID: synthetic")
}

func agyGlogMonitor(at time.Time, goroutine int) string {
	return agyGlog(at, goroutine, "llm_monitor.go:706", "LLM Monitor synthetic: Calling LLM (model: SYNTHETIC)")
}

func TestParseAgyStallTailMainAndMonitorHeaders(t *testing.T) {
	b := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	before := b.Add(-30 * time.Second)
	prompt := agyGlogRequest(b, 2, "session/prompt")
	const noRequest = "no session/prompt request line"
	cases := []struct {
		name   string
		tail   string
		want   agyStallVerdict
		reason string
	}{
		{
			name: "main header after the boundary",
			tail: agyGlogStart(before, 1) + agyGlogMonitor(before, 7) + agyGlogHeader(before, 7) + agyGlogHeader(b.Add(5*time.Second), 9),
			want: agyStallHeader,
		},
		{
			name: "main header inside the skew margin",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(-time.Second), 9),
			want: agyStallHeader,
		},
		{
			name: "only an earlier request's header",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlogPrompt(b, 2, "s1"),
			want: agyStallHeaderless,
		},
		{
			name: "only a monitor header after the boundary",
			tail: agyGlogStart(before, 1) + prompt + agyGlogMonitor(b.Add(time.Second), 11) + agyGlogHeader(b.Add(3*time.Second), 11),
			want: agyStallHeaderless,
		},
		{
			name: "monitor marker later in the tail still marks its goroutine",
			tail: agyGlogStart(before, 1) + prompt + agyGlogHeader(b.Add(3*time.Second), 11) + agyGlogMonitor(b.Add(4*time.Second), 11),
			want: agyStallHeaderless,
		},
		{
			name: "request line alone, without the user-message line",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + prompt,
			want: agyStallHeaderless,
		},
		{
			name: "request line just after the boundary",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlogRequest(b.Add(time.Millisecond), 2, "session/prompt"),
			want: agyStallHeaderless,
		},
		{
			name:   "request line inside the skew margin, before the boundary",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlogRequest(b.Add(-time.Second), 2, "session/prompt"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "request line a microsecond before the boundary",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlogRequest(b.Add(-time.Microsecond), 2, "session/prompt"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name: "monitor header whose marker is not in the tail counts as main",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(3*time.Second), 11),
			want: agyStallHeader,
		},
		{
			name: "continuation lines are ignored",
			tail: agyGlogStart(before, 1) + "  a wrapped continuation streamGenerateContent\n" + agyGlogHeader(b.Add(-20*time.Second), 9) + "not glog\n" + agyGlogPrompt(b, 2, "s1"),
			want: agyStallHeaderless,
		},
		{
			name:   "verified tail that ends before the window",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "ordinary lines in the window but no request line",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlog(b.Add(-time.Second), 3, "run.go:10", "working") + agyGlogUserMessage(b, 2, "s1"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "request line before the window",
			tail:   agyGlogStart(before, 1) + agyGlogRequest(b.Add(-10*time.Second), 2, "session/prompt") + agyGlogHeader(b.Add(-9*time.Second), 9) + agyGlog(b.Add(time.Second), 3, "run.go:10", "working"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "request line of another method",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlogRequest(b, 2, "session/cancel"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "method token is a whole field",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlogRequest(b, 2, "session/prompt_status"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "method token from another source line",
			tail:   agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9) + agyGlog(b, 2, "server.go:214", "ACP Request received: session/prompt"),
			want:   agyStallUnknown,
			reason: noRequest,
		},
		{
			name:   "tail starting after the boundary minus the skew margin",
			tail:   agyGlogStart(b.Add(-time.Second), 1) + agyGlogHeader(b.Add(-time.Second), 7) + agyGlogMonitor(b, 7),
			want:   agyStallUnknown,
			reason: "starts after",
		},
		{
			name:   "covered glog tail with no recognized header",
			tail:   agyGlogStart(before, 1) + agyGlogPrompt(b, 2, "s1") + agyGlog(b.Add(time.Second), 3, "run.go:10", "working"),
			want:   agyStallUnknown,
			reason: "no recognized",
		},
		{
			name:   "covered tail whose HTTP marker changed",
			tail:   agyGlogStart(before, 1) + agyGlog(b.Add(-5*time.Second), 9, "http_client.go:12", "Endpoint: https://model.example.invalid/v1internal:streamGenerateContent"),
			want:   agyStallUnknown,
			reason: "no recognized",
		},
		{
			name:   "garbled",
			tail:   "\x00\x01 not a log\n{\"json\":true}\n",
			want:   agyStallUnknown,
			reason: "no glog line",
		},
		{
			name:   "empty",
			tail:   "",
			want:   agyStallUnknown,
			reason: "no glog line",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, reason := parseAgyStallTail([]byte(tc.tail), false, b, agyStallSkew)
			if got != tc.want || !strings.Contains(reason, tc.reason) {
				t.Fatalf("verdict = %v (%q), want %v (%q)", got, reason, tc.want, tc.reason)
			}
		})
	}
}

// A clipped tail drops its first, possibly partial line before the coverage
// check; a header after the window inside that partial line never counts.
func TestParseAgyStallTailClippedFirstLine(t *testing.T) {
	b := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	partial := agyGlogHeader(b.Add(time.Second), 9)[5:]
	tail := partial + agyGlogStart(b.Add(-time.Minute), 1) + agyGlogHeader(b.Add(-30*time.Second), 9) + agyGlogPrompt(b, 2, "s1")
	if got, _, reason := parseAgyStallTail([]byte(tail), true, b, agyStallSkew); got != agyStallHeaderless {
		t.Fatalf("clipped tail verdict = %v (%q), want headerless", got, reason)
	}
	if got, _, _ := parseAgyStallTail([]byte("no newline at all"), true, b, agyStallSkew); got != agyStallUnknown {
		t.Fatalf("clipped tail with no complete line = %v, want unknown", got)
	}
}

// A valid tail never validates a later one: format drift in the next read
// is unknown even though the earlier read was verified.
func TestParseAgyStallTailFormatDriftAfterValidTail(t *testing.T) {
	b := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	first := agyGlogStart(b.Add(-time.Minute), 1) + agyGlogMonitor(b.Add(-50*time.Second), 7) + agyGlogHeader(b.Add(-50*time.Second), 7) + agyGlogPrompt(b, 2, "s1")
	if got, _, reason := parseAgyStallTail([]byte(first), false, b, agyStallSkew); got != agyStallHeaderless {
		t.Fatalf("first tail = %v (%q), want headerless", got, reason)
	}
	b2 := b.Add(3 * time.Minute)
	drifted := agyGlogStart(b2.Add(-time.Minute), 1) + agyGlog(b2.Add(-50*time.Second), 7, "http_helpers.go:760", "Request: https://model.example.invalid/v1internal:streamGenerateContent?alt=sse")
	if got, _, reason := parseAgyStallTail([]byte(drifted), false, b2, agyStallSkew); got != agyStallUnknown {
		t.Fatalf("drifted tail = %v (%q), want unknown", got, reason)
	}
}

// anyMain reports a main header anywhere in the tail; a monitor header
// alone verifies the format but is never one.
func TestParseAgyStallTailReportsAnyMainHeader(t *testing.T) {
	b := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	before := b.Add(-30 * time.Second)
	current := agyGlogPrompt(b, 2, "s1")
	monitorOnly := agyGlogStart(before, 1) + agyGlogMonitor(before, 7) + agyGlogHeader(before, 7)
	if got, anyMain, _ := parseAgyStallTail([]byte(monitorOnly+current), false, b, agyStallSkew); got != agyStallHeaderless || anyMain {
		t.Fatalf("monitor only = %v anyMain=%v, want headerless with no main", got, anyMain)
	}
	earlierMain := monitorOnly + agyGlogHeader(b.Add(-10*time.Second), 9) + current
	if got, anyMain, _ := parseAgyStallTail([]byte(earlierMain), false, b, agyStallSkew); got != agyStallHeaderless || !anyMain {
		t.Fatalf("earlier main = %v anyMain=%v, want headerless with a main", got, anyMain)
	}
}

// A local time the fall-back hour repeats names two instants; the reading
// that never cuts wins on both sides of the window.
func TestParseAgyStallTailRepeatedLocalHour(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	wall := func(hour, minute, sec int) time.Time {
		return time.Date(2026, 11, 1, hour, minute, sec, 0, time.FixedZone("wall", 0))
	}
	line := func(hour, minute, sec, goroutine int, file, msg string) string {
		return agyGlogWall(wall(hour, minute, sec), goroutine, file, msg)
	}
	header := func(hour, minute, sec, goroutine int) string {
		return line(hour, minute, sec, goroutine, "http_helpers.go:760", "URL: https://model.example.invalid/v1internal:streamGenerateContent?alt=sse")
	}
	monitor := func(hour, minute, sec, goroutine int) string {
		return line(hour, minute, sec, goroutine, "llm_monitor.go:706", "LLM Monitor synthetic: Calling LLM")
	}
	// 01:10:00 EST, the second 01:10: a header at 01:10:01 is in the window
	// under its later reading, whichever one time.Date picks.
	second := time.Date(2026, 11, 1, 6, 10, 0, 0, time.UTC).In(ny)
	tail := line(0, 50, 0, 1, "server.go:100", "agent ready") + monitor(0, 55, 0, 7) + header(0, 55, 0, 7) + header(1, 10, 1, 9)
	if got, _, reason := parseAgyStallTail([]byte(tail), false, second, agyStallSkew); got != agyStallHeader {
		t.Fatalf("repeated-hour header = %v (%q), want header", got, reason)
	}
	// A request line at 01:10:01 could be the first 01:10:01, an hour
	// before the boundary: it never attributes the tail.
	tail = line(0, 50, 0, 1, "server.go:100", "agent ready") + header(0, 55, 0, 9) + line(1, 10, 1, 2, "server.go:213", "ACP Request received: session/prompt")
	if got, _, reason := parseAgyStallTail([]byte(tail), false, second, agyStallSkew); got != agyStallUnknown || !strings.Contains(reason, "no session/prompt request line") {
		t.Fatalf("repeated-hour request line = %v (%q), want unknown", got, reason)
	}
	// 01:10:00 EDT, the first 01:10: a first line at 01:05 could be the
	// second 01:05, after the boundary, so the tail does not cover it.
	first := time.Date(2026, 11, 1, 5, 10, 0, 0, time.UTC).In(ny)
	tail = line(1, 5, 0, 1, "server.go:100", "agent ready") + monitor(1, 5, 1, 7) + header(1, 5, 1, 7) + header(0, 59, 0, 9)
	if got, _, reason := parseAgyStallTail([]byte(tail), false, first, agyStallSkew); got != agyStallUnknown || !strings.Contains(reason, "starts after") {
		t.Fatalf("repeated-hour first line = %v (%q), want unknown", got, reason)
	}
	early, late := localInstants(2026, time.November, 1, 1, 30, 0, 0, ny)
	if late.Sub(early) != time.Hour {
		t.Fatalf("01:30 on the fall-back day = %v and %v, want an hour apart", early, late)
	}
	if early, late := localInstants(2026, time.March, 8, 2, 30, 0, 0, ny); !early.Equal(late) {
		t.Fatalf("a skipped time = %v and %v, want one instant", early, late)
	}
	if early, late := localInstants(2026, time.November, 22, 12, 0, 0, 0, ny); !early.Equal(late) {
		t.Fatalf("an ordinary time = %v and %v, want one instant", early, late)
	}
}

func TestParseAgyGlogLineYearWrap(t *testing.T) {
	boundary := time.Date(2027, 1, 1, 0, 0, 30, 0, time.Local)
	line, ok := parseAgyGlogLine("I1231 23:59:50.000001     1 server.go:1] x", boundary)
	if !ok || line.at.Year() != 2026 || line.at.Month() != time.December {
		t.Fatalf("year wrap = %+v (ok %v)", line, ok)
	}
	if _, ok := parseAgyGlogLine("I1345 23:59:50.000001     1 server.go:1] x", boundary); ok {
		t.Fatal("month 13 parsed")
	}
}

func writeAgyLog(t *testing.T, dir, name, content string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocateAgyWorkerLogBySessionAndAge(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Hour)
	at := started.Add(time.Minute)
	writeAgyLog(t, dir, "acp-worker-20261122_100000-11.log", agyGlogUserMessage(at, 1, "s-target"), started.Add(-time.Hour))
	writeAgyLog(t, dir, "acp-worker-20261122_110000-12.log", agyGlogUserMessage(at, 1, "s-other"), started.Add(3*time.Minute))
	writeAgyLog(t, dir, "acp-worker-20261122_110001-13.log", agyGlogUserMessage(at, 1, "s-target-2"), started.Add(4*time.Minute))
	want := writeAgyLog(t, dir, "acp-worker-20261122_110002-14.log", agyGlogStart(at, 1)+agyGlogUserMessage(at, 1, "s-target"), started.Add(2*time.Minute))
	writeAgyLog(t, dir, "unrelated.log", agyGlogUserMessage(at, 1, "s-target"), started.Add(5*time.Minute))
	got, err := locateAgyWorkerLog(dir, "s-target", started)
	if err != nil || got != want {
		t.Fatalf("locate = %q (%v), want %q", got, err, want)
	}
	// The same session named by an older process's log only: not found.
	if got, err := locateAgyWorkerLog(dir, "s-target", started.Add(3*time.Minute)); err == nil {
		t.Fatalf("locate after the process start = %q, want none", got)
	}
	if _, err := locateAgyWorkerLog(filepath.Join(dir, "missing"), "s-target", started); err == nil {
		t.Fatal("a missing directory located a log")
	}
}

// A predecessor of the same session that stopped just before this process
// started is never this process's log, even when it is the only match.
func TestLocateAgyWorkerLogIgnoresAJustStoppedPredecessor(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Minute)
	b := started.Add(30 * time.Second)
	writeAgyLog(t, dir, "acp-worker-20261122_115900-21.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogUserMessage(b.Add(-50*time.Second), 1, "s1")+agyGlogMonitor(b.Add(-40*time.Second), 7)+agyGlogHeader(b.Add(-40*time.Second), 9), started.Add(-time.Second))
	if got, err := locateAgyWorkerLog(dir, "s1", started); err == nil {
		t.Fatalf("located the predecessor %q", got)
	}
	l := newAgyStallLog(dir, started)
	if got, reason := l.verdict("s1", b); got != agyStallUnknown {
		t.Fatalf("predecessor only = %v (%q), want unknown", got, reason)
	}
}

// The newest matching log wins when two processes of one session both
// postdate the start.
func TestLocateAgyWorkerLogPrefersNewest(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Hour)
	at := started.Add(time.Minute)
	writeAgyLog(t, dir, "acp-worker-a.log", agyGlogUserMessage(at, 1, "s1"), started.Add(time.Minute))
	newest := writeAgyLog(t, dir, "acp-worker-b.log", agyGlogUserMessage(at, 1, "s1"), started.Add(2*time.Minute))
	if got, err := locateAgyWorkerLog(dir, "s1", started); err != nil || got != newest {
		t.Fatalf("locate = %q (%v), want %q", got, err, newest)
	}
}

func TestAgyStallLogVerdictUnknownCases(t *testing.T) {
	b := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	started := time.Now().Add(-time.Minute)
	t.Run("missing", func(t *testing.T) {
		l := newAgyStallLog(filepath.Join(t.TempDir(), "absent"), started)
		if got, reason := l.verdict("s1", b); got != agyStallUnknown || reason == "" {
			t.Fatalf("missing log = %v (%q)", got, reason)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		path := writeAgyLog(t, dir, "acp-worker-x.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogHeader(b.Add(-30*time.Second), 3)+agyGlogPrompt(b, 1, "s1"), time.Now())
		if got, _ := l.verdict("s1", b); got != agyStallHeaderless {
			t.Fatalf("readable log = %v, want headerless", got)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if got, reason := l.verdict("s1", b); got != agyStallUnknown || !strings.Contains(reason, "open agy log") {
			t.Fatalf("unreadable log = %v (%q)", got, reason)
		}
	})
	t.Run("garbled", func(t *testing.T) {
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		writeAgyLog(t, dir, "acp-worker-x.log", agyGlogUserMessage(b, 1, "s1")+"\x00\x00garbage\n", time.Now())
		// Its only glog line is after the boundary minus the margin.
		if got, _ := l.verdict("s1", b); got != agyStallUnknown {
			t.Fatalf("garbled log = %v, want unknown", got)
		}
	})
	t.Run("no main header from this process yet", func(t *testing.T) {
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		path := writeAgyLog(t, dir, "acp-worker-x.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogPrompt(b, 1, "s1")+agyGlogMonitor(b.Add(time.Second), 7)+agyGlogHeader(b.Add(time.Second), 7), time.Now())
		if got, reason := l.verdict("s1", b); got != agyStallUnknown || !strings.Contains(reason, "no main request header") {
			t.Fatalf("monitor headers only = %v (%q), want unknown", got, reason)
		}
		// A main header in a later tail enables cuts from then on, even
		// once it has scrolled out of the window or the tail.
		b2 := b.Add(time.Minute)
		writeAgyLog(t, dir, "acp-worker-x.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogHeader(b.Add(10*time.Second), 9)+agyGlogRequest(b2, 1, "session/prompt")+agyGlogMonitor(b2.Add(time.Second), 7)+agyGlogHeader(b2.Add(time.Second), 7), time.Now())
		if got, reason := l.verdict("s1", b2); got != agyStallHeaderless {
			t.Fatalf("after a main header = %v (%q), want headerless", got, reason)
		}
		b3 := b.Add(2 * time.Minute)
		if err := os.WriteFile(path, []byte(agyGlogStart(b2, 1)+agyGlogRequest(b3, 1, "session/prompt")+agyGlogMonitor(b3.Add(time.Second), 7)+agyGlogHeader(b3.Add(time.Second), 7)), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, reason := l.verdict("s1", b3); got != agyStallHeaderless {
			t.Fatalf("main header no longer in the tail = %v (%q), want headerless", got, reason)
		}
	})
	t.Run("worker replaced after the log was cached", func(t *testing.T) {
		// Worker A serves the first request; its log is located and cached.
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		writeAgyLog(t, dir, "acp-worker-20261122_115900-100.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogPrompt(b, 1, "s1")+agyGlogHeader(b.Add(time.Second), 3), time.Now())
		if got, reason := l.verdict("s1", b); got != agyStallHeader {
			t.Fatalf("worker A's request = %v (%q), want header", got, reason)
		}
		// A stops; worker B, under the same front process, takes a fresh
		// prompt and logs its main header in its own file.
		b2 := b.Add(5 * time.Minute)
		writeAgyLog(t, dir, "acp-worker-20261122_120400-200.log", agyGlogStart(b2.Add(-time.Minute), 1)+agyGlogPrompt(b2, 1, "s1")+agyGlogHeader(b2.Add(time.Second), 4), time.Now())
		if got, reason := l.verdict("s1", b2); got != agyStallUnknown || !strings.Contains(reason, "no session/prompt request line") {
			t.Fatalf("cached predecessor log for B's request = %v (%q), want unknown", got, reason)
		}
	})
	t.Run("worker replaced just before the request", func(t *testing.T) {
		// Worker A serves the first request; its log is located and cached.
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		aName := "acp-worker-20261122_115900-100.log"
		aFirst := agyGlogStart(b.Add(-time.Minute), 1) + agyGlogPrompt(b, 1, "s1") + agyGlogHeader(b.Add(time.Second), 3)
		writeAgyLog(t, dir, aName, aFirst, time.Now())
		if got, reason := l.verdict("s1", b); got != agyStallHeader {
			t.Fatalf("worker A's request = %v (%q), want header", got, reason)
		}
		// A's last main header is 20s before B's boundary and A still logs
		// an ordinary line 1s before it, inside the skew window; B takes
		// the prompt and logs its main header in its own file.
		b2 := b.Add(5 * time.Minute)
		aLater := aFirst + agyGlogHeader(b2.Add(-20*time.Second), 3) + agyGlog(b2.Add(-time.Second), 4, "run.go:10", "shutting down")
		writeAgyLog(t, dir, aName, aLater, time.Now())
		writeAgyLog(t, dir, "acp-worker-20261122_120400-200.log", agyGlogStart(b2.Add(-time.Minute), 1)+agyGlogPrompt(b2, 1, "s1")+agyGlogHeader(b2.Add(time.Second), 4), time.Now())
		if got, reason := l.verdict("s1", b2); got != agyStallUnknown || !strings.Contains(reason, "no session/prompt request line") {
			t.Fatalf("predecessor log current to the window = %v (%q), want unknown", got, reason)
		}
		// A received an earlier prompt 1s before B's boundary (inside the
		// skew margin) that ended with no header; B serves the current
		// prompt in its own file. A's exact request record is not this
		// request's.
		aEarlierPrompt := aFirst + agyGlogHeader(b2.Add(-20*time.Second), 3) + agyGlogRequest(b2.Add(-time.Second), 2, "session/prompt")
		writeAgyLog(t, dir, aName, aEarlierPrompt, time.Now())
		if got, reason := l.verdict("s1", b2); got != agyStallUnknown || !strings.Contains(reason, "no session/prompt request line") {
			t.Fatalf("predecessor's earlier prompt inside the skew margin = %v (%q), want unknown", got, reason)
		}
		// Had A itself received the prompt, its request line attributes
		// the tail to the current request: no header since is headerless.
		writeAgyLog(t, dir, aName, aLater+agyGlogRequest(b2, 2, "session/prompt"), time.Now())
		if got, reason := l.verdict("s1", b2); got != agyStallHeaderless {
			t.Fatalf("A's own request with no header = %v (%q), want headerless", got, reason)
		}
	})
	t.Run("locates once", func(t *testing.T) {
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		got, first := l.verdict("s1", b)
		if got != agyStallUnknown {
			t.Fatalf("empty dir = %v", got)
		}
		// The new log would read headerless; a second firing keeps the
		// first locate failure instead of scanning again.
		writeAgyLog(t, dir, "acp-worker-x.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogHeader(b.Add(-30*time.Second), 3)+agyGlogPrompt(b, 1, "s1"), time.Now())
		if got, reason := l.verdict("s1", b); got != agyStallUnknown || reason != first {
			t.Fatalf("a second firing scanned again: %v (%q), want unknown (%q)", got, reason, first)
		}
	})
}

// readAgyLogTail keeps the last limit bytes and reports the clip.
func TestReadAgyLogTailBounded(t *testing.T) {
	dir := t.TempDir()
	path := writeAgyLog(t, dir, "acp-worker-x.log", strings.Repeat("a", 100)+"\nlast\n", time.Now())
	tail, clipped, err := readAgyLogTail(path, 10)
	if err != nil || !clipped || string(tail) != "aaaa\nlast\n" {
		t.Fatalf("tail = %q clipped=%v err=%v", tail, clipped, err)
	}
	tail, clipped, err = readAgyLogTail(path, 1<<20)
	if err != nil || clipped || len(tail) != 106 {
		t.Fatalf("whole = %d clipped=%v err=%v", len(tail), clipped, err)
	}
}
