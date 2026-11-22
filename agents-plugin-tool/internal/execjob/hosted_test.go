package execjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// memHostedStore is an in-memory HostedStore.
type memHostedStore struct {
	mu      sync.Mutex
	rows    map[string]HostedJobRow
	puts    int
	deleted []string
}

func newMemHostedStore() *memHostedStore { return &memHostedStore{rows: map[string]HostedJobRow{}} }

func (s *memHostedStore) Load() ([]HostedJobRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HostedJobRow, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	return out, nil
}

func (s *memHostedStore) Put(row HostedJobRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[row.ID] = row
	s.puts++
	return nil
}

func (s *memHostedStore) Delete(ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.rows, id)
	}
	s.deleted = append(s.deleted, ids...)
	return nil
}

func (s *memHostedStore) Close() error { return nil }

func (s *memHostedStore) row(id string) (HostedJobRow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	return r, ok
}

// fakeExecHolders is a HostedHolders whose holders the test drives: it
// feeds their records to the Hosted under test, as the manager's sink does.
type fakeExecHolders struct {
	mu       sync.Mutex
	sink     *Hosted
	n        int
	launched map[string]wsholder.LaunchSpec
	spawned  map[string]wsholder.SpawnSpec
	inputs   map[string][]string
	stopped  []string
	live     map[string]json.RawMessage
	exited   map[string]bool
	seq      map[string]uint64
	spawnErr error
	// onSpawn runs after a spawn, outside the lock (the child's own
	// behaviour: output, exit).
	onSpawn func(id string)
}

func newFakeExecHolders() *fakeExecHolders {
	return &fakeExecHolders{launched: map[string]wsholder.LaunchSpec{}, spawned: map[string]wsholder.SpawnSpec{}, inputs: map[string][]string{}, live: map[string]json.RawMessage{}, exited: map[string]bool{}, seq: map[string]uint64{}}
}

func (f *fakeExecHolders) LaunchWith(spec wsholder.LaunchSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("h%012d", f.n)
	f.launched[id] = spec
	f.live[id] = spec.Session
	return id, nil
}

func (f *fakeExecHolders) Spawn(id string, spec wsholder.SpawnSpec) (wsholder.ChildStatus, error) {
	f.mu.Lock()
	if f.spawnErr != nil {
		f.mu.Unlock()
		return wsholder.ChildStatus{}, f.spawnErr
	}
	f.spawned[id] = spec
	hook := f.onSpawn
	f.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	return wsholder.ChildStatus{State: "running", PID: 4242}, nil
}

func (f *fakeExecHolders) Input(id string, data []byte, eof bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := string(data)
	if eof {
		entry += "<EOF>"
	}
	f.inputs[id] = append(f.inputs[id], entry)
	return nil
}

// Stop ends the holder: a child still running gets the stop's signal and
// its exit record, as the real holder sends before it goes.
func (f *fakeExecHolders) Stop(id string) error {
	f.mu.Lock()
	if _, ok := f.live[id]; !ok {
		f.mu.Unlock()
		return wsholder.ErrUnknownHolder
	}
	f.stopped = append(f.stopped, id)
	running := !f.exited[id] && f.spawned[id].Argv != nil
	f.mu.Unlock()
	if running {
		sig := 15
		f.emit(id, wsholder.Event{Event: "exit", Signal: &sig})
	}
	f.mu.Lock()
	delete(f.live, id)
	f.mu.Unlock()
	return nil
}

func (f *fakeExecHolders) Scan() {}

func (f *fakeExecHolders) List() wsholder.Listing {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := wsholder.Listing{}
	for id, session := range f.live {
		out.Holders = append(out.Holders, wsholder.Info{ID: id, Attached: true, Session: session})
	}
	return out
}

func (f *fakeExecHolders) emit(id string, ev wsholder.Event) {
	f.mu.Lock()
	f.seq[id]++
	ev.Seq = f.seq[id]
	if ev.Event == "exit" {
		f.exited[id] = true
	}
	sink := f.sink
	f.mu.Unlock()
	sink.HolderEvent(id, ev)
}

func (f *fakeExecHolders) out(id, stream, text string) {
	f.emit(id, wsholder.Event{Event: "output", Stream: stream, Data: []byte(text)})
}

