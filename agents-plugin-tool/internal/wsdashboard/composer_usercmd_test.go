package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// usercmdFake is the user commands' service over the real registry, with the
// lead's bound worktree steered by the test. It counts the fresh and cached
// worktree reads the routes ask for.
type usercmdFake struct {
	reg *wsusercmd.Registry

	mu            sync.Mutex
	root          string
	freshReads    int
	cachedReads   int
	openErr       error
	removeCommits int
}

func (u *usercmdFake) scope(fresh bool) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.openErr != nil {
		return "", u.openErr
	}
	if fresh {
		u.freshReads++
	} else {
		u.cachedReads++
	}
	return u.root, nil
}

func (u *usercmdFake) List(replyID string, fresh bool) ([]wsusercmd.Command, string, error) {
	root, err := u.scope(fresh)
	if err != nil {
		return nil, "", err
	}
	return u.reg.List(replyID, root), u.reg.Revision(replyID), nil
}

func (u *usercmdFake) Get(replyID, id string) (wsusercmd.Command, bool, error) {
	root, err := u.scope(true)
	if err != nil {
		return wsusercmd.Command{}, false, err
	}
	c, ok := u.reg.Get(replyID, root, id)
	return c, ok, nil
}

func (u *usercmdFake) Remove(replyID, id string) (bool, error) {
	root, err := u.scope(true)
	if err != nil {
		return false, err
	}
	err = u.reg.Remove(replyID, root, id)
	if errors.Is(err, wsusercmd.ErrUnknown) {
		return false, nil
	}
	return err == nil, err
}

func (u *usercmdFake) Revision(replyID string) string { return u.reg.Revision(replyID) }

type usercmdFixture struct {
	*composerFixture
	users *usercmdFake
}

func startUserCommandFixture(t *testing.T) *usercmdFixture {
	t.Helper()
	f := startComposerFixture(t)
	users := &usercmdFake{root: filepath.Join(t.TempDir(), "worktree")}
	reg, err := wsusercmd.Open(filepath.Join(t.TempDir(), wsusercmd.StoreName), f.d.UserCommandsChanged)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	users.reg = reg
	f.d.SetUserCommands(users)
	return &usercmdFixture{composerFixture: f, users: users}
}

