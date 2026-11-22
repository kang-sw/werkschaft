package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wslive"
)

// werkModel is the fake model endpoint a werk child talks to in these
// fixtures: the same Responses shapes the recorded-stream tests of
// internal/werk pin, one step per request.
type werkModel struct {
	mu    sync.Mutex
	steps []func(ctx context.Context, req werk.Request) (*werk.Response, error)
	reqs  []werk.Request
}

func (m *werkModel) Respond(ctx context.Context, req werk.Request, _ werk.StreamHandler) (*werk.Response, error) {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	n := len(m.reqs) - 1
	var step func(context.Context, werk.Request) (*werk.Response, error)
	if n < len(m.steps) {
		step = m.steps[n]
	}
	m.mu.Unlock()
	if step == nil {
		return nil, fmt.Errorf("unscripted request %d", n+1)
	}
	return step(ctx, req)
}

func (m *werkModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reqs)
}

func werkMessage(text string) func(context.Context, werk.Request) (*werk.Response, error) {
	return func(context.Context, werk.Request) (*werk.Response, error) {
		cost := 0.0042
		item, _ := json.Marshal(map[string]any{"type": "message", "id": "msg_1", "role": "assistant",
			"content": []map[string]string{{"type": "output_text", "text": text}}})
		return &werk.Response{ID: "r", Output: []json.RawMessage{item}, Usage: werk.Usage{InputTokens: 200, OutputTokens: 20, CachedTokens: 150, CostUSD: &cost}}, nil
	}
}

func werkCall(id, name, args string) string {
	raw, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": name, "arguments": args})
	return string(raw)
}

// werkWS is the daemon's ws tool surface a werk child calls.
type werkWS struct {
	mu    sync.Mutex
	tools []werk.MCPTool
	calls []string
}

func (w *werkWS) ListTools(context.Context) ([]werk.MCPTool, error) { return w.tools, nil }
func (w *werkWS) Call(_ context.Context, name string, _ json.RawMessage) (werk.CallResult, error) {
	w.mu.Lock()
	w.calls = append(w.calls, name)
	w.mu.Unlock()
	return werk.CallResult{Text: "ran " + name}, nil
}

// werkFleet runs the real werk loop in-process behind a fake holder: a spawn
// starts an Agent from the spawn's own argv, the holder's input feeds the
// agent's standard input, and its output lines become the holder's records.
// The child outlives a runner, as a held process outlives its daemon.
type werkFleet struct {
	t     *testing.T
	host  *fakeHolders
	model *werkModel
	ws    *werkWS

	mu     sync.Mutex
	argv   map[string][]string
	env    map[string]map[string]string
	pipes  map[string]*io.PipeWriter
	agents map[string]*werk.Agent
}

func newWerkFleet(t *testing.T, host *fakeHolders, model *werkModel) *werkFleet {
	t.Helper()
	f := &werkFleet{t: t, host: host, model: model, ws: &werkWS{}, argv: map[string][]string{}, env: map[string]map[string]string{},
		pipes: map[string]*io.PipeWriter{}, agents: map[string]*werk.Agent{}}
	host.onSpawn = f.spawn
	host.onInput = f.input
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, pw := range f.pipes {
			pw.Close()
		}
	})
	return f
}

// flagValue reads --name from argv.
func argValue(argv []string, name string) string {
	for i, a := range argv {
		if a == name && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

type lineSink struct {
	emit func(string)
	buf  bytes.Buffer
}

func (l *lineSink) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		raw := l.buf.Bytes()
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			break
		}
		line := string(raw[:i])
		l.buf.Next(i + 1)
		l.emit(line)
	}
	return len(p), nil
}

