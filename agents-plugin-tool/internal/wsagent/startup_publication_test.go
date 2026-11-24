package wsagent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

type startupGateHolders struct {
	sinks []wsholder.Sink
}

func (h *startupGateHolders) AddSink(s wsholder.Sink) { h.sinks = append(h.sinks, s) }
func (h *startupGateHolders) Scan()                   {}
func (h *startupGateHolders) List() wsholder.Listing  { return wsholder.Listing{} }
func (h *startupGateHolders) LaunchWith(wsholder.LaunchSpec) (string, error) {
	return "", nil
}
func (h *startupGateHolders) Spawn(string, wsholder.SpawnSpec) (wsholder.ChildStatus, error) {
	return wsholder.ChildStatus{}, nil
}
func (h *startupGateHolders) Input(string, []byte, bool) error          { return nil }
func (h *startupGateHolders) Status(string) (wsholder.AttachInfo, error) { return wsholder.AttachInfo{}, nil }
func (h *startupGateHolders) Stop(string) error                         { return nil }

type startupTestKeys struct{}

func (startupTestKeys) Mint(string) (string, error)    { return "minted-key", nil }
func (startupTestKeys) Ensure(string, string) error    { return nil }

type startupTestBackend struct {
	onStart func(spec StartSpec) (Process, error)
}

func (b *startupTestBackend) Start(_ context.Context, spec StartSpec) (Process, error) {
	return b.onStart(spec)
}

type startupTestProc struct {
	events chan Event
	once   sync.Once
}

func newStartupTestProc() *startupTestProc {
	return &startupTestProc{
		events: make(chan Event, 8),
	}
}

func (p *startupTestProc) Send(string) error       { return nil }
func (p *startupTestProc) Interrupt() error        { return nil }
func (p *startupTestProc) SetModel(string) error   { return ErrUnsupported }
func (p *startupTestProc) SetEffort(string) error  { return ErrUnsupported }
func (p *startupTestProc) Events() <-chan Event    { return p.events }
func (p *startupTestProc) Close() error {
	p.once.Do(func() {
		close(p.events)
	})
	return nil
}

