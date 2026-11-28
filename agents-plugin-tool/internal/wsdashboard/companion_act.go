package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Interruptible ws/companion.act pacing bounds and Tier-1 burst cap (D7, D11).
const (
	CompanionActDefaultDelayMS = 3000
	CompanionActMinDelayMS     = 1500
	CompanionActMaxDelayMS     = 6000

	// CompanionBurstMaxCalls bounds Tier-1 burst tool calls (act, memory tools,
	// delegate_work) per lease/turn (D11).
	CompanionBurstMaxCalls = 12

	CompanionActWakeTimer       = "timer"
	CompanionActWakeUserMessage = "user_message"
	CompanionActWakeGlassKnock  = "glass_knock"
	CompanionActWakeCancelled   = "cancelled"

	companionKnockRoute     = "/api/companion/knock"
	companionKnockBodyLimit = 4 << 10
	companionKnockMaxCount  = 99

	companionRecallDefaultLimit = 10
	companionRecallMaxLimit     = 30
)

// CompanionActRequest carries visual/speech beat fields and pacing delay for
// ws/companion.act (D7).
type CompanionActRequest struct {
	SessionID  string `json:"session_id,omitempty"`
	Expression string `json:"expression,omitempty"`
	Gesture    string `json:"gesture,omitempty"`
	Prop       string `json:"prop,omitempty"`
	FX         string `json:"fx,omitempty"`
	Speech     string `json:"speech,omitempty"`
	DelayMS    int    `json:"delay_ms,omitempty"`
	HasDelay   bool   `json:"-"`
}

