package mcp

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscron"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

type cronTestClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*cronTestTimer
}

type cronTestTimer struct {
	c              *cronTestClock
	at             time.Time
	f              func()
	stopped, fired bool
}

func (t *cronTestTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

func (c *cronTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *cronTestClock) AfterFunc(d time.Duration, f func()) wscron.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &cronTestTimer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (c *cronTestClock) Advance(d time.Duration) {
	target := c.Now().Add(d)
	for {
		c.mu.Lock()
		var best *cronTestTimer
		for _, t := range c.timers {
			if !t.stopped && !t.fired && !t.at.After(target) && (best == nil || t.at.Before(best.at)) {
				best = t
			}
		}
		if best == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		best.fired = true
		if best.at.After(c.now) {
			c.now = best.at
		}
		c.mu.Unlock()
		best.f()
	}
}

// cronTestSessions delivers into real inbox directories; owners in leads are
// hosted leads, owners in workers are hosted workers, any other is unknown.
type cronTestSessions struct {
	root    string
	leads   map[string]bool
	workers map[string]bool
}

func (s *cronTestSessions) inbox(owner string) string {
	return wsinbox.Dir(filepath.Join(s.root, owner))
}

func (s *cronTestSessions) IsLead(id string) (bool, error) {
	if s.leads[id] {
		return true, nil
	}
	if s.workers[id] {
		return false, nil
	}
	return false, errors.New("conversation is closed or unknown")
}

func (s *cronTestSessions) Exists(id string) bool { return s.leads[id] || s.workers[id] }

func (s *cronTestSessions) HeldSchedules(id string) []string {
	held, _ := wsinbox.Held(s.inbox(id))
	var out []string
	for _, r := range held {
		if r.Entry.Cron != nil {
			out = append(out, r.Entry.Cron.ID)
		}
	}
	return out
}

func (s *cronTestSessions) Deliver(id string, mark wsinbox.Cron, prompt string) (wscron.Delivery, error) {
	if !s.leads[id] {
		return wscron.Suppressed, nil
	}
	_, _, err := wsinbox.Send(s.inbox(id), wsinbox.Entry{Parts: []string{prompt}, Cron: &mark}, time.Now())
	return wscron.Delivered, err
}

type cronFixture struct {
	t     *testing.T
	root  string
	key   string
	clock *cronTestClock
	sess  *cronTestSessions
	path  string
	sched *wscron.Scheduler
	s     *Server
}

func newCronFixture(t *testing.T) *cronFixture {
	t.Helper()
	useLeadProfile(t)
	root := t.TempDir()
	mustWrite(t, root, "README.md", "x\n")
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	f := &cronFixture{
		t: t, root: root,
		clock: &cronTestClock{now: time.Date(2026, 3, 10, 0, 0, 30, 0, time.UTC)},
		sess:  &cronTestSessions{root: t.TempDir(), leads: map[string]bool{"lead": true}, workers: map[string]bool{"worker": true}},
		path:  filepath.Join(t.TempDir(), wscron.StoreName),
	}
	f.openScheduler()
	s := NewServer(root, "test")
	f.key, _ = parseLoginResponse(t, callLogin(t, s, 900002, root, nil))
	s.conversation = "lead"
	f.s = s
	f.bind()
	return f
}

func (f *cronFixture) openScheduler() {
	f.t.Helper()
	store, err := wscron.OpenStore(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.sched, err = wscron.New(wscron.Options{Store: store, Sessions: f.sess, Clock: f.clock})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = f.sched.Close() })
}

func (f *cronFixture) bind() {
	sched := f.sched
	f.s.cron = func() *wscron.Scheduler { return sched }
}

func (f *cronFixture) restart() {
	f.t.Helper()
	if err := f.sched.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.openScheduler()
	f.bind()
}

func (f *cronFixture) call(args map[string]any) (string, bool) {
	f.t.Helper()
	return callExecTool(f.t, f.s, f.key, "cron", args)
}

func (f *cronFixture) add(name, expr, prompt string, tz string) (string, bool) {
	spec := map[string]any{"name": name, "expr": expr, "prompt": prompt}
	if tz != "" {
		spec["tz"] = tz
	}
	return f.call(map[string]any{"add": spec})
}