// TestStartForPublicationDefersProcesslessKickUntilServing ensures that
// StartForPublication never spawns a new harness process for a processless
// conversation with pending inbox or mail before rejoined closes and serving
// opens (preventing a self-deadlock against the unpublished /mcp/session), and
// kicks those conversations promptly once serving closes.
func TestStartForPublicationDefersProcesslessKickUntilServing(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339)

	// Conversation 1: processless with a held inbox item.
	idInbox := "abcdef0123456789"
	dirInbox := wsdashboard.SessionDir(dir, idInbox)
	if err := os.MkdirAll(dirInbox, 0o755); err != nil {
		t.Fatal(err)
	}
	recInbox := Record{
		ReplyID:   idInbox,
		WSKey:     "inbox-conv-key",
		Backend:   BackendCodex,
		Profile:   "chat",
		Root:      root,
		CreatedAt: now,
	}
	if err := writeRecord(dirInbox, recInbox); err != nil {
		t.Fatal(err)
	}
	if _, _, err := wsinbox.Send(wsinbox.Dir(dirInbox), wsinbox.Entry{Parts: []string{"resume after restart"}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Conversation 2: processless with persisted pending mail.
	idMail := "fedcba9876543210"
	dirMail := wsdashboard.SessionDir(dir, idMail)
	if err := os.MkdirAll(dirMail, 0o755); err != nil {
		t.Fatal(err)
	}
	recMail := Record{
		ReplyID:   idMail,
		WSKey:     "mail-conv-key",
		Backend:   BackendCodex,
		Profile:   "chat",
		Root:      root,
		CreatedAt: now,
		Mail:      []string{"mail waiting while daemon restarted"},
	}
	if err := writeRecord(dirMail, recMail); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	startedIDs := map[string]int{}
	startedCh := make(chan string, 8)
	var preServingStarts atomic.Int32
	serving := make(chan struct{})

	r := NewRunner(Options{
		Dir:     dir,
		Keys:    startupTestKeys{},
		Holders: &startupGateHolders{},
		Log:     io.Discard,
		Backends: map[string]BackendRegistration{
			BackendCodex: {
				Backend: &startupTestBackend{
					onStart: func(spec StartSpec) (Process, error) {
						select {
						case <-serving:
						default:
							preServingStarts.Add(1)
						}
						id := filepath.Base(spec.Dir)
						mu.Lock()
						startedIDs[id]++
						mu.Unlock()
						startedCh <- id
						return newStartupTestProc(), nil
					},
				},
			},
		},
	})
	r.fixedProfile = &ResolvedProfile{Name: "chat", Definition: Profile{Backend: BackendCodex}}
	defer r.Close()

	// StartForPublication must return promptly (closing rejoined) without
	// invoking backend Start before serving is closed.
	done := make(chan struct{})
	go func() {
		r.StartForPublication(nil, serving)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartForPublication blocked waiting on processless kick")
	}

	// Give conversation loops a brief window to confirm no pre-serving Start fires.
	time.Sleep(100 * time.Millisecond)
	if got := preServingStarts.Load(); got != 0 {
		t.Fatalf("backend Start called %d time(s) before serving closed", got)
	}
	select {
	case id := <-startedCh:
		t.Fatalf("conversation %s started before serving closed", id)
	default:
	}

	// Closing serving must wake and start both deferred conversations.
	close(serving)
	want := map[string]bool{idInbox: false, idMail: false}
	deadline := time.After(5 * time.Second)
	for !(want[idInbox] && want[idMail]) {
		select {
		case id := <-startedCh:
			want[id] = true
		case <-deadline:
			t.Fatalf("timed out waiting for deferred kicks after serving closed: got=%v", want)
		}
	}
	if got := preServingStarts.Load(); got != 0 {
		t.Fatalf("preServingStarts = %d, want 0", got)
	}
}

// TestStartupLoadSkipsUnchangedSnapshotRewrite verifies that reopening a
// liveWriter over a current-format snapshot seeds lastSnapshot so boot-time
// writeSnapshot() publishes to RAM without rewriting snapshot.json on disk.
func TestStartupLoadSkipsUnchangedSnapshotRewrite(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		t.Run(map[bool]string{false: "file-only", true: "hosted"}[hosted], func(t *testing.T) {
			dir := t.TempDir()
			now := time.Unix(2000, 0)
			initial := newLiveWriter(dir, "boot-key", BackendCodex, func() time.Time { return now }, io.Discard)
			initial.coverage = func() (string, uint64) { return "", 0 }
			initial.forceSnapshot()

			now = now.Add(5 * time.Second)
			reopened := newLiveWriter(dir, "boot-key", BackendCodex, func() time.Time { return now }, io.Discard)
			reopened.coverage = func() (string, uint64) { return "", 0 }
			var slot *idleSnapshotSlot
			if hosted {
				slot = &idleSnapshotSlot{}
				reopened.slot = slot
			}
			writes := 0
			reopened.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
				writes++
				return writeFileAtomic(path, raw, mode)
			}

			// Startup calls writeSnapshot (and runner_startup calls it a second
			// time after all records load): neither should touch disk.
			reopened.writeSnapshot()
			reopened.writeSnapshot()
			if writes != 0 {
				t.Fatalf("unchanged startup writeSnapshot wrote disk %d time(s), want 0", writes)
			}
			if hosted && slot.snapshot.UpdatedAt != now.UnixMilli() {
				t.Fatalf("hosted startup did not publish snapshot to RAM slot: UpdatedAt=%d, want %d", slot.snapshot.UpdatedAt, now.UnixMilli())
			}

			// A real change still flushes to disk once.
			reopened.setAdapter(BackendClaude)
			if writes != 1 {
				t.Fatalf("changed writeSnapshot wrote disk %d time(s), want 1", writes)
			}
		})
	}
}

