package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// usercmdFake is the user commands' service over the real registry, with the
// lead's bound worktree steered by the test.
type usercmdFake struct {
	reg *wsusercmd.Registry

	mu        sync.Mutex
	root      string
	rootByID  map[string]string
	openErr   error
}

func (u *usercmdFake) scope(replyID string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.openErr != nil {
		return "", u.openErr
	}
	if r, ok := u.rootByID[replyID]; ok {
		return r, nil
	}
	return u.root, nil
}

func (u *usercmdFake) List(replyID string) ([]wsusercmd.Command, string, error) {
	root, err := u.scope(replyID)
	if err != nil {
		return nil, "", err
	}
	cmds, rev := u.reg.Snapshot(replyID, root)
	return cmds, rev + ".t" + strconv.Itoa(len(root)), nil
}

func (u *usercmdFake) Get(replyID, source, id string) (wsusercmd.Command, string, bool, error) {
	root, err := u.scope(replyID)
	if err != nil {
		return wsusercmd.Command{}, "", false, err
	}
	c, ok := u.reg.GetBySource(replyID, root, source, id)
	return c, root, ok, nil
}

func (u *usercmdFake) Create(replyID string, in wsusercmd.Input) (wsusercmd.Command, error) {
	root, err := u.scope(replyID)
	if err != nil {
		return wsusercmd.Command{}, err
	}
	if _, err := wsusercmd.ValidateWorktreeCwd(root, in.Cwd); err != nil {
		return wsusercmd.Command{}, err
	}
	return u.reg.CreateUser(root, in)
}

func (u *usercmdFake) PatchIf(replyID, source, id string, rev int64, patch wsusercmd.Patch) (wsusercmd.Command, error) {
	root, err := u.scope(replyID)
	if err != nil {
		return wsusercmd.Command{}, err
	}
	if patch.Cwd != nil {
		if _, err := wsusercmd.ValidateWorktreeCwd(root, *patch.Cwd); err != nil {
			return wsusercmd.Command{}, err
		}
	}
	return u.reg.PatchIf(replyID, root, source, id, rev, patch)
}

func (u *usercmdFake) RemoveIf(replyID, source, id string, rev int64) error {
	root, err := u.scope(replyID)
	if err != nil {
		return err
	}
	return u.reg.RemoveIfSource(replyID, root, source, id, rev)
}

func (u *usercmdFake) Revision(replyID string) string {
	root, _ := u.scope(replyID)
	return u.reg.RevisionFor(replyID, root) + ".t" + strconv.Itoa(len(root))
}

type usercmdFixture struct {
	*composerFixture
	users *usercmdFake
}

