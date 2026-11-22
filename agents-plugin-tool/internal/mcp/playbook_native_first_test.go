package mcp

import (
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// A hosted lead whose backend registration sets PreferNativeSubagent (agy)
// reads native-first terminology in the text it executes itself
// (workflow_manual, playbook.read): exploration goes to its native subagent
// and spawns go to agent.spawn on an external harness. A prompt rendered for
// another process (playbook.render) keeps today's daemon-agent wording.

const (
	nativeFirstExplore = "your native subagent (no ws tools, your harness's default model; when the question needs a specific tier, a read-only `ws/agent.spawn` subagent at an external-harness tier)"
	nativeFirstSpawn   = "`ws/agent.spawn` on an external harness (tier: the resolved capability tier, not a model name, as a `claude.<size>`, `codex.<size>`, or configured route target; put `claude.` or `codex.` before a bare size or a role tier that resolves to your own harness; authority: at most your own)"
	nativeFirstTip     = "When a subagent started with `ws/agent.spawn` returns an agent id, use `" + piMCPOnlyContinue + "`"
	daemonAgentExplore = piMCPOnlyExplore + " at the tier the question needs"
	daemonAgentSpawn   = "`ws/agent.spawn` (tier: the resolved capability tier, not a model name; authority: at most your own)"
	genericTip         = "When the subagent returns an agent id, use `" + piMCPOnlyContinue + "`"
)

// nativeFirstMarkers are phrases only the native-first wording carries.
var nativeFirstMarkers = []string{"your native subagent", "external-harness tier", "on an external harness", "When a subagent started with"}

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
	on.pi.agents = wsagent.NewRunner(wsagent.Options{Backends: agyRegistrations(true)})
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
	assertContains(t, "native-first manual", manual, nativeFirstExplore+" at the tier the question needs")
	assertLacks(t, "native-first manual", manual, daemonAgentExplore, "{{")
	assertContains(t, "today's manual", connectionManual(t, off, f.hosted), daemonAgentExplore)
	assertLacks(t, "today's manual", connectionManual(t, off, f.hosted), nativeFirstMarkers...)

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
	assertContains(t, "daemon manual", daemonManual, nativeFirstExplore)
	daemonDiscuss, err := RenderLeadPlaybook(os.Environ(), f.root, render, f.hosted, "lead-discuss")
	if err != nil {
		t.Fatal(err)
	}
	if daemonDiscuss != strings.TrimSpace(discuss) {
		t.Fatalf("the daemon render differs from the connection's read:\n--- render\n%s\n--- read\n%s", daemonDiscuss, discuss)
	}
}

// TestNativeFirstLeavesPlaybookRenderUnchanged: a delegate prompt the agy
// lead renders (an explore delegate, the design reviewer) runs on another
// process, so it is byte-identical with and without the option and names no
// native subagent. The explore render recommends a bare size; the lead's
// size-prefix rule qualifies it to the Claude target that wsagent's
// TestAgyLeadHarnessQualifiedTierStartsClaudeChild admits.
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
		if name == "explore" {
			if afterTier != "small" {
				t.Fatalf("explore recommends %q; the size-prefix rule expects a bare size", afterTier)
			}
			if qualified := "claude." + afterTier; qualified != "claude.small" {
				t.Fatalf("qualified target = %q", qualified)
			}
		}
	}
}

// TestNativeFirstOtherHarnessesUnchanged: Claude, Codex, and Pi do not set
// PreferNativeSubagent, so none of their hosted leads' manual,
// playbook.read, or playbook.render outputs carries the native-first
// wording; the exact-wording tests of those rows pin the rest unchanged.
func TestNativeFirstOtherHarnessesUnchanged(t *testing.T) {
	for _, backend := range []string{wsagent.BackendClaude, wsagent.BackendCodex, wsagent.BackendPi} {
		regs := wsagent.BuiltinBackendRegistrations()
		if regs[backend].PreferNativeSubagent {
			t.Fatalf("%s sets PreferNativeSubagent", backend)
		}
		s, f := hostedBackendConnection(t, backend, regs)
		for label, text := range map[string]string{
			"manual":       connectionManual(t, s, f.hosted),
			"lead-run":     connectionRead(t, s, f.hosted, "lead-run"),
			"lead-discuss": connectionRead(t, s, f.hosted, "lead-discuss"),
		} {
			assertLacks(t, backend+" "+label, text, nativeFirstMarkers...)
		}
		for _, name := range []string{"explore", "ticket-reviewer-design"} {
			body, _ := connectionRender(t, s, f.hosted, f.root, name)
			assertLacks(t, backend+" "+name+" render", body, nativeFirstMarkers...)
		}
	}
}
