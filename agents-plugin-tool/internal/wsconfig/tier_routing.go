package wsconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ItemAgentTierRouting is the tier pointer graph: one structured
// (JSON-object) value in the global config file's structured_overrides
// member. Its "nodes" map re-points tier nodes; its "bundles" map defines
// named {harness, model, effort} terminals. Global-only, like
// agent.tier_aliases, so validation and reset stay atomic across every
// worktree that references a node. Builtin default: no overrides.
const ItemAgentTierRouting = "agent.tier_routing"

func init() {
	RegisterGlobalOnly(ItemAgentTierRouting)
}

// TierRoutingMaxHops bounds one resolution: each pointer, alias, or implicit
// fallthrough transition is one hop, and a path may end after this many.
const TierRoutingMaxHops = 8

// TierSizes are the four canonical tier sizes, in rank order.
var TierSizes = []string{"small", "medium", "large", "xlarge"}

// TierGroups are the built-in default parent nodes a playbook role points
// to by its primary work. Each has no pointer of its own, so it falls
// through to the bare size on the caller's harness.
var TierGroups = []string{"explore", "implement", "review"}

// tierSelectorAll is the default-pointer selector of a node.
const tierSelectorAll = "*"

// legacyTierSpellings are every spelling NormalizedTier accepts; a user
// node or bundle may not take one.
var legacyTierSpellings = []string{"small", "light", "haiku", "medium", "core", "sonnet", "large", "deep", "opus", "xlarge"}

var tierSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// LaunchModelPattern and LaunchEffortPattern are the model and effort
// shapes a spawn accepts (wsagent Runner.Spawn uses them); a bundle is
// checked against them before it is stored, so a saved bundle can launch.
var (
	LaunchModelPattern  = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]~-]{1,128}$`)
	LaunchEffortPattern = regexp.MustCompile(`^[a-z]{1,16}$`)
)

// TierBundle is a named terminal: a concrete harness, model, and effort.
type TierBundle struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
}

// TierRouting is agent.tier_routing's value. Nodes maps a node name to its
// pointer overrides, keyed by "*" (the default pointer) or a canonical size
// (a per-size exception). An entry for a built-in node overlays its
// packaged default per selector; any other entry defines a user node, and
// an empty object makes one that falls through to the caller's harness.
type TierRouting struct {
	Nodes   map[string]map[string]string `json:"nodes,omitempty"`
	Bundles map[string]TierBundle        `json:"bundles,omitempty"`
}

// ParseTierRouting strictly decodes an agent.tier_routing value and checks
// its syntax: selectors, target shapes, name slugs, and bundle fields.
// Whether targets exist, reserved names, cycles, and hop limits need the
// packaged roles and are checked by TierGraph.Validate.
func ParseTierRouting(raw json.RawMessage) (TierRouting, error) {
	var routing TierRouting
	if len(bytes.TrimSpace(raw)) == 0 {
		return routing, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&routing); err != nil {
		return TierRouting{}, fmt.Errorf("%s: %w", ItemAgentTierRouting, err)
	}
	if dec.More() {
		return TierRouting{}, fmt.Errorf("%s: trailing data after the object", ItemAgentTierRouting)
	}
	normalized := TierRouting{}
	if len(routing.Nodes) > 0 {
		normalized.Nodes = map[string]map[string]string{}
	}
	for name, pointers := range routing.Nodes {
		if !tierSlugPattern.MatchString(name) {
			return TierRouting{}, fmt.Errorf("%s: node %q must be a lowercase slug of letters, digits, and hyphens", ItemAgentTierRouting, name)
		}
		if pointers == nil {
			return TierRouting{}, fmt.Errorf("%s: node %q must be an object of pointers ({} for none)", ItemAgentTierRouting, name)
		}
		out := map[string]string{}
		for selector, target := range pointers {
			if selector != tierSelectorAll && !isTierSize(selector) {
				return TierRouting{}, fmt.Errorf("%s: node %q selector %q must be * or one of %s", ItemAgentTierRouting, name, selector, strings.Join(TierSizes, ", "))
			}
			target = strings.TrimSpace(target)
			if _, err := parseTierTarget(target); err != nil {
				return TierRouting{}, fmt.Errorf("%s: node %q selector %q: %w", ItemAgentTierRouting, name, selector, err)
			}
			out[selector] = target
		}
		normalized.Nodes[name] = out
	}
	if len(routing.Bundles) > 0 {
		normalized.Bundles = map[string]TierBundle{}
	}
	for name, bundle := range routing.Bundles {
		if !tierSlugPattern.MatchString(name) {
			return TierRouting{}, fmt.Errorf("%s: bundle %q must be a lowercase slug of letters, digits, and hyphens", ItemAgentTierRouting, name)
		}
		if _, isNode := normalized.Nodes[name]; isNode {
			return TierRouting{}, fmt.Errorf("%s: %q is both a node and a bundle", ItemAgentTierRouting, name)
		}
		bundle.Harness = strings.TrimSpace(bundle.Harness)
		bundle.Model = strings.TrimSpace(bundle.Model)
		bundle.Effort = strings.TrimSpace(bundle.Effort)
		if !tierSlugPattern.MatchString(bundle.Harness) {
			return TierRouting{}, fmt.Errorf("%s: bundle %q needs a harness (a lowercase name such as claude or codex)", ItemAgentTierRouting, name)
		}
		if bundle.Model == "" {
			return TierRouting{}, fmt.Errorf("%s: bundle %q needs a model", ItemAgentTierRouting, name)
		}
		if !LaunchModelPattern.MatchString(bundle.Model) {
			return TierRouting{}, fmt.Errorf("%s: bundle %q model %q must match %s", ItemAgentTierRouting, name, bundle.Model, LaunchModelPattern.String())
		}
		if bundle.Effort != "" && !LaunchEffortPattern.MatchString(bundle.Effort) {
			return TierRouting{}, fmt.Errorf("%s: bundle %q effort %q must be a lowercase word", ItemAgentTierRouting, name, bundle.Effort)
		}
		normalized.Bundles[name] = bundle
	}
	return normalized, nil
}

// tierTargetKind is the shape of a pointer target.
type tierTargetKind int

const (
	tierTargetNode    tierTargetKind = iota // <node>:<size|*>
	tierTargetHarness                       // <harness>.<size|*>
	tierTargetBundle                        // <bundle>
)

// tierTarget is one parsed pointer target. Size is "*" (carry the requested
// size through) or a canonical size (pin it).
type tierTarget struct {
	kind tierTargetKind
	name string
	size string
}

func parseTierTarget(target string) (tierTarget, error) {
	if name, size, ok := strings.Cut(target, ":"); ok {
		if !tierSlugPattern.MatchString(name) || (size != tierSelectorAll && !isTierSize(size)) {
			return tierTarget{}, fmt.Errorf("target %q must be <node>:<size|*> with size small, medium, large, or xlarge", target)
		}
		return tierTarget{kind: tierTargetNode, name: name, size: size}, nil
	}
	if harness, size, ok := strings.Cut(target, "."); ok {
		if !tierSlugPattern.MatchString(harness) || (size != tierSelectorAll && !isTierSize(size)) {
			return tierTarget{}, fmt.Errorf("target %q must be <harness>.<size|*> with size small, medium, large, or xlarge", target)
		}
		return tierTarget{kind: tierTargetHarness, name: harness, size: size}, nil
	}
	if !tierSlugPattern.MatchString(target) {
		return tierTarget{}, fmt.Errorf("target %q must be <node>:<size|*>, <harness>.<size|*>, or a bundle name", target)
	}
	return tierTarget{kind: tierTargetBundle, name: target}, nil
}

func isTierSize(s string) bool {
	for _, size := range TierSizes {
		if s == size {
			return true
		}
	}
	return false
}

// TierRole is one packaged playbook role node: its name, its default
// pointer target, and the size its playbook declares.
type TierRole struct {
	Name   string
	Parent string
	Size   string
}

// TierNodeKind classifies a graph node.
type TierNodeKind string

const (
	TierNodeGroup TierNodeKind = "group"
	TierNodeRole  TierNodeKind = "role"
	TierNodeUser  TierNodeKind = "user"
)

// TierNodeInfo describes one node for listings: its kind, the size its
// playbook declares (roles only), and its effective pointers.
type TierNodeInfo struct {
	Name     string            `json:"name"`
	Kind     TierNodeKind      `json:"kind"`
	Size     string            `json:"size,omitempty"`
	Pointers map[string]string `json:"pointers,omitempty"`
}

// TierGraph is the effective pointer graph: packaged groups and roles with
// user overrides overlaid per selector, user nodes, bundles, and the legacy
// agent.tier_aliases entries, which keep exact-name precedence.
type TierGraph struct {
	nodes    map[string]*tierNode
	bundles  map[string]TierBundle
	aliases  map[string]string
	harness  map[string]bool // known harness names; nil accepts any
	routing  TierRouting
	invalid  error
	parseErr error
}

type tierNode struct {
	kind     TierNodeKind
	size     string
	pointers map[string]string
}

// NewTierGraph overlays routing on the packaged groups and roles. aliases
// is ParseTierAliases's result. knownHarnesses, when non-empty, limits
// harness names in targets and bundles; an unavailable-but-known harness
// stays valid here and reports at resolution. The graph is validated once;
// Err reports the result.
func NewTierGraph(roles []TierRole, routing TierRouting, aliases map[string]string, knownHarnesses []string) *TierGraph {
	g := &TierGraph{nodes: map[string]*tierNode{}, bundles: map[string]TierBundle{}, aliases: aliases, routing: routing}
	if len(knownHarnesses) > 0 {
		g.harness = map[string]bool{}
		for _, h := range knownHarnesses {
			g.harness[strings.ToLower(strings.TrimSpace(h))] = true
		}
	}
	for _, name := range TierGroups {
		g.nodes[name] = &tierNode{kind: TierNodeGroup, pointers: map[string]string{}}
	}
	var errs []string
	for _, role := range roles {
		if _, taken := g.nodes[role.Name]; taken {
			errs = append(errs, fmt.Sprintf("packaged role %q repeats a node name", role.Name))
			continue
		}
		node := &tierNode{kind: TierNodeRole, size: NormalizedTier(role.Size), pointers: map[string]string{}}
		if p := strings.TrimSpace(role.Parent); p != "" {
			node.pointers[tierSelectorAll] = p
		}
		g.nodes[role.Name] = node
	}
	for name, pointers := range routing.Nodes {
		node, builtin := g.nodes[name]
		if !builtin {
			node = &tierNode{kind: TierNodeUser, pointers: map[string]string{}}
			g.nodes[name] = node
		}
		for selector, target := range pointers {
			node.pointers[selector] = target
		}
	}
	for name, bundle := range routing.Bundles {
		g.bundles[name] = bundle
	}
	if len(errs) > 0 {
		g.invalid = fmt.Errorf("%s: %s", ItemAgentTierRouting, strings.Join(errs, "; "))
	} else {
		g.invalid = g.validate()
	}
	return g
}

// newTierGraphParseError is a graph whose stored value failed to parse:
// role and bundle tokens refuse with err, legacy tokens still resolve.
func newTierGraphParseError(roles []TierRole, aliases map[string]string, knownHarnesses []string, err error) *TierGraph {
	g := NewTierGraph(roles, TierRouting{}, aliases, knownHarnesses)
	g.parseErr = err
	return g
}

// Err reports why the graph is unusable: a stored value that does not
// parse, or one that fails validation. Legacy tokens resolve regardless.
func (g *TierGraph) Err() error {
	if g.parseErr != nil {
		return g.parseErr
	}
	return g.invalid
}

// Routing is the user overrides the graph overlays.
func (g *TierGraph) Routing() TierRouting { return g.routing }

func (g *TierGraph) reserved(name string) (string, bool) {
	for _, s := range legacyTierSpellings {
		if name == s {
			return "a tier spelling", true
		}
	}
	if node, ok := g.nodes[name]; ok && node.kind != TierNodeUser {
		return "a built-in node", true
	}
	return "", false
}

func (g *TierGraph) knownHarness(h string) bool {
	return g.harness == nil || g.harness[h]
}

// Older daemons reject the entire saved graph if a target names werk.
func (g *TierGraph) validateStoredHarness(harness string) error {
	if harness == "werk" {
		return fmt.Errorf("harness %q is available through direct tiers only", harness)
	}
	if !g.knownHarness(harness) {
		return fmt.Errorf("unknown harness %q", harness)
	}
	return nil
}

// validate checks names, targets, acyclicity over every pointer edge of any
// selector, and the hop limit at all four sizes from every node. Legacy
// aliases are ignored: they only shorten a path, and the graph must stay
// valid after any alias is reset.
func (g *TierGraph) validate() error {
	for name := range g.routing.Nodes {
		if g.nodes[name].kind == TierNodeUser {
			if why, bad := g.reserved(name); bad {
				return fmt.Errorf("%s: node %q is %s; pick another name", ItemAgentTierRouting, name, why)
			}
		}
	}
	for _, name := range sortedKeys(g.bundles) {
		if why, bad := g.reserved(name); bad {
			return fmt.Errorf("%s: bundle %q is %s; pick another name", ItemAgentTierRouting, name, why)
		}
		if _, isNode := g.nodes[name]; isNode {
			return fmt.Errorf("%s: bundle %q has a node's name", ItemAgentTierRouting, name)
		}
		if err := g.validateStoredHarness(g.bundles[name].Harness); err != nil {
			return fmt.Errorf("%s: bundle %q: %w", ItemAgentTierRouting, name, err)
		}
	}
	names := sortedKeys(g.nodes)
	for _, name := range names {
		node := g.nodes[name]
		for _, selector := range sortedKeys(node.pointers) {
			target, err := parseTierTarget(node.pointers[selector])
			if err != nil {
				return fmt.Errorf("%s: node %q selector %q: %w", ItemAgentTierRouting, name, selector, err)
			}
			switch target.kind {
			case tierTargetNode:
				if _, ok := g.nodes[target.name]; !ok {
					return fmt.Errorf("%s: node %q selector %q points to unknown node %q", ItemAgentTierRouting, name, selector, target.name)
				}
			case tierTargetHarness:
				if err := g.validateStoredHarness(target.name); err != nil {
					return fmt.Errorf("%s: node %q selector %q: %w", ItemAgentTierRouting, name, selector, err)
				}
			case tierTargetBundle:
				if _, ok := g.bundles[target.name]; !ok {
					return fmt.Errorf("%s: node %q selector %q points to unknown bundle %q", ItemAgentTierRouting, name, selector, target.name)
				}
			}
		}
	}
	if cycle := g.findCycle(names); cycle != nil {
		return fmt.Errorf("%s: pointer cycle %s; a node may not reach itself at any size", ItemAgentTierRouting, strings.Join(cycle, " -> "))
	}
	for _, name := range names {
		for _, size := range TierSizes {
			if _, err := g.walk(name, size, "", false); err != nil {
				return err
			}
		}
	}
	return nil
}

// findCycle runs a depth-first search over node edges of every selector and
// returns the first cycle found as a closed node list, or nil.
func (g *TierGraph) findCycle(names []string) []string {
	const (
		unvisited = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(string) []string
	visit = func(name string) []string {
		state[name] = active
		stack = append(stack, name)
		node := g.nodes[name]
		for _, selector := range sortedKeys(node.pointers) {
			target, err := parseTierTarget(node.pointers[selector])
			if err != nil || target.kind != tierTargetNode {
				continue
			}
			switch state[target.name] {
			case active:
				for i, n := range stack {
					if n == target.name {
						return append(append([]string{}, stack[i:]...), target.name)
					}
				}
			case unvisited:
				if c := visit(target.name); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		return nil
	}
	for _, name := range names {
		if state[name] == unvisited {
			if c := visit(name); c != nil {
				return c
			}
		}
	}
	return nil
}

// TierHop is one transition of a resolution. Via says what decided it:
// "alias" (an exact agent.tier_aliases entry), "exact" (a per-size
// pointer), "default" (the "*" pointer), "fallthrough" (no pointer: the
// bare size), or "caller" (the bare size's slot on the caller's harness).
type TierHop struct {
	From string `json:"from"`
	To   string `json:"to"`
	Via  string `json:"via"`
}

// TierResolution is where a tier token lands. A slot terminal sets Harness
// and Size; a bundle terminal sets Bundle, Harness, Model, and Effort, with
// Size the routed size ("" for a bare bundle name).
type TierResolution struct {
	Token   string    `json:"token"`
	Harness string    `json:"harness"`
	Size    string    `json:"size,omitempty"`
	Bundle  string    `json:"bundle,omitempty"`
	Model   string    `json:"model,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Hops    []TierHop `json:"hops,omitempty"`
	// CallerFallthrough is set when the terminal slot came from the bare
	// size on the caller's harness.
	CallerFallthrough bool `json:"caller_fallthrough,omitempty"`
}

