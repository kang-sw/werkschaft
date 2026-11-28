package wsagent

import (
	"os"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// conv is one open conversation. Its loop goroutine owns the process and
// every conversation write but Send's inbox write; the runner heartbeat
// sweep may refresh only this conversation's registration heartbeat. Other
// goroutines read through state(), act through do(), and tell the loop of an
// inbox change through signalInbox().
type conv struct {
	r             *Runner
	id            string // immutable ReplyID set at startConvWith; safe to read without mu
	dir           string
	inbox         string
	live          *liveWriter
	summaryGap    bool
	summaryReason string
	reqs          chan func()
	// inboxC (one slot) tells the loop the inbox changed (signalInbox).
	inboxC chan struct{}
	done   chan struct{}

	// closedOrParked and awakeMirror mirror lock-guarded lifecycle/awake state
	// (updated via syncStateLocked under mu) so lock-free readers (OwnsLive,
	// awakeChildrenLocked) never acquire c.mu.
	closedOrParked atomic.Bool
	awakeMirror    atomic.Bool

	// mu guards what state() reads: rec and the status fields below. Send
	// holds it across its closed check and inbox write; close sets closed
	// under it before it drops the held messages.
	mu            sync.Mutex
	rec           Record
	proc          Process
	turn          bool
	lastErr       string
	closed        bool
	held          int // inbox messages, maintained at inbox changes/drains under mu
	hidden        bool
	// parkedOut is set under mu when the conversation left memory for the
	// runner's parked set (runner_park.go); its loop ends right after.
	parkedOut     bool
	contextTokens *float64
	// contextWindow and contextCompaction are the context scale of the
	// latest status that knew its window, kept while a later one does not
	// (State.ContextWindow, State.ContextCompaction).
	contextWindow     int64
	contextCompaction *wslive.UsageCompaction
	outputTps         *float64
	observed          *wslive.ModelObservation
	observations      []wslive.ModelObservation
	batches           []wslive.BatchView
	// rejoinKeyPending retries a failed attach's key check on State reads
	// until it succeeds; healthy reads never touch the key store.
	rejoinKeyPending bool
	// approvals are a lead's waiting tool permission prompts, oldest first;
	// activeMode and bgTasks are what its harness last reported.
	approvals []ApprovalRequest
	// execAnswers are the daemon-owned exec approvals among approvals
	// (ApproveExec) by request id, each with the channel its waiting call
	// reads; loop-owned.
	execAnswers map[string]chan execAnswer
	// acceptedEvents is the set AdmitCompletion dedupes on, loaded at its
	// first use from Record.AcceptedEvents and the inbox (nil before).
	acceptedEvents map[string]struct{}
	// mcpCalls counts the ws MCP tool calls in flight on the conversation's
	// session token (Runner.MCPCallBegin).
	mcpCalls atomic.Int64
	// execCutover reports whether the current process started under the
	// exec cutover (exec_cutover.go): its native process tools removed in
	// favour of the ws exec tools. Set at each start and rejoin.
	execCutover bool
	// convHistoryAccessEffective is the conv_history_access snapshot bound
	// when the current process attached its MCP token. Set at each start
	// and rejoin under mu.
	convHistoryAccessEffective bool
	// unrequestedCmdWarn records a user-visible advisory when a native command
	// ran without a permission request under the exec cutover; cleared when a
	// new process starts.
	unrequestedCmdWarn string
	// launchBinary fingerprints the binary the current process launched
	// from (harnessBinaryOf), "" for one this daemon did not launch or a
	// backend without a cutover mechanism. Set with execCutover.
	launchBinary string
	// cutoverNudge is the current turn's first refused or unrequested
	// native command under the cutover, whose instruction prompt the
	// turn's end sends (sendCutoverInstruction); loop-owned.
	cutoverNudge string
	// cutoverInstructing reports that a turn's instruction prompt is on
	// its way to the inbox, or in it and not yet taken by a turn: a
	// worker's done report waits for the turn it starts (holdsReport);
	// loop-owned.
	cutoverInstructing bool
	// cutoverQueued: the instruction was admitted to the inbox while
	// cutoverInstructing holds, so its absence from the held entries means
	// it left (cutoverInstructionGone); loop-owned.
	cutoverQueued bool
	// cutoverInstructed counts the instruction prompts the current process
	// was sent (cutoverInstructionCap); loop-owned, reset at each start
	// and rejoin.
	cutoverInstructed int
	activeMode        string
	// dashboardControlEffective is the running lead process's attach-time
	// snapshot of Record.DashboardControlAccess; under mu.
	dashboardControlEffective bool
	bgTasks           int
	background        *int
	monitors          *int
	// actionNeeded is State.ActionNeeded.
	actionNeeded string
	// Worker permission projection is transient. Explicit selection lives in
	// permission.json; a legacy-derived value is recomputed at each launch.
	workerPermissionLevel  PermissionLevel
	workerPermissionSource string
	// failure and mcpBroken are State.Failure and State.MCPBroken.
	failure   *wslive.Notice
	mcpBroken []string
	// usageLimitSettled: the last turn to settle had a usage-limit alert
	// (turnSettled), for publish's exhaustion check (quota_hop.go);
	// exhausted holds that report's notice by pending report id, and
	// exhaustReport/exhaustNotice by Record.ReportID, until delivered;
	// parkedReports counts the Record.Reports a hosted lead keeps for its
	// scheduled continuation (reportsParked). All loop-owned and in-memory.
	usageLimitSettled bool
	exhausted         map[string]wslive.Notice
	exhaustReport     string
	exhaustNotice     wslive.Notice
	parkedReports     int
	// caps is the catalog profile resolved for the process's harness
	// version and demoted by its init shape (harnessReported); nil for a
	// backend without a profile or before the first report. commands is
	// the harness's last command list before allowedCommands took the
	// commands of unsupported items out of it.
	caps     *Capabilities
	commands []wslive.SlashCommand
	// heldKinds memoizes refreshHeld's reads under mu: held entry name ->
	// whether it is a message (not an interrupt entry).
	heldKinds map[string]bool

	// Compaction state is loop-owned, never persisted per observation.
	compactPending                            *compactRequest
	compacting, compactResume, compactCommand bool
	compactC                                  <-chan time.Time
	compactStop                               func()
	compactNote                               string
	// passive is the pending hand-off of dashboard-run exec results
	// (passive_input.go); passiveTook is what the send being built took,
	// set by turnInput in the loop and acknowledged when that send succeeds.
	passive     passiveInput
	passiveTook []passiveTake
	// wbRev is the lead's board revision when the running turn started, valid
	// when wbRevOK (whiteboard_only.go). Loop-owned.
	wbRev   uint64
	wbRevOK bool

	// compactOrigin is the origin (wslive.CompactOriginAuto or Manual) of the
	// compaction ws itself requested, from its dispatch until its end or
	// failure; empty while none runs or the harness compacts on its own. It is
	// loop-owned.
	compactOrigin string
	// compactRotated is true while rotateExternalSession dispatches CompactEnd
	// for a freshly started process that already received composed instructions.
	compactRotated bool
	// cmpActive, cmpCount and cmpLast mirror the compaction state State shows
	// (Compacting, Compactions, CompactOrigin) under mu, as the loop-owned
	// fields above are not readable there.
	cmpActive bool
	cmpCount  int
	cmpLast   string

	// A nudge and any requested post-compaction continuation have no parent result.
	compactNudge                               compactionNudge
	compactInternalTurn, compactResumeInternal bool

	compactAdvisory, compactHard wsconfig.Threshold
	// Lead mode is also read by MCP discovery, outside the conversation loop.
	compactModeMu  sync.Mutex
	compactModeGen uint64
	// compactWorkerMode caches compaction.worker_mode under its own generation.
	compactWorkerMode                                                                                              string
	compactWorkerModeGen                                                                                           uint64
	compactLeadMode                                                                                                string
	compactTokens, compactWindow                                                                                   float64
	compactPrevTokens, compactPrevWindow                                                                           int64
	compactGen                                                                                                     uint64
	compactLoaded                                                                                                  bool
	compactUsageFresh, compactAdvised, compactM1Issued, compactM2Issued, compactHardIssued, compactAdvisoryEnabled bool
	compactAwaitTurn                                                                                               bool
	compactHardSteered, compactHardHandled, compactHardAtStart                                                     bool
	compactHardSteerID                                                                                             string
	// Stage 2 hard-cap overflow: post-steer model-step boundaries seen since
	// the first hard steer, whether a fresh post-steer usage update arrived
	// before the next tool boundary, daemon-initiated interrupt vs cap abort
	// (both distinct from interruptAsked), direct-rotation request, per-turn
	// auto-rotation count, and consecutive compactions that left usage >= hard.
	compactHardBoundaries, compactNoShrink int
	compactHardStepPending                 bool
	compactHardInterrupt, compactHardAbort bool
	compactHardForce                       bool
	compactTurnRotations                   int
	compactPostRotationCheck               bool
	compactPostRotationNoShrink            int
	compactContinueAtIdle, compactIdleInternal                                                                     bool
	replayCompactEnded, replayCompactFailed                                                                        bool
	compactStage1Active, compactStage1Resume, compactStage1Internal, compactStage1Auto, compactStage1UsageLimited  bool
	// Account-slot boundary state (loop-owned, in-memory only).
	accountSlotBoundaryPending bool
	slot2aVerifyPending        bool
	slot2aFallbackRetried      bool
	// deferRecordPersist suppresses disk record writes under wmu while an
	// in-place harness switch launches the target process before atomic commit.
	deferRecordPersist bool

	// Agy model-wait watchdog state (loop-owned, in-memory only).
	modelWaitTimeout            time.Duration
	modelWaitMaxRetries         int
	modelWaitArmed              bool
	modelWaitRetries            int
	modelWaitPending            int
	modelWaitCancelling         bool
	modelWaitFailErr            string
	modelWaitDiagnostic         string
	modelWaitExhaustedPublished bool
	modelWaitFailureClasses     []string
	modelWaitFailureExcluded    bool
	modelWaitC                  <-chan time.Time
	modelWaitStop               func()
	modelWaitFailure            string
	modelWaitDrainC             <-chan time.Time
	modelWaitDrainStop          func()
	modelWaitCancelC            <-chan time.Time
	modelWaitCancelStop         func()
	modelWaitBackoffC           <-chan time.Time
	modelWaitBackoffStop        func()
	modelWaitBackoffFailure     string
	// A hosted agy lead's log-confirmed stall cutoff (agy_watchdog.go):
	// modelWaitLogCheck is agy.stall_log_check, modelWaitBoundary the wall
	// time ws last started a fresh model request, modelWaitContent whether
	// model content arrived since, agyLog the process's worker log, and
	// agyLogNoted whether this session logged its one unknown-log notice.
	// agyFrameTrace is the frame capture file when agy.http2_frame_trace
	// is on.
	modelWaitLogCheck bool
	modelWaitBoundary time.Time
	modelWaitContent  bool
	agyLog            *agyStallLog
	agyLogNoted       bool
	agyFrameTrace     string
	// modelWaitBackoffFence is the only model-wait field shared with Send:
	// pending -> cancelled lets a successful inbox write win before it
	// returns; pending -> firing commits the automatic continue instead.
	modelWaitBackoffFence atomic.Uint32

	// Hosted-lead quota continuation scheduler state (quota_continuation.go).
	quotaC                 <-chan time.Time
	quotaStop              func()
	quotaUsageLimitAlerted bool
	quotaUsageLimitHandled bool
	quotaInjecting         bool
	quotaPendingFire       bool
	quotaCancelFence       atomic.Uint32
	quotaInputGen          atomic.Uint64
	quotaArmedInputGen     uint64
	// Test-only hooks for quotaContinuationTimedOut race windows (nil in production).
	onQuotaAfterFence func()
	onQuotaBeforeSend func()
	// Test-only hook to inject record persistence errors (nil in production).
	onPersistRecord func(Record) error

	// loop-owned, except registration timing below.
	events          <-chan Event
	idle            *time.Timer
	idleAfter       time.Duration
	bgGraceTimer    *time.Timer
	dirtyModel      bool
	dirtyEffort     bool
	dirtyMode       bool
	dirtyPermission bool
	// reconcileMode marks replay-reconstructed intent, not a new user setting.
	// Failed reconciliation is advisory; it must not kill an unenforceable lead.
	reconcileMode bool
	// regMu is leaf-only: it guards the timestamp in memory and is never held
	// across a registration lock or file I/O.
	regMu       sync.Mutex
	lastReg     time.Time
	lastWritten wsdashboard.Registration
	haveWritten bool
	rowStampSet bool
	rowStampV   rowStamp

	// A lead's or spawned child's mail watch. mailC (one slot) tells the loop
	// the mailbox may hold mail (signalMail); stopMail and mailStopped are
	// guarded by c.mu so late Watch installs after loop exit stop immediately.
	mailC       chan struct{}
	stopMail    func()
	mailStopped bool
	// spawnC coalesces daemon-owned descendant changes for this writer.
	spawnC     chan struct{}
	spawnDirty bool
	spawnNext  time.Time
	spawnTimer *time.Timer
	// spawning is set from the row write that precedes a spawn until the
	// spawn settled: the row then names no process and is not stopped, so
	// a terminal login is refused meanwhile (Registration.HostMayRun).
	// foreignRow is set once the session's row was found to be a terminal
	// harness's (lockRow, checkRow); the loop then lets the session go
	// (settle).
	spawning bool
	// foreignRow is monotonic for this conversation generation. rowHeld is an
	// in-memory hint that lets the heartbeat sweep skip a row whose ordinary
	// owner already holds its flock, without touching the lock file.
	foreignRow atomic.Bool
	rowHeld    atomic.Int32
	// rowReads counts lockRowRead passes (the registration lock and file
	// read); ownerProof is the hosted row identity the last one proved
	// (lockSummaryWriter), which a RAM-only publish relies on
	// (lockSummaryWriterRAM).
	rowReads   atomic.Int64
	ownerProof atomic.Pointer[hostedOwnerProof]
	// replaying is set from a rejoin of the holder's process (reattach)
	// until its replay ends: no turn is submitted meanwhile, since the
	// replay is what tells whether one runs.
	replaying bool
	// agyNativeReplay is what the running replay staged of agy native rows
	// (stageAgyNativeReplay, resolveAgyNativeReplay).
	agyNativeReplay agyNativeReplayRows
	// eventSeq is the holder record sequence of the event being handled (0
	// for the daemon's own child or a synthetic event).
	eventSeq uint64
	// rejoinErr is a worker rejoin's start failure: checkReattach ends that
	// recovery after the scan instead of waiting on a replay with no process.
	rejoinErr       string
	replayAsked     bool
	replayTurnError string
	// replayGap is set when records left the holder's ring during the
	// replay, and replayBoundary when the replay showed a turn begin or
	// end after the last such loss. wrapHold is set from a rejoin that lost
	// records with no turn boundary after them (replayDone) until the
	// harness's next turn end: the turn is taken as running, since its
	// start may be among the lost records, and nothing is submitted. A
	// harness that can tell is asked whether a turn runs (probeWrapHold);
	// wrapTurnSeen marks a turn start seen since the ask.
	replayGap      bool
	replayBoundary bool
	wrapHold       bool
	wrapTurnSeen   bool
	// seq is the last holder record sequence applied whole; seqDirty that
	// the record does not hold it yet. live.ringDirty makes a ring line's
	// end persist it at once.
	seq      uint64
	seqDirty bool
	// lineOpen is the holder record sequence of a line some of whose events
	// are applied and whose LineEnd is not; 0 between lines.
	lineOpen uint64
	// harnessState is the state the harness last reported; actionTimer runs
	// while it waits on the user with no prompt to show (checkAction).
	harnessState string
	actionTimer  *time.Timer
	// leverC is the lever hold's cap timer and leverStop its stop: armed
	// at the end of a turn that carried a successful compaction lever call
	// (Event.CompactRequested), it holds kick until that compaction starts
	// (its CompactStart), leverHoldCap passes, the next turn ends, or the
	// process exits. interruptAsked is set when the runner interrupted the
	// running turn, until its end: an interrupted lever turn does not arm.
	leverC         <-chan time.Time
	leverStop      func()
	interruptAsked bool
	// The process's MCP recovery (runner_recovery.go). mcpSeen are the
	// servers it reported connected; mcpBroken (above) are those that broke
	// and did not come back in-stream, and a restart is due at the next
	// idle point (mcpRestartDue). mcpRestarted outlives the process: the
	// servers an automatic
	// restart was for that have not connected since. mcpChecking runs
	// while a check is off the loop, and mcpAgain asks for one more after
	// it, for mcpAgainNames.
	mcpSeen      map[string]bool
	mcpRestarted map[string]bool
	// mcpStale is a rejoined process whose ws-mcp route (Record.MCPURL)
	// this daemon does not serve; it restarts at the first idle point.
	mcpStale bool
	// Backend-filtered hello environment captured at attach, before token restore.
	workerMCPEnv []string
	// workerRejoined marks that checkReattach already ran the once-per-attached-process
	// worker rejoin setup (restoreToken, restoreWorkerMCP, events wiring, allowHolderReplay)
	// so a Holders.Status retry only re-issues Status without voiding the restored MCP token.
	workerRejoined bool
	workerRetiring bool // validated downgrade retirement retries before replay
	// crossHarnessRejoin is a held worker admitWorkerRecord refuses (a
	// cross-harness worker) that reattach attached for the ownership match only. checkReattach ends a matching
	// holder (retireCrossHarnessWorker) and never stops a mismatched one.
	crossHarnessRejoin bool
	// crossHarnessRetiring is a matched cross-harness holder whose stop failed; each
	// heartbeat's checkReattach retries it until it ends.
	crossHarnessRetiring bool
	mcpChecking          bool
	mcpAgain             bool
	mcpAgainNames        []string
	// retry is the API request retry under way; alerted that the running
	// turn brought a typed failure, so its end clears none.
	retry   *wslive.Retry
	alerted bool
	// sendRetried is set when a turn's failed send armed its one automatic
	// retry (kick) and cleared by the next send that succeeds; a send that
	// fails again meanwhile waits for the next inbox or mail signal.
	sendRetried bool
	// durableRetried is the turn already given an automatic recovery attempt
	// after a submission, process or settlement failure in this daemon.
	durableRetried   string
	inboxClaimsAcked bool
	// systemPrompt is agent.spawn's system prompt (Record.SystemPrompt),
	// read once from the session directory; loop-owned.
	systemPrompt string
	// ambient is the fully rendered hosted-lead turn suffix. It is resolved
	// at conversation creation/restoration and refreshed by config.tune,
	// never by the send path.
	ambient string
	// ambientLang is the workflow.lang value ambient was rendered from,
	// published with it for readers off the loop; nil before the first
	// resolution.
	ambientLang atomic.Pointer[string]
	// expiring is set while a child-agent-waiting expiry is dispatched
	// (expireIfDue) and not yet decided; loop-owned.
	expiring bool
	// wakeRefused is set while the last process start was refused by
	// permission admission (childAdmission): the input stays, and the
	// child counts as asleep. Under mu.
	wakeRefused bool
	// releasedCheckout is the checkout whose completed release ended this
	// spawned conversation's binding, once the runner knows it (from the
	// runner's in-memory releases, a release's notice, or a refused start);
	// it then takes no input there, even while its process lives. It is
	// never cleared. Under mu.
	releasedCheckout string
	// inputMu fences input against a release notice: the loop holds it
	// from its last release check until a turn or steer has been handed to
	// the backend (admitInput), and the notice holds it while it marks the
	// conversation released (noteReleased). The conversation's own lock;
	// only c.mu is ever taken under it.
	inputMu sync.Mutex
	// canonRoot and canonCwd are the record's Root and effective working
	// directory (Record.workDir) with symlinks resolved
	// (canonicalWorktreePath), once, in startConv; both are fixed for a
	// conversation's life. Immutable after startConv.
	canonRoot, canonCwd string
	// checkouts are the checkouts this spawned conversation is bound to
	// (resolveCheckouts), resolved once when it is loaded or spawned; nil for
	// a conversation no release can end. Immutable after startConv.
	checkouts []string
	// starting is set from the moment a process start passed the release
	// check until that start finished or failed: occupancy counts the
	// conversation live meanwhile, so no acquisition reuses its checkout
	// under a backend that is still starting. Under mu, which the release
	// check and the occupancy read share.
	starting bool
	// heldSenders are the senders of mail claimed into the record and not yet
	// delivered or told of a refused start, in claim order: a refusal found
	// after the claim (a start deferred past the holder scan, a later kick)
	// still reaches each of them, once. Memory only. Loop-owned.
	heldSenders []string
	// deliverySenders tracks direct Deliver sends that have neither reached a
	// turn nor already been refused. It is keyed by send coverage under mu.
	deliverySenders map[uint64]pendingDelivery
	// spawnPending is true from registration in r.convs until spawned()'s
	// first Send (or close), so awakeLocked counts a newly registered spawn
	// with no gap after pendingSpawns handoff. Under mu.
	spawnPending bool
	// Keep-awake membership (keep_awake.go), loop-owned: awakeMember is what
	// this instance last published, awakeSending is set while a standalone
	// prompt's send is in flight.
	awakeMember  bool
	awakeSending bool
	// loopBusy mirrors loop-owned safe-stop blockers (replaying, bgGraceTimer,
	// compacting, compactCommand) for lock-free reads off the loop.
	loopBusy atomic.Bool

	// Turn settles (spawn.go). sends counts Runner.Send's messages and
	// settled is the last settled turn, settleC closed and replaced at each
	// settle and at close; voided is the sends no turn will take, since
	// the page cancelled them before a drain (voidCancelled); all four
	// under mu. drained is the sends the last drain covered, turnMark the
	// sends the running turn's messages covered, and reply its last reply
	// text; all three loop-owned. quitting is set at the daemon's
	// shutdown, whose cut-off turn the next daemon reports instead.
	// quitting is set at the daemon's shutdown, whose cut-off turn the next
	// daemon reports instead. kickDeferred marks a processless conversation
	// whose start kick was deferred until StartForPublication's serving channel
	// closes.
	sends        uint64
	pageInputs   uint64
	settled      TurnResult
	settleC      chan struct{}
	voided       uint64
	drained      uint64
	turnMark     uint64
	reply        string
	quitting     bool
	kickDeferred atomic.Bool

	// wmu orders the record writes of update, which other conversations'
	// goroutines also make (children.go takes a delivered report).
	wmu sync.Mutex
	// recordDirty is guarded by wmu. Only Send's coverage/activity stamp waits;
	// consumed input and replay-dedupe mutations still persist immediately.
	recordDirty bool

	// The spawn tree (children.go). settledReport is the pending report id
	// of the last settled turn, and parentWaits counts the conversation
	// parent's waits by target, and holding mirrors heldRes != nil; all
	// three under mu. Loop-owned: heldRes is a
	// done turn held for its children's reports; steered are the
	// reports and user messages steered into the running turn and not yet taken
	// by it, steerSeq the steer commands the process was written and
	// steerAnswers how many it answered.
	settledReport string
	parentWaits   map[uint64]int
	holding       bool
	holdingShell  bool // protected by mu, refreshed by the loop
	heldRes       *TurnResult
	steered       []steeredReport
	steerSeq      int
	steerAnswers  int
}

// do runs fn in the loop and waits for it; false when the loop has ended. The
// wrapper closes ran in a defer, so a fn that exits its goroutine (a test's
// t.Fatal runs runtime.Goexit) still releases the caller instead of leaving it
// parked until the package timeout; the loop goroutine itself is then gone.
// Tests copy values out inside fn and assert on their own goroutine.
func (c *conv) do(fn func()) bool {
	ran := make(chan struct{})
	select {
	case c.reqs <- func() { defer close(ran); defer c.syncLoopBusy(); fn() }:
		<-ran
		return true
	case <-c.done:
		return false
	}
}

func (c *conv) syncLoopBusy() {
	c.loopBusy.Store(c.replaying || c.bgGraceTimer != nil || c.compacting || c.compactCommand)
}

// safeStopSelfLocked reports whether this conversation's own process is safe to
// stop right now (ignoring descendants, which must be checked without holding
// c.mu). Caller holds c.mu.
func (c *conv) safeStopSelfLocked() bool {
	if c.closed || c.proc == nil || !isHolderBacked(c.proc) {
		return false
	}
	if len(c.approvals) != 0 || c.turn || c.holding || c.bgTasks > 0 || c.cmpActive || c.ownsLiveExec() {
		return false
	}
	if c.loopBusy.Load() || c.live.hasRunningNativeAgents() {
		return false
	}
	return true
}

// signalInbox tells the loop the inbox changed, without waiting; signals
// sent before the loop takes one fold into it.
func (c *conv) signalInbox() {
	select {
	case c.inboxC <- struct{}{}:
	default:
	}
}

// signalMail tells the loop the session's mailbox may hold mail, without
// waiting.
func (c *conv) signalMail() {
	select {
	case c.mailC <- struct{}{}:
	default:
	}
}

// mailChanged claims the session's waiting mail and delivers it: steered
// into the running turn when the session takes a message mid-turn
// (steersMail), else into the record, so a message taken from the mailbox
// survives until it is delivered, and submitted with the held messages
// (kick), which wakes a sleeping child past its parent's width cap. Mail a
// sleeping child's refused wake keeps stays in the record, and each of its
// senders is told once per claim (Options.WakeRefused). It runs in the
// loop.
func (c *conv) mailChanged() {
	if c.closed || c.hidden || c.r.opts.Mail == nil {
		return
	}
	rec := c.record()
	relation := func(sender string) string { return c.r.mailRelation(rec, sender) }
	var mail []ClaimedMail
	var err error
	if rec.Lead {
		// The claim and the check that the session is still the daemon's
		// are one step: a terminal login that takes the row waits for the
		// lock, and what was claimed before it goes back to the mailbox
		// (release).
		unlock, ok := c.lockRow()
		if !ok {
			return
		}
		mail, err = c.r.opts.Mail.Drain(rec.Root, rec.WSKey, relation)
		unlock()
	} else {
		// A child's row is never a terminal's: its claim touches no file.
		mail, err = c.r.opts.Mail.Drain(rec.Root, rec.WSKey, relation)
	}
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: mailbox: %v\n", rec.ReplyID, err)
	}
	// A conversation whose checkout was released steers nothing into a
	// turn that outlived the release. Its release state was settled when it
	// was loaded, so a claim made before the first holder scan knows it too.
	released := c.released()
	var held []string
	for _, m := range mail {
		// Once a steer fails, the rest waits too, so the batch keeps its
		// order. A held text keeps Drain's text, without a legend; its
		// sender is kept beside it, in memory, for a refusal found later.
		if released != "" || len(held) > 0 || !c.steerMail(m) {
			held = append(held, m.Text)
			c.holdSender(m.Sender)
		}
	}
	if len(held) > 0 {
		c.update(func(rec *Record) {
			rec.Mail = append(rec.Mail, held...)
			if rec.Parent != nil {
				// Any mail resets a spawned child's waiting expiry; a
				// steered one rides a turn, whose settle resets it.
				rec.LastActiveAt = c.r.opts.Now().UTC().Format(time.RFC3339)
			}
		})
	}
	if len(mail) > 0 {
		// The claim changed the session's mail history and held count. A
		// turn that runs submits nothing now, so nothing else would tell
		// the page.
		c.r.notify(rec.ReplyID, rec.Root)
	}
	if (len(mail) > 0 || len(c.rec.Mail) > 0) && c.hasQuotaContinuation() {
		c.cancelQuotaContinuation(false)
	}
	// Mail claimed earlier and not yet delivered (a start that failed, a
	// daemon that ended) goes with it: nothing else would submit it.
	if len(c.rec.Mail) > 0 {
		c.kick()
	}
	if released == "" {
		// A release that landed after the snapshot refused a steer above
		// (admitInput); its senders are told too.
		released = c.released()
	}
	if released != "" {
		// Told whether or not the kick reached a start: a running turn, or
		// a start deferred past the first holder scan, defers the kick, and
		// the refusal does not wait for it.
		c.tellHeldSenders(released, checkoutReleasedRefusal(released).Error())
	} else if len(held) > 0 {
		c.tellRefusedWake()
	}
}

