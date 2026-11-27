package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// --- Phase 3a: stripModeGatedRegion pure-logic tests (TDD) -------------------

func TestStripModeGatedRegion_NoMarker(t *testing.T) {
	body := "line one\nline two\nline three"
	// Without any marker, the body should be unchanged regardless of keepContent.
	if got := stripModeGatedRegion(body, true); got != body {
		t.Fatalf("keepContent=true mutated marker-free body:\n got: %q\nwant: %q", got, body)
	}
	if got := stripModeGatedRegion(body, false); got != body {
		t.Fatalf("keepContent=false mutated marker-free body:\n got: %q\nwant: %q", got, body)
	}
}

func TestStripModeGatedRegion_KeepContent(t *testing.T) {
	// keepContent=true: marker lines removed, inner content kept.
	body := "before\n<!-- ws:fresh-only:start -->\nbootstrap line\n<!-- ws:fresh-only:end -->\nafter"
	want := "before\nbootstrap line\nafter"
	if got := stripModeGatedRegion(body, true); got != want {
		t.Fatalf("keepContent=true:\n got: %q\nwant: %q", got, want)
	}
}

func TestStripModeGatedRegion_StripContent(t *testing.T) {
	// keepContent=false: marker lines AND inner content removed.
	body := "before\n<!-- ws:fresh-only:start -->\nbootstrap line\n<!-- ws:fresh-only:end -->\nafter"
	want := "before\nafter"
	if got := stripModeGatedRegion(body, false); got != want {
		t.Fatalf("keepContent=false:\n got: %q\nwant: %q", got, want)
	}
}

func TestStripModeGatedRegion_MultipleLines(t *testing.T) {
	// Multiple inner lines in the gated region.
	body := "before\n<!-- ws:fresh-only:start -->\nline A\nline B\nline C\n<!-- ws:fresh-only:end -->\nafter"
	wantKeep := "before\nline A\nline B\nline C\nafter"
	wantStrip := "before\nafter"
	if got := stripModeGatedRegion(body, true); got != wantKeep {
		t.Fatalf("multi-line keep:\n got: %q\nwant: %q", got, wantKeep)
	}
	if got := stripModeGatedRegion(body, false); got != wantStrip {
		t.Fatalf("multi-line strip:\n got: %q\nwant: %q", got, wantStrip)
	}
}

func TestStripModeGatedRegion_MultipleRegions(t *testing.T) {
	// Multiple gated regions in one body.
	body := "a\n<!-- ws:fresh-only:start -->\nR1\n<!-- ws:fresh-only:end -->\nb\n<!-- ws:fresh-only:start -->\nR2\n<!-- ws:fresh-only:end -->\nc"
	wantKeep := "a\nR1\nb\nR2\nc"
	wantStrip := "a\nb\nc"
	if got := stripModeGatedRegion(body, true); got != wantKeep {
		t.Fatalf("multi-region keep:\n got: %q\nwant: %q", got, wantKeep)
	}
	if got := stripModeGatedRegion(body, false); got != wantStrip {
		t.Fatalf("multi-region strip:\n got: %q\nwant: %q", got, wantStrip)
	}
}

func TestStripModeGatedRegion_UnclosedMarker(t *testing.T) {
	// Unclosed start marker: trailing lines treated per keepContent.
	body := "before\n<!-- ws:fresh-only:start -->\norphaned line\nno end marker"
	wantKeep := "before\norphaned line\nno end marker"
	wantStrip := "before"
	if got := stripModeGatedRegion(body, true); got != wantKeep {
		t.Fatalf("unclosed keep:\n got: %q\nwant: %q", got, wantKeep)
	}
	if got := stripModeGatedRegion(body, false); got != wantStrip {
		t.Fatalf("unclosed strip:\n got: %q\nwant: %q", got, wantStrip)
	}
}

// callToolNoKey issues a single tools/call MCP request WITHOUT injecting a
// session_key — needed to test the fresh-mode path of ws.workflow_manual.
func callToolNoKey(t *testing.T, server *Server, id int, name string, args map[string]any) string {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	// Do NOT inject session_key (contrast with callToolWithKey).
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := server.ServeStdio(context.Background(), strings.NewReader(string(raw)+"\n"), &out); err != nil {
		t.Fatalf("ServeStdio(%s) error: %v", name, err)
	}
	byID := responseLinesByID(t, strings.Split(strings.TrimSpace(out.String()), "\n"))
	return toolText(t, byID[fmt.Sprint(id)])
}

// --- Phase 3a: ws.workflow_manual integration tests --------------------------

func TestWorkflowManualFreshMode(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	server := NewServer(root, "test")

	// Fresh mode: call with the reserved sentinel key (not keyless). The sentinel
	// is never minted, so the keyed lead-only gate sees a lookup-miss and skips;
	// the call reaches the handler, which maps the sentinel to fresh mode.
	resp := callToolWithKey(t, server, 5001, freshBootstrapKey, "workflow_manual", nil)
	if !strings.HasPrefix(resp, workflowManualReadFullNotice+"\n\n") {
		t.Fatalf("fresh mode must start with the full-result notice: %s", resp)
	}

	// No "Session State" section in fresh mode.
	if strings.Contains(resp, "Session State") {
		t.Errorf("fresh mode: unexpected Session State section in response:\n%s", resp)
	}
}

