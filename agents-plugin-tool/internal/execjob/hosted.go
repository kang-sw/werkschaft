package execjob

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// deleting holds the ids of the jobs the job cap dropped whose row
	// delete has not succeeded (still under way, or failed): a holder's end
	// for one of them leaves the exit copy to the delete, the id draw skips
	// them, and admit retries their delete and counts them toward the job
	// cap, so a failing store bounds them. deleteGen numbers the claims.
	deleting  map[string]*tombstone
	deleteGen uint64
	// retiring counts, per exec id, a predecessor's holders Start is
	// stopping whose id no live job records (one no job owns, or one of an
	// ended job): the holder may still write the exit copy at that id's
	// path, so the id is not drawn, its ended job not pruned, and both count
	// toward the job cap until the stop is confirmed (retireOld).
	retiring map[string]int
	started  bool
	closed   bool
	retry    *time.Timer
	// quit closes with Close: it ends the retried holder stops
	// (retryRetire).
	quit chan struct{}
	// ownerFences are the conversations whose new jobs are refused (a close
	// in progress); rootFences the worktrees being released, removed or
	// acquired, each with its live fences. Both under mu.
	ownerFences map[string]bool
	rootFences  map[string]*rootFence
}

// tombstone is a dropped job whose row delete has not succeeded.
type tombstone struct {
	holder string
	// claim is the delete under way for the id (its claim number), 0 when
	// none: one delete at a time owns a tombstone, from fencing its job's
	// row writes through the copy's removal or the claim's release, so a
	// retry never deletes the row of a later job under the same id.
	claim uint64
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
	// the running set RunningOwners counts, and after a user stop's notice
	// outcome is recorded (what the job list shows); nil observes nothing.
	// It runs on the launching or ending goroutine, so it must return
	// promptly.
	OnRunning func()
}

type hostedJob struct {
	row  HostedJobRow
	out  *JobOutput
	done chan struct{}
	// real holds the resolved root and working directory of a running row
	// an older daemon stored without them (restoredPlaces at restore, a
	// fallback the row does not persist). Read under h.mu by the worktree
	// fence, never rewritten.
	real []string
	// lastSeq is the last holder record taken: a replay after a daemon
	// restart starts from the ring's first record again.
	lastSeq uint64
	// stdinOpen: the job's stdin takes exec.write. writeMu orders the
	// writes, so an eof closes it only once its holder took the eof.
	stdinOpen bool
	// stdinUnknown: a rejoined job whose holder has not reported its
	// stdin yet (not attached); a write asks the listing again first.
	stdinUnknown bool
	writeMu      sync.Mutex
	// stopping: exec.stop or an interrupt asked for the end; the end is
	// then HostedStopped and sends no completion notice.
	stopping bool
	// origin is who launched the job (OriginUser, or "" for an agent's); in
	// memory only, set at admission.
	origin string
	// stopIntent: the stop that set stopping was the user's (stop byUser).
	// The first stop to set stopping decides it; it clears with stopping
	// when the holder did not go. userStopped commits it where the job
	// ends HostedStopped while it is set (endLocked): a stop that did not
	// end the job, or a job that ended another way first, is never marked.
	// stopOwed: the marked end owes the owner a notice (a job already
	// converted to async, past its launch); stopNotice is that notice's
	// outcome (StopNotice*), written under noticeMu (which orders its
	// admission) and h.mu, read under h.mu. None of these is stored: a
	// restart loses them.
	stopIntent  bool
	userStopped bool
	stopOwed    bool
	stopNotice  string
	noticeMu    sync.Mutex
	delivering  bool
	// publishing: a converted job's id is being handed to its caller; its
	// completion notice waits for that (deliver), so the id is out first.
	publishing bool
	// starting: Launch admitted the job and has not handed its process to
	// the holder yet; a stop then ends the job at once, and Launch, seeing
	// the end, never spawns. spawned is open while that hand-over runs; a
	// stop waits for it, so the process it starts is the one it ends.
	starting bool
	spawned  chan struct{}
	// holderPinned: a holder launched for j may run without j recording
	// it: set from j's admission until j records the holder, and kept
	// when j ended meanwhile (a stop during the launch). That holder writes
	// the exit copy at j's id's path as it goes, so j is not pruned, and
	// its id not reused, until the holder is confirmed gone
	// (retireHolder). Under h.mu.
	holderPinned bool
	// rev numbers j's row snapshots for the store (snapLocked, under h.mu);
	// saveMu orders their writes and saved is the last one written, so a
	// snapshot taken before the job's end never lands after it. A dropped
	// job's saved is the maximum: nothing writes its row again.
	rev    uint64
	saveMu sync.Mutex
	saved  uint64
	// endStored: a terminal row of j is in the store (under saveMu). Until
	// then j's exit copy stays, and exitCopyOwed records that its removal
	// waits for that store write (or the row's delete, which removes it).
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
	h := &Hosted{opts: opts, rings: NewOutputRings(opts.JobBytes, opts.TotalBytes), jobs: map[string]*hostedJob{}, byHolder: map[string]*hostedJob{}, deleting: map[string]*tombstone{}, retiring: map[string]int{}, quit: make(chan struct{})}
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
			// stdin waits for the live holder's report (Start, or a later
			// write): the row does not record an eof exec.write sent.
			h.byHolder[row.HolderID] = j
			j.stdinUnknown = !row.Legacy
		}
		if !row.Terminal() && row.RealRoot == "" && row.RealCwd == "" {
			// No launch-time resolution stored: an admitted launch always
			// stores its resolved root, so only an older daemon's row lacks
			// it. Resolve now, as that daemon matched the job.
			j.real = restoredPlaces(row.Root, row.Cwd)
		}
		h.jobs[row.ID] = j
	}
	return h, nil
}

