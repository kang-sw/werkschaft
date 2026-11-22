package wsagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// claudeFixtureInits reads the trimmed system/init lines of a fixture,
// skipping its comment lines.
func claudeFixtureInits(t *testing.T, name string) []claudeInit {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []claudeInit
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var init claudeInit
		if err := json.Unmarshal([]byte(line), &init); err != nil {
			t.Fatal(err)
		}
		out = append(out, init)
	}
	return out
}

// The exec cutover removes exactly these Claude tools, and the recorded
// 2.1.330 init stream shows the removal: the shell, its monitor and task
// stop are gone (Glob and Grep come in), and nothing else is lost.
func TestClaudeExecCutoverRemovesTheNativeProcessTools(t *testing.T) {
	if !slices.Equal(claudeNativeProcessTools, []string{"Bash", "PowerShell", "Monitor", "TaskStop", "BashOutput", "KillShell"}) {
		t.Fatalf("claudeNativeProcessTools = %v", claudeNativeProcessTools)
	}
	inits := claudeFixtureInits(t, "claude-2.1.330-native-process-tools.jsonl")
	if len(inits) != 2 {
		t.Fatalf("fixture inits = %d", len(inits))
	}
	base, cut := inits[0].Tools, inits[1].Tools
	for _, name := range []string{"Bash", "Monitor", "TaskStop"} {
		if !slices.Contains(base, name) {
			t.Fatalf("the baseline init lacks %s: %v", name, base)
		}
	}
	for _, name := range claudeNativeProcessTools {
		if slices.Contains(cut, name) {
			t.Fatalf("the cutover init still lists %s", name)
		}
	}
	for _, name := range base {
		if !slices.Contains(claudeNativeProcessTools, name) && !slices.Contains(cut, name) {
			t.Fatalf("the cutover init lost %s, which it does not remove", name)
		}
	}
	for _, name := range []string{"Read", "Edit", "Write", "Glob", "Grep", "WebSearch"} {
		if !slices.Contains(cut, name) {
			t.Fatalf("the cutover init misses preserved tool %s", name)
		}
	}

	lead := StartSpec{Root: t.TempDir(), Dir: t.TempDir(), Lead: &LeadSpec{PermissionMode: "default"}, RemoveSubagents: true,
		WSMCP: &MCPCommand{Command: "/bin/ws-mcp"}}
	args, err := claudeArgs(lead)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := flagValue(args, "--disallowedTools"); got != "Agent,Task" {
		t.Fatalf("a lead outside the cutover removes %q", got)
	}
	lead.ExecCutover = true
	if args, err = claudeArgs(lead); err != nil {
		t.Fatal(err)
	}
	if got, _ := flagValue(args, "--disallowedTools"); got != "Agent,Task,Bash,PowerShell,Monitor,TaskStop,BashOutput,KillShell" {
		t.Fatalf("a cutover lead removes %q", got)
	}

	// A worker's init check accepts the cutover init's file tools.
	worker := StartSpec{Root: t.TempDir(), Profile: builtinProfile(t, AgentWorkerProfile(BackendClaude)), WSMCP: &MCPCommand{Command: "/bin/ws-mcp"},
		PermissionLevel: PermissionReadOnly, RemoveSubagents: true, ExecCutover: true}
	if args, err = claudeArgs(worker); err != nil {
		t.Fatal(err)
	}
	if got, _ := flagValue(args, "--disallowedTools"); !strings.Contains(got, "Bash,PowerShell,Monitor,TaskStop") {
		t.Fatalf("a cutover worker removes %q", got)
	}
	var init claudeInit
	if err := json.Unmarshal([]byte(`{"permissionMode":"dontAsk","mcp_servers":[{"name":"ws"}]}`), &init); err != nil {
		t.Fatal(err)
	}
	init.Tools = []string{"Read", "Edit", "Write", "Glob", "Grep", claudeMCPToolName(ReservedMCPServer, "exec.shell")}
	if err := checkClaudeInit(init, worker); err != nil {
		t.Fatalf("cutover worker init: %v", err)
	}
	init.Tools = append(init.Tools, "Bash")
	if err := checkClaudeInit(init, worker); err == nil {
		t.Fatal("a cutover worker whose init still lists Bash was admitted")
	}
	// Read-only workers run under dontAsk: the exec tools must be allowed,
	// or the removed shell has no replacement.
	rules := claudeAllowedRules(worker)
	for _, name := range ExecWSTools {
		if !slices.Contains(rules, claudeMCPToolName(ReservedMCPServer, name)) {
			t.Fatalf("read-only cutover worker rules miss %s: %v", name, rules)
		}
	}
	worker.ExecCutover = false
	if rules := claudeAllowedRules(worker); slices.Contains(rules, claudeMCPToolName(ReservedMCPServer, "exec.shell")) {
		t.Fatalf("read-only worker outside the cutover allows exec: %v", rules)
	}
}

