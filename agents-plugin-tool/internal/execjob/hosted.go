package execjob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsgit"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wskey"
)

// Hosted runs the exec jobs of hosted sessions: one holder per job, so a
// job outlives the daemon that started it; output in memory rings, read
// back from the holder's own ring after a daemon restart; metadata in a
// HostedStore written at the start and the end only; terminal status
// copied outside the holder's directory (ExitDir), so it survives an older
// daemon's cleanup of a holder it does not know. Safe for concurrent use.
type Hosted struct {
	opts  HostedOptions
	rings *OutputRings

	mu       sync.Mutex
	jobs     map[string]*hostedJob
	byHolder map[string]*hostedJob
	started  bool
	closed   bool
	retry    *time.Timer
}

// HostedOptions configures Hosted. Holders, Store and Sessions are
// required.
type HostedOptions struct {
	Holders  HostedHolders
	Store    HostedStore
	Sessions HostedSessions
	// ExitDir holds each job holder's exit copy (wsholder.LaunchSpec.ExitCopy),
	// one file per job, named by its exec id.
	ExitDir string
	// Log receives diagnostics; nil discards them.
	Log io.Writer
	// Window replaces HostedWindow (a test seam); Now replaces time.Now.
	Window time.Duration
	Now    func() time.Time
	// JobBytes and TotalBytes replace the ring bounds (a test seam).
	JobBytes, TotalBytes int64
}

type hostedJob struct {
	row  HostedJobRow
	out  *JobOutput
	done chan struct{}
	// lastSeq is the last holder record taken: a replay after a daemon
	// restart starts from the ring's first record again.
	lastSeq uint64
	// stdinOpen: the job's stdin takes exec.write.
	stdinOpen bool
	// stopping: exec.stop or an interrupt asked for the end; the end is
	// then HostedStopped and sends no notice.
	stopping   bool
	delivering bool
	// rev numbers j's row snapshots for the store (snapLocked, under h.mu);
	// saveMu orders their writes and saved is the last one written, so a
	// snapshot taken before the job's end never lands after it. A dropped
	// job's saved is the maximum: nothing writes its row again.
	rev    uint64
	saveMu sync.Mutex
	saved  uint64
}