func (f *usercmdFixture) register(t *testing.T, id, line string) wsusercmd.Command {
	t.Helper()
	c, _, err := f.users.reg.Set("livesession", f.users.root, wsusercmd.Input{ID: id, Label: "Label " + id, Description: "does " + id, Line: line})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ucPost(d *Daemon, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	return composerHTTP(d, http.MethodPost, path, string(raw), "http://127.0.0.1", "127.0.0.1:1234", nil)
}

func (f *usercmdFixture) run(id string, rev int64, sub string, retry bool) *httptest.ResponseRecorder {
	return ucPost(f.d, "/s/livesession/user-commands/"+id+"/run", map[string]any{"submission_id": sub, "revision": rev, "retry": retry, "token": f.d.composer.token()})
}

// launches waits for n launches to have started: a run goes out after its
// acknowledgement.
func (f *usercmdFixture) launches(t *testing.T, n int) {
	t.Helper()
	waitFor(t, "launches", func() bool { return f.svc.launchCount() >= n })
	if got := f.svc.launchCount(); got != n {
		t.Fatalf("%d launches, want %d", got, n)
	}
}

func (f *usercmdFixture) list(t *testing.T) userCommandList {
	t.Helper()
	res := composerGet(f.d, "/s/livesession/user-commands")
	if res.Code != 200 {
		t.Fatalf("list: %d %s", res.Code, res.Body)
	}
	var l userCommandList
	if err := json.Unmarshal(res.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// TestUserCommandListAndRunUseTheStoredLine: a click names the command and the
// revision it displayed; the line that runs is the stored one, shown on the card
// with the command's label, with no turn side effect of the dashboard's own.
func TestUserCommandListAndRunUseTheStoredLine(t *testing.T) {
	f := startUserCommandFixture(t)
	c := f.register(t, "build", "make all\necho done")
	l := f.list(t)
	if len(l.Commands) != 1 || l.Commands[0] != c || l.Rev == "" || l.Token == "" {
		t.Fatalf("list %+v", l)
	}
	res := f.run("build", c.Revision, "click-build-0001", false)
	if res.Code != 202 {
		t.Fatalf("run: %d %s", res.Code, res.Body)
	}
	card := decodeCard(t, res)
	if card.SlotID != "build" || card.Label != "Label build" || card.Command != "make all\necho done" {
		t.Fatalf("card %+v", card)
	}
	done := waitCard(t, f.d, "click-build-0001", "done")
	if done.Output != "hi\n" || done.Label != "Label build" {
		t.Fatalf("done %+v", done)
	}
	if n := f.svc.launchCount(); n != 1 {
		t.Fatalf("%d launches", n)
	}
	got := f.svc.launches[0]
	if got.Line != "make all\necho done" || got.Window != ComposerWindow || got.Gen != 4 || got.ReplyID != "livesession" {
		t.Fatalf("launch %+v", got)
	}
}

// TestUserCommandStaleAndWithdrawnRejectWithoutRunning: a click with a stale
// revision or on a withdrawn command runs nothing and answers the fresh list.
func TestUserCommandStaleAndWithdrawnRejectWithoutRunning(t *testing.T) {
	f := startUserCommandFixture(t)
	old := f.register(t, "build", "make")
	fresh := f.register(t, "build", "make clean all")
	res := f.run("build", old.Revision, "click-stale-0001", false)
	var l userCommandList
	if res.Code != 409 || json.Unmarshal(res.Body.Bytes(), &l) != nil || l.Error != "stale" || len(l.Commands) != 1 || l.Commands[0] != fresh {
		t.Fatalf("stale: %d %s", res.Code, res.Body)
	}
	if err := f.users.reg.Remove("livesession", f.users.root, "build"); err != nil {
		t.Fatal(err)
	}
	res = f.run("build", fresh.Revision, "click-gone-0001", false)
	l = userCommandList{}
	if res.Code != 409 || json.Unmarshal(res.Body.Bytes(), &l) != nil || l.Error != "withdrawn" || len(l.Commands) != 0 {
		t.Fatalf("withdrawn: %d %s", res.Code, res.Body)
	}
	if res := f.run("never-registered", 1, "click-none-0001", false); res.Code != 409 {
		t.Fatalf("unknown command: %d", res.Code)
	}
	if n := f.svc.launchCount(); n != 0 {
		t.Fatalf("a rejected click launched %d jobs", n)
	}
	// A rejected submission id is not burned: the same id runs once the click
	// names the current revision.
	again := f.register(t, "build", "make")
	if res := f.run("build", again.Revision, "click-stale-0001", false); res.Code != 202 {
		t.Fatalf("rerun: %d %s", res.Code, res.Body)
	}
	f.launches(t, 1)
}

// TestUserCommandClickIdempotency: a double click and concurrent identical
// clicks map to one run per submission id; a retry returns the card even after
// the command changed; a fresh id is a new run; an unknown retry never runs.
func TestUserCommandClickIdempotency(t *testing.T) {
	f := startUserCommandFixture(t)
	c := f.register(t, "build", "make")
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- f.run("build", c.Revision, "click-conc-00001", false).Code
		}()
	}
	wg.Wait()
	close(codes)
	fresh, known := 0, 0
	for code := range codes {
		switch code {
		case 202:
			fresh++
		case 200:
			known++
		default:
			t.Fatalf("concurrent click answered %d", code)
		}
	}
	f.launches(t, 1)
	if fresh != 1 || known != 7 {
		t.Fatalf("fresh %d known %d", fresh, known)
	}
	// The command changes; a retry of the same click still maps to its one job.
	f.register(t, "build", "make changed")
	res := f.run("build", c.Revision, "click-conc-00001", true)
	if res.Code != 200 || decodeCard(t, res).Command != "make" {
		t.Fatalf("retry after the change: %d %s", res.Code, res.Body)
	}
	// The same id for another command or revision is a conflict.
	if res := f.run("other", c.Revision, "click-conc-00001", false); res.Code != 409 {
		t.Fatalf("id reused for another command: %d", res.Code)
	}
	// An id nobody admitted, retried, reports the outcome unknown and runs nothing.
	res = f.run("build", c.Revision, "click-never-0001", true)
	if card := decodeCard(t, res); res.Code != 200 || !card.Unknown || f.svc.launchCount() != 1 {
		t.Fatalf("unknown retry: %d %+v launches %d", res.Code, card, f.svc.launchCount())
	}
	// A fresh id is a new run.
	cur, _, _ := f.users.Get("livesession", "build")
	if res := f.run("build", cur.Revision, "click-conc-00002", false); res.Code != 202 {
		t.Fatalf("fresh id: %d", res.Code)
	}
	f.launches(t, 2)
	if line := f.svc.launches[1].Line; line != "make changed" {
		t.Fatalf("the second run ran %q", line)
	}
}

// TestUserCommandAuthority: the list, Run and remove routes are localhost-only
// behind the composer's gate: a LAN peer, a missing or cross Origin, a
// non-hosted session, a closed lead and an unbound service are all refused.
func TestUserCommandAuthority(t *testing.T) {
	f := startUserCommandFixture(t)
	c := f.register(t, "build", "make")
	run := "/s/livesession/user-commands/build/run"
	remove := "/s/livesession/user-commands/build/remove"
	body := `{"submission_id":"click-auth-00001","revision":` + jsonNumber(c.Revision) + `}`
	for _, route := range []string{run, remove} {
		for _, tc := range []struct {
			name, origin, remote string
			code                 int
		}{
			{"LAN peer", "http://127.0.0.1", "192.0.2.1:9", 401},
			{"no origin", "", "127.0.0.1:1", 403},
			{"cross origin", "http://evil.test", "127.0.0.1:1", 403},
		} {
			if res := composerHTTP(f.d, http.MethodPost, route, body, tc.origin, tc.remote, nil); res.Code != tc.code {
				t.Fatalf("%s %s: %d %s", route, tc.name, res.Code, res.Body)
			}
		}
	}
	if res := composerHTTP(f.d, http.MethodGet, "/s/livesession/user-commands", "", "", "192.0.2.1:9", nil); res.Code != 401 {
		t.Fatalf("LAN list: %d", res.Code)
	}
	if res := composerHTTP(f.d, http.MethodGet, "/s/livesession/user-commands", "", "", "127.0.0.1:1", map[string]string{"Sec-Fetch-Site": "cross-site"}); res.Code != 405 {
		t.Fatalf("cross-site list: %d", res.Code)
	}
	if res := composerHTTP(f.d, http.MethodPost, "/s/boardsession/user-commands/build/run", body, "http://127.0.0.1", "127.0.0.1:1", nil); res.Code != 404 {
		t.Fatalf("non-hosted session: %d", res.Code)
	}
	// The body is closed: a command's text can never come from the browser.
	if res := ucPost(f.d, run, map[string]any{"submission_id": "click-auth-00002", "revision": c.Revision, "text": "rm -rf /"}); res.Code != 400 {
		t.Fatalf("a body carrying text: %d", res.Code)
	}
	if res := ucPost(f.d, "/s/livesession/user-commands/bad%20id/run", map[string]any{"submission_id": "click-auth-00003", "revision": c.Revision}); res.Code != 404 {
		t.Fatalf("malformed id: %d", res.Code)
	}
	if res := ucPost(f.d, "/s/livesession/user-commands/build/other", map[string]any{}); res.Code != 404 {
		t.Fatalf("unknown action: %d", res.Code)
	}
	if n := f.svc.launchCount(); n != 0 {
		t.Fatalf("a refused request launched %d jobs", n)
	}
	if _, ok := f.users.reg.Get("livesession", f.users.root, "build"); !ok {
		t.Fatal("a refused removal removed the command")
	}
	// A closed lead serves nothing.
	f.svc.mu.Lock()
	f.svc.open = map[string]composerOpen{"livesession": {err: errors.New("closed")}}
	f.svc.mu.Unlock()
	if res := composerGet(f.d, "/s/livesession/user-commands"); res.Code != 404 {
		t.Fatalf("closed lead list: %d", res.Code)
	}
	if res := f.run("build", c.Revision, "click-auth-00004", false); res.Code != 404 {
		t.Fatalf("closed lead run: %d", res.Code)
	}
	f.svc.mu.Lock()
	f.svc.open = nil
	f.svc.mu.Unlock()
	f.d.SetUserCommands(nil)
	if res := composerGet(f.d, "/s/livesession/user-commands"); res.Code != 503 {
		t.Fatalf("unbound user commands: %d", res.Code)
	}
}

// TestUserCommandRemoveRacesAClick: the user's removal is the agent's service;
// a removal before a click makes the click withdrawn, one after it leaves the
// run alone; a stale removal removes nothing.
func TestUserCommandRemoveRacesAClick(t *testing.T) {
	f := startUserCommandFixture(t)
	old := f.register(t, "build", "make")
	cur := f.register(t, "build", "make v2")
	res := ucPost(f.d, "/s/livesession/user-commands/build/remove", map[string]any{"revision": old.Revision})
	var l userCommandList
	if res.Code != 409 || json.Unmarshal(res.Body.Bytes(), &l) != nil || l.Error != "stale" || len(l.Commands) != 1 {
		t.Fatalf("stale removal: %d %s", res.Code, res.Body)
	}
	// A click wins the race: it runs, then the removal withdraws the command.
	if res := f.run("build", cur.Revision, "click-race-00001", false); res.Code != 202 {
		t.Fatalf("click: %d", res.Code)
	}
	res = ucPost(f.d, "/s/livesession/user-commands/build/remove", map[string]any{"revision": cur.Revision})
	l = userCommandList{}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &l) != nil || len(l.Commands) != 0 || l.Error != "" {
		t.Fatalf("removal: %d %s", res.Code, res.Body)
	}
	waitCard(t, f.d, "click-race-00001", "done")
	f.launches(t, 1)
	// The removal wins the race: the click on the withdrawn command runs nothing.
	if res := f.run("build", cur.Revision, "click-race-00002", false); res.Code != 409 {
		t.Fatalf("click after removal: %d", res.Code)
	}
	if n := f.svc.launchCount(); n != 1 {
		t.Fatalf("%d launches", n)
	}
	// Removing what is gone answers the list, not an error.
	if res := ucPost(f.d, "/s/livesession/user-commands/build/remove", map[string]any{}); res.Code != 200 {
		t.Fatalf("remove of a missing command: %d", res.Code)
	}
}