// Rank is the size spawn admission and the child's recorded tier use: the
// resolved size, or xlarge for a bare bundle name, which supplies none.
func (r TierResolution) Rank() string {
	if r.Size == "" {
		return "xlarge"
	}
	return r.Size
}

// Terminal is the resolution's end as written in a trace.
func (r TierResolution) Terminal() string {
	if r.Bundle != "" {
		return r.Bundle
	}
	return r.Harness + "." + r.Size
}

// Trace renders the path as "a -> b -> c", marking an alias hop.
func (r TierResolution) Trace() string {
	parts := []string{r.Token}
	for _, hop := range r.Hops {
		step := hop.To
		if hop.Via == "alias" {
			step += " [alias " + hop.From + "]"
		}
		parts = append(parts, step)
	}
	return strings.Join(parts, " -> ")
}

// IsRoutedTierToken reports whether token is a routing-graph token rather
// than a bare size or an explicit <harness>.<size>: a <node>:<size> role
// tier or a bundle-shaped name. An alias may share that shape; resolution
// checks aliases first.
func IsRoutedTierToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" || strings.Contains(token, ".") || NormalizedTier(token) != "" {
		return false
	}
	return true
}

// Resolve resolves token for a caller on callerHarness. Order: an explicit
// <harness>.<size>; a bare size on the caller's harness; an exact legacy
// alias; then a <node>:<size> role tier walked through the graph, or a
// bundle name. A role or bundle token on an unusable graph fails with the
// graph's error.
func (g *TierGraph) Resolve(token, callerHarness string) (TierResolution, error) {
	raw := strings.ToLower(strings.TrimSpace(token))
	res := TierResolution{Token: raw}
	if raw == "" {
		return res, fmt.Errorf("tier is required")
	}
	if h, t, dotted := strings.Cut(raw, "."); dotted {
		size := NormalizedTier(t)
		if h == "" || size == "" {
			return res, fmt.Errorf("tier %q names no tier (small, medium, large, xlarge)", token)
		}
		res.Harness, res.Size = h, size
		return res, nil
	}
	if size := NormalizedTier(raw); size != "" {
		res.Harness, res.Size, res.CallerFallthrough = callerHarness, size, true
		res.Hops = []TierHop{{From: raw, To: callerHarness + "." + size, Via: "caller"}}
		return res, nil
	}
	if target, ok := g.aliases[raw]; ok {
		h, t, _ := strings.Cut(target, ".")
		res.Harness, res.Size = h, t
		res.Hops = []TierHop{{From: raw, To: target, Via: "alias"}}
		return res, nil
	}
	if err := g.Err(); err != nil {
		return res, err
	}
	if name, size, ok := strings.Cut(raw, ":"); ok {
		if _, known := g.nodes[name]; !known {
			return res, fmt.Errorf("tier %q: %q is not a tier node (built-in groups %s, playbook roles, or a node in %s)", token, name, strings.Join(TierGroups, ", "), ItemAgentTierRouting)
		}
		canonical := NormalizedTier(size)
		if canonical == "" {
			return res, fmt.Errorf("tier %q: %q is not a size (small, medium, large, xlarge)", token, size)
		}
		walked, err := g.walk(name, canonical, callerHarness, true)
		if err != nil {
			return res, err
		}
		walked.Token = raw
		return walked, nil
	}
	if bundle, ok := g.bundles[raw]; ok {
		res.Bundle, res.Harness, res.Model, res.Effort = raw, bundle.Harness, bundle.Model, bundle.Effort
		return res, nil
	}
	return res, fmt.Errorf("tier %q is neither a tier, <harness>.<tier>, nor an alias in %s, and no bundle in %s", token, ItemAgentTierAliases, ItemAgentTierRouting)
}

