package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

// SupportRoutePrefix is where the daemon mounts the tracker support
// conversations: one ongoing runner conversation per tracker store, opened
// from the store's chat card.
//
//	GET  /support/choices               harnesses, their models and efforts, the last choice
//	GET  /support/stores/<store>        the store's conversation (or null) and the selection
//	POST /support/stores/<store>/send   {text}; opens the conversation first when none is open
//	POST /support/stores/<store>/new    closes the open conversation and opens a fresh one
//	POST /support/stores/<store>/select {harness?, model?, effort?}
//
// Every POST follows the runner routes' rule (writeAllowed): a JSON body,
// and no Origin or the daemon's own. An unknown store is 404.
const SupportRoutePrefix = wsdashboard.SupportRoutePrefix

// supportFileName is the support state file in the dashboard state
// directory: the last selection and the store-to-conversation mapping.
const supportFileName = "support.json"

const supportSchemaVersion = 1

// choicesTTL is how long a harness's listed choices are reused.
const choicesTTL = 5 * time.Minute

// choicesFailTTL is how long a failed listing is reused, so a harness whose
// listing hangs does not hold every choices request for choicesTimeout.
const choicesFailTTL = 30 * time.Second

// choicesTimeout bounds one harness's listing.
const choicesTimeout = 15 * time.Second

