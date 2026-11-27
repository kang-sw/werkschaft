package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

// dynamicTierRouterTimeout bounds a single agent.spawn dynamic tier classifier
// call before falling back deterministically to static tier resolution.
var dynamicTierRouterTimeout = 10 * time.Second

const (
	dynamicTierRouterMaxPromptRunes = 4000
	dynamicTierRouterSystemPrompt   = "You are a tier router. Select exactly one tier token from the allowed spawnable tiers list for the given task. Respond with a single JSON object matching {\"tier\": \"<allowed target>\"} and nothing else."
)

type dynamicTierCandidate struct {
	Tier  string
	Label string
}

// maybeRouteDynamicTier resolves an agent.spawn tier dynamically when the
// opt-in agent.tier_routing_dynamic setting is true and the server holds an
// EphemeralPool. On opt-out, timeout, error, invalid JSON, out-of-set output,
// or authority refusal, it returns req.Tier unchanged so existing static tier
// resolution remains the exact fallback and final authority.
func (s *Server) maybeRouteDynamicTier(
	ctx context.Context,
	sessionKey string,
	callerHarness string,
	configOpts wsconfig.Options,
	req wsagent.AgentSpawnRequest,
	loadTiers func() (wsagent.AgentTiers, error),
) string {
	if s.ephemeralPool == nil {
		return req.Tier
	}
	resolver := s.sessionResolver(sessionKey, builtinConfigDefaults())
	enabled, err := resolver.Get(sessionKey, wsconfig.ItemAgentTierRoutingDynamic)
	if err != nil || !strings.EqualFold(strings.TrimSpace(enabled.Value), "true") {
		return req.Tier
	}
	tiers, err := loadTiers()
	if err != nil {
		return req.Tier
	}
	candidates := allowedDynamicTierCandidates(tiers)
	if len(candidates) == 0 {
		return req.Tier
	}
	effectiveHarness := strings.ToLower(strings.TrimSpace(callerHarness))
	if effectiveHarness == "" {
		effectiveHarness = strings.ToLower(strings.TrimSpace(tiers.Harness))
	}

	classifierToken := ""
	if rv, err := resolver.Get(sessionKey, wsconfig.ItemAgentTierRoutingDynamicClassifier); err == nil {
		classifierToken = strings.TrimSpace(rv.Value)
	}
	acquire, ok := s.resolveDynamicClassifier(classifierToken, effectiveHarness, configOpts)
	if !ok {
		return req.Tier
	}

	allowed := make([]string, 0, len(candidates))
	allowedSet := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		allowed = append(allowed, c.Tier)
		allowedSet[c.Tier] = struct{}{}
	}
	schemaRaw, err := dynamicTierResponseSchema(allowed)
	if err != nil {
		return req.Tier
	}

	guidance := ""
	if rv, err := resolver.Get(sessionKey, wsconfig.ItemAgentTierRoutingGuidance); err == nil {
		guidance = strings.TrimSpace(rv.Value)
	}
	prompt := buildDynamicTierPrompt(req, candidates, guidance)
	timeout := dynamicTierRouterTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reply, err := s.ephemeralPool.Execute(ctx, wsagent.EphemeralRequest{
		Acquire:        acquire,
		Prompt:         prompt,
		ResponseSchema: schemaRaw,
		Timeout:        timeout,
	})
	if err != nil {
		return req.Tier
	}
	chosen, ok := parseDynamicTierReply(reply, allowedSet)
	if !ok {
		return req.Tier
	}
	if !s.validateDynamicTierChoice(chosen, effectiveHarness, configOpts) {
		return req.Tier
	}
	return chosen
}

func isRetiredDirectTierToken(token string) bool {
	h, t, dotted := strings.Cut(strings.ToLower(strings.TrimSpace(token)), ".")
	return dotted && wsconfig.NormalizedTier(t) != "" && wsagent.IsRetiredDirectTierHarness(h)
}

// resolveDynamicClassifier resolves the classifier tier token via the static
// tier resolver (mcp/tier_routing.go), honouring CallerRefusal,
// SpawnableHarness, and ProviderGap.
func (s *Server) resolveDynamicClassifier(classifierToken, callerHarness string, configOpts wsconfig.Options) (wsagent.AcquireSpec, bool) {
	classifierToken = strings.TrimSpace(classifierToken)
	if classifierToken == "" {
		classifierToken = "small"
	}
	if isRetiredDirectTierToken(classifierToken) {
		return wsagent.AcquireSpec{}, false
	}
	tierResolver := wsagent.NewTierBindingResolver(configOpts, s.agentBackends()...)
	walkHarness := strings.ToLower(strings.TrimSpace(callerHarness))
	lookupToken := classifierToken
	if size := wsconfig.NormalizedTier(classifierToken); size != "" && walkHarness != "" {
		lookupToken = walkHarness + "." + size
	}
	res, binding, err := s.routedBinding(lookupToken, walkHarness, configOpts, tierResolver)
	if err != nil {
		return wsagent.AcquireSpec{}, false
	}
	runtimeHarness := wsagent.TierRuntimeHarness(res.Harness)
	if runtimeHarness == "" {
		runtimeHarness = strings.TrimSpace(binding.Backend)
	}
	model := strings.TrimSpace(binding.Model)
	if runtimeHarness == "" || model == "" {
		return wsagent.AcquireSpec{}, false
	}
	if !s.ephemeralPool.SupportsHarness(runtimeHarness) {
		return wsagent.AcquireSpec{}, false
	}
	return wsagent.AcquireSpec{
		Harness:      runtimeHarness,
		Model:        model,
		Effort:       strings.TrimSpace(binding.Effort),
		Provider:     strings.TrimSpace(binding.Provider),
		Mode:         wsagent.PureOneShot,
		SystemPrompt: dynamicTierRouterSystemPrompt,
	}, true
}

