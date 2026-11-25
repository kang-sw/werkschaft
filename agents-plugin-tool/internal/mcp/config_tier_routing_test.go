package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wspackage"
)

// callRaw is callToolWithKey keeping the JSON-RPC line, so a caller can read
// isError as well as the text.
func callRaw(t *testing.T, f *piRecordFixture, id int, name string, args map[string]any) string {
	t.Helper()
	args["session_key"] = f.leadKey
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := f.s.ServeStdio(context.Background(), strings.NewReader(string(raw)+"\n"), &out); err != nil {
		t.Fatalf("ServeStdio(%s): %v", name, err)
	}
	return responseLinesByID(t, strings.Split(strings.TrimSpace(out.String()), "\n"))[fmt.Sprint(id)]
}

// tuneRouting calls config.tune on agent.tier_routing; extra adds arguments
// such as scope or reset.
func tuneRouting(t *testing.T, f *piRecordFixture, id int, value any, extra map[string]any) (text string, isErr bool) {
	t.Helper()
	args := map[string]any{"key": wsconfig.ItemAgentTierRouting}
	if value != nil {
		args["value"] = value
	}
	for k, v := range extra {
		args[k] = v
	}
	resp := callRaw(t, f, id, "config.tune", args)
	return toolText(t, resp), toolIsError(t, resp)
}

func tuneAliases(t *testing.T, f *piRecordFixture, id int, value string) string {
	t.Helper()
	resp := callRaw(t, f, id, "config.tune", map[string]any{"key": wsconfig.ItemAgentTierAliases, "value": value})
	if toolIsError(t, resp) {
		t.Fatalf("alias write refused: %s", toolText(t, resp))
	}
	return toolText(t, resp)
}