// ModelChoice is one model a harness offers, as --model takes it.
type ModelChoice struct {
	ID          string `json:"id"`
	Provider    string `json:"provider,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// Thinking reports whether the model takes a reasoning effort; absent
	// when the harness does not say.
	Thinking      *bool `json:"thinking,omitempty"`
	contextWindow int64
}

// ChoiceLister is a Backend that can tell what it offers: the executable
// its availability is checked against, and its models and effort levels. A
// backend without it is available whenever registered, with no choices.
type ChoiceLister interface {
	// Executable is empty for a harness that runs inside the daemon.
	Executable() string
	// Choices lists the models and the effort levels, lowest first. A
	// listing failure still returns the efforts it knows.
	Choices(ctx context.Context) ([]ModelChoice, []string, error)
}

// observedChoiceLister preserves the original age of a persistent success.
// Failure ages still begin when the listing ends, as for any ChoiceLister.
type observedChoiceLister interface {
	choicesObserved(context.Context, func() time.Time) ([]ModelChoice, []string, time.Time, error)
}

// Selection is a harness, model, and effort choice; an empty model or
// effort is the profile's, else the harness's, default.
type Selection struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// supportState is support.json.
type supportState struct {
	SchemaVersion int `json:"schema_version"`
	// Last is the machine-wide last selection, one across stores; nil until
	// one is made.
	Last *Selection `json:"last,omitempty"`
	// Stores maps a store id to its conversation's reply-id.
	Stores map[string]string `json:"stores"`
}

// SupportOptions configures a Support.
type SupportOptions struct {
	Runner *Runner
	// Dir is the dashboard state directory support.json lives in.
	Dir string
	// StoreRoot returns a tracker store's working root (its registry
	// entry's directory, which the tracker gate pins the conversation's
	// writes to). A store that is not registered answers an error wrapping
	// ErrStoreUnregistered; any other error means the registry could not be
	// read, which a request reports as an unknown store too, but which
	// never ends a conversation (CloseUnregistered).
	StoreRoot func(id string) (string, error)
	// Config is the base of the support profiles' resolution for the
	// harness list (global and builtin layers only); zero uses the
	// runner's.
	Config wsconfig.Options
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// DeferLoad skips reading support.json during NewSupport so the daemon can
	// load from the SQLite metadata snapshot (LoadFromMeta) or fall back to
	// legacy support.json (LoadLegacy) once cutover resolves.
	DeferLoad bool
}

// ErrStoreUnregistered is the SupportOptions.StoreRoot error of a store the
// registry does not hold.
var ErrStoreUnregistered = errors.New("tracker store is not registered")

// Support keeps one ongoing support conversation per tracker store over
// the runner, and the last harness, model, and effort selection.
type Support struct {
	opts SupportOptions

	// Each store's conversation operations (find, open, send, close) run
	// under that store's lock (lockStore), so a send cannot race a new
	// conversation or a harness switch closing the one it found, while
	// another store's operations never wait on them. storesMu guards
	// storeLocks.
	storesMu   sync.Mutex
	storeLocks map[string]*sync.Mutex

	// mu guards state, meta, and saves; it is never held across a runner call.
	mu    sync.Mutex
	meta  *wsmeta.Store
	state supportState

	// choicesMu guards cache; a listing runs under it, so concurrent
	// requests share one.
	choicesMu sync.Mutex
	cache     map[string]cachedChoices

	// pickerMu guards pickerCache and pickerBusy: the picker harnesses' listings
	// run in the background, never under choicesMu, so a slow public endpoint
	// holds no other harness's choices.
	pickerMu    sync.Mutex
	pickerCache map[string]cachedChoices
	pickerBusy  map[string]bool
}

type cachedChoices struct {
	revision uint64
	at       time.Time
	models   []ModelChoice
	efforts  []string
	// err is a failed listing's message; such an entry answers for
	// choicesFailTTL only.
	err string
}

// fresh reports whether c still answers at now.
func (c cachedChoices) fresh(now time.Time) bool {
	if c.err != "" {
		return now.Sub(c.at) < choicesFailTTL
	}
	return now.Sub(c.at) < choicesTTL
}

// NewSupport returns a Support over opts, loading support.json unless
// opts.DeferLoad is set; a missing or unreadable file starts empty.
func NewSupport(opts SupportOptions) *Support {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Config == (wsconfig.Options{}) {
		opts.Config = opts.Runner.opts.Config
	}
	opts.Config.RepoRoot = ""
	s := &Support{
		opts:       opts,
		state:      supportState{Stores: map[string]string{}},
		cache:      map[string]cachedChoices{},
		storeLocks: map[string]*sync.Mutex{},
	}
	if !opts.DeferLoad {
		s.loadLegacyLocked()
	}
	return s
}

func (s *Support) loadLegacyLocked() {
	s.state = supportState{}
	raw, err := os.ReadFile(s.path())
	if err == nil {
		err = json.Unmarshal(raw, &s.state)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(s.opts.Runner.opts.Log, "support: ignore %s: %v\n", s.path(), err)
		s.state = supportState{}
	}
	if s.state.Stores == nil {
		s.state.Stores = map[string]string{}
	}
}

// LoadLegacy reads legacy support.json after a deferred start falls back off
// SQLite.
func (s *Support) LoadLegacy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = nil
	s.loadLegacyLocked()
}

// LoadFromMeta binds the daemon's SQLite metadata store and seeds state from
// snap without touching support.json.
func (s *Support) LoadFromMeta(store *wsmeta.Store, snap *wsmeta.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = store
	s.state = supportState{Stores: map[string]string{}}
	if snap == nil {
		return
	}
	if len(snap.Support.Last) > 0 {
		var last Selection
		if err := json.Unmarshal(snap.Support.Last, &last); err != nil {
			fmt.Fprintf(s.opts.Runner.opts.Log, "support: ignore sqlite last selection: %v\n", err)
		} else {
			s.state.Last = &last
		}
	}
	for storeID, convID := range snap.Support.Stores {
		s.state.Stores[storeID] = convID
	}
}

func (s *Support) setStoreMapping(store, convID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta != nil {
		if err := s.meta.SetSupportMapping(context.Background(), store, convID); err != nil {
			return err
		}
		s.state.Stores[store] = convID
		return nil
	}
	s.state.Stores[store] = convID
	return s.save()
}

func (s *Support) deleteStoreMapping(store string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta != nil {
		if err := s.meta.DeleteSupportMapping(context.Background(), store); err != nil {
			return err
		}
		delete(s.state.Stores, store)
		return nil
	}
	delete(s.state.Stores, store)
	return s.save()
}

func (s *Support) setLastSelection(sel Selection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta != nil {
		raw, err := json.Marshal(sel)
		if err != nil {
			return err
		}
		if err := s.meta.SetSupportLast(context.Background(), raw); err != nil {
			return err
		}
		cp := sel
		s.state.Last = &cp
		return nil
	}
	cp := sel
	s.state.Last = &cp
	return s.save()
}

func (s *Support) path() string { return filepath.Join(s.opts.Dir, supportFileName) }

// lockStore takes store's lock and returns its unlock.
func (s *Support) lockStore(store string) func() {
	s.storesMu.Lock()
	l := s.storeLocks[store]
	if l == nil {
		l = &sync.Mutex{}
		s.storeLocks[store] = l
	}
	s.storesMu.Unlock()
	l.Lock()
	return l.Unlock
}

// update changes state under mu and saves it.
func (s *Support) update(fn func(st *supportState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
	return s.save()
}

// save writes support.json; s.mu is held.
func (s *Support) save() error {
	s.state.SchemaVersion = supportSchemaVersion
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(), append(raw, '\n'), 0o600)
}

// SupportHarness is one support harness: a support profile's resolved
// backend.
type SupportHarness struct {
	Harness   string `json:"harness"`
	Profile   string `json:"profile"`
	Available bool   `json:"available"`
	// Error is why the harness is unavailable, or why its model listing
	// failed.
	Error           string        `json:"error,omitempty"`
	Models          []ModelChoice `json:"models"`
	Efforts         []string      `json:"efforts"`
	Lead            bool          `json:"lead,omitempty"`
	PermissionModes []string      `json:"permission_modes,omitempty"`
	ModelProviders  []string      `json:"model_providers,omitempty"`
	ModelNotice     string        `json:"model_notice,omitempty"`
	// PermissionLevels advertises lead/settings selection only, not workers.
	PermissionLevels []PermissionLevel `json:"permission_levels,omitempty"`
	// DashboardControl advertises support for per-session dashboard_control_access
	// opt-in on hosted leads (POST /agents/leads*, /agents/<id>/settings/dashboard-control).
	DashboardControl bool `json:"dashboard_control,omitempty"`
	// HarnessSwitch advertises whether this harness can participate in
	// cross-harness session switching (non-durable lead backend). Additive T0.
	HarnessSwitch bool `json:"harness_switch,omitempty"`
}

// harnesses resolves the support profiles into the harness list, in
// default order, without listing choices. Each profile is resolved through
// the global and builtin layers; one whose backend another already took is
// left out.
// Retained name check: old support selection and response notice (TestRetiredPiSupportSelectionFallsBackWithoutRewriting).
func (s *Support) harnesses() []SupportHarness {
	var out []SupportHarness
	seen := map[string]bool{}
	for _, sp := range builtinSupportProfiles() {
		h := SupportHarness{Harness: sp.backend, Profile: sp.name, Models: []ModelChoice{}, Efforts: []string{}}
		rp, err := ResolveProfile(s.opts.Config, "", sp.name, s.opts.Runner.opts.Backends)
		if err == nil {
			h.Harness = rp.Definition.Backend
		}
		if h.Harness == BackendPi || seen[h.Harness] {
			continue
		}
		seen[h.Harness] = true
		reg := s.opts.Runner.opts.Backends[h.Harness]
		h.Lead = reg.LeadProfile != ""
		h.DashboardControl = h.Lead
		h.HarnessSwitch = h.Lead && !s.opts.Runner.durableProcess(h.Harness)
		h.PermissionModes = append([]string(nil), reg.PermissionModes...)
		h.PermissionLevels = s.leadPermissionLevels(h.Harness)
		if err != nil {
			h.Error = err.Error()
		} else {
			h.Available, h.Error = supportBackendAvailability(h.Harness, reg.Backend)
		}
		out = append(out, h)
	}
	return out
}

// supportBackendAvailability checks registration and, for choice listers, the
// executable. Backends without a choice lister need only be registered.
func supportBackendAvailability(name string, backend Backend) (bool, string) {
	if backend == nil {
		return false, fmt.Sprintf("backend %q is not available in this daemon", name)
	}
	if cl, ok := backend.(ChoiceLister); ok {
		if err := choiceExecutableError(cl); err != "" {
			return false, err
		}
	}
	if lead, ok := backend.(leadProviderAdmitter); ok {
		if why := lead.leadCredentialGap(); why != "" {
			return false, why
		}
	} else if setup, ok := backend.(workerSetup); ok {
		if why := setup.workerCredentialGap(); why != "" {
			return false, why
		}
	}
	return true, ""
}

func choiceExecutableError(lister ChoiceLister) string {
	if exe := lister.Executable(); exe != "" {
		if _, err := lookPath(exe); err != nil {
			return fmt.Sprintf("%s is not installed: %v", exe, err)
		}
	}
	return ""
}

// listChoices fills each available harness's models and efforts, from the
// cache when fresh. A listing failure is that harness's error alone, and is
// cached briefly too. A listing runs apart from ctx's cancellation, bounded
// by choicesTimeout alone: a request its caller abandoned still fills the
// cache for the next one instead of leaving it to list again.
func (s *Support) listChoices(ctx context.Context, hs []SupportHarness) (pending bool) {
	s.choicesMu.Lock()
	defer s.choicesMu.Unlock()
	for i := range hs {
		h := &hs[i]
		backend := s.opts.Runner.opts.Backends[h.Harness].Backend
		if source, ok := backend.(leadProviderAdmitter); ok {
			h.ModelProviders, h.ModelNotice = source.leadProviders()
			c, ready := s.pickerListing(h.Harness, source)
			if !ready {
				pending = true
				continue
			}
			if c.models != nil {
				h.Models = c.models
			}
			h.Efforts = c.efforts
			if h.Error == "" {
				h.Error = c.err
			}
			continue
		}
		if !h.Available {
			continue
		}
		cl, ok := s.opts.Runner.opts.Backends[h.Harness].Backend.(ChoiceLister)
		if !ok {
			continue
		}
		if reported, ok := cl.(sessionChoiceLister); ok {
			if models, efforts := reported.sessionChoices(); len(models) > 0 {
				h.Models, h.Efforts, h.Error = models, efforts, ""
				continue
			}
		}
		if c, ok := s.cache[h.Harness]; ok && c.fresh(s.opts.Now()) {
			h.Models, h.Efforts, h.Error = c.models, c.efforts, c.err
			continue
		}
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), choicesTimeout)
		var models []ModelChoice
		var efforts []string
		var observed time.Time
		var err error
		if lister, ok := cl.(observedChoiceLister); ok {
			models, efforts, observed, err = lister.choicesObserved(lctx, s.opts.Now)
		} else {
			models, efforts, err = cl.Choices(lctx)
		}
		cancel()
		// Stamped when the listing ended, not when it began: a listing that
		// hung until choicesTimeout would otherwise cache an entry already
		// that old.
		now := s.opts.Now()
		if efforts != nil {
			h.Efforts = efforts
		}
		if err != nil {
			h.Error = err.Error()
			s.cache[h.Harness] = cachedChoices{at: now, models: h.Models, efforts: h.Efforts, err: h.Error}
			continue
		}
		if models != nil {
			h.Models = models
		}
		if !observed.IsZero() {
			now = observed
		}
		s.cache[h.Harness] = cachedChoices{at: now, models: h.Models, efforts: h.Efforts}
	}
	return pending
}

// findHarness returns the harness named name in hs.
func findHarness(hs []SupportHarness, name string) (SupportHarness, bool) {
	for _, h := range hs {
		if h.Harness == name {
			return h, true
		}
	}
	return SupportHarness{}, false
}

// selection is the effective selection: the last one when its harness is
// available, else the first available harness with default model and
// effort, else the empty selection.
// Retained name check: old support selection and response notice (TestRetiredPiSupportSelectionFallsBackWithoutRewriting).
func (s *Support) selection(hs []SupportHarness) (Selection, bool) {
	s.mu.Lock()
	last := s.state.Last
	s.mu.Unlock()
	if last != nil {
		if h, ok := findHarness(hs, last.Harness); ok && h.Available && h.Profile != "" {
			return *last, false
		}
	}
	retiredPi := last != nil && last.Harness == BackendPi
	for _, h := range hs {
		if h.Available && h.Profile != "" {
			return Selection{Harness: h.Harness}, retiredPi
		}
	}
	return Selection{}, retiredPi
}

var errNoHarness = &requestError{status: http.StatusServiceUnavailable, msg: "no support harness is installed and registered"}

// current returns the store's open conversation, if any: the mapped one,
// still open, at root.
func (s *Support) current(store, root string) (State, bool) {
	s.mu.Lock()
	id := s.state.Stores[store]
	s.mu.Unlock()
	if id == "" {
		return State{}, false
	}
	st, err := s.opts.Runner.Get(id)
	if err != nil || st.Status == StatusClosed || st.Root != filepath.Clean(root) {
		return State{}, false
	}
	return st, true
}

// open creates a conversation at root with sel, maps it to store, and
// saves. The store's lock is held.
func (s *Support) open(store, root string, sel Selection, hs []SupportHarness) (State, error) {
	h, ok := findHarness(hs, sel.Harness)
	if !ok || !h.Available {
		return State{}, errNoHarness
	}
	st, err := s.opts.Runner.Create(h.Profile, root)
	if err != nil {
		return State{}, err
	}
	var model, effort *string
	if sel.Model != "" {
		model = &sel.Model
	}
	if sel.Effort != "" {
		effort = &sel.Effort
	}
	if model != nil || effort != nil {
		// SetSettings answers State{} on error, so close by the created id.
		created := st.ID
		if st, err = s.opts.Runner.SetSettings(created, model, effort); err != nil {
			_, _ = s.opts.Runner.CloseConversation(created)
			return State{}, err
		}
	}
	if err := s.setStoreMapping(store, st.ID); err != nil {
		return State{}, err
	}
	return st, nil
}

// StoreAnswer is a store's support state.
// Retained name check: notice for an old stored support selection (TestRetiredPiSupportSelectionFallsBackWithoutRewriting).
type StoreAnswer struct {
	Store string `json:"store"`
	// Conversation is the store's open conversation; null when none is.
	Conversation *State    `json:"conversation"`
	Selection    Selection `json:"selection"`
	RetiredPi    bool      `json:"retired_pi,omitempty"`
}

// SelectAnswer is a select's answer.
type SelectAnswer struct {
	Conversation *State    `json:"conversation"`
	Selection    Selection `json:"selection"`
}

// ChoicesAnswer is the choices route's answer.
type ChoicesAnswer struct {
	Harnesses []SupportHarness `json:"harnesses"`
	// Last is the selection a new conversation starts with.
	Last Selection `json:"last"`
	// Tiers are the agent.tier_routing named bundles the new-session dialog
	// offers as presets, resolved by the daemon (an exact legacy alias of
	// the same name wins, as it does for agent.spawn). Selecting one only
	// fills the dialog's harness, model, and effort; the launch still sends
	// those concrete fields. TiersError says why none can be listed.
	Tiers      []SupportTier `json:"tiers"`
	TiersError string        `json:"tiers_error,omitempty"`
	// Pickers are the harnesses that list models for a picker but run no
	// support conversation and no lead (a worker-only backend that lists
	// choices): the settings model picker reads them beside Harnesses, and the
	// support and lead dialogs, which offer only what they can open, do not.
	Pickers []SupportHarness `json:"pickers,omitempty"`
	// PickersPending says a picker's model list is still being fetched in the
	// background and is left out of Pickers; asking again later returns it.
	PickersPending bool `json:"pickers_pending,omitempty"`
}

// SupportTier is one named bundle: its trace and the binding it resolves
// to, or why it does not resolve. Availability is the harness's own.
type SupportTier struct {
	Name     string `json:"name"`
	Trace    string `json:"trace,omitempty"`
	Harness  string `json:"harness,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Error    string `json:"error,omitempty"`
}