// CompanionKnockEvent describes a glass-knock interaction delivered via
// POST /api/companion/knock (D10).
type CompanionKnockEvent struct {
	Kind      string `json:"kind,omitempty"`
	Count     int    `json:"count,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// CompanionActResult is returned by ws/companion.act as soon as the animation
// delay expires or an interruption (user message, glass knock, cancellation)
// occurs.
type CompanionActResult struct {
	WakeReason   string                  `json:"wake_reason"`
	DelayMS      int                     `json:"delay_ms"`
	UserMessages []CompanionPhoneMessage `json:"user_messages,omitempty"`
	Knock        *CompanionKnockEvent    `json:"knock,omitempty"`
}

// CompanionRecallResult is returned by ws/companion.recall_memory.
type CompanionRecallResult struct {
	SoulID     string                   `json:"soul_id,omitempty"`
	Scope      string                   `json:"scope"`
	ProjectKey string                   `json:"project_key,omitempty"`
	Query      string                   `json:"query,omitempty"`
	Snippets   []CompanionRecallSnippet `json:"snippets"`
}

// ClampCompanionActDelayMS enforces D7 pacing bounds: omitted delay_ms defaults
// to 3000 ms; any provided value is clamped to 1500..6000 ms so act always paces
// and never spins.
func ClampCompanionActDelayMS(delayMS int, provided bool) int {
	if !provided {
		return CompanionActDefaultDelayMS
	}
	if delayMS < CompanionActMinDelayMS {
		return CompanionActMinDelayMS
	}
	if delayMS > CompanionActMaxDelayMS {
		return CompanionActMaxDelayMS
	}
	return delayMS
}

type companionActWaiter struct {
	msgCh   chan struct{}
	knockCh chan CompanionKnockEvent
}

// companionActHub coordinates in-memory fan-out of incoming user messages and
// glass-knock events into blocked ws/companion.act waiters (zero file I/O, zero
// polling).
type companionActHub struct {
	mu       sync.Mutex
	waiters  map[*companionActWaiter]struct{}
	changed  chan struct{}
	newTimer func(d time.Duration) (<-chan time.Time, func())
}

func (h *companionActHub) notifyChangedLocked() {
	if h.changed != nil {
		close(h.changed)
		h.changed = nil
	}
}

func (h *companionActHub) register() *companionActWaiter {
	w := &companionActWaiter{
		msgCh:   make(chan struct{}, 1),
		knockCh: make(chan CompanionKnockEvent, 1),
	}
	h.mu.Lock()
	if h.waiters == nil {
		h.waiters = make(map[*companionActWaiter]struct{})
	}
	h.waiters[w] = struct{}{}
	h.notifyChangedLocked()
	h.mu.Unlock()
	return w
}

func (h *companionActHub) unregister(w *companionActWaiter) {
	if w == nil {
		return
	}
	h.mu.Lock()
	delete(h.waiters, w)
	h.notifyChangedLocked()
	h.mu.Unlock()
}

func (h *companionActHub) notifyUserMessage(_ CompanionPhoneMessage) {
	h.mu.Lock()
	for w := range h.waiters {
		select {
		case w.msgCh <- struct{}{}:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *companionActHub) notifyKnock(ev CompanionKnockEvent) int {
	h.mu.Lock()
	woken := 0
	for w := range h.waiters {
		select {
		case w.knockCh <- ev:
			woken++
		default:
		}
	}
	h.mu.Unlock()
	return woken
}

func (h *companionActHub) waiterCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.waiters)
}

func (h *companionActHub) waiterWatch() (int, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.changed == nil {
		h.changed = make(chan struct{})
	}
	return len(h.waiters), h.changed
}

func (h *companionActHub) startTimer(d time.Duration) (<-chan time.Time, func()) {
	h.mu.Lock()
	fn := h.newTimer
	h.mu.Unlock()
	if fn != nil {
		return fn(d)
	}
	t := time.NewTimer(d)
	return t.C, func() {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}
}

// companionBurstHub tracks per-owner Tier-1 burst call counts strictly in memory (D11).
type companionBurstHub struct {
	mu     sync.Mutex
	counts map[string]int
}

func (b *companionBurstHub) consume(owner string, limit int) (int, bool) {
	owner = strings.TrimSpace(owner)
	if owner == "" || limit <= 0 {
		return 0, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.counts == nil {
		b.counts = make(map[string]int)
	}
	cur := b.counts[owner]
	if cur >= limit {
		return cur, false
	}
	cur++
	b.counts[owner] = cur
	return cur, true
}

func (b *companionBurstHub) reset(owner string) {
	owner = strings.TrimSpace(owner)
	b.mu.Lock()
	if b.counts != nil {
		if owner == "" {
			clear(b.counts)
		} else {
			delete(b.counts, owner)
		}
	}
	b.mu.Unlock()
}

func (d *Daemon) ensureCompanionActWired() {
	d.companionActOnce.Do(func() {
		d.companionPhone.SetOnUserMessage(d.companionAct.notifyUserMessage)
	})
}

// SetCompanionActTimer installs a deterministic timer factory for
// ws/companion.act (test seam; nil restores time.NewTimer).
func (d *Daemon) SetCompanionActTimer(fn func(d time.Duration) (<-chan time.Time, func())) {
	d.companionAct.mu.Lock()
	d.companionAct.newTimer = fn
	d.companionAct.mu.Unlock()
}

// CompanionActWaiterCount reports the number of currently blocked
// ws/companion.act calls (for deterministic synchronization in tests).
func (d *Daemon) CompanionActWaiterCount() int {
	return d.companionAct.waiterCount()
}

// CompanionActWaiterWatch returns the current blocked act waiter count along
// with a channel closed on the next register or unregister transition (for
// zero-sleep deterministic test synchronization).
func (d *Daemon) CompanionActWaiterWatch() (int, <-chan struct{}) {
	return d.companionAct.waiterWatch()
}

// CompanionConsumeBurstCall records one counted Tier-1 burst tool call for
// owner, returning (count, false) once limit is reached (D11).
func (d *Daemon) CompanionConsumeBurstCall(owner string, limit int) (int, bool) {
	return d.companionBurst.consume(owner, limit)
}

// CompanionResetBurst resets the per-turn burst counter for owner (or all
// owners when owner is empty).
func (d *Daemon) CompanionResetBurst(owner string) {
	d.companionBurst.reset(owner)
}

// CompanionBurstCount reports the current in-memory Tier-1 burst call count for
// owner (read-only, zero I/O).
func (d *Daemon) CompanionBurstCount(owner string) int {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return 0
	}
	d.companionBurst.mu.Lock()
	defer d.companionBurst.mu.Unlock()
	return d.companionBurst.counts[owner]
}

// CompanionAct broadcasts a companion-command beat immediately and performs an
// interruptible select wait across animation delay timer, incoming user
// messenger message, glass-knock event, and context/daemon cancellation (D7, D10).
func (d *Daemon) CompanionAct(ctx context.Context, owner string, req CompanionActRequest) (CompanionActResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	d.ensureCompanionActWired()

	delayMS := ClampCompanionActDelayMS(req.DelayMS, req.HasDelay)
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = companionPhoneBroadcastSession
	}
	if owner = strings.TrimSpace(owner); owner != "" {
		d.companionPhone.RenewLease(owner)
	}

	// Register waiter BEFORE publishing command so any concurrent user message
	// or glass knock arriving while the beat is live is captured without loss.
	waiter := d.companionAct.register()
	defer d.companionAct.unregister(waiter)

	d.PublishCompanionCommand(CompanionCommand{
		SessionID:  sessionID,
		Expression: strings.TrimSpace(req.Expression),
		Gesture:    strings.TrimSpace(req.Gesture),
		Prop:       strings.TrimSpace(req.Prop),
		FX:         strings.TrimSpace(req.FX),
		Quip:       strings.TrimSpace(req.Speech),
		TTLms:      delayMS,
	})

	timerC, stopTimer := d.companionAct.startTimer(time.Duration(delayMS) * time.Millisecond)
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			return CompanionActResult{
				WakeReason: CompanionActWakeCancelled,
				DelayMS:    delayMS,
			}, nil
		case <-d.stop:
			return CompanionActResult{
				WakeReason: CompanionActWakeCancelled,
				DelayMS:    delayMS,
			}, nil
		case <-timerC:
			return CompanionActResult{
				WakeReason: CompanionActWakeTimer,
				DelayMS:    delayMS,
			}, nil
		case <-waiter.msgCh:
			msgs := d.companionPhone.ConsumeUnreadForAct()
			if len(msgs) == 0 {
				continue
			}
			return CompanionActResult{
				WakeReason:   CompanionActWakeUserMessage,
				DelayMS:      delayMS,
				UserMessages: msgs,
			}, nil
		case ev := <-waiter.knockCh:
			cp := ev
			return CompanionActResult{
				WakeReason: CompanionActWakeGlassKnock,
				DelayMS:    delayMS,
				Knock:      &cp,
			}, nil
		}
	}
}

// CompanionRememberSoul writes a portable personal/rapport memory entry through
// Phase 1's 2-layer scrubber (saved / held_pending / downgraded_episode /
// rejected_secret). Isolated from ws/note.*.
func (d *Daemon) CompanionRememberSoul(ctx context.Context, owner, content, kind, projectKey string, tags []string) (RememberSoulResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return RememberSoulResult{}, errors.New("content is required")
	}
	if owner = strings.TrimSpace(owner); owner != "" {
		d.companionPhone.RenewLease(owner)
	}
	cfg := d.companion.current().config
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" {
		projectKey = companionProjectKeyFromRoot(d.CompanionActiveRoot())
	}
	return d.companionMemoryStore().RememberSoul(ctx, RememberSoulInput{
		SoulDir:        cfg.SoulDir,
		SoulID:         cfg.Soul,
		Kind:           strings.TrimSpace(kind),
		Content:        content,
		Tags:           tags,
		ActiveProjects: d.activeProjectNames(),
		ProjectKey:     projectKey,
		Source:         "companion.remember_soul",
	})
}

// CompanionRememberEpisode stores a machine-local project episode in SQLite
// FTS5. Isolated from Portable Soul and ws/note.*.
func (d *Daemon) CompanionRememberEpisode(ctx context.Context, owner, projectKey, summary string, tags []string) (CompanionEpisode, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return CompanionEpisode{}, errors.New("summary is required")
	}
	if owner = strings.TrimSpace(owner); owner != "" {
		d.companionPhone.RenewLease(owner)
	}
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" {
		projectKey = companionProjectKeyFromRoot(d.CompanionActiveRoot())
	}
	ep, err := d.companionMemoryStore().Episodes()
	if err != nil {
		return CompanionEpisode{}, err
	}
	return ep.AddEpisode(ctx, CompanionEpisodeInput{
		ProjectKey: projectKey,
		Summary:    summary,
		Tags:       tags,
		Source:     "companion.remember_episode",
	})
}

// CompanionRecallMemory searches Portable Soul shards (RAM cache) and/or Local
// Episodes (SQLite FTS5), strictly isolated from ws/note.*.
func (d *Daemon) CompanionRecallMemory(ctx context.Context, query, scope, projectKey string, limit int) (CompanionRecallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "all", "soul", "episodes":
	default:
		return CompanionRecallResult{}, errors.New("scope must be all, soul, or episodes")
	}
	if limit <= 0 {
		limit = companionRecallDefaultLimit
	}
	if limit > companionRecallMaxLimit {
		limit = companionRecallMaxLimit
	}

	cfg := d.companion.current().config
	soulID, err := ResolveSoulID(cfg.Soul)
	if err != nil {
		soulID = companionDefaultSoulID
	}
	query = strings.TrimSpace(query)
	projectKey = strings.TrimSpace(projectKey)
	epFilterProject := projectKey
	if epFilterProject == "*" {
		epFilterProject = ""
	}

	var soulSnips []CompanionRecallSnippet
	if scope == "all" || scope == "soul" {
		if entries, ok, err := d.companionMemoryStore().Soul().SearchCached(cfg.SoulDir, soulID, query, limit); err == nil && ok {
			soulSnips = soulEntriesToRecallSnippets(entries)
		}
	}

	var epSnips []CompanionRecallSnippet
	if scope == "all" || scope == "episodes" {
		if ep, ok, err := d.companionMemoryStore().EpisodesIfPresent(); err == nil && ok {
			if rows, err := ep.SearchEpisodes(ctx, CompanionEpisodeQuery{
				ProjectKey: epFilterProject,
				Query:      query,
				Limit:      limit,
			}); err == nil {
				epSnips = episodesToRecallSnippets(rows)
			}
		} else if err != nil && !errors.Is(err, errCompanionEpisodesClosed) {
			return CompanionRecallResult{}, err
		}
	}

	var combined []CompanionRecallSnippet
	switch scope {
	case "soul":
		combined = soulSnips
	case "episodes":
		combined = epSnips
	default:
		combined = interleaveRecallSnippets(epSnips, soulSnips, limit)
	}
	if combined == nil {
		combined = []CompanionRecallSnippet{}
	}
	return CompanionRecallResult{
		SoulID:     soulID,
		Scope:      scope,
		ProjectKey: projectKey,
		Query:      query,
		Snippets:   combined,
	}, nil
}

func soulEntriesToRecallSnippets(entries []SoulShardEntry) []CompanionRecallSnippet {
	if len(entries) == 0 {
		return nil
	}
	out := make([]CompanionRecallSnippet, 0, len(entries))
	for _, entry := range entries {
		out = append(out, CompanionRecallSnippet{
			Source:    "soul:" + companionOr(entry.Kind, "note"),
			Timestamp: entry.Timestamp,
			Content:   entry.Content,
			Tags:      append([]string(nil), entry.Tags...),
		})
	}
	return out
}

func episodesToRecallSnippets(episodes []CompanionEpisode) []CompanionRecallSnippet {
	if len(episodes) == 0 {
		return nil
	}
	out := make([]CompanionRecallSnippet, 0, len(episodes))
	for _, ep := range episodes {
		src := "episode"
		if pk := strings.TrimSpace(ep.ProjectKey); pk != "" {
			src = "episode:" + pk
		}
		out = append(out, CompanionRecallSnippet{
			Source:    src,
			Timestamp: ep.CreatedAt,
			Content:   ep.Summary,
			Tags:      append([]string(nil), ep.Tags...),
		})
	}
	return out
}

func interleaveRecallSnippets(primary, secondary []CompanionRecallSnippet, limit int) []CompanionRecallSnippet {
	if limit <= 0 {
		return nil
	}
	out := make([]CompanionRecallSnippet, 0, min(limit, len(primary)+len(secondary)))
	i, j := 0, 0
	for len(out) < limit && (i < len(primary) || j < len(secondary)) {
		if i < len(primary) {
			out = append(out, primary[i])
			i++
		}
		if len(out) < limit && j < len(secondary) {
			out = append(out, secondary[j])
			j++
		}
	}
	return out
}

func companionProjectKeyFromRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(root))
	if base == "." || base == "/" || base == `\` {
		return ""
	}
	return base
}

// --- HTTP route: POST /api/companion/knock (D10) -----------------------------

func (d *Daemon) registerCompanionKnockRoute(mux *http.ServeMux) {
	mux.HandleFunc(companionKnockRoute, d.handleCompanionKnock)
}

func normalizeCompanionKnockKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "annoyed_tap":
		return "annoyed_tap"
	case "wakeup_jolt":
		return "wakeup_jolt"
	default:
		return "knock_startle"
	}
}

func (d *Daemon) handleCompanionKnock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}

	var body struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, companionKnockBodyLimit)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid companion knock payload", http.StatusBadRequest)
			return
		}
	}
	kind := normalizeCompanionKnockKind(body.Kind)
	count := body.Count
	if count <= 0 {
		count = 1
	} else if count > companionKnockMaxCount {
		count = companionKnockMaxCount
	}

	ev := CompanionKnockEvent{
		Kind:      kind,
		Count:     count,
		Timestamp: time.Now().UnixMilli(),
	}
	// Update in-memory mood state and fan out strictly to blocked act waiters.
	// Never opens messenger, never mutates phone inbox, never wakes idle Tier-1 (D10).
	d.ObserveCompanionMood(CompanionMoodEventKnock)
	woken := d.companionAct.notifyKnock(ev)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"woken": woken,
		"event": ev,
	})
}
