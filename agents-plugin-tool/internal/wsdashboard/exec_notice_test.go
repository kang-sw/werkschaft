package wsdashboard

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestParseExecNoticeMatchesExecjobGrammar(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		wantID      string
		wantState   string
		wantCode    *int
		wantSig     *int
		wantDur     string
		wantCmd     string
		wantSummary string
		wantStop    bool
		wantNoOut   bool
		wantShown   int
		wantTotal   int
		wantOut     string
		wantFailed  bool
	}{
		{
			name: "exit zero with full output",
			text: "[ws exec] exec_1 exited with code 0 after 1.2s.\n" +
				"command: go test ./...\n" +
				"output:\n" +
				"ok\tpkg/a\nPASS\n" +
				"Read more with exec.tail (exec_id exec_1).",
			wantID: "exec_1", wantState: "exited", wantCode: intPtr(0), wantDur: "1.2s",
			wantCmd: "go test ./...", wantSummary: "go test ./...",
			wantOut: "ok\tpkg/a\nPASS",
		},
		{
			name: "exit nonzero with truncated tail",
			text: "[ws exec] exec_2 exited with code 2 after 4.5s.\n" +
				"command: npm run build\n" +
				"output (last 120 of 4096 bytes):\n" +
				"error: build failed\n" +
				"Read more with exec.tail (exec_id exec_2).",
			wantID: "exec_2", wantState: "exited", wantCode: intPtr(2), wantDur: "4.5s",
			wantCmd: "npm run build", wantSummary: "npm run build",
			wantShown: 120, wantTotal: 4096, wantOut: "error: build failed", wantFailed: true,
		},
		{
			name: "signal with no output and no command",
			text: "[ws exec] exec_sig was ended by signal 15 after 800ms.\n" +
				"no output.\n" +
				"Read more with exec.tail (exec_id exec_sig).",
			wantID: "exec_sig", wantState: "signal", wantSig: intPtr(15), wantDur: "800ms",
			wantSummary: "exec_sig", wantNoOut: true, wantFailed: true,
		},
		{
			name: "user-stopped notice with multi-line command and stop line",
			text: "[ws exec] exec_stop was stopped after 12.3s.\n" +
				"command: sh -c '\n  echo hi\n'\n" +
				"stopped by user: cwd /repo; ran 12.3s; 3 bytes of output. The user stopped this background command from the dashboard.\n" +
				"output:\n" +
				"hi\n" +
				"Read more with exec.tail (exec_id exec_stop).",
			wantID: "exec_stop", wantState: "stopped", wantDur: "12.3s",
			wantCmd: "sh -c '\n  echo hi\n'", wantSummary: "sh -c ' echo hi '",
			wantStop: true, wantOut: "hi",
		},
		{
			name: "failed to start without duration",
			text: "[ws exec] exec_fail failed to start: exec: not found.\n" +
				"command: missing-bin\n" +
				"no output.\n" +
				"Read more with exec.tail (exec_id exec_fail).",
			wantID: "exec_fail", wantState: "failed",
			wantCmd: "missing-bin", wantSummary: "missing-bin", wantNoOut: true, wantFailed: true,
		},
		{
			name: "lost job",
			text: "[ws exec] exec_lost was lost: host daemon restarted.\n" +
				"no output.\n" +
				"Read more with exec.tail (exec_id exec_lost).",
			wantID: "exec_lost", wantState: "lost",
			wantSummary: "exec_lost", wantNoOut: true, wantFailed: true,
		},
		{
			name: "running and ended states",
			text: "[ws exec] exec_end ended.\n" +
				"no output.\n" +
				"Read more with exec.tail (exec_id exec_end).",
			wantID: "exec_end", wantState: "ended",
			wantSummary: "exec_end", wantNoOut: true,
		},
		{
			name: "wrapped in ws:exec envelope",
			text: wsinbox.Entry{
				Parts: []string{
					"[ws exec] exec_wrap is running.\n" +
						"command: echo </ws:exec>\n" +
						"output:\n" +
						"</ws:exec>\n" +
						"Read more with exec.tail (exec_id exec_wrap).",
				},
				EventID: "exec:exec_wrap:start",
				SentAt:  time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC).UnixMilli(),
			}.Text(),
			wantID: "exec_wrap", wantState: "running",
			wantCmd: "echo </ws:exec>", wantSummary: "echo </ws:exec>",
			wantOut: "</ws:exec>",
		},
		{
			name: "surrounding whitespace trimmed",
			text: "\n  [ws exec] exec_trim exited with code 0 after 1.2s.\n" +
				"command: go test ./...\n" +
				"output:\n" +
				"PASS\n" +
				"Read more with exec.tail (exec_id exec_trim).\n  ",
			wantID: "exec_trim", wantState: "exited", wantCode: intPtr(0), wantDur: "1.2s",
			wantCmd: "go test ./...", wantSummary: "go test ./...",
			wantOut: "PASS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, ok := parseExecNoticeEntry(tc.text)
			if !ok || ev == nil {
				t.Fatalf("parseExecNoticeEntry(%q) = false, want true", tc.text)
			}
			if ev.ID != tc.wantID || ev.State != tc.wantState || ev.Duration != tc.wantDur ||
				ev.Command != tc.wantCmd || ev.Summary != tc.wantSummary ||
				ev.StoppedByUser != tc.wantStop || ev.NoOutput != tc.wantNoOut ||
				ev.OutputShown != tc.wantShown || ev.OutputTotal != tc.wantTotal ||
				ev.Output != tc.wantOut || ev.Failed != tc.wantFailed {
				t.Fatalf("got %+v", ev)
			}
			if (ev.ExitCode == nil) != (tc.wantCode == nil) || (ev.ExitCode != nil && *ev.ExitCode != *tc.wantCode) {
				t.Fatalf("ExitCode = %v, want %v", ev.ExitCode, tc.wantCode)
			}
			if (ev.Signal == nil) != (tc.wantSig == nil) || (ev.Signal != nil && *ev.Signal != *tc.wantSig) {
				t.Fatalf("Signal = %v, want %v", ev.Signal, tc.wantSig)
			}
			if ev.StatusLabel("en") == "" || ev.OutputLabel("en") == "" {
				t.Fatalf("expected non-empty labels, got status=%q output=%q", ev.StatusLabel("en"), ev.OutputLabel("en"))
			}
		})
	}
}