func TestWorkflowManualFreshModeWithRoot(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	server := NewServer(root, "test")

	// Fresh-with-root mode: sentinel key + root supplied. The handler must mint a
	// lead session key inline and return Session Key
	// and Session State sections.
	resp := callToolWithKey(t, server, 5010, freshBootstrapKey, "workflow_manual", map[string]any{
		"root": root,
	})

	// (a) Session Key section must be present.
	if !strings.Contains(resp, "## Session Key") {
		t.Errorf("fresh-with-root: ## Session Key section absent from response:\n%s", resp)
	}
	// (c) Session State section must be present.
	if !strings.Contains(resp, "## Session State") {
		t.Errorf("fresh-with-root: ## Session State section absent from response:\n%s", resp)
	}
	// (d) Extract the minted key from the response and verify it resolves to a lead
	//     session entry. The key appears on the first non-empty line after the
	//     "## Session Key" heading.
	var mintedKey string
	inSessionKeySection := false
	for _, line := range strings.Split(resp, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Session Key" {
			inSessionKeySection = true
			continue
		}
		if inSessionKeySection {
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "#") {
				// Next section — key not found
				break
			}
			mintedKey = trimmed
			break
		}
	}
	if mintedKey == "" {
		t.Fatalf("fresh-with-root: could not extract minted key from response:\n%s", resp)
	}
	entry, ok := server.sessions.lookup(mintedKey)
	if !ok {
		t.Fatalf("fresh-with-root: minted key %q does not resolve to a session entry", mintedKey)
	}
	if entry.scope != roleLead {
		t.Errorf("fresh-with-root: minted key scope = %q; want %q", entry.scope, roleLead)
	}
}

func TestWorkflowManualKeylessRejected(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")

	// Keyless call must be rejected with a required session_key error.
	resp := callToolNoKey(t, server, 5002, "workflow_manual", nil)

	if !strings.Contains(resp, "session_key") {
		t.Errorf("keyless: response must mention session_key, got:\n%s", resp)
	}
}

func TestWorkflowManualDelegateKeyBlocked(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")

	// Mint a delegate-scoped key directly.
	delegateKey, err := server.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatalf("mint delegate key: %v", err)
	}

	// ws.workflow_manual must be rejected for delegate keys at the keyed gate.
	// The gate returns an RPC-level error (-32601), not a toolText response, so
	// read the raw JSON line (same pattern as session_auth_test.go assertGateError).
	rawResp := callToolOnce(t, server, 5003, "workflow_manual", map[string]any{
		"session_key": delegateKey,
	})

	// Must receive the lead-only profile rejection (JSON-RPC error -32601).
	if !strings.Contains(rawResp, "tool not available in current") {
		t.Errorf("delegate key: expected lead-only rejection, got:\n%s", rawResp)
	}
	// The rejection must carry the keyed-gate RPC error code (-32601), not a soft
	// text response — confirm the code is present in the raw JSON-RPC error.
	if !strings.Contains(rawResp, "-32601") {
		t.Errorf("delegate key: expected JSON-RPC error code -32601, got:\n%s", rawResp)
	}
	// Must NOT contain a manual body.
	if strings.Contains(rawResp, "Session State") {
		t.Errorf("delegate key: manual body must be absent from rejection response:\n%s", rawResp)
	}
}

func TestWorkflowManualContinueMode(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5100, root, nil))

	// Enter implement mode to populate agenda+todos.
	enter := callToolWithKey(t, server, 5101, key, "route.resolve_implement", implementSkipDocsArgs("text"))
	if !strings.Contains(enter, "Implementation Verdict") {
		t.Fatalf("route.resolve_implement unexpected: %s", enter)
	}

	// Continue mode: key present and resolves.
	resp := callToolWithKey(t, server, 5102, key, "workflow_manual", nil)

	// Session State section must be present.
	if !strings.Contains(resp, "Session State") {
		t.Errorf("continue mode: Session State section absent:\n%s", resp)
	}
	// Agenda content present: renderSessionState emits "### agenda: <key>" headings.
	if !strings.Contains(resp, "### agenda: implement") {
		t.Errorf("continue mode: agenda heading '### agenda: implement' absent:\n%s", resp)
	}
	// Todo summary content present (Route and Prep are active).
	if !strings.Contains(resp, "Route") {
		t.Errorf("continue mode: todo 'Route' absent from summary:\n%s", resp)
	}
	if !strings.Contains(resp, "Prep") {
		t.Errorf("continue mode: todo 'Prep' absent from summary:\n%s", resp)
	}
}

func TestWorkflowManualTodoInstructionPreview(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5110, root, nil))
	longInstruction := "Restore this todo instruction through the workflow manual summary path without showing the extra full detail."
	wantPreview := "Restore this todo instruction through the workflow manual su"
	wantTail := "mmary path without showing the extra full detail."

	if got := callToolWithKey(t, server, 5111, key, "todo.add", map[string]any{
		"key":         "restore",
		"title":       "Restore instruction",
		"instruction": longInstruction,
	}); !strings.Contains(got, "todo added: restore") {
		t.Fatalf("append unexpected: %s", got)
	}

	resp := callToolWithKey(t, server, 5112, key, "workflow_manual", nil)
	if !strings.Contains(resp, "### Todos") {
		t.Fatalf("workflow manual response missing todo summary:\n%s", resp)
	}
	if !strings.Contains(resp, "- [ ] {restore} Restore instruction\n      "+wantPreview) {
		t.Fatalf("workflow manual missing instruction preview:\n%s", resp)
	}
	if strings.Contains(resp, wantTail) {
		t.Fatalf("workflow manual rendered instruction tail, want preview only:\n%s", resp)
	}
}