// tellRefusedWake tells each sender of held mail, claimed for a sleeping
// child whose wake admission refused, that its mail waits
// (Options.WakeRefused). It runs in the loop.
func (c *conv) tellRefusedWake() {
	c.mu.Lock()
	refused, reason, checkout := c.wakeRefused, c.lastErr, c.releasedCheckout
	c.mu.Unlock()
	if !refused {
		return
	}
	c.tellHeldSenders(checkout, reason)
}

// holdSender records the sender of one pending message. It runs in the loop.
func (c *conv) holdSender(sender string) {
	if sender == "" {
		return
	}
	c.heldSenders = append(c.heldSenders, sender)
}

// tellHeldSenders tells each sender of held mail not yet told, once, that
// conversation c refused its start (checkout set: by that checkout's
// release). It runs in the loop.
func (c *conv) tellHeldSenders(checkout, reason string) {
	senders := c.heldSenders
	c.heldSenders = nil
	if len(senders) == 0 {
		return
	}
	dead := c.r.deadBindingRefusal(c.record(), checkout)
	seen := make(map[string]bool)
	for _, sender := range senders {
		if dead != nil {
			c.r.tellUndeliverable(sender, c.rec.ReplyID, dead)
		} else if !seen[sender] {
			c.r.tellRefused(sender, c.rec.ReplyID, checkout, reason)
			seen[sender] = true
		}
	}
}

