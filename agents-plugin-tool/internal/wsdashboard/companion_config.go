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
	CompanionAgentCooldownKey  = "dashboard.companion_agent_cooldown_seconds"
	CompanionAgentHourlyCapKey = "dashboard.companion_agent_hourly_cap"

	companionChangedPath  = "/config/companion-changed"
	companionConfigHeader = "X-WS-Global-Config"

	// companionModalSurface is the settings-catalog surface the companion
	// modal renders instead of the settings panel.
	companionModalSurface = "companion-modal"
)

// shellSettingsStrings are the js.settings.* string prefixes the shell page
// keeps although it drops the settings page's strings: the companion modal
// labels its catalog rows with them.
var shellSettingsStrings = []string{"js.settings.key.dashboard.companion_", "js.settings.unit."}

// companionModalRow is one settings-catalog row the companion modal renders.
type companionModalRow struct {
	Key         string `json:"key"`
	Control     string `json:"control"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Unit        string `json:"unit,omitempty"`
}

// companionModalRows returns the catalog rows whose surface is the companion
// modal, in catalog order, as the JSON array the shell page embeds (the
// shell loads no settings catalog of its own).
func companionModalRows(catalog []byte) (string, error) {
	var rows []struct {
		companionModalRow
		Surface string `json:"surface"`
	}
	if err := json.Unmarshal(catalog, &rows); err != nil {
		return "", fmt.Errorf("settings catalog: %w", err)
	}
	modal := []companionModalRow{}
	for _, row := range rows {
		if row.Surface == companionModalSurface {
			modal = append(modal, row.companionModalRow)
		}
	}
	raw, err := json.Marshal(modal)
	return string(raw), err
}

func IsCompanionConfigKey(key string) bool {
	switch key {
	case CompanionEnabledKey, CompanionDirectorKey, CompanionPackKey, CompanionPresetKey,
		CompanionAgentCooldownKey, CompanionAgentHourlyCapKey:
		return true
	}
	return false
}

// CompanionConfig is the global configuration shared by every dashboard shell.
// The pack field carries the selector so each shell can resolve auto itself.
type CompanionConfig struct {
	Enabled              bool   `json:"enabled"`
	Director             string `json:"director"`
	Pack                 string `json:"pack"`
	Preset               string `json:"preset"`
	AgentCooldownSeconds int    `json:"agentCooldownSeconds"`
	AgentHourlyCap       int    `json:"agentHourlyCap"`
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
	if configID == companionConfigID(path) {
		return d.refreshCompanion()
	}
	return nil
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