func (f *fakeExecHolders) exit(id string, code int) {
	f.emit(id, wsholder.Event{Event: "exit", ExitCode: &code})
}

func (f *fakeExecHolders) stops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stopped)
}

func (f *fakeExecHolders) spawnOf(id string) wsholder.SpawnSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawned[id]
}

func (f *fakeExecHolders) inputOf(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.inputs[id])
}

type execNotice struct {
	owner, event, text string
	gen                uint64
}

// fakeExecSessions is a HostedSessions over a fixed set of owners.
type fakeExecSessions struct {
	mu        sync.Mutex
	owners    map[string]HostedOwner
	approve   func(ctx context.Context, id string, call HostedCall) (bool, error)
	immediate func(id string) (bool, error)
	approvals []HostedCall
	immChecks int
	notices   []execNotice
	seen      map[string]bool
	outcome   NoticeOutcome
}

func newFakeExecSessions() *fakeExecSessions {
	return &fakeExecSessions{owners: map[string]HostedOwner{"lead": {ID: "lead", Gen: 3, Lead: true}, "other": {ID: "other", Gen: 1}}, seen: map[string]bool{}}
}

func (s *fakeExecSessions) Owner(id string) (HostedOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.owners[id]
	if !ok {
		return HostedOwner{}, errors.New("unknown conversation")
	}
	return o, nil
}

func (s *fakeExecSessions) Approve(ctx context.Context, id string, call HostedCall) (bool, error) {
	s.mu.Lock()
	s.approvals = append(s.approvals, call)
	fn := s.approve
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, call)
	}
	return false, nil
}

func (s *fakeExecSessions) Immediate(id string) (bool, error) {
	s.mu.Lock()
	s.immChecks++
	fn := s.immediate
	s.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	return true, nil
}

func (s *fakeExecSessions) Notify(id string, gen uint64, eventID, text string) (NoticeOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome == NoticeDeferred || s.outcome == NoticeDropped {
		return s.outcome, nil
	}
	if s.seen[eventID] {
		return NoticeDuplicate, nil
	}
	s.seen[eventID] = true
	s.notices = append(s.notices, execNotice{owner: id, gen: gen, event: eventID, text: text})
	return NoticeAccepted, nil
}

func (s *fakeExecSessions) noticeList() []execNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.notices)
}

type hostedFixture struct {
	t        *testing.T
	h        *Hosted
	holders  *fakeExecHolders
	sessions *fakeExecSessions
	store    *memHostedStore
	exitDir  string
}

func newHostedFixture(t *testing.T, window time.Duration) *hostedFixture {
	t.Helper()
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	fx.open(window)
	return fx
}

// open builds a Hosted over the fixture's store, as a daemon start does.
func (fx *hostedFixture) open(window time.Duration) {
	fx.t.Helper()
	h, err := NewHosted(HostedOptions{Holders: fx.holders, Store: fx.store, Sessions: fx.sessions, ExitDir: fx.exitDir, Window: window})
	if err != nil {
		fx.t.Fatal(err)
	}
	fx.holders.mu.Lock()
	fx.holders.sink = h
	fx.holders.mu.Unlock()
	fx.h = h
	h.Start()
}

func (fx *hostedFixture) launch(ctx context.Context, drain <-chan struct{}, req HostedLaunch) (HostedView, error) {
	if req.Owner == "" {
		req.Owner = "lead"
	}
	if req.Root == "" {
		req.Root = fx.exitDir
	}
	if req.Cwd == "" {
		req.Cwd = req.Root
	}
	if req.Tool == "" {
		req.Tool = "exec.run"
	}
	if req.Argv == nil {
		req.Argv = []string{"/bin/fake", "arg"}
	}
	if drain == nil {
		drain = make(chan struct{})
	}
	return fx.h.Launch(ctx, drain, req)
}

