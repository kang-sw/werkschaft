package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wslive"
)

// fakeEphemeralBackend records every StartSpec and constructs fakeEphemeralProcess instances.
type fakeEphemeralBackend struct {
	mu       sync.Mutex
	specs    []StartSpec
	procs    []*fakeEphemeralProcess
	newProc  func(spec StartSpec, idx int) *fakeEphemeralProcess
	startErr error
}

func (b *fakeEphemeralBackend) Start(_ context.Context, spec StartSpec) (Process, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.specs = append(b.specs, spec)
	if b.startErr != nil {
		return nil, b.startErr
	}
	idx := len(b.procs) + 1
	var p *fakeEphemeralProcess
	if b.newProc != nil {
		p = b.newProc(spec, idx)
	} else {
		p = newFakeEphemeralProcess(idx, true)
	}
	b.procs = append(b.procs, p)
	return p, nil
}

func (b *fakeEphemeralBackend) startCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.specs)
}

type fakeEphemeralProcess struct {
	id           int
	supportReset bool
	resetErr     error
	events       chan Event
	mu           sync.Mutex
	sends        []string
	resets       int
	interrupts   int
	closed       bool
	onSend       func(p *fakeEphemeralProcess, text string)
	onReset      func(p *fakeEphemeralProcess) error
	closeOnce    sync.Once
}

func newFakeEphemeralProcess(id int, supportReset bool) *fakeEphemeralProcess {
	p := &fakeEphemeralProcess{
		id:           id,
		supportReset: supportReset,
		events:       make(chan Event, 64),
	}
	p.onSend = func(fp *fakeEphemeralProcess, text string) {
		fp.events <- Event{
			Live: &wslive.Event{Kind: wslive.KindReply, Text: fmt.Sprintf("p%d:%s", fp.id, text), Final: true},
		}
		fp.events <- Event{TurnEnd: true}
	}
	return p
}

func (p *fakeEphemeralProcess) Events() <-chan Event { return p.events }

func (p *fakeEphemeralProcess) Send(text string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("closed")
	}
	p.sends = append(p.sends, text)
	fn := p.onSend
	p.mu.Unlock()
	if fn != nil {
		go fn(p, text)
	}
	return nil
}

func (p *fakeEphemeralProcess) Interrupt() error {
	p.mu.Lock()
	p.interrupts++
	p.mu.Unlock()
	return nil
}

func (p *fakeEphemeralProcess) SetModel(string) error  { return nil }
func (p *fakeEphemeralProcess) SetEffort(string) error { return nil }

func (p *fakeEphemeralProcess) Reset(ctx context.Context) error {
	if !p.supportReset {
		return ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.resets++
	err := p.resetErr
	fn := p.onReset
	p.mu.Unlock()
	if fn != nil {
		return fn(p)
	}
	return err
}

func (p *fakeEphemeralProcess) processControls() processControls {
	c := processControls{Process: p}
	if p.supportReset {
		c.reset = p
	}
	return c
}

func (p *fakeEphemeralProcess) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		close(p.events)
	})
	return nil
}

func TestEphemeralPoolExactKeyingAndZeroSideEffects(t *testing.T) {
	claudeFake := &fakeEphemeralBackend{}
	codexFake := &fakeEphemeralBackend{}
	scratch := t.TempDir()

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends: map[string]Backend{
			BackendClaude: claudeFake,
			BackendCodex:  codexFake,
		},
		ScratchRoot:   scratch,
		SweepInterval: -1,
	})
	defer pool.Close()

	// Call 1 & 2 on identical (claude, haiku, low) -> reuse same slot/process.
	spec1 := AcquireSpec{Harness: BackendClaude, Model: "haiku", Effort: "low", Mode: BoundedRotation, SystemPrompt: "be concise"}
	r1, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec1, Prompt: "hi"})
	if err != nil || r1 != "p1:hi" {
		t.Fatalf("call 1 = (%q, %v), want p1:hi", r1, err)
	}
	r2, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec1, Prompt: "again"})
	if err != nil || r2 != "p1:again" {
		t.Fatalf("call 2 = (%q, %v), want p1:again", r2, err)
	}
	if claudeFake.startCount() != 1 {
		t.Fatalf("claude starts = %d, want 1", claudeFake.startCount())
	}

	// Different effort -> distinct slot, never reuses (claude, haiku, low).
	spec2 := spec1
	spec2.Effort = "high"
	r3, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec2, Prompt: "deep"})
	if err != nil || r3 != "p2:deep" {
		t.Fatalf("different effort = (%q, %v), want p2:deep", r3, err)
	}

	// Different model -> distinct slot.
	spec3 := spec1
	spec3.Model = "sonnet"
	r4, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec3, Prompt: "sonnet"})
	if err != nil || r4 != "p3:sonnet" {
		t.Fatalf("different model = (%q, %v), want p3:sonnet", r4, err)
	}

	// Different harness -> distinct slot.
	spec4 := AcquireSpec{Harness: BackendCodex, Model: "o3", Effort: "low", Mode: PureOneShot}
	r5, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec4, Prompt: "codex"})
	if err != nil || r5 != "p1:codex" {
		t.Fatalf("different harness = (%q, %v), want p1:codex", r5, err)
	}

	if pool.SlotCount() != 4 {
		t.Fatalf("SlotCount = %d, want 4", pool.SlotCount())
	}

	// Verify StartSpec has zero Runner/holder/MCP/audit/session-dir plumbing and uses pool scratch cwd.
	spec := claudeFake.specs[0]
	if !spec.Ephemeral {
		t.Fatal("StartSpec.Ephemeral = false, want true")
	}
	if spec.Dir != "" || spec.Holder != nil || spec.MCPSession != nil || spec.WSMCP != nil || spec.Lead != nil || spec.audit != nil {
		t.Fatalf("StartSpec leaked session plumbing: %+v", spec)
	}
	if spec.PermissionLevel != PermissionReadOnly {
		t.Fatalf("PermissionLevel = %q, want %q", spec.PermissionLevel, PermissionReadOnly)
	}
	if spec.SystemPrompt != "be concise" {
		t.Fatalf("SystemPrompt = %q, want %q", spec.SystemPrompt, "be concise")
	}
	if !strings.HasPrefix(spec.Cwd, scratch) || spec.Root != spec.Cwd {
		t.Fatalf("Cwd/Root = (%q, %q), want scratch under %q", spec.Cwd, spec.Root, scratch)
	}
}

