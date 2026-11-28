package wsagent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// compactCommandTimeout bounds how long a dispatched native compact command
// may wait without a harness response before stopping the stalled process.
const compactCommandTimeout = 10 * time.Minute

// RestoreCause identifies why the same transient hosted-state bundle is being
// delivered. It is runtime-only and is never written to a conversation record.
type RestoreCause string

const (
	RestoreCompaction RestoreCause = "compaction"
	RestoreRestart    RestoreCause = "restart"

	compactionRestoreMarker = "\x00ws-restore:compaction\x00"
	restartRestoreMarker    = "\x00ws-restore:restart\x00"
	leadStartStateMarker    = "\x00ws-lead-start-state\x00"
)

func (r BackendRegistration) CompactionProtocol() CompactionProtocol {
	if r.NativeCompaction != nil {
		return *r.NativeCompaction
	}
	if r.Catalog != nil {
		return r.Catalog.Compaction
	}
	return CompactionProtocol{}
}

const (
	compactStageQueued    = "queued"
	compactStageRunning   = "running"
	compactStageCompleted = "completed"
)

// PendingCompaction holds a carry-forward compaction handoff (for a rotated
// lead or an in-flight/completed worker resume compaction) until the session
// completes continuation. Additive top-level member on Record; preserved as
// unknown by older daemons (T0).
// RotatedAfterSends records Record.Sends when persisted so a downgrade-reopen
// (where an older daemon ran newer turns without knowing about
// PendingCompaction) drops the stale handoff instead of replaying it.
type PendingCompaction struct {
	Note              string  `json:"note"`
	RestoreBundle     bool    `json:"restore_bundle,omitempty"`
	Resume            bool    `json:"resume,omitempty"`
	ResumeInternal    bool    `json:"resume_internal,omitempty"`
	Origin            string  `json:"origin,omitempty"`
	RotatedAfterSends *uint64 `json:"rotated_after_sends,omitempty"`
	Stage             string  `json:"stage,omitempty"`
	AfterSeq          uint64  `json:"after_seq,omitempty"`
}

func clonePendingCompaction(pc *PendingCompaction) *PendingCompaction {
	if pc == nil {
		return nil
	}
	cp := *pc
	if pc.RotatedAfterSends != nil {
		sends := *pc.RotatedAfterSends
		cp.RotatedAfterSends = &sends
	}
	return &cp
}

func pendingCompactionFromState(note string, resume, resumeInternal bool, origin string) *PendingCompaction {
	trimmed, hasRestore := strings.CutPrefix(note, compactionRestoreMarker)
	return &PendingCompaction{
		Note:           strings.TrimSpace(trimmed),
		RestoreBundle:  hasRestore,
		Resume:         resume,
		ResumeInternal: resume && resumeInternal,
		Origin:         origin,
	}
}

func (c *conv) restorePendingCompaction(pc *PendingCompaction) {
	if pc == nil {
		return
	}
	if !c.rec.Lead && pc.Stage == compactStageQueued {
		triggerPath := "manual"
		if pc.Origin == wslive.TriggerAuto {
			triggerPath = "auto_hard_cap"
		}
		c.compactPending = &compactRequest{
			resume:      pc.Resume,
			internal:    pc.ResumeInternal,
			auto:        pc.Origin == wslive.TriggerAuto,
			triggerPath: triggerPath,
		}
		if pc.Origin != "" {
			c.compactOrigin = pc.Origin
		}
		return
	}
	if !c.rec.Lead && pc.Stage == compactStageRunning {
		c.compactResume = pc.Resume
		c.compactResumeInternal = pc.Resume && pc.ResumeInternal
		if pc.Origin != "" {
			c.compactOrigin = pc.Origin
		}
		c.compacting = true
		c.compactCommand = true
		return
	}
	if pc.RestoreBundle && c.r != nil && c.r.opts.RestoreBundle != nil {
		note := strings.TrimSpace(strings.TrimPrefix(pc.Note, compactionRestoreMarker))
		if note != "" {
			c.compactNote = compactionRestoreMarker + note
		} else {
			c.compactNote = compactionRestoreMarker
		}
	} else {
		c.compactNote = pc.Note
	}
	c.compactResume = pc.Resume
	c.compactResumeInternal = pc.Resume && pc.ResumeInternal
	if pc.Origin != "" {
		c.compactOrigin = pc.Origin
	}
}

func (c *conv) clearPendingCompactionResume() {
	if c.rec.PendingCompaction == nil {
		return
	}
	if !c.rec.Lead && c.rec.PendingCompaction.Stage != compactStageCompleted {
		c.update(func(rec *Record) { rec.PendingCompaction = nil })
		return
	}
	if !c.rec.PendingCompaction.Resume && !c.rec.PendingCompaction.ResumeInternal {
		return
	}
	c.update(func(rec *Record) {
		if rec.PendingCompaction == nil {
			return
		}
		pc := clonePendingCompaction(rec.PendingCompaction)
		pc.Resume, pc.ResumeInternal = false, false
		rec.PendingCompaction = pc
	})
}

func (c *conv) persistWorkerPendingCompaction(stage string, afterSeq uint64) {
	hasRestore := c.r != nil && c.r.opts.RestoreBundle != nil
	note := strings.TrimSpace(strings.TrimPrefix(c.compactNote, compactionRestoreMarker))
	if !hasRestore && stage == compactStageCompleted && note == "" {
		note = c.compactionFallbackRestoreNote()
	}
	c.update(func(rec *Record) {
		sends := rec.Sends
		rec.PendingCompaction = &PendingCompaction{
			Note:              note,
			RestoreBundle:     hasRestore,
			Resume:            true,
			ResumeInternal:    c.compactResumeInternal,
			Origin:            c.compactOrigin,
			RotatedAfterSends: &sends,
			Stage:             stage,
			AfterSeq:          afterSeq,
		}
	})
}

type compactRequest struct {
	resume      bool
	internal    bool
	auto        bool
	crossing    bool
	stage1      bool
	triggerPath string
	err         error
	lead        *wscompact.LeadSummary
	target      *harnessTarget
}

