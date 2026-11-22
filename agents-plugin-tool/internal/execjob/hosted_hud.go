package execjob

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The dashboard's job HUD reads and stops a conversation's own jobs through
// the methods here. Every one reads memory only: the summary list, the
// offset output read and the stop's marker never touch a file (the stop's one
// durable write is the row save stop already makes).

// Outcomes of a user stop's notice to the owner, as the HUD shows them.
const (
	// StopNoticeNone: no notice was admitted (yet): the job was not stopped
	// by the user, or ended inside its launch call, which reports it itself.
	StopNoticeNone = ""
	// StopNoticeSent: the owner has the notice, or will at its next turn
	// (accepted, deduplicated, or held for a handed-off session).
	StopNoticeSent = "sent"
	// StopNoticeClosed: the owning conversation was closed or reopened since
	// the job's admission, so the notice was dropped for good.
	StopNoticeClosed = "closed"
	// StopNoticeFailed: admission failed; a repeated stop of the job retries.
	StopNoticeFailed = "failed"
	// StopNoticePending: the owner's session was handed over at that moment
	// and held nothing yet (a deferred admission). It is tried again on its
	// own with the completion notices' retry, and a repeated stop tries it
	// at once.
	StopNoticePending = "pending"
)

// HostedOutputLimit bounds one output read: below textreader.MaxReadLimit,
// so a peek's poll stays small.
const HostedOutputLimit = 16 << 10

// HostedSummary is one job as the HUD's list shows it: the row's facts
// without the output tails a view copies, and the in-memory origin and
// stop marker.
type HostedSummary struct {
	ID      string
	Tool    string
	Command string
	Cwd     string
	State   string
	Error   string
	// Origin is OriginUser for the composer's `!` command, else OriginAgent.
	Origin   string
	ExitCode *int
	Signal   *int
	// StartedAt and EndedAt are epoch milliseconds; EndedAt is zero while
	// running.
	StartedAt int64
	EndedAt   int64
	// Written is the combined output's total bytes.
	Written int64
	// StoppedByUser: the user's stop ended it (state stopped). StopNotice is
	// the notice's outcome (StopNotice*), meaningful for an agent's job.
	StoppedByUser bool
	StopNotice    string
}

// Terminal reports whether the job ended.
func (s HostedSummary) Terminal() bool { return s.State != HostedRunning }

// summaryLocked is j's summary. h.mu is held.
func (h *Hosted) summaryLocked(j *hostedJob) HostedSummary {
	row := &j.row
	origin := OriginAgent
	if j.origin == OriginUser {
		origin = OriginUser
	}
	return HostedSummary{
		ID: row.ID, Tool: row.Tool, Command: displayCommand(*row), Cwd: row.Cwd, State: row.State, Error: row.Error,
		Origin: origin, ExitCode: row.ExitCode, Signal: row.Signal, StartedAt: row.StartedAt, EndedAt: row.EndedAt,
		Written:       j.out.Written(StreamCombined),
		StoppedByUser: j.userStopped && row.State == HostedStopped,
		StopNotice:    j.stopNotice,
	}
}

// Jobs returns every job owner owns, for the HUD: the running ones first,
// newest first, then the finished ones, last ended first. Unlike List it is
// not cut at HostedListMax (the retained jobs are bounded by HostedMaxJobs)
// and copies no output tail. A nil h returns none.
func (h *Hosted) Jobs(owner string) []HostedSummary {
	if h == nil || owner == "" {
		return nil
	}
	h.mu.Lock()
	var out []HostedSummary
	for _, j := range h.jobs {
		if j.row.Owner == owner {
			out = append(out, h.summaryLocked(j))
		}
	}
	h.mu.Unlock()
	sort.Slice(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if x.Terminal() != y.Terminal() {
			return !x.Terminal()
		}
		if !x.Terminal() {
			if x.StartedAt != y.StartedAt {
				return x.StartedAt > y.StartedAt
			}
		} else if x.EndedAt != y.EndedAt {
			return x.EndedAt > y.EndedAt
		}
		return x.ID < y.ID
	})
	return out
}

