package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// AcquireMode selects how an EphemeralPool slot manages conversation context
// across consecutive calls on the same key.
type AcquireMode int

const (
	// PureOneShot resets conversation context before every subsequent call on
	// the slot so each invocation runs with an uncontaminated context window.
	PureOneShot AcquireMode = iota
	// BoundedRotation keeps warm context across up to MaxTurns / MaxTokens
	// before resetting the slot.
	BoundedRotation
)

// Documented package defaults when callers leave AcquireSpec or pool option
// fields zero.
const (
	DefaultEphemeralIdleTTL          = 5 * time.Minute
	DefaultEphemeralMaxTurns         = 20
	DefaultEphemeralMaxTokens        = int64(0) // 0 = no token cap
	DefaultEphemeralTimeout          = 30 * time.Second
	DefaultEphemeralMaxStateSessions = 4
)

var (
	// ErrEphemeralPoolClosed is returned when Execute or Reset is called after Close.
	ErrEphemeralPoolClosed = errors.New("ephemeral pool is closed")
	// ErrInvalidJSONResponse reports a non-JSON reply when ResponseSchema was requested.
	ErrInvalidJSONResponse = errors.New("ephemeral reply is not valid JSON")
	// ErrSchemaViolation reports a JSON reply that failed ResponseSchema validation.
	ErrSchemaViolation = errors.New("ephemeral reply violated response schema")
)

// AcquireSpec identifies an exact warm slot and its rotation/idle policy.
type AcquireSpec struct {
	Harness      string
	Model        string
	Effort       string
	Provider     string
	Mode         AcquireMode
	MaxTurns     int           // BoundedRotation rotation cap (0 -> DefaultEphemeralMaxTurns)
	MaxTokens    int64         // BoundedRotation token cap (0 -> DefaultEphemeralMaxTokens)
	SystemPrompt string
	IdleTTL      time.Duration // 0 -> DefaultEphemeralIdleTTL
}

// EphemeralRequest is one lightweight call executed by EphemeralPool.
type EphemeralRequest struct {
	Acquire        AcquireSpec
	Prompt         string
	ResponseSchema json.RawMessage // optional structured JSON output contract
	Timeout        time.Duration   // bounded wait (0 -> DefaultEphemeralTimeout); caller falls back on timeout
}

// EphemeralPoolOptions configures an EphemeralPool. Zero values use package
// defaults and builtin backend registrations.
type EphemeralPoolOptions struct {
	// Backends supplies harness backends by name (e.g. daemon-configured
	// backends with credentials or Command overrides, or fakes in tests).
	// Missing harnesses fall back to builtinRegistration(harness).Backend.
	Backends map[string]Backend
	// ScratchRoot is the parent directory for pool-owned empty scratch cwds
	// and isolated ephemeral state trees. Empty uses os.TempDir().
	ScratchRoot string
	// IdleTTL overrides DefaultEphemeralIdleTTL when positive.
	IdleTTL time.Duration
	// DefaultTimeout overrides DefaultEphemeralTimeout when positive. Applied
	// whenever an EphemeralRequest leaves Timeout <= 0.
	DefaultTimeout time.Duration
	// SweepInterval controls how often idle slots are checked for eviction.
	// Zero derives a sensible interval from IdleTTL; negative disables the
	// background ticker (callers or tests can call SweepIdle explicitly).
	SweepInterval time.Duration
	// MaxStateSessions caps retained session artifacts in a backend's isolated
	// ephemeral state directory before bounded LRU pruning (0 -> DefaultEphemeralMaxStateSessions).
	MaxStateSessions int
	// Now overrides time.Now for deterministic tests.
	Now func() time.Time
}

type ephemeralSlotKey struct {
	harness      string
	model        string
	effort       string
	provider     string
	mode         AcquireMode
	systemPrompt string
}