func (f *werkFleet) spawn(id string, spec wsholder.SpawnSpec) {
	argv := spec.Argv
	agent, err := werk.New(werk.Config{
		Dir: argValue(argv, "--dir"), Root: argValue(argv, "--root"), Cwd: argValue(argv, "--cwd"),
		Authority: werk.Authority(argValue(argv, "--authority")), Model: argValue(argv, "--model"),
		Effort: argValue(argv, "--effort"), Session: argValue(argv, "--session"),
		LocalTools: strings.Split(argValue(argv, "--tools"), ","),
		Endpoint:   f.model, WS: f.ws,
		Out:   &lineSink{emit: func(line string) { f.host.out(id, line) }},
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		f.t.Errorf("start the werk loop: %v", err)
		return
	}
	pr, pw := io.Pipe()
	f.mu.Lock()
	f.argv[id], f.env[id], f.pipes[id], f.agents[id] = argv, spec.Env, pw, agent
	f.mu.Unlock()
	go func() {
		_ = agent.Serve(context.Background(), pr)
		agent.Close()
	}()
}

func (f *werkFleet) input(id string, data []byte, eof bool) {
	f.mu.Lock()
	pw := f.pipes[id]
	f.mu.Unlock()
	if pw == nil {
		return
	}
	if len(data) > 0 {
		_, _ = pw.Write(data)
	}
	if eof {
		pw.Close()
	}
}

// werkHarness is a runner harness with the werk registration and a configured
// small tier.
func werkHarness(t *testing.T, model *werkModel, configured bool) (*leadHarness, *werkFleet, *reports) {
	t.Helper()
	h := newLeadHarness(t)
	got := &reports{}
	h.opts.Report = got.add
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{Command: "/unused/ws-mcp"}, nil }
	_, reg := WerkBackend{Command: fakeHarness("ws-mcp")}.registration()
	h.opts.Backends[BackendWerk] = reg
	// The hosted exec jobs are open, as in a daemon: a werk worker has no
	// native shell, so it runs under the exec cutover and lists the ws exec
	// tools.
	h.execJobs = newFakeExecJobs()
	fleet := newWerkFleet(t, h.holders, model)
	if configured {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.opts.Config, "small", "", "vendor/some-model", BackendWerk, "medium"); err != nil {
			t.Fatal(err)
		}
	}
	h.startAt("http://127.0.0.1:1")
	return h, fleet, got
}

func spawnWerk(t *testing.T, h *leadHarness, authority string) (id, holder string) {
	t.Helper()
	caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
	res, err := h.runner.AgentSpawn(caller, AgentSpawnRequest{Prompt: "do the work", Tier: BackendWerk + ".small", Authority: authority})
	if err != nil {
		t.Fatal(err)
	}
	id = agentID(res.Address)
	return id, h.holder(id)
}