// NewHosted loads the stored jobs: finished ones with their output preview,
// running ones waiting for their holder's replay (Start).
func NewHosted(opts HostedOptions) (*Hosted, error) {
	if opts.Holders == nil || opts.Store == nil || opts.Sessions == nil {
		return nil, errors.New("hosted exec needs holders, a store, and sessions")
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Window <= 0 {
		opts.Window = HostedWindow
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.JobBytes <= 0 {
		opts.JobBytes = hostedJobBytes
	}
	if opts.TotalBytes <= 0 {
		opts.TotalBytes = hostedTotalBytes
	}
	h := &Hosted{opts: opts, rings: NewOutputRings(opts.JobBytes, opts.TotalBytes), jobs: map[string]*hostedJob{}, byHolder: map[string]*hostedJob{}}
	rows, err := opts.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("load hosted exec jobs: %w", err)
	}
	for _, row := range rows {
		j := &hostedJob{row: row, out: h.rings.NewJob(), done: make(chan struct{})}
		if row.Terminal() {
			h.rings.Restore(j.out, row.Written, row.Tails[StreamStdout], row.Tails[StreamStderr], row.Tails[StreamCombined])
			close(j.done)
		} else if row.HolderID != "" {
			h.byHolder[row.HolderID] = j
		}
		h.jobs[row.ID] = j
	}
	return h, nil
}

func (h *Hosted) logf(format string, args ...any) {
	fmt.Fprintf(h.opts.Log, "exec: "+format+"\n", args...)
}

// Start settles the jobs a predecessor left: it scans the holders, so a
// live job's holder attaches and replays its output and exit, ends a job
// whose holder is gone with the status its exit copy kept (lost without
// one), stops an exec holder no job owns, and delivers the pending
// completion notices. Run it after the sessions' side has started, since a
// notice for a conversation that side does not know yet is dropped.
func (h *Hosted) Start() {
	h.opts.Holders.Scan()
	listing := h.opts.Holders.List()
	live := map[string]bool{}
	for _, info := range listing.Holders {
		live[info.ID] = true
		execID, ok := ExecHolderID(info.Session)
		if !ok {
			continue
		}
		h.mu.Lock()
		j := h.jobs[execID]
		owned := j != nil && j.row.HolderID == info.ID
		ended := owned && j.row.Terminal()
		h.mu.Unlock()
		switch {
		case !owned:
			// Launched and never recorded (a crash between the holder's
			// start and the first row), or of a job this daemon pruned.
			h.logf("stopping exec holder %s: no job owns it", info.ID)
			go h.stopHolder(info.ID)
		case ended:
			// Its job's end was stored, and the daemon went before the
			// holder's stop.
			go h.stopHolder(info.ID)
		}
	}
	h.mu.Lock()
	var gone []*hostedJob
	var replay []string
	for _, j := range h.jobs {
		switch {
		case j.row.Terminal():
		case live[j.row.HolderID]:
			replay = append(replay, j.row.HolderID)
		default:
			gone = append(gone, j)
		}
	}
	h.started = true
	h.mu.Unlock()
	// A holder the manager attached before this sink took events replays
	// its ring now; records already taken are skipped by sequence.
	for _, id := range replay {
		if err := h.opts.Holders.Replay(id); err != nil {
			h.logf("replay holder %s: %v", id, err)
		}
	}
	for _, j := range gone {
		h.endFromExitCopy(j)
	}
	h.deliverPending()
}

// endFromExitCopy ends job j, whose holder is gone, with the status its
// exit copy kept.
func (h *Hosted) endFromExitCopy(j *hostedJob) {
	path := h.exitCopyPath(j.row.ID)
	var exit wsholder.Exit
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &exit)
	}
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	if err == nil {
		j.row.State = HostedExited
		if exit.Reason == "stop" && j.stopping {
			j.row.State = HostedStopped
		}
		j.row.ExitCode, j.row.Signal = exit.ExitCode, exit.Signal
		if exit.ExitCode == nil && exit.Signal == nil {
			j.row.Error = "the job's holder ended (" + exit.Reason + ") with no exit status"
		}
	} else {
		j.row.State = HostedLost
		j.row.Error = "exec job worker is no longer active: its holder is gone and left no exit status"
	}
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	_ = os.Remove(path)
	h.deliver(j)
}

// HolderGone is the daemon's holder OnExit for an exec job's holder: the
// holder's directory is about to go, and with it the need for its exit
// copy once the job's end is stored.
func (h *Hosted) HolderGone(desc wsholder.Descriptor, exit wsholder.Exit) {
	execID, ok := ExecHolderID(desc.Session)
	if !ok {
		return
	}
	h.mu.Lock()
	j := h.jobs[execID]
	if j != nil && j.row.HolderID == desc.ID {
		delete(h.byHolder, desc.ID)
	}
	ended := j == nil || j.row.Terminal()
	h.mu.Unlock()
	if !ended {
		// The holder went before its exit record reached this daemon.
		h.endFromExit(j, exit)
		return
	}
	_ = os.Remove(h.exitCopyPath(execID))
}

// endFromExit ends running job j with the status its holder's exit.json
// kept.
func (h *Hosted) endFromExit(j *hostedJob, exit wsholder.Exit) {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	j.row.State = HostedExited
	if j.stopping {
		j.row.State = HostedStopped
	}
	j.row.ExitCode, j.row.Signal = exit.ExitCode, exit.Signal
	if exit.ExitCode == nil && exit.Signal == nil && !j.stopping {
		j.row.State, j.row.Error = HostedLost, "exec job worker is no longer active: its holder ended ("+exit.Reason+") with no exit status"
	}
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	_ = os.Remove(h.exitCopyPath(row.ID))
	h.deliver(j)
}