func TestEphemeralPoolModesPureOneShotAndBoundedRotation(t *testing.T) {
	var tokenReport atomic.Int64
	fake := &fakeEphemeralBackend{
		newProc: func(_ StartSpec, idx int) *fakeEphemeralProcess {
			p := newFakeEphemeralProcess(idx, true)
			p.onSend = func(fp *fakeEphemeralProcess, text string) {
				if tok := tokenReport.Load(); tok > 0 {
					v := tok
					fp.events <- Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &v}}}
				}
				fp.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: text, Final: true}}
				fp.events <- Event{TurnEnd: true}
			}
			return p
		},
	}

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: fake},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	// PureOneShot: 3 calls on same key -> 1 process, 2 resets (before call 2 and call 3).
	oneShot := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: PureOneShot}
	for i := 0; i < 3; i++ {
		if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: oneShot, Prompt: fmt.Sprintf("q%d", i)}); err != nil {
			t.Fatalf("oneShot %d: %v", i, err)
		}
	}
	if fake.startCount() != 1 || fake.procs[0].resets != 2 {
		t.Fatalf("PureOneShot starts=%d resets=%d, want 1 and 2", fake.startCount(), fake.procs[0].resets)
	}

	// BoundedRotation by MaxTurns=2: 3 calls -> 0 resets on call 1-2, 1 reset before call 3.
	bounded := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: BoundedRotation, MaxTurns: 2}
	for i := 0; i < 3; i++ {
		if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: bounded, Prompt: fmt.Sprintf("b%d", i)}); err != nil {
			t.Fatalf("bounded %d: %v", i, err)
		}
	}
	if fake.startCount() != 2 || fake.procs[1].resets != 1 {
		t.Fatalf("BoundedRotation MaxTurns starts=%d resets=%d, want 2 and 1", fake.startCount(), fake.procs[1].resets)
	}

	// BoundedRotation by MaxTokens=500: report 600 tokens on turn 1 -> turn 2 triggers Reset even though MaxTurns=10.
	tokenReport.Store(600)
	tokBounded := AcquireSpec{Harness: BackendClaude, Model: "sonnet", Mode: BoundedRotation, MaxTurns: 10, MaxTokens: 500}
	if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: tokBounded, Prompt: "t1"}); err != nil {
		t.Fatalf("tokBounded 1: %v", err)
	}
	tokenReport.Store(100)
	if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: tokBounded, Prompt: "t2"}); err != nil {
		t.Fatalf("tokBounded 2: %v", err)
	}
	if fake.procs[2].resets != 1 {
		t.Fatalf("BoundedRotation MaxTokens resets=%d, want 1", fake.procs[2].resets)
	}
}

func TestEphemeralPoolResetUnsupportedFallbackRestart(t *testing.T) {
	fake := &fakeEphemeralBackend{
		newProc: func(_ StartSpec, idx int) *fakeEphemeralProcess {
			// Does NOT support Reset -> returns ErrUnsupported.
			return newFakeEphemeralProcess(idx, false)
		},
	}
	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: fake},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: PureOneShot}
	r1, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "first"})
	if err != nil || r1 != "p1:first" {
		t.Fatalf("call 1 = (%q, %v)", r1, err)
	}
	r2, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "second"})
	if err != nil || r2 != "p2:second" {
		t.Fatalf("call 2 = (%q, %v), want p2:second after restart", r2, err)
	}
	if !fake.procs[0].closed {
		t.Fatal("first process was not closed on ErrUnsupported fallback")
	}
	if fake.startCount() != 2 {
		t.Fatalf("starts = %d, want 2", fake.startCount())
	}
}

