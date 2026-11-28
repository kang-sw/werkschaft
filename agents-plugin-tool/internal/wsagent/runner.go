package wsagent

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsawake"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

// KeyStore mints and keeps the ws session key of each conversation. The
// daemon command supplies ws-mcp's key store, which this package cannot
// import.
type KeyStore interface {
	// Mint creates a new key rooted at root.
	Mint(root string) (string, error)
	// Ensure keeps key usable for root: it refreshes the key's record, or
	// writes it again when the key store pruned it.
	Ensure(root, key string) error
}

// ChildKeyStore is a KeyStore that can mint a spawned child's key so the key
// inherits its spawner's session-scope config (review_phase, the sage knobs).
// The runner uses it when the spawning caller supplies its ws key
// (AgentCaller.PolicyKey) and falls back to Mint otherwise.
type ChildKeyStore interface {
	MintChild(root, parentKey string) (string, error)
}

// LeadKeyStore mints and keeps the lead ws session key of each hosted lead
// session (Profile.Lead); ws-mcp's key store implements it.
type LeadKeyStore interface {
	// LeadRoot is root's canonical git root, where a lead key lives; it
	// fails for a folder outside git.
	LeadRoot(root string) (string, error)
	// MintLead creates a parent-less lead key at root's canonical git root.
	MintLead(root string) (string, error)
	// EnsureLead keeps the lead key usable for its canonical root, as
	// KeyStore.Ensure does for a conversation key.
	EnsureLead(canonical, key string) error
	// LeadSession finds the lead key behind a registered session (the
	// parent-less lead key rooted at root whose reply-id is replyID) with
	// the harness conversation its last login reported, so the daemon can
	// take the session over under the same key. ok is false when no such
	// key is left.
	LeadSession(root, replyID string) (session LeadSession, ok bool)
}

// CompanionLeadKeyStore is an optional LeadKeyStore extension implemented by
// ws-mcp's AgentKeyStore to mint and maintain companion-scoped lead keys
// ("companion-body" or "companion-work") without requiring a git worktree root.
type CompanionLeadKeyStore interface {
	MintCompanionLead(root, role string) (string, error)
	EnsureCompanionLead(canonical, key, role string) error
}

// LeadSession is a terminal lead session as its ws key record describes it.
type LeadSession struct {
	// Key is the session's lead key and Root its canonical git root.
	Key, Root string
	// Harness is the harness that last logged in with the key, Session its
	// own name for the conversation (a claude session id, a pi session
	// file path), and Cwd the directory it ran in. All three are empty for
	// a key whose logins reported none.
	Harness, Session, Cwd string
}

// LeadMail is the ws mailbox of the sessions the runner owns, hosted leads
// and spawned children, as the runner uses it: the daemon, not the
// session's own mailbox wait, delivers their mail. A child's address
// (AgentAddress) is its key's reply-id queue, so mail to it lives in the
// mailbox store's memory and restore ring, never in a file of the
// runner's.
type LeadMail interface {
	// Watch calls changed whenever the mailbox of key may hold new mail,
	// from any goroutine, until the returned stop is called. While watched,
	// the session reads to its peers as listening.
	Watch(root, key string, changed func()) (stop func())
	// Drain claims every message waiting for key and returns each as the
	// text the session's model reads for it, oldest first. relation, when
	// not nil, names how a sender (its reply-id) relates to the session
	// ("parent", "child", "peer"; "" for none), for the message's head.
	Drain(root, key string, relation func(sender string) string) ([]ClaimedMail, error)
	// Return puts texts, mail a Drain claimed and the session never read,
	// back in key's mailbox for its next claim: the session moved to a
	// terminal harness, whose own mailbox wait delivers it.
	Return(root, key string, texts []string) error
}

// ClaimedMail is one message Drain claimed: the text the session reads,
// and its sender's reply-id, "" for system mail or a sender with none.
type ClaimedMail struct {
	Text   string
	Sender string
}

// Env names the runner sets on a hosted lead session's harness. The flag
// and the key are what the hosted-session mode reads; the row is the
// session's registration file, whose host_pid names the one process the
// flag holds for.
const (
	EnvHostedSession    = "WS_HOSTED_SESSION"
	EnvHostedSessionKey = "WS_HOSTED_SESSION_KEY"
	EnvHostedSessionRow = "WS_HOSTED_SESSION_ROW"
)