func TestWerkWorkerRunsATurnAndReportsItThroughTheRunner(t *testing.T) {
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(context.Context, werk.Request) (*werk.Response, error) {
			item := werkCall("call_1", "git_status", `{}`)
			return &werk.Response{Output: []json.RawMessage{json.RawMessage(item)}, Usage: werk.Usage{InputTokens: 100, OutputTokens: 5}}, nil
		},
		werkMessage("all done: clean tree"),
	}}
	h, fleet, got := werkHarness(t, model, true)
	fleet.ws.tools = []werk.MCPTool{{Name: "git.status", Description: "status"}}
	id, holder := spawnWerk(t, h, "workdir-write")

	eventually(t, "the worker's report", func() bool { return len(got.list()) == 1 })
	rep := got.list()[0]
	if rep.Result.Status != TurnDone || rep.Result.Message != "all done: clean tree" || rep.Result.ID != id {
		t.Fatalf("report = %+v", rep)
	}
	if calls := fleet.ws.calls; len(calls) != 1 || calls[0] != "git.status" {
		t.Errorf("ws tool calls = %v", calls)
	}

	rec := h.record(id)
	if rec.Backend != BackendWerk || rec.SessionID == "" || rec.Model != "vendor/some-model" || rec.Effort != "medium" {
		t.Errorf("record = backend %q session %q model %q effort %q", rec.Backend, rec.SessionID, rec.Model, rec.Effort)
	}
	if rec.MCPToken == "" || rec.MCPHolder != holder {
		t.Errorf("the worker's MCP tuple was not persisted: %+v", rec)
	}
	// The worker started under the exec cutover with no version gate (the
	// harness has no native process tool, so the removal is the absence of
	// the tool): its connection lists the exec tools, and the record keeps the
	// holder it was started under, as a rejoin reads it.
	if rec.ExecCutoverHolder != holder {
		t.Errorf("exec cutover holder = %q, want %q", rec.ExecCutoverHolder, holder)
	}
	if hello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken); !ok || hello.ExecCutover != BackendWerk {
		t.Errorf("the worker's token is not marked for the exec cutover: %+v %v", hello, ok)
	}

	// The spawn: the ws binary's hidden subcommand, authority and model in
	// its argv; the bearer and route in its environment, never its argv; no
	// provider credential anywhere.
	argv, env := fleet.argv[holder], fleet.env[holder]
	if len(argv) < 2 || argv[0] != fakeHarness("ws-mcp") || argv[1] != WerkSubcommand {
		t.Fatalf("argv = %v", argv)
	}
	for flag, want := range map[string]string{"--authority": "workdir-write", "--model": "vendor/some-model", "--effort": "medium", "--tools": "read,edit,write,grep,glob"} {
		if got := argValue(argv, flag); got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}
	if env[EnvMCPSessionToken] == "" || env[EnvMCPSessionToken] != rec.MCPToken || env[EnvMCPSessionURL] == "" {
		t.Errorf("session route not in the child's environment: %v", env)
	}
	for _, a := range argv {
		if strings.Contains(a, rec.MCPToken) {
			t.Error("the bearer is in argv")
		}
	}
	for name := range env {
		if strings.Contains(strings.ToLower(name), "openrouter") || strings.Contains(strings.ToLower(name), "api_key") {
			t.Errorf("a provider credential variable reached the child: %s", name)
		}
	}

}

func TestWerkReadOnlyWorkerIsOfferedTheReadingTools(t *testing.T) {
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){werkMessage("looked")}}
	h, fleet, got := werkHarness(t, model, true)
	_, holder := spawnWerk(t, h, "read-only")
	eventually(t, "the report", func() bool { return len(got.list()) == 1 })
	if tools := argValue(fleet.argv[holder], "--tools"); tools != "read,grep,glob" {
		t.Errorf("read-only tools = %q", tools)
	}
	if lvl := argValue(fleet.argv[holder], "--authority"); lvl != "read-only" {
		t.Errorf("authority = %q", lvl)
	}
	reqs := model.reqs
	var names []string
	for _, d := range reqs[0].Tools {
		names = append(names, d.Name)
	}
	if slices.Contains(names, "edit") || slices.Contains(names, "write") {
		t.Errorf("a read-only worker was offered %v", names)
	}
}