func TestCronToolAddListDelete(t *testing.T) {
	f := newCronFixture(t)
	if text, isErr := f.call(nil); isErr || text != "no schedules" {
		t.Fatalf("empty list = %q, %v", text, isErr)
	}
	text, isErr := f.add("daily", "0 9 * * *", "do the daily review", "")
	if isErr || !strings.Contains(text, "id: cron-") || !strings.Contains(text, "next_at: 2026-03-10T09:00:00Z") {
		t.Fatalf("add = %q, %v", text, isErr)
	}
	id := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(text, "\n", 2)[0], "id:"))

	list, isErr := f.call(nil)
	if isErr || !strings.HasPrefix(list, "1 schedule\n") || !strings.Contains(list, id+"  daily  \"0 9 * * *\"  tz=UTC  next=2026-03-10T09:00:00Z  last=-  pending=no  dropped=0") {
		t.Fatalf("list = %q, %v", list, isErr)
	}
	raw, isErr := f.call(map[string]any{"format": "json"})
	var got []map[string]any
	if err := json.Unmarshal([]byte(raw), &got); isErr || err != nil || len(got) != 1 {
		t.Fatalf("json list = %q, %v, %v", raw, isErr, err)
	}
	e := got[0]
	if e["id"] != id || e["name"] != "daily" || e["expr"] != "0 9 * * *" || e["tz"] != "UTC" || e["next_at"] != "2026-03-10T09:00:00Z" ||
		e["last_at"] != nil || e["pending"] != false || e["dropped"] != float64(0) {
		t.Fatalf("json entry = %v", e)
	}
	addJSON, isErr := f.call(map[string]any{"format": "json", "add": map[string]any{"name": "weekly", "expr": "@weekly", "prompt": "p", "tz": "+09:00"}})
	var added map[string]any
	if err := json.Unmarshal([]byte(addJSON), &added); isErr || err != nil || !strings.HasPrefix(added["id"].(string), "cron-") || added["next_at"] != "2026-03-15T00:00:00+09:00" {
		t.Fatalf("json add = %q, %v, %v", addJSON, isErr, err)
	}

	if text, isErr := f.add("daily", "@hourly", "again", ""); !isErr || !strings.Contains(text, "already exists") {
		t.Fatalf("duplicate = %q, %v", text, isErr)
	}
	if text, isErr := f.call(map[string]any{"add": map[string]any{"name": "x", "expr": "@daily", "prompt": "p"}, "delete": "daily"}); !isErr || !strings.Contains(text, "not both") {
		t.Fatalf("add and delete = %q, %v", text, isErr)
	}
	if n := len(f.sched.List("lead")); n != 2 {
		t.Fatalf("the refused add/delete changed the list: %d schedules", n)
	}
	if text, isErr := f.call(map[string]any{"delete": id}); isErr || text != "ok" {
		t.Fatalf("delete by id = %q, %v", text, isErr)
	}
	if text, isErr := f.call(map[string]any{"delete": id}); !isErr || !strings.Contains(text, "unknown schedule") {
		t.Fatalf("second delete = %q, %v", text, isErr)
	}
	if text, isErr := f.call(map[string]any{"delete": "weekly"}); isErr || text != "ok" {
		t.Fatalf("delete by name = %q, %v", text, isErr)
	}
	if text, _ := f.call(nil); text != "no schedules" {
		t.Fatalf("list after deletes = %q", text)
	}
	// Errors name the field.
	if text, isErr := f.add("bad", "* * * * * *", "p", ""); !isErr || !strings.Contains(text, "fields") {
		t.Fatalf("bad expr = %q, %v", text, isErr)
	}
	if text, isErr := f.add("bad", "@daily", "p", "Asia/Seoul"); !isErr || !strings.Contains(text, "tz") {
		t.Fatalf("bad tz = %q, %v", text, isErr)
	}
	if text, isErr := f.call(map[string]any{"add": map[string]any{"name": "x", "expr": "@daily"}}); !isErr || !strings.Contains(text, "add.prompt") {
		t.Fatalf("missing prompt = %q, %v", text, isErr)
	}
	if text, isErr := f.call(map[string]any{"add": map[string]any{"name": "x", "expr": "@daily", "prompt": "p", "once": true}}); !isErr || !strings.Contains(text, "once") {
		t.Fatalf("unknown add field = %q, %v", text, isErr)
	}
}