func TestEphemeralPoolIdleTTLEviction(t *testing.T) {
	fake := &fakeEphemeralBackend{}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	clock := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: fake},
		ScratchRoot:   t.TempDir(),
		IdleTTL:       5 * time.Minute,
		SweepInterval: -1,
		Now:           clock,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: BoundedRotation}
	if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	advance(4 * time.Minute)
	if evicted := pool.SweepIdle(); evicted != 0 || pool.SlotCount() != 1 {
		t.Fatalf("early sweep evicted=%d count=%d, want 0 and 1", evicted, pool.SlotCount())
	}
	advance(2 * time.Minute)
	if evicted := pool.SweepIdle(); evicted != 1 || pool.SlotCount() != 0 {
		t.Fatalf("idle sweep evicted=%d count=%d, want 1 and 0", evicted, pool.SlotCount())
	}
	if !fake.procs[0].closed {
		t.Fatal("evicted slot process was not closed")
	}
}

func TestEphemeralPoolTimeoutInterruptAndRestart(t *testing.T) {
	firstStarted := make(chan struct{})
	fake := &fakeEphemeralBackend{
		newProc: func(_ StartSpec, idx int) *fakeEphemeralProcess {
			p := newFakeEphemeralProcess(idx, true)
			if idx == 1 {
				p.onSend = func(fp *fakeEphemeralProcess, _ string) {
					close(firstStarted)
					// Stall indefinitely until closed.
				}
			}
			return p
		},
	}
	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: fake},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: BoundedRotation}
	_, err := pool.Execute(context.Background(), EphemeralRequest{
		Acquire: spec,
		Prompt:  "slow",
		Timeout: 40 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err = %v, want DeadlineExceeded", err)
	}
	<-firstStarted

	// Next call on the same key must wait for cleanup and start a clean p2.
	reply, err := pool.Execute(context.Background(), EphemeralRequest{
		Acquire: spec,
		Prompt:  "fast",
		Timeout: time.Second,
	})
	if err != nil || reply != "p2:fast" {
		t.Fatalf("post-timeout call = (%q, %v), want p2:fast", reply, err)
	}
	p1 := fake.procs[0]
	p1.mu.Lock()
	interrupted, closed := p1.interrupts, p1.closed
	p1.mu.Unlock()
	if interrupted == 0 || !closed {
		t.Fatalf("p1 interrupts=%d closed=%v, want >=1 and true", interrupted, closed)
	}
}

func TestEphemeralPoolDefaultTimeoutAndPreSendExpiry(t *testing.T) {
	// Zero DefaultTimeout uses documented DefaultEphemeralTimeout (30s).
	defaultPool := NewEphemeralPool(EphemeralPoolOptions{SweepInterval: -1})
	defer defaultPool.Close()
	if defaultPool.defaultTimeout != DefaultEphemeralTimeout {
		t.Fatalf("defaultTimeout = %v, want %v", defaultPool.defaultTimeout, DefaultEphemeralTimeout)
	}

	// Zero req.Timeout falls back to DefaultTimeout and interrupts a hung process.
	fake := &fakeEphemeralBackend{
		newProc: func(_ StartSpec, idx int) *fakeEphemeralProcess {
			p := newFakeEphemeralProcess(idx, true)
			if idx == 1 {
				p.onSend = func(_ *fakeEphemeralProcess, _ string) {
					// Hang without TurnEnd to verify zero-Timeout fallback bounds the wait.
				}
			}
			return p
		},
	}
	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:       map[string]Backend{BackendClaude: fake},
		ScratchRoot:    t.TempDir(),
		DefaultTimeout: 40 * time.Millisecond,
		SweepInterval:  -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: BoundedRotation}
	_, err := pool.Execute(context.Background(), EphemeralRequest{
		Acquire: spec,
		Prompt:  "hang-with-zero-timeout",
		Timeout: 0,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("zero-Timeout err = %v, want DeadlineExceeded", err)
	}

	// Warm slot recovers on next call.
	reply, err := pool.Execute(context.Background(), EphemeralRequest{
		Acquire: spec,
		Prompt:  "healthy",
		Timeout: time.Second,
	})
	if err != nil || reply != "p2:healthy" {
		t.Fatalf("recovery call = (%q, %v), want p2:healthy", reply, err)
	}

	// A pre-cancelled ctx must not tear down the healthy warm process.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Execute(cancelled, EphemeralRequest{Acquire: spec, Prompt: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call err = %v, want context.Canceled", err)
	}
	if fake.procs[1].closed {
		t.Fatal("pre-send cancelled context closed a healthy warm process")
	}
	reply, err = pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "still-warm", Timeout: time.Second})
	if err != nil || reply != "p2:still-warm" {
		t.Fatalf("post-cancel warm call = (%q, %v), want p2:still-warm", reply, err)
	}
	if fake.startCount() != 2 {
		t.Fatalf("starts = %d, want 2 (no unnecessary restart)", fake.startCount())
	}
}