// globalFile is the global config file's text, empty when it does not exist.
func globalFile(t *testing.T, f *piRecordFixture) string {
	t.Helper()
	path, err := wsconfig.GlobalPath(f.s.configOptions())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// storedRouting is the raw structured_overrides member of the global file.
func storedRouting(t *testing.T, f *piRecordFixture) string {
	t.Helper()
	text := globalFile(t, f)
	if text == "" {
		return ""
	}
	var file struct {
		StructuredOverrides map[string]json.RawMessage `json:"structured_overrides"`
	}
	if err := json.Unmarshal([]byte(text), &file); err != nil {
		t.Fatalf("global file: %v", err)
	}
	raw := file.StructuredOverrides[wsconfig.ItemAgentTierRouting]
	if raw == nil {
		return ""
	}
	// The file is indented; compare the member's content, not its layout.
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	return compact.String()
}

// initializePi completes a Pi client's initialize, so the session's
// harness (the caller harness traces resolve for) is pi.
func initializePi(t *testing.T, s *Server) {
	t.Helper()
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(piInitNative+"\n"), &out); err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

func TestConfigTuneTierRoutingRoundTripAndGlobalScope(t *testing.T) {
	f := roleRenderFixture(t)
	initializePi(t, f.s)
	value := map[string]any{
		"bundles": map[string]any{"discussion": map[string]any{"model": " opus ", "harness": "claude", "effort": "high"}},
		"nodes": map[string]any{
			"ticket-worker": map[string]any{"*": "review:*", "large": "claude.xlarge"},
			"mine":          map[string]any{},
		},
	}
	// The default scope is global, and the stored value is the canonical
	// re-marshalled routing, not the caller's bytes.
	text, isErr := tuneRouting(t, f, 1, value, nil)
	if isErr {
		t.Fatalf("write refused: %s", text)
	}
	want := `{"nodes":{"mine":{},"ticket-worker":{"*":"review:*","large":"claude.xlarge"}},"bundles":{"discussion":{"harness":"claude","model":"opus","effort":"high"}}}`
	if got := storedRouting(t, f); got != want {
		t.Fatalf("stored routing = %s, want %s", got, want)
	}
	if !strings.Contains(text, wsconfig.ItemAgentTierRouting+": "+want+" [scope:global]") {
		t.Fatalf("echo does not name the write: %s", text)
	}
	if strings.Contains(text, "collision:") {
		t.Fatalf("no alias is set, yet the echo reports a collision: %s", text)
	}
	// The echo traces the routes the value touches on the caller's harness
	// (Pi here): written nodes at every size and bundles, but not
	// untouched routes.
	for _, want := range []string{
		"trace: discussion\n",
		"trace: ticket-worker:medium -> review:medium -> medium -> pi.medium\n",
		"trace: ticket-worker:large -> claude.xlarge\n",
		"trace: mine:small -> small -> pi.small\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("echo missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "trace: explore:") || strings.Contains(text, "trace: review:") {
		t.Errorf("echo traces untouched routes:\n%s", text)
	}

	// Every other scope is refused, and the stored value stays.
	for i, scope := range []string{"project", "repo", "session"} {
		for _, reset := range []bool{false, true} {
			extra := map[string]any{"scope": scope}
			var v any = value
			if reset {
				extra["reset"] = true
				v = nil
			}
			text, isErr := tuneRouting(t, f, 10+2*i, v, extra)
			if !isErr || !strings.Contains(text, "never "+scope+"-scoped") {
				t.Fatalf("%s scope (reset=%v) = error %v: %s", scope, reset, isErr, text)
			}
		}
	}
	if got := storedRouting(t, f); got != want {
		t.Fatalf("a refused scope changed the stored routing: %s", got)
	}

	// A non-object value is refused with the shape named.
	if text, isErr := tuneRouting(t, f, 20, "nodes", nil); !isErr || !strings.Contains(text, "routing object") {
		t.Fatalf("string value = error %v: %s", isErr, text)
	}

	// Reset removes the whole key from global.
	text, isErr = tuneRouting(t, f, 21, nil, map[string]any{"reset": true})
	if isErr || !strings.Contains(text, wsconfig.ItemAgentTierRouting+": reset [scope:global]") || !strings.Contains(text, "packaged tier routing defaults apply") {
		t.Fatalf("reset = error %v: %s", isErr, text)
	}
	if got := storedRouting(t, f); got != "" {
		t.Fatalf("reset left %s", got)
	}
	if routing, err := wsconfig.LoadTierRouting(f.s.configOptions()); err != nil || routing.Nodes != nil || routing.Bundles != nil {
		t.Fatalf("routing after reset = %+v, %v", routing, err)
	}
}

func TestConfigTuneTierRoutingRefusesInvalidGraphs(t *testing.T) {
	f := roleRenderFixture(t)
	nineHops := map[string]any{}
	for i := 1; i < 9; i++ {
		nineHops[fmt.Sprintf("n%d", i)] = map[string]any{"*": fmt.Sprintf("n%d:*", i+1)}
	}
	nineHops["n9"] = map[string]any{}

	cases := []struct {
		name  string
		value map[string]any
		want  string
	}{
		{"cycle across sizes", map[string]any{"nodes": map[string]any{
			"a": map[string]any{"small": "b:*"},
			"b": map[string]any{"large": "a:*"},
		}}, "pointer cycle"},
		{"unknown node", map[string]any{"nodes": map[string]any{"x": map[string]any{"*": "nosuch:*"}}}, `unknown node "nosuch"`},
		{"unknown bundle", map[string]any{"nodes": map[string]any{"x": map[string]any{"*": "nosuch"}}}, `unknown bundle "nosuch"`},
		{"reserved name", map[string]any{"nodes": map[string]any{"opus": map[string]any{}}}, "a tier spelling"},
		{"nine-hop chain", map[string]any{"nodes": nineHops}, "hops"},
		{"unknown harness", map[string]any{"nodes": map[string]any{"review": map[string]any{"*": "nosuch.*"}}}, `unknown harness "nosuch"`},
		{"non-werk bundle provider", map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "claude", "provider": "openai", "model": "m"}}}, "provider is available only for werk"},
		{"invalid werk bundle provider", map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "werk", "provider": "bogus", "model": "m"}}}, "unsupported werk provider"},
		{"unlaunchable bundle model", map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "claude", "model": "two words"}}}, "must match"},
		{"unlaunchable bundle effort", map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "claude", "model": "m", "effort": "HIGH"}}}, "lowercase word"},
	}
	run := func(id int, label string) {
		for i, tc := range cases {
			before := globalFile(t, f)
			text, isErr := tuneRouting(t, f, id+i, tc.value, nil)
			if !isErr || !strings.Contains(text, tc.want) {
				t.Errorf("%s/%s: error %v, text %q, want %q", label, tc.name, isErr, text, tc.want)
			}
			if strings.Contains(text, wsconfig.ItemAgentTierRouting+" "+wsconfig.ItemAgentTierRouting) {
				t.Errorf("%s/%s: the key is doubled in %q", label, tc.name, text)
			}
			if after := globalFile(t, f); after != before {
				t.Errorf("%s/%s: refused write changed the config:\n%s\n->\n%s", label, tc.name, before, after)
			}
		}
	}
	run(100, "plain")

	// An exact legacy alias that would mask one edge of the cycle does not
	// make the cycle acceptable.
	tuneAliases(t, f, 200, "a:small=claude.small")
	run(300, "alias-masked")
	if got := storedRouting(t, f); got != "" {
		t.Fatalf("a refused write left %s", got)
	}

	// The refused write keeps an earlier valid value too.
	valid := map[string]any{"nodes": map[string]any{"mine": map[string]any{"*": "claude.*"}}}
	if text, isErr := tuneRouting(t, f, 400, valid, nil); isErr {
		t.Fatalf("valid write refused: %s", text)
	}
	before := storedRouting(t, f)
	if _, isErr := tuneRouting(t, f, 401, cases[0].value, nil); !isErr {
		t.Fatal("cycle accepted over a valid value")
	}
	if got := storedRouting(t, f); got != before {
		t.Fatalf("stored routing = %s, want %s", got, before)
	}
}

