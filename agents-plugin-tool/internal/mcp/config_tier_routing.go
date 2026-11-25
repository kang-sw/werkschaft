package mcp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

// tierRoutingValueSchema is the JSON Schema config.list publishes for
// agent.tier_routing's value. wsconfig.ParseTierRouting and the graph
// validation enforce the same rules (and more: reserved names, unknown
// targets, cycles, the hop limit); keep the two in step.
func tierRoutingValueSchema() map[string]any {
	selectors := map[string]any{"*": map[string]any{"type": "string"}}
	for _, size := range wsconfig.TierSizes {
		selectors[size] = map[string]any{"type": "string"}
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"nodes": map[string]any{
				"type":        "object",
				"description": "Pointer overrides keyed by node name: a built-in group, a playbook role, or your own lowercase slug (letters, digits, hyphens). Each override replaces the packaged pointer for that selector only; an empty object defines your own node that falls through to the caller's harness.",
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"description":          "Selector \"*\" is the node's default pointer; small, medium, large, and xlarge are per-size exceptions that win over it.",
					"properties":           selectors,
				},
			},
			"bundles": map[string]any{
				"type":        "object",
				"description": "Named harness | model | effort terminals, keyed by your own lowercase slug. A node points to a bundle by its bare name.",
				"additionalProperties": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"harness", "model"},
					"properties": map[string]any{
						"harness":  map[string]any{"type": "string", "description": "A harness name such as claude or codex."},
						"provider": map[string]any{"type": "string", "description": "Optional werk provider (openrouter, openai, or local/<name>); absent defaults to openrouter for werk and is refused for other harnesses."},
						"model":    map[string]any{"type": "string"},
						"effort":   map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

// tierRoutingGraph is the graph a candidate routing would form: the server's
// packaged roles, the stored legacy aliases, and the harnesses it knows. An
// alias read failure drops the aliases: they never affect validation, only
// the collisions reported beside it.
func (s *Server) tierRoutingGraph(routing wsconfig.TierRouting, opts wsconfig.Options) (*wsconfig.TierGraph, error) {
	root, err := s.rsrcRoot()
	if err != nil {
		return nil, err
	}
	return tierRoutingGraphFor(root, routing, opts, s.agentBackends()...)
}

func tierRoutingGraphFor(root fs.FS, routing wsconfig.TierRouting, opts wsconfig.Options, registries ...map[string]wsagent.BackendRegistration) (*wsconfig.TierGraph, error) {
	roles, err := packagedTierRoles(root)
	if err != nil {
		return nil, fmt.Errorf("read packaged tier roles: %w", err)
	}
	aliases, _ := wsconfig.LoadTierAliases(opts)
	return wsconfig.NewTierGraph(roles, routing, aliases, wsagent.TierHarnesses(registries...)), nil
}

// tierRoutingFamily is config.tune's structured family for the one
// agent.tier_routing key: global-only, validated as a whole graph before any
// write, with the collisions with legacy aliases echoed after it.
func (s *Server) tierRoutingFamily() structuredFamily {
	opts := s.configOptions()
	return structuredFamily{
		prefix:       wsconfig.ItemAgentTierRouting,
		label:        "tier routing",
		noun:         "routing object",
		defaultScope: wsconfig.ScopeGlobal,
		validateName: func(string) error { return nil },
		parse: func(raw json.RawMessage, scope wsconfig.Scope) (any, error) {
			if scope != wsconfig.ScopeGlobal {
				return nil, fmt.Errorf("is global-only; got scope %s", scope)
			}
			routing, err := wsconfig.ParseTierRouting(raw)
			if err != nil {
				return nil, err
			}
			graph, err := s.tierRoutingGraph(routing, opts)
			if err != nil {
				return nil, err
			}
			if err := graph.Err(); err != nil {
				return nil, err
			}
			return routing, nil
		},
		note: func(reset bool) string {
			if reset {
				return "effective: packaged tier routing defaults apply\n" + s.tierCollisionLines()
			}
			return s.tierWriteTraceLines() + s.tierCollisionLines()
		},
	}
}

// tierWriteTraceLines is one "trace: ..." line per route the stored
// routing touches, resolved for this session's harness as agent.spawn
// resolves it: each named node at all four sizes, each named bundle, and
// every listed route whose walk passes one of them. An unreadable graph
// reports nothing here.
func (s *Server) tierWriteTraceLines() string {
	opts := s.configOptions()
	graph, err := s.tierGraph(opts)
	if err != nil || graph.Err() != nil {
		return ""
	}
	routing, err := wsconfig.LoadTierRouting(opts)
	if err != nil {
		return ""
	}
	named := map[string]bool{}
	var tokens []string
	for _, name := range sortedKeys(routing.Bundles) {
		named[name] = true
		tokens = append(tokens, name)
	}
	for _, name := range sortedKeys(routing.Nodes) {
		named[name] = true
		for _, size := range wsconfig.TierSizes {
			tokens = append(tokens, name+":"+size)
		}
	}
	tokens = append(tokens, tierListedTokens(graph)...)
	touches := func(res wsconfig.TierResolution) bool {
		if named[tierTokenNode(res.Token)] {
			return true
		}
		for _, hop := range res.Hops {
			if named[tierTokenNode(hop.To)] {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	for _, line := range tierRouteTraces(graph, s.playbookHarness(), tokens, touches) {
		fmt.Fprintf(&b, "trace: %s\n", line)
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tierTokenNode is the node or bundle name a trace token names.
func tierTokenNode(token string) string {
	name, _, _ := strings.Cut(token, ":")
	return name
}

// tierListedTokens is every bundle and route token agent.tiers lists.
func tierListedTokens(graph *wsconfig.TierGraph) []string {
	return append(sortedKeys(graph.Bundles()), graph.RouteTokens()...)
}

// tierRouteTraces resolves tokens for harness, once each, keeping those
// keep accepts (nil keeps all): the trace, or the token and why it does not
// resolve. A session whose harness is not known yet shows the caller slot
// as <caller>.<size>.
func tierRouteTraces(graph *wsconfig.TierGraph, harness string, tokens []string, keep func(wsconfig.TierResolution) bool) []string {
	if strings.TrimSpace(harness) == "" {
		harness = "<caller>"
	}
	out := []string{}
	seen := map[string]bool{}
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		res, err := graph.Resolve(token, harness)
		if err != nil {
			out = append(out, token+": "+err.Error())
			continue
		}
		if keep == nil || keep(res) {
			out = append(out, res.Trace())
		}
	}
	return out
}

// tierCollisionLines is one "collision: ..." line per place a legacy alias
// overlaps the stored graph. Informational: a collision never fails a write,
// and an unreadable graph reports nothing here (config.list shows why).
func (s *Server) tierCollisionLines() string {
	graph, err := s.tierGraph(s.configOptions())
	if err != nil || graph.Err() != nil {
		return ""
	}
	var b strings.Builder
	for _, line := range graph.Collisions() {
		fmt.Fprintf(&b, "collision: %s\n", line)
	}
	return b.String()
}

// tierRoutingTuningKnob is the config.list knob for agent.tier_routing. A
// stored value that fails to parse or validate surfaces as the knob's error
// rather than failing the whole listing.
func tierRoutingTuningKnob(rsrcRoot fs.FS, configOpts wsconfig.Options, harness string, registries ...map[string]wsagent.BackendRegistration) (configKeyEntry, tuningKnob) {
	entry := registryEntryByKey(wsconfig.ItemAgentTierRouting)
	knob := tuningKnob{
		ID:          entry.Key,
		Kind:        "tier_routing",
		Description: "How a tier name reaches a harness, model, and effort when agent.spawn launches a child. Every tier name is a node with one default pointer and optional per-size exceptions: a playbook role such as ticket-worker:large points to a group (explore, implement, or review), which points to the bare size, which resolves to that slot on the caller's harness. A pointer target is <node>:<size|*>, <harness>.<size|*>, or a bundle name; * carries the requested size through and a size pins it. The value is one object (value_schema): nodes overrides pointers or defines your own nodes, and bundles defines named harness/model/effort terminals. An override replaces only its own selector of the packaged default. A write is refused, and nothing is stored, when a name is reserved, a target is unknown, a node reaches itself at any size, or a path needs more than 8 hops. An agent.tier_aliases entry keeps exact-name precedence; each overlap is listed as a collision after a write. Reset removes the whole value and the packaged defaults apply again. current lists the stored routing, every node's effective pointers, and the resolution trace of every bundle and route on your harness; a write echoes the traces of the routes it touches. Global-only.",
		Writer:      tuningWriter{Tool: entry.WriterTool, FixedArguments: map[string]string{"key": entry.Key}},
		Reset: &tuningWriter{
			Tool:           entry.ResetTool,
			FixedArguments: map[string]string{"key": entry.Key, "reset": "true"},
		},
		SelectorFields: entry.SelectorFields,
		ValueFields:    entry.ValueFields,
		ValueSchema:    tierRoutingValueSchema(),
	}
	roles, err := packagedTierRoles(rsrcRoot)
	if err != nil {
		knob.Error = fmt.Sprintf("read packaged tier roles: %v", err)
		return entry, knob
	}
	graph, err := wsconfig.LoadTierGraph(configOpts, roles, wsagent.TierHarnesses(registries...))
	if err != nil {
		knob.Error = err.Error()
		return entry, knob
	}
	routing := graph.Routing()
	if routing.Nodes == nil && routing.Bundles == nil {
		// A stored value that failed to parse leaves the graph's routing empty.
		if stored, loadErr := wsconfig.LoadTierRouting(configOpts); loadErr == nil {
			routing = stored
		}
	}
	knob.Current = tierRoutingCurrent{Routing: routing, Nodes: graph.Nodes(), Traces: []string{}}
	if err := graph.Err(); err != nil {
		knob.Error = err.Error()
	} else {
		knob.Current = tierRoutingCurrent{Routing: routing, Nodes: graph.Nodes(), Traces: tierRouteTraces(graph, harness, tierListedTokens(graph), nil)}
	}
	return entry, knob
}

// tierRoutingCurrent is config.list's current value for agent.tier_routing.
type tierRoutingCurrent struct {
	Routing wsconfig.TierRouting    `json:"routing"`
	Nodes   []wsconfig.TierNodeInfo `json:"nodes"`
	// Traces resolves every bundle and route for the listing session's
	// harness, as agent.tiers does; empty while the graph is invalid.
	Traces []string `json:"traces"`
}