func TestEphemeralPoolFIFOSerializationSameKey(t *testing.T) {
	var inFlight atomic.Int32
	var maxConcurrent atomic.Int32
	var mu sync.Mutex
	var order []string
	releaseFirst := make(chan struct{})
	firstEntered := make(chan struct{})

	fake := &fakeEphemeralBackend{
		newProc: func(_ StartSpec, idx int) *fakeEphemeralProcess {
			p := newFakeEphemeralProcess(idx, true)
			p.onSend = func(fp *fakeEphemeralProcess, text string) {
				cur := inFlight.Add(1)
				for {
					prev := maxConcurrent.Load()
					if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
						break
					}
				}
				mu.Lock()
				order = append(order, text)
				mu.Unlock()
				if text == "first" {
					close(firstEntered)
					<-releaseFirst
				}
				inFlight.Add(-1)
				fp.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: text, Final: true}}
				fp.events <- Event{TurnEnd: true}
			}
			return p
		},
	}

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: fake},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: BoundedRotation}
	var wg sync.WaitGroup
	errs := make([]error, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0] = pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "first", Timeout: 2 * time.Second})
	}()
	<-firstEntered

	// Queue "second" then "third" strictly in FIFO order while "first" is running.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[1] = pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "second", Timeout: 2 * time.Second})
	}()
	// Wait briefly until "second" has queued onto slot.gate.
	time.Sleep(20 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[2] = pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "third", Timeout: 2 * time.Second})
	}()
	time.Sleep(20 * time.Millisecond)

	close(releaseFirst)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d err: %v", i, err)
		}
	}
	if fake.startCount() != 1 {
		t.Fatalf("same-key concurrent starts = %d, want 1", fake.startCount())
	}
	if maxConcurrent.Load() != 1 {
		t.Fatalf("maxConcurrent on same slot = %d, want 1", maxConcurrent.Load())
	}
	if want := []string{"first", "second", "third"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("FIFO order = %v, want %v", order, want)
	}
}

func TestEphemeralPoolResponseSchemaValidation(t *testing.T) {
	replies := []string{
		"```json\n{\"tier\":\"small\"}\n```",
		`{"tier":"huge"}`,
		"not json at all",
		`{"tier":"medium","extra":true}`,
	}
	var idx atomic.Int32
	fake := &fakeEphemeralBackend{
		newProc: func(_ StartSpec, id int) *fakeEphemeralProcess {
			p := newFakeEphemeralProcess(id, true)
			p.onSend = func(fp *fakeEphemeralProcess, _ string) {
				i := int(idx.Add(1)) - 1
				fp.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: replies[i], Final: true}}
				fp.events <- Event{TurnEnd: true}
			}
			return p
		},
	}

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: fake},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	schema := json.RawMessage(`{
		"type": "object",
		"required": ["tier"],
		"additionalProperties": false,
		"properties": {
			"tier": {"type": "string", "enum": ["small", "medium", "large"]}
		}
	}`)
	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Mode: BoundedRotation}

	// 1. Valid fenced JSON -> passes and returns cleaned JSON.
	got, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "1", ResponseSchema: schema})
	if err != nil || got != `{"tier":"small"}` {
		t.Fatalf("valid JSON = (%q, %v)", got, err)
	}
	// 2. Enum mismatch -> ErrSchemaViolation.
	if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "2", ResponseSchema: schema}); !errors.Is(err, ErrSchemaViolation) {
		t.Fatalf("enum mismatch err = %v, want ErrSchemaViolation", err)
	}
	// 3. Invalid JSON -> ErrInvalidJSONResponse.
	if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "3", ResponseSchema: schema}); !errors.Is(err, ErrInvalidJSONResponse) {
		t.Fatalf("invalid JSON err = %v, want ErrInvalidJSONResponse", err)
	}
	// 4. Additional property rejected -> ErrSchemaViolation.
	if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "4", ResponseSchema: schema}); !errors.Is(err, ErrSchemaViolation) {
		t.Fatalf("additionalProperties err = %v, want ErrSchemaViolation", err)
	}
	// Validation errors should not kill the warm process.
	if fake.startCount() != 1 {
		t.Fatalf("starts = %d, want 1", fake.startCount())
	}
}