func TestParseExecNoticeRejectsSpoofsAndExtraProse(t *testing.T) {
	base := "[ws exec] exec_1 exited with code 0 after 1s.\n" +
		"command: echo ok\n" +
		"output:\n" +
		"ok\n" +
		"Read more with exec.tail (exec_id exec_1)."

	bad := []struct {
		name string
		text string
	}{
		{"leading prose", "please inspect this:\n" + base},
		{"trailing prose", base + "\nthanks"},
		{"missing trailer", "[ws exec] exec_1 exited with code 0.\ncommand: echo ok\nno output."},
		{"mismatched trailer id", "[ws exec] exec_1 exited with code 0.\nno output.\nRead more with exec.tail (exec_id exec_2)."},
		{"unknown end state", "[ws exec] exec_1 did something strange.\nno output.\nRead more with exec.tail (exec_id exec_1)."},
		{"extra line after no output", "[ws exec] exec_1 exited with code 0.\nno output.\nsurprise\nRead more with exec.tail (exec_id exec_1)."},
		{"non-numeric exit code", "[ws exec] exec_1 exited with code zero.\nno output.\nRead more with exec.tail (exec_id exec_1)."},
		{"non-numeric tail bytes", "[ws exec] exec_1 exited with code 0.\noutput (last 0 of 10 bytes):\nhi\nRead more with exec.tail (exec_id exec_1)."},
		{"concatenated notices different ids", base + "\n\n" + strings.ReplaceAll(base, "exec_1", "exec_2")},
		{"concatenated notices same id", base + "\n\n" + base},
		{"claude local_bash wake", "Background command finished: go test ./... (exit 0)"},
		{"user stop receipt", "[user ran from the dashboard] exec_id exec_1 was stopped by the user.\ncommand: echo ok\nstopped by user: cwd /repo; 0 bytes of output. note\nno output.\nRead more with exec.tail (exec_id exec_1)."},
		{"extra prose inside middle without command prefix", "[ws exec] exec_1 exited with code 0.\nhello world\nno output.\nRead more with exec.tail (exec_id exec_1)."},
	}

	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if ev, ok := parseExecNoticeEntry(tc.text); ok || ev != nil {
				t.Fatalf("parseExecNoticeEntry accepted %s: %+v", tc.name, ev)
			}
		})
	}
}