// HolderEvent takes a job holder's records (wsholder.Sink): output into the
// job's ring, the exit record as the job's end.
func (h *Hosted) HolderEvent(id string, ev wsholder.Event) {
	h.mu.Lock()
	j := h.byHolder[id]
	if j == nil {
		h.mu.Unlock()
		return
	}
	if ev.Event != "output" && ev.Event != "exit" {
		h.mu.Unlock()
		return
	}
	if ev.Seq != 0 && ev.Seq <= j.lastSeq {
		h.mu.Unlock()
		return
	}
	if ev.Seq != 0 {
		j.lastSeq = ev.Seq
	}
	if ev.Event == "output" {
		h.mu.Unlock()
		stream := StreamStdout
		if ev.Stream == "stderr" {
			stream = StreamStderr
		}
		h.rings.Append(j.out, stream, ev.Data)
		return
	}
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	j.row.State = HostedExited
	if j.stopping {
		j.row.State = HostedStopped
	}
	j.row.ExitCode, j.row.Signal = ev.ExitCode, ev.Signal
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	h.deliver(j)
	// The process ended; its holder stays until stopped, and its stop takes
	// the rest of the job's process group with it.
	go h.stopHolder(id)
}

func (h *Hosted) stopHolder(id string) {
	if err := h.opts.Holders.Stop(id); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		h.logf("stop holder %s: %v", id, err)
	}
}

// endLocked marks j ended now and opens its waiters; a job that owes a
// notice owes it from now. h.mu is held.
func (h *Hosted) endLocked(j *hostedJob) {
	j.row.EndedAt = h.opts.Now().UnixMilli()
	j.stdinOpen = false
	if j.row.Notify && !j.stopping && j.row.Notice == NoticeNone {
		j.row.Notice = NoticePending
	}
	select {
	case <-j.done:
	default:
		close(j.done)
	}
}

// rowLocked is j's row with its current output totals and, once ended, its
// output preview. h.mu is held.
func (h *Hosted) rowLocked(j *hostedJob) HostedJobRow {
	row := j.row
	for _, s := range []Stream{StreamCombined, StreamStdout, StreamStderr} {
		row.Written[s] = j.out.Written(s)
		row.Tails[s] = nil
		if row.Terminal() {
			row.Tails[s] = j.out.Tail(s, HostedPreviewBytes).Data
		}
	}
	return row
}

// snapLocked is j's row for the store, numbered after every earlier
// snapshot. h.mu is held.
func (h *Hosted) snapLocked(j *hostedJob) (HostedJobRow, uint64) {
	j.rev++
	return h.rowLocked(j), j.rev
}

// save stores j's snapshot row numbered rev, unless a later one is stored
// already: the writes run outside h.mu, so a running row taken before the
// job's end can reach save after the end's row did.
func (h *Hosted) save(j *hostedJob, row HostedJobRow, rev uint64) {
	j.saveMu.Lock()
	defer j.saveMu.Unlock()
	if rev <= j.saved {
		return
	}
	j.saved = rev
	if err := h.opts.Store.Put(row); err != nil {
		h.logf("store job %s: %v", row.ID, err)
	}
}

// forget marks dropped jobs never to be written again, then deletes their
// rows: a write already under way finishes before the delete.
func (h *Hosted) forget(dropped []*hostedJob, ids []string) {
	for _, j := range dropped {
		j.saveMu.Lock()
		j.saved = ^uint64(0)
		j.saveMu.Unlock()
	}
	if err := h.opts.Store.Delete(ids...); err != nil {
		h.logf("drop finished jobs: %v", err)
	}
}

func (h *Hosted) exitCopyPath(id string) string {
	return filepath.Join(h.opts.ExitDir, id+".json")
}