// A daemon replacement while the model request is in flight: the holder and
// its child survive, the successor attaches and replays, and the one report
// of the finished turn reaches the parent once, from the same process.
func TestWerkHolderSurvivesADaemonReplaceMidTurn(t *testing.T) {
	gate := make(chan struct{})
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			<-gate
			return werkMessage("survived the swap")(ctx, req)
		},
	}}
	h, fleet, got := werkHarness(t, model, true)
	id, holder := spawnWerk(t, h, "workdir-write")
	eventually(t, "the model request", func() bool { return model.requestCount() == 1 })
	before := h.record(id)
	if before.HolderID != holder || !before.TurnOpen {
		t.Fatalf("worker is not mid-turn under its holder: %+v", before)
	}

	h.runner.Close()
	if h.holders.opCount("stop") != 0 {
		t.Fatalf("the runner's shutdown stopped the worker: %v", h.holders.opList())
	}
	h.holders.restart()
	close(gate)
	h.startAt("http://127.0.0.1:1")

	eventually(t, "the report after the replacement", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.Status != TurnDone || rep.Result.Message != "survived the swap" {
		t.Fatalf("report = %+v", rep)
	}
	after := h.record(id)
	if after.HolderID != holder || after.HostPID != before.HostPID || after.TurnOpen || after.LastTurn == nil || after.LastTurn.Result.Status != TurnDone {
		t.Fatalf("rejoin lost the holder or the turn: before %+v, after %+v", before, after)
	}
	if after.MCPToken != before.MCPToken || after.MCPURL != before.MCPURL {
		t.Errorf("the MCP tuple changed: %+v / %+v", before, after)
	}
	if h.holders.opCount("launch") != 1 || h.holders.opCount("spawn") != 1 || h.holders.opCount("stop") != 0 {
		t.Fatalf("the replacement changed the physical process: %v", h.holders.opList())
	}
	if len(got.list()) != 1 {
		t.Fatalf("more than one report: %+v", got.list())
	}
	if n := len(fleet.agents); n != 1 {
		t.Errorf("agents started = %d", n)
	}
	// The successor drives the same child: a second prompt reaches it and
	// the turn runs on the conversation the first one built.
	model.mu.Lock()
	model.steps = append(model.steps, werkMessage("second turn"))
	model.mu.Unlock()
	h.send(id, "again")
	eventually(t, "the second request", func() bool { return model.requestCount() == 2 })
	model.mu.Lock()
	second := model.reqs[1].Input
	model.mu.Unlock()
	if len(second) != 3 {
		t.Errorf("the second request carries %d items, want the first turn's user and reply plus the second user", len(second))
	}
}

func TestWerkInterruptAbortsTheTurnAndTheSessionStays(t *testing.T) {
	started := make(chan struct{}, 1)
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, _ werk.Request) (*werk.Response, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		werkMessage("after the interrupt"),
	}}
	h, _, got := werkHarness(t, model, true)
	id, _ := spawnWerk(t, h, "workdir-write")
	<-started
	if _, err := h.runner.AgentInterrupt(LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config), "id:"+id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the interrupted report", func() bool { return len(got.list()) == 1 })
	// The turn ends as an aborted one: it settles with no reply and no error,
	// as an interrupted turn of every other harness does.
	if rep := got.list()[0]; rep.Result.Status != TurnDone || rep.Result.Message != "" || rep.Result.Error != "" {
		t.Fatalf("report result = %+v", rep.Result)
	}
	if st := h.state(id); !st.Process {
		t.Errorf("an interrupt ended the process: %+v", st)
	}
	h.send(id, "carry on")
	eventually(t, "the next turn's report", func() bool { return len(got.list()) == 2 })
	if rep := got.list()[1]; rep.Result.Status != TurnDone || rep.Result.Message != "after the interrupt" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestWerkRefusesAWorkerWithoutAModelOrAuthority(t *testing.T) {
	host := newFakeHolders()
	mk := func(level PermissionLevel, model string) StartSpec {
		return StartSpec{
			Root: t.TempDir(), Dir: t.TempDir(), Model: model, PermissionLevel: level,
			MCPSession: &MCPSession{URL: "http://127.0.0.1:1/mcp/session", Token: "tok"},
			Holder:     &holderChild{host: host, table: &holderTable{}, session: holderSession{Kind: holderKindAgentWorker, Backend: BackendWerk}},
		}
	}
	b := WerkBackend{Command: fakeHarness("ws-mcp")}
	if _, err := b.Start(context.Background(), mk("", "vendor/m")); err == nil || !strings.Contains(err.Error(), "needs an authority") {
		t.Errorf("no authority: %v", err)
	}
	if _, err := b.Start(context.Background(), mk(PermissionFull, "")); err == nil || !strings.Contains(err.Error(), "no tier model is configured") || !strings.Contains(err.Error(), "agents.tier") {
		t.Errorf("no model: %v", err)
	}
	if _, err := b.Start(context.Background(), StartSpec{Lead: &LeadSpec{}, Holder: &holderChild{}}); !errors.Is(err, errWerkLead) {
		t.Errorf("a lead: %v", err)
	}
	noHolder := mk(PermissionFull, "vendor/m")
	noHolder.Holder = nil
	if _, err := b.Start(context.Background(), noHolder); err == nil || !strings.Contains(err.Error(), "session holder") {
		t.Errorf("no holder: %v", err)
	}
	if n := host.opCount("launch"); n != 0 {
		t.Errorf("a refused start launched %d holders", n)
	}
}

