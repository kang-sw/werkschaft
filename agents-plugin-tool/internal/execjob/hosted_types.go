package execjob

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// Hosted exec states. A job is running from its process start; each other
// state is terminal.
const (
	// HostedRunning: the process runs under its holder.
	HostedRunning = "running"
	// HostedExited: the process ended on its own (ExitCode or Signal).
	HostedExited = "exited"
	// HostedStopped: exec.stop, a legacy abort, or an interrupt inside the
	// execution window ended it.
	HostedStopped = "stopped"
	// HostedFailed: the process never started.
	HostedFailed = "failed"
	// HostedLost: its holder is gone and left no exit status.
	HostedLost = "lost"
)

// Completion notice states of an async or converted job.
const (
	NoticeNone       = ""
	NoticePending    = "pending"
	NoticeDelivered  = "delivered"
	NoticeSuppressed = "suppressed"
)

// HostedJobRow is one hosted job's durable metadata (performance.md type 2,
// small mutable state): written at the process start and at the end, never
// per output. Output stays in memory; Tails is the bounded preview the end
// keeps, so a restarted daemon still shows a finished job's last output.
type HostedJobRow struct {
	// ID is the exec id: exec_<three words> for the new tools, or a legacy
	// key (exec-<digits>-<16 hex>) for a hosted exec.spawn or legacy-form
	// exec.shell launch.
	ID     string
	Legacy bool
	// Owner is the conversation (reply-id) the trusted binding named and
	// Gen its open generation at admission.
	Owner string
	Gen   uint64
	// Tool is the launching tool's name.
	Tool string
	Root string
	Cwd  string
	// RealRoot and RealCwd are Root and Cwd as admission resolved them
	// (admittedPlaces), stored even when equal to them; RealCwd is "" only
	// for an empty Cwd, and both are "" only in a row an older daemon wrote.
	// The worktree fence matches a job by them too, so a replaced daemon
	// keeps the place the job started in even after an alias it was
	// launched through changed, or its directory was renamed and an alias
	// put in its place.
	RealRoot string
	RealCwd  string
	// Argv is what ran; Command and Shell are a shell launch's line and
	// shell, as the legacy record keeps them.
	Argv    []string
	Command string
	Shell   string
	// HolderID is the job's holder; PID its process.
	HolderID string
	PID      int
	State    string
	ExitCode *int
	Signal   *int
	Error    string
	// StartedAt and EndedAt are epoch milliseconds; EndedAt is zero while
	// running.
	StartedAt int64
	EndedAt   int64
	// Notify marks an async or converted job, whose end sends one
	// completion notice; Notice is that notice's state.
	Notify bool
	Notice string
	// StdinBytes counts the bytes supplied at launch.
	StdinBytes int64
	// Written are each stream's total bytes, indexed by Stream; Tails are
	// each stream's last bytes (at most HostedPreviewBytes), same index.
	Written [3]int64
	Tails   [3][]byte
}

// Terminal reports whether the row's state is final.
func (r HostedJobRow) Terminal() bool { return r.State != HostedRunning }

// HostedStore persists hosted job rows. Load runs once, at construction;
// Put inserts or replaces one row in one transaction; Delete removes rows.
// The store holds its database open for the daemon's life (performance.md
// Rule 4); a store returns ErrHostedStoreClosed after Close.
type HostedStore interface {
	Load() ([]HostedJobRow, error)
	Put(row HostedJobRow) error
	Delete(ids ...string) error
	Close() error
}

// ErrHostedStoreClosed is a HostedStore's answer after Close.
var ErrHostedStoreClosed = errors.New("hosted exec store is closed")

// HostedOwner is the conversation a trusted binding names: Gen is its open
// generation, which its completion notice must still match.
type HostedOwner struct {
	ID   string
	Gen  uint64
	Lead bool
}

// ModelLaunch identifies the daemon-owned session's pending tool call.
// The resolved approval preview arrives before a job is allocated. The
// allocated job identity arrives separately, before the holder starts.
type ModelLaunch struct {
	ID, Tool, JobID string
	Arguments       json.RawMessage
	Approval        *HostedCall
}

// HostedCall is one launch as its approval shows it: everything the call
// gives that decides what runs. Program is argv[0] resolved on the job's
// PATH, the binary that starts; Env is the call's own environment overlay
// as sorted NAME=value entries; Stdin is the head of the stdin it gives
// (HostedStdinPreviewBytes at most) and StdinBytes its whole length.
type HostedCall struct {
	Tool       string
	Command    string
	Cwd        string
	Program    string
	Env        []string
	Stdin      string
	StdinBytes int
}

// HostedStdinPreviewBytes bounds the stdin an approval shows.
const HostedStdinPreviewBytes = 1024

// NoticeOutcome is what the owner side did with a completion notice.
type NoticeOutcome int

const (
	NoticeAccepted NoticeOutcome = iota + 1
	NoticeDuplicate
	NoticeDropped
	NoticeDeferred
)

// Origins of a hosted job: who launched it. The origin is in memory only: a
// job a restarted daemon restores reads as an agent's.
const (
	// OriginAgent: launched by the owning agent's exec tool (the zero value).
	OriginAgent = "agent"
	// OriginUser: launched by the user's own composer `!` command.
	OriginUser = "user"
)

