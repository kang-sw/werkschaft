package mcp

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsrsrc"
)

// packagedTierRoles converts a resource tree's tier-role declarations into
// the routing graph's packaged role nodes.
func packagedTierRoles(root fs.FS) ([]wsconfig.TierRole, error) {
	declared, err := wsrsrc.TierRoles(root)
	if err != nil {
		return nil, err
	}
	roles := make([]wsconfig.TierRole, 0, len(declared))
	for _, d := range declared {
		roles = append(roles, wsconfig.TierRole{Name: d.Role, Parent: d.Parent, Size: wsconfig.NormalizedTier(d.Size)})
	}
	return roles, nil
}

// PackagedTierRoles is the daemon runner's Options.TierRoles: the role nodes
// of the ws package's playbooks, the package whose sessions delegate
// through agent.spawn.
func PackagedTierRoles() ([]wsconfig.TierRole, error) {
	root, err := wsrsrc.PackageResources("ws")
	if err != nil {
		return nil, err
	}
	return packagedTierRoles(root)
}

// tierGraph is the effective agent.tier_routing graph this server's
// playbooks and agent backends define, with the user's overrides and
// aliases from configOpts. It is the graph agent.spawn resolves through.
func (s *Server) tierGraph(configOpts wsconfig.Options) (*wsconfig.TierGraph, error) {
	root, err := s.rsrcRoot()
	if err != nil {
		return nil, err
	}
	roles, err := packagedTierRoles(root)
	if err != nil {
		return nil, fmt.Errorf("read packaged tier roles: %w", err)
	}
	return wsconfig.LoadTierGraph(configOpts, roles, wsagent.TierHarnesses(s.agentBackends()...))
}

// renderTierBinding is the binding a playbook.render recommendation carries:
// the model and effort the recommended tier resolves to. A bare size
// resolves through the config fallback chain for bareHarness as it always
// has. A role tier walks the routing graph for walkHarness (the caller
// harness agent.spawn uses). When the walk ends at the caller fallthrough
// (the bare size on the caller's harness), that bare size resolves exactly
// as the bare token does, for bareHarness and with its fallback chain, so
// with no user config a role tier carries its bare size's binding byte for
// byte. Any other terminal takes the binding agent.spawn launches
// (routedBinding). ok is false when the tier does not resolve or, past the
// fallthrough, the spawn could not launch it.
func (s *Server) renderTierBinding(walkHarness, bareHarness, tier string, configOpts wsconfig.Options) (model, effort string, ok bool) {
	tier = strings.TrimSpace(tier)
	if !wsconfig.IsRoutedTierToken(tier) {
		_, model, effort, err := wsconfig.ResolveAgentForHarnessConfig(configOpts, tier, "", "", bareHarness)
		return model, effort, err == nil
	}
	res, binding, err := s.routedBinding(tier, walkHarness, configOpts)
	if res.CallerFallthrough {
		_, model, effort, err := wsconfig.ResolveAgentForHarnessConfig(configOpts, res.Size, "", "", bareHarness)
		return model, effort, err == nil
	}
	if err != nil {
		return "", "", false
	}
	return binding.Model, binding.Effort, true
}

// harnessAvailability is the runner's per-harness spawn check
// (*wsagent.Runner); a server without a runner of its own (a forwarding
// spawned agent) skips it, and agent.tiers stays the availability source.
type harnessAvailability interface {
	SpawnableHarness(harness string) string
}

// routedBinding resolves a routed agent.spawn tier the way the spawn does:
// the graph walk, the terminal harness's own agents.tier slot with no
// cross-harness fallback (wsagent.RoutedTierBinding), and, where this server
// hosts the runner, the harness availability check. The resolution is
// returned whenever the walk succeeded, even when the binding fails, so a
// caller can tell a caller-fallthrough terminal apart.
func (s *Server) routedBinding(tier, harness string, configOpts wsconfig.Options) (wsconfig.TierResolution, wsconfig.AgentTier, error) {
	graph, err := s.tierGraph(configOpts)
	if err != nil {
		return wsconfig.TierResolution{}, wsconfig.AgentTier{}, err
	}
	res, err := graph.Resolve(tier, harness)
	if err != nil {
		return wsconfig.TierResolution{}, wsconfig.AgentTier{}, err
	}
	binding, err := wsagent.RoutedTierBinding(configOpts, res)
	if err != nil {
		return res, wsconfig.AgentTier{}, fmt.Errorf("tier %q (%s): %w", tier, res.Trace(), err)
	}
	if check, ok := s.pi.agents.(harnessAvailability); ok {
		if why := check.SpawnableHarness(res.Harness); why != "" {
			return res, wsconfig.AgentTier{}, fmt.Errorf("tier %q (%s): %s", tier, res.Trace(), why)
		}
	}
	return res, binding, nil
}