// recordWorkerInstruction persists a worker's bounded original prompt and
// latest distinct parent/user instruction on Record alongside existing writes.
func recordWorkerInstruction(rec *Record, text string) {
	if rec == nil || rec.Lead {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	bounded := wscompact.BoundedText(trimmed, wscompact.WorkerTaskBytes)
	if rec.InitialPrompt == "" {
		rec.InitialPrompt = bounded
		return
	}
	if bounded != rec.InitialPrompt {
		rec.LatestInstruction = bounded
	}
}

// workerTaskFromRing scans full ring history (oldest to newest, including
// records before prior RingCompact cuts) to recover a legacy worker's original
// prompt and latest distinct user instruction when Record lacks them.
func workerTaskFromRing(records []wslive.RingRecord) (initial, latest string) {
	for _, rec := range records {
		if rec.Kind != wslive.RingUser {
			continue
		}
		text := strings.TrimSpace(rec.Text)
		if text == "" {
			continue
		}
		bounded := wscompact.BoundedText(text, wscompact.WorkerTaskBytes)
		if initial == "" {
			initial = bounded
		} else if bounded != initial {
			latest = bounded
		}
	}
	return initial, latest
}

type compactionNudge struct {
	text       string
	level      int // 1 for soft, 2 and 3 for the two milestones
	standalone bool
}

// CompactResult is the additive compact tool's result. Queued does not claim
// completion: only a native successful end can restore context.
type CompactResult struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Compact accepts self's key or an owned descendant's spawn address/key.
// The presented key is the authority on child connections, as for mailbox
// control; no key or lineage changes during compaction.
func (r *Runner) Compact(callerKey, target string, resume *bool) CompactResult {
	return r.CompactWithSummary(callerKey, target, resume, nil)
}

// LeadSummaryRefusalPrefix starts the error CompactWithSummary returns when the
// caller's role or target cannot carry a lead_summary; the mcp draft cache
// keys off it to drop a draft that can never be used.
const LeadSummaryRefusalPrefix = "lead_summary is only available"

// CompactWithSummary queues a checkpoint with prose supplied by its own lead.
func (r *Runner) CompactWithSummary(callerKey, target string, resume *bool, lead *wscompact.LeadSummary) CompactResult {
	target = strings.TrimSpace(target)
	var own, dest *conv
	if target != "" {
		// A parked target is rehydrated first: only open sessions compact.
		for _, rec := range r.parkedRecords() {
			if rec.WSKey == target || AgentAddress(rec.ReplyID) == target {
				r.unpark(rec.ReplyID)
			}
		}
	}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	for _, c := range convs {
		rec := c.record()
		if rec.WSKey == callerKey {
			own = c
		}
		if rec.WSKey == target || AgentAddress(rec.ReplyID) == target {
			dest = c
		}
	}
	if own == nil {
		return CompactResult{"unsupported", "the caller is not a hosted session"}
	}
	if target == "" || target == callerKey {
		dest = own
	}
	if dest == nil {
		return CompactResult{"error", "target is not an open hosted session"}
	}
	if dest != own && !r.compactDescendant(own.record(), dest.record()) {
		return CompactResult{"error", "target is not this session's descendant"}
	}
	again := dest == own
	if resume != nil {
		again = *resume
	}
	out := CompactResult{"error", "session closed"}
	dest.do(func() {
		wsWorker := dest.wsManagedWorkerCompaction()
		if why := dest.compactUnsupported(); why != "" && !wsWorker {
			out = CompactResult{"unsupported", why}
			return
		}
		if dest.proc == nil {
			out = CompactResult{"error", "compaction requires a running harness"}
			return
		}
		if lead != nil {
			if dest != own || !(dest.rec.Lead || wsWorker) {
				out = CompactResult{"error", LeadSummaryRefusalPrefix + " for a hosted lead or ws-mode worker compacting itself"}
				return
			}
		}
		if wsWorker && lead == nil {
			lead = emptyWorkerSummary()
		}
		if dest.compactPending != nil || dest.compacting || dest.compactCommand {
			out = CompactResult{"queued", "compaction already pending" + compactQueuedGuidance(dest == own)}
			return
		}
		internal := dest.compactInternalTurn
		crossing := dest.aboveCompactHard()
		triggerPath := "manual"
		if dest.compactStage1Active {
			internal = dest.compactStage1Internal
			crossing = crossing || dest.compactStage1Auto
			if dest.compactStage1Auto {
				triggerPath = "stage1_hard_cap"
			} else {
				triggerPath = "stage1_manual"
			}
		}
		req := &compactRequest{
			resume:      again,
			internal:    internal,
			auto:        dest.compactStage1Active && dest.compactStage1Auto,
			crossing:    crossing,
			triggerPath: triggerPath,
			lead:        lead,
		}
		dest.compactPending = req
		dest.publishCompaction(false)
		if lead != nil {
			out = CompactResult{"queued", "ws-managed compaction waits for the idle boundary" + compactQueuedGuidance(dest == own)}
		} else {
			out = CompactResult{"queued", "native compaction waits for the idle boundary" + compactQueuedGuidance(dest == own)}
		}
		dest.kick()
		if req.err != nil {
			out = CompactResult{"error", req.err.Error()}
		} else if !dest.rec.Lead && !dest.r.durableProcess(dest.rec.Backend) && dest.compactPending == req && req.resume {
			dest.persistWorkerPendingCompaction(compactStageQueued, dest.seq)
		}
	})
	return out
}

// compactQueuedGuidance tells a caller that queued compaction runs only once
// the target's turn ends. A session compacting itself mid-turn must therefore
// stop calling tools; one that keeps working is steered or interrupted once
// usage passes the hard cap.
func compactQueuedGuidance(self bool) string {
	if self {
		return ". Stop calling tools and end your turn now: compaction runs only after this turn ends, and a turn that keeps running past the hard cap is steered or interrupted"
	}
	return ". It runs once the target's current turn ends"
}

func (r *Runner) compactDescendant(own, child Record) bool {
	for child.Parent != nil {
		if child.Parent.ReplyID == own.ReplyID {
			return true
		}
		parent := r.conv(child.Parent.ReplyID)
		if parent == nil {
			return false
		}
		child = parent.record()
	}
	return false
}

func (c *conv) compactUnsupported() string {
	if c.wsManagedWorkerCompaction() {
		// Rotation needs no native command or lifecycle detection.
		if c.caps != nil && !c.caps.Allows(ItemCompaction) {
			return "the session catalog reports compaction unsupported"
		}
		return ""
	}
	p := c.r.opts.Backends[c.rec.Backend].CompactionProtocol()
	if !p.DetectsLifecycle() {
		return "native compaction lifecycle is unsupported by this harness descriptor"
	}
	if (c.rec.Lead && !p.LeadCommand) || (!c.rec.Lead && !p.WorkerCommand) {
		return "native compaction command is unsupported for this role"
	}
	if c.caps != nil && !c.caps.Allows(ItemCompaction) {
		return "the session catalog reports compaction unsupported"
	}
	return ""
}

// compactionConfigGen is bumped in memory whenever config.tune writes or resets
// a compaction key so running conversations re-resolve thresholds lazily at the
// next threshold check without file polling or watchers.
var compactionConfigGen atomic.Uint64

// NotifyCompactionConfigChanged signals running conversations to re-resolve
// compaction thresholds on their next threshold check.
func NotifyCompactionConfigChanged() {
	compactionConfigGen.Add(1)
}

// loadCompaction runs at process start, loading thresholds and resetting
// process-scoped usage observations. During an in-progress rotation
// (c.compacting), it preserves rotation-carried circuit-breaker state
// (compactHardAtStart, compactOrigin, compactPostRotationNoShrink) and never
// clears compactTurnRotations (which is owned by the logical turn lifecycle).
func (c *conv) loadCompaction() {
	c.loadCompactionThresholds()
	c.compactLoaded = true
	c.compactUsageFresh, c.compactAwaitTurn, c.compactContinueAtIdle = false, false, false
	c.compactPrevTokens, c.compactPrevWindow = 0, 0
	c.compactHardSteerID = ""
	c.compactHardStepPending = false
	c.compactHardBoundaries = 0
	c.compactHardInterrupt, c.compactHardAbort, c.compactHardForce = false, false, false
	c.compactAdvised, c.compactM1Issued, c.compactM2Issued = false, false, false
	c.compactNudge = compactionNudge{}
	c.compactStage1Active, c.compactStage1Resume, c.compactStage1Internal, c.compactStage1Auto, c.compactStage1UsageLimited = false, false, false, false, false
	if !c.compacting {
		c.compactHardIssued, c.compactHardSteered, c.compactHardHandled, c.compactHardAtStart = false, false, false, false
		c.resetHardOverflow()
		c.compactOrigin = ""
	}
}

func (c *conv) wsManagedLeadCompaction() bool {
	return c.rec.Lead && !c.r.durableProcess(c.rec.Backend) && c.leadCompactionMode(c.record()) == wsconfig.CompactionModeWS
}

// wsManagedWorkerCompaction reports a non-durable hosted worker whose effective
// compaction.worker_mode is ws: it compacts by ws-managed session rotation.
func (c *conv) wsManagedWorkerCompaction() bool {
	return !c.rec.Lead && !c.r.durableProcess(c.rec.Backend) && c.workerCompactionMode(c.record()) == wsconfig.CompactionModeWS
}

// WorkerCompactionMode reports the effective mode of an open hosted worker.
func (r *Runner) WorkerCompactionMode(conversation string) string {
	c := r.resident(conversation)
	if c == nil {
		return ""
	}
	c.mu.Lock()
	rec, openWorker := c.rec, !c.rec.Lead && !c.closed
	c.mu.Unlock()
	if !openWorker {
		return ""
	}
	return c.workerCompactionMode(rec)
}

func (c *conv) workerCompactionMode(rec Record) string {
	c.compactModeMu.Lock()
	defer c.compactModeMu.Unlock()
	gen := compactionConfigGen.Load()
	if c.compactWorkerMode != "" && c.compactWorkerModeGen == gen {
		return c.compactWorkerMode
	}
	var raw string
	if !WorkerCompactionWSOnly(rec.Backend, c.r.opts.Backends) {
		resolver := c.compactionResolver(rec)
		if value, _, err := resolver.Compaction(rec.WSKey, wsconfig.ItemCompactionWorkerMode, rec.Backend, ""); err == nil {
			raw = value.Value
		}
	}
	c.compactWorkerMode = EffectiveWorkerCompactionMode(rec.Backend, raw, c.r.opts.Backends)
	c.compactWorkerModeGen = gen
	return c.compactWorkerMode
}

// emptyWorkerSummary asks a ws-managed rotation to rebuild the checkpoint from
// the ring dialog alone, so a worker that supplies no prose never stalls.
func emptyWorkerSummary() *wscompact.LeadSummary {
	return &wscompact.LeadSummary{Prose: make([]string, len(wscompact.ProseFields()))}
}

// LeadCompactionMode reports the effective mode of an authenticated open
// hosted lead. Discovery needs only the in-memory binding and mode cache.
func (r *Runner) LeadCompactionMode(conversation string) string {
	c := r.resident(conversation)
	if c == nil {
		return ""
	}
	c.mu.Lock()
	rec, openLead := c.rec, c.rec.Lead && !c.closed
	c.mu.Unlock()
	if !openLead {
		return ""
	}
	return c.leadCompactionMode(rec)
}

func (c *conv) leadCompactionMode(rec Record) string {
	c.compactModeMu.Lock()
	defer c.compactModeMu.Unlock()
	gen := compactionConfigGen.Load()
	if c.compactLeadMode != "" && c.compactModeGen == gen {
		return c.compactLeadMode
	}
	var raw string
	if !LeadCompactionWSOnly(rec.Backend, c.r.opts.Backends) {
		resolver := c.compactionResolver(rec)
		if value, _, err := resolver.Compaction(rec.WSKey, wsconfig.ItemCompactionLeadMode, rec.Backend, ""); err == nil {
			raw = value.Value
		}
	}
	c.compactLeadMode = EffectiveLeadCompactionMode(rec.Backend, raw, c.r.opts.Backends)
	c.compactModeGen = gen
	return c.compactLeadMode
}

func (c *conv) compactionResolver(rec Record) wsconfig.Resolver {
	cfg := c.r.opts.Config
	cfg.RepoRoot = rec.Root
	if roots, ok := c.r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
		if root, found := roots.Root(rec.WSKey); found {
			cfg.RepoRoot = root
		}
	}
	reader, _ := c.r.opts.Keys.(wsconfig.SessionReader)
	return wsconfig.NewResolver(cfg, nil, reader, nil)
}

