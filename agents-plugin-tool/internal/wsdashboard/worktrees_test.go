package wsdashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// fakeWorktrees is a WorktreeSource that answers one fixed pool.
type fakeWorktrees struct {
	mu   sync.Mutex
	pool WorktreePool
	err  error
}

func (f *fakeWorktrees) ReadWorktrees(Registration) (WorktreePool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pool := f.pool
	pool.Worktrees = append([]PooledWorktree(nil), f.pool.Worktrees...)
	return pool, f.err
}

// worktreeFixture is a page fixture whose daemon reads pools from a fake
// over two real directories: a leased worktree of 1500 bytes and an idle
// one of 250.
func worktreeFixture(t *testing.T) (*pageFixture, *fakeWorktrees, PooledWorktree, PooledWorktree) {
	t.Helper()
	poolDir := t.TempDir()
	mk := func(name string, sizes ...int) string {
		dir := filepath.Join(poolDir, name)
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		for i, n := range sizes {
			if err := os.WriteFile(filepath.Join(dir, "sub", string(rune('a'+i))), make([]byte, n), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	acquired := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC).UnixMilli()
	leased := PooledWorktree{Name: "busy", Path: mk("busy", 1000, 500), Profile: "full", Branch: "impl/x", Leased: true, Holder: "workerrid", Parent: "livesession", AcquiredAt: acquired, ActiveAt: acquired}
	idle := PooledWorktree{Name: "free", Path: mk("free", 250), Profile: "full", Detached: true, ActiveAt: acquired - 3600_000}
	src := &fakeWorktrees{pool: WorktreePool{Pool: poolDir, Worktrees: []PooledWorktree{leased, idle}}}
	f := startPageFixture(t, func(o *DaemonOptions) { o.Worktrees = src })
	return f, src, leased, idle
}

func getPool(t *testing.T, url string) WorktreePool {
	t.Helper()
	code, body := get(t, url)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", url, code, body)
	}
	var pool WorktreePool
	if err := json.Unmarshal([]byte(body), &pool); err != nil {
		t.Fatalf("pool answer %q: %v", body, err)
	}
	return pool
}

func TestWorktreesRouteAnswersAtOnceThenWithSizes(t *testing.T) {
	f, _, leased, idle := worktreeFixture(t)
	first := getPool(t, f.base+"/s/livesession/worktrees")
	if !first.Sizing || len(first.Worktrees) != 2 || first.Worktrees[0].Size != nil || first.Worktrees[1].Size != nil {
		t.Fatalf("first read = %+v, want both worktrees, unsized, sizing", first)
	}
	got := getPool(t, f.base+"/s/livesession/worktrees?wait=1")
	if got.Sizing {
		t.Fatalf("a waited read still sizing: %+v", got)
	}
	sizes := map[string]int64{}
	for _, wt := range got.Worktrees {
		if wt.Size == nil {
			t.Fatalf("waited read left %s unsized", wt.Name)
		}
		sizes[wt.Name] = *wt.Size
	}
	if sizes["busy"] != 1500 || sizes["free"] != 250 {
		t.Fatalf("sizes = %v, want busy 1500 and free 250", sizes)
	}
	w0 := got.Worktrees[0]
	w0.Size = nil
	if w0 != leased {
		t.Errorf("leased worktree = %+v, want %+v", w0, leased)
	}
	if got.Worktrees[1].Detached != idle.Detached || got.Worktrees[1].Leased {
		t.Errorf("idle worktree = %+v", got.Worktrees[1])
	}
	// A fresh size answers the next read at once.
	if again := getPool(t, f.base+"/s/livesession/worktrees"); again.Sizing || again.Worktrees[1].Size == nil {
		t.Errorf("a read inside the size TTL = %+v, want cached sizes", again)
	}
}

// countWalks replaces the fixture daemon's size walk with a counter over the
// real walk, before any request is served.
func countWalks(f *pageFixture) *atomic.Int64 {
	var n atomic.Int64
	f.d.sizes.walk = func(path string) int64 {
		n.Add(1)
		return dirSize(path)
	}
	return &n
}

// settleWalks gives a walk a lightweight read wrongly started time to run, so a
// zero count is evidence rather than a race.
func settleWalks() { time.Sleep(100 * time.Millisecond) }

// TestWorktreesNoSizeStartsNoWalk: ?no_size=1 lists the pool with no size, not
// sizing, and starts no walk and caches nothing, ?wait=1 notwithstanding; the
// full read then measures, and the lightweight read answers those sizes while
// they are fresh, still without a walk, and drops them (without evicting the
// entry) once past the TTL.
func TestWorktreesNoSizeStartsNoWalk(t *testing.T) {
	f, _, _, _ := worktreeFixture(t)
	walks := countWalks(f)

	for _, q := range []string{"?no_size=1", "?no_size=1&wait=1"} {
		got := getPool(t, f.base+"/s/livesession/worktrees"+q)
		if got.Sizing || len(got.Worktrees) != 2 || got.Worktrees[0].Size != nil || got.Worktrees[1].Size != nil {
			t.Fatalf("GET worktrees%s = %+v, want two unsized worktrees, not sizing", q, got)
		}
	}
	settleWalks()
	f.d.sizes.mu.Lock()
	cached := len(f.d.sizes.entries)
	f.d.sizes.mu.Unlock()
	if walks.Load() != 0 || cached != 0 {
		t.Fatalf("lightweight reads started %d walks and cached %d entries, want 0 and 0", walks.Load(), cached)
	}

	if full := getPool(t, f.base+"/s/livesession/worktrees?wait=1"); full.Sizing || walks.Load() != 2 {
		t.Fatalf("full read: sizing %v, walks %d, want a finished scan of 2 walks", full.Sizing, walks.Load())
	}
	got := getPool(t, f.base+"/s/livesession/worktrees?no_size=1")
	if got.Sizing || got.Worktrees[0].Size == nil || *got.Worktrees[0].Size != 1500 || got.Worktrees[1].Size == nil || *got.Worktrees[1].Size != 250 {
		t.Fatalf("lightweight read after a scan = %+v, want the cached 1500 and 250", got)
	}

	// Past the TTL the figure is not answered, and the read neither
	// refreshes nor evicts it.
	f.d.sizes.mu.Lock()
	for _, e := range f.d.sizes.entries {
		e.at = time.Now().Add(-2 * worktreeSizeTTL)
	}
	f.d.sizes.mu.Unlock()
	got = getPool(t, f.base+"/s/livesession/worktrees?no_size=1")
	settleWalks()
	f.d.sizes.mu.Lock()
	kept := len(f.d.sizes.entries)
	f.d.sizes.mu.Unlock()
	if got.Sizing || got.Worktrees[0].Size != nil || got.Worktrees[1].Size != nil || kept != 2 || walks.Load() != 2 {
		t.Fatalf("lightweight read of stale sizes = %+v, kept %d, walks %d; want unsized, 2 entries kept, 2 walks", got, kept, walks.Load())
	}
}

// TestWorktreeCleanupStartsNoWalk: a cleanup request reads the pool cache-only,
// so it starts no walk, carries "size: unknown" for what was never measured,
// and carries a fresh measured size once a scan has run.
func TestWorktreeCleanupStartsNoWalk(t *testing.T) {
	f, _, _, _ := worktreeFixture(t)
	walks := countWalks(f)

	if code, _, raw := postJSON(t, f.base+"/s/livesession/worktrees/cleanup", f.base, ""); code != http.StatusOK {
		t.Fatalf("cleanup = %d: %s", code, raw)
	}
	settleWalks()
	if walks.Load() != 0 {
		t.Fatalf("a cold cleanup started %d walks, want 0", walks.Load())
	}
	held := f.held(t)
	if len(held) != 1 || strings.Count(inboxBody(t, held[0].Entry), "  size: unknown") != 2 {
		t.Fatalf("held = %+v, want one message with both sizes unknown", held)
	}

	getPool(t, f.base+"/s/livesession/worktrees?wait=1")
	scanned := walks.Load()
	if code, _, raw := postJSON(t, f.base+"/s/livesession/worktrees/cleanup", f.base, ""); code != http.StatusOK {
		t.Fatalf("second cleanup = %d: %s", code, raw)
	}
	settleWalks()
	if walks.Load() != scanned || scanned != 2 {
		t.Fatalf("walks = %d after the scan and %d after another cleanup, want 2 and 2", scanned, walks.Load())
	}
}

func TestWorktreesRouteRefusals(t *testing.T) {
	f, src, _, _ := worktreeFixture(t)
	if code, _ := get(t, f.base+"/s/nosuchsession/worktrees"); code != http.StatusNotFound {
		t.Errorf("unknown session = %d, want 404", code)
	}
	src.mu.Lock()
	src.err = errors.New("git exploded")
	src.mu.Unlock()
	if code, body := get(t, f.base+"/s/livesession/worktrees"); code != http.StatusInternalServerError || !strings.Contains(body, "git exploded") {
		t.Errorf("failed read = %d %q, want 500 with the reason", code, body)
	}
	if code, _, _ := postJSON(t, f.base+"/s/livesession/worktrees", f.base, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("POST to the read route = %d, want 405", code)
	}

	bare := startPageFixture(t)
	if code, _ := get(t, bare.base+"/s/livesession/worktrees"); code != http.StatusNotFound {
		t.Errorf("a daemon without a worktree source = %d, want 404", code)
	}
}

func TestWorktreeCleanupHoldsOneMessageWithThePool(t *testing.T) {
	f, _, leased, idle := worktreeFixture(t)
	// Measure first, so the message carries the sizes.
	getPool(t, f.base+"/s/livesession/worktrees?wait=1")

	if code, _, _ := postJSON(t, f.base+"/s/livesession/worktrees/cleanup", "", ""); code != http.StatusForbidden {
		t.Fatalf("cleanup without an Origin = %d, want 403", code)
	}
	if held := f.held(t); len(held) != 0 {
		t.Fatalf("a refused cleanup held %+v", held)
	}
	code, _, raw := postJSON(t, f.base+"/s/livesession/worktrees/cleanup", f.base, "")
	if code != http.StatusOK {
		t.Fatalf("cleanup = %d: %s", code, raw)
	}
	held := f.held(t)
	if len(held) != 1 {
		t.Fatalf("held after cleanup = %+v, want one message", held)
	}
	text := inboxBody(t, held[0].Entry)
	for _, want := range []string{
		"The user asks to clean up unused worktrees.\n\npool: ",
		"worktree: " + leased.Path + "\n  profile: full\n  branch: impl/x\n  state: leased; holder id:workerrid; parent id:livesession; acquired_at 2026-11-08T12:00:00Z\n",
		"  size: 1.5 kB\n",
		"worktree: " + idle.Path + "\n  profile: full\n  branch: (detached)\n  state: idle\n  last_activity: 2026-11-08T11:00:00Z\n  size: 250 B",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("cleanup message lacks %q:\n%s", want, text)
		}
	}
}

