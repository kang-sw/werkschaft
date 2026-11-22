package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsgit"
)

// occupancyFake is the daemon-side reader with its optional release notice,
// which it records.
type occupancyFake struct {
	fakeWorktreeChildren
	outcomes []wsagent.ReleasedConversation
	notices  []releaseNotice
	// observe, when set, runs inside the notice, before it returns.
	observe func(worktree string, releasedAt time.Time)
}

type releaseNotice struct {
	target      string
	releasedAt  time.Time
	cancelTurns bool
}

func (f *occupancyFake) CheckoutReleased(worktree string, releasedAt time.Time, cancelTurns bool) []wsagent.ReleasedConversation {
	f.mu.Lock()
	observe := f.observe
	f.notices = append(f.notices, releaseNotice{target: canonicalWorktreeTestPath(worktree), releasedAt: releasedAt, cancelTurns: cancelTurns})
	outcomes := append([]wsagent.ReleasedConversation(nil), f.outcomes...)
	f.mu.Unlock()
	if observe != nil {
		observe(worktree, releasedAt)
	}
	return outcomes
}

func (f *occupancyFake) noticeList() []releaseNotice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]releaseNotice(nil), f.notices...)
}

// releasedCandidate acquires and releases one pooled worktree, leaving it
// detached, clean, unleased, and with a recorded release.
func releasedCandidate(t *testing.T, s *Server, lead, base string) string {
	t.Helper()
	acq := acquireForTest(t, s, 1, lead, base, "impl/test/candidate")
	if rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": lead, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("release failed: %s", rel)
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(acq.Path); err != nil || !ok {
		t.Fatalf("a completed release must record its boundary: ok=%v err=%v", ok, err)
	}
	return acq.Path
}

// TestWorktreeAcquireSkipsAnExtantLease: a detached, clean checkout that
// still carries a lease is another holder's; acquisition creates a new one
// and leaves the original lease and checkout as they were.
func TestWorktreeAcquireSkipsAnExtantLease(t *testing.T) {
	s, base, leadA, leadB := releaseGuardServer(t)
	held := acquireForTest(t, s, 1, leadA, base, "impl/test/held")
	// The holder's checkout detaches (a worker parked it) but its lease stays.
	runGit(t, held.Path, "checkout", "--detach")
	before, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, held.Path)
	if err != nil || before == nil {
		t.Fatalf("precondition: lease %+v err=%v", before, err)
	}
	next := acquireForTest(t, s, 2, leadB, base, "impl/test/next")
	if canonicalWorktreeTestPath(next.Path) == canonicalWorktreeTestPath(held.Path) || next.Reused {
		t.Fatalf("acquisition reused a leased checkout: %+v", next)
	}
	after, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, held.Path)
	if err != nil || after == nil || *after != *before {
		t.Fatalf("the original lease must be preserved: before %+v after %+v err=%v", before, after, err)
	}
	if b := wtBranch(t, held.Path); b != "HEAD" {
		t.Fatalf("the original checkout must be untouched, branch %q", b)
	}
}