func (c *conv) loadCompactionThresholds() {
	// Capture generation before reading config so a concurrent tune during the
	// read leaves compactGen behind and triggers one more reload next check.
	gen := compactionConfigGen.Load()
	r := c.compactionResolver(c.record())
	p := c.r.opts.Backends[c.rec.Backend].CompactionProtocol()
	role := "worker"
	if c.rec.Lead {
		role = "lead"
	}
	c.compactAdvisoryEnabled = true
	if c.rec.Lead && p.AdvisoryEnabled != "" {
		v, err := r.Get(c.rec.WSKey, p.AdvisoryEnabled)
		c.compactAdvisoryEnabled = err == nil && v.Value != "false"
	}
	for _, slot := range []struct {
		name string
		to   *wsconfig.Threshold
	}{{"advisory", &c.compactAdvisory}, {"hard", &c.compactHard}} {
		key := "compaction." + role + "_" + slot.name
		v, _, err := r.Compaction(c.rec.WSKey, key, c.rec.Backend, p.LegacyKey(key))
		if err == nil {
			*slot.to, err = wsconfig.ParseThreshold(v.Value)
		}
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", c.rec.ReplyID, key, err)
			*slot.to = wsconfig.Threshold{}
		}
	}
	c.compactGen = gen
}

func (c *conv) compactMilestones(window float64) (m1, m2 float64, ok bool) {
	if !c.compactAdvisoryEnabled || window <= 0 {
		return 0, 0, false
	}
	soft := c.compactAdvisory.Point(window)
	hard := c.compactHard.Point(window)
	if math.IsInf(soft, 0) || math.IsInf(hard, 0) || !(hard > soft) {
		return 0, 0, false
	}
	step := (hard - soft) / 3
	return soft + step, soft + 2*step, true
}

func (c *conv) usageCompaction(window int64) *wslive.UsageCompaction {
	if !c.compactLoaded || window <= 0 {
		return nil
	}
	w := float64(window)
	finite := func(v float64) int64 {
		if math.IsInf(v, 0) || math.IsNaN(v) || v <= 0 {
			return 0
		}
		return int64(math.Round(v))
	}
	soft := finite(c.compactAdvisory.Point(w))
	hard := finite(c.compactHard.Point(w))
	var milestones []int64
	if m1, m2, ok := c.compactMilestones(w); ok {
		milestones = []int64{int64(math.Round(m1)), int64(math.Round(m2))}
	}
	if soft == 0 && hard == 0 && len(milestones) == 0 {
		return nil
	}
	return &wslive.UsageCompaction{
		Soft:            soft,
		Hard:            hard,
		Milestones:      milestones,
		AdvisoryEnabled: c.compactAdvisoryEnabled,
	}
}

func (c *conv) disarmCompactionLatches() {
	if c.compactTokens < c.compactAdvisory.Point(c.compactWindow) {
		c.compactAdvised = false
	}
	if m1, m2, ok := c.compactMilestones(c.compactWindow); ok {
		if c.compactTokens < m1 {
			c.compactM1Issued = false
		}
		if c.compactTokens < m2 {
			c.compactM2Issued = false
		}
	} else {
		c.compactM1Issued, c.compactM2Issued = false, false
	}
	if c.compactTokens < c.compactHard.Point(c.compactWindow) {
		c.resetHardOverflow()
		c.clearCompactionSteerReplies()
	}
}

// refreshCompaction lazily reloads thresholds when config.tune has modified a
// compaction key since this conversation last loaded them. Steady-state checks
// perform only an atomic load with zero file I/O.
func (c *conv) refreshCompaction() {
	if !c.compactLoaded || c.r == nil {
		return
	}
	if compactionConfigGen.Load() == c.compactGen {
		return
	}
	c.loadCompactionThresholds()
	if c.compactUsageFresh && !c.compacting {
		c.disarmCompactionLatches()
	}
}

func (c *conv) observeCompaction(ev wslive.Event) {
	if ev.Kind == wslive.KindTurnStart {
		c.compactUsageFresh, c.compactAwaitTurn = false, false
		c.compactContinueAtIdle = false
		c.compactHardInterrupt, c.compactHardAbort, c.compactHardBoundaries = false, false, 0
		c.compactHardStepPending = false
		c.clearCompactionSteerReplies()
	}
	if ev.Kind != wslive.KindStatus || ev.Context == nil || c.compactAwaitTurn {
		return
	}
	ctx := ev.Context
	c.compactUsageFresh = false
	// A sample without a window stays stale: no threshold resolves without
	// one (wsconfig.Threshold.Point), and the next sample carries it.
	if ctx.Tokens == nil || *ctx.Tokens < 0 || *ctx.Tokens > wslive.MaxContextTokens || ctx.Window <= 0 {
		return
	}
	c.compactTokens, c.compactWindow = float64(*ctx.Tokens), float64(ctx.Window)
	c.compactUsageFresh = true
	c.refreshCompaction()
	if c.compactHardSteered {
		// A fresh context-usage sample while steered indicates a new model
		// step ran after the Stage 1 steer, allowing the next tool boundary to
		// advance compactHardBoundaries once rather than counting queued
		// pre-steer sequential tool completions.
		c.compactHardStepPending = true
	}
	if c.compactPostRotationCheck {
		c.compactPostRotationCheck = false
		if c.aboveCompactHard() {
			c.compactNoShrink = c.compactPostRotationNoShrink
			c.compactHardHandled = c.compactNoShrink >= compactNoShrinkCap
		} else {
			c.compactPostRotationNoShrink = 0
		}
	}
	c.disarmCompactionLatches()
	if c.compactPending != nil && c.aboveCompactHard() {
		c.compactPending.crossing = true
	}
}

// checkUsageDropCompaction detects native compaction completion on harnesses
// whose descriptor declares UsageDropRatio rather than explicit wire events.
// A window change resets the baseline (model/tokenizer switch, not compaction).
func (c *conv) checkUsageDropCompaction(ev wslive.Event) {
	if c.r == nil || ev.Kind != wslive.KindStatus || ev.Context == nil || ev.Context.Tokens == nil {
		return
	}
	p := c.r.opts.Backends[c.rec.Backend].CompactionProtocol()
	if p.UsageDropRatio <= 0 || p.UsageDropRatio >= 1 {
		return
	}
	used, window := *ev.Context.Tokens, ev.Context.Window
	if used <= 0 || used > wslive.MaxContextTokens || window <= 0 {
		return
	}
	if c.compactPrevWindow > 0 && window != c.compactPrevWindow {
		c.compactPrevTokens, c.compactPrevWindow = 0, window
		return
	}
	prev := c.compactPrevTokens
	c.compactPrevTokens, c.compactPrevWindow = used, window
	if prev <= 0 {
		return
	}
	dropped := prev - used
	if dropped < p.UsageDropMinDelta || float64(used) >= float64(prev)*p.UsageDropRatio {
		return
	}
	trigger := wslive.TriggerAuto
	if c.compacting {
		trigger = wslive.TriggerManual
	} else {
		c.compacting = true
	}
	c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: trigger})
	// This sample is already post-compaction: keep it as the new baseline and
	// fresh usage so the next turn does not re-trigger on the same drop.
	c.compactPrevTokens = used
	c.compactAwaitTurn = false
	c.compactTokens, c.compactWindow, c.compactUsageFresh = float64(used), float64(window), true
	c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: trigger})
	if !c.compactCommand {
		c.stopCompactWatchdog()
	}
}

// recordInferredCompaction records a daemon-inferred compaction boundary in the
// process's audit capture with meta.estimated = true (Decision 6). It never
// blocks and is a no-op when audit capture is inactive.
func (c *conv) recordInferredCompaction(trigger, inferredBy string, preTokens, postTokens int64) {
	if c.proc == nil {
		return
	}
	rec, ok := c.proc.(auditSyntheticRecorder)
	if !ok {
		return
	}
	at := time.Now()
	if c.r != nil && c.r.opts.Now != nil {
		at = c.r.opts.Now()
	}
	ev, err := wsaudit.NewEnvelope(c.rec.ReplyID, c.rec.Backend, at, wsaudit.KindCompaction)
	if err != nil {
		return
	}
	ev.Subkind = trigger
	meta := map[string]any{
		wsaudit.MetaEstimated: true,
		"inferred_by":         inferredBy,
	}
	if trigger != "" {
		meta["trigger"] = trigger
	}
	if preTokens > 0 {
		meta["pre_tokens"] = preTokens
	}
	if postTokens > 0 {
		meta["post_tokens"] = postTokens
	}
	ev.Meta = meta
	rec.recordAuditEvent("", ev)
}