// steersMail reports whether a mail claimed now reaches the running turn
// as a steer rather than waiting for the next turn: a turn runs that
// takes a message mid-turn, and the harness's catalog profile, when it has
// one and reported, does not grade steer unsupported. A lead steers
// through its steer handle, a spawned child through the acknowledged
// report path (reportSteerer) that keeps its report covering the message.
func (c *conv) steersMail() bool {
	if !c.turn || c.proc == nil || c.replaying || c.wrapHold || c.closed || c.hidden || c.compacting || c.compactCommand || c.compactInternalTurn {
		return false
	}
	c.mu.Lock()
	caps := c.caps
	c.mu.Unlock()
	if caps != nil && !caps.Allows(ItemSteer) {
		return false
	}
	ctl := controlsFor(c.proc)
	if c.rec.Lead {
		return ctl.steer != nil
	}
	return ctl.reports != nil
}

// steerMail steers one claimed mail into the running turn when the session
// takes it now (steersMail), and reports whether it did; a failed steer
// leaves it for the next turn. The steer is a submission of its own, so it
// carries the legend of its own sender (mailLegend). It runs in the loop.
func (c *conv) steerMail(m ClaimedMail) bool {
	if !c.steersMail() {
		return false
	}
	text := c.mailLegend([]string{m.Text}, []string{m.Sender})[0]
	ctl := controlsFor(c.proc)
	if p, ok := c.proc.(durableSteerProcess); ok && c.rec.DurableTurn != nil {
		queued := false
		c.admitInput(func() {
			queued = c.queueDurableSteer(p, durableSteer{Text: text, Mail: m.Text}, nil, []wslive.Event{*wakeEvent("mail", "mailbox", "", text).Live})
		})
		return queued
	}
	if c.rec.Lead {
		if err := ctl.steer.Steer(text); err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: steer a mail: %v\n", c.rec.ReplyID, err)
			return false
		}
		c.modelWaitSteered()
		c.apply(*wakeEvent("mail", "mailbox", "", text).Live)
		return true
	}
	// Persist before the holder write. The child can accept the steer and
	// outlive this daemon before its acknowledgement reaches the runner.
	// Text is the labeled steer the child's taken messages match; Mail
	// keeps Drain's text for a requeue (steeredReport.requeued).
	c.steerSeq++
	seq := c.steerSeq
	steered := steeredReport{Text: text, Seq: seq}
	if text != m.Text {
		steered.Mail = m.Text
	}
	c.steered = append(c.steered, steered)
	c.saveSteered()
	var err error
	if refusedBy := c.admitInput(func() { err = ctl.reports.Steer(text) }); refusedBy != "" {
		// A release landed after mailChanged's snapshot: the mail is held
		// and its sender told (mailChanged).
		err = checkoutReleasedRefusal(refusedBy)
	}
	if err != nil {
		c.steered = slices.DeleteFunc(c.steered, func(s steeredReport) bool { return s.Seq == seq })
		c.saveSteered()
		fmt.Fprintf(c.r.opts.Log, "agent %s: steer a mail: %v\n", c.rec.ReplyID, err)
		return false
	}
	c.apply(*wakeEvent("mail", "mailbox", "", text).Live)
	c.changed(false)
	return true
}

// mailLegend is texts as one submission of claimed mail carries them, with
// the sender legend Options.MailLegend places; texts itself without one.
// senders are Drain's sender reply-ids, nil for a turn's held texts and
// reports. The result is only submitted (and shown as the submission's
// wake or user entry), never stored.
func (c *conv) mailLegend(texts, senders []string) []string {
	if len(texts) == 0 || c.r.opts.MailLegend == nil {
		return texts
	}
	if out := c.r.opts.MailLegend(c.rec.Root, texts, senders); len(out) == len(texts) {
		return out
	}
	return texts
}

// inboxChanged takes the inbox's interrupt requests and submits the held
// messages, starting the process when needed (kick). It runs in the loop.
func (c *conv) inboxChanged() {
	c.refreshHeld()
	c.cutoverInstructionGone()
	c.mu.Lock()
	held := c.held
	c.mu.Unlock()
	if c.modelWaitBackoffC != nil && held > 0 {
		c.cancelModelWaitBackoff()
	}
	if !c.quotaInjecting && held > 0 && c.hasQuotaContinuation() {
		c.cancelQuotaContinuation(false)
	}
	c.checkInterrupt()
	c.steer()
	c.kick()
}

// inboxMutated wakes readers only after an actual inbox mutation.
func (c *conv) inboxMutated(n int) {
	if n == 0 {
		return
	}
	if slot, ok := c.live.currentSlot().(wsdashboard.InboxMutationSlot); ok {
		slot.InboxMutated()
	} else {
		c.r.notify(c.rec.ReplyID, c.rec.Root)
	}
}

// steer writes steered messages to the process while a turn runs:
// the user marked them to reach the harness now, not at the turn's end.
// They join the running turn, so no turn starts. Messages not marked stay
// held, with no turn running a marked one is an ordinary send (kick), and
// a backend that takes no message mid-turn, or a rejoin that cannot tell
// whether the turn runs (wrapHold), keeps it held.
func (c *conv) steer() {
	if !c.turn || c.proc == nil || c.replaying || c.wrapHold || c.closed || c.hidden || c.compacting || c.compactCommand || c.compactInternalTurn {
		return
	}
	if c.released() != "" {
		// A turn that outlived its checkout's release takes no steer; the
		// sender heard the refusal from Deliver or the mail claim.
		return
	}
	if !c.rec.Lead {
		c.steerWorker()
		return
	}
	if p, ok := c.proc.(durableSteerProcess); ok && c.rec.DurableTurn != nil {
		c.steerDurableInbox(p)
		return
	}
	ctl := controlsFor(c.proc)
	st := ctl.steer
	if st == nil {
		return
	}
	// A system notice or scheduled prompt waits out the ws MCP calls in flight
	// on a harness whose steer replaces the running turn (holdsNotices); the
	// user's own steered messages still go.
	var take func(wsinbox.Entry) bool
	if c.holdsNotices(ctl) {
		take = func(e wsinbox.Entry) bool { return e.EventID == "" && e.Cron == nil }
	}
	c.mu.Lock()
	recs, _ := wsinbox.DrainSteerWhere(c.inbox, take)
	c.held = max(0, c.held-len(recs))
	c.mu.Unlock()
	c.inboxMutated(len(recs))
	c.cutoverInstructionLeft(recs)
	parts := recordParts(recs)
	if len(parts) == 0 {
		return
	}
	c.noteSteerWhiteboard(recs)
	text := c.steerWithDirective(partsText(parts), recs)
	c.applyLive(liveEvents(parts))
	if err := st.Steer(text); err != nil {
		c.setStatus(func() { c.lastErr = "send the steered message: " + err.Error() })
		c.stopProcess("")
		return
	}
	c.modelWaitSteered()
}