// TestWorktreeAcquireSkipsOccupiedCheckouts: on an unleased, released
// checkout, acquisition skips a current binding (direct or nested), a
// binding from before the release whose process still lives; it reuses the checkout once only a pre-release binding without a live
// process remains. A daemon that cannot answer makes it busy.
func TestWorktreeAcquireSkipsOccupiedCheckouts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		children []wsagent.BoundWorktreeChild
		reuse    bool
	}{
		{name: "current direct child", children: []wsagent.BoundWorktreeChild{{Address: "id:direct", State: wsagent.AgentWaiting}}},
		{name: "current nested grandchild", children: []wsagent.BoundWorktreeChild{
			{Address: "id:old", State: wsagent.AgentWaiting, BoundBeforeRelease: true},
			{Address: "id:grandchild", State: wsagent.AgentRunning, Live: true},
		}},
		{name: "pre-release child with a live process", children: []wsagent.BoundWorktreeChild{{Address: "id:bg", State: wsagent.AgentIdle, BoundBeforeRelease: true, Live: true}}},
		{name: "closed child whose process is still stopping", children: []wsagent.BoundWorktreeChild{{Address: "id:closing", State: wsagent.AgentClosed, Live: true}}},
		{name: "pre-release asleep child", children: []wsagent.BoundWorktreeChild{{Address: "id:asleep", State: wsagent.AgentWaiting, BoundBeforeRelease: true}}, reuse: true},
		{name: "nothing bound", reuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, leadA, leadB := releaseGuardServer(t)
			occ := &occupancyFake{}
			s.pi.worktreeChildren = occ
			candidate := releasedCandidate(t, s, leadA, base)
			occ.set(candidate, tc.children...)
			next := acquireForTest(t, s, 3, leadB, base, "impl/test/next")
			reused := canonicalWorktreeTestPath(next.Path) == canonicalWorktreeTestPath(candidate)
			if reused != tc.reuse || next.Reused != tc.reuse {
				t.Fatalf("reused = %v (result %v), want %v", reused, next.Reused, tc.reuse)
			}
			if !tc.reuse {
				if b := wtBranch(t, candidate); b != "HEAD" {
					t.Fatalf("a skipped checkout must be untouched, branch %q", b)
				}
				if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, candidate); err != nil || lease != nil {
					t.Fatalf("a skipped checkout gains no lease: %+v err=%v", lease, err)
				}
			}
		})
	}

	t.Run("daemon unreachable", func(t *testing.T) {
		sLead, root, base, lead := worktreeListServer(t)
		candidate := releasedCandidate(t, sLead, lead, base)
		daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		}))
		defer daemon.Close()
		worker := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Env: append(sLead.environ(), wsagent.EnvPiToken+"=tok", wsagent.EnvPiEndpoint+"="+daemon.URL)})
		defer worker.Close()
		next, err := acquireWorktreeWith(context.Background(), worker.gitRunner(), worker.environ(), root, base, "impl/test/next", builtinProfile(t, worktreeProfileFull), "", lead, func(path string) (string, error) {
			return sLead.sessions.mint(path, roleLead, lead)
		}, worker.worktreeBusy)
		if err != nil {
			t.Fatal(err)
		}
		if canonicalWorktreeTestPath(next.Path) == canonicalWorktreeTestPath(candidate) {
			t.Fatal("a candidate the daemon could not vouch for must be skipped")
		}
	})
}