func TestWerkAttachSendsNothing(t *testing.T) {
	host := newFakeHolders()
	id := host.add(holderSession{Kind: holderKindAgentWorker, Backend: BackendWerk, ReplyID: "worker"})
	host.Scan()
	proc, err := WerkBackend{Command: fakeHarness("ws-mcp")}.Start(context.Background(), StartSpec{
		Root: t.TempDir(), Dir: t.TempDir(), SessionID: "existing",
		Holder: &holderChild{host: host, table: &holderTable{}, id: id},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.(interface{ Detach() bool }).Detach() })
	if ops, inputs := host.opList(), host.inputs(id, ""); len(ops) != 0 || len(inputs) != 0 {
		t.Fatalf("attach launched or sent to a process: ops %v, inputs %v", ops, inputs)
	}
}

// Availability: no OpenRouter key is a reason in agent.tiers and the
// dashboard's check; no tier model is another; with both, werk lists tiers.
func TestWerkAvailabilityNamesWhatIsMissing(t *testing.T) {
	configured := false
	h := newLeadHarness(t)
	_, reg := WerkBackend{Command: fakeHarness("ws-mcp"), credentialStatus: func(string) (bool, error) { return configured, nil }}.registration()
	h.opts.Backends[BackendWerk] = reg
	h.startAt("http://127.0.0.1:1")
	lead := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)

	why, known := h.runner.HarnessAvailability(BackendClaude, BackendWerk)
	if !known || !strings.Contains(why, "credential set openrouter") {
		t.Fatalf("no key: availability = %q, %v", why, known)
	}
	tiers, err := h.runner.AgentTiers(lead)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(tiers.Unavailable, func(s string) bool {
		return strings.HasPrefix(s, "werk: ") && strings.Contains(s, "credential set openrouter")
	}) {
		t.Errorf("agent.tiers unavailable = %v", tiers.Unavailable)
	}
	if slices.ContainsFunc(tiers.Ext, func(e AgentTierEntry) bool { return strings.HasPrefix(e.Tier, "werk.") }) {
		t.Errorf("an unavailable harness listed tiers: %+v", tiers.Ext)
	}
	_, err = h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: "werk.small", Authority: "read-only"})
	if err == nil || !strings.Contains(err.Error(), "credential set openrouter") {
		t.Errorf("spawn without a key: %v", err)
	}

	configured = true
	if why, _ := h.runner.HarnessAvailability(BackendClaude, BackendWerk); why != "" {
		t.Fatalf("with a key: availability = %q", why)
	}
	tiers, _ = h.runner.AgentTiers(lead)
	if !slices.ContainsFunc(tiers.Unavailable, func(s string) bool {
		return strings.HasPrefix(s, "werk: ") && strings.Contains(s, "no tier model") && strings.Contains(s, "agents.tier")
	}) {
		t.Errorf("a key and no tier model: unavailable = %v", tiers.Unavailable)
	}
	if slices.ContainsFunc(tiers.Ext, func(e AgentTierEntry) bool { return strings.HasPrefix(e.Tier, "werk.") }) {
		t.Errorf("tiers have no default model, yet listed %+v", tiers.Ext)
	}
	_, err = h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: "werk.small", Authority: "read-only"})
	if err == nil || !strings.Contains(err.Error(), "werk.small has no model in agents.tier") {
		t.Errorf("spawn with no tier model: %v", err)
	}

	if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.opts.Config, "medium", "", "vendor/m", BackendWerk, ""); err != nil {
		t.Fatal(err)
	}
	tiers, _ = h.runner.AgentTiers(lead)
	if slices.ContainsFunc(tiers.Unavailable, func(s string) bool { return strings.HasPrefix(s, "werk: ") }) {
		t.Errorf("configured: unavailable = %v", tiers.Unavailable)
	}
	if !slices.ContainsFunc(tiers.Ext, func(e AgentTierEntry) bool { return e.Tier == "werk.medium" && e.Model == "vendor/m" }) {
		t.Errorf("configured: tiers = %+v", tiers.Ext)
	}
}