func (c *conv) loop() {
	defer c.r.wg.Done()
	defer close(c.done)
	defer c.live.closeSlot()
	// Whatever way the loop ends (close, hand-off, shutdown), this
	// instance's membership goes with it.
	defer c.publishAwake(false)
	defer func() {
		c.stopCompactWatchdog()
		c.stopModelWaitTimer()
		c.stopModelWaitBackoff()
		c.stopQuotaTimer()
		if c.spawnTimer != nil {
			c.spawnTimer.Stop()
		}
		c.stopBgGrace()
		c.stopMailWatch()
	}()
	// Before the first scan, Stop can report an existing holder unknown.
	// Keep its rejoined transport and persisted identity until holdersScanned
	// can establish absence or stop it; otherwise releaseHolder could forget
	// a still-running process and a later unhide could launch a second one.
	if c.hidden && c.r.scanDone() {
		c.stopProcess("")
		if err := c.retireHolder(); err != nil {
			c.setStatus(func() { c.lastErr = "archive process: " + err.Error() })
		}
	}
	beat := time.NewTicker(c.r.opts.heartbeat)
	defer beat.Stop()
	// Settle the predecessor before inbox signals (including nested reports)
	// can start a genuinely new turn.
	if c.r.durableProcess(c.rec.Backend) {
		if c.rec.DurableTurn == nil && c.requeueSteered() {
			c.signalInbox()
		}
	} else if c.rec.Parent != nil && c.rec.HolderID == "" {
		c.interrupted()
		if c.requeueSteered() {
			c.signalInbox()
		}
	}
	c.reportLast()
	if c.settle() {
		return
	}
	// A rejoined child whose record shows its turn open runs it under a
	// trusted holder: publish that before the replay settles it.
	c.publishAwake(c.wantAwake())
	c.restoreQuotaContinuation()
	if c.refreshShellHold() {
		c.watched()
	}
	for {
		c.syncLoopBusy()
		var idleC, actionC, leverC, compactC, modelWaitC, modelWaitDrainC, modelWaitCancelC, modelWaitBackoffC, quotaC, spawnC, bgGraceC <-chan time.Time
		leverC = c.leverC
		compactC = c.compactC
		modelWaitC = c.modelWaitC
		modelWaitDrainC = c.modelWaitDrainC
		modelWaitCancelC = c.modelWaitCancelC
		modelWaitBackoffC = c.modelWaitBackoffC
		quotaC = c.quotaC
		if c.spawnTimer != nil {
			spawnC = c.spawnTimer.C
		}
		if c.idle != nil {
			idleC = c.idle.C
		}
		if c.actionTimer != nil {
			actionC = c.actionTimer.C
		}
		if c.bgGraceTimer != nil {
			bgGraceC = c.bgGraceTimer.C
		}
		select {
		case fn := <-c.reqs:
			// An inbox change signaled before the request is handled
			// first, so a request sees what a send that returned before
			// it was made did, as when sends ran in the loop.
			select {
			case <-c.inboxC:
				c.inboxChanged()
			default:
			}
			fn()
		case <-c.inboxC:
			c.inboxChanged()
		case <-c.mailC:
			c.mailChanged()
		case <-c.spawnC:
			if c.refreshShellHold() {
				c.watched()
			}
			if c.compactResume && c.r.durableProcess(c.rec.Backend) {
				c.kick()
			}
			c.syncHost(true)
			c.spawnDirty = true
			c.live.writeSnapshot()
			if c.live.currentSlot() == nil {
				c.changed(false)
			}
			if c.idleNow() {
				c.armIdle()
				c.restartIfDue()
			}
		case <-spawnC:
			c.spawnTimer, c.spawnNext = nil, time.Time{}
			c.live.writeSnapshot()
			if c.live.currentSlot() == nil {
				c.changed(false)
			}
		case ev, ok := <-c.events:
			if !ok {
				c.processExited()
			} else {
				c.handle(ev)
			}
		case <-idleC:
			c.idle = nil
			if c.idleNow() {
				c.stopProcess("idle_timeout")
			}
		case <-actionC:
			c.actionTimer = nil
			c.actionDue()
		case <-leverC:
			// No compaction started within the cap: what waited goes now.
			c.leverC, c.leverStop = nil, nil
			c.kick()
		case <-bgGraceC:
			c.bgGraceTimer = nil
			c.releaseHeld()
			if !c.turn {
				c.armIdle()
				c.restartIfDue()
				c.kickCompactionNudge()
			}
		case <-compactC:
			c.compactTimedOut()
		case <-modelWaitC:
			c.modelWaitTimedOut()
		case <-modelWaitDrainC:
			c.modelFailureDrainTimedOut()
		case <-modelWaitCancelC:
			c.modelWaitCancelTimedOut()
		case <-modelWaitBackoffC:
			c.modelWaitBackoffTimedOut()
		case <-quotaC:
			c.quotaContinuationTimedOut()
		case <-beat.C:
			c.heartbeat()
		case <-c.r.quit:
			c.letGo()
			return
		}
		c.syncAwake()
		if c.settle() {
			return
		}
	}
}

// heartbeat runs each interval in the loop. Liveness of an in-daemon session
// is memory (Runner.OwnsLive, read by the index), so it never rewrites
// registration.json: that file is written only when its content changes.
func (c *conv) heartbeat() {
	c.live.heartbeatSnapshot()
	c.persistSeq()
	if c.replaying {
		// A holder that attached late (a rescan) is found here.
		c.checkReattach()
	}
	c.checkRow()
	c.expireIfDue()
	c.maybePark()
}

// settle follows each step of the loop: a session whose row a terminal
// harness took is let go (release), and it reports whether the loop ends
// (the conversation closed, or was let go), having stopped the mail watch.
func (c *conv) settle() bool {
	if c.isForeign() && !c.closed {
		c.release()
	}
	c.mu.Lock()
	ended := c.closed || c.parkedOut
	c.mu.Unlock()
	if !ended {
		return false
	}
	return true
}

func (c *conv) isMailStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed || c.parkedOut || c.mailStopped
}

// installMailStop attaches a resident mail watch stop handle under c.mu. If
// the conversation already closed, parked, or exited its loop, stop runs
// immediately outside c.mu so the watch never leaks.
func (c *conv) installMailStop(stop func()) bool {
	if stop == nil {
		return false
	}
	c.mu.Lock()
	if c.closed || c.parkedOut || c.mailStopped {
		c.mu.Unlock()
		stop()
		return false
	}
	c.stopMail = stop
	c.mu.Unlock()
	return true
}

// stopMailWatch stops any installed resident mail watch and fences late
// Watch installs from startConvWith.
func (c *conv) stopMailWatch() {
	c.mu.Lock()
	c.mailStopped = true
	stop := c.stopMail
	c.stopMail = nil
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// refreshHeld reads only this conversation's inbox, never a global listing.
// It lists the inbox on every call, since another process may add or claim
// an entry, but opens only names it has not read yet: a held entry file is
// never rewritten (wsinbox.ReadHeld), so a known name's kind stays valid.
// The count is what wsinbox.Held would return: readable non-interrupt
// entries. An unreadable entry is not remembered and is retried next time.
func (c *conv) refreshHeld() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.r.opts.stateInboxRead != nil {
		c.r.opts.stateInboxRead(c.rec.ReplyID)
	}
	names, err := wsinbox.Names(c.inbox)
	if err != nil {
		return
	}
	kinds := make(map[string]bool, len(names))
	held := 0
	for _, name := range names {
		message, known := c.heldKinds[name]
		if !known {
			entry, err := wsinbox.ReadHeld(c.inbox, name)
			if err != nil {
				continue
			}
			message = !entry.Interrupt
		}
		kinds[name] = message
		if message {
			held++
		}
	}
	c.heldKinds, c.held = kinds, held
}

func (c *conv) state() State { return c.stateWithKeyCheck(true) }

func (c *conv) memoryState() State { return c.stateWithKeyCheck(false) }

func (c *conv) stateWithKeyCheck(checkKey bool) State {
	c.mu.Lock()
	st := closedState(c.rec)
	st.Status, st.ClosedAt = StatusIdle, ""
	if c.turn {
		st.Status = StatusRunning
		if !c.closed && c.rec.Parent != nil && c.rec.TurnOpen {
			st.TurnStartedAt = c.rec.TurnStartedAt
		}
	}
	if c.closed {
		st.Status, st.ClosedAt = StatusClosed, c.rec.ClosedAt
		if c.rec.ClosedAt == "" && c.rec.handedOff() {
			st.Status, st.HandedOffAt = StatusHandedOff, c.rec.HandedOffAt
			st.ResumeCommand = c.r.resumeCommand(c.rec, c.dir)
		}
	}
	st.WakeBlocked = !c.closed && (c.releasedLocked() != "" || c.r.deadBindingRefusal(c.rec, "") != nil)
	st.Process = c.proc != nil
	st.ConvHistoryAccess = c.rec.ConvHistoryAccess
	if c.proc != nil {
		st.ConvHistoryAccessEffective = c.convHistoryAccessEffective
	} else {
		st.ConvHistoryAccessEffective = c.rec.ConvHistoryAccess
	}
	selfSafe := c.safeStopSelfLocked()
	replyID, isLead := c.rec.ReplyID, c.rec.Lead
	st.Holding = c.holding && !c.closed
	st.HoldingShell = st.Holding && c.holdingShell
	st.Hidden = c.hidden
	if c.rec.Lead && !c.closed {
		st.Lifecycle = wsdashboard.SessionAsleep
		if c.hidden {
			st.Lifecycle = wsdashboard.SessionArchived
		} else if c.proc != nil {
			st.Lifecycle = wsdashboard.SessionActiveOn
		}
	} else if c.childWaitingLocked() {
		st.Lifecycle = wsdashboard.SessionChildWaiting
	}
	st.Held = c.held + len(c.rec.Mail) + len(c.rec.Queued) + len(c.rec.Reports)
	st.Error = c.lastErr
	if c.unrequestedCmdWarn != "" {
		st.Error = appendDiagnostic(st.Error, c.unrequestedCmdWarn)
	}
	if c.rejoinKeyPending && checkKey {
		if err := c.r.opts.LeadKeys.EnsureLead(c.rec.Root, c.rec.WSKey); err != nil {
			st.Error = appendDiagnostic(st.Error, "rejoin ws session key (advisory): "+err.Error())
		} else {
			c.rejoinKeyPending = false
		}
	}
	st.ContextTokens = c.contextTokens
	st.ContextWindow, st.ContextCompaction = c.contextWindow, cloneUsageCompaction(c.contextCompaction)
	st.Compacting, st.Compactions, st.CompactOrigin = c.cmpActive && !c.closed, c.cmpCount, c.cmpLast
	if c.observed != nil {
		observed := *c.observed
		st.Observed = &observed
	}
	st.Observations = append([]wslive.ModelObservation(nil), c.observations...)
	st.Batches = c.batches
	if c.turn && !c.closed {
		st.OutputTps = c.outputTps
	}
	st.BackgroundTasks = c.bgTasks
	st.Background, st.Monitors = copyCount(c.background), copyCount(c.monitors)
	if c.rec.Lead {
		st.ActiveMode, st.ActionNeeded = c.activeMode, c.actionNeeded
		st.DashboardControlAccess = c.rec.DashboardControlAccess
		if c.proc != nil && !c.closed {
			st.DashboardControlAccessEffective = c.dashboardControlEffective
		} else {
			st.DashboardControlAccessEffective = c.rec.DashboardControlAccess
		}
		st.Approvals = append([]ApprovalRequest(nil), c.approvals...)
		if c.failure != nil {
			f := *c.failure
			st.Failure = &f
		}
		st.QuotaContinuation = cloneQuotaContinuation(c.rec.QuotaContinuation)
		st.MCPBroken = slices.Clone(c.mcpBroken)
	}
	if c.caps != nil {
		caps := c.caps.clone()
		st.Capabilities = &caps
	}
	if !c.rec.Lead {
		st.PermissionLevel = c.workerPermissionLevel
		st.PermissionSource = c.workerPermissionSource
		if st.PermissionLevel != "" {
			st.PermissionEnforcement = c.r.permissionPolicy(c.rec.Backend).quality(st.PermissionLevel, "")
		}
	}
	st.IsChild = c.r.isChildRecord(c.rec)
	if c.rec.Lead {
		st = c.r.permissionState(st, c.rec, c.dir)
	}
	c.mu.Unlock()
	if selfSafe {
		st.SafeStop = c.r.descendantsAllowSafeStop(replyID, isLead)
	}
	return st
}

func (c *conv) record() Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec
}

// update changes the record under mu and persists it; wmu keeps the
// writes in the order of the changes.
func (c *conv) update(fn func(rec *Record)) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	fn(&c.rec)
	c.syncStateLocked()
	c.mu.Unlock()
	if c.deferRecordPersist {
		return nil
	}
	return c.persistRecord()
}

// persistRecord requires wmu, so an immediate write also flushes deferred sends
// without letting an older copy overwrite a newer change.
func (c *conv) persistRecord() error {
	if c.deferRecordPersist {
		c.recordDirty = true
		return nil
	}
	rec := c.record()
	if c.onPersistRecord != nil {
		if err := c.onPersistRecord(rec); err != nil {
			c.recordDirty = true
			fmt.Fprintf(c.r.opts.Log, "agent %s: record write: %v\n", rec.ReplyID, err)
			return err
		}
	}
	err := c.r.persistAgentRecord(c.dir, rec)
	c.recordDirty = err != nil
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: record write: %v\n", rec.ReplyID, err)
	}
	return err
}

func (c *conv) flushRecord() {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.recordDirty {
		c.persistRecord()
	}
}