// namedTiers resolves every named bundle of the global routing graph.
func (s *Support) namedTiers() ([]SupportTier, string) {
	out := []SupportTier{}
	caller := AgentCaller{Config: s.opts.Config}
	graph, err := s.opts.Runner.tierGraph(caller)
	if err != nil {
		return out, err.Error()
	}
	if err := graph.Err(); err != nil {
		return out, err.Error()
	}
	resolver := NewTierBindingResolver(caller.Config, s.opts.Runner.opts.Backends)
	bundles := graph.Bundles()
	names := make([]string, 0, len(bundles))
	for name := range bundles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tier := SupportTier{Name: name}
		res, err := graph.Resolve(name, "")
		if err != nil {
			tier.Error = err.Error()
			out = append(out, tier)
			continue
		}
		tier.Trace, tier.Harness = res.Trace(), TierRuntimeHarness(res.Harness)
		if binding, err := resolver.Routed(res); err != nil {
			tier.Error = err.Error()
		} else if why := resolver.ProviderGap(tier.Harness, binding.Provider); why != "" {
			tier.Error = why
		} else {
			tier.Provider, tier.Model, tier.Effort = binding.Provider, binding.Model, binding.Effort
		}
		out = append(out, tier)
	}
	return out, ""
}

func (s *Support) leadPermissionLevels(backend string) []PermissionLevel {
	var levels []PermissionLevel
	for _, level := range LeadPermissionLevels() {
		if s.opts.Runner.supportsLeadPermission(backend, level) {
			levels = append(levels, level)
		}
	}
	return levels
}

