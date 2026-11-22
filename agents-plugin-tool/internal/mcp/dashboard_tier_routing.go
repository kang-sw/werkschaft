package mcp

import (
	"encoding/json"
	"io/fs"
	"slices"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

// dashboardTierView is the settings editor's picture of the
// agent.tier_routing graph, for the stored value or a draft. Every edge,
// depth, inbound reference, and trace comes from the daemon's graph and
// resolver, so the page lays out and explains what agent.spawn resolves and
// never re-implements resolution.
type dashboardTierView struct {
	// Harness is the caller harness the traces resolve for: the slot a node
	// with no pointer of its own falls through to.
	Harness   string   `json:"harness"`
	Harnesses []string `json:"harnesses"`
	Sizes     []string `json:"sizes"`
	MaxHops   int      `json:"max_hops"`
	// Routing is the value the view describes: the stored overrides, or the
	// draft as parsed (canonical) when it parses.
	Routing wsconfig.TierRouting `json:"routing"`
	// Error is why the value does not parse or validate; a draft with an
	// error is refused on save. Empty when the graph is usable.
	Error      string                `json:"error,omitempty"`
	Collisions []string              `json:"collisions"`
	Nodes      []dashboardTierNode   `json:"nodes"`
	Bundles    []dashboardTierBundle `json:"bundles"`
}

// dashboardTierNode is one routing node. Pointers are effective (packaged
// default overlaid with Overrides); Defaults are the packaged pointers;
// Overrides are the user's own selectors (nil: none stored for the node).
type dashboardTierNode struct {
	Name      string                `json:"name"`
	Kind      wsconfig.TierNodeKind `json:"kind"`
	Size      string                `json:"size,omitempty"`
	Pointers  map[string]string     `json:"pointers"`
	Defaults  map[string]string     `json:"defaults"`
	Overrides map[string]string     `json:"overrides,omitempty"`
	Edges     []dashboardTierEdge   `json:"edges"`
	// Depth is the longest edge path to a terminal: 1 for a node whose
	// every edge ends at a harness slot, a bundle, or the caller fallthrough.
	Depth int `json:"depth"`
	// Inbound names each pointer that targets this node, as "<node> <selector>".
	Inbound []string             `json:"inbound"`
	Routes  []dashboardTierRoute `json:"routes"`
}

// dashboardTierEdge is one outgoing pointer. Kind "caller" is the implicit
// fallthrough a node without a "*" pointer takes: its requested size, as the
// bare size, on the caller's harness (To).
type dashboardTierEdge struct {
	Selector string `json:"selector"`
	Target   string `json:"target,omitempty"`
	Kind     string `json:"kind"`
	To       string `json:"to"`
	Size     string `json:"size,omitempty"`
}

// dashboardTierRoute is one resolution: Size is the requested size ("" for
// a bare bundle name), Resolved the routed size, and Model and Effort the
// binding agent.spawn launches (BindingError when it has none).
type dashboardTierRoute struct {
	Token             string             `json:"token"`
	Size              string             `json:"size,omitempty"`
	Trace             string             `json:"trace,omitempty"`
	Hops              []wsconfig.TierHop `json:"hops,omitempty"`
	Harness           string             `json:"harness,omitempty"`
	Resolved          string             `json:"resolved,omitempty"`
	Bundle            string             `json:"bundle,omitempty"`
	Model             string             `json:"model,omitempty"`
	Effort            string             `json:"effort,omitempty"`
	CallerFallthrough bool               `json:"caller_fallthrough,omitempty"`
	BindingError      string             `json:"binding_error,omitempty"`
	Error             string             `json:"error,omitempty"`
}

type dashboardTierBundle struct {
	Name string `json:"name"`
	wsconfig.TierBundle
	Inbound []string           `json:"inbound"`
	Route   dashboardTierRoute `json:"route"`
}

// dashboardTierRoutingView builds the view for draft (nil: the stored value)
// with traces for harness (empty or unknown: claude, else the first known
// harness). Validation matches the dashboard write rule: packaged roles and
// the built-in harnesses; legacy aliases apply at resolution as they do for
// agent.spawn.
func dashboardTierRoutingView(rsrc fs.FS, opts wsconfig.Options, draft json.RawMessage, harness string) (dashboardTierView, error) {
	roles, err := packagedTierRoles(rsrc)
	if err != nil {
		return dashboardTierView{}, err
	}
	harnesses := wsagent.TierHarnesses()
	if !slices.Contains(harnesses, harness) {
		harness = ""
		if slices.Contains(harnesses, wsagent.BackendClaude) {
			harness = wsagent.BackendClaude
		} else if len(harnesses) > 0 {
			harness = harnesses[0]
		}
	}
	view := dashboardTierView{Harness: harness, Harnesses: harnesses, Sizes: wsconfig.TierSizes, MaxHops: wsconfig.TierRoutingMaxHops, Collisions: []string{}, Nodes: []dashboardTierNode{}, Bundles: []dashboardTierBundle{}}
	var routing wsconfig.TierRouting
	var parseErr error
	if draft == nil {
		routing, parseErr = wsconfig.LoadTierRouting(opts)
	} else {
		routing, parseErr = wsconfig.ParseTierRouting(draft)
	}
	if parseErr != nil {
		view.Error = parseErr.Error()
		routing = wsconfig.TierRouting{}
	}
	view.Routing = routing
	aliases, _ := wsconfig.LoadTierAliases(opts)
	graph := wsconfig.NewTierGraph(roles, routing, aliases, harnesses)
	if view.Error == "" && graph.Err() != nil {
		view.Error = graph.Err().Error()
	}
	if c := graph.Collisions(); c != nil {
		view.Collisions = c
	}
	defaults := map[string]map[string]string{}
	for _, role := range roles {
		if role.Parent != "" {
			defaults[role.Name] = map[string]string{"*": role.Parent}
		}
	}
	bundles := graph.Bundles()
	nodes := graph.Nodes()
	inbound := map[string][]string{}
	index := map[string]int{}
	for i, info := range nodes {
		index[info.Name] = i
		node := dashboardTierNode{Name: info.Name, Kind: info.Kind, Size: info.Size, Pointers: info.Pointers, Defaults: defaults[info.Name], Overrides: routing.Nodes[info.Name], Edges: []dashboardTierEdge{}, Inbound: []string{}, Routes: []dashboardTierRoute{}}
		if node.Pointers == nil {
			node.Pointers = map[string]string{}
		}
		if node.Defaults == nil {
			node.Defaults = map[string]string{}
		}
		for _, selector := range append([]string{"*"}, wsconfig.TierSizes...) {
			target, ok := node.Pointers[selector]
			if !ok {
				if selector == "*" {
					node.Edges = append(node.Edges, dashboardTierEdge{Selector: "*", Kind: "caller", To: harness, Size: "*"})
				}
				continue
			}
			edge := dashboardTierEdge{Selector: selector, Target: target}
			edge.Kind, edge.To, edge.Size = tierTargetShape(target)
			if edge.Kind == "node" || edge.Kind == "bundle" {
				inbound[edge.To] = append(inbound[edge.To], info.Name+" "+selector)
			}
			node.Edges = append(node.Edges, edge)
		}
		view.Nodes = append(view.Nodes, node)
	}
	for i := range view.Nodes {
		if refs := inbound[view.Nodes[i].Name]; refs != nil {
			view.Nodes[i].Inbound = refs
		}
		view.Nodes[i].Depth = tierNodeDepth(view.Nodes, index, i, map[int]bool{})
		for _, size := range wsconfig.TierSizes {
			view.Nodes[i].Routes = append(view.Nodes[i].Routes, dashboardTierResolve(graph, opts, view.Nodes[i].Name+":"+size, size, harness))
		}
	}
	for _, name := range sortedKeys(bundles) {
		b := dashboardTierBundle{Name: name, TierBundle: bundles[name], Inbound: []string{}, Route: dashboardTierResolve(graph, opts, name, "", harness)}
		if refs := inbound[name]; refs != nil {
			b.Inbound = refs
		}
		view.Bundles = append(view.Bundles, b)
	}
	return view, nil
}

// tierTargetShape classifies a pointer target the way wsconfig parses it:
// <node>:<size|*>, <harness>.<size|*>, or a bundle name.
func tierTargetShape(target string) (kind, to, size string) {
	for i := 0; i < len(target); i++ {
		switch target[i] {
		case ':':
			return "node", target[:i], target[i+1:]
		case '.':
			return "harness", target[:i], target[i+1:]
		}
	}
	return "bundle", target, ""
}

// tierNodeDepth is the longest edge path from node i to a terminal. A cycle
// (an invalid draft) stops at the repeated node.
func tierNodeDepth(nodes []dashboardTierNode, index map[string]int, i int, seen map[int]bool) int {
	if seen[i] {
		return 0
	}
	seen[i] = true
	defer delete(seen, i)
	depth := 1
	for _, edge := range nodes[i].Edges {
		if edge.Kind != "node" {
			continue
		}
		if j, ok := index[edge.To]; ok {
			if d := tierNodeDepth(nodes, index, j, seen) + 1; d > depth {
				depth = d
			}
		}
	}
	return depth
}

// dashboardTierResolve resolves token as agent.spawn does for a caller on
// harness, with the binding it would launch.
func dashboardTierResolve(graph *wsconfig.TierGraph, opts wsconfig.Options, token, size, harness string) dashboardTierRoute {
	route := dashboardTierRoute{Token: token, Size: size}
	res, err := graph.Resolve(token, harness)
	if err != nil {
		route.Error = err.Error()
		return route
	}
	route.Trace, route.Hops, route.Harness, route.Resolved, route.Bundle = res.Trace(), res.Hops, res.Harness, res.Size, res.Bundle
	route.CallerFallthrough = res.CallerFallthrough
	binding, err := wsagent.RoutedTierBinding(opts, res)
	if err != nil {
		route.BindingError = err.Error()
	} else {
		route.Model, route.Effort = binding.Model, binding.Effort
	}
	return route
}