func slotKeyFor(spec AcquireSpec) ephemeralSlotKey {
	return ephemeralSlotKey{
		harness:      strings.TrimSpace(spec.Harness),
		model:        strings.TrimSpace(spec.Model),
		effort:       strings.TrimSpace(spec.Effort),
		provider:     strings.TrimSpace(spec.Provider),
		mode:         spec.Mode,
		systemPrompt: strings.TrimSpace(spec.SystemPrompt),
	}
}

// fifoLock is a cancellable FIFO mutex so same-key acquisitions serialize in
// arrival order and respect per-call timeouts without opening a second process.
type fifoLock struct {
	mu      sync.Mutex
	locked  bool
	waiters []chan struct{}
}

func (l *fifoLock) acquire(ctx context.Context) error {
	l.mu.Lock()
	if !l.locked && len(l.waiters) == 0 {
		l.locked = true
		l.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	l.waiters = append(l.waiters, ch)
	l.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		l.mu.Lock()
		select {
		case <-ch:
			// Lock was handed to us concurrently with cancellation; pass it on.
			l.unlockLocked()
		default:
			for i, w := range l.waiters {
				if w == ch {
					l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
					break
				}
			}
		}
		l.mu.Unlock()
		return ctx.Err()
	}
}

func (l *fifoLock) release() {
	l.mu.Lock()
	l.unlockLocked()
	l.mu.Unlock()
}

func (l *fifoLock) unlockLocked() {
	if len(l.waiters) > 0 {
		next := l.waiters[0]
		l.waiters = l.waiters[1:]
		close(next)
		return
	}
	l.locked = false
}

type ephemeralSlot struct {
	key  ephemeralSlotKey
	gate fifoLock

	// Fields protected by pool.mu:
	inUse    int
	lastUsed time.Time
	idleTTL  time.Duration

	// Fields owned exclusively by the goroutine holding gate (or by pool
	// eviction/Close once removed from pool.slots with inUse == 0):
	proc        Process
	controls    processControls
	lifecycle   LifecyclePolicy
	statePruner ephemeralStatePruner
	turns       int
	tokens      int64
	resets      int
	sessionID   string
	stateDir    string
}

// EphemeralPool caches warm, non-persistent wsagent.Process slots keyed by
// exact (harness, model, effort, provider, mode, systemPrompt) with lazy start,
// FIFO per-slot serialization, bounded rotation or per-call reset, idle TTL
// eviction, and bounded cleanup of isolated harness state directories.
type EphemeralPool struct {
	backends         map[string]Backend
	scratchRoot      string
	defaultIdleTTL   time.Duration
	defaultTimeout   time.Duration
	maxStateSessions int
	now              func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu         sync.Mutex
	closed     bool
	slots      map[ephemeralSlotKey]*ephemeralSlot
	scratchCwd string
	stopSweep  chan struct{}
	sweepDone  chan struct{}
}