// TestWorktreeAcquireClaimExcludesSpawnAndRelease: the per-worktree release
// lock is acquisition's admission boundary with agent.spawn. A spawn holding
// its reservation makes the candidate busy, and while acquisition holds its
// claim (paused in a post_acquire hook) a spawn into that checkout is refused.
func TestWorktreeAcquireClaimExcludesSpawnAndRelease(t *testing.T) {
	t.Run("in-flight spawn makes the candidate busy", func(t *testing.T) {
		s, base, leadA, leadB := releaseGuardServer(t)
		candidate := releasedCandidate(t, s, leadA, base)
		unlockSpawn, err := wsgit.ReserveWorktreeSpawn(candidate)
		if err != nil {
			t.Fatal(err)
		}
		next := acquireForTest(t, s, 3, leadB, base, "impl/test/next")
		unlockSpawn()
		if canonicalWorktreeTestPath(next.Path) == canonicalWorktreeTestPath(candidate) {
			t.Fatal("acquisition reused a checkout a spawn was registering a child in")
		}
	})

	t.Run("spawn refused while acquisition holds its claim", func(t *testing.T) {
		s, root, base, parent := worktreeListServer(t)
		pool := acquireTempDir(t)
		gate := filepath.Join(t.TempDir(), "claim")
		profile := hookProfile(t, "claim", nil, map[string][]map[string]any{
			worktreeHookPostAcquire: {hook([]string{os.Args[0], "-test.run=^TestWorktreeAcquireGateHook$"})},
		})
		seed, err := provisionWorktree(context.Background(), s.gitRunner(), s.environ(), root, base, "impl/test/seed", profile, pool)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := releaseWorktree(context.Background(), s.gitRunner(), s.environ(), seed.Path, pool, lookupOf(profile)); err != nil {
			t.Fatal(err)
		}
		t.Setenv("WS_TEST_ACQUIRE_HOOK_GATE", gate)
		done := make(chan error, 1)
		var acquired worktreeAcquireResult
		t.Cleanup(func() { _ = os.WriteFile(gate+".resume", nil, 0o600) })
		go func() {
			var err error
			acquired, err = acquireWithServer(context.Background(), s, s.gitRunner(), root, base, "impl/test/claimed", profile, pool, parent)
			done <- err
		}()
		waitAcquireGate(t, gate+".held")
		if unlock, err := wsgit.ReserveWorktreeSpawn(seed.Path); !errors.Is(err, wsgit.ErrWorktreeReleasing) {
			if unlock != nil {
				unlock()
			}
			t.Fatalf("spawn into a checkout being acquired = %v, want refusal", err)
		}
		if err := os.WriteFile(gate+".resume", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if canonicalWorktreeTestPath(acquired.Path) != canonicalWorktreeTestPath(seed.Path) {
			t.Fatalf("expected reuse of %s, got %s", seed.Path, acquired.Path)
		}
		unlock, err := wsgit.ReserveWorktreeSpawn(seed.Path)
		if err != nil {
			t.Fatalf("the claim must end once the lease is published: %v", err)
		}
		unlock()
	})
}

// TestWorktreeReleaseIgnoresBindingsFromBeforeTheLastRelease: release counts
// only children bound since the checkout's last completed release; earlier
// ones (the stale waiting children of earlier leases) no longer refuse it.
func TestWorktreeReleaseIgnoresBindingsFromBeforeTheLastRelease(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	occ := &occupancyFake{}
	s.pi.worktreeChildren = occ
	acq := acquireForTest(t, s, 1, leadA, base, "impl/test/stale")
	stale := []wsagent.BoundWorktreeChild{
		{Address: "id:stale01", State: wsagent.AgentWaiting, BoundBeforeRelease: true},
		{Address: "id:stale02", State: wsagent.AgentIdle, BoundBeforeRelease: true},
	}
	occ.set(acq.Path, append(stale, wsagent.BoundWorktreeChild{Address: "id:current03", State: wsagent.AgentWaiting})...)
	rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path})
	if !toolIsError(t, rel) {
		t.Fatalf("a current child must still refuse release: %s", rel)
	}
	if text := toolText(t, rel); !strings.Contains(text, "id:current03") || strings.Contains(text, "id:stale01") || strings.Contains(text, "id:stale02") {
		t.Fatalf("refusal must name only the current child: %s", text)
	}
	// A closed child listed only because its process is still stopping keeps
	// acquisition out but is no open child for release.
	occ.set(acq.Path, append(stale, wsagent.BoundWorktreeChild{Address: "id:closing04", State: wsagent.AgentClosed, Live: true})...)
	if rel := callToolOnce(t, s, 3, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("children bound before the last release, or closed, must not refuse it: %s", rel)
	}
	if b := wtBranch(t, acq.Path); b != "HEAD" {
		t.Fatalf("released worktree not detached: %q", b)
	}
	if got := occ.noticeList(); len(got) != 1 || got[0].cancelTurns || got[0].target != canonicalWorktreeTestPath(acq.Path) {
		t.Fatalf("a normal release notifies the daemon without cancelling turns: %+v", got)
	}
}

