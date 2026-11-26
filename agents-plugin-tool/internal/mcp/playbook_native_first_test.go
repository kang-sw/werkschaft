package mcp

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsrsrc"
)

// A hosted lead whose backend registration sets PreferNativeSubagent (agy)
// reads native-first terminology in the text it executes itself
// (workflow_manual, playbook.read): exploration goes to its native subagent
// and spawns go to agent.spawn with a rendered tier passed as returned, an
// agy route allowed but restrained toward an external fixed tier by the
// Antigravity terms risk. A prompt rendered for
// another process (playbook.render) keeps today's daemon-agent wording.

const (
	nativeFirstExplore      = "your native subagent (no ws tools, your harness's default model; when the question needs a specific tier, a read-only `ws/agent.spawn` subagent at an external-harness tier)"
	wantNativeFirstTierRule = "pass a rendered tier as returned: a role tier keeps its configured route, model and effort, and may end on agy, which is allowed; an agy route carries the Antigravity terms risk, so when another harness fits the work, choose an external fixed tier such as `claude.small` for `playbook-explore:small` instead; never put a harness prefix before a role tier (`claude.playbook-explore:small` is rejected)"
	nativeFirstSpawn        = "`ws/agent.spawn` (tier: the resolved capability tier, not a model name; " + wantNativeFirstTierRule + "; authority: at most your own)"
	nativeFirstTip          = "When a subagent started with `ws/agent.spawn` returns an agent id, use `" + piMCPOnlyContinue + "`"
	daemonAgentExplore      = piMCPOnlyExplore + " at the tier the question needs"
	daemonAgentSpawn        = "`ws/agent.spawn` (tier: the resolved capability tier, not a model name; authority: at most your own)"
	genericTip              = "When the subagent returns an agent id, use `" + piMCPOnlyContinue + "`"
)

// The workflow manual's exploration and spawning bullets and its rendered
// playbook tier line, complete, as the agy lead reads them with and without
// the option.
const (
	nativeFirstExploreBullet = "- Bounded evidence gathering that benefits from an independent read →\n" +
		"  " + nativeFirstExplore + "; pick that tier by `### Choosing a tier`.\n" +
		"  English prompt with the scoped question; require cited evidence, gaps, and\n" +
		"  follow-ups. Run independent questions in parallel; synthesize once the\n" +
		"  decision has enough evidence.\n"
	nativeFirstSpawnBullet = "- Bundled delegate prompt → `ws/playbook.render` with the lead\n" +
		"  `session_key`; spawn the subagent through\n" +
		"  `ws/agent.spawn` with the returned path and the render's tier, chosen as `### Choosing a tier` states. Delegate works in another worktree →\n" +
		"  select that worktree at render time.\n"
	nativeFirstTierLine = "- A rendered playbook → the tier its render recommends; as a `ws/agent.spawn` target, " + wantNativeFirstTierRule + ".\n"
	todayExploreBullet  = "- Bounded evidence gathering that benefits from an independent read →\n" +
		"  " + daemonAgentExplore + " (`### Choosing a tier`).\n" +
		"  English prompt with the scoped question; require cited evidence, gaps, and\n" +
		"  follow-ups. Run independent questions in parallel; synthesize once the\n" +
		"  decision has enough evidence.\n"
	todaySpawnBullet = "- Bundled delegate prompt → `ws/playbook.render` with the lead\n" +
		"  `session_key`; give the subagent the returned path and spawn it with the\n" +
		"  tier and bindings the render returns. Delegate works in another worktree →\n" +
		"  select that worktree at render time.\n"
	todayTierLine = "- A rendered playbook → the tier its render recommends, passed as returned;\n  a role tier such as `ticket-worker:large` is valid there.\n"
)

// nativeFirstMarkers are phrases only the native-first wording carries.
var nativeFirstMarkers = []string{"your native subagent", "external-harness tier", "Antigravity terms risk", "When a subagent started with"}

func agyRegistrations(prefer bool) map[string]wsagent.BackendRegistration {
	regs := descriptorRegistrations()
	reg := regs[wsagent.BackendAgy]
	reg.PreferNativeSubagent = prefer
	regs[wsagent.BackendAgy] = reg
	return regs
}

