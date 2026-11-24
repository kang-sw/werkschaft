package mcp

import (
	"encoding/json"
	"fmt"
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
	// Revision identifies the stored value this view read
	// (wsconfig.TierRoutingRevision); a save carries it back so the daemon
	// refuses a graph built from a copy that has since changed. Empty for a
	// draft preview.
	Revision string `json:"revision,omitempty"`
	// Error is why the value does not parse or validate; a draft with an
	// error is refused on save. Empty when the graph is usable.
	Error string `json:"error,omitempty"`
	// Unavailable names each harness a route ends on that agent.spawn
	// cannot launch on now, with why (the runner's availability check); a
	// route ending there carries the same reason. Empty without a runner to
	// ask. AvailabilityUnknown names each such harness whose check would
	// need a probe that has not run yet (Pi's version): the view never
	// waits for one.
	Unavailable         map[string]string     `json:"unavailable,omitempty"`
	AvailabilityUnknown map[string]string     `json:"availability_unknown,omitempty"`
	Collisions          []string              `json:"collisions"`
	Nodes               []dashboardTierNode   `json:"nodes"`
	Bundles             []dashboardTierBundle `json:"bundles"`
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
// binding agent.spawn launches (BindingError when it has none). Unavailable
// is why agent.spawn cannot launch the terminal harness now; the route then
// has no binding and BindingError says the same. AvailabilityUnknown says
// the harness's availability is not known yet; the binding is still shown.
type dashboardTierRoute struct {
	Token               string             `json:"token"`
	Size                string             `json:"size,omitempty"`
	Trace               string             `json:"trace,omitempty"`
	Hops                []wsconfig.TierHop `json:"hops,omitempty"`
	Harness             string             `json:"harness,omitempty"`
	Resolved            string             `json:"resolved,omitempty"`
	Bundle              string             `json:"bundle,omitempty"`
	Model               string             `json:"model,omitempty"`
	Effort              string             `json:"effort,omitempty"`
	CallerFallthrough   bool               `json:"caller_fallthrough,omitempty"`
	BindingError        string             `json:"binding_error,omitempty"`
	Unavailable         string             `json:"unavailable,omitempty"`
	AvailabilityUnknown string             `json:"availability_unknown,omitempty"`
	Error               string             `json:"error,omitempty"`
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
// agent.spawn. available is the spawn's harness availability check for an
// interactive view (wsagent Runner.HarnessAvailability: why a harness
// cannot launch now for a caller, "" when it can, known false while that
// would need a probe); it runs once per harness a route ends on, never for
// one no route reaches, with the view's caller harness as the caller (a
// harness that admits only same-harness callers is available in its own
// preview alone), and marks every route ending on an unavailable harness,
// as agent.spawn would refuse it. Nil skips it.
func dashboardTierRoutingView(rsrc fs.FS, opts wsconfig.Options, draft json.RawMessage, harness string, available func(caller, harness string) (string, bool), registries ...map[string]wsagent.BackendRegistration) (dashboardTierView, error) {
	roles, err := packagedTierRoles(rsrc)
	if err != nil {
		return dashboardTierView{}, err
	}
	harnesses := wsagent.TierHarnesses()
	if !slices.Contains(harnesses, harness) {
		harness = ""
		// The first harness is the first built-in registration, which the
		// registration floor keeps first (TestBuiltinBackendRegistrationDefaults).
		if len(harnesses) > 0 {
			harness = harnesses[0]
		}
	}
	view := dashboardTierView{Harness: harness, Harnesses: harnesses, Sizes: wsconfig.TierSizes, MaxHops: wsconfig.TierRoutingMaxHops, Collisions: []string{}, Nodes: []dashboardTierNode{}, Bundles: []dashboardTierBundle{}}
	type availability struct {
		why   string
		known bool
	}
	checked := map[string]availability{}
	check := func(h string) (string, bool) {
		if available == nil || h == "" {
			return "", true
		}
		a, ok := checked[h]
		if !ok {
			a.why, a.known = available(view.Harness, h)
			checked[h] = a
			switch {
			case !a.known:
				if view.AvailabilityUnknown == nil {
					view.AvailabilityUnknown = map[string]string{}
				}
				view.AvailabilityUnknown[h] = fmt.Sprintf("%s availability is not known yet: this daemon has not checked it", h)
			case a.why != "":
				if view.Unavailable == nil {
					view.Unavailable = map[string]string{}
				}
				view.Unavailable[h] = a.why
			}
		}
		return a.why, a.known
	}
	unknown := func(h string) string { return view.AvailabilityUnknown[h] }
	var routing wsconfig.TierRouting
	var parseErr error
	if draft == nil {
		routing, view.Revision, parseErr = wsconfig.LoadTierRoutingRevision(opts)
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
	resolver := wsagent.NewTierBindingResolver(opts, registries...)
	depths := map[int]int{}
	for i := range view.Nodes {
		if refs := inbound[view.Nodes[i].Name]; refs != nil {
			view.Nodes[i].Inbound = refs
		}
		view.Nodes[i].Depth = tierNodeDepth(view.Nodes, index, i, map[int]bool{}, depths)
		for _, size := range wsconfig.TierSizes {
			view.Nodes[i].Routes = append(view.Nodes[i].Routes, dashboardTierResolve(graph, resolver, view.Nodes[i].Name+":"+size, size, harness, check, unknown))
		}
	}
	for _, name := range sortedKeys(bundles) {
		b := dashboardTierBundle{Name: name, TierBundle: bundles[name], Inbound: []string{}, Route: dashboardTierResolve(graph, resolver, name, "", harness, check, unknown)}
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

// tierNodeDepth is the longest edge path from node i to a terminal, capped
// one past the hop limit. A cycle (an invalid draft) stops at the repeated
// node. depths memoizes each node, so the walk is linear in the edges even
// for a densely cross-pointing draft; in a cyclic draft the memo may record
// a depth cut at whichever node closed the cycle first, which only affects
// the column an invalid draft would be drawn in.
func tierNodeDepth(nodes []dashboardTierNode, index map[string]int, i int, seen map[int]bool, depths map[int]int) int {
	if d, ok := depths[i]; ok {
		return d
	}
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
			if d := tierNodeDepth(nodes, index, j, seen, depths) + 1; d > depth {
				depth = d
			}
		}
	}
	if limit := wsconfig.TierRoutingMaxHops + 1; depth > limit {
		depth = limit
	}
	depths[i] = depth
	return depth
}

// dashboardTierResolve resolves token as agent.spawn does for a caller on
// harness, with the binding it would launch: none when check reports that
// the terminal harness cannot launch now, which agent.spawn checks before
// the harness's tier slot. A harness check cannot answer yet keeps its
// binding and carries unknown's note.
func dashboardTierResolve(graph *wsconfig.TierGraph, resolver *wsagent.TierBindingResolver, token, size, harness string, check func(string) (string, bool), unknown func(string) string) dashboardTierRoute {
	route := dashboardTierRoute{Token: token, Size: size}
	res, err := graph.Resolve(token, harness)
	if err != nil {
		route.Error = err.Error()
		return route
	}
	route.Trace, route.Hops, route.Harness, route.Resolved, route.Bundle = res.Trace(), res.Hops, wsagent.TierRuntimeHarness(res.Harness), res.Size, res.Bundle
	route.CallerFallthrough = res.CallerFallthrough
	why, known := check(route.Harness)
	if why != "" {
		route.Unavailable, route.BindingError = why, why
		return route
	}
	if !known {
		route.AvailabilityUnknown = unknown(route.Harness)
	}
	binding, err := resolver.Routed(res)
	if err != nil {
		route.BindingError = err.Error()
	} else if why := resolver.ProviderGap(route.Harness, binding.Provider); why != "" {
		route.Unavailable, route.BindingError = why, why
	} else {
		route.Model, route.Effort = binding.Model, binding.Effort
	}
	return route
}
