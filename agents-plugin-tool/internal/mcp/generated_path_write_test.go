package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/mcpframe"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestPathWriteRequiresSameSessionGeneratedCachePath(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	parent, err := s.sessions.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.sessions.mint(root, roleDelegate, parent)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.sessions.mint(root, roleDelegate, parent)
	if err != nil {
		t.Fatal(err)
	}

	generate := func(id int, key, kind string) string {
		t.Helper()
		resp := callToolOnce(t, s, id, "path.generate", map[string]any{
			"session_key": key, "kind": kind, "stems": []any{"findings"},
		})
		if toolIsError(t, resp) {
			t.Fatalf("path.generate(%s): %s", kind, resp)
		}
		return strings.TrimSpace(toolText(t, resp))
	}
	write := func(id int, key, path, content string) string {
		t.Helper()
		return callToolOnce(t, s, id, "path.write", map[string]any{
			"session_key": key, "path": path, "content": content,
		})
	}

	childPath := generate(1, child, "review")
	if resp := write(2, child, childPath, "complete findings\n"); toolIsError(t, resp) {
		t.Fatalf("same-session path.write: %s", resp)
	}
	if got, err := os.ReadFile(childPath); err != nil || string(got) != "complete findings\n" {
		t.Fatalf("written findings = %q, %v", got, err)
	}

	// Proxy reconnects construct a fresh Server in the same daemon process.
	// The same session key must retain its minted cache-path capability.
	reconnectPath := generate(21, child, "review")
	reconnected := NewServer(root, "test")
	if resp := callToolOnce(t, reconnected, 22, "path.write", map[string]any{
		"session_key": child, "path": reconnectPath, "content": "after reconnect\n",
	}); toolIsError(t, resp) {
		t.Fatalf("same-session path.write after reconnect: %s", resp)
	}
	if got, err := os.ReadFile(reconnectPath); err != nil || string(got) != "after reconnect\n" {
		t.Fatalf("reconnected findings = %q, %v", got, err)
	}

	if resp := callToolOnce(t, s, 23, "path.write", map[string]any{
		"session_key": child, "path": reconnectPath, "content": 7,
	}); !toolIsError(t, resp) || !strings.Contains(resp, "path and content must be strings") {
		t.Fatalf("non-string content refusal = %s", resp)
	}
	if got, err := os.ReadFile(reconnectPath); err != nil || string(got) != "after reconnect\n" {
		t.Fatalf("non-string content changed findings = %q, %v", got, err)
	}

	for name, tc := range map[string]struct {
		key  string
		path string
	}{
		"unminted":        {child, filepath.Join(filepath.Dir(childPath), "unminted.md")},
		"cross-session":   {other, childPath},
		"parent-minted":   {child, generate(3, parent, "review")},
		"repository-path": {child, filepath.Join(root, "README.md")},
		"traversal-alias": {child, filepath.Dir(childPath) + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(filepath.Dir(childPath)) + string(os.PathSeparator) + filepath.Base(childPath)},
	} {
		t.Run(name, func(t *testing.T) {
			before, _ := os.ReadFile(tc.path)
			resp := write(10, tc.key, tc.path, "must not land")
			if !toolIsError(t, resp) || !strings.Contains(resp, "not generated for this session key") {
				t.Fatalf("refusal = %s", resp)
			}
			after, _ := os.ReadFile(tc.path)
			if string(after) != string(before) {
				t.Fatalf("refused write changed %s", tc.path)
			}
		})
	}

	planPath := generate(20, child, "plan")
	if resp := write(21, child, planPath, "source tree write"); !toolIsError(t, resp) {
		t.Fatalf("repo-local generated plan was writable: %s", resp)
	}
}