// HostedOutput is one offset read of a job's merged output.
type HostedOutput struct {
	Job HostedSummary
	// From is the offset the read started at (the one asked for, or Evicted
	// when output before it was dropped); Next is where the following read
	// starts.
	From, Next int64
	// Written and Evicted are the job's total and first retained combined
	// offsets: Gap says output before the asked offset was dropped.
	Written, Evicted int64
	Gap              bool
	Segments         []Segment
}

// ReadOutput reads owner's job id's combined output from offset from, at
// most limit bytes (HostedOutputLimit at most), as stream-attributed runs. An
// offset below Evicted reports a gap; one past Written clamps to it. It
// copies retained memory and does no I/O; another owner's job is not found.
func (h *Hosted) ReadOutput(owner, id string, from int64, limit int) (HostedOutput, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedOutput{}, err
	}
	if limit <= 0 || limit > HostedOutputLimit {
		limit = HostedOutputLimit
	}
	from = max(from, 0)
	h.mu.Lock()
	sum := h.summaryLocked(j)
	h.mu.Unlock()
	read := j.out.ReadSegments(from, int64(limit), !sum.Terminal())
	return HostedOutput{
		Job: sum, From: read.Start, Next: read.End, Written: read.Written, Evicted: read.Evicted,
		Gap: from < read.Evicted, Segments: read.Segments,
	}, nil
}

// StopByUser is the user's stop of owner's job id (the HUD's Stop): the
// process group ends as exec.stop's does, and a job that this stop ended is
// marked stopped-by-user. The owner is told once, after the end:
//   - an agent's job that already converted to async gets a steered notice
//     (exec:<id>:stopped-by-user), waking an idle agent; a job still inside
//     its launch call gets none, since that call reports the stop itself;
//   - the user's own `!` job gets a receipt in the lead's pending input
//     instead, which wakes nothing.
//
// A job that ended before the stop is never marked and gets no notice (an
// idempotent no-op), except that a job already marked whose notice failed
// is the Retry: the same event ID is admitted again, so it deduplicates if
// the first notice did land. The returned summary carries the outcome.
func (h *Hosted) StopByUser(owner, id string) (HostedSummary, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedSummary{}, err
	}
	h.mu.Lock()
	ended := j.row.Terminal()
	h.mu.Unlock()
	var stopErr error
	if ended {
		h.retryStopNotice(j)
	} else {
		stopErr = h.stop(j, true)
		h.noticeStopped(j)
	}
	h.mu.Lock()
	sum := h.summaryLocked(j)
	h.mu.Unlock()
	return sum, stopErr
}

// noticeStopped admits the notice a marked stop owes its owner, once: the
// first call after the marked end admits and records the outcome, every
// later one (the ending goroutine and the stopping caller both call it) finds
// it recorded. A converted job whose id is not published yet is skipped
// here; convert calls it again once it is.
func (h *Hosted) noticeStopped(j *hostedJob) {
	j.noticeMu.Lock()
	defer j.noticeMu.Unlock()
	h.admitStopNoticeLocked(j, StopNoticeNone)
}

// stopNoticeOwedLocked reports whether j's user-stop notice is still owed: the
// stop marked the job and owes a notice, and the outcome is not recorded yet
// (none: its first admission is under way) or is pending (deferred, retried by
// the timer). Pruning keeps such a row. h.mu is held.
func stopNoticeOwedLocked(j *hostedJob) bool {
	return j.userStopped && j.stopOwed && (j.stopNotice == StopNoticeNone || j.stopNotice == StopNoticePending)
}

// retryStopNotice admits a failed or deferred user-stop notice again.
func (h *Hosted) retryStopNotice(j *hostedJob) {
	j.noticeMu.Lock()
	defer j.noticeMu.Unlock()
	h.mu.Lock()
	from := j.stopNotice
	h.mu.Unlock()
	if from != StopNoticeFailed && from != StopNoticePending {
		return
	}
	h.admitStopNoticeLocked(j, from)
}