// agyLeadConnections are two connections of one hosted agy lead: off under
// a registration without PreferNativeSubagent (today's descriptor), on with it.
func agyLeadConnections(t *testing.T) (off, on *Server, f *hostedKeyFixture) {
	t.Helper()
	off, f = hostedBackendConnection(t, wsagent.BackendAgy, agyRegistrations(false))
	on = f.connect(t)
	on.agents = wsagent.NewRunner(wsagent.Options{Backends: agyRegistrations(true)})
	return off, on, f
}

func connectionManual(t *testing.T, s *Server, key string) string {
	t.Helper()
	resp := s.renderWorkflowManual(nil, map[string]any{"session_key": key}, false)
	result, _ := resp.Result.(map[string]any)
	content, _ := result["content"].([]map[string]string)
	if responseIsError(resp) || len(content) == 0 {
		t.Fatalf("workflow manual: %+v", resp)
	}
	return content[0]["text"]
}

func connectionRead(t *testing.T, s *Server, key, name string) string {
	t.Helper()
	text, err := s.readPlaybook(name, key, nil)
	if err != nil {
		t.Fatalf("playbook.read %s: %v", name, err)
	}
	return text
}

// connectionRender is playbook.render's prompt body and recommended tier
// for key on s, without a child key (whose random value would differ per
// render).
func connectionRender(t *testing.T, s *Server, key, root, name string) (string, string) {
	t.Helper()
	rsrcRoot, err := s.resolveRsrcRoot("")
	if err != nil {
		t.Fatal(err)
	}
	path, _, tier, err := renderPlaybookWithPlan(s, key, "", rsrcRoot, root, name, nil, s.configOptions(), "", "", "", nil, "", "")
	if err != nil {
		t.Fatalf("playbook.render %s: %v", name, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), tier
}

func assertContains(t *testing.T, label, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s lacks %q:\n%s", label, w, text)
		}
	}
}

func assertLacks(t *testing.T, label, text string, banned ...string) {
	t.Helper()
	for _, b := range banned {
		if strings.Contains(text, b) {
			t.Errorf("%s carries %q:\n%s", label, b, text)
		}
	}
}

// TestNativeFirstLeadExecutedText: the agy lead's workflow manual and its
// playbook.read of lead playbooks name its native subagent for exploration
// and the external-harness agent.spawn for spawning, with the continuity
// tip naming agent.spawn's subagents; the same lead without the option
// reads today's daemon-agent wording. The daemon's own renders for the
// lead's prompt and commands match the connection.
func TestNativeFirstLeadExecutedText(t *testing.T) {
	off, on, f := agyLeadConnections(t)

	manual := connectionManual(t, on, f.hosted)
	assertContains(t, "native-first manual", manual, nativeFirstExploreBullet, nativeFirstSpawnBullet, nativeFirstTierLine)
	assertLacks(t, "native-first manual", manual, daemonAgentExplore, "at the tier the question needs", "bindings", "{{")
	offManual := connectionManual(t, off, f.hosted)
	assertContains(t, "today's manual", offManual, todayExploreBullet, todaySpawnBullet, todayTierLine)
	assertLacks(t, "today's manual", offManual, nativeFirstMarkers...)

	run := connectionRead(t, on, f.hosted, "lead-run")
	assertContains(t, "native-first lead-run", run, nativeFirstSpawn)
	assertLacks(t, "native-first lead-run", run, daemonAgentSpawn)
	offRun := connectionRead(t, off, f.hosted, "lead-run")
	assertContains(t, "today's lead-run", offRun, daemonAgentSpawn)
	assertLacks(t, "today's lead-run", offRun, nativeFirstMarkers...)

	discuss := connectionRead(t, on, f.hosted, "lead-discuss")
	assertContains(t, "native-first lead-discuss", discuss, nativeFirstExplore, nativeFirstTip)
	assertLacks(t, "native-first lead-discuss", discuss, genericTip)
	offDiscuss := connectionRead(t, off, f.hosted, "lead-discuss")
	assertContains(t, "today's lead-discuss", offDiscuss, piMCPOnlyExplore, genericTip)
	assertLacks(t, "today's lead-discuss", offDiscuss, nativeFirstMarkers...)

	// The daemon renders as the lead's own connection would.
	t.Setenv(envHostedSession, "")
	reg := agyRegistrations(true)[wsagent.BackendAgy]
	render := HostedRender{Harness: reg.ManualHarness, PlaybookCommands: reg.PlaybookCommands, PreferNativeSubagent: reg.PreferNativeSubagent, ToolNameScheme: reg.ToolNameScheme, CallFallback: reg.CallFallback, DirectTools: reg.DirectTools}
	daemonManual, err := RenderLeadManual(os.Environ(), f.root, render, f.hosted)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "daemon manual", daemonManual, nativeFirstExploreBullet, nativeFirstSpawnBullet, nativeFirstTierLine)
	daemonDiscuss, err := RenderLeadPlaybook(os.Environ(), f.root, render, f.hosted, "lead-discuss")
	if err != nil {
		t.Fatal(err)
	}
	if daemonDiscuss != strings.TrimSpace(discuss) {
		t.Fatalf("the daemon render differs from the connection's read:\n--- render\n%s\n--- read\n%s", daemonDiscuss, discuss)
	}
}