// validateDynamicTierChoice checks that the classifier's selected tier passes
// static routedBinding (including CallerRefusal, SpawnableHarness, and
// ProviderGap) before handing it to agent.spawn, where the runner also remains
// final authority.
func (s *Server) validateDynamicTierChoice(chosen, callerHarness string, configOpts wsconfig.Options) bool {
	tierResolver := wsagent.NewTierBindingResolver(configOpts, s.agentBackends()...)
	walkHarness := strings.ToLower(strings.TrimSpace(callerHarness))
	lookupToken := strings.TrimSpace(chosen)
	if lookupToken == "" || isRetiredDirectTierToken(lookupToken) {
		return false
	}
	if size := wsconfig.NormalizedTier(lookupToken); size != "" && walkHarness != "" {
		lookupToken = walkHarness + "." + size
	}
	_, _, err := s.routedBinding(lookupToken, walkHarness, configOpts, tierResolver)
	return err == nil
}

func allowedDynamicTierCandidates(tiers wsagent.AgentTiers) []dynamicTierCandidate {
	seen := make(map[string]struct{})
	var out []dynamicTierCandidate
	add := func(tier, label string) {
		tier = strings.TrimSpace(tier)
		if tier == "" {
			return
		}
		if _, dup := seen[tier]; dup {
			return
		}
		seen[tier] = struct{}{}
		out = append(out, dynamicTierCandidate{Tier: tier, Label: label})
	}
	for _, e := range tiers.Native {
		add(e.Tier, formatDynamicCandidateLabel(e.Model, e.Effort, e.Provider))
	}
	for _, e := range tiers.Ext {
		add(e.Tier, formatDynamicCandidateLabel(e.Model, e.Effort, e.Provider))
	}
	for _, a := range tiers.Aliases {
		if a.Error == "" {
			add(a.Alias, formatDynamicCandidateLabel(a.Model, a.Effort, ""))
		}
	}
	if tiers.Routing != nil && tiers.Routing.Error == "" {
		for _, b := range tiers.Routing.Bundles {
			if b.Error == "" {
				add(b.Tier, formatDynamicCandidateLabel(b.Model, b.Effort, b.Provider))
			}
		}
		for _, r := range tiers.Routing.Routes {
			if r.Error == "" {
				add(r.Tier, formatDynamicCandidateLabel(r.Model, r.Effort, r.Provider))
			}
		}
	}
	return out
}

func formatDynamicCandidateLabel(model, effort, provider string) string {
	parts := make([]string, 0, 3)
	if p := strings.TrimSpace(provider); p != "" {
		parts = append(parts, p)
	}
	if m := strings.TrimSpace(model); m != "" {
		parts = append(parts, m)
	}
	if e := strings.TrimSpace(effort); e != "" {
		parts = append(parts, "effort="+e)
	}
	return strings.Join(parts, " ")
}

func dynamicTierResponseSchema(allowed []string) (json.RawMessage, error) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"tier"},
		"properties": map[string]any{
			"tier": map[string]any{
				"type": "string",
				"enum": allowed,
			},
		},
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func buildDynamicTierPrompt(req wsagent.AgentSpawnRequest, candidates []dynamicTierCandidate, guidance string) string {
	var b strings.Builder
	b.WriteString("Choose the best tier token from the allowed spawnable tiers for this agent.spawn request.\n")
	b.WriteString("Return JSON only: {\"tier\": \"<one allowed tier>\"}.\n\n")
	if req.Tier != "" {
		fmt.Fprintf(&b, "Requested tier/role: %s\n", strings.TrimSpace(req.Tier))
	}
	if req.Authority != "" {
		fmt.Fprintf(&b, "Authority: %s\n", strings.TrimSpace(req.Authority))
	}
	if req.Label != "" {
		fmt.Fprintf(&b, "Label: %s\n", strings.TrimSpace(req.Label))
	}
	if guidance != "" {
		fmt.Fprintf(&b, "\nUser routing guidance:\n%s\n", truncateRunes(guidance, 1000))
	}
	b.WriteString("\nAllowed spawnable tiers:\n")
	for _, c := range candidates {
		if c.Label != "" {
			fmt.Fprintf(&b, "- %s (%s)\n", c.Tier, c.Label)
		} else {
			fmt.Fprintf(&b, "- %s\n", c.Tier)
		}
	}
	b.WriteString("\nTask prompt:\n")
	b.WriteString(truncateRunes(strings.TrimSpace(req.Prompt), dynamicTierRouterMaxPromptRunes))
	b.WriteString("\n")
	return b.String()
}

func parseDynamicTierReply(reply string, allowed map[string]struct{}) (string, bool) {
	text := strings.TrimSpace(reply)
	if text == "" {
		return "", false
	}
	var payload struct {
		Tier string `json:"tier"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return "", false
	}
	chosen := strings.TrimSpace(payload.Tier)
	if chosen == "" {
		return "", false
	}
	if _, ok := allowed[chosen]; !ok {
		return "", false
	}
	return chosen, true
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes-1]) + "…"
}