func TestWorkflowManualUnknownKey(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("WS_CACHE_HOME", cacheDir)

	server := NewServer(root, "test")

	// Use a syntactically valid but never-minted key.
	badKey := "no-such-key-here"
	resp := callToolWithKey(t, server, 5200, badKey, "workflow_manual", nil)

	// Must contain the no-restorable-state notice.
	if !strings.Contains(resp, "no restorable state for session key") {
		t.Errorf("unknown key: no-restorable-state notice absent:\n%s", resp)
	}
	// Fail-loud renders NO manual body, so the always-shown per-root rule and the
	// ws.ferrule mention it carries must be absent — a non-lead caller reaching
	// fail-loud (any unregistered key) must not learn the lead self-bootstrap call.
	if strings.Contains(resp, "ferrule") {
		t.Errorf("unknown key: manual body / ferrule mention must be absent in fail-loud:\n%s", resp)
	}
	// The recovery pointer names only the lead-revive skill (no ferrule/sentinel).
	if !strings.Contains(resp, "lead-revive") {
		t.Errorf("unknown key: fail-loud notice should point to lead-revive recovery:\n%s", resp)
	}
	// Must NOT have minted a key record. Use os.Stat on the specific record path so
	// the check is meaningful even when the sessions/ directory was never created.
	_, statErr := os.Stat(filepath.Join(cacheDir, sessionsDirName, badKey))
	if !os.IsNotExist(statErr) {
		t.Errorf("unknown key: key record was minted (stat: %v)", statErr)
	}
}

func TestWorkflowManualGitCommitReinjection(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5300, root, nil))

	// Enter implement mode to populate todos.
	callToolWithKey(t, server, 5301, key, "route.resolve_implement", implementSkipDocsArgs("text"))

	// Stage a file and commit.
	testFile := filepath.Join(root, "test-p3a.txt")
	if err := os.WriteFile(testFile, []byte("p3a test\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	// git add
	{
		var out bytes.Buffer
		payload := map[string]any{
			"jsonrpc": "2.0",
			"id":      5302,
			"method":  "tools/call",
			"params": map[string]any{"name": "git.commit", "arguments": map[string]any{
				"session_key":     key,
				"paths":           []any{"test-p3a.txt"},
				"title":           "test(p3a): re-injection test",
				"ai_context":      []any{"Phase 3a git.commit re-injection test"},
				"expected_branch": headBranch(t, root),
			}},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := server.ServeStdio(context.Background(), strings.NewReader(string(raw)+"\n"), &out); err != nil {
			t.Fatalf("git.commit error: %v", err)
		}
		byID := responseLinesByID(t, strings.Split(strings.TrimSpace(out.String()), "\n"))
		commitResp := toolText(t, byID["5302"])

		// Assert that the commit response contains a todo summary fragment.
		if !strings.Contains(commitResp, "## TODO(ws reminder: update this if stale)") {
			t.Errorf("git.commit re-injection: reminder heading absent from response:\n%s", commitResp)
		}
		if !strings.Contains(commitResp, "Route") && !strings.Contains(commitResp, "- [") {
			t.Errorf("git.commit re-injection: todo summary absent from response:\n%s", commitResp)
		}

		// The session-key tip trailer must land after the TODO summary (key
		// line last, for maximal recency/salience).
		wantTip := fmt.Sprintf("tip: preserve this session key: %s during compaction", key)
		if !strings.Contains(commitResp, wantTip) {
			t.Errorf("git.commit re-injection: session-key tip absent from response:\n%s", commitResp)
		}
		if idx := strings.Index(commitResp, "## TODO("); idx == -1 || strings.Index(commitResp, wantTip) < idx {
			t.Errorf("git.commit re-injection: session-key tip must follow the TODO summary trailer:\n%s", commitResp)
		}
	}

	// Also assert: a commit with no todos appends nothing extra.
	// Create a new session with no todos, commit, and check the output shape.
	root2 := t.TempDir()
	initGit(t, root2)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	server2 := NewServer(root2, "test")
	key2, _ := parseLoginResponse(t, callLogin(t, server2, 5400, root2, nil))

	testFile2 := filepath.Join(root2, "test-p3a-notodo.txt")
	if err := os.WriteFile(testFile2, []byte("no todo\n"), 0o644); err != nil {
		t.Fatalf("write test file2: %v", err)
	}
	{
		var out bytes.Buffer
		payload := map[string]any{
			"jsonrpc": "2.0",
			"id":      5401,
			"method":  "tools/call",
			"params": map[string]any{"name": "git.commit", "arguments": map[string]any{
				"session_key":     key2,
				"paths":           []any{"test-p3a-notodo.txt"},
				"title":           "test(p3a): no-todo commit",
				"ai_context":      []any{"no-todo test"},
				"expected_branch": headBranch(t, root2),
			}},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := server2.ServeStdio(context.Background(), strings.NewReader(string(raw)+"\n"), &out); err != nil {
			t.Fatalf("git.commit(no-todo) error: %v", err)
		}
		byID := responseLinesByID(t, strings.Split(strings.TrimSpace(out.String()), "\n"))
		commitResp := toolText(t, byID["5401"])
		// No todo summary section appended.
		if strings.Contains(commitResp, "Todo (post-commit)") || strings.Contains(commitResp, "TODO(ws reminder: update this if stale)") {
			t.Errorf("git.commit(no-todo): unexpected Todo section:\n%s", commitResp)
		}
		// The session-key tip still appends even with no todos.
		wantTip := fmt.Sprintf("tip: preserve this session key: %s during compaction", key2)
		if !strings.Contains(commitResp, wantTip) {
			t.Errorf("git.commit(no-todo): session-key tip absent from response:\n%s", commitResp)
		}
	}
}

func TestGitCommitOmitsTodoReminderForAnAllDoneList(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5500, root, nil))

	commit := func(id int, name string) string {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return callToolWithKey(t, server, id, key, "git.commit", map[string]any{
			"paths":           []any{name},
			"title":           "test: " + name,
			"ai_context":      []any{"todo reminder test"},
			"expected_branch": headBranch(t, root),
		})
	}

	callToolWithKey(t, server, 5501, key, "todo.add", map[string]any{"key": "only", "title": "the only step"})
	if resp := commit(5502, "one.txt"); !strings.Contains(resp, "## TODO(ws reminder: update this if stale)") {
		t.Fatalf("a list with a pending item lost its reminder:\n%s", resp)
	}

	callToolWithKey(t, server, 5503, key, "todo.check", map[string]any{"key": "only", "status": "done"})
	resp := commit(5504, "two.txt")
	if strings.Contains(resp, "## TODO(") {
		t.Fatalf("an all-done list still appended a reminder:\n%s", resp)
	}
	if !strings.Contains(resp, "tip: preserve this session key: "+key) {
		t.Fatalf("session-key tip lost:\n%s", resp)
	}
}

func TestAppendSessionKeyTip(t *testing.T) {
	const key = "scarily-imminent-ploy-plated-84"
	withKey := appendSessionKeyTip("commit ok", key)
	wantTip := fmt.Sprintf("tip: preserve this session key: %s during compaction", key)
	if !strings.Contains(withKey, wantTip) {
		t.Errorf("appendSessionKeyTip: tip absent for non-empty key:\n%s", withKey)
	}
	if !strings.HasPrefix(withKey, "commit ok") {
		t.Errorf("appendSessionKeyTip: original text not preserved:\n%s", withKey)
	}

	noKey := appendSessionKeyTip("commit ok", "")
	if noKey != "commit ok" {
		t.Errorf("appendSessionKeyTip: expected no-op for empty key, got:\n%s", noKey)
	}
}

// --- 260702: ws.workflow_state integration tests -----------------------------

func TestWorkflowStateReturnsSessionStateOnly(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5500, root, nil))

	// Enter implement mode to populate agenda+todos.
	enter := callToolWithKey(t, server, 5501, key, "route.resolve_implement", implementSkipDocsArgs("text"))
	if !strings.Contains(enter, "Implementation Verdict") {
		t.Fatalf("route.resolve_implement unexpected: %s", enter)
	}

	manualResp := callToolWithKey(t, server, 5502, key, "workflow_manual", nil)
	stateResp := callToolWithKey(t, server, 5503, key, "workflow_state", nil)

	// The workflow_state output must exactly equal the "## Session State" section
	// workflow_manual renders for the same session at the same point in time.
	manualIdx := strings.Index(manualResp, "## Session State")
	if manualIdx < 0 {
		t.Fatalf("workflow_manual response missing Session State section:\n%s", manualResp)
	}
	wantState := manualResp[manualIdx:]
	if stateResp != wantState {
		t.Fatalf("workflow_state mismatch.\ngot:\n%s\nwant (workflow_manual's Session State suffix):\n%s", stateResp, wantState)
	}

	// No manual reference/primitives text: workflow_state must be substantially
	// shorter than the full workflow_manual render, and must not carry manual-only
	// fragments (e.g. the always-shown per-root rule or ferrule mention).
	if len(stateResp) >= len(manualResp) {
		t.Fatalf("workflow_state (%d bytes) not shorter than workflow_manual (%d bytes)", len(stateResp), len(manualResp))
	}
	if strings.Contains(stateResp, "ferrule") {
		t.Errorf("workflow_state leaked manual body content:\n%s", stateResp)
	}

	// Session State content itself is present (agenda + todos).
	if !strings.Contains(stateResp, "### agenda: implement") {
		t.Errorf("workflow_state missing agenda heading:\n%s", stateResp)
	}
	if !strings.Contains(stateResp, "### Todos") {
		t.Errorf("workflow_state missing Todos heading:\n%s", stateResp)
	}
}

