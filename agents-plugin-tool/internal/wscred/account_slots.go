package wscred

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsstate"
)

const (
	// AccountSlotsFileName is the daemon-owned per-harness account slot
	// registry file (Storage Type 5, no tokens stored).
	AccountSlotsFileName = "account-slots.json"
	// DefaultAccountSlotID identifies the implicit default-home slot for a
	// harness (empty ConfigDir, resolving through the D10 fallback chain).
	DefaultAccountSlotID     = "default"
	slotsDirName             = "slots"
	accountSlotsSchema       = 1
	maxAccountSlotLabelRunes = 80
)

var (
	// ErrInvalidAccountSlot reports malformed slot metadata (harness, id, or config_dir).
	ErrInvalidAccountSlot = errors.New("invalid account slot")
	// ErrAccountSlotNotFound reports a switch or lookup targeting an unknown slot.
	ErrAccountSlotNotFound = errors.New("account slot not found")
	// ErrAccountSlotExists reports an attempt to register a duplicate slot id.
	ErrAccountSlotExists = errors.New("account slot already exists")
	// ErrDefaultSlotImmutable reports an attempt to remove or overwrite the implicit default slot.
	ErrDefaultSlotImmutable = errors.New("the default account slot cannot be removed")
)

var accountSlotIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// AccountSlot is one persisted harness account slot entry (never holds tokens).
type AccountSlot struct {
	ID        string `json:"id"`
	Harness   string `json:"harness"`
	Label     string `json:"label"`
	ConfigDir string `json:"config_dir,omitempty"`
	Managed   bool   `json:"managed,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// AccountSlotView is the display projection served from memory for status APIs
// and SSE updates (never carries raw tokens).
type AccountSlotView struct {
	ID        string `json:"id"`
	Harness   string `json:"harness"`
	Label     string `json:"label"`
	ConfigDir string `json:"config_dir,omitempty"`
	Managed   bool   `json:"managed,omitempty"`
	Implicit  bool   `json:"implicit,omitempty"`
	Active    bool   `json:"active"`
}

// DefaultAccountSlotsPath returns the account slot registry file path under
// cacheHome's cache root ("" uses wsstate.CacheRoot).
func DefaultAccountSlotsPath(cacheHome string) (string, error) {
	root, err := wsstate.CacheRoot(wsstate.Options{CacheHome: cacheHome})
	if err != nil {
		return "", err
	}
	return filepath.Join(root, daemonDirName, dirName, AccountSlotsFileName), nil
}

// DefaultSlotsRoot returns the daemon-managed per-slot config directory root
// (<cacheRoot>/dashboard/slots).
func DefaultSlotsRoot(cacheHome string) (string, error) {
	root, err := wsstate.CacheRoot(wsstate.Options{CacheHome: cacheHome})
	if err != nil {
		return "", err
	}
	return filepath.Join(root, daemonDirName, slotsDirName), nil
}

// DefaultSlotDir returns the managed config directory path for (harness, slotID).
func DefaultSlotDir(cacheHome, harness, slotID string) (string, error) {
	if !SupportedSlotHarness(harness) {
		return "", fmt.Errorf("%w: unsupported harness %q", ErrInvalidAccountSlot, harness)
	}
	if !ValidAccountSlotID(slotID) || slotID == DefaultAccountSlotID {
		return "", fmt.Errorf("%w: invalid slot id %q", ErrInvalidAccountSlot, slotID)
	}
	root, err := DefaultSlotsRoot(cacheHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, harness, slotID), nil
}

// CreateManagedSlotDir allocates <cacheRoot>/dashboard/slots/<harness>/<slotID>
// with owner-only permissions (0700 on POSIX, protected User-SID DACL on Windows).
func CreateManagedSlotDir(cacheHome, harness, slotID string) (string, error) {
	dir, err := DefaultSlotDir(cacheHome, harness, slotID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := RestrictPrivateDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// SupportedSlotHarness reports whether harness participates in config-dir account slots.
func SupportedSlotHarness(harness string) bool {
	return harness == "codex" || harness == "claude"
}

// ValidAccountSlotID validates a caller-supplied account slot identifier.
func ValidAccountSlotID(id string) bool {
	return accountSlotIDPattern.MatchString(id)
}

// NormalizeAccountSlotID maps an empty slot id (legacy records / default) to
// DefaultAccountSlotID.
func NormalizeAccountSlotID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return DefaultAccountSlotID
	}
	return id
}

// HarnessHomeOptions configures the D10 single slot-aware home resolver.
type HarnessHomeOptions struct {
	// Harness is "codex" or "claude".
	Harness string
	// ExplicitHome (e.g., DaemonOptions.Home), when non-empty, suppresses
	// inherited CODEX_HOME / CLAUDE_CONFIG_DIR environment overrides (075fa3ff)
	// and serves as the default home root when SlotConfigDir is empty.
	ExplicitHome string
	// SlotConfigDir is the active or bound account slot's isolated config_dir.
	// Empty selects the implicit default slot fallback chain.
	SlotConfigDir string
	// Getenv resolves environment variables; nil uses os.Getenv.
	Getenv func(string) string
	// UserHomeDir resolves the OS user home; nil uses os.UserHomeDir.
	UserHomeDir func() (string, error)
}

func harnessHomeSpec(harness string) (envKey, defaultSubdir string, ok bool) {
	switch harness {
	case "codex":
		return "CODEX_HOME", ".codex", true
	case "claude":
		return "CLAUDE_CONFIG_DIR", ".claude", true
	default:
		return "", "", false
	}
}

// HarnessForConfigEnv maps a provider config-dir environment variable to its
// canonical slot harness name, or "" if unknown.
func HarnessForConfigEnv(envKey string) string {
	switch envKey {
	case "CODEX_HOME":
		return "codex"
	case "CLAUDE_CONFIG_DIR":
		return "claude"
	default:
		return ""
	}
}

// SlotConfigEnvKey returns the environment variable used to isolate config_dir
// for harness (e.g. CODEX_HOME, CLAUDE_CONFIG_DIR), or "" if unsupported.
func SlotConfigEnvKey(harness string) string {
	key, _, _ := harnessHomeSpec(harness)
	return key
}

// ResolveHarnessHome resolves CODEX_HOME / CLAUDE_CONFIG_DIR using the D10
// precedence order:
//  1. Explicit slot config_dir (SlotConfigDir)
//  2. Inherited env (CODEX_HOME / CLAUDE_CONFIG_DIR) unless ExplicitHome is set
//  3. Default home (ExplicitHome or UserHomeDir) joined with .codex / .claude
func ResolveHarnessHome(opts HarnessHomeOptions) string {
	envKey, subdir, ok := harnessHomeSpec(opts.Harness)
	if !ok {
		return ""
	}
	if dir := strings.TrimSpace(opts.SlotConfigDir); dir != "" {
		return filepath.Clean(dir)
	}
	explicitHome := strings.TrimSpace(opts.ExplicitHome)
	if explicitHome == "" {
		getenv := opts.Getenv
		if getenv == nil {
			getenv = os.Getenv
		}
		if envDir := strings.TrimSpace(getenv(envKey)); envDir != "" {
			return filepath.Clean(envDir)
		}
		homeFn := opts.UserHomeDir
		if homeFn == nil {
			homeFn = os.UserHomeDir
		}
		home, err := homeFn()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		return filepath.Join(filepath.Clean(home), subdir)
	}
	return filepath.Join(filepath.Clean(explicitHome), subdir)
}

// ResolveHarnessHomeFromRegistry resolves the config home for (harness, slotID).
// When slotID is empty, it uses the registry's active slot for harness.
// When slotID is DefaultAccountSlotID, it uses the implicit default slot.
func ResolveHarnessHomeFromRegistry(reg *AccountSlotRegistry, harness, slotID, explicitHome string, getenv func(string) string) string {
	var slotDir string
	if reg != nil {
		if slotID == "" {
			slotDir = reg.ActiveSlot(harness).ConfigDir
		} else if slot, ok := reg.ResolveSlot(harness, slotID); ok {
			slotDir = slot.ConfigDir
		}
	}
	return ResolveHarnessHome(HarnessHomeOptions{
		Harness:       harness,
		ExplicitHome:  explicitHome,
		SlotConfigDir: slotDir,
		Getenv:        getenv,
	})
}

// slotDirDenyRegistry keeps the union of all live registries' custom slot
// config_dir paths in memory so deniedBasePaths protects them with zero disk I/O.
var slotDirDenyRegistry = struct {
	mu   sync.RWMutex
	dirs map[*AccountSlotRegistry][]string
}{
	dirs: map[*AccountSlotRegistry][]string{},
}

func updateRegisteredSlotDirs(reg *AccountSlotRegistry, dirs []string) {
	slotDirDenyRegistry.mu.Lock()
	defer slotDirDenyRegistry.mu.Unlock()
	if len(dirs) == 0 {
		delete(slotDirDenyRegistry.dirs, reg)
		return
	}
	slotDirDenyRegistry.dirs[reg] = slices.Clone(dirs)
}

func removeRegisteredSlotDirs(reg *AccountSlotRegistry) {
	slotDirDenyRegistry.mu.Lock()
	defer slotDirDenyRegistry.mu.Unlock()
	delete(slotDirDenyRegistry.dirs, reg)
}

// RegisteredSlotDirs returns every non-empty account slot config_dir currently
// loaded across live AccountSlotRegistry instances plus any in-progress staging
// slot directories (in-memory, zero disk I/O).
func RegisteredSlotDirs() []string {
	slotDirDenyRegistry.mu.RLock()
	tempSlotDirs.mu.RLock()
	defer tempSlotDirs.mu.RUnlock()
	defer slotDirDenyRegistry.mu.RUnlock()
	if len(slotDirDenyRegistry.dirs) == 0 && len(tempSlotDirs.dirs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, dirs := range slotDirDenyRegistry.dirs {
		for _, dir := range dirs {
			if dir != "" && !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	for dir := range tempSlotDirs.dirs {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	slices.Sort(out)
	return out
}

type accountSlotsDocument struct {
	version int
	active  map[string]string
	slots   []AccountSlot
	extra   map[string]json.RawMessage
}

func (d accountSlotsDocument) clone() accountSlotsDocument {
	out := accountSlotsDocument{
		version: d.version,
		active:  make(map[string]string, len(d.active)),
		slots:   slices.Clone(d.slots),
		extra:   make(map[string]json.RawMessage, len(d.extra)),
	}
	for k, v := range d.active {
		out.active[k] = v
	}
	for k, v := range d.extra {
		out.extra[k] = v
	}
	return out
}

func decodeAccountSlotsDocument(raw []byte) (accountSlotsDocument, error) {
	doc := accountSlotsDocument{
		version: accountSlotsSchema,
		active:  map[string]string{},
		extra:   map[string]json.RawMessage{},
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return doc, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return accountSlotsDocument{}, errInvalidFile
	}
	for name, value := range top {
		switch name {
		case "schema_version":
			if err := json.Unmarshal(value, &doc.version); err != nil {
				return accountSlotsDocument{}, errInvalidFile
			}
		case "active":
			var active map[string]string
			if err := json.Unmarshal(value, &active); err != nil {
				return accountSlotsDocument{}, errInvalidFile
			}
			for h, id := range active {
				if h != "" && id != "" {
					doc.active[h] = id
				}
			}
		case "slots":
			var slots []AccountSlot
			if err := json.Unmarshal(value, &slots); err != nil {
				return accountSlotsDocument{}, errInvalidFile
			}
			doc.slots = slots
		default:
			doc.extra[name] = value
		}
	}
	return doc, nil
}

func (d accountSlotsDocument) encode() ([]byte, error) {
	top := make(map[string]any, len(d.extra)+3)
	for k, v := range d.extra {
		top[k] = v
	}
	top["schema_version"] = d.version
	top["active"] = d.active
	if d.slots == nil {
		top["slots"] = []AccountSlot{}
	} else {
		top["slots"] = d.slots
	}
	raw, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// AccountSlotRegistry is the daemon's in-memory owner of per-harness account
// slots (D11). It persists non-secret slot metadata in a Storage Type 5 private
// file (atomic replace + Windows User-SID DACL) and serves reads from memory.
type AccountSlotRegistry struct {
	path     string
	now      func() time.Time
	revision atomic.Uint64

	mu        sync.Mutex
	recovered bool
	loaded    bool
	loadErr   error
	failedAt  fileStamp
	closed    bool
	doc       accountSlotsDocument
	onChange  func()

	writeStep func(credentialWriteStep) // test checkpoint during replacement
}

// NewAccountSlotRegistry returns a registry backed by path. No disk I/O happens
// until the first read or write.
func NewAccountSlotRegistry(path string) *AccountSlotRegistry {
	return &AccountSlotRegistry{
		path: path,
		now:  time.Now,
		doc: accountSlotsDocument{
			version: accountSlotsSchema,
			active:  map[string]string{},
			extra:   map[string]json.RawMessage{},
		},
	}
}

// Path returns the backing registry file path.
func (r *AccountSlotRegistry) Path() string { return r.path }

// SetOnChange registers a callback invoked after a committed slot change.
func (r *AccountSlotRegistry) SetOnChange(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

// Revision increments on every committed change (used for cache/SSE invalidation).
func (r *AccountSlotRegistry) Revision() uint64 { return r.revision.Load() }

// Close prevents further writes and unregisters this instance's in-memory deny
// entries. Reads continue serving the last loaded snapshot from memory.
func (r *AccountSlotRegistry) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	removeRegisteredSlotDirs(r)
}

// Load eagerly loads the registry file into memory (called once at daemon start
// so subsequent status reads and deniedBasePaths checks perform zero disk I/O).
func (r *AccountSlotRegistry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(false)
}

func (r *AccountSlotRegistry) recoverLocked() error {
	if r.recovered || r.path == "" {
		return nil
	}
	if err := recoverPrivateFile(r.path); err != nil {
		return fmt.Errorf("recover account slot registry: %w", err)
	}
	r.recovered = true
	return nil
}

func (r *AccountSlotRegistry) loadLocked(retry bool) error {
	if err := r.recoverLocked(); err != nil {
		return err
	}
	if r.loaded && r.loadErr == nil {
		return nil
	}
	if r.loaded && !retry && stampOf(r.path) == r.failedAt {
		return r.loadErr
	}
	r.loaded, r.loadErr = true, nil
	if r.path == "" {
		return nil
	}
	r.failedAt = stampOf(r.path)
	file, _, err := OpenCredentialFileForRead(r.path)
	var raw []byte
	if err == nil && file != nil {
		raw, err = io.ReadAll(file)
		_ = file.Close()
	}
	if err != nil {
		r.loadErr = fmt.Errorf("read account slot registry: %w", err)
		return r.loadErr
	}
	doc, err := decodeAccountSlotsDocument(raw)
	if err != nil {
		r.loadErr = fmt.Errorf("account slot registry %s is not valid JSON", r.path)
		return r.loadErr
	}
	r.doc = doc
	r.syncDenyDirsLocked()
	return nil
}

func (r *AccountSlotRegistry) syncDenyDirsLocked() {
	if r.closed {
		return
	}
	var dirs []string
	for _, slot := range r.doc.slots {
		if d := strings.TrimSpace(slot.ConfigDir); d != "" {
			dirs = append(dirs, filepath.Clean(d))
		}
	}
	updateRegisteredSlotDirs(r, dirs)
}

func (r *AccountSlotRegistry) prepareWriteLocked() error {
	if r.closed {
		return ErrStoreClosed
	}
	if err := r.loadLocked(true); err != nil {
		return err
	}
	if r.doc.version > accountSlotsSchema {
		return fmt.Errorf("account slot registry schema version %d is newer than supported version %d", r.doc.version, accountSlotsSchema)
	}
	return nil
}

// writeLocked persists next. published reports whether the replacement became
// the committed generation, which can hold even when err is non-nil (a failed
// post-publish scrub of the retiring generation); callers must notify observers
// on published, not on err == nil.
func (r *AccountSlotRegistry) writeLocked(next accountSlotsDocument) (published bool, err error) {
	if r.path == "" {
		r.doc = next
		r.revision.Add(1)
		r.syncDenyDirsLocked()
		return true, nil
	}
	raw, err := next.encode()
	if err != nil {
		return false, err
	}
	published, err = writePrivateFile(r.path, raw, replaceCredentialFile, r.writeStep)
	if published {
		r.doc = next
		r.revision.Add(1)
		r.syncDenyDirsLocked()
	}
	if err != nil {
		// A failed write can leave a .retiring or .staged generation behind;
		// recover again before the next write instead of wedging it on EEXIST.
		r.recovered = false
	}
	return published, err
}

func implicitDefaultSlot(harness string) AccountSlot {
	return AccountSlot{
		ID:      DefaultAccountSlotID,
		Harness: harness,
		Label:   "Default",
	}
}

// Slots returns the ordered account slots for harness, always starting with the
// implicit default-home slot. Reads from memory once loaded.
func (r *AccountSlotRegistry) Slots(harness string) []AccountSlotView {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.loadLocked(false)
	return r.slotsLocked(harness)
}

func (r *AccountSlotRegistry) slotsLocked(harness string) []AccountSlotView {
	if !SupportedSlotHarness(harness) {
		return nil
	}
	activeID := r.activeIDLocked(harness)
	views := make([]AccountSlotView, 0, len(r.doc.slots)+1)
	def := implicitDefaultSlot(harness)
	views = append(views, AccountSlotView{
		ID:       def.ID,
		Harness:  harness,
		Label:    def.Label,
		Implicit: true,
		Active:   activeID == DefaultAccountSlotID,
	})
	for _, slot := range r.doc.slots {
		if slot.Harness != harness {
			continue
		}
		views = append(views, AccountSlotView{
			ID:        slot.ID,
			Harness:   slot.Harness,
			Label:     slot.Label,
			ConfigDir: slot.ConfigDir,
			Managed:   slot.Managed,
			Active:    slot.ID == activeID,
		})
	}
	return views
}

func (r *AccountSlotRegistry) activeIDLocked(harness string) string {
	id := strings.TrimSpace(r.doc.active[harness])
	if id == "" || id == DefaultAccountSlotID {
		return DefaultAccountSlotID
	}
	for _, slot := range r.doc.slots {
		if slot.Harness == harness && slot.ID == id {
			return id
		}
	}
	return DefaultAccountSlotID
}

// ActiveSlot returns the active slot view for harness (implicit default when unset).
func (r *AccountSlotRegistry) ActiveSlot(harness string) AccountSlotView {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.loadLocked(false)
	for _, v := range r.slotsLocked(harness) {
		if v.Active {
			return v
		}
	}
	return AccountSlotView{ID: DefaultAccountSlotID, Harness: harness, Label: "Default", Implicit: true, Active: true}
}

// ActiveSlots returns the active slot ID per supported harness from memory.
func (r *AccountSlotRegistry) ActiveSlots() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.loadLocked(false)
	out := make(map[string]string, 2)
	for _, h := range []string{"codex", "claude"} {
		out[h] = r.activeIDLocked(h)
	}
	return out
}

// ResolveSlot resolves (harness, slotID), treating "" and DefaultAccountSlotID
// as the implicit default slot.
func (r *AccountSlotRegistry) ResolveSlot(harness, slotID string) (AccountSlot, bool) {
	if !SupportedSlotHarness(harness) {
		return AccountSlot{}, false
	}
	id := NormalizeAccountSlotID(slotID)
	if id == DefaultAccountSlotID {
		return implicitDefaultSlot(harness), true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.loadLocked(false)
	for _, slot := range r.doc.slots {
		if slot.Harness == harness && slot.ID == id {
			return slot, true
		}
	}
	return AccountSlot{}, false
}

// ConfigDirs returns all registered non-empty slot config_dir paths from memory.
func (r *AccountSlotRegistry) ConfigDirs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.loadLocked(false)
	var out []string
	for _, slot := range r.doc.slots {
		if d := strings.TrimSpace(slot.ConfigDir); d != "" {
			out = append(out, filepath.Clean(d))
		}
	}
	return out
}

// AddSlot registers a new isolated account slot for a supported harness.
func (r *AccountSlotRegistry) AddSlot(slot AccountSlot) (AccountSlot, error) {
	slot.Harness = strings.TrimSpace(slot.Harness)
	slot.ID = strings.TrimSpace(slot.ID)
	slot.Label = strings.TrimSpace(slot.Label)
	slot.ConfigDir = strings.TrimSpace(slot.ConfigDir)
	if !SupportedSlotHarness(slot.Harness) {
		return AccountSlot{}, fmt.Errorf("%w: unsupported harness %q", ErrInvalidAccountSlot, slot.Harness)
	}
	if !ValidAccountSlotID(slot.ID) || slot.ID == DefaultAccountSlotID {
		return AccountSlot{}, fmt.Errorf("%w: invalid slot id %q", ErrInvalidAccountSlot, slot.ID)
	}
	if slot.ConfigDir == "" || !filepath.IsAbs(slot.ConfigDir) {
		return AccountSlot{}, fmt.Errorf("%w: config_dir must be an absolute path", ErrInvalidAccountSlot)
	}
	slot.ConfigDir = filepath.Clean(slot.ConfigDir)
	if utf8.RuneCountInString(slot.Label) > maxAccountSlotLabelRunes {
		return AccountSlot{}, fmt.Errorf("%w: label exceeds %d characters", ErrInvalidAccountSlot, maxAccountSlotLabelRunes)
	}
	if slot.Label == "" {
		slot.Label = slot.ID
	}

	r.mu.Lock()
	if err := r.prepareWriteLocked(); err != nil {
		r.mu.Unlock()
		return AccountSlot{}, err
	}
	for _, existing := range r.doc.slots {
		if existing.Harness == slot.Harness && existing.ID == slot.ID {
			r.mu.Unlock()
			return AccountSlot{}, ErrAccountSlotExists
		}
	}
	if slot.CreatedAt == "" {
		slot.CreatedAt = r.now().UTC().Format(time.RFC3339)
	}
	next := r.doc.clone()
	next.slots = append(next.slots, slot)
	published, err := r.writeLocked(next)
	cb := r.onChange
	r.mu.Unlock()
	if published && cb != nil {
		cb()
	}
	return slot, err
}

// SetActive sets the default active account slot for harness.
func (r *AccountSlotRegistry) SetActive(harness, slotID string) error {
	harness = strings.TrimSpace(harness)
	if !SupportedSlotHarness(harness) {
		return fmt.Errorf("%w: unsupported harness %q", ErrInvalidAccountSlot, harness)
	}
	id := NormalizeAccountSlotID(slotID)

	r.mu.Lock()
	if err := r.prepareWriteLocked(); err != nil {
		r.mu.Unlock()
		return err
	}
	if id != DefaultAccountSlotID {
		found := false
		for _, slot := range r.doc.slots {
			if slot.Harness == harness && slot.ID == id {
				found = true
				break
			}
		}
		if !found {
			r.mu.Unlock()
			return ErrAccountSlotNotFound
		}
	}
	if r.activeIDLocked(harness) == id {
		r.mu.Unlock()
		return nil
	}
	next := r.doc.clone()
	if id == DefaultAccountSlotID {
		delete(next.active, harness)
	} else {
		next.active[harness] = id
	}
	published, err := r.writeLocked(next)
	cb := r.onChange
	r.mu.Unlock()
	if published && cb != nil {
		cb()
	}
	return err
}

// RemoveSlot deletes a registered slot. Removing the active slot falls back to
// the implicit default slot. Removing the implicit default slot is refused.
func (r *AccountSlotRegistry) RemoveSlot(harness, slotID string) error {
	harness = strings.TrimSpace(harness)
	id := strings.TrimSpace(slotID)
	if !SupportedSlotHarness(harness) {
		return fmt.Errorf("%w: unsupported harness %q", ErrInvalidAccountSlot, harness)
	}
	if id == "" || id == DefaultAccountSlotID {
		return ErrDefaultSlotImmutable
	}

	r.mu.Lock()
	if err := r.prepareWriteLocked(); err != nil {
		r.mu.Unlock()
		return err
	}
	idx := -1
	for i, slot := range r.doc.slots {
		if slot.Harness == harness && slot.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		r.mu.Unlock()
		return ErrAccountSlotNotFound
	}
	next := r.doc.clone()
	next.slots = append(next.slots[:idx], next.slots[idx+1:]...)
	if next.active[harness] == id {
		delete(next.active, harness)
	}
	published, err := r.writeLocked(next)
	cb := r.onChange
	r.mu.Unlock()
	if published && cb != nil {
		cb()
	}
	return err
}

// UpdateSlotLabel updates the display label for a registered slot.
func (r *AccountSlotRegistry) UpdateSlotLabel(harness, slotID, label string) error {
	harness = strings.TrimSpace(harness)
	id := strings.TrimSpace(slotID)
	label = strings.TrimSpace(label)
	if !SupportedSlotHarness(harness) || label == "" || utf8.RuneCountInString(label) > maxAccountSlotLabelRunes {
		return ErrInvalidAccountSlot
	}
	if id == "" || id == DefaultAccountSlotID {
		return ErrDefaultSlotImmutable
	}
	r.mu.Lock()
	if err := r.prepareWriteLocked(); err != nil {
		r.mu.Unlock()
		return err
	}
	next := r.doc.clone()
	found := false
	for i := range next.slots {
		if next.slots[i].Harness == harness && next.slots[i].ID == id {
			next.slots[i].Label = label
			found = true
			break
		}
	}
	if !found {
		r.mu.Unlock()
		return ErrAccountSlotNotFound
	}
	published, err := r.writeLocked(next)
	cb := r.onChange
	r.mu.Unlock()
	if published && cb != nil {
		cb()
	}
	return err
}