func eventuallyExec(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestHostedSyncEndInsideTheWindow: a job that ends inside the window
// answers done with its output, sends no notice, and its holder is
// stopped after the end; its launch names an exit copy outside the holder
// directory and a holder session of kind exec.
func TestHostedSyncEndInsideTheWindow(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.holders.onSpawn = func(id string) {
		go func() {
			fx.holders.out(id, "stdout", "hello\n")
			fx.holders.out(id, "stderr", "warn\n")
			fx.holders.exit(id, 0)
		}()
	}
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "fake arg"})
	if err != nil || v.Outcome != OutcomeDone || v.State != HostedExited || v.ExitCode == nil || *v.ExitCode != 0 {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	if !strings.HasPrefix(v.ID, "exec_") || strings.Count(v.ID, "-") != 2 {
		t.Fatalf("exec id = %q", v.ID)
	}
	_, tail, err := fx.h.Tail("lead", v.ID, 0)
	if err != nil || string(tail.Data) != "hello\nwarn\n" || tail.Written != int64(len("hello\nwarn\n")) {
		t.Fatalf("tail = %q (%+v), %v", tail.Data, tail, err)
	}
	spec := fx.holders.launched[v.HolderID]
	if execID, ok := ExecHolderID(spec.Session); !ok || execID != v.ID || spec.ExitCopy != filepath.Join(fx.exitDir, v.ID+".json") {
		t.Fatalf("launch spec = %+v", spec)
	}
	if s := fx.holders.spawnOf(v.HolderID); s.InputEOF || s.Input != nil || s.Mode != "pipe" {
		t.Fatalf("a new launch without stdin keeps stdin open: %+v", s)
	}
	eventuallyExec(t, "the ended job's holder stop", func() bool { return slices.Contains(fx.holders.stops(), v.HolderID) })
	if n := fx.sessions.noticeList(); len(n) != 0 {
		t.Fatalf("a synchronous end sent notices: %+v", n)
	}
	if row, ok := fx.store.row(v.ID); !ok || row.State != HostedExited || string(row.Tails[StreamCombined]) != "hello\nwarn\n" || row.Notify {
		t.Fatalf("stored row = %+v", row)
	}
}