// compactIdle precedes inbox reads. All crossing/lifecycle state is in memory.
func (c *conv) compactIdle() bool {
	defer c.publishCompaction(false)
	if c.compacting || c.compactCommand {
		return true
	}
	if (strings.HasPrefix(c.compactNote, restartRestoreMarker) || strings.HasPrefix(c.compactNote, compactionRestoreMarker)) && !c.r.startupReady() {
		return true
	}
	if c.accountSlotBoundaryPending {
		c.accountSlotBoundaryPending = false
		if c.hasPendingAccountSlot() && !c.r.durableProcess(c.rec.Backend) {
			if !c.rec.Lead && !c.compactResume {
				if err := c.retireAndCommitPendingSlot(); err != nil {
					c.setStatus(func() { c.lastErr = "account slot switch: " + err.Error() })
				}
				return false
			}
			if err := c.coldRestartPendingAccountSlot("compact_boundary"); err != nil {
				c.setStatus(func() { c.lastErr = "account slot switch: " + err.Error() })
				c.compactResume, c.compactResumeInternal = false, false
				return false
			}
			goto postCompact
		}
	}
	if c.compactNote == "" && c.compactUsageFresh {
		c.refreshCompaction()
		if c.aboveCompactHard() {
			c.stageHardCompaction(c.compactContinueAtIdle, c.compactIdleInternal)
		} else if c.compactPending == nil {
			c.stageCompactionNudge(c.compactHard.Point(c.compactWindow))
		}
	}
	if req := c.compactPending; req != nil {
		if c.r.durableProcess(c.rec.Backend) {
			release, err := c.r.admitDurableTransition()
			if err != nil {
				return true
			}
			defer release()
		}
		pendingAccountSwitch := !c.r.durableProcess(c.rec.Backend) && c.hasPendingAccountSlot()
		if c.proc == nil && !pendingAccountSwitch {
			c.compactPending = nil
			if !c.rec.Lead && c.rec.PendingCompaction != nil {
				c.update(func(rec *Record) { rec.PendingCompaction = nil })
			}
			c.setStatus(func() { c.lastErr = "compaction requires a running harness" })
			c.releaseHeld()
			return false
		}
		c.compactPending = nil
		if req.stage1 && c.proc != nil {
			if err := c.sendStandalonePrompt(false, func() error { return c.proc.Send(stage1CompactionPrompt()) }); err != nil {
				if checkout, ok := releasedInput(err); ok {
					req.err = err
					c.dropReleasedInput("compaction stage 1", checkout)
					c.releaseHeld()
					return false
				}
				// Stage 1 steer failed to dispatch; fall through to Stage 2 native /compact.
				req.stage1 = false
			} else {
				c.compactStage1Active = true
				c.compactStage1Resume = req.resume
				c.compactStage1Internal = req.internal
				c.compactStage1Auto = req.auto
				c.compactStage1UsageLimited = false
				c.compactInternalTurn = true
				c.beginTurn(false)
				return true
			}
		}
		if pendingAccountSwitch {
			if !c.rec.Lead && !req.resume {
				// Worker finished natural turn-end compaction with resume=false:
				// retire the process and commit the pending account slot before
				// going idle so the next wake spawns with the new slot's env (D6).
				if err := c.retireAndCommitPendingSlot(); err != nil {
					req.err = err
					c.setStatus(func() { c.lastErr = "account slot switch: " + err.Error() })
				}
				c.releaseHeld()
				return false
			}
			// Block c.proc reuse even in native compaction mode by routing
			// through rotateExternalSession (retire -> commit slot -> respawn).
			if req.lead == nil {
				req.lead = emptyWorkerSummary()
			}
		}
		c.compactResume, c.compactCommand = req.resume, true
		c.compactResumeInternal = req.resume && req.internal
		trigger := wslive.TriggerManual
		if req.auto {
			trigger = wslive.TriggerAuto
		}
		c.compactOrigin = trigger
		_, acceptsSummary := c.proc.(leadSummaryCompactor)
		if req.lead != nil && !acceptsSummary {
			rotated, err := c.rotateExternalSession(req, trigger)
			if checkout, ok := releasedInput(err); ok {
				req.err = err
				c.compactOrigin = ""
				c.compactCommand, c.compactResume, c.compactResumeInternal = false, false, false
				c.dropReleasedInput("compaction", checkout)
				c.releaseHeld()
				return false
			}
			if err != nil {
				req.err = err
				c.compactCommand = false
				c.stopCompactWatchdog()
				if c.compacting {
					c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
					c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed, Text: err.Error()})
				} else {
					c.compactResume, c.compactResumeInternal, c.compactOrigin = false, false, ""
					c.publishCompaction(false)
				}
				c.setStatus(func() { c.lastErr = "compaction: " + err.Error() })
				c.releaseHeld()
				return false
			}
			if rotated {
				// CompactEnd already ran synchronously inside rotateExternalSession;
				// fall through so compactResume opens the continuation turn now.
				goto postCompact
			}
		}
		cmd := wsinbox.Command{Name: "compact"}
		// The compaction starts with its command, behind the last release
		// check: a refused one leaves no started compaction to unwind.
		err := c.sendStandalonePrompt(false, func() error {
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart})
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart, Trigger: trigger})
			if req.lead == nil {
				return c.runCommand(cmd)
			}
			p, ok := c.proc.(leadSummaryCompactor)
			if !ok {
				return errors.New("lead summary requires a running builtin lead")
			}
			err := p.compactWithSummary(req.lead)
			if err == nil {
				c.armCompactWatchdog()
			}
			return err
		})
		if checkout, ok := releasedInput(err); ok {
			req.err = err
			c.compactOrigin = ""
			c.compactCommand, c.compactResume, c.compactResumeInternal = false, false, false
			if !c.rec.Lead && c.rec.PendingCompaction != nil {
				c.update(func(rec *Record) { rec.PendingCompaction = nil })
			}
			c.dropReleasedInput("compaction", checkout)
			c.releaseHeld()
			return false
		}
		if err != nil {
			req.err = err
			c.compactCommand = false
			c.stopCompactWatchdog()
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
			if !c.rec.Lead && c.rec.PendingCompaction != nil {
				c.update(func(rec *Record) { rec.PendingCompaction = nil })
			}
			c.setStatus(func() { c.lastErr = "compaction: " + err.Error() })
			c.releaseHeld()
			return false
		}
		if !c.rec.Lead && !c.r.durableProcess(c.rec.Backend) && c.compactResume {
			if c.compacting || c.compactCommand {
				c.persistWorkerPendingCompaction(compactStageRunning, c.seq)
			} else {
				c.persistWorkerPendingCompaction(compactStageCompleted, 0)
			}
		}
		return true
	}
postCompact:
	if c.compactResume && c.compactNote != "" {
		if c.proc == nil && c.rec.PendingCompaction != nil && (c.rec.Lead || c.rec.SessionID != "") {
			if err := c.startProcess(); err != nil {
				if errors.Is(err, errDurableStopping) {
					return true
				}
				c.compactResume, c.compactResumeInternal = false, false
				c.clearPendingCompactionResume()
				var refused *admissionRefusal
				if errors.As(err, &refused) {
					c.refuseStart(refused)
					if dead := c.r.deadBindingRefusal(c.record(), refused.checkout); dead != nil {
						c.closeDeadAfterRefusal(dead)
					}
					return true
				}
				c.setStatus(func() { c.lastErr, c.wakeRefused = "resume after compaction: "+err.Error(), false })
				return false
			}
			c.setStatus(func() { c.wakeRefused = false })
		}
	}
	if c.compactResume && c.compactNote != "" && c.proc != nil {
		if c.r.durableProcess(c.rec.Backend) {
			return c.compactDurableContinue()
		}
		note := strings.TrimSpace(c.renderPendingNote(c.compactNote))
		if held := formatHeldMailRestore(c.record().Mail); held != "" {
			note = insertBeforeRestoreClose(note, held)
		}
		text := note + "\n\nContinue the work that was pending."
		c.compactResume = false
		if err := c.sendStandalonePrompt(!c.compactResumeInternal, func() error { return c.proc.Send(text) }); err != nil {
			c.compactResumeInternal = false
			c.clearPendingCompactionResume()
			if checkout, ok := releasedInput(err); ok {
				c.dropReleasedInput("continuation after compaction", checkout)
				c.releaseHeld()
				return false
			}
			c.setStatus(func() { c.lastErr = err.Error() })
			c.releaseHeld()
			return false
		}
		c.compactNote = ""
		c.compactInternalTurn, c.compactResumeInternal = c.compactResumeInternal, false
		if c.rec.PendingCompaction != nil {
			c.update(func(rec *Record) { rec.PendingCompaction = nil })
		}
		c.beginTurn(false)
		return true
	}
	if strings.HasPrefix(c.compactNote, restartRestoreMarker) && c.proc != nil {
		if c.restartRestoreHasInput() {
			// Let the ordinary turn path prepend the bundle instead of opening
			// a standalone restore turn ahead of input that already waits.
			return false
		}
		text := c.renderPendingNote(c.compactNote)
		if err := c.sendStandalonePrompt(true, func() error { return c.proc.Send(text) }); err != nil {
			if checkout, ok := releasedInput(err); ok {
				c.dropReleasedInput("restore turn", checkout)
				c.releaseHeld()
				return false
			}
			c.setStatus(func() { c.lastErr = err.Error() })
			c.releaseHeld()
			return false
		}
		c.compactNote = ""
		if c.rec.PendingCompaction != nil {
			c.update(func(rec *Record) { rec.PendingCompaction = nil })
		}
		c.beginTurn(false)
		return true
	}
	return false
}

func (c *conv) aboveCompactHard() bool {
	return c.compactUsageFresh && c.compactTokens >= c.compactHard.Point(c.compactWindow)
}

