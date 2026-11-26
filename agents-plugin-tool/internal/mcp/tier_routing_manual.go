package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// tierRoutingManualHeading is the lead workflow manual section the tier
// routing block extends.
const tierRoutingManualHeading = "### Choosing a tier"

// tierRoutingManualBlock is the text the lead workflow manual's tier section
// gains for the caller holding callerKey: the user's own agent.tier_routing
// nodes and bundles when agent.tier_routing_expose resolves on, and the
// agent.tier_routing_guidance text when it is non-empty. It is "" when
// neither applies, so the manual stays byte-identical by default.
//
// Built-in groups and playbook roles are never listed: rendered playbooks
// already recommend their role tiers. A graph that does not load or fails
// validation lists nothing, since agent.spawn refuses its routed tokens.
func (s *Server) tierRoutingManualBlock(callerKey string, configOpts wsconfig.Options) (string, error) {
	callerKey = strings.TrimSpace(callerKey)
	// configOpts with the caller's repo anchor (sessionResolver's rule), so
	// session, project, repo, and global values all apply.
	opts := configOpts
	opts.RepoRoot = s.sessionConfigOptions(callerKey).RepoRoot
	adapter := sessionConfigAdapter{s: s.sessions}
	resolver := wsconfig.NewResolver(opts, builtinConfigDefaults(), adapter, adapter)
	expose, err := resolver.Get(callerKey, wsconfig.ItemAgentTierRoutingExpose)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", wsconfig.ItemAgentTierRoutingExpose, err)
	}
	guidance, err := resolver.Get(callerKey, wsconfig.ItemAgentTierRoutingGuidance)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", wsconfig.ItemAgentTierRoutingGuidance, err)
	}
	var parts []string
	if strings.EqualFold(strings.TrimSpace(expose.Value), "on") {
		if graph, err := s.tierGraph(configOpts); err == nil && graph.Err() == nil {
			if list := customTierList(graph); list != "" {
				parts = append(parts, list)
			}
		}
	}
	if text := strings.TrimSpace(guidance.Value); text != "" {
		parts = append(parts, tierRoutingGuidanceText(text))
	}
	return strings.Join(parts, "\n\n"), nil
}

// customTierList lists graph's user nodes as <node>:<size> tokens and its
// bundles by name, each with its target, or "" when it has neither.
func customTierList(graph *wsconfig.TierGraph) string {
	var lines []string
	for _, node := range graph.Nodes() {
		if node.Kind != wsconfig.TierNodeUser {
			continue
		}
		lines = append(lines, "- `"+node.Name+":<size>` → "+describeTierPointers(node.Pointers))
	}
	bundles := graph.Bundles()
	names := make([]string, 0, len(bundles))
	for name := range bundles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lines = append(lines, "- `"+name+"` (bundle) → "+describeTierBundle(bundles[name]))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Custom tiers from `agent.tier_routing`; pass the token as the tier, with `<size>` one of small, medium, large, xlarge:\n" + strings.Join(lines, "\n")
}

// describeTierPointers writes a user node's pointers: the default pointer,
// then each per-size exception in rank order. A node with no default
// pointer falls through to the caller's harness at the requested size.
func describeTierPointers(pointers map[string]string) string {
	var parts []string
	target, hasDefault := pointers["*"]
	if hasDefault {
		parts = append(parts, "`"+target+"`")
	}
	for _, size := range wsconfig.TierSizes {
		if target, ok := pointers[size]; ok {
			parts = append(parts, size+": `"+target+"`")
		}
	}
	if !hasDefault {
		if len(parts) == 0 {
			return "the caller's harness at that size"
		}
		parts = append(parts, "other sizes: the caller's harness")
	}
	return strings.Join(parts, "; ")
}

// describeTierBundle writes a bundle's harness (with its provider), model,
// and effort.
func describeTierBundle(bundle wsconfig.TierBundle) string {
	harness := bundle.Harness
	if bundle.Provider != "" {
		harness += "/" + bundle.Provider
	}
	out := harness + " `" + bundle.Model + "`"
	if bundle.Effort != "" {
		out += ", effort " + bundle.Effort
	}
	return out
}

// tierRoutingGuidanceText quotes the user's guidance line by line, so text
// that holds Markdown headings cannot open a section of its own.
func tierRoutingGuidanceText(guidance string) string {
	lines := strings.Split(strings.ReplaceAll(guidance, "\r\n", "\n"), "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return "The user's tier routing preferences; apply them when choosing a tier:\n" + strings.Join(lines, "\n")
}