// HostedLaunch is one launch.
type HostedLaunch struct {
	// Owner is the conversation the connection's trusted binding names.
	Owner string
	Tool  string
	// Root is the session's root the call's session_key granted; Cwd is
	// already resolved inside it.
	Root, Cwd string
	// Argv is what runs; Argv[0] is resolved on Environ's PATH.
	Argv []string
	// Display is the command as the approval and the listing show it;
	// Command and Shell are a shell launch's line and shell.
	Display, Command, Shell string
	// Environ is the job's whole environment; Env is the call's own overlay
	// in it, which the approval shows.
	Environ []string
	Env     map[string]string
	// Stdin is written at the start, then stdin closes; StdinSet says it
	// was given. Without it a legacy launch's stdin closes at once and a
	// new launch's stays open for Write.
	Stdin    []byte
	StdinSet bool
	Async    bool
	// Legacy launches keep the legacy key and reply shape.
	Legacy bool
}

// HostedOutcome is how a launch call ended.
type HostedOutcome int

const (
	// OutcomeDone: the job ended inside the window.
	OutcomeDone HostedOutcome = iota + 1
	// OutcomeAsync: async; the call returned at once.
	OutcomeAsync
	// OutcomeConverted: the window passed (or the daemon began a
	// replacement) with the job still running; it goes on as async.
	OutcomeConverted
)

// HostedView is a snapshot of one job.
type HostedView struct {
	HostedJobRow
	Outcome HostedOutcome
}

// ErrExecNotFound answers an exec id the caller cannot see.
var ErrExecNotFound = errors.New("exec job not found")

// Launch approves req for its owner, starts it under a new holder, and
// waits through the execution window, which starts with the process: an
// end inside it answers OutcomeDone, otherwise the job goes on as async and
// owes its owner one completion notice. ctx's end inside the window (the
// caller's interrupt) stops the job; drain's (a daemon replacement)
// converts it instead.
func (h *Hosted) Launch(ctx context.Context, drain <-chan struct{}, req HostedLaunch) (HostedView, error) {
	if len(req.Argv) == 0 || strings.TrimSpace(req.Argv[0]) == "" {
		return HostedView{}, errors.New("nothing to run")
	}
	owner, err := h.opts.Sessions.Owner(req.Owner)
	if err != nil {
		return HostedView{}, fmt.Errorf("exec: the owning session cannot take a job: %w", err)
	}
	// The program resolves before the approval, so the prompt names the
	// binary that starts.
	argv := append([]string(nil), req.Argv...)
	bin, err := lookProgram(argv[0], req.Environ)
	if err != nil {
		return HostedView{}, err
	}
	argv[0] = bin
	call := approvalCall(req, bin)
	explicit, err := h.opts.Sessions.Approve(ctx, owner.ID, call)
	if err != nil {
		return HostedView{}, err
	}
	id, err := h.newID(req.Legacy)
	if err != nil {
		return HostedView{}, err
	}
	j := &hostedJob{done: make(chan struct{}), out: h.rings.NewJob(), stdinOpen: !req.Legacy && !req.StdinSet}
	j.row = HostedJobRow{ID: id, Legacy: req.Legacy, Owner: owner.ID, Gen: owner.Gen, Tool: req.Tool, Root: filepath.Clean(req.Root), Cwd: req.Cwd, Argv: argv, Command: req.Command, Shell: req.Shell, State: HostedRunning, StdinBytes: int64(len(req.Stdin)), Notify: req.Async}
	if err := h.admit(j); err != nil {
		return HostedView{}, err
	}
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: id, Owner: owner.ID})
	holder, err := h.opts.Holders.LaunchWith(wsholder.LaunchSpec{Session: session, RingBytes: hostedHolderRing, ExitCopy: h.exitCopyPath(id)})
	if err != nil {
		return h.failStart(j, "", fmt.Errorf("start the job's holder: %w", err))
	}
	h.mu.Lock()
	j.row.HolderID = holder
	h.byHolder[holder] = j
	h.mu.Unlock()
	// The live mode decides the start: an allow by role or mode that no
	// longer holds asks the user now.
	for !explicit {
		ok, err := h.opts.Sessions.Immediate(owner.ID)
		if err != nil {
			return h.failStart(j, holder, err)
		}
		if ok {
			break
		}
		if explicit, err = h.opts.Sessions.Approve(ctx, owner.ID, call); err != nil {
			return h.failStart(j, holder, err)
		}
	}
	h.mu.Lock()
	j.row.StartedAt = h.opts.Now().UnixMilli()
	h.mu.Unlock()
	st, err := h.opts.Holders.Spawn(holder, wsholder.SpawnSpec{Argv: argv, Cwd: req.Cwd, Env: environMap(req.Environ), Mode: "pipe", Input: req.Stdin, InputEOF: req.Legacy || req.StdinSet})
	if err != nil {
		return h.failStart(j, holder, fmt.Errorf("start %s: %w", argv[0], err))
	}
	h.mu.Lock()
	j.row.PID = st.PID
	if !j.row.Terminal() {
		row, rev := h.snapLocked(j)
		h.mu.Unlock()
		h.save(j, row, rev)
	} else {
		h.mu.Unlock()
	}
	if req.Async {
		return h.view(j, OutcomeAsync), nil
	}
	window := time.NewTimer(h.opts.Window)
	defer window.Stop()
	select {
	case <-j.done:
		return h.view(j, OutcomeDone), nil
	case <-window.C:
		return h.convert(j), nil
	case <-drain:
		return h.convert(j), nil
	case <-ctx.Done():
		h.stop(j)
		return h.view(j, OutcomeDone), ctx.Err()
	}
}