// Choices lists the harnesses with their models and efforts, and the
// selection a new conversation starts with.
func (s *Support) Choices(ctx context.Context) ChoicesAnswer {
	hs := s.harnesses()
	seen := map[string]bool{}
	for _, h := range hs {
		seen[h.Harness] = true
	}
	var names []string
	for name, reg := range s.opts.Runner.opts.Backends {
		if !seen[name] && reg.LeadProfile != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		reg := s.opts.Runner.opts.Backends[name]
		h := SupportHarness{Harness: name, Lead: true, DashboardControl: true, HarnessSwitch: !s.opts.Runner.durableProcess(name), PermissionModes: append([]string(nil), reg.PermissionModes...), Models: []ModelChoice{}, Efforts: []string{}}
		h.PermissionLevels = s.leadPermissionLevels(name)
		h.Available, h.Error = supportBackendAvailability(name, reg.Backend)
		hs = append(hs, h)
	}
	leadPending := s.listChoices(ctx, hs)
	pickers, pending := s.pickers(seen)
	tiers, tiersErr := s.namedTiers()
	last, _ := s.selection(hs)
	return ChoicesAnswer{Harnesses: hs, Last: last, Tiers: tiers, TiersError: tiersErr, Pickers: pickers, PickersPending: pending || leadPending}
}