func TestPathWriteGrantsAreBoundedAndConsumed(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}

	paths := make([]string, 0, maxGeneratedPathGrants+1)
	for i := 0; i <= maxGeneratedPathGrants; i++ {
		resp := callToolOnce(t, s, i+1, "path.generate", map[string]any{
			"session_key": key, "kind": "review", "stems": []any{"bounded"},
		})
		if toolIsError(t, resp) {
			t.Fatalf("path.generate(%d): %s", i, resp)
		}
		paths = append(paths, strings.TrimSpace(toolText(t, resp)))
	}
	processGeneratedWrites.mu.RLock()
	count := processGeneratedWrites.grantCountLocked()
	processGeneratedWrites.mu.RUnlock()
	if count > maxGeneratedPathGrants {
		t.Fatalf("open generated-path grants = %d, want <= %d", count, maxGeneratedPathGrants)
	}
	if resp := callToolOnce(t, s, 100, "path.write", map[string]any{
		"session_key": key, "path": paths[0], "content": "evicted",
	}); !toolIsError(t, resp) {
		t.Fatalf("oldest grant remained writable after capacity eviction: %s", resp)
	}
	last := paths[len(paths)-1]
	if resp := callToolOnce(t, s, 101, "path.write", map[string]any{
		"session_key": key, "path": last, "content": "complete",
	}); toolIsError(t, resp) {
		t.Fatalf("newest grant write: %s", resp)
	}
	if resp := callToolOnce(t, s, 102, "path.write", map[string]any{
		"session_key": key, "path": last, "content": "second",
	}); !toolIsError(t, resp) {
		t.Fatalf("completed grant was not consumed: %s", resp)
	}
}

// A legacy path.generate batch larger than the grant budget must still
// return every path (v0.50.8 behavior); the budget bounds only which of them
// path.write accepts.
func TestPathGenerateOversizedBatchReturnsAllPathsAndBoundsGrants(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}

	stems := make([]any, 0, maxGeneratedPathGrants+1)
	for i := 0; i <= maxGeneratedPathGrants; i++ {
		stems = append(stems, fmt.Sprintf("batch-%02d", i))
	}
	resp := callToolOnce(t, s, 1, "path.generate", map[string]any{
		"session_key": key, "kind": "prompt", "stems": stems,
	})
	if toolIsError(t, resp) {
		t.Fatalf("oversized legacy path.generate batch refused: %s", resp)
	}
	paths := strings.Split(strings.TrimRight(toolText(t, resp), "\n"), "\n")
	if len(paths) != len(stems) {
		t.Fatalf("path.generate returned %d paths, want %d", len(paths), len(stems))
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("generated path %q not allocated: %v", path, err)
		}
	}
	processGeneratedWrites.mu.RLock()
	count := processGeneratedWrites.grantCountLocked()
	processGeneratedWrites.mu.RUnlock()
	if count > maxGeneratedPathGrants {
		t.Fatalf("open generated-path grants = %d, want <= %d", count, maxGeneratedPathGrants)
	}
	if resp := callToolOnce(t, s, 2, "path.write", map[string]any{
		"session_key": key, "path": paths[0], "content": "over budget",
	}); !toolIsError(t, resp) {
		t.Fatalf("batch entry beyond the grant budget was writable: %s", resp)
	}
	if resp := callToolOnce(t, s, 3, "path.write", map[string]any{
		"session_key": key, "path": paths[len(paths)-1], "content": "within budget",
	}); toolIsError(t, resp) {
		t.Fatalf("newest batch entry write: %s", resp)
	}
}

