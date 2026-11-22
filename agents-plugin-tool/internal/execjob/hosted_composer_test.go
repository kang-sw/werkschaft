package execjob

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHostedLaunchWindowOverridesTheDaemonWindow: a launch's own window
// replaces the daemon-wide one, so a composer run converts at its 10 s
// while the MCP default stays; the default window still applies without it.
func TestHostedLaunchWindowOverridesTheDaemonWindow(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	start := time.Now()
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "slow", Window: 30 * time.Millisecond})
	if err != nil || v.Outcome != OutcomeConverted {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the override did not shorten the wait: %s", time.Since(start))
	}
	if HostedWindow != 30*time.Second {
		t.Fatalf("the MCP default window changed: %s", HostedWindow)
	}
	// Without an override the daemon's window rules.
	fast := newHostedFixture(t, 40*time.Millisecond)
	if v, err := fast.launch(context.Background(), nil, HostedLaunch{Display: "slow"}); err != nil || v.Outcome != OutcomeConverted {
		t.Fatalf("default window launch = %+v, %v", v, err)
	}
}

// TestHostedUserApprovedLaunchAsksNoApproval: an explicit user launch is its
// own approval; the agent's approval and live-mode checks are not asked, and
// an ordinary launch beside it still is.
func TestHostedUserApprovedLaunchAsksNoApproval(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.sessions.immediate = func(string) (bool, error) { return false, nil }
	fx.sessions.approve = func(context.Context, string, HostedCall) (bool, error) {
		return false, context.DeadlineExceeded
	}
	fx.holders.onSpawn = func(id string) { go fx.holders.exit(id, 0) }
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "ls", UserApproved: true})
	if err != nil || v.Outcome != OutcomeDone {
		t.Fatalf("user-approved launch = %+v, %v", v, err)
	}
	fx.sessions.mu.Lock()
	asked, checked := len(fx.sessions.approvals), fx.sessions.immChecks
	fx.sessions.mu.Unlock()
	if asked != 0 || checked != 0 {
		t.Fatalf("approvals asked %d, live-mode checks %d; want none", asked, checked)
	}
	// The agent's own launch keeps asking.
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "ls"}); err == nil {
		t.Fatal("an ordinary launch skipped its approval")
	}
}

// TestHostedLaunchRefusesAStaleGeneration: a request that decided under an
// older open generation is refused at admission, including generation 0.
func TestHostedLaunchRefusesAStaleGeneration(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.holders.onSpawn = func(id string) { go fx.holders.exit(id, 0) }
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "x", CheckGen: true, ExpectGen: 2}); err == nil || !strings.Contains(err.Error(), "reopened") {
		t.Fatalf("stale generation launch error = %v", err)
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "x", CheckGen: true, ExpectGen: 0}); err == nil {
		t.Fatal("generation 0 passed against open generation 3")
	}
	if v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "x", CheckGen: true, ExpectGen: 3}); err != nil || v.Gen != 3 {
		t.Fatalf("current generation launch = %+v, %v", v, err)
	}
}

// TestHostedOnConvertRunsBeforeTheNotice: the converted job's id reaches the
// caller before the job can owe a notice, even when it ends the instant it
// converts; a job that ends inside the window never calls it.
func TestHostedOnConvertRunsBeforeTheNotice(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	var mu sync.Mutex
	var seen []string
	var noticesAtConvert int
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "slow", Window: 20 * time.Millisecond, OnConvert: func(v HostedView) {
		mu.Lock()
		defer mu.Unlock()
		noticesAtConvert = len(fx.sessions.noticeList())
		seen = append(seen, v.ID)
		// The job ends the moment its id is out.
		fx.holders.exit(v.HolderID, 0)
	}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(seen) != 1 || noticesAtConvert != 0 {
		t.Fatalf("OnConvert ids %v, notices at the call %d", seen, noticesAtConvert)
	}
	mu.Unlock()
	// The end won the race: exactly one outcome, never a notice beside an
	// inline result.
	if v.Outcome == OutcomeDone {
		time.Sleep(50 * time.Millisecond)
		if n := fx.sessions.noticeList(); len(n) != 0 {
			t.Fatalf("a done outcome also sent notices: %+v", n)
		}
		return
	}
	eventuallyExec(t, "the one notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := fx.sessions.noticeList(); len(n) != 1 {
		t.Fatalf("notices = %+v", n)
	}

	// An end inside the window converts nothing.
	fx2 := newHostedFixture(t, 5*time.Second)
	fx2.holders.onSpawn = func(id string) { go fx2.holders.exit(id, 0) }
	called := false
	if v, err := fx2.launch(context.Background(), nil, HostedLaunch{Display: "quick", OnConvert: func(HostedView) { called = true }}); err != nil || v.Outcome != OutcomeDone || called {
		t.Fatalf("quick launch = %+v, %v, converted=%v", v, err, called)
	}
}

// TestHostedRetainedIsTheWholeCombinedOutput: the retained snapshot is the
// whole combined range with its byte count, not the preview's last bytes.
func TestHostedRetainedIsTheWholeCombinedOutput(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	big := strings.Repeat("x", 10000)
	fx.holders.onSpawn = func(id string) {
		go func() {
			fx.holders.out(id, "stdout", big)
			fx.holders.out(id, "stderr", "err\n")
			fx.holders.exit(id, 0)
		}()
	}
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "big"})
	if err != nil || v.Outcome != OutcomeDone {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	_, got, err := fx.h.Retained("lead", v.ID)
	if err != nil || got.Written != int64(len(big)+4) || got.Start != 0 || string(got.Data) != big+"err\n" {
		t.Fatalf("retained = %d bytes (written %d, start %d), %v", len(got.Data), got.Written, got.Start, err)
	}
	if _, _, err := fx.h.Retained("other", v.ID); err == nil {
		t.Fatal("another owner read the output")
	}
}