func TestWerkShipsNoDefaultTierModel(t *testing.T) {
	tiers, err := wsconfig.HarnessTiers(wsconfig.Options{CacheHome: t.TempDir()}, BackendWerk)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiers) != 0 {
		t.Fatalf("werk tiers with no configuration = %+v", tiers)
	}
}

func TestWerkCredentialStatusFailureIsReported(t *testing.T) {
	b := WerkBackend{credentialStatus: func(string) (bool, error) { return false, errors.New("the credential file is not valid") }}
	if gap := b.workerCredentialGap(); !strings.Contains(gap, "could not be read") || !strings.Contains(gap, "not valid") {
		t.Errorf("gap = %q", gap)
	}
	if gap := (WerkBackend{}).workerCredentialGap(); gap != "" {
		t.Errorf("a daemon with no store status blocks: %q", gap)
	}
}

// The catalog states every item for both roles, with no transitional
// fallback.
func TestWerkCatalogGradesEveryItemForBothRoles(t *testing.T) {
	cat := WerkCatalog()
	if cat.Oldest != werk.ProtocolVersion || cat.Newest != werk.ProtocolVersion {
		t.Errorf("range = %s..%s", cat.Oldest, cat.Newest)
	}
	for _, role := range []CatalogRole{RoleWorker, RoleLead} {
		stated := map[CapabilityItem]bool{}
		for _, e := range cat.Entries {
			if e.Role == role {
				stated[e.Item] = true
				if e.Source == "" {
					t.Errorf("%s %s cites no source", role, e.Item)
				}
			}
		}
		for _, item := range CatalogItems {
			if !stated[item] {
				t.Errorf("%s: item %s is not stated", role, item)
			}
		}
	}
	reg := BuiltinBackendRegistrations()[BackendWerk]
	if reg.NativeCompaction != nil {
		t.Error("werk keeps no transitional compaction descriptor")
	}
	caps := cat.Resolve(RoleWorker, werk.ProtocolVersion)
	if caps.Unverified || caps.Grade(ItemSteer) != GradeNative || caps.Grade(ItemInterrupt) != GradeNative || caps.Grade(ItemResume) != GradeNative ||
		caps.Grade(ItemFollowUp) != GradeSubstitute || caps.Grade(ItemModelEffort) != GradeNative || caps.Grade(ItemUsageQuery) != GradeNative {
		t.Errorf("worker capabilities = %+v", caps)
	}
	if lead := cat.Resolve(RoleLead, werk.ProtocolVersion); lead.Allows(ItemSteer) {
		t.Errorf("no werk lead exists yet: %+v", lead)
	}
}

