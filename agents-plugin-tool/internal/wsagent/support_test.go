package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/claudeagents"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// stubLookPath makes every executable resolve except those in missing.
func stubLookPath(t *testing.T, missing ...string) {
	t.Helper()
	gone := map[string]bool{}
	for _, name := range missing {
		gone[name] = true
	}
	old := lookPath
	lookPath = func(name string) (string, error) {
		if gone[name] {
			return "", exec.ErrNotFound
		}
		return "/usr/local/bin/" + filepath.Base(name), nil
	}
	t.Cleanup(func() { lookPath = old })
}

// supportHarness is a runner with a fake Claude backend, two tracker
// stores, and a Support over the runner's directory.
type supportHarness struct {
	*harness
	claude *fakeBackend
	stores map[string]string
	// rootErr, when set, is what storeRoot answers for an unlisted store
	// in place of ErrStoreUnregistered: a registry that cannot be read.
	rootErr error
	support *Support
	now     time.Time
}

func newSupportHarness(t *testing.T) *supportHarness {
	t.Helper()
	h := newHarness(t, nil)
	sh := &supportHarness{
		harness: h,
		claude:  &fakeBackend{exe: "fake-claude"},
		stores:  map[string]string{"alpha": t.TempDir(), "beta": t.TempDir()},
		now:     time.Date(2026, 11, 15, 9, 35, 42, 0, time.UTC),
	}
	h.config.Now = func() time.Time { return sh.now }
	h.config.Backends = map[string]BackendRegistration{BackendClaude: {Backend: sh.claude, Permission: builtinPermission(BackendClaude), Lifecycle: builtinLifecycle(BackendClaude), LeadProfile: LeadProfileClaude, PermissionModes: LeadPermissionModes}}
	stubLookPath(t)
	sh.restart()
	return sh
}

func (sh *supportHarness) storeRoot(id string) (string, error) {
	if root, ok := sh.stores[id]; ok {
		return root, nil
	}
	if err := sh.rootErr; err != nil {
		return "", err
	}
	return "", fmt.Errorf("%w: %q", ErrStoreUnregistered, id)
}

// restart replaces the runner and the Support with fresh ones over the
// same directory, as a daemon restart does.
func (sh *supportHarness) restart() {
	sh.runner.Close()
	sh.start()
	sh.support = NewSupport(SupportOptions{Runner: sh.runner, Dir: sh.dir, StoreRoot: sh.storeRoot, Now: func() time.Time { return sh.now }})
}

func (sh *supportHarness) send(store, text string) State {
	sh.t.Helper()
	st, err := sh.support.Send(store, text)
	if err != nil {
		sh.t.Fatalf("Send(%s): %v", store, err)
	}
	return st
}

func (sh *supportHarness) storeConv(store string) *State {
	sh.t.Helper()
	ans, err := sh.support.Store(store)
	if err != nil {
		sh.t.Fatalf("Store(%s): %v", store, err)
	}
	return ans.Conversation
}

func (sh *supportHarness) choose(store string, req selectRequest) SelectAnswer {
	sh.t.Helper()
	ans, err := sh.support.Select(store, req)
	if err != nil {
		sh.t.Fatalf("Select(%s): %v", store, err)
	}
	return ans
}

func strp(s string) *string { return &s }

func TestSupportKeepsOneConversationPerStore(t *testing.T) {
	sh := newSupportHarness(t)
	if c := sh.storeConv("alpha"); c != nil {
		t.Fatalf("a store with no conversation reports %+v", c)
	}
	first := sh.send("alpha", "hello")
	if first.Profile != SupportProfileClaude || first.Backend != BackendClaude || first.Root != sh.stores["alpha"] {
		t.Fatalf("first conversation = %+v, want the claude support profile at the store root", first)
	}
	p := sh.claude.waitProc(t, 0)
	if got := p.nextSent(t); got != expectedUserEnvelope(sh.now, "hello") {
		t.Fatalf("first turn = %q", got)
	}
	p.reply("hi")
	eventually(t, "turn end", func() bool { return sh.state(first.ID).Status == StatusIdle })
	second := sh.send("alpha", "again")
	if second.ID != first.ID {
		t.Fatalf("second send opened %s, want the store's conversation %s", second.ID, first.ID)
	}
	if got := p.nextSent(t); got != expectedUserEnvelope(sh.now, "again") {
		t.Fatalf("second turn = %q", got)
	}
	p.reply("ok")

	sh.restart()
	c := sh.storeConv("alpha")
	if c == nil || c.ID != first.ID {
		t.Fatalf("after a restart the store's conversation = %+v, want %s", c, first.ID)
	}
	if st := sh.send("alpha", "after restart"); st.ID != first.ID {
		t.Fatalf("send after restart opened %s, want %s", st.ID, first.ID)
	}
	if got := sh.claude.waitProc(t, 1).nextSent(t); got != expectedUserEnvelope(sh.now, "after restart") {
		t.Fatalf("resumed turn = %q", got)
	}
	if c := sh.storeConv("beta"); c != nil {
		t.Fatalf("another store shares the conversation: %+v", c)
	}
}