// Codex: the cutover disables the shell tool on start and resume (the one
// config builder serves both) and pre-approves the exec and fs.* tools for
// leads and workers, the read-only worker under approval policy never
// included; outside it nothing changes.
func TestCodexExecCutoverConfig(t *testing.T) {
	session := &MCPSession{URL: "http://127.0.0.1:9/mcp/session"}
	want := append(append([]string{}, ExecWSTools...), CodexReadWSTools...)
	for _, tc := range []struct {
		name string
		spec StartSpec
	}{
		{"read-only worker", StartSpec{Profile: builtinProfile(t, AgentWorkerProfile(BackendCodex)), PermissionLevel: PermissionReadOnly}},
		{"workdir-write worker", StartSpec{Profile: builtinProfile(t, AgentWorkerProfile(BackendCodex)), PermissionLevel: PermissionWorkdirWrite}},
		{"lead", StartSpec{Profile: builtinProfile(t, LeadProfileCodex), Lead: &LeadSpec{DaemonVersion: "0.50.8"}}},
		{"resumed lead", StartSpec{Profile: builtinProfile(t, LeadProfileCodex), Lead: &LeadSpec{DaemonVersion: "0.50.8"}, SessionID: "thread-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.MCPSession, tc.spec.ExecCutover = session, true
			cfg := codexThreadConfig(tc.spec)
			if v := jsonAt(roundTrip(t, cfg), "features", "shell_tool"); v != false {
				t.Fatalf("features.shell_tool = %v", v)
			}
			tools, _ := cfg["mcp_servers"].(map[string]any)["ws"].(map[string]any)["tools"].(map[string]any)
			for _, name := range want {
				if entry, _ := tools[name].(map[string]any); entry["approval_mode"] != "approve" {
					t.Fatalf("ws.tools[%s] = %v", name, tools[name])
				}
			}
			if tc.spec.Lead == nil {
				for _, name := range ChildSafeWSTools {
					if _, ok := tools[name]; !ok {
						t.Fatalf("worker lost child-safe tool %s", name)
					}
				}
			}
			tc.spec.ExecCutover = false
			cfg = codexThreadConfig(tc.spec)
			if _, ok := cfg["features"]; ok {
				t.Fatalf("features outside the cutover: %v", cfg["features"])
			}
			tools, _ = cfg["mcp_servers"].(map[string]any)["ws"].(map[string]any)["tools"].(map[string]any)
			for _, name := range want {
				if _, ok := tools[name]; ok {
					t.Fatalf("%s pre-approved outside the cutover", name)
				}
			}
		})
	}
	// A profile whose ws tools leave out the replacement gets none of it
	// pre-approved (and execCutoverFor keeps its shell).
	cfg := codexThreadConfig(StartSpec{Profile: Profile{Backend: BackendCodex, WSTools: []string{"git.status"}}, MCPSession: session, ExecCutover: true})
	tools, _ := cfg["mcp_servers"].(map[string]any)["ws"].(map[string]any)["tools"].(map[string]any)
	if _, ok := tools["exec.shell"]; ok {
		t.Fatalf("exec.shell pre-approved for a profile without it: %v", tools)
	}
}