// TestWorkflowStateByteIdenticalToWorkflowManualWhenNotesPresent is the
// notes-present sibling of TestWorkflowStateReturnsSessionStateOnly: once
// facet B (260823) makes wsnote.Compute always render a non-empty "# Notes"
// block, workflow_state's resolved-record branch must append the identical
// computeNotes(rec.Root) text workflow_manual's CONTINUE branch does, or the
// two responses silently diverge only when a note actually exists.
func TestWorkflowStateByteIdenticalToWorkflowManualWhenNotesPresent(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5505, root, nil))

	callToolWithKey(t, server, 5506, key, "note.write", map[string]any{
		"layer": "worktree",
		"notes": []any{map[string]any{"key": "state.parity", "value": "seen by both tools", "priority": 1}},
	})

	manualResp := callToolWithKey(t, server, 5507, key, "workflow_manual", nil)
	stateResp := callToolWithKey(t, server, 5508, key, "workflow_state", nil)

	manualIdx := strings.Index(manualResp, "## Session State")
	if manualIdx < 0 {
		t.Fatalf("workflow_manual response missing Session State section:\n%s", manualResp)
	}
	wantState := manualResp[manualIdx:]
	if stateResp != wantState {
		t.Fatalf("workflow_state mismatch with notes present.\ngot:\n%s\nwant (workflow_manual's Session State suffix):\n%s", stateResp, wantState)
	}
	if !strings.Contains(stateResp, "state.parity") || !strings.Contains(stateResp, "seen by both tools") {
		t.Fatalf("workflow_state missing the written note:\n%s", stateResp)
	}
}

