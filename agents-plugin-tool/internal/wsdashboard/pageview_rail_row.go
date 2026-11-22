package wsdashboard

import (
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// The rail row: RailRow, how an ended row revives, its tool-approval and host-exit
// readings, and railRow, which derives one row from a listed session.

// RailRow is one session.
type RailRow struct {
	// Hover is presentation-only reuse of this session's existing snapshot.
	Hover            railHover
	ReplyID, Harness string
	Live             bool
	Lifecycle        SessionLifecycle
	// Label is the session's display label, shown in place of ReplyID;
	// empty when it has none.
	Label string
	// Project is the registration's project folder. MailboxOpen is the
	// row's copy tag up to its closing (mailboxOpen), from the worktree the
	// row sits under, never a per-row git read: set by buildRailView once
	// that worktree's branch is known.
	Project, MailboxOpen string
	// Working reports a lead at work: a live adapter and a phase other than
	// idle and permission (a permission wait is the lead waiting on the
	// user). The index's working total counts permission waits; the rail
	// does not.
	Working bool
	// BackgroundTasks counts background commands or monitors the harness is
	// still running (taskBadgeCounts); shown on idle rows as "background".
	BackgroundTasks int
	// Subagents counts the running agents the lead itself spawned (level 1),
	// zero without a live adapter.
	Subagents int
	// runningAgents counts every running agent in the lead's snapshot, at
	// any depth (a grandchild a settled direct child started included),
	// zero without a live adapter: the full-idle gate (railSetBusy) reads
	// it, while Subagents keeps the level-1 badge count.
	runningAgents int
	// ExecJobs counts the live exec jobs this daemon runs whose worktree
	// root is the row's Project or holds it (railExecJobs). A job counts on
	// every row of its worktree, not only the session that launched it.
	ExecJobs int
	// OwnExecJobs counts the hosted exec jobs this session owns that still
	// run. Unlike ExecJobs they are known by their owning conversation, so
	// they count on this row alone.
	OwnExecJobs int
	// Busy reports a live session not yet fully idle, rendered as
	// data-busy="1": the lead works or waits on a permission (an approval
	// or a terminal's prompt), or a ws-known activity remains: a running
	// subagent, a background task, an exec job (ExecJobs, OwnExecJobs), or
	// a busy live child row at any depth. The rail layout sets it once the
	// children are nested (orderTree, railSetBusy). A harness that reports
	// no background task count (taskBadgeCounts clamps its unknown count to
	// zero) is fully idle once its turn ended and none of the rest remains,
	// so such a session may read idle while unseen background work still
	// runs.
	Busy bool
	// Active is Busy by the session's own activity, rendered as
	// data-active="1": everything Busy counts except ExecJobs, which are
	// known only by worktree and so also keep other sessions there busy;
	// the session's own hosted jobs (OwnExecJobs) count. A page notifies a
	// finish only for a busy span that was active at some point, so a
	// session busy only through another one's job never reads as finished.
	Active bool
	// permission is the lead's permission wait, from a present adapter,
	// for any session: a hosted one also has Approval, a terminal one waits
	// on its own prompt. Busy reads it.
	permission bool
	// CountPolicy is the board's open policy and check items.
	CountPolicy int
	// OpenAskIDs is the index's already-read set, exposed as data-asks.
	OpenAskIDs string
	// LastTool is the time of the lead's last tool call (epoch ms; 0 when
	// unknown). An ended session's stale snapshot still supplies it.
	LastTool int64
	// Message is the session's last user message or lead reply, one line;
	// User marks a user message.
	Message string
	User    bool
	// Hosted marks a lead session the daemon spawned and feeds
	// (Registration.Hosted).
	Hosted bool
	Exit   *HostExit
	// Approval is the tool approval a live hosted session waits on, which
	// the dashboard answers; nil when it waits on none.
	Approval *ApprovalWait
	// settingsWorktree is the settings ID of the worktree or plain folder
	// the rail placed the session under (buildRailView), which the settings
	// routes index (settingsTargets); SettingsWorktree reads it.
	settingsWorktree string
	// Failure is the typed failure (a login needed, a model unavailable, a
	// usage limit) that keeps a hosted session from working until a turn
	// succeeds, as its runner reports it (wslive.Host); nil when none.
	Failure *wslive.Notice
	// Starred marks a starred session whose star counts (the rail layout's
	// stars; a child's is inert while it is a child).
	Starred bool
	// The tree, as the rail layout sets it (orderRailView). Group is the
	// path of the worktree group the session belongs to (its worktree, or
	// a plain-folder workspace). Children are a live top-level row's live
	// children in their order, rendered under it; Child marks such a row,
	// and BranchTag names its worktree (its branch, else its folder) when
	// that differs from its parent's. StarHidden marks a stored child of a
	// live parent (an ended one too, in the ended fold): its star is inert,
	// so the row shows none. HasChildren marks a stored parent of a live or
	// an ended child, which can never become a child. KidApprovals and
	// KidWorking roll the children's approval waits and work up onto the
	// parent, shown while the page collapses it. KidBusy counts the
	// descendants at work (railKidBusy), shown beside the toggle whatever
	// the collapse state.
	Group        string
	Children     []RailRow
	Child        bool
	BranchTag    string
	StarHidden   bool
	HasChildren  bool
	KidApprovals int
	KidWorking   bool
	KidBusy      int
	// Revive is how the dashboard brings an ended session back, on a daemon
	// that hosts lead sessions: ReviveReopen for a closed hosted session,
	// ReviveTakeover for a session a terminal harness ran, empty when it
	// offers neither (a live session, a plain-folder session, a harness the
	// daemon does not host). ReviveBlocked names why the entry is shown
	// disabled (ReviveNoSession), empty when it is not.
	Revive, ReviveBlocked string
	// Archived marks an open hosted lead the user archived (the dashboard's
	// visibility sidecar, SessionArchived): it is drawn asleep in the
	// worktree's archived fold, never live, and offers unarchive instead of
	// archive. DeleteAt is the registry prune time displayed for a deleted
	// session (epoch ms), using its last sighting and RegistryRetention.
	Archived bool
	DeleteAt int64
}

const (
	// ReviveReopen and ReviveTakeover are RailRow.Revive's values; each is
	// the agent route the entry posts to (/agents/<id>/<value>).
	ReviveReopen   = "reopen"
	ReviveTakeover = "takeover"
	// ReviveNoSession blocks a takeover of a session whose record names no
	// harness conversation: its harness never reported one.
	ReviveNoSession = "nosession"
)

// HostExit is the recorded outcome of a stopped hosted harness.
type HostExit struct {
	HasCode, HasSignal bool
	Code, Signal       int
	Reason, StoppedAt  string
}

func hostExit(reg Registration) *HostExit {
	if !reg.Hosted || !reg.HostStopped {
		return nil
	}
	exit := &HostExit{Reason: reg.HostExitReason, StoppedAt: reg.HostStoppedAt}
	if reg.HostExitCode != nil {
		exit.HasCode = true
		exit.Code = *reg.HostExitCode
	}
	if reg.HostExitSignal != nil {
		exit.HasSignal = true
		exit.Signal = *reg.HostExitSignal
	}
	return exit
}

// railRevive is how an ended session comes back (RailRow.Revive). Whether
// a terminal session's harness has exited is asked when the entry is used:
// the route refuses a takeover while it may run.
func railRevive(in railInput) (revive, blocked string) {
	reg := in.Session.Registration
	switch {
	case !in.CanRevive || reg.Agent:
		return "", ""
	case reg.Hosted:
		if reg.EndedAt == "" {
			return "", ""
		}
		return ReviveReopen, ""
	case in.Repo == "" || (reg.Harness != "claude" && reg.Harness != "pi"):
		// A lead key lives at a git root, and the daemon hosts these two
		// harnesses alone.
		return "", ""
	case !in.Session.Resumable:
		return ReviveTakeover, ReviveNoSession
	}
	return ReviveTakeover, ""
}

// ApprovalWait is the tool approval a hosted session waits on, as a row
// shows it: the tool and its arguments, one line.
type ApprovalWait struct {
	Tool, Args string
}

// approvalWait is the approval a hosted session waits on: its lead's
// permission wait, as a present adapter reports it. Any other session's
// permission wait is answered in its terminal, and is none here.
func approvalWait(reg Registration, snap *wslive.Snapshot) *ApprovalWait {
	if !reg.Hosted || snap.Lead.Phase != wslive.PhasePermission {
		return nil
	}
	wait := &ApprovalWait{}
	if p := snap.Lead.Permission; p != nil {
		wait.Tool, wait.Args = p.ToolName, p.ArgsPreview
	}
	return wait
}

// railRow is one session's row.
func railRow(in railInput, live bool, now time.Time) RailRow {
	reg := in.Session.Registration
	lifecycle := DeriveSessionLifecycle(reg, in.Session.Hidden, now)
	archived := lifecycle == SessionArchived
	live = live && !archived
	row := RailRow{
		ReplyID:     reg.ReplyID,
		Label:       in.Session.Label,
		Project:     reg.Project,
		Harness:     reg.Harness,
		Live:        live,
		Lifecycle:   lifecycle,
		Archived:    archived,
		CountPolicy: in.Session.CountPolicy,
		OpenAskIDs:  in.Session.OpenAskIDs,
		Message:     in.Message.Text,
		User:        in.Message.User,
		Hosted:      reg.Hosted,
		Exit:        hostExit(reg),
		Hover:       sessionHover(reg, in.Snapshot, live, now),
		ExecJobs:    in.ExecJobs,
		OwnExecJobs: in.OwnExecJobs,
	}
	if !live {
		row.Revive, row.ReviveBlocked = railRevive(in)
	}
	if lifecycle == SessionPendingDeletion {
		if seen := reg.LastSeen(); !seen.IsZero() {
			row.DeleteAt = seen.Add(RegistryRetention).UnixMilli()
		}
	}
	snap := in.Snapshot
	if snap == nil {
		return row
	}
	row.LastTool = lastToolAt(snap.Lead)
	if row.Exit == nil && lifecycle != SessionStopped && adapterPresent(snap, now) {
		row.Working = leadWorking(snap.Lead.Phase)
		row.permission = snap.Lead.Phase == wslive.PhasePermission
		row.Subagents = levelOneRunning(snap.Agents)
		for _, a := range snap.Agents {
			if a.Status == wslive.StatusRunning {
				row.runningAgents++
			}
		}
		bg, mon := taskBadgeCounts(snap)
		row.BackgroundTasks = bg + mon
		if live {
			row.Approval = approvalWait(reg, snap)
		}
		if snap.Host != nil {
			row.Failure = snap.Host.Failure
		}
	}
	return row
}

// MailboxRef is the tag the row's copy button copies: for a session in a
// listed worktree, the very tag the head's name copies.
func (r RailRow) MailboxRef() string { return mailboxRef(r.MailboxOpen, r.Label) }

// railMailbox sets each row's copy tag from the worktree it sits under:
// root is that worktree's top level, branch its branch unless detached. A
// plain folder passes neither, as the head's name copies no root or branch
// outside a repository.
func railMailbox(rows []RailRow, root, branch string) {
	for i := range rows {
		r := &rows[i]
		r.MailboxOpen = mailboxOpen(r.ReplyID, r.Project, root, branch, r.Harness)
	}
}

// InSessionList reports a row that keeps its normal order and nesting.
// Heartbeat liveness is tracked separately for the live counters and controls.
func (r RailRow) InSessionList() bool {
	return r.Live || r.Lifecycle == SessionActiveOn || r.Lifecycle == SessionAsleep || r.Lifecycle == SessionStopped
}

// Stopped reports a row drawn with a muted title and harness icon.
func (r RailRow) Stopped() bool {
	return r.Archived || r.Lifecycle == SessionAsleep || r.Lifecycle == SessionStopped
}

// leadWorking reports a phase in which the lead works: anything but idle
// (or none) and a permission wait.
func leadWorking(phase string) bool {
	return phase != "" && phase != wslive.PhaseIdle && phase != wslive.PhasePermission
}

// lastToolAt is the time of the lead's last tool call: over its batches
// that called a tool, the running call's start, else a finished batch's end
// (its start when no end is known). A message still generating is no call.
func lastToolAt(lead wslive.Lead) int64 {
	var last int64
	for _, b := range lead.Batches {
		var t int64
		switch {
		case b.Running != nil:
			t = b.Running.StartedAt
			if t == 0 {
				t = b.StartedAt
			}
		case b.Generating != nil || b.CallCount == 0:
			continue
		default:
			t = b.EndedAt
			if t == 0 {
				t = b.StartedAt
			}
		}
		last = max(last, t)
	}
	return last
}

// levelOneRunning counts the running agents the lead spawned itself. Both
// adapters (Claude and Pi) send a lead-spawned agent without a parent id, so
// that is level 1; an agent whose parent left the list (retention keeps
// every running agent, so only a malformed snapshot does this) counts as
// level 1 too, as subagentCounts counts it.
func levelOneRunning(agents []wslive.Agent) int {
	return subagentCounts("", agents, sessionLookup{}, time.Time{}).Direct
}