func roundTrip(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A Pi worker under the cutover drops its built-in shell; its other tools
// and its ws tools stay.
func TestPiWorkerExecCutoverDropsBash(t *testing.T) {
	spec := StartSpec{Root: t.TempDir(), Dir: t.TempDir(), Profile: builtinProfile(t, PiWorkerProfile), WSMCP: &MCPCommand{Command: "/bin/ws-mcp"}}
	tools := func() []string {
		v, _ := flagValue(piArgs(spec, "/bridge.mjs"), "--tools")
		return strings.Split(v, ",")
	}
	if got := tools(); !slices.Contains(got, "bash") || !slices.Contains(got, "mcp__ws__exec_shell") {
		t.Fatalf("pi worker tools = %v", got)
	}
	spec.ExecCutover = true
	got := tools()
	if slices.Contains(got, "bash") {
		t.Fatalf("cutover pi worker keeps bash: %v", got)
	}
	for _, name := range []string{"read", "edit", "write", "grep", "find", "ls", "mcp__ws__exec_shell", "mcp__ws__git_commit"} {
		if !slices.Contains(got, name) {
			t.Fatalf("cutover pi worker lost %s: %v", name, got)
		}
	}
	// A lead passes no --tools at all: it would filter the extension's.
	lead := StartSpec{Dir: t.TempDir(), Lead: &LeadSpec{}, ExecCutover: true}
	if _, ok := flagValue(piLeadArgs(lead), "--tools"); ok {
		t.Fatal("a pi lead launch carries --tools")
	}
}

// The exec tools are on every worker allowlist before any native tool is
// removed, and Codex's read tools on the agent.spawn child's.
func TestExecToolsOnWorkerAllowlists(t *testing.T) {
	for _, name := range ExecWSTools {
		for list, tools := range map[string][]string{"PiWorkerWSTools": PiWorkerWSTools, "PiMiniLeadWSTools": PiMiniLeadWSTools, "AgentWSTools": AgentWSTools} {
			if !slices.Contains(tools, name) {
				t.Fatalf("%s misses %s", list, name)
			}
		}
		if slices.Contains(ChildSafeWSTools, name) {
			t.Fatalf("ChildSafeWSTools holds %s", name)
		}
	}
	for _, name := range CodexReadWSTools {
		if !slices.Contains(AgentWSTools, name) {
			t.Fatalf("AgentWSTools misses %s", name)
		}
	}
	for _, backend := range []string{BackendClaude, BackendCodex, BackendAgy} {
		if p := builtinProfile(t, AgentWorkerProfile(backend)); !slices.Contains(p.WSTools, "exec.shell") {
			t.Fatalf("%s worker profile lacks exec.shell", backend)
		}
	}
}

// cutoverHarness is a lead harness whose Claude backend carries the
// verified removal and whose runner serves the HTTP ws-mcp route.
func cutoverHarness(t *testing.T) *leadHarness {
	t.Helper()
	h := newLeadHarness(t)
	claude := h.opts.Backends[BackendClaude]
	claude.ExecCutover = &ExecCutoverSupport{Since: "2.1.330"}
	h.opts.Backends[BackendClaude] = claude
	codex := h.opts.Backends[BackendCodex]
	codex.ExecCutover = &ExecCutoverSupport{Since: "0.160.0"}
	h.opts.Backends[BackendCodex] = codex
	seedHarnessVersions(t, h, map[string]string{BackendClaude: "2.1.330", BackendCodex: "0.160.0"})
	h.execJobs = newFakeExecJobs()
	h.start()
	h.runner.SetEndpoint("http://127.0.0.1:1")
	return h
}

// seedHarnessVersions saves the harness versions a predecessor daemon's
// processes reported, each from the binary h's backend launches, as a
// runner over h.dir reads them.
func seedHarnessVersions(t *testing.T, h *leadHarness, versions map[string]string) {
	t.Helper()
	var b strings.Builder
	for backend, version := range versions {
		raw, err := json.Marshal(harnessSeen{Backend: backend, Version: version, Binary: harnessBinaryOf(h.opts.Backends[backend])})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, harnessVersionsFile), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func leadLaunch(t *testing.T, h *leadHarness, id string) ([]string, Record) {
	t.Helper()
	h.send(id, "hi")
	holder := h.holder(id)
	rec, err := readRecord(wsdashboard.SessionDir(h.dir, id))
	if err != nil {
		t.Fatal(err)
	}
	return h.holders.spawned(holder).Argv, rec
}

// A hosted Claude lead starts under the cutover by default: its launch
// removes the native process tools, its record names the holder it started
// under, and its ws-mcp token tells the connection. The per-harness switch,
// an older harness version, and the stdio fallback (no trusted binding)
// each keep the native tools.
func TestExecCutoverDecisionAtAHostedLeadStart(t *testing.T) {
	h := cutoverHarness(t)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	argv, rec := leadLaunch(t, h, st.ID)
	if got, _ := flagValue(argv, "--disallowedTools"); !strings.Contains(got, "Bash,PowerShell,Monitor,TaskStop") {
		t.Fatalf("cutover lead --disallowedTools = %q", got)
	}
	if rec.ExecCutoverHolder == "" || rec.ExecCutoverHolder != rec.HolderID {
		t.Fatalf("record cutover holder %q, holder %q", rec.ExecCutoverHolder, rec.HolderID)
	}
	if rec.MCPToken == "" {
		t.Fatal("the lead has no ws-mcp token")
	}
	hello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken)
	if !ok || hello.Conversation != st.ID || hello.ExecCutover != BackendClaude {
		t.Fatalf("token hello = %+v %v", hello, ok)
	}

	// A daemon restart rejoins the process and keeps its cutover.
	h.runner.Close()
	h.holders.restart()
	h.runner = NewRunner(h.opts)
	h.runner.SetEndpoint("http://127.0.0.1:1")
	h.runner.Start(func(string, string) {})
	h.runner.BindExecJobs(h.execJobs)
	t.Cleanup(h.runner.Close)
	h.quiesce(st.ID)
	if hello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken); !ok || hello.ExecCutover != BackendClaude {
		t.Fatalf("rejoined token hello = %+v %v", hello, ok)
	}

	// The per-harness switch reverts new starts.
	writeOverrides(t, filepath.Join(h.opts.Config.ConfigHome, "config.json"), map[string]string{
		wsconfig.ItemExecNativeToolRemoval + ".claude": "off",
	})
	off := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	argv, rec = leadLaunch(t, h, off.ID)
	if got, _ := flagValue(argv, "--disallowedTools"); strings.Contains(got, "Bash") {
		t.Fatalf("switched-off lead removes %q", got)
	}
	if rec.ExecCutoverHolder != "" {
		t.Fatalf("switched-off lead records cutover holder %q", rec.ExecCutoverHolder)
	}
	if hello, _, _ := h.runner.ResolveMCPToken(rec.MCPToken); hello.ExecCutover != "" {
		t.Fatalf("switched-off token hello cutover = %q", hello.ExecCutover)
	}
	if !strings.Contains(h.log.String(), "keeps its native process tools: "+wsconfig.ItemExecNativeToolRemoval+" is off for claude") {
		t.Fatalf("log: %s", h.log.String())
	}
	writeOverrides(t, filepath.Join(h.opts.Config.ConfigHome, "config.json"), map[string]string{
		wsconfig.ItemExecNativeToolRemoval + ".all":    "off",
		wsconfig.ItemExecNativeToolRemoval + ".claude": "on",
	})
	on := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	if _, rec = leadLaunch(t, h, on.ID); rec.ExecCutoverHolder == "" {
		t.Fatal("the harness bucket's on does not win over all's off")
	}
	writeOverrides(t, filepath.Join(h.opts.Config.ConfigHome, "config.json"), map[string]string{})

	// A harness version older than the verified one keeps native tools.
	claudeBinary := harnessBinaryOf(h.opts.Backends[BackendClaude])
	h.runner.noteHarnessVersion(BackendClaude, "2.1.326", claudeBinary)
	old := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	if _, rec = leadLaunch(t, h, old.ID); rec.ExecCutoverHolder != "" {
		t.Fatal("a lead on an unverified older harness started under the cutover")
	}
	h.runner.noteHarnessVersion(BackendClaude, "2.1.331", claudeBinary)
	newer := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	if _, rec = leadLaunch(t, h, newer.ID); rec.ExecCutoverHolder == "" {
		t.Fatal("a lead on a newer harness kept its native tools")
	}
	// The version a process reported outlives the daemon.
	if got := NewRunner(h.opts).harnessVersion(BackendClaude); got.Version != "2.1.331" || got.Binary != claudeBinary {
		t.Fatalf("a later daemon reads harness version %+v", got)
	}

	// Hosted exec jobs that failed to open leave nothing to replace the
	// native tools with.
	h.runner.exec.src.Store(nil)
	noJobs := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	argv, rec = leadLaunch(t, h, noJobs.ID)
	if got, _ := flagValue(argv, "--disallowedTools"); strings.Contains(got, "Bash") || rec.ExecCutoverHolder != "" {
		t.Fatalf("lead without hosted exec jobs: removes %q, cutover holder %q", got, rec.ExecCutoverHolder)
	}
	if !strings.Contains(h.log.String(), "keeps its native process tools: the daemon's hosted exec jobs are not open") {
		t.Fatalf("log: %s", h.log.String())
	}
	h.runner.BindExecJobs(h.execJobs)

	// Without the HTTP route the lead runs ws-mcp over stdio: no binding.
	h.runner.SetEndpoint("")
	stdio := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	argv, rec = leadLaunch(t, h, stdio.ID)
	if got, _ := flagValue(argv, "--disallowedTools"); strings.Contains(got, "Bash") || rec.ExecCutoverHolder != "" {
		t.Fatalf("stdio-fallback lead: removes %q, cutover holder %q", got, rec.ExecCutoverHolder)
	}
}