// NewEphemeralPool creates a new EphemeralPool. Call Close on daemon shutdown
// to terminate cached processes and remove temporary scratch/agy directories.
func NewEphemeralPool(opts EphemeralPoolOptions) *EphemeralPool {
	idleTTL := opts.IdleTTL
	if idleTTL <= 0 {
		idleTTL = DefaultEphemeralIdleTTL
	}
	defaultTimeout := opts.DefaultTimeout
	if defaultTimeout <= 0 {
		defaultTimeout = DefaultEphemeralTimeout
	}
	maxState := opts.MaxStateSessions
	if maxState <= 0 {
		maxState = DefaultEphemeralMaxStateSessions
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	backends := make(map[string]Backend, len(opts.Backends))
	for k, v := range opts.Backends {
		if v != nil {
			backends[k] = v
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &EphemeralPool{
		backends:         backends,
		scratchRoot:      opts.ScratchRoot,
		defaultIdleTTL:   idleTTL,
		defaultTimeout:   defaultTimeout,
		maxStateSessions: maxState,
		now:              nowFn,
		ctx:              ctx,
		cancel:           cancel,
		slots:            make(map[ephemeralSlotKey]*ephemeralSlot),
	}
	sweep := opts.SweepInterval
	if sweep == 0 {
		sweep = min(idleTTL/2, 30*time.Second)
		if sweep < time.Second {
			sweep = time.Second
		}
	}
	if sweep > 0 {
		p.stopSweep = make(chan struct{})
		p.sweepDone = make(chan struct{})
		go p.sweepLoop(sweep)
	}
	return p
}

func (p *EphemeralPool) sweepLoop(interval time.Duration) {
	defer close(p.sweepDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.SweepIdle()
		case <-p.stopSweep:
			return
		}
	}
}

func (p *EphemeralPool) backendFor(harness string) (Backend, error) {
	if b, ok := p.backends[harness]; ok && b != nil {
		return b, nil
	}
	if reg := builtinRegistration(harness); reg.Backend != nil {
		return reg.Backend, nil
	}
	return nil, fmt.Errorf("ephemeral pool: unknown harness %q", harness)
}

func (p *EphemeralPool) ensureScratchCwd() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", ErrEphemeralPoolClosed
	}
	if p.scratchCwd != "" {
		return p.scratchCwd, nil
	}
	dir, err := os.MkdirTemp(p.scratchRoot, "ws-ephemeral-cwd-*")
	if err != nil {
		return "", fmt.Errorf("ephemeral pool: create scratch cwd: %w", err)
	}
	p.scratchCwd = dir
	return dir, nil
}

func (p *EphemeralPool) acquireSlotRef(spec AcquireSpec) (*ephemeralSlot, error) {
	key := slotKeyFor(spec)
	if key.harness == "" {
		return nil, errors.New("ephemeral pool: harness is required")
	}
	idleTTL := spec.IdleTTL
	if idleTTL <= 0 {
		idleTTL = p.defaultIdleTTL
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrEphemeralPoolClosed
	}
	slot, ok := p.slots[key]
	if !ok {
		slot = &ephemeralSlot{
			key:      key,
			lastUsed: p.now(),
			idleTTL:  idleTTL,
		}
		p.slots[key] = slot
	} else if spec.IdleTTL > 0 {
		slot.idleTTL = spec.IdleTTL
	}
	slot.inUse++
	p.wg.Add(1)
	return slot, nil
}

func (p *EphemeralPool) releaseSlotRef(slot *ephemeralSlot) {
	p.mu.Lock()
	slot.inUse--
	slot.lastUsed = p.now()
	p.mu.Unlock()
}

// SlotCount reports the number of currently tracked slots in the pool.
func (p *EphemeralPool) SlotCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