// TestHolderChildSettlesOnSyntheticExit verifies that a synthetic exit event
// emitted by wsholder when a holder dies abruptly without a protocol "exit"
// frame finishes the streamProcess and closes its event stream.
func TestHolderChildSettlesOnSyntheticExit(t *testing.T) {
	var table holderTable
	p := newStreamProcess(nil, nil)
	hc := &holderChild{
		id:   "holder-abrupt-1",
		base: "codex",
		proc: p,
	}
	p.child = hc
	table.add("holder-abrupt-1", hc)

	table.HolderEvent("holder-abrupt-1", wsholder.Event{
		Event:  "exit",
		Reason: "unknown",
	})

	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("streamProcess did not exit on synthetic holder exit")
	}

	var gotFatal error
	for ev := range p.events {
		if ev.Fatal != nil {
			gotFatal = ev.Fatal
		}
	}
	if gotFatal == nil || !strings.Contains(gotFatal.Error(), "holder exited (unknown)") {
		t.Fatalf("fatal event = %v, want holder exited (unknown)", gotFatal)
	}
}

// TestKickStoreThenRecheckPreventsMissedWakeupOnServingClose exercises the
// exact TOCTOU window where close(serving) and kickAfterServing() run between
// conv.kick()'s initial !publicationReady() check and kickDeferred.Store(true).
// The store-then-recheck in deferKickIfUnpublished must detect that serving is
// now open and launch the backend instead of stranding the kick.
func TestKickStoreThenRecheckPreventsMissedWakeupOnServingClose(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339)

	id := "1122334455667788"
	sessDir := wsdashboard.SessionDir(dir, id)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := Record{
		ReplyID:   id,
		WSKey:     "recheck-conv-key",
		Backend:   BackendCodex,
		Profile:   "chat",
		Root:      root,
		CreatedAt: now,
	}
	if err := writeRecord(sessDir, rec); err != nil {
		t.Fatal(err)
	}

	serving := make(chan struct{})
	startedCh := make(chan string, 4)
	var armed atomic.Bool
	var hookOnce sync.Once
	var r *Runner
	r = NewRunner(Options{
		Dir:     dir,
		Keys:    startupTestKeys{},
		Holders: &startupGateHolders{},
		Log:     io.Discard,
		Backends: map[string]BackendRegistration{
			BackendCodex: {
				Backend: &startupTestBackend{
					onStart: func(spec StartSpec) (Process, error) {
						startedCh <- filepath.Base(spec.Dir)
						return newStartupTestProc(), nil
					},
				},
			},
		},
	})
	r.fixedProfile = &ResolvedProfile{Name: "chat", Definition: Profile{Backend: BackendCodex}}
	r.onDeferKick = func() {
		if !armed.Load() {
			return
		}
		hookOnce.Do(func() {
			// Simulate the worst-case TOCTOU interleaving: serving closes and
			// kickAfterServing sweeps (seeing kickDeferred == false) right
			// before conv.kick() stores kickDeferred = true.
			close(serving)
			r.kickAfterServing()
		})
	}
	defer r.Close()

	r.StartForPublication(nil, serving)

	// Arm the hook and enqueue a message so conv.kick() enters
	// deferKickIfUnpublished() while serving is still open.
	armed.Store(true)
	if _, _, err := wsinbox.Send(wsinbox.Dir(sessDir), wsinbox.Entry{Parts: []string{"wake across boundary"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	c := r.convs[id]
	r.mu.Unlock()
	if c == nil {
		t.Fatal("conversation not loaded")
	}
	c.signalInbox()

	select {
	case got := <-startedCh:
		if got != id {
			t.Fatalf("started conversation = %s, want %s", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missed wakeup: kick stranded after serving closed between check and Store(true)")
	}
}