// A process an older daemon started since (another holder than the one
// recorded) rejoins without the cutover.
func TestExecCutoverRejoinNeedsTheSameHolder(t *testing.T) {
	h := cutoverHarness(t)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	_, rec := leadLaunch(t, h, st.ID)
	c := h.runner.conv(st.ID)
	rec.ExecCutoverHolder = "holder-of-an-older-start"
	if c.rejoinExecCutover(rec) {
		t.Fatal("a rejoin under another holder kept the cutover")
	}
	if hello, _, _ := h.runner.ResolveMCPToken(rec.MCPToken); hello.ExecCutover != BackendClaude {
		// The token was marked at the start; a refused rejoin leaves it.
		t.Fatalf("token hello cutover = %q", hello.ExecCutover)
	}
	rec.ExecCutoverHolder = rec.HolderID
	if !c.rejoinExecCutover(rec) {
		t.Fatal("a rejoin under the recorded holder lost the cutover")
	}
}

// A backend without a verified mechanism, a profile without the exec
// replacement, and agentless ws-mcp each keep native tools.
func TestExecCutoverExclusions(t *testing.T) {
	h := cutoverHarness(t)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	leadLaunch(t, h, st.ID)
	c := h.runner.conv(st.ID)
	rec := c.record()
	binary := harnessBinaryOf(h.opts.Backends[BackendClaude])
	bound := execCutoverInputs{binding: true, binary: binary}
	if on, why := c.execCutoverFor(rec, Profile{}, bound); !on {
		t.Fatalf("lead with binding: %s", why)
	}
	worker := rec
	worker.Lead = false
	if on, why := c.execCutoverFor(worker, Profile{WSTools: []string{"git.status"}}, bound); on || !strings.Contains(why, "no exec.shell") {
		t.Fatalf("worker without exec tools: %v %s", on, why)
	}
	// A Codex worker's shell is also its read surface: exec.shell alone
	// leaves it nothing to read with.
	codexWorker := worker
	codexWorker.Backend = BackendCodex
	codexBound := execCutoverInputs{binding: true, binary: harnessBinaryOf(h.opts.Backends[BackendCodex])}
	if on, why := c.execCutoverFor(codexWorker, Profile{WSTools: append([]string{"exec.shell"}, CodexReadWSTools[:2]...)}, codexBound); on || !strings.Contains(why, "no fs.list") {
		t.Fatalf("codex worker without fs.list: %v %s", on, why)
	}
	if on, why := c.execCutoverFor(codexWorker, builtinProfile(t, AgentWorkerProfile(BackendCodex)), codexBound); !on {
		t.Fatalf("codex agent worker profile refused: %s", why)
	}
	if on, _ := c.execCutoverFor(worker, builtinProfile(t, AgentWorkerProfile(BackendClaude)), bound); !on {
		t.Fatal("agent worker profile refused")
	}
	if on, why := c.execCutoverFor(rec, Profile{}, execCutoverInputs{binding: true, binary: binary, mcpEnv: map[string]string{envNoAgent: "1"}}); on || !strings.Contains(why, "agentless") {
		t.Fatalf("agentless: %v %s", on, why)
	}
	if on, why := c.execCutoverFor(rec, Profile{}, execCutoverInputs{binary: binary}); on || !strings.Contains(why, "trusted ws-mcp binding") {
		t.Fatalf("no binding: %v %s", on, why)
	}
	pi := rec
	pi.Backend = BackendPi
	if on, why := c.execCutoverFor(pi, Profile{}, bound); on || !strings.Contains(why, "no verified native tool removal") {
		t.Fatalf("pi: %v %s", on, why)
	}
}