// Execute runs req on the exact slot matching req.Acquire, lazily starting the
// backend process on demand, serializing same-key calls FIFO, applying the
// PureOneShot or BoundedRotation reset policy, and optionally validating the
// JSON reply against req.ResponseSchema.
func (p *EphemeralPool) Execute(ctx context.Context, req EphemeralRequest) (string, error) {
	var schema map[string]any
	if len(bytes.TrimSpace(req.ResponseSchema)) > 0 {
		if err := json.Unmarshal(req.ResponseSchema, &schema); err != nil {
			return "", fmt.Errorf("ephemeral pool: invalid response schema: %w", err)
		}
	}
	backend, err := p.backendFor(strings.TrimSpace(req.Acquire.Harness))
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	slot, err := p.acquireSlotRef(req.Acquire)
	if err != nil {
		return "", err
	}

	handedOff := false
	defer func() {
		if !handedOff {
			p.releaseSlotRef(slot)
			p.wg.Done()
		}
	}()

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = p.defaultTimeout
	}
	callCtx, cancel := p.deriveCallContext(ctx, timeout)
	defer cancel()

	if err := slot.gate.acquire(callCtx); err != nil {
		return "", err
	}
	gateReleased := false
	defer func() {
		if !gateReleased && !handedOff {
			slot.gate.release()
		}
	}()

	if slot.proc != nil && slot.shouldReset(req.Acquire) {
		if err := slot.resetOrRestart(callCtx, p.maxStateSessions); err != nil {
			return "", err
		}
	}
	if slot.proc == nil {
		if err := p.startSlot(callCtx, slot, backend); err != nil {
			return "", err
		}
	}
	if err := callCtx.Err(); err != nil {
		// Context expired before Send: the warm process was not touched, so
		// keep it alive for the next caller instead of tearing it down.
		return "", err
	}

	reply, timedOut, err := p.runTurn(callCtx, slot, req.Prompt)
	if timedOut {
		// Transfer gate, slot ref, and wg ownership to an async cleanup so the
		// caller falls back immediately while Interrupt + Close finish before
		// the next queued caller can reuse or restart the slot.
		proc := slot.proc
		stateDir := slot.stateDir
		slot.proc = nil
		slot.controls = processControls{}
		slot.turns = 0
		slot.tokens = 0
		slot.sessionID = ""
		slot.stateDir = ""
		handedOff = true
		gateReleased = true
		go func() {
			defer p.wg.Done()
			defer func() {
				slot.gate.release()
				p.releaseSlotRef(slot)
			}()
			if proc != nil {
				_ = proc.Interrupt()
				_ = proc.Close()
			}
			if stateDir != "" {
				_ = os.RemoveAll(stateDir)
			}
		}()
		return "", err
	}
	if err != nil {
		slot.closeProcess()
		return "", err
	}
	slot.turns++

	if schema != nil {
		cleaned, err := validateResponseJSON(reply, schema)
		if err != nil {
			return "", err
		}
		return cleaned, nil
	}
	return reply, nil
}

// Reset explicitly resets the warm slot matching spec if one is running.
// Returns nil when no slot exists yet or after resetting/closing the slot.
func (p *EphemeralPool) Reset(ctx context.Context, spec AcquireSpec) error {
	if ctx == nil {
		ctx = context.Background()
	}
	key := slotKeyFor(spec)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrEphemeralPoolClosed
	}
	slot, ok := p.slots[key]
	if !ok {
		p.mu.Unlock()
		return nil
	}
	slot.inUse++
	p.wg.Add(1)
	p.mu.Unlock()

	defer func() {
		p.releaseSlotRef(slot)
		p.wg.Done()
	}()

	resetTimeout := time.Duration(0)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		resetTimeout = p.defaultTimeout
	}
	callCtx, cancel := p.deriveCallContext(ctx, resetTimeout)
	defer cancel()
	if err := slot.gate.acquire(callCtx); err != nil {
		return err
	}
	defer slot.gate.release()

	if slot.proc == nil {
		return nil
	}
	return slot.resetOrRestart(callCtx, p.maxStateSessions)
}

func (p *EphemeralPool) deriveCallContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	merged, cancelMerged := context.WithCancel(ctx)
	stop := context.AfterFunc(p.ctx, cancelMerged)
	if timeout > 0 {
		tCtx, cancelTimeout := context.WithTimeout(merged, timeout)
		return tCtx, func() {
			cancelTimeout()
			stop()
			cancelMerged()
		}
	}
	return merged, func() {
		stop()
		cancelMerged()
	}
}

func (s *ephemeralSlot) shouldReset(spec AcquireSpec) bool {
	switch spec.Mode {
	case PureOneShot:
		return s.turns > 0
	case BoundedRotation:
		maxTurns := spec.MaxTurns
		if maxTurns <= 0 {
			maxTurns = DefaultEphemeralMaxTurns
		}
		if s.turns >= maxTurns {
			return true
		}
		maxTokens := spec.MaxTokens
		if maxTokens > 0 && s.tokens >= maxTokens {
			return true
		}
		return false
	default:
		return s.turns > 0
	}
}