// withRecommendedRenderBindingFor is withRecommendedRenderBinding for a
// tier that may be a role tier (see renderTierBinding). A role tier is
// walked for the caller harness agent.spawn uses (playbookHarness), so a
// mapped recommendation names the binding the spawn launches; a walk that
// falls through to the bare size resolves it as a bare recommendation for
// harness would.
func (s *Server) withRecommendedRenderBindingFor(payload, harness, tier string, configOpts wsconfig.Options) string {
	if !wsconfig.IsRoutedTierToken(tier) {
		return withRecommendedRenderBinding(payload, harness, tier, configOpts)
	}
	payload = withRecommendedTier(payload, tier)
	model, effort, ok := s.renderTierBinding(s.playbookHarness(), harness, tier, configOpts)
	if !ok {
		return payload
	}
	if model = strings.TrimSpace(model); model != "" {
		payload += "\nrecommended-model: " + model
	}
	if effort = strings.TrimSpace(effort); effort != "" {
		payload += "\nrecommended-reasoning-effort: " + formatEffortForText(effort)
	}
	return payload
}

// resolveAgentTier is config.resolve_agent: a bare size resolves through the
// config fallback chain exactly as before; any other agent.spawn tier (a
// role tier, alias, explicit <harness>.<size>, or bundle) resolves as
// agent.spawn resolves it (routedBinding): the terminal harness's own slot,
// resolved_from that harness, or a bundle's fields, resolved_from "bundle".
// A walk that ends at the caller fallthrough resolves its bare size as the
// bare token does (the fallback chain for harness, the default bucket when
// harness is empty), so an unmapped role tier answers what its bare size
// answers, plus the trace.
func (s *Server) resolveAgentTier(tier, harness string) (resolveAgentTierResult, error) {
	opts := s.configOptions()
	if wsconfig.NormalizedTier(tier) != "" {
		backend, model, effort, from, err := wsconfig.ResolveAgentTierForHarness(opts, tier, harness)
		return resolveAgentTierResult{Backend: backend, Model: model, Effort: effort, ResolvedFrom: from}, err
	}
	if strings.TrimSpace(tier) == "" {
		_, _, _, _, err := wsconfig.ResolveAgentTierForHarness(opts, tier, harness)
		return resolveAgentTierResult{}, err
	}
	walk := harness
	if walk == "" {
		// Only a caller fallthrough uses the walk's harness, and that
		// terminal resolves for harness below; the placeholder keeps the
		// trace readable, as config.tune's traces show it.
		walk = "<caller>"
	}
	res, binding, err := s.routedBinding(tier, walk, opts)
	if res.CallerFallthrough {
		backend, model, effort, from, err := wsconfig.ResolveAgentTierForHarness(opts, res.Size, harness)
		if err != nil {
			return resolveAgentTierResult{}, err
		}
		return resolveAgentTierResult{Backend: backend, Model: model, Effort: effort, ResolvedFrom: from, Trace: res.Trace()}, nil
	}
	if err != nil {
		return resolveAgentTierResult{}, fmt.Errorf("tier must be small, medium, large, or xlarge, or a tier agent.spawn accepts: %w", err)
	}
	from := res.Harness
	if res.Bundle != "" {
		from = "bundle"
	}
	return resolveAgentTierResult{Backend: res.Harness, Model: binding.Model, Effort: binding.Effort, ResolvedFrom: from, Trace: res.Trace()}, nil
}

// roleRecommendedTier is the tier a render recommends: <role>:<size> when
// the caller delegates through daemon-owned agent.spawn and the playbook
// declares a tier role, else the bare size unchanged. Native callers keep
// the bare size, so their bindings and in-body text are unchanged.
func roleRecommendedTier(daemonAgents bool, role, size string) string {
	role, size = strings.TrimSpace(role), strings.TrimSpace(size)
	if !daemonAgents || role == "" || size == "" {
		return size
	}
	return role + ":" + size
}