// stageHardCompaction runs before settlement so a worker's report can wait
// through a compaction and its continuation. Dispatch still waits for idle.
func (c *conv) stageHardCompaction(resume, internal bool) {
	if c.quitting || c.replaying || c.compacting || c.compactCommand || c.leverC != nil ||
		!c.aboveCompactHard() || c.compactHardHandled || c.compactHardIssued {
		return
	}
	if c.compactPending != nil {
		c.compactPending.crossing = true
		return
	}
	if c.compactTurnRotations >= compactTurnRotationCap {
		c.compactHardHandled = true
		if c.rec.Lead {
			c.setStatus(func() {
				c.lastErr = fmt.Sprintf("auto-compaction paused after %d automatic compactions in one turn; run /compact or split the task", compactTurnRotationCap)
			})
			c.changed(false)
		}
		return
	}
	c.compactHardIssued, c.compactAdvised, c.compactM1Issued, c.compactM2Issued = true, true, true, true
	c.compactNudge = compactionNudge{}
	if why := c.compactUnsupported(); why != "" {
		c.setStatus(func() { c.lastErr = why })
		if c.compactAdvisoryEnabled && c.compactNote == "" {
			c.compactNote = fmt.Sprintf("Context usage is %.0f tokens (%.0f%%). Native compaction is unavailable. %s\n", c.compactTokens, c.compactTokens/c.compactWindow*100, c.compactionStateInstruction())
		}
		return
	}
	// Only leads auto-resume after a natural-end crossing: workers report
	// their finished turn to their parent and compact without reopening it.
	// A Stage 2 forced interrupt aborted the worker mid-work, so it must
	// resume (its report is held by compactHoldsReport) instead of settling.
	// Explicit ws/compact calls already returned above via c.compactPending != nil.
	resume = (resume && c.rec.Lead) || c.compactHardForce
	triggerPath := "auto_hard_cap"
	if c.compactHardForce {
		triggerPath = "stage2_hard_cap"
	}
	// A legacy companion-body session has no ws/compact tool: the daemon
	// rotates it from the ring dialog without a Stage 1 prose round trip.
	// companion-work (Brain) is a Full Lead and uses standard Lead compaction.
	companion := c.rec.CompanionRole == companionRoleBody
	req := &compactRequest{resume: resume, internal: internal, auto: true, crossing: true, stage1: c.wsManagedLeadCompaction() && !c.compactHardForce && !companion, triggerPath: triggerPath}
	if c.wsManagedWorkerCompaction() || (c.compactHardForce || companion) && c.wsManagedLeadCompaction() {
		// No Stage 1 prose round trip for a worker: rotate from the ring dialog.
		req.lead = emptyWorkerSummary()
	}
	c.compactPending = req
	c.publishCompaction(false)
}

// compactNoShrinkCap bounds consecutive hard-crossing compactions that left
// usage at or above hard, so a compaction that frees nothing cannot loop.
const compactNoShrinkCap = 2

// compactTurnRotationCap bounds consecutive automatic compactions within one
// logical turn even when each compaction shrinks context, preventing a task
// that repeatedly re-inflates to hard from looping indefinitely.
const compactTurnRotationCap = 3

// resetHardOverflow clears every hard-cap latch and Stage 2 counter: usage
// fell below hard, or a fresh session replaced the overflowed one. It never
// resets compactTurnRotations, which spans rotations within a logical turn.
func (c *conv) resetHardOverflow() {
	c.compactHardIssued, c.compactHardSteered, c.compactHardHandled, c.compactHardAtStart = false, false, false, false
	c.compactHardBoundaries, c.compactNoShrink = 0, 0
	c.compactHardStepPending = false
	c.compactHardInterrupt, c.compactHardAbort, c.compactHardForce = false, false, false
	c.compactPostRotationCheck, c.compactPostRotationNoShrink = false, 0
}

// escalateHardOverflow is Stage 2 of hard-cap enforcement. At a tool boundary
// (never mid-tool, never during compaction) a turn that ignored the Stage 1
// steer, or that reached 120% of hard, is interrupted so endTurn can compact
// and resume it. A worker whose harness takes no compaction steer escalates
// on the 120% ceiling alone, leaving the harness's own auto-compaction room.
func (c *conv) escalateHardOverflow() {
	if !c.turn || c.proc == nil || c.quitting || c.replaying || c.wrapHold || c.closed || c.hidden ||
		c.compacting || c.compactCommand || (c.compactPending != nil && !c.compactQueuedForIdle()) || c.compactInternalTurn ||
		c.compactHardInterrupt || c.compactHardAbort || c.compactNoShrink >= compactNoShrinkCap ||
		c.caps != nil && !c.caps.Allows(ItemInterrupt) {
		return
	}
	c.refreshCompaction()
	if !c.aboveCompactHard() {
		return
	}
	over := c.compactTokens >= c.compactHard.Point(c.compactWindow)*120/100
	trigger := false
	switch {
	case c.compactHardSteered:
		if c.compactHardStepPending {
			c.compactHardStepPending = false
			c.compactHardBoundaries++
		}
		trigger = over || c.compactHardBoundaries >= 3
	case !c.rec.Lead && c.r.lifecycle(c.rec.Backend).NoWorkerCompactionSteer:
		trigger = over
	}
	if !trigger {
		return
	}
	if c.compactTurnRotations >= compactTurnRotationCap {
		c.compactHardHandled = true
		c.compactHardBoundaries = 0
		c.compactHardStepPending = false
		if c.rec.Lead {
			c.setStatus(func() {
				c.lastErr = fmt.Sprintf("auto-compaction paused after %d automatic compactions in one turn; run /compact or split the task", compactTurnRotationCap)
			})
			c.changed(false)
			return
		}
		c.compactHardAbort = true
		c.disarmModelWait()
		if err := c.proc.Interrupt(); err != nil {
			c.compactHardAbort = false
			fmt.Fprintf(c.r.opts.Log, "agent %s: hard-cap abort interrupt: %v\n", c.rec.ReplyID, err)
		}
		return
	}
	c.compactHardInterrupt, c.compactHardBoundaries, c.compactHardStepPending = true, 0, false
	c.disarmModelWait()
	if err := c.proc.Interrupt(); err != nil {
		c.compactHardInterrupt = false
		fmt.Fprintf(c.r.opts.Log, "agent %s: hard-cap interrupt: %v\n", c.rec.ReplyID, err)
	}
}

func stage1CompactionPrompt() string {
	var fields []string
	for _, f := range wscompact.ProseFields() {
		fields = append(fields, f.Key)
	}
	return "Compaction is requested. Call ws/compact directly with lead_summary containing all seven string fields (" +
		strings.Join(fields, ", ") +
		"; use empty strings for sections with nothing to carry forward), optional reread (up to 12 worktree text file paths), and optional agenda_updates if your agenda needs updating so ws can rotate the session with your checkpoint. End your turn after calling ws/compact."
}