// walk follows pointers from node at size. useAliases applies exact legacy
// aliases at each node (resolution); validation walks without them.
func (g *TierGraph) walk(name, size, callerHarness string, useAliases bool) (TierResolution, error) {
	res := TierResolution{Token: name + ":" + size}
	hop := func(from, to, via string) error {
		res.Hops = append(res.Hops, TierHop{From: from, To: to, Via: via})
		if len(res.Hops) > TierRoutingMaxHops {
			return fmt.Errorf("%s: %s needs more than %d hops (%s); shorten the pointer chain", ItemAgentTierRouting, res.Token, TierRoutingMaxHops, res.Trace())
		}
		return nil
	}
	for {
		here := name + ":" + size
		if useAliases {
			if target, ok := g.aliases[here]; ok {
				if err := hop(here, target, "alias"); err != nil {
					return res, err
				}
				h, t, _ := strings.Cut(target, ".")
				res.Harness, res.Size = h, t
				return res, nil
			}
		}
		node := g.nodes[name]
		pointer, via := node.pointers[size], "exact"
		if pointer == "" {
			pointer, via = node.pointers[tierSelectorAll], "default"
		}
		if pointer == "" {
			if err := hop(here, size, "fallthrough"); err != nil {
				return res, err
			}
			if err := hop(size, callerHarness+"."+size, "caller"); err != nil {
				return res, err
			}
			res.Harness, res.Size, res.CallerFallthrough = callerHarness, size, true
			return res, nil
		}
		target, err := parseTierTarget(pointer)
		if err != nil {
			return res, fmt.Errorf("%s: node %q: %w", ItemAgentTierRouting, name, err)
		}
		next := size
		if target.size != "" && target.size != tierSelectorAll {
			next = target.size
		}
		switch target.kind {
		case tierTargetHarness:
			if err := hop(here, target.name+"."+next, via); err != nil {
				return res, err
			}
			res.Harness, res.Size = target.name, next
			return res, nil
		case tierTargetBundle:
			if err := hop(here, target.name, via); err != nil {
				return res, err
			}
			bundle := g.bundles[target.name]
			res.Bundle, res.Harness, res.Model, res.Effort, res.Size = target.name, bundle.Harness, bundle.Model, bundle.Effort, size
			return res, nil
		default:
			if err := hop(here, target.name+":"+next, via); err != nil {
				return res, err
			}
			if _, ok := g.nodes[target.name]; !ok {
				return res, fmt.Errorf("%s: node %q points to unknown node %q", ItemAgentTierRouting, name, target.name)
			}
			name, size = target.name, next
		}
	}
}

