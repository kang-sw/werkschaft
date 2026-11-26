package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// tierManualFixture is a top-level lead on a fresh repository, with the
// workflow-manual render environment isolated (setupWorkflowManualNoteEnv).
func tierManualFixture(t *testing.T) (s *Server, root, key string) {
	t.Helper()
	setupWorkflowManualNoteEnv(t)
	root = t.TempDir()
	initGit(t, root)
	s = NewServer(root, "test")
	key, _ = parseLoginResponse(t, callLogin(t, s, 1, root, nil))
	return s, root, key
}

// tuneKnob is a config.tune call that reports isError along with the text.
func tuneKnob(t *testing.T, s *Server, id int, key string, args map[string]any) (string, bool) {
	t.Helper()
	args["session_key"] = key
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "config.tune", "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(string(raw)+"\n"), &out); err != nil {
		t.Fatalf("ServeStdio(config.tune): %v", err)
	}
	line := responseLinesByID(t, strings.Split(strings.TrimSpace(out.String()), "\n"))[fmt.Sprint(id)]
	return toolText(t, line), toolIsError(t, line)
}

func mustTune(t *testing.T, s *Server, id int, key string, args map[string]any) {
	t.Helper()
	if text, isErr := tuneKnob(t, s, id, key, args); isErr {
		t.Fatalf("config.tune %v refused: %s", args, text)
	}
}

// leadManualRenders returns the three lead-manual surfaces: the hosted
// system-prompt manual (RenderLeadManual, the workflow_manual path), the
// hosted playbook command (RenderLeadPlaybook), and the playbook.read tool.
func leadManualRenders(t *testing.T, s *Server, root, key string, id int) (manual, hosted, read string) {
	t.Helper()
	manual, err := RenderLeadManual(os.Environ(), root, HostedRender{}, key)
	if err != nil {
		t.Fatal(err)
	}
	hosted, err = RenderLeadPlaybook(os.Environ(), root, HostedRender{}, key, workflowManualPlaybookName)
	if err != nil {
		t.Fatal(err)
	}
	read = callToolWithKey(t, s, id, key, "playbook.read", map[string]any{"name": workflowManualPlaybookName})
	return manual, hosted, read
}

