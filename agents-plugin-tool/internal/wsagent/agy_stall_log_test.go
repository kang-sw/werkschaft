package wsagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic agy worker log lines in agy's glog shape. No real log content.

func agyGlog(at time.Time, goroutine int, file, msg string) string {
	at = at.Local()
	return fmt.Sprintf("I%02d%02d %02d:%02d:%02d.%06d %7d %s] %s\n", int(at.Month()), at.Day(), at.Hour(), at.Minute(), at.Second(), at.Nanosecond()/1000, goroutine, file, msg)
}

func agyGlogStart(at time.Time, goroutine int) string {
	return agyGlog(at, goroutine, "server.go:100", "agent ready")
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
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(-10*time.Second), 9),
			want: agyStallHeaderless,
		},
		{
			name: "only a monitor header after the boundary",
			tail: agyGlogStart(before, 1) + agyGlogMonitor(b.Add(time.Second), 11) + agyGlogHeader(b.Add(3*time.Second), 11),
			want: agyStallHeaderless,
		},
		{
			name: "monitor marker later in the tail still marks its goroutine",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(3*time.Second), 11) + agyGlogMonitor(b.Add(4*time.Second), 11),
			want: agyStallHeaderless,
		},
		{
			name: "monitor header whose marker is not in the tail counts as main",
			tail: agyGlogStart(before, 1) + agyGlogHeader(b.Add(3*time.Second), 11),
			want: agyStallHeader,
		},
		{
			name: "continuation lines are ignored",
			tail: agyGlogStart(before, 1) + "  a wrapped continuation streamGenerateContent\n" + agyGlogHeader(b.Add(-20*time.Second), 9) + "not glog\n",
			want: agyStallHeaderless,
		},
		{
			name:   "tail starting after the boundary minus the skew margin",
			tail:   agyGlogStart(b.Add(-time.Second), 1) + agyGlogHeader(b.Add(-time.Second), 7) + agyGlogMonitor(b, 7),
			want:   agyStallUnknown,
			reason: "starts after",
		},
		{
			name:   "covered glog tail with no recognized header",
			tail:   agyGlogStart(before, 1) + agyGlogUserMessage(b, 2, "s1") + agyGlog(b.Add(time.Second), 3, "run.go:10", "working"),
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
			got, reason := parseAgyStallTail([]byte(tc.tail), false, b, agyStallSkew)
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
	tail := partial + agyGlogStart(b.Add(-time.Minute), 1) + agyGlogHeader(b.Add(-30*time.Second), 9)
	if got, reason := parseAgyStallTail([]byte(tail), true, b, agyStallSkew); got != agyStallHeaderless {
		t.Fatalf("clipped tail verdict = %v (%q), want headerless", got, reason)
	}
	if got, _ := parseAgyStallTail([]byte("no newline at all"), true, b, agyStallSkew); got != agyStallUnknown {
		t.Fatalf("clipped tail with no complete line = %v, want unknown", got)
	}
}

// A valid tail never validates a later one: format drift in the next read
// is unknown even though the earlier read was verified.
func TestParseAgyStallTailFormatDriftAfterValidTail(t *testing.T) {
	b := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	first := agyGlogStart(b.Add(-time.Minute), 1) + agyGlogMonitor(b.Add(-50*time.Second), 7) + agyGlogHeader(b.Add(-50*time.Second), 7)
	if got, reason := parseAgyStallTail([]byte(first), false, b, agyStallSkew); got != agyStallHeaderless {
		t.Fatalf("first tail = %v (%q), want headerless", got, reason)
	}
	b2 := b.Add(3 * time.Minute)
	drifted := agyGlogStart(b2.Add(-time.Minute), 1) + agyGlog(b2.Add(-50*time.Second), 7, "http_helpers.go:760", "Request: https://model.example.invalid/v1internal:streamGenerateContent?alt=sse")
	if got, reason := parseAgyStallTail([]byte(drifted), false, b2, agyStallSkew); got != agyStallUnknown {
		t.Fatalf("drifted tail = %v (%q), want unknown", got, reason)
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
		path := writeAgyLog(t, dir, "acp-worker-x.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogUserMessage(b, 1, "s1")+agyGlogHeader(b.Add(-30*time.Second), 3), time.Now())
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
	t.Run("locates once", func(t *testing.T) {
		dir := t.TempDir()
		l := newAgyStallLog(dir, started)
		if got, _ := l.verdict("s1", b); got != agyStallUnknown {
			t.Fatalf("empty dir = %v", got)
		}
		writeAgyLog(t, dir, "acp-worker-x.log", agyGlogStart(b.Add(-time.Minute), 1)+agyGlogUserMessage(b, 1, "s1")+agyGlogHeader(b.Add(-30*time.Second), 3), time.Now())
		if got, _ := l.verdict("s1", b); got != agyStallUnknown {
			t.Fatalf("a second firing scanned again: %v", got)
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
