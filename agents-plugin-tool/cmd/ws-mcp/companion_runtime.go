package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

const (
	companionTierBody = "small"
	companionTierWork = "medium"
)

type companionLaunchSpec struct {
	Backend  string
	Model    string
	Effort   string
	Provider string
}

// companionRuntime coordinates the persistent Mobile Full Lead (Brain,
// companion-work) lifecycle in the 3-tier Companion architecture: lazy attach
// and launch via GET /api/companion/brain, cross-harness/model/effort handoffs
// with CompanionHandoffCheckpoint, startup reattach of any surviving Brain
// session, and startup teardown of any pre-upgrade persistent companion-body
// sessions.
type companionRuntime struct {
	dir     string
	root    string
	agents  *wsagent.Runner
	daemon  func() *wsdashboard.Daemon
	logw    io.Writer
	nowFunc func() time.Time

	// Test seam: overrides harness/tier resolution when non-nil.
	resolveLaunch func(role string) (companionLaunchSpec, bool)
	// configOpts locates the config the companion tier settings and
	// agents.tier read; zero (the default homes) outside tests.
	configOpts wsconfig.Options

	// Serialization mutexes ordered strictly before r.mu -> wsagent locks.
	bodyLaunchMu sync.Mutex
	workLaunchMu sync.Mutex

	mu sync.Mutex

	workReplyID          string
	workWSKey            string
	workPrevID           string
	workHandoffs         int
	workSeededFromLegacy bool
	closedWorkKeys       map[string]struct{}

	// Counter for observability/testing.
	workLaunches int

	stopOnce      sync.Once
	stopCh        chan struct{}
	closedRuntime bool
	wg            sync.WaitGroup
}

func newCompanionRuntime(dir, root string, agents *wsagent.Runner, daemon func() *wsdashboard.Daemon, logw io.Writer) *companionRuntime {
	if logw == nil {
		logw = io.Discard
	}
	return &companionRuntime{
		dir:            dir,
		root:           root,
		agents:         agents,
		daemon:         daemon,
		logw:           logw,
		closedWorkKeys: map[string]struct{}{},
		stopCh:         make(chan struct{}),
	}
}

func (r *companionRuntime) launchRoot(d *wsdashboard.Daemon) string {
	if r.root != "" {
		return r.root
	}
	if d != nil {
		if wsRoot := d.CompanionActiveRoot(); wsRoot != "" {
			return wsRoot
		}
	}
	return r.dir
}

func (r *companionRuntime) now() time.Time {
	if r.nowFunc != nil {
		return r.nowFunc()
	}
	return time.Now()
}

// spawn launches fn tracked by r.wg unless Close has already begun, ensuring
// background runtime tasks drain cleanly before Close returns.
func (r *companionRuntime) spawn(fn func()) {
	r.mu.Lock()
	if r.closedRuntime {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

// Start reattaches any surviving persistent Brain session (closing duplicates)
// and gracefully closes any surviving pre-upgrade companion-body sessions.
func (r *companionRuntime) Start() {
	r.spawn(func() {
		if d := r.liveDaemon(); d != nil && r.agents != nil {
			r.workLaunchMu.Lock()
			r.reattachWorkLocked(d)
			r.workLaunchMu.Unlock()
			r.bodyLaunchMu.Lock()
			r.teardownLegacyBodiesLocked()
			r.bodyLaunchMu.Unlock()
		}
	})
}

// Close stops background runtime tasks and waits for them to drain.
func (r *companionRuntime) Close() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.closedRuntime = true
		r.mu.Unlock()
		close(r.stopCh)
	})
	r.wg.Wait()
}

// IsHolderAlive reports whether holder currently names a live hosted
// conversation. Retained for T0 CompanionPhoneStore CAS compatibility.
func (r *companionRuntime) IsHolderAlive(holder string) bool {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return false
	}
	if r.agents != nil {
		replyID := wsmailbox.ReplyID(holder)
		if r.agents.IsConversationOpen(replyID) {
			return true
		}
		if _, known := r.agents.CompanionRecord(replyID); known {
			return false
		}
	}
	return true
}