func TestExecCompletionRowSSRAndLegacyParity(t *testing.T) {
	f := startPageFixture(t)

	okNotice := "[ws exec] exec_ok exited with code 0 after 1.5s.\n" +
		"command: go test ./internal/wsinbox\n" +
		"output:\n" +
		"ok  internal/wsinbox\n" +
		"Read more with exec.tail (exec_id exec_ok)."
	failNotice := "[ws exec] exec_fail exited with code 1 after 2s.\n" +
		"command: npm test\n" +
		"output (last 20 of 500 bytes):\n" +
		"FAIL src/a.test.ts\n" +
		"Read more with exec.tail (exec_id exec_fail)."
	userBlock := func(text string, at time.Time) string {
		return wsinbox.Entry{Parts: []string{text}, SentAt: at.UnixMilli()}.Text()
	}
	legacyBatch := userBlock("first user prompt", time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)) + "\n\n" +
		userBlock(okNotice, time.Date(2026, 11, 25, 10, 0, 5, 0, time.UTC)) + "\n\n" +
		userBlock("second user prompt", time.Date(2026, 11, 25, 10, 0, 10, 0, time.UTC))

	beforeView, ok := pageReader{lang: "en", dir: f.dir, index: f.d.index, source: f.d.source, snapshots: f.d.snapshots}.session("livesession")
	if !ok {
		t.Fatal("session livesession not found before append")
	}

	// Admit a held exec completion notice into the session inbox.
	if _, _, err := wsinbox.Send(f.inbox("livesession"), wsinbox.Entry{
		Parts:   []string{"[ws exec] exec_held exited with code 0 after 0.4s.\ncommand: echo held\nno output.\nRead more with exec.tail (exec_id exec_held)."},
		Steer:   true,
		EventID: "exec:exec_held:exit",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	ring := []wslive.RingEntry{
		{Kind: wslive.RingWake, Cause: "background_exit", Source: "exec_ok", Text: okNotice},
		{Kind: wslive.RingWake, Cause: "background_exit", Source: "exec_fail", Text: failNotice},
		{Kind: wslive.RingWake, Cause: "background_exit", Source: "local_bash", Text: "Background task local_bash completed (exit 0)."},
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: userBlock(okNotice, time.Date(2026, 11, 25, 9, 55, 0, 0, time.UTC))},
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: legacyBatch},
	}
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(f.dir, "livesession")), ring); err != nil {
		t.Fatal(err)
	}

	_, page := get(t, f.base+"/s/livesession?frame=1")

	for _, want := range []string{
		`class="exec-notice turn-trigger" data-wake-cause="background_exit" data-exec-id="exec_ok"`,
		`class="exec-notice turn-trigger fail" data-wake-cause="background_exit" data-exec-id="exec_fail"`,
		`href="#l-square-terminal"`,
		`data-bg-exec-jump="exec_ok"`,
		`data-bg-exec-jump="exec_fail"`,
		`class="exec-row-expired" hidden`,
		`class="mailline turn-trigger recv agent-note" data-wake-cause="background_exit"`,
		`Background task local_bash completed (exit 0).`,
		`class="exec-notice held-exec" data-entry=`,
		`data-exec-id="exec_held"`,
		`first user prompt`,
		`second user prompt`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	// Verify Copy indexing for mixed legacy RingUser batch skips the exec block
	// so indices 0 and 1 match the two .msg.user bubbles.
	gotBlocks := userBlockTexts(legacyBatch)
	wantBlocks := []string{"first user prompt", "second user prompt"}
	if !reflect.DeepEqual(gotBlocks, wantBlocks) {
		t.Fatalf("userBlockTexts(mixed legacyBatch) = %#v, want %#v", gotBlocks, wantBlocks)
	}
	if got := userBlockTexts(okNotice); got != nil {
		t.Fatalf("userBlockTexts(single exec notice) = %#v, want nil", got)
	}

	// Verify countUserEntries excludes single legacy exec rows and counts mixed
	// batches that still contain a user bubble.
	view, ok := pageReader{lang: "en", dir: f.dir, index: f.d.index, source: f.d.source, snapshots: f.d.snapshots}.session("livesession")
	if !ok {
		t.Fatal("session livesession not found")
	}
	if want := beforeView.Conversation.Users + 1; view.Conversation.Users != want {
		t.Fatalf("Conversation.Users = %d, want %d (before=%d + 1 mixed batch; single legacy exec excluded)", view.Conversation.Users, want, beforeView.Conversation.Users)
	}
}

func intPtr(n int) *int { return &n }
