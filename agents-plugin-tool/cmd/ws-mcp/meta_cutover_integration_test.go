package main

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsmeta"
	"github.com/kang-sw/devenv/internal/wsstate"
	"github.com/kang-sw/devenv/internal/wstrackerhook"
	_ "modernc.org/sqlite"
)

const (
	pinnedPreCutoverCommit  = "b118102ab"
	pinnedPreCutoverVersion = "0.53.0-precutover"
	postCutoverVersion      = "0.53.1-postcutover"
)

var (
	cutoverBinsOnce sync.Once
	cutoverOldBin   string
	cutoverNewBin   string
	cutoverBinsErr  error
	cutoverBinsSkip string
)

// cutoverTestBins builds the pinned pre-cutover ws-mcp binary (from git archive
// of pinnedPreCutoverCommit, never git worktree add) and the current ws-mcp
// binary once per test binary invocation inside TestMain's disposable HOME so
// runTestMain removes them on exit.
func cutoverTestBins(t *testing.T) (oldBin, newBin string) {
	t.Helper()
	cutoverBinsOnce.Do(func() {
		repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
		if err != nil {
			cutoverBinsErr = err
			return
		}
		check := exec.Command("git", "-C", repoRoot, "cat-file", "-e", pinnedPreCutoverCommit+"^{commit}")
		if err := check.Run(); err != nil {
			cutoverBinsSkip = fmt.Sprintf("pinned pre-cutover commit %s unavailable in git history: %v", pinnedPreCutoverCommit, err)
			return
		}
		binRoot := filepath.Join(os.Getenv("HOME"), "meta-cutover-bins")
		if err := os.MkdirAll(binRoot, 0o755); err != nil {
			cutoverBinsErr = err
			return
		}
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		newPath := filepath.Join(binRoot, "ws-mcp-new"+ext)
		buildNew := exec.Command("go", "build", "-ldflags", "-X main.version="+postCutoverVersion, "-o", newPath, ".")
		if out, err := buildNew.CombinedOutput(); err != nil {
			cutoverBinsErr = fmt.Errorf("build current ws-mcp: %v\n%s", err, out)
			return
		}

		srcRoot, err := os.MkdirTemp(binRoot, "precutover-src-*")
		if err != nil {
			cutoverBinsErr = err
			return
		}
		defer os.RemoveAll(srcRoot)
		if err := extractGitArchive(repoRoot, pinnedPreCutoverCommit, "agents-plugin-tool", srcRoot); err != nil {
			cutoverBinsErr = fmt.Errorf("extract %s: %w", pinnedPreCutoverCommit, err)
			return
		}
		oldPath := filepath.Join(binRoot, "ws-mcp-old"+ext)
		buildOld := exec.Command("go", "build", "-ldflags", "-X main.version="+pinnedPreCutoverVersion, "-o", oldPath, "./cmd/ws-mcp")
		buildOld.Dir = filepath.Join(srcRoot, "agents-plugin-tool")
		if out, err := buildOld.CombinedOutput(); err != nil {
			cutoverBinsErr = fmt.Errorf("build pinned %s ws-mcp: %v\n%s", pinnedPreCutoverCommit, err, out)
			return
		}
		cutoverOldBin = oldPath
		cutoverNewBin = newPath
	})
	if cutoverBinsSkip != "" {
		t.Skip(cutoverBinsSkip)
	}
	if cutoverBinsErr != nil {
		t.Fatal(cutoverBinsErr)
	}
	return cutoverOldBin, cutoverNewBin
}