// OnUserWake is retained as a T0-safe callback hook; in the 3-tier
// architecture Messenger communicates directly with Brain (companion-work)
// via the embedded compact frame and Puppeteer observes Brain directly.
func (r *companionRuntime) OnUserWake(reason string) {
	_ = reason
}

// OnMessengerClosed is retained as a T0-safe callback hook; Messenger closing
// now pauses the compact Brain iframe stream directly via wsHideFrame.
func (r *companionRuntime) OnMessengerClosed(holder string) {
	_ = holder
}

// OnCompanionActivity is a runner callback hook kept for interface parity.
func (r *companionRuntime) OnCompanionActivity(role, replyID, wsKey string) {
	_, _, _ = role, replyID, wsKey
}

// OnCompanionTurnEnd is a runner callback hook kept for interface parity.
func (r *companionRuntime) OnCompanionTurnEnd(role, replyID, wsKey, status, errText string) {
	_, _, _, _, _ = role, replyID, wsKey, status, errText
}

// OnCompanionClose handles conversation close or unexpected crash for
// companion-work (Brain). Must not block on the runner loop.
func (r *companionRuntime) OnCompanionClose(role, replyID, wsKey, reason string) {
	if role != mcp.CompanionRoleWork {
		return
	}
	r.spawn(func() { r.handleWorkClose(replyID, wsKey, reason) })
}

func (r *companionRuntime) handleWorkClose(replyID, wsKey, reason string) {
	r.mu.Lock()
	if wsKey != "" && (reason == "close" || reason == "crash") {
		r.closedWorkKeys[wsKey] = struct{}{}
	}
	if replyID != "" && replyID == r.workReplyID {
		r.workPrevID = replyID
		r.workReplyID = ""
		r.workWSKey = ""
	}
	r.mu.Unlock()
}

// companionSpecMatches reports whether rec runs on spec. An empty spec field
// (harness default) matches anything.
func companionSpecMatches(rec wsagent.Record, spec companionLaunchSpec) bool {
	eq := func(have, want string) bool { return want == "" || have == want }
	return eq(rec.Backend, spec.Backend) && eq(rec.Model, spec.Model) && eq(rec.Effort, spec.Effort) && eq(rec.Provider, spec.Provider)
}

// companionHandoffPreamble introduces the checkpoint a replacement Brain
// session inherits from the session it replaces.
const companionHandoffPreamble = "[companion handoff]\nYou are continuing as the same companion after a session handoff (a model/harness change or a restart). The checkpoint below carries the previous session's conversation; treat it as your own memory and continue naturally without announcing the handoff."

// teardownLegacyBodiesLocked gracefully closes any surviving pre-upgrade
// persistent companion-body hosted sessions left open by an earlier daemon.
// The caller holds bodyLaunchMu.
func (r *companionRuntime) teardownLegacyBodiesLocked() {
	if r.agents == nil {
		return
	}
	recs := r.agents.CompanionSessions(mcp.CompanionRoleBody)
	for _, rec := range recs {
		if _, err := r.agents.CloseConversation(rec.ReplyID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close legacy companion-body %s: %v\n", rec.ReplyID, err)
		}
	}
}