func TestWorkflowStateEmptySessionReturnsEmptyPayloadNotError(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 5510, root, nil))

	// No enter.* call, no todos: session exists but has empty agenda/todos.
	resp := callToolWithKey(t, server, 5511, key, "workflow_state", nil)

	if !strings.Contains(resp, "## Session State") {
		t.Fatalf("workflow_state empty session missing Session State heading:\n%s", resp)
	}
	if !strings.Contains(resp, "(no todos)") {
		t.Errorf("workflow_state empty session missing empty-todos marker:\n%s", resp)
	}
	// Must not be the fail-loud no-restorable-state notice — this is a resolved,
	// merely-empty session, not an unknown key.
	if strings.Contains(resp, "no restorable state for session key") {
		t.Errorf("workflow_state empty session incorrectly returned fail-loud notice:\n%s", resp)
	}
}

func TestWorkflowStateKeylessRejected(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")

	resp := callToolNoKey(t, server, 5520, "workflow_state", nil)

	if !strings.Contains(resp, "session_key") {
		t.Errorf("keyless: response must mention session_key, got:\n%s", resp)
	}
	if strings.Contains(resp, "mint your lead key") {
		t.Errorf("keyless: self-bootstrap fragment must be absent from error response:\n%s", resp)
	}
}

func TestWorkflowStateDelegateKeyBlockedSameAsWorkflowManual(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")

	delegateKey, err := server.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatalf("mint delegate key: %v", err)
	}

	manualRawResp := callToolOnce(t, server, 5530, "workflow_manual", map[string]any{
		"session_key": delegateKey,
	})
	stateRawResp := callToolOnce(t, server, 5531, "workflow_state", map[string]any{
		"session_key": delegateKey,
	})

	// Same lead-only profile rejection shape (RPC-level -32601 error) as
	// workflow_manual for a non-lead scoped key.
	for _, rawResp := range []string{manualRawResp, stateRawResp} {
		if !strings.Contains(rawResp, "tool not available in current") {
			t.Errorf("delegate key: expected lead-only rejection, got:\n%s", rawResp)
		}
		if !strings.Contains(rawResp, "-32601") {
			t.Errorf("delegate key: expected JSON-RPC error code -32601, got:\n%s", rawResp)
		}
	}
	if strings.Contains(stateRawResp, "Session State") {
		t.Errorf("delegate key: workflow_state body must be absent from rejection response:\n%s", stateRawResp)
	}
}

func TestWorkflowStateUnknownKeySameFailLoudAsWorkflowManual(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("WS_CACHE_HOME", cacheDir)

	server := NewServer(root, "test")

	badKey := "no-such-key-here"
	manualResp := callToolWithKey(t, server, 5540, badKey, "workflow_manual", nil)
	stateResp := callToolWithKey(t, server, 5541, badKey, "workflow_state", nil)

	// workflow_state must produce the identical fail-loud notice text that
	// workflow_manual renders for the same unresolvable key (both end with the
	// same "## Session State ... no restorable state ..." notice).
	manualIdx := strings.Index(manualResp, "## Session State")
	if manualIdx < 0 {
		t.Fatalf("workflow_manual fail-loud response missing Session State heading:\n%s", manualResp)
	}
	wantNotice := manualResp[manualIdx:]
	if stateResp != wantNotice {
		t.Fatalf("workflow_state fail-loud mismatch.\ngot:\n%s\nwant (workflow_manual's fail-loud notice):\n%s", stateResp, wantNotice)
	}
	if !strings.Contains(stateResp, "lead-revive") {
		t.Errorf("unknown key: workflow_state fail-loud notice should point to lead-revive recovery:\n%s", stateResp)
	}
	if strings.Contains(stateResp, "ferrule") || strings.Contains(stateResp, "one Git worktree") {
		t.Errorf("unknown key: workflow_state must not leak manual body / ferrule mention:\n%s", stateResp)
	}

	// Must NOT have minted a key record.
	_, statErr := os.Stat(filepath.Join(cacheDir, sessionsDirName, badKey))
	if !os.IsNotExist(statErr) {
		t.Errorf("unknown key: key record was minted (stat: %v)", statErr)
	}
}

func TestWorkflowStateFreshSentinelIsFailLoudNotFresh(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")

	// The fresh-bootstrap sentinel is never a stored record; workflow_state has
	// no FRESH mode, so it must fall through to the same fail-loud path as any
	// other unresolvable key rather than minting or rendering manual content.
	resp := callToolWithKey(t, server, 5550, freshBootstrapKey, "workflow_state", nil)

	if !strings.Contains(resp, "no restorable state for session key") {
		t.Errorf("sentinel key: expected fail-loud notice, got:\n%s", resp)
	}
	if strings.Contains(resp, "mint your lead key") {
		t.Errorf("sentinel key: workflow_state must not render fresh-mode manual content:\n%s", resp)
	}
}

func TestWorkflowStateToolSchema(t *testing.T) {
	useLeadProfile(t)
	server := NewServer(t.TempDir(), "test")
	properties := toolPropertiesByName(t, callToolsList(t, server), "workflow_state")
	if _, ok := properties["session_key"]; !ok {
		t.Fatalf("workflow_state schema missing session_key property: %#v", properties)
	}
}