func TestConfigTuneTierRoutingAndAliasesEchoCollisions(t *testing.T) {
	f := roleRenderFixture(t)

	// Routing written after an alias reports what the alias shadows.
	tuneAliases(t, f, 1, "discussion=claude.large ticket-worker:small=claude.small")
	text, isErr := tuneRouting(t, f, 2, map[string]any{
		"bundles": map[string]any{"discussion": map[string]any{"harness": "claude", "model": "opus"}},
	}, nil)
	if isErr {
		t.Fatalf("collision refused the write: %s", text)
	}
	for _, want := range []string{
		"collision: alias discussion -> claude.large shadows bundle discussion\n",
		"collision: alias ticket-worker:small -> claude.small takes precedence over node ticket-worker's pointers at size small\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("routing echo missing %q:\n%s", want, text)
		}
	}

	// An alias written after the routing reports the same overlap.
	if text, isErr := tuneRouting(t, f, 3, nil, map[string]any{"reset": true}); isErr || strings.Contains(text, "bundle discussion") {
		t.Fatalf("reset echo = error %v: %s", isErr, text)
	}
	if text, isErr := tuneRouting(t, f, 4, map[string]any{
		"bundles": map[string]any{"quick": map[string]any{"harness": "claude", "model": "haiku"}},
	}, nil); isErr || strings.Contains(text, "shadows bundle quick") {
		t.Fatalf("unrelated bundle = error %v: %s", isErr, text)
	}
	text = tuneAliases(t, f, 5, "quick=claude.small")
	if !strings.Contains(text, "collision: alias quick -> claude.small shadows bundle quick\n") {
		t.Fatalf("alias echo missing the collision:\n%s", text)
	}
	if !strings.Contains(text, "agent.tier_aliases: quick=claude.small [scope:global]") {
		t.Fatalf("alias echo lost its write line:\n%s", text)
	}

	// An alias write with no overlap stays a single line.
	if text := tuneAliases(t, f, 6, "mockup=pi.large"); strings.Contains(text, "collision:") {
		t.Fatalf("no overlap, yet: %s", text)
	}
}