func (c *conv) setStatus(fn func()) {
	c.mu.Lock()
	fn()
	c.syncStateLocked()
	c.mu.Unlock()
}

// writeRegistration writes the conversation's row and reports whether it
// did. A lead's row may have been taken by a terminal login (the session
// then is that harness's), so its write is one step with the check that
// the row is still the daemon's (lockRow): a row found taken is left as it
// is.
func (c *conv) writeRegistration() bool {
	rec := c.record()
	var previousRow *wsdashboard.Registration
	if rec.Lead {
		unlock, previous, ok := c.lockRowRead()
		if !ok {
			return false
		}
		defer unlock()
		if previous != nil {
			previousRow = previous
		}
	}
	// Take the timestamp after the lead's row lock. A heartbeat sweep that won
	// the flock first therefore cannot be rolled back by this full-row write.
	now := c.r.opts.Now()
	reg := registration(rec, now)
	// WriteRegistration normalizes this default before persisting it.
	reg.SchemaVersion = 1
	if rec.Lead {
		reg.HostStopped = rec.HostPID == 0 && !c.spawning
		// The holder manager writes the last exit independently of the
		// runner's heartbeat; a regenerated row must not discard it.
		if previousRow != nil {
			reg.HostExitCode = previousRow.HostExitCode
			reg.HostExitSignal = previousRow.HostExitSignal
			reg.HostExitReason = previousRow.HostExitReason
			reg.HostStoppedAt = previousRow.HostStoppedAt
		}
	}
	// Reuse only the row already read under the lead's ownership lock, not
	// process-local evidence: a spawn needs a real reservation on disk.
	if previousRow != nil {
		previous := *previousRow
		previous.HeartbeatAt = reg.HeartbeatAt
		if previous == reg {
			c.setLastRow(reg)
			return true // no semantic change: nothing to write or announce
		}
	} else if last, ok := c.lastRow(); ok && !rec.Lead {
		last.HeartbeatAt = reg.HeartbeatAt
		if last == reg {
			return true
		}
	}
	err := wsdashboard.WriteRegistration(c.r.opts.Dir, reg)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: registration write: %v\n", rec.ReplyID, err)
		return false
	}
	c.setLastReg(now)
	c.setLastRow(reg)
	c.r.notify(rec.ReplyID, rec.Root)
	return true
}

// lastRow is the row this conversation last wrote or found equal on disk; a
// write of a row equal to it (heartbeat aside) is skipped.
func (c *conv) lastRow() (wsdashboard.Registration, bool) {
	c.regMu.Lock()
	defer c.regMu.Unlock()
	return c.lastWritten, c.haveWritten
}

func (c *conv) setLastRow(reg wsdashboard.Registration) {
	c.regMu.Lock()
	c.lastWritten, c.haveWritten = reg, true
	c.regMu.Unlock()
}

func (c *conv) isForeign() bool { return c.foreignRow.Load() }

func (c *conv) markForeign() { c.foreignRow.Store(true) }

func (c *conv) lastRegAt() time.Time {
	c.regMu.Lock()
	defer c.regMu.Unlock()
	return c.lastReg
}

func (c *conv) setLastReg(now time.Time) {
	c.regMu.Lock()
	c.lastReg = now
	c.regMu.Unlock()
}

func (c *conv) regDue(now time.Time) bool {
	return now.Sub(c.lastRegAt()) >= wsdashboard.HeartbeatInterval
}

// rowForeign reports whether a session's row, as read, is no longer the
// daemon's: it exists and is neither a hosted nor an agent row. A terminal
// harness that logged in with the session's key while no process of the
// daemon's ran rewrote it (a hand to the terminal). A missing or unreadable
// row is the daemon's to write.
func rowForeign(dir, replyID string) bool {
	return isForeignRow(wsdashboard.ReadRegistration(dir, replyID))
}

// isForeignRow is rowForeign's test of a row as read.
func isForeignRow(row wsdashboard.Registration, err error) bool {
	// An agent row is no terminal's either, since a terminal login never
	// writes Agent: a daemon of a release that knew no hosted lead served
	// the session as an agent conversation while it ran, and its row is
	// the daemon's to take back.
	return err == nil && !row.Hosted && !row.Agent
}

// lockRow takes a lead session's row lock and checks the row is still the
// daemon's: the step every row write, mail claim, and spawn is guarded by.
// It returns the lock's release and true to go on. False means nothing may
// be done now: the row is a terminal harness's (foreignRow is set, and the
// loop lets the session go), or the lock could not be taken (logged; the
// caller's next try asks again).
func (c *conv) lockRow() (unlock func(), ok bool) {
	unlock, _, ok = c.lockRowRead()
	return unlock, ok
}

// lockRowRead is lockRow that also returns the row it checked under the
// lock (nil when missing or unreadable), so a caller that needs the row
// reads it once per lock: the live writer takes this lock for every ring
// and snapshot write (lockSummaryWriter).
func (c *conv) lockRowRead() (unlock func(), row *wsdashboard.Registration, ok bool) {
	if c.isForeign() {
		return nil, nil, false
	}
	flockUnlock, err := wsdashboard.LockRegistration(c.r.opts.Dir, c.rec.ReplyID)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %v\n", c.rec.ReplyID, err)
		return nil, nil, false
	}
	c.rowHeld.Add(1)
	c.rowReads.Add(1)
	unlock = func() {
		c.rowHeld.Add(-1)
		flockUnlock()
	}
	read, err := wsdashboard.ReadRegistration(c.r.opts.Dir, c.rec.ReplyID)
	if isForeignRow(read, err) {
		c.markForeign()
		unlock()
		return nil, nil, false
	}
	if err == nil {
		row = &read
	}
	return unlock, row, true
}

// checkRow reads a lead session's row between its own writes, without the
// lock, so a hand to the terminal shows within a heartbeat. Nothing is
// decided on this read alone: every guarded step checks again under the
// lock, and the row leaves the daemon only through a terminal login, which
// never gives it back.
func (c *conv) checkRow() {
	if !c.rec.Lead || c.isForeign() {
		return
	}
	// One stat, not a read: the daemon owns the row and writes it only on a
	// change it records (setRowStamp). A different mtime or size means another
	// writer (a terminal login) replaced it, and only then is it read.
	path := wsdashboard.RegistrationPath(c.r.opts.Dir, c.rec.ReplyID)
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	stamp := rowStamp{mod: info.ModTime().UnixNano(), size: info.Size()}
	c.regMu.Lock()
	known, same := c.rowStampSet, c.rowStampV == stamp
	c.regMu.Unlock()
	if known && same {
		return
	}
	if rowForeign(c.r.opts.Dir, c.rec.ReplyID) {
		c.markForeign()
		return
	}
	c.regMu.Lock()
	c.rowStampSet, c.rowStampV = true, stamp
	c.regMu.Unlock()
}

type rowStamp struct {
	mod  int64
	size int64
}

// release lets a lead session go to the terminal harness that took its
// row: the process and its holder end (the login was accepted only with no
// live process), mail claimed and not delivered goes back to the session's
// mailbox, held dashboard messages are dropped as a close drops them, and
// the record becomes a handed-off one, which no daemon start serves again.
// The row is the terminal's: nothing here writes it. The loop ends after
// (settle), which stops the mail watch.
func (c *conv) release() {
	c.stopProcess("")
	if err := c.retireHolder(); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %v\n", c.rec.ReplyID, err)
	}
	mail := c.rec.Mail
	if len(mail) > 0 && c.r.opts.Mail != nil {
		if err := c.r.opts.Mail.Return(c.rec.Root, c.rec.WSKey, mail); err != nil {
			// Kept in the record: a later takeover delivers it.
			fmt.Fprintf(c.r.opts.Log, "agent %s: return claimed mail: %v\n", c.rec.ReplyID, err)
		} else {
			mail = nil
		}
	}
	c.stopQuotaTimer()
	c.update(func(rec *Record) {
		rec.HandedOffAt = c.r.opts.Now().UTC().Format(time.RFC3339)
		rec.Mail = mail
		rec.QuotaContinuation = nil
		rec.PendingCompaction = nil
		c.closed = true
	})
	c.syncHost(false)
	c.dropHeld()
	c.live.setAdapter("")
	rec := c.record()
	st := c.memoryState()
	c.r.mu.Lock()
	if c.r.convs[rec.ReplyID] == c {
		delete(c.r.convs, rec.ReplyID)
		c.r.liveOwners.CompareAndDelete(rec.ReplyID, c)
	}
	c.r.closed[rec.ReplyID] = rec
	if c.r.closedRows == nil {
		c.r.closedRows = map[string]State{}
	}
	c.r.closedRows[rec.ReplyID] = st
	c.r.mu.Unlock()
	fmt.Fprintf(c.r.opts.Log, "agent %s: handed to a terminal session\n", rec.ReplyID)
	c.r.notify(rec.ReplyID, rec.Root)
}

// dropHeld drops the dashboard messages held for a conversation that takes
// no more turns, the ones drained and still queued, and its pending
// interrupt requests.
func (c *conv) dropHeld() []wsinbox.Record {
	if len(c.rec.Queued) > 0 {
		c.update(func(rec *Record) { rec.Queued = nil })
	}
	removed := 0
	var dropped []wsinbox.Record
	if held, err := wsinbox.Held(c.inbox); err == nil {
		for _, rec := range held {
			if _, err := wsinbox.Cancel(c.inbox, rec.Name); err == nil {
				removed++
				dropped = append(dropped, rec)
			}
		}
		c.cutoverInstructionLeft(dropped)
	}
	c.inboxMutated(removed)
	_, _ = wsinbox.DrainInterrupt(c.inbox)
	c.setStatus(func() { c.held = 0 })
	return dropped
}

// apply applies a wslive event and tells the daemon when it wrote. quiet
// (a replayed holder record) applies it to the state alone.
func (c *conv) apply(ev wslive.Event, quiet ...bool) {
	replayed := len(quiet) > 0 && quiet[0]
	c.applyMode(ev, replayed, false)
}

// applyCoalesced applies a mapper status whose immediately following TurnEnd
// is its recovery boundary, avoiding a redundant file-only snapshot rewrite.
func (c *conv) applyCoalesced(ev wslive.Event, quiet bool) {
	c.applyMode(ev, quiet, true)
}

func (c *conv) applyMode(ev wslive.Event, quiet, coalesceTurnEnd bool) {
	if c.stageAgyNativeReplay(ev, quiet) {
		return
	}
	if ev.Kind == wslive.KindCompact && !quiet && c.compactOrigin != "" {
		// A compaction ws requested is recorded as that request's origin, not
		// the harness's own trigger: a harness reports the command that ran
		// it ("manual") whoever sent it.
		ev.Trigger = c.compactOrigin
		if ev.Phase == wslive.CompactEnd || ev.Phase == wslive.CompactFailed {
			defer func() { c.compactOrigin = "" }()
		}
	}
	if !quiet {
		c.observeCompaction(ev)
	}
	if ev.Kind == wslive.KindStatus && ev.Context != nil && ev.Context.Compaction == nil {
		if !quiet {
			c.refreshCompaction()
		}
		if comp := c.usageCompaction(ev.Context.Window); comp != nil {
			ctx := *ev.Context
			ctx.Compaction = comp
			ev.Context = &ctx
		}
	}
	if ev.Kind == wslive.KindReset {
		c.spawnDirty = true
		if !quiet {
			c.passive.clearAdvisory()
		}
	}
	usageChanged := false
	if ev.Kind == wslive.KindStatus {
		c.setStatus(func() {
			if ctx := ev.Context; ctx != nil {
				// An explicit unknown context (e.g. after compaction) clears
				// the previous figure in the row as well as the child's band.
				tokens := float64(0)
				if ctx.Tokens != nil {
					tokens = float64(*ctx.Tokens)
				}
				if tokens >= 0 && tokens <= wslive.MaxContextTokens && (c.contextTokens == nil || *c.contextTokens != tokens) {
					c.contextTokens, usageChanged = &tokens, true
				}
				if ctx.Window > 0 && (c.contextWindow != ctx.Window || !reflect.DeepEqual(c.contextCompaction, ctx.Compaction)) {
					comp := cloneUsageCompaction(ctx.Compaction)
					c.contextWindow, c.contextCompaction, usageChanged = ctx.Window, comp, true
				}
			}
			if rate := ev.OutputTps; rate != nil && *rate >= 0 && *rate <= wslive.MaxOutputTps && (c.outputTps == nil || *c.outputTps != *rate) {
				v := *rate
				c.outputTps, usageChanged = &v, true
			}
		})
	}
	// A native subagent row (agy, Codex) publishes RAM-only through the live
	// slot, under the in-memory ownership fence, and never calls the
	// recovery writer itself: it reaches disk with the next heartbeat or
	// turn-end write, so a chatty child costs no file touches on the event
	// path.
	nativeRow := ev.Kind == wslive.KindAgent && wslive.NativeAgentID(ev.AgentID)
	if quiet {
		c.live.applyQuiet(ev)
	} else if nativeRow {
		c.live.applyRAM(ev)
	} else if coalesceTurnEnd {
		c.live.applyCoalesced(ev)
	} else {
		c.live.apply(ev)
	}
	if ev.Kind == wslive.KindStatus && ev.Observed != nil {
		observations := c.live.st.ObservedResponses("")
		c.setStatus(func() {
			if !reflect.DeepEqual(c.observations, observations) {
				c.observations, usageChanged = observations, true
			}
		})
		if own := c.live.st.Snapshot(c.r.opts.Now().UnixMilli()).Lead.Observed; own != nil {
			c.setStatus(func() {
				if c.observed == nil || !reflect.DeepEqual(*c.observed, own.Current) {
					observed := own.Current
					c.observed, usageChanged = &observed, true
				}
			})
		}
	}
	activityChanged := false
	if (ev.Kind == wslive.KindAgent && ev.AgentID == "" && ev.Batches != nil) || ev.Kind == wslive.KindReset || ev.Kind == wslive.KindTurnEnd || ev.Kind == wslive.KindReply {
		batches := c.live.st.Snapshot(c.r.opts.Now().UnixMilli()).Lead.Batches
		c.setStatus(func() {
			if !reflect.DeepEqual(c.batches, batches) {
				c.batches, activityChanged = batches, true
			}
		})
	}
	if usageChanged || activityChanged {
		c.watched()
	}
}

