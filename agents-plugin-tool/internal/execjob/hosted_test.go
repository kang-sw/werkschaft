package execjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	// beforePut runs at the start of each Put, before the row is stored.
	beforePut func(HostedJobRow)
	// failPut refuses the rows it matches, as a busy database does.
	failPut func(HostedJobRow) bool
	// failDelete refuses every Delete.
	failDelete bool
	// duringDelete runs inside Delete, before it answers: what another
	// goroutine does while the delete is under way.
	duringDelete func(ids []string)
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
	if s.beforePut != nil {
		s.beforePut(row)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failPut != nil && s.failPut(row) {
		return errors.New("database is locked")
	}
	s.rows[row.ID] = row
	s.puts++
	return nil
}

func (s *memHostedStore) Delete(ids ...string) error {
	s.mu.Lock()
	during := s.duringDelete
	s.mu.Unlock()
	if during != nil {
		during(ids)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDelete {
		return errors.New("database is locked")
	}
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
	inputErr error
	replays  []string
	// incomplete is the listing's Incomplete flag; onScan runs in Scan
	// (a test completes the census there).
	incomplete bool
	onScan     func()
	// inputClosed is each holder's stdin-closed state, as a holder tracks
	// it: an eof at spawn or in an input closes it, and a later input is
	// refused. noInputState lists holders without input_closed, as one
	// that predates the field does. inputCalls counts Input calls.
	inputClosed  map[string]bool
	noInputState bool
	inputCalls   int
	// unattached lists holders the manager has not attached (yet): the
	// listing gives them no child status.
	unattached map[string]bool
	// onSpawn runs after a spawn, outside the lock (the child's own
	// behaviour: output, exit).
	onSpawn func(id string)
	// beforeSpawn and beforeLaunch, when set, run at the start of Spawn and
	// LaunchWith, outside the lock (a test holds the hand-over there).
	beforeSpawn  func(id string)
	beforeLaunch func()
	// beforeStop, when set, runs at the start of Stop, outside the lock
	// (a test holds one stop in flight there).
	beforeStop func(id string)
	// afterUnknownStop, when set, runs once Stop has found its id unknown
	// and read the census flag, before it answers (a scan that completes
	// the census in that gap).
	afterUnknownStop func()
	// stopErr fails Stop: the holder stays, its child runs on. stopCalls
	// counts Stop calls, failed ones included.
	stopErr   error
	stopCalls int
	// stopEmitsExit makes Stop send the exit record of a holder whose
	// process never started too, as a real holder's does for its job.
	stopEmitsExit bool
	// onLaunch runs inside LaunchWith, outside the lock, before the new
	// holder's id reaches the job.
	onLaunch func(id string)
	// attachErr fails LaunchWith after the holder registered, as a failed
	// attach does: the holder runs and its id comes back with the error.
	attachErr error
}

func newFakeExecHolders() *fakeExecHolders {
	return &fakeExecHolders{launched: map[string]wsholder.LaunchSpec{}, spawned: map[string]wsholder.SpawnSpec{}, inputs: map[string][]string{}, live: map[string]json.RawMessage{}, exited: map[string]bool{}, seq: map[string]uint64{}, inputClosed: map[string]bool{}, unattached: map[string]bool{}}
}

func (f *fakeExecHolders) LaunchWith(spec wsholder.LaunchSpec) (string, error) {
	f.mu.Lock()
	hold := f.beforeLaunch
	f.mu.Unlock()
	if hold != nil {
		hold()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("h%012d", f.n)
	f.launched[id] = spec
	f.live[id] = spec.Session
	if f.onLaunch != nil {
		hook := f.onLaunch
		f.mu.Unlock()
		hook(id)
		f.mu.Lock()
	}
	if f.attachErr != nil {
		return id, f.attachErr
	}
	return id, nil
}

func (f *fakeExecHolders) Spawn(id string, spec wsholder.SpawnSpec) (wsholder.ChildStatus, error) {
	f.mu.Lock()
	hold := f.beforeSpawn
	f.mu.Unlock()
	if hold != nil {
		hold(id)
	}
	f.mu.Lock()
	if f.spawnErr != nil {
		f.mu.Unlock()
		return wsholder.ChildStatus{}, f.spawnErr
	}
	f.spawned[id] = spec
	f.inputClosed[id] = spec.InputEOF
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
	f.inputCalls++
	if f.inputErr != nil {
		return f.inputErr
	}
	if _, ok := f.spawned[id]; !ok {
		return &wsholder.InputError{Refused: true, Err: &wsholder.OpError{Code: "no_child", Message: "no child is running"}}
	}
	if f.inputClosed[id] && !f.noInputState {
		return &wsholder.OpError{Code: "input_closed", Message: "the child's stdin was closed by an earlier eof"}
	}
	entry := string(data)
	if eof {
		entry += "<EOF>"
		f.inputClosed[id] = true
	}
	f.inputs[id] = append(f.inputs[id], entry)
	return nil
}

// Stop ends the holder: a child still running gets the stop's signal and
// its exit record, as the real holder sends before it goes.
func (f *fakeExecHolders) Stop(id string) error {
	f.mu.Lock()
	hook := f.beforeStop
	f.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	f.mu.Lock()
	f.stopCalls++
	if err := f.stopErr; err != nil {
		f.mu.Unlock()
		return err
	}
	if _, ok := f.live[id]; !ok {
		incomplete, after := f.incomplete, f.afterUnknownStop
		f.mu.Unlock()
		if after != nil {
			after()
		}
		if incomplete {
			// Decided with the lookup, as the manager does.
			return wsholder.ErrUnknownHolderIncomplete
		}
		return wsholder.ErrUnknownHolder
	}
	f.stopped = append(f.stopped, id)
	running := !f.exited[id] && (f.spawned[id].Argv != nil || f.stopEmitsExit)
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

func (f *fakeExecHolders) Scan() {
	f.mu.Lock()
	fn := f.onScan
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeExecHolders) Replay(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replays = append(f.replays, id)
	return nil
}

func (f *fakeExecHolders) List() wsholder.Listing {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := wsholder.Listing{Incomplete: f.incomplete}
	for id, session := range f.live {
		if f.unattached[id] {
			out.Holders = append(out.Holders, wsholder.Info{ID: id, Cause: wsholder.CauseNoAnswer, Session: session})
			continue
		}
		child := &wsholder.ChildStatus{State: "running"}
		if f.exited[id] {
			child.State = "exited"
		} else if !f.noInputState {
			closed := f.inputClosed[id]
			child.InputClosed = &closed
		}
		out.Holders = append(out.Holders, wsholder.Info{ID: id, Attached: true, Session: session, Child: child})
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
	// notifyErr fails every Notify; handErr every Hand; hands are the
	// passive receipts handed.
	notifyErr, handErr error
	hands              []execNotice
	// ownerCalls counts Owner calls; ownerHook runs at the start of call n
	// (1-based), outside the lock: a test's interleaving point.
	ownerCalls int
	ownerHook  func(call int)
	// beforeNotify, when set, runs at the start of Notify with the event id,
	// outside the lock (a test holds an admission in flight there).
	beforeNotify func(eventID string)
}

func newFakeExecSessions() *fakeExecSessions {
	return &fakeExecSessions{owners: map[string]HostedOwner{"lead": {ID: "lead", Gen: 3, Lead: true}, "other": {ID: "other", Gen: 1}}, seen: map[string]bool{}}
}

func (s *fakeExecSessions) Owner(id string) (HostedOwner, error) {
	s.mu.Lock()
	s.ownerCalls++
	call, hook := s.ownerCalls, s.ownerHook
	s.mu.Unlock()
	if hook != nil {
		hook(call)
	}
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

func (s *fakeExecSessions) StopNote(id string) string { return "NOTE: deliberate stop" }

func (s *fakeExecSessions) Hand(id string, gen uint64, execID, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handErr != nil {
		return s.handErr
	}
	s.hands = append(s.hands, execNotice{owner: id, gen: gen, event: execID, text: text})
	return nil
}

func (s *fakeExecSessions) handList() []execNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.hands)
}

func (s *fakeExecSessions) Notify(id string, gen uint64, eventID, text string) (NoticeOutcome, error) {
	s.mu.Lock()
	hook := s.beforeNotify
	s.mu.Unlock()
	if hook != nil {
		hook(eventID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notifyErr != nil {
		return 0, s.notifyErr
	}
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
	// onRunning is the next open's HostedOptions.OnRunning.
	onRunning func()
	// fastRetire makes the next open retry holder stops within
	// milliseconds (HostedOptions.RetireRetryMin and RetireRetryMax).
	fastRetire bool
	// now is the next open's HostedOptions.Now (nil: the wall clock).
	now func() time.Time
}

func newHostedFixture(t *testing.T, window time.Duration) *hostedFixture {
	t.Helper()
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	fx.open(window)
	return fx
}

// newFastRetireFixture is newHostedFixture whose Hosted retries holder stops
// within milliseconds.
func newFastRetireFixture(t *testing.T, window time.Duration) *hostedFixture {
	t.Helper()
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir(), fastRetire: true}
	fx.open(window)
	return fx
}

// open builds a Hosted over the fixture's store, as a daemon start does.
func (fx *hostedFixture) open(window time.Duration) {
	fx.t.Helper()
	opts := HostedOptions{Holders: fx.holders, Store: fx.store, Sessions: fx.sessions, ExitDir: fx.exitDir, Window: window, OnRunning: fx.onRunning, Now: fx.now}
	if fx.fastRetire {
		opts.RetireRetryMin, opts.RetireRetryMax = time.Millisecond, 4*time.Millisecond
	}
	h, err := NewHosted(opts)
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

// TestHostedLateRunningRowNeverOverwritesTheEnd: a launch's running row
// that reaches the store after the job's end does not replace the end's
// row, so a reopen reads the job as exited, not lost.
func TestHostedLateRunningRowNeverOverwritesTheEnd(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	holders := make(chan string, 1)
	fx.holders.onSpawn = func(id string) { holders <- id }
	held, release := make(chan string, 1), make(chan struct{})
	var once sync.Once
	fx.store.beforePut = func(row HostedJobRow) {
		if row.State == HostedRunning && row.PID != 0 {
			once.Do(func() {
				held <- row.ID
				<-release
			})
		}
	}
	type launched struct {
		v   HostedView
		err error
	}
	result := make(chan launched, 1)
	go func() {
		v, err := fx.launch(context.Background(), nil, HostedLaunch{})
		result <- launched{v, err}
	}()
	id, holder := <-held, <-holders
	// The job ends while its launch's running row waits to be written.
	exited := make(chan struct{})
	go func() {
		fx.holders.exit(holder, 0)
		close(exited)
	}()
	eventuallyExec(t, "the job's end in memory", func() bool { got, _ := fx.h.Get("lead", id); return got.Terminal() })
	// Give an end row that does not wait for the running one time to land
	// first, as it would without the ordering.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if row, ok := fx.store.row(id); ok && row.Terminal() {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(release)
	r := <-result
	if r.err != nil || r.v.Outcome != OutcomeDone || r.v.State != HostedExited {
		t.Fatalf("launch = %+v, %v", r.v, r.err)
	}
	<-exited
	if row, ok := fx.store.row(id); !ok || row.State != HostedExited || row.ExitCode == nil || *row.ExitCode != 0 {
		t.Fatalf("stored row = %+v, %v; the late running row replaced the end", row, ok)
	}
	eventuallyExec(t, "the ended job's holder stop", func() bool { return slices.Contains(fx.holders.stops(), holder) })
	_ = fx.h.Close()
	fx.open(5 * time.Second)
	if got, err := fx.h.Get("lead", id); err != nil || got.State != HostedExited {
		t.Fatalf("after reopen = %+v, %v", got, err)
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

// TestHostedRunningOwnersFollowEachJob: RunningOwners counts the running
// jobs by owning conversation; OnRunning runs, with no lock held, once a
// job is admitted, once it is spawned and saved (its start time is set), and
// once it ends, whether it exits, is stopped, or never starts; a reopened Hosted counts a running job its predecessor left.
func TestHostedRunningOwnersFollowEachJob(t *testing.T) {
	fx := newHostedFixture(t, time.Hour)
	var mu sync.Mutex
	var seen []map[string]int
	fx.onRunning = func() {
		// No Hosted lock is held: RunningOwners must not deadlock here.
		owners := fx.h.RunningOwners()
		mu.Lock()
		seen = append(seen, owners)
		mu.Unlock()
	}
	fx.open(time.Hour)
	if got := fx.h.RunningOwners(); len(got) != 0 {
		t.Fatalf("before any launch: RunningOwners = %v", got)
	}
	lead, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil || lead.State != HostedRunning {
		t.Fatalf("lead launch = %+v, %v", lead, err)
	}
	other, err := fx.launch(context.Background(), nil, HostedLaunch{Owner: "other", Async: true})
	if err != nil || other.State != HostedRunning {
		t.Fatalf("other launch = %+v, %v", other, err)
	}
	if got := fx.h.RunningOwners(); got["lead"] != 1 || got["other"] != 1 || len(got) != 2 {
		t.Fatalf("two running jobs: RunningOwners = %v", got)
	}
	fx.holders.exit(lead.HolderID, 0)
	if _, err := fx.h.Stop("other", other.ID); err != nil {
		t.Fatal(err)
	}
	fx.holders.spawnErr = errors.New("no such program")
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err == nil {
		t.Fatal("a launch whose spawn fails succeeded")
	}
	mu.Lock()
	got := slices.Clone(seen)
	mu.Unlock()
	want := []map[string]int{
		{"lead": 1},             // lead admitted
		{"lead": 1},             // lead spawned (its start time is set)
		{"lead": 1, "other": 1}, // other admitted
		{"lead": 1, "other": 1}, // other spawned
		{"other": 1},            // lead exited
		{},                      // other stopped
		{"lead": 1},             // the failing launch admitted
		{},                      // and ended before it started
	}
	if len(got) != len(want) {
		t.Fatalf("OnRunning calls = %v, want %v", got, want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("OnRunning call %d saw %v, want %v (all: %v)", i, got[i], want[i], got)
		}
		for k, n := range want[i] {
			if got[i][k] != n {
				t.Fatalf("OnRunning call %d saw %v, want %v (all: %v)", i, got[i], want[i], got)
			}
		}
	}
	// A job still running when the daemon restarts counts at once. Its end
	// is observed when its holder leaves before the exit record arrives,
	// and when the next start finds a running job's holder gone.
	fx.holders.spawnErr = nil
	kept, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	seen = nil
	mu.Unlock()
	fx.open(time.Hour)
	if got := fx.h.RunningOwners(); got["lead"] != 1 || len(got) != 1 {
		t.Fatalf("after a reopen with %s running: RunningOwners = %v", kept.ID, got)
	}
	left, err := fx.launch(context.Background(), nil, HostedLaunch{Owner: "other", Async: true})
	if err != nil || left.State != HostedRunning {
		t.Fatalf("other launch after the reopen = %+v, %v", left, err)
	}
	fx.holders.mu.Lock()
	session := fx.holders.live[kept.HolderID]
	delete(fx.holders.live, left.HolderID)
	fx.holders.mu.Unlock()
	code := 0
	fx.h.HolderGone(wsholder.Descriptor{ID: kept.HolderID, Session: session}, wsholder.Exit{ExitCode: &code, Reason: "exit"})
	fx.open(time.Hour)
	if got := fx.h.RunningOwners(); len(got) != 0 {
		t.Fatalf("after a reopen with %s's holder gone: RunningOwners = %v", left.ID, got)
	}
	mu.Lock()
	got = slices.Clone(seen)
	mu.Unlock()
	want = []map[string]int{
		{"lead": 1, "other": 1}, // other admitted beside the kept job
		{"lead": 1, "other": 1}, // other spawned
		{"other": 1},            // the kept job's holder left (HolderGone)
		{},                      // the next start found other's holder gone
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OnRunning calls across reopens = %v, want %v", got, want)
	}
	if got := (*Hosted)(nil).RunningOwners(); len(got) != 0 {
		t.Fatalf("a nil Hosted counts %v", got)
	}
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
	// An eof the holder refuses (detached, input queue full) leaves stdin
	// open, so a retry still delivers it.
	fx.holders.mu.Lock()
	fx.holders.inputErr = wsholder.ErrDetached
	fx.holders.mu.Unlock()
	if _, err := fx.h.Write("lead", v.ID, []byte("line2\n"), true); err == nil {
		t.Fatal("a refused eof write reported success")
	}
	fx.holders.mu.Lock()
	fx.holders.inputErr = nil
	fx.holders.mu.Unlock()
	if _, err := fx.h.Write("lead", v.ID, []byte("line2\n"), true); err != nil {
		t.Fatalf("the eof retry = %v", err)
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

// TestHostedStdinSurvivesARestart: a running job launched without stdin
// takes exec.write and its eof under the next daemon, whose holder still
// holds the pipe open; a job given stdin at launch, and a legacy launch,
// stay closed.
func TestHostedStdinSurvivesARestart(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	open, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	given, _ := fx.launch(context.Background(), nil, HostedLaunch{Async: true, Stdin: []byte("x"), StdinSet: true})
	legacy, _ := fx.launch(context.Background(), nil, HostedLaunch{Async: true, Legacy: true, Tool: "exec.spawn"})
	// Launched with explicit empty stdin, and closed by an acked eof before
	// the replacement: the row cannot tell either from an open stdin.
	empty, _ := fx.launch(context.Background(), nil, HostedLaunch{Async: true, StdinSet: true})
	acked, _ := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if _, err := fx.h.Write("lead", acked.ID, []byte("last\n"), true); err != nil {
		t.Fatal(err)
	}
	_ = fx.h.Close()
	fx.open(10 * time.Millisecond)
	for _, v := range []HostedView{empty, acked} {
		fx.holders.mu.Lock()
		before := fx.holders.inputCalls
		fx.holders.mu.Unlock()
		if _, err := fx.h.Write("lead", v.ID, []byte("x"), false); err == nil || !strings.Contains(err.Error(), "no open stdin") {
			t.Fatalf("a write to %s's closed stdin after restart = %v", v.ID, err)
		}
		fx.holders.mu.Lock()
		after := fx.holders.inputCalls
		fx.holders.mu.Unlock()
		if after != before {
			t.Fatalf("a write to %s's closed stdin reached the holder after restart", v.ID)
		}
	}
	if _, err := fx.h.Write("lead", open.ID, []byte("after\n"), false); err != nil {
		t.Fatalf("write after restart = %v", err)
	}
	if _, err := fx.h.Write("lead", open.ID, nil, true); err != nil {
		t.Fatalf("eof after restart = %v", err)
	}
	if got := fx.holders.inputOf(open.HolderID); !slices.Equal(got, []string{"after\n", "<EOF>"}) {
		t.Fatalf("inputs after restart = %q", got)
	}
	for _, v := range []HostedView{given, legacy} {
		if _, err := fx.h.Write("lead", v.ID, []byte("x"), false); err == nil {
			t.Fatalf("a write to %s's closed stdin was accepted after restart", v.ID)
		}
	}
}

// TestHostedPartialWriteSaysWhatToResend: a multi-chunk write the holder
// stopped partway answers how many bytes it took and whether the rest was
// refused or its delivery is unknown, so a retry does not duplicate the
// accepted prefix; stdin stays open.
func TestHostedPartialWriteSaysWhatToResend(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{})
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 600<<10)
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&wsholder.InputError{Accepted: 512 << 10, Refused: true, Err: &wsholder.OpError{Code: "input_overflow", Message: "the child is not reading its input"}}, "refused the rest: holder: input_overflow: the child is not reading its input; stdin stays open: resend from byte 524288 of 614400"},
		{&wsholder.InputError{Accepted: 256 << 10, Err: wsholder.ErrDetached}, "whether the rest arrived is unknown: holder: not attached; check the job before resending from byte 262144 of 614400"},
	} {
		fx.holders.mu.Lock()
		fx.holders.inputErr = tc.err
		fx.holders.mu.Unlock()
		_, err := fx.h.Write("lead", v.ID, data, true)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("partial write = %v, want it to say %q", err, tc.want)
		}
	}
	fx.holders.mu.Lock()
	fx.holders.inputErr = nil
	fx.holders.mu.Unlock()
	if _, err := fx.h.Write("lead", v.ID, data[512<<10:], true); err != nil {
		t.Fatalf("the resend after a partial write = %v", err)
	}
}

// TestHostedWriteToAGoneStdinGivesNoResendAdvice: a refusal that no
// resend can cure (the child already ended, its stdin closed, or a bad
// request) says so instead of "stdin stays open: resend"; a gone stdin
// refuses the next write without reaching the holder.
func TestHostedWriteToAGoneStdinGivesNoResendAdvice(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
		gone bool
	}{
		{"no_child first chunk", &wsholder.InputError{Refused: true, Err: &wsholder.OpError{Code: "no_child", Message: "no child is running"}}, "no open stdin: the job's process has ended (the holder took 0 of 614400 bytes)", true},
		{"no_child later chunk", &wsholder.InputError{Accepted: 256 << 10, Refused: true, Err: &wsholder.OpError{Code: "no_child", Message: "no child is running"}}, "no open stdin: the job's process has ended (the holder took 262144 of 614400 bytes)", true},
		{"input_closed later chunk", &wsholder.InputError{Accepted: 256 << 10, Refused: true, Err: &wsholder.OpError{Code: "input_closed", Message: "closed"}}, "no open stdin: an earlier eof closed it (the holder took 262144 of 614400 bytes)", true},
		{"bad_request", &wsholder.InputError{Refused: true, Err: &wsholder.OpError{Code: "bad_request", Message: "input data must be base64"}}, "holder: bad_request: input data must be base64", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newHostedFixture(t, 10*time.Millisecond)
			v, err := fx.launch(context.Background(), nil, HostedLaunch{})
			if err != nil {
				t.Fatal(err)
			}
			fx.holders.mu.Lock()
			fx.holders.inputErr = tc.err
			fx.holders.mu.Unlock()
			_, err = fx.h.Write("lead", v.ID, make([]byte, 600<<10), true)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "resend") {
				t.Fatalf("write = %v, want it to say %q and give no resend advice", err, tc.want)
			}
			fx.holders.mu.Lock()
			fx.holders.inputErr = nil
			fx.holders.mu.Unlock()
			_, err = fx.h.Write("lead", v.ID, []byte("x"), false)
			if gone := err != nil && strings.Contains(err.Error(), "no open stdin"); gone != tc.gone {
				t.Fatalf("the next write = %v, want stdin gone %v", err, tc.gone)
			}
		})
	}
}

// TestHostedStdinSettlesOnALaterAttach: a rejoined job whose holder did
// not attach by Start has an unknown stdin; a write refuses without
// reaching the holder until the holder attaches, then delivers.
func TestHostedStdinSettlesOnALaterAttach(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = fx.h.Close()
	fx.holders.mu.Lock()
	fx.holders.unattached[v.HolderID] = true
	fx.holders.mu.Unlock()
	fx.open(10 * time.Millisecond)
	if _, err := fx.h.Write("lead", v.ID, []byte("early\n"), false); err == nil || !strings.Contains(err.Error(), "not known yet") {
		t.Fatalf("a write before the holder attached = %v", err)
	}
	// The manager's rescan attaches the holder.
	fx.holders.mu.Lock()
	delete(fx.holders.unattached, v.HolderID)
	fx.holders.mu.Unlock()
	if _, err := fx.h.Write("lead", v.ID, []byte("later\n"), true); err != nil {
		t.Fatalf("a write after the holder attached = %v", err)
	}
	if got := fx.holders.inputOf(v.HolderID); !slices.Equal(got, []string{"later\n<EOF>"}) {
		t.Fatalf("inputs = %q", got)
	}
	if _, err := fx.h.Write("lead", v.ID, []byte("x"), false); err == nil || !strings.Contains(err.Error(), "no open stdin") {
		t.Fatalf("a write after the eof = %v", err)
	}
}

// TestHostedStdinUnknownAfterRestartStaysClosed: a holder that does not
// report its stdin state leaves a rejoined job's stdin closed, so a write
// never claims a success the holder may have dropped.
func TestHostedStdinUnknownAfterRestartStaysClosed(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = fx.h.Close()
	fx.holders.mu.Lock()
	fx.holders.noInputState = true
	fx.holders.mu.Unlock()
	fx.open(10 * time.Millisecond)
	if _, err := fx.h.Write("lead", v.ID, []byte("x"), false); err == nil {
		t.Fatalf("a write to a stdin of unknown state was accepted")
	}
	if got := fx.holders.inputOf(v.HolderID); len(got) != 0 {
		t.Fatalf("inputs = %q", got)
	}
}

// TestHostedRejoinKeepsAbsoluteOffsets: a running job rejoined after a
// daemon replacement places the holder's replayed records at their
// absolute offsets, so a saved exec.raw.read cursor reads the same bytes
// and a read below the replay reports the prefix the holder evicted.
func TestHostedRejoinKeepsAbsoluteOffsets(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.spawn", Async: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = fx.h.Close()
	fx.open(10 * time.Millisecond)
	// The holder's ring kept only the output from byte 4096 on.
	at := func(off int64) *int64 { return &off }
	fx.holders.emit(v.HolderID, wsholder.Event{Event: "output", Stream: "stdout", Data: []byte("kept-1\n"), Offset: at(4096), CombinedOffset: at(4096)})
	fx.holders.emit(v.HolderID, wsholder.Event{Event: "output", Stream: "stdout", Data: []byte("kept-2\n"), Offset: at(4103), CombinedOffset: at(4103)})
	read, ok, err := fx.h.LegacyRead(fx.exitDir, v.ID, "stdout", 4103, 100)
	if !ok || err != nil || read.Offset != 4103 || read.Text != "kept-2\n" || read.Size != 4110 || read.EvictedBefore != 0 {
		t.Fatalf("read from a saved cursor = %+v %v %v", read, ok, err)
	}
	read, ok, err = fx.h.LegacyRead(fx.exitDir, v.ID, "stdout", 0, 100)
	if !ok || err != nil || read.EvictedBefore != 4096 || read.Offset != 4096 || read.Text != "kept-1\nkept-2\n" {
		t.Fatalf("read below the replay = %+v %v %v", read, ok, err)
	}
}

// TestHostedExitOnlyReplayKeepsTheTotals: a rejoin whose replay holds only
// the exit record still ends the job with its output totals, so a saved
// cursor reports the gap instead of clamping to 0.
func TestHostedExitOnlyReplayKeepsTheTotals(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.spawn", Async: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = fx.h.Close()
	fx.open(10 * time.Millisecond)
	code, total := 0, int64(3<<20)
	fx.holders.emit(v.HolderID, wsholder.Event{Event: "exit", ExitCode: &code, StreamBytes: map[string]int64{"stdout": total - 40, "stderr": 40}, OutputBytes: &total})
	eventuallyExec(t, "the job's end", func() bool { got, _ := fx.h.Get("lead", v.ID); return got.Terminal() })
	read, ok, err := fx.h.LegacyRead(fx.exitDir, v.ID, "stderr", 10, 100)
	if !ok || err != nil || read.Size != 40 || read.EvictedBefore != 40 || read.Offset != 40 {
		t.Fatalf("stderr from a saved cursor = %+v %v %v", read, ok, err)
	}
	got, _ := fx.h.Get("lead", v.ID)
	if got.Written[StreamStdout] != total-40 || got.Written[StreamCombined] != total {
		t.Fatalf("totals after an exit-only replay = %v", got.Written)
	}
	if row, _ := fx.store.row(v.ID); row.Written[StreamCombined] != total {
		t.Fatalf("stored totals = %v", row.Written)
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
	fx.holders.mu.Lock()
	replays := slices.Clone(fx.holders.replays)
	fx.holders.mu.Unlock()
	if !slices.Equal(replays, []string{alive.HolderID}) {
		t.Fatalf("replayed holders = %q, want only the live job's", replays)
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

// TestHostedExitCopyStaysUntilTheEndIsStored: when the store refuses the
// job's terminal row, the holder's cleanup keeps the exit copy, so the next
// daemon, still reading the running row, ends the job with its real status
// and owed notice instead of as lost; it removes the copy once the end is
// stored.
func TestHostedExitCopyStaysUntilTheEndIsStored(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(fx.exitDir, v.ID+".json")
	code := 3
	raw, _ := json.Marshal(wsholder.Exit{ExitCode: &code, Reason: "child_exited"})
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.store.mu.Lock()
	fx.store.failPut = func(r HostedJobRow) bool { return r.Terminal() }
	fx.store.mu.Unlock()
	fx.holders.exit(v.HolderID, code)
	eventuallyExec(t, "the ended job's holder stop", func() bool { return slices.Contains(fx.holders.stops(), v.HolderID) })
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: v.ID})
	fx.h.HolderGone(wsholder.Descriptor{ID: v.HolderID, Session: session}, wsholder.Exit{ExitCode: &code, Reason: "stop"})
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the exit copy went before the end was stored: %v", err)
	}
	if row, _ := fx.store.row(v.ID); row.Terminal() {
		t.Fatalf("stored row = %+v, want the running row the refused end left", row)
	}
	_ = fx.h.Close()

	fx.store.mu.Lock()
	fx.store.failPut = nil
	fx.store.mu.Unlock()
	fx.sessions.mu.Lock()
	fx.sessions.seen = map[string]bool{}
	fx.sessions.notices = nil
	fx.sessions.mu.Unlock()
	fx.open(10 * time.Millisecond)
	got, _ := fx.h.Get("lead", v.ID)
	if got.State != HostedExited || got.ExitCode == nil || *got.ExitCode != code {
		t.Fatalf("job after restart = %+v, want exited %d", got, code)
	}
	if row, _ := fx.store.row(v.ID); !row.Terminal() {
		t.Fatalf("stored row after restart = %+v", row)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the exit copy stays after the end was stored: %v", err)
	}
	eventuallyExec(t, "the owed notice", func() bool { return len(fx.sessions.noticeList()) == 1 })
}

// TestHostedDroppedJobTakesItsOwedExitCopy: a finished job whose terminal
// row was never stored keeps its exit copy until the job cap drops it;
// dropping the job deletes its row and removes the copy with it. A failed
// delete keeps both, so a successor still ends the job from the copy. The
// holder's end may come before the prune, or while the delete is under
// way: through HolderGone (the job already left h.jobs) or through a
// callback that captured the job before the prune; the copy goes only
// after the delete succeeded either way.
func TestHostedDroppedJobTakesItsOwedExitCopy(t *testing.T) {
	for _, during := range []string{"", "holder gone", "captured job"} {
		for _, fails := range []bool{false, true} {
			name := during
			if name == "" {
				name = "before the prune"
			}
			if fails {
				name += ", delete fails"
			}
			t.Run(name, func(t *testing.T) { testDroppedJobExitCopy(t, fails, during) })
		}
	}
}

func testDroppedJobExitCopy(t *testing.T, deleteFails bool, during string) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(fx.exitDir, v.ID+".json")
	code := 0
	raw, _ := json.Marshal(wsholder.Exit{ExitCode: &code, Reason: "child_exited"})
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.store.mu.Lock()
	fx.store.failPut = func(r HostedJobRow) bool { return r.Terminal() }
	fx.store.mu.Unlock()
	fx.holders.exit(v.HolderID, code)
	eventuallyExec(t, "the delivered notice", func() bool {
		got, _ := fx.h.Get("lead", v.ID)
		return got.Notice == NoticeDelivered
	})
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: v.ID})
	gone := func() {
		fx.h.HolderGone(wsholder.Descriptor{ID: v.HolderID, Session: session}, wsholder.Exit{ExitCode: &code, Reason: "stop"})
	}
	fx.h.mu.Lock()
	captured := fx.h.jobs[v.ID]
	fx.h.mu.Unlock()
	var copyDuring error = errors.New("the delete never ran")
	fx.store.mu.Lock()
	fx.store.failPut = nil
	fx.store.failDelete = deleteFails
	fx.store.duringDelete = func(ids []string) {
		if !slices.Contains(ids, v.ID) {
			return
		}
		switch during {
		case "holder gone":
			gone()
		case "captured job":
			fx.h.dropExitCopy(captured)
		}
		_, copyDuring = os.Stat(copyPath)
	}
	fx.store.mu.Unlock()
	if during == "" {
		gone()
		if _, err := os.Stat(copyPath); err != nil {
			t.Fatalf("the exit copy went before the end was stored: %v", err)
		}
	}
	for i := 0; i < HostedMaxJobs; i++ {
		if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err != nil {
			t.Fatal(err)
		}
	}
	if copyDuring != nil {
		t.Fatalf("the exit copy went before the delete answered: %v", copyDuring)
	}
	if deleteFails {
		// A holder end after the failed delete still keeps the copy.
		gone()
		if _, ok := fx.store.row(v.ID); !ok {
			t.Fatalf("the row went though its delete failed")
		}
		if _, err := os.Stat(copyPath); err != nil {
			t.Fatalf("the exit copy went though the row stays: %v", err)
		}
		return
	}
	fx.store.mu.Lock()
	dropped := slices.Contains(fx.store.deleted, v.ID)
	fx.store.mu.Unlock()
	if !dropped {
		t.Fatalf("the finished job %s was not dropped", v.ID)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the dropped job's exit copy stays: %v", err)
	}
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
// jobs go, from memory and the store, except one whose notice is still
// owed.
func TestHostedJobCapDropsOldestFinished(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	_ = fx.h.Close()
	zero := 0
	for i := 0; i < HostedMaxJobs; i++ {
		row := HostedJobRow{ID: fmt.Sprintf("exec_old-job-%03d", i), Owner: "lead", State: HostedExited, ExitCode: &zero, StartedAt: int64(i + 1), EndedAt: int64(i + 1)}
		if i == 0 {
			// Its owner took a handoff: the notice waits for a retry.
			row.Notify, row.Notice = true, NoticePending
		}
		_ = fx.store.Put(row)
	}
	fx.sessions.outcome = NoticeDeferred
	fx.open(10 * time.Millisecond)
	t.Cleanup(func() { _ = fx.h.Close() })
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fx.store.deleted, []string{"exec_old-job-001"}) {
		t.Fatalf("dropped = %q", fx.store.deleted)
	}
	if row, ok := fx.store.row("exec_old-job-000"); !ok || row.Notice != NoticePending {
		t.Fatalf("the job owing a notice = %+v, %v", row, ok)
	}
	list, more := fx.h.List("lead")
	if len(list) != HostedListMax || more != HostedMaxJobs-HostedListMax || list[0].State != HostedRunning {
		t.Fatalf("listing has %d (+%d more), first %+v", len(list), more, list[0])
	}
}