// tierSection is the body of the manual's "### Choosing a tier" section.
func tierSection(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, tierRoutingManualHeading+"\n")
	if !ok {
		t.Fatalf("manual has no %q section:\n%s", tierRoutingManualHeading, body)
	}
	if i := strings.Index(rest, "\n### "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// With the knobs at their defaults, and with expose on but no user node or
// bundle in the graph, every lead-manual surface is byte-identical to the
// unconfigured render: the feature costs zero tokens until it has content.
func TestWorkflowManualTierRoutingDefaultAddsNothing(t *testing.T) {
	s, root, key := tierManualFixture(t)
	// The first render may spend one-shot nudges stored on the session;
	// the baseline is the settled second render.
	leadManualRenders(t, s, root, key, 10)
	baseManual, baseHosted, baseRead := leadManualRenders(t, s, root, key, 11)
	if strings.Contains(baseManual, "Custom tiers") || strings.Contains(baseManual, "tier routing preferences") {
		t.Fatalf("default manual carries tier routing text:\n%s", tierSection(t, baseManual))
	}

	// A routing value that only re-points a built-in group defines no user
	// node or bundle, so expose on still adds nothing.
	setRouting(t, s, `{"nodes":{"review":{"*":"claude.*"}}}`)
	mustTune(t, s, 12, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "value": "on"})
	manual, hosted, read := leadManualRenders(t, s, root, key, 13)
	if manual != baseManual || hosted != baseHosted || read != baseRead {
		t.Fatalf("expose on with no user node or bundle changed the manual:\n--- base\n%s\n--- got\n%s", tierSection(t, baseManual), tierSection(t, manual))
	}

	// Whitespace-only guidance (stored directly; config.tune refuses an
	// empty value) is empty after trimming.
	resolver := wsconfig.NewResolver(s.configOptions(), nil, nil, nil)
	if err := resolver.Set(wsconfig.ItemAgentTierRoutingGuidance, " \n\t ", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	manual, hosted, read = leadManualRenders(t, s, root, key, 14)
	if manual != baseManual || hosted != baseHosted || read != baseRead {
		t.Fatalf("blank guidance changed the manual:\n%s", tierSection(t, manual))
	}
}

// expose on lists only user nodes (as <node>:<size>) and bundles, inside the
// tier section, on every lead-manual surface; built-in groups and playbook
// roles, even re-pointed ones, never appear.
func TestWorkflowManualTierRoutingExposesOnlyUserNodesAndBundles(t *testing.T) {
	s, root, key := tierManualFixture(t)
	// Re-point one packaged playbook role of this server's package, so the
	// listing is shown to skip overridden roles as well as groups.
	graph, err := s.tierGraph(s.configOptions())
	if err != nil {
		t.Fatal(err)
	}
	role := ""
	for _, node := range graph.Nodes() {
		if node.Kind == wsconfig.TierNodeRole {
			role = node.Name
			break
		}
	}
	if role == "" {
		t.Fatal("the package declares no tier role to re-point")
	}
	setRouting(t, s, `{
		"nodes": {
			"mockup": {"*": "claude.*", "large": "codex.xlarge"},
			"pinned": {"small": "codex.medium"},
			"solo": {},
			"review": {"*": "codex.*"},
			"`+role+`": {"*": "review:*"}
		},
		"bundles": {"quick": {"harness": "claude", "model": "claude-haiku", "effort": "low"}}
	}`)

	off, _, _ := leadManualRenders(t, s, root, key, 20)
	if strings.Contains(off, "Custom tiers") {
		t.Fatalf("expose off (builtin) must list nothing:\n%s", tierSection(t, off))
	}

	mustTune(t, s, 21, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "value": "on"})
	manual, hosted, read := leadManualRenders(t, s, root, key, 22)
	want := []string{
		"Custom tiers from `agent.tier_routing`; pass the token as the tier, with `<size>` one of small, medium, large, xlarge:",
		"- `mockup:<size>` → `claude.*`; large: `codex.xlarge`",
		"- `pinned:<size>` → small: `codex.medium`; other sizes: the caller's harness",
		"- `solo:<size>` → the caller's harness at that size",
		"- `quick` (bundle) → claude `claude-haiku`, effort low",
	}
	for name, body := range map[string]string{"RenderLeadManual": manual, "RenderLeadPlaybook": hosted, "playbook.read": read} {
		section := tierSection(t, body)
		for _, line := range want {
			if !strings.Contains(section, line+"\n") {
				t.Errorf("%s tier section missing %q:\n%s", name, line, section)
			}
		}
		for _, builtin := range []string{"`review:", "`explore:", "`implement:", "`" + role + ":"} {
			if strings.Contains(section, builtin) {
				t.Errorf("%s lists built-in node %s:\n%s", name, builtin, section)
			}
		}
		if strings.Count(body, "Custom tiers from") != 1 {
			t.Errorf("%s carries the custom tier list %d times", name, strings.Count(body, "Custom tiers from"))
		}
	}

	// A graph that fails validation lists nothing: agent.spawn would refuse
	// its routed tokens.
	setRouting(t, s, `{"nodes":{"mockup":{"*":"missing:*"}}}`)
	manual, _, _ = leadManualRenders(t, s, root, key, 23)
	if strings.Contains(manual, "Custom tiers") {
		t.Fatalf("an invalid graph was exposed:\n%s", tierSection(t, manual))
	}
}

// Guidance renders under the tier section on its own when expose is off,
// quoted line by line, from a non-global scope too; with expose on, the list
// comes first and the guidance follows.
func TestWorkflowManualTierRoutingGuidance(t *testing.T) {
	s, root, key := tierManualFixture(t)
	guidance := "Prefer mockup:large for UI mockups.\n\n## Not a heading\nUse agy.small for quick lookups."
	mustTune(t, s, 30, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingGuidance, "value": "  " + guidance + "\n", "scope": "project"})
	manual, hosted, read := leadManualRenders(t, s, root, key, 31)
	quoted := "The user's tier routing preferences; apply them when choosing a tier:\n" +
		"> Prefer mockup:large for UI mockups.\n>\n> ## Not a heading\n> Use agy.small for quick lookups.\n"
	for name, body := range map[string]string{"RenderLeadManual": manual, "RenderLeadPlaybook": hosted, "playbook.read": read} {
		section := tierSection(t, body)
		if !strings.Contains(section, quoted) {
			t.Errorf("%s tier section missing quoted guidance:\n%s", name, section)
		}
		if strings.Contains(section, "Custom tiers") {
			t.Errorf("%s lists custom tiers with expose off:\n%s", name, section)
		}
	}

	setRouting(t, s, `{"bundles":{"quick":{"harness":"claude","model":"claude-haiku"}}}`)
	mustTune(t, s, 32, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "value": "on"})
	manual, _, _ = leadManualRenders(t, s, root, key, 33)
	section := tierSection(t, manual)
	list, prefs := strings.Index(section, "- `quick` (bundle) → claude `claude-haiku`\n"), strings.Index(section, quoted)
	if list < 0 || prefs < 0 || list > prefs {
		t.Fatalf("want the custom tier list, then the guidance:\n%s", section)
	}

	// Reset returns the section to its packaged text.
	mustTune(t, s, 34, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingGuidance, "reset": true, "scope": "project"})
	mustTune(t, s, 35, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "reset": true})
	manual, _, _ = leadManualRenders(t, s, root, key, 36)
	if section := tierSection(t, manual); strings.Contains(section, "preferences") || strings.Contains(section, "Custom tiers") {
		t.Fatalf("reset left tier routing text:\n%s", section)
	}
}