// rotateExternalSession executes ws-managed compaction for an external hosted
// lead or ws-mode worker: builds the checkpoint, replaces the holder, and
// records the explicit audit cut before sending any input to the replacement.
func (c *conv) rotateExternalSession(req *compactRequest, trigger string) (bool, error) {
	rec := c.record()
	var (
		rp  ResolvedProfile
		err error
	)
	if req.target != nil {
		rp = req.target.resolved
	} else {
		rp, err = c.r.resolve(rec.Profile, rec.Root)
		if err != nil {
			return false, fmt.Errorf("resolve profile: %w", err)
		}
	}
	var records []wslive.RingRecord
	if c.dir != "" {
		if page, err := wslive.ReadRingPage(filepath.Join(c.dir, "ring"), "", wslive.RingHistoryCap); err == nil {
			records = page.Records
		}
	}
	dialogRes := wscompact.BuildRingDialog(records, "ring")
	initialPrompt, latestInstruction := "", ""
	ticket := dialogRes.Ticket
	if !rec.Lead {
		initialPrompt, latestInstruction = rec.InitialPrompt, rec.LatestInstruction
		if initialPrompt == "" {
			ringInitial, ringLatest := workerTaskFromRing(records)
			initialPrompt = ringInitial
			if latestInstruction == "" {
				latestInstruction = ringLatest
			}
		}
		if ticket == "" {
			if t := wscompact.TicketRefRE.FindString(latestInstruction); t != "" {
				ticket = t
			} else if t := wscompact.TicketRefRE.FindString(initialPrompt); t != "" {
				ticket = t
			}
		}
	}
	prose := make([]string, len(req.lead.Prose))
	for i, text := range req.lead.Prose {
		// Carried whole: the lead's own prose has no per-field cap (ParseLeadSummaryDraft).
		prose[i] = wscompact.Scrub(text)
	}
	var rereads string
	if len(req.lead.Paths) > 0 {
		files := wsdashboard.ReadWorktreeTextFiles(rec.Root, req.lead.Paths, wscompact.RereadBytes)
		rereads = wscompact.FormatRereads(req.lead.Paths, files)
	}
	state := c.r.compactionState(rec.ReplyID)(dialogRes.LastCompactAt)
	checkpoint := wscompact.BuildCheckpoint(wscompact.CheckpointInput{
		Lead:              rec.Lead,
		Ticket:            ticket,
		Playbook:          dialogRes.Playbook,
		InitialPrompt:     initialPrompt,
		LatestInstruction: latestInstruction,
		State:             state,
		OmitChildren:      rec.Lead && c.r.opts.RestoreBundle != nil,
		Prose:             prose,
		Rereads:           rereads,
		Dialog:            dialogRes.Dialog,
	})

	preTokens := int64(math.Round(c.compactTokens))
	wasHard := c.compactHardAtStart || c.aboveCompactHard() || c.compactHardSteered || req.crossing
	at := time.Now()
	if c.r.opts.Now != nil {
		at = c.r.opts.Now()
	}
	cut, err := wsaudit.NewEnvelope(rec.ReplyID, rec.Backend, at, wsaudit.KindCompaction)
	if err != nil {
		return false, fmt.Errorf("build compaction audit envelope: %w", err)
	}
	cut.Subkind, cut.Out = trigger, checkpoint
	cutMeta := map[string]any{"mode": wsconfig.CompactionModeWS, "trigger": trigger}
	if preTokens > 0 {
		cutMeta["pre_tokens"] = preTokens
	}
	if req.triggerPath != "" {
		cutMeta["trigger_path"] = req.triggerPath
	}
	if req.target != nil {
		cutMeta["from_backend"] = rec.Backend
		cutMeta["to_backend"] = req.target.backend
	}
	if trigger == wslive.TriggerAuto {
		cutMeta["turn_rotations"] = c.compactTurnRotations + 1
	}
	cut.Meta = cutMeta

	started := false
	err = c.sendStandalonePrompt(false, func() error {
		started = true
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart})
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart, Trigger: trigger})

		if err := c.retireCompactionProcess(); err != nil {
			return fmt.Errorf("retire session holder: %w", err)
		}

		noteBefore := c.compactNote
		var switchState *harnessSwitchState
		if req.target != nil {
			switchState = c.beginHarnessSwitch(req.target)
		}

		// Launch with an empty native binding and keep the prior on-disk
		// SessionID/SessionFile intact until both startLead and
		// commitAuditCheckpoint succeed, so a failed rotation rolls back cleanly.
		fresh := c.record()
		fresh.SessionID, fresh.SessionFile = "", ""
		pendingSlot := ""
		if req.target == nil {
			pendingSlot = c.pendingAccountSlot()
			if pendingSlot != "" {
				fresh.AccountSlotID = storedAccountSlotID(pendingSlot)
				fresh.PendingAccountSlotID = ""
			}
		}
		start := c.startLead
		if !rec.Lead {
			start = c.startWorker
		}
		if err := start(fresh, rp.Definition, false); err != nil {
			c.rollbackHarnessSwitch(switchState, noteBefore)
			return fmt.Errorf("start fresh session: %w", err)
		}

		if committer, ok := c.proc.(auditCheckpointCommitter); ok {
			id := wsaudit.SourceID{Source: "ws-compact-" + newReportID(), Seq: 1}
			if err := committer.commitAuditCheckpoint(context.Background(), id, cut); err != nil {
				c.compactNote = noteBefore
				retireErr := c.retireCompactionProcess()
				c.rollbackHarnessSwitch(switchState, noteBefore)
				return errors.Join(fmt.Errorf("commit compaction audit checkpoint: %w", err), retireErr)
			}
		}

		prevPostRotationNoShrink := c.compactPostRotationNoShrink
		c.compactOrigin = trigger
		c.compactCommand = false
		c.compactRotated = true
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: trigger})
		c.compactRotated = false
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: trigger})
		// The replacement session starts with a clean immediate hard-cap slate;
		// if its first fresh usage sample is still >= hard, observeCompaction
		// restores the no-shrink count from compactPostRotationNoShrink. A
		// cross-harness switch moves to a different harness/window, so it never
		// carries the source harness's hard-cap/no-shrink state forward.
		c.resetHardOverflow()
		if wasHard && req.target == nil {
			c.compactPostRotationCheck = true
			c.compactPostRotationNoShrink = prevPostRotationNoShrink + 1
		}
		if trigger == wslive.TriggerAuto {
			c.compactTurnRotations++
		} else {
			c.compactTurnRotations = 0
		}
		if strings.TrimSpace(c.compactNote) != "" {
			c.compactNote += "\n\n" + checkpoint
		} else {
			c.compactNote = checkpoint
		}
		pending := pendingCompactionFromState(c.compactNote, c.compactResume, c.compactResumeInternal, trigger)
		c.accountSlotBoundaryPending = false
		c.slot2aVerifyPending = false
		if switchState != nil {
			if err := c.commitHarnessSwitch(switchState, req.target, pending); err != nil {
				retireErr := c.retireCompactionProcess()
				c.rollbackHarnessSwitch(switchState, noteBefore)
				return errors.Join(fmt.Errorf("persist switched record: %w", err), retireErr)
			}
		} else {
			c.update(func(rec *Record) {
				sends := rec.Sends
				saved := clonePendingCompaction(pending)
				saved.RotatedAfterSends = &sends
				rec.SessionID, rec.SessionFile = "", ""
				if pendingSlot != "" {
					rec.AccountSlotID = storedAccountSlotID(pendingSlot)
					rec.PendingAccountSlotID = ""
				}
				rec.PendingCompaction = saved
				if !rec.Lead && rec.InitialPrompt == "" && initialPrompt != "" {
					rec.InitialPrompt = initialPrompt
					if rec.LatestInstruction == "" && latestInstruction != "" && latestInstruction != initialPrompt {
						rec.LatestInstruction = latestInstruction
					}
				}
			})
			if pendingSlot != "" {
				c.changed(false)
				c.watched()
			}
		}
		return nil
	})
	if err != nil {
		if started && c.compacting {
			c.compactCommand = false
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed, Text: err.Error()})
		}
		return false, err
	}
	return true, nil
}

func (c *conv) retireCompactionProcess() error {
	proc := c.proc
	c.setStatus(func() { c.proc = nil })
	c.events = nil
	c.clearProcessMCP()
	c.clearWorkerRejoin()
	if proc != nil {
		_ = proc.Close()
		if audited, ok := proc.(interface{ waitForAudit() }); ok {
			audited.waitForAudit()
		}
		c.releaseHolder(proc)
	}
	return c.retireHolder()
}

func (c *conv) compactionStateInstruction() string {
	if c.rec.CompanionRole == companionRoleBody {
		// No ws/compact or agenda tool: the daemon carries companion-body state.
		return ""
	}
	if !c.rec.Lead && c.workerPermissionLevel == PermissionReadOnly {
		return "Put the state needed to resume in your report."
	}
	if c.r != nil && c.compactUnsupported() == "" {
		return "If your agenda needs updating, pass agenda_updates when calling ws/compact."
	}
	return "Save the state needed to resume in your agenda."
}

// compactQueuedForIdle reports a compaction request that is only waiting for
// the idle boundary (a mid-turn ws/compact, or one restored after a restart):
// nothing is dispatched or rotating yet, so the open turn is what delays it.
// Hard-cap enforcement must keep working for such a turn; otherwise a long
// tool loop that never ends its turn outgrows the cap unchecked. Stage 1
// internal requests and an active Stage 1 prose turn dispatch themselves and
// are excluded.
func (c *conv) compactQueuedForIdle() bool {
	req := c.compactPending
	return req != nil && !req.stage1 && !c.compacting && !c.compactCommand && !c.compactStage1Active
}

func (c *conv) canSteerCompaction() bool {
	return c.turn && c.proc != nil && !c.quitting && !c.replaying && !c.wrapHold && !c.closed && !c.hidden &&
		!c.compacting && !c.compactCommand && (c.compactPending == nil || c.compactQueuedForIdle()) && !c.compactHardHandled && !c.compactHardIssued &&
		(!c.r.lifecycle(c.rec.Backend).NoWorkerCompactionSteer || c.rec.Lead) && (c.caps == nil || c.caps.Allows(ItemSteer)) &&
		// A turn that outlived its checkout's release gets no guidance.
		c.released() == ""
}

// steerCompaction uses live usage for the first warning and one completed
// tool batch for each subsequent warning. A request takes over at idle.
func (c *conv) steerCompaction(boundary bool) {
	if !c.canSteerCompaction() {
		return
	}
	c.refreshCompaction()
	st := controlsFor(c.proc).steer
	if st == nil || !c.aboveCompactHard() || c.compactHardSteerID != "" ||
		(c.compactHardSteered && (!boundary || c.r.lifecycle(c.rec.Backend).SingleHardCompactionSteer)) {
		return
	}
	hard := c.compactHard.Point(c.compactWindow)
	var text string
	if c.rec.CompanionRole == companionRoleBody {
		// A companion-body session cannot call ws/compact: only ask it to end
		// the turn so the daemon-managed rotation can run.
		text = fmt.Sprintf("Context usage is %s and has passed the hard point of %s. Wrap up and end your turn now so the session can be refreshed.", compactTokenCount(c.compactTokens), compactTokenCount(hard))
	} else if c.compactPending != nil && !c.compactHardSteered {
		// ws/compact is already queued: calling it again changes nothing, only
		// ending the turn lets it run.
		text = fmt.Sprintf("Context usage is %s and has passed the hard point of %s. Your ws/compact request is queued and cannot run while your turn is open: stop calling tools and end your turn now so compaction can run and work can continue.", compactTokenCount(c.compactTokens), compactTokenCount(hard))
	} else if c.compactHardSteered {
		text = fmt.Sprintf("Hard %s, +%.0f%% (%s). End your turn for auto-compaction and continuation.", compactTokenCount(hard), (c.compactTokens/hard-1)*100, compactTokenCount(c.compactTokens))
	} else {
		text = fmt.Sprintf("Context usage is %s and has reached the hard point of %s. %s Wrap up the current step and end your turn so compaction can run and work can continue.", compactTokenCount(c.compactTokens), compactTokenCount(hard), c.compactionStateInstruction())
	}
	var id string
	var err error
	// canSteerCompaction's release check is a snapshot; the steer is
	// admitted against it once more, and a refused one is dropped quietly.
	if checkout := c.admitInput(func() {
		if p, ok := c.proc.(durableSteerProcess); ok && c.rec.DurableTurn != nil {
			err = p.submitSteer(c.rec.DurableTurn.ID, durableSteer{ID: newReportID(), Text: text})
		} else if advisory, ok := st.(interface{ steerCompaction(string) (string, error) }); ok {
			id, err = advisory.steerCompaction(text)
		} else {
			err = st.Steer(text)
		}
	}); checkout != "" {
		c.dropReleasedInput("compaction guidance", checkout)
		return
	}
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: steer compaction guidance: %v\n", c.rec.ReplyID, err)
		return
	}
	if id == "" {
		if !c.compactHardSteered {
			c.compactHardStepPending = false
		}
		c.compactHardSteered = true
	} else if !c.compactHardSteered {
		c.compactHardSteerID = id
	}
}