func (s *ephemeralSlot) resetOrRestart(ctx context.Context, maxStateSessions int) error {
	if s.proc == nil {
		return nil
	}
	if err := s.controls.resetConversation(ctx); err != nil {
		// On ErrUnsupported or any reset error, close the process so the next
		// step starts a clean replacement for this exact slot.
		s.closeProcess()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	s.turns = 0
	s.tokens = 0
	s.resets++
	// Drain any buffered post-reset session id events without blocking so
	// isolated state-dir pruning preserves the newly opened session id.
	s.drainPendingEvents()
	if s.statePruner != nil && s.stateDir != "" && maxStateSessions > 0 && s.resets%maxStateSessions == 0 {
		s.statePruner.pruneEphemeralStateDir(s.stateDir, s.sessionID, maxStateSessions)
	}
	return nil
}

func (s *ephemeralSlot) drainPendingEvents() {
	if s.proc == nil {
		return
	}
	events := s.proc.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.SessionID != "" {
				s.sessionID = ev.SessionID
			}
		default:
			return
		}
	}
}

func (s *ephemeralSlot) closeProcess() {
	if s.proc != nil {
		_ = s.proc.Close()
		s.proc = nil
	}
	s.controls = processControls{}
	s.turns = 0
	s.tokens = 0
	s.sessionID = ""
	if s.stateDir != "" {
		_ = os.RemoveAll(s.stateDir)
		s.stateDir = ""
	}
}

func (p *EphemeralPool) startSlot(ctx context.Context, slot *ephemeralSlot, backend Backend) error {
	reg := builtinRegistration(slot.key.harness)
	lifecycle := reg.Lifecycle
	slot.lifecycle = lifecycle
	if pruner, ok := backend.(ephemeralStatePruner); ok {
		slot.statePruner = pruner
	} else if pruner, ok := reg.Backend.(ephemeralStatePruner); ok {
		slot.statePruner = pruner
	} else {
		slot.statePruner = nil
	}
	spec := StartSpec{
		Ephemeral:       true,
		Model:           slot.key.model,
		Effort:          slot.key.effort,
		Provider:        slot.key.provider,
		PermissionLevel: PermissionReadOnly,
		SystemPrompt:    slot.key.systemPrompt,
		Env:             environ(),
	}
	if lifecycle.EphemeralNoScratchCwd {
		// Keep in-process ephemeral slots completely off disk (no scratch mkdir).
		spec.Root = os.TempDir()
		spec.Cwd = spec.Root
	} else {
		cwd, err := p.ensureScratchCwd()
		if err != nil {
			return err
		}
		spec.Root = cwd
		spec.Cwd = cwd
	}
	if lifecycle.EphemeralIsolatedStateDir {
		stateDir, err := os.MkdirTemp(p.scratchRoot, "ws-ephemeral-state-*")
		if err != nil {
			return fmt.Errorf("ephemeral pool: create isolated state dir: %w", err)
		}
		slot.stateDir = stateDir
		spec.ephemeralStateDir = stateDir
	}
	// Start the long-lived process with the pool's lifetime context (p.ctx), not
	// the per-call ctx, because in-process backends bind their event loop
	// lifetime to the context passed to Start. Still honor call ctx
	// cancellation/timeout while Start is in progress.
	type startResult struct {
		proc Process
		err  error
	}
	started := make(chan startResult, 1)
	go func() {
		proc, err := backend.Start(p.ctx, spec)
		started <- startResult{proc: proc, err: err}
	}()
	select {
	case res := <-started:
		if res.err != nil {
			if slot.stateDir != "" {
				_ = os.RemoveAll(slot.stateDir)
				slot.stateDir = ""
			}
			return res.err
		}
		if ctx.Err() != nil {
			_ = res.proc.Close()
			if slot.stateDir != "" {
				_ = os.RemoveAll(slot.stateDir)
				slot.stateDir = ""
			}
			return ctx.Err()
		}
		slot.proc = res.proc
		slot.controls = controlsFor(res.proc)
		slot.turns = 0
		slot.tokens = 0
		slot.resets = 0
		return nil
	case <-ctx.Done():
		stateDir := slot.stateDir
		slot.stateDir = ""
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			res := <-started
			if res.proc != nil {
				_ = res.proc.Close()
			}
			if stateDir != "" {
				_ = os.RemoveAll(stateDir)
			}
		}()
		return ctx.Err()
	}
}