// TestNativeFirstManualKeepsCallerContextValidation: the native-first
// manual rewrite is renderer-internal, so a caller-supplied context key
// named like one of its parts is still undeclared on a manual read, with
// the option on as with it off.
func TestNativeFirstManualKeepsCallerContextValidation(t *testing.T) {
	off, on, f := agyLeadConnections(t)
	for label, s := range map[string]*Server{"on": on, "off": off} {
		_, err := s.readPlaybook(workflowManualPlaybookName, f.hosted, map[string]string{"ExploreTierClause": "x"})
		var undeclared wsrsrc.ErrUndeclaredVar
		if !errors.As(err, &undeclared) || undeclared.Name != "ExploreTierClause" {
			t.Fatalf("%s: manual read with context ExploreTierClause = %v, want ErrUndeclaredVar", label, err)
		}
	}
}

// TestNativeFirstLeavesPlaybookRenderUnchanged: a delegate prompt the agy
// lead renders (an explore delegate, the design reviewer) runs on another
// process, so it is byte-identical with and without the option and names no
// native subagent. TestAgyLeadRenderedTierSpawnsAsReturnedOrClaudeChild carries its
// recommendation through agent.spawn.
func TestNativeFirstLeavesPlaybookRenderUnchanged(t *testing.T) {
	off, on, f := agyLeadConnections(t)
	for _, name := range []string{"explore", "ticket-reviewer-design"} {
		before, beforeTier := connectionRender(t, off, f.hosted, f.root, name)
		after, afterTier := connectionRender(t, on, f.hosted, f.root, name)
		if after != before || afterTier != beforeTier {
			t.Fatalf("%s render changed under PreferNativeSubagent (tier %q -> %q):\n--- before\n%s\n--- after\n%s", name, beforeTier, afterTier, before, after)
		}
		assertLacks(t, name+" render", after, nativeFirstMarkers...)
		assertContains(t, name+" render", after, piMCPOnlyExplore)
	}
}

// TestNativeFirstOtherHarnessesUnchanged: only agy sets
// PreferNativeSubagent; TestHarnessWordingGolden pins the Claude, Codex, and
// Pi hosted leads' outputs byte-identical to the base.
func TestNativeFirstOtherHarnessesUnchanged(t *testing.T) {
	for backend, reg := range wsagent.BuiltinBackendRegistrations() {
		if reg.PreferNativeSubagent != (backend == wsagent.BackendAgy) {
			t.Errorf("%s PreferNativeSubagent = %v", backend, reg.PreferNativeSubagent)
		}
	}
}