// stdinFromHolderLocked settles a rejoined job's unknown stdin from its
// holder's listing. The holder keeps a pipe child's stdin across a daemon
// replacement and says whether an eof closed it; an attached holder that
// does not say (one that predates the field) leaves it closed, so no write
// claims success. An unattached holder (no child status) leaves it
// unknown for a later look. h.mu is held.
func stdinFromHolderLocked(j *hostedJob, info wsholder.Info) {
	if !j.stdinUnknown || info.Child == nil {
		return
	}
	j.stdinUnknown = false
	c := info.Child
	j.stdinOpen = c.State == "running" && c.InputClosed != nil && !*c.InputClosed
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
		if owned && !ended {
			stdinFromHolderLocked(j, info)
		}
		if !owned || ended {
			// Reserved before the jobs are published (this start returns).
			h.retiring[execID]++
		}
		h.mu.Unlock()
		switch {
		case !owned:
			// Launched and never recorded (a crash between the holder's
			// start and the first row), or of a job this daemon pruned.
			h.logf("stopping exec holder %s: no job owns it", info.ID)
			go h.retireOld(execID, info.ID)
		case ended:
			// Its job's end was stored, and the daemon went before the
			// holder's stop.
			go h.retireOld(execID, info.ID)
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
	h.sweepExitCopies()
	h.deliverPending()
}

// sweepExitCopies removes the exit copies no job row needs: a removal that
// failed, or one a predecessor never reached. A copy whose job still has a
// row, or whose row delete has not succeeded, stays.
func (h *Hosted) sweepExitCopies() {
	entries, err := os.ReadDir(h.opts.ExitDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			h.logf("sweep exit copies: %v", err)
		}
		return
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		h.mu.Lock()
		_, live := h.jobs[id]
		_, pending := h.deleting[id]
		h.mu.Unlock()
		if !live && !pending {
			h.removeExitCopy(id)
		}
	}
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
	h.noticeStopped(j)
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
	if j != nil && j.row.HolderID != desc.ID {
		// A holder of an earlier job under a reused id: the live job, and
		// the exit copy at its id's path, are not this holder's.
		current := j.row.HolderID
		h.mu.Unlock()
		h.logf("holder %s ended: job %s runs under holder %q, so it stays", desc.ID, execID, current)
		return
	}
	if b := h.byHolder[desc.ID]; b != nil && b.row.ID == execID {
		delete(h.byHolder, desc.ID)
	}
	_, pending := h.deleting[execID]
	ended := j == nil || j.row.Terminal()
	h.mu.Unlock()
	switch {
	case !ended:
		// The holder went before its exit record reached this daemon.
		h.endFromExit(j, exit)
	case j != nil:
		h.dropExitCopy(j)
	case !pending:
		// The job's row delete succeeded (or no job row ever named it).
		h.removeExitCopy(execID)
	}
	// A dropped job whose row delete has not succeeded keeps its copy:
	// the delete removes it once it succeeds (forget).
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
	h.noticeStopped(j)
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
	h.noticeStopped(j)
	// The process ended; its holder stays until stopped, and its stop takes
	// the rest of the job's process group with it.
	go h.stopHolder(id)
}