// TestUserCommandInteractiveReadsUseTheCachedScope: a plain list read asks for
// the cached worktree scope; a click, a removal and a refused write's refresh
// ask for it afresh.
func TestUserCommandInteractiveReadsUseTheCachedScope(t *testing.T) {
	f := startUserCommandFixture(t)
	c := f.register(t, "build", "make")
	for range 5 {
		f.list(t)
	}
	f.users.mu.Lock()
	cached, fresh := f.users.cachedReads, f.users.freshReads
	f.users.mu.Unlock()
	if cached != 5 || fresh != 0 {
		t.Fatalf("list reads: %d cached, %d fresh", cached, fresh)
	}
	f.run("build", c.Revision+99, "click-scope-0001", false) // stale: a fresh Get and a fresh list
	f.users.mu.Lock()
	fresh = f.users.freshReads
	f.users.mu.Unlock()
	if fresh != 2 {
		t.Fatalf("a rejected click made %d fresh reads, want 2", fresh)
	}
}

// TestUserCommandRevisionEventOnTheStream: a stream to a viewer on this machine
// sends the opaque usercmd revision on connect and again after each change, and
// nothing while the revision stands.
func TestUserCommandRevisionEventOnTheStream(t *testing.T) {
	f := startUserCommandFixture(t)
	events := openSSE(t, f.base+"/s/livesession/events")
	revs := func(got []sseEvent) (out []string) {
		for _, ev := range got {
			if ev.name != "usercmd" {
				continue
			}
			var p struct {
				Rev string `json:"rev"`
			}
			if err := json.Unmarshal([]byte(ev.data), &p); err != nil || p.Rev == "" {
				t.Fatalf("usercmd event %q: %v", ev.data, err)
			}
			out = append(out, p.Rev)
		}
		return out
	}
	first := revs(mustCollect(t, events))
	if len(first) != 1 || first[0] != f.users.Revision("livesession") {
		t.Fatalf("on connect: %v", first)
	}
	if got := revs(mustCollect(t, events)); len(got) != 0 {
		t.Fatalf("an unchanged revision was sent again: %v", got)
	}
	f.register(t, "build", "make")
	var after []string
	waitFor(t, "the usercmd event after a registration", func() bool {
		got, _ := collect(events)
		after = append(after, revs(got)...)
		return len(after) > 0
	})
	if len(after) != 1 || after[0] != f.users.Revision("livesession") || after[0] == first[0] {
		t.Fatalf("after a registration: %v (was %v)", after, first)
	}
	// Another lead's change moves no revision of this one.
	if _, _, err := f.users.reg.Set("boardsession", f.users.root, wsusercmd.Input{ID: "x", Label: "x", Line: "true"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := revs(mustCollect(t, events)); len(got) != 0 {
		t.Fatalf("another lead's change reached this stream: %v", got)
	}
}

func mustCollect(t *testing.T, events <-chan sseEvent) []sseEvent {
	t.Helper()
	got, closed := collect(events)
	if closed {
		t.Fatal("stream closed")
	}
	return got
}

// TestUserCommandRevisionNeedsAServiceAndASession: no revision is sent for a
// page that names no session or while no service is bound.
func TestUserCommandRevisionNeedsAServiceAndASession(t *testing.T) {
	f := startUserCommandFixture(t)
	f.register(t, "build", "make")
	if f.d.userCommandRev("") != "" {
		t.Fatal("a revision for no session")
	}
	f.d.SetUserCommands(nil)
	if f.d.userCommandRev("livesession") != "" {
		t.Fatal("a revision without a service")
	}
}

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return strings.TrimSpace(string(raw))
}
