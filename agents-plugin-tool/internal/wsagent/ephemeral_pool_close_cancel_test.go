package wsagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func waitForMarkerFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for marker file %s", path)
}

// TestEphemeralPoolCloseReturnsPromptlyOnHangingStart verifies Finding 3:
// EphemeralPool.Close() cancels the pool context and returns promptly (<1s)
// even when AgyBackend.Start or CodexBackend.Start is stalled in handshake or
// control RPC calls (initialize, thread/start, session/set_config_option).
func TestEphemeralPoolCloseReturnsPromptlyOnHangingStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake CLI")
	}

	t.Run("agy_hang_on_initialize", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "started")
		script := writeFakeCLIScript(t, fmt.Sprintf(`
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf 'init\n' > %q
      ;;
  esac
done
`, marker))

		pool := NewEphemeralPool(EphemeralPoolOptions{
			Backends:      map[string]Backend{BackendAgy: AgyBackend{Command: script}},
			ScratchRoot:   t.TempDir(),
			SweepInterval: -1,
		})

		execDone := make(chan error, 1)
		go func() {
			_, err := pool.Execute(context.Background(), EphemeralRequest{
				Acquire: AcquireSpec{Harness: BackendAgy, Model: "flash", Effort: "low", Mode: BoundedRotation},
				Prompt:  "hello",
				Timeout: 30 * time.Second,
			})
			execDone <- err
		}()
		waitForMarkerFile(t, marker)

		start := time.Now()
		if err := pool.Close(); err != nil {
			t.Fatalf("pool.Close: %v", err)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("pool.Close took %s (want < 1s)", elapsed)
		}
		select {
		case err := <-execDone:
			if err == nil {
				t.Fatal("expected Execute error after pool.Close")
			}
		case <-time.After(time.Second):
			t.Fatal("Execute did not unblock after pool.Close")
		}
	})

	t.Run("agy_hang_on_set_config_option", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "set_config")
		script := writeFakeCLIScript(t, fmt.Sprintf(`
while IFS= read -r line; do
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"protocolVersion":1,"agentInfo":{"version":"1.5.15"}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"sessionId":"agy-s1","modes":{"currentModeId":"default","availableModes":[{"id":"default"}]},"configOptions":[{"id":"model","currentValue":"pro","options":[{"value":"pro"},{"value":"flash"}]},{"id":"thought_level","currentValue":"high","options":[{"value":"low"},{"value":"high"}]}]}}\n' "$id"
      ;;
    *'"method":"session/set_config_option"'*)
      printf 'set_config\n' > %q
      ;;
  esac
done
`, marker))

		pool := NewEphemeralPool(EphemeralPoolOptions{
			Backends:      map[string]Backend{BackendAgy: AgyBackend{Command: script}},
			ScratchRoot:   t.TempDir(),
			SweepInterval: -1,
		})

		execDone := make(chan error, 1)
		go func() {
			_, err := pool.Execute(context.Background(), EphemeralRequest{
				Acquire: AcquireSpec{Harness: BackendAgy, Model: "flash", Effort: "low", Mode: BoundedRotation},
				Prompt:  "hello",
				Timeout: 30 * time.Second,
			})
			execDone <- err
		}()
		waitForMarkerFile(t, marker)

		start := time.Now()
		if err := pool.Close(); err != nil {
			t.Fatalf("pool.Close: %v", err)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("pool.Close took %s (want < 1s)", elapsed)
		}
		select {
		case err := <-execDone:
			if err == nil {
				t.Fatal("expected Execute error after pool.Close")
			}
		case <-time.After(time.Second):
			t.Fatal("Execute did not unblock after pool.Close")
		}
	})

	t.Run("codex_hang_on_initialize", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "codex_init")
		script := writeFakeCLIScript(t, fmt.Sprintf(`
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf 'init\n' > %q
      ;;
  esac
done
`, marker))

		pool := NewEphemeralPool(EphemeralPoolOptions{
			Backends:      map[string]Backend{BackendCodex: CodexBackend{Command: script}},
			ScratchRoot:   t.TempDir(),
			SweepInterval: -1,
		})

		execDone := make(chan error, 1)
		go func() {
			_, err := pool.Execute(context.Background(), EphemeralRequest{
				Acquire: AcquireSpec{Harness: BackendCodex, Model: "gpt-5", Mode: BoundedRotation},
				Prompt:  "hello",
				Timeout: 30 * time.Second,
			})
			execDone <- err
		}()
		waitForMarkerFile(t, marker)

		start := time.Now()
		if err := pool.Close(); err != nil {
			t.Fatalf("pool.Close: %v", err)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("pool.Close took %s (want < 1s)", elapsed)
		}
		select {
		case err := <-execDone:
			if err == nil {
				t.Fatal("expected Execute error after pool.Close")
			}
		case <-time.After(time.Second):
			t.Fatal("Execute did not unblock after pool.Close")
		}
	})

	t.Run("codex_hang_on_thread_start", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "codex_thread_start")
		script := writeFakeCLIScript(t, fmt.Sprintf(`
while IFS= read -r line; do
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":"%%s","result":{"userAgent":"codex-test"}}\n' "$id"
      ;;
    *'"method":"thread/start"'*)
      printf 'thread_start\n' > %q
      ;;
  esac
done
`, marker))

		pool := NewEphemeralPool(EphemeralPoolOptions{
			Backends:      map[string]Backend{BackendCodex: CodexBackend{Command: script}},
			ScratchRoot:   t.TempDir(),
			SweepInterval: -1,
		})

		execDone := make(chan error, 1)
		go func() {
			_, err := pool.Execute(context.Background(), EphemeralRequest{
				Acquire: AcquireSpec{Harness: BackendCodex, Model: "gpt-5", Mode: BoundedRotation},
				Prompt:  "hello",
				Timeout: 30 * time.Second,
			})
			execDone <- err
		}()
		waitForMarkerFile(t, marker)

		start := time.Now()
		if err := pool.Close(); err != nil {
			t.Fatalf("pool.Close: %v", err)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("pool.Close took %s (want < 1s)", elapsed)
		}
		select {
		case err := <-execDone:
			if err == nil {
				t.Fatal("expected Execute error after pool.Close")
			}
		case <-time.After(time.Second):
			t.Fatal("Execute did not unblock after pool.Close")
		}
	})

	t.Run("acp_reset_honors_ctx_on_set_config_option", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "reset_set_config")
		script := writeFakeCLIScript(t, fmt.Sprintf(`
sess=0
while IFS= read -r line; do
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"protocolVersion":1,"agentInfo":{"version":"1.5.15"}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      sess=$((sess + 1))
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"sessionId":"agy-s%%d","modes":{"currentModeId":"default","availableModes":[{"id":"default"}]},"configOptions":[{"id":"model","currentValue":"pro","options":[{"value":"pro"},{"value":"flash"}]},{"id":"thought_level","currentValue":"high","options":[{"value":"low"},{"value":"high"}]}]}}\n' "$id" "$sess"
      ;;
    *'"method":"session/close"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{}}\n' "$id"
      ;;
    *'"method":"session/set_config_option"'*)
      if [ "$sess" -eq 1 ]; then
        printf '{"jsonrpc":"2.0","id":"%%s","result":{"configOptions":[{"id":"model","currentValue":"flash","options":[{"value":"pro"},{"value":"flash"}]},{"id":"thought_level","currentValue":"low","options":[{"value":"low"},{"value":"high"}]}]}}\n' "$id"
      else
        printf 'reset_hang\n' > %q
      fi
      ;;
  esac
done
`, marker))

		proc, err := (AgyBackend{Command: script}).Start(context.Background(), StartSpec{
			Cwd:               t.TempDir(),
			Ephemeral:         true,
			ephemeralStateDir: t.TempDir(),
			Model:             "flash",
			Effort:            "low",
		})
		if err != nil {
			t.Fatalf("AgyBackend.Start: %v", err)
		}
		defer proc.Close()

		acp := proc.(*acpProcess)
		resetCtx, cancel := context.WithCancel(context.Background())
		resetDone := make(chan error, 1)
		go func() {
			resetDone <- acp.Reset(resetCtx)
		}()
		waitForMarkerFile(t, marker)

		start := time.Now()
		cancel()
		select {
		case err := <-resetDone:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("acp.Reset error = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("acp.Reset did not return promptly after ctx cancel")
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("acp.Reset took %s after cancel (want < 1s)", elapsed)
		}
	})

	t.Run("agy_start_honors_ctx_on_mcp_connect_wait", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "session_new_done")
		script := writeFakeCLIScript(t, fmt.Sprintf(`
while IFS= read -r line; do
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"protocolVersion":1,"agentInfo":{"version":"1.5.15"}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":"%%s","result":{"sessionId":"agy-mcp-1","modes":{"currentModeId":"default","availableModes":[{"id":"default"}]}}}\n' "$id"
      printf 'new_done\n' > %q
      ;;
  esac
done
`, marker))

		startCtx, cancel := context.WithCancel(context.Background())
		startDone := make(chan error, 1)
		go func() {
			p, err := (AgyBackend{Command: script}).Start(startCtx, StartSpec{
				Cwd: t.TempDir(),
				MCPSession: &MCPSession{
					URL:       "http://127.0.0.1:0/mcp",
					Token:     "tok",
					Connected: make(chan struct{}), // never closes
				},
			})
			if p != nil {
				_ = p.Close()
			}
			startDone <- err
		}()
		waitForMarkerFile(t, marker)
		// Give openSession a moment to enter the Connected select.
		time.Sleep(25 * time.Millisecond)

		start := time.Now()
		cancel()
		select {
		case err := <-startDone:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("AgyBackend.Start error = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("AgyBackend.Start did not return promptly after ctx cancel during MCP connect wait")
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("AgyBackend.Start took %s after cancel (want < 1s)", elapsed)
		}
	})
}