// Env names the runner sets on an attached ws-mcp.
const (
	// EnvAllowedTools is ws-mcp's tool allowlist: a comma list of ws tool
	// names; tools/list advertises and tools/call accepts only these.
	EnvAllowedTools = "WS_MCP_ALLOWED_TOOLS"
	// EnvNoDashboard turns ws-mcp's dashboard off (no daemon ensure, no
	// session registration), whatever its config resolves.
	EnvNoDashboard = "WS_MCP_NO_DASHBOARD"
)

// mailboxIdentityEnv are the variables that give a ws-mcp process a mailbox
// identity of its own; the attached ws-mcp gets them blank.
var mailboxIdentityEnv = []string{"WS_MAILBOX", "WS_MAILBOX_AUTO"}

// Options configures a Runner.
type Options struct {
	// LoadProviderConfig refreshes the daemon's in-memory provider settings.
	// It runs before session recovery and after a global configuration change.
	LoadProviderConfig func(wsconfig.Options) error
	// DaemonVersion bounds hosted package registry roots to this daemon's minor.
	DaemonVersion string
	// Dir is the dashboard daemon's state directory; conversations live in
	// its session registry (wsdashboard.SessionDir).
	Dir      string
	Backends map[string]BackendRegistration
	Keys     KeyStore
	// Config is the base of each profile resolution; RepoRoot is replaced by
	// the conversation's working root.
	Config wsconfig.Options
	// AgyModelWaitTimeoutSeconds is the selected hosted package's manifest
	// default for agy.model_wait_timeout_seconds. Nil uses the ws default.
	AgyModelWaitTimeoutSeconds *int
	// AgyModelWaitRetries is the selected hosted package's manifest default
	// for agy.model_wait_retries. Nil uses the ws default.
	AgyModelWaitRetries *int
	// AgyModelWaitBackoff returns the delay before automatic continue attempt
	// n (one-based). Nil uses bounded exponential jitter.
	AgyModelWaitBackoff func(attempt int) time.Duration
	// AgyLogDir is where agy writes its acp-worker-*.log files, which a
	// hosted agy lead's stall cutoff reads. Empty uses agy's own directory
	// under the user's home.
	AgyLogDir string
	// WSMCP is the stdio ws-mcp command serving root, without the runner's
	// environment; nil refuses profiles that allow ws tools.
	WSMCP func(root string) (MCPCommand, error)
	// DaemonMCP provides hosted-package MCP environment without a launcher.
	// Tests or standalone callers may supply the environment through WSMCP.
	DaemonMCP func(root string) (MCPCommand, error)
	// LeadManual renders the workflow manual of a hosted lead for its lead
	// key, from the selected package: backend is the lead's harness, dir
	// its working directory. LeadSessionState supplies the mutable state.
	// The lead's launch appends it to the harness's system prompt
	// (composeLeadInstructions), so the session reads it from its first
	// turn. internal/mcp renders the manual and imports this package, so the
	// daemon supplies the function; nil, or an error, leaves the prompt
	// with the key sentence alone.
	LeadManual func(backend, dir, key string) (string, error)
	// LeadSessionState renders current state for the first model-bound turn
	// after a lead process launches. A pending restore serves that turn instead.
	LeadSessionState func(key string) (string, error)
	// RestoreBundle renders fresh hosted session state at the point it is
	// delivered after compaction or daemon restart. children is the caller's
	// direct agent.list projection; the daemon owns the renderer so this
	// package does not read the ws session store itself. Nil keeps the Phase 1
	// key-bearing pointer note and disables restart restore.
	RestoreBundle func(backend, dir, key string, cause RestoreCause, children []AgentChild) (string, error)
	// PlaybookCommands lists the lead playbooks a hosted lead of a backend
	// with BackendRegistration.PlaybookCommands offers as dashboard
	// commands. The runner reads it once and keeps the list for its
	// lifetime; nil, or an error, offers none.
	PlaybookCommands func() ([]wslive.SlashCommand, error)
	// ExpandCommand renders the body of playbook command name for the
	// hosted lead whose key is key (backend its harness, dir its working
	// directory): the body the session's own playbook.read returns, which
	// the turn running the command carries, so the session needs no read of
	// its own. internal/mcp renders it, so the daemon supplies the function;
	// nil, or an error, sends a pointer to that read instead.
	ExpandCommand func(backend, dir, key, name string) (string, error)
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Timer starts the lever hold's cap timer (leverHoldCap): a channel
	// that fires once d passed, and a stop that ends it early; nil uses
	// time.NewTimer.
	Timer func(d time.Duration) (<-chan time.Time, func())
	// Log receives diagnostics; nil discards them.
	Log io.Writer
	// Holders is the session holder manager lead conversations run under,
	// LeadKeys their key store, and Mail their mailbox; a lead profile is
	// refused without the first two, and gets no mail without the third.
	Holders  HolderHost
	LeadKeys LeadKeyStore
	Mail     LeadMail
	// AuditSink receives the audit capture of every daemon-hosted lead and
	// worker process whose backend has an audit adapter (audit_capture.go);
	// nil captures nothing. A sink that also implements wsaudit.SourcedSink
	// gets source identities, so capture resumes across a restart without
	// duplicates. The holder manager should offer its audit channel
	// (wsholder.Options.AuditChannel) when this is set.
	AuditSink wsaudit.Sink
	// MailLegend places the sender legend on one submission of claimed mail
	// (a steer, or a turn's held mail): texts are exactly the mail texts
	// submitted together, in order, and senders the reply-ids Drain gave
	// them, nil for held texts (Record.Mail keeps no sender). It returns the
	// texts to submit. The legend is never stored: Record.Mail keeps Drain's
	// texts, so a held text gets a fresh legend when it is submitted. nil
	// submits the texts as they are.
	MailLegend func(root string, texts, senders []string) []string
	// Alive reports whether a process of this machine runs: what refuses a
	// takeover of a session whose terminal harness has not exited. nil is
	// wsstate.ProcessAlive.
	Alive func(pid int) bool
	// Trusted is the gate of a lead conversation's control writes (spawn,
	// settings, stop, close, approval answers); nil is
	// wsdashboard.TrustedWrite: a browser on this machine that sends the
	// daemon's own Origin.
	Trusted func(req *http.Request) (local, own bool)
	// CardBound reports whether conversation id is bound to a subordinate
	// whiteboard agent card (whose Record.Parent stays unset); nil defaults
	// to false. Used by IsChild for both HTTP settings refusal and SessionView.
	CardBound func(id string) bool
	// TrackerChanged, when not nil, is called after a conversation's
	// successful tracker write (an Event with TrackerWrite set), so the
	// daemon re-reads its tracker stores. It may be called from any
	// goroutine.
	TrackerChanged func()
	// AgyActivity, when not nil, is called after a live agy conversation's
	// turn settles. It may be called from any conversation loop and must not
	// block on the runner.
	AgyActivity func()
	// QuotaResetAt, when not nil, looks up a cached future quota reset time
	// (epoch ms) from daemon memory for (backend, model) when a usage-limit
	// alert arrives with ResetsAt == 0. Must not perform disk or network I/O.
	QuotaResetAt func(backend, model string) int64
	// WhiteboardRevision, when not nil, reads the in-memory revision of the
	// whiteboard a lead (working root, ws session key) already holds, 0 when
	// none is held. A whiteboard-only lead's turn that leaves it unchanged yet
	// answers at length in plain text earns a one-shot advisory. It must not
	// perform I/O or open a board, and may be called from any conversation loop.
	WhiteboardRevision func(root, key string) uint64
	// QuotaContinuationJitter, when not nil, returns the jitter duration added
	// past ResetsAt + buffer when scheduling a quota continuation (clamped to
	// [0, max]). Nil uses bounded random jitter.
	QuotaContinuationJitter func(max time.Duration) time.Duration
	// Report, when not nil, receives each settled turn of a conversation
	// with a parent (Runner.Spawn): a turn's end, a failed start, a process
	// lost mid-turn, and, when the next daemon loads the record, a turn
	// this daemon's exit cut off. Closing the conversation reports nothing.
	// It runs on the conversation's loop and must not block on the runner.
	Report func(TurnReport)
	// OnCompanionTurnEnd, when not nil, is called when a companion-role
	// conversation's turn settles (normal turn end, start failure, or
	// process-loss turn failure). Must not block on the runner.
	OnCompanionTurnEnd func(role, replyID, wsKey, status, errText string)
	// OnCompanionActivity, when not nil, is called on live turn activity of a
	// companion session or of a child spawned by a companion session (used to
	// renew sliding lease/delegation deadlines). Must not block on the runner.
	OnCompanionActivity func(role, replyID, wsKey string)
	// OnCompanionClose, when not nil, is called when a companion-role
	// conversation closes or its backend process exits unexpectedly
	// (reason: "close" or "crash"). Must not block on the runner.
	OnCompanionClose func(role, replyID, wsKey, reason string)
	// LiveHost is the daemon's in-memory snapshot hand-off. The daemon sets
	// it before Start; nil keeps the legacy file-only projection.
	LiveHost wsdashboard.LiveHost
	// TierRoles, when not nil, lists the packaged playbook role nodes of
	// the agent.tier_routing graph agent.spawn and agent.tiers resolve
	// through. Nil leaves only the built-in groups and user nodes.
	TierRoles func() ([]wsconfig.TierRole, error)
	// heartbeat overrides wslive.HeartbeatInterval for tests.
	heartbeat time.Duration
	// beforeExpiryClose pauses heartbeat expiry before starting its close (tests).
	beforeExpiryClose func()
	// beforeExpiryCascade pauses expiry between snapshot and fenced close (tests).
	beforeExpiryCascade func(id string)
	// beforeExpiryNodeClose pauses expiry right before closing node id (tests).
	beforeExpiryNodeClose func(id string)
	// admitCompletionAdmitting pauses AdmitCompletion between open and wmu (tests).
	admitCompletionAdmitting func(id string)
	// pageInputAdmitting pauses PageInput between conv lookup and wmu (tests).
	pageInputAdmitting func(id string)
	// WakeRefused, when not nil, tells the sender of a message Deliver
	// queued for a sleeping conversation whose start admission refused
	// now (from: the sender's reply-id, id: the conversation, reason).
	WakeRefused func(from, id, reason string)
	// WakeUndeliverable tells the sender when a permanent admission refusal
	// closes the worker. Nil falls back to WakeRefused.
	WakeUndeliverable func(from, id, reason string)
	// CheckoutReleased, when not nil, tells the sender of a message for a
	// spawned conversation whose checkout was released after it was bound
	// (from: the sender's reply-id, id: the conversation, checkout: the
	// released checkout, reason). Such a conversation never launches, wakes
	// or resumes there again. Nil falls back to WakeRefused.
	CheckoutReleased func(from, id, checkout, reason string)
	// ChildWaitingDays overrides agent.child_waiting_days; 0 reads it.
	ChildWaitingDays int
	// ParkIdleChildren lets a waiting spawned child leave memory until a
	// message, mail or control wakes it (runner_park.go). The daemon sets it.
	ParkIdleChildren bool
	// keepAwake replaces the platform's idle-sleep backend in tests.
	keepAwake wsawake.Backend
	// spawnListed observes global spawn-projection listings in tests.
	spawnListed func(string)
	// stateInboxRead observes state-held inbox scans in tests.
	stateInboxRead func(string)
	// detachFinishedAtShutdown makes a test runner's shutdown (letGo)
	// detach a finished-idle child's holder instead of putting it to sleep,
	// as a crash or a predecessor daemon without that sleep leaves it, so
	// a test can exercise the successor's rejoin of an idle held worker.
	detachFinishedAtShutdown bool
	// closedSnapshotRead observes closed/handed-off snapshot reads in tests.
	closedSnapshotRead func(string)
	// sendAdmitting observes the revival admission fence in tests.
	sendAdmitting func(string)
	// OnHired, when not nil, notifies the daemon's in-memory hiring and label
	// caches after SQLite MarkHired commits for a newly created hired lead.
	OnHired func(replyID, requestID, label string)
	// AccountSlots is the daemon's in-memory per-harness account slot registry
	// (D11). Nil uses the implicit default slot for every conversation.
	AccountSlots *wscred.AccountSlotRegistry
	// ExplicitHome mirrors DaemonOptions.Home for the D10 home resolver so an
	// explicit home suppresses inherited CODEX_HOME / CLAUDE_CONFIG_DIR env.
	ExplicitHome string
	// SlotAuthPreflight overrides a backend's default target-slot auth
	// pre-flight check in tests. Nil delegates to the backend's
	// accountSlotContinuity implementation.
	SlotAuthPreflight func(backend, configDir string) error
	// beforeDurableWrite injects a failed private record write in tests.
	beforeDurableWrite     func(Record) error
	removeRetiredPiArchive func(string) error // archive removal fault in tests
	// suppressReportDelivery simulates loss after settlement commits in tests.
	suppressReportDelivery func(Record) bool
}