func TestConfigListTierRoutingKnob(t *testing.T) {
	f := roleRenderFixture(t)
	list := func(id int) tuningKnob {
		t.Helper()
		resp := callRaw(t, f, id, "config.list", map[string]any{"format": "json"})
		if toolIsError(t, resp) {
			t.Fatalf("config.list failed: %s", toolText(t, resp))
		}
		return requireTuningKnob(t, parseTuningCatalogResponse(t, resp), wsconfig.ItemAgentTierRouting)
	}
	current := func(k tuningKnob) tierRoutingCurrent {
		t.Helper()
		raw, err := json.Marshal(k.Current)
		if err != nil {
			t.Fatal(err)
		}
		var got tierRoutingCurrent
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("current %s: %v", raw, err)
		}
		return got
	}

	knob := list(1)
	if knob.Error != "" || knob.ValueSchema == nil || knob.Writer.Tool != "config.tune" || knob.Reset == nil || knob.RepoScope {
		t.Fatalf("knob = %+v", knob)
	}
	if strings.Join(tuningFieldLabels(knob.SelectorFields), ",") == "" || !strings.Contains(knob.Description, "Global-only") {
		t.Fatalf("selectors/description = %+v", knob)
	}
	if strings.Contains(knob.Description, "ai-docs") || strings.Contains(knob.Description, "agents-plugin") {
		t.Fatalf("shipped description names a repository path: %s", knob.Description)
	}
	got := current(knob)
	if got.Routing.Nodes != nil || len(got.Nodes) == 0 {
		t.Fatalf("default current = %+v", got)
	}
	var worker *wsconfig.TierNodeInfo
	for i := range got.Nodes {
		if got.Nodes[i].Name == "ticket-worker" {
			worker = &got.Nodes[i]
		}
	}
	if worker == nil || worker.Pointers["*"] != "implement:*" {
		t.Fatalf("packaged role missing from current nodes: %+v", got.Nodes)
	}
	// Before the client names its harness, the caller slot is a placeholder.
	if traces := strings.Join(got.Traces, "\n"); !strings.Contains(traces, "explore:small -> small -> <caller>.small") {
		t.Fatalf("pre-initialize traces:\n%s", traces)
	}
	initializePi(t, f.s)

	if text, isErr := tuneRouting(t, f, 2, map[string]any{"nodes": map[string]any{"ticket-worker": map[string]any{"*": "review:*"}}}, nil); isErr {
		t.Fatal(text)
	}
	got = current(list(3))
	if got.Routing.Nodes["ticket-worker"]["*"] != "review:*" {
		t.Fatalf("stored routing missing from current: %+v", got)
	}
	traces := strings.Join(got.Traces, "\n")
	for _, want := range []string{
		"ticket-worker:medium -> review:medium -> medium -> pi.medium",
		"review:large -> large -> pi.large",
		"explore:xlarge -> xlarge -> pi.xlarge",
	} {
		if !strings.Contains(traces, want) {
			t.Errorf("current traces missing %q:\n%s", want, traces)
		}
	}
	for _, n := range got.Nodes {
		if n.Name == "ticket-worker" && n.Pointers["*"] != "review:*" {
			t.Fatalf("effective pointer = %+v", n)
		}
	}

	// A stored graph that fails validation, and one that fails to parse,
	// surface as the knob's error; the listing itself still succeeds.
	setRouting(t, f.s, `{"nodes":{"a":{"*":"b:*"},"b":{"*":"a:*"}}}`)
	if knob := list(4); !strings.Contains(knob.Error, "pointer cycle") {
		t.Fatalf("cycle error = %q", knob.Error)
	}
	setRouting(t, f.s, `{"nodes":{"Bad Name":{}}}`)
	if knob := list(5); !strings.Contains(knob.Error, "lowercase slug") {
		t.Fatalf("parse error = %q", knob.Error)
	}
}