// A legacy caller may save a generated artifact by replace (write a sibling,
// rename over the path, or delete and recreate) while the daemon holds the
// path.write grant. On Windows this requires the held handle to share delete
// access; the replaced file must then fail path.write's identity check.
func TestPathGenerateGrantAllowsReplaceSaveAndRefusesReplacedTarget(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	generate := func(id int, stem string) string {
		t.Helper()
		resp := callToolOnce(t, s, id, "path.generate", map[string]any{
			"session_key": key, "kind": "review", "stems": []any{stem},
		})
		if toolIsError(t, resp) {
			t.Fatalf("path.generate: %s", resp)
		}
		return strings.TrimSpace(toolText(t, resp))
	}

	// Windows refuses a rename-replace over a file that any handle has open
	// (ERROR_ACCESS_DENIED), FILE_SHARE_DELETE or not, so the grant's held
	// handle cannot allow it there; the delete-and-recreate save below covers
	// the replaced-target refusal on Windows.
	renamed := generate(1, "rename-save")
	if runtime.GOOS != "windows" {
		sibling := renamed + ".tmp"
		if err := os.WriteFile(sibling, []byte("editor save"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(sibling, renamed); err != nil {
			t.Fatalf("rename-replace over a granted artifact: %v", err)
		}
	}
	removed := generate(2, "delete-save")
	if err := os.Remove(removed); err != nil {
		t.Fatalf("delete of a granted artifact: %v", err)
	}
	// On Windows a delete under a held FILE_SHARE_DELETE handle can leave
	// the name delete-pending; a save that recreates the same name must
	// still succeed.
	if err := os.WriteFile(removed, []byte("recreated"), 0o644); err != nil {
		t.Fatalf("recreate after deleting a granted artifact: %v", err)
	}
	if resp := callToolOnce(t, s, 4, "path.write", map[string]any{
		"session_key": key, "path": removed, "content": "must not land",
	}); !toolIsError(t, resp) {
		t.Fatalf("path.write wrote a recreated artifact: %s", resp)
	}
	if got, err := os.ReadFile(removed); err != nil || string(got) != "recreated" {
		t.Fatalf("recreated artifact = %q, %v", got, err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	resp := callToolOnce(t, s, 3, "path.write", map[string]any{
		"session_key": key, "path": renamed, "content": "must not land",
	})
	if !toolIsError(t, resp) {
		t.Fatalf("path.write wrote a replaced artifact: %s", resp)
	}
	if got, err := os.ReadFile(renamed); err != nil || string(got) != "editor save" {
		t.Fatalf("replaced artifact = %q, %v", got, err)
	}
}

// A grant needs the reservation's creation identity: a GeneratedPath with
// none is refused (fail closed), and the refusal deletes nothing.
func TestPathGenerateGrantRefusesUnknownIdentityAndKeepsFiles(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	generated, err := wsstate.NewManager(wsstate.Options{}).GeneratePaths(root, "review", []string{"known", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if generated[0].Created == nil || generated[1].Created == nil {
		t.Fatal("GeneratePaths did not record creation identities")
	}
	if err := os.WriteFile(generated[0].Path, []byte("saved by another process"), 0o644); err != nil {
		t.Fatal(err)
	}
	generated[1].Created = nil
	if err := processGeneratedWrites.grant("unknown-identity-key", generated); err == nil || !strings.Contains(err.Error(), "no creation identity") {
		t.Fatalf("grant with an unknown creation identity = %v", err)
	}
	if got, err := os.ReadFile(generated[0].Path); err != nil || string(got) != "saved by another process" {
		t.Fatalf("saved reservation after refusal = %q, %v", got, err)
	}
	if _, err := os.Lstat(generated[1].Path); err != nil {
		t.Fatalf("refused reservation was deleted: %v", err)
	}
}

// At capacity, a refused new grant must not evict the existing grants: the
// new grants are validated before any eviction.
func TestPathGenerateRefusedGrantDoesNotEvictExistingGrants(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	stems := make([]any, 0, maxGeneratedPathGrants)
	for i := 0; i < maxGeneratedPathGrants; i++ {
		stems = append(stems, fmt.Sprintf("full-%02d", i))
	}
	resp := callToolOnce(t, s, 1, "path.generate", map[string]any{
		"session_key": key, "kind": "review", "stems": stems,
	})
	if toolIsError(t, resp) {
		t.Fatalf("path.generate: %s", resp)
	}
	paths := strings.Split(strings.TrimRight(toolText(t, resp), "\n"), "\n")

	notRegular := filepath.Join(t.TempDir(), "dir-artifact")
	if err := os.Mkdir(notRegular, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := processGeneratedWrites.grant(key, []wsstate.GeneratedPath{{Kind: "review", Path: notRegular}}); err == nil {
		t.Fatal("grant of a non-regular artifact was accepted")
	}
	processGeneratedWrites.mu.RLock()
	count := processGeneratedWrites.grantCountLocked()
	processGeneratedWrites.mu.RUnlock()
	if count != maxGeneratedPathGrants {
		t.Fatalf("grants after a refused grant = %d, want %d (none evicted)", count, maxGeneratedPathGrants)
	}
	if resp := callToolOnce(t, s, 2, "path.write", map[string]any{
		"session_key": key, "path": paths[0], "content": "still granted",
	}); toolIsError(t, resp) {
		t.Fatalf("a refused grant evicted the oldest existing grant: %s", resp)
	}
}

func TestPathWriteRefusesSymlinkReplacementAndAcceptsOversizedContent(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	generate := func(id int) string {
		resp := callToolOnce(t, s, id, "path.generate", map[string]any{
			"session_key": key, "kind": "review", "stems": []any{"oversized"},
		})
		if toolIsError(t, resp) {
			t.Fatal(resp)
		}
		return strings.TrimSpace(toolText(t, resp))
	}

	path := generate(1)
	large := strings.Repeat("finding\n", (17<<20)/len("finding\n"))
	if resp := callToolOnce(t, s, 2, "path.write", map[string]any{"session_key": key, "path": path, "content": large}); toolIsError(t, resp) {
		t.Fatalf("oversized path.write: %s", resp)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != large {
		t.Fatalf("oversized artifact bytes = %d, err = %v", len(got), err)
	}

	symlinkPath := generate(3)
	victim := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(victim, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(symlinkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resp := callToolOnce(t, s, 4, "path.write", map[string]any{"session_key": key, "path": symlinkPath, "content": "escaped"})
	if !toolIsError(t, resp) || !strings.Contains(resp, "not a regular file") {
		t.Fatalf("symlink refusal = %s", resp)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "safe" {
		t.Fatalf("symlink victim = %q, %v", got, err)
	}

	// Windows refuses to rename a directory with open handles beneath it
	// (the held grants), so the parent-swap attack cannot be staged there.
	if runtime.GOOS == "windows" {
		return
	}
	parentSwapPath := generate(5)
	originalParent := filepath.Dir(parentSwapPath)
	movedParent := originalParent + "-moved"
	victimDir := t.TempDir()
	victimPath := filepath.Join(victimDir, filepath.Base(parentSwapPath))
	if err := os.WriteFile(victimPath, []byte("victim-safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalParent, movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victimDir, originalParent); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	resp = callToolOnce(t, s, 6, "path.write", map[string]any{"session_key": key, "path": parentSwapPath, "content": "escaped"})
	if !toolIsError(t, resp) || !strings.Contains(resp, "not a regular file") {
		t.Fatalf("parent symlink refusal = %s", resp)
	}
	if got, err := os.ReadFile(victimPath); err != nil || string(got) != "victim-safe" {
		t.Fatalf("parent symlink victim = %q, %v", got, err)
	}
}

func TestServeStdioRejectsPathWriteBeyondRequestLimit(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	generated := callToolOnce(t, s, 1, "path.generate", map[string]any{
		"session_key": key, "kind": "review", "stems": []any{"limit"},
	})
	path := strings.TrimSpace(toolText(t, generated))
	oversize := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"path.write","arguments":{"session_key":` +
		fmt.Sprintf("%q", key) + `,"path":` + fmt.Sprintf("%q", path) + `,"content":"` + strings.Repeat("x", mcpframe.MaxRequestBytes) + `"}}}`
	followUp := `{"jsonrpc":"2.0","id":3,"method":"ping"}`
	var out bytes.Buffer
	err = s.ServeStdio(context.Background(), strings.NewReader(oversize+"\n"+followUp+"\n"), &out)
	if err != nil {
		t.Fatalf("ServeStdio oversized request error = %v, output = %.200q", err, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("ServeStdio produced %d responses, want 2: %q", len(lines), out.String())
	}
	var errResp struct {
		ID    json.RawMessage `json:"id"`
		Error *rpcError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &errResp); err != nil {
		t.Fatalf("decode oversize response: %v (%q)", err, lines[0])
	}
	if string(errResp.ID) != "2" || errResp.Error == nil || errResp.Error.Code != -32600 || errResp.Error.Message != mcpframe.TooLargeMessage(mcpframe.MaxRequestBytes) {
		t.Fatalf("oversize response = %s, want id=2 -32600 %q", lines[0], mcpframe.TooLargeMessage(mcpframe.MaxRequestBytes))
	}
	var pingResp struct {
		ID     json.RawMessage `json:"id"`
		Result map[string]any  `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &pingResp); err != nil {
		t.Fatalf("decode follow-up ping response: %v (%q)", err, lines[1])
	}
	if string(pingResp.ID) != "3" || pingResp.Error != nil || pingResp.Result == nil {
		t.Fatalf("follow-up response = %s, want id=3 ok result", lines[1])
	}
	if got, readErr := os.ReadFile(path); readErr != nil || len(got) != 0 {
		t.Fatalf("rejected oversized request changed artifact: bytes=%d err=%v", len(got), readErr)
	}
}