// config.tune accepts the expose toggle's on/off domain (normalized) at
// every scope and keeps guidance text as written; config.list lists both.
func TestConfigTuneTierRoutingExposeAndGuidance(t *testing.T) {
	s, _, key := tierManualFixture(t)
	if text, isErr := tuneKnob(t, s, 40, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "value": "maybe"}); !isErr || !strings.Contains(text, "on, off") {
		t.Fatalf("expose maybe = error %v: %s", isErr, text)
	}
	mustTune(t, s, 41, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "value": " ON "})
	for i, scope := range []string{"session", "project", "global"} {
		mustTune(t, s, 42+i, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingExpose, "value": "off", "scope": scope})
	}
	if text, isErr := tuneKnob(t, s, 46, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingGuidance, "value": "   "}); !isErr {
		t.Fatalf("blank guidance must be refused (reset is the unset path): %s", text)
	}
	mustTune(t, s, 47, key, map[string]any{"key": wsconfig.ItemAgentTierRoutingGuidance, "value": "Prefer Mockup:Large for UI"})
	resolver := wsconfig.NewResolver(s.configOptions(), builtinConfigDefaults(), nil, nil)
	if rv, err := resolver.Get("", wsconfig.ItemAgentTierRoutingGuidance); err != nil || rv.Value != "Prefer Mockup:Large for UI" || rv.Scope != wsconfig.ScopeGlobal {
		t.Fatalf("guidance = %+v, %v; want the caller's text in global", rv, err)
	}
	// Neither knob touched the routing graph.
	if routing, err := wsconfig.LoadTierRouting(s.configOptions()); err != nil || routing.Nodes != nil || routing.Bundles != nil {
		t.Fatalf("routing = %+v, %v", routing, err)
	}

	list := callToolWithKey(t, s, 48, key, "config.list", map[string]any{"format": "json"})
	for _, id := range []string{wsconfig.ItemAgentTierRoutingExpose, wsconfig.ItemAgentTierRoutingGuidance} {
		if !strings.Contains(list, `"id":"`+id+`"`) && !strings.Contains(list, `"id": "`+id+`"`) {
			t.Errorf("config.list does not list %s", id)
		}
	}
}

func TestTierRoutingManualFormatting(t *testing.T) {
	body := "# M\n\n### Choosing a tier\n\nPick one.\n\n### Next\n\nrest\n"
	got := injectAtSectionEnd(body, tierRoutingManualHeading, "BLOCK")
	if want := "# M\n\n### Choosing a tier\n\nPick one.\n\nBLOCK\n\n### Next\n\nrest\n"; got != want {
		t.Fatalf("injectAtSectionEnd = %q, want %q", got, want)
	}
	if got := injectAtSectionEnd("# M\n", tierRoutingManualHeading, "BLOCK"); got != "# M\n\nBLOCK" {
		t.Fatalf("a body without the heading appends the block: %q", got)
	}
	if got := describeTierBundle(wsconfig.TierBundle{Harness: "werk", Provider: "openrouter", Model: "m"}); got != "werk/openrouter `m`" {
		t.Fatalf("describeTierBundle = %q", got)
	}
	if got := tierRoutingGuidanceText("a\r\nb  "); got != "The user's tier routing preferences; apply them when choosing a tier:\n> a\n> b" {
		t.Fatalf("tierRoutingGuidanceText = %q", got)
	}
}