// agyNativeSettle is an agy native row whose observation ended at a time.
type agyNativeSettle struct {
	id string
	at int64
}

// endAgyNativeObservation is a live, accepted logical turn end's effect on
// agy native rows: agy reports no child end, so observation of every row
// still running ends at at (EndUnknown). While a replay runs, the end is
// staged in order with the rows' events instead (stageAgyNativeReplay) and
// settles when the replay ends: a replayed record that only follows the
// holder's sequence mark, such as a buffered prompt response, is not quiet,
// yet the replay is not over.
func (c *conv) endAgyNativeObservation(at int64) {
	if c.replaying {
		c.agyNativeReplay.staged = append(c.agyNativeReplay.staged, agyNativeStaged{endAt: at})
		return
	}
	for _, id := range c.live.st.RunningAgents(wslive.NativeAgyPrefix) {
		c.settleAgyNative([]agyNativeSettle{{id, at}})
	}
}

// agyNativeReplayRows is what a replay staged of agy native rows: their
// events and the live turn ends between them, in order (staged), the holder
// the replay read them from (holderID), the record of it each row was first
// seen at (start), and the time of the last record past the holder's
// sequence mark that touched it (live). holderID is taken with the first
// staged event: a process end releases the holder from the record
// (releaseHolder, processGone, a retirement) before its fold runs, and the
// fold still judges the staged starts against that holder's coverage.
type agyNativeReplayRows struct {
	staged   []agyNativeStaged
	holderID string
	start    map[string]uint64
	live     map[string]int64
}

// agyNativeStaged is one staged step: a row event (quiet when replayed from
// a record the previous daemon applied), or, with endAt set, a live turn
// end that ended observation of the rows then running.
type agyNativeStaged struct {
	ev    wslive.Event
	quiet bool
	endAt int64
}

// stageAgyNativeReplay holds an agy native row event of a running replay out
// of the state, and so out of retention, until the replay ends
// (resolveAgyNativeReplay); it reports whether it held ev. The event keeps
// the time it arrived at.
func (c *conv) stageAgyNativeReplay(ev wslive.Event, quiet bool) bool {
	if !c.replaying || ev.Kind != wslive.KindAgent || !strings.HasPrefix(ev.AgentID, wslive.NativeAgyPrefix) {
		return false
	}
	o := &c.agyNativeReplay
	if o.holderID == "" {
		o.holderID = c.rec.HolderID
	}
	if ev.At <= 0 {
		ev.At = c.r.opts.Now().UnixMilli()
	}
	if _, seen := o.start[ev.AgentID]; !seen {
		if o.start == nil {
			o.start = map[string]uint64{}
		}
		o.start[ev.AgentID] = c.eventSeq
	}
	if !quiet {
		if o.live == nil {
			o.live = map[string]int64{}
		}
		o.live[ev.AgentID] = ev.At
	}
	o.staged = append(o.staged, agyNativeStaged{ev: ev, quiet: quiet})
	return true
}

// finishAgyNativeReplay ends a rejoin's observation of agy native rows: it
// resolves what the replay staged (resolveAgyNativeReplay), then, since
// observation is not continuous across a daemon replacement and a replayed
// cancellation cannot be told from a watchdog continuation awaiting its
// first output, closes every row still running end-unknown at the time
// agyNativeReplayEnd gives it; with processGone, the process ended instead,
// and endAgents fails every subagent still running, as at any process end.
//
// Retention waits for the whole fold (wslive.State.HoldRetention): applied
// one by one, the staged starts of a long replay could reach the running
// cap and evict a row a later failure would have left the oldest open
// observation, so caps apply once, to the final lifecycles. Publications
// wait for it too (liveWriter.beginBatch): no reader sees an intermediate,
// uncapped roster, and the retained one goes out in a single snapshot
// write. Last, the restored recovery file's evidence is in the state, so
// the file may be replaced (liveWriter.diskHold): a fold that lifts the
// hold writes even when it changed nothing. It reports whether it wrote, so
// a caller with its own changes to write writes only when it did not.
func (c *conv) finishAgyNativeReplay(processGone bool) bool {
	c.live.beginBatch()
	release := c.live.st.HoldRetention()
	rows, restored := c.resolveAgyNativeReplay()
	if processGone {
		c.live.endAgents()
	} else {
		var rest []agyNativeSettle
		for _, id := range c.live.st.RunningAgents(wslive.NativeAgyPrefix) {
			rest = append(rest, agyNativeSettle{id, c.agyNativeReplayEnd(id, rows, restored)})
		}
		c.settleAgyNative(rest)
	}
	release()
	held := c.live.diskHold
	c.live.diskHold = false
	return c.live.endBatch(held)
}