// pickers lists the registered worker-only backends that list their choices
// (no lead, no support profile), which the harnesses above leave out. A
// listing needs only the executable: the models are public, so a harness that
// still lacks a setup step (a credential) lists them and says in Error why
// it is not available yet. skip names the harnesses already listed.
//
// The listing is a remote call, so it never runs in the request: a fresh or
// stale cached list answers at once (a stale one starts a refresh), and a
// harness never listed is left out and reported pending while a background
// fetch fills the cache. A choices request for the other harnesses therefore
// never waits on it.
func (s *Support) pickers(skip map[string]bool) (out []SupportHarness, pending bool) {
	var names []string
	for name, reg := range s.opts.Runner.opts.Backends {
		if _, ok := reg.Backend.(ChoiceLister); ok && reg.WorkerCapable && reg.LeadProfile == "" && !skip[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		reg := s.opts.Runner.opts.Backends[name]
		lister := reg.Backend.(ChoiceLister)
		h := SupportHarness{Harness: name, Available: true, Models: []ModelChoice{}, Efforts: []string{}}
		if err := choiceExecutableError(lister); err != "" {
			h.Available, h.Error = false, err
		}
		c, ok := s.pickerListing(name, lister)
		if !ok {
			pending = true
			continue
		}
		if c.models != nil {
			h.Models = c.models
		}
		if c.efforts != nil {
			h.Efforts = c.efforts
		}
		if c.err != "" {
			h.Error = c.err
		}
		// The listing is done; what the harness still lacks (a credential)
		// makes it unavailable, whatever the listing said.
		if ok, why := supportBackendAvailability(name, reg.Backend); !ok {
			h.Available, h.Error = false, why
		}
		out = append(out, h)
	}
	return out, pending
}

// pickerListing returns the picker harness's cached listing and whether there
// is one; when it is missing or past its time it starts one background fetch
// (one at a time per harness), detached from any request, bounded by
// choicesTimeout.
func (s *Support) pickerListing(name string, lister ChoiceLister) (cachedChoices, bool) {
	s.pickerMu.Lock()
	defer s.pickerMu.Unlock()
	if s.pickerCache == nil {
		s.pickerCache = map[string]cachedChoices{}
		s.pickerBusy = map[string]bool{}
	}
	var revision uint64
	if source, ok := lister.(interface{ choiceRevision() uint64 }); ok {
		revision = source.choiceRevision()
	}
	c, have := s.pickerCache[name]
	have = have && c.revision == revision
	if (!have || !c.fresh(s.opts.Now())) && !s.pickerBusy[name] {
		s.pickerBusy[name] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), choicesTimeout)
			models, efforts, err := lister.Choices(ctx)
			cancel()
			entry := cachedChoices{at: s.opts.Now(), models: models, efforts: efforts, revision: revision}
			if err != nil {
				entry.err = err.Error()
			}
			s.pickerMu.Lock()
			s.pickerCache[name] = entry
			s.pickerBusy[name] = false
			s.pickerMu.Unlock()
		}()
	}
	return c, have
}

