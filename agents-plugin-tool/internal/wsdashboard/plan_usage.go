package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

const planUsageCache = "plan-usage.json"

// These values alone cross the viewer boundary and the private cache. Never
// retain auth documents, account identifiers, subprocess output, or errors here.
type planUsageWindow struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt int64   `json:"resetsAt,omitempty"`
	AsOf     int64   `json:"asOf"`
	Disabled bool    `json:"disabled,omitempty"`
}
// Retained name check: the "werk" JSON member on planUsageEntry is part of the
// /rail/usage wire contract
// (TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe).
type planUsageEntry struct {
	Harness      string               `json:"harness"`
	Group        string               `json:"group,omitempty"`
	Label        string               `json:"label,omitempty"`
	AccountLabel string               `json:"accountLabel,omitempty"`
	Pending      bool                 `json:"pending,omitempty"`
	Plan         string               `json:"plan,omitempty"`
	Source       string               `json:"source,omitempty"`
	Status       string               `json:"status,omitempty"`
	AsOf         int64                `json:"asOf,omitempty"`
	Windows      []planUsageWindow    `json:"windows"`
	OpenRouter   *openRouterUsageView `json:"openrouter,omitempty"`
	Werk         *werkUsageView       `json:"werk,omitempty"`
}

type slotKey struct {
	harness string
	slotID  string
}

// planUsageSlotPreview is the additive /rail/usage?history=1 slotPreviews
// payload for one inactive slot's cached usage and drawable history samples.
type planUsageSlotPreview struct {
	AsOf    int64             `json:"asOf,omitempty"`
	Windows []planUsageWindow `json:"windows"`
	Samples []planUsageSample `json:"samples"`
}
type planUsageStore struct {
	mu          sync.Mutex
	queryMu     sync.Mutex
	dir, home    string
	explicitHome string
	accountSlots *wscred.AccountSlotRegistry
	meta         *wsmeta.Store
	getenv       func(string) string
	claude, gpt planUsageEntry
	lastQuery   time.Time
	// lastSaved holds the last successfully written cache bytes (legacy JSON),
	// and lastClaudeJSON/lastGPTJSON track per-key SQLite writes so only the
	// changed entry is updated. Guarded by mu.
	lastSaved      []byte
	lastClaudeJSON []byte
	lastGPTJSON    []byte
	// probing marks the one background probe in flight; probes lets tests and
	// daemon shutdown wait for it. closed and lifetimeCtx/lifetimeCancel tie
	// background probes to the owning daemon's lifetime. Guarded by mu.
	probing        bool
	probes         sync.WaitGroup
	lifetimeCtx    context.Context
	lifetimeCancel context.CancelFunc
	closed         bool
	hist           planUsageHistoryMemo
	codexHist      planUsageHistoryMemo
	// agy state is kept separate from Claude/Codex so plan-usage.json and
	// plan-usage-history.json stay byte-identical (T0). All fields below
	// except resolveAgy, agyDebounce, agyTimeout, agyEnv, and now are guarded by mu.
	resolveAgy func() (string, []string, error)
	// harnesses declares which harnesses push usage and keep a separate
	// five-hour history (DaemonOptions.Harnesses); set before use, read-only.
	harnesses      HarnessTraitSet
	agyGroups      []planUsageEntry
	agyLastSaved   []byte
	agyLastProbe   time.Time
	agyProbing     bool
	agyProbed      bool
	agyAuthHold    bool
	agyBilledLatch bool
	agyStatus      string
	agyHist        planUsageHistoryMemo
	agyDebounce    time.Duration
	agyTimeout     time.Duration
	agyEnv         []string
	agyProbeMu     sync.Mutex
	onAgyChange    func()
	now            func() time.Time
	// Request dependencies are private test seams, not configuration knobs.
	// Production always uses the fixed HTTPS origin below.
	usageURL string
	client   *http.Client
	// File-system seams for the history memo and detection reads; nil means
	// os.Stat/os.ReadFile.
	statFile func(string) (os.FileInfo, error)
	readFile func(string) ([]byte, error)
	// Write seam for all usage files; nil means writePrivateFile.
	writeFile func(dir, name string, data []byte) error
	// detected memoizes detect() by the credential files' stats. Guarded by mu.
	detected      usageDetectMemo
	gptSlotID     string
	claudeSlotID  string
	slotSnapshots map[slotKey]planUsageEntry
	lastSlotJSON  map[slotKey][]byte
	// Werk per-model usage aggregates (RAM cache + debounced wsmeta WAL deltas).
	// Guarded by mu.
	werkUsage         map[wsmeta.WerkModelUsageKey]wsmeta.WerkModelUsage
	werkPending       map[wsmeta.WerkModelUsageKey]wsmeta.WerkModelUsage
	werkFlushTimer    *time.Timer
	werkFlushDebounce time.Duration
	// Optional credential store (*wscred.Store in production; fake in tests) for
	// OpenRouter and OpenAI SIWC usage probes. Set before use, read-only.
	credentials any
	// OpenRouter /key + /credits probe cache and 10m debounce. Guarded by mu.
	openRouterSnap      *wscred.OpenRouterUsageSnapshot
	openRouterStatus    string
	openRouterLastProbe time.Time
	openRouterProbing   bool
	openRouterGen       uint64
	openRouterDebounce  time.Duration
	// Werk OpenAI SIWC conservative wham/usage probe cache and 10m debounce.
	// Guarded by mu.
	werkOpenAIClientID  string
	werkOpenAIProbe     *wscred.OpenAIPlanUsageResult
	werkOpenAILastProbe time.Time
	werkOpenAIProbing   bool
	werkOpenAIDebounce  time.Duration
}