// reattachWorkLocked adopts the newest open companion-work Brain session that
// is not known closed, closes any stale duplicates, and registers Brain's
// replyID on the daemon so Puppeteer immediately observes it. The caller holds
// workLaunchMu.
func (r *companionRuntime) reattachWorkLocked(d *wsdashboard.Daemon) (string, string) {
	if r.agents == nil {
		return "", ""
	}
	recs := r.agents.CompanionSessions(mcp.CompanionRoleWork)
	r.mu.Lock()
	currentID, currentKey := r.workReplyID, r.workWSKey
	chosenID, chosenKey := currentID, currentKey
	var stale []wsagent.Record
	for _, rec := range recs {
		if rec.ReplyID == currentID {
			continue
		}
		_, closedKey := r.closedWorkKeys[rec.WSKey]
		if chosenID == "" && rec.WSKey != "" && !closedKey {
			chosenID, chosenKey = rec.ReplyID, rec.WSKey
			continue
		}
		stale = append(stale, rec)
	}
	for _, rec := range stale {
		if rec.WSKey != "" {
			r.closedWorkKeys[rec.WSKey] = struct{}{}
		}
	}
	adopted := currentID == "" && chosenID != ""
	if adopted {
		r.workReplyID, r.workWSKey = chosenID, chosenKey
	}
	r.mu.Unlock()
	if adopted {
		fmt.Fprintf(r.logw, "companion runtime: reattached companion-work brain %s\n", chosenID)
	}
	if chosenID != "" && d != nil {
		d.SetCompanionBrainReplyID(chosenID)
	}
	for _, rec := range stale {
		if _, err := r.agents.CloseConversation(rec.ReplyID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close stale companion-work %s: %v\n", rec.ReplyID, err)
		}
	}
	return chosenID, chosenKey
}

// BrainEndpoint serves GET /api/companion/brain by lazily ensuring or
// reattaching the Mobile Full Lead (Brain) session.
func (r *companionRuntime) BrainEndpoint() (wsdashboard.CompanionBrainInfo, error) {
	return r.EnsureBrainSession(r.liveDaemon())
}

// EnsureBrainSession lazily attaches, launches, or cleanly hands off the
// persistent Mobile Full Lead (Brain, companion-work) hosted session.
func (r *companionRuntime) EnsureBrainSession(d *wsdashboard.Daemon) (wsdashboard.CompanionBrainInfo, error) {
	if r.agents == nil {
		return wsdashboard.CompanionBrainInfo{Ready: false}, fmt.Errorf("agent runner unavailable")
	}
	r.workLaunchMu.Lock()
	defer r.workLaunchMu.Unlock()

	r.mu.Lock()
	existingID, existingKey := r.workReplyID, r.workWSKey
	seeded := r.workSeededFromLegacy
	r.mu.Unlock()
	if existingID == "" {
		existingID, existingKey = r.reattachWorkLocked(d)
	}

	personaName := "Companion"
	if d != nil {
		if name := strings.TrimSpace(d.CompanionResolvedPersona().Sheet.Name); name != "" {
			personaName = name
		}
	}

	spec, ok := r.pickLaunchSpec(mcp.CompanionRoleWork)
	tierLabel := companionTierWork
	if r.resolveLaunch == nil && r.agents != nil {
		if res := r.resolveRole(mcp.CompanionRoleWork); strings.TrimSpace(res.Tier) != "" {
			tierLabel = strings.TrimSpace(res.Tier)
		} else if strings.TrimSpace(res.Configured) != "" {
			tierLabel = strings.TrimSpace(res.Configured)
		}
	}

	if existingID != "" && existingKey != "" && r.agents.IsConversationOpen(existingID) {
		rec, known := r.agents.CompanionRecord(existingID)
		if !ok || !known || companionSpecMatches(rec, spec) {
			if d != nil {
				d.SetCompanionBrainReplyID(existingID)
			}
			return wsdashboard.CompanionBrainInfo{
				ReplyID:          existingID,
				WSKey:            existingKey,
				Harness:          rec.Backend,
				Model:            rec.Model,
				Tier:             tierLabel,
				PersonaName:      personaName,
				Ready:            true,
				SeededFromLegacy: seeded,
			}, nil
		}
		fmt.Fprintf(r.logw, "companion runtime: companion-work brain spec changed (%s/%s -> %s/%s); handing off\n", rec.Backend, rec.Model, spec.Backend, spec.Model)
	}
	if !ok {
		return wsdashboard.CompanionBrainInfo{
			PersonaName: personaName,
			Tier:        tierLabel,
			Ready:       false,
		}, fmt.Errorf("no available harness backend for companion-work")
	}
	if existingID == "" {
		r.mu.Lock()
		existingID = r.workPrevID
		r.mu.Unlock()
	}
	return r.launchWorkLocked(d, spec, tierLabel, personaName, existingID, existingKey)
}

