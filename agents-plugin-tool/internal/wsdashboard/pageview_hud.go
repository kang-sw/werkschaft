package wsdashboard

import (
	"slices"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

const hudMaxDepth = 4

// HUDView is the serialized hierarchical org-chart projection embedded in
// #r-tree .ag-source for the floating compact HUD.
type HUDView struct {
	Lead      string `json:"lead,omitempty"`
	Completed int    `json:"completed"`
	Folded    int    `json:"folded,omitempty"`
	// ObservationEnded counts the folded native rows whose observation ended
	// with their outcome unknown (hudEndUnknown): they are never counted as
	// completed, so the page and the companion relay never read them as a
	// finish.
	ObservationEnded int       `json:"observationEnded,omitempty"`
	Peers            int       `json:"peers"`
	Roots            []HUDNode `json:"roots"`
}

// HUDNode is one node in the compact horizontal org-chart HUD.
type HUDNode struct {
	ID          string    `json:"id"`
	ReplyID     string    `json:"replyId,omitempty"`
	Linked      bool      `json:"linked,omitempty"`
	Label       string    `json:"label"`
	Harness     string    `json:"harness"`
	State       string    `json:"state"`
	CloseActive bool      `json:"closeActive,omitempty"`
	Model       string    `json:"model,omitempty"`
	Effort      string    `json:"effort,omitempty"`
	ElapsedMs   int64     `json:"elapsedMs,omitempty"`
	ActiveSince int64     `json:"activeSince,omitempty"`
	Chain       []string  `json:"chain,omitempty"`
	Task        string    `json:"task,omitempty"`
	Overflow    int       `json:"overflow,omitempty"`
	Children    []HUDNode `json:"children,omitempty"`
	// Native marks a harness's own subagent (hudNative): it shows a native
	// badge and its lifecycle, and never links to a transcript. StartedAt,
	// EndedAt (epoch ms), and EndUnknown are a native row's alone, so every
	// other row's JSON stays as before. EndUnknown marks a row whose
	// observation ended with the outcome unknown (hudEndUnknown): it reads as
	// such, never as a completion, and carries no EndedAt, since its end is
	// not a known one.
	Native     bool  `json:"native,omitempty"`
	StartedAt  int64 `json:"startedAt,omitempty"`
	EndedAt    int64 `json:"endedAt,omitempty"`
	EndUnknown bool  `json:"endUnknown,omitempty"`
	// OwnRow marks nodes rendered in #r-tree's .agent list so hudJSON can omit
	// their Task from .ag-source (keeping tree-rows shell invariant on caption edits).
	OwnRow bool `json:"-"`
}

type hudRawNode struct {
	agent    wslive.Agent
	linked   bool
	ownRow   bool
	native   bool
	children []*hudRawNode
}

// hudFold is the tally of folded rows: completed ones, native rows whose
// observation ended with the outcome unknown, and the folded top-level rows.
type hudFold struct{ completed, observationEnded, roots int }

func (f *hudFold) add(n *hudRawNode) {
	if hudEndUnknown(n.agent, n.native) {
		f.observationEnded++
	} else {
		f.completed++
	}
}

// hudProjection builds the compact HUD forest from the session's own agents and
// any live linked delegate subtrees, keeping own-snapshot ParentID placement
// authoritative on duplicate agent IDs and capping depth at hudMaxDepth.
func hudProjection(self, leadLabel, leadHarness string, agents []wslive.Agent, peers int, lookup sessionLookup, now time.Time) HUDView {
	out := HUDView{
		Lead:  leadLabel,
		Peers: peers,
		Roots: []HUDNode{},
	}
	seenIDs := map[string]bool{}
	roots := buildHUDRawForest(agents, true, leadHarness, seenIDs, lookup)
	visitedSessions := map[string]bool{self: true}
	expandHUDLinked(roots, seenIDs, visitedSessions, lookup, now)

	visible := map[*hudRawNode]bool{}
	var mark func(*hudRawNode, int) bool
	mark = func(n *hudRawNode, depth int) bool {
		st := hudNodeState(n.agent)
		vis := st != "idle" || (depth > 1 && n.agent.Alive)
		for _, c := range n.children {
			if mark(c, depth+1) {
				vis = true
			}
		}
		visible[n] = vis
		return vis
	}
	for _, r := range roots {
		mark(r, 1)
	}
	var fold hudFold
	out.Roots = projectHUDNodes(roots, 1, normalizeHUDHarness(leadHarness), nil, visible, &fold, now)
	out.Completed, out.Folded, out.ObservationEnded = fold.completed, fold.roots, fold.observationEnded
	return out
}

// buildHUDRawForest builds the raw forest of one snapshot's agents;
// ownerHarness is the harness of the session whose snapshot it is, which
// decides a Claude lead's native rows (hudNative).
func buildHUDRawForest(agents []wslive.Agent, ownRow bool, ownerHarness string, seenIDs map[string]bool, lookup sessionLookup) []*hudRawNode {
	present := map[string]bool{}
	for _, a := range agents {
		if !seenIDs[a.ID] {
			present[a.ID] = true
		}
	}
	children := map[string][]wslive.Agent{}
	var roots []wslive.Agent
	for _, a := range agents {
		if seenIDs[a.ID] {
			continue
		}
		if a.ParentID != "" && a.ParentID != a.ID && present[a.ParentID] {
			children[a.ParentID] = append(children[a.ParentID], a)
		} else {
			roots = append(roots, a)
		}
	}
	var build func([]wslive.Agent) []*hudRawNode
	build = func(list []wslive.Agent) []*hudRawNode {
		var out []*hudRawNode
		for _, a := range list {
			if seenIDs[a.ID] {
				continue
			}
			seenIDs[a.ID] = true
			node := &hudRawNode{agent: a, ownRow: ownRow, native: hudNative(a, ownerHarness)}
			if a.ReplyID != "" && !node.native {
				if s, ok := lookup.get(a.ReplyID); ok && (s.HasBoard || s.Agent) {
					node.linked = true
				}
			}
			node.children = build(children[a.ID])
			out = append(out, node)
		}
		return out
	}
	return build(roots)
}

func expandHUDLinked(nodes []*hudRawNode, seenIDs, visited map[string]bool, lookup sessionLookup, now time.Time) {
	for _, n := range nodes {
		expandHUDLinked(n.children, seenIDs, visited, lookup, now)
		rid := n.agent.ReplyID
		if rid == "" || visited[rid] || !hudShouldExpandLinked(n.agent) {
			continue
		}
		visited[rid] = true
		if _, ok := lookup.get(rid); !ok {
			continue
		}
		if snap := lookup.snap(rid); adapterPresent(snap, now) {
			attached := buildHUDRawForest(snap.Agents, false, n.agent.Harness, seenIDs, lookup)
			expandHUDLinked(attached, seenIDs, visited, lookup, now)
			n.children = append(n.children, attached...)
		}
	}
}

func hudShouldExpandLinked(a wslive.Agent) bool {
	return a.Status == wslive.StatusRunning || a.Holding || a.Alive
}

func projectHUDNodes(nodes []*hudRawNode, depth int, parentHarness string, chain []string, visible map[*hudRawNode]bool, fold *hudFold, now time.Time) []HUDNode {
	out := []HUDNode{}
	for _, n := range nodes {
		if !visible[n] {
			foldHUDRawSubtree(n, fold)
			// The folded chip reads as finished agents: an end-unknown
			// native row is never one.
			if depth == 1 && !hudEndUnknown(n.agent, n.native) {
				fold.roots++
			}
			continue
		}
		a := n.agent
		harness := resolveHUDHarness(a, parentHarness)
		label := a.Label
		if label == "" {
			label = shortID(a.ID)
		}
		an := AgentNode{Agent: a}
		node := HUDNode{
			ID:          a.ID,
			ReplyID:     a.ReplyID,
			Linked:      n.linked,
			Label:       label,
			Harness:     harness,
			State:       hudNodeState(a),
			CloseActive: a.Status == wslive.StatusRunning || a.Holding || a.Permission != nil,
			Model:       an.DisplayModel(),
			Effort:      a.Effort,
			ElapsedMs:   max(a.ActiveMs, 0),
			ActiveSince: an.ActiveSince(),
			Chain:       slices.Clone(chain),
			Task:        hudNodeTask(a),
			OwnRow:      n.ownRow,
		}
		if n.native {
			node.Native, node.StartedAt = true, max(a.StartedAt, 0)
			if hudEndUnknown(a, true) {
				node.EndUnknown = true
			} else {
				node.EndedAt = max(a.EndedAt, 0)
			}
		}
		if depth < hudMaxDepth {
			nextChain := append(slices.Clone(chain), label)
			kids := projectHUDNodes(n.children, depth+1, harness, nextChain, visible, fold, now)
			if len(kids) > 0 {
				node.Children = kids
			}
		} else {
			node.Overflow = foldHUDDepthOverflow(n.children, visible, fold)
		}
		out = append(out, node)
	}
	return out
}

func foldHUDRawSubtree(n *hudRawNode, fold *hudFold) {
	fold.add(n)
	for _, c := range n.children {
		foldHUDRawSubtree(c, fold)
	}
}

func foldHUDDepthOverflow(nodes []*hudRawNode, visible map[*hudRawNode]bool, fold *hudFold) int {
	overflow := 0
	for _, n := range nodes {
		if visible[n] {
			overflow++
		} else {
			fold.add(n)
		}
		overflow += foldHUDDepthOverflow(n.children, visible, fold)
	}
	return overflow
}

// hudNative reports a harness's own subagent: a row whose id carries a
// native prefix (wslive.NativeAgentID), or a row in a Claude session's
// snapshot without a daemon identity (daemon-spawned rows always carry a
// harness, a reply-id, or an agent kind). ownerHarness is the harness of the
// session whose snapshot holds the row.
func hudNative(a wslive.Agent, ownerHarness string) bool {
	if wslive.NativeAgentID(a.ID) {
		return true
	}
	return normalizeHUDHarness(ownerHarness) == "claude" && a.Harness == "" && a.ReplyID == "" && a.AgentKind == ""
}

// hudEndUnknown reports a native row whose observation ended with the
// outcome unknown (wslive.Agent.EndUnknown; an agy native subagent at its
// parent's turn end): it never reads as a completion. Its EndedAt is when
// observation ended, which only ranks it for retention.
func hudEndUnknown(a wslive.Agent, native bool) bool {
	return native && a.EndUnknown && (a.Status == wslive.StatusDone || a.Status == wslive.StatusFailed)
}

func hudNodeState(a wslive.Agent) string {
	switch {
	case a.Permission != nil:
		return "permission"
	case a.Status == wslive.StatusFailed:
		return "failed"
	case a.Status == wslive.StatusRunning:
		if a.Phase == wslive.PhaseTool || a.Phase == wslive.PhaseReplying {
			return "tool"
		}
		return "thinking"
	case a.Holding:
		return "holding"
	default:
		return "idle"
	}
}

func normalizeHUDHarness(h string) string {
	switch strings.ToLower(strings.TrimSpace(h)) {
	case "claude", "codex", "agy", "pi":
		return strings.ToLower(strings.TrimSpace(h))
	default:
		return ""
	}
}

func resolveHUDHarness(a wslive.Agent, parentHarness string) string {
	if h := normalizeHUDHarness(a.Harness); h != "" {
		return h
	}
	if parentHarness != "" {
		return parentHarness
	}
	if a.AgentKind == wslive.AgentKindPi {
		return "pi"
	}
	return ""
}

func hudNodeTask(a wslive.Agent) string {
	if a.Permission != nil {
		return formatHUDToolTask(a.Permission.ToolName, a.Permission.ArgsPreview)
	}
	_, running, generating := splitBatches(a.Batches)
	if running != nil {
		return formatHUDToolTask(running.ToolName, running.ArgsPreview)
	}
	if generating != nil && generating.ToolName != "" {
		return toolName(generating.ToolName)
	}
	return a.Caption
}

func formatHUDToolTask(name, args string) string {
	short := toolName(name)
	args = strings.TrimSpace(args)
	if args == "" {
		return short
	}
	if short == "" {
		return args
	}
	return short + " " + args
}
