package mcp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wspackage"
)

func DashboardConfigCatalog(packageName string) (*wsconfigservice.Catalog, error) {
	pkg, err := wspackage.Open(packageName)
	if err != nil {
		return nil, err
	}
	rsrc, err := fs.Sub(pkg, "rsrc")
	if err != nil {
		return nil, err
	}
	return dashboardConfigCatalog(pkg, rsrc)
}

func dashboardConfigCatalog(pkg, rsrc fs.FS) (*wsconfigservice.Catalog, error) {
	defaults := builtinConfigAndPromptDefaults()
	rules := make([]wsconfigservice.Rule, 0, len(configRegistry)+40)
	add := func(entry configKeyEntry) {
		rule := wsconfigservice.Rule{
			Key: entry.Key, Type: "string", Default: defaults[entry.Key],
			GlobalOnly: entry.GlobalOnly(), Structured: entry.Structured,
			Compaction: isCompactionKey(entry.Key),
			Enum:       fieldEnum(entry.ValueFields, "value"),
			Normalize: func(value string) (string, error) {
				if strings.HasPrefix(entry.Key, "prompt.") {
					if strings.TrimSpace(value) == "" {
						return "", fmt.Errorf("config.tune: prompt must be non-empty")
					}
					return value, nil
				}
				return normalizeTuneScalar(entry, value, true)
			},
		}
		if entry.HarnessApplicable {
			rule.Harnesses = fieldEnum(entry.SelectorFields, "harness")
		}
		if entry.Declared != nil {
			key := entry.Declared
			rule.Default, rule.Type = key.Default, key.Type
			rule.Minimum, rule.Maximum = key.Minimum, key.Maximum
			rule.Decode = key.parse
			rule.NoRepo, rule.NoSession = dashboardAdapterReadLimits(key.Key)
		}
		if rule.Compaction {
			rule.LegacyKey = func(harness string) string { return wsagent.BuiltinCompaction(harness).LegacyKey(entry.Key) }
		}
		if entry.Key == wsconfig.ItemCompactionWorkerMode {
			rule.ValidateHarnessValue = func(harness, value string) error {
				return validateCompactionWorkerModeTarget(entry.Key, harness, value)
			}
			rule.FixedHarnessValue = func(harness string) string {
				if wsagent.WorkerCompactionWSOnly(harness) {
					return wsconfig.CompactionModeWS
				}
				return ""
			}
		}
		if entry.Key == wsconfig.ItemCompactionLeadMode {
			rule.ValidateHarnessValue = func(harness, value string) error {
				return validateCompactionLeadModeTarget(entry.Key, harness, value)
			}
			rule.FixedHarnessValue = func(harness string) string {
				if wsagent.LeadCompactionWSOnly(harness) {
					return wsconfig.CompactionModeWS
				}
				return ""
			}
		}
		if entry.Key == wsconfig.ItemAgentTierRouting {
			// The one exact-key structured setting: no name part, validated as
			// a whole graph against the package's roles and the built-in
			// harnesses, the daemon runner's registered backends
			// (cmd/ws-mcp newAgentRunner), so a dashboard write cannot store a
			// graph config.tune and agent.spawn would refuse. A known harness
			// that is not installed still validates.
			rule.Type = "object"
			rule.Editable = true
			rule.NormalizeObject = func(value any, scope wsconfig.Scope) (json.RawMessage, error) {
				if _, ok := value.(map[string]any); !ok || scope != wsconfig.ScopeGlobal {
					return nil, wsconfigservice.ErrInvalid
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				routing, err := wsconfig.ParseTierRouting(raw)
				if err != nil {
					return nil, err
				}
				roles, err := packagedTierRoles(rsrc)
				if err != nil {
					return nil, err
				}
				if err := wsconfig.NewTierGraph(roles, routing, nil, wsagent.TierHarnesses()).Err(); err != nil {
					return nil, err
				}
				return json.Marshal(routing)
			}
		} else if entry.Structured {
			rule.Type = "object"
			family := worktreeProfileFamily
			if strings.HasPrefix(entry.Key, wsagent.ProfilePrefix) {
				family = registeredAgentProfileFamily()
			}
			rule.ValidateName = family.validateName
			rule.NormalizeObject = func(value any, scope wsconfig.Scope) (json.RawMessage, error) {
				if _, ok := value.(map[string]any); !ok {
					return nil, wsconfigservice.ErrInvalid
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				parsed, err := family.parse(raw, scope)
				if err != nil {
					return nil, err
				}
				return json.Marshal(parsed)
			}
		}
		if entry.Key == "agents.tier" {
			rule.Type = "tier"
		}
		rules = append(rules, rule)
	}
	for _, entry := range configRegistry {
		add(entry)
	}
	for _, key := range append(append([]declaredConfigKey{worktreeNudgeStaleDaysKey, worktreeNudgePreservedGBKey}, gitMergeSizeKeys...), werkWebSearchKeys...) {
		add(key.entry())
	}
	// The pool is visible in config.show and resolved by worktree tools.
	// It has no config.tune writer, so the settings sheet can inspect it.
	rules = append(rules, wsconfigservice.Rule{Key: wsconfig.ItemWorktreePool, Type: "string", Default: defaults[wsconfig.ItemWorktreePool], ReadOnly: true})
	add(preferenceKnobEntry("preference.*"))
	add(worktreeProfileKnobEntry("*"))
	add(agentProfileKnobEntry("*"))
	points, seeds, err := scanOverridePointsAndSeeds(rsrc)
	if err != nil {
		return nil, err
	}
	for _, point := range points {
		add(promptKnobEntry(point.PointId))
		if seed, ok := seeds[point.PointId]; ok {
			rules[len(rules)-1].Seed = &seed
		}
	}
	namespaces := make(map[string]string)
	for _, name := range []string{wspackage.ConfigManifestFile, wspackage.AgYConfigManifestFile, wspackage.DashboardConfigManifestFile} {
		raw, err := fs.ReadFile(pkg, name)
		if err != nil {
			return nil, err
		}
		namespace, keys, err := parseConfigManifestBytes(name, raw, builtinConfigKeySet(), namespaces)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		namespaces[namespace] = name
		for _, key := range keys {
			add(key.entry())
		}
	}
	return wsconfigservice.NewCatalog(rules)
}

// DashboardConfigBackend is the daemon's settings backend for packageName.
// available is agent.spawn's harness availability check without probes
// (wsagent Runner.HarnessAvailability) the tier routing view reports at each
// terminal, for the view's simulated caller harness; nil reports none.
func DashboardConfigBackend(packageName string, opts wsconfig.Options, src *DashboardSessionSource, ambient func(), available func(caller, harness string) (why string, known bool), registries ...map[string]wsagent.BackendRegistration) (*wsdashboard.ConfigBackend, error) {
	catalog, err := DashboardConfigCatalog(packageName)
	if err != nil {
		return nil, err
	}
	pkg, err := wspackage.Open(packageName)
	if err != nil {
		return nil, err
	}
	rsrc, err := fs.Sub(pkg, "rsrc")
	if err != nil {
		return nil, err
	}
	structured := builtinWorktreeProfiles()
	for key, value := range wsagent.BuiltinProfiles() {
		structured[key] = value
	}
	backend := &wsdashboard.ConfigBackend{
		Catalog: catalog, Options: opts, Structured: structured,
		LoadCompanion: func() (wsdashboard.CompanionConfig, error) {
			return dashboardCompanionConfig(catalog, opts)
		},
		LoadAuditLimits: func() (wsauditstore.Limits, []string, error) {
			return dashboardAuditLimits(opts)
		},
		Session:            src.settingsSession,
		IsLead:             src.settingsLead,
		OrchestrateWarning: orchestrateWhitelistWarning,
		Changed: func(key string) {
			notifyCompactionTune(key)
			if key == wsconfig.ItemWorkflowLang && ambient != nil {
				ambient()
			}
		},
	}
	// The graph view reads the backend's options at call time, the same
	// config the snapshot and writes use.
	backend.TierRouting = func(draft json.RawMessage, harness string) (any, error) {
		return dashboardTierRoutingView(rsrc, backend.Options, draft, harness, available, registries...)
	}
	return backend, nil
}

// dashboardAuditLimits reads the global audit knobs (global-only keys) and
// clamps a stored set that breaks the knob rules to the nearest valid values,
// one warning per change ((2129)=a).
func dashboardAuditLimits(opts wsconfig.Options) (wsauditstore.Limits, []string, error) {
	overrides, err := wsconfig.GlobalOverrides(opts)
	if err != nil {
		return wsauditstore.DefaultLimits(), nil, err
	}
	limits, warnings := wsauditstore.LimitsFromValues(overrides)
	limits, clamped := limits.Clamp()
	return limits, append(warnings, clamped...), nil
}

func dashboardCompanionConfig(catalog *wsconfigservice.Catalog, opts wsconfig.Options) (wsdashboard.CompanionConfig, error) {
	overrides, err := wsconfig.GlobalOverrides(opts)
	if err != nil {
		return wsdashboard.CompanionConfig{}, err
	}
	// resolved returns the catalog-validated global override, or the manifest
	// default when the key is unset or its stored value no longer validates
	// (as config.get does). The default's dynamic type is the key's type.
	resolved := func(key string) any {
		entry := catalog.Entry(key, nil, wsconfig.ScopeGlobal, wsconfigservice.Layers{})
		raw, ok := overrides[key]
		if !ok {
			return entry.Default
		}
		mutation, err := catalog.Validate(wsconfig.ScopeGlobal, wsconfigservice.Operation{Key: key, Action: "set", Value: raw})
		if err != nil {
			return entry.Default
		}
		switch entry.Default.(type) {
		case bool:
			if parsed, err := strconv.ParseBool(mutation.Scalar); err == nil {
				return parsed
			}
		case int64:
			if parsed, err := strconv.ParseInt(mutation.Scalar, 10, 64); err == nil {
				return parsed
			}
		default:
			return mutation.Scalar
		}
		return entry.Default
	}
	text := func(key string) string { s, _ := resolved(key).(string); return s }
	flag := func(key string) bool { b, _ := resolved(key).(bool); return b }
	number := func(key string) int { n, _ := resolved(key).(int64); return int(n) }
	return wsdashboard.CompanionConfig{
		Enabled: flag(wsdashboard.CompanionEnabledKey), Director: text(wsdashboard.CompanionDirectorKey),
		Pack: text(wsdashboard.CompanionPackKey), Preset: text(wsdashboard.CompanionPresetKey),
		Outfit: text(wsdashboard.CompanionOutfitKey), Hair: text(wsdashboard.CompanionHairKey), Eyes: text(wsdashboard.CompanionEyesKey),
		Attachments: text(wsdashboard.CompanionAttachmentsKey), Palette: text(wsdashboard.CompanionPaletteKey),
		AgentCooldownSeconds: number(wsdashboard.CompanionAgentCooldownKey),
		AgentHourlyCap:       number(wsdashboard.CompanionAgentHourlyCapKey),
		Sources:              text(wsdashboard.CompanionSourcesKey),
		Soul:                 text(wsdashboard.CompanionSoulKey),
		SoulDir:              text(wsdashboard.CompanionSoulDirKey),
		Lang:                 strings.TrimSpace(overrides[wsconfig.ItemWorkflowLang]),
	}, nil
}

// settingsSession batches the lineage read without touching session activity.
// Its writer binds the secret key in the daemon and never serializes it.
func (src *DashboardSessionSource) settingsSession(reg wsdashboard.Registration) (wsconfigservice.SessionLayer, wsconfig.SessionWriter, string, bool) {
	d, err := src.store.dirs()
	if err != nil {
		return wsconfigservice.SessionLayer{}, nil, "", false
	}
	keys, err := src.keysFor(d, reg.ReplyID, false)
	if err != nil {
		return wsconfigservice.SessionLayer{}, nil, "", false
	}
	src.store.mu.Lock()
	defer src.store.mu.Unlock()
	for _, key := range keys {
		record, ok := src.store.readRecord(d, key, partKey)
		if !ok || record.Root != reg.Project {
			continue
		}
		values := make(map[string]string)
		src.store.walkLineage(d, key, record, func(r sessionRecord) bool {
			for k, v := range r.Overrides {
				if _, seen := values[k]; !seen {
					values[k] = v
				}
			}
			return false
		})
		return wsconfigservice.SessionLayer{Effective: values, Own: maps.Clone(record.Overrides)}, sessionConfigAdapter{s: src.store}, key, true
	}
	return wsconfigservice.SessionLayer{}, nil, "", false
}

// Startup-only adapter readers do not have a session key or worktree.
// These limits describe the effective adapter behavior, while MCP keeps
// accepting its historical config.tune arguments.
func dashboardAdapterReadLimits(key string) (noRepo, noSession bool) {
	switch key {
	// Retained name check: the settings page routes the harness-bucketed config
	// keys a user's config may hold by group; the keys are persisted names
	// (TestDashboardSettingsReadTunedValues).
	case "claude.status_band_enabled":
		return false, false
	case "agy.model_wait_timeout_seconds", "agy.model_wait_retries", "agy.stall_log_check", "agy.http2_frame_trace",
		"agy.prefer_native_subagent", "agy.avoid_agy_subagent":
		return false, true
	case DashboardPortKey:
		return true, true
	case wsdashboard.CompanionBodyTierKey, wsdashboard.CompanionWorkTierKey:
		// The companion runtime launches outside any repository or session,
		// so only the project and global values apply.
		return true, true
	}
	if strings.HasPrefix(key, "claude.") {
		return true, true
	}
	return false, false
}

func (src *DashboardSessionSource) settingsLead(reg wsdashboard.Registration) bool {
	d, err := src.store.dirs()
	if err != nil {
		return false
	}
	keys, err := src.keysFor(d, reg.ReplyID, false)
	if err != nil {
		return false
	}
	src.store.mu.Lock()
	defer src.store.mu.Unlock()
	for _, key := range keys {
		record, ok := src.store.readRecord(d, key, partKey)
		if ok && record.Root == reg.Project && record.Parent == "" && record.Scope == string(roleLead) {
			return true
		}
	}
	return false
}