// admitStopNoticeLocked is noticeStopped with j.noticeMu held: it admits the
// notice while j's outcome is still from (none for the first admission, the
// failed or pending outcome for a retry), so an admission that does not run
// leaves the outcome, and its retry, as it was. The revision moves only when
// the recorded outcome differs from from: a deferred notice tried again and
// deferred again changes nothing the list shows.
func (h *Hosted) admitStopNoticeLocked(j *hostedJob, from string) {
	h.mu.Lock()
	if !j.userStopped || !j.stopOwed || j.stopNotice != from || !h.started || h.closed || j.publishing {
		h.mu.Unlock()
		return
	}
	row, origin := h.rowLocked(j), j.origin
	h.mu.Unlock()
	note := h.opts.Sessions.StopNote(row.Owner)
	var outcome string
	if origin == OriginUser {
		text := userStopReceipt(row, j.out, note)
		if err := h.opts.Sessions.Hand(row.Owner, row.Gen, row.ID, text); err != nil {
			h.logf("stop receipt of %s: %v", row.ID, err)
			outcome = StopNoticeFailed
		} else {
			outcome = StopNoticeSent
		}
	} else {
		text := stopNoticeText(row, j.out, note)
		out, err := h.opts.Sessions.Notify(row.Owner, row.Gen, "exec:"+row.ID+":stopped-by-user", text)
		switch {
		case err != nil:
			h.logf("stop notice of %s: %v", row.ID, err)
			outcome = StopNoticeFailed
		case out == NoticeDropped:
			outcome = StopNoticeClosed
		case out == NoticeDeferred:
			// Nothing was queued: the session was between owners. Tried
			// again on the completion notices' timer, not recorded as sent.
			outcome = StopNoticePending
		default:
			// Accepted, or a duplicate of one that landed: the agent has it.
			outcome = StopNoticeSent
		}
	}
	h.mu.Lock()
	j.stopNotice = outcome
	if outcome == StopNoticePending {
		h.scheduleRetryLocked()
	}
	h.mu.Unlock()
	// What the list shows changed; the revision moves once it is recorded.
	if outcome != from {
		h.runningChanged()
	}
}

// stopNoticeText is the notice of a job the user stopped: the completion
// notice's own format (head, command, output tail, exec.tail pointer) with
// a stopped-by-user line after the command carrying the working directory,
// runtime and output size, then note.
func stopNoticeText(row HostedJobRow, out *JobOutput, note string) string {
	return noticeBody(fmt.Sprintf("[ws exec] %s %s", row.ID, endText(row)), row, out, note)
}

// userStopReceipt is the receipt of the user's own `!` job that the user
// stopped, for the lead's next turn.
func userStopReceipt(row HostedJobRow, out *JobOutput, note string) string {
	return noticeBody(fmt.Sprintf("[user ran from the dashboard] exec_id %s was stopped by the user", row.ID), row, out, note)
}

// noticeBody renders head, the runtime, the command, the stopped-by-user
// line, the output tail and the exec.tail pointer.
func noticeBody(head string, row HostedJobRow, out *JobOutput, note string) string {
	var b strings.Builder
	b.WriteString(head)
	var runtime string
	if row.StartedAt > 0 && row.EndedAt >= row.StartedAt {
		runtime = (time.Duration(row.EndedAt-row.StartedAt) * time.Millisecond).Round(100 * time.Millisecond).String()
		fmt.Fprintf(&b, " after %s", runtime)
	}
	b.WriteString(".\n")
	if cmd := displayCommand(row); cmd != "" {
		fmt.Fprintf(&b, "command: %s\n", cmd)
	}
	fmt.Fprintf(&b, "stopped by user: cwd %s", row.Cwd)
	if runtime != "" {
		fmt.Fprintf(&b, "; ran %s", runtime)
	}
	fmt.Fprintf(&b, "; %d bytes of output. %s\n", out.Written(StreamCombined), note)
	writeOutputTail(&b, out)
	fmt.Fprintf(&b, "Read more with exec.tail (exec_id %s).", row.ID)
	return b.String()
}