func TestHostedRestoreBundleSessionStateTargetsAndChildren(t *testing.T) {
	ticketTarget := json.RawMessage(`{"target":{"kind":"ticket","ticket_stem":"260101-feat-example","ticket_path":"ai-docs/tickets/ready/260101-feat-example.md","scope_label":"Phase 2"}}`)
	inlineTarget := json.RawMessage(`{"target":{"kind":"inline","label":"Inline restore plan"}}`)
	noTarget := json.RawMessage(`{"target":{"kind":"ticket","ticket_path":"must-not-be-inferred.md"}}`)
	todos := []todoItem{{Key: "restore", Title: "Restore state", Status: todoPending}}
	children := []wsagent.AgentChild{
		{Address: "id:child-a", Label: "alpha", Tier: "pi.small", Authority: "read-only", State: "running", LastReport: "done: alpha"},
		{Address: "id:child-b", Label: "beta", Tier: "codex.large", Authority: "workdir-write", State: "waiting", Error: "fixture stopped"},
	}
	for _, tc := range []struct {
		name       string
		agenda     map[string]json.RawMessage
		children   []wsagent.AgentChild
		wantTarget string
		wantChild  []string
	}{
		{
			name: "typed ticket and multiple direct children",
			agenda: map[string]json.RawMessage{
				"implement": ticketTarget,
				"scratch":   json.RawMessage(`{"note":"state rendered through the existing renderer"}`),
			},
			children: children, wantTarget: "ticket: ai-docs/tickets/ready/260101-feat-example.md (Phase 2)",
			wantChild: []string{"id:child-a (alpha): running; tier=pi.small; authority=read-only; last=done: alpha", "id:child-b (beta): waiting; tier=codex.large; authority=workdir-write; error=fixture stopped"},
		},
		{
			name:       "typed inline plan and zero children",
			agenda:     map[string]json.RawMessage{"implement": inlineTarget},
			wantTarget: "plan: Inline restore plan", wantChild: []string{"## Direct Children\nnone"},
		},
		{
			name:       "absent typed target does not infer arbitrary agenda",
			agenda:     map[string]json.RawMessage{"scratch": noTarget},
			wantTarget: "none recorded", wantChild: []string{"## Direct Children\nnone"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderHostedRestoreBundle("session-key", "compaction", sessionRecord{Agenda: tc.agenda, Todos: todos}, tc.children, false)
			for _, want := range []string{
				`<ws-restore cause="compaction">`,
				"## Hosted Session Restore",
				"Your ws session key is `session-key`", "Context was compacted",
				"<session-state>\n## Session State",
				"### Todos\n- [ ] {restore} Restore state",
				"</session-state>",
				"<work-target>\n## Work Target\n" + tc.wantTarget + "\n</work-target>",
				"<direct-children>\n## Direct Children",
				"</direct-children>\n</ws-restore>",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("restore bundle lacks %q:\n%s", want, got)
				}
			}
			for _, want := range tc.wantChild {
				if !strings.Contains(got, want) {
					t.Fatalf("restore bundle lacks child projection %q:\n%s", want, got)
				}
			}
			if tc.wantTarget == "none recorded" && strings.Contains(got, "must-not-be-inferred.md") && strings.Contains(got, "## Work Target\nticket:") {
				t.Fatalf("work target was inferred from arbitrary agenda:\n%s", got)
			}
		})
	}
}

func TestHostedLeadManualAndTurnsReadSessionStateAtDelivery(t *testing.T) {
	f := newWhiteboardFixture(t)
	f.update(t, `<wb:topic name="startup"><wb:log>Current whiteboard summary</wb:log></wb:topic>`)
	if response := f.call(t, "todo.add", map[string]any{"key": "startup", "title": "Current todo"}); isToolError(response) {
		t.Fatal(response)
	}
	store := NewAgentKeyStore(f.s.sessions.boards)
	setAgenda := func(version string) {
		t.Helper()
		if err := store.store.setAgenda(f.key, "freshness", json.RawMessage(fmt.Sprintf(`{"version":%q}`, version))); err != nil {
			t.Fatal(err)
		}
	}
	setAgenda("at-launch")
	render := HostedRender{Harness: "claude", Boards: f.s.sessions.boards}
	manual, err := RenderLeadManual(os.Environ(), f.root, render, f.key)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**Session invariant:**", "> **Session key: `" + f.key + "`**", "## Session Key\n" + f.key} {
		if !strings.Contains(manual, want) {
			t.Fatalf("hosted manual lacks %q:\n%s", want, manual)
		}
	}
	stateParts := []string{"## Session State", "### agenda: freshness", "### Whiteboard topics\n- startup: Current whiteboard summary", "### Todos\n- [ ] {startup} Current todo"}
	for _, unwanted := range append(stateParts, "at-launch") {
		if strings.Contains(manual, unwanted) {
			t.Fatalf("hosted manual contains %q:\n%s", unwanted, manual)
		}
	}
	assertState := func(text, version string) {
		t.Helper()
		for _, want := range append(stateParts, fmt.Sprintf(`"version": %q`, version)) {
			if strings.Count(text, want) != 1 {
				t.Fatalf("expected one %q in session state:\n%s", want, text)
			}
		}
	}
	assertState(toolText(t, f.call(t, "workflow_manual", map[string]any{})), "at-launch")

	setAgenda("first-turn")
	first, err := store.RenderHostedSessionState(f.key)
	if err != nil {
		t.Fatal(err)
	}
	assertState(first, "first-turn")
	for _, cause := range []string{"compaction", "restart"} {
		setAgenda(cause)
		bundle, err := RenderHostedRestoreBundle(os.Environ(), f.root, render, f.key, cause, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertState(bundle, cause)
	}
}

