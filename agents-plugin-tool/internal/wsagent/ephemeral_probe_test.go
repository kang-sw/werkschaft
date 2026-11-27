package wsagent

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEphemeralPoolRealCLIProbe is an env-gated real-CLI isolation probe test.
// It is skipped by default; enable with WS_EPHEMERAL_PROBE=1 (and optionally
// WS_EPHEMERAL_PROBE_HARNESS=claude,codex,agy,werk) on a machine with the
// target CLIs installed and authenticated.
//
// After N pool calls plus a Reset and pool.Close(), it asserts:
//   - claude: zero new .jsonl files under ~/.claude/projects (CLAUDE_CONFIG_DIR)
//   - codex: zero new rollout .jsonl files under ~/.codex/sessions (CODEX_HOME)
//   - agy: zero new files under ~/.gemini (artifacts stay strictly inside the
//     pool's isolated --gemini-dir and are removed on pool.Close()), and an
//     attempted shell command prompt does not stall on ACP permission
//   - werk: zero disk files created when Config.Ephemeral is true
func TestEphemeralPoolRealCLIProbe(t *testing.T) {
	if os.Getenv("WS_EPHEMERAL_PROBE") != "1" {
		t.Skip("set WS_EPHEMERAL_PROBE=1 to run real-CLI ephemeral isolation probes")
	}
	filter := strings.TrimSpace(os.Getenv("WS_EPHEMERAL_PROBE_HARNESS"))
	wantHarness := func(name string) bool {
		if filter == "" {
			return true
		}
		for _, part := range strings.Split(filter, ",") {
			if strings.TrimSpace(part) == name {
				return true
			}
		}
		return false
	}

	schema := json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)
	jsonPrompt := `Reply with ONLY this exact JSON object and nothing else: {"ok": true}`
	toolListSchema := json.RawMessage(`{"type":"object","required":["tools"],"properties":{"tools":{"type":"array"}}}`)
	toolListPrompt := `List the exact names of all tools and MCP tools currently available to you as JSON {"tools": ["..."]}. If none are available, return {"tools": []}. Output JSON only.`

	if wantHarness(BackendClaude) {
		t.Run("claude", func(t *testing.T) {
			if _, _, err := harnessCommand("", "claude"); err != nil {
				t.Skipf("claude CLI unavailable: %v", err)
			}
			claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
			if claudeDir == "" {
				home, _ := os.UserHomeDir()
				claudeDir = filepath.Join(home, ".claude")
			}
			projectsDir := filepath.Join(claudeDir, "projects")
			before := snapshotFilesBySuffix(projectsDir, ".jsonl")

			pool := NewEphemeralPool(EphemeralPoolOptions{
				ScratchRoot:   t.TempDir(),
				SweepInterval: -1,
			})
			defer pool.Close()

			spec := AcquireSpec{
				Harness:      BackendClaude,
				Model:        "haiku",
				Effort:       "low",
				Mode:         PureOneShot,
				SystemPrompt: "Respond with strict JSON only. Never call tools.",
			}
			for i := 0; i < 2; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				reply, err := pool.Execute(ctx, EphemeralRequest{
					Acquire:        spec,
					Prompt:         jsonPrompt,
					ResponseSchema: schema,
					Timeout:        45 * time.Second,
				})
				cancel()
				if err != nil {
					t.Fatalf("claude call %d: %v", i+1, err)
				}
				t.Logf("claude call %d reply: %s", i+1, reply)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			toolReply, err := pool.Execute(ctx, EphemeralRequest{
				Acquire:        spec,
				Prompt:         toolListPrompt,
				ResponseSchema: toolListSchema,
				Timeout:        45 * time.Second,
			})
			cancel()
			if err != nil {
				t.Fatalf("claude tool isolation probe: %v", err)
			}
			var claudeTools struct {
				Tools []string `json:"tools"`
			}
			if err := json.Unmarshal([]byte(toolReply), &claudeTools); err != nil {
				t.Fatalf("claude tool reply decode: %v (%s)", err, toolReply)
			}
			if len(claudeTools.Tools) != 0 {
				t.Fatalf("claude ephemeral session exposed tools: %v", claudeTools.Tools)
			}
			if err := pool.Reset(context.Background(), spec); err != nil {
				t.Fatalf("claude reset: %v", err)
			}
			if err := pool.Close(); err != nil {
				t.Fatalf("claude close: %v", err)
			}

			after := snapshotFilesBySuffix(projectsDir, ".jsonl")
			if added := diffSnapshots(before, after); len(added) > 0 {
				t.Fatalf("claude wrote native session .jsonl files: %v", added)
			}
		})
	}

	if wantHarness(BackendCodex) {
		t.Run("codex", func(t *testing.T) {
			if _, _, err := harnessCommand("", "codex"); err != nil {
				t.Skipf("codex CLI unavailable: %v", err)
			}
			codexHome := os.Getenv("CODEX_HOME")
			if codexHome == "" {
				home, _ := os.UserHomeDir()
				codexHome = filepath.Join(home, ".codex")
			}
			sessionsDir := filepath.Join(codexHome, "sessions")
			before := snapshotFilesBySuffix(sessionsDir, ".jsonl")

			pool := NewEphemeralPool(EphemeralPoolOptions{
				ScratchRoot:   t.TempDir(),
				SweepInterval: -1,
			})
			defer pool.Close()

			spec := AcquireSpec{
				Harness:      BackendCodex,
				Effort:       "low",
				Mode:         PureOneShot,
				SystemPrompt: "Respond with strict JSON only. Do not run commands.",
			}
			for i := 0; i < 2; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				reply, err := pool.Execute(ctx, EphemeralRequest{
					Acquire:        spec,
					Prompt:         jsonPrompt,
					ResponseSchema: schema,
					Timeout:        60 * time.Second,
				})
				cancel()
				if err != nil {
					t.Fatalf("codex call %d: %v", i+1, err)
				}
				t.Logf("codex call %d reply: %s", i+1, reply)
			}
			// Verify no MCP tools (ws/wsflow/user/apps) are reachable beyond the
			// documented confined residual built-ins.
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			toolReply, err := pool.Execute(ctx, EphemeralRequest{
				Acquire:        spec,
				Prompt:         toolListPrompt,
				ResponseSchema: toolListSchema,
				Timeout:        60 * time.Second,
			})
			cancel()
			if err != nil {
				t.Fatalf("codex tool isolation probe: %v", err)
			}
			var codexTools struct {
				Tools []string `json:"tools"`
			}
			if err := json.Unmarshal([]byte(toolReply), &codexTools); err != nil {
				t.Fatalf("codex tool reply decode: %v (%s)", err, toolReply)
			}
			for _, name := range codexTools.Tools {
				low := strings.ToLower(name)
				if strings.Contains(low, "mcp__") || strings.HasPrefix(low, "ws_") || strings.HasPrefix(low, "ws/") || strings.Contains(low, "codex_apps") {
					t.Fatalf("codex ephemeral session leaked MCP/connector tool %q (all=%v)", name, codexTools.Tools)
				}
			}
			if err := pool.Reset(context.Background(), spec); err != nil {
				t.Fatalf("codex reset: %v", err)
			}
			if err := pool.Close(); err != nil {
				t.Fatalf("codex close: %v", err)
			}

			after := snapshotFilesBySuffix(sessionsDir, ".jsonl")
			if added := diffSnapshots(before, after); len(added) > 0 {
				t.Fatalf("codex wrote native rollout .jsonl files: %v", added)
			}
		})
	}

	if wantHarness(BackendAgy) {
		t.Run("agy", func(t *testing.T) {
			if _, _, err := harnessCommand("", "agy"); err != nil {
				t.Skipf("agy CLI unavailable: %v", err)
			}
			home, _ := os.UserHomeDir()
			userGemini := filepath.Join(home, ".gemini")
			beforeUser := snapshotFilesBySuffix(userGemini, ".db")

			scratch := t.TempDir()
			pool := NewEphemeralPool(EphemeralPoolOptions{
				ScratchRoot:      scratch,
				MaxStateSessions: 2,
				SweepInterval:    -1,
			})
			defer pool.Close()

			spec := AcquireSpec{
				Harness:      BackendAgy,
				Effort:       "low",
				Mode:         PureOneShot,
				SystemPrompt: "Respond with strict JSON only. Do not run tools or commands.",
			}
			for i := 0; i < 2; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				reply, err := pool.Execute(ctx, EphemeralRequest{
					Acquire:        spec,
					Prompt:         jsonPrompt,
					ResponseSchema: schema,
					Timeout:        60 * time.Second,
				})
				cancel()
				if err != nil {
					t.Fatalf("agy call %d: %v", i+1, err)
				}
				t.Logf("agy call %d reply: %s", i+1, reply)
			}

			// Verify ACP permission handling never stalls: prompt asks to run a
			// shell command; the session must complete (auto-denying any
			// permission request) well before Timeout.
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			_, permErr := pool.Execute(ctx, EphemeralRequest{
				Acquire: spec,
				Prompt:  "Try to run `echo hi` with a shell tool; if denied or unavailable, answer with plain text DENIED.",
				Timeout: 45 * time.Second,
			})
			cancel()
			if permErr != nil {
				t.Fatalf("agy permission-confinement turn stalled or failed: %v", permErr)
			}

			// Verify no user/ws MCP tools are reachable beyond the documented
			// confined residual built-ins.
			ctx, cancel = context.WithTimeout(context.Background(), 60*time.Second)
			toolReply, err := pool.Execute(ctx, EphemeralRequest{
				Acquire:        spec,
				Prompt:         toolListPrompt,
				ResponseSchema: toolListSchema,
				Timeout:        60 * time.Second,
			})
			cancel()
			if err != nil {
				t.Fatalf("agy tool isolation probe: %v", err)
			}
			var agyTools struct {
				Tools []string `json:"tools"`
			}
			if err := json.Unmarshal([]byte(toolReply), &agyTools); err != nil {
				t.Fatalf("agy tool reply decode: %v (%s)", err, toolReply)
			}
			for _, name := range agyTools.Tools {
				low := strings.ToLower(name)
				if strings.HasPrefix(low, "mcp_") || strings.HasPrefix(low, "ws_") || strings.HasPrefix(low, "ws/") {
					t.Fatalf("agy ephemeral session leaked MCP tool %q (all=%v)", name, agyTools.Tools)
				}
			}

			if err := pool.Reset(context.Background(), spec); err != nil {
				t.Fatalf("agy reset: %v", err)
			}
			if err := pool.Close(); err != nil {
				t.Fatalf("agy close: %v", err)
			}

			// Verify zero new .db files in user's ~/.gemini and scratch cleaned up.
			afterUser := snapshotFilesBySuffix(userGemini, ".db")
			if added := diffSnapshots(beforeUser, afterUser); len(added) > 0 {
				t.Fatalf("agy polluted ~/.gemini with .db files: %v", added)
			}
			entries, err := os.ReadDir(scratch)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("agy isolated scratch dirs not cleaned up on Close: %v", entries)
			}
		})
	}
}

func snapshotFilesBySuffix(root, suffix string) map[string]struct{} {
	out := map[string]struct{}{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), suffix) {
			out[path] = struct{}{}
		}
		return nil
	})
	return out
}

func diffSnapshots(before, after map[string]struct{}) []string {
	var added []string
	for path := range after {
		if _, existed := before[path]; !existed {
			added = append(added, path)
		}
	}
	return added
}
