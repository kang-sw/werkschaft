package wsdashboard

import (
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// Release 0.51.0 shipped the companion as dashboard.companion_mode
// (off | visual | full) and dashboard.companion_director
// (rules | sidecar | lead-tool). Later releases replaced them with
// dashboard.companion_enabled and the director values rules | agent. An older
// binary, and any script written against 0.51.0, still reads and writes the
// shipped strings, so they stay a deprecated alias:
//
//   - companion_mode stays a declared global-only key. A write of either key
//     writes the other in the same locked file rewrite (the mirror), and a
//     global read takes the legacy value as the enabled state when one is
//     stored (the reconcile), so an older writer's change is honoured and an
//     older reader sees a value that matches what the pet does.
//   - sidecar and lead-tool are accepted values of companion_director and mean
//     rules.
//
// 0.51.0 showed the pet for visual and for full alike and read no director
// value anywhere, so the mapping that keeps its visible behaviour is
// off -> disabled, visual and full -> enabled, sidecar and lead-tool -> rules.
// Neither starts the agent director, which would spend model calls 0.51.0
// never made.

const (
	// CompanionModeKey is the deprecated 0.51.0 key kept as an alias of
	// CompanionEnabledKey.
	CompanionModeKey = "dashboard.companion_mode"

	// CompanionLegacyEnabledMode is the legacy value the mirror writes when the
	// companion is enabled and the stored legacy value is not already visual
	// or full.
	CompanionLegacyEnabledMode = "visual"
)

// companionLegacyDirectors maps a removed companion_director value to the
// value that replaced it.
var companionLegacyDirectors = map[string]string{
	"sidecar":   "rules",
	"lead-tool": "rules",
}

// CompanionLegacyDirector reports the current director value a removed
// 0.51.0 companion_director value stands for.
func CompanionLegacyDirector(value string) (string, bool) {
	current, ok := companionLegacyDirectors[strings.TrimSpace(value)]
	return current, ok
}

// CompanionModeEnabled reports whether a 0.51.0 companion_mode value shows the
// pet, and whether the value is one 0.51.0 accepted.
func CompanionModeEnabled(mode string) (enabled, valid bool) {
	switch strings.TrimSpace(mode) {
	case "off":
		return false, true
	case "visual", "full":
		return true, true
	}
	return false, false
}

// mirrorCompanionOverrides keeps companion_enabled and companion_mode equal
// inside one config rewrite. The key just written wins; a reset of either key
// resets both (the default of both is the disabled pet). A value that does not
// parse is left alone: the write that carried it is refused or ignored on read.
func mirrorCompanionOverrides(key string, overrides map[string]string, removed bool) {
	switch key {
	case CompanionEnabledKey:
		if removed {
			delete(overrides, CompanionModeKey)
			return
		}
		switch enabled, err := strconv.ParseBool(strings.TrimSpace(overrides[key])); {
		case err != nil:
		case !enabled:
			overrides[CompanionModeKey] = "off"
		default:
			// Keep full when the legacy key already says enabled.
			if on, _ := CompanionModeEnabled(overrides[CompanionModeKey]); !on {
				overrides[CompanionModeKey] = CompanionLegacyEnabledMode
			}
		}
	case CompanionModeKey:
		if removed {
			delete(overrides, CompanionEnabledKey)
			return
		}
		if enabled, valid := CompanionModeEnabled(overrides[key]); valid {
			overrides[CompanionEnabledKey] = strconv.FormatBool(enabled)
		}
	}
}

// reconcileCompanionOverrides applies the stored legacy values to a freshly
// read global overrides map: a valid companion_mode decides companion_enabled
// (the mirror keeps them equal, so a difference means an older binary wrote
// companion_mode alone), and a removed director value becomes its successor.
func reconcileCompanionOverrides(overrides map[string]string) {
	if mode, stored := overrides[CompanionModeKey]; stored {
		if enabled, valid := CompanionModeEnabled(mode); valid {
			overrides[CompanionEnabledKey] = strconv.FormatBool(enabled)
		}
	}
	if director, stored := overrides[CompanionDirectorKey]; stored {
		if current, legacy := CompanionLegacyDirector(director); legacy {
			overrides[CompanionDirectorKey] = current
		}
	}
}

func init() {
	wsconfig.RegisterOverrideMirror([]string{CompanionEnabledKey, CompanionModeKey}, mirrorCompanionOverrides)
	wsconfig.RegisterGlobalReconcile(reconcileCompanionOverrides)
}
