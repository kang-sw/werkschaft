package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// harnessExtensionRegistrations adds a backend no common code knows by name: it declares a
// bridge client name, names itself, and may opt in to daemon delegation, so
// the detector and the render read registration data alone.
func harnessExtensionRegistrations() map[string]wsagent.BackendRegistration {
	regs := wsagent.BuiltinBackendRegistrations()
	regs["fixture"] = wsagent.BackendRegistration{
		LeadProfile: "fixture-lead",
		Connection: wsagent.ConnectionPolicy{
			NamedClient:        true,
			BridgeClientName:   "fixture-bridge",
			ClientDaemonAgents: true,
		},
		Lifecycle: wsagent.LifecyclePolicy{TerminalSummaries: true},
	}
	return regs
}

func registryServer(t *testing.T, regs map[string]wsagent.BackendRegistration) *Server {
	t.Helper()
	return NewServerWithOptions(ServerOptions{Root: t.TempDir(), Version: "test",
		AgentBackends: wsagent.NewRunner(wsagent.Options{Backends: regs})})
}

// TestInitializeDetectionAcrossHarnesses: clientInfo detection for all four
// supported harnesses, the legacy bridge, and unknown clients, on a server with no daemon registry
// (a terminal harness's stdio server) and with the full built-in registry.
func TestInitializeDetectionAcrossHarnesses(t *testing.T) {
	servers := map[string]*Server{
		"no registry":       {},
		"builtin registry":  registryServer(t, wsagent.BuiltinBackendRegistrations()),
		"fixture registry":  registryServer(t, harnessExtensionRegistrations()),
		"no-declaration pi": registryServer(t, descriptorRegistrations()),
	}
	cases := []struct {
		name, raw, want string
		// registryOnly: resolved through the daemon's registration, so a
		// server with no registry reads nothing.
		registryOnly bool
	}{
		{name: "pi bridge", raw: `{"clientInfo":{"name":"ws-pi-bridge"}}`, want: "pi"},
		{name: "pi bridge padded", raw: `{"clientInfo":{"name":"  ws-pi-bridge "}}`, want: "pi"},
		{name: "pi bridge is case-exact", raw: `{"clientInfo":{"name":"WS-PI-BRIDGE"}}`, want: ""},
		{name: "retired native name", raw: `{"clientInfo":{"name":"Pi"}}`, want: ""},
		{name: "claude by name", raw: `{"clientInfo":{"name":"claude"}}`, want: "claude"},
		{name: "claude cli", raw: `{"clientInfo":{"name":"claude-code","version":"2"}}`, want: "claude"},
		{name: "codex by name", raw: `{"clientInfo":{"name":"codex"}}`, want: "codex"},
		{name: "codex by marker", raw: `{"clientInfo":{"name":"x"},"capabilities":{"x-codex-turn-metadata":{}}}`, want: "codex"},
		{name: "anthropic marker", raw: `{"clientInfo":{"name":"vendor","version":"anthropic-1"}}`, want: "claude"},
		{name: "codex wins over claude", raw: `{"clientInfo":{"name":"x","version":"claude codex"}}`, want: "codex"},
		{name: "unknown client", raw: `{"clientInfo":{"name":"some-editor"}}`, want: ""},
		{name: "no clientInfo", raw: `{}`, want: ""},
		{name: "agy through its registration", raw: `{"clientInfo":{"name":"agy"}}`, want: wsagent.BackendAgy, registryOnly: true},
	}
	for label, s := range servers {
		for _, tc := range cases {
			want := tc.want
			if tc.registryOnly && label == "no registry" {
				want = ""
			}
			if got := s.detectHarnessFromInitializeParams(json.RawMessage(tc.raw)); got != want {
				t.Errorf("%s / %s: detected %q, want %q", label, tc.name, got, want)
			}
		}
	}
}

// TestInitializeDetectionReadsDeclaredNames: a registered extension's own
// bridge name and self-naming client are detected from its declaration, with
// no change to common code.
func TestInitializeDetectionReadsDeclaredNames(t *testing.T) {
	s := registryServer(t, harnessExtensionRegistrations())
	for raw, want := range map[string]string{
		`{"clientInfo":{"name":"fixture-bridge"}}`: "fixture",
		`{"clientInfo":{"name":"Fixture"}}`:        "fixture",
		`{"clientInfo":{"name":"fixture-other"}}`:  "",
	} {
		if got := s.detectHarnessFromInitializeParams(json.RawMessage(raw)); got != want {
			t.Errorf("detect %s = %q, want %q", raw, got, want)
		}
	}
	// A server that has no such registration does not know the name.
	if got := (&Server{}).detectHarnessFromInitializeParams(json.RawMessage(`{"clientInfo":{"name":"fixture-bridge"}}`)); got != "" {
		t.Errorf("no registry detected %q for an extension's bridge", got)
	}
}