func TestWorkflowStateHostedOmitsRedundantNotes(t *testing.T) {
	setupWorkflowManualNoteEnv(t)
	root := t.TempDir()
	initGit(t, root)

	// Mint a lead key and write a note via a non-hosted server first.
	cliServer := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, cliServer, 1, root, nil))
	callToolWithKey(t, cliServer, 2, key, "note.write", map[string]any{
		"layer": "worktree",
		"notes": []any{map[string]any{"key": "hosted.dedup", "value": "already in hosted system prompt", "priority": 1}},
	})

	// Non-hosted CLI caller still receives the # Notes block.
	cliState := callToolWithKey(t, cliServer, 3, key, "workflow_state", nil)
	if notesBlockIndex(cliState) < 0 || !strings.Contains(cliState, "hosted.dedup") {
		t.Fatalf("non-hosted workflow_state must preserve # Notes:\n%s", cliState)
	}

	// Hosted caller omits # Notes and points to note.query instead.
	hostedSessionEnv(t, key)
	hostedServer := NewServer(root, "test")
	hostedState := callToolWithKey(t, hostedServer, 4, key, "workflow_state", nil)
	if !strings.Contains(hostedState, "## Session State") || !strings.Contains(hostedState, "### Todos") {
		t.Fatalf("hosted workflow_state missing Session State:\n%s", hostedState)
	}
	if notesBlockIndex(hostedState) >= 0 || strings.Contains(hostedState, "hosted.dedup") {
		t.Fatalf("hosted workflow_state must omit redundant # Notes:\n%s", hostedState)
	}
	if !strings.Contains(hostedState, "note.query") {
		t.Fatalf("hosted workflow_state missing compact note.query pointer:\n%s", hostedState)
	}
}

func TestRenderHostedRestoreBundleWhiteboardQuickCard(t *testing.T) {
	if n := len(whiteboardXMLQuickCard); n < 400 || n > 700 {
		t.Fatalf("whiteboardXMLQuickCard byte length = %d, want 400..700", n)
	}

	f := newWhiteboardFixture(t)
	f.update(t, `<wb:topic name="card-test"><wb:log>Topic summary</wb:log></wb:topic>`)
	render := HostedRender{Harness: "claude", Boards: f.s.sessions.boards}

	// 1. Top-level lead with whiteboard_enabled on: quick-card appears right after ### Whiteboard topics.
	bundleOn, err := RenderHostedRestoreBundle(os.Environ(), f.root, render, f.key, "compaction", nil)
	if err != nil {
		t.Fatalf("RenderHostedRestoreBundle (on): %v", err)
	}
	topicsIdx := strings.Index(bundleOn, "### Whiteboard topics")
	cardIdx := strings.Index(bundleOn, "### Whiteboard XML Quick-Card")
	todosIdx := strings.Index(bundleOn, "### Todos")
	if topicsIdx < 0 || cardIdx < 0 || todosIdx < 0 || !(topicsIdx < cardIdx && cardIdx < todosIdx) {
		t.Fatalf("quick-card not positioned between Whiteboard topics and Todos:\n%s", bundleOn)
	}
	for _, want := range []string{
		`<wb:topic name="<stem>"`,
		`NOT ` + "`id=`",
		`<wb:panel name="..." recap="...">`,
		`<wb:change><wb:old>...</wb:old><wb:new>...</wb:new></wb:change>`,
		`<wb:log>`,
		`<wb:add class="policy|technical|announced|check">`,
		`<wb:answer id="N" value="...">`,
		`<wb:state value="discussing|researching|waiting|implementing|awaiting-confirmation"`,
		`<wb:eta minutes="<total>" reason="..."/>`,
		`<wb:close/>`,
	} {
		if !strings.Contains(bundleOn, want) {
			t.Fatalf("quick-card missing %q:\n%s", want, bundleOn)
		}
	}

	// 2. Non-top-level (worker / child key with parent) must NOT emit the quick-card.
	childKey, err := f.s.sessions.mint(f.root, roleLead, f.key)
	if err != nil {
		t.Fatalf("mint child key: %v", err)
	}
	bundleChild, err := RenderHostedRestoreBundle(os.Environ(), f.root, render, childKey, "compaction", nil)
	if err != nil {
		t.Fatalf("RenderHostedRestoreBundle (child): %v", err)
	}
	if strings.Contains(bundleChild, "### Whiteboard XML Quick-Card") {
		t.Fatalf("child restore bundle must not emit Whiteboard XML Quick-Card:\n%s", bundleChild)
	}

	// 3. Top-level lead with whiteboard_enabled off must NOT emit the quick-card.
	setupWorkflowManualNoteEnv(t)
	rootOff := t.TempDir()
	initGit(t, rootOff)
	sOff := NewServer(rootOff, "test")
	keyOff, err := sOff.sessions.mint(rootOff, roleLead, "")
	if err != nil {
		t.Fatalf("mint keyOff: %v", err)
	}
	if resp := callToolWithKey(t, sOff, 1, keyOff, "config.tune", map[string]any{
		"key":   "workflow.whiteboard_enabled",
		"value": "off",
		"scope": "session",
	}); strings.Contains(resp, "error") {
		t.Fatalf("disable whiteboard_enabled: %s", resp)
	}
	bundleOff, err := RenderHostedRestoreBundle(os.Environ(), rootOff, HostedRender{Harness: "claude"}, keyOff, "compaction", nil)
	if err != nil {
		t.Fatalf("RenderHostedRestoreBundle (off): %v", err)
	}
	if strings.Contains(bundleOff, "### Whiteboard XML Quick-Card") {
		t.Fatalf("whiteboard-off restore bundle must not emit Whiteboard XML Quick-Card:\n%s", bundleOff)
	}
}