// TestSupportOpenClosesAConversationItCannotSet: a conversation whose
// selection the runner refuses is closed and left unmapped, not open.
func TestSupportOpenClosesAConversationItCannotSet(t *testing.T) {
	sh := newSupportHarness(t)
	sh.support.mu.Lock()
	_, err := sh.support.open("alpha", sh.stores["alpha"], Selection{Harness: BackendClaude, Model: "bad model"}, sh.support.harnesses())
	sh.support.mu.Unlock()
	if err == nil {
		t.Fatalf("a model the runner refuses must fail the open")
	}
	for _, st := range sh.runner.List() {
		if st.Status != StatusClosed {
			t.Fatalf("the refused conversation stays open: %+v", st)
		}
	}
	if c := sh.storeConv("alpha"); c != nil {
		t.Fatalf("the refused conversation is mapped: %+v", c)
	}
}

func TestSupportNewConversationClosesTheOld(t *testing.T) {
	sh := newSupportHarness(t)
	old := sh.send("alpha", "hello")
	fresh, err := sh.support.New("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == old.ID || fresh.Status != StatusIdle {
		t.Fatalf("new conversation = %+v (old %s)", fresh, old.ID)
	}
	if st := sh.state(old.ID); st.Status != StatusClosed {
		t.Fatalf("old conversation status %s, want closed", st.Status)
	}
	if c := sh.storeConv("alpha"); c == nil || c.ID != fresh.ID {
		t.Fatalf("store maps %+v, want %s", c, fresh.ID)
	}
	if sh.claude.startCount() != 1 {
		t.Fatalf("new must not send: %d process starts", sh.claude.startCount())
	}
	// New with no open conversation just opens one.
	if st, err := sh.support.New("beta"); err != nil || st.Root != sh.stores["beta"] {
		t.Fatalf("New(beta) = %+v, %v", st, err)
	}
}

// TestSupportCloseUnregisteredEndsARemovedStoresConversation: a store that
// is no longer registered has its conversation closed and its mapping
// dropped, also on disk; a registered store keeps its conversation; with no
// store mapped nothing is read.
func TestSupportCloseUnregisteredEndsARemovedStoresConversation(t *testing.T) {
	sh := newSupportHarness(t)
	reads := 0
	sh.support.opts.StoreRoot = func(id string) (string, error) { reads++; return sh.storeRoot(id) }
	if dropped := sh.support.CloseUnregistered(); len(dropped) != 0 || reads != 0 {
		t.Fatalf("with no store mapped: dropped %v after %d store reads, want none and none", dropped, reads)
	}
	alpha := sh.send("alpha", "hello")
	beta := sh.send("beta", "hi")
	if dropped := sh.support.CloseUnregistered(); len(dropped) != 0 {
		t.Fatalf("with every store registered, dropped %v", dropped)
	}

	delete(sh.stores, "alpha")
	// A registry that cannot be read says nothing about the store.
	sh.rootErr = errors.New("registry unreadable")
	if dropped := sh.support.CloseUnregistered(); len(dropped) != 0 || sh.state(alpha.ID).Status == StatusClosed {
		t.Fatalf("an unreadable registry dropped %v", dropped)
	}
	sh.rootErr = nil
	if dropped := sh.support.CloseUnregistered(); !reflect.DeepEqual(dropped, []string{"alpha"}) {
		t.Fatalf("dropped %v, want [alpha]", dropped)
	}
	if st := sh.state(alpha.ID); st.Status != StatusClosed {
		t.Fatalf("the unregistered store's conversation is %s, want closed", st.Status)
	}
	if st := sh.state(beta.ID); st.Status == StatusClosed {
		t.Fatal("a registered store's conversation was closed")
	}
	if c := sh.storeConv("beta"); c == nil || c.ID != beta.ID {
		t.Fatalf("beta maps %+v, want %s", c, beta.ID)
	}
	raw, err := os.ReadFile(filepath.Join(sh.dir, supportFileName))
	if err != nil {
		t.Fatal(err)
	}
	var saved supportState
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Stores["alpha"]; ok || saved.Stores["beta"] != beta.ID {
		t.Fatalf("support.json maps %v, want beta alone", saved.Stores)
	}
	// A store registered again under the id starts a fresh conversation.
	sh.stores["alpha"] = t.TempDir()
	if st := sh.send("alpha", "back"); st.ID == alpha.ID {
		t.Fatalf("the re-registered store reopened the closed conversation %s", st.ID)
	}
}