// TestWorktreeReleaseForceIsReservedToTheRootLead: a worker's (parented
// lead) key may not force; nothing changes. The root lead may.
func TestWorktreeReleaseForceIsReservedToTheRootLead(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	acq := acquireForTest(t, s, 1, leadA, base, "impl/test/force")
	draft := writeDraft(t, acq.Path)
	rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": acq.WorkerKey, "path": acq.Path, "force": true})
	if !toolIsError(t, rel) || !strings.Contains(toolText(t, rel), "root lead") || !strings.Contains(toolText(t, rel), "Nothing was changed") {
		t.Fatalf("a worker key's force must be refused: %s", rel)
	}
	if _, err := os.Stat(draft); err != nil {
		t.Fatalf("a refused force must keep uncommitted work: %v", err)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease == nil {
		t.Fatalf("a refused force must keep the lease: %+v err=%v", lease, err)
	}
	if rel := callToolOnce(t, s, 3, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path, "force": true}); toolIsError(t, rel) {
		t.Fatalf("the root lead's force failed: %s", rel)
	}
}

// TestWorktreeForcedReleaseCancelsEarlierTurnsAndReportsOutcomes: a forced
// release with a running bound child completes at once (lease removed,
// boundary recorded), asks the daemon to cancel earlier-bound turns, and
// reports each outcome; a failed interrupt is reported, and the checkout stays
// out of reuse while that child's process lives.
func TestWorktreeForcedReleaseCancelsEarlierTurnsAndReportsOutcomes(t *testing.T) {
	s, base, leadA, leadB := releaseGuardServer(t)
	occ := &occupancyFake{}
	s.pi.worktreeChildren = occ
	acq := acquireForTest(t, s, 1, leadB, base, "impl/test/forced")
	occ.set(acq.Path, wsagent.BoundWorktreeChild{Address: "id:runner01", State: wsagent.AgentRunning, Live: true})
	occ.outcomes = []wsagent.ReleasedConversation{
		{Address: "id:runner01", State: wsagent.AgentRunning, Error: "interrupt refused by backend"},
		{Address: "id:runner02", State: wsagent.AgentIdle, Cancelled: true},
	}
	before := time.Now().Add(-time.Second)
	resp := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path, "force": true, "format": "json"})
	if toolIsError(t, resp) {
		t.Fatalf("forced release with a running child failed: %s", resp)
	}
	var res worktreeReleaseResult
	if err := json.Unmarshal([]byte(toolText(t, resp)), &res); err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"could not cancel the running turn of id:runner01", "interrupt refused by backend", "keeps running", "cancelled the running turn of id:runner02"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("release outcome lacks %q: %v", want, res.Warnings)
		}
	}
	if got := occ.noticeList(); len(got) != 1 || !got[0].cancelTurns {
		t.Fatalf("a forced release asks the daemon to cancel turns: %+v", got)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease != nil {
		t.Fatalf("a forced release removes the lease at once: %+v err=%v", lease, err)
	}
	at, ok, err := wsgit.WorktreeReleasedAt(acq.Path)
	if err != nil || !ok || at.Before(before) {
		t.Fatalf("a forced release records its boundary at once: %v ok=%v err=%v", at, ok, err)
	}
	// The child's process lives (its interrupt failed): the checkout stays
	// out of reuse until it exits or sleeps.
	occ.set(acq.Path, wsagent.BoundWorktreeChild{Address: "id:runner01", State: wsagent.AgentRunning, BoundBeforeRelease: true, Live: true})
	if next := acquireForTest(t, s, 3, leadA, base, "impl/test/next"); canonicalWorktreeTestPath(next.Path) == canonicalWorktreeTestPath(acq.Path) {
		t.Fatal("acquisition reused a checkout whose earlier child still has a live process")
	}
	occ.set(acq.Path, wsagent.BoundWorktreeChild{Address: "id:runner01", State: wsagent.AgentWaiting, BoundBeforeRelease: true})
	if next := acquireForTest(t, s, 4, leadA, base, "impl/test/later"); canonicalWorktreeTestPath(next.Path) != canonicalWorktreeTestPath(acq.Path) {
		t.Fatalf("once the earlier child sleeps the checkout is reusable, got %s", next.Path)
	}
}

