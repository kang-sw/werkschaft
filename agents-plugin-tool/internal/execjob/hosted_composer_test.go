package execjob

import (
	"context"
	"errors"
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

// TestHostedConvertCommitsOneOutcome: the conversion outcome is decided once.
// A job still running when the window passes is converted for good even when
// it ends while its id is being published: the id reaches the caller first
// (no notice yet), then exactly one notice follows, and the view says
// converted. A job that ended before the decision is done: no id is
// published and no notice is owed.
func TestHostedConvertCommitsOneOutcome(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	var mu sync.Mutex
	var seen []string
	var noticesAtConvert int
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "slow", Window: 20 * time.Millisecond, OnConvert: func(v HostedView) error {
		mu.Lock()
		defer mu.Unlock()
		noticesAtConvert = len(fx.sessions.noticeList())
		seen = append(seen, v.ID)
		// The job ends the moment its id is out, and its notice cannot
		// be admitted before this call returns.
		fx.holders.exit(v.HolderID, 0)
		time.Sleep(30 * time.Millisecond)
		if n := len(fx.sessions.noticeList()); n != 0 {
			noticesAtConvert = n
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Outcome != OutcomeConverted || v.Terminal() {
		t.Fatalf("the committed outcome = %v terminal=%v, want converted and the commit-time running view", v.Outcome, v.Terminal())
	}
	mu.Lock()
	if len(seen) != 1 || noticesAtConvert != 0 {
		t.Fatalf("OnConvert ids %v, notices admitted before the id was out %d", seen, noticesAtConvert)
	}
	mu.Unlock()
	eventuallyExec(t, "the one notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := fx.sessions.noticeList(); len(n) != 1 || n[0].event != "exec:"+v.ID+":exit" {
		t.Fatalf("notices = %+v", n)
	}

	// The end wins before the decision: done, and nothing is published or owed.
	fx2 := newHostedFixture(t, 5*time.Second)
	fx2.holders.onSpawn = func(id string) { go fx2.holders.exit(id, 0) }
	called := false
	if v, err := fx2.launch(context.Background(), nil, HostedLaunch{Display: "quick", OnConvert: func(HostedView) error { called = true; return nil }}); err != nil || v.Outcome != OutcomeDone || called {
		t.Fatalf("quick launch = %+v, %v, converted=%v", v, err, called)
	}
	time.Sleep(50 * time.Millisecond)
	if n := fx2.sessions.noticeList(); len(n) != 0 {
		t.Fatalf("a done outcome sent notices: %+v", n)
	}
}

// TestHostedConvertEndAfterTheViewKeepsOneNotice: an end that follows a
// converted outcome (after the view was taken) sends the one notice, after
// the id was published.
func TestHostedConvertEndAfterTheViewKeepsOneNotice(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	var order []string
	var mu sync.Mutex
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "slow", Window: 20 * time.Millisecond, OnConvert: func(v HostedView) error {
		mu.Lock()
		order = append(order, "id:"+v.ID)
		mu.Unlock()
		return nil
	}})
	if err != nil || v.Outcome != OutcomeConverted {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	fx.holders.exit(v.HolderID, 0)
	eventuallyExec(t, "the notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || order[0] != "id:"+v.ID {
		t.Fatalf("published %v", order)
	}
	if n := fx.sessions.noticeList(); len(n) != 1 {
		t.Fatalf("notices = %+v", n)
	}
}

// TestHostedGenerationIsEnforcedAtRegistration: the generation a request was
// made under is checked again after the job is registered, so a close and
// revival that completes between the owner read and the admission (the
// fence lifted again) leaves nothing running on the new generation. Both
// interleavings start at generation 0.
func TestHostedGenerationIsEnforcedAtRegistration(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook int // the Owner call after which the conversation is revived
	}{
		{"before the first read", 1},
		{"between the early check and the registration", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newHostedFixture(t, 5*time.Second)
			fx.sessions.mu.Lock()
			fx.sessions.owners["lead"] = HostedOwner{ID: "lead", Gen: 0, Lead: true}
			fx.sessions.mu.Unlock()
			fx.sessions.ownerHook = func(call int) {
				if call == tc.hook {
					fx.sessions.mu.Lock()
					fx.sessions.owners["lead"] = HostedOwner{ID: "lead", Gen: 1, Lead: true}
					fx.sessions.mu.Unlock()
				}
			}
			fx.holders.onSpawn = func(id string) { go fx.holders.exit(id, 0) }
			_, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "x", CheckGen: true, ExpectGen: 0, UserApproved: true})
			if err == nil || !strings.Contains(err.Error(), "reopened") {
				t.Fatalf("a request made under generation 0 ran on generation 1: %v", err)
			}
			fx.holders.mu.Lock()
			spawned := len(fx.holders.spawned)
			fx.holders.mu.Unlock()
			if spawned != 0 {
				t.Fatalf("%d processes started on the revived generation", spawned)
			}
		})
	}
	// A request that names no generation keeps the one it read: a launch
	// that raced a revival is refused too, and a quiet one runs.
	fx := newHostedFixture(t, 5*time.Second)
	fx.sessions.mu.Lock()
	fx.sessions.owners["lead"] = HostedOwner{ID: "lead", Gen: 0, Lead: true}
	fx.sessions.mu.Unlock()
	fx.holders.onSpawn = func(id string) { go fx.holders.exit(id, 0) }
	if v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "x", UserApproved: true}); err != nil || v.Gen != 0 {
		t.Fatalf("quiet generation 0 launch = %+v, %v", v, err)
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

// TestHostedConvertIsNotNoticeEligibleUntilPublished: until OnConvert returns,
// neither the live job nor its stored row owes a notice, at any moment of the
// publication, including one in which the job ended; a daemon that goes away
// there restores a job that owes none. Once the id is out the stored row owes
// the notice and the one notice goes. A failed publication leaves the
// ordinary notice as the one delivery.
func TestHostedConvertIsNotNoticeEligibleUntilPublished(t *testing.T) {
	for _, fail := range []bool{false, true} {
		fx := newHostedFixture(t, 5*time.Second)
		var during []HostedJobRow
		var restored []HostedJobRow
		var noticesDuring int
		v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "slow", Window: 20 * time.Millisecond, OnConvert: func(v HostedView) error {
			rows, _ := fx.store.Load()
			during = append(during, rows...)
			// The job ends while its id is being published: the end's row is
			// stored too.
			fx.holders.exit(v.HolderID, 0)
			eventuallyExec(t, "the end stored", func() bool {
				rows, _ := fx.store.Load()
				return len(rows) == 1 && rows[0].Terminal()
			})
			rows, _ = fx.store.Load()
			during = append(during, rows...)
			// A daemon restarting right here reads exactly these rows.
			restored = append(restored, rows...)
			noticesDuring = len(fx.sessions.noticeList())
			if fail {
				return errors.New("the conversation reopened")
			}
			return nil
		}})
		if err != nil || v.Outcome != OutcomeConverted {
			t.Fatalf("launch = %+v, %v", v, err)
		}
		if len(during) != 2 {
			t.Fatalf("stored rows during publication = %d", len(during))
		}
		for _, r := range append(during, restored...) {
			if r.Notify || r.Notice != NoticeNone {
				t.Fatalf("the stored row owed a notice before the id was published (fail=%v): %+v", fail, r)
			}
		}
		if noticesDuring != 0 {
			t.Fatalf("a notice was admitted before publication returned (fail=%v)", fail)
		}
		eventuallyExec(t, "the one notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
		eventuallyExec(t, "the delivered row stored", func() bool {
			rows, _ := fx.store.Load()
			return len(rows) == 1 && rows[0].Notify && rows[0].Notice == NoticeDelivered
		})
	}
}