// TestWorktreePoolTextIsBounded: a pool too large for one message lists
// what fits and ends with how many worktrees it left out, and a cleanup of
// it still sends.
func TestWorktreePoolTextIsBounded(t *testing.T) {
	pool := WorktreePool{Pool: "/p"}
	for i := range 2000 {
		pool.Worktrees = append(pool.Worktrees, PooledWorktree{Path: "/p/" + strings.Repeat("w", 40) + strconv.Itoa(i), Branch: "impl/x"})
	}
	text := pool.text(worktreeTextMax)
	if len(text) > worktreeTextMax+64 {
		t.Fatalf("text is %d bytes, bound %d", len(text), worktreeTextMax)
	}
	listed := strings.Count(text, "\nworktree: ")
	if listed == 0 || listed == len(pool.Worktrees) || !strings.HasSuffix(text, "\n... "+strconv.Itoa(len(pool.Worktrees)-listed)+" more worktrees\n") {
		t.Fatalf("listed %d of %d; text ends %q", listed, len(pool.Worktrees), text[len(text)-60:])
	}
	if small := (WorktreePool{Pool: "/p", Worktrees: pool.Worktrees[:2]}).text(worktreeTextMax); strings.Contains(small, "more worktrees") {
		t.Errorf("a small pool is cut: %q", small)
	}
	two := WorktreePool{Pool: "/p", Worktrees: pool.Worktrees[:2]}
	one := WorktreePool{Pool: "/p", Worktrees: pool.Worktrees[:1]}.text(worktreeTextMax)
	if cut := two.text(len(one)); !strings.HasSuffix(cut, "\n... 1 more worktree\n") {
		t.Errorf("one worktree left out: text ends %q", cut[len(cut)-30:])
	}

	f, src, _, _ := worktreeFixture(t)
	src.mu.Lock()
	src.pool = pool
	src.mu.Unlock()
	if code, _, raw := postJSON(t, f.base+"/s/livesession/worktrees/cleanup", f.base, ""); code != http.StatusOK {
		t.Fatalf("cleanup of a large pool = %d: %s", code, raw)
	}
}