// TestWorktreeReleaseBoundaryMixedVersions covers the token route across
// versions: a forwarding server honours the new flags and posts the release
// notice; an old daemon's flagless answer counts every child; an old daemon
// that lacks the notice route leaves a warning and a completed release.
func TestWorktreeReleaseBoundaryMixedVersions(t *testing.T) {
	sLead, root, base, leadKey := worktreeListServer(t)
	acq := acquireForTest(t, sLead, 1, leadKey, base, "impl/test/mixed")

	var mu sync.Mutex
	oldDaemon, noChildren := false, false
	var notices []map[string]any
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		old, none := oldDaemon, noChildren
		mu.Unlock()
		switch r.URL.Path {
		case wsagent.RoutePrefix + "/by-token/worktree.children":
			children := []any{}
			if !none {
				child := map[string]any{"address": "id:stale01", "state": wsagent.AgentWaiting}
				if !old {
					child["bound_before_release"] = true
				}
				children = append(children, child)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"children": children})
		case wsagent.RoutePrefix + "/by-token/worktree.released":
			if old {
				http.Error(w, "unknown agent route", http.StatusNotFound)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			notices = append(notices, body)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"conversations": []any{}})
		default:
			http.Error(w, "unknown", http.StatusNotFound)
		}
	}))
	defer daemon.Close()
	worker := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Env: append(sLead.environ(), wsagent.EnvPiToken+"=tok", wsagent.EnvPiEndpoint+"="+daemon.URL)})
	defer worker.Close()

	// An old daemon's answer has no flag: the child counts and refuses.
	mu.Lock()
	oldDaemon = true
	mu.Unlock()
	rel := callToolOnce(t, worker, 10, "worktree.release", map[string]any{"session_key": acq.WorkerKey, "path": acq.Path})
	if !toolIsError(t, rel) || !strings.Contains(toolText(t, rel), "id:stale01") {
		t.Fatalf("an old daemon's flagless child must still refuse: %s", rel)
	}

	// A new daemon flags it as bound before the last release: release
	// proceeds and the notice reaches the daemon without cancelling turns.
	mu.Lock()
	oldDaemon = false
	mu.Unlock()
	if rel := callToolOnce(t, worker, 11, "worktree.release", map[string]any{"session_key": acq.WorkerKey, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("release with only a pre-release child failed: %s", rel)
	}
	mu.Lock()
	got := append([]map[string]any(nil), notices...)
	mu.Unlock()
	if len(got) != 1 || got[0]["cancel_turns"] != false || canonicalWorktreeTestPath(got[0]["worktree"].(string)) != canonicalWorktreeTestPath(acq.Path) {
		t.Fatalf("release notice = %+v", got)
	}
	// The notice carries the marker's instant, so the daemon need not read
	// the marker.
	if at, ok, err := wsgit.WorktreeReleasedAt(acq.Path); err != nil || !ok || got[0]["released_at"] != at.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("release notice released_at = %v, marker %v ok=%v err=%v", got[0]["released_at"], at, ok, err)
	}

	// An old daemon without the notice route (and nothing bound): the
	// release still completes and records its boundary, with a warning.
	again := acquireForTest(t, sLead, 2, leadKey, base, "impl/test/mixed-again")
	mu.Lock()
	oldDaemon, noChildren = true, true
	mu.Unlock()
	resp := callToolOnce(t, worker, 12, "worktree.release", map[string]any{"session_key": again.WorkerKey, "path": again.Path, "format": "json"})
	if toolIsError(t, resp) {
		t.Fatalf("release against an old daemon failed: %s", resp)
	}
	var res worktreeReleaseResult
	if err := json.Unmarshal([]byte(toolText(t, resp)), &res); err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(res.Warnings, "\n"); !strings.Contains(w, "predates release notices") || !strings.Contains(w, "restarting the same older daemon binary does not help") {
		t.Fatalf("an old daemon's missing notice route must be a warning naming the daemon swap: %v", res.Warnings)
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(again.Path); err != nil || !ok {
		t.Fatalf("the boundary is recorded regardless of the daemon: ok=%v err=%v", ok, err)
	}
}