// TestHostedRefusedLaunchKeepsNoRing: a launch the running-job cap
// refuses leaves no output ring behind.
func TestHostedRefusedLaunchKeepsNoRing(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	for i := 0; i < HostedMaxJobs; i++ {
		if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err != nil {
			t.Fatal(err)
		}
	}
	rings := func() int {
		fx.h.rings.mu.Lock()
		defer fx.h.rings.mu.Unlock()
		return len(fx.h.rings.jobs)
	}
	before := rings()
	for i := 0; i < 3; i++ {
		if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err == nil || !strings.Contains(err.Error(), "already run") {
			t.Fatalf("launch over the cap = %v", err)
		}
	}
	if after := rings(); after != before {
		t.Fatalf("refused launches left rings: %d before, %d after", before, after)
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
	// A new-tool job of the same root is its owner's alone: no legacy reader
	// or abort finds it by root.
	fx.holders.onSpawn = nil
	nv, err := fx.launch(context.Background(), nil, HostedLaunch{Root: root, Async: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fx.h.LegacyStatus(root, nv.ID); ok {
		t.Fatal("legacy status finds a new-tool job by root")
	}
	if _, ok, _ := fx.h.LegacyRead(root, nv.ID, "stdout", 0, 10); ok {
		t.Fatal("legacy read finds a new-tool job by root")
	}
	if _, ok := fx.h.LegacyAbort(root, nv.ID); ok {
		t.Fatal("legacy abort finds a new-tool job by root")
	}
	if got, _ := fx.h.Get("lead", nv.ID); got.State != HostedRunning {
		t.Fatalf("the new-tool job after a refused legacy abort = %+v", got)
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

// TestHostedApprovalShowsWhatRuns: the approval names the program the job
// starts, resolved on the job's PATH before the prompt, the call's env
// overlay, and the head of its stdin cut at a character; a program not on
// that PATH is refused before any prompt.
func TestHostedApprovalShowsWhatRuns(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	bin := t.TempDir()
	file := "tool"
	if goruntime.GOOS == "windows" {
		file += ".exe"
	}
	prog := filepath.Join(bin, file)
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdin := strings.Repeat("a", HostedStdinPreviewBytes-1) + "가" + "tail"
	env := map[string]string{"PATH": bin, "BASH_ENV": "/tmp/x.sh"}
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Argv: []string{"tool", "x"}, Display: "tool x", Environ: []string{"PATH=" + bin}, Env: env, Stdin: []byte(stdin), StdinSet: true})
	if err != nil {
		t.Fatal(err)
	}
	fx.sessions.mu.Lock()
	calls := slices.Clone(fx.sessions.approvals)
	fx.sessions.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("approvals = %+v", calls)
	}
	c := calls[0]
	wantEnv := []string{"BASH_ENV=/tmp/x.sh", "PATH=" + bin}
	if !strings.EqualFold(c.Program, prog) || c.Command != "tool x" || !slices.Equal(c.Env, wantEnv) || c.StdinBytes != len(stdin) || c.Stdin != strings.Repeat("a", HostedStdinPreviewBytes-1) {
		t.Fatalf("approval call = %+v", c)
	}
	if s := fx.holders.spawnOf(v.HolderID); len(s.Argv) == 0 || s.Argv[0] != c.Program {
		t.Fatalf("spawned %q, approved %q", s.Argv, c.Program)
	}

	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Argv: []string{"missing-tool"}, Environ: []string{"PATH=" + bin}}); err == nil || !strings.Contains(err.Error(), "not on the job's PATH") {
		t.Fatalf("a missing program = %v", err)
	}
	fx.sessions.mu.Lock()
	n := len(fx.sessions.approvals)
	fx.sessions.mu.Unlock()
	if n != 1 {
		t.Fatalf("a missing program prompted: %d approvals", n)
	}

	// A legacy launch of a missing program keeps the file-backed answer: a
	// failed record under a legacy key the legacy readers find, unprompted.
	lv, err := fx.launch(context.Background(), nil, HostedLaunch{Legacy: true, Tool: "exec.spawn", Argv: []string{"missing-tool"}, Environ: []string{"PATH=" + bin}})
	if err == nil || lv.State != HostedFailed || !keyPattern.MatchString(lv.ID) || !strings.Contains(lv.Error, "not on the job's PATH") {
		t.Fatalf("a legacy missing program = %+v, %v", lv, err)
	}
	if st, ok := fx.h.LegacyStatus(fx.exitDir, lv.ID); !ok || st.Status != stateFailed || !st.ResultReady || st.ExecKey != lv.ID {
		t.Fatalf("legacy status of the failed start = %+v, %v", st, ok)
	}
	if row, ok := fx.store.row(lv.ID); !ok || row.State != HostedFailed {
		t.Fatalf("stored failed start = %+v, %v", row, ok)
	}
	fx.sessions.mu.Lock()
	n = len(fx.sessions.approvals)
	fx.sessions.mu.Unlock()
	if n != 1 {
		t.Fatalf("a legacy missing program prompted: %d approvals", n)
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

// quickJobs makes every later launch's process exit at once, so a
// synchronous launch comes back finished with no notice owed.
func (fx *hostedFixture) quickJobs() {
	fx.holders.mu.Lock()
	fx.holders.onSpawn = func(id string) { go fx.holders.exit(id, 0) }
	fx.holders.mu.Unlock()
}

func (fx *hostedFixture) launchFinished() HostedView {
	fx.t.Helper()
	v, err := fx.launch(context.Background(), nil, HostedLaunch{})
	if err != nil || v.Outcome != OutcomeDone {
		fx.t.Fatalf("a quick job = %+v, %v", v, err)
	}
	return v
}

// TestHostedFailingDeletesStayBounded: while the store refuses every
// delete, the job cap's dropped jobs pile up only to the cap: launches are
// refused once they and the running jobs fill it, and a dropped job keeps
// no holder entry. Once the store recovers, the next launch retries the
// failed deletes and clears them.
func TestHostedFailingDeletesStayBounded(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.quickJobs()
	for i := 0; i < HostedMaxJobs; i++ {
		fx.launchFinished()
	}
	fx.store.mu.Lock()
	fx.store.failDelete = true
	fx.store.mu.Unlock()
	var refusal error
	for i := 0; i < 3*HostedMaxJobs && refusal == nil; i++ {
		_, refusal = fx.launch(context.Background(), nil, HostedLaunch{})
	}
	fx.h.mu.Lock()
	pending, holders := len(fx.h.deleting), len(fx.h.byHolder)
	fx.h.mu.Unlock()
	if refusal == nil || !strings.Contains(refusal.Error(), "could not be deleted") {
		t.Fatalf("launch while deletes fail = %v, want a refusal once the cap fills", refusal)
	}
	if pending > HostedMaxJobs || holders > HostedMaxJobs {
		t.Fatalf("%d dropped jobs pending, %d holder entries; want at most %d each", pending, holders, HostedMaxJobs)
	}
	fx.store.mu.Lock()
	fx.store.failDelete = false
	fx.store.mu.Unlock()
	fx.launchFinished()
	fx.h.mu.Lock()
	pending = len(fx.h.deleting)
	fx.h.mu.Unlock()
	fx.store.mu.Lock()
	rows := len(fx.store.rows)
	fx.store.mu.Unlock()
	if pending != 0 || rows > HostedMaxJobs {
		t.Fatalf("after the store recovered: %d pending, %d rows", pending, rows)
	}
}

// TestHostedIDOfAPendingDeleteIsNotReused: the id draw skips the id of a
// dropped job whose row delete has not succeeded, so a new job cannot
// overwrite that row or its exit copy.
func TestHostedIDOfAPendingDeleteIsNotReused(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.quickJobs()
	for i := 0; i < HostedMaxJobs; i++ {
		fx.launchFinished()
	}
	fx.store.mu.Lock()
	fx.store.failDelete = true
	fx.store.mu.Unlock()
	fx.launchFinished() // drops the oldest job; its delete fails
	var first HostedJobRow
	fx.h.mu.Lock()
	for id := range fx.h.deleting {
		first, _ = fx.store.row(id)
	}
	fx.h.mu.Unlock()
	if first.ID == "" {
		t.Fatalf("no job is pending delete")
	}
	keys := []string{strings.TrimPrefix(first.ID, "exec_"), "fresh-new-key"}
	defer func(old func() (string, error)) { newExecKey = old }(newExecKey)
	newExecKey = func() (string, error) {
		k := keys[0]
		if len(keys) > 1 {
			keys = keys[1:]
		}
		return k, nil
	}
	v := fx.launchFinished()
	if v.ID == first.ID {
		t.Fatalf("the new job reused %s, whose delete is pending", first.ID)
	}
	if row, _ := fx.store.row(first.ID); row.ID != first.ID || row.HolderID != first.HolderID {
		t.Fatalf("the pending row = %+v, want job %s's own", row, first.ID)
	}
}

// TestHostedStaleHolderEndLeavesTheJob: a holder's end that names a job's
// id under another holder (an earlier job's holder under a reused id)
// neither ends the job nor removes the exit copy at its id's path.
func TestHostedStaleHolderEndLeavesTheJob(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(fx.exitDir, v.ID+".json")
	if err := os.WriteFile(copyPath, []byte(`{"reason":"child_exited"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: v.ID})
	code := 9
	fx.h.HolderGone(wsholder.Descriptor{ID: "stale-holder", Session: session}, wsholder.Exit{ExitCode: &code, Reason: "stop"})
	if got, _ := fx.h.Get("lead", v.ID); got.State != HostedRunning {
		t.Fatalf("job after a stale holder's end = %+v, want running", got)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("a stale holder's end removed the live job's exit copy: %v", err)
	}
	fx.holders.out(v.HolderID, "stdout", "still here\n")
	if _, got, _ := fx.h.Tail("lead", v.ID, 1<<10); !strings.Contains(string(got.Data), "still here") {
		t.Fatalf("output after a stale holder's end = %q", got.Data)
	}
}

// TestHostedRepeatedHolderEndsKeepTheCopyUntilADeleteSucceeds: the
// manager calls a holder's end again when its directory removal failed,
// and the callbacks race the job cap's delete. Whatever their order, the
// exit copy of a job whose running row is still stored stays, and only a
// delete that succeeded removes it.
func TestHostedRepeatedHolderEndsKeepTheCopyUntilADeleteSucceeds(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(fx.exitDir, v.ID+".json")
	code := 0
	raw, _ := json.Marshal(wsholder.Exit{ExitCode: &code, Reason: "child_exited"})
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.store.mu.Lock()
	fx.store.failPut = func(r HostedJobRow) bool { return r.Terminal() }
	fx.store.mu.Unlock()
	fx.holders.exit(v.HolderID, code)
	eventuallyExec(t, "the delivered notice", func() bool {
		got, _ := fx.h.Get("lead", v.ID)
		return got.Notice == NoticeDelivered
	})
	time.Sleep(3 * time.Millisecond) // the oldest end: the cap drops it first
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: v.ID})
	gone := func() {
		fx.h.HolderGone(wsholder.Descriptor{ID: v.HolderID, Session: session}, wsholder.Exit{ExitCode: &code, Reason: "stop"})
	}
	goneRacing := func(n int) {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); gone() }()
		}
		wg.Wait()
	}
	rowKeepsCopy := func(when string) {
		t.Helper()
		if _, ok := fx.store.row(v.ID); !ok {
			return
		}
		if _, err := os.Stat(copyPath); err != nil {
			t.Fatalf("%s: the exit copy went while the running row stays: %v", when, err)
		}
	}
	goneRacing(4)
	rowKeepsCopy("repeated ends before the prune")

	fx.store.mu.Lock()
	fx.store.failPut = nil
	fx.store.failDelete = true
	fx.store.duringDelete = func(ids []string) {
		if slices.Contains(ids, v.ID) {
			goneRacing(4)
			rowKeepsCopy("ends racing the failing delete")
		}
	}
	fx.store.mu.Unlock()
	fx.quickJobs()
	for i := 0; i < HostedMaxJobs; i++ {
		fx.launchFinished()
	}
	fx.h.mu.Lock()
	_, pending := fx.h.deleting[v.ID]
	fx.h.mu.Unlock()
	if !pending {
		t.Fatalf("job %s is not pending delete", v.ID)
	}
	goneRacing(4)
	rowKeepsCopy("repeated ends after the failed delete")

	var during error = errors.New("the retry never ran")
	fx.store.mu.Lock()
	fx.store.failDelete = false
	fx.store.duringDelete = func(ids []string) {
		if slices.Contains(ids, v.ID) {
			goneRacing(4)
			_, during = os.Stat(copyPath)
		}
	}
	fx.store.mu.Unlock()
	fx.launchFinished()
	if during != nil {
		t.Fatalf("the exit copy went before the retried delete answered: %v", during)
	}
	if _, ok := fx.store.row(v.ID); ok {
		t.Fatalf("the row stays after the retried delete")
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the exit copy stays after its row's delete: %v", err)
	}
	goneRacing(4)
}

// TestHostedStartSweepsExitCopiesWithoutARow: an exit copy whose job has
// no row (its removal failed, or a predecessor never reached it) goes at
// the next start; a copy whose job still has a row, and anything that is
// not an exit copy, stays.
func TestHostedStartSweepsExitCopiesWithoutARow(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.quickJobs()
	kept := fx.launchFinished()
	write := func(name string) string {
		p := filepath.Join(fx.exitDir, name)
		if err := os.WriteFile(p, []byte(`{"reason":"child_exited"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	keptCopy, orphan, other := write(kept.ID+".json"), write("exec_gone-job-copy.json"), write("notes.txt")
	_ = fx.h.Close()
	fx.open(5 * time.Second)
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("the exit copy with no row stays after a start: %v", err)
	}
	for _, p := range []string{keptCopy, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s went at the start: %v", filepath.Base(p), err)
		}
	}
}

// TestHostedCompetingRetriesNeverDeleteAReusedID: two launches retry the
// failed deletes at once. The first's retry clears an id and a new job
// takes that id; the second launch, which came in while the first's
// delete was under way, must not delete the new job's row or exit copy.
func TestHostedCompetingRetriesNeverDeleteAReusedID(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.quickJobs()
	for i := 0; i < HostedMaxJobs; i++ {
		fx.launchFinished()
	}
	fx.store.mu.Lock()
	fx.store.failDelete = true
	fx.store.mu.Unlock()
	fx.launchFinished() // drops one job; its delete fails
	var reused string
	fx.h.mu.Lock()
	for id := range fx.h.deleting {
		reused = id
	}
	fx.h.mu.Unlock()
	if reused == "" {
		t.Fatal("no job is pending delete")
	}

	// The first retry's delete waits inside the store until the second
	// launch either reached the store for the same id (a retry of an id
	// another delete owns) or finished without doing so; the second
	// retry's delete then waits until the id has been reused.
	secondIn, reusedNow, competed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	// The key seam is set once, before the competing launch starts, and
	// restored only after it finished: the draws read reuseKey under its
	// lock instead.
	var keyMu sync.Mutex
	var reuseKey string
	var competing atomic.Bool
	old := newExecKey
	newExecKey = func() (string, error) {
		keyMu.Lock()
		k := reuseKey
		keyMu.Unlock()
		if k != "" {
			return k, nil
		}
		return old()
	}
	t.Cleanup(func() {
		if competing.Load() {
			select {
			case <-competed:
			case <-time.After(5 * time.Second):
			}
		}
		newExecKey = old
	})
	fx.store.mu.Lock()
	fx.store.failDelete = false
	fx.store.duringDelete = func(ids []string) {
		if !slices.Contains(ids, reused) {
			return
		}
		callsMu.Lock()
		calls++
		n := calls
		callsMu.Unlock()
		switch n {
		case 1:
			competing.Store(true)
			go func() {
				defer close(competed)
				v, err := fx.launch(context.Background(), nil, HostedLaunch{})
				if err != nil || v.Outcome != OutcomeDone {
					t.Errorf("the competing launch = %+v, %v", v, err)
				}
			}()
			select {
			case <-secondIn:
			case <-competed:
			}
		case 2:
			close(secondIn)
			<-reusedNow
		}
	}
	fx.store.mu.Unlock()
	fx.launchFinished() // the first retry clears reused

	keyMu.Lock()
	reuseKey = strings.TrimPrefix(reused, "exec_")
	keyMu.Unlock()
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	keyMu.Lock()
	reuseKey = ""
	keyMu.Unlock()
	if err != nil || v.ID != reused {
		t.Fatalf("the reusing launch = %+v, %v; want id %s", v, err, reused)
	}
	copyPath := filepath.Join(fx.exitDir, reused+".json")
	if err := os.WriteFile(copyPath, []byte(`{"reason":"child_exited"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	callsMu.Lock()
	second := calls >= 2
	callsMu.Unlock()
	if second {
		close(reusedNow)
	}
	select {
	case <-competed:
	case <-time.After(5 * time.Second):
		t.Fatal("the competing launch never finished")
	}
	if row, ok := fx.store.row(reused); !ok || row.HolderID != v.HolderID {
		t.Fatalf("the new job's row under the reused id = %+v, %v", row, ok)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the new job's exit copy went: %v", err)
	}
}

// TestHostedWriteBeforeTheSpawnKeepsStdin: between the holder's launch and
// the process's spawn (here, a reapproval) the job is listed as running;
// a write then is refused without reaching the holder, whose no_child
// would read as an ended process, and stdin stays open for the process.
func TestHostedWriteBeforeTheSpawnKeepsStdin(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	waiting, release := make(chan struct{}), make(chan struct{})
	fx.sessions.mu.Lock()
	fx.sessions.immediate = func(string) (bool, error) {
		close(waiting)
		<-release
		return true, nil
	}
	fx.sessions.mu.Unlock()
	type result struct {
		v   HostedView
		err error
	}
	launched := make(chan result, 1)
	go func() {
		v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
		launched <- result{v, err}
	}()
	<-waiting
	views, _ := fx.h.List("lead")
	if len(views) != 1 || views[0].State != HostedRunning {
		t.Fatalf("jobs before the spawn = %+v", views)
	}
	id := views[0].ID
	if _, err := fx.h.Write("lead", id, []byte("early\n"), false); err == nil || !strings.Contains(err.Error(), "not started its process yet") {
		t.Fatalf("a write before the spawn = %v", err)
	}
	fx.holders.mu.Lock()
	calls := fx.holders.inputCalls
	fx.holders.mu.Unlock()
	if calls != 0 {
		t.Fatalf("a write before the spawn reached the holder %d times", calls)
	}
	close(release)
	r := <-launched
	if r.err != nil {
		t.Fatal(r.err)
	}
	if _, err := fx.h.Write("lead", id, []byte("later\n"), true); err != nil {
		t.Fatalf("a write after the spawn = %v", err)
	}
	if got := fx.holders.inputOf(r.v.HolderID); !slices.Equal(got, []string{"later\n<EOF>"}) {
		t.Fatalf("inputs = %q", got)
	}
}

// TestHostedEarlyHolderEndReadsTheJobUnderTheLock: a holder that ends
// before the launch has recorded it on its job takes HolderGone's mismatch
// path while the launch records the holder; the log reads the job's
// holder under h.mu (run with -race: the read raced the launch's write).
func TestHostedEarlyHolderEndReadsTheJobUnderTheLock(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	done := make(chan struct{})
	fx.holders.mu.Lock()
	fx.holders.onLaunch = func(holder string) {
		fx.holders.mu.Lock()
		session := fx.holders.launched[holder].Session
		fx.holders.mu.Unlock()
		go func() {
			defer close(done)
			fx.h.HolderGone(wsholder.Descriptor{ID: holder, Session: session}, wsholder.Exit{Reason: "killed"})
		}()
		time.Sleep(50 * time.Millisecond)
	}
	fx.holders.mu.Unlock()
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err != nil {
		t.Fatal(err)
	}
	<-done
}

// TestHostedBulkPruneFitsTheTombstoneRoom: a prune that drops many jobs at
// once (their notices were owed until now) drops no more than the room the
// pending deletes leave under the job cap, so a failed bulk delete parks at
// most the cap.
func TestHostedBulkPruneFitsTheTombstoneRoom(t *testing.T) {
	fx := newHostedFixture(t, 5*time.Second)
	fx.quickJobs()
	fx.store.mu.Lock()
	fx.store.failDelete = true
	fx.store.mu.Unlock()
	fx.h.mu.Lock()
	for i := 0; i < HostedMaxJobs*3/4; i++ {
		fx.h.deleting[fmt.Sprintf("exec_parked-%d", i)] = &tombstone{holder: "gone"}
	}
	for i := 0; i < HostedMaxJobs*3/2; i++ {
		id := fmt.Sprintf("exec_finished-%d", i)
		j := &hostedJob{out: fx.h.rings.NewJob(), done: make(chan struct{})}
		j.row = HostedJobRow{ID: id, Owner: "lead", State: HostedExited, EndedAt: int64(i + 1)}
		close(j.done)
		fx.h.jobs[id] = j
	}
	fx.h.mu.Unlock()
	fx.launchFinished()
	fx.h.mu.Lock()
	pending := len(fx.h.deleting)
	fx.h.mu.Unlock()
	if pending > HostedMaxJobs {
		t.Fatalf("%d deletes pending after a failed bulk prune, want at most %d", pending, HostedMaxJobs)
	}
}

// TestHostedLaunchesDrawingOneIDBothRun: two launches that draw the same
// id before either is admitted do not share it: the second admission
// finds it taken and draws again.
func TestHostedLaunchesDrawingOneIDBothRun(t *testing.T) {
	fx := newHostedFixture(t, 10*time.Millisecond)
	var mu sync.Mutex
	draws := 0
	both := make(chan struct{})
	defer func(old func() (string, error)) { newExecKey = old }(newExecKey)
	newExecKey = func() (string, error) {
		mu.Lock()
		draws++
		n := draws
		mu.Unlock()
		switch n {
		case 1:
			<-both
			return "same-drawn-key", nil
		case 2:
			close(both)
			return "same-drawn-key", nil
		}
		return fmt.Sprintf("fresh-drawn-key%d", n), nil
	}
	views := make(chan HostedView, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
			if err != nil {
				t.Errorf("launch = %v", err)
			}
			views <- v
		}()
	}
	wg.Wait()
	close(views)
	var ids []string
	for v := range views {
		ids = append(ids, v.ID)
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("the two launches got ids %q, want two different ones", ids)
	}
	listed, _ := fx.h.List("lead")
	if len(listed) != 2 {
		t.Fatalf("listed jobs = %d, want 2", len(listed))
	}
}

// TestHostedPanicsReleaseTheDeleteClaim: a Store.Delete or OnRunning
// panic that the caller recovers leaves no tombstone claimed: the dropped
// jobs were fenced first, and the next launch's retry deletes them.
func TestHostedPanicsReleaseTheDeleteClaim(t *testing.T) {
	for _, where := range []string{"delete", "on running"} {
		t.Run(where, func(t *testing.T) {
			var once sync.Once
			boom := func() { once.Do(func() { panic("boom") }) }
			var armed atomic.Bool
			var calls atomic.Int64
			fx := newHostedFixture(t, 5*time.Second)
			_ = fx.h.Close()
			fx.onRunning = func() {
				calls.Add(1)
				if armed.Load() {
					boom()
				}
			}
			fx.open(5 * time.Second)
			fx.quickJobs()
			for i := 0; i < HostedMaxJobs; i++ {
				fx.launchFinished()
			}
			fx.store.mu.Lock()
			if where == "delete" {
				fx.store.duringDelete = func([]string) { boom() }
			} else {
				// Only the prune fails its delete, so the claim's release
				// is what the retry needs.
				fx.store.failDelete = true
			}
			fx.store.mu.Unlock()
			// Each finished job's admission, spawn and end have called
			// OnRunning, so the armed panic hits this launch's admission only.
			eventuallyExec(t, "the finished jobs' running updates", func() bool { return calls.Load() == 3*HostedMaxJobs })
			armed.Store(where == "on running")
			func() {
				defer func() { _ = recover() }()
				_, _ = fx.launch(context.Background(), nil, HostedLaunch{})
			}()
			armed.Store(false)
			fx.h.mu.Lock()
			claimed := 0
			for _, tomb := range fx.h.deleting {
				if tomb.claim != 0 {
					claimed++
				}
			}
			fx.h.mu.Unlock()
			if claimed != 0 {
				t.Fatalf("%d tombstones stay claimed after the recovered panic", claimed)
			}
			fx.store.mu.Lock()
			fx.store.duringDelete, fx.store.failDelete = nil, false
			fx.store.mu.Unlock()
			fx.launchFinished()
			fx.h.mu.Lock()
			pending := len(fx.h.deleting)
			fx.h.mu.Unlock()
			if pending != 0 {
				t.Fatalf("%d deletes pending after the retry", pending)
			}
		})
	}
}

// TestHostedIncompleteCensusKeepsLivingJobFenced: a replacement daemon whose
// startup scan could not read every holder registration does not store a
// job lost on the listing's word. The job stays running (so the owner's
// close and the worktree guards still see it) until a complete scan, which
// then ends it from its exit copy or, with none, as lost; a holder that
// the later scan lists is replayed and its job lives on.
func TestHostedIncompleteCensusKeepsLivingJobFenced(t *testing.T) {

	fx := newFastRetireFixture(t, 10*time.Millisecond)
	unreadable, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "unreadable"})
	copied, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "copied"})
	vanished, _ := fx.launch(context.Background(), nil, HostedLaunch{Display: "vanished"})
	_ = fx.h.Close()

	code := 3
	raw, _ := json.Marshal(wsholder.Exit{ExitCode: &code, Reason: "child_exited", StoppedAt: time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(filepath.Join(fx.exitDir, copied.ID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// The scan cannot read two holders' registrations: both are omitted
	// from the listing, though the first one's process still runs.
	fx.holders.mu.Lock()
	session := fx.holders.live[unreadable.HolderID]
	delete(fx.holders.live, unreadable.HolderID)
	delete(fx.holders.live, copied.HolderID)
	delete(fx.holders.live, vanished.HolderID)
	fx.holders.incomplete = true
	fx.holders.mu.Unlock()

	fx.open(10 * time.Millisecond)
	if a, _ := fx.h.Get("lead", unreadable.ID); a.State != HostedRunning {
		t.Fatalf("job omitted by an incomplete census = %+v, want running", a)
	}
	if a, _ := fx.h.Get("lead", vanished.ID); a.State != HostedRunning {
		t.Fatalf("job omitted by an incomplete census, no copy = %+v, want running", a)
	}
	if a, _ := fx.h.Get("lead", copied.ID); a.State != HostedExited || a.ExitCode == nil || *a.ExitCode != 3 {
		t.Fatalf("job with an exit copy = %+v, want its status", a)
	}
	if n := fx.h.RunningOwners()["lead"]; n != 2 {
		t.Fatalf("running jobs counted for the owner = %d, want 2 (the close fence)", n)
	}
	if n := fx.h.LiveIn(fx.exitDir); n != 2 {
		t.Fatalf("live jobs in the worktree = %d, want 2 (the worktree fence)", n)
	}
	row, _ := fx.store.row(unreadable.ID)
	if row.Terminal() {
		t.Fatalf("stored row = %+v, want it left running", row)
	}

	// A later complete scan lists the first holder again; the second is gone.
	fx.holders.mu.Lock()
	fx.holders.onScan = func() {
		fx.holders.mu.Lock()
		fx.holders.live[unreadable.HolderID] = session
		fx.holders.incomplete = false
		fx.holders.mu.Unlock()
	}
	fx.holders.mu.Unlock()
	eventuallyExec(t, "the unlisted job's reconcile", func() bool {
		a, _ := fx.h.Get("lead", vanished.ID)
		return a.State == HostedLost
	})
	if a, _ := fx.h.Get("lead", unreadable.ID); a.State != HostedRunning {
		t.Fatalf("job whose holder the complete scan lists = %+v, want running", a)
	}
	fx.holders.mu.Lock()
	replays := slices.Clone(fx.holders.replays)
	fx.holders.mu.Unlock()
	if !slices.Equal(replays, []string{unreadable.HolderID}) {
		t.Fatalf("replayed holders = %q, want the relisted job's", replays)
	}
	if n := fx.h.RunningOwners()["lead"]; n != 1 {
		t.Fatalf("running jobs after reconcile = %d, want 1", n)
	}
}