// TestHostedConversionAndOnceOnlyNotice: a job outliving the window is
// converted in the same call; its end sends one notice under the owner's
// generation and stable event id, and a replayed exit record or a second
// delivery pass sends nothing more.
func TestHostedConversionAndOnceOnlyNotice(t *testing.T) {
	fx := newHostedFixture(t, 30*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "slow job"})
	if err != nil || v.Outcome != OutcomeConverted || v.State != HostedRunning || !v.Notify {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	fx.holders.out(v.HolderID, "stdout", "progress\n")
	fx.holders.exit(v.HolderID, 3)
	eventuallyExec(t, "the notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
	n := fx.sessions.noticeList()[0]
	if n.owner != "lead" || n.gen != 3 || n.event != "exec:"+v.ID+":exit" || !strings.Contains(n.text, "exited with code 3") || !strings.Contains(n.text, "progress") || !strings.Contains(n.text, "command: /bin/fake arg") {
		t.Fatalf("notice = %+v", n)
	}
	// A daemon restart replays the holder's ring from its first record.
	code := 3
	fx.h.HolderEvent(v.HolderID, wsholder.Event{Event: "exit", Seq: 2, ExitCode: &code})
	fx.h.deliverPending()
	if got := fx.sessions.noticeList(); len(got) != 1 {
		t.Fatalf("notices after replay = %+v", got)
	}
	got, _ := fx.h.Get("lead", v.ID)
	if got.Notice != NoticeDelivered {
		t.Fatalf("notice state = %q", got.Notice)
	}
}

// TestHostedAsyncAnswersAtOnce: async returns the id before any end and
// the end notifies.
func TestHostedAsyncAnswersAtOnce(t *testing.T) {
	fx := newHostedFixture(t, time.Hour)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil || v.Outcome != OutcomeAsync || v.State != HostedRunning {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	fx.holders.exit(v.HolderID, 0)
	eventuallyExec(t, "the async notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
}

// TestHostedWindowExcludesApprovalAndRechecksTheLiveMode: the window starts
// at the process start, so a slow approval does not convert a quick job; a
// role or mode allow that no longer holds at the start asks again.
func TestHostedWindowExcludesApprovalAndRechecksTheLiveMode(t *testing.T) {
	fx := newHostedFixture(t, 100*time.Millisecond)
	fx.sessions.approve = func(context.Context, string, HostedCall) (bool, error) {
		time.Sleep(250 * time.Millisecond)
		return true, nil
	}
	fx.holders.onSpawn = func(id string) {
		go func() {
			time.Sleep(30 * time.Millisecond)
			fx.holders.exit(id, 0)
		}()
	}
	v, err := fx.launch(context.Background(), nil, HostedLaunch{})
	if err != nil || v.Outcome != OutcomeDone {
		t.Fatalf("a quick job after a slow approval = %+v, %v", v, err)
	}
	if fx.sessions.immChecks != 0 {
		t.Fatalf("an explicit allow was rechecked %d times", fx.sessions.immChecks)
	}

	// A posture allow, then the mode changed before the start: one prompt.
	calls := 0
	fx.sessions.approve = func(context.Context, string, HostedCall) (bool, error) {
		calls++
		return calls > 1, nil
	}
	fx.sessions.immediate = func(string) (bool, error) { return false, nil }
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("approvals = %d, want the posture allow and one prompt", calls)
	}

	// A denial at the start stops the holder and fails the job.
	calls = 0
	fx.sessions.approve = func(context.Context, string, HostedCall) (bool, error) {
		calls++
		if calls > 1 {
			return false, errors.New("the user denied this exec call")
		}
		return false, nil
	}
	v, err = fx.launch(context.Background(), nil, HostedLaunch{})
	if err == nil || v.State != HostedFailed || !strings.Contains(v.Error, "denied") {
		t.Fatalf("denied at the start = %+v, %v", v, err)
	}
	eventuallyExec(t, "the denied job's holder stop", func() bool { return slices.Contains(fx.holders.stops(), v.HolderID) })
	if s := fx.holders.spawnOf(v.HolderID); s.Argv != nil {
		t.Fatalf("a denied job started: %+v", s)
	}
}

// TestHostedInterruptStopsAndDrainConverts: the caller's interrupt inside
// the window stops the job with no notice; a daemon drain converts it.
func TestHostedInterruptStopsAndDrainConverts(t *testing.T) {
	fx := newHostedFixture(t, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	fx.holders.onSpawn = func(string) { go func() { time.Sleep(20 * time.Millisecond); cancel() }() }
	v, err := fx.launch(ctx, nil, HostedLaunch{})
	if !errors.Is(err, context.Canceled) || v.State != HostedStopped {
		t.Fatalf("interrupted = %+v, %v", v, err)
	}
	if n := fx.sessions.noticeList(); len(n) != 0 {
		t.Fatalf("an interrupted job notified: %+v", n)
	}

	drain := make(chan struct{})
	fx.holders.onSpawn = func(string) { go func() { time.Sleep(20 * time.Millisecond); close(drain) }() }
	v, err = fx.launch(context.Background(), drain, HostedLaunch{})
	if err != nil || v.Outcome != OutcomeConverted || v.State != HostedRunning {
		t.Fatalf("drained = %+v, %v", v, err)
	}
	fx.holders.onSpawn = nil
}

// TestHostedStdinAndWrite: stdin given at launch closes after it; without
// it a new job takes exec.write until eof; a legacy job's stdin closes at
// once even without stdin.
func TestHostedStdinAndWrite(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Stdin: []byte("given"), StdinSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if s := fx.holders.spawnOf(v.HolderID); string(s.Input) != "given" || !s.InputEOF {
		t.Fatalf("spawn with stdin = %+v", s)
	}
	if _, err := fx.h.Write("lead", v.ID, []byte("more"), false); err == nil {
		t.Fatal("a write after launch stdin was accepted")
	}

	v, err = fx.launch(context.Background(), nil, HostedLaunch{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.h.Write("lead", v.ID, []byte("line1\n"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.h.Write("lead", v.ID, []byte("line2\n"), true); err != nil {
		t.Fatal(err)
	}
	if got := fx.holders.inputOf(v.HolderID); !slices.Equal(got, []string{"line1\n", "line2\n<EOF>"}) {
		t.Fatalf("inputs = %q", got)
	}
	if _, err := fx.h.Write("lead", v.ID, []byte("late"), false); err == nil {
		t.Fatal("a write after eof was accepted")
	}
	if _, err := fx.h.Write("other", v.ID, []byte("x"), false); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("another conversation's write = %v", err)
	}

	v, err = fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.spawn"})
	if err != nil {
		t.Fatal(err)
	}
	if s := fx.holders.spawnOf(v.HolderID); s.Input != nil || !s.InputEOF {
		t.Fatalf("legacy spawn without stdin = %+v", s)
	}
	if !keyPattern.MatchString(v.ID) {
		t.Fatalf("legacy key %q does not match the legacy key form", v.ID)
	}
}

// TestHostedOwnershipAndListing: only the owner sees, stops or tails a
// job, and a listing shows running jobs first.
func TestHostedOwnershipAndListing(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	a, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "first"})
	fx.holders.exit(a.HolderID, 0)
	b, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "second"})
	o, _ := fx.launch(context.Background(), nil, HostedLaunch{Owner: "other"})
	for _, call := range []func() error{
		func() error { _, err := fx.h.Get("other", a.ID); return err },
		func() error { _, _, err := fx.h.Tail("other", b.ID, 0); return err },
		func() error { _, err := fx.h.Stop("other", b.ID); return err },
		func() error { _, err := fx.h.Get("", b.ID); return err },
	} {
		if err := call(); !errors.Is(err, ErrExecNotFound) {
			t.Fatalf("cross-owner access = %v", err)
		}
	}
	list, more := fx.h.List("lead")
	if more != 0 || len(list) != 2 || list[0].ID != b.ID || list[1].ID != a.ID {
		t.Fatalf("listing = %+v (more %d)", list, more)
	}
	if fx.h.LiveCount("lead") != 1 || fx.h.LiveCount("other") != 1 {
		t.Fatalf("live counts = %d, %d", fx.h.LiveCount("lead"), fx.h.LiveCount("other"))
	}
	st, err := fx.h.Stop("lead", b.ID)
	if err != nil || st.State != HostedStopped || st.Signal == nil {
		t.Fatalf("stop = %+v, %v", st, err)
	}
	eventuallyExec(t, "the first job's notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
	for _, n := range fx.sessions.noticeList() {
		if n.event != "exec:"+a.ID+":exit" {
			t.Fatalf("a stopped converted job notified: %+v", n)
		}
	}
	_ = o
}

// TestHostedRestartSettlesGoneHolders: a new daemon over the same store
// ends a job whose holder went with its exit copy's status (lost without
// one), delivers the pending notices, and stops exec holders no job owns
// or whose job already ended.
func TestHostedRestartSettlesGoneHolders(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	withCopy, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "copied"})
	noCopy, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "lost"})
	alive, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "alive"})
	ended, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "ended"})
	_ = fx.h.Close()

	// While no daemon runs: two holders end under an older daemon, which
	// removes their directories; one of them left its exit copy.
	code := 7
	raw, _ := json.Marshal(wsholder.Exit{ExitCode: &code, Reason: "child_exited", StoppedAt: time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(filepath.Join(fx.exitDir, withCopy.ID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.holders.mu.Lock()
	delete(fx.holders.live, withCopy.HolderID)
	delete(fx.holders.live, noCopy.HolderID)
	fx.holders.live["h-orphan"] = json.RawMessage(`{"kind":"exec","exec_id":"exec_never-recorded-job"}`)
	fx.holders.live["h-terminal"] = json.RawMessage(`{"kind":"lead"}`)
	fx.holders.mu.Unlock()
	// The ended job's end was stored, its holder's stop never happened.
	row, _ := fx.store.row(ended.ID)
	row.State, row.ExitCode, row.EndedAt, row.Notice = HostedExited, &code, time.Now().UnixMilli(), NoticePending
	_ = fx.store.Put(row)

	fx.open(10 * time.Millisecond)
	a, _ := fx.h.Get("lead", withCopy.ID)
	if a.State != HostedExited || a.ExitCode == nil || *a.ExitCode != 7 {
		t.Fatalf("job with an exit copy = %+v", a)
	}
	if _, err := os.Stat(filepath.Join(fx.exitDir, withCopy.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("the consumed exit copy stays: %v", err)
	}
	b, _ := fx.h.Get("lead", noCopy.ID)
	if b.State != HostedLost || !strings.Contains(b.Error, "no longer active") {
		t.Fatalf("job with no exit copy = %+v", b)
	}
	if c, _ := fx.h.Get("lead", alive.ID); c.State != HostedRunning {
		t.Fatalf("job whose holder lives = %+v", c)
	}
	eventuallyExec(t, "the orphan and ended holders' stop", func() bool {
		s := fx.holders.stops()
		return slices.Contains(s, "h-orphan") && slices.Contains(s, ended.HolderID)
	})
	if slices.Contains(fx.holders.stops(), "h-terminal") || slices.Contains(fx.holders.stops(), alive.HolderID) {
		t.Fatalf("stopped holders = %q", fx.holders.stops())
	}
	notices := fx.sessions.noticeList()
	events := []string{}
	for _, n := range notices {
		events = append(events, n.event)
	}
	slices.Sort(events)
	want := []string{"exec:" + withCopy.ID + ":exit", "exec:" + noCopy.ID + ":exit", "exec:" + ended.ID + ":exit"}
	slices.Sort(want)
	if !slices.Equal(events, want) {
		t.Fatalf("notices after restart = %q, want %q", events, want)
	}
	// The live job still ends normally under the new daemon.
	fx.holders.exit(alive.HolderID, 0)
	eventuallyExec(t, "the live job's notice", func() bool { return len(fx.sessions.noticeList()) == 4 })
}

// TestHostedDeferredAndDroppedNotices: a deferred notice stays pending for
// a later pass; a dropped one is never retried.
func TestHostedDeferredAndDroppedNotices(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	fx.sessions.outcome = NoticeDeferred
	v, _ := fx.launch(context.Background(), nil, HostedLaunch{})
	fx.holders.exit(v.HolderID, 0)
	eventuallyExec(t, "the job's end", func() bool { got, _ := fx.h.Get("lead", v.ID); return got.Terminal() })
	if got, _ := fx.h.Get("lead", v.ID); got.Notice != NoticePending {
		t.Fatalf("deferred notice state = %q", got.Notice)
	}
	fx.sessions.mu.Lock()
	fx.sessions.outcome = 0
	fx.sessions.mu.Unlock()
	fx.h.deliverPending()
	if got, _ := fx.h.Get("lead", v.ID); got.Notice != NoticeDelivered || len(fx.sessions.noticeList()) != 1 {
		t.Fatalf("retried notice = %q, %d sent", got.Notice, len(fx.sessions.noticeList()))
	}

	fx.sessions.mu.Lock()
	fx.sessions.outcome = NoticeDropped
	fx.sessions.mu.Unlock()
	w, _ := fx.launch(context.Background(), nil, HostedLaunch{})
	fx.holders.exit(w.HolderID, 0)
	eventuallyExec(t, "the dropped notice", func() bool { got, _ := fx.h.Get("lead", w.ID); return got.Notice == NoticeSuppressed })
}

// TestHostedJobCapDropsOldestFinished: past the job cap the oldest finished
// jobs go, from memory and the store.
func TestHostedJobCapDropsOldestFinished(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	_ = fx.h.Close()
	zero := 0
	for i := 0; i < HostedMaxJobs; i++ {
		id := fmt.Sprintf("exec_old-job-%03d", i)
		_ = fx.store.Put(HostedJobRow{ID: id, Owner: "lead", State: HostedExited, ExitCode: &zero, StartedAt: int64(i + 1), EndedAt: int64(i + 1)})
	}
	fx.open(10 * time.Millisecond)
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fx.store.deleted, []string{"exec_old-job-000"}) {
		t.Fatalf("dropped = %q", fx.store.deleted)
	}
	list, more := fx.h.List("lead")
	if len(list) != HostedListMax || more != HostedMaxJobs-HostedListMax || list[0].State != HostedRunning {
		t.Fatalf("listing has %d (+%d more), first %+v", len(list), more, list[0])
	}
}

// TestHostedLegacyShapeAndReaders: a hosted legacy launch answers the
// legacy Response, and the legacy readers find it by root.
func TestHostedLegacyShapeAndReaders(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.holders.onSpawn = func(id string) {
		go func() {
			fx.holders.out(id, "stdout", "out-1\nout-2\n")
			fx.holders.out(id, "stderr", "err-1\n")
			fx.holders.exit(id, 2)
		}()
	}
	root := fx.exitDir
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.shell", Command: "false", Shell: "/bin/sh", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	r := fx.h.LegacyResponse(v, true)
	if r.ExecKey != v.ID || r.Status != stateFailed || r.ExitCode != 2 || r.Error != "exit status 2" || !r.ResultReady || r.Stdout != "out-1\nout-2\n" || r.Stderr != "err-1\n" || r.CombinedBytes != 18 || r.CompletedAt == "" {
		t.Fatalf("legacy response = %+v", r)
	}
	if _, ok := fx.h.LegacyStatus(filepath.Join(root, "elsewhere"), v.ID); ok {
		t.Fatal("another root sees the job")
	}
	if st, ok := fx.h.LegacyStatus(root, v.ID); !ok || st.Status != stateFailed || st.Stdout != "" {
		t.Fatalf("status = %+v, %v", st, ok)
	}
	if res, ok := fx.h.LegacyResult(context.Background(), root, v.ID, 0); !ok || res.Stdout != "out-1\nout-2\n" {
		t.Fatalf("result = %+v", res)
	}
	tail, ok, err := fx.h.LegacyTail(root, v.ID, "", 1)
	if !ok || err != nil || tail.Text != "out-2\n" || tail.Stream != "stdout" {
		t.Fatalf("tail = %+v %v %v", tail, ok, err)
	}
	read, ok, err := fx.h.LegacyRead(root, v.ID, "stdout", 6, 100)
	if !ok || err != nil || read.Text != "out-2\n" || read.Offset != 6 || read.NextOffset != 12 || !read.EOF || read.EvictedBefore != 0 {
		t.Fatalf("read = %+v %v %v", read, ok, err)
	}
	grep, ok, err := fx.h.LegacyGrep(root, v.ID, "combined", "err", 0, 0, 10, false)
	if !ok || err != nil || len(grep.Matches) != 1 || grep.Matches[0].Line != 3 {
		t.Fatalf("grep = %+v %v %v", grep, ok, err)
	}
	if _, ok, _ := fx.h.LegacyRead(root, "exec-00000000", "stdout", 0, 10); ok {
		t.Fatal("an unknown key is hosted")
	}

	// A running legacy job: the timed reply carries the guidance, and abort
	// cancels it.
	fx.holders.onSpawn = nil
	fx.h.opts.Window = 10 * time.Millisecond
	v, err = fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.spawn", Root: root})
	if err != nil || v.Outcome != OutcomeConverted {
		t.Fatalf("timed legacy launch = %+v, %v", v, err)
	}
	if r := fx.h.LegacyResponse(v, false); r.Status != stateRunning || r.ResultReady || r.Guidance == "" {
		t.Fatalf("timed legacy response = %+v", r)
	}
	ab, ok := fx.h.LegacyAbort(root, v.ID)
	if !ok || ab.Status != stateCancelled || !ab.ResultReady {
		t.Fatalf("abort = %+v", ab)
	}
}

// TestHostedLegacyReadReportsEvictedOffsets: a read below the ring's
// retained window starts at its oldest byte and says where.
func TestHostedLegacyReadReportsEvictedOffsets(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.h.rings = NewOutputRings(16, 1<<20)
	fx.holders.onSpawn = func(id string) {
		go func() {
			fx.holders.out(id, "stdout", "0123456789abcdefghij\n")
			fx.holders.exit(id, 0)
		}()
	}
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.spawn"})
	if err != nil {
		t.Fatal(err)
	}
	read, ok, err := fx.h.LegacyRead(fx.exitDir, v.ID, "stdout", 0, 100)
	if !ok || err != nil || read.EvictedBefore == 0 || read.Offset != read.EvictedBefore || read.Size != 21 || !strings.HasSuffix(read.Text, "ghij\n") {
		t.Fatalf("read = %+v %v %v", read, ok, err)
	}
	if r := fx.h.LegacyResponse(v, true); !strings.Contains(r.Error, "left the output ring") {
		t.Fatalf("inline result hides the evicted prefix: %+v", r)
	}
}

// TestHostedLaunchRefusals: no argv, an unknown owner, and a spawn failure.
func TestHostedLaunchRefusals(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Argv: []string{""}}); err == nil {
		t.Fatal("an empty argv launched")
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Owner: "nobody"}); err == nil {
		t.Fatal("an unknown owner launched")
	}
	fx.holders.spawnErr = errors.New("no such file")
	v, err := fx.launch(context.Background(), nil, HostedLaunch{})
	if err == nil || v.State != HostedFailed {
		t.Fatalf("spawn failure = %+v, %v", v, err)
	}
	eventuallyExec(t, "the failed job's holder stop", func() bool { return slices.Contains(fx.holders.stops(), v.HolderID) })
}