func (c *conv) compactSteerAnswered(reply compactSteerReply) {
	if reply.id == c.compactHardSteerID {
		c.compactHardSteerID = ""
		if reply.refusal == "" {
			if !c.compactHardSteered {
				c.compactHardStepPending = false
			}
			c.compactHardSteered = true
		} else {
			c.compactHardSteered = false
		}
	}
	if reply.refusal != "" {
		fmt.Fprintf(c.r.opts.Log, "agent %s: the harness refused compaction guidance: %s\n", c.rec.ReplyID, reply.refusal)
	}
}

func (c *conv) clearCompactionSteerReplies() {
	c.compactHardSteerID = ""
}

func compactTokenCount(tokens float64) string {
	if tokens < 1000 {
		return fmt.Sprintf("%.0f", tokens)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", tokens/1000), ".0") + "k"
}

// stageCompactionNudge prepares the note and the levels its delivery covers.
// A jump from below soft still uses the first advisory's wording.
func (c *conv) stageCompactionNudge(hard float64) {
	soft := c.compactAdvisory.Point(c.compactWindow)
	level := 0
	var point float64
	if c.compactAdvisoryEnabled && c.compactTokens >= soft {
		level, point = 1, soft
		if m1, m2, ok := c.compactMilestones(c.compactWindow); ok {
			switch {
			case c.compactTokens >= m2:
				level, point = 3, m2
			case c.compactTokens >= m1:
				level, point = 2, m1
			}
		}
	}
	if level == 0 || c.compactAdvised && (level == 1 || level == 2 && c.compactM1Issued || level == 3 && c.compactM2Issued) {
		c.compactNudge = compactionNudge{}
		return
	}
	if c.rec.CompanionRole == companionRoleBody {
		// A companion-body session has no ws/compact tool to act on the advisory;
		// its rotation is daemon-managed (hard cap or runtime rotation).
		c.compactNudge = compactionNudge{}
		return
	}
	supported := c.compactUnsupported() == ""
	standalone := supported && !c.r.durableProcess(c.rec.Backend)
	usage := fmt.Sprintf("Context usage is %.0f tokens (%.0f%%); ", c.compactTokens, c.compactTokens/c.compactWindow*100)
	if c.compactAdvised {
		usage += fmt.Sprintf("compaction reminder %d/3 (%.0f tokens, hard point %.0f tokens). ", level-1, point, hard)
	} else {
		usage += fmt.Sprintf("advisory point %.0f tokens (%.0f%%). ", soft, soft/c.compactWindow*100)
	}
	action := "Write durable state to your agenda before compacting. No summary prose is requested."
	switch {
	case supported && c.rec.Lead && (c.r.durableProcess(c.rec.Backend) || c.wsManagedLeadCompaction()):
		action = "Call ws/compact directly at a safe boundary with the carry-forward fields in lead_summary (and agenda_updates if your agenda needs updating)."
	case supported:
		action = "Call ws/compact directly at a safe boundary before continuing (include agenda_updates if your agenda needs updating). No summary prose is requested."
	}
	c.compactNudge = compactionNudge{usage + action, level, standalone}
}

func (c *conv) consumeCompactionNudge() {
	if level := c.compactNudge.level; level > 0 {
		c.compactAdvised = true
		c.compactM1Issued = c.compactM1Issued || level >= 2
		c.compactM2Issued = c.compactM2Issued || level >= 3
		c.compactNudge = compactionNudge{}
	}
}

func (c *conv) compactionNudgeReady() bool {
	// A worker may still be waiting for the harness to resume its held turn.
	return c.compactNudge.standalone && c.compactUsageFresh && c.proc != nil &&
		!c.compacting && !c.compactCommand && c.compactPending == nil && c.compactNote == "" &&
		(c.rec.Lead || c.bgTasks == 0 && c.bgGraceTimer == nil)
}

func (c *conv) sendCompactionNudge() bool {
	if !c.compactionNudgeReady() {
		return false
	}
	c.releaseHeld()
	if err := c.sendStandalonePrompt(false, func() error { return c.proc.Send(c.compactNudge.text) }); err != nil {
		if checkout, ok := releasedInput(err); ok {
			// A release refused the nudge: nothing was sent, and the
			// process keeps running; the input that waits meets the same
			// refusal in kickInput.
			c.dropReleasedInput("compaction advisory", checkout)
			return false
		}
		// Leave the crossing unconsumed for the next fresh usage sample.
		// Ordinary input, if any, can restart the harness.
		c.setStatus(func() { c.lastErr = "send compaction advisory: " + err.Error() })
		c.stopProcess("")
		return false
	}
	c.consumeCompactionNudge()
	c.compactInternalTurn = true
	c.beginTurn(false)
	return true
}

func (c *conv) restartRestoreHasInput() bool {
	if len(c.rec.Queued) > 0 || len(c.rec.Mail) > 0 || len(c.rec.Reports) > 0 {
		return true
	}
	held, err := wsinbox.Held(c.inbox)
	return err != nil || len(held) > 0
}

func (c *conv) compactHoldsReport() bool {
	return (c.compacting && c.heldRes != nil) || (c.compactPending != nil && c.compactPending.resume) ||
		((c.compacting || c.compactCommand || c.compactNote != "") && c.compactResume)
}

func (c *conv) armCompactWatchdog() {
	c.stopCompactWatchdog()
	if c.r != nil && c.r.opts.Timer != nil {
		c.compactC, c.compactStop = c.r.opts.Timer(compactCommandTimeout)
	}
}

func (c *conv) stopCompactWatchdog() {
	if c.compactStop != nil {
		c.compactStop()
	}
	c.compactC, c.compactStop = nil, nil
}

func (c *conv) compactTimedOut() {
	c.compactC, c.compactStop = nil, nil
	if !c.compacting && !c.compactCommand {
		return
	}
	c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
	c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed, Text: "compaction timed out"})
	c.setStatus(func() { c.lastErr = "compaction timed out" })
	c.stopProcess("compaction_timeout")
	c.changed(false)
	c.kick()
	c.releaseHeld()
}

func (c *conv) compactEvent(ev wslive.Event) {
	if ev.Kind != wslive.KindCompact {
		return
	}
	completed := false
	defer func() { c.publishCompaction(completed) }()
	switch ev.Phase {
	case wslive.CompactStart:
		c.stopIdle()
		if !c.compacting {
			c.compactHardAtStart = c.aboveCompactHard() || c.compactHardSteered
		}
		c.compacting = true
		c.clearCompactionSteerReplies()
		c.compactUsageFresh = false
		c.compactNudge = compactionNudge{}
		c.compactAdvised, c.compactM1Issued, c.compactM2Issued, c.compactHardIssued = true, true, true, true
	case wslive.CompactFailed:
		c.compacting, c.compactResume = false, false
		c.compactResumeInternal = false
		c.compactHardAtStart = false
		c.compactOrigin = ""
		if !c.rec.Lead && !c.replaying && c.rec.PendingCompaction != nil {
			c.update(func(rec *Record) { rec.PendingCompaction = nil })
		}
	case wslive.CompactEnd:
		// Duplicate ends carry neither a second note nor another continue turn.
		if !c.compacting {
			return
		}
		c.compacting = false
		completed = true
		origin := c.compactOrigin
		if origin == "" {
			origin = wslive.CompactOriginNative
		}
		c.setStatus(func() { c.cmpCount++; c.cmpLast = origin })
		if c.compactPending != nil && c.compactPending.crossing {
			// Native compaction already handled the queued request's crossing.
			c.compactPending = nil
		}
		if c.compactStage1Active {
			// The harness supplied the cut while Stage 1 was running. The
			// terminal event still settles the internal prompt.
			if c.compactPending == nil {
				c.compactResume = c.compactStage1Resume
				c.compactResumeInternal = c.compactStage1Resume && c.compactStage1Internal
			}
			c.compactStage1Active, c.compactStage1Resume, c.compactStage1Internal, c.compactStage1Auto, c.compactStage1UsageLimited = false, false, false, false, false
		}
		// A compaction that began at or above hard counts as no-shrink until a
		// later sample drops below hard (disarmCompactionLatches resets it).
		// Only a bounded run of them disarms overflow protection.
		if c.compactHardAtStart || c.aboveCompactHard() {
			c.compactNoShrink++
			c.compactHardHandled = c.compactNoShrink >= compactNoShrinkCap
		}
		if !c.compactRotated {
			if origin == wslive.TriggerAuto {
				c.compactTurnRotations++
			} else if origin == wslive.TriggerManual {
				c.compactTurnRotations = 0
			}
		}
		c.compactHardAtStart = false
		// A stats answer queued before compaction may still carry old tokens.
		// Only the next model turn can produce a fresh post-compaction sample.
		c.compactAwaitTurn = true
		c.compactPrevTokens = 0
		c.compactAdvised, c.compactM1Issued, c.compactM2Issued, c.compactHardIssued, c.compactUsageFresh = false, false, false, false, false
		c.queueRestore(RestoreCompaction)
		c.appendRestoreInstructions()
		if !c.compactRotated && !c.replaying && !c.r.durableProcess(c.rec.Backend) && c.hasPendingAccountSlot() {
			// Never tear down mid-turn during native auto-compaction; defer the
			// cold-restart commit until endTurn -> compactIdle() (D6).
			c.accountSlotBoundaryPending = true
		}
		if !c.rec.Lead && !c.r.durableProcess(c.rec.Backend) && c.compactResume && !c.replaying && c.rec.PendingCompaction != nil {
			c.persistWorkerPendingCompaction(compactStageCompleted, 0)
		}
	}
}