// The dashboard catalog carries the exact structured key: a reset removes the
// whole structured value (not a scalar override), a set stores the canonical
// graph, and a non-global scope is refused.
func TestDashboardCatalogTierRoutingMutation(t *testing.T) {
	t.Setenv(wspackage.EnvDevCheckout, "")
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	opts := wsconfig.Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	op := func(action string, value any) wsconfigservice.Operation {
		return wsconfigservice.Operation{Key: wsconfig.ItemAgentTierRouting, Selectors: map[string]string{}, Action: action, Value: value}
	}
	for _, scope := range []wsconfig.Scope{wsconfig.ScopeProject, wsconfig.ScopeRepo, wsconfig.ScopeSession} {
		if _, err := catalog.Validate(scope, op("set", map[string]any{})); err == nil {
			t.Errorf("%s scope accepted", scope)
		}
	}
	for _, bad := range []any{
		"text",
		map[string]any{"nodes": map[string]any{"a": map[string]any{"*": "b:*"}, "b": map[string]any{"*": "a:*"}}},
		map[string]any{"nodes": map[string]any{"x": map[string]any{"*": "nosuch:*"}}},
		map[string]any{"nodes": map[string]any{"review": map[string]any{"*": "nosuch.*"}}},
		map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "claude", "model": "m", "effort": "HIGH"}}},
	} {
		if _, err := catalog.Validate(wsconfig.ScopeGlobal, op("set", bad)); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}

	for _, h := range wsagent.BuiltinHarnessNames() {
		if _, err := catalog.Validate(wsconfig.ScopeGlobal, op("set", map[string]any{"nodes": map[string]any{"mine": map[string]any{"*": h + ".*"}}})); err != nil {
			t.Errorf("built-in harness %s refused: %v", h, err)
		}
	}
	if _, err := catalog.Validate(wsconfig.ScopeGlobal, op("set", map[string]any{
		"bundles": map[string]any{
			"oa":  map[string]any{"harness": "werk", "provider": "openai", "model": "gpt-5.4", "effort": "high"},
			"loc": map[string]any{"harness": "werk", "provider": "local/desk", "model": "qwen"},
		},
	})); err != nil {
		t.Errorf("werk bundles with valid providers refused: %v", err)
	}
	for _, bad := range []any{
		map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "claude", "provider": "openai", "model": "m"}}},
		map[string]any{"bundles": map[string]any{"b": map[string]any{"harness": "werk", "provider": "bogus", "model": "m"}}},
	} {
		if _, err := catalog.Validate(wsconfig.ScopeGlobal, op("set", bad)); err == nil {
			t.Errorf("invalid bundle provider accepted: %v", bad)
		}
	}
	set, err := catalog.Validate(wsconfig.ScopeGlobal, op("set", map[string]any{"nodes": map[string]any{"mine": map[string]any{"*": "claude.*"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Apply(wsconfig.ScopeGlobal, opts, "", nil); err != nil {
		t.Fatal(err)
	}
	routing, err := wsconfig.LoadTierRouting(opts)
	if err != nil || routing.Nodes["mine"]["*"] != "claude.*" {
		t.Fatalf("routing after set = %+v, %v", routing, err)
	}
	reset, err := catalog.Validate(wsconfig.ScopeGlobal, op("reset", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := reset.Apply(wsconfig.ScopeGlobal, opts, "", nil); err != nil {
		t.Fatal(err)
	}
	if routing, err := wsconfig.LoadTierRouting(opts); err != nil || routing.Nodes != nil {
		t.Fatalf("routing after reset = %+v, %v", routing, err)
	}
}
