package main

import (
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// companionAgentsReadback is the Settings readback of the companion tier
// selectors: each role's resolution (the same one the next launch uses) and
// the values the selectors offer.
type companionAgentsReadback struct {
	Body    wsagent.CompanionTierResolution `json:"body"`
	Work    wsagent.CompanionTierResolution `json:"work"`
	Options []wsagent.CompanionTierOption   `json:"options"`
}

// companionRoleTier is a role's config key and its auto tier size.
func companionRoleTier(role string) (key, autoSize string) {
	if role == mcp.CompanionRoleBody {
		return wsdashboard.CompanionBodyTierKey, companionTierBody
	}
	return wsdashboard.CompanionWorkTierKey, companionTierWork
}

// companionTierSetting reads key with no repository or session: the project
// value over the global one, "" when neither is set or the read fails.
func companionTierSetting(opts wsconfig.Options, key string) string {
	resolver := wsconfig.NewResolver(opts, map[string]string{key: ""}, nil, nil)
	value, err := resolver.Get("", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

// resolveRole is role's launch resolution from the current settings.
func (r *companionRuntime) resolveRole(role string) wsagent.CompanionTierResolution {
	key, autoSize := companionRoleTier(role)
	return r.agents.ResolveCompanionTier(r.configOpts, companionTierSetting(r.configOpts, key), autoSize)
}

// AgentsReadback serves /api/companion/agents.
func (r *companionRuntime) AgentsReadback() any {
	if r.agents == nil {
		return companionAgentsReadback{Options: []wsagent.CompanionTierOption{}}
	}
	return companionAgentsReadback{
		Body:    r.resolveRole(mcp.CompanionRoleBody),
		Work:    r.resolveRole(mcp.CompanionRoleWork),
		Options: r.agents.CompanionTierOptions(r.configOpts),
	}
}

// pickLaunchSpec is role's launch binding: the configured tier, or auto
// (the role's legacy size on the first companion harness). A configured
// value that cannot launch falls back to auto and logs why.
func (r *companionRuntime) pickLaunchSpec(role string) (companionLaunchSpec, bool) {
	if r.resolveLaunch != nil {
		return r.resolveLaunch(role)
	}
	if r.agents == nil {
		return companionLaunchSpec{}, false
	}
	res := r.resolveRole(role)
	if res.FallbackReason != "" {
		fmt.Fprintf(r.logw, "companion: %s tier: %s\n", role, res.FallbackReason)
	}
	if !res.Available {
		return companionLaunchSpec{}, false
	}
	return companionLaunchSpec{Backend: res.Harness, Model: res.Model, Effort: res.Effort, Provider: res.Provider}, true
}
