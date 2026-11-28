package wsagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// TestBuiltinSupportProfilesResolve: each harness has a builtin support
// profile that resolves through any root with no config file, allows the
// tracker tools, the wiki tools, git.search, and playbook.read (and, on
// Claude, the Agent tool alone), and names the embedded instruction text,
// never a file under the root.
func TestBuiltinSupportProfilesResolve(t *testing.T) {
	opts := testConfig(t)
	root := t.TempDir()
	for name, backend := range map[string]string{SupportProfileClaude: BackendClaude} {
		rp, err := ResolveProfile(opts, root, name)
		if err != nil {
			t.Fatalf("ResolveProfile(%s): %v", name, err)
		}
		p := rp.Definition
		if rp.Scope != wsconfig.ScopeBuiltin || p.Backend != backend {
			t.Fatalf("%s should resolve from the builtin floor on %s: %+v", name, backend, rp)
		}
		for _, tool := range []string{"tracker.query", "tracker.update", "tracker.show", "tracker.ui", "wiki.query", "wiki.update", "git.search", "playbook.read"} {
			if !slices.Contains(p.WSTools, tool) {
				t.Errorf("%s ws_tools %v lack %s", name, p.WSTools, tool)
			}
		}
		// Claude's support agent may spawn subagents (Agent, nothing else);
		// Pi has no subagent tool, so its profile allows no built-in tool.
		wantTools := []string(nil)
		if backend == BackendClaude {
			wantTools = []string{"Agent"}
		}
		if !slices.Equal(p.Tools, wantTools) {
			t.Errorf("%s built-in tools = %v, want %v", name, p.Tools, wantTools)
		}
		if len(p.MCPServers) != 0 || len(p.Plugins) != 0 || p.Skills || p.ListInIndex {
			t.Errorf("%s should allow no server, plugin, or skill and stay off the index: %+v", name, p)
		}
		if p.InstructionsPath(root) != "" {
			t.Errorf("%s instructions should not resolve to a file: %s", name, p.InstructionsPath(root))
		}
		text, ok := p.BuiltinInstructions()
		if !ok || !strings.Contains(text, "tracker.show") || !strings.Contains(text, "tracker.ui") || !strings.Contains(text, "git.search") || !strings.Contains(text, "wiki.update") {
			t.Errorf("%s should carry the embedded support text, got %v %q", name, ok, text)
		}
	}
	resolver := wsconfig.NewResolver(opts, nil, nil, nil)
	all, err := ResolveProfiles(&resolver)
	if err != nil || len(all) != len(BuiltinProfiles()) {
		t.Fatalf("ResolveProfiles with no config should list all builtin profiles: %+v, %v", all, err)
	}
}

// TestBuiltinAgentWorkerProfiles: each worker harness has an internal
// agent-worker profile with the common ws tools and any backend additions.
func TestBuiltinAgentWorkerProfiles(t *testing.T) {
	for backend, tools := range map[string][]string{BackendClaude: AgentClaudeTools, BackendAgy: nil, BackendCodex: nil, BackendWerk: nil, BackendPiRPC: nil} {
		name := AgentWorkerProfile(backend)
		rp, err := ResolveProfile(testConfig(t), t.TempDir(), name)
		if err != nil {
			t.Fatalf("ResolveProfile(%s): %v", name, err)
		}
		p := rp.Definition
		wsTools := AgentWSTools
		if backend == BackendWerk {
			wsTools = append(slices.Clone(wsTools), "fs.edit", "fs.write", "web.fetch")
		}
		if rp.Scope != wsconfig.ScopeBuiltin || p.Backend != backend || p.Lead || !slices.Equal(p.Tools, tools) || !slices.Equal(p.WSTools, wsTools) {
			t.Fatalf("%s = %+v", name, rp)
		}
		for _, tool := range []string{"agent.spawn", "agent.interrupt", "agent.list", "agent.tiers", "mailbox.send", "config.resolve_agent", "infra.read", "path.write"} {
			if !slices.Contains(p.WSTools, tool) {
				t.Errorf("%s ws_tools %v lack %s", name, p.WSTools, tool)
			}
		}
		for _, tool := range p.WSTools {
			if strings.HasPrefix(tool, "pi.") {
				t.Errorf("%s ws_tools carry %s", name, tool)
			}
		}
		if !isAgentWorkerProfile(name) {
			t.Errorf("%s is not recognized as an agent-worker profile", name)
		}
	}
	if !isAgentWorkerProfile(AgentWorkerProfile("future")) || isAgentWorkerProfile("pi-worker") {
		t.Error("the internal namespace is reserved even before a backend is registered")
	}
}