func TestCronToolFireEndToEnd(t *testing.T) {
	f := newCronFixture(t)
	if text, isErr := f.add("tick", "*/5 * * * *", "check the board", ""); isErr {
		t.Fatal(text)
	}
	f.clock.Advance(4*time.Minute + 29*time.Second)
	if held, _ := wsinbox.Held(f.sess.inbox("lead")); len(held) != 0 {
		t.Fatalf("delivered early: %+v", held)
	}
	f.clock.Advance(time.Second)
	held, _ := wsinbox.Held(f.sess.inbox("lead"))
	if len(held) != 1 || !strings.Contains(held[0].Entry.Text(), `name="tick" at="2026-03-10T00:05:00Z">`+"\ncheck the board\n</ws:cron>") {
		t.Fatalf("held = %+v", held)
	}
	if list, _ := f.call(nil); !strings.Contains(list, "pending=yes") {
		t.Fatalf("list = %q", list)
	}
}

func TestCronToolPersistsAcrossARestart(t *testing.T) {
	f := newCronFixture(t)
	if text, isErr := f.add("tick", "* * * * *", "p", "-05:30"); isErr {
		t.Fatal(text)
	}
	before, _ := f.call(map[string]any{"format": "json"})
	f.restart()
	after, _ := f.call(map[string]any{"format": "json"})
	if !strings.Contains(after, `"name":"tick"`) || !strings.Contains(after, `"tz":"-05:30"`) {
		t.Fatalf("list after restart = %q (before %q)", after, before)
	}
	f.clock.Advance(time.Minute)
	if held, _ := wsinbox.Held(f.sess.inbox("lead")); len(held) != 1 {
		t.Fatalf("a fire after the restart delivered %d entries", len(held))
	}
}

func TestCronToolRefusals(t *testing.T) {
	f := newCronFixture(t)
	args := map[string]any{"add": map[string]any{"name": "x", "expr": "@daily", "prompt": "p"}}

	// A launcher or CLI connection: no trusted binding.
	f.s.conversation = ""
	if text, isErr := f.call(args); !isErr || !strings.Contains(text, "not one") {
		t.Fatalf("unbound = %q, %v", text, isErr)
	}
	// A worker's connection: bound, but not a lead.
	f.s.conversation = "worker"
	if text, isErr := f.call(args); !isErr || !strings.Contains(text, "lead session only") {
		t.Fatalf("worker = %q, %v", text, isErr)
	}
	// A token that resolves to a conversation the daemon does not know.
	f.s.conversation = "ghost"
	if text, isErr := f.call(nil); !isErr || !strings.Contains(text, "no longer known") || strings.Contains(text, "worker") {
		t.Fatalf("unknown conversation = %q, %v", text, isErr)
	}
	// A daemon whose store failed to open.
	f.s.conversation = "lead"
	f.s.cron = nil
	if text, isErr := f.call(args); !isErr || !strings.Contains(text, "could not open") {
		t.Fatalf("no store = %q, %v", text, isErr)
	}
	f.s.cron = func() *wscron.Scheduler { return nil }
	if text, isErr := f.call(args); !isErr || !strings.Contains(text, "could not open") {
		t.Fatalf("nil scheduler = %q, %v", text, isErr)
	}
	f.bind()
	if n := len(f.sched.List("lead")) + len(f.sched.List("worker")) + len(f.sched.List("ghost")); n != 0 {
		t.Fatalf("a refused call wrote %d schedules", n)
	}
	msgs := map[string]bool{errCronNotHosted.Error(): true, errCronLeadOnly.Error(): true, errCronUnavailable.Error(): true, errCronEnded.Error(): true}
	if len(msgs) != 4 {
		t.Fatal("refusal messages are not distinct")
	}

	// A hosted lead passing another session's key cannot add to that session:
	// the owner is always the connection's own binding.
	other := t.TempDir()
	initGit(t, other)
	otherKey, _ := parseLoginResponse(t, callLogin(t, NewServer(other, "test"), 900003, other, nil))
	if text, isErr := callExecTool(t, f.s, otherKey, "cron", args); isErr {
		t.Fatalf("add with a foreign key = %q", text)
	}
	if n := len(f.sched.List("lead")); n != 1 {
		t.Fatalf("the schedule went to the connection's own session: %d", n)
	}
	if n := len(f.sched.List("ghost")) + len(f.sched.List(otherKey)); n != 0 {
		t.Fatal("a supplied session_key chose the owner")
	}
}

