package execjob

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
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
	// OnRunning is called, with no lock held, after a job enters or leaves
	// the running set RunningOwners counts; nil observes nothing. It runs
	// on the launching or ending goroutine, so it must return promptly.
	OnRunning func()
}

type hostedJob struct {
	row  HostedJobRow
	out  *JobOutput
	done chan struct{}
	// lastSeq is the last holder record taken: a replay after a daemon
	// restart starts from the ring's first record again.
	lastSeq uint64
	// stdinOpen: the job's stdin takes exec.write. writeMu orders the
	// writes, so an eof closes it only once its holder took the eof.
	stdinOpen bool
	writeMu   sync.Mutex
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
	// endStored: a terminal row of j is in the store (under saveMu). Until
	// then j's exit copy stays, and exitCopyOwed records that its removal
	// waits for that store write.
	endStored    bool
	exitCopyOwed bool
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
		j := &hostedJob{row: row, out: h.rings.NewJob(), done: make(chan struct{}), endStored: row.Terminal()}
		if row.Terminal() {
			h.rings.Restore(j.out, row.Written, row.Tails[StreamStdout], row.Tails[StreamStderr], row.Tails[StreamCombined])
			close(j.done)
		} else if row.HolderID != "" {
			// stdin stays closed until Start reads the live holder's
			// report: the row does not record an eof exec.write sent.
			h.byHolder[row.HolderID] = j
		}
		h.jobs[row.ID] = j
	}
	return h, nil
}

// RunningOwners counts the jobs still running by their owning conversation
// (HostedJobRow.Owner), including the running jobs a predecessor left that
// Start has not settled yet. It reads memory only; a nil h counts none.
func (h *Hosted) RunningOwners() map[string]int {
	out := map[string]int{}
	if h == nil {
		return out
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, j := range h.jobs {
		if !j.row.Terminal() && j.row.Owner != "" {
			out[j.row.Owner]++
		}
	}
	return out
}

// runningChanged calls OnRunning; h.mu must not be held.
func (h *Hosted) runningChanged() {
	if h.opts.OnRunning != nil {
		h.opts.OnRunning()
	}
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
		if owned && !ended && !j.row.Legacy {
			// The holder keeps a pipe child's stdin across a daemon
			// replacement and says whether an eof closed it; one that
			// does not say leaves it closed, so no write claims success.
			j.stdinOpen = info.Child != nil && info.Child.State == "running" && info.Child.InputClosed != nil && !*info.Child.InputClosed
		}
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
	h.runningChanged()
	h.save(j, row, rev)
	h.dropExitCopy(j)
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
	if j == nil {
		_ = os.Remove(h.exitCopyPath(execID))
		return
	}
	h.dropExitCopy(j)
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
	h.runningChanged()
	h.save(j, row, rev)
	h.dropExitCopy(j)
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
		if ev.Offset != nil && ev.CombinedOffset != nil {
			// A rejoin replays the holder's ring from its oldest record:
			// its offsets keep a reader's saved byte cursor on the same
			// bytes and mark the prefix the holder no longer has.
			h.rings.AppendAt(j.out, stream, ev.Data, *ev.Offset, *ev.CombinedOffset)
		} else {
			h.rings.Append(j.out, stream, ev.Data)
		}
		return
	}
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	if ev.OutputBytes != nil {
		// A replay whose output records all left the holder's ring still
		// ends with the job's totals.
		h.rings.AdvanceTo(j.out, ev.StreamBytes["stdout"], ev.StreamBytes["stderr"], *ev.OutputBytes)
	}
	j.row.State = HostedExited
	if j.stopping {
		j.row.State = HostedStopped
	}
	j.row.ExitCode, j.row.Signal = ev.ExitCode, ev.Signal
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.runningChanged()
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
		return
	}
	if row.Terminal() && !j.endStored {
		j.endStored = true
		if j.exitCopyOwed {
			j.exitCopyOwed = false
			_ = os.Remove(h.exitCopyPath(row.ID))
		}
	}
}

// dropExitCopy removes j's exit copy once a terminal row of j is stored.
// Until then the copy stays: a successor daemon that still reads the
// running row ends the job from the copy's status instead of as lost, and
// the next stored terminal row (save) removes it. A forgotten job's row is
// gone, so nothing needs its copy.
func (h *Hosted) dropExitCopy(j *hostedJob) {
	j.saveMu.Lock()
	defer j.saveMu.Unlock()
	if j.endStored || j.saved == ^uint64(0) {
		_ = os.Remove(h.exitCopyPath(j.row.ID))
		return
	}
	j.exitCopyOwed = true
}

// forget marks dropped jobs never to be written again, then deletes their
// rows: a write already under way finishes before the delete. An exit copy
// still owed (its terminal row never stored) goes with the deleted row;
// if the delete fails, the row and its copy stay together for a successor.
func (h *Hosted) forget(dropped []*hostedJob, ids []string) {
	var owed []string
	for _, j := range dropped {
		j.saveMu.Lock()
		j.saved = ^uint64(0)
		if j.exitCopyOwed {
			j.exitCopyOwed = false
			owed = append(owed, j.row.ID)
		}
		j.saveMu.Unlock()
	}
	if err := h.opts.Store.Delete(ids...); err != nil {
		h.logf("drop finished jobs: %v", err)
		return
	}
	for _, id := range owed {
		_ = os.Remove(h.exitCopyPath(id))
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