// Store reads the store's conversation and the current selection, without
// waiting on the store's conversation operations.
func (s *Support) Store(store string) (StoreAnswer, error) {
	root, err := s.root(store)
	if err != nil {
		return StoreAnswer{}, err
	}
	hs := s.harnesses()
	sel, retiredPi := s.selection(hs)
	ans := StoreAnswer{Store: store, Selection: sel, RetiredPi: retiredPi}
	if st, ok := s.current(store, root); ok {
		ans.Conversation = &st
	}
	return ans, nil
}

// Send sends text to the store's conversation, opening one with the
// current selection when none is open. It returns once the message is held
// (Runner.Send): the process starts in the conversation's loop.
func (s *Support) Send(store, text string) (State, error) {
	if strings.TrimSpace(text) == "" {
		return State{}, badRequest("text is empty")
	}
	root, err := s.root(store)
	if err != nil {
		return State{}, err
	}
	hs := s.harnesses()
	defer s.lockStore(store)()
	st, ok := s.current(store, root)
	if !ok {
		sel, _ := s.selection(hs)
		if st, err = s.open(store, root, sel, hs); err != nil {
			return State{}, err
		}
	}
	return s.opts.Runner.Send(st.ID, text)
}

// New closes the store's open conversation, if any, and opens a fresh one
// with the current selection; it sends nothing.
func (s *Support) New(store string) (State, error) {
	root, err := s.root(store)
	if err != nil {
		return State{}, err
	}
	hs := s.harnesses()
	defer s.lockStore(store)()
	if st, ok := s.current(store, root); ok {
		if _, err := s.opts.Runner.CloseConversation(st.ID); err != nil {
			return State{}, err
		}
		if err := s.deleteStoreMapping(store); err != nil {
			return State{}, err
		}
	}
	sel, _ := s.selection(hs)
	return s.open(store, root, sel, hs)
}