// convert makes j async when it still runs; an end that won the race is an
// OutcomeDone.
func (h *Hosted) convert(j *hostedJob) HostedView {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return h.view(j, OutcomeDone)
	}
	j.row.Notify = true
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	return h.view(j, OutcomeConverted)
}

// admit registers j, refusing a new job once the running ones fill the job
// cap, and drops the oldest finished jobs over it.
func (h *Hosted) admit(j *hostedJob) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("exec: the daemon is shutting down")
	}
	running := 0
	var finished []*hostedJob
	for _, have := range h.jobs {
		switch {
		case !have.row.Terminal():
			running++
		case have.row.Notice == NoticePending || have.delivering:
			// Its notice is still owed; it stays until delivered.
		default:
			finished = append(finished, have)
		}
	}
	if running >= HostedMaxJobs {
		h.mu.Unlock()
		return fmt.Errorf("exec: %d jobs already run; stop one first", running)
	}
	h.jobs[j.row.ID] = j
	var dropped []*hostedJob
	var ids []string
	if over := len(h.jobs) - HostedMaxJobs; over > 0 {
		sort.Slice(finished, func(a, b int) bool { return finished[a].row.EndedAt < finished[b].row.EndedAt })
		for _, old := range finished[:min(over, len(finished))] {
			delete(h.jobs, old.row.ID)
			h.rings.Release(old.out)
			dropped = append(dropped, old)
			ids = append(ids, old.row.ID)
		}
	}
	h.mu.Unlock()
	if len(dropped) > 0 {
		h.forget(dropped, ids)
	}
	return nil
}

// failStart ends j before its process ran, stopping its holder.
func (h *Hosted) failStart(j *hostedJob, holder string, cause error) (HostedView, error) {
	if holder != "" {
		go h.stopHolder(holder)
	}
	h.mu.Lock()
	if holder != "" {
		delete(h.byHolder, holder)
	}
	j.row.State, j.row.Error = HostedFailed, cause.Error()
	j.row.Notify = false
	if j.row.StartedAt == 0 {
		j.row.StartedAt = h.opts.Now().UnixMilli()
	}
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	return h.view(j, OutcomeDone), cause
}