// TestDetectHarnessFromRawKeepsExternalMarkers pins the (1881) survivors: the
// raw substrings and their precedence, and Codex's workspace metadata key.
func TestDetectHarnessFromRawKeepsExternalMarkers(t *testing.T) {
	for raw, want := range map[string]string{
		`{"a":"x-codex-turn-metadata"}`: "codex",
		`{"a":"Codex CLI"}`:             "codex",
		`{"a":"Claude Code"}`:           "claude",
		`{"a":"by Anthropic"}`:          "claude",
		`{"a":"claude and codex"}`:      "codex",
		`{"a":"agy"}`:                   "",
		`{"a":"ws-pi-bridge"}`:          "",
		``:                              "",
	} {
		if got := detectHarnessFromRaw(json.RawMessage(raw)); got != want {
			t.Errorf("detectHarnessFromRaw(%s) = %q, want %q", raw, got, want)
		}
	}
	meta := map[string]any{"x-codex-turn-metadata": map[string]any{"workspaces": map[string]any{"/w": map[string]any{}}}}
	if got := detectHarnessFromMeta(meta); got != "codex" {
		t.Errorf("detectHarnessFromMeta(codex workspaces) = %q", got)
	}
	if got := detectHarnessFromMeta(map[string]any{"client": "claude-code"}); got != "claude" {
		t.Errorf("detectHarnessFromMeta(claude) = %q", got)
	}
}

// TestConnectionDeclarationDrivesCapabilities: the daemon-agents opt-in and
// the terminal summary gate read the connection's harness registration, with
// the built-in floor for a server that has none.
func TestConnectionDeclarationDrivesCapabilities(t *testing.T) {
	ext := registryServer(t, harnessExtensionRegistrations())
	if !ext.connectionOf("fixture").ClientDaemonAgents || !ext.lifecycleOf("fixture").TerminalSummaries {
		t.Fatal("a registered extension's declarations are not read")
	}
	if ext.connectionOf("fixture-missing").ClientDaemonAgents {
		t.Fatal("an unregistered name declares nothing")
	}
	bare := &Server{}
	for harness, want := range map[string]bool{"pi": false, "claude": false, "codex": false, "agy": false, "": false} {
		if got := bare.connectionOf(harness).ClientDaemonAgents; got != want {
			t.Errorf("no registry: %q opts in to daemon agents = %v, want %v", harness, got, want)
		}
		if got := bare.lifecycleOf(harness).TerminalSummaries; got != want {
			t.Errorf("no registry: %q takes terminal summaries = %v, want %v", harness, got, want)
		}
	}
}

// TestInitializeRecordsBridgeOnlyRegistration: a registration that declares
// only a bridge client name (no NamedClient, no ManualHarness) is recorded by
// the initialize request itself, not just matched by the detector.
func TestInitializeRecordsBridgeOnlyRegistration(t *testing.T) {
	regs := wsagent.BuiltinBackendRegistrations()
	regs["bridgeonly"] = wsagent.BackendRegistration{
		LeadProfile: "bridgeonly-lead",
		Connection:  wsagent.ConnectionPolicy{BridgeClientName: "bridgeonly-bridge"},
	}
	s := registryServer(t, regs)
	if got := s.normalizedHarness("bridgeonly"); got != "" {
		t.Fatalf("fixture must be rejected by name normalization, got %q", got)
	}
	params := json.RawMessage(`{"clientInfo":{"name":"bridgeonly-bridge"}}`)
	s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: params})
	if got := s.currentHarness(); got != "bridgeonly" {
		t.Fatalf("initialize recorded harness %q, want bridgeonly", got)
	}
	// A hello (observeHarness) keeps the strict name check.
	strict := registryServer(t, regs)
	strict.observeHarness("hello", "bridgeonly")
	if got := strict.currentHarness(); got != "" {
		t.Fatalf("hello recorded %q for a bridge-only name, want none", got)
	}
}