func (p *EphemeralPool) runTurn(ctx context.Context, slot *ephemeralSlot, prompt string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	// Clear any trailing buffered events from a prior turn (including
	// BoundedRotation turns where Reset did not run) before sending.
	slot.drainPendingEvents()
	if err := slot.proc.Send(prompt); err != nil {
		return "", false, err
	}
	var finalParts []string
	var lastPartial string
	events := slot.proc.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return "", false, io.ErrClosedPipe
			}
			if ev.SessionID != "" {
				slot.sessionID = ev.SessionID
			}
			if ev.Live != nil {
				switch ev.Live.Kind {
				case wslive.KindReply:
					if ev.Live.Final {
						if t := strings.TrimSpace(ev.Live.Text); t != "" {
							finalParts = append(finalParts, t)
						}
					} else if t := strings.TrimSpace(ev.Live.Text); t != "" {
						lastPartial = t
					}
				case wslive.KindStatus:
					if ev.Live.Context != nil && ev.Live.Context.Tokens != nil {
						slot.tokens = *ev.Live.Context.Tokens
					}
				}
			}
			if ev.Fatal != nil {
				return "", false, ev.Fatal
			}
			if ev.TurnEnd {
				if ev.TurnError != "" {
					return "", false, errors.New(ev.TurnError)
				}
				if ev.ModelFailure != "" {
					return "", false, errors.New(ev.ModelFailure)
				}
				if len(finalParts) > 0 {
					return strings.Join(finalParts, "\n"), false, nil
				}
				return lastPartial, false, nil
			}
		case <-ctx.Done():
			return "", true, ctx.Err()
		}
	}
}

// SweepIdle evicts and closes all idle slots whose IdleTTL has elapsed.
func (p *EphemeralPool) SweepIdle() int {
	now := p.now()
	var evicted []*ephemeralSlot
	p.mu.Lock()
	for key, slot := range p.slots {
		if slot.inUse == 0 && now.Sub(slot.lastUsed) >= slot.idleTTL {
			delete(p.slots, key)
			evicted = append(evicted, slot)
		}
	}
	p.mu.Unlock()

	for _, slot := range evicted {
		slot.closeProcess()
	}
	return len(evicted)
}

// Close stops idle sweeping, cancels in-flight calls, closes every cached
// process, and deletes all temporary isolated state and scratch cwd trees.
func (p *EphemeralPool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	stopSweep := p.stopSweep
	sweepDone := p.sweepDone
	p.mu.Unlock()

	if stopSweep != nil {
		close(stopSweep)
		<-sweepDone
	}

	p.cancel()
	p.wg.Wait()

	p.mu.Lock()
	slots := make([]*ephemeralSlot, 0, len(p.slots))
	for key, slot := range p.slots {
		slots = append(slots, slot)
		delete(p.slots, key)
	}
	scratchCwd := p.scratchCwd
	p.scratchCwd = ""
	p.mu.Unlock()

	for _, slot := range slots {
		slot.closeProcess()
	}
	if scratchCwd != "" {
		_ = os.RemoveAll(scratchCwd)
	}
	return nil
}

// extractJSONReply trims whitespace and strips a single enclosing markdown
// fenced code block (```json ... ``` or ``` ... ```) if present.
func extractJSONReply(reply string) string {
	s := strings.TrimSpace(reply)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			body := s[nl+1:]
			if end := strings.LastIndex(body, "```"); end >= 0 {
				s = strings.TrimSpace(body[:end])
			}
		}
	}
	return s
}