func (h *Hosted) stopHolder(id string) {
	if err := h.opts.Holders.Stop(id); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		h.logf("stop holder %s: %v", id, err)
	}
}

// retireHolder stops holder, launched for job j after j ended, and once
// it is confirmed gone removes the exit copy it left at j's id's path and
// lets j be pruned. A holder that did not go keeps j pinned, so its id is
// never reused under a copy the holder may still write, and its stop is
// retried in the background (retryRetire); the error says the first
// attempt failed.
func (h *Hosted) retireHolder(j *hostedJob, holder string) error {
	retired := func() {
		h.removeExitCopy(j.row.ID)
		h.mu.Lock()
		j.holderPinned = false
		h.mu.Unlock()
	}
	if err := h.stopGone(holder); err != nil {
		h.logf("stop holder %s of ended job %s: %v; the job's id stays reserved while the stop is retried", holder, j.row.ID, err)
		go h.retryRetire(holder, retired)
		return fmt.Errorf("stop exec job %s's holder: %w", j.row.ID, err)
	}
	retired()
	return nil
}

// retireOld stops holder, a predecessor's holder under exec id execID
// that no live job records, and releases execID's reservation (retiring)
// once the holder is confirmed gone; a failed stop is retried
// (retryRetire).
func (h *Hosted) retireOld(execID, holder string) {
	release := func() {
		h.mu.Lock()
		if h.retiring[execID]--; h.retiring[execID] <= 0 {
			delete(h.retiring, execID)
		}
		h.mu.Unlock()
	}
	if err := h.stopGone(holder); err != nil {
		h.logf("stop exec holder %s: %v; exec id %s stays reserved while the stop is retried", holder, err, execID)
		h.retryRetire(holder, release)
		return
	}
	release()
}

// stopGone stops holder; one already gone counts as stopped.
func (h *Hosted) stopGone(holder string) error {
	if err := h.opts.Holders.Stop(holder); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		return err
	}
	return nil
}

// retireRetryMin and retireRetryMax bound the delay between retried holder
// stops (a test seam).
var retireRetryMin, retireRetryMax = time.Second, time.Minute

// retryRetire retries stopping holder, doubling the delay from
// retireRetryMin up to retireRetryMax, until it is confirmed gone, and
// then runs retired. Close ends the retries: the next daemon's start stops
// the holder.
func (h *Hosted) retryRetire(holder string, retired func()) {
	delay := retireRetryMin
	for {
		t := time.NewTimer(delay)
		select {
		case <-h.quit:
			t.Stop()
			return
		case <-t.C:
		}
		err := h.stopGone(holder)
		if err == nil {
			retired()
			return
		}
		h.logf("stop holder %s: %v; retrying", holder, err)
		delay = min(delay*2, retireRetryMax)
	}
}

// endLocked marks j ended now and opens its waiters; a job that owes a
// notice owes it from now. h.mu is held.
func (h *Hosted) endLocked(j *hostedJob) {
	j.row.EndedAt = h.opts.Now().UnixMilli()
	j.stdinOpen = false
	if j.row.State == HostedStopped && j.stopIntent {
		// The user's stop is the one that ended it as stopped.
		j.userStopped = true
		j.stopOwed = j.row.Notify && !j.starting
	}
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
	row := h.rowLocked(j)
	if j.publishing {
		// The id is not out yet: the stored row owes no notice.
		row.Notify, row.Notice = false, NoticeNone
	}
	return row, j.rev
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
			h.removeExitCopy(row.ID)
		}
	}
}

// dropExitCopy removes j's exit copy once a terminal row of j is stored.
// Until then the copy stays: a successor daemon that still reads the
// running row ends the job from the copy's status instead of as lost, and
// the next stored terminal row (save) or the row's delete (forget) removes
// it.
func (h *Hosted) dropExitCopy(j *hostedJob) {
	j.saveMu.Lock()
	defer j.saveMu.Unlock()
	if j.endStored {
		h.removeExitCopy(j.row.ID)
		return
	}
	j.exitCopyOwed = true
}