func TestWorktreeCleanupFollowsThePageLanguage(t *testing.T) {
	f, _, _, _ := worktreeFixture(t)
	req, err := http.NewRequest(http.MethodPost, f.base+"/s/livesession/worktrees/cleanup", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", f.base)
	req.AddCookie(&http.Cookie{Name: wsi18n.CookieName, Value: "ko"})
	resp, err := testClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cleanup = %d: %s", resp.StatusCode, body)
	}
	held := f.held(t)
	if len(held) != 1 || !strings.HasPrefix(inboxBody(t, held[0].Entry), wsi18n.T("ko", "worktree.cleanup.ask")+"\n\n") {
		t.Fatalf("held = %+v, want the Korean request line first", held)
	}
}

func TestWorktreeCleanupRefusesASessionWithoutAnAdapter(t *testing.T) {
	f, _, _, _ := worktreeFixture(t)
	if code, _, raw := postJSON(t, f.base+"/s/boardsession/worktrees/cleanup", f.base, ""); code != http.StatusConflict {
		t.Fatalf("cleanup of a session without an adapter = %d (%s), want 409", code, raw)
	}
	held, _ := wsinbox.Held(f.inbox("boardsession"))
	if len(held) != 0 {
		t.Fatalf("a refused cleanup held %+v", held)
	}
}

func TestByteSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 999: "999 B", 1000: "1.0 kB", 1_500_000: "1.5 MB", 2_340_000_000: "2.3 GB"} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestWorktreesCardOnTheGitPage pins the Git page's pooled worktrees: the
// strip's Worktrees button (with its count badge) and the popover it
// controls, on a session page of a daemon that reads pools, inside the Git
// page (not the drawer), with the light-list mode, the manual full-scan
// button, and its cleanup button live with an adapter and disabled with the
// reason without one; a daemon without a worktree source keeps the button
// and its popover shows the no-pool line and no list.
func TestWorktreesCardOnTheGitPage(t *testing.T) {
	f, _, _, _ := worktreeFixture(t)
	_, live := get(t, f.base+"/s/livesession?frame=1")
	page, btn, pop, list, graph := strings.Index(live, `id="gitpage"`), strings.Index(live, `id="gpwtbtn" aria-haspopup="dialog" aria-controls="gpwtpop" aria-expanded="false"`),
		strings.Index(live, `<div class="gp-wtpop" id="gpwtpop" role="dialog"`), strings.Index(live, `id="wtlist"`), strings.Index(live, `id="glog"`)
	if page < 0 || btn < page || pop < btn || list < pop || graph < list {
		t.Fatalf("git page order: page %d, button %d, popover %d, list %d, graph %d", page, btn, pop, list, graph)
	}
	if strings.Contains(live, `id="wtcard"`) {
		t.Error("the page still carries the retired worktrees card")
	}
	if strings.Contains(live, `id="p-worktrees"`) {
		t.Error("the drawer still carries the retired worktrees card")
	}
	assertSettingsOwner(t, live, false)
	for _, want := range []string{`id="wtcount"`, `id="gpwtmode" data-mode="light"`, `id="gpscan"`, `<button type="button" class="btn wtclean" id="wtclean" title="ask the session to clean up unused worktrees">Request cleanup</button>`} {
		if !strings.Contains(live, want) {
			t.Errorf("live page lacks %q", want)
		}
	}
	_, board := get(t, f.base+"/s/boardsession?frame=1")
	if !strings.Contains(board, `id="wtclean" disabled title="this session has no live host adapter to ask">`) {
		t.Errorf("board-first page's cleanup button is not disabled with its reason")
	}

	bare := startPageFixture(t)
	_, bp := get(t, bare.base+"/s/livesession?frame=1")
	pop = strings.Index(bp, `id="gpwtpop"`)
	if strings.Contains(bp, `id="wtlist"`) || strings.Contains(bp, `id="gpscan"`) || !strings.Contains(bp, `id="gpwtbtn"`) || pop < 0 || strings.Index(bp, `class="gp-none"`) < pop {
		t.Errorf("a daemon without a worktree source: a pool list or scan button present, or no button, or no no-pool line in the popover")
	}
}