func TestCronToolEndsWithItsSession(t *testing.T) {
	f := newCronFixture(t)
	if text, isErr := f.add("tick", "* * * * *", "p", ""); isErr {
		t.Fatal(text)
	}
	// The runner's permanent-removal notice (ForgetClosed -> ConversationGone).
	// The real ForgetClosed on a hosted lead, with the stored rows checked at
	// once, is wsagent's TestForgetClosedRemovesAHostedLeadsStoredSchedules.
	f.sess.leads["lead"] = false
	f.sess.workers["lead"] = false
	f.sched.ConversationGone("lead")
	if text, isErr := f.call(nil); !isErr || !strings.Contains(text, "no longer known") || strings.Contains(text, "worker") {
		t.Fatalf("list for the dead session = %q, %v", text, isErr)
	}
	f.restart()
	if n := len(f.sched.List("lead")); n != 0 {
		t.Fatalf("%d schedules came back", n)
	}
}

func TestCronToolSchemaInventoryAndShippedText(t *testing.T) {
	var cron map[string]any
	for _, tool := range tools() {
		if tool["name"] == "cron" {
			cron = tool
		}
	}
	if cron == nil {
		t.Fatal("cron is not registered")
	}
	schema := cron["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for _, name := range []string{"add", "delete", "session_key", "format"} {
		if _, ok := props[name]; !ok {
			t.Errorf("cron schema lacks %s", name)
		}
	}
	if schema["required"] != nil {
		t.Errorf("cron has required arguments: %v", schema["required"])
	}
	// A lead's connection lists it; a worker's call is refused by the
	// handler's lead check (TestCronToolRefusals), and no worker allowlist
	// names it (wsagent's TestCronIsNotAChildTool).
	s := NewServer(t.TempDir(), "test")
	if !s.toolAllowed("cron") {
		t.Fatal("a lead connection does not list cron")
	}
	if !slices.Contains(LeadToolNames(), "cron") {
		t.Fatal("the lead tool inventory lacks cron")
	}
	// Shipped text names nothing of this repository and no namespace form that
	// the other distribution would have to rewrite.
	description, _ := cron["description"].(string)
	for _, leak := range []string{"ws/", "wsflow", "ws:", "agents-plugin", "ai-docs", "ticket", "devenv", "scripts/install-claude-plugin.sh"} {
		if strings.Contains(description, leak) {
			t.Errorf("cron description contains %q", leak)
		}
	}
	if regexp.MustCompile(`\b26\d{4}-|\b[0-9a-f]{7,40}\b`).MatchString(description) {
		t.Errorf("cron description cites a ticket stem or commit: %s", description)
	}
	for _, want := range []string{"@reboot", "UTC", "+HH:MM", "32 schedules", "16 KiB", "not caught up", "lead sessions only", "one-shot", "RFC 3339"} {
		if !strings.Contains(description, want) {
			t.Errorf("cron description lacks %q", want)
		}
	}
}