func TestEphemeralPoolAgyBoundedCleanupAndEviction(t *testing.T) {
	var createdStateDir string
	fake := &fakeEphemeralBackend{
		newProc: func(spec StartSpec, idx int) *fakeEphemeralProcess {
			createdStateDir = spec.ephemeralStateDir
			p := newFakeEphemeralProcess(idx, true)
			sessSeq := 0
			p.onReset = func(fp *fakeEphemeralProcess) error {
				sessSeq++
				sid := fmt.Sprintf("sess-%d", sessSeq)
				// Simulate agy writing brain/<sid> and <sid>.db artifacts inside --gemini-dir.
				brainDir := filepath.Join(spec.ephemeralStateDir, "antigravity-cli", "brain", sid)
				_ = os.MkdirAll(brainDir, 0o755)
				_ = os.WriteFile(filepath.Join(brainDir, "meta.txt"), []byte(sid), 0o644)
				dbPath := filepath.Join(spec.ephemeralStateDir, "antigravity-cli", sid+".db")
				_ = os.WriteFile(dbPath, []byte("sqlite"), 0o644)
				// Bump mtime monotonically so LRU order is deterministic.
				mt := time.Now().Add(time.Duration(sessSeq) * time.Second)
				_ = os.Chtimes(brainDir, mt, mt)
				_ = os.Chtimes(dbPath, mt, mt)
				fp.events <- Event{SessionID: sid}
				return nil
			}
			return p
		},
	}

	scratch := t.TempDir()
	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:         map[string]Backend{BackendAgy: fake},
		ScratchRoot:      scratch,
		MaxStateSessions: 2,
		SweepInterval:    -1,
	})

	spec := AcquireSpec{Harness: BackendAgy, Model: "flash", Mode: PureOneShot}
	// 5 calls -> 4 resets -> prune triggers at resets 2 and 4 keeping at most 2 (+ active).
	for i := 0; i < 5; i++ {
		if _, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatalf("agy call %d: %v", i, err)
		}
	}
	if createdStateDir == "" {
		t.Fatal("agy StartSpec.ephemeralStateDir was empty")
	}
	brainEntries, err := os.ReadDir(filepath.Join(createdStateDir, "antigravity-cli", "brain"))
	if err != nil {
		t.Fatalf("read brain dir: %v", err)
	}
	// Active sess-4 plus at most MaxStateSessions(2) older entries -> <= 3.
	if len(brainEntries) > 3 {
		t.Fatalf("brain entries after bounded prune = %d, want <= 3", len(brainEntries))
	}

	// Pool Close must remove the entire isolated --gemini-dir and scratch cwd.
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(createdStateDir); !os.IsNotExist(err) {
		t.Fatalf("agy stateDir %q still exists after pool.Close(): err=%v", createdStateDir, err)
	}
}

// fakeWerkResponder captures requests sent by an ephemeral werk.Agent.
type fakeWerkResponder struct {
	mu       sync.Mutex
	requests []werk.Request
}

func (r *fakeWerkResponder) Respond(_ context.Context, req werk.Request, handler werk.StreamHandler) (*werk.Response, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	n := len(r.requests)
	r.mu.Unlock()
	text := fmt.Sprintf("items=%d req=%d", len(req.Input), n)
	if handler.TextDelta != nil {
		handler.TextDelta(fmt.Sprintf("msg-%d", n), text)
	}
	msg, _ := json.Marshal(map[string]any{
		"type": "message",
		"id":   fmt.Sprintf("msg-%d", n),
		"role": "assistant",
		"content": []map[string]any{{
			"type": "output_text",
			"text": text,
		}},
	})
	return &werk.Response{
		ID:     fmt.Sprintf("resp-%d", n),
		Output: []json.RawMessage{msg},
		Usage:  werk.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

type fakeWerkBackend struct {
	responder *fakeWerkResponder
}

func (b fakeWerkBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	return startWerkLoop(ctx, spec, b.responder)
}

func TestWerkEphemeralZeroDiskAndReset(t *testing.T) {
	emptyDir := t.TempDir()
	responder := &fakeWerkResponder{}

	// Directly test werk.Agent with Ephemeral: true -> zero files created in Dir.
	turnEnds := make(chan struct{}, 8)
	agent, err := werk.New(werk.Config{
		Ephemeral:    true,
		Dir:          emptyDir,
		Root:         emptyDir,
		Authority:    werk.AuthorityReadOnly,
		Model:        "test-model",
		Instructions: "ephemeral system prompt",
		Endpoint:     responder,
		WebSearch:    func() werk.WebSearchPolicy { return werk.WebSearchPolicy{Enabled: false} },
		Emit: func(l werk.Line) {
			if l.T == werk.LineTurnEnd {
				turnEnds <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatalf("werk.New(Ephemeral): %v", err)
	}
	defer agent.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmds := make(chan werk.Command, 8)
	done := make(chan error, 1)
	go func() { done <- agent.ServeCommands(ctx, cmds) }()

	cmds <- werk.Command{Type: werk.CmdUser, Text: "turn-1"}
	<-turnEnds
	cmds <- werk.Command{Type: werk.CmdUser, Text: "turn-2"}
	<-turnEnds
	ack := make(chan error, 1)
	cmds <- werk.Command{Type: werk.CmdReset, Ack: ack}
	if err := <-ack; err != nil {
		t.Fatalf("CmdReset: %v", err)
	}
	if len(agent.Transcript()) != 0 {
		t.Fatalf("transcript after Reset = %d items, want 0", len(agent.Transcript()))
	}
	cmds <- werk.Command{Type: werk.CmdUser, Text: "turn-3"}
	<-turnEnds
	close(cmds)
	if err := <-done; err != nil {
		t.Fatalf("ServeCommands: %v", err)
	}
	cancel()

	entries, err := os.ReadDir(emptyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ephemeral werk wrote files to disk: %v", entries)
	}
	// Verify zero tools were advertised (cfg.WS == nil) and Reset cleared context.
	responder.mu.Lock()
	reqs := slices.Clone(responder.requests)
	responder.mu.Unlock()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	for i, req := range reqs {
		if len(req.Tools) != 0 {
			t.Fatalf("req %d leaked tools: %+v", i, req.Tools)
		}
		if !strings.Contains(req.Instructions, "ephemeral system prompt") {
			t.Fatalf("req %d missing inline system prompt: %q", i, req.Instructions)
		}
	}
	if len(reqs[1].Input) <= len(reqs[0].Input) {
		t.Fatalf("turn 2 should carry turn 1 context: req0=%d req1=%d", len(reqs[0].Input), len(reqs[1].Input))
	}
	if len(reqs[2].Input) != len(reqs[0].Input) {
		t.Fatalf("turn 3 after Reset should have clean context: req0=%d req2=%d", len(reqs[0].Input), len(reqs[2].Input))
	}

	// Now verify werk through EphemeralPool (PureOneShot resets between turns, zero disk I/O).
	poolResponder := &fakeWerkResponder{}
	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendWerk: fakeWerkBackend{responder: poolResponder}},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendWerk, Model: "test-model", Mode: PureOneShot, SystemPrompt: "classifier"}
	r1, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "first"})
	if err != nil || r1 != "items=1 req=1" {
		t.Fatalf("pool werk call 1 = (%q, %v)", r1, err)
	}
	r2, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "second"})
	if err != nil || r2 != "items=1 req=2" {
		t.Fatalf("pool werk call 2 = (%q, %v), want items=1 req=2 (reset cleared prior turn)", r2, err)
	}
}

func writeFakeCLIScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script fake CLI test")
	}
	path := filepath.Join(t.TempDir(), "fake-cli.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexEphemeralBackendAndReset(t *testing.T) {
	codexHome := t.TempDir()
	configTOML := `
[mcp_servers.user_http]
url = "http://127.0.0.1:1/nope"

[mcp_servers.user_stdio]
command = "/nonexistent-user-mcp"

[mcp_servers.user_stdio.env]
FOO = "bar"

[mcp_servers."dotted.srv"]
url = "http://127.0.0.1:1/dotted"
`
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(configTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	argvFile := filepath.Join(t.TempDir(), "codex-argv.txt")
	logFile := filepath.Join(t.TempDir(), "codex-in.jsonl")
	script := writeFakeCLIScript(t, fmt.Sprintf(`
printf '%%s\n' "$@" > %q
thread_n=0
turn_n=0
while IFS= read -r line; do
  printf '%%s\n' "$line" >> %q
  case "$line" in
    *'"method":"initialize"'*)
      id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
      printf '{"id":"%%s","result":{"userAgent":"codex/0.170.0"}}\n' "$id"
      ;;
    *'"method":"thread/start"'*)
      thread_n=$((thread_n + 1))
      id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
      printf '{"id":"%%s","result":{"thread":{"id":"th-%%d","cliVersion":"0.170.0","model":"o3"},"approvalPolicy":"never","sandbox":{"type":"readOnly"}}}\n' "$id" "$thread_n"
      ;;
    *'"method":"thread/unsubscribe"'*)
      id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
      printf '{"id":"%%s","result":{"status":"unsubscribed"}}\n' "$id"
      ;;
    *'"method":"turn/start"'*)
      turn_n=$((turn_n + 1))
      id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
      tid=$(printf '%%s' "$line" | sed -n 's/.*"threadId":"\([^"]*\)".*/\1/p')
      printf '{"id":"%%s","result":{"turn":{"id":"tu-%%d"}}}\n' "$id" "$turn_n"
      printf '{"method":"turn/started","params":{"threadId":"%%s","turn":{"id":"tu-%%d"}}}\n' "$tid" "$turn_n"
      printf '{"method":"item/completed","params":{"threadId":"%%s","turnId":"tu-%%d","item":{"id":"m-%%d","type":"agentMessage","text":"reply-%%s"}}}\n' "$tid" "$turn_n" "$turn_n" "$tid"
      printf '{"method":"turn/completed","params":{"threadId":"%%s","turn":{"id":"tu-%%d","status":"completed"}}}\n' "$tid" "$turn_n"
      ;;
  esac
done
`, argvFile, logFile))

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendCodex: CodexBackend{Command: script}},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendCodex, Model: "o3", Effort: "low", Mode: PureOneShot, SystemPrompt: "json only"}
	r1, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "one", Timeout: 5 * time.Second})
	if err != nil || r1 != "reply-th-1" {
		t.Fatalf("codex call 1 = (%q, %v), want reply-th-1", r1, err)
	}
	r2, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "two", Timeout: 5 * time.Second})
	if err != nil || r2 != "reply-th-2" {
		t.Fatalf("codex call 2 = (%q, %v), want reply-th-2", r2, err)
	}

	argvRaw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argvRaw)), "\n")
	for _, want := range []string{
		"app-server",
		"features.apps=false",
		"features.plugins=false",
		"mcp_servers.user_http.enabled=false",
		"mcp_servers.user_stdio.enabled=false",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("codex argv missing %q: %v", want, args)
		}
	}

	raw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var initCount, threadStartCount, unsubCount int
	for _, line := range lines {
		var msg map[string]any
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		switch msg["method"] {
		case "initialize":
			initCount++
		case "thread/unsubscribe":
			unsubCount++
			params, _ := msg["params"].(map[string]any)
			if params["threadId"] != "th-1" {
				t.Fatalf("thread/unsubscribe threadId = %v, want th-1", params["threadId"])
			}
		case "thread/start":
			threadStartCount++
			params, _ := msg["params"].(map[string]any)
			if params["ephemeral"] != true {
				t.Fatalf("thread/start missing ephemeral=true: %v", params)
			}
			if params["sandbox"] != "read-only" || params["approvalPolicy"] != "never" {
				t.Fatalf("thread/start confinement = (%v, %v), want (read-only, never)", params["sandbox"], params["approvalPolicy"])
			}
			if params["developerInstructions"] != "json only" {
				t.Fatalf("developerInstructions = %v, want json only", params["developerInstructions"])
			}
			cfg, _ := params["config"].(map[string]any)
			feats, _ := cfg["features"].(map[string]any)
			if feats["shell_tool"] != false || feats["apps"] != false || feats["plugins"] != false {
				t.Fatalf("features = %v, want shell_tool/apps/plugins all false", feats)
			}
			mcps, _ := cfg["mcp_servers"].(map[string]any)
			for _, srv := range []string{"ws", "wsflow", "user_http", "user_stdio", "dotted.srv"} {
				entry, _ := mcps[srv].(map[string]any)
				if entry["enabled"] != false {
					t.Fatalf("mcp_servers[%q] = %v, want enabled=false", srv, entry)
				}
			}
		}
	}
	if initCount != 1 || threadStartCount != 2 || unsubCount != 1 {
		t.Fatalf("codex init=%d thread/start=%d thread/unsubscribe=%d, want 1, 2, 1", initCount, threadStartCount, unsubCount)
	}
}