// Runner hosts the agent conversations of one dashboard daemon.
type Runner struct {
	opts   Options
	notify func(replyID, project string)
	// Opened during daemon startup under the dashboard lock, then held
	// until all conversations have drained.
	// meta and metaSnap bind the shared daemon SQLite metadata store when
	// cutover is active (SetMeta); the daemon owns closing meta.
	meta               *wsmeta.Store
	metaSnap           *wsmeta.Snapshot
	recordBaseMu       sync.Mutex
	recordBase         map[string][]byte // reply-id -> last persisted JSON in SQLite
	perms              sync.Map          // reply-id -> *permissionSelection (when meta != nil)
	hiredByReqMu       sync.Mutex
	hiredByReq         map[string]string // request-id -> reply-id (when meta != nil)
	durableRecords     *wsmeta.Store
	durableProjections sync.Map   // reply-id -> last published projection digest
	deadClosureMu      sync.Mutex // serializes persistence of holder-backed dead closures
	piPurgeMu          sync.Mutex
	piPurgeStore       *wsmeta.Store
	piPurges           map[string]string // reply ID -> worktree, under piPurgeMu after startup
	piArchiveIDs       map[string]bool   // under piPurgeMu after startup
	piPurgeLoadErr     error
	// Full Quit refuses durable starts and input before draining transitions
	// that already hold admission.
	durableStopping    atomic.Bool
	durableAdmissionMu sync.RWMutex
	// fixedProfile belongs only to a private one-shot answer runner. Freeze
	// the checked ceiling so config edits cannot widen it between create
	// and process start. Ordinary conversations still resolve on every start.
	fixedProfile *ResolvedProfile

	mu            sync.Mutex
	convs         map[string]*conv
	closed        map[string]Record
	childrenIndex map[ParentRef]map[string]bool // mu; parent identity is immutable
	pendingSpawns map[ParentRef]int             // mu; in-flight width-checked spawns until registered
	// liveOwners mirrors resident *conv entries and parked records
	// (parkedOwnerSentinel) for lock-free OwnsLive lookups without r.mu or c.mu.
	liveOwners sync.Map
	// releases is each checkout's latest completed release this daemon
	// knows of, keyed by releaseKey: fed by worktree.release's notice and by
	// every marker a cold check reads. It holds only releases found, never
	// their absence, and only ever moves later (worktree_release.go). Under
	// relMu, never held with mu.
	relMu    sync.Mutex
	releases map[string]time.Time
	// expiry is childWaitingExpiry's value, read once (expiryOnce).
	expiryOnce sync.Once
	expiry     time.Duration
	// awake holds the idle-sleep assertion while live turns run and
	// agent.keep_awake_during_turns is on (keep_awake.go); awakeReloadMu
	// serializes the setting's read with its publish.
	awake         *wsawake.Core
	awakeReloadMu sync.Mutex
	quit          chan struct{}
	wg            sync.WaitGroup
	once          sync.Once
	// summaryMu only guards the two maps below, briefly; it is never held
	// across another lock or I/O (summaryLock, setSummaryOwner,
	// summaryOwner). summaryLocks holds one mutex per reply-id, created
	// lazily and never deleted, so every conversation generation of a
	// session shares it: a writer holds its own session's mutex through
	// summary mutation/ring I/O (lockSummaryOwner) without blocking another
	// session's snapshot publish, and summaryOwners[id] changes only under
	// that mutex.
	summaryMu     sync.Mutex
	summaryOwners map[string]*conv
	summaryLocks  map[string]*sync.Mutex
	// closedRows caches immutable closed states under mu, until reopened.
	// Handed-off snapshots still belong to a terminal and are not cached.
	closedRows map[string]State
	// parked marks the ids of closed that are open, waiting spawned
	// children with no loop (runner_park.go): their record stays in closed
	// so every read of a record sees them, and a wake path rehydrates the
	// conversation (unpark). parkStop holds each one's mail watch stop and
	// unparking the rehydrations under way. expiring holds a close-barrier
	// channel per node while an expiry cascade is closing its tree, fencing
	// concurrent spawns against publishing live children under an expiring
	// ancestor. All under mu.
	parked    map[string]bool
	parkStop  map[string]func()
	unparking map[string]chan struct{}
	expiring  map[string]chan struct{}
	// convGen counts resident/parked incarnation transitions per reply-id under
	// mu so expiry fences detect any unpark, re-park, or reopen since snapshot.
	convGen map[string]uint64
	// closing is set under mu by Close before closing quit and calling wg.Wait,
	// fencing late park-wake and expiry callbacks from calling wg.Add.
	closing bool
	// Retired Pi process bindings are independent of the ended history rows.
	// The holder manager supplies their liveness. Guarded by mu.
	retiredPiBindings map[string]retiredPiBinding
	// Closed dead children can still occupy their checkout while a process lives.
	deadBindings map[string]deadBindingOccupancy
	deadRefusals map[string]*admissionRefusal
	// resumeMu serializes Pi child revival with close cascades without
	// holding mu over store writes or conversation startup.
	resumeMu sync.Mutex
	// hireMu serializes idempotent owner-approved creation retries.
	hireMu sync.Mutex
	// scanned is closed once the holder manager's first scan returned, or
	// at Start when there is no manager (scanDone); scanOnce closes it.
	scanned  chan struct{}
	scanOnce sync.Once
	// rejoined gates nested calls until held-process capabilities are validated.
	rejoined   chan struct{}
	rejoinOnce sync.Once
	// started is set when Start begins: from then until rejoined closes, a
	// token not yet registered may still be restored by the startup rejoin.
	started atomic.Bool
	// startupLoaded closes after Start installed every open record. Restore
	// markers may be present before then, but compactIdle sends none until the
	// complete direct-child projection exists. serving, when non-nil
	// (StartForPublication), closes once the daemon has published its endpoint
	// and started accepting connections; processless conversations defer
	// starting new harness processes until then so they never self-deadlock on
	// an unpublished /mcp/session endpoint.
	startupMu     sync.RWMutex
	startupLoaded chan struct{}
	serving       <-chan struct{}
	// onDeferKick, when non-nil in tests, runs between deferKickIfUnpublished's
	// initial !publicationReady() check and kickDeferred.Store(true) to exercise
	// the store-then-recheck boundary deterministically.
	onDeferKick func()
	// playbookCmds is Options.PlaybookCommands' list, read once
	// (playbookCommands) under playbookMu.
	playbookMu     sync.Mutex
	playbookLoaded bool
	playbookCmds   []wslive.SlashCommand

	// holders routes the session holders' events to the lead processes.
	holders holderTable

	// cron is the daemon's scheduler, told when a conversation is gone
	// for good (BindCron).
	cron atomic.Pointer[cronLink]

	// watchers are Watch's callbacks, by registration (watchMu).
	watchMu  sync.Mutex
	watchers map[*func(State)]struct{}

	// The spawn tree (children.go), under mu: endpoint is the daemon's
	// loopback base URL (SetEndpoint), tokens the capability tokens of
	// the running processes by token, and tokenOf by conversation id.
	endpoint string
	tokens   map[string]string
	tokenOf  map[string]string
	// mcpTokens are the hosted ws-mcp session tokens (mcp_session.go) by
	// token, and mcpTokenOf by conversation id.
	mcpTokens  map[string]*mcpToken
	mcpTokenOf map[string]string
	// mcpWaitMisses correlates early timeouts with later worker restoration.
	// It holds only bounded, ephemeral digests under mu.
	mcpWaitMisses map[[32]byte]struct{}
	// versions are the harness versions processes last reported, by
	// backend (harness_versions.go).
	versions *harnessVersions
	// exec is the hosted exec jobs' view (exec_lifecycle.go).
	exec execState
}

// NewRunner returns a runner; Start loads its conversations.