func extractGitArchive(repoRoot, commit, subdir, dest string) error {
	cmd := exec.Command("git", "-C", repoRoot, "archive", "--format=tar", commit, subdir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git archive %s %s: %v (%s)", commit, subdir, err, strings.TrimSpace(stderr.String()))
	}
	tr := tar.NewReader(&stdout)
	cleanDest := filepath.Clean(dest) + string(filepath.Separator)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(filepath.Clean(target)+string(filepath.Separator), cleanDest) && filepath.Clean(target) != filepath.Clean(dest) {
			return fmt.Errorf("archive path escapes dest: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			_, cpErr := io.Copy(f, tr)
			closeErr := f.Close()
			if cpErr != nil {
				return cpErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

type cutoverDaemon struct {
	cmd *exec.Cmd
	ep  wsdashboard.Endpoint
	out *lockedBuffer
	dir string
}

func startCutoverDaemon(t *testing.T, bin string, env []string, dir, wantVersion string, extraArgs ...string) *cutoverDaemon {
	t.Helper()
	hasPort := false
	for _, a := range extraArgs {
		if a == "--port" || strings.HasPrefix(a, "--port=") {
			hasPort = true
			break
		}
	}
	args := []string{"dashboard"}
	if !hasPort {
		args = append(args, "--port", "0")
	}
	args = append(args, extraArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out := &lockedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	d := &cutoverDaemon{cmd: cmd, out: out, dir: dir}
	t.Cleanup(func() {
		if d.cmd.ProcessState == nil && d.cmd.Process != nil {
			if ep, err := wsdashboard.ReadEndpoint(dir); err == nil && ep.PID == d.cmd.Process.Pid {
				_ = wsdashboard.RequestShutdown(context.Background(), dashboardTestClient, ep)
			}
			done := make(chan error, 1)
			go func() { done <- d.cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = d.cmd.Process.Kill()
				<-done
			}
		}
	})
	d.ep = waitForEndpoint(t, dir, wantVersion, 15*time.Second)
	if d.ep.PID != cmd.Process.Pid {
		t.Fatalf("started daemon pid=%d, endpoint pid=%d (log:\n%s)", cmd.Process.Pid, d.ep.PID, out.String())
	}
	return d
}

func (d *cutoverDaemon) stop(t *testing.T) {
	t.Helper()
	if err := wsdashboard.RequestShutdown(context.Background(), dashboardTestClient, d.ep); err != nil {
		t.Fatalf("shutdown %s: %v\nlog:\n%s", d.ep.Version, err, d.out.String())
	}
	if err := waitExit(t, d.cmd, 15*time.Second); err != nil {
		t.Fatalf("daemon exit (%s): %v\nlog:\n%s", d.ep.Version, err, d.out.String())
	}
}

func (d *cutoverDaemon) stopAllHolders(t *testing.T) {
	t.Helper()
	// Stop all holders spawned under d.dir so no stray python holder outlives the test.
	holdersDir := filepath.Join(d.dir, "holders")
	entries, err := os.ReadDir(holdersDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "bin" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(holdersDir, e.Name(), "descriptor.json"))
		if err != nil {
			continue
		}
		var desc struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(raw, &desc) == nil && desc.PID > 0 {
			if p, err := os.FindProcess(desc.PID); err == nil {
				_ = p.Kill()
				_ = p.Release()
			}
		}
	}
}

func stopAllHoldersInDir(dir string) {
	holdersDir := filepath.Join(dir, "holders")
	entries, err := os.ReadDir(holdersDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "bin" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(holdersDir, e.Name(), "descriptor.json"))
		if err != nil {
			continue
		}
		var desc struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(raw, &desc) == nil && desc.PID > 0 {
			if p, err := os.FindProcess(desc.PID); err == nil {
				_ = p.Kill()
				_ = p.Release()
			}
		}
	}
}

func daemonCookie(t *testing.T, ep wsdashboard.Endpoint) *http.Cookie {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/auth/cookie", ep.Port), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	resp, err := dashboardTestClient.Do(req)
	if err != nil {
		t.Fatalf("POST /auth/cookie: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /auth/cookie status=%d body=%s", resp.StatusCode, body)
	}
	var payload struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Name == "" || payload.Value == "" {
		t.Fatalf("decode /auth/cookie: %v (%s)", err, body)
	}
	return &http.Cookie{Name: payload.Name, Value: payload.Value}
}

func daemonJSON(t *testing.T, ep wsdashboard.Endpoint, cookie *http.Cookie, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", ep.Port)
	req, err := http.NewRequest(method, origin+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	if method == http.MethodPost {
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := dashboardTestClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, raw
}

func runWSMCP(t *testing.T, bin string, env []string, cwd string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func initCutoverGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

func writeFakeClaudeOnPath(t *testing.T, env []string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake claude wrapper script")
	}
	binDir := t.TempDir()
	script := `#!/usr/bin/env python3
import json, os, sys, time

if "--version" in sys.argv:
    print("2.1.326 (fake)")
    sys.exit(0)

init = {
    "type": "system",
    "subtype": "init",
    "session_id": "fake-claude-sess-1",
    "permissionMode": "dontAsk",
    "claude_code_version": "2.1.326",
    "tools": [],
    "slash_commands": ["compact"],
}
print(json.dumps(init), flush=True)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue
    t = msg.get("type")
    if t == "control_request":
        req_id = msg.get("request_id", "")
        print(json.dumps({"type": "control_response", "response": {"subtype": "success", "request_id": req_id, "response": {}}}), flush=True)
        continue
    if t == "user":
        raw_text = json.dumps(msg)
        marker = "WAIT_UNTIL_FILE:"
        if marker in raw_text:
            after = raw_text.split(marker, 1)[1]
            gate = ""
            for ch in after:
                if ch in '"\\\n\r\t ':
                    break
                gate += ch
            if gate:
                deadline = time.time() + 30
                while time.time() < deadline and not os.path.exists(gate):
                    time.sleep(0.05)
        print(json.dumps({"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "ack"}]}}), flush=True)
        print(json.dumps({"type": "result", "subtype": "success", "session_id": "fake-claude-sess-1", "result": "ack", "duration_ms": 5}), flush=True)
`
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			out = append(out, "PATH="+binDir+string(os.PathListSeparator)+strings.TrimPrefix(entry, "PATH="))
			replaced = true
		} else {
			out = append(out, entry)
		}
	}
	if !replaced {
		out = append(out, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return out
}

func envWithManifest(t *testing.T, dashboardEnabled bool) ([]string, string) {
	t.Helper()
	env, dir := dashboardTestEnv(t)
	env = append(env, "WS_MCP_CONFIG_MANIFESTS="+writeProxyTestManifest(t, dashboardEnabled))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAllHoldersInDir(dir) })
	return env, dir
}

func setEnvKey(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			out = append(out, prefix+value)
			replaced = true
		} else {
			out = append(out, entry)
		}
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func mintKeyWithEnv(t *testing.T, env []string, root string, lead bool) string {
	t.Helper()
	cacheHome := envValue(t, env, "WS_CACHE_HOME")
	configHome := envValue(t, env, "WS_CONFIG_HOME")
	t.Setenv("WS_CACHE_HOME", cacheHome)
	t.Setenv("WS_CONFIG_HOME", configHome)
	ks := mcp.NewAgentKeyStore()
	if lead {
		key, err := ks.MintLead(root)
		if err != nil {
			t.Fatalf("MintLead(%s): %v", root, err)
		}
		return key
	}
	key, err := ks.Mint(root)
	if err != nil {
		t.Fatalf("Mint(%s): %v", root, err)
	}
	return key
}

func triggerOldPrune(t *testing.T, oldBin string, env []string, repo string) {
	t.Helper()
	// Mint a fresh top-level lead key and invoke todo.list through the pinned
	// b118102ab binary. On first dashboard registration, b118102ab's dashboardTouch
	// invokes wsdashboard.PruneRegistrations(dir, now, RegistryRetention).
	leadKey := mintKeyWithEnv(t, env, repo, true)
	argsJSON, _ := json.Marshal(map[string]any{"session_key": leadKey})
	// Direct call in b118102ab calls dashboardTouch when dashboard is enabled in config.
	stdout, stderr, err := runWSMCP(t, oldBin, env, repo, "call", "todo.list", string(argsJSON))
	if err != nil {
		t.Fatalf("oldBin todo.list failed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
}

func waitHeldTurnDone(t *testing.T, ep wsdashboard.Endpoint, cookie *http.Cookie, dir, replyID string, deadline time.Duration) wsagent.State {
	t.Helper()
	ringDir := filepath.Join(wsdashboard.SessionDir(dir, replyID), "ring")
	return waitAgentState(t, ep, cookie, replyID, deadline, func(s wsagent.State) bool {
		if !s.Process || s.Status != wsagent.StatusIdle {
			return false
		}
		entries, err := os.ReadDir(ringDir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if raw, err := os.ReadFile(filepath.Join(ringDir, e.Name())); err == nil && strings.Contains(string(raw), "ack") {
				return true
			}
		}
		return false
	})
}

func waitAgentState(t *testing.T, ep wsdashboard.Endpoint, cookie *http.Cookie, replyID string, deadline time.Duration, pred func(wsagent.State) bool) wsagent.State {
	t.Helper()
	end := time.Now().Add(deadline)
	var last wsagent.State
	var lastBody []byte
	var lastStatus int
	for time.Now().Before(end) {
		st, body := daemonJSON(t, ep, cookie, http.MethodGet, "/agents/"+replyID, nil)
		lastStatus, lastBody = st, body
		if st == http.StatusOK {
			var cur wsagent.State
			if json.Unmarshal(body, &cur) == nil {
				last = cur
				if pred(cur) {
					return cur
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for agent %s state (status=%d body=%s parsed=%+v)", replyID, lastStatus, lastBody, last)
	return wsagent.State{}
}

func writeConfigJSON(t *testing.T, env []string, doc map[string]any) {
	t.Helper()
	cfgDir := envValue(t, env, "WS_CONFIG_HOME")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyAgentRecord(t *testing.T, dir string, rec wsagent.Record) {
	t.Helper()
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = 1
	}
	sessDir := wsdashboard.SessionDir(dir, rec.ReplyID)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, wsdashboard.AgentRecordFileName), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMetaCutoverInterruptedWithPinnedOldDaemonIntervening verifies (2125) and
// (2135): interrupting cutover before fencing and letting the real pinned
// b118102ab daemon lower a permission and prune an expired session, interrupting
// after a fence replacement before its journal mark, and interrupting on both
// sides of the import commit while keeping protected adjacent files intact.
func TestMetaCutoverInterruptedWithPinnedOldDaemonIntervening(t *testing.T) {
	oldBin, newBin := cutoverTestBins(t)

	t.Run("interrupted-before-fence-with-real-old-daemon-and-prune", func(t *testing.T) {
		env, dir := envWithManifest(t, true)
		env = writeFakeClaudeOnPath(t, env)
		repo := initCutoverGitRepo(t, filepath.Join(t.TempDir(), "repo"))
		leadKey := mintKeyWithEnv(t, env, repo, true)
		leadID := wsmailbox.ReplyID(leadKey)
		nowStr := time.Now().UTC().Format(time.RFC3339)

		// Active hosted lead with workdir-write permission.
		if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
			SchemaVersion: 1, ReplyID: leadID, Project: repo, Harness: wsagent.BackendClaude,
			Hosted: true, RegisteredAt: nowStr, HeartbeatAt: nowStr,
		}); err != nil {
			t.Fatal(err)
		}
		writeLegacyAgentRecord(t, dir, wsagent.Record{
			ReplyID: leadID, Profile: wsagent.LeadProfileClaude, Backend: wsagent.BackendClaude,
			Root: repo, WSKey: leadKey, Lead: true, CreatedAt: nowStr,
		})
		leadDir := wsdashboard.SessionDir(dir, leadID)
		writeLegacyFile(t, filepath.Join(leadDir, "permission.json"), []byte(`{"permission_level":"workdir-write"}`+"\n"))
		writeLegacyFile(t, filepath.Join(leadDir, "label.json"), []byte(`{"label":"Intervened Lead"}`+"\n"))

		// Expired non-hosted session with a label so old prune removes it.
		const expiredID = "expiredbeforefence01"
		oldStamp := time.Now().Add(-wsdashboard.RegistryRetention - 48*time.Hour).UTC().Format(time.RFC3339)
		if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
			SchemaVersion: 1, ReplyID: expiredID, Project: repo, Harness: wsagent.BackendClaude,
			RegisteredAt: oldStamp, HeartbeatAt: oldStamp, EndedAt: oldStamp,
		}); err != nil {
			t.Fatal(err)
		}
		writeLegacyFile(t, filepath.Join(wsdashboard.SessionDir(dir, expiredID), "label.json"), []byte(`{"label":"Should Be Pruned"}`+"\n"))

		// Protected adjacent files that cutover must never alter or remove.
		// Place live snapshot/ring files under an active terminal (non-hosted) session
		// so the runner's hosted-lead startup snapshot refresh does not touch them, while
		// also asserting leadID's own inbox, visibility, registration, and root files stay intact.
		const terminalID = "terminaladjacent01"
		if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
			SchemaVersion: 1, ReplyID: terminalID, Project: repo, Harness: wsagent.BackendClaude,
			RegisteredAt: nowStr, HeartbeatAt: nowStr,
		}); err != nil {
			t.Fatal(err)
		}
		termDir := wsdashboard.SessionDir(dir, terminalID)
		adjacent := map[string][]byte{
			filepath.Join(termDir, "snapshot.json"):        []byte(`{"schema":1,"marker":"keep-snap"}` + "\n"),
			filepath.Join(termDir, "ring", "000001.jsonl"): []byte(`{"k":"keep-ring"}` + "\n"),
			filepath.Join(leadDir, "inbox", "000001.json"): []byte(`{"id":"keep-inbox"}` + "\n"),
			filepath.Join(dir, "hire-proposals.json"):      []byte(`[]` + "\n"),
			filepath.Join(dir, "workspace-admission.json"): []byte(`{"version":1,"entries":[]}` + "\n"),
			filepath.Join(leadDir, "visibility.json"):      []byte(`{"hidden":false}` + "\n"),
		}
		for p, b := range adjacent {
			writeLegacyFile(t, p, b)
		}
		regBefore, err := os.ReadFile(wsdashboard.RegistrationPath(dir, terminalID))
		if err != nil {
			t.Fatal(err)
		}

		// 1. Interrupt cutover right after Stage, BEFORE any fence is applied.
		st, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
		if err != nil {
			t.Fatal(err)
		}
		stopErr := errors.New("injected crash before fencing")
		_, err = st.Cutover(context.Background(), wsmeta.CutoverOptions{
			Validate: map[string]wsmeta.LegacyValidator{
				wsmeta.StoreAgentRecord:       wsagent.ValidateLegacyRecord,
				wsmeta.StoreUsageHistory:      wsdashboard.ValidateLegacyUsageHistory,
				wsmeta.StoreUsageHistoryCodex: wsdashboard.ValidateLegacyUsageHistory,
				wsmeta.StoreUsageHistoryAgy:   wsdashboard.ValidateLegacyUsageHistory,
			},
			BeforeFence: func(int, []wsmeta.Capture) error { return stopErr },
		})
		if !errors.Is(err, stopErr) {
			st.Close()
			t.Fatalf("expected injected pre-fence error, got %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}

		// 2. Intervene with the REAL pinned b118102ab daemon: lower leadID's
		// permission to read-only, and trigger b118102ab's real PruneRegistrations
		// to prune expiredID.
		oldD := startCutoverDaemon(t, oldBin, env, dir, pinnedPreCutoverVersion)
		cookie := daemonCookie(t, oldD.ep)
		status, body := daemonJSON(t, oldD.ep, cookie, http.MethodPost, "/agents/"+leadID+"/settings/permission", map[string]any{
			"permission_level": "read-only",
		})
		if status != http.StatusOK {
			t.Fatalf("old daemon lower permission status=%d body=%s", status, body)
		}
		permOnDisk, err := os.ReadFile(filepath.Join(leadDir, "permission.json"))
		if err != nil || !strings.Contains(string(permOnDisk), `"read-only"`) {
			t.Fatalf("old daemon did not persist lowered permission.json: %q (%v)", permOnDisk, err)
		}
		triggerOldPrune(t, oldBin, env, repo)
		if _, err := os.Stat(wsdashboard.SessionDir(dir, expiredID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pinned old binary did not prune expired session %s: %v", expiredID, err)
		}
		oldD.stop(t)

		// 3. Retry cutover by starting the NEW daemon.
		newD := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion)
		newCookie := daemonCookie(t, newD.ep)
		got := waitAgentState(t, newD.ep, newCookie, leadID, 5*time.Second, func(s wsagent.State) bool {
			return s.PermissionLevel == wsagent.PermissionReadOnly
		})
		if got.PermissionSource != "explicit" {
			t.Fatalf("expected explicit lowered permission after retry, got %+v", got)
		}
		newD.stop(t)

		// Verify pruned session was neither recreated on disk nor imported into SQLite.
		if _, err := os.Stat(wsdashboard.SessionDir(dir, expiredID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pruned session %s was recreated during cutover retry: %v", expiredID, err)
		}
		verifyStore, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
		if err != nil {
			t.Fatal(err)
		}
		defer verifyStore.Close()
		snap, err := verifyStore.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := snap.Labels[expiredID]; ok {
			t.Fatalf("pruned session %s was imported into labels", expiredID)
		}
		if _, ok := snap.Records[expiredID]; ok {
			t.Fatalf("pruned session %s was imported into agent_records", expiredID)
		}
		// Protected adjacent files must remain untouched.
		for p, want := range adjacent {
			gotBytes, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(gotBytes, want) {
				t.Fatalf("protected adjacent file %s changed: got=%q want=%q err=%v", p, gotBytes, want, err)
			}
		}
		if regAfter, err := os.ReadFile(wsdashboard.RegistrationPath(dir, terminalID)); err != nil || !bytes.Equal(regAfter, regBefore) {
			t.Fatalf("registration.json changed across cutover: err=%v", err)
		}
	})

	t.Run("interrupted-after-partial-fence-and-both-sides-of-commit", func(t *testing.T) {
		validators := map[string]wsmeta.LegacyValidator{
			wsmeta.StoreAgentRecord:       wsagent.ValidateLegacyRecord,
			wsmeta.StoreUsageHistory:      wsdashboard.ValidateLegacyUsageHistory,
			wsmeta.StoreUsageHistoryCodex: wsdashboard.ValidateLegacyUsageHistory,
			wsmeta.StoreUsageHistoryAgy:   wsdashboard.ValidateLegacyUsageHistory,
		}
		seedDir := func(t *testing.T) (string, string) {
			t.Helper()
			dir := t.TempDir()
			const rid = "faultlead01"
			sessDir := wsdashboard.SessionDir(dir, rid)
			writeLegacyFile(t, filepath.Join(sessDir, "permission.json"), []byte(`{"permission_level":"workdir-write"}`+"\n"))
			writeLegacyFile(t, filepath.Join(sessDir, "label.json"), []byte(`{"label":"Original Label"}`+"\n"))
			writeLegacyFile(t, filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName), []byte(`{"hooks":[]}`+"\n"))
			return dir, rid
		}

		// Case 1: kill after fence replacement on disk, BEFORE its journal mark (fence-replaced).
		dir1, rid1 := seedDir(t)
		fenceFault := errors.New("crash after fence replacement before mark")
		hitCount := 0
		st1, err := wsmeta.Open(dir1, wsmeta.Options{
			CheckpointIdle: -1,
			Fault: func(stage string) error {
				if stage == "fence-replaced" {
					hitCount++
					if hitCount == 2 { // 1st is tracker-hooks removal, 2nd is permission.json replacement
						return fenceFault
					}
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = st1.Cutover(context.Background(), wsmeta.CutoverOptions{Validate: validators})
		if !errors.Is(err, fenceFault) {
			st1.Close()
			t.Fatalf("expected fence-replaced fault, got %v", err)
		}
		st1.Close()
		// Legacy permission.json on disk is ALREADY replaced by the fence bytes!
		if raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir1, rid1), "permission.json")); err != nil || !strings.Contains(string(raw), wsmeta.PermissionCutoverFencedLevel) {
			t.Fatalf("expected fenced permission.json on disk before crash recovery, got %q (%v)", raw, err)
		}
		// Retry without fault: must recover ORIGINAL staged bytes ("workdir-write"), never "!cutover-fenced".
		st1Retry, err := wsmeta.Open(dir1, wsmeta.Options{CheckpointIdle: -1})
		if err != nil {
			t.Fatal(err)
		}
		defer st1Retry.Close()
		if done, err := st1Retry.ImportComplete(context.Background()); err != nil || done {
			t.Fatalf("ImportComplete before retry = (%v, %v), want false", done, err)
		}
		if _, err := st1Retry.Cutover(context.Background(), wsmeta.CutoverOptions{Validate: validators}); err != nil {
			t.Fatalf("retry after fence-replaced crash: %v", err)
		}
		snap1, err := st1Retry.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if gotPerm := snap1.Permissions[rid1].Level; gotPerm != "workdir-write" {
			t.Fatalf("recovery lost original staged permission: got %+v", snap1.Permissions[rid1])
		}
		if snap1.Labels[rid1] != "Original Label" {
			t.Fatalf("recovery label = %q, want Original Label", snap1.Labels[rid1])
		}

		// Case 2: interrupt on BOTH sides of the import commit (commit-imported = rolled back; commit-done = committed).
		dir2, rid2 := seedDir(t)
		preCommitFault := errors.New("crash inside tx before marker")
		st2, err := wsmeta.Open(dir2, wsmeta.Options{
			CheckpointIdle: -1,
			Fault: func(stage string) error {
				if stage == "commit-imported" {
					return preCommitFault
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = st2.Cutover(context.Background(), wsmeta.CutoverOptions{Validate: validators})
		if !errors.Is(err, preCommitFault) {
			st2.Close()
			t.Fatalf("expected commit-imported fault, got %v", err)
		}
		st2.Close()
		// Neither imported rows nor marker may be visible after rollback.
		db, err := sql.Open("sqlite", filepath.Join(dir2, wsmeta.DBName))
		if err != nil {
			t.Fatal(err)
		}
		var labelCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM labels`).Scan(&labelCount); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		if labelCount != 0 {
			t.Fatalf("rolled-back commit left %d visible rows in labels", labelCount)
		}

		// Now crash AFTER commit tx committed (commit-done): both import and marker MUST be visible together.
		postCommitFault := errors.New("crash right after commit tx")
		st2Post, err := wsmeta.Open(dir2, wsmeta.Options{
			CheckpointIdle: -1,
			Fault: func(stage string) error {
				if stage == "commit-done" {
					return postCommitFault
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = st2Post.Cutover(context.Background(), wsmeta.CutoverOptions{Validate: validators})
		if !errors.Is(err, postCommitFault) {
			st2Post.Close()
			t.Fatalf("expected commit-done fault, got %v", err)
		}
		st2Post.Close()

		st2Verify, err := wsmeta.Open(dir2, wsmeta.Options{CheckpointIdle: -1})
		if err != nil {
			t.Fatal(err)
		}
		defer st2Verify.Close()
		if done, err := st2Verify.ImportComplete(context.Background()); err != nil || !done {
			t.Fatalf("ImportComplete after post-commit crash = (%v, %v), want true", done, err)
		}
		snap2, err := st2Verify.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if snap2.Labels[rid2] != "Original Label" || snap2.Permissions[rid2].Level != "workdir-write" {
			t.Fatalf("import not visible with completion marker: labels=%v perms=%+v", snap2.Labels, snap2.Permissions)
		}
	})
}

// TestMetaCutoverPinnedOldBinaryAgainstFencedState exercises the real pinned
// b118102ab binary (both daemon and ws-mcp call) against cutover-fenced state
// and re-upgrade back to the new daemon (2125, 2136).
func TestMetaCutoverPinnedOldBinaryAgainstFencedState(t *testing.T) {
	oldBin, newBin := cutoverTestBins(t)
	env, dir := envWithManifest(t, true)
	env = writeFakeClaudeOnPath(t, env)
	repo := initCutoverGitRepo(t, filepath.Join(t.TempDir(), "repo"))

	// Configure custom profiles before cutover:
	// - tracker-child-writer: grants tracker.update (child at store root at cutover)
	// - plain-child: read-only at cutover (will be overridden post-cutover!)
	// - worker-noperm: ordinary worker profile
	writeConfigJSON(t, env, map[string]any{
		"schema_version": 1,
		"structured_overrides": map[string]any{
			wsagent.ProfilePrefix + "tracker-child-writer": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"tracker.query", "tracker.update"},
			},
			wsagent.ProfilePrefix + "plain-child": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"playbook.read"},
			},
			wsagent.ProfilePrefix + "worker-noperm": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"playbook.read"},
			},
		},
	})

	// Register tracker store "me" and create one item via a top-level lead key.
	topLeadKey := mintKeyWithEnv(t, env, repo, true)
	registerCmd := `<tk:register store="me" member="ada" given="Ada"/>`
	regArgs, _ := json.Marshal(map[string]any{"session_key": topLeadKey, "commands": registerCmd})
	if out, errOut, err := runWSMCP(t, oldBin, env, repo, "call", "tracker.update", string(regArgs)); err != nil {
		t.Fatalf("register store me: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	createCmd := `<tk:store name="me"><tk:item.create kind="task" title="Cutover test item"/></tk:store>`
	createArgs, _ := json.Marshal(map[string]any{"session_key": topLeadKey, "commands": createCmd})
	createOut, errOut, err := runWSMCP(t, oldBin, env, repo, "call", "tracker.update", string(createArgs))
	if err != nil {
		t.Fatalf("create item in store me: %v\nstdout=%s\nstderr=%s", err, createOut, errOut)
	}
	// Query store "me" in JSON to deterministically get the created item ID.
	qArgs, _ := json.Marshal(map[string]any{"session_key": topLeadKey, "payload": map[string]any{"store": "me"}, "format": "json"})
	qOut, qErr, err := runWSMCP(t, oldBin, env, repo, "call", "tracker.query", string(qArgs))
	if err != nil {
		t.Fatalf("tracker.query me: %v\nstdout=%s\nstderr=%s", err, qOut, qErr)
	}
	var qResp struct {
		Items map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(qOut), &qResp); err != nil || len(qResp.Items) == 0 {
		t.Fatalf("parse tracker.query json for item ID: err=%v out=%s", err, qOut)
	}
	var meItemID string
	for id := range qResp.Items {
		meItemID = id
		break
	}
	// Locate store "me" dir from tracker stores.json. Create an existing
	// directory laterStoreDir that is NOT registered as a tracker store until
	// AFTER cutover (so wsagent keeps childLaterStore open during cutover).
	meDir := findTrackerStoreDir(t, env, "me")
	laterStoreDir := filepath.Join(t.TempDir(), "later-store")
	if err := os.MkdirAll(laterStoreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(laterStoreDir); err == nil {
		laterStoreDir = real
	}

	// Start pinned old daemon first to spawn TWO REAL held processes (one lead,
	// one child worker at meDir) that will survive cutover into the downgrade test!
	// Use one constant loopback port across restarts (matching real daemon
	// replacement) so MCP URLs stay valid and holders are rejoined rather than
	// restarted for a stale port.
	daemonPort := strconv.Itoa(freeLoopbackPort(t))
	oldPre := startCutoverDaemon(t, oldBin, env, dir, pinnedPreCutoverVersion, "--port", daemonPort)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("=== oldPre log ===\n%s", oldPre.out.String())
		}
	})
	preCookie := daemonCookie(t, oldPre.ep)

	// 1. Spawn a real held Claude lead (heldLead) and complete one turn so its
	// holder process stays alive across restarts.
	stCode, body := daemonJSON(t, oldPre.ep, preCookie, http.MethodPost, "/agents/leads/permission", map[string]any{
		"root":             repo,
		"harness":          wsagent.BackendClaude,
		"permission_level": "workdir-write",
	})
	if stCode != http.StatusCreated {
		t.Fatalf("create heldLead on oldPre: status=%d body=%s", stCode, body)
	}
	var heldLeadSt wsagent.State
	if err := json.Unmarshal(body, &heldLeadSt); err != nil {
		t.Fatal(err)
	}
	heldLeadID := heldLeadSt.ID
	stCode, body = daemonJSON(t, oldPre.ep, preCookie, http.MethodPost, "/agents/"+heldLeadID+"/send", map[string]any{"text": "hello held lead"})
	if stCode != http.StatusOK {
		t.Fatalf("send heldLead on oldPre: status=%d body=%s", stCode, body)
	}
	waitHeldTurnDone(t, oldPre.ep, preCookie, dir, heldLeadID, 15*time.Second)

	// 2. Spawn a real held Claude child worker at meDir with tracker-child-writer!
	// Keep its turn active across oldPre -> newD1 -> oldPost using a gate file
	// so daemon shutdown lets go of its live holder instead of sleeping a
	// finished child.
	childGate := filepath.Join(t.TempDir(), "release-child-turn")
	t.Cleanup(func() { _ = os.WriteFile(childGate, []byte("done\n"), 0o600) })
	stCode, body = daemonJSON(t, oldPre.ep, preCookie, http.MethodPost, "/agents", map[string]any{
		"profile": "tracker-child-writer",
		"root":    meDir,
	})
	if stCode != http.StatusCreated {
		t.Fatalf("create childHeld on oldPre: status=%d body=%s", stCode, body)
	}
	var childHeldSt wsagent.State
	if err := json.Unmarshal(body, &childHeldSt); err != nil {
		t.Fatal(err)
	}
	childHeldID := childHeldSt.ID
	stCode, body = daemonJSON(t, oldPre.ep, preCookie, http.MethodPost, "/agents/"+childHeldID+"/send", map[string]any{"text": "WAIT_UNTIL_FILE:" + childGate})
	if stCode != http.StatusOK {
		t.Fatalf("send childHeld on oldPre: status=%d body=%s", stCode, body)
	}
	waitAgentState(t, oldPre.ep, preCookie, childHeldID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process && s.Status == wsagent.StatusRunning
	})
	oldPre.stop(t)

	// Mark childHeld as a parented child in its legacy agent.json (parent != nil)
	// so cutover applies the child profile fence rather than the support closed_at fence.
	childHeldRec, err := wsagent.ReadRecord(dir, childHeldID)
	if err != nil {
		t.Fatal(err)
	}
	childHeldRec.Parent = &wsagent.ParentRef{Kind: wsagent.ParentConversation, ReplyID: heldLeadID}
	writeLegacyAgentRecord(t, dir, childHeldRec)
	childHeldKey := childHeldRec.WSKey

	nowStr := time.Now().UTC().Format(time.RFC3339)
	oldStamp := time.Now().Add(-wsdashboard.RegistryRetention - 48*time.Hour).UTC().Format(time.RFC3339)

	// 3. Seed leadWithPerm (explicit permission.json + hiring enabled & hired identity).
	leadWithPermKey := mintKeyWithEnv(t, env, repo, true)
	leadWithPermID := wsmailbox.ReplyID(leadWithPermKey)
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: leadWithPermID, Project: repo, Harness: wsagent.BackendClaude,
		Hosted: true, RegisteredAt: nowStr, HeartbeatAt: nowStr,
	}); err != nil {
		t.Fatal(err)
	}
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: leadWithPermID, Profile: wsagent.LeadProfileClaude, Backend: wsagent.BackendClaude,
		Root: repo, WSKey: leadWithPermKey, Lead: true, CreatedAt: nowStr,
	})
	writeLegacyFile(t, filepath.Join(wsdashboard.SessionDir(dir, leadWithPermID), "permission.json"), []byte(`{"permission_level":"workdir-write"}`+"\n"))
	writeLegacyFile(t, filepath.Join(wsdashboard.SessionDir(dir, leadWithPermID), "hiring.json"), []byte(`{"enabled":true,"hired":true,"request_id":"req-keep-1"}`+"\n"))

	// 4. Seed leadNoPerm (NO permission.json on disk at import).
	leadNoPermKey := mintKeyWithEnv(t, env, repo, true)
	leadNoPermID := wsmailbox.ReplyID(leadNoPermKey)
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: leadNoPermID, Project: repo, Harness: wsagent.BackendClaude,
		Hosted: true, RegisteredAt: nowStr, HeartbeatAt: nowStr,
	}); err != nil {
		t.Fatal(err)
	}
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: leadNoPermID, Profile: wsagent.LeadProfileClaude, Backend: wsagent.BackendClaude,
		Root: repo, WSKey: leadNoPermKey, Lead: true, CreatedAt: nowStr,
	})

	// 5. Seed workerNoPerm (NO permission.json on disk at import).
	workerNoPermKey := mintKeyWithEnv(t, env, repo, false)
	workerNoPermID := wsmailbox.ReplyID(workerNoPermKey)
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: workerNoPermID, Project: repo, Harness: wsagent.BackendClaude,
		Hosted: true, Agent: true, RegisteredAt: nowStr, HeartbeatAt: nowStr,
	}); err != nil {
		t.Fatal(err)
	}
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: workerNoPermID, Profile: "worker-noperm", Backend: wsagent.BackendClaude,
		Root: repo, WSKey: workerNoPermKey, Parent: &wsagent.ParentRef{Kind: wsagent.ParentConversation, ReplyID: leadWithPermID}, CreatedAt: nowStr,
	})

	// 6. Seed supportConv (parentless non-lead open record at meDir).
	supportKey := mintKeyWithEnv(t, env, meDir, false)
	supportID := wsmailbox.ReplyID(supportKey)
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: supportID, Profile: wsagent.SupportProfileClaude, Backend: wsagent.BackendClaude,
		Root: meDir, WSKey: supportKey, CreatedAt: nowStr,
	})

	// 7. Seed childAtStore (open parented child at meDir with tracker-child-writer,
	// aged registration > retention so we verify old prune keeps it!).
	childAtStoreKey := mintKeyWithEnv(t, env, meDir, false)
	childAtStoreID := wsmailbox.ReplyID(childAtStoreKey)
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: childAtStoreID, Project: meDir, Harness: wsagent.BackendClaude,
		Hosted: true, Agent: true, RegisteredAt: oldStamp, HeartbeatAt: oldStamp, EndedAt: oldStamp,
	}); err != nil {
		t.Fatal(err)
	}
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: childAtStoreID, Profile: "tracker-child-writer", Backend: wsagent.BackendClaude,
		Root: meDir, WSKey: childAtStoreKey, Parent: &wsagent.ParentRef{Kind: wsagent.ParentConversation, ReplyID: leadWithPermID}, CreatedAt: oldStamp,
	})

	// 8. Seed childLaterProfile (open parented child at meDir with plain-child,
	// qualifies for tracker.update only after post-cutover profile override).
	childLaterProfKey := mintKeyWithEnv(t, env, meDir, false)
	childLaterProfID := wsmailbox.ReplyID(childLaterProfKey)
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: childLaterProfID, Profile: "plain-child", Backend: wsagent.BackendClaude,
		Root: meDir, WSKey: childLaterProfKey, Parent: &wsagent.ParentRef{Kind: wsagent.ParentConversation, ReplyID: leadWithPermID}, CreatedAt: nowStr,
	})

	// 9. Seed childLaterStore (open parented child at laterStoreDir, which is NOT
	// registered as a tracker store until AFTER cutover).
	childLaterStoreKey := mintKeyWithEnv(t, env, laterStoreDir, false)
	childLaterStoreID := wsmailbox.ReplyID(childLaterStoreKey)
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: childLaterStoreID, Profile: "tracker-child-writer", Backend: wsagent.BackendClaude,
		Root: laterStoreDir, WSKey: childLaterStoreKey, Parent: &wsagent.ParentRef{Kind: wsagent.ParentConversation, ReplyID: leadWithPermID}, CreatedAt: nowStr,
	})

	// 10. Seed legacy tracker-hooks.json + legacy tracker-hooks state file.
	hookLog := filepath.Join(t.TempDir(), "hook-events.log")
	hookScript := filepath.Join(t.TempDir(), "log-hook.py")
	writeLegacyFile(t, hookScript, []byte(fmt.Sprintf(
		"import sys\ndata = sys.stdin.read()\nwith open(%q, 'a', encoding='utf-8') as f:\n    f.write(data.strip() + '\\n')\n",
		hookLog,
	)))
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 required for hook test: %v", err)
	}
	hooksDoc := map[string]any{
		"hooks": []map[string]any{{
			"command": []string{pyBin, hookScript},
			"scope":   wstrackerhook.ScopeAll,
		}},
	}
	hooksBytes, _ := json.Marshal(hooksDoc)
	writeLegacyFile(t, filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName), append(hooksBytes, '\n'))

	// Pre-cutover sanity check: verify pinned oldBin DOES accept tracker.update
	// from supportKey and childAtStoreKey BEFORE cutover!
	preUpdate := func(key, text string) error {
		cmdStr := fmt.Sprintf(`<tk:store name="me"><tk:item.create kind="task" title="%s"/></tk:store>`, text)
		a, _ := json.Marshal(map[string]any{"session_key": key, "commands": cmdStr})
		_, _, err := runWSMCP(t, oldBin, env, repo, "call", "tracker.update", string(a))
		return err
	}
	if err := preUpdate(supportKey, "pre-cutover support write"); err != nil {
		t.Fatalf("pre-cutover supportKey should pass old gate: %v", err)
	}
	if err := preUpdate(childAtStoreKey, "pre-cutover child write"); err != nil {
		t.Fatalf("pre-cutover childAtStoreKey should pass old gate: %v", err)
	}

	// === STEP 1: CUTOVER WITH NEW DAEMON ===
	newD1 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion, "--port", daemonPort)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("=== newD1 log ===\n%s", newD1.out.String())
		}
	})
	newCookie1 := daemonCookie(t, newD1.ep)
	waitAgentState(t, newD1.ep, newCookie1, heldLeadID, 10*time.Second, func(s wsagent.State) bool {
		return s.Process
	})
	waitAgentState(t, newD1.ep, newCookie1, childHeldID, 10*time.Second, func(s wsagent.State) bool {
		return s.Process
	})

	// Confirm new daemon imported values cleanly.
	waitAgentState(t, newD1.ep, newCookie1, leadWithPermID, 5*time.Second, func(s wsagent.State) bool {
		return s.PermissionLevel == wsagent.PermissionWorkdirWrite
	})
	stCode, body = daemonJSON(t, newD1.ep, newCookie1, http.MethodGet, "/s/"+leadWithPermID+"/hiring", nil)
	if stCode != http.StatusOK || !strings.Contains(string(body), `"enabled":true`) || !strings.Contains(string(body), `"hired":true`) || !strings.Contains(string(body), `"req-keep-1"`) {
		t.Fatalf("new daemon imported hiring = (%d, %s)", stCode, body)
	}
	// Verify fences landed on disk.
	for _, id := range []string{leadWithPermID, leadNoPermID, workerNoPermID, heldLeadID} {
		raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir, id), "permission.json"))
		if err != nil || !strings.Contains(string(raw), wsmeta.PermissionCutoverFencedLevel) {
			t.Fatalf("session %s missing permission fence: %q (%v)", id, raw, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir, leadWithPermID), "hiring.json")); err != nil || !strings.Contains(string(raw), `"enabled":false`) || !strings.Contains(string(raw), `"hired":true`) || !strings.Contains(string(raw), `"req-keep-1"`) {
		t.Fatalf("hiring fence malformed: %q (%v)", raw, err)
	}
	if raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir, supportID), wsdashboard.AgentRecordFileName)); err != nil || !strings.Contains(string(raw), `"closed_at"`) {
		t.Fatalf("support record missing closed_at fence: %q (%v)", raw, err)
	}
	for _, tc := range []struct{ id, orig string }{
		{childAtStoreID, "tracker-child-writer"},
		{childLaterProfID, "plain-child"},
		{childLaterStoreID, "tracker-child-writer"},
		{childHeldID, "tracker-child-writer"},
	} {
		raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir, tc.id), wsdashboard.AgentRecordFileName))
		if err != nil {
			t.Fatalf("read fenced child %s: %v", tc.id, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if doc["profile"] != wsmeta.ChildCutoverFencedProfile || doc["cutover_profile"] != tc.orig {
			t.Fatalf("child %s fence = %v", tc.id, doc)
		}
		if ca, _ := doc["closed_at"].(string); ca != "" {
			t.Fatalf("child %s fence must keep closed_at empty, got %q", tc.id, ca)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy tracker-hooks.json should be removed after cutover: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, wsmeta.TrackerHooksWitnessName)); err != nil {
		t.Fatalf("tracker-hooks-v2.json missing after cutover: %v", err)
	}
	// Stop newD1 (live holders stay running!).
	newD1.stop(t)

	// === STEP 2: POST-CUTOVER PROFILE OVERRIDE + SENTINEL-NAMED PROFILE + NEW STORE ===
	writeConfigJSON(t, env, map[string]any{
		"schema_version": 1,
		"structured_overrides": map[string]any{
			wsagent.ProfilePrefix + "tracker-child-writer": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"tracker.query", "tracker.update"},
			},
			// Override plain-child to grant tracker.update post-cutover!
			wsagent.ProfilePrefix + "plain-child": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"tracker.query", "tracker.update"},
			},
			// Configure a profile literally named "!cutover-fenced" granting tracker.update!
			wsagent.ProfilePrefix + wsmeta.ChildCutoverFencedProfile: map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"tracker.query", "tracker.update"},
			},
			wsagent.ProfilePrefix + "worker-noperm": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"playbook.read"},
			},
		},
	})
	// Register a brand-new tracker store "later" AFTER cutover and point its
	// store dir at the pre-existing laterStoreDir so childLaterStore now sits at
	// a real registered store root!
	regLaterArgs, _ := json.Marshal(map[string]any{
		"session_key": topLeadKey,
		"commands":    `<tk:register store="later" member="ada" given="Ada"/>`,
	})
	if out, errOut, err := runWSMCP(t, newBin, env, repo, "call", "tracker.update", string(regLaterArgs)); err != nil {
		t.Fatalf("register post-cutover store later: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	relocateRegisteredStoreDir(t, env, "later", laterStoreDir)
	if gotLaterDir := findTrackerStoreDir(t, env, "later"); gotLaterDir != laterStoreDir {
		t.Fatalf("post-cutover store later dir = %q, want %q", gotLaterDir, laterStoreDir)
	}

	// === STEP 3: RUN PINNED OLD BINARY AGAINST FENCED STATE ===
	oldPost := startCutoverDaemon(t, oldBin, env, dir, pinnedPreCutoverVersion, "--port", daemonPort)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("=== oldPost log ===\n%s", oldPost.out.String())
			if dlog, err := os.ReadFile(filepath.Join(dir, "dashboard.log")); err == nil {
				t.Logf("=== dashboard.log ===\n%s", string(dlog))
			}
		}
	})
	oldCookie := daemonCookie(t, oldPost.ep)

	// 3a. Old daemon started cleanly with fenced children present AND rejoined live holders!
	waitAgentState(t, oldPost.ep, oldCookie, heldLeadID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process
	})
	waitAgentState(t, oldPost.ep, oldCookie, childHeldID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process
	})
	// Sending a turn to the ALREADY-HELD lead still works despite fenced permission.json!
	if code, resp := daemonJSON(t, oldPost.ep, oldCookie, http.MethodPost, "/agents/"+heldLeadID+"/send", map[string]any{"text": "turn on held lead"}); code != http.StatusOK {
		t.Fatalf("held lead rejoin send under old daemon failed: status=%d body=%s", code, resp)
	}

	// 3b. Fresh lead & worker launch refusal from fenced permission.json
	// (including originally absent permission.json on leadNoPerm and workerNoPerm!).
	// Confirm workerNoPerm ALREADY surfaces invalid permission_level from its
	// newly-fenced permission.json on load, and refuses fresh launch on /send.
	waitAgentState(t, oldPost.ep, oldCookie, workerNoPermID, 5*time.Second, func(s wsagent.State) bool {
		return !s.Process && strings.Contains(strings.ToLower(s.Error), "permission")
	})
	for _, tc := range []struct{ name, id string }{
		{"lead-with-perm", leadWithPermID},
		{"lead-absent-perm", leadNoPermID},
		{"worker-absent-perm", workerNoPermID},
	} {
		// Trigger launch attempt via /send; old daemon refuses launch with permission/profile fence error.
		code, resp := daemonJSON(t, oldPost.ep, oldCookie, http.MethodPost, "/agents/"+tc.id+"/send", map[string]any{"text": "try fresh launch"})
		if code == http.StatusOK {
			st := waitAgentState(t, oldPost.ep, oldCookie, tc.id, 10*time.Second, func(s wsagent.State) bool {
				return s.Error != "" && !s.Process
			})
			lowerErr := strings.ToLower(st.Error)
			if !strings.Contains(lowerErr, "permission") && !strings.Contains(st.Error, wsmeta.ChildCutoverFencedProfile) {
				t.Fatalf("%s: expected permission/fence refusal error, got %+v", tc.name, st)
			}
		} else {
			lowerResp := strings.ToLower(string(resp))
			if !strings.Contains(lowerResp, "permission") && !strings.Contains(string(resp), wsmeta.ChildCutoverFencedProfile) {
				t.Fatalf("%s: unexpected HTTP %d response %s", tc.name, code, resp)
			}
		}
	}

	// 3c. Hiring disabled on old daemon with hired identity & request_id preserved!
	stCode, body = daemonJSON(t, oldPost.ep, oldCookie, http.MethodGet, "/s/"+leadWithPermID+"/hiring", nil)
	if stCode != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) || !strings.Contains(string(body), `"hired":true`) || !strings.Contains(string(body), `"req-keep-1"`) {
		t.Fatalf("old daemon hiring view = (%d, %s), want enabled=false hired=true req-keep-1", stCode, body)
	}

	// 3d. Tracker writes REFUSED through pinned oldBin `ws-mcp call` for EVERY (2136) key!
	for _, tc := range []struct {
		name, key, store string
	}{
		{"retained-support-open-at-cutover", supportKey, "me"},
		{"child-open-at-cutover-at-store-root", childAtStoreKey, "me"},
		{"child-qualifying-via-profile-override-and-sentinel-profile", childLaterProfKey, "me"},
		{"child-qualifying-via-new-store-registration", childLaterStoreKey, "later"},
		{"fenced-child-after-holder-rejoin", childHeldKey, "me"},
	} {
		cmdStr := fmt.Sprintf(`<tk:store name="%s"><tk:item.create kind="task" title="smuggled-%s"/></tk:store>`, tc.store, tc.name)
		a, _ := json.Marshal(map[string]any{"session_key": tc.key, "commands": cmdStr})
		out, errOut, err := runWSMCP(t, oldBin, env, repo, "call", "tracker.update", string(a))
		if err == nil {
			t.Fatalf("%s: pinned oldBin MUST refuse tracker.update after cutover, succeeded with: %s", tc.name, out)
		}
		combined := out + "\n" + errOut
		if !strings.Contains(combined, "not available to a spawned session") && !strings.Contains(combined, "refused") {
			t.Fatalf("%s: unexpected error text: %s", tc.name, combined)
		}
	}

	// 3e. Hooks disabled on old daemon while legacy tracker-hooks.json is absent.
	hookLogBefore, _ := os.ReadFile(hookLog)
	writeWhileOldArgs, _ := json.Marshal(map[string]any{
		"session_key": topLeadKey,
		"commands":    fmt.Sprintf(`<tk:store name="me"><tk:comment.add item="%s">during-old-daemon</tk:comment.add></tk:store>`, meItemID),
	})
	if out, errOut, err := runWSMCP(t, oldBin, env, repo, "call", "tracker.update", string(writeWhileOldArgs)); err != nil {
		t.Fatalf("top-level lead tracker write during old daemon: %v\n%s\n%s", err, out, errOut)
	}
	time.Sleep(600 * time.Millisecond)
	if hookLogAfter, _ := os.ReadFile(hookLog); !bytes.Equal(hookLogAfter, hookLogBefore) {
		t.Fatalf("old daemon ran tracker hooks while legacy tracker-hooks.json was absent: %q", hookLogAfter)
	}

	// 3f. Old prune keeps fenced child directories after retention!
	triggerOldPrune(t, oldBin, env, repo)
	if _, err := os.Stat(wsdashboard.SessionDir(dir, childAtStoreID)); err != nil {
		t.Fatalf("old prune deleted fenced open child directory %s: %v", childAtStoreID, err)
	}

	oldPost.stop(t)

	// === STEP 4: RE-UPGRADE TO NEW DAEMON ===
	newD2 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion, "--port", daemonPort)
	newCookie2 := daemonCookie(t, newD2.ep)

	// Original profiles, open state, hiring, and permissions restored strictly from SQLite!
	for _, tc := range []struct{ id, wantProfile string }{
		{childAtStoreID, "tracker-child-writer"},
		{childLaterProfID, "plain-child"},
		{childLaterStoreID, "tracker-child-writer"},
		{childHeldID, "tracker-child-writer"},
		{supportID, wsagent.SupportProfileClaude},
	} {
		st := waitAgentState(t, newD2.ep, newCookie2, tc.id, 5*time.Second, func(s wsagent.State) bool {
			return s.Profile == tc.wantProfile && s.Status != wsagent.StatusClosed && s.ClosedAt == ""
		})
		if st.Profile != tc.wantProfile || st.Status == wsagent.StatusClosed || st.ClosedAt != "" {
			t.Fatalf("re-upgrade %s: got profile=%q status=%q closed_at=%q", tc.id, st.Profile, st.Status, st.ClosedAt)
		}
	}
	stCode, body = daemonJSON(t, newD2.ep, newCookie2, http.MethodGet, "/s/"+leadWithPermID+"/hiring", nil)
	if stCode != http.StatusOK || !strings.Contains(string(body), `"enabled":true`) {
		t.Fatalf("re-upgrade hiring = (%d, %s), want enabled:true", stCode, body)
	}
	waitAgentState(t, newD2.ep, newCookie2, leadWithPermID, 5*time.Second, func(s wsagent.State) bool {
		return s.PermissionLevel == wsagent.PermissionWorkdirWrite
	})

	// Verify new daemon runs tracker hooks from tracker-hooks-v2.json and preserves
	// progress across a reload of tracker-hooks-v2.json!
	fireArgs, _ := json.Marshal(map[string]any{
		"session_key": topLeadKey,
		"commands":    fmt.Sprintf(`<tk:store name="me"><tk:comment.add item="%s">post-reupgrade-hook-fire</tk:comment.add></tk:store>`, meItemID),
	})
	if out, errOut, err := runWSMCP(t, newBin, env, repo, "call", "tracker.update", string(fireArgs)); err != nil {
		t.Fatalf("post-reupgrade tracker.update: %v\n%s\n%s", err, out, errOut)
	}
	waitForFileContains(t, hookLog, "post-reupgrade-hook-fire", 15*time.Second)

	// Edit tracker-hooks-v2.json (change due_soon_hours) and wait for the 10s
	// fileCheckInterval reload, then confirm reload fires only the NEW event
	// without replaying the already-delivered event!
	dlogPath := filepath.Join(dir, "dashboard.log")
	dlogBefore, _ := os.ReadFile(dlogPath)
	loadsBefore := strings.Count(string(dlogBefore), "tracker-hooks-v2.json loaded")
	hooksDoc2 := map[string]any{
		"due_soon_hours": 12,
		"hooks": []map[string]any{{
			"command": []string{pyBin, hookScript},
			"scope":   wstrackerhook.ScopeAll,
		}},
	}
	hooksBytes2, _ := json.Marshal(hooksDoc2)
	writeLegacyFile(t, filepath.Join(dir, wsmeta.TrackerHooksWitnessName), append(hooksBytes2, '\n'))
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); {
		if raw, err := os.ReadFile(dlogPath); err == nil && strings.Count(string(raw), "tracker-hooks-v2.json loaded") > loadsBefore {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if raw, _ := os.ReadFile(dlogPath); strings.Count(string(raw), "tracker-hooks-v2.json loaded") <= loadsBefore {
		t.Fatalf("timed out waiting for tracker-hooks-v2.json reload (loadsBefore=%d)", loadsBefore)
	}
	fireArgs2, _ := json.Marshal(map[string]any{
		"session_key": topLeadKey,
		"commands":    fmt.Sprintf(`<tk:store name="me"><tk:comment.add item="%s">post-reload-hook-fire</tk:comment.add></tk:store>`, meItemID),
	})
	if out, errOut, err := runWSMCP(t, newBin, env, repo, "call", "tracker.update", string(fireArgs2)); err != nil {
		t.Fatalf("post-reload tracker.update: %v\n%s\n%s", err, out, errOut)
	}
	waitForFileContains(t, hookLog, "post-reload-hook-fire", 15*time.Second)
	rawLog, _ := os.ReadFile(hookLog)
	if strings.Count(string(rawLog), "post-reupgrade-hook-fire") != 1 {
		t.Fatalf("hook reload replayed earlier event (progress not preserved):\n%s", rawLog)
	}
	newD2.stop(t)
}

func findTrackerStoreDir(t *testing.T, env []string, storeID string) string {
	t.Helper()
	// Check both WS_CONFIG_HOME/tracker and WS_CACHE_HOME/tracker for registry.json.
	candidates := []string{
		filepath.Join(envValue(t, env, "WS_CONFIG_HOME"), "tracker", "stores.json"),
		filepath.Join(envValue(t, env, "WS_CONFIG_HOME"), "tracker", "registry.json"),
		filepath.Join(envValue(t, env, "WS_CACHE_HOME"), "tracker", "stores.json"),
		filepath.Join(envValue(t, env, "WS_CACHE_HOME"), "tracker", "registry.json"),
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var doc struct {
			Stores []struct {
				ID  string `json:"id"`
				Dir string `json:"dir"`
			} `json:"stores"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			for _, s := range doc.Stores {
				if s.ID == storeID && s.Dir != "" {
					if real, err := filepath.EvalSymlinks(s.Dir); err == nil {
						return real
					}
					return s.Dir
				}
			}
		}
	}
	// Fallback: walk WS_CONFIG_HOME and WS_CACHE_HOME for stores.json/registry.json.
	for _, root := range []string{envValue(t, env, "WS_CONFIG_HOME"), envValue(t, env, "WS_CACHE_HOME")} {
		var found string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil || !bytes.Contains(raw, []byte(`"`+storeID+`"`)) {
				return nil
			}
			var doc struct {
				Stores []struct {
					ID  string `json:"id"`
					Dir string `json:"dir"`
				} `json:"stores"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				for _, s := range doc.Stores {
					if s.ID == storeID && s.Dir != "" {
						found = s.Dir
						return fs.SkipAll
					}
				}
			}
			return nil
		})
		if found != "" {
			if real, err := filepath.EvalSymlinks(found); err == nil {
				return real
			}
			return found
		}
	}
	t.Fatalf("could not locate tracker store %q directory", storeID)
	return ""
}

func relocateRegisteredStoreDir(t *testing.T, env []string, storeID, destDir string) {
	t.Helper()
	regPath := filepath.Join(envValue(t, env, "WS_CONFIG_HOME"), "tracker", "stores.json")
	raw, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	stores, _ := doc["stores"].([]any)
	moved := false
	for _, entry := range stores {
		m, _ := entry.(map[string]any)
		if m["id"] != storeID {
			continue
		}
		srcDir, _ := m["dir"].(string)
		if srcDir == "" {
			t.Fatalf("store %q has empty dir in %s", storeID, regPath)
		}
		if err := os.RemoveAll(destDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(srcDir, destDir); err != nil {
			t.Fatalf("relocate store %q %s -> %s: %v", storeID, srcDir, destDir, err)
		}
		m["dir"] = destDir
		moved = true
		break
	}
	if !moved {
		t.Fatalf("store %q not found in %s", storeID, regPath)
	}
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForFileContains(t *testing.T, path, substr string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), substr) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	raw, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %s to contain %q; current content:\n%s", path, substr, raw)
}

// TestMetaCutoverZeroLegacyWritesAndReupgradeFromSQLite verifies that after
// cutover, mutations across bundled stores and daemon shutdown produce ZERO
// writes to legacy bundled files, and that subsequent edits/deletions/additions
// of legacy bundled files are completely ignored on re-upgrade while shared
// contracts remain preserved.
func TestMetaCutoverZeroLegacyWritesAndReupgradeFromSQLite(t *testing.T) {
	_, newBin := cutoverTestBins(t)
	env, dir := envWithManifest(t, true)
	env = writeFakeClaudeOnPath(t, env)
	repo := initCutoverGitRepo(t, filepath.Join(t.TempDir(), "repo"))
	leadKey := mintKeyWithEnv(t, env, repo, true)
	leadID := wsmailbox.ReplyID(leadKey)
	nowStr := time.Now().UTC().Format(time.RFC3339)

	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: leadID, Project: repo, Harness: wsagent.BackendClaude,
		Hosted: true, RegisteredAt: nowStr, HeartbeatAt: nowStr,
	}); err != nil {
		t.Fatal(err)
	}
	leadDir := wsdashboard.SessionDir(dir, leadID)
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: leadID, Profile: wsagent.LeadProfileClaude, Backend: wsagent.BackendClaude,
		Root: repo, WSKey: leadKey, Lead: true, CreatedAt: nowStr,
	})
	writeLegacyFile(t, filepath.Join(leadDir, "permission.json"), []byte(`{"permission_level":"workdir-write"}`+"\n"))
	writeLegacyFile(t, filepath.Join(leadDir, "label.json"), []byte(`{"label":"Before Cutover"}`+"\n"))
	writeLegacyFile(t, filepath.Join(leadDir, "hiring.json"), []byte(`{"enabled":true}`+"\n"))
	writeLegacyFile(t, filepath.Join(dir, "rail-layout.json"), []byte(`{"schema_version":1,"workspace_order":[],"workspaces":{}}`+"\n"))
	writeLegacyFile(t, filepath.Join(dir, "support.json"), []byte(`{"schema_version":1,"selection":{"harness":"claude","model":"sonnet","effort":"medium"},"stores":{}}`+"\n"))
	// Shared contracts that must survive untouched (snapshot/ring on a terminal
	// session whose live writer isn't owned by the runner, plus root/session sidecars).
	const termID = "sharedcontractterm01"
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: termID, Project: repo, Harness: wsagent.BackendClaude,
		RegisteredAt: nowStr, HeartbeatAt: nowStr,
	}); err != nil {
		t.Fatal(err)
	}
	termDir := wsdashboard.SessionDir(dir, termID)
	sharedContracts := map[string][]byte{
		filepath.Join(termDir, "snapshot.json"):        []byte(`{"schema":1,"shared":"snap"}` + "\n"),
		filepath.Join(termDir, "ring", "000001.jsonl"): []byte(`{"k":"shared-ring"}` + "\n"),
		filepath.Join(dir, "hire-proposals.json"):      []byte(`[]` + "\n"),
		filepath.Join(leadDir, "visibility.json"):      []byte(`{"hidden":false}` + "\n"),
	}
	for p, b := range sharedContracts {
		writeLegacyFile(t, p, b)
	}

	// Start new daemon -> completes cutover.
	d1 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion)
	cookie := daemonCookie(t, d1.ep)

	// Record exact state of every bundled legacy file right AFTER cutover!
	type fileSnap struct {
		exists bool
		mtime  time.Time
		data   []byte
	}
	snapPath := func(p string) fileSnap {
		info, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			return fileSnap{}
		}
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return fileSnap{exists: true, mtime: info.ModTime(), data: data}
	}
	legacyPaths := []string{
		filepath.Join(leadDir, wsdashboard.AgentRecordFileName),
		filepath.Join(leadDir, "permission.json"),
		filepath.Join(leadDir, "label.json"),
		filepath.Join(leadDir, "hiring.json"),
		filepath.Join(leadDir, "ack-check.json"),
		filepath.Join(dir, "rail-layout.json"),
		filepath.Join(dir, "support.json"),
		filepath.Join(dir, "plan-usage.json"),
		filepath.Join(dir, "plan-usage-history.json"),
		filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName),
	}
	before := map[string]fileSnap{}
	for _, p := range legacyPaths {
		before[p] = snapPath(p)
	}

	// Exercise post-cutover mutations across bundled stores!
	if code, body := daemonJSON(t, d1.ep, cookie, http.MethodPost, "/s/"+leadID+"/label", map[string]any{"label": "SQLite Label"}); code != http.StatusOK {
		t.Fatalf("POST label: %d %s", code, body)
	}
	if code, body := daemonJSON(t, d1.ep, cookie, http.MethodPost, "/s/"+leadID+"/hiring", map[string]any{"enabled": false}); code != http.StatusOK {
		t.Fatalf("POST hiring: %d %s", code, body)
	}
	if code, body := daemonJSON(t, d1.ep, cookie, http.MethodPost, "/rail/layout", map[string]any{"op": "star", "rid": leadID}); code != http.StatusOK {
		t.Fatalf("POST rail/layout star: %d %s", code, body)
	}
	if code, body := daemonJSON(t, d1.ep, cookie, http.MethodPost, "/rail/memos", map[string]any{"workspace": repo, "action": "add", "text": "SQLite memo"}); code != http.StatusOK {
		t.Fatalf("POST rail/memos: %d %s", code, body)
	}
	if code, body := daemonJSON(t, d1.ep, cookie, http.MethodPost, "/agents/"+leadID+"/settings/permission", map[string]any{"permission_level": "read-only"}); code != http.StatusOK {
		t.Fatalf("POST permission: %d %s", code, body)
	}
	// Send a real turn on leadID so its holder runs & advances holder cursor without touching agent.json!
	if code, body := daemonJSON(t, d1.ep, cookie, http.MethodPost, "/agents/"+leadID+"/send", map[string]any{"text": "advance holder cursor"}); code != http.StatusOK {
		t.Fatalf("POST send: %d %s", code, body)
	}
	waitHeldTurnDone(t, d1.ep, cookie, dir, leadID, 15*time.Second)
	d1.stop(t)

	// Assert ZERO legacy bundled publications during mutations or shutdown!
	for _, p := range legacyPaths {
		after := snapPath(p)
		if after.exists != before[p].exists || !bytes.Equal(after.data, before[p].data) || !after.mtime.Equal(before[p].mtime) {
			t.Fatalf("legacy bundled file %s changed post-cutover: before=%+v after=%+v", p, before[p], after)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "memos")); err == nil && len(entries) > 0 {
		t.Fatalf("post-cutover memo wrote legacy files under memos/: %v", entries)
	}

	// Mutate legacy bundled files externally: edit existing, delete existing, add new bundled file!
	writeLegacyFile(t, filepath.Join(leadDir, "label.json"), []byte(`{"label":"Rogue Legacy Edit"}`+"\n"))
	_ = os.Remove(filepath.Join(dir, "rail-layout.json"))
	writeLegacyFile(t, filepath.Join(dir, "support.json"), []byte(`{"schema_version":1,"selection":{"harness":"codex","model":"bad","effort":"low"},"stores":{}}`+"\n"))
	const rogueID = "roguelegacy01"
	writeLegacyFile(t, filepath.Join(wsdashboard.SessionDir(dir, rogueID), "label.json"), []byte(`{"label":"Rogue Added"}`+"\n"))

	// Re-upgrade (restart new daemon): must restore strictly from SQLite alone!
	d2 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion)
	cookie2 := daemonCookie(t, d2.ep)
	waitAgentState(t, d2.ep, cookie2, leadID, 10*time.Second, func(s wsagent.State) bool {
		return s.PermissionLevel == wsagent.PermissionReadOnly && s.Process
	})
	if code, body := daemonJSON(t, d2.ep, cookie2, http.MethodGet, "/rail/memos?workspace="+url.QueryEscape(repo), nil); code != http.StatusOK || !strings.Contains(string(body), "SQLite memo") {
		t.Fatalf("re-upgrade memos = (%d, %s), want SQLite memo", code, body)
	}
	if code, body := daemonJSON(t, d2.ep, cookie2, http.MethodGet, "/s/"+leadID+"/hiring", nil); code != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("re-upgrade hiring = (%d, %s), want enabled:false", code, body)
	}
	d2.stop(t)

	// Verify SQLite snapshot has SQLite Label (not Rogue Legacy Edit) and no rogueID.
	verifyStore, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer verifyStore.Close()
	snap, err := verifyStore.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Labels[leadID] != "SQLite Label" {
		t.Fatalf("SQLite label = %q, want SQLite Label", snap.Labels[leadID])
	}
	if _, ok := snap.Labels[rogueID]; ok {
		t.Fatalf("rogue legacy file %s was re-imported after cutover", rogueID)
	}
	// Shared contracts must remain preserved.
	for p, want := range sharedContracts {
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("shared contract %s modified: got=%q want=%q err=%v", p, got, want, err)
		}
	}
}

// TestMetaCutoverDirectCallsStdioBarrierAndRecovery verifies:
//   - (1986)/(2121) authenticated direct ws-mcp call & serve --stdio with daemon
//     available and unavailable;
//   - (2122) prestarted successor (including Support) reads metadata only after
//     acquiring dashboard.lock while predecessor drains and closes SQLite before unlock;
//   - (1985) schema/error protection (newer schema_version refused) and the
//     documented offline SQLite recovery path (stop -> inspect/repair -> restart).
func TestMetaCutoverDirectCallsStdioBarrierAndRecovery(t *testing.T) {
	_, newBin := cutoverTestBins(t)
	env, dir := envWithManifest(t, true)
	repo := initCutoverGitRepo(t, filepath.Join(t.TempDir(), "repo"))
	topLeadKey := mintKeyWithEnv(t, env, repo, true)

	// Register tracker store "me" and seed a support conversation.
	regArgs, _ := json.Marshal(map[string]any{"session_key": topLeadKey, "commands": `<tk:register store="me" member="ada" given="Ada"/>`})
	if out, errOut, err := runWSMCP(t, newBin, env, repo, "call", "tracker.update", string(regArgs)); err != nil {
		t.Fatalf("register store me: %v\n%s\n%s", err, out, errOut)
	}
	meDir := findTrackerStoreDir(t, env, "me")
	supportKey := mintKeyWithEnv(t, env, meDir, false)
	supportID := wsmailbox.ReplyID(supportKey)
	nowStr := time.Now().UTC().Format(time.RFC3339)
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: supportID, Profile: wsagent.SupportProfileClaude, Backend: wsagent.BackendClaude,
		Root: meDir, WSKey: supportKey, CreatedAt: nowStr,
	})

	// 1. Direct ws-mcp call WITHOUT a running daemon MUST fail naming daemon-start (2121).
	qArgs, _ := json.Marshal(map[string]any{"session_key": supportKey})
	out, errOut, err := runWSMCP(t, newBin, env, repo, "call", "tracker.query", string(qArgs))
	if err == nil {
		t.Fatalf("direct ws-mcp call with support key should fail without daemon, got: %s", out)
	}
	if !strings.Contains(out+errOut, "ws-mcp daemon-start") {
		t.Fatalf("direct call without daemon did not name ws-mcp daemon-start:\nstdout=%s\nstderr=%s", out, errOut)
	}

	// 2. Direct serve --stdio with dashboard disabled MUST return a recoverable
	// unavailable error (never fall back to reading legacy agent.json).
	offEnv := setEnvKey(env, "WS_MCP_CONFIG_MANIFESTS", writeProxyTestManifest(t, false))
	stdioIn := fmt.Sprintf(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"tracker.query\",\"arguments\":{\"session_key\":%q}}}\n",
		supportKey,
	)
	stdioCmd := exec.Command(newBin, "serve", "--stdio", "--root", repo)
	stdioCmd.Env = offEnv
	stdioCmd.Stdin = strings.NewReader(stdioIn)
	stdioOut, stdioErr := stdioCmd.CombinedOutput()
	if stdioErr != nil {
		t.Fatalf("serve --stdio exited with %v:\n%s", stdioErr, stdioOut)
	}
	if !strings.Contains(string(stdioOut), `"error"`) || !strings.Contains(string(stdioOut), "recoverable") || !strings.Contains(string(stdioOut), "ws-mcp daemon-start") {
		t.Fatalf("serve --stdio without daemon should return recoverable error, got:\n%s", stdioOut)
	}

	// 3. Start new daemon -> authenticated direct ws-mcp call succeeds via /meta/agent-record!
	d1 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion)
	out, errOut, err = runWSMCP(t, newBin, env, repo, "call", "tracker.query", string(qArgs))
	if err != nil || !strings.Contains(out, "me") {
		t.Fatalf("authenticated direct call with daemon running failed: err=%v stdout=%s stderr=%s", err, out, errOut)
	}

	// 4. Prestarted successor (including Support) blocks on dashboard.lock until
	// predecessor mutates Support state, drains SQLite users, and closes metadata.sqlite
	// before unlock (2122).
	cookie1 := daemonCookie(t, d1.ep)
	if code, body := daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/support/stores/me/select", map[string]any{
		"harness": wsagent.BackendClaude, "model": "haiku", "effort": "low",
	}); code != http.StatusOK {
		t.Fatalf("predecessor support select failed: %d %s", code, body)
	}
	succCmd := exec.Command(newBin, "dashboard", "--port", "0", "--lock-wait", "10s")
	succCmd.Env = env
	var succOut bytes.Buffer
	succCmd.Stdout = &succOut
	succCmd.Stderr = &succOut
	if err := succCmd.Start(); err != nil {
		t.Fatalf("start prestarted successor: %v", err)
	}
	t.Cleanup(func() {
		if succCmd.ProcessState == nil && succCmd.Process != nil {
			_ = succCmd.Process.Kill()
			_, _ = succCmd.Process.Wait()
		}
	})
	// Give successor time to block on dashboard.lock, then update Support once more
	// right before shutting down predecessor so successor MUST observe the final write!
	time.Sleep(150 * time.Millisecond)
	if code, body := daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/support/stores/me/select", map[string]any{
		"harness": wsagent.BackendClaude, "model": "opus", "effort": "high",
	}); code != http.StatusOK {
		t.Fatalf("predecessor final support select failed: %d %s", code, body)
	}
	d1.stop(t)
	// Successor must now acquire the lock cleanly without SQLITE_BUSY and see model="opus"!
	var succEP wsdashboard.Endpoint
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		if ep, err := wsdashboard.ReadEndpoint(dir); err == nil && ep.PID == succCmd.Process.Pid {
			if _, err := wsdashboard.Health(context.Background(), dashboardTestClient, ep); err == nil {
				succEP = ep
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if succEP.PID != succCmd.Process.Pid {
		t.Fatalf("prestarted successor failed to take over after lock release:\n%s", succOut.String())
	}
	succCookie := daemonCookie(t, succEP)
	code, body := daemonJSON(t, succEP, succCookie, http.MethodGet, "/support/stores/me", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"opus"`) {
		t.Fatalf("prestarted successor Support state = (%d, %s), want opus", code, body)
	}
	if err := wsdashboard.RequestShutdown(context.Background(), dashboardTestClient, succEP); err != nil {
		t.Fatal(err)
	}
	if err := waitExit(t, succCmd, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// 5. Schema protection (newer schema_version refuses start) + documented offline
	// SQLite recovery path (1985): stop daemon -> inspect/repair metadata.sqlite -> restart.
	dbPath := filepath.Join(dir, wsmeta.DBName)
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`UPDATE meta SET value = '999' WHERE key = 'schema_version'`); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	_ = rawDB.Close()

	refused := exec.Command(newBin, "dashboard", "--port", "0", "--lock-wait", "2s")
	refused.Env = env
	refusedOut, refusedErr := refused.CombinedOutput()
	if refusedErr == nil {
		t.Fatalf("daemon started against newer schema_version=999:\n%s", refusedOut)
	}
	if !strings.Contains(string(refusedOut), "newer schema") {
		t.Fatalf("expected newer schema refusal, got:\n%s", refusedOut)
	}

	// Offline recovery: run PRAGMA integrity_check, repair schema_version & insert a repaired label.
	rawDB, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := rawDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		rawDB.Close()
		t.Fatalf("PRAGMA integrity_check = (%q, %v), want ok", integrity, err)
	}
	if _, err := rawDB.Exec(`UPDATE meta SET value = '1' WHERE key = 'schema_version'`); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`INSERT INTO labels(reply_id, label, body_json) VALUES(?, ?, ?) ON CONFLICT(reply_id) DO UPDATE SET label = excluded.label, body_json = excluded.body_json`, supportID, "Recovered Offline", `{"label":"Recovered Offline"}`); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	_ = rawDB.Close()

	dRecovered := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion)
	dRecovered.stop(t)
	checkStore, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer checkStore.Close()
	snap, err := checkStore.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Labels[supportID] != "Recovered Offline" {
		t.Fatalf("offline recovery label = %q, want Recovered Offline", snap.Labels[supportID])
	}
}

// TestMetaCutoverMixedVersionPruneReproduction reproduces the documented (2125)
// same-root downgrade residual: when a session that was closed and expired in
// legacy agent.json is revived in SQLite after cutover, a pinned pre-cutover
// binary's PruneRegistrations still reads the stale expired agent.json on disk
// and deletes the session directory.
func TestMetaCutoverMixedVersionPruneReproduction(t *testing.T) {
	oldBin, newBin := cutoverTestBins(t)
	env, dir := envWithManifest(t, true)
	repo := initCutoverGitRepo(t, filepath.Join(t.TempDir(), "repo"))
	revivedKey := mintKeyWithEnv(t, env, repo, true)
	revivedID := wsmailbox.ReplyID(revivedKey)
	oldStamp := time.Now().Add(-wsdashboard.RegistryRetention - 48*time.Hour).UTC().Format(time.RFC3339)

	// Seed a closed, retention-expired hosted session before cutover.
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: revivedID, Project: repo, Harness: wsagent.BackendClaude,
		Hosted: true, RegisteredAt: oldStamp, HeartbeatAt: oldStamp, EndedAt: oldStamp,
	}); err != nil {
		t.Fatal(err)
	}
	writeLegacyAgentRecord(t, dir, wsagent.Record{
		ReplyID: revivedID, Profile: wsagent.LeadProfileClaude, Backend: wsagent.BackendClaude,
		Root: repo, WSKey: revivedKey, Lead: true, CreatedAt: oldStamp, ClosedAt: oldStamp,
	})

	// Cut over to SQLite, then revive the session in SQLite (POST /agents/<id>/reopen)
	// while legacy on-disk agent.json remains untouched with stale expired closed_at.
	d := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion)
	cookie := daemonCookie(t, d.ep)
	if code, body := daemonJSON(t, d.ep, cookie, http.MethodPost, "/agents/"+revivedID+"/reopen", map[string]any{}); code != http.StatusOK {
		t.Fatalf("reopen in SQLite failed: %d %s", code, body)
	}
	waitAgentState(t, d.ep, cookie, revivedID, 5*time.Second, func(s wsagent.State) bool {
		return s.Status != wsagent.StatusClosed && s.ClosedAt == ""
	})
	d.stop(t)

	// Confirm legacy on-disk agent.json still carries the stale expired closed_at.
	if raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir, revivedID), wsdashboard.AgentRecordFileName)); err != nil || !strings.Contains(string(raw), oldStamp) {
		t.Fatalf("expected stale closed_at on disk, got %q (%v)", raw, err)
	}

	// Re-age registration.json (reopen refreshed it on disk) to simulate a
	// revived session whose on-disk registration also fell past retention while
	// SQLite keeps it open, OR verify the exact condition under which old prune
	// deletes it. Wait: old PruneRegistrations checks BOTH LastSeen(reg) and
	// agentRecordEnd(agent.json). Both expired -> old binary prunes it even
	// though SQLite has the session open (closed_at == "").
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		SchemaVersion: 1, ReplyID: revivedID, Project: repo, Harness: wsagent.BackendClaude,
		Hosted: true, RegisteredAt: oldStamp, HeartbeatAt: oldStamp, EndedAt: oldStamp,
	}); err != nil {
		t.Fatal(err)
	}
	// Run pinned oldBin -> its PruneRegistrations reads stale agent.json and deletes the directory!
	triggerOldPrune(t, oldBin, env, repo)
	if _, err := os.Stat(wsdashboard.SessionDir(dir, revivedID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected mixed-version old prune to delete %s based on stale agent.json, stat err=%v", revivedID, err)
	}
}

// TestMetaCutoverHardKillRejoinHeldLeadAndWorker verifies (2122) hard-kill/rejoin
// and graceful replacement for BOTH a held lead and a held worker: holder cursor
// deltas persist in SQLite without creating or rewriting legacy agent.json, and
// a successor daemon after SIGKILL cleanly rejoins both live holders and finishes
// an in-flight turn plus queued follow-up input.
func TestMetaCutoverHardKillRejoinHeldLeadAndWorker(t *testing.T) {
	_, newBin := cutoverTestBins(t)
	env, dir := envWithManifest(t, true)
	env = writeFakeClaudeOnPath(t, env)
	repo := initCutoverGitRepo(t, filepath.Join(t.TempDir(), "repo"))
	writeConfigJSON(t, env, map[string]any{
		"schema_version": 1,
		"structured_overrides": map[string]any{
			wsagent.ProfilePrefix + "held-worker": map[string]any{
				"backend":  wsagent.BackendClaude,
				"ws_tools": []string{"playbook.read"},
			},
		},
	})
	daemonPort := strconv.Itoa(freeLoopbackPort(t))

	d1 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion, "--port", daemonPort)
	cookie1 := daemonCookie(t, d1.ep)

	// 1. Create a held lead and complete one turn.
	stCode, body := daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/agents/leads/permission", map[string]any{
		"root":             repo,
		"harness":          wsagent.BackendClaude,
		"permission_level": "workdir-write",
	})
	if stCode != http.StatusCreated {
		t.Fatalf("create lead: %d %s", stCode, body)
	}
	var leadSt wsagent.State
	if err := json.Unmarshal(body, &leadSt); err != nil {
		t.Fatal(err)
	}
	leadID := leadSt.ID
	if code, resp := daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/agents/"+leadID+"/send", map[string]any{"text": "lead turn 1"}); code != http.StatusOK {
		t.Fatalf("lead send 1: %d %s", code, resp)
	}
	waitHeldTurnDone(t, d1.ep, cookie1, dir, leadID, 15*time.Second)

	// 2. Create a held worker and start a gated mid-turn plus a queued follow-up input.
	gateFile := filepath.Join(t.TempDir(), "worker-release-gate")
	t.Cleanup(func() { _ = os.WriteFile(gateFile, []byte("go\n"), 0o600) })
	stCode, body = daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/agents", map[string]any{
		"profile": "held-worker",
		"root":    repo,
	})
	if stCode != http.StatusCreated {
		t.Fatalf("create worker: %d %s", stCode, body)
	}
	var workerSt wsagent.State
	if err := json.Unmarshal(body, &workerSt); err != nil {
		t.Fatal(err)
	}
	workerID := workerSt.ID
	if code, resp := daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/agents/"+workerID+"/send", map[string]any{"text": "WAIT_UNTIL_FILE:" + gateFile}); code != http.StatusOK {
		t.Fatalf("worker send 1: %d %s", code, resp)
	}
	waitAgentState(t, d1.ep, cookie1, workerID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process && s.Status == wsagent.StatusRunning
	})
	// Queue a second input while the first turn is still running.
	if code, resp := daemonJSON(t, d1.ep, cookie1, http.MethodPost, "/agents/"+workerID+"/send", map[string]any{"text": "follow-up after hard kill"}); code != http.StatusOK {
		t.Fatalf("worker queued send: %d %s", code, resp)
	}

	// Verify NO legacy agent.json was written for either post-cutover session.
	for _, id := range []string{leadID, workerID} {
		if _, err := os.Stat(filepath.Join(wsdashboard.SessionDir(dir, id), wsdashboard.AgentRecordFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("post-cutover session %s unexpectedly created legacy agent.json: %v", id, err)
		}
	}

	// 3. Hard-kill d1 (SIGKILL — no graceful shutdown/drain) so the live holders
	// keep running across an abrupt daemon crash!
	if err := d1.cmd.Process.Kill(); err != nil {
		t.Fatalf("hard-kill d1: %v", err)
	}
	_, _ = d1.cmd.Process.Wait()

	// 4. Start successor d2 on the same port: must recover SQLite WAL, rejoin BOTH
	// held lead and held worker, finish the gated turn + queued follow-up, and
	// still never write legacy agent.json.
	d2 := startCutoverDaemon(t, newBin, env, dir, postCutoverVersion, "--port", daemonPort)
	cookie2 := daemonCookie(t, d2.ep)
	waitAgentState(t, d2.ep, cookie2, leadID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process
	})
	waitAgentState(t, d2.ep, cookie2, workerID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process && s.Status == wsagent.StatusRunning
	})
	// Release the worker's turn gate; both turn 1 and queued follow-up must complete!
	if err := os.WriteFile(gateFile, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitAgentState(t, d2.ep, cookie2, workerID, 15*time.Second, func(s wsagent.State) bool {
		return s.Process && s.Status == wsagent.StatusIdle && s.Held == 0
	})
	// Send another turn to the rejoined lead to confirm interactive continuity.
	if code, resp := daemonJSON(t, d2.ep, cookie2, http.MethodPost, "/agents/"+leadID+"/send", map[string]any{"text": "lead turn 2 after kill"}); code != http.StatusOK {
		t.Fatalf("lead send 2 after hard kill: %d %s", code, resp)
	}
	waitHeldTurnDone(t, d2.ep, cookie2, dir, leadID, 15*time.Second)

	// Graceful stop of d2 -> verify SQLite recorded holder cursor progress and
	// zero legacy agent.json files were written.
	d2.stop(t)
	for _, id := range []string{leadID, workerID} {
		if _, err := os.Stat(filepath.Join(wsdashboard.SessionDir(dir, id), wsdashboard.AgentRecordFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("after hard-kill/rejoin session %s created legacy agent.json: %v", id, err)
		}
	}
	verifyStore, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer verifyStore.Close()
	snap, err := verifyStore.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{leadID, workerID} {
		rec, ok := snap.Records[id]
		if !ok {
			t.Fatalf("SQLite missing record for %s after hard-kill/rejoin", id)
		}
		maxCur := uint64(0)
		for _, seq := range rec.HolderCursors {
			if seq > maxCur {
				maxCur = seq
			}
		}
		if maxCur == 0 {
			t.Fatalf("session %s holder cursor did not advance in SQLite: %+v", id, rec.HolderCursors)
		}
	}
}

var _ = wstrackerhook.WitnessFileName
var _ = wsstate.EnvCacheHome