func validateResponseJSON(reply string, schema map[string]any) (string, error) {
	cleaned := extractJSONReply(reply)
	var value any
	dec := json.NewDecoder(strings.NewReader(cleaned))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidJSONResponse, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return "", fmt.Errorf("%w: trailing content after JSON value", ErrInvalidJSONResponse)
	}
	if err := validateSchemaValue(value, schema, "$"); err != nil {
		return "", fmt.Errorf("%w: %v", ErrSchemaViolation, err)
	}
	return cleaned, nil
}

func validateSchemaValue(value any, schema map[string]any, path string) error {
	if schema == nil {
		return nil
	}
	if typ, ok := schema["type"]; ok {
		if err := validateSchemaType(value, typ, path); err != nil {
			return err
		}
	}
	if rawEnum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range rawEnum {
			if jsonValuesEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value %v is not in enum", path, value)
		}
	}
	if constVal, ok := schema["const"]; ok {
		if !jsonValuesEqual(value, constVal) {
			return fmt.Errorf("%s: value %v does not match const %v", path, value, constVal)
		}
	}
	switch v := value.(type) {
	case map[string]any:
		if reqs, ok := schema["required"].([]any); ok {
			for _, r := range reqs {
				name, _ := r.(string)
				if name != "" {
					if _, exists := v[name]; !exists {
						return fmt.Errorf("%s: missing required property %q", path, name)
					}
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for k, propVal := range v {
			if propSchema, exists := props[k]; exists {
				if sm, ok := propSchema.(map[string]any); ok {
					if err := validateSchemaValue(propVal, sm, path+"."+k); err != nil {
						return err
					}
				}
				continue
			}
			if addl, hasAddl := schema["additionalProperties"]; hasAddl {
				switch a := addl.(type) {
				case bool:
					if !a {
						return fmt.Errorf("%s: unexpected property %q", path, k)
					}
				case map[string]any:
					if err := validateSchemaValue(propVal, a, path+"."+k); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		if minItems, ok := schemaNumber(schema["minItems"]); ok && float64(len(v)) < minItems {
			return fmt.Errorf("%s: array length %d < minItems %v", path, len(v), minItems)
		}
		if maxItems, ok := schemaNumber(schema["maxItems"]); ok && float64(len(v)) > maxItems {
			return fmt.Errorf("%s: array length %d > maxItems %v", path, len(v), maxItems)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range v {
				if err := validateSchemaValue(item, itemSchema, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case string:
		if minLen, ok := schemaNumber(schema["minLength"]); ok && float64(len([]rune(v))) < minLen {
			return fmt.Errorf("%s: string length < minLength %v", path, minLen)
		}
		if maxLen, ok := schemaNumber(schema["maxLength"]); ok && float64(len([]rune(v))) > maxLen {
			return fmt.Errorf("%s: string length > maxLength %v", path, maxLen)
		}
	case json.Number:
		if n, err := v.Float64(); err == nil {
			if minVal, ok := schemaNumber(schema["minimum"]); ok && n < minVal {
				return fmt.Errorf("%s: value %v < minimum %v", path, n, minVal)
			}
			if maxVal, ok := schemaNumber(schema["maximum"]); ok && n > maxVal {
				return fmt.Errorf("%s: value %v > maximum %v", path, n, maxVal)
			}
		}
	}
	return nil
}

func validateSchemaType(value any, rawType any, path string) error {
	var allowed []string
	switch t := rawType.(type) {
	case string:
		allowed = []string{t}
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				allowed = append(allowed, s)
			}
		}
	default:
		return nil
	}
	for _, kind := range allowed {
		if matchesSchemaType(value, kind) {
			return nil
		}
	}
	return fmt.Errorf("%s: expected type %v", path, allowed)
}

func matchesSchemaType(value any, kind string) bool {
	switch kind {
	case "null":
		return value == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		num, ok := value.(json.Number)
		if !ok {
			return false
		}
		f, err := num.Float64()
		return err == nil && math.Trunc(f) == f
	}
	return false
}

func schemaNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func jsonValuesEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}
