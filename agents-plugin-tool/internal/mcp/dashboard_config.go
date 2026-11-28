package mcp

import (
	"context"
	"net/http"

	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func (s *Server) writeScalarTune(resolver *wsconfig.Resolver, opts wsconfig.Options, key string, value *string, setOpts wsconfig.SetOptions) error {
	if key == wsconfig.ItemWerkEndpoints && (setOpts.ExplicitScope == "" || setOpts.ExplicitScope == wsconfig.ScopeGlobal) {
		if host, ok := s.live.host.(wsdashboard.EndpointConfigHost); ok {
			return host.SetLocalEndpointOverride(opts, value)
		}
		return wsdashboard.SetOfflineLocalEndpointOverride(opts, value)
	}
	if value == nil {
		return resolver.Unset(key, setOpts)
	}
	return resolver.Set(key, *value, setOpts)
}

func (s *Server) notifyScalarTune(key string, opts wsconfig.Options) {
	notifyCompactionTune(key)
	if key == wsconfig.ItemWorkflowLang {
		s.refreshAgentAmbient()
	}
	// The daemon holds these global keys in memory; the notification only
	// invalidates them, and the daemon rereads its own global file.
	if !wsdashboard.IsDaemonGlobalConfigKey(key) {
		return
	}
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		return
	}
	dir, err := s.dashboard.stateDir()
	if err != nil {
		return
	}
	if host, ok := s.live.host.(wsdashboard.CompanionConfigHost); ok && host.NotifyCompanionConfigChange(dir, path) {
		return
	}
	endpoint, err := wsdashboard.ReadEndpoint(dir)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dashboardNudgeTimeout)
	defer cancel()
	if err := wsdashboard.SendCompanionConfigChange(ctx, http.DefaultClient, endpoint, path); err != nil {
		s.debugEvent("dashboard.companion_notification_failed", map[string]any{"error": err.Error()})
	}
}

// The dashboard keys live in a "dashboard."-namespace manifest each package
// ships beside its own manifest. ws-mcp declares no built-in value for them:
// an instance whose launcher did not load that manifest (or whose manifest was
// rejected) has the dashboard off.
const (
	DashboardEnabledKey = "dashboard.enabled"
	DashboardPortKey    = "dashboard.port"
	// DashboardLANKey turns the daemon's LAN mode on. It is global-only: one
	// daemon serves every session on the machine, so one project must not be
	// able to turn LAN on for all of them, and the value must not depend on
	// which session started the daemon. The resolver therefore reads only the
	// global layer and the manifest default for it, and refuses a write at any
	// other scope.
	DashboardLANKey = "dashboard.lan_enabled"
	// The machine-wide mailbox sweep likewise reads only the global value.
	DashboardMailboxUndeliveredKey = "dashboard.mailbox_undelivered_minutes"
)

// manifestGlobalOnlyKeys are manifest-declared keys that pin themselves
// global-only through wsconfig. The manifest namespace check treats a
// registered key as ws-mcp's own, so it skips these.
var manifestGlobalOnlyKeys = map[string]struct{}{
	DashboardLANKey: {}, DashboardMailboxUndeliveredKey: {},
	wsdashboard.CompanionEnabledKey: {}, wsdashboard.CompanionModeKey: {}, wsdashboard.CompanionDirectorKey: {},
	wsdashboard.CompanionPackKey: {}, wsdashboard.CompanionPresetKey: {},
	wsdashboard.CompanionOutfitKey: {}, wsdashboard.CompanionHairKey: {}, wsdashboard.CompanionEyesKey: {},
	wsdashboard.CompanionAttachmentsKey: {}, wsdashboard.CompanionPaletteKey: {},
	wsauditstore.KeySessionCapMiB: {}, wsauditstore.KeyTotalCapMiB: {},
	wsauditstore.KeyBodyCapKiB: {}, wsauditstore.KeyRetentionDays: {},
	wsdashboard.CompanionAgentCooldownKey: {}, wsdashboard.CompanionAgentHourlyCapKey: {},
	wsdashboard.CompanionSourcesKey: {},
	// The companion tiers are launch settings of a daemon-wide session that
	// starts outside any repository or session ((1792)=a).
	wsdashboard.CompanionBodyTierKey: {}, wsdashboard.CompanionWorkTierKey: {},
}

func init() {
	for key := range manifestGlobalOnlyKeys {
		wsconfig.RegisterGlobalOnly(key)
	}
	// The audit byte knobs hold only as a set ((2129)=a): a global write or
	// reset that would break it is refused before it commits.
	wsconfig.RegisterSetCheck(wsauditstore.Keys(), func(overrides map[string]string) error {
		return wsauditstore.ValidateValues(overrides)
	})
}

// DashboardSettings is the dashboard configuration a stdio server reads once
// at startup.
type DashboardSettings struct {
	// Enabled is true only when dashboard.enabled is declared by a loaded
	// manifest and resolves to true.
	Enabled bool
	// Port is dashboard.port, or wsdashboard.DefaultPort when undeclared.
	Port int
	// LAN is the global dashboard.lan_enabled value, false when the key is
	// undeclared.
	LAN bool
}

// DashboardSettings resolves the dashboard keys without a session: startup
// has none, so only the project, global, and built-in values apply (the LAN
// key reads the global and built-in values only). A stored
// value that fails its declared type resolves to the manifest default, as
// config.get does.
func (s *Server) DashboardSettings() DashboardSettings {
	out := DashboardSettings{Port: wsdashboard.DefaultPort}
	if !s.dashboardDeclared() {
		return out
	}
	if got, err := s.resolveConfigGet("", DashboardEnabledKey); err == nil {
		out.Enabled, _ = got.Value.(bool)
	}
	if _, ok := s.declared.lookup(DashboardPortKey); ok {
		if got, err := s.resolveConfigGet("", DashboardPortKey); err == nil {
			if port, ok := got.Value.(int64); ok {
				out.Port = int(port)
			}
		}
	}
	if _, ok := s.declared.lookup(DashboardLANKey); ok {
		if got, err := s.resolveConfigGet("", DashboardLANKey); err == nil {
			out.LAN, _ = got.Value.(bool)
		}
	}
	return out
}