// A harness whose version no process has reported, to this daemon or a
// predecessor, keeps its native tools until one does.
func TestExecCutoverUnknownHarnessVersionKeepsNativeTools(t *testing.T) {
	h := newLeadHarness(t)
	claude := h.opts.Backends[BackendClaude]
	claude.ExecCutover = &ExecCutoverSupport{Since: "2.1.330"}
	h.opts.Backends[BackendClaude] = claude
	h.execJobs = newFakeExecJobs()
	h.start()
	h.runner.SetEndpoint("http://127.0.0.1:1")
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	argv, rec := leadLaunch(t, h, st.ID)
	if got, _ := flagValue(argv, "--disallowedTools"); strings.Contains(got, "Bash") || rec.ExecCutoverHolder != "" {
		t.Fatalf("unknown-version lead: removes %q, cutover holder %q", got, rec.ExecCutoverHolder)
	}
	if !strings.Contains(h.log.String(), "keeps its native process tools: no process of the harness has reported its version yet") {
		t.Fatalf("log: %s", h.log.String())
	}
}

// A version stands for the binary its process ran from: a downgrade (any
// change of the binary) since the report leaves the version unverified,
// and the next start keeps its native tools.
func TestExecCutoverBinaryChangeUnverifiesTheVersion(t *testing.T) {
	h := newLeadHarness(t)
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n# 2.1.331\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	claude := h.opts.Backends[BackendClaude]
	claude.Backend = ClaudeBackend{Command: bin}
	claude.ExecCutover = &ExecCutoverSupport{Since: "2.1.330"}
	h.opts.Backends[BackendClaude] = claude
	seedHarnessVersions(t, h, map[string]string{BackendClaude: "2.1.331"})
	h.execJobs = newFakeExecJobs()
	h.start()
	h.runner.SetEndpoint("http://127.0.0.1:1")
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	if _, rec := leadLaunch(t, h, st.ID); rec.ExecCutoverHolder == "" {
		t.Fatalf("the reported binary's lead kept its native tools; log: %s", h.log.String())
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n# 2.1.300, a downgrade\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	down := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	argv, rec := leadLaunch(t, h, down.ID)
	if got, _ := flagValue(argv, "--disallowedTools"); strings.Contains(got, "Bash") || rec.ExecCutoverHolder != "" {
		t.Fatalf("a lead on a changed binary: removes %q, cutover holder %q", got, rec.ExecCutoverHolder)
	}
	if !strings.Contains(h.log.String(), "keeps its native process tools: the harness binary changed since a process of it reported version 2.1.331") {
		t.Fatalf("log: %s", h.log.String())
	}
}

// Reports noted concurrently land in the log in the order the store
// took them: a later daemon reads what this one holds.
func TestHarnessVersionsLogOrderMatchesMemory(t *testing.T) {
	dir := t.TempDir()
	s := loadHarnessVersions(dir, io.Discard)
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.note(BackendCodex, fmt.Sprintf("0.%d.0", 160+i), "bin")
		}()
	}
	wg.Wait()
	if got, want := loadHarnessVersions(dir, io.Discard).get(BackendCodex), s.get(BackendCodex); got != want {
		t.Fatalf("reloaded %+v, memory %+v", got, want)
	}
	// An unchanged report writes nothing.
	before, _ := os.ReadFile(filepath.Join(dir, harnessVersionsFile))
	v := s.get(BackendCodex)
	s.note(BackendCodex, v.Version, v.Binary)
	if after, _ := os.ReadFile(filepath.Join(dir, harnessVersionsFile)); len(after) != len(before) {
		t.Fatal("an unchanged report appended a line")
	}
	if strings.Count(string(before), "\n") != 64 {
		t.Fatalf("log lines = %d", strings.Count(string(before), "\n"))
	}
}

// The rollout switch costs one config resolution per start, not one per
// key it falls back through.
func TestExecRolloutReadsTheConfigOnce(t *testing.T) {
	h := cutoverHarness(t)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	c := h.runner.conv(st.ID)
	rec := c.record()
	resolver := c.rolloutResolver(rec)
	ops := func(f func()) int64 {
		before := wsstate.FileIOCounters()["file_io_total"]
		f()
		return wsstate.FileIOCounters()["file_io_total"] - before
	}
	one := ops(func() { _, _ = resolver.ListScalars(rec.WSKey, wsconfig.ItemExecNativeToolRemoval) })
	if got := ops(func() { c.execRolloutOn(rec) }); got > one {
		t.Fatalf("the rollout check took %d file operations; one resolution takes %d", got, one)
	}
	if !c.execRolloutOn(rec) {
		t.Fatal("the rollout is off by default")
	}
}

// writeOverrides writes a config file whose plain overrides are values.
func writeOverrides(t *testing.T, path string, values map[string]string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "overrides": values})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