// CloseUnregistered closes the conversation of every mapped store that is
// no longer registered (StoreRoot answers ErrStoreUnregistered; a registry
// that could not be read closes nothing) and drops its mapping, so no agent
// keeps running in the directory an unregister deleted, and returns the
// stores it dropped. The daemon calls it after an unregister and on every
// tracker store change, which is how an unregister by another process
// arrives; with no store mapped it reads nothing. Each store is handled
// under its lock and checked again there, so a store registered again
// meanwhile keeps its conversation.
func (s *Support) CloseUnregistered() []string {
	s.mu.Lock()
	mapped := make([]string, 0, len(s.state.Stores))
	for store := range s.state.Stores {
		mapped = append(mapped, store)
	}
	s.mu.Unlock()
	var dropped []string
	for _, store := range mapped {
		if _, err := s.opts.StoreRoot(store); errors.Is(err, ErrStoreUnregistered) && s.closeUnregistered(store) {
			dropped = append(dropped, store)
		}
	}
	return dropped
}

// closeUnregistered closes and unmaps store's conversation when the store
// is still unregistered under its lock; it reports whether it dropped the
// mapping. A close or save failure is logged and leaves the mapping for the
// next call.
func (s *Support) closeUnregistered(store string) bool {
	defer s.lockStore(store)()
	if _, err := s.opts.StoreRoot(store); !errors.Is(err, ErrStoreUnregistered) {
		return false
	}
	s.mu.Lock()
	id := s.state.Stores[store]
	s.mu.Unlock()
	if id == "" {
		return false
	}
	if st, err := s.opts.Runner.Get(id); err == nil && st.Status != StatusClosed {
		if _, err := s.opts.Runner.CloseConversation(id); err != nil {
			fmt.Fprintf(s.opts.Runner.opts.Log, "support: close the conversation of unregistered store %q: %v\n", store, err)
			return false
		}
	}
	if err := s.deleteStoreMapping(store); err != nil {
		fmt.Fprintf(s.opts.Runner.opts.Log, "support: forget unregistered store %q: %v\n", store, err)
		return false
	}
	return true
}

type selectRequest struct {
	Harness *string `json:"harness"`
	Model   *string `json:"model"`
	Effort  *string `json:"effort"`
}