// stop ends j's process group through its holder and waits for the end.
func (h *Hosted) stop(j *hostedJob) {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	j.stopping = true
	holder := j.row.HolderID
	h.mu.Unlock()
	err := h.opts.Holders.Stop(holder)
	if err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		h.logf("stop job %s: %v", j.row.ID, err)
	}
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	if err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		// The holder did not go: the job may still run.
		j.stopping = false
		h.mu.Unlock()
		return
	}
	// The holder is gone and its exit record did not arrive first.
	j.row.State = HostedStopped
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
}

// Stop stops owner's job id and returns its view after the end.
func (h *Hosted) Stop(owner, id string) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	h.stop(j)
	return h.view(j, 0), nil
}

// Write writes data to owner's running job id's stdin, closing it after
// with eof.
func (h *Hosted) Write(owner, id string, data []byte, eof bool) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	h.mu.Lock()
	running, open, holder := !j.row.Terminal(), j.stdinOpen, j.row.HolderID
	if running && open && eof {
		j.stdinOpen = false
	}
	h.mu.Unlock()
	switch {
	case !running:
		return h.view(j, 0), fmt.Errorf("exec job %s is not running (%s)", id, j.row.State)
	case !open:
		return h.view(j, 0), fmt.Errorf("exec job %s has no open stdin: it was given at launch, closed by eof, or the job is a legacy launch", id)
	}
	if err := h.opts.Holders.Input(holder, data, eof); err != nil {
		return h.view(j, 0), fmt.Errorf("write to exec job %s: %w", id, err)
	}
	return h.view(j, 0), nil
}

// Get returns owner's job id.
func (h *Hosted) Get(owner, id string) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	return h.view(j, 0), nil
}

// Tail returns the last max bytes of owner's job id's combined output.
func (h *Hosted) Tail(owner, id string, max int) (HostedView, Slice, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, Slice{}, err
	}
	if max <= 0 || max > HostedPreviewBytes {
		max = HostedPreviewBytes
	}
	return h.view(j, 0), j.out.Tail(StreamCombined, max), nil
}

// List returns owner's jobs: the running ones, newest first, then the
// finished ones, last ended first, HostedListMax at most, and how many
// more there were.
func (h *Hosted) List(owner string) ([]HostedView, int) {
	h.mu.Lock()
	var mine []*hostedJob
	for _, j := range h.jobs {
		if j.row.Owner == owner {
			mine = append(mine, j)
		}
	}
	h.mu.Unlock()
	views := make([]HostedView, 0, len(mine))
	for _, j := range mine {
		views = append(views, h.view(j, 0))
	}
	sort.Slice(views, func(a, b int) bool {
		ra, rb := views[a].HostedJobRow, views[b].HostedJobRow
		if ra.Terminal() != rb.Terminal() {
			return !ra.Terminal()
		}
		if !ra.Terminal() {
			return ra.StartedAt > rb.StartedAt
		}
		return ra.EndedAt > rb.EndedAt
	})
	more := 0
	if len(views) > HostedListMax {
		more = len(views) - HostedListMax
		views = views[:HostedListMax]
	}
	return views, more
}

// LiveCount counts owner's running jobs.
func (h *Hosted) LiveCount(owner string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, j := range h.jobs {
		if j.row.Owner == owner && !j.row.Terminal() {
			n++
		}
	}
	return n
}

// Output returns job id's output for a reader of its own (legacy readers).
func (h *Hosted) output(id string) *JobOutput {
	h.mu.Lock()
	defer h.mu.Unlock()
	if j := h.jobs[id]; j != nil {
		return j.out
	}
	return nil
}

// owned returns job id when owner owns it.
func (h *Hosted) owned(owner, id string) (*hostedJob, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[strings.TrimSpace(id)]
	if j == nil || owner == "" || j.row.Owner != owner {
		return nil, fmt.Errorf("%w: %s", ErrExecNotFound, id)
	}
	return j, nil
}