// Nodes lists every node, sorted: groups, then roles, then user nodes.
func (g *TierGraph) Nodes() []TierNodeInfo {
	order := map[TierNodeKind]int{TierNodeGroup: 0, TierNodeRole: 1, TierNodeUser: 2}
	out := make([]TierNodeInfo, 0, len(g.nodes))
	for name, node := range g.nodes {
		info := TierNodeInfo{Name: name, Kind: node.kind, Size: node.size}
		if len(node.pointers) > 0 {
			info.Pointers = map[string]string{}
			for k, v := range node.pointers {
				info.Pointers[k] = v
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Bundles lists the named bundles by name.
func (g *TierGraph) Bundles() map[string]TierBundle {
	out := make(map[string]TierBundle, len(g.bundles))
	for k, v := range g.bundles {
		out[k] = v
	}
	return out
}

// RouteTokens is the routed tier set the resolution traces list, in
// Nodes order: each role at the size its playbook declares (all four when
// it declares none) and each group and user node at all four sizes.
// Bundles are listed by Bundles.
func (g *TierGraph) RouteTokens() []string {
	var out []string
	for _, node := range g.Nodes() {
		if node.Kind == TierNodeRole && node.Size != "" {
			out = append(out, node.Name+":"+node.Size)
			continue
		}
		for _, size := range TierSizes {
			out = append(out, node.Name+":"+size)
		}
	}
	return out
}

// Collisions names each legacy alias that shadows a graph name: an alias
// equal to a bundle or user-node name wins over it as a token, and an
// alias named <node>:<size> wins at that node and size.
func (g *TierGraph) Collisions() []string {
	var out []string
	for _, alias := range sortedKeys(g.aliases) {
		target := g.aliases[alias]
		if _, ok := g.bundles[alias]; ok {
			out = append(out, fmt.Sprintf("alias %s -> %s shadows bundle %s", alias, target, alias))
		}
		if _, ok := g.nodes[alias]; ok {
			out = append(out, fmt.Sprintf("alias %s -> %s shares node %s's name", alias, target, alias))
		}
		if name, size, ok := strings.Cut(alias, ":"); ok {
			if _, known := g.nodes[name]; known && isTierSize(size) {
				out = append(out, fmt.Sprintf("alias %s -> %s takes precedence over node %s's pointers at size %s", alias, target, name, size))
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LoadTierRouting reads agent.tier_routing from the global config file; a
// project or repo file never supplies it. Absent is the zero value.
func LoadTierRouting(opts Options) (TierRouting, error) {
	routing, _, err := LoadTierRoutingRevision(opts)
	return routing, err
}

// LoadTierRoutingRevision is LoadTierRouting plus the stored value's
// revision (TierRoutingRevision) from the same read. The revision is set
// whenever the file was read, even when the stored value does not parse.
func LoadTierRoutingRevision(opts Options) (TierRouting, string, error) {
	cfg, err := loadGlobalConfig(opts)
	if err != nil {
		return TierRouting{}, "", err
	}
	revision := TierRoutingRevision(cfg)
	raw, ok := cfg.StructuredOverrides[ItemAgentTierRouting]
	if !ok {
		return TierRouting{}, revision, nil
	}
	routing, err := ParseTierRouting(raw)
	return routing, revision, err
}

// TierRoutingRevision identifies the agent.tier_routing value stored in cfg
// (the global file's layer): a hash of its compacted bytes, or of nothing
// when no value is stored. A whole-graph writer that edited a value read at
// one revision compares it with the current one before it writes, so an
// edit made from a stale copy cannot silently replace a newer graph.
func TierRoutingRevision(cfg Config) string {
	var compacted bytes.Buffer
	if raw, ok := cfg.StructuredOverrides[ItemAgentTierRouting]; ok {
		compacted.WriteByte('=')
		if json.Compact(&compacted, raw) != nil {
			compacted.Write(raw)
		}
	}
	sum := sha256.Sum256(compacted.Bytes())
	return hex.EncodeToString(sum[:16])
}

// ErrTierRoutingStale refuses a whole-graph write whose base revision no
// longer names the stored agent.tier_routing value.
var ErrTierRoutingStale = errors.New("agent.tier_routing changed since the edited copy was read")

// TierRoutingAt is the precondition a whole-graph writer passes to
// SetStructuredIf / UnsetStructuredIf: the global file, read under its lock,
// must still hold the value at revision, or the write is refused with
// ErrTierRoutingStale.
func TierRoutingAt(revision string) StructuredPrecondition {
	return func(current Config) error {
		if TierRoutingRevision(current) != revision {
			return ErrTierRoutingStale
		}
		return nil
	}
}

// LoadTierAliases reads agent.tier_aliases (global-only) for opts.
func LoadTierAliases(opts Options) (map[string]string, error) {
	resolver := NewResolver(opts, map[string]string{ItemAgentTierAliases: ""}, nil, nil)
	raw, err := resolver.Get("", ItemAgentTierAliases)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ItemAgentTierAliases, err)
	}
	return ParseTierAliases(raw.Value)
}

// LoadTierGraph builds the effective graph for opts. An alias read failure
// is an error; a routing value that fails to parse yields a graph whose Err
// reports it, so legacy tokens keep resolving.
func LoadTierGraph(opts Options, roles []TierRole, knownHarnesses []string) (*TierGraph, error) {
	aliases, err := LoadTierAliases(opts)
	if err != nil {
		return nil, err
	}
	routing, err := LoadTierRouting(opts)
	if err != nil {
		return newTierGraphParseError(roles, aliases, knownHarnesses, err), nil
	}
	return NewTierGraph(roles, routing, aliases, knownHarnesses), nil
}