// TestBuiltinLeadProfilesResolve: each harness has a builtin lead profile
// that names no capability of its own (the harness loads the user's setup),
// stays off the session index, and starts claude in the default permission
// mode; a user may not define a lead profile in a repository's config.
func TestBuiltinLeadProfilesResolve(t *testing.T) {
	opts := testConfig(t)
	root := t.TempDir()
	for backend, name := range map[string]string{BackendClaude: LeadProfileClaude, BackendCodex: LeadProfileCodex, BackendAgy: LeadProfileAgy, BackendWerk: LeadProfileWerk, BackendPiRPC: LeadProfilePiRPC} {
		if LeadProfileFor(backend) != name {
			t.Fatalf("LeadProfileFor(%s) = %q, want %q", backend, LeadProfileFor(backend), name)
		}
		rp, err := ResolveProfile(opts, root, name)
		if err != nil {
			t.Fatalf("ResolveProfile(%s): %v", name, err)
		}
		p := rp.Definition
		if !p.Lead || p.Backend != backend || rp.Scope != wsconfig.ScopeBuiltin {
			t.Fatalf("%s = %+v (scope %s), want a builtin lead profile on %s", name, p, rp.Scope, backend)
		}
		if len(p.Tools)+len(p.WSTools)+len(p.MCPServers)+len(p.Plugins) != 0 || p.Skills || p.ListInIndex || p.InstructionsPath(root) != "" {
			t.Errorf("%s should name no capability and stay off the index: %+v", name, p)
		}
	}
	if LeadProfileFor("other") != "" {
		t.Fatal("LeadProfileFor should name no profile for an unknown harness")
	}
	claude, codex := builtinProfile(t, LeadProfileClaude), builtinProfile(t, LeadProfileCodex)
	if claude.PermissionMode != DefaultLeadPermissionMode || codex.PermissionMode != "" {
		t.Fatalf("permission modes = %q, %q; want the default for claude and none for codex", claude.PermissionMode, codex.PermissionMode)
	}
	// Both lead backends default to never idle-stopping.
	if claude.IdleTimeout() != 0 || codex.IdleTimeout() != 0 {
		t.Fatalf("idle timeouts = %s, %s; want zero for both", claude.IdleTimeout(), codex.IdleTimeout())
	}
	for _, bad := range []string{
		`{"backend":"claude","permission_mode":"auto"}`,
		`{"backend":"claude","lead":true,"permission_mode":"yolo"}`,
		`{"backend":"claude","lead":true,"tools":["Bash"]}`,
		`{"backend":"claude","lead":true,"list_in_index":true}`,
	} {
		if _, err := ParseProfile(json.RawMessage(bad)); err == nil {
			t.Errorf("ParseProfile(%s) should be refused", bad)
		}
	}
	lead, err := ParseProfile(json.RawMessage(`{"backend":"claude","lead":true,"permission_mode":"plan"}`))
	if err != nil {
		t.Fatalf("a user lead profile should parse: %v", err)
	}
	if err := CheckProfileScope(lead, wsconfig.ScopeRepo); err == nil {
		t.Fatal("a lead profile in a repository's config should be refused")
	}
	if err := CheckProfileScope(lead, wsconfig.ScopeGlobal); err != nil {
		t.Fatalf("a lead profile in the user's own config should be allowed: %v", err)
	}
}

func builtinProfile(t *testing.T, name string) Profile {
	t.Helper()
	var p Profile
	if err := json.Unmarshal(BuiltinProfiles()[ProfilePrefix+name], &p); err != nil {
		t.Fatalf("builtin %s: %v", name, err)
	}
	return p
}

// TestBuiltinSupportProfileOverriddenWhole: a global profile of the same
// name replaces the builtin one with no field merge.
func TestBuiltinSupportProfileOverriddenWhole(t *testing.T) {
	opts := testConfig(t)
	writeStructured(t, filepath.Join(opts.ConfigHome, "config.json"), map[string]string{
		ProfilePrefix + SupportProfileClaude: `{"backend":"claude","model":"sonnet","ws_tools":["tracker.query"],"instructions":"builtin:tracker-support"}`,
	})
	rp, err := ResolveProfile(opts, t.TempDir(), SupportProfileClaude)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Scope != wsconfig.ScopeGlobal || rp.Definition.Model != "sonnet" || !slices.Equal(rp.Definition.WSTools, []string{"tracker.query"}) {
		t.Fatalf("the global definition should replace the builtin one whole: %+v", rp)
	}
	if _, ok := rp.Definition.BuiltinInstructions(); !ok {
		t.Fatalf("a user profile may name the builtin text too")
	}
}

func TestParseProfileRefusesUnknownBuiltinInstructions(t *testing.T) {
	_, err := ParseProfile(json.RawMessage(`{"backend":"claude","instructions":"builtin:nope"}`))
	if err == nil || !strings.Contains(err.Error(), "builtin:nope") || !strings.Contains(err.Error(), "tracker-support") {
		t.Fatalf("an unknown builtin text should be refused naming the known ones, got %v", err)
	}
}

// TestSupportConversationComposesBuiltinText: a conversation of the builtin
// support profile starts with the embedded text and the session key in its
// instruction file, though its root holds no instruction file.
func TestSupportConversationComposesBuiltinText(t *testing.T) {
	h := newHarness(t, nil)
	profile := SupportProfileClaude
	if suiteBackend == BackendPi {
		profile = SupportProfilePi
	}
	st := h.create(profile)
	if _, err := h.runner.Send(st.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a process start", func() bool { return h.backend.startCount() == 1 })
	spec := h.backend.start(0)
	raw, err := os.ReadFile(spec.Instructions)
	if err != nil {
		t.Fatalf("instruction file: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "# Tracker support agent") || !strings.Contains(text, "Your ws session key is `") {
		t.Fatalf("instruction file should hold the builtin text and the key:\n%s", text)
	}
	if spec.WSMCP == nil || !slices.Contains(spec.Profile.WSTools, "tracker.show") {
		t.Fatalf("the support profile attaches ws-mcp with its ws tools: %+v", spec)
	}
}