func clonePlanUsageEntry(e planUsageEntry) planUsageEntry {
	if len(e.Windows) > 0 {
		e.Windows = append([]planUsageWindow(nil), e.Windows...)
	}
	return e
}

func slotStorageKey(k slotKey) string {
	return k.harness + ":" + k.slotID
}

func (s *planUsageStore) classifyActiveSlotsLocked(active map[string]string) (claudeHarness, claudeActive, gptHarness, gptActive string) {
	if len(active) == 0 {
		return "", "", "", ""
	}
	harnesses := make([]string, 0, len(active))
	for h := range active {
		harnesses = append(harnesses, h)
	}
	sort.Strings(harnesses)
	for _, h := range harnesses {
		id := active[h]
		if s.harnesses.pushesClaudeUsage(h) {
			claudeHarness, claudeActive = h, id
		} else if s.harnesses.separateFiveHour(h) {
			gptHarness, gptActive = h, id
		}
	}
	if claudeActive == "" && s.claude.Harness != "" {
		if id, ok := active[s.claude.Harness]; ok && s.claude.Harness != gptHarness {
			claudeHarness, claudeActive = s.claude.Harness, id
		}
	}
	if gptActive == "" && s.gpt.Harness != "" {
		if id, ok := active[s.gpt.Harness]; ok && s.gpt.Harness != claudeHarness {
			gptHarness, gptActive = s.gpt.Harness, id
		}
	}
	if gptActive == "" {
		for _, h := range harnesses {
			if _, registered := s.harnesses[h]; registered && !s.harnesses.separateFiveHour(h) {
				continue
			}
			if !s.harnesses.pushesClaudeUsage(h) && h != claudeHarness && slices.Contains(wscred.SlotLoginHarnesses(), h) {
				gptHarness, gptActive = h, active[h]
				break
			}
		}
	}
	return claudeHarness, claudeActive, gptHarness, gptActive
}

// seedSlots initializes claudeSlotID and gptSlotID from the slot registry after
// harnesses traits have been wired, so post-restart stashes and history stamps
// target the real active slot rather than assuming "default".
func (s *planUsageStore) seedSlots() {
	if s == nil || s.accountSlots == nil {
		return
	}
	active := s.accountSlots.ActiveSlots()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, claudeActive, _, gptActive := s.classifyActiveSlotsLocked(active)
	if claudeActive != "" && s.claudeSlotID == "" {
		s.claudeSlotID = claudeActive
	}
	if gptActive != "" && s.gptSlotID == "" {
		s.gptSlotID = gptActive
	}
}