// removeExitCopy removes job id's exit copy; a failure stays for the next
// start's sweep (sweepExitCopies).
func (h *Hosted) removeExitCopy(id string) {
	if err := os.Remove(h.exitCopyPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.logf("remove job %s's exit copy: %v", id, err)
	}
}

// claimLocked starts a delete claim: the caller owns the tombstones it
// marks with the returned number. h.mu is held.
func (h *Hosted) claimLocked() uint64 {
	h.deleteGen++
	return h.deleteGen
}

// claimRetriesLocked claims every tombstone no delete owns, for a retry.
// h.mu is held.
func (h *Hosted) claimRetriesLocked() ([]string, uint64) {
	var ids []string
	claim := h.claimLocked()
	for id, t := range h.deleting {
		if t.claim == 0 {
			t.claim = claim
			ids = append(ids, id)
		}
	}
	return ids, claim
}

// ownedLocked keeps the ids whose tombstone claim still holds. h.mu is
// held.
func (h *Hosted) ownedLocked(ids []string, claim uint64) []string {
	var out []string
	for _, id := range ids {
		if t := h.deleting[id]; t != nil && t.claim == claim {
			out = append(out, id)
		}
	}
	return out
}

// forget marks dropped jobs never to be written again, then deletes the
// rows of ids, whose tombstones claim owns: a write already under way
// finishes before the delete. Only a delete that succeeded removes their
// exit copies and their tombstones; after a failure the rows and copies
// stay together for a successor, and the claim is released for the next
// admit's retry.
func (h *Hosted) forget(dropped []*hostedJob, ids []string, claim uint64) {
	for _, j := range dropped {
		j.saveMu.Lock()
		j.saved = ^uint64(0)
		j.saveMu.Unlock()
	}
	// Fenced: from here a release exposes the ids to a retry. Any exit
	// short of the tombstones' removal (a failed delete, or a panic the
	// caller recovers) releases the claim.
	settled := false
	defer func() {
		if settled {
			return
		}
		h.mu.Lock()
		for _, id := range h.ownedLocked(ids, claim) {
			h.deleting[id].claim = 0
		}
		h.mu.Unlock()
	}()
	h.mu.Lock()
	ids = h.ownedLocked(ids, claim)
	h.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	if err := h.opts.Store.Delete(ids...); err != nil {
		h.logf("drop finished jobs: %v", err)
		return
	}
	// Still claimed, so still reserved: no job under these ids can own
	// the copies yet.
	for _, id := range ids {
		h.removeExitCopy(id)
	}
	h.mu.Lock()
	for _, id := range h.ownedLocked(ids, claim) {
		delete(h.deleting, id)
	}
	settled = true
	h.mu.Unlock()
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
	// Window overrides the execution window of this launch (zero keeps the
	// daemon's).
	Window time.Duration
	// CheckGen makes ExpectGen binding (zero is a real generation): a
	// conversation that has reopened since the caller decided refuses the
	// launch.
	ExpectGen uint64
	CheckGen  bool
	// UserApproved marks a launch the user explicitly asked for through a
	// trusted dashboard route (the composer's `!`): it is its own one-use
	// approval, and the agent's approval policy is neither asked nor changed.
	// Only in-process callers set it; no tool argument reaches it.
	UserApproved bool
	// OnConvert runs once when a still-running job converts to async, before
	// the job owes its completion notice, so a caller can make the exec id
	// visible ahead of the first notice admission: the job is not eligible for
	// a notice, in memory or in its stored row, until it returns. A job that
	// ends first converts nothing and never calls it. An error is logged and
	// leaves the ordinary notice as the one delivery (a lead decision: a refused
	// publication falls back to the ordinary notice as the single delivery), so
	// the callback must be all-or-nothing: it returns an error only when nothing
	// reached the owner or the client, never after a partial publication.
	OnConvert func(HostedView) error
	// Origin is who launched it: OriginUser for the composer's own `!`
	// command, else (empty) the agent's tool. Kept in memory for the job
	// list and the stop notice's route.
	Origin string
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
	if !h.closed {
		close(h.quit)
	}
	h.closed = true
	if h.retry != nil {
		h.retry.Stop()
	}
	h.mu.Unlock()
	return h.opts.Store.Close()
}
