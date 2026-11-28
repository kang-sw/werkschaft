package wsharnessenv

import (
	"slices"
	"testing"
)

func TestDropForOSMatchesWindowsEnvironmentNamesCaseInsensitively(t *testing.T) {
	env := []string{"PATH=C:\\bin", "Ws_MaIlBoX=parent@machine", "ws_hosted_session=1", "USER_SETTING=kept"}
	if got := DropForOS("windows", env); !slices.Equal(got, []string{"PATH=C:\\bin", "USER_SETTING=kept"}) {
		t.Fatalf("Windows drop = %v", got)
	}
	if got := DropForOS("linux", env); !slices.Equal(got, env) {
		t.Fatalf("Unix environment names should remain case-sensitive: %v", got)
	}
}

func TestDropDurableForOSDropsParentHarnessAndSessionVars(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"WS_CACHE_HOME=/cache",
		"WS_MCP_DEBUG_LOG=/debug.log",
		"CLAUDECODE=1",
		"CLAUDE_CODE_SSE_PORT=123",
		"CODEX_THREAD_ID=abc",
		"PI_SESSION=1",
		"ORCA_FOO=1",
		"WS_PI_CHANNEL_CREDENTIAL=sec",
		"claude_code_sse_port=456",
		"codex_thread_id=def",
	}
	wantUnix := []string{"PATH=/usr/bin", "WS_CACHE_HOME=/cache", "claude_code_sse_port=456", "codex_thread_id=def"}
	if got := DropDurableForOS("linux", env); !slices.Equal(got, wantUnix) {
		t.Fatalf("Unix DropDurable = %v, want %v", got, wantUnix)
	}
	wantWin := []string{"PATH=/usr/bin", "WS_CACHE_HOME=/cache"}
	if got := DropDurableForOS("windows", env); !slices.Equal(got, wantWin) {
		t.Fatalf("Windows DropDurable = %v, want %v", got, wantWin)
	}
	// DropForOS must continue to preserve parent-harness vars such as CODEX_HOME.
	withCodexHome := []string{"PATH=/usr/bin", "CODEX_HOME=/codex", "WS_MCP_DEBUG_LOG=/debug.log"}
	if got := DropForOS("linux", withCodexHome); !slices.Equal(got, []string{"PATH=/usr/bin", "CODEX_HOME=/codex"}) {
		t.Fatalf("DropForOS unexpectedly dropped parent-harness var: %v", got)
	}
}