func (h *Hosted) view(j *hostedJob, outcome HostedOutcome) HostedView {
	h.mu.Lock()
	defer h.mu.Unlock()
	return HostedView{HostedJobRow: h.rowLocked(j), Outcome: outcome}
}

// Close stops the notice retries and the store. The jobs keep running
// under their holders for the next daemon.
func (h *Hosted) Close() error {
	h.mu.Lock()
	h.closed = true
	if h.retry != nil {
		h.retry.Stop()
	}
	h.mu.Unlock()
	return h.opts.Store.Close()
}

// deliver admits j's pending completion notice, once.
func (h *Hosted) deliver(j *hostedJob) {
	h.mu.Lock()
	if !h.started || h.closed || j.row.Notice != NoticePending || j.delivering {
		h.mu.Unlock()
		return
	}
	j.delivering = true
	row := h.rowLocked(j)
	h.mu.Unlock()
	text := noticeText(row, j.out)
	out, err := h.opts.Sessions.Notify(row.Owner, row.Gen, "exec:"+row.ID+":exit", text)
	h.mu.Lock()
	j.delivering = false
	switch {
	case err != nil || out == NoticeDeferred:
		if err != nil {
			h.logf("completion notice of %s: %v", row.ID, err)
		}
		h.scheduleRetryLocked()
		h.mu.Unlock()
		return
	case out == NoticeDropped:
		j.row.Notice = NoticeSuppressed
	default:
		j.row.Notice = NoticeDelivered
	}
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
}

// deliverPending tries every pending notice.
func (h *Hosted) deliverPending() {
	h.mu.Lock()
	var pending []*hostedJob
	for _, j := range h.jobs {
		if j.row.Notice == NoticePending {
			pending = append(pending, j)
		}
	}
	h.mu.Unlock()
	for _, j := range pending {
		h.deliver(j)
	}
}

func (h *Hosted) scheduleRetryLocked() {
	if h.closed || h.retry != nil {
		return
	}
	h.retry = time.AfterFunc(noticeRetryEvery, func() {
		h.mu.Lock()
		h.retry = nil
		h.mu.Unlock()
		h.deliverPending()
	})
}

// noticeText is a completion notice: what ended how, and its output tail.
func noticeText(row HostedJobRow, out *JobOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[ws exec] %s %s", row.ID, endText(row))
	if row.StartedAt > 0 && row.EndedAt >= row.StartedAt {
		fmt.Fprintf(&b, " after %s", (time.Duration(row.EndedAt-row.StartedAt) * time.Millisecond).Round(100*time.Millisecond))
	}
	b.WriteString(".\n")
	if cmd := displayCommand(row); cmd != "" {
		fmt.Fprintf(&b, "command: %s\n", cmd)
	}
	tail := out.Tail(StreamCombined, HostedPreviewBytes)
	if len(tail.Data) > 0 {
		text, _ := DecodeText(tail.Data)
		if tail.Start > 0 {
			fmt.Fprintf(&b, "output (last %d of %d bytes):\n", len(tail.Data), tail.Written)
		} else {
			b.WriteString("output:\n")
		}
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
	} else {
		b.WriteString("no output.\n")
	}
	fmt.Fprintf(&b, "Read more with exec.tail (exec_id %s).", row.ID)
	return b.String()
}

// endText says how a finished job ended.
func endText(row HostedJobRow) string {
	switch row.State {
	case HostedRunning:
		return "is running"
	case HostedStopped:
		return "was stopped"
	case HostedFailed:
		return "failed to start: " + row.Error
	case HostedLost:
		return "was lost: " + row.Error
	}
	switch {
	case row.ExitCode != nil:
		return "exited with code " + strconv.Itoa(*row.ExitCode)
	case row.Signal != nil:
		return "was ended by signal " + strconv.Itoa(*row.Signal)
	}
	return "ended"
}