// TestWorktreeReleaseFailsAndKeepsTheLeaseWithoutItsBoundary: when the
// release marker cannot be written the release fails, its lease stays, and no
// notice reaches the daemon: no release completes without its boundary.
func TestWorktreeReleaseFailsAndKeepsTheLeaseWithoutItsBoundary(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	occ := &occupancyFake{}
	s.pi.worktreeChildren = occ
	acq := acquireForTest(t, s, 1, leadA, base, "impl/test/no-marker")
	restore := recordWorktreeRelease
	recordWorktreeRelease = func(string, time.Time) error { return errors.New("disk full") }
	defer func() { recordWorktreeRelease = restore }()
	rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path})
	if !toolIsError(t, rel) || !strings.Contains(toolText(t, rel), "boundary could not be recorded") || !strings.Contains(toolText(t, rel), "disk full") {
		t.Fatalf("a release without its boundary must fail: %s", rel)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease == nil {
		t.Fatalf("a release without its boundary keeps the lease: %+v err=%v", lease, err)
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(acq.Path); err != nil || ok {
		t.Fatalf("no boundary was recorded: ok=%v err=%v", ok, err)
	}
	if got := occ.noticeList(); len(got) != 0 {
		t.Fatalf("a failed release must not notify the daemon: %+v", got)
	}
	recordWorktreeRelease = restore
	if rel := callToolOnce(t, s, 3, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("releasing again once the marker can be written: %s", rel)
	}
}

// TestWorktreeReleaseNotifiesAfterTheMarkerUnderTheLock: the daemon hears of
// a release after its marker is written, with the marker's instant, while
// the per-worktree lock still holds (no spawn or acquisition claim can pass
// between them) and before the lease goes.
func TestWorktreeReleaseNotifiesAfterTheMarkerUnderTheLock(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	occ := &occupancyFake{}
	s.pi.worktreeChildren = occ
	acq := acquireForTest(t, s, 1, leadA, base, "impl/test/order")
	var markerAt time.Time
	var markerOK, lockHeld, leaseHeld bool
	occ.observe = func(worktree string, releasedAt time.Time) {
		at, ok, err := wsgit.WorktreeReleasedAt(worktree)
		markerAt, markerOK = at, ok && err == nil && at.Equal(releasedAt)
		lock, err := wsgit.WorktreeReleaseLock(context.Background(), wsgit.ExecRunner{}, worktree)
		if err == nil {
			if locked, err := lock.TryRLock(); err == nil && locked {
				_ = lock.Unlock()
			} else {
				lockHeld = true
			}
		}
		lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, worktree)
		leaseHeld = err == nil && lease != nil
	}
	if rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("release failed: %s", rel)
	}
	if !markerOK {
		t.Fatalf("the notice must follow the marker and carry its instant (marker %v)", markerAt)
	}
	if !lockHeld {
		t.Fatal("the notice must be published before the per-worktree lock is let go")
	}
	if !leaseHeld {
		t.Fatal("the notice comes before the lease goes, so a failed lease removal still left the daemon told")
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease != nil {
		t.Fatalf("the completed release removes the lease: %+v err=%v", lease, err)
	}
}