func startUserCommandFixture(t *testing.T) *usercmdFixture {
	t.Helper()
	f := startComposerFixture(t)
	root := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	users := &usercmdFake{root: root}
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
	if got.Line != "make all\necho done" || got.Root != f.users.root || got.Window != ComposerWindow || got.Gen != 4 || got.ReplyID != "livesession" {
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
	cur, _, _, _ := f.users.Get("livesession", wsusercmd.SourceAgent, "build")
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
	// Removing what is gone answers the current list as withdrawn, and a removal
	// that names no revision is refused outright (zero, missing or negative).
	res = ucPost(f.d, "/s/livesession/user-commands/build/remove", map[string]any{"revision": cur.Revision})
	l = userCommandList{}
	if res.Code != 409 || json.Unmarshal(res.Body.Bytes(), &l) != nil || l.Error != "withdrawn" {
		t.Fatalf("remove of a gone command: %d %s", res.Code, res.Body)
	}
	for _, body := range []map[string]any{{}, {"revision": 0}, {"revision": -3}} {
		if res := ucPost(f.d, "/s/livesession/user-commands/build/remove", body); res.Code != 400 {
			t.Fatalf("removal without a revision %v: %d", body, res.Code)
		}
	}
	keep := f.register(t, "keep", "true")
	if res := ucPost(f.d, "/s/livesession/user-commands/keep/remove", map[string]any{}); res.Code != 400 {
		t.Fatalf("revisionless removal: %d", res.Code)
	}
	if got, ok := f.users.reg.Get("livesession", f.users.root, "keep"); !ok || got != keep {
		t.Fatal("a revisionless removal removed the command")
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
	// Another worktree's change moves no revision of this one.
	if _, _, err := f.users.reg.Set("boardsession", filepath.Join(filepath.Dir(f.users.root), "elsewhere"), wsusercmd.Input{ID: "x", Label: "x", Line: "true"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := revs(mustCollect(t, events)); len(got) != 0 {
		t.Fatalf("another worktree's change reached this stream: %v", got)
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

// TestUserCommandWithdrawalRacesTheClaim: the revision check and the claim of a
// click are one step. With concurrent clicks sharing one submission id and a
// withdrawal landing at an arbitrary moment, every request gets the same
// outcome as the first: one launch of the displayed line with every other
// request mapped to its card, or no launch and every request refused.
func TestUserCommandWithdrawalRacesTheClaim(t *testing.T) {
	f := startUserCommandFixture(t)
	for round := range 40 {
		id := "race" + jsonNumber(int64(round))
		c := f.register(t, id, "echo "+id)
		sub := "click-" + id + "-000001"
		before := f.svc.launchCount()
		var wg sync.WaitGroup
		type answer struct {
			code int
			card composerCard
		}
		answers := make(chan answer, 6)
		start := make(chan struct{})
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				res := f.run(id, c.Revision, sub, false)
				var a answer
				a.code = res.Code
				_ = json.Unmarshal(res.Body.Bytes(), &a.card)
				answers <- a
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = f.users.reg.RemoveIf("livesession", f.users.root, id, c.Revision)
		}()
		close(start)
		wg.Wait()
		close(answers)
		fresh, known, refused := 0, 0, 0
		for a := range answers {
			switch a.code {
			case 202:
				fresh++
			case 200:
				known++
				if a.card.ID != sub || a.card.Command != "echo "+id {
					t.Fatalf("round %d: a duplicate got card %+v", round, a.card)
				}
			case 409:
				refused++
			default:
				t.Fatalf("round %d: answer %d", round, a.code)
			}
		}
		switch {
		case fresh == 1 && refused == 0 && known == 4:
			waitFor(t, "the launch", func() bool { return f.svc.launchCount() == before+1 })
			if line := f.svc.launches[before].Line; line != "echo "+id {
				t.Fatalf("round %d: ran %q", round, line)
			}
		case fresh == 0 && known == 0 && refused == 5:
			time.Sleep(time.Millisecond)
			if f.svc.launchCount() != before {
				t.Fatalf("round %d: a refused click launched", round)
			}
		default:
			t.Fatalf("round %d: mixed outcome: %d fresh, %d known, %d refused", round, fresh, known, refused)
		}
	}
}

func ucPut(d *Daemon, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	return composerHTTP(d, http.MethodPut, path, string(raw), "http://127.0.0.1", "127.0.0.1:1234", nil)
}

// TestUserCommandCreateAndUpdateRoutes covers POST /user-commands, PUT /user-commands/<id>,
// cross-origin gating, and verifies PUT /composer-exec remains 405.
func TestUserCommandCreateAndUpdateRoutes(t *testing.T) {
	f := startUserCommandFixture(t)
	sub := filepath.Join(f.users.root, "backend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// PUT /composer-exec must stay 405 Method Not Allowed.
	if res := ucPut(f.d, "/s/livesession/composer-exec", map[string]any{"submission_id": "x", "line": "true"}); res.Code != 405 {
		t.Fatalf("PUT /composer-exec = %d, want 405", res.Code)
	}

	// Cross-origin POST/PUT rejected with 403.
	if res := composerHTTP(f.d, http.MethodPost, "/s/livesession/user-commands", `{"label":"X","line":"true"}`, "http://evil.test", "127.0.0.1:1", nil); res.Code != 403 {
		t.Fatalf("cross-origin POST create = %d, want 403", res.Code)
	}
	if res := composerHTTP(f.d, http.MethodPut, "/s/livesession/user-commands/x", `{"revision":1,"line":"true"}`, "http://evil.test", "127.0.0.1:1", nil); res.Code != 403 {
		t.Fatalf("cross-origin PUT update = %d, want 403", res.Code)
	}

	// Bad cwd or missing label/line returns 400.
	if res := ucPost(f.d, "/s/livesession/user-commands", map[string]any{"label": "Bad", "line": "true", "cwd": "../escape"}); res.Code != 400 {
		t.Fatalf("create bad cwd = %d %s", res.Code, res.Body)
	}
	if res := ucPost(f.d, "/s/livesession/user-commands", map[string]any{"label": "", "line": "true"}); res.Code != 400 {
		t.Fatalf("create empty label = %d %s", res.Code, res.Body)
	}

	// Valid POST /user-commands returns 201 with created command + refreshed list.
	res := ucPost(f.d, "/s/livesession/user-commands", map[string]any{
		"label":  "Go Tests",
		"line":   "go test ./...",
		"prompt": "Fix failing tests",
		"cwd":    "backend",
	})
	var created userCommandList
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &created) != nil || created.Command == nil || created.Command.ID != "go-tests" || created.Command.Source != wsusercmd.SourceUser || len(created.Commands) != 1 {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}

	// Explicit duplicate ID returns 409.
	if res := ucPost(f.d, "/s/livesession/user-commands", map[string]any{"id": "go-tests", "label": "Dup", "line": "true"}); res.Code != 409 {
		t.Fatalf("duplicate id = %d %s", res.Code, res.Body)
	}

	// PUT /user-commands/<id> updates line & prompt atomically.
	res = ucPut(f.d, "/s/livesession/user-commands/go-tests", map[string]any{
		"source":   wsusercmd.SourceUser,
		"revision": created.Command.Revision,
		"line":     "go test -race ./...",
		"prompt":   "Investigate race",
	})
	var updated userCommandList
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &updated) != nil || updated.Command == nil || updated.Command.Line != "go test -race ./..." || updated.Command.Prompt != "Investigate race" {
		t.Fatalf("put user = %d %s", res.Code, res.Body)
	}

	// Stale revision returns 409 with error="stale".
	res = ucPut(f.d, "/s/livesession/user-commands/go-tests", map[string]any{
		"source":   wsusercmd.SourceUser,
		"revision": created.Command.Revision,
		"line":     "go test",
	})
	var stale userCommandList
	if res.Code != 409 || json.Unmarshal(res.Body.Bytes(), &stale) != nil || stale.Error != "stale" {
		t.Fatalf("stale put = %d %s", res.Code, res.Body)
	}

	// Bad cwd on PUT returns 400.
	if res := ucPut(f.d, "/s/livesession/user-commands/go-tests", map[string]any{
		"source":   wsusercmd.SourceUser,
		"revision": updated.Command.Revision,
		"cwd":      "../escape",
	}); res.Code != 400 {
		t.Fatalf("bad cwd put = %d %s", res.Code, res.Body)
	}

	// PUT also edits an agent-scoped command (keeping source="agent").
	ag := f.register(t, "agent-tests", "make agent")
	res = ucPut(f.d, "/s/livesession/user-commands/agent-tests", map[string]any{
		"source":   wsusercmd.SourceAgent,
		"revision": ag.Revision,
		"line":     "make agent-edited",
		"prompt":   "Edited agent prompt",
	})
	var agUp userCommandList
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &agUp) != nil || agUp.Command == nil || agUp.Command.Source != wsusercmd.SourceAgent || agUp.Command.Line != "make agent-edited" {
		t.Fatalf("put agent = %d %s", res.Code, res.Body)
	}

	// Remove user command leaves the worktree's agent command intact.
	res = ucPost(f.d, "/s/livesession/user-commands/go-tests/remove", map[string]any{
		"source":   wsusercmd.SourceUser,
		"revision": updated.Command.Revision,
	})
	if res.Code != 200 {
		t.Fatalf("remove user = %d %s", res.Code, res.Body)
	}
	if _, _, ok, _ := f.users.Get("livesession", wsusercmd.SourceAgent, "agent-tests"); !ok {
		t.Fatal("removing user command removed the agent command")
	}
}

// TestUserCommandRunOverridesCwdFenceAndIdempotency verifies inline line/cwd
// overrides, synchronous 400 cwd fence before slot claim, and composerKnown
// binding to (source, id, revision, hasLine, lineOverride, hasCwd, cwdOverride).
func TestUserCommandRunOverridesCwdFenceAndIdempotency(t *testing.T) {
	f := startUserCommandFixture(t)
	sub := filepath.Join(f.users.root, "backend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ag := f.register(t, "agbuild", "make agent")
	us, err := f.users.Create("livesession", wsusercmd.Input{ID: "build", Label: "User Build", Line: "make user", Cwd: "backend"})
	if err != nil {
		t.Fatal(err)
	}

	// Bad cwd override fails synchronously with 400 BEFORE claiming the submission slot.
	res := ucPost(f.d, "/s/livesession/user-commands/build/run", map[string]any{
		"submission_id": "sub-fence-0001",
		"revision":      us.Revision,
		"source":        wsusercmd.SourceUser,
		"cwd":           "../escape",
		"token":         f.d.composer.token(),
	})
	if res.Code != 400 {
		t.Fatalf("bad cwd run = %d %s, want 400", res.Code, res.Body)
	}
	if f.svc.launchCount() != 0 {
		t.Fatalf("bad cwd launched %d jobs", f.svc.launchCount())
	}

	// The same submission_id is NOT fenced and runs cleanly with valid line+cwd override.
	res = ucPost(f.d, "/s/livesession/user-commands/build/run", map[string]any{
		"submission_id": "sub-fence-0001",
		"revision":      us.Revision,
		"source":        wsusercmd.SourceUser,
		"line":          "make user --dry-run",
		"cwd":           "backend",
		"token":         f.d.composer.token(),
	})
	if res.Code != 202 {
		t.Fatalf("valid override run = %d %s", res.Code, res.Body)
	}
	f.launches(t, 1)
	if got := f.svc.launches[0]; got.Line != "make user --dry-run" || got.Cwd != "backend" {
		t.Fatalf("launch = %+v", got)
	}

	// Reusing the same submission_id with a different line override or source returns 409.
	res = ucPost(f.d, "/s/livesession/user-commands/build/run", map[string]any{
		"submission_id": "sub-fence-0001",
		"revision":      us.Revision,
		"source":        wsusercmd.SourceUser,
		"line":          "make user --other",
		"cwd":           "backend",
		"token":         f.d.composer.token(),
	})
	if res.Code != 409 {
		t.Fatalf("different line same submission_id = %d, want 409", res.Code)
	}
	res = ucPost(f.d, "/s/livesession/user-commands/build/run", map[string]any{
		"submission_id": "sub-fence-0001",
		"revision":      ag.Revision,
		"source":        wsusercmd.SourceAgent,
		"token":         f.d.composer.token(),
	})
	if res.Code != 409 {
		t.Fatalf("different source same submission_id = %d, want 409", res.Code)
	}

	// Removing the command afterwards still lets a retry of the exact override tuple return 200.
	if err := f.users.RemoveIf("livesession", wsusercmd.SourceUser, "build", us.Revision); err != nil {
		t.Fatal(err)
	}
	res = ucPost(f.d, "/s/livesession/user-commands/build/run", map[string]any{
		"submission_id": "sub-fence-0001",
		"revision":      us.Revision,
		"source":        wsusercmd.SourceUser,
		"line":          "make user --dry-run",
		"cwd":           "backend",
		"retry":         true,
		"token":         f.d.composer.token(),
	})
	if res.Code != 200 || decodeCard(t, res).Command != "make user --dry-run" {
		t.Fatalf("retry after withdrawal = %d %s", res.Code, res.Body)
	}
}

// TestUserCommandRootScopedSSEFanOut verifies that a user-scoped command change
// pushes a usercmd SSE event to every session on the same root and NOT to a
// session bound to a different root.
func TestUserCommandRootScopedSSEFanOut(t *testing.T) {
	f := startUserCommandFixture(t)
	otherRoot := filepath.Join(t.TempDir(), "other-wt")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed two additional sessions: peersession (same root) and foreignsession (different root).
	now := time.Now()
	for _, spec := range []struct{ id, root string }{
		{"peersession", f.users.root},
		{"foreignsession", otherRoot},
	} {
		register(t, f.dir, spec.id, spec.root, now)
		r, err := ReadRegistration(f.dir, spec.id)
		if err != nil {
			t.Fatal(err)
		}
		r.Hosted = true
		if err := WriteRegistration(f.dir, r); err != nil {
			t.Fatal(err)
		}
		f.src.setPage(spec.id, PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, spec.id)})
	}
	f.d.Index().Refresh()
	waitFor(t, "peer and foreign sessions registered", func() bool {
		_, ok1 := f.d.Index().Session("peersession")
		_, ok2 := f.d.Index().Session("foreignsession")
		return ok1 && ok2
	})
	f.users.mu.Lock()
	f.users.rootByID = map[string]string{
		"livesession":    f.users.root,
		"peersession":    f.users.root,
		"foreignsession": otherRoot,
	}
	f.users.mu.Unlock()

	evLive := openSSE(t, f.base+"/s/livesession/events")
	evPeer := openSSE(t, f.base+"/s/peersession/events")
	evForeign := openSSE(t, f.base+"/s/foreignsession/events")

	revs := func(got []sseEvent) (out []string) {
		for _, ev := range got {
			if ev.name == "usercmd" {
				var p struct{ Rev string `json:"rev"` }
				if err := json.Unmarshal([]byte(ev.data), &p); err == nil && p.Rev != "" {
					out = append(out, p.Rev)
				}
			}
		}
		return out
	}
	// Drain initial connect events.
	_ = revs(mustCollect(t, evLive))
	_ = revs(mustCollect(t, evPeer))
	_ = revs(mustCollect(t, evForeign))

	// Create a user-scoped command from livesession.
	if res := ucPost(f.d, "/s/livesession/user-commands", map[string]any{"label": "Shared", "line": "echo shared"}); res.Code != 201 {
		t.Fatalf("create shared: %d %s", res.Code, res.Body)
	}
	var liveGot, peerGot []string
	waitFor(t, "both same-root sessions receive usercmd", func() bool {
		if g, _ := collect(evLive); len(g) > 0 {
			liveGot = append(liveGot, revs(g)...)
		}
		if g, _ := collect(evPeer); len(g) > 0 {
			peerGot = append(peerGot, revs(g)...)
		}
		return len(liveGot) > 0 && len(peerGot) > 0
	})
	time.Sleep(50 * time.Millisecond)
	if g := revs(mustCollect(t, evForeign)); len(g) != 0 {
		t.Fatalf("foreign-root session received usercmd: %v", g)
	}
}

// TestUserCommandClientSuites runs the user command client suites under Node:
// the v2 popover contracts and the compact two-zone tiles (one-line $ collapse,
// the bash-highlighted overlay editor, inline rename, and its CSS contract).
func TestUserCommandClientSuites(t *testing.T) {
	runNodeTestSuite(t, "testdata/composer-usercmd-v2.test.cjs")
	runNodeTestSuite(t, "testdata/composer-usercmd-compact.test.cjs")
}