// displayCommand is the command as a person reads it.
func displayCommand(row HostedJobRow) string {
	if row.Command != "" {
		return row.Command
	}
	return strings.Join(row.Argv, " ")
}

// newID draws an unused exec id: three words for the new tools; for a
// legacy launch the long legacy key form, whose 64 random bits keep it
// apart from the file-backed jobs' keys without opening their store.
func (h *Hosted) newID(legacy bool) (string, error) {
	for i := 0; i < 16; i++ {
		var id string
		if legacy {
			var b [8]byte
			if _, err := rand.Read(b[:]); err != nil {
				return "", err
			}
			id = "exec-" + strconv.FormatInt(h.opts.Now().UnixMilli(), 10) + "-" + hex.EncodeToString(b[:])
		} else {
			key, err := wskey.Generate()
			if err != nil {
				return "", err
			}
			id = "exec_" + key
		}
		h.mu.Lock()
		_, taken := h.jobs[id]
		h.mu.Unlock()
		if !taken {
			return id, nil
		}
	}
	return "", errors.New("exec: could not draw an unused exec id")
}

// lookProgram resolves a bare program name on the job's PATH, not the
// daemon's.
func lookProgram(name string, environ []string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return name, nil
	}
	bin, err := wsgit.LookPathEnv(name, environ)
	if err != nil {
		return "", fmt.Errorf("exec: %s is not on the job's PATH: %w", name, err)
	}
	return bin, nil
}

// approvalCall is req as its approval shows it, starting program.
func approvalCall(req HostedLaunch, program string) HostedCall {
	call := HostedCall{Tool: req.Tool, Command: req.Display, Cwd: req.Cwd, Program: program, StdinBytes: len(req.Stdin)}
	for name, value := range req.Env {
		call.Env = append(call.Env, name+"="+value)
	}
	sort.Strings(call.Env)
	head := req.Stdin
	if len(head) > HostedStdinPreviewBytes {
		head = head[:HostedStdinPreviewBytes]
		// Cut at a character's start, not inside one.
		for i := len(head) - 1; i >= 0 && i >= len(head)-utf8.UTFMax; i-- {
			if utf8.RuneStart(head[i]) {
				if !utf8.FullRune(head[i:]) {
					head = head[:i]
				}
				break
			}
		}
	}
	call.Stdin = strings.ToValidUTF8(string(head), "�")
	return call
}

// environMap is environ as the holder's whole child environment; a later
// entry of a name wins, as in an overlay.
func environMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && name != "" {
			if goruntime.GOOS == "windows" {
				for have := range out {
					if strings.EqualFold(have, name) && have != name {
						delete(out, have)
					}
				}
			}
			out[name] = value
		}
	}
	return out
}

// ShellArgv is the new exec.shell form's argv for line: Bash on the job's
// PATH (Git for Windows' bash on Windows), else /bin/sh on POSIX.
func ShellArgv(line string, environ []string) ([]string, error) {
	if goruntime.GOOS == "windows" {
		if git, err := wsgit.LookPathEnv("git", environ); err == nil {
			// <Git>\cmd\git.exe or <Git>\bin\git.exe -> <Git>\bin\bash.exe
			bash := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
			if info, statErr := os.Stat(bash); statErr == nil && !info.IsDir() {
				return []string{bash, "-c", line}, nil
			}
		}
		if bash, err := wsgit.LookPathEnv("bash", environ); err == nil {
			return []string{bash, "-c", line}, nil
		}
		return nil, errors.New("exec.shell: no Git Bash found (install Git for Windows, or use exec.run with another shell)")
	}
	if bash, err := wsgit.LookPathEnv("bash", environ); err == nil {
		return []string{bash, "-c", line}, nil
	}
	return []string{"/bin/sh", "-c", line}, nil
}