// TestWorktreeReleaseAgainstAnOldDaemon: an older daemon has no
// worktree.released route, so it would keep running the bindings a release
// ends. With a live child bound, even a root lead's forced release is refused
// before it changes anything (lease, uncommitted work, branch and marker all
// stay), naming the daemon swap. With nothing live bound the release
// completes, records its boundary, and warns.
func TestWorktreeReleaseAgainstAnOldDaemon(t *testing.T) {
	sLead, root, base, leadKey := worktreeListServer(t)
	acq := acquireForTest(t, sLead, 1, leadKey, base, "impl/test/old-daemon")
	draft := writeDraft(t, acq.Path)
	var mu sync.Mutex
	children := []any{map[string]any{"address": "id:bound01", "state": wsagent.AgentRunning}}
	occupancyFails := false
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case wsagent.RoutePrefix + "/by-token/worktree.children":
			// An old daemon's answer: no release flags.
			mu.Lock()
			list, fails := children, occupancyFails
			mu.Unlock()
			if fails {
				http.Error(w, "occupancy unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"children": list})
		default:
			// Its token route answers an unknown op like this.
			http.Error(w, "unknown agent route", http.StatusNotFound)
		}
	}))
	defer daemon.Close()
	worker := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Env: append(sLead.environ(), wsagent.EnvPiToken+"=tok", wsagent.EnvPiEndpoint+"="+daemon.URL)})
	defer worker.Close()

	resp := callToolOnce(t, worker, 2, "worktree.release", map[string]any{"session_key": leadKey, "path": acq.Path, "force": true})
	if !toolIsError(t, resp) {
		t.Fatalf("a forced release with a live child against an old daemon must be refused: %s", resp)
	}
	for _, want := range []string{"predates release notices", "Nothing was changed", "Swap the daemon for a release-aware build", "restarting the same older daemon binary does not help"} {
		if !strings.Contains(toolText(t, resp), want) {
			t.Fatalf("refusal lacks %q: %s", want, toolText(t, resp))
		}
	}
	if _, err := os.Stat(draft); err != nil {
		t.Fatalf("a refused release keeps uncommitted work: %v", err)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease == nil {
		t.Fatalf("a refused release keeps the lease: %+v err=%v", lease, err)
	}
	if b := wtBranch(t, acq.Path); b == "HEAD" {
		t.Fatal("a refused release must not detach the worktree")
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(acq.Path); err != nil || ok {
		t.Fatalf("a refused release records no boundary: ok=%v err=%v", ok, err)
	}

	// Nothing live bound: the release proceeds, with the warning.
	mu.Lock()
	children = []any{}
	mu.Unlock()
	resp = callToolOnce(t, worker, 3, "worktree.release", map[string]any{"session_key": leadKey, "path": acq.Path, "force": true, "format": "json"})
	if toolIsError(t, resp) {
		t.Fatalf("a release with nothing live bound must complete against an old daemon: %s", resp)
	}
	var res worktreeReleaseResult
	if err := json.Unmarshal([]byte(toolText(t, resp)), &res); err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"predates release notices", "no live agent was bound here", "Swap the daemon for a release-aware build", "restarting the same older daemon binary does not help", "running turns were not cancelled"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("old-daemon warning lacks %q: %v", want, res.Warnings)
		}
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(acq.Path); err != nil || !ok {
		t.Fatalf("the boundary is recorded: ok=%v err=%v", ok, err)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease != nil {
		t.Fatalf("the release completed and removed the lease: %+v err=%v", lease, err)
	}

	// The occupancy read fails: the binding state is unknown, and the
	// warning claims no more than that.
	unknown := acquireForTest(t, sLead, 4, leadKey, base, "impl/test/old-daemon-unknown")
	mu.Lock()
	occupancyFails = true
	mu.Unlock()
	resp = callToolOnce(t, worker, 5, "worktree.release", map[string]any{"session_key": leadKey, "path": unknown.Path, "force": true, "format": "json"})
	if toolIsError(t, resp) {
		t.Fatalf("a forced release whose occupancy read failed completes: %s", resp)
	}
	res = worktreeReleaseResult{}
	if err := json.Unmarshal([]byte(toolText(t, resp)), &res); err != nil {
		t.Fatal(err)
	}
	warnings = strings.Join(res.Warnings, "\n")
	for _, want := range []string{"predates release notices", "whether a live agent is bound here is unknown", "Swap the daemon for a release-aware build"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("unknown-binding warning lacks %q: %v", want, res.Warnings)
		}
	}
	if strings.Contains(warnings, "no live agent was bound") {
		t.Fatalf("an unread occupancy must not be reported as no live agent: %v", res.Warnings)
	}
}