func TestCronToolOneShotAt(t *testing.T) {
	f := newCronFixture(t) // now 2026-03-10T00:00:30Z
	spec := func(kv ...string) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return map[string]any{"add": m}
	}
	text, isErr := f.call(spec("name", "wake", "at", "2026-03-10T09:10:00+09:00", "prompt", "quota reset: resume"))
	if isErr || !strings.Contains(text, "id: cron-") || !strings.Contains(text, "next_at: 2026-03-10T09:10:00+09:00") {
		t.Fatalf("add at = %q, %v", text, isErr)
	}
	id := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(text, "\n", 2)[0], "id:"))
	list, isErr := f.call(nil)
	if isErr || !strings.Contains(list, id+"  wake  at=2026-03-10T09:10:00+09:00  tz=+09:00  next=2026-03-10T09:10:00+09:00  last=-  pending=no  dropped=0") {
		t.Fatalf("list = %q, %v", list, isErr)
	}
	if text, isErr := f.add("daily", "0 9 * * *", "p", ""); isErr {
		t.Fatal(text)
	}
	raw, isErr := f.call(map[string]any{"format": "json"})
	var got []map[string]any
	if err := json.Unmarshal([]byte(raw), &got); isErr || err != nil || len(got) != 2 {
		t.Fatalf("json list = %q, %v, %v", raw, isErr, err)
	}
	for _, e := range got {
		at, hasAt := e["at"]
		switch e["name"] {
		case "wake":
			if at != "2026-03-10T09:10:00+09:00" || e["expr"] != "" {
				t.Errorf("one-shot entry = %v", e)
			}
		case "daily":
			if hasAt {
				t.Errorf("recurring entry carries at: %v", e)
			}
		}
	}
	addJSON, isErr := f.call(map[string]any{"format": "json", "add": map[string]any{"name": "utc", "at": "2026-03-11T00:00:00Z", "prompt": "p"}})
	var added map[string]any
	if err := json.Unmarshal([]byte(addJSON), &added); isErr || err != nil || added["next_at"] != "2026-03-11T00:00:00Z" {
		t.Fatalf("json add at = %q, %v, %v", addJSON, isErr, err)
	}

	// Refusals name the rule and write nothing.
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{spec("name", "x", "expr", "@daily", "at", "2026-03-11T00:00:00Z", "prompt", "p"), "exactly one of expr"},
		{spec("name", "x", "prompt", "p"), "exactly one of expr"},
		{spec("name", "x", "at", "2026-03-11T00:00:00Z", "prompt", "p", "tz", "+09:00"), "add.tz applies to expr only"},
		{spec("name", "x", "at", "tomorrow", "prompt", "p"), "RFC 3339"},
		{spec("name", "x", "at", "2026-03-11 00:00:00", "prompt", "p"), "RFC 3339"},
		{spec("name", "x", "at", "2026-03-10T00:00:30Z", "prompt", "p"), "future"},
		{spec("name", "x", "at", "2026-03-09T00:00:00Z", "prompt", "p"), "future"},
		{spec("name", "wake", "at", "2026-03-12T00:00:00Z", "prompt", "p"), "already exists"},
		{map[string]any{"add": map[string]any{"name": "x", "at": 5, "prompt": "p"}}, "add.at must be a string"},
	} {
		if text, isErr := f.call(tc.args); !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("add %v = %q, %v; want an error containing %q", tc.args, text, isErr, tc.want)
		}
	}
	if n := len(f.sched.List("lead")); n != 3 {
		t.Fatalf("refused adds changed the list: %d schedules", n)
	}

	// It fires once, is gone from the list, and delete by name of a pending
	// one-shot works before it fires.
	if text, isErr := f.call(map[string]any{"delete": "utc"}); isErr || text != "ok" {
		t.Fatalf("delete one-shot by name = %q, %v", text, isErr)
	}
	f.clock.Advance(9*time.Minute + 30*time.Second)
	held, _ := wsinbox.Held(f.sess.inbox("lead"))
	if len(held) != 1 || !strings.Contains(held[0].Entry.Text(), `name="wake" at="2026-03-10T09:10:00+09:00">`+"\nquota reset: resume\n</ws:cron>") {
		t.Fatalf("held = %+v", held)
	}
	if list, _ := f.call(nil); strings.Contains(list, "wake") || !strings.HasPrefix(list, "1 schedule\n") {
		t.Fatalf("list after the one-shot fired = %q", list)
	}
	f.clock.Advance(24 * time.Hour)
	held, _ = wsinbox.Held(f.sess.inbox("lead"))
	wake := 0
	for _, h := range held {
		if h.Entry.Cron != nil && h.Entry.Cron.Name == "wake" {
			wake++
		}
	}
	if wake != 1 {
		t.Fatalf("one-shot delivered %d times", wake)
	}
	f.restart()
	if list, _ := f.call(nil); strings.Contains(list, "wake") {
		t.Fatalf("the delivered one-shot came back after a restart: %q", list)
	}
}