// HostedSessions is the owning conversations' side (the agent runner):
// identity, daemon-owned approval, and once-only notice admission.
type HostedSessions interface {
	// Owner returns the open conversation id's identity.
	Owner(id string) (HostedOwner, error)
	// Approve returns once id may run call: explicit reports the user's
	// allow for this call, false a role or live-mode allow the caller
	// rechecks right before the start (Immediate). A denial or the end of
	// ctx is an error.
	Approve(ctx context.Context, id string, call HostedCall) (explicit bool, err error)
	// Immediate reports whether id's live role and mode run a call without
	// a prompt now.
	Immediate(id string) (bool, error)
	// Notify admits the completion notice text of event eventID for id
	// under generation gen, once per event.
	Notify(id string, gen uint64, eventID, text string) (NoticeOutcome, error)
	// StopNote is the sentence a user-stop notice carries, in id's
	// language: the stop was deliberate, do not relaunch unless the user
	// asks, and the job's command and output are data. English for a
	// conversation with no known language.
	StopNote(id string) string
	// Hand puts a receipt of job execID into the pending input of id under
	// generation gen, for its next accepted send; it starts or steers no
	// turn (the passive hand-off of the user's own composer jobs).
	Hand(id string, gen uint64, execID, text string) error
}

// HostedHolders is the holder manager as the hosted jobs use it.
type HostedHolders interface {
	// LaunchWith's failure with an id is a holder that registered and runs
	// unattached; the caller stops it.
	LaunchWith(spec wsholder.LaunchSpec) (string, error)
	Spawn(id string, spec wsholder.SpawnSpec) (wsholder.ChildStatus, error)
	Input(id string, data []byte, eof bool) error
	Stop(id string) error
	Scan()
	List() wsholder.Listing
	// Replay asks attached holder id to send its whole ring again, to
	// every sink: a sink added after the holder attached gets its records.
	Replay(id string) error
}

// HolderManager adapts a *wsholder.Manager to HostedHolders.
type HolderManager struct{ *wsholder.Manager }

func (m HolderManager) Spawn(id string, spec wsholder.SpawnSpec) (wsholder.ChildStatus, error) {
	c := m.Client(id)
	if c == nil {
		return wsholder.ChildStatus{}, wsholder.ErrDetached
	}
	return c.Spawn(spec)
}

func (m HolderManager) Replay(id string) error {
	c := m.Client(id)
	if c == nil {
		return wsholder.ErrDetached
	}
	_, _, err := c.Replay(0)
	return err
}

func (m HolderManager) Input(id string, data []byte, eof bool) error {
	c := m.Client(id)
	if c == nil {
		return wsholder.ErrDetached
	}
	return c.Input(data, eof)
}

// hostedHolderSession is the Session a job's holder records: kind "exec"
// marks it for this package, which owns it by ExecID; other holder
// consumers (and older daemons) leave an unknown kind alone.
type hostedHolderSession struct {
	Kind   string `json:"kind"`
	ExecID string `json:"exec_id"`
	Owner  string `json:"owner,omitempty"`
}

// HolderKindExec is the Session kind of a hosted exec job's holder.
const HolderKindExec = "exec"

// ExecHolderID returns the exec id a holder descriptor's Session names,
// and whether it is a hosted exec job's holder.
func ExecHolderID(session json.RawMessage) (string, bool) {
	var s hostedHolderSession
	if json.Unmarshal(session, &s) != nil || s.Kind != HolderKindExec || s.ExecID == "" {
		return "", false
	}
	return s.ExecID, true
}

// Hosted bounds and timing.
const (
	// HostedWindow is the execution window a synchronous launch waits
	// before it converts to async. It starts once the process starts:
	// approval and holder start do not count.
	HostedWindow = 30 * time.Second
	// HostedPreviewBytes bounds the inline output a reply or notice
	// carries and each stream's persisted tail.
	HostedPreviewBytes = InlineBudget
	// hostedJobBytes and hostedTotalBytes bound the in-memory output
	// rings, per job and across jobs.
	hostedJobBytes   = 1 << 20
	hostedTotalBytes = 32 << 20
	// hostedHolderRing is each job holder's own ring: what a restarted
	// daemon replays from a running job.
	hostedHolderRing = 1 << 20
	// hostedHolderRingCeiling lets that ring grow, only while the holder's
	// relay has not sent the records yet, so the last hostedJobBytes of raw
	// output always reach the daemon on a healthy attach. The ring charges a
	// record its data plus 64 bytes (holder.py Record.cost) and trims whole
	// records, so the base ring alone keeps 1 MiB / 71 = 14768 rows of 7 bytes,
	// not 1 MiB. The ceiling keeps the newest records whose cost fits it, so
	// it must hold what the newest hostedJobBytes of output can cost:
	//   - every record holds at least one byte of it, so the window spans at
	//     most 2^20 records, 64 bytes each: 64 MiB;
	//   - their data is the window's 2^20 bytes, plus up to 2^20-1 more in
	//     the one record straddling the window's start (a record is at most
	//     MAX_RECORD_BYTES = 1 MiB): 2 MiB - 1;
	//   - the exit record, 64 bytes, is the newest.
	// That sums to 66 MiB - 1 + 64, under 66 MiB + 64. Sent records are
	// reclaimed to the base ring at once, so the memory is transient: held
	// only while the relay lags, and by accounting at most this many bytes.
	hostedHolderRingCeiling = 66<<20 + 64
	// HostedMaxJobs bounds the jobs kept (running ones always stay; the
	// oldest finished ones go first) and HostedListMax one listing.
	HostedMaxJobs = 200
	HostedListMax = 50
	// noticeRetryEvery paces the retry of notices that could not be
	// admitted yet.
	noticeRetryEvery = time.Minute
)