func TestSupportModelAndEffortChangeKeepTheConversation(t *testing.T) {
	sh := newSupportHarness(t)
	conv := sh.send("alpha", "hello")
	ans := sh.choose("alpha", selectRequest{Model: strp("sonnet"), Effort: strp("high")})
	if ans.Conversation == nil || ans.Conversation.ID != conv.ID {
		t.Fatalf("model change answered %+v, want conversation %s", ans.Conversation, conv.ID)
	}
	rec, err := ReadRecord(sh.dir, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Model != "sonnet" || rec.Effort != "high" || rec.ClosedAt != "" {
		t.Fatalf("record = model %q effort %q closed %q", rec.Model, rec.Effort, rec.ClosedAt)
	}
	if want := (Selection{Harness: BackendClaude, Model: "sonnet", Effort: "high"}); ans.Selection != want {
		t.Fatalf("selection = %+v, want %+v", ans.Selection, want)
	}
	// Naming the conversation's own harness is no switch.
	same := sh.choose("alpha", selectRequest{Harness: strp(BackendClaude), Effort: strp("low")})
	if same.Conversation.ID != conv.ID || same.Conversation.Model != "sonnet" || same.Conversation.Effort != "low" {
		t.Fatalf("same-harness select = %+v", same.Conversation)
	}
	if _, err := sh.support.Select("alpha", selectRequest{Effort: strp("NOT A WORD")}); err == nil {
		t.Fatal("a malformed effort must be refused")
	}
}

func TestSupportLastChoiceIsTheDefaultAndPersists(t *testing.T) {
	sh := newSupportHarness(t)
	if ans, _ := sh.support.Store("alpha"); ans.Selection != (Selection{Harness: BackendClaude}) {
		t.Fatalf("default selection = %+v, want claude with defaults", ans.Selection)
	}
	ans := sh.choose("alpha", selectRequest{Harness: strp(BackendClaude), Effort: strp("high")})
	if ans.Conversation != nil {
		t.Fatalf("select with no conversation opened %+v", ans.Conversation)
	}
	if len(sh.runner.List()) != 0 {
		t.Fatalf("select with no conversation must only save the choice")
	}
	conv := sh.send("beta", "hello")
	if conv.Backend != BackendClaude || conv.Profile != SupportProfileClaude || conv.Effort != "high" {
		t.Fatalf("another store's first conversation = %+v, want the last choice", conv)
	}

	info, err := os.Stat(filepath.Join(sh.dir, supportFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !isPrivate(info) {
		t.Fatalf("support.json mode %v, want 0600", info.Mode().Perm())
	}

	sh.restart()
	want := Selection{Harness: BackendClaude, Effort: "high"}
	if got := sh.support.Choices(context.Background()).Last; got != want {
		t.Fatalf("last choice after restart = %+v, want %+v", got, want)
	}
	if c := sh.storeConv("beta"); c == nil || c.ID != conv.ID {
		t.Fatalf("beta's conversation after restart = %+v", c)
	}
}

func TestRetiredPiSupportSelectionFallsBackWithoutRewriting(t *testing.T) {
	sh := newSupportHarness(t)
	path := filepath.Join(sh.dir, supportFileName)
	raw := []byte(`{"schema_version":1,"last":{"harness":"pi","model":"provider/legacy","effort":"high"},"stores":{}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sh.restart()
	got, err := sh.support.Store("alpha")
	if err != nil || !got.RetiredPi || got.Selection != (Selection{Harness: BackendClaude}) {
		t.Fatalf("legacy selection = %+v, %v", got, err)
	}
	if choices := sh.support.Choices(context.Background()); len(choices.Harnesses) != 1 || choices.Harnesses[0].Harness != BackendClaude {
		t.Fatalf("support choices = %+v", choices.Harnesses)
	}
	if saved, err := os.ReadFile(path); err != nil || string(saved) != string(raw) {
		t.Fatalf("stored selection changed: %q, %v", saved, err)
	}
	if st := sh.send("alpha", "hello"); st.Backend != BackendClaude || st.Model == "provider/legacy" {
		t.Fatalf("fallback conversation = %+v", st)
	}
}

func TestSupportDefaultSkipsAnUnavailableHarness(t *testing.T) {
	sh := newSupportHarness(t)
	stubLookPath(t, "fake-claude")
	if ans, _ := sh.support.Store("alpha"); ans.Selection != (Selection{}) {
		t.Fatalf("default with claude missing = %+v, want no harness", ans.Selection)
	}
	if _, err := sh.support.Select("alpha", selectRequest{Harness: strp(BackendClaude)}); err == nil {
		t.Fatal("an unavailable harness must be refused")
	}
	if _, err := sh.support.Select("alpha", selectRequest{Harness: strp("codex")}); err == nil {
		t.Fatal("an unknown harness must be refused")
	}
	var re *requestError
	if _, err := sh.support.Send("alpha", "hello"); !errors.As(err, &re) || re.status != http.StatusServiceUnavailable {
		t.Fatalf("send with no harness = %v, want 503", err)
	}
}

type supportListingFixture struct {
	ClaudeBackend
	mu     sync.Mutex
	calls  int
	err    error
	during func()
}

func (f *supportListingFixture) Choices(ctx context.Context) ([]ModelChoice, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.during != nil {
		f.during()
	}
	if err := ctx.Err(); err != nil {
		return nil, claudeagents.Levels, err
	}
	if f.err != nil {
		return nil, claudeagents.Levels, f.err
	}
	return f.ClaudeBackend.Choices(ctx)
}

func (f *supportListingFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func supportChoices(t *testing.T, now *time.Time, backend Backend) *Support {
	t.Helper()
	r := NewRunner(Options{
		Dir:      t.TempDir(),
		Backends: map[string]BackendRegistration{BackendClaude: {Backend: backend}},
		Config:   testConfig(t),
	})
	t.Cleanup(r.Close)
	return NewSupport(SupportOptions{Runner: r, Dir: t.TempDir(), StoreRoot: func(string) (string, error) { return "", errors.New("none") }, Now: func() time.Time { return *now }})
}

func harnessByName(t *testing.T, ans ChoicesAnswer, name string) SupportHarness {
	t.Helper()
	h, ok := findHarness(ans.Harnesses, name)
	if !ok {
		t.Fatalf("no %s harness in %+v", name, ans.Harnesses)
	}
	return h
}

func TestSupportChoicesListEachHarness(t *testing.T) {
	stubLookPath(t)
	listing := &supportListingFixture{ClaudeBackend: ClaudeBackend{Command: "claude-x"}}
	now := time.Now()
	s := supportChoices(t, &now, listing)
	ans := s.Choices(context.Background())
	if len(ans.Harnesses) != 1 || ans.Harnesses[0].Harness != BackendClaude {
		t.Fatalf("harnesses = %+v, want claude then claude", ans.Harnesses)
	}
	if ans.Last != (Selection{Harness: BackendClaude}) {
		t.Fatalf("last = %+v", ans.Last)
	}

	claude := harnessByName(t, ans, BackendClaude)
	var ids []string
	for _, m := range claude.Models {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, []string{"opus", "sonnet", "haiku", "fable"}) {
		t.Fatalf("claude models = %v, want exactly the version-less aliases", ids)
	}
	if !reflect.DeepEqual(claude.Efforts, claudeagents.Levels) || claude.Profile != SupportProfileClaude || !claude.Available {
		t.Fatalf("claude harness = %+v", claude)
	}

	// The listing is cached for a while.
	s.Choices(context.Background())
	if listing.count() != 1 {
		t.Fatalf("listing ran %d times within the cache window", listing.count())
	}
	now = now.Add(6 * time.Minute)
	s.Choices(context.Background())
	if listing.count() != 2 {
		t.Fatalf("listing ran %d times after the cache window", listing.count())
	}
}

func TestSupportChoicesMarkAMissingBinaryUnavailable(t *testing.T) {
	stubLookPath(t, "claude-x")
	listing := &supportListingFixture{ClaudeBackend: ClaudeBackend{Command: "claude-x"}}
	now := time.Now()
	ans := supportChoices(t, &now, listing).Choices(context.Background())
	claude := harnessByName(t, ans, BackendClaude)
	if claude.Available || claude.Error == "" || len(claude.Models) != 0 {
		t.Fatalf("claude with no binary = %+v, want unavailable", claude)
	}
	if listing.count() != 0 {
		t.Fatal("an unavailable harness must not be listed")
	}
}

func TestSupportChoicesCachesListingErrors(t *testing.T) {
	stubLookPath(t)
	listing := &supportListingFixture{err: errors.New("listing failed")}
	now := time.Now()
	s := supportChoices(t, &now, listing)
	ans := s.Choices(context.Background())
	claude := harnessByName(t, ans, BackendClaude)
	if !claude.Available || !strings.Contains(claude.Error, "listing failed") || len(claude.Models) != 0 || len(claude.Efforts) != len(claudeagents.Levels) {
		t.Fatalf("claude with a failed listing = %+v", claude)
	}
	// A failure is cached briefly: the next request answers it again without
	// listing, and one past choicesFailTTL lists again.
	listing.mu.Lock()
	listing.err = nil
	listing.mu.Unlock()
	if claude := harnessByName(t, s.Choices(context.Background()), BackendClaude); !strings.Contains(claude.Error, "listing failed") || len(claude.Efforts) != len(claudeagents.Levels) || listing.count() != 1 {
		t.Fatalf("claude inside the failure window = %+v after %d listings", claude, listing.count())
	}
	now = now.Add(choicesFailTTL)
	if claude := harnessByName(t, s.Choices(context.Background()), BackendClaude); claude.Error != "" || len(claude.Models) != 4 || listing.count() != 2 {
		t.Fatalf("claude after the listing recovered = %+v after %d listings", claude, listing.count())
	}
}

// TestSupportChoicesFailureWindowStartsWhenTheListingEnds: a failed listing
// that hangs until choicesTimeout is cached for choicesFailTTL from when it
// ended, not from when it began.
func TestSupportChoicesFailureWindowStartsWhenTheListingEnds(t *testing.T) {
	stubLookPath(t)
	listing := &supportListingFixture{err: errors.New("listing failed")}
	now := time.Now()
	listing.during = func() { now = now.Add(choicesTimeout) }
	s := supportChoices(t, &now, listing)
	if claude := harnessByName(t, s.Choices(context.Background()), BackendClaude); !strings.Contains(claude.Error, "listing failed") {
		t.Fatalf("claude with a failed listing = %+v", claude)
	}
	listing.mu.Lock()
	listing.during = nil
	listing.mu.Unlock()
	now = now.Add(choicesFailTTL - time.Second)
	if claude := harnessByName(t, s.Choices(context.Background()), BackendClaude); !strings.Contains(claude.Error, "listing failed") || listing.count() != 1 {
		t.Fatalf("claude just inside the failure window = %+v after %d listings", claude, listing.count())
	}
}

// TestSupportChoicesListingOutlivesACanceledRequest: a request canceled
// before its listing runs still lists in full and fills the cache, so the
// next request does not list again.
func TestSupportChoicesListingOutlivesACanceledRequest(t *testing.T) {
	stubLookPath(t)
	listing := &supportListingFixture{ClaudeBackend: ClaudeBackend{Command: "claude-x"}}
	now := time.Now()
	s := supportChoices(t, &now, listing)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if claude := harnessByName(t, s.Choices(ctx), BackendClaude); claude.Error != "" || len(claude.Models) != 4 {
		t.Fatalf("claude under a canceled request = %+v", claude)
	}
	if claude := harnessByName(t, s.Choices(context.Background()), BackendClaude); claude.Error != "" || len(claude.Models) != 4 || listing.count() != 1 {
		t.Fatalf("claude on the next request = %+v after %d listings", claude, listing.count())
	}
}

func TestModelPatternAcceptsTilde(t *testing.T) {
	for _, ok := range []string{"openrouter/~anthropic/claude-fable-latest", "opus", "anthropic/claude-x:high", "m[1m]"} {
		if !modelPattern.MatchString(ok) {
			t.Errorf("modelPattern refuses %q", ok)
		}
	}
	for _, bad := range []string{"", "a b", "$(x)", "a;b"} {
		if modelPattern.MatchString(bad) {
			t.Errorf("modelPattern accepts %q", bad)
		}
	}
}

// supportDaemon serves a supportHarness's Support and runner from a real
// dashboard daemon.
func supportDaemon(t *testing.T) (*supportHarness, *daemonHarness) {
	t.Helper()
	sh := newSupportHarness(t)
	sh.runner.Close()
	sh.runner = NewRunner(sh.config)
	sh.support = NewSupport(SupportOptions{Runner: sh.runner, Dir: sh.dir, StoreRoot: sh.storeRoot})
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: sh.dir, Port: 0, Sessions: leadSessions{}, Agents: sh.runner, Support: sh.support, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return sh, &daemonHarness{harness: sh.harness, base: fmt.Sprintf("http://127.0.0.1:%d", d.Endpoint().Port), cookie: daemonCookieForTest(t, d)}
}

func TestSupportRoutesEndToEnd(t *testing.T) {
	sh, d := supportDaemon(t)

	r := d.do(http.MethodGet, "/support/choices", "", "", nil)
	var choices ChoicesAnswer
	if r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &choices) != nil || len(choices.Harnesses) != 1 {
		t.Fatalf("choices: %d %s", r.status, r.body)
	}
	r = d.do(http.MethodGet, "/support/stores/alpha", "", "", nil)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"conversation":null`) || !strings.Contains(r.body, `"store":"alpha"`) {
		t.Fatalf("store before any send: %d %s", r.status, r.body)
	}
	r = d.post("/support/stores/alpha/send", map[string]string{"text": "hello"})
	if r.status != http.StatusOK {
		t.Fatalf("send: %d %s", r.status, r.body)
	}
	id := decodeState(t, r).ID
	if strings.Contains(r.body, "fake-agent-key") {
		t.Fatalf("a support route revealed the ws session key: %s", r.body)
	}
	if got := sh.claude.waitProc(t, 0).nextSent(t); got != expectedUserEnvelope(sh.now, "hello") {
		t.Fatalf("turn = %q", got)
	}
	var store StoreAnswer
	r = d.do(http.MethodGet, "/support/stores/alpha", "", "", nil)
	if json.Unmarshal([]byte(r.body), &store) != nil || store.Conversation == nil || store.Conversation.ID != id {
		t.Fatalf("store after send: %s", r.body)
	}
	r = d.post("/support/stores/alpha/select", map[string]string{"harness": BackendClaude, "effort": "high"})
	var sel SelectAnswer
	if r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &sel) != nil || sel.Conversation == nil || sel.Conversation.Backend != BackendClaude || sel.Conversation.Effort != "high" {
		t.Fatalf("select: %d %s", r.status, r.body)
	}
	r = d.post("/support/stores/alpha/new", nil)
	if r.status != http.StatusOK || decodeState(t, r).ID == sel.Conversation.ID {
		t.Fatalf("new: %d %s", r.status, r.body)
	}
	if r := d.post("/support/stores/alpha/select", map[string]string{"harness": "codex"}); r.status != http.StatusBadRequest {
		t.Fatalf("unknown harness: %d %s", r.status, r.body)
	}

	// An unknown store is 404 on every route.
	if r := d.do(http.MethodGet, "/support/stores/nope", "", "", nil); r.status != http.StatusNotFound {
		t.Fatalf("unknown store GET: %d %s", r.status, r.body)
	}
	for _, action := range []string{"send", "new", "select"} {
		if r := d.post("/support/stores/nope/"+action, map[string]string{"text": "x"}); r.status != http.StatusNotFound {
			t.Fatalf("unknown store %s: %d %s", action, r.status, r.body)
		}
	}
}

func TestSupportWritesRefuseOtherOrigins(t *testing.T) {
	_, d := supportDaemon(t)
	body := map[string]string{"text": "hello"}
	if r := d.do(http.MethodPost, "/support/stores/alpha/send", "http://evil.example", "application/json", body); r.status != http.StatusForbidden {
		t.Fatalf("foreign origin: %d %s", r.status, r.body)
	}
	if r := d.do(http.MethodPost, "/support/stores/alpha/send", "", "text/plain", body); r.status != http.StatusForbidden {
		t.Fatalf("non-JSON content type: %d %s", r.status, r.body)
	}
	if r := d.do(http.MethodPost, "/support/stores/alpha/select", "", "text/plain", map[string]string{"harness": BackendPi}); r.status != http.StatusForbidden {
		t.Fatalf("non-JSON select: %d %s", r.status, r.body)
	}
	if r := d.do(http.MethodPost, "/support/stores/alpha/send", d.base, "application/json", body); r.status != http.StatusOK {
		t.Fatalf("own origin: %d %s", r.status, r.body)
	}
}