func TestClaudeEphemeralBackendAndReset(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "claude-argv.txt")
	inFile := filepath.Join(t.TempDir(), "claude-in.jsonl")
	script := writeFakeCLIScript(t, fmt.Sprintf(`
printf '%%s\n' "$@" > %q
turn=0
while IFS= read -r line; do
  printf '%%s\n' "$line" >> %q
  case "$line" in
    *'"content":"/clear"'*)
      printf '{"type":"system","subtype":"init","session_id":"s-clear","model":"haiku","tools":[],"mcp_servers":[],"plugins":[],"permissionMode":"dontAsk","slash_commands":["clear","compact"]}\n'
      printf '{"type":"result","subtype":"success","is_error":false,"local_command":"clear","duration_api_ms":0,"result":""}\n'
      ;;
    *'"type":"user"'*)
      turn=$((turn + 1))
      printf '{"type":"system","subtype":"init","session_id":"s-1","model":"haiku","tools":[],"mcp_servers":[],"plugins":[],"permissionMode":"dontAsk","slash_commands":["clear","compact"]}\n'
      printf '{"type":"assistant","message":{"id":"m-%%d","model":"haiku","content":[{"type":"text","text":"claude-%%d"}]}}\n' "$turn" "$turn"
      printf '{"type":"result","subtype":"success","is_error":false,"result":"claude-%%d"}\n' "$turn"
      ;;
  esac
done
`, argvFile, inFile))

	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendClaude: ClaudeBackend{Command: script}},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendClaude, Model: "haiku", Effort: "low", Mode: PureOneShot, SystemPrompt: "brief"}
	r1, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "p1", Timeout: 5 * time.Second})
	if err != nil || r1 != "claude-1" {
		t.Fatalf("claude call 1 = (%q, %v)", r1, err)
	}
	r2, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "p2", Timeout: 5 * time.Second})
	if err != nil || r2 != "claude-2" {
		t.Fatalf("claude call 2 = (%q, %v)", r2, err)
	}

	argvRaw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argvRaw)), "\n")
	for _, want := range []string{"--no-session-persistence", "--strict-mcp-config", "--disallowedTools", "Skill", "--append-system-prompt", "brief"} {
		if !slices.Contains(args, want) {
			t.Fatalf("claude argv missing %q: %v", want, args)
		}
	}
	if slices.Contains(args, "--disable-slash-commands") {
		t.Fatalf("claude ephemeral argv must keep slash commands enabled for /clear: %v", args)
	}
	// Verify --tools "" pair.
	toolsIdx := slices.Index(args, "--tools")
	if toolsIdx < 0 || toolsIdx+1 >= len(args) || args[toolsIdx+1] != "" {
		t.Fatalf("claude argv missing --tools \"\": %v", args)
	}

	inRaw, err := os.ReadFile(inFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inRaw), `"/clear"`) {
		t.Fatalf("expected /clear reset message in claude stdin, got:\n%s", string(inRaw))
	}
}

