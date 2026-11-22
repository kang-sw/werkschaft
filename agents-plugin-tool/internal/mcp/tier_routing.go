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
// the model and effort the recommended tier resolves to for harness. A bare
// size resolves through the config fallback chain as it always has; a role
// tier walks the routing graph, and a slot it lands on resolves through the
// same chain, so the default chain's binding is the bare size's. ok is
// false when the tier does not resolve.
func (s *Server) renderTierBinding(harness, tier string, configOpts wsconfig.Options) (model, effort string, ok bool) {
	tier = strings.TrimSpace(tier)
	if !wsconfig.IsRoutedTierToken(tier) {
		_, model, effort, err := wsconfig.ResolveAgentForHarnessConfig(configOpts, tier, "", "", harness)
		return model, effort, err == nil
	}
	graph, err := s.tierGraph(configOpts)
	if err != nil {
		return "", "", false
	}
	res, err := graph.Resolve(tier, harness)
	if err != nil {
		return "", "", false
	}
	if res.Bundle != "" {
		return res.Model, res.Effort, true
	}
	_, model, effort, err = wsconfig.ResolveAgentForHarnessConfig(configOpts, res.Size, "", "", res.Harness)
	return model, effort, err == nil
}

// withRecommendedRenderBindingFor is withRecommendedRenderBinding for a
// tier that may be a role tier (see renderTierBinding). A role tier is
// resolved for the caller harness agent.spawn uses (playbookHarness), so
// the recommendation names the binding the spawn launches.
func (s *Server) withRecommendedRenderBindingFor(payload, harness, tier string, configOpts wsconfig.Options) string {
	if !wsconfig.IsRoutedTierToken(tier) {
		return withRecommendedRenderBinding(payload, harness, tier, configOpts)
	}
	payload = withRecommendedTier(payload, tier)
	model, effort, ok := s.renderTierBinding(s.playbookHarness(), tier, configOpts)
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
// role tier, alias, explicit <harness>.<size>, or bundle) walks the routing
// graph first, and the slot it lands on resolves through the same chain.
// A bundle answers with its own fields and resolved_from "bundle".
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
	graph, err := s.tierGraph(opts)
	if err == nil {
		var res wsconfig.TierResolution
		if res, err = graph.Resolve(tier, harness); err == nil {
			return s.routedAgentTier(opts, res)
		}
	}
	return resolveAgentTierResult{}, fmt.Errorf("tier must be small, medium, large, or xlarge, or a tier agent.spawn accepts: %w", err)
}

func (s *Server) routedAgentTier(opts wsconfig.Options, res wsconfig.TierResolution) (resolveAgentTierResult, error) {
	if res.Bundle != "" {
		return resolveAgentTierResult{Backend: res.Harness, Model: res.Model, Effort: res.Effort, ResolvedFrom: "bundle", Trace: res.Trace()}, nil
	}
	backend, model, effort, from, err := wsconfig.ResolveAgentTierForHarness(opts, res.Size, res.Harness)
	if err != nil {
		return resolveAgentTierResult{}, err
	}
	return resolveAgentTierResult{Backend: backend, Model: model, Effort: effort, ResolvedFrom: from, Trace: res.Trace()}, nil
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
