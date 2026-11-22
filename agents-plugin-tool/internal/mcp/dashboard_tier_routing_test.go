package mcp

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wspackage"
)

func dashboardTierFixture(t *testing.T) (fs.FS, wsconfig.Options) {
	t.Helper()
	t.Setenv(wspackage.EnvDevCheckout, "")
	pkg, err := wspackage.Open("ws")
	if err != nil {
		t.Fatal(err)
	}
	rsrc, err := fs.Sub(pkg, "rsrc")
	if err != nil {
		t.Fatal(err)
	}
	return rsrc, wsconfig.Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
}

func tierViewNode(t *testing.T, view dashboardTierView, name string) dashboardTierNode {
	t.Helper()
	for _, n := range view.Nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no node %q in view", name)
	return dashboardTierNode{}
}

func tierViewRoute(t *testing.T, node dashboardTierNode, size string) dashboardTierRoute {
	t.Helper()
	for _, r := range node.Routes {
		if r.Size == size {
			return r
		}
	}
	t.Fatalf("node %s has no %s route", node.Name, size)
	return dashboardTierRoute{}
}

// The editor's graph is the daemon's: with no user config, a group has no
// pointer of its own and its only edge is the implicit fallthrough to the
// caller's harness, which the traces name for the requested caller.
func TestDashboardTierRoutingViewDefaultChainAndCallerFallthrough(t *testing.T) {
	rsrc, opts := dashboardTierFixture(t)
	view, err := dashboardTierRoutingView(rsrc, opts, nil, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if view.Error != "" || view.Harness != "codex" || view.MaxHops != wsconfig.TierRoutingMaxHops || !slices.Contains(view.Harnesses, "claude") {
		t.Fatalf("view header: harness %q, error %q, hops %d, harnesses %v", view.Harness, view.Error, view.MaxHops, view.Harnesses)
	}
	group := tierViewNode(t, view, "implement")
	if group.Kind != wsconfig.TierNodeGroup || group.Depth != 1 || len(group.Edges) != 1 {
		t.Fatalf("implement: %+v", group)
	}
	if e := group.Edges[0]; e.Kind != "caller" || e.To != "codex" || e.Selector != "*" || e.Target != "" {
		t.Fatalf("implement edge = %+v, want the implicit caller fallthrough", e)
	}
	r := tierViewRoute(t, group, "large")
	if !r.CallerFallthrough || r.Harness != "codex" || r.Resolved != "large" || r.Trace != "implement:large -> large -> codex.large" {
		t.Fatalf("implement:large = %+v", r)
	}
	if r.Hops[0].Via != "fallthrough" || r.Hops[1].Via != "caller" {
		t.Fatalf("hops = %+v", r.Hops)
	}
	role := tierViewNode(t, view, "ticket-worker")
	if role.Kind != wsconfig.TierNodeRole || role.Depth != 2 || role.Defaults["*"] != "implement:*" || role.Overrides != nil {
		t.Fatalf("ticket-worker: %+v", role)
	}
	if e := role.Edges[0]; e.Kind != "node" || e.To != "implement" || e.Size != "*" {
		t.Fatalf("ticket-worker edge = %+v", e)
	}
	if !slices.Contains(group.Inbound, "ticket-worker *") {
		t.Fatalf("implement inbound = %v", group.Inbound)
	}
	if r := tierViewRoute(t, role, "medium"); r.Trace != "ticket-worker:medium -> implement:medium -> medium -> codex.medium" {
		t.Fatalf("ticket-worker:medium = %+v", r)
	}

	// Unknown caller harness: the view picks claude and says so.
	if view, _ := dashboardTierRoutingView(rsrc, opts, nil, "nosuch"); view.Harness != "claude" {
		t.Fatalf("fallback caller = %q", view.Harness)
	}
}

// A draft is described before anything is saved: user nodes, bundles,
// pinned sizes, exceptions, and their inbound references, from the same
// validation and resolver a save and agent.spawn use.
func TestDashboardTierRoutingViewDraftReferencesAndPins(t *testing.T) {
	rsrc, opts := dashboardTierFixture(t)
	draft := json.RawMessage(`{"nodes":{"review":{"*":"codex.*","small":"claude.small"},"deep-review":{"*":"deep-design"},"ticket-reviewer-design":{"*":"deep-review:*"},"lead-explore":{}},"bundles":{"deep-design":{"harness":"claude","model":"opus-4-6","effort":"high"}}}`)
	view, err := dashboardTierRoutingView(rsrc, opts, draft, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if view.Error != "" {
		t.Fatalf("draft error: %s", view.Error)
	}
	review := tierViewNode(t, view, "review")
	if review.Overrides["small"] != "claude.small" || review.Pointers["*"] != "codex.*" {
		t.Fatalf("review: %+v", review)
	}
	if r := tierViewRoute(t, review, "small"); r.Trace != "review:small -> claude.small" || r.CallerFallthrough {
		t.Fatalf("review:small exception = %+v", r)
	}
	if r := tierViewRoute(t, review, "large"); r.Trace != "review:large -> codex.large" {
		t.Fatalf("review:large = %+v", r)
	}
	design := tierViewNode(t, view, "ticket-reviewer-design")
	if design.Depth != 2 {
		t.Fatalf("ticket-reviewer-design depth = %d", design.Depth)
	}
	if r := tierViewRoute(t, design, "large"); r.Trace != "ticket-reviewer-design:large -> deep-review:large -> deep-design" || r.Model != "opus-4-6" || r.Effort != "high" {
		t.Fatalf("ticket-reviewer-design:large = %+v", r)
	}
	if b := view.Bundles; len(b) != 1 || b[0].Name != "deep-design" || !slices.Equal(b[0].Inbound, []string{"deep-review *"}) {
		t.Fatalf("bundles = %+v", b)
	}
	if n := tierViewNode(t, view, "deep-review"); n.Kind != wsconfig.TierNodeUser || !slices.Equal(n.Inbound, []string{"ticket-reviewer-design *"}) {
		t.Fatalf("deep-review = %+v", n)
	}
	user := tierViewNode(t, view, "lead-explore")
	if user.Overrides == nil || len(user.Edges) != 1 || user.Edges[0].Kind != "caller" || !tierViewRoute(t, user, "small").CallerFallthrough {
		t.Fatalf("empty user node = %+v", user)
	}
}

// A cycle or a ninth hop comes back as the draft's error, the text a save
// would be refused with, and the stored value is untouched.
func TestDashboardTierRoutingViewRefusesCyclesAndTheHopLimit(t *testing.T) {
	rsrc, opts := dashboardTierFixture(t)
	cycle := json.RawMessage(`{"nodes":{"a":{"*":"b:*"},"b":{"small":"a:large"}}}`)
	view, err := dashboardTierRoutingView(rsrc, opts, cycle, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view.Error, "pointer cycle") || tierViewRoute(t, tierViewNode(t, view, "a"), "small").Error == "" {
		t.Fatalf("cycle: error %q", view.Error)
	}
	nodes := map[string]map[string]string{}
	for i := 1; i <= 8; i++ {
		nodes["n"+string(rune('0'+i))] = map[string]string{"*": "n" + string(rune('0'+i+1)) + ":*"}
	}
	nodes["n9"] = map[string]string{}
	raw, _ := json.Marshal(map[string]any{"nodes": nodes})
	view, err = dashboardTierRoutingView(rsrc, opts, raw, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view.Error, "more than 8 hops") {
		t.Fatalf("hop limit: error %q", view.Error)
	}
	if stored, err := wsconfig.LoadTierRouting(opts); err != nil || stored.Nodes != nil {
		t.Fatalf("a preview wrote: %+v %v", stored, err)
	}
	if view, _ := dashboardTierRoutingView(rsrc, opts, json.RawMessage(`{"nodes":{"Bad Name":{}}}`), "claude"); view.Error == "" {
		t.Fatal("unparsable draft accepted")
	}
}