// TestAgyLeadRenderedTierSpawnsAsReturnedOrClaudeChild: the render-to-spawn
// handoff of an agy hosted lead, end to end on one connection served by a
// real runner with the packaged tier roles (as production wires
// Options.TierRoles), the daemon's own agy registration (its cross-harness
// refusal included) over fake Claude and agy backends, and no configured
// external route. The tier and prompt path come from that connection's
// playbook.render response. The role tier's default route falls through to
// the caller's agy slot: passed as returned, it starts an agy child, which
// the same-harness admission allows; the external fixed tier the manual
// steers to when another harness fits (`claude.small`) starts the Claude
// backend with the rendered prompt. Both halves live in this package
// because wsagent cannot reach playbook.render.
func TestAgyLeadRenderedTierSpawnsAsReturnedOrClaudeChild(t *testing.T) {
	f := newHostedKeyFixture(t)
	dir, err := NewServer(f.root, "test").dashboard.stateDir()
	if err != nil {
		t.Fatal(err)
	}
	rec := wsagent.Record{ReplyID: wsmailbox.ReplyID(f.hosted), WSKey: f.hosted, Root: f.root, CreatedAt: time.Now().UTC().Format(time.RFC3339), Backend: wsagent.BackendAgy, Lead: true}
	if err := wsagent.WriteRecordForTest(dir, rec); err != nil {
		t.Fatal(err)
	}
	s := f.connect(t)
	cfg := s.sessionConfigOptions(f.hosted)
	for harness, model := range map[string]string{wsagent.BackendClaude: "claude-small", wsagent.BackendAgy: "agy-small"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, "small", "", model, harness, "high"); err != nil {
			t.Fatal(err)
		}
	}
	claude, agy := &routeBackend{}, &routeBackend{}
	regs := agyRegistrations(true)
	// The shipped agy registration over the fake backend, without the
	// launch features this fake does not serve (the HTTP MCP route and the
	// exec cutover); its admission is the shipped one.
	agyReg := wsagent.BuiltinBackendRegistrations()[wsagent.BackendAgy]
	agyReg.Backend, agyReg.WorkerHTTPMCP, agyReg.ExecCutover = agy, false, nil
	regs[wsagent.BackendAgy] = agyReg
	claudeReg := regs[wsagent.BackendClaude]
	claudeReg.Backend, claudeReg.WorkerCapable, claudeReg.WorkerProfile = claude, true, wsagent.AgentWorkerProfile(wsagent.BackendClaude)
	regs[wsagent.BackendClaude] = claudeReg
	runner := wsagent.NewRunner(wsagent.Options{Dir: dir, Backends: regs, Keys: &routeKeys{}, Config: cfg, TierRoles: PackagedTierRoles,
		WSMCP: func(root string) (wsagent.MCPCommand, error) {
			return wsagent.MCPCommand{Command: "/bin/ws-mcp", Args: []string{"serve", "--stdio", "--root", root}}, nil
		}})
	daemon := httptest.NewServer(runner.Handler())
	defer daemon.Close()
	runner.SetEndpoint(daemon.URL)
	runner.Start(nil)
	defer runner.Close()
	// The token binding of the agy lead's connection: the runner holds
	// its conversation open, loaded from the record.
	s.agents, s.recordReader, s.callerBinding, s.conversation = runner, runner, runner, rec.ReplyID

	out := callToolWithKey(t, s, 2, f.hosted, "playbook.render", map[string]any{"name": "explore"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	promptPath, tier := strings.TrimSpace(lines[0]), ""
	for _, line := range lines[1:] {
		if v, ok := strings.CutPrefix(line, "recommended-tier: "); ok {
			tier = strings.TrimSpace(v)
		}
	}
	prompt, err := os.ReadFile(promptPath)
	// The explore role's default route is the caller's own slot, which a
	// v0.51.0 spawn also accepts as the bare size, so the render returns
	// that form (TestRenderedTierSurvivesDaemonRollbackBeforeSpawn).
	if err != nil || tier != "small" {
		t.Fatalf("playbook.render = %q (prompt %v); want a prompt path and the bare size the explore role resolves to", out, err)
	}

	// The role tier's default route: the packaged chain falls through to
	// the caller's own slot, agy's.
	resolver := wsagent.NewTierBindingResolver(cfg, s.agentBackends()...)
	route, binding, err := s.routedBinding("playbook-explore:small", wsagent.BackendAgy, cfg, resolver)
	if err != nil || !route.CallerFallthrough || route.Harness != wsagent.BackendAgy || route.Size != "small" || binding.Model != "agy-small" {
		t.Fatalf("default route of the explore role = %+v (%s), binding %+v, %v", route, route.Trace(), binding, err)
	}

	// Passed as returned, it starts an agy child: agy admits its own lead.
	raw := callToolWithKey(t, s, 3, f.hosted, "agent.spawn", map[string]any{"prompt": "Run the playbook at the system prompt path.", "system_prompt_path": promptPath, "tier": tier, "authority": "read-only", "format": "json"})
	var agyRes wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(raw), &agyRes); err != nil || agyRes.Address == "" {
		t.Fatalf("agent.spawn %s = %q (%v)", tier, raw, err)
	}
	agySpec, agyProc := agy.waitStart(t, 0)
	agyProc.nextSent(t)
	if agySpec.Model != "agy-small" {
		t.Fatalf("the agy child's model = %q, want the agy small tier's", agySpec.Model)
	}
	agyChild, err := wsagent.ReadRecord(dir, strings.TrimPrefix(agyRes.Address, "id:"))
	if err != nil || agyChild.Backend != wsagent.BackendAgy || agyChild.SpawnerBackend != wsagent.BackendAgy {
		t.Fatalf("agy child record = %+v, %v; want an agy child stamped with its agy spawner", agyChild, err)
	}

	// The external fixed tier qualifies the role tier's size: agent.spawn's
	// dotted tier takes no role-tier node.
	raw = callToolWithKey(t, s, 4, f.hosted, "agent.spawn", map[string]any{"prompt": "Run the playbook at the system prompt path.", "system_prompt_path": promptPath, "tier": "claude." + tier, "authority": "read-only", "format": "json"})
	var res wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil || res.Address == "" {
		t.Fatalf("agent.spawn = %q (%v)", raw, err)
	}
	spec, proc := claude.waitStart(t, 0)
	proc.nextSent(t)
	if spec.Model != "claude-small" {
		t.Fatalf("the Claude child's model = %q, want the claude small tier's", spec.Model)
	}
	instructions, err := os.ReadFile(spec.Instructions)
	if err != nil || !strings.Contains(string(instructions), strings.TrimSpace(string(prompt))) {
		t.Fatalf("the Claude child's instructions lack the rendered prompt: %v\n%s", err, instructions)
	}
	child, err := wsagent.ReadRecord(dir, strings.TrimPrefix(res.Address, "id:"))
	if err != nil || child.Backend != wsagent.BackendClaude {
		t.Fatalf("child record = %+v, %v; want a Claude child", child, err)
	}
	agy.mu.Lock()
	defer agy.mu.Unlock()
	if len(agy.starts) != 1 {
		t.Fatalf("the agy backend started %d process(es), want the as-returned child only", len(agy.starts))
	}
}

