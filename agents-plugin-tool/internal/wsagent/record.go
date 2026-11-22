package wsagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// recordFileName is the conversation record's file in its session
// directory, beside the wslive snapshot and ring.
const recordFileName = wsdashboard.AgentRecordFileName

const recordSchemaVersion = 1

// Record is one conversation's persisted state: everything a process start
// needs after a daemon restart. Field names are an on-disk format. The file
// is private to the user (it holds the ws session key).
type Record struct {
	SchemaVersion int    `json:"schema_version"`
	ReplyID       string `json:"reply_id"`
	Profile       string `json:"profile"`
	// ResolvedDefinition is the Pi worker's last launched profile, not a
	// re-resolution of its name. It is the capability ceiling for children
	// and the definition used when reattaching its still-running process.
	// Older records snapshot on their next launch.
	ResolvedDefinition *Profile `json:"resolved_definition,omitempty"`
	Backend            string   `json:"backend"`
	Root               string   `json:"root"`
	// SessionID is the backend session the next process resumes; empty
	// until the first process reported one.
	SessionID string `json:"backend_session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	// WSKey is the conversation's ws session key; it never leaves this file,
	// the composed instruction file, and the child processes.
	WSKey string `json:"ws_session_key"`
	// Listed lists the conversation in the dashboard session index (the
	// profile's list_in_index when the conversation last started).
	Listed    bool   `json:"listed,omitempty"`
	CreatedAt string `json:"created_at"`
	// CreatedUnixNano is the creation instant at nanosecond precision, which
	// orders a binding against a worktree release in the same second
	// (wsgit.BoundBeforeRelease). Additive: a record from before it has 0,
	// and an older writer keeps it as an unknown member.
	CreatedUnixNano int64  `json:"created_unix_nano,omitempty"`
	ClosedAt        string `json:"closed_at,omitempty"`

	// HandedOffAt is when a lead session was handed to a terminal harness
	// (conv.release): the daemon serves it no more, as it serves no closed
	// one, until a takeover clears it. A non-lead with HolderID uses this
	// field as an old-daemon fence; heldWorker keeps it live in this daemon.
	HandedOffAt string `json:"handed_off_at,omitempty"`

	// Lead marks a hosted lead session (Profile.Lead when it was opened):
	// WSKey is a lead key, Root its canonical git root, and the fields
	// below apply.
	Lead bool `json:"lead,omitempty"`
	// PermissionMode is the permission mode the next process launches in.
	PermissionMode string `json:"permission_mode,omitempty"`
	// AgyBypass is ws-side approval intent; PermissionMode stays native.
	// Keep it top-level so older Record writers retain it as unknown data.
	AgyBypass bool `json:"agy_bypass,omitempty"`
	// PluginIsolation selects the lead's plugin loadout across restarts.
	PluginIsolation string `json:"plugin_isolation,omitempty"`
	// PluginWarning records a bundled-plugin fallback warning, if any.
	PluginWarning string `json:"plugin_warning,omitempty"`
	// Cwd is the directory the session's process runs in when that is not
	// Root: a session taken over from a terminal harness keeps the
	// directory that harness ran in, since the harness resolves its
	// sessions by it, and a spawned conversation (Runner.Spawn) the one its
	// spawner chose. SessionFile is the session file of a taken-over
	// session's harness when it keeps its conversations in files of its
	// own (pi), used in place of the conversation's own file. Both are
	// empty for any other session the daemon started.
	Cwd         string `json:"cwd,omitempty"`
	SessionFile string `json:"session_file,omitempty"`
	// HolderID is the session holder the conversation's process runs under,
	// empty when it has none; HostPID is that process. No new process
	// starts while this holder's child still runs.
	HolderID string `json:"holder_id,omitempty"`
	HostPID  int    `json:"host_pid,omitempty"`
	// HolderSeq is the last record sequence of HolderID the runner applied
	// whole: after a daemon restart the holder replays its ring, and
	// records up to this one rebuild state without being written again.
	HolderSeq uint64 `json:"holder_seq,omitempty"`
	// Answered are the request ids of the tool permission prompts answered
	// and not yet seen settled. The answer went to the process's input,
	// which the holder's ring does not hold, so a replay shows such a
	// prompt as still waiting; this list keeps it answered exactly once.
	Answered []string `json:"answered,omitempty"`
	// Mail holds the mailbox messages claimed for the session and not yet
	// delivered as a turn, each as the text the model reads.
	Mail []string `json:"mail,omitempty"`
	// Queued holds what a drain took from the inbox and no turn has sent
	// yet, in drain order: a slash command runs as a turn of its own, so the
	// messages and commands drained after it wait here across turn ends,
	// although the inbox already shows them received.
	Queued []QueuedInput `json:"queued,omitempty"`

	// Parent is the session that spawned the conversation (Runner.Spawn),
	// to which each of its settled turns is reported (Options.Report); nil
	// for one opened from the dashboard or the REST routes. Label is the
	// one-line name its spawner gave it.
	Parent *ParentRef `json:"parent,omitempty"`
	Label  string     `json:"label,omitempty"`
	// SpawnerBackend is the verified backend of the session that spawned
	// the conversation (AgentCaller.VerifiedBackend), stamped once at
	// spawn: empty when the spawner had no verified binding, or for a
	// record made before the stamp, whose startup load derives it from its
	// parent's record (resolveSpawnerBackends). A backend registered with a
	// cross-harness refusal admits the record from it (admitWorkerRecord).
	// It stays out of Parent, whose whole value keys the child index and
	// parent ownership, and top-level so an older writer keeps it as
	// unknown data.
	SpawnerBackend string `json:"spawner_backend,omitempty"`
	// TurnOpen is set on a conversation with a Parent while one of its
	// turns runs: a successor rejoins a held process, or reports interruption
	// when that process is gone.
	TurnOpen bool `json:"turn_open,omitempty"`
	// Tier is the capability tier its spawner chose (small, medium, large,
	// or xlarge); a conversation it spawns may ask for no higher one.
	// Empty for a conversation spawned without one, which spawns none.
	Tier string `json:"tier,omitempty"`
	// SystemPrompt marks a conversation agent.spawn gave a system prompt:
	// its text is in the session directory's system_prompt.md, written
	// once at spawn and appended to the harness's system prompt at every
	// process start (agent_tools.go).
	SystemPrompt bool `json:"system_prompt,omitempty"`
	// LastActiveAt (RFC 3339) is a spawned conversation's last send or
	// settled turn: the start of its child-agent-waiting expiry
	// (agent_tools.go). Empty is CreatedAt.
	LastActiveAt string `json:"last_active_at,omitempty"`
	// PendingReports are the reported turns of a conversation whose parent
	// is a conversation (ParentConversation) that the runner has not handed
	// to that parent yet, oldest first: a report's absence is its
	// "delivered" mark. A report whose parent is closed stays here.
	PendingReports []PendingReport `json:"pending_reports,omitempty"`
	// Reports are child reports and steered user messages no turn took,
	// oldest first: they go with its next turn, as a lead's claimed Mail
	// does. A refused or untaken steer is cleared from Pi before requeue.
	Reports []string `json:"reports,omitempty"`
	// LastTurn is the last reported turn of a conversation with a Parent,
	// with the id of the pending report it made (empty when its parent's
	// wait took it instead): the next daemon answers a wait with it at
	// once, since the settles a wait reads are not persisted. Nil before
	// the first report and while a done turn is held for the children.
	LastTurn *PendingReport `json:"last_turn,omitempty"`
	// Worker recovery fields are additive; old writers fall back to interruption.
	ReportID         string `json:"report_id,omitempty"`
	LastReportedTurn string `json:"last_reported_turn,omitempty"`
	Capability       string `json:"capability_secret,omitempty"`
	Sends            uint64 `json:"sends,omitempty"`
	// OpenGen counts the explicit closes of this conversation (closeIf): a
	// revival keeps the count, so an exec job admitted before a close can
	// tell its owner was closed since and drop its completion notice.
	OpenGen uint64 `json:"open_gen,omitempty"`
	// AcceptedEvents are the newest system notice event ids the runner
	// admitted (AdmitCompletion), at most acceptedEventsCap: they keep a
	// replayed notice out once its inbox entry has been drained and trimmed.
	AcceptedEvents []string `json:"accepted_events,omitempty"`
	// ExecCutoverHolder is the holder of a process started under the exec
	// cutover (exec_cutover.go). A rejoin restores the cutover only when the
	// holder it attaches is this one, so a process an older daemon started
	// since keeps its native tools.
	ExecCutoverHolder string `json:"exec_cutover_holder_id,omitempty"`
	Drained           uint64 `json:"drained,omitempty"`
	TurnMark          uint64 `json:"turn_mark,omitempty"`
	// An agy worker can also have a prompt outstanding with TurnOpen false:
	// an internal compaction prompt has no result to report to its parent.
	TurnStartedAt int64 `json:"turn_started_at,omitempty"`

	// MCPToken is a held process's ws-mcp session token
	// (mcp_session.go), kept so a later daemon rejoining the process
	// accepts the token it still sends. MCPURL is the route the process
	// sends it to, which that daemon may no longer serve; MCPEnv the ws-mcp
	// entry's environment its hello was built from (the bundle the process
	// loaded, whatever is selected now); MCPHolder the holder the token was
	// drawn for, so a token a later start (by a daemon that keeps the
	// members without knowing them) left behind is told from a live one.
	MCPToken  string            `json:"mcp_token_secret,omitempty"`
	MCPURL    string            `json:"mcp_url,omitempty"`
	MCPEnv    map[string]string `json:"mcp_env,omitempty"`
	MCPHolder string            `json:"mcp_holder_id,omitempty"`

	// ActiveMs is the accumulated duration of closed child turns.
	ActiveMs     int64           `json:"active_ms,omitempty"`
	TurnStartSeq uint64          `json:"turn_start_seq,omitempty"`
	Voided       uint64          `json:"voided,omitempty"`
	HeldTurn     *TurnResult     `json:"held_turn,omitempty"`
	Steered      []steeredReport `json:"steered_reports,omitempty"`
	// SteerMarks holds Send coverage by steer sequence. Keep this optional
	// metadata top-level so an older Record writer preserves it as unknown.
	SteerMarks     map[int]uint64 `json:"steer_marks,omitempty"`
	SteerSeq       int            `json:"steer_seq,omitempty"`
	SteerAnswers   int            `json:"steer_answers,omitempty"`
	SteerAnswerSeq uint64         `json:"steer_answer_seq,omitempty"`

	// unknown holds newer writers' top-level members across read-modify-write.
	// It is immutable after decoding, so value copies of Record can share it.
	unknown map[string]json.RawMessage
}

// UnmarshalJSON keeps fields this daemon does not know without treating known
// optional fields as unknown when their next value is omitted.
func (rec *Record) UnmarshalJSON(data []byte) error {
	type plain Record
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	fields := reflect.TypeOf(decoded)
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		// encoding/json accepts case-insensitive spellings of known names.
		for key := range members {
			if strings.EqualFold(key, name) {
				delete(members, key)
			}
		}
	}
	*rec = Record(decoded)
	rec.unknown = members
	return nil
}

// MarshalJSON overlays the current known fields without changing the retained
// unknown members (other copies of this Record may still use them).
func (rec Record) MarshalJSON() ([]byte, error) {
	type plain Record
	raw, err := json.Marshal(plain(rec))
	if err != nil || len(rec.unknown) == 0 {
		return raw, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	for key, value := range rec.unknown {
		if _, known := members[key]; !known {
			members[key] = value
		}
	}
	return json.Marshal(members)
}

// PendingReport is a settled turn's report waiting for its conversation
// parent (Record.PendingReports). ID tells one report from another, so
// the wait that answers with a turn takes that turn's report alone.
type PendingReport struct {
	ID     string     `json:"id"`
	Result TurnResult `json:"result"`
	Mark   uint64     `json:"mark,omitempty"`
}

// Parent kinds a ParentRef names.
const (
	// ParentLead is a lead session, reached through its mailbox reply-id:
	// its child's turn reports arrive as mail.
	ParentLead = "lead"
	// ParentConversation is a conversation of this runner (a spawned Pi
	// worker), reached by its id: the runner delivers its child's turn
	// reports into it itself (children.go).
	ParentConversation = "conversation"
)

// ParentRef names the session that spawned a conversation. It holds the
// parent's reply-id, never its ws session key, so no credential of the
// parent's lands in the child's record; Kind says how the parent is
// reached (ParentLead, ParentConversation).
type ParentRef struct {
	Kind    string `json:"kind"`
	ReplyID string `json:"reply_id"`
}

// QueuedInput is one drained dashboard input waiting for its turn: a
// message's text, or a slash command.
type QueuedInput struct {
	Text    string           `json:"text,omitempty"`
	Command *wsinbox.Command `json:"command,omitempty"`
	// After is the send coverage before this input's drain.
	After uint64 `json:"after,omitempty"`
}

// workDir is the directory the conversation's process runs in.
func (rec Record) workDir() string {
	if rec.Cwd != "" {
		return rec.Cwd
	}
	return rec.Root
}

func recordPath(sessionDir string) string { return filepath.Join(sessionDir, recordFileName) }

// ReadRecord reads the conversation record of reply-id replyID from the
// dashboard state directory dir. ws-mcp reads it to recognise a
// conversation's own key (its tracker gate); the record is the daemon's,
// written only by the runner.
func ReadRecord(dir, replyID string) (Record, error) {
	if !wsdashboard.ValidReplyID(replyID) {
		return Record{}, fmt.Errorf("invalid reply-id %q", replyID)
	}
	return readRecord(wsdashboard.SessionDir(dir, replyID))
}

// WriteRecordForTest writes rec as reply-id rec.ReplyID's conversation
// record under dir, as the runner does. It exists for other packages' tests
// of ReadRecord callers; production code never writes a record outside the
// runner.
func WriteRecordForTest(dir string, rec Record) error {
	return writeRecord(wsdashboard.SessionDir(dir, rec.ReplyID), rec)
}

func readRecord(sessionDir string) (Record, error) {
	raw, err := readFile(recordPath(sessionDir))
	if err != nil {
		return Record{}, err
	}
	return decodeRecord(recordPath(sessionDir), filepath.Base(sessionDir), raw)
}

// decodeRecord is readRecord's decode of the agent.json at path in the
// session directory of replyID.
func decodeRecord(path, replyID string, raw []byte) (Record, error) {
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Record{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if rec.ReplyID != replyID || rec.WSKey == "" || rec.Root == "" {
		return Record{}, fmt.Errorf("%s is not a complete agent record", path)
	}
	return rec, nil
}

// ValidateLegacyRecord reports whether readRecord would accept raw as the
// agent.json of replyID's session directory. The daemon metadata import uses
// it as its agent_record validator (wsmeta.LegacyValidator: rel, the
// capture's dashboard-relative path, is not needed), so a record the legacy
// reader rejects stays out of the live store.
func ValidateLegacyRecord(_, replyID string, raw []byte) error {
	_, err := decodeRecord(recordFileName, replyID, raw)
	return err
}

func writeRecord(sessionDir string, rec Record) error {
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = recordSchemaVersion
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(recordPath(sessionDir), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if observe := recordWritten.Load(); observe != nil {
		(*observe)(sessionDir, rec)
	}
	return nil
}

// recordWritten, when set by a test, observes every record written, so a
// test can restart from the record as it stood after any one write.
var recordWritten atomic.Pointer[func(sessionDir string, rec Record)]

// writeFileAtomic replaces path with data through a temporary file in the
// same directory.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := wsstate.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := replaceFile(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// liveWriter keeps one conversation's wslive state and writes it to the
// session directory: ring entries as they finish, snapshots for recovery.
// A hosted slot receives every change; file-only writers keep disk heartbeats.
type liveWriter struct {
	dir string
	st  *wslive.State
	now func() time.Time
	log io.Writer
	// beforeSnapshot refreshes daemon-owned rows in this writer's loop.
	beforeSnapshot func()
	owner          func() (func(), bool)
	// ownerRAM is owner's in-memory fence for a publish that only reaches
	// the live slot (applyRAM); owner is used when it is unset.
	ownerRAM func() (func(), bool)
	wake     func()
	slotMu   sync.Mutex
	slot     wsdashboard.HostedSlot
	// The last successfully written disk projection, without its timestamp.
	lastSnapshot []byte
	lastDiskAt   time.Time
	// ringDirty is loop-owned: only a real ring append advances the replay fence.
	ringDirty bool
	// writeSnapshotFile is the atomic writer, replaceable for deterministic IO counts.
	writeSnapshotFile func(string, []byte, os.FileMode) error
	// appendRing is the ring appender (wslive.AppendRing), replaceable so a
	// test can stall one session's ring I/O under its owner locks.
	appendRing func(string, []wslive.RingEntry) error
	// outputRates is a lazy held append handle; pendingOutputRate is persisted
	// only with an existing snapshot recovery flush, never in apply's hot path.
	outputRates       *wslive.OutputRateLog
	pendingOutputRate *wslive.OutputRate
	// Only an observed holder agent_start supplies this real identity.
	summarySource     string
	summaryNaturalEnd bool
	// observedSeeded reports that st seeded from the whole evidence
	// directory (wsdashboard.ObservedFoldSlot).
	observedSeeded bool
	// restored is what the snapshot an earlier daemon left says of agy
	// native rows, for the first replay to end (takeRestored).
	restored restoredAgyNative
	// coverage names the holder records the state applied, written into the
	// recovery file (wslive.Snapshot.HolderID, HolderSeq); unset writes none.
	coverage func() (holderID string, seq uint64)
	// diskHold keeps the recovery file as an earlier daemon left it while
	// a rejoin's replay has not yet folded that file's evidence (its running
	// agy native rows, its holder coverage) into the state: a write before
	// then would replace the checkpoint a second interruption recovers
	// from with one that lacks it. Snapshots still reach the live slot.
	// Loop-owned; set before the conversation's first write and cleared
	// once the replay's rows are resolved (conv.finishAgyNativeReplay).
	diskHold bool
}

// restoredAgyNative is what a restored snapshot says of agy native rows: the
// rows it lists (any status), those it lists running (as listed), its write
// time, and the holder records it covers (covered, with holderID and
// coveredSeq; unset from a writer that predates them). readable is false
// when no snapshot of this session could be read.
type restoredAgyNative struct {
	readable   bool
	updatedAt  int64
	listed     map[string]bool
	running    map[string]wslive.Agent
	covered    bool
	holderID   string
	coveredSeq uint64
}

// evicted reports whether a row a replay rebuilt from records the previous
// daemon applied alone, first seen at holder record startSeq of holderID,
// was evicted by retention: the snapshot is readable and does not list it,
// and its start is one the snapshot covers. Without the snapshot's coverage
// (a writer that predates it) an unlisted row counts as evicted. Coverage
// of another holder covers none of this one's records, and a known empty
// coverage (seq 0) none of any.
func (r restoredAgyNative) evicted(id, holderID string, startSeq uint64) bool {
	if !r.readable || r.listed[id] {
		return false
	}
	if !r.covered {
		return true
	}
	return r.holderID == holderID && startSeq <= r.coveredSeq
}

// restoredAgyNativeOf reads prev, a snapshot of session replyID.
func restoredAgyNativeOf(prev wslive.Snapshot, replyID string) restoredAgyNative {
	if prev.ReplyID != replyID {
		return restoredAgyNative{}
	}
	r := restoredAgyNative{readable: true, updatedAt: prev.UpdatedAt, listed: map[string]bool{}, running: map[string]wslive.Agent{}}
	if prev.HolderID != "" && prev.HolderSeq != nil {
		r.covered, r.holderID, r.coveredSeq = true, prev.HolderID, *prev.HolderSeq
	}
	for _, a := range prev.Agents {
		if !strings.HasPrefix(a.ID, wslive.NativeAgyPrefix) {
			continue
		}
		r.listed[a.ID] = true
		if a.Status == wslive.StatusRunning {
			r.running[a.ID] = a
		}
	}
	return r
}

// takeRestored returns what the restored snapshot said, once: after the
// first replay ended, the writer's own state is the record a later one
// would rebuild against, readable and with nothing restored running.
func (w *liveWriter) takeRestored() restoredAgyNative {
	r := w.restored
	w.restored = restoredAgyNative{readable: true}
	return r
}

// newLiveWriter starts from the finished subagents of the snapshot an
// earlier daemon left in dir: the first write replaces that snapshot, and
// nothing else remembers them. A missing or unreadable one seeds nothing.
func newLiveWriter(dir, key, adapter string, now func() time.Time, log io.Writer) *liveWriter {
	st := wslive.NewState(key)
	st.SetAdapter(adapter)
	records, folded, err := wslive.ReadObservedFold(dir)
	if err == nil {
		st.SeedObserved(records)
	}
	if rate, err := wslive.ReadOutputRate(dir); err == nil && rate != nil {
		st.SeedOutputRate(rate)
	} else if err != nil {
		folded = false
	}
	var restored restoredAgyNative
	if prev, err := wslive.ReadSnapshot(wslive.SnapshotPath(dir)); err == nil {
		restored = restoredAgyNativeOf(prev, st.ReplyID())
		st.SeedFinished(prev)
		st.SeedLastTurn(prev)
		if prev.Lead.OutputRate != nil {
			st.SeedOutputRate(prev.Lead.OutputRate)
		}
		if u := prev.Usage; u != nil {
			_, _ = st.Apply(wslive.Event{Kind: wslive.KindStatus, Context: u.Context, Cost: u.Cost, Limits: u.Limits, LimitsAsOf: u.LimitsAsOf}, now().UnixMilli())
		}
		// The command list too: a session whose process is not running
		// reports it again only at its next turn (claude) or start (pi),
		// and the page offers commands only while the list is there.
		if len(prev.Commands) > 0 {
			_, _ = st.Apply(wslive.Event{Kind: wslive.KindCommands, Commands: prev.Commands}, now().UnixMilli())
		}
	}
	return &liveWriter{
		dir: dir, st: st, now: now, log: log,
		writeSnapshotFile: writeFileAtomic,
		appendRing:        wslive.AppendRing,
		observedSeeded:    folded,
		outputRates:       wslive.NewOutputRateLog(dir),
		restored:          restored,
	}
}

// apply applies ev and writes what it changed; it reports whether anything
// was written.
func (w *liveWriter) apply(ev wslive.Event) bool {
	return w.applyMode(ev, false)
}

// applyCoalesced publishes ev in RAM but leaves its recovery write to the
// immediately following TurnEnd snapshot.
func (w *liveWriter) applyCoalesced(ev wslive.Event) bool {
	return w.applyMode(ev, true)
}

// applyRAM applies ev and publishes it to the live slot alone, under the
// in-memory ownership fence (ownerRAM): no file is read or written. What it
// changed reaches disk with the next snapshot write.
func (w *liveWriter) applyRAM(ev wslive.Event) bool {
	res, err := w.st.Apply(ev, w.now().UnixMilli())
	if err != nil {
		fmt.Fprintf(w.log, "agent %s: drop %s event: %v\n", w.st.ReplyID(), ev.Kind, err)
		return false
	}
	if len(res.Ring) > 0 || len(res.Observed) > 0 || res.OutputRate != nil {
		// Not a RAM-only change after all: take the file-fenced path.
		return w.write(res, nil, ev.Kind, true)
	}
	if !res.Changed {
		return false
	}
	slot := w.currentSlot()
	if slot == nil {
		return true
	}
	fence := w.ownerRAM
	if fence == nil {
		fence = w.owner
	}
	if fence != nil {
		release, ok := fence()
		if !ok {
			return true
		}
		defer release()
	}
	slot.Publish(w.st.Snapshot(w.now().UnixMilli()))
	return true
}

func (w *liveWriter) applyMode(ev wslive.Event, coalesceTurnEnd bool) bool {
	var res wslive.Result
	var err error
	if ev.ReasoningSummary != nil || (ev.Kind == wslive.KindTurnEnd && w.st.Snapshot(w.now().UnixMilli()).Lead.ReasoningSummary != nil) {
		res, err = w.applySummary(ev)
	} else {
		res, err = w.st.Apply(ev, w.now().UnixMilli())
	}
	// A backend may attach a stable native identity to a finished reply.
	// Keep it in the existing ring source member, in the same write as the
	// text, so lost-holder-ring recovery can distinguish persisted output
	// from a genuinely missed reply without a second checkpoint file.
	if ev.Kind == wslive.KindReply && ev.Final && ev.Source != "" {
		for i := range res.Ring {
			if res.Ring[i].Kind == wslive.RingReply {
				res.Ring[i].Source = ev.Source
			}
		}
	}
	changed := w.write(res, err, ev.Kind, coalesceTurnEnd)
	if w.owner != nil {
		w.retrySummaries()
	}
	return changed
}

// applyQuiet applies a replayed event to the state alone: its ring entries
// were written under an earlier daemon, and the snapshot is written once
// the replay ends.
func (w *liveWriter) applyQuiet(ev wslive.Event) {
	res, err := w.st.Apply(ev, w.now().UnixMilli())
	if err == nil {
		w.writeObserved(res.Observed)
		w.holdOutputRate(res.OutputRate)
	}
}

// card applies a tracker card reference the lead showed, as apply does.
func (w *liveWriter) card(ref wslive.CardRef) bool {
	res, err := w.st.Card(ref, w.now().UnixMilli())
	return w.write(res, err, wslive.RingCard, false)
}

// ui applies a dashboard UI command the lead sent, as apply does.
func (w *liveWriter) ui(cmd wslive.UICommand) bool {
	res, err := w.st.UI(cmd, w.now().UnixMilli())
	return w.write(res, err, wslive.RingUI, false)
}

// notice writes a typed failure line, as apply does.
func (w *liveWriter) notice(n wslive.Notice) bool {
	res, err := w.st.Notice(n, w.now().UnixMilli())
	return w.write(res, err, wslive.RingNotice, false)
}

// setHost replaces the snapshot's host state and, unless quiet, writes the
// snapshot when it changed, which it reports.
func (w *liveWriter) setHost(h *wslive.Host, quiet bool) bool {
	if !w.st.SetHost(h) {
		return false
	}
	if !quiet {
		w.writeSnapshot()
	}
	return true
}

// write writes what an applied change of kind brought; a refused one (err)
// is logged and dropped.
func (w *liveWriter) write(res wslive.Result, err error, kind string, coalesceTurnEnd bool) bool {
	if err != nil {
		fmt.Fprintf(w.log, "agent %s: drop %s event: %v\n", w.st.ReplyID(), kind, err)
		return false
	}
	w.writeObserved(res.Observed)
	w.holdOutputRate(res.OutputRate)
	ringAppended := false
	if len(res.Ring) > 0 {
		if err := w.withOwner(func() error {
			// AppendRing can fail after writing part of a batch. Fence that
			// line too so replay cannot duplicate its successful entries.
			w.ringDirty = true
			return w.appendRing(wslive.RingDir(w.dir), res.Ring)
		}); err != nil {
			fmt.Fprintf(w.log, "agent %s: ring write: %v\n", w.st.ReplyID(), err)
		} else {
			ringAppended = true
		}
	}
	if !res.Changed && len(res.Ring) == 0 {
		return false
	}
	if coalesceTurnEnd {
		w.publishSnapshot(w.st.Snapshot(w.now().UnixMilli()))
		return true
	}
	if kind == wslive.KindTurnEnd {
		w.forceSnapshot()
	} else {
		w.writeSnapshot()
	}
	// A file-only reader must see the new snapshot before its ring wake;
	// there is no later Publish wake on that path to correct an early read.
	if w.wake != nil && (ringAppended || w.currentSlot() == nil) {
		w.wake()
	}
	return true
}

// endAgents marks every running subagent failed: the process that ran them
// is gone, so no end will come for them.
func (w *liveWriter) endAgents() {
	for _, a := range w.st.Snapshot(w.now().UnixMilli()).Agents {
		if a.Status == wslive.StatusRunning && a.AgentKind != wslive.AgentKindPi {
			w.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: a.ID, Status: wslive.StatusFailed})
		}
	}
}

// setAdapter changes the snapshot's adapter name ("" for a closed
// conversation, so the page's composer refuses input) and writes it.
func (w *liveWriter) setAdapter(name string) {
	w.st.SetAdapter(name)
	w.writeSnapshot()
}

func (w *liveWriter) writeObserved(records []wslive.ObservedRecord) {
	if err := wslive.AppendObserved(w.dir, records); err != nil {
		fmt.Fprintf(w.log, "agent %s: model evidence write: %v\n", w.st.ReplyID(), err)
	}
}

func (w *liveWriter) holdOutputRate(rate *wslive.OutputRate) {
	if rate == nil {
		return
	}
	copy := *rate
	w.pendingOutputRate = &copy
}

func (w *liveWriter) writeSnapshot() { w.flushSnapshot(false, false) }

// Only file-only writers need a disk heartbeat. Hosted liveness stays in RAM.
func (w *liveWriter) heartbeatSnapshot() { w.flushSnapshot(true, false) }

// Turn end and shutdown are recovery boundaries, even inside the disk interval.
func (w *liveWriter) forceSnapshot() { w.flushSnapshot(false, true) }

func (w *liveWriter) flushSnapshot(heartbeat, force bool) {
	if w.beforeSnapshot != nil {
		w.beforeSnapshot()
	}
	now := w.now()
	snap := w.st.Snapshot(now.UnixMilli())
	w.publishSnapshot(snap)
	if w.diskHold {
		// Pending output-rate evidence waits too: it goes with the first
		// write after the hold.
		return
	}
	disk := snap.ForDisk()
	disk.UpdatedAt = 0
	content, err := json.Marshal(disk)
	if !force && err == nil {
		if w.currentSlot() != nil {
			if w.pendingOutputRate != nil && w.lastDiskAt.IsZero() && !heartbeat {
				err = w.withOwner(func() error {
					if err := w.outputRates.Append(w.pendingOutputRate); err != nil {
						return err
					}
					w.pendingOutputRate = nil
					return nil
				})
				if err != nil {
					fmt.Fprintf(w.log, "agent %s: output-rate write: %v\n", w.st.ReplyID(), err)
				}
				return
			}
			if (!w.lastDiskAt.IsZero() && now.Sub(w.lastDiskAt) < wslive.HeartbeatInterval) || (bytes.Equal(content, w.lastSnapshot) && w.pendingOutputRate == nil) {
				return
			}
		} else if !heartbeat && bytes.Equal(content, w.lastSnapshot) && w.pendingOutputRate == nil {
			return
		}
	}
	var raw []byte
	if err == nil {
		disk.UpdatedAt = snap.UpdatedAt
		// The covered holder record is not part of content: a write the
		// content skips keeps an older, smaller coverage, which only keeps
		// more rebuilt rows on a rejoin.
		if w.coverage != nil {
			// A known holder always names its coverage, 0 included (no
			// record applied yet): only an absent one is a writer that
			// predates coverage.
			if id, seq := w.coverage(); id != "" {
				disk.HolderID, disk.HolderSeq = id, &seq
			}
		}
		raw, err = json.Marshal(disk)
	}
	if err == nil {
		err = w.withOwner(func() error {
			if err := w.writeSnapshotFile(wslive.SnapshotPath(w.dir), raw, 0o644); err != nil {
				return err
			}
			w.lastSnapshot, w.lastDiskAt = content, now
			if w.pendingOutputRate != nil {
				if err := w.outputRates.Append(w.pendingOutputRate); err != nil {
					return err
				}
				w.pendingOutputRate = nil
			}
			return nil
		})
	}
	if err != nil {
		fmt.Fprintf(w.log, "agent %s: snapshot write: %v\n", w.st.ReplyID(), err)
	}
}

func (w *liveWriter) currentSlot() wsdashboard.HostedSlot {
	w.slotMu.Lock()
	slot := w.slot
	w.slotMu.Unlock()
	return slot
}

func (w *liveWriter) setSlot(slot wsdashboard.HostedSlot) {
	w.slotMu.Lock()
	w.slot = slot
	w.slotMu.Unlock()
}

// publishSnapshot retains the full projection only in the daemon's live slot.
// Hold the registration/conversation fence, not the slot mutex: Publish owns it.
func (w *liveWriter) publishSnapshot(snap wslive.Snapshot) {
	slot := w.currentSlot()
	if slot == nil {
		return
	}
	if w.owner != nil {
		release, ok := w.owner()
		if !ok {
			return
		}
		defer release()
	}
	slot.Publish(snap)
}

func (w *liveWriter) closeSlot() {
	defer func() { _ = w.outputRates.Close() }()
	slot := w.currentSlot()
	if slot == nil {
		return
	}
	if w.owner != nil {
		w.flushSummaries()
	}
	w.forceSnapshot()
	if w.owner != nil {
		w.summaryStore().SetWake(nil)
	}
	w.setSlot(nil)
	slot.Close()
}

// registration is the conversation's session-registry entry.
func registration(rec Record, now time.Time) wsdashboard.Registration {
	reg := wsdashboard.Registration{
		ReplyID:      rec.ReplyID,
		Project:      rec.Root,
		Harness:      rec.Backend,
		RegisteredAt: rec.CreatedAt,
		HeartbeatAt:  now.UTC().Format(time.RFC3339),
		Agent:        true,
		Unlisted:     !rec.Listed,
	}
	if rec.Lead {
		// A lead session is an ordinary dashboard session: it has a session
		// record (its board, mail, and contacts are read as any lead's) and
		// is always listed. Only the writer of this row differs.
		reg.Agent, reg.Unlisted = false, false
		reg.Hosted, reg.HostPID = true, rec.HostPID
	}
	if rec.Parent != nil {
		reg.ParentReplyID = rec.Parent.ReplyID
	}
	if rec.ClosedAt != "" {
		reg.EndedAt = rec.ClosedAt
	}
	return reg
}

func (rec Record) heldWorker() bool {
	return wsdashboard.HeldAgentWorker(rec.Lead, rec.HolderID, rec.HandedOffAt)
}

func (rec Record) heldAgyWorker() bool {
	return rec.Backend == BackendAgy && rec.Parent != nil && rec.heldWorker()
}

func (rec Record) agyInternalPrompt() bool {
	return rec.Backend == BackendAgy && rec.Parent != nil && !rec.TurnOpen && rec.TurnStartedAt != 0
}

func (rec Record) handedOff() bool {
	return rec.HandedOffAt != "" && !rec.heldWorker()
}