// Select saves a selection as the machine-wide last choice and applies it
// to the store's open conversation: a harness change replaces the
// conversation with one on the new harness's profile (model and effort
// reset unless given), and a model or effort change applies from its next
// message. With no open conversation only the choice is saved.
func (s *Support) Select(store string, req selectRequest) (SelectAnswer, error) {
	root, err := s.root(store)
	if err != nil {
		return SelectAnswer{}, err
	}
	if req.Model != nil && *req.Model != "" && !modelPattern.MatchString(*req.Model) {
		return SelectAnswer{}, badRequest("model %q must match %s", *req.Model, modelPattern.String())
	}
	if req.Effort != nil && *req.Effort != "" && !effortPattern.MatchString(*req.Effort) {
		return SelectAnswer{}, badRequest("effort %q must be a lowercase word", *req.Effort)
	}
	hs := s.harnesses()
	defer s.lockStore(store)()
	// The base is the open conversation's own settings, else the last
	// selection: "same harness" is the conversation's.
	st, open := s.current(store, root)
	cur, _ := s.selection(hs)
	if open {
		cur = Selection{Harness: st.Backend, Model: st.Model, Effort: st.Effort}
	}
	next := cur
	if req.Harness != nil {
		h, ok := findHarness(hs, *req.Harness)
		switch {
		case !ok:
			return SelectAnswer{}, badRequest("unknown support harness %q", *req.Harness)
		case !h.Available:
			return SelectAnswer{}, badRequest("support harness %q is not available: %s", h.Harness, h.Error)
		}
		if h.Harness != cur.Harness {
			next = Selection{Harness: h.Harness}
		}
	}
	if req.Model != nil {
		next.Model = *req.Model
	}
	if req.Effort != nil {
		next.Effort = *req.Effort
	}
	ans := SelectAnswer{Selection: next}
	switch {
	case !open:
	case next.Harness != cur.Harness:
		fresh, err := s.open(store, root, next, hs)
		if err != nil {
			return SelectAnswer{}, err
		}
		if _, err := s.opts.Runner.CloseConversation(st.ID); err != nil {
			return SelectAnswer{}, err
		}
		ans.Conversation = &fresh
	case req.Model != nil || req.Effort != nil:
		changed, err := s.opts.Runner.SetSettings(st.ID, req.Model, req.Effort)
		if err != nil {
			return SelectAnswer{}, err
		}
		ans.Conversation = &changed
	default:
		ans.Conversation = &st
	}
	if err := s.setLastSelection(next); err != nil {
		return SelectAnswer{}, err
	}
	return ans, nil
}

// root returns the store's working root; an unknown store is 404.
func (s *Support) root(store string) (string, error) {
	root, err := s.opts.StoreRoot(store)
	if err != nil {
		return "", &requestError{status: http.StatusNotFound, msg: fmt.Sprintf("unknown tracker store %q: %v", store, err)}
	}
	return root, nil
}

// ServeHTTP serves SupportRoutePrefix and everything under it.
func (s *Support) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, SupportRoutePrefix), "/")
	parts := strings.Split(rest, "/")
	if req.Method == http.MethodPost && !writeAllowed(req) {
		http.Error(w, "support routes take a JSON body from this machine's clients or the dashboard's own pages", http.StatusForbidden)
		return
	}
	switch {
	case len(parts) == 1 && parts[0] == "choices" && req.Method == http.MethodGet:
		writeAnswer(w, http.StatusOK, s.Choices(req.Context()), nil)
	case len(parts) < 2 || len(parts) > 3 || parts[0] != "stores" || parts[1] == "":
		http.Error(w, "unknown support route", http.StatusNotFound)
	case len(parts) == 2 && req.Method == http.MethodGet:
		ans, err := s.Store(parts[1])
		writeAnswer(w, http.StatusOK, ans, err)
	case len(parts) == 2 || req.Method != http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	case parts[2] == "send":
		var body sendRequest
		if !decode(w, req, &body) {
			return
		}
		st, err := s.Send(parts[1], body.Text)
		writeAnswer(w, http.StatusOK, st, err)
	case parts[2] == "new":
		var body struct{}
		if !decode(w, req, &body) {
			return
		}
		st, err := s.New(parts[1])
		writeAnswer(w, http.StatusOK, st, err)
	case parts[2] == "select":
		var body selectRequest
		if !decode(w, req, &body) {
			return
		}
		ans, err := s.Select(parts[1], body)
		writeAnswer(w, http.StatusOK, ans, err)
	default:
		http.Error(w, "unknown support route", http.StatusNotFound)
	}
}