// TestAgySubagentConfigOverridesLiveRender verifies all four combinations of
// agy.prefer_native_subagent and agy.avoid_agy_subagent across workflow_manual,
// playbook.read, and RenderLeadManual/RenderLeadPlaybook, confirms live
// per-render updates on an active connection without reconnecting, and pins
// playbook.render unchanged.
func TestAgySubagentConfigOverridesLiveRender(t *testing.T) {
	regs := agyRegistrations(true)
	reg := wsagent.BuiltinBackendRegistrations()[wsagent.BackendAgy]
	agyReg := regs[wsagent.BackendAgy]
	agyReg.ResolveLeadSubagent = reg.ResolveLeadSubagent
	regs[wsagent.BackendAgy] = agyReg
	s, f := hostedBackendConnection(t, wsagent.BackendAgy, regs)
	cfg := s.sessionConfigOptions(f.hosted)
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	setProject := func(k, v string) {
		t.Helper()
		if err := resolver.Set(k, v, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeProject}); err != nil {
			t.Fatal(err)
		}
	}
	hosted := HostedRender{
		Harness:              reg.ManualHarness,
		PlaybookCommands:     reg.PlaybookCommands,
		PreferNativeSubagent: reg.PreferNativeSubagent,
		ResolveLeadSubagent:  reg.ResolveLeadSubagent,
		ToolNameScheme:       reg.ToolNameScheme,
		CallFallback:         reg.CallFallback,
		DirectTools:          reg.DirectTools,
	}

	// Consume the one-shot review-track nudge before taking the baseline.
	_ = connectionManual(t, s, f.hosted)

	baselineRenderExplore, _ := connectionRender(t, s, f.hosted, f.root, "explore")
	baselineManual := connectionManual(t, s, f.hosted)
	baselineRun := connectionRead(t, s, f.hosted, "lead-run")
	baselineDiscuss := connectionRead(t, s, f.hosted, "lead-discuss")

	// (true, false) matches (true, true) byte-for-byte.
	setProject(wsconfig.ItemAgyAvoidAgySubagent, "false")
	if got := connectionManual(t, s, f.hosted); got != baselineManual {
		t.Fatalf("manual changed with prefer=true avoid=false")
	}
	if got := connectionRead(t, s, f.hosted, "lead-run"); got != baselineRun {
		t.Fatalf("lead-run changed with prefer=true avoid=false")
	}
	if got := connectionRead(t, s, f.hosted, "lead-discuss"); got != baselineDiscuss {
		t.Fatalf("lead-discuss changed with prefer=true avoid=false")
	}

	// (false, true): flip in project config WITHOUT reconnecting s.
	setProject(wsconfig.ItemAgyPreferNativeSubagent, "false")
	setProject(wsconfig.ItemAgyAvoidAgySubagent, "true")
	manualFT := connectionManual(t, s, f.hosted)
	assertContains(t, "manual (false,true)", manualFT, "never use `invoke_subagent`", "ws/agent.spawn", "Antigravity terms risk", "never put a harness prefix before a role tier")
	assertLacks(t, "manual (false,true)", manualFT, "your native subagent", "When a subagent started with")
	runFT := connectionRead(t, s, f.hosted, "lead-run")
	assertContains(t, "lead-run (false,true)", runFT, "never use `invoke_subagent`", "ws/agent.spawn", "Antigravity terms risk")
	assertLacks(t, "lead-run (false,true)", runFT, "your native subagent", "When a subagent started with")
	discussFT := connectionRead(t, s, f.hosted, "lead-discuss")
	assertContains(t, "lead-discuss (false,true)", discussFT, "never use `invoke_subagent`", piMCPOnlyExplore, genericTip)
	assertLacks(t, "lead-discuss (false,true)", discussFT, "your native subagent", "When a subagent started with")

	t.Setenv(envHostedSession, "")
	daemonManualFT, err := RenderLeadManual(os.Environ(), f.root, hosted, f.hosted)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "daemon manual (false,true)", daemonManualFT, "never use `invoke_subagent`", "Antigravity terms risk")
	daemonDiscussFT, err := RenderLeadPlaybook(os.Environ(), f.root, hosted, f.hosted, "lead-discuss")
	if err != nil {
		t.Fatal(err)
	}
	if daemonDiscussFT != strings.TrimSpace(discussFT) {
		t.Fatalf("daemon discuss differs from live read under (false,true)")
	}

	// (false, false): flip on same connection again.
	setProject(wsconfig.ItemAgyAvoidAgySubagent, "false")
	manualFF := connectionManual(t, s, f.hosted)
	assertContains(t, "manual (false,false)", manualFF, "never use `invoke_subagent`", "ws/agent.spawn", "never put a harness prefix before a role tier")
	assertLacks(t, "manual (false,false)", manualFF, nativeFirstMarkers...)
	runFF := connectionRead(t, s, f.hosted, "lead-run")
	assertContains(t, "lead-run (false,false)", runFF, "never use `invoke_subagent`", "ws/agent.spawn", "never put a harness prefix before a role tier")
	assertLacks(t, "lead-run (false,false)", runFF, nativeFirstMarkers...)

	// playbook.render stays untouched under every combination.
	if afterRender, _ := connectionRender(t, s, f.hosted, f.root, "explore"); afterRender != baselineRenderExplore {
		t.Fatalf("playbook.render changed under agy subagent overrides")
	}

	// Restore (true, true) on the same live connection without reconnect.
	setProject(wsconfig.ItemAgyPreferNativeSubagent, "true")
	if got := connectionManual(t, s, f.hosted); got != baselineManual {
		t.Fatalf("live manual did not revert to baseline without reconnect")
	}
}