func (s *planUsageStore) stashSlotSnapshotLocked(k slotKey, entry planUsageEntry) {
	if k.harness == "" || k.slotID == "" {
		return
	}
	if entry.AsOf == 0 && len(entry.Windows) == 0 {
		return
	}
	if s.slotSnapshots == nil {
		s.slotSnapshots = map[slotKey]planUsageEntry{}
	}
	cloned := clonePlanUsageEntry(entry)
	s.slotSnapshots[k] = cloned
	if s.meta == nil {
		return
	}
	raw, err := json.Marshal(cloned)
	if err != nil {
		return
	}
	if s.lastSlotJSON == nil {
		s.lastSlotJSON = map[slotKey][]byte{}
	}
	if bytes.Equal(raw, s.lastSlotJSON[k]) {
		return
	}
	if s.meta.PutUsageLatestEntry(context.Background(), wsmeta.UsageSourcePlan, slotStorageKey(k), raw) == nil {
		s.lastSlotJSON[k] = raw
	}
}

func (s *planUsageStore) activeSlotForHarnessLocked(h string) string {
	if s.harnesses.pushesClaudeUsage(h) {
		return s.claudeSlotID
	}
	if s.harnesses.separateFiveHour(h) {
		return s.gptSlotID
	}
	if s.claude.Harness == h && s.claudeSlotID != "" {
		return s.claudeSlotID
	}
	if s.gpt.Harness == h && s.gptSlotID != "" {
		return s.gptSlotID
	}
	return ""
}