// TestWorktreeReleaseReportsAPartialReleaseWhenTheLeaseStays: a release
// whose lease removal fails after its boundary was recorded and the daemon
// told says what did complete, keeps the lease (so acquisition does not hand
// the worktree out), and a second release finishes it.
func TestWorktreeReleaseReportsAPartialReleaseWhenTheLeaseStays(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	occ := &occupancyFake{}
	s.pi.worktreeChildren = occ
	acq := acquireForTest(t, s, 1, leadA, base, "impl/test/lease-stays")
	restore := removeReleasedWorktreeLease
	removeReleasedWorktreeLease = func(context.Context, wsgit.Runner, string) error { return errors.New("lease store read-only") }
	defer func() { removeReleasedWorktreeLease = restore }()
	rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path})
	if !toolIsError(t, rel) {
		t.Fatalf("a release that kept its lease must not report success: %s", rel)
	}
	for _, want := range []string{"partly released", "release boundary is recorded", "the running daemon applied the release notice", "lease store read-only", "release it again"} {
		if !strings.Contains(toolText(t, rel), want) {
			t.Fatalf("partial release report lacks %q: %s", want, toolText(t, rel))
		}
	}
	if strings.Contains(toolText(t, rel), "are ended") {
		t.Fatalf("the boundary alone ends no running binding: %s", toolText(t, rel))
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(acq.Path); err != nil || !ok {
		t.Fatalf("the boundary stays recorded: ok=%v err=%v", ok, err)
	}
	if got := occ.noticeList(); len(got) != 1 {
		t.Fatalf("the daemon was told before the lease step: %+v", got)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease == nil {
		t.Fatalf("the lease stays: %+v err=%v", lease, err)
	}
	removeReleasedWorktreeLease = restore
	if rel := callToolOnce(t, s, 3, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("releasing again removes the lease: %s", rel)
	}
	if lease, err := readWorktreeLease(context.Background(), wsgit.ExecRunner{}, acq.Path); err != nil || lease != nil {
		t.Fatalf("the second release removed the lease: %+v err=%v", lease, err)
	}

	// A daemon-side reader that takes no notice: the partial report says the
	// notice was not applied, so running bindings may still take input.
	s.pi.worktreeChildren = &fakeWorktreeChildren{}
	unheard := acquireForTest(t, s, 4, leadA, base, "impl/test/lease-stays-unheard")
	removeReleasedWorktreeLease = func(context.Context, wsgit.Runner, string) error { return errors.New("lease store read-only") }
	rel = callToolOnce(t, s, 5, "worktree.release", map[string]any{"session_key": leadA, "path": unheard.Path})
	if !toolIsError(t, rel) {
		t.Fatalf("a release that kept its lease must not report success: %s", rel)
	}
	for _, want := range []string{"partly released", "release boundary is recorded", "the running daemon did not apply the release notice", "can take input in this worktree", "no ws daemon was told about this release"} {
		if !strings.Contains(toolText(t, rel), want) {
			t.Fatalf("unapplied partial release report lacks %q: %s", want, toolText(t, rel))
		}
	}
}

// TestWorktreeReleaseWarnsWhenNoDaemonHearsOfIt: a server whose daemon-side
// reader takes no release notice, or that has no daemon at all, completes the
// release and warns that no daemon was told.
func TestWorktreeReleaseWarnsWhenNoDaemonHearsOfIt(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	for i, reader := range []WorktreeChildrenReader{&fakeWorktreeChildren{}, nil} {
		s.pi.worktreeChildren = reader
		acq := acquireForTest(t, s, 10+2*i, leadA, base, fmt.Sprintf("impl/test/unheard-%d", i))
		resp := callToolOnce(t, s, 11+2*i, "worktree.release", map[string]any{"session_key": leadA, "path": acq.Path, "format": "json"})
		if toolIsError(t, resp) {
			t.Fatalf("release failed: %s", resp)
		}
		var res worktreeReleaseResult
		if err := json.Unmarshal([]byte(toolText(t, resp)), &res); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(res.Warnings, "\n"), "no ws daemon was told about this release") {
			t.Fatalf("reader %d: a release no daemon heard of must warn: %v", i, res.Warnings)
		}
	}
}