// launchWorkLocked launches a new Mobile Full Lead (Brain, companion-work)
// session on spec. When prevID names an earlier Brain (cross-harness/model/
// effort handoff or crash recovery), it captures CompanionHandoffCheckpoint
// from prevID, updates workReplyID/workWSKey, broadcasts the handoff via
// SetCompanionBrainReplyID, and closes prevID. On a brand-new Brain with no
// prior Brain checkpoint, it performs a single read-only check of legacy
// companion-messenger/seg-*.jsonl to preserve recent conversational continuity.
// The caller holds workLaunchMu.
func (r *companionRuntime) launchWorkLocked(d *wsdashboard.Daemon, spec companionLaunchSpec, tierLabel, personaName, prevID, prevKey string) (wsdashboard.CompanionBrainInfo, error) {
	var sheet wsdashboard.CharacteristicSheet
	var lang string
	var legacySeed string
	if d != nil {
		sheet, lang, _ = d.CompanionActiveSoulLaunch()
		if name := strings.TrimSpace(sheet.Name); name != "" {
			personaName = name
		}
		if prevID == "" {
			legacySeed = d.CompanionLegacyMessengerSeed(10, 4096)
		}
	}
	seeded := legacySeed != ""
	prompt := wsdashboard.BuildCompanionBrainSystemPrompt(sheet, lang, legacySeed)
	if prevID != "" {
		if checkpoint, err := r.agents.CompanionHandoffCheckpoint(prevID, ""); err == nil {
			prompt += "\n\n" + companionHandoffPreamble + "\n\n" + checkpoint
		} else {
			fmt.Fprintf(r.logw, "companion runtime: handoff checkpoint from companion-work %s: %v\n", prevID, err)
		}
	}
	st, err := r.agents.CreateLead(spec.Backend, r.launchRoot(d), wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleWork,
		CompanionSystemPrompt: prompt,
		Model:                 spec.Model,
		Provider:              spec.Provider,
		Effort:                spec.Effort,
	})
	if err != nil {
		return wsdashboard.CompanionBrainInfo{PersonaName: personaName, Tier: tierLabel, Ready: false}, err
	}
	rec, ok := r.agents.CompanionRecord(st.ID)
	if !ok || rec.WSKey == "" {
		return wsdashboard.CompanionBrainInfo{PersonaName: personaName, Tier: tierLabel, Ready: false}, fmt.Errorf("companion-work session %s has no minted key", st.ID)
	}
	r.mu.Lock()
	r.workReplyID = st.ID
	r.workWSKey = rec.WSKey
	r.workPrevID = ""
	r.workSeededFromLegacy = seeded
	r.workLaunches++
	if prevKey != "" {
		r.closedWorkKeys[prevKey] = struct{}{}
	}
	if prevID != "" {
		r.workHandoffs++
	}
	r.mu.Unlock()
	if d != nil {
		d.SetCompanionBrainReplyID(st.ID)
	}
	if prevID != "" && r.agents.IsConversationOpen(prevID) {
		if _, err := r.agents.CloseConversation(prevID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close handed-off companion-work %s: %v\n", prevID, err)
		}
	}
	return wsdashboard.CompanionBrainInfo{
		ReplyID:          st.ID,
		WSKey:            rec.WSKey,
		Harness:          rec.Backend,
		Model:            rec.Model,
		Tier:             tierLabel,
		PersonaName:      personaName,
		Ready:            true,
		SeededFromLegacy: seeded,
	}, nil
}

func (r *companionRuntime) ensureWorkSession(d *wsdashboard.Daemon) (string, error) {
	info, err := r.EnsureBrainSession(d)
	if err != nil {
		return "", err
	}
	return info.ReplyID, nil
}

func (r *companionRuntime) liveDaemon() *wsdashboard.Daemon {
	if r.daemon == nil {
		return nil
	}
	return r.daemon()
}

func (r *companionRuntime) launchCounts() (body, work int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return 0, r.workLaunches
}
