package wsagent

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// CompanionTierAuto is the companion tier setting's explicit auto spelling;
// the empty value means the same.
const CompanionTierAuto = "auto"

// Companion tier option kinds.
const (
	CompanionTierKindAuto    = "auto"
	CompanionTierKindHarness = "harness"
	CompanionTierKindAlias   = "alias"
	CompanionTierKindBundle  = "bundle"
)

// CompanionTierResolution is where one companion role's configured tier
// lands: the harness and agents.tier binding its lead launches with. Model
// empty means the harness's own default model. Available is false only
// when no harness can host a companion lead at all. FallbackReason says why
// a configured value was not honored and auto was used instead.
type CompanionTierResolution struct {
	Configured     string `json:"configured"`
	Harness        string `json:"harness,omitempty"`
	Tier           string `json:"tier,omitempty"`
	Bundle         string `json:"bundle,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Available      bool   `json:"available"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// CompanionTierOption is one value the companion tier selectors offer, with
// the binding it launches now. Target is an alias's <harness>.<tier>.
type CompanionTierOption struct {
	Value   string `json:"value"`
	Kind    string `json:"kind"`
	Harness string `json:"harness,omitempty"`
	Tier    string `json:"tier,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Target  string `json:"target,omitempty"`
}

// companionHarnesses are the registered harnesses that can host a
// companion lead now (a lead profile and an available backend), in the
// launch preference order: the built-in harness order, then the rest by
// name. The first one is what auto launches.
func (r *Runner) companionHarnesses() []string {
	var usable []string
	for name := range r.opts.Backends {
		if r.companionHarnessGap(name) == "" {
			usable = append(usable, name)
		}
	}
	sort.Strings(usable)
	out := make([]string, 0, len(usable))
	for _, preferred := range BuiltinHarnessNames() {
		if slices.Contains(usable, preferred) {
			out = append(out, preferred)
		}
	}
	for _, name := range usable {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// companionHarnessGap says why harness cannot host a companion lead now,
// "" when it can.
func (r *Runner) companionHarnessGap(harness string) string {
	reg, ok := r.opts.Backends[harness]
	if !ok || reg.Backend == nil {
		return fmt.Sprintf("harness %q is not registered in this daemon", harness)
	}
	if LeadProfileFor(harness, r.opts.Backends) == "" {
		return fmt.Sprintf("harness %q cannot host a lead session", harness)
	}
	return r.registeredWorker("", harness)
}

// ResolveCompanionTier resolves a companion role's configured tier with cfg
// for agents.tier, agent.tier_aliases and agent.tier_routing. Empty or
// "auto" is the role's autoSize on the first companion harness, with that
// harness's default model when its slot is unset. An explicit value pins
// the harness it names (<harness>.<tier>, an alias, or a routing token; a
// bare tier uses the first companion harness) and must have a slot there;
// any value that cannot launch falls back to auto with FallbackReason.
func (r *Runner) ResolveCompanionTier(cfg wsconfig.Options, configured, autoSize string) CompanionTierResolution {
	raw := strings.ToLower(strings.TrimSpace(configured))
	harnesses := r.companionHarnesses()
	auto := func(reason string) CompanionTierResolution {
		out := CompanionTierResolution{Configured: raw, Tier: autoSize, FallbackReason: reason}
		if len(harnesses) == 0 {
			if out.FallbackReason == "" {
				out.FallbackReason = "no harness can host a companion session now"
			}
			return out
		}
		out.Harness, out.Available = harnesses[0], true
		if tiers, err := wsconfig.HarnessTiers(cfg, out.Harness); err == nil {
			if slot, ok := tiers[autoSize]; ok && slot.Model != "" {
				out.Model, out.Effort, out.Provider = slot.Model, slot.Effort, slot.Provider
			}
		}
		return out
	}
	if raw == "" || raw == CompanionTierAuto {
		return auto("")
	}
	if len(harnesses) == 0 {
		return auto(fmt.Sprintf("%q: no harness can host a companion session now", raw))
	}
	res, err := r.companionExplicitTier(cfg, raw, harnesses[0], NewTierBindingResolver(cfg, r.opts.Backends))
	if err != nil {
		return auto(fmt.Sprintf("%q: %v; using auto", raw, err))
	}
	res.Configured = raw
	return res
}

// companionExplicitTier resolves a non-auto value the way agent.spawn reads
// a tier, with first as the bare tier's harness, then checks the terminal
// harness can host a companion lead.
func (r *Runner) companionExplicitTier(cfg wsconfig.Options, raw, first string, resolver *TierBindingResolver) (CompanionTierResolution, error) {
	var harness, size string
	switch h, t, dotted := strings.Cut(raw, "."); {
	case dotted:
		if IsRetiredDirectTierHarness(h) {
			return CompanionTierResolution{}, fmt.Errorf("harness %q is retired", h)
		}
		harness, size = h, wsconfig.NormalizedTier(t)
		if harness == "" || size == "" {
			return CompanionTierResolution{}, errors.New("names no tier (small, medium, large, xlarge)")
		}
	case wsconfig.NormalizedTier(raw) != "":
		harness, size = first, wsconfig.NormalizedTier(raw)
	default:
		aliases, err := tierAliases(cfg)
		if err != nil {
			return CompanionTierResolution{}, err
		}
		if target, ok := aliases[raw]; ok {
			harness, size, _ = strings.Cut(target, ".")
			break
		}
		graph, err := r.tierGraph(AgentCaller{Config: cfg})
		if err != nil {
			return CompanionTierResolution{}, err
		}
		routed, err := graph.Resolve(raw, first)
		if err != nil {
			return CompanionTierResolution{}, err
		}
		if routed.Bundle == "" {
			harness, size = routed.Harness, routed.Size
			break
		}
		harness = TierRuntimeHarness(routed.Harness)
		if why := r.companionHarnessGap(harness); why != "" {
			return CompanionTierResolution{}, errors.New(why)
		}
		binding, err := resolver.Routed(routed)
		if err != nil {
			return CompanionTierResolution{}, err
		}
		if why := r.workerProviderGap(harness, binding.Provider); why != "" {
			return CompanionTierResolution{}, errors.New(why)
		}
		return CompanionTierResolution{Harness: harness, Tier: routed.Size, Bundle: routed.Bundle,
			Model: binding.Model, Effort: binding.Effort, Provider: binding.Provider, Available: true}, nil
	}
	harness = TierRuntimeHarness(harness)
	if why := r.companionHarnessGap(harness); why != "" {
		return CompanionTierResolution{}, errors.New(why)
	}
	slot, err := resolver.harnessSlot(harness, size)
	if err != nil {
		return CompanionTierResolution{}, err
	}
	if why := r.workerProviderGap(harness, slot.Provider); why != "" {
		return CompanionTierResolution{}, errors.New(why)
	}
	return CompanionTierResolution{Harness: harness, Tier: size, Model: slot.Model, Effort: slot.Effort,
		Provider: slot.Provider, Available: true}, nil
}

// CompanionTierOptions lists what the companion tier selectors offer: auto,
// every companion harness's set agents.tier slot as <harness>.<tier>, then
// each agent.tier_aliases name and agent.tier_routing bundle that resolves
// to a launchable binding now. Slots read agents.tier directly, so listing
// never fetches a model catalog.
func (r *Runner) CompanionTierOptions(cfg wsconfig.Options) []CompanionTierOption {
	out := []CompanionTierOption{{Value: CompanionTierAuto, Kind: CompanionTierKindAuto}}
	harnesses := r.companionHarnesses()
	for _, h := range harnesses {
		tiers, err := wsconfig.HarnessTiers(cfg, h)
		if err != nil {
			continue
		}
		for _, size := range wsconfig.TierSizes {
			if slot, ok := tiers[size]; ok && slot.Model != "" {
				out = append(out, CompanionTierOption{Value: h + "." + size, Kind: CompanionTierKindHarness,
					Harness: h, Tier: size, Model: slot.Model, Effort: slot.Effort})
			}
		}
	}
	if len(harnesses) == 0 {
		return out
	}
	resolver := NewTierBindingResolver(cfg, r.opts.Backends)
	aliases, _ := tierAliases(cfg)
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if res, err := r.companionExplicitTier(cfg, name, harnesses[0], resolver); err == nil {
			out = append(out, CompanionTierOption{Value: name, Kind: CompanionTierKindAlias, Harness: res.Harness,
				Tier: res.Tier, Model: res.Model, Effort: res.Effort, Target: aliases[name]})
		}
	}
	graph, err := r.tierGraph(AgentCaller{Config: cfg})
	if err != nil || graph.Err() != nil {
		return out
	}
	bundles := graph.Bundles()
	bundleNames := make([]string, 0, len(bundles))
	for name := range bundles {
		if _, shadowed := aliases[name]; !shadowed {
			bundleNames = append(bundleNames, name)
		}
	}
	sort.Strings(bundleNames)
	for _, name := range bundleNames {
		if res, err := r.companionExplicitTier(cfg, name, harnesses[0], resolver); err == nil {
			out = append(out, CompanionTierOption{Value: name, Kind: CompanionTierKindBundle, Harness: res.Harness,
				Tier: res.Tier, Model: res.Model, Effort: res.Effort})
		}
	}
	return out
}