// resolveAgyNativeReplay applies what a replay staged of agy native rows,
// once, and returns it with what the restored snapshot said. Retention
// already decided which finished rows the restored snapshot kept, so first
// the rows the snapshot shows evicted are dropped: rows the state does not
// hold, touched by no record past the holder's sequence mark, whose start
// the snapshot covers and which it does not list (restoredAgyNative.evicted).
// Their staged events never reach the state, so they cannot displace a
// restored row. The surviving events then apply in order (a row started
// after the snapshot's coverage keeps the terminal record that followed),
// noting at each staged live turn end the rows it found running; those rows
// settle at that end's time after every event, so a launch failure buffered
// later in the same replay still wins. The caller holds retention across
// the fold (finishAgyNativeReplay), so no cap sees an intermediate roster.
// Last, a row the
// snapshot listed running whose start left the holder ring is seeded from
// the snapshot and closes end-unknown at the snapshot's write time.
func (c *conv) resolveAgyNativeReplay() (agyNativeReplayRows, restoredAgyNative) {
	restored := c.live.takeRestored()
	rows := c.agyNativeReplay
	c.agyNativeReplay = agyNativeReplayRows{}
	holderID := rows.holderID
	dropped := map[string]bool{}
	for id, seq := range rows.start {
		if _, live := rows.live[id]; !live && !c.live.st.HasAgent(id) && restored.evicted(id, holderID, seq) {
			dropped[id] = true
		}
	}
	var deferred []agyNativeSettle
	for _, step := range rows.staged {
		if step.endAt > 0 {
			for _, id := range c.live.st.RunningAgents(wslive.NativeAgyPrefix) {
				if !slices.ContainsFunc(deferred, func(d agyNativeSettle) bool { return d.id == id }) {
					deferred = append(deferred, agyNativeSettle{id, step.endAt})
				}
			}
			continue
		}
		if !dropped[step.ev.AgentID] {
			c.applyAgyNativeRow(step.ev, step.quiet)
		}
	}
	c.settleAgyNative(deferred)
	var wrapped []string
	for id := range restored.running {
		if !c.live.st.HasAgent(id) && !dropped[id] {
			wrapped = append(wrapped, id)
		}
	}
	slices.Sort(wrapped)
	var seeded []agyNativeSettle
	for _, id := range wrapped {
		a := restored.running[id]
		at := restored.updatedAt
		if at <= 0 {
			at = c.r.opts.Now().UnixMilli()
		}
		start := a.StartedAt
		if start <= 0 || start > at {
			start = at
		}
		c.applyAgyNativeRow(wslive.Event{Kind: wslive.KindAgent, AgentID: id, ParentID: a.ParentID, Label: a.Label, Status: wslive.StatusRunning, At: start}, false)
		seeded = append(seeded, agyNativeSettle{id, at})
	}
	c.settleAgyNative(seeded)
	if len(dropped) > 0 {
		ids := make([]string, 0, len(dropped))
		for id := range dropped {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if f, ok := c.proc.(nativeLaunchForgetter); ok {
			f.forgetNativeLaunches(ids)
		}
	}
	return rows, restored
}

// agyNativeReplayEnd is when a rejoin's observation of the agy native row id,
// still running at the replay's end, ended: the time of the last record past
// the holder's sequence mark that touched it; else, for a row the restored
// snapshot listed running, that snapshot's write time; else (no readable
// snapshot, a row started after the snapshot's coverage, or a later replay)
// the replay's end.
func (c *conv) agyNativeReplayEnd(id string, rows agyNativeReplayRows, restored restoredAgyNative) int64 {
	if at, ok := rows.live[id]; ok {
		return at
	}
	if _, listed := restored.running[id]; listed && restored.updatedAt > 0 {
		return restored.updatedAt
	}
	return c.r.opts.Now().UnixMilli()
}

// snapshotCoverage is the holder records the live state applied, for the
// recovery file (liveWriter.coverage): every whole line of the current
// holder through c.seq. A rejoin's replay writes no file until it folded
// what it staged (liveWriter.diskHold), so c.seq, already at the holder's
// mark while it runs, is never written ahead of the state. Loop-owned
// fields, read on the loop like every snapshot write.
func (c *conv) snapshotCoverage() (string, uint64) {
	return c.rec.HolderID, c.seq
}

// applyAgyNativeRow applies an agy native row event straight to the live
// writer, past replay staging: replayed (quiet) as a replay applies it,
// else RAM-only like every native row.
func (c *conv) applyAgyNativeRow(ev wslive.Event, quiet bool) {
	if quiet {
		c.live.applyQuiet(ev)
	} else {
		c.live.applyRAM(ev)
	}
}

// settleAgyNative ends each of rows that still runs done as observation
// ended, outcome unknown (EndUnknown), at its time. The reducer keeps a
// settled native row from changing again, so a later replayed or live event
// leaves it as it is. The process forgets the rows' launch correlations: no
// launch failure can change them any more.
func (c *conv) settleAgyNative(rows []agyNativeSettle) {
	var settled []string
	for _, row := range rows {
		if !slices.Contains(c.live.st.RunningAgents(wslive.NativeAgyPrefix), row.id) {
			continue
		}
		c.applyAgyNativeRow(wslive.Event{Kind: wslive.KindAgent, AgentID: row.id, Status: wslive.StatusDone, EndUnknown: true, At: row.at}, false)
		settled = append(settled, row.id)
	}
	if f, ok := c.proc.(nativeLaunchForgetter); ok && len(settled) > 0 {
		f.forgetNativeLaunches(settled)
	}
}

// nativeLaunchForgetter is a process whose mapper correlates native subagent
// launches with their rows (agy): forgetNativeLaunches drops the rows'
// pending launch correlations once the rows settled.
type nativeLaunchForgetter interface {
	forgetNativeLaunches(rowIDs []string)
}

// kick submits the next turn when no turn runs, starting the process first
// when there is none. A failed start leaves everything held.
//
// The turn is taken from the drained inbox, in order, behind what earlier
// drains queued (Record.Queued): the messages up to the first slash command
// are joined into one turn with a lead's claimed mail, and a command at the
// front runs alone, as its bare typed line, so the harness runs it as a
// command. What follows the turn's part stays queued for the turns after
// it, which each turn end submits; mail waits for the next message turn, or
// goes alone once nothing is queued.
func (c *conv) canKick() bool {
	if c.proc == nil && !c.r.publicationReady() {
		return false
	}
	durable := c.r.durableProcess(c.rec.Backend)
	if durable && (c.r.durableStopping.Load() || !c.r.startupReady() || c.r.exec.deferred.Load() && !c.r.exec.bound.Load()) {
		return false
	}
	return !c.closed && !c.hidden && !c.quitting && !c.turn && !c.replaying &&
		(len(c.steered) == 0 || durable && c.rec.DurableTurn != nil) && c.leverC == nil &&
		!(c.r.opts.Holders != nil && !c.r.scanDone())
}

// hasPendingKick reports from in-memory state whether a processless
// conversation has queued work waiting to kick once serving opens.
func (c *conv) hasPendingKick() bool {
	c.mu.Lock()
	held := c.held
	c.mu.Unlock()
	return held > 0 || len(c.rec.Queued) > 0 || len(c.rec.Mail) > 0 || len(c.rec.Reports) > 0 && !c.reportsParked() ||
		c.rec.DurableTurn != nil || c.kickDeferred.Load() || c.quotaPendingFire ||
		(c.compactResume && c.compactNote != "")
}

// deferKickIfUnpublished defers a processless start kick until
// StartForPublication's serving channel closes. Storing kickDeferred before
// re-checking publicationReady prevents a missed wakeup when close(serving) +
// kickAfterServing runs between the first check and the store.
func (c *conv) deferKickIfUnpublished() bool {
	if c.r.publicationReady() {
		return false
	}
	if hook := c.r.onDeferKick; hook != nil {
		hook()
	}
	c.kickDeferred.Store(true)
	if c.r.publicationReady() {
		c.kickDeferred.Store(false)
		return false
	}
	return true
}

func (c *conv) kick() {
	if c.proc == nil && c.deferKickIfUnpublished() {
		return
	}
	if !c.canKick() {
		return
	}
	if c.rec.DurableTurn != nil && c.r.durableProcess(c.rec.Backend) {
		c.resumeDurableTurn()
		return
	}
	if c.quotaPendingFire && !c.compacting && !c.compactCommand && c.quotaContinuationTimedOut() {
		return
	}
	// A released binding gets no compaction command, continuation or restore
	// turn: kickInput refuses whatever input waits instead.
	if c.released() == "" && c.compactIdle() {
		return
	}
	c.kickInput()
}

func (c *conv) kickCompactionNudge() {
	if c.compactionNudgeReady() {
		c.kick()
	}
}

// refuseStart records a refused start (an admission refusal, or a release
// of the conversation's checkout): the input stays for a later admitted
// start, and the conversation counts as asleep. It runs in the loop.
func (c *conv) refuseStart(refused *admissionRefusal) {
	c.setStatus(func() {
		c.lastErr, c.wakeRefused = refused.Error(), true
		if refused.checkout != "" {
			c.releasedCheckout = refused.checkout
		}
	})
	fmt.Fprintf(c.r.opts.Log, "agent %s: start: %v\n", c.rec.ReplyID, refused)
	c.live.writeSnapshot()
	c.r.notify(c.rec.ReplyID, c.rec.Root)
	c.watched()
	// Persist the input's coverage so crash recovery cannot expire it as
	// quiet.
	c.flushRecord()
	// The senders of mail held since its claim hear the refusal now, also
	// when the claim's own kick was deferred (the first holder scan).
	c.tellHeldSenders(refused.checkout, refused.Error())
}

func (c *conv) kickInput() {
	// Checked before anything is sent: a released binding gets no
	// compaction nudge, and no new turn or start below.
	released := c.released()
	if released == "" && !c.quotaInjecting && c.sendCompactionNudge() {
		return
	}
	c.mu.Lock()
	held, err := wsinbox.Held(c.inbox)
	if err == nil {
		c.held = len(held)
	}
	c.mu.Unlock()
	durable := c.r.durableProcess(c.rec.Backend)
	var pending []wsinbox.Record
	if durable {
		if err := c.ackInboxClaims(); err != nil {
			c.durableTurnError("acknowledge queued input", err)
			return
		}
		var claimErr error
		pending, claimErr = wsinbox.PendingClaims(c.inbox, c.rec.DurableInboxClaims)
		if claimErr != nil {
			c.durableTurnError("read claimed input", claimErr)
			return
		}
	}
	mail := c.rec.Mail
	// A child's reports (children.go) go with the next message turn, as
	// mail does.
	reports := c.rec.Reports
	// Reports parked for a scheduled quota continuation (quota_hop.go) wait
	// for its turn, or any other input's, and never start one alone.
	reportsWake := len(reports) > 0 && !c.reportsParked()
	if (err != nil || len(held) == 0) && len(mail) == 0 && len(c.rec.Queued) == 0 && !reportsWake && len(pending) == 0 {
		c.voidCancelled()
		return
	}
	if released != "" {
		// Its checkout's release ended the binding: a live process that
		// outlived it takes no new turn. The release notice handles its
		// cancellation. A processless binding can be closed here too.
		refused := deadCheckoutRefusal(released)
		c.refuseStart(refused)
		if c.proc == nil && !c.replaying {
			c.closeDeadAfterRefusal(refused)
		}
		return
	}
	if c.proc == nil && !(!durable && c.hasPendingAccountSlot() && c.frontBoundaryCommandPending(held)) {
		if c.rec.Lead && c.rec.HolderID != "" && !c.r.scanDone() {
			// The holder the record names may still run unseen: the start
			// waits for the first scan (holdersScanned kicks again), since
			// retireHolder would not find that holder to end it first.
			return
		}
		if err := c.startProcess(); err != nil {
			if errors.Is(err, errDurableStopping) {
				return
			}
			var refused *admissionRefusal
			if errors.As(err, &refused) {
				c.refuseStart(refused)
				if dead := c.r.deadBindingRefusal(c.record(), refused.checkout); dead != nil {
					c.closeDeadAfterRefusal(dead)
				}
				return
			}
			c.setStatus(func() { c.lastErr, c.wakeRefused = err.Error(), false })
			fmt.Fprintf(c.r.opts.Log, "agent %s: start: %v\n", c.rec.ReplyID, err)
			c.live.writeSnapshot()
			c.r.notify(c.rec.ReplyID, c.rec.Root)
			c.settleTurn(TurnFailed, err.Error(), c.sendCount())
			return
		}
		c.setStatus(func() { c.wakeRefused = false })
	}
	// Under mu, as Send writes: count the sends covered by the drain
	// (Wait's turn marks).
	var isolatedCron bool
	c.mu.Lock()
	covered := c.sends
	var recs []wsinbox.Record
	if durable {
		// Refresh under the same lock as Send, since a process start
		// can take time and the earlier held snapshot is only advisory.
		held, err = wsinbox.Held(c.inbox)
		if err == nil {
			c.held = len(held)
			c.withCronClaimConsent(pending, held, func(disposition wsinbox.CronClaimDisposition) {
				if err = classifyCronClaims(c.inbox, pending, held, disposition); err != nil {
					return
				}
				take, cron := planInputClaims(c.rec.Queued, pending, held)
				if take != nil {
					isolatedCron = cron
					available := len(pending) + len(held)
					pending = slices.DeleteFunc(pending, func(rec wsinbox.Record) bool { return !take(rec) })
					held = slices.DeleteFunc(held, func(rec wsinbox.Record) bool { return !take(rec) })
					if len(pending)+len(held) < available {
						// A held cron can be older than the input taken here.
						// Advance the prefix once the deferred entries settle.
						covered = c.drained
					}
				}
				recs, err = wsinbox.ClaimInputs(c.inbox, held)
			})
		}
	} else {
		recs, _ = wsinbox.Drain(c.inbox)
	}
	c.held = max(0, c.held-len(recs))
	c.mu.Unlock()
	c.inboxMutated(len(recs))
	if durable && err != nil {
		c.durableTurnError("claim input", err)
		return
	}
	if len(pending) > 0 {
		recs = append(pending, recs...)
		orderInboxClaims(recs)
	}
	if isolatedCron {
		if len(recs) > 0 {
			c.settlePlanCronRefusal(recs[0].Name, covered)
		} else {
			// A cancellation won the claim. Recheck the deferred entries.
			c.signalInbox()
		}
		return
	}
	from := c.drained
	if !durable {
		c.drained = covered
	}
	queue := slices.Clone(c.rec.Queued)
	c.cutoverInstructionLeft(recs)
	for _, rec := range recs {
		operator := durable && c.rec.Lead && rec.Entry.Operator
		if cmd := rec.Entry.Command; cmd != nil {
			cmd := *cmd
			queue = append(queue, QueuedInput{Command: &cmd, After: from, Operator: operator})
		} else if text := strings.TrimSpace(rec.Entry.Text()); text != "" {
			_, exec := rec.Entry.ExecNotice()
			queue = append(queue, QueuedInput{Text: text, After: from, Operator: operator, Exec: exec, WhiteboardFirst: rec.Entry.WhiteboardFirst, User: c.rec.Lead && userOriginEntry(rec.Entry)})
		}
	}
	if durable && len(recs) > 0 {
		if err := c.commitInboxClaims(recs, queue, covered); err != nil {
			c.durableTurnError("save queued input", err)
			return
		}
	}
	if len(queue) == 0 && len(mail) == 0 && !reportsWake {
		// A cancel took the message after the start: the process waits for
		// input like after a turn.
		c.voidCancelled()
		if !c.turn {
			c.armIdle()
		}
		return
	}
	cmd, inputs, rest := nextTurn(queue)
	parts := make([]string, len(inputs))
	var workerInstructionParts []string
	for i, input := range inputs {
		parts[i] = input.Text
		if !c.rec.Lead && cmd == nil && !input.Exec {
			if t := strings.TrimSpace(input.Text); t != "" {
				workerInstructionParts = append(workerInstructionParts, t)
			}
		}
	}
	workerInstruction := strings.Join(workerInstructionParts, "\n\n")
	// The sticky whiteboard-only state this turn's user input leaves; a turn
	// woken by mail, reports or a notice keeps it as it was.
	whiteboard := c.setWhiteboardOnly(whiteboardOnlyAfter(c.rec.WhiteboardOnly, inputs))
	if !durable && cmd != nil && cmd.Name == "clear" && c.hasPendingAccountSlot() {
		// Intercept /clear before it reaches RunCommand (which codex does not
		// support and claude would execute on the existing process), forcing a
		// cold teardown + pending account slot commit + fresh respawn (D6).
		c.update(func(rec *Record) {
			rec.Queued = rest
			rec.Drained, rec.Voided = c.drained, c.voided
		})
		if err := c.coldRestartPendingAccountSlot("clear"); err != nil {
			c.setStatus(func() { c.lastErr = "account slot switch on /clear: " + err.Error() })
			c.live.writeSnapshot()
			c.r.notify(c.rec.ReplyID, c.rec.Root)
			return
		}
		c.apply(wslive.Event{Kind: wslive.KindReset})
		if len(rest) > 0 {
			c.signalInbox()
		}
		return
	}
	if !durable && cmd != nil && cmd.Name == "compact" {
		c.refreshCompaction()
		if c.wsManagedLeadCompaction() {
			c.update(func(rec *Record) {
				rec.Queued = rest
				rec.Drained, rec.Voided = c.drained, c.voided
			})
			if c.compactPending == nil && !c.compacting && !c.compactCommand && !c.compactStage1Active {
				c.compactPending = &compactRequest{resume: false, internal: false, auto: false, stage1: true}
				c.publishCompaction(false)
			}
			if !c.compactIdle() {
				c.signalInbox()
			}
			return
		}
		if c.hasPendingAccountSlot() {
			// Native compaction mode with a queued account slot switch: route
			// through compactIdle so c.proc reuse is blocked and a cold restart
			// commits the pending slot (D6).
			c.update(func(rec *Record) {
				rec.Queued = rest
				rec.Drained, rec.Voided = c.drained, c.voided
			})
			if c.compactPending == nil && !c.compacting && !c.compactCommand {
				c.compactPending = &compactRequest{resume: false, internal: false, auto: false, triggerPath: "account_slot_compact"}
				c.publishCompaction(false)
			}
			if !c.compactIdle() {
				c.signalInbox()
			}
			return
		}
	}
	if c.proc == nil && !durable {
		// Re-ensure a process when the pre-drain boundary command peek was
		// superseded by a concurrent cancel or non-boundary input.
		if err := c.startProcess(); err != nil {
			var refused *admissionRefusal
			if errors.As(err, &refused) {
				c.refuseStart(refused)
				if dead := c.r.deadBindingRefusal(c.record(), refused.checkout); dead != nil {
					c.closeDeadAfterRefusal(dead)
				}
				return
			}
			c.setStatus(func() { c.lastErr, c.wakeRefused = err.Error(), false })
			fmt.Fprintf(c.r.opts.Log, "agent %s: start: %v\n", c.rec.ReplyID, err)
			c.live.writeSnapshot()
			c.r.notify(c.rec.ReplyID, c.rec.Root)
			c.settleTurn(TurnFailed, err.Error(), c.sendCount())
			return
		}
		c.setStatus(func() { c.wakeRefused = false })
	}
	if cmd != nil {
		mail, reports = nil, nil
	}
	// The held mail goes with its legend, resolved now; the record keeps
	// the texts without it. A steered mail no run took is among the
	// reports (requeueSteered), so one legend covers both, and lands on
	// the first mail text of the turn.
	legended := c.mailLegend(append(slices.Clone(reports), mail...), nil)
	sentReports, submitted := legended[:len(reports)], legended[len(reports):]
	// The turn covers the sends its messages hold: all the drain took
	// when nothing waits behind it, else only those before the drain that
	// took the first input left waiting (a leading slash command's turn
	// leaves the text drained with it for the next turn).
	mark := covered
	if len(rest) > 0 {
		mark = rest[0].After
	}
	c.stopIdle()
	// Read before the send: a request boundary is never later than the
	// prompt's delivery.
	sentAt := c.r.opts.Now()
	startedAt := sentAt.UnixMilli()
	heldAgy := c.r.heldInternalWorker(c.record())
	if !durable && len(recs) > 0 {
		// Keep drained input recoverable until the send succeeds.
		c.update(func(rec *Record) { rec.Queued = queue })
	}
	// The turn reaches the page only once its send went through: a failed
	// send leaves no user entry or turn line of its own (its error shows),
	// so its retry shows the message once. The process's events for the
	// turn are handled by this loop after kick returns, so they still
	// follow the turn's start.
	ordinary := cmd == nil
	var committed bool
	// The release check above is a snapshot: a release notice can land
	// between it and the send, so the send is admitted against it once more
	// (admitInput).
	refusedBy := c.admitInput(func() {
		if durable {
			text := strings.Join(append(append(slices.Clone(parts), sentReports...), submitted...), "\n\n")
			if cmd != nil {
				if body, ok := c.playbookTurn(*cmd); ok {
					text, ordinary = body, true
				} else {
					text = cmd.Line()
				}
			}
			c.passiveTook = nil
			if ordinary {
				text = c.turnInput(text, whiteboard)
			}
			interactive := c.rec.Lead && slices.ContainsFunc(queue[:len(queue)-len(rest)], func(input QueuedInput) bool { return input.Operator })
			committed, err = c.admitDurableTurn(text, rest, mark, startedAt, len(mail) > 0, len(reports) > 0, false, interactive)
			return
		}
		c.setStatus(func() { c.turn, c.lastErr = true, "" })
		// The admitted turn is a keep-awake member before its send is
		// tried; a failed send clears turn below, and the loop step's sync
		// drops it. A refused input never sets turn, so it never joins.
		c.syncAwake()
		c.passiveTook = nil
		if cmd == nil {
			err = c.proc.Send(c.turnInput(strings.Join(append(append(slices.Clone(parts), sentReports...), submitted...), "\n\n"), whiteboard))
		} else if text, ok := c.playbookTurn(*cmd); ok {
			// The runner expands a playbook command itself: the harness
			// has no such command, and the turn carries the playbook's
			// body.
			ordinary = true
			err = c.proc.Send(c.turnInput(text, whiteboard))
		} else {
			err = c.runCommand(*cmd)
		}
	})
	if refusedBy != "" {
		// Nothing was sent: the input stays queued (persisted above), as a
		// refused start keeps it, and the idle process may sleep again.
		c.refuseStart(checkoutReleasedRefusal(refusedBy))
		c.armIdle()
		return
	}
	if durable && err != nil {
		if committed {
			c.passive.ack(c.passiveTook)
			c.passiveTook = nil
		}
		if errors.Is(err, errDurableStopping) {
			return
		}
		c.durableTurnError("start durable turn", err)
		if committed {
			c.stopProcess("")
			c.retryDurableTurn()
		}
		return
	}
	if err == nil && heldAgy {
		// The holder accepted the prompt and outlives the daemon from here.
		// Record the prompt's boundary and take its input off the queue in
		// one write at once: a successor then waits for this prompt's
		// response and never submits its input again. Nothing is recorded
		// before the holder accepts, so a send the holder never took leaves
		// the input queued and no prompt for a successor to wait on.
		accepted := func(rec *Record) {
			rec.Queued = rest
			rec.Drained, rec.TurnMark, rec.Voided = c.drained, mark, c.voided
			if len(mail) > 0 {
				rec.Mail = nil
			}
			if len(reports) > 0 {
				rec.Reports = nil
				c.parkedReports = 0
			}
			c.setPromptBoundary(rec, startedAt, true)
			if ordinary {
				rec.PendingCompaction = nil
			}
			recordWorkerInstruction(rec, workerInstruction)
			// The open successor supersedes or publishes (below) a held
			// turn; the record never keeps both.
			rec.HeldTurn = nil
		}
		if isAgentWorkerProfile(c.rec.Profile) && c.heldRes != nil && len(reports) > 0 && !c.ownsLiveExec() {
			// A worker that still owns a live exec job keeps holding (below).
			// The reports open the held turn's successor, so the held turn
			// is published in that same write (as below for other workers):
			// a restart between two writes would restore the predecessor's
			// HeldTurn beside the successor's open turn, mark and consumed
			// reports.
			res := *c.heldRes
			c.turnMark, c.reply = mark, ""
			c.snapshotBoardRevision()
			c.hold(nil)
			c.publishWith(res, true, accepted)
		} else {
			c.update(accepted)
		}
	}
	if err != nil && cmd != nil && cmd.Name == "compact" {
		c.setStatus(func() { c.turn, c.lastErr = false, "compaction: "+err.Error() })
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
		c.update(func(rec *Record) { rec.Queued = rest })
		c.signalInbox()
		return
	}
	if err != nil {
		if !durable && ordinary && c.slot2aVerifyPending && !c.slot2aFallbackRetried && c.isSlot2aTurnRejection(err) {
			c.slot2aVerifyPending = false
			c.slot2aFallbackRetried = true
			c.setStatus(func() { c.turn, c.lastErr = false, "" })
			if fbErr := c.fallbackSlot2aTo2b(err); fbErr == nil {
				c.sendRetried = true
				c.signalInbox()
				return
			} else {
				err = fbErr
			}
		}
		// The turn never began: its process ends with no turn to close.
		c.setStatus(func() { c.turn, c.lastErr = false, "send turn: "+err.Error() })
		c.stopProcess("")
		// The turn stays queued (its mail stays claimed), and nothing else
		// would submit it before an unrelated signal: it is retried once on
		// a new process, through the loop's inbox signal rather than on
		// this stack. A send that fails again leaves it for the next inbox
		// or mail signal, so a send that always fails does not spin.
		if (len(c.rec.Queued) > 0 || len(c.rec.Mail) > 0 || (len(c.rec.Reports) > 0 && !c.reportsParked())) && !c.sendRetried {
			c.sendRetried = true
			c.signalInbox()
		} else {
			c.settleTurn(TurnFailed, c.lastError(), mark)
		}
		return
	}
	if ordinary {
		c.compactNote = ""
		c.compactTurnRotations = 0
		c.passive.ack(c.passiveTook)
		c.passiveTook = nil
		if !c.compactNudge.standalone {
			c.consumeCompactionNudge()
		}
		c.compactResume = false
		c.compactResumeInternal = false
		c.startModelWait(sentAt)
	}
	c.sendRetried = false
	c.mu.Lock()
	for send := range c.deliverySenders {
		if send <= mark {
			delete(c.deliverySenders, send)
		}
	}
	c.mu.Unlock()
	c.stopBgGrace()
	c.turnMark, c.reply = mark, ""
	c.snapshotBoardRevision()
	// A child report satisfies an agent worker's hold on the completed turn
	// that waited for it. Publish that turn before opening the report's
	// successor. Legacy pi-worker turns, and report-free successors such as
	// background follow-ups, retain their interim-supersession behavior.
	// A worker that still owns a live exec job keeps holding: its report
	// waits for the job, and this successor's own turn reports later.
	if !durable {
		if isAgentWorkerProfile(c.rec.Profile) && c.heldRes != nil && len(reports) > 0 && !c.ownsLiveExec() {
			res := *c.heldRes
			c.hold(nil)
			c.publish(res, heldAgy)
		} else {
			c.hold(nil)
		}
	}
	if !heldAgy && !durable {
		// A held agy worker wrote all of this when its holder accepted
		// the prompt (above); a second identical write would only cost.
		c.update(func(rec *Record) {
			rec.Queued = rest
			rec.Drained, rec.TurnMark, rec.Voided = c.drained, mark, c.voided
			rec.TurnStartedAt = startedAt
			rec.TurnStartSeq = max(c.seq, c.lineOpen)
			if len(mail) > 0 {
				rec.Mail = nil
			}
			if len(reports) > 0 {
				rec.Reports = nil
				c.parkedReports = 0
			}
			if ordinary {
				rec.PendingCompaction = nil
			}
			recordWorkerInstruction(rec, workerInstruction)
			if rec.Parent != nil {
				rec.TurnOpen, rec.HeldTurn = true, nil
			}
		})
	}
	if len(mail) > 0 {
		// The held mail reached a turn: no refusal is owed its senders.
		c.heldSenders = nil
	}
	// Mail shows on the page through the session's mail history, so only
	// the dashboard's own text is a user entry; a turn of mail alone is
	// marked as mail's.
	//
	// An exec job's completion notice is a background_exit wake, never the
	// user's text: it is recorded in queue order beside the user entries,
	// and a turn made of notices alone keeps the mail origin so the ring
	// marks it a wake turn.
	origin := wslive.OriginMail
	var shown []livePart
	if cmd != nil {
		shown = []livePart{{Text: cmd.Line()}}
	} else {
		for _, input := range inputs {
			shown = append(shown, livePart{Text: input.Text, Exec: input.Exec})
		}
		shown = append(shown, reportParts(reports)...)
		for i := range sentReports {
			// A text report shows as sent (with its legend, if any); a
			// notice keeps its envelope, which a legend would not parse.
			if p := &shown[len(shown)-len(reports)+i]; !p.Exec {
				p.Text = sentReports[i]
			}
		}
	}
	shown = slices.DeleteFunc(shown, func(p livePart) bool { return p.Text == "" })
	live := liveEvents(shown)
	if hasUserText(live) {
		origin = wslive.OriginWhiteboard
	}
	c.applyLive(live)
	for _, text := range submitted {
		c.apply(*wakeEvent("mail", "mailbox", "", text).Live)
	}
	c.apply(wslive.Event{Kind: wslive.KindTurnStart, Origin: origin})
	c.watched()
}

// voidCancelled counts the sends no drain took as voided when kick finds
// nothing to send with no turn running: every send a drain took went in a
// turn, so one the inbox no longer holds was cancelled from the page, and
// no turn will ever cover it. A conversation parent whose done turn waited
// for this child (openChildOf) is released. It runs in the loop.
func (c *conv) voidCancelled() {
	c.mu.Lock()
	held, err := wsinbox.Held(c.inbox)
	grew := err == nil && len(held) == 0 && c.sends > max(c.voided, c.settled.mark)
	if grew {
		c.voided = c.sends
		clear(c.deliverySenders)
		c.syncStateLocked()
	}
	parent := c.rec.Parent
	c.mu.Unlock()
	if grew && parent != nil && parent.Kind == ParentConversation {
		c.r.releaseLater(parent.ReplyID)
	}
}

// nextTurn splits queue at its first slash command: a command at the front
// is the turn (cmd), else the texts before the first command are (inputs).
// rest is what waits for the turns after it; nil when nothing does.
func nextTurn(queue []QueuedInput) (cmd *wsinbox.Command, inputs []QueuedInput, rest []QueuedInput) {
	n := 0
	if len(queue) > 0 && queue[0].Command != nil {
		cmd, n = queue[0].Command, 1
	} else {
		for n < len(queue) && queue[n].Command == nil {
			inputs = append(inputs, queue[n])
			n++
		}
	}
	if n < len(queue) {
		rest = slices.Clone(queue[n:])
	}
	return cmd, inputs, rest
}