// Mapper fixtures: one line per kind of the loop's protocol.
func TestWerkMapperMapsEveryLineKind(t *testing.T) {
	line := func(l werk.Line) []byte {
		l.At = 1_700_000_000_000
		raw, _ := json.Marshal(l)
		return raw
	}
	m := newWerkMapper("")
	var answered []string
	replies := repliesFunc{onAnswer: func(id string, a controlAnswer) { answered = append(answered, id) }}

	if evs := m.mapLine(line(werk.Line{T: werk.LineInit, Session: "abc123", Version: werk.ProtocolVersion}), replies); len(evs) != 2 || evs[0].SessionID != "abc123" || evs[1].Harness == nil || evs[1].Harness.Version != werk.ProtocolVersion {
		t.Errorf("init = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineTurnStart, Turn: 1}), replies); len(evs) != 1 || !evs[0].TurnStart {
		t.Errorf("turn_start = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineReply, Turn: 1, Item: "m1", Text: "partial"}), replies); len(evs) != 1 || evs[0].Live.Kind != wslive.KindReply || evs[0].Live.Final || evs[0].Live.Source != "" {
		t.Errorf("streamed reply = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineReply, Turn: 1, Item: "m1", Text: "whole", Final: true}), replies); len(evs) != 1 || !evs[0].Live.Final || evs[0].Live.Source != "werk/abc123/1/m1" {
		t.Errorf("final reply = %+v", evs)
	}
	cost := 0.5
	cost2 := 0.25
	evs := m.mapLine(line(werk.Line{T: werk.LineUsage, Turn: 1, InputTokens: 1000, CachedTokens: 800, OutputTokens: 50, Context: 1050, Window: 10500, CostUSD: &cost}), replies)
	if len(evs) != 1 || evs[0].Live.Kind != wslive.KindStatus || *evs[0].Live.Context.Tokens != 1050 || *evs[0].Live.Context.Percent != 10 || *evs[0].Live.Context.CacheHit != 80 || evs[0].Live.Cost.USD != 0.5 {
		t.Errorf("usage = %+v", evs[0].Live)
	}
	m.mapLine(line(werk.Line{T: werk.LineToolStart, Turn: 1, Call: "c1", Name: "exec_shell", Args: `{"cmd":"ls"}`}), replies)
	evs = m.mapLine(line(werk.Line{T: werk.LineToolEnd, Turn: 1, Call: "c1", Name: "exec_shell", Output: "x"}), replies)
	if len(evs) != 1 || !evs[0].ToolBoundary || evs[0].Live.Kind != wslive.KindAgent || len(evs[0].Live.Batches) != 1 {
		t.Errorf("tool_end = %+v", evs)
	}
	evs = m.mapLine(line(werk.Line{T: werk.LineUsage, Turn: 1, InputTokens: 10, CostUSD: &cost2}), replies)
	if evs[0].Live.Cost.USD != 0.75 {
		t.Errorf("cost does not accumulate over responses: %+v", evs[0].Live.Cost)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineRetry, Attempt: 2, MaxRetries: 5, Status: 429, Error: "slow down"}), replies); len(evs) != 1 || evs[0].Retry == nil || evs[0].Retry.Attempt != 2 || evs[0].Retry.Status != 429 {
		t.Errorf("retry = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineSteerAck, ID: "s1"}), replies); len(evs) != 1 || evs[0].SteerAnswer == nil || evs[0].SteerAnswer.Refusal != "" {
		t.Errorf("steer ack = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineSteerAck, ID: "s2", Refusal: "no turn is running"}), replies); evs[0].SteerAnswer.Refusal != "no turn is running" {
		t.Errorf("steer refusal = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineUserTaken, Text: "also X"}), replies); len(evs) != 1 || evs[0].UserTaken != "also X" {
		t.Errorf("user_taken = %+v", evs)
	}
	if evs := m.mapLine(line(werk.Line{T: werk.LineNotice, Text: "careful"}), replies); len(evs) != 1 || evs[0].Notice != "careful" {
		t.Errorf("notice = %+v", evs)
	}
	m.mapLine(line(werk.Line{T: werk.LineStatus, ID: "wsc/status/1"}), replies)
	if len(answered) != 1 || answered[0] != "wsc/status/1" {
		t.Errorf("status answers = %v", answered)
	}
	for _, quiet := range []werk.Line{{T: werk.LineUser, Text: "hi"}, {T: werk.LineReasoning, Text: "thinking"}} {
		if evs := m.mapLine(line(quiet), replies); len(evs) != 0 {
			t.Errorf("%s mapped to %+v", quiet.T, evs)
		}
	}
	if evs := m.mapLine([]byte("not json"), replies); evs != nil {
		t.Errorf("junk = %+v", evs)
	}

	// An open batch is finished by the turn end, which names its reason.
	m.mapLine(line(werk.Line{T: werk.LineToolStart, Turn: 2, Call: "c9", Name: "read"}), replies)
	evs = m.mapLine(line(werk.Line{T: werk.LineTurnEnd, Turn: 2, Reason: werk.ReasonAborted}), replies)
	last := evs[len(evs)-1]
	if len(evs) != 3 || !last.TurnEnd || last.TurnReason != wslive.TurnAborted || last.TurnError != "" || !evs[1].RetryEnd {
		t.Errorf("aborted turn end = %+v", evs)
	}
	evs = m.mapLine(line(werk.Line{T: werk.LineTurnEnd, Turn: 3, Reason: werk.ReasonError, Error: "the model response failed (server_error)"}), replies)
	if last := evs[len(evs)-1]; !last.TurnEnd || last.TurnError != "the model response failed (server_error)" {
		t.Errorf("error turn end = %+v", evs)
	}
	evs = m.mapLine(line(werk.Line{T: werk.LineTurnEnd, Turn: 4, Reason: werk.ReasonDone}), replies)
	if last := evs[len(evs)-1]; !last.TurnEnd || last.TurnError != "" || last.TurnReason != "" {
		t.Errorf("done turn end = %+v", evs)
	}
}

type repliesFunc struct {
	onAnswer func(id string, a controlAnswer)
}

func (r repliesFunc) answer(id string, a controlAnswer) { r.onAnswer(id, a) }
func (r repliesFunc) send(any)                          {}

// The records of a turn that ended while no daemon was attached left the
// ring before the successor replayed them: the successor asks the process
// (the status command) and settles the turn from its answer, once.
func TestWerkRecoversATurnWhoseRecordsLeftTheRing(t *testing.T) {
	gate := make(chan struct{})
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			<-gate
			return werkMessage("finished while the daemon was away")(ctx, req)
		},
	}}
	h, _, got := werkHarness(t, model, true)
	id, holder := spawnWerk(t, h, "workdir-write")
	eventually(t, "the model request", func() bool { return model.requestCount() == 1 })
	h.runner.Close()
	h.holders.restart()
	close(gate)
	eventually(t, "the child's turn end", func() bool {
		for _, line := range holderOutputs(h.holders, holder) {
			if strings.Contains(line, `"t":"turn_end"`) {
				return true
			}
		}
		return false
	})
	// Only the last record is still in the ring.
	h.holders.trim(holder, h.holders.records(holder)-1)
	h.startAt("http://127.0.0.1:1")

	eventually(t, "the recovered report", func() bool { return len(got.list()) == 1 })
	rep := got.list()[0]
	if rep.Result.Status != TurnDone || rep.Result.Message != "finished while the daemon was away" || rep.Result.ID != id {
		t.Fatalf("report = %+v", rep.Result)
	}
	if n := len(h.holders.inputs(holder, "status")); n != 1 {
		t.Errorf("status asks = %d", n)
	}
	if len(got.list()) != 1 {
		t.Fatalf("reports = %+v", got.list())
	}
}

// holderOutputs are the output records the holder's child wrote, oldest first.
func holderOutputs(f *fakeHolders, id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.holders[id].ring {
		if ev.Event == "output" {
			out = append(out, string(ev.Data))
		}
	}
	return out
}
