package mcp

import (
	"encoding/json"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
)

func codexWorkspaceRoots(meta map[string]any) []string {
	// Retained name check: x-codex-turn-metadata is Codex's own request metadata key
	// (TestDetectHarnessFromRawKeepsExternalMarkers).
	turn, ok := meta["x-codex-turn-metadata"].(map[string]any)
	if !ok {
		return nil
	}
	rawWorkspaces, ok := turn["workspaces"].(map[string]any)
	if !ok || len(rawWorkspaces) == 0 {
		return nil
	}
	roots := make([]string, 0, len(rawWorkspaces))
	for root := range rawWorkspaces {
		if strings.TrimSpace(root) != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

// observeHarness records a harness name a client reported (a hello, call
// metadata), accepted only when normalizedHarness knows it.
func (s *Server) observeHarness(source, harness string) {
	s.recordHarness(source, s.normalizedHarness(harness))
}

// recordHarness records a harness the caller already resolved, without
// re-normalizing it: initialize detection resolves a bridge-only registration
// (ConnectionPolicy.BridgeClientName without NamedClient or ManualHarness),
// which the strict name check above would reject a second time.
func (s *Server) recordHarness(source, harness string) {
	if harness == "" {
		return
	}
	s.rootMu.Lock()
	defer s.rootMu.Unlock()
	if s.sessionHarness == "" {
		s.sessionHarness = harness
		s.debugEvent("harness.detected", map[string]any{"source": source, "harness": harness})
		return
	}
	if s.sessionHarness != harness {
		s.debugEvent("harness.conflict", map[string]any{
			"source":   source,
			"current":  s.sessionHarness,
			"observed": harness,
		})
	}
}

// observePiDaemonAgents records the client's explicit model-facing opt-in.
// Missing or malformed metadata never implies support. The capability key
// wsPiDaemonAgents is the Pi bridge's own external identifier (1881 survivor);
// which backend may use it is its ConnectionPolicy.ClientDaemonAgents.
func (s *Server) observePiDaemonAgents(raw json.RawMessage) {
	var params struct {
		Capabilities struct {
			Experimental struct {
				PiDaemonAgents bool `json:"wsPiDaemonAgents"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		params.Capabilities.Experimental.PiDaemonAgents = false
	}
	s.rootMu.Lock()
	s.piDaemonAgents = params.Capabilities.Experimental.PiDaemonAgents
	s.rootMu.Unlock()
}

func (s *Server) clientPiDaemonAgents() bool {
	s.rootMu.RLock()
	defer s.rootMu.RUnlock()
	return s.piDaemonAgents
}

func (s *Server) currentHarness() string {
	s.rootMu.RLock()
	defer s.rootMu.RUnlock()
	return s.sessionHarness
}

// connectionOf is the connection declaration of the backend called name: the
// daemon's registration when it has one, else the built-in floor, which a
// connection with no daemon registry (a terminal harness's stdio server)
// reads. A name neither knows declares nothing.
func (s *Server) connectionOf(name string) wsagent.ConnectionPolicy {
	if lookup := s.policyLookup(); lookup != nil {
		if policy, ok := lookup.BackendConnection(name); ok {
			return policy
		}
		return wsagent.BuiltinConnection(name)
	}
	for _, registrations := range s.agentBackends() {
		if reg, ok := registrations[name]; ok {
			return reg.Connection
		}
	}
	return wsagent.BuiltinConnection(name)
}

// lifecycleOf is connectionOf for a backend's lifecycle declaration.
func (s *Server) lifecycleOf(name string) wsagent.LifecyclePolicy {
	if lookup := s.policyLookup(); lookup != nil {
		if policy, ok := lookup.BackendLifecycle(name); ok {
			return policy
		}
		return wsagent.BuiltinLifecycle(name)
	}
	for _, registrations := range s.agentBackends() {
		if reg, ok := registrations[name]; ok {
			return reg.Lifecycle
		}
	}
	return wsagent.BuiltinLifecycle(name)
}

// detectHarnessFromInitializeParams parses the "initialize" request's
// clientInfo.name via structured JSON first: an exact match on a backend's
// declared bridge client name (ConnectionPolicy.BridgeClientName, the Pi
// extension's "ws-pi-bridge") identifies that harness. Other names are
// accepted through the backend registrations (normalizedHarness); anything
// else (or an absent clientInfo) falls through to the substring-based
// detectHarnessFromRaw, which stays byte-identical for Codex/Claude detection.
func (s *Server) detectHarnessFromInitializeParams(raw json.RawMessage) string {
	var params struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(raw, &params); err == nil {
		if harness := s.bridgeHarness(strings.TrimSpace(params.ClientInfo.Name)); harness != "" {
			return harness
		}
		if harness := s.normalizedHarness(params.ClientInfo.Name); harness != "" {
			return harness
		}
	}
	return detectHarnessFromRaw(raw)
}

// bridgeHarness is the backend whose declared bridge client name is exactly
// name, "" for none.
func (s *Server) bridgeHarness(name string) string {
	if name == "" {
		return ""
	}
	for _, registrations := range s.agentBackends() {
		for harness, reg := range registrations {
			if reg.Connection.BridgeClientName == name {
				return harness
			}
		}
	}
	for _, named := range wsagent.BuiltinConnections() {
		if named.Connection.BridgeClientName == name {
			return named.Name
		}
	}
	return ""
}

// detectHarnessFromRaw sniffs a harness out of the raw initialize or call
// metadata a client sent, for clients that name no registered harness. The
// substrings and their precedence (codex before claude) are the external
// payloads of those clients as detection has always read them (1881 survivor;
// TestDetectHarnessFromRawKeepsExternalMarkers), not names registration data
// could reorder.
func detectHarnessFromRaw(raw json.RawMessage) string {
	text := strings.ToLower(string(raw))
	if text == "" {
		return ""
	}
	if strings.Contains(text, "x-codex-turn-metadata") || strings.Contains(text, "codex") {
		return "codex"
	}
	if strings.Contains(text, "claude") || strings.Contains(text, "anthropic") {
		return "claude"
	}
	return ""
}

// detectHarnessFromMeta reads a call's metadata: Codex's workspace-root key is
// its external identifier (1881 survivor), else the raw sniff.
func detectHarnessFromMeta(meta map[string]any) string {
	if len(codexWorkspaceRoots(meta)) > 0 {
		return "codex"
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return detectHarnessFromRaw(raw)
}

func (s *Server) normalizedHarness(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	// A backend whose client names itself (ConnectionPolicy.NamedClient) is
	// accepted by its own name, registered with the daemon or not.
	for _, registrations := range s.agentBackends() {
		if reg, ok := registrations[value]; ok && reg.Connection.NamedClient {
			return value
		}
	}
	for _, named := range wsagent.BuiltinConnections() {
		if named.Connection.NamedClient && named.Name == value {
			return value
		}
	}
	// Hosted backends declare the harness name their manual and config use.
	// Accept that descriptor instead of teaching common MCP code each backend.
	for _, registrations := range s.agentBackends() {
		for _, registration := range registrations {
			if strings.EqualFold(strings.TrimSpace(registration.ManualHarness), value) {
				return value
			}
		}
	}
	return ""
}
