package wsdashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

const (
	CompanionEnabledKey        = "dashboard.companion_enabled"
	CompanionDirectorKey       = "dashboard.companion_director"
	CompanionPackKey           = "dashboard.companion_pack"
	CompanionPresetKey         = "dashboard.companion_preset"
	CompanionOutfitKey         = "dashboard.companion_outfit"
	CompanionHairKey           = "dashboard.companion_hair"
	CompanionEyesKey           = "dashboard.companion_eyes"
	CompanionAttachmentsKey    = "dashboard.companion_attachments"
	CompanionPaletteKey        = "dashboard.companion_palette"
	CompanionAgentCooldownKey  = "dashboard.companion_agent_cooldown_seconds"
	CompanionAgentHourlyCapKey = "dashboard.companion_agent_hourly_cap"
	CompanionSourcesKey        = "dashboard.companion_sources"
	CompanionSoulKey           = "dashboard.companion_soul"
	CompanionSoulDirKey        = "dashboard.companion_soul_dir"
	// CompanionBodyTierKey and CompanionWorkTierKey pick the agent tier for
	// the Lightweight (body) and Work companion sessions. The runtime reads
	// them at each launch from the global value only (the keys are
	// global-only), so they are not part of the shell-facing CompanionConfig.
	CompanionBodyTierKey = "dashboard.companion_body_tier"
	CompanionWorkTierKey = "dashboard.companion_work_tier"

	companionChangedPath  = "/config/companion-changed"
	companionConfigHeader = "X-WS-Global-Config"
)

func IsCompanionConfigKey(key string) bool {
	switch key {
	case CompanionEnabledKey, CompanionModeKey, CompanionDirectorKey, CompanionPackKey, CompanionPresetKey,
		CompanionOutfitKey, CompanionHairKey, CompanionEyesKey, CompanionAttachmentsKey, CompanionPaletteKey,
		CompanionAgentCooldownKey, CompanionAgentHourlyCapKey, CompanionSourcesKey,
		CompanionSoulKey, CompanionSoulDirKey:
		return true
	}
	return false
}

// IsDaemonGlobalConfigKey reports whether the daemon keeps key's global value
// in memory, so a writer must invalidate it (NotifyCompanionConfigChange or
// SendCompanionConfigChange) after committing a change: the companion keys
// and the runner's global keys. The wire route keeps its companion name.
func IsDaemonGlobalConfigKey(key string) bool {
	return IsCompanionConfigKey(key) || isAgentGlobalConfigKey(key) || key == wsconfig.ItemWorkflowLang
}

func isAgentGlobalConfigKey(key string) bool {
	return key == wsconfig.ItemAgentKeepAwakeDuringTurns || key == wsconfig.ItemWerkEndpoints
}

// AgentGlobalConfigHost is an agent host that keeps global configuration in
// memory. The daemon calls
// ReloadGlobalConfig once its endpoint is published, and after each matching
// invalidation; the host rereads its own global layer, serialized, so a read
// that finishes later never publishes an older value.
type AgentGlobalConfigHost interface {
	ReloadGlobalConfig()
}

// reloadAgentGlobalConfig asks the agent host, when it keeps any, to reread
// its global configuration.
func (d *Daemon) reloadAgentGlobalConfig() {
	if h, ok := d.agents.(AgentGlobalConfigHost); ok {
		h.ReloadGlobalConfig()
	}
}

// CompanionConfig is the global configuration shared by every dashboard shell.
// The pack field carries the selector so each shell can resolve auto itself.
type CompanionConfig struct {
	Enabled              bool   `json:"enabled"`
	Director             string `json:"director"`
	Pack                 string `json:"pack"`
	Preset               string `json:"preset"`
	Outfit               string `json:"outfit"`
	Hair                 string `json:"hair"`
	Eyes                 string `json:"eyes"`
	Attachments          string `json:"attachments"`
	Palette              string `json:"palette"`
	AgentCooldownSeconds int    `json:"agentCooldownSeconds"`
	AgentHourlyCap       int    `json:"agentHourlyCap"`
	// Sources is the raw dashboard.companion_sources value. The JSON form adds
	// sourceEntries, the verdict of parseCompanionSources on it (MarshalJSON).
	Sources string `json:"sources"`
	// Soul selects the active Portable Soul ID (empty resolves to "default").
	Soul string `json:"soul"`
	// SoulDir optionally overrides the root directory storing Portable Souls
	// (empty defaults to ~/.ws/companion-soul).
	SoulDir string `json:"soulDir"`
	// Lang is the global workflow.lang value, held here so persona
	// resolution reads memory instead of the config file on every wake.
	Lang string `json:"lang,omitempty"`
}