// invalidateSlots drops the memoized credential detection, stashes outgoing
// active-slot snapshots, restores any cached snapshot for the incoming slot,
// and prunes snapshots of removed slots (D13).
func (s *planUsageStore) invalidateSlots() {
	if s == nil {
		return
	}
	var active map[string]string
	var validSlots map[string]map[string]bool
	if s.accountSlots != nil {
		active = s.accountSlots.ActiveSlots()
		validSlots = make(map[string]map[string]bool, len(active))
		for h := range active {
			views := s.accountSlots.Slots(h)
			set := make(map[string]bool, len(views))
			for _, v := range views {
				set[v.ID] = true
			}
			validSlots[h] = set
		}
	}
	slotValid := func(harness, slotID string) bool {
		if harness == "" || slotID == "" {
			return false
		}
		if len(validSlots) == 0 {
			return true
		}
		known, managed := validSlots[harness]
		return !managed || known[slotID]
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	claudeHarness, claudeActive, gptHarness, gptActive := s.classifyActiveSlotsLocked(active)
	s.detected = usageDetectMemo{}
	s.openRouterLastProbe = time.Time{}
	s.resetWerkOpenAILocked()
	switched := false
	if claudeActive != "" {
		prev := s.claudeSlotID
		if prev == "" {
			prev = wscred.DefaultAccountSlotID
		}
		if claudeActive != prev {
			if slotValid(s.claude.Harness, prev) {
				s.stashSlotSnapshotLocked(slotKey{harness: s.claude.Harness, slotID: prev}, s.claude)
			}
			if restored, ok := s.slotSnapshots[slotKey{harness: claudeHarness, slotID: claudeActive}]; ok {
				s.claude = clonePlanUsageEntry(restored)
			} else {
				s.claude = planUsageEntry{}
			}
			switched = true
		}
		s.claudeSlotID = claudeActive
	}
	if gptActive != "" {
		prev := s.gptSlotID
		if prev == "" {
			prev = wscred.DefaultAccountSlotID
		}
		if gptActive != prev {
			if slotValid(s.gpt.Harness, prev) {
				s.stashSlotSnapshotLocked(slotKey{harness: s.gpt.Harness, slotID: prev}, s.gpt)
			}
			if restored, ok := s.slotSnapshots[slotKey{harness: gptHarness, slotID: gptActive}]; ok {
				s.gpt = clonePlanUsageEntry(restored)
			} else {
				s.gpt = planUsageEntry{}
			}
			s.lastQuery = time.Time{}
			switched = true
		}
		s.gptSlotID = gptActive
	}
	if len(validSlots) > 0 && len(s.slotSnapshots) > 0 {
		for k := range s.slotSnapshots {
			if known, managed := validSlots[k.harness]; managed && !known[k.slotID] {
				delete(s.slotSnapshots, k)
				delete(s.lastSlotJSON, k)
				if s.meta != nil {
					_ = s.meta.DeleteUsageLatestEntry(context.Background(), wsmeta.UsageSourcePlan, slotStorageKey(k))
				}
			}
		}
	}
	if switched {
		s.saveLocked()
	}
}

// usageCredStat identifies one credential file version. Only the detected
// plan/mode/presence is memoized, never file contents.
type usageCredStat struct {
	path    string
	missing bool
	size    int64
	modTime int64
}
type usageDetectMemo struct {
	ok              bool
	stats           [2]usageCredStat
	plan            string
	codex           bool
	codexMatchToken string
}

func (s *planUsageStore) write(name string, data []byte) error {
	if s.writeFile != nil {
		return s.writeFile(s.dir, name, data)
	}
	return writePrivateFile(s.dir, name, data)
}

func newPlanUsageStore(dir, home string, getenv func(string) string) *planUsageStore {
	s := newPlanUsageStoreBase(dir, home, getenv)
	var saved struct {
		// Retained name check: the plan-usage file's member names are persisted bytes
		// (TestPlanUsageHistoryValidatorsKeepPersistedNames).
		Claude planUsageEntry `json:"claude"`
		GPT    planUsageEntry `json:"gpt"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, planUsageCache)); err == nil && json.Unmarshal(raw, &saved) == nil {
		s.claude, s.gpt = saved.Claude, saved.GPT
	}
	s.loadAgyLocked()
	return s
}

func newPlanUsageStoreBase(dir, home string, getenv func(string) string) *planUsageStore {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &planUsageStore{
		dir:            dir,
		home:           home,
		getenv:         getenv,
		lifetimeCtx:    lifeCtx,
		lifetimeCancel: lifeCancel,
		usageURL:       "https://chatgpt.com/backend-api/wham/usage",
		client:         &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// newPlanUsageStoreFromMeta initializes the usage store from the startup
// SQLite snapshot without reading legacy plan-usage*.json files.
func newPlanUsageStoreFromMeta(dir, home string, getenv func(string) string, store *wsmeta.Store, snap *wsmeta.Snapshot) *planUsageStore {
	s := newPlanUsageStoreBase(dir, home, getenv)
	s.meta = store
	s.loadFromMetaLocked(snap)
	return s
}

func (s *planUsageStore) loadFromMetaLocked(snap *wsmeta.Snapshot) {
	if snap != nil {
		if latest, ok := snap.LatestUsage[wsmeta.UsageSourcePlan]; ok {
			// Retained name check: the plan-usage SQLite entry key "claude"
			// preserves the persisted plan-usage.json member name
			// (TestPlanUsageHistoryValidatorsKeepPersistedNames).
			for _, e := range latest.Entries {
				switch e.Key {
				case "claude":
					var c planUsageEntry
					if json.Unmarshal(e.Body, &c) == nil {
						s.claude = c
					}
				case "gpt":
					var g planUsageEntry
					if json.Unmarshal(e.Body, &g) == nil {
						s.gpt = g
					}
				default:
					if provider, slotID, ok := strings.Cut(e.Key, ":"); ok && provider != "" && slotID != "" {
						var entry planUsageEntry
						if json.Unmarshal(e.Body, &entry) == nil {
							if s.slotSnapshots == nil {
								s.slotSnapshots = map[slotKey]planUsageEntry{}
							}
							if s.lastSlotJSON == nil {
								s.lastSlotJSON = map[slotKey][]byte{}
							}
							k := slotKey{harness: provider, slotID: slotID}
							s.slotSnapshots[k] = entry
							if raw, err := json.Marshal(entry); err == nil {
								s.lastSlotJSON[k] = raw
							}
						}
					}
				}
			}
		}
		if agyLatest, ok := snap.LatestUsage[wsmeta.UsageSourceAgy]; ok {
			rawGroups := make([]planUsageEntry, 0, len(agyLatest.Entries))
			for _, e := range agyLatest.Entries {
				var g planUsageEntry
				if json.Unmarshal(e.Body, &g) == nil {
					rawGroups = append(rawGroups, g)
				}
			}
			if groups := sanitizeAgyGroups(rawGroups); len(groups) > 0 {
				s.agyGroups = groups
				if canonical, err := json.Marshal(planUsageAgyFile{SchemaVersion: 1, Groups: groups}); err == nil {
					s.agyLastSaved = canonical
				}
			}
		}
	}
	s.lastClaudeJSON, _ = json.Marshal(s.claude)
	s.lastGPTJSON, _ = json.Marshal(s.gpt)
	s.loadHistoryFromMetaLocked(&s.hist, defaultHistorySpec, snap)
	s.loadHistoryFromMetaLocked(&s.codexHist, codexFiveHourHistorySpec, snap)
	s.loadHistoryFromMetaLocked(&s.agyHist, agyHistorySpec, snap)
	s.loadWerkUsageFromMetaLocked(snap)
}

// beginClose marks the store closed and cancels its daemon-lifetime context so
// no new probe starts, in-flight probes terminate, and no further writes occur.
func (s *planUsageStore) beginClose() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.stopWerkFlushLocked()
	cancel := s.lifetimeCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitClose waits for in-flight background probes until they finish or ctx
// expires. Call beginClose first so no new probe can join probes.
func (s *planUsageStore) waitClose(ctx context.Context) {
	if s == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		s.probes.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	_ = s.flushWerkUsage(ctx)
}

func (s *planUsageStore) close(ctx context.Context) {
	s.beginClose()
	s.waitClose(ctx)
}

func (s *planUsageStore) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}
func (s *planUsageStore) saveLocked() {
	if s.meta != nil {
		if raw, err := json.Marshal(s.claude); err == nil && !bytes.Equal(raw, s.lastClaudeJSON) {
			if s.meta.PutUsageLatestEntry(context.Background(), wsmeta.UsageSourcePlan, "claude", raw) == nil {
				s.lastClaudeJSON = raw
			}
		}
		if raw, err := json.Marshal(s.gpt); err == nil && !bytes.Equal(raw, s.lastGPTJSON) {
			if s.meta.PutUsageLatestEntry(context.Background(), wsmeta.UsageSourcePlan, "gpt", raw) == nil {
				s.lastGPTJSON = raw
			}
		}
		return
	}
	// Retained name check: the plan-usage file's claude member is persisted bytes
	// (TestPlanUsageHistoryValidatorsKeepPersistedNames).
	raw, err := json.Marshal(struct {
		Claude planUsageEntry `json:"claude"`
		GPT    planUsageEntry `json:"gpt"`
	}{s.claude, s.gpt})
	if err == nil && !bytes.Equal(raw, s.lastSaved) {
		if s.write(planUsageCache, raw) == nil {
			s.lastSaved = raw
		}
	}
}
func validUsage(percent float64) bool {
	return !math.IsNaN(percent) && !math.IsInf(percent, 0) && percent >= 0 && percent <= 100
}

func (s *planUsageStore) pushClaude(snap *wslive.Snapshot) {
	// The push is the one pushed-usage provider's persisted shape (the
	// "claude" cache entry and the "Claude push" history source), kept under
	// its own name (1881); which snapshots may feed it is the harness's trait.
	if s == nil || snap == nil || !s.harnesses.pushesClaudeUsage(snap.Adapter) || snap.Usage == nil {
		return
	}
	// Legacy snapshots can contain limits without an observation time.
	// UpdatedAt tracks snapshot activity, including heartbeats for old limits.
	observedAt := snap.Usage.LimitsAsOf
	if observedAt <= 0 || len(snap.Usage.Limits) == 0 {
		return
	}
	// Invalid figures cannot safely establish which windows are absent.
	for _, v := range snap.Usage.Limits {
		if (v.Kind != wslive.LimitFiveHour && v.Kind != wslive.LimitSevenDay) || !validUsage(v.Percent) {
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || observedAt < s.claude.AsOf {
		return
	}
	// Sessions disagree near a window boundary: a stale one still reports the
	// expired five-hour window while a fresh one reports none. Taking either
	// as-is made the window flap on every refresh, and each flap rewrote the
	// whole history file. So an expired reported window is ignored, and a
	// report without a kind ends only a window that has already expired. Live
	// matches currentUsage: an unknown (zero) reset never expires.
	now := time.Now().UnixMilli()
	live := func(reset int64) bool { return reset == 0 || reset > now }
	changed := false
	present := make(map[string]bool, len(snap.Usage.Limits))
	for _, v := range snap.Usage.Limits {
		present[v.Kind] = true
	}
	kept := s.claude.Windows[:0]
	for _, old := range s.claude.Windows {
		if present[old.Kind] || live(old.ResetsAt) {
			kept = append(kept, old)
		} else {
			changed = true
		}
	}
	s.claude.Windows = kept
	var samples []planUsageSample
	for _, v := range snap.Usage.Limits {
		if !live(v.ResetsAt) {
			continue
		}
		next := planUsageWindow{Kind: v.Kind, Percent: v.Percent, ResetsAt: v.ResetsAt, AsOf: observedAt}
		i := 0
		for i < len(s.claude.Windows) && s.claude.Windows[i].Kind != v.Kind {
			i++
		}
		if i < len(s.claude.Windows) {
			old := s.claude.Windows[i]
			if next.AsOf < old.AsOf {
				continue
			}
			// Equal-time duplicate reports keep the old monotonic tie-breaker.
			// A strictly newer observation is authoritative even when an account
			// switch makes its reset earlier or its percentage lower.
			if next.AsOf == old.AsOf && (next.ResetsAt < old.ResetsAt || (next.ResetsAt == old.ResetsAt && next.Percent <= old.Percent)) {
				continue
			}
			s.claude.Windows[i] = next
		} else {
			s.claude.Windows = append(s.claude.Windows, next)
		}
		changed = true
		// Retained name check: the persisted history harness and source labels
		// (TestPlanUsageHistoryValidatorsKeepPersistedNames).
		samples = append(samples, planUsageSample{Harness: "claude", Kind: next.Kind, Source: "Claude push", SlotID: s.claudeSlotID, ResetsAt: next.ResetsAt, AsOf: next.AsOf, Percent: next.Percent})
		if next.AsOf > s.claude.AsOf {
			s.claude.AsOf = next.AsOf
		}
	}
	if changed {
		if observedAt > s.claude.AsOf {
			s.claude.AsOf = observedAt
		}
		s.claude.Harness = "claude"
		s.claude.Source = "Claude push"
		s.saveLocked()
		s.appendHistoryLocked(samples, now)
	}
}

func usageReset(raw json.RawMessage) int64 {
	text := strings.Trim(string(raw), "\"")
	if n, err := strconv.ParseFloat(text, 64); err == nil && n > 0 && !math.IsInf(n, 0) {
		if n < 1e12 {
			n *= 1000
		}
		return int64(n)
	}
	if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return t.UnixMilli()
	}
	return 0
}
func readUsageJSON(path string, out any) bool {
	raw, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(raw, out) == nil
}
func (s *planUsageStore) configHome(key, fallback string) string {
	if harness := wscred.HarnessForConfigEnv(key); harness != "" {
		var slotDir string
		if s.accountSlots != nil {
			slotDir = s.accountSlots.ActiveSlot(harness).ConfigDir
		}
		if resolved := wscred.ResolveHarnessHome(wscred.HarnessHomeOptions{
			Harness:       harness,
			ExplicitHome:  s.explicitHome,
			SlotConfigDir: slotDir,
			Getenv:        s.getenv,
			UserHomeDir:   func() (string, error) { return s.home, nil },
		}); resolved != "" {
			return resolved
		}
	}
	if s.getenv != nil {
		if p := s.getenv(key); p != "" {
			return p
		}
	}
	return filepath.Join(s.home, fallback)
}

// Detection decodes only plan/mode/presence, never credential values. Every
// usage request detects, so the result is memoized by the two files' stats
// (path, size, mtime): an unchanged set costs two stats and no reads. A
// rewrite keeping both size and mtime goes unnoticed until the next change.
// Stat-confirmed absence is cacheable; a read failure is not, so transient
// credential access failures remain retryable even without metadata changes.
func (s *planUsageStore) detect() (claudePlan string, codexLogin bool) {
	claudePlan, codexLogin, _ = s.detectWithCodexMatch()
	return claudePlan, codexLogin
}

func (s *planUsageStore) detectWithCodexMatch() (claudePlan string, codexLogin bool, codexMatchToken string) {
	paths := [2]string{
		// Retained name check: each provider's own credential location, read to learn
		// whether the provider is signed in (TestPlanUsageCodexHomeSelection).
		filepath.Join(s.configHome("CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json"),
		filepath.Join(s.configHome("CODEX_HOME", ".codex"), "auth.json"),
	}
	var stats [2]usageCredStat
	cacheable := true
	for i, path := range paths {
		stats[i].path = path
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			stats[i].missing = true
		case err != nil:
			cacheable = false
		default:
			stats[i].size, stats[i].modTime = info.Size(), info.ModTime().UnixNano()
		}
	}
	s.mu.Lock()
	m := s.detected
	s.mu.Unlock()
	if cacheable && m.ok && m.stats == stats {
		return m.plan, m.codex, m.codexMatchToken
	}
	readJSON := func(index int, out any) bool {
		if stats[index].missing {
			return false
		}
		read := s.readFile
		if read == nil {
			read = os.ReadFile
		}
		raw, err := read(paths[index])
		if err != nil {
			cacheable = false
			return false
		}
		return json.Unmarshal(raw, out) == nil
	}
	// Retained name check: the provider's credential file member, claudeAiOauth.
	var claude struct {
		OAuth struct {
			Plan string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if readJSON(0, &claude) {
		claudePlan = claude.OAuth.Plan
	}
	var codex struct {
		Mode   string `json:"auth_mode"`
		Tokens *struct {
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if readJSON(1, &codex) {
		codexLogin = codex.Mode == "chatgpt" || (codex.Mode == "" && codex.Tokens != nil)
		if codexLogin && codex.Tokens != nil {
			codexMatchToken = wscred.AccountMatchToken(codex.Tokens.Account)
		}
	}
	// Stat precedes read, so a replace in between memoizes newer content under
	// an older stat and costs only one extra read next time.
	s.mu.Lock()
	s.detected = usageDetectMemo{
		ok:              cacheable,
		stats:           stats,
		plan:            claudePlan,
		codex:           codexLogin,
		codexMatchToken: codexMatchToken,
	}
	s.mu.Unlock()
	return
}
func currentUsage(e planUsageEntry, now int64) planUsageEntry {
	e.Windows = append([]planUsageWindow{}, e.Windows...)
	live := e.Windows[:0]
	for _, w := range e.Windows {
		if validUsage(w.Percent) && (w.ResetsAt == 0 || w.ResetsAt > now) {
			live = append(live, w)
		}
	}
	e.Windows = live
	if len(live) == 0 && e.Status == "" {
		e.Status = "usage unavailable"
	}
	return e
}

// entries lists the usage providers' current entries. Each provider labels its
// entry with the persisted "harness" value its cache and history files and the
// /usage answer have always carried: the Claude push provider "claude" and the
// ChatGPT-account reader "codex" (agy labels its own in plan_usage_agy.go).
// These are persisted names, not behavior branches (1881 survivor).
func (s *planUsageStore) entries() []planUsageEntry {
	plan, codex, codexMatch := s.detectWithCodexMatch()
	werkID, hasOpenAI := s.openAIUsageIdentity()
	var orConfigured, orRejected bool
	var orMasked string
	var orGen uint64
	if orStore := s.openRouterStore(); orStore != nil {
		orConfigured, orMasked, orRejected, orGen = orStore.OpenRouterUsageMeta()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowTime().UnixMilli()
	out := []planUsageEntry{}
	if plan != "" || len(s.claude.Windows) > 0 {
		c := s.claude
		c.Harness = "claude"
		c.Plan = plan
		out = append(out, currentUsage(c, now))
	}
	if matchesCodexAndWerkOpenAI(codex, codexMatch, werkID) {
		out = append(out, s.unifiedCodexOpenAIEntryLocked(now, werkID))
	} else {
		if codex {
			x := s.gpt
			x.Harness = "codex"
			out = append(out, currentUsage(x, now))
		}
		if hasOpenAI && werkID.Configured {
			out = append(out, s.werkOpenAIEntryLocked(now, werkID))
		}
	}
	out = append(out, s.agyEntriesLocked(now)...)
	if orEntry, ok := s.openRouterEntryWithMetaLocked(orConfigured, orMasked, orRejected, orGen); ok {
		out = append(out, orEntry)
	}
	return out
}

func maxPlanReset(windows []planUsageWindow, now int64, minPercent float64) int64 {
	var best int64
	for _, w := range windows {
		if w.ResetsAt <= now {
			continue
		}
		if w.Disabled || w.Percent >= minPercent {
			if w.ResetsAt > best {
				best = w.ResetsAt
			}
		}
	}
	return best
}

// quotaResetAt returns a cached future quota reset timestamp (epoch ms) from
// daemon memory for (backend, model), or 0 when unavailable. Zero disk/network I/O.
func (s *planUsageStore) quotaResetAt(backend, model string) int64 {
	if s == nil || backend == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowTime().UnixMilli()
	if at, ok := s.agyQuotaResetAtLocked(backend, model, now); ok {
		return at
	}
	for _, entry := range []planUsageEntry{s.claude, s.gpt} {
		if entry.Harness == backend {
			return maxPlanReset(entry.Windows, now, 100)
		}
	}
	return 0
}

// slotPreviews builds the in-memory ?history=1 slotPreviews map for every
// non-active stashed slot that still has at least one live window. Zero disk
// reads or probes occur here.
func (s *planUsageStore) slotPreviews(now int64) map[string]map[string]planUsageSlotPreview {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.slotSnapshots) == 0 {
		return nil
	}
	var sharedPruned, codex5hPruned []planUsageHistoryRecord
	prunedLoaded := false
	loadPruned := func() {
		if prunedLoaded {
			return
		}
		prunedLoaded = true
		if s.hist.ok && s.hist.err == nil && len(s.hist.file.Records) > 0 {
			sharedPruned = prunePlanUsageHistory(s.hist.file.Records, now)
		}
		if s.codexHist.ok && s.codexHist.err == nil && len(s.codexHist.file.Records) > 0 {
			codex5hPruned = prunePlanUsageHistory(s.codexHist.file.Records, now)
		}
	}
	var out map[string]map[string]planUsageSlotPreview
	for k, entry := range s.slotSnapshots {
		activeID := s.activeSlotForHarnessLocked(k.harness)
		if activeID == "" || k.slotID == activeID {
			continue
		}
		cur := currentUsage(entry, now)
		if len(cur.Windows) == 0 {
			continue
		}
		cur.Harness = k.harness
		loadPruned()
		filter := map[string]string{k.harness: k.slotID}
		samples := []planUsageSample{}
		if len(sharedPruned) > 0 {
			samples = append(samples, projectPlanUsageHistoryForSlots(sharedPruned, []planUsageEntry{cur}, filter, true)...)
		}
		if s.harnesses.separateFiveHour(k.harness) && len(codex5hPruned) > 0 {
			var fiveWindows []planUsageWindow
			for _, w := range cur.Windows {
				if w.Kind == wslive.LimitFiveHour {
					fiveWindows = append(fiveWindows, w)
				}
			}
			if len(fiveWindows) > 0 {
				fiveEntry := cur
				fiveEntry.Windows = fiveWindows
				samples = append(samples, projectPlanUsageHistoryForSlots(codex5hPruned, []planUsageEntry{fiveEntry}, filter, true)...)
			}
		}
		if out == nil {
			out = map[string]map[string]planUsageSlotPreview{}
		}
		if out[k.harness] == nil {
			out[k.harness] = map[string]planUsageSlotPreview{}
		}
		out[k.harness][k.slotID] = planUsageSlotPreview{
			AsOf:    cur.AsOf,
			Windows: cur.Windows,
			Samples: samples,
		}
	}
	return out
}

// GET is viewer-authorized by the daemon's ordinary access wrapper. Pages
// request while connected; there is no polling goroutine without viewers.
func (d *Daemon) handlePlanUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if d.planUsage == nil {
		body := map[string]any{"entries": []planUsageEntry{}, "warnPercent": bandWarnPercent, "critPercent": bandCritPercent}
		if r.URL.Query().Get("history") == "1" {
			body["history"] = d.planUsage.history(time.Now().UnixMilli())
		}
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	if d.index != nil {
		for _, session := range d.index.Sessions() {
			if d.harnesses.pushesClaudeUsage(session.Harness) {
				d.planUsage.pushClaude(d.snapshots.snapshotWithoutObserved(d.dir, session.ReplyID))
			}
		}
	}
	// Cross-origin pages cannot set the custom header without a CORS preflight, which the daemon does not grant.
	refresh := r.URL.Query().Get("refresh") == "1" && (r.Header.Get("Sec-Fetch-Site") == "same-origin" || sameOrigin(r) || r.Header.Get("X-WS-Usage-Refresh") == "1")
	d.planUsage.refresh(r.Context(), refresh)
	entries := d.planUsage.entries()
	body := map[string]any{"entries": entries, "warnPercent": bandWarnPercent, "critPercent": bandCritPercent}
	if r.URL.Query().Get("history") == "1" {
		now := d.planUsage.nowTime().UnixMilli()
		body["history"] = d.planUsage.historyView(entries, now)
		if codexHist, ok := d.planUsage.codexFiveHourHistoryView(entries, now); ok {
			// Retained name check: the response member the dashboard page reads.
			body["codexFiveHourHistory"] = codexHist
		}
		d.planUsage.addAgyHistory(body, entries, now)
		if previews := d.planUsage.slotPreviews(now); len(previews) > 0 {
			body["slotPreviews"] = previews
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}