func TestAgyEphemeralBackendAutoDenyResetAndIsolation(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "agy-argv.txt")
	inFile := filepath.Join(t.TempDir(), "agy-in.jsonl")
	script := writeFakeCLIScript(t, fmt.Sprintf(`
printf '%%s\n' "$@" > %q
sess=0
while IFS= read -r line; do
  printf '%%s\n' "$line" >> %q
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"protocolVersion":1,"agentInfo":{"version":"1.5.15"}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      sess=$((sess + 1))
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"sessionId":"agy-s%%d","modes":{"currentModeId":"default","availableModes":[{"id":"default"}]},"configOptions":[{"id":"model","currentValue":"flash","options":[{"value":"flash"}]},{"id":"thought_level","currentValue":"low","options":[{"value":"low"}]}]}}\n' "$id" "$sess"
      ;;
    *'"method":"session/close"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{}}\n' "$id"
      ;;
    *'"method":"session/set_config_option"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"configOptions":[{"id":"model","currentValue":"flash","options":[{"value":"flash"}]},{"id":"thought_level","currentValue":"low","options":[{"value":"low"}]}]}}\n' "$id"
      ;;
    *'"method":"session/prompt"'*)
      sid=$(printf '%%s' "$line" | sed -n 's/.*"sessionId":"\([^"]*\)".*/\1/p')
      # On turn 1, emit an ACP permission request and wait for the client's auto-deny before answering.
      if [ "$sess" = "1" ]; then
        printf '{"jsonrpc":"2.0","id":"perm-1","method":"session/request_permission","params":{"toolCall":{"toolCallId":"tc-1","name":"run_command","title":"run_command"},"options":[{"optionId":"opt-allow","kind":"allow_once"},{"optionId":"opt-deny","kind":"reject_once"}]}}\n'
        IFS= read -r perm_reply
        printf '%%s\n' "$perm_reply" >> %q
      fi
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"%%s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"agy-reply-%%s"}}}}\n' "$sid" "$sid"
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"stopReason":"end_turn"}}\n' "$id"
      ;;
  esac
done
`, argvFile, inFile, inFile))

	scratch := t.TempDir()
	pool := NewEphemeralPool(EphemeralPoolOptions{
		Backends:      map[string]Backend{BackendAgy: AgyBackend{Command: script}},
		ScratchRoot:   scratch,
		SweepInterval: -1,
	})
	defer pool.Close()

	spec := AcquireSpec{Harness: BackendAgy, Model: "flash", Effort: "low", Mode: PureOneShot, SystemPrompt: "strict"}
	r1, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "try cmd", Timeout: 5 * time.Second})
	if err != nil || r1 != "agy-reply-agy-s1" {
		t.Fatalf("agy call 1 = (%q, %v), want agy-reply-agy-s1", r1, err)
	}
	r2, err := pool.Execute(context.Background(), EphemeralRequest{Acquire: spec, Prompt: "next", Timeout: 5 * time.Second})
	if err != nil || r2 != "agy-reply-agy-s2" {
		t.Fatalf("agy call 2 = (%q, %v), want agy-reply-agy-s2", r2, err)
	}

	argvRaw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argvRaw)), "\n")
	if !slices.Contains(args, "acp") {
		t.Fatalf("agy argv missing acp: %v", args)
	}
	if slices.Contains(args, "--process-per-session") {
		t.Fatalf("agy ephemeral argv must not pass --process-per-session: %v", args)
	}
	gIdx := slices.Index(args, "--gemini-dir")
	if gIdx < 0 || gIdx+1 >= len(args) || !strings.HasPrefix(args[gIdx+1], scratch) {
		t.Fatalf("agy argv missing isolated --gemini-dir under %q: %v", scratch, args)
	}
	geminiDir := args[gIdx+1]

	inRaw, err := os.ReadFile(inFile)
	if err != nil {
		t.Fatal(err)
	}
	inText := string(inRaw)
	if !strings.Contains(inText, `"mcpServers":[]`) {
		t.Fatalf("agy session/new must pass empty mcpServers: %s", inText)
	}
	if !strings.Contains(inText, `"optionId":"opt-deny"`) {
		t.Fatalf("agy ephemeral session must auto-deny session/request_permission with reject_once: %s", inText)
	}
	if !strings.Contains(inText, `"method":"session/close"`) {
		t.Fatalf("agy Reset must call session/close on prior session: %s", inText)
	}

	// Closing the pool removes the isolated --gemini-dir.
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(geminiDir); !os.IsNotExist(err) {
		t.Fatalf("agy geminiDir %q remained after pool.Close(): %v", geminiDir, err)
	}
}