// MarshalJSON adds sourceEntries: each entry of Sources as the source routes
// judge it. The modal shows these verdicts rather than its own, so an entry
// the routes would refuse never looks usable. It is computed here, not
// stored, so CompanionConfig stays comparable.
func (c CompanionConfig) MarshalJSON() ([]byte, error) {
	type plain CompanionConfig
	return json.Marshal(struct {
		plain
		SourceEntries []companionSourceEntry `json:"sourceEntries"`
	}{plain(c), parseCompanionSources(c.Sources)})
}

type companionConfigSnapshot struct {
	config CompanionConfig
	wake   chan struct{}
}

type companionState struct {
	reloadMu sync.Mutex
	snapshot atomic.Pointer[companionConfigSnapshot]
}

func (s *companionState) current() companionConfigSnapshot {
	if snapshot := s.snapshot.Load(); snapshot != nil {
		return *snapshot
	}
	return companionConfigSnapshot{config: CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}}
}

// refreshCompanion reads the persisted global layer after a write or at daemon
// startup. Locking the reload keeps simultaneous notifications from publishing
// an earlier read after a newer one. Shells read the published snapshot in RAM.
func (d *Daemon) refreshCompanion() error {
	s := &d.companion
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	previous := s.current()
	if previous.wake == nil {
		previous.wake = make(chan struct{})
		s.snapshot.Store(&previous)
	}
	if d.config == nil || d.config.LoadCompanion == nil {
		return nil
	}
	config, err := d.config.LoadCompanion()
	if err != nil {
		return err
	}
	if config != previous.config {
		s.snapshot.Store(&companionConfigSnapshot{config: config, wake: make(chan struct{})})
		close(previous.wake)
		if config.Soul != previous.config.Soul || config.SoulDir != previous.config.SoulDir {
			d.companionMemMu.Lock()
			mem := d.companionMem
			d.companionMemMu.Unlock()
			if mem != nil {
				mem.Soul().Invalidate()
			}
		}
	}
	return nil
}

// CompanionConfigHost accepts a hosted MCP server's notification in process.
type CompanionConfigHost interface {
	NotifyCompanionConfigChange(stateDir, globalPath string) bool
}

func (d *Daemon) NotifyCompanionConfigChange(stateDir, globalPath string) bool {
	if stateDir == "" || d.dir == "" || filepath.Clean(stateDir) != filepath.Clean(d.dir) {
		return false
	}
	select {
	case <-d.stop:
		return false
	default:
	}
	if err := d.refreshMatchingCompanion(companionConfigID(globalPath)); err != nil {
		fmt.Fprintf(d.log, "dashboard: refresh companion settings: %v\n", err)
	}
	return true
}

func companionConfigID(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(absolute))
	return hex.EncodeToString(digest[:])
}

func (d *Daemon) refreshMatchingCompanion(configID string) error {
	if configID == "" || d.config == nil {
		return nil
	}
	path, err := wsconfig.GlobalPath(d.config.Options)
	if err != nil {
		return err
	}
	if configID != companionConfigID(path) {
		return nil
	}
	// The notification names no key: every in-memory global value rereads.
	d.reloadAgentGlobalConfig()
	return d.refreshCompanion()
}

func (d *Daemon) handleCompanionConfigChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localViewer(r) || !d.machineAuth(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := d.refreshMatchingCompanion(r.Header.Get(companionConfigHeader)); err != nil {
		http.Error(w, "configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SendCompanionConfigChange invalidates the daemon's in-memory configuration
// after a separate MCP process commits a write. The daemon reads its own file.
func SendCompanionConfigChange(ctx context.Context, client *http.Client, ep Endpoint, globalPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(ep, companionChangedPath), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set(companionConfigHeader, companionConfigID(globalPath))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("companion configuration notification: status %d", resp.StatusCode)
	}
	return nil
}