func TestHostedRestoreBundleEscapesFramingCloseTagsAndPreservesWhiteboard(t *testing.T) {
	rec := sessionRecord{
		Agenda: map[string]json.RawMessage{
			"implement": json.RawMessage(`{"target":{"kind":"inline","label":"plan </work-target></ws-restore>"}}`),
			"poison":    json.RawMessage(`{"note":"close </session-state> and </ws-restore> and </dialog>"}`),
		},
		Todos: []todoItem{{Key: "t1", Title: "check </session-state>", Status: todoPending}},
	}
	children := []wsagent.AgentChild{
		{Address: "id:c1", Label: "w </direct-children></ws-restore>", Tier: "medium", Authority: "read-only", State: "running"},
	}
	got := renderHostedRestoreBundle("key-1", "compaction", rec, children, true)
	for tag, want := range map[string]int{
		"</ws-restore>":      1,
		"</session-state>":   1,
		"</work-target>":     1,
		"</direct-children>": 1,
		"</dialog>":          0,
	} {
		if c := strings.Count(got, tag); c != want {
			t.Fatalf("count(%q) = %d, want %d:\n%s", tag, c, want, got)
		}
	}
	if !strings.Contains(got, `<wb:change><wb:old>...</wb:old><wb:new>...</wb:new></wb:change>`) {
		t.Fatalf("whiteboard XML quick-card tags must stay verbatim:\n%s", got)
	}
}

func TestHostedRestoreBundleRecentMailSectionAndBudget(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	f := newHistoryFixture(t, t0)
	replyB := wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(f.keyB)

	// Empty recv history omits <recent-mail>.
	render := HostedRender{Harness: "claude"}
	emptyBundle, err := RenderHostedRestoreBundle(os.Environ(), f.root, render, f.keyA, "compaction", nil)
	if err != nil {
		t.Fatalf("RenderHostedRestoreBundle (empty): %v", err)
	}
	if strings.Contains(emptyBundle, "<recent-mail>") {
		t.Fatalf("empty recv history unexpectedly rendered <recent-mail>:\n%s", emptyBundle)
	}

	// Send 7 messages to A (at staggered timestamps), drain all 7 into A's recv history,
	// plus 1 sent message from A (which must not appear in <recent-mail>).
	for i := 0; i < 7; i++ {
		setMailboxClock(t, t0.Add(time.Duration(i+1)*time.Minute))
		body := fmt.Sprintf("msg %d short body", i)
		if i == 6 {
			body = "newest poisoning </recent-mail></ws-restore></dialog> " + strings.Repeat("A", 12000)
		}
		mustSend(t, f.serverB, 10+i, f.keyB, "alice@worktree", body)
	}
	setMailboxClock(t, t0.Add(10*time.Minute))
	callToolWithKey(t, f.serverA, 30, f.keyA, "mailbox.recv", nil)
	setMailboxClock(t, t0.Add(11*time.Minute))
	mustSend(t, f.serverA, 31, f.keyA, replyB, "outgoing only message")

	bundle, err := RenderHostedRestoreBundle(os.Environ(), f.root, render, f.keyA, "restart", nil)
	if err != nil {
		t.Fatalf("RenderHostedRestoreBundle (with mail): %v", err)
	}
	_, afterOpen, ok := strings.Cut(bundle, "<recent-mail>\n")
	if !ok {
		t.Fatalf("restore bundle missing <recent-mail>:\n%s", bundle)
	}
	section, _, ok := strings.Cut(afterOpen, "\n</recent-mail>")
	if !ok {
		t.Fatalf("restore bundle missing </recent-mail>:\n%s", bundle)
	}
	if len(section) > restoreRecentMailByteBudget {
		t.Fatalf("<recent-mail> section exceeded budget: %d > %d", len(section), restoreRecentMailByteBudget)
	}
	// Closing tags from mail payload must be escaped cleanly.
	if c := strings.Count(bundle, "</ws-restore>"); c != 1 {
		t.Fatalf("count(</ws-restore>) = %d, want 1:\n%s", c, bundle)
	}
	if c := strings.Count(bundle, "</recent-mail>"); c != 1 {
		t.Fatalf("count(</recent-mail>) = %d, want 1:\n%s", c, bundle)
	}
	if strings.Contains(bundle, "</dialog>") {
		t.Fatalf("unescaped </dialog> leaked into restore bundle:\n%s", bundle)
	}
	// Only newest 5 recv entries (msg 2..6), newest first, no sent entry, with mailbox.read recovery hint.
	for _, forbidden := range []string{"outgoing only message", "msg 0 short body", "msg 1 short body"} {
		if strings.Contains(section, forbidden) {
			t.Fatalf("<recent-mail> included %q:\n%s", forbidden, section)
		}
	}
	for _, want := range []string{"msg 2 short body", "msg 5 short body", "mailbox.read(id:", "Older received mail (7 total)"} {
		if !strings.Contains(section, want) {
			t.Fatalf("<recent-mail> missing %q:\n%s", want, section)
		}
	}
	// Verify ordering: msg 6 appears before msg 5 which appears before msg 2.
	pos6 := strings.Index(section, "newest poisoning")
	pos5 := strings.Index(section, "msg 5 short body")
	pos2 := strings.Index(section, "msg 2 short body")
	if !(pos6 >= 0 && pos6 < pos5 && pos5 < pos2) {
		t.Fatalf("recent mail order not newest-first: pos6=%d pos5=%d pos2=%d\n%s", pos6, pos5, pos2, section)
	}
}