func (c *conv) appendRestoreInstructions() {
	if c.r.opts.Backends[c.rec.Backend].CompactionProtocol().PromptPersists {
		return
	}
	if c.rec.Lead {
		// Rotation starts a fresh process via startLead, which already
		// embeds composeLeadInstructions (including leadManual).
		if !c.compactRotated || !c.r.lifecycle(c.rec.Backend).ComposedLeadInstructions {
			c.compactNote += c.leadManual(c.record())
		}
		return
	}
	if c.systemPrompt == "" && c.rec.SystemPrompt && c.dir != "" {
		if raw, err := os.ReadFile(filepath.Join(c.dir, systemPromptFile)); err == nil {
			c.systemPrompt = string(raw)
		}
	}
	if c.systemPrompt != "" {
		c.compactNote += c.systemPrompt + "\n"
	}
}

func (c *conv) reconcileWorkerCompactionReplay() {
	if c.rec.Lead || c.r.durableProcess(c.rec.Backend) {
		return
	}
	if c.replayCompactFailed {
		c.replayCompactFailed, c.replayCompactEnded = false, false
		c.compacting, c.compactResume, c.compactResumeInternal, c.compactCommand = false, false, false, false
		c.compactOrigin = ""
		if c.rec.PendingCompaction != nil {
			c.update(func(rec *Record) { rec.PendingCompaction = nil })
		}
		return
	}
	if c.replayCompactEnded {
		c.replayCompactEnded = false
		c.compacting, c.compactCommand = false, false
		if c.compactResume {
			c.queueRestore(RestoreCompaction)
			c.appendRestoreInstructions()
			c.persistWorkerPendingCompaction(compactStageCompleted, 0)
		}
		return
	}
	if pc := c.rec.PendingCompaction; pc != nil && pc.Stage == compactStageRunning && !c.compacting {
		// Non-quiet replay processed CompactEnd or CompactFailed while c.replaying
		// was true; reconcile disk PendingCompaction with the resulting memory state.
		c.compactCommand = false
		if c.compactResume {
			c.persistWorkerPendingCompaction(compactStageCompleted, 0)
		} else {
			c.update(func(rec *Record) { rec.PendingCompaction = nil })
		}
	}
}

// publishCompaction mirrors the compaction state State shows and tells the
// watchers when it changed: a compaction queued, running or ended (active), or
// one completed (completed).
func (c *conv) publishCompaction(completed bool) {
	active := c.compactPending != nil || c.compacting || c.compactCommand
	changed := completed
	c.setStatus(func() {
		if c.cmpActive != active {
			c.cmpActive, changed = active, true
		}
	})
	if changed {
		c.watched()
	}
}

// seedCompactions raises the count a restarted runner begins from zero at to
// the one an earlier run's row still holds (a lower one is stale), taking the
// row's latest origin only while this run has completed none itself.
func (c *conv) seedCompactions(n int, origin string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > c.cmpCount {
		c.cmpCount = n
		if c.cmpLast == "" {
			c.cmpLast = origin
		}
	}
}

func (c *conv) compactionCount() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmpCount, c.cmpLast
}

func (c *conv) compactInput(text string) string {
	if !c.compactNudge.standalone && c.compactNudge.text != "" {
		text = c.compactNudge.text + "\n\n" + text
	}
	if c.compactNote != "" {
		text = strings.TrimSpace(c.renderPendingNote(c.compactNote)) + "\n\n" + text
	}
	return text
}

func (c *conv) compactionFallbackRestoreNote() string {
	return fmt.Sprintf("<ws-restore cause=\"compaction\">\nYour ws session key is `%s`. Context was compacted. Fresh session state is available through the lead-only ws workflow_state tool. Keep using this same key.\n</ws-restore>\n", wscompact.EscapeFramingCloseTags(c.rec.WSKey))
}

func (c *conv) queueRestore(cause RestoreCause) {
	marker := compactionRestoreMarker
	if cause == RestoreRestart {
		marker = restartRestoreMarker
	}
	if c.r == nil || c.r.opts.RestoreBundle == nil {
		if cause == RestoreCompaction {
			c.compactNote = c.compactionFallbackRestoreNote()
		}
		return
	}
	c.compactNote = marker
}

const heldMailRestoreByteBudget = 4 * 1024

func formatHeldMailRestore(mails []string) string {
	if len(mails) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<held-mail>\n## Held Undelivered Mail\n")
	remaining := heldMailRestoreByteBudget - len("</held-mail>\n") - b.Len()
	for i, raw := range mails {
		if remaining <= 40 {
			fmt.Fprintf(&b, "- ... %d more held message(s) will deliver with your next input turn.\n", len(mails)-i)
			break
		}
		escaped := wscompact.EscapeFramingCloseTags(strings.TrimSpace(raw))
		if escaped == "" {
			escaped = "(empty)"
		}
		if len(escaped) > remaining-16 {
			escaped = wscompact.EscapeFramingCloseTags(wscompact.BoundedText(escaped, max(32, remaining-16)))
		}
		line := fmt.Sprintf("- [%d] %s\n", i+1, escaped)
		if len(line) > remaining && i > 0 {
			fmt.Fprintf(&b, "- ... %d more held message(s) will deliver with your next input turn.\n", len(mails)-i)
			break
		}
		b.WriteString(line)
		remaining -= len(line)
	}
	b.WriteString("</held-mail>")
	return b.String()
}

func insertBeforeRestoreClose(bundle, section string) string {
	if section == "" {
		return bundle
	}
	if idx := strings.LastIndex(bundle, "</ws-restore>"); idx >= 0 {
		head := strings.TrimRight(bundle[:idx], "\n")
		return head + "\n\n" + section + "\n" + bundle[idx:]
	}
	return strings.TrimSpace(bundle) + "\n\n" + section
}

// renderPendingNote resolves the marker at the last possible point before a
// send. The marker stays in compactNote until that send succeeds, so a failed
// send retries with newly read state.
func (c *conv) renderPendingNote(note string) string {
	if tail, ok := strings.CutPrefix(note, leadStartStateMarker); ok {
		state, err := c.r.opts.LeadSessionState(c.rec.WSKey)
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: first-turn session state: %v\n", c.rec.ReplyID, err)
			state = "The hosted session state could not be rendered for this turn. Call workflow_state with your existing key before continuing."
		}
		return strings.TrimSpace(state) + "\n\n" + tail
	}
	cause, tail := RestoreCause(""), ""
	switch {
	case strings.HasPrefix(note, compactionRestoreMarker):
		cause, tail = RestoreCompaction, strings.TrimPrefix(note, compactionRestoreMarker)
	case strings.HasPrefix(note, restartRestoreMarker):
		cause, tail = RestoreRestart, strings.TrimPrefix(note, restartRestoreMarker)
	default:
		return note
	}
	rec := c.record()
	cfg := c.r.opts.Config
	cfg.RepoRoot = rec.Root
	caller := LeadCaller(rec.ReplyID, rec.Root, rec.Backend, cfg)
	if !rec.Lead {
		caller = c.r.holderCaller(rec)
	}
	bundle, err := c.r.opts.RestoreBundle(rec.Backend, rec.workDir(), rec.WSKey, cause, c.r.AgentList(caller))
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: restore bundle: %v\n", rec.ReplyID, err)
		bundle = fmt.Sprintf("<ws-restore cause=\"%s\" status=\"error\">\nYour ws session key is `%s`. The hosted session-state restore bundle could not be rendered after %s: %s. Call workflow_state with this same key before continuing.\n</ws-restore>\n", wscompact.EscapeXMLAttr(string(cause)), wscompact.EscapeFramingCloseTags(rec.WSKey), wscompact.EscapeFramingCloseTags(string(cause)), wscompact.EscapeFramingCloseTags(err.Error()))
	}
	if strings.TrimSpace(tail) != "" {
		bundle = strings.TrimSpace(bundle) + "\n\n" + strings.TrimSpace(tail)
	}
	return bundle
}
