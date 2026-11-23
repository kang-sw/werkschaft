package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsinbox"
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

type werkFixtureBackend struct {
	WerkBackend
	model *werkModel
}

func (b werkFixtureBackend) startLoop(ctx context.Context, spec StartSpec) (Process, error) {
	if spec.Holder != nil || len(spec.Env) != 0 {
		return nil, errors.New("daemon loop got child launch context")
	}
	spec.modelContextWindow = b.contextWindow
	return startWerkLoop(ctx, spec, b.model)
}

type werkFixture struct {
	runner   atomic.Pointer[Runner]
	endpoint string
	tools    []werk.MCPTool
	calls    chan string
	bindings chan wsdashboard.ProxyHello
}

func (f *werkFixture) serve(w http.ResponseWriter, req *http.Request) {
	bearer := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	hello, _, ok := f.runner.Load().ResolveMCPToken(bearer)
	if !ok || hello.FileWrites == nil || hello.Conversation == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var call struct {
		Method string
		ID     json.RawMessage
		Params struct{ Name string }
	}
	if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var result any
	switch call.Method {
	case "initialize":
		f.bindings <- hello
		result = map[string]any{}
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	case "tools/list":
		tools := slices.Clone(f.tools)
		if hello.FileWrites.CurrentAuthority() != wsfs.AuthorityReadOnly {
			tools = append(tools, werk.MCPTool{Name: "fs.write"}, werk.MCPTool{Name: "fs.edit"})
		}
		result = map[string]any{"tools": tools}
	case "tools/call":
		f.calls <- call.Params.Name
		result = map[string]any{"content": []map[string]string{{"type": "text", "text": "ran " + call.Params.Name}}}
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
}

func (f *werkFixture) start(h *leadHarness) {
	h.runner = NewRunner(h.opts)
	h.runner.SetEndpoint(f.endpoint)
	h.runner.ExpectExecJobs()
	f.runner.Store(h.runner)
	h.runner.Start(nil)
	h.runner.BindExecJobs(h.execJobs)
	h.t.Cleanup(h.runner.Close)
}

func werkHarness(t *testing.T, model *werkModel) (*leadHarness, *werkFixture, *reports) {
	t.Helper()
	h := newLeadHarness(t)
	got := &reports{}
	h.opts.Report = got.add
	h.opts.WSMCP = func(string) (MCPCommand, error) {
		return MCPCommand{}, errors.New("daemon loop attempted to resolve a launcher")
	}
	h.opts.DaemonMCP = func(string) (MCPCommand, error) { return MCPCommand{}, nil }
	_, reg := (WerkBackend{}).registration()
	if reg.ProcessKind != ProcessDurable {
		t.Fatal("the production registration did not select the daemon loop")
	}
	reg.Backend = werkFixtureBackend{WerkBackend: reg.Backend.(WerkBackend), model: model}
	h.opts.Backends[BackendWerk] = reg
	h.execJobs = newFakeExecJobs()
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.opts.Config, "small", "", "vendor/some-model", BackendWerk, "medium"); err != nil {
		t.Fatal(err)
	}
	f := &werkFixture{
		tools: []werk.MCPTool{{Name: "git.status"}, {Name: "fs.read"}, {Name: "fs.grep"}, {Name: "fs.list"}},
		calls: make(chan string, 8), bindings: make(chan wsdashboard.ProxyHello, 8),
	}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	f.endpoint = server.URL
	f.start(h)
	return h, f, got
}

func (f *werkFixture) nextBinding(t *testing.T) wsdashboard.ProxyHello {
	t.Helper()
	select {
	case hello := <-f.bindings:
		return hello
	default:
		t.Fatal("no authenticated MCP binding reached the fixture")
		return wsdashboard.ProxyHello{}
	}
}

const werkSpawnInstructions = "Use the repository conventions for this worker."

func spawnWerk(t *testing.T, h *leadHarness, authority string) string {
	t.Helper()
	caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
	res, err := h.runner.AgentSpawn(caller, AgentSpawnRequest{
		Prompt: "do the work", SystemPrompt: werkSpawnInstructions, Tier: BackendWerk + ".small", Authority: authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	return agentID(res.Address)
}

func werkRecord(t *testing.T, h *leadHarness, id string) Record {
	t.Helper()
	c := h.runner.conv(id)
	if c == nil {
		t.Fatal("no live durable conversation")
	}
	return c.record()
}

func assertWerkRequestInstructions(t *testing.T, model *werkModel, rec Record) {
	t.Helper()
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.reqs) == 0 {
		t.Fatal("no model request received")
	}
	for i, req := range model.reqs {
		if !strings.Contains(req.Instructions, werkSpawnInstructions) ||
			rec.WSKey == "" || !strings.Contains(req.Instructions, "Your ws session key is `"+rec.WSKey+"`") ||
			rec.Parent == nil || !strings.Contains(req.Instructions, "id:"+rec.Parent.ReplyID) {
			t.Errorf("request %d omitted the worker's composed instructions or ws identity", i+1)
		}
	}
}

func TestWerkWorkerRunsATurnAndReportsItThroughTheRunner(t *testing.T) {
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(context.Context, werk.Request) (*werk.Response, error) {
			item := werkCall("call_1", "git_status", `{}`)
			return &werk.Response{Output: []json.RawMessage{json.RawMessage(item)}, Usage: werk.Usage{InputTokens: 100, OutputTokens: 5}}, nil
		},
		werkMessage("all done: clean tree"),
	}}
	h, fleet, got := werkHarness(t, model)
	id := spawnWerk(t, h, "workdir-write")

	eventually(t, "the worker's report", func() bool { return len(got.list()) == 1 })
	rep := got.list()[0]
	if rep.Result.Status != TurnDone || rep.Result.Message != "all done: clean tree" || rep.Result.ID != id {
		t.Fatalf("report = %+v", rep)
	}
	select {
	case name := <-fleet.calls:
		if name != "git.status" {
			t.Errorf("called %q, want git.status", name)
		}
	default:
		t.Error("the model's tool did not use authenticated HTTP")
	}
	binding := fleet.nextBinding(t)
	if binding.Conversation != id || binding.ExecCutover != BackendWerk || binding.OwnerPID != 0 ||
		binding.FileWrites.Authority != wsfs.AuthorityWorkdirWrite || binding.FileWrites.Record == nil {
		t.Fatal("the daemon process did not get its trusted tool authority")
	}
	rec := werkRecord(t, h, id)
	assertWerkRequestInstructions(t, model, rec)
	if rec.Backend != BackendWerk || rec.SessionID == "" || rec.Model != "vendor/some-model" || rec.Effort != "medium" ||
		rec.HolderID != "" || rec.MCPToken != "" || h.holders.opCount("spawn") != 0 {
		t.Fatal("daemon worker did not keep its stable identity without a holder")
	}
}

func TestWerkReadOnlyWorkerIsOfferedTheReadingTools(t *testing.T) {
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){werkMessage("looked")}}
	h, fleet, got := werkHarness(t, model)
	id := spawnWerk(t, h, "read-only")
	eventually(t, "the report", func() bool { return len(got.list()) == 1 })
	binding := fleet.nextBinding(t)
	if binding.Conversation != id || binding.FileWrites.Authority != wsfs.AuthorityReadOnly {
		t.Fatal("read-only worker did not bind its admitted permission")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	var names []string
	for _, tool := range model.reqs[0].Tools {
		names = append(names, tool.Name)
	}
	for _, name := range []string{"fs_read", "fs_grep", "fs_list"} {
		if !slices.Contains(names, name) {
			t.Errorf("missing %s in %v", name, names)
		}
	}
	if slices.Contains(names, "fs_edit") || slices.Contains(names, "fs_write") {
		t.Errorf("a read-only worker was offered %v", names)
	}
}

func TestWerkResumesActiveTurnAcrossRunnerReplacement(t *testing.T) {
	first := make(chan struct{}, 1)
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, _ werk.Request) (*werk.Response, error) {
			first <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		werkMessage("resumed the request"),
	}}
	h, fleet, got := werkHarness(t, model)
	id := spawnWerk(t, h, "workdir-write")
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("no initial request")
	}
	before := werkRecord(t, h, id)
	if before.DurableTurn == nil {
		t.Fatal("the initial request lacked a durable turn")
	}
	h.runner.Close()
	if len(got.list()) != 0 {
		t.Fatal("replacement reported an interrupted model request")
	}

	fleet.start(h)
	eventually(t, "the resumed turn report", func() bool { return len(got.list()) == 1 })
	after := werkRecord(t, h, id)
	if after.SessionID != before.SessionID || after.ReportID != before.DurableTurn.ID || after.DurableTurn != nil ||
		after.LastTurn.Result.Message != "resumed the request" || h.holders.opCount("spawn") != 0 {
		t.Fatal("the replacement lost the original logical turn")
	}
	assertWerkRequestInstructions(t, model, after)
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.reqs) != 2 || !slices.EqualFunc(model.reqs[0].Input, model.reqs[1].Input, func(x, y json.RawMessage) bool {
		return string(x) == string(y)
	}) {
		t.Fatal("the interrupted model input changed or was duplicated")
	}
}

func TestWerkResumesPendingSteerOnTheOriginalTurn(t *testing.T) {
	started := make(chan struct{})
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, _ werk.Request) (*werk.Response, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
		werkMessage("resumed with steering"),
		werkMessage("resumed with steering"),
	}}
	h, fleet, reports := werkHarness(t, model)
	id := spawnWerk(t, h, "workdir-write")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no initial model request")
	}
	h.send(id, "take this steer")
	c := h.runner.conv(id)
	held, err := wsinbox.Held(c.inbox)
	if err != nil || len(held) != 1 {
		t.Fatalf("expected one held steer candidate: %v, %v", held, err)
	}
	if _, err := wsinbox.Steer(c.inbox, held[0].Name); err != nil {
		t.Fatal(err)
	}
	c.signalInbox()
	eventually(t, "the durable steer queue", func() bool {
		return len(c.record().DurableSteers) == 1
	})
	var mailQueued bool
	if !c.do(func() {
		c.takeReport("child report fixture")
		mailQueued = c.steerMail(ClaimedMail{Text: "mail fixture"})
	}) {
		t.Fatal("the conversation stopped before admitting inputs")
	}
	before := c.record()
	if !mailQueued || len(before.DurableSteers) != 3 {
		t.Fatal("child report and mailbox input did not enter the durable steer queue")
	}
	steer := before.DurableSteers[0]
	if steer.ID == "" || steer.Mark <= before.TurnMark {
		t.Fatal("pending steering did not retain its own send coverage")
	}
	h.runner.Close()
	if len(reports.list()) != 0 {
		t.Fatal("the predecessor reported an abandoned turn")
	}

	var failed atomic.Bool
	h.opts.beforeDurableWrite = func(rec Record) error {
		if rec.ReplyID == id && rec.DurableTurn != nil && rec.TurnMark == steer.Mark && !failed.Swap(true) {
			return errors.New("simulated error persisting the first steer receipt")
		}
		return nil
	}
	fleet.start(h)
	eventually(t, "the original turn's report", func() bool { return len(reports.list()) == 1 })
	if !failed.Load() {
		t.Fatal("the fixture did not test a failed receipt write")
	}
	after := werkRecord(t, h, id)
	if after.ReportID != before.DurableTurn.ID || after.LastTurn == nil || after.LastTurn.Mark != steer.Mark ||
		len(after.DurableSteers) != 0 || len(after.Reports) != 0 {
		t.Fatal("the resumed steer did not settle with its original turn and coverage")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	reqs := model.reqs
	if len(reqs) < 2 || len(reqs) > 3 {
		t.Fatalf("replacing and retrying the loop yielded %d requests", len(reqs))
	}
	for _, req := range reqs[1:] {
		if len(req.Input) != 4 {
			t.Fatalf("a recovered model request had %d inputs, want 4", len(req.Input))
		}
		for i, text := range []string{"take this steer", "child report fixture", "mail fixture"} {
			if !strings.Contains(string(req.Input[i+1]), text) {
				t.Errorf("recovered input %d omitted %q", i+1, text)
			}
		}
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
	h, _, got := werkHarness(t, model)
	id := spawnWerk(t, h, "workdir-write")
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
	mk := func(level PermissionLevel, model string) StartSpec {
		return StartSpec{
			Root: t.TempDir(), Dir: t.TempDir(), Model: model, PermissionLevel: level,
			MCPSession: &MCPSession{URL: "http://127.0.0.1:1/mcp/session", Token: "tok"},
		}
	}
	b := WerkBackend{}
	if _, err := b.Start(context.Background(), mk("", "vendor/m")); err == nil || !strings.Contains(err.Error(), "authority is required") {
		t.Errorf("no authority: %v", err)
	}
	if _, err := b.Start(context.Background(), mk(PermissionFull, "")); err == nil || !strings.Contains(err.Error(), "tier model is required") {
		t.Errorf("no model: %v", err)
	}
	if _, err := b.Start(context.Background(), StartSpec{Lead: &LeadSpec{}}); !errors.Is(err, errWerkLead) {
		t.Errorf("a lead: %v", err)
	}
	spec := mk(PermissionFull, "vendor/m")
	spec.MCPSession = nil
	if _, err := b.Start(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "authenticated MCP session is required") {
		t.Errorf("no MCP binding: %v", err)
	}
}

func TestWerkRefusesToStartWhenItsInstructionsCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	spec := StartSpec{
		Dir: dir, Root: dir, Model: "fixture-model", PermissionLevel: PermissionReadOnly,
		Instructions: filepath.Join(dir, "missing.md"),
		MCPSession:   &MCPSession{URL: "http://127.0.0.1:1", Token: "fixture"},
	}
	loop, err := startWerkLoop(context.Background(), spec, &werkModel{})
	if loop != nil {
		_ = loop.Close()
	}
	if !errors.Is(err, os.ErrNotExist) || loop != nil {
		t.Fatalf("start with unreadable instructions: %v", err)
	}
}

// Availability: no OpenRouter key is a reason in agent.tiers and the
// dashboard's check; no tier model is another; with both, werk lists tiers.
func TestWerkAvailabilityNamesWhatIsMissing(t *testing.T) {
	configured := false
	h := newLeadHarness(t)
	_, reg := WerkBackend{credentialStatus: func(string) (bool, error) { return configured, nil }}.registration()
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
	if err == nil || !strings.Contains(err.Error(), "werk.small has no usable OpenRouter model") {
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
	m := newWerkMapper("")

	if evs := m.mapEvent(werk.Line{T: werk.LineInit, Session: "abc123", Version: werk.ProtocolVersion}); len(evs) != 2 || evs[0].SessionID != "abc123" || evs[1].Harness == nil || evs[1].Harness.Version != werk.ProtocolVersion {
		t.Errorf("init = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineTurnStart, Turn: 1}); len(evs) != 1 || !evs[0].TurnStart {
		t.Errorf("turn_start = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineReply, Turn: 1, Item: "m1", Text: "partial"}); len(evs) != 1 || evs[0].Live.Kind != wslive.KindReply || evs[0].Live.Final || evs[0].Live.Source != "" {
		t.Errorf("streamed reply = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineReply, Turn: 1, Item: "m1", Text: "whole", Final: true}); len(evs) != 1 || !evs[0].Live.Final || evs[0].Live.Source != "werk/abc123/1/m1" {
		t.Errorf("final reply = %+v", evs)
	}
	cost := 0.5
	cost2 := 0.25
	evs := m.mapEvent(werk.Line{T: werk.LineUsage, Turn: 1, InputTokens: 1000, CachedTokens: 800, OutputTokens: 50, Context: 1050, Window: 10500, CostUSD: &cost})
	if len(evs) != 1 || evs[0].Live.Kind != wslive.KindStatus || *evs[0].Live.Context.Tokens != 1050 || *evs[0].Live.Context.Percent != 10 || *evs[0].Live.Context.CacheHit != 80 || evs[0].Live.Cost.USD != 0.5 {
		t.Errorf("usage = %+v", evs[0].Live)
	}
	m.mapEvent(werk.Line{T: werk.LineToolStart, Turn: 1, Call: "c1", Name: "exec_shell", Args: `{"cmd":"ls"}`})
	evs = m.mapEvent(werk.Line{T: werk.LineToolEnd, Turn: 1, Call: "c1", Name: "exec_shell", Output: "x"})
	if len(evs) != 1 || !evs[0].ToolBoundary || evs[0].Live.Kind != wslive.KindAgent || len(evs[0].Live.Batches) != 1 {
		t.Errorf("tool_end = %+v", evs)
	}
	evs = m.mapEvent(werk.Line{T: werk.LineUsage, Turn: 1, InputTokens: 10, CostUSD: &cost2})
	if evs[0].Live.Cost.USD != 0.75 {
		t.Errorf("cost does not accumulate over responses: %+v", evs[0].Live.Cost)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineRetry, Attempt: 2, MaxRetries: 5, Status: 429, Error: "slow down"}); len(evs) != 1 || evs[0].Retry == nil || evs[0].Retry.Attempt != 2 || evs[0].Retry.Status != 429 {
		t.Errorf("retry = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineSteerAck, ID: "s1"}); len(evs) != 1 || evs[0].SteerAnswer == nil || evs[0].SteerAnswer.Refusal != "" {
		t.Errorf("steer ack = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineSteerAck, ID: "s2", Refusal: "no turn is running"}); evs[0].SteerAnswer.Refusal != "no turn is running" {
		t.Errorf("steer refusal = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineUserTaken, Text: "also X"}); len(evs) != 1 || evs[0].UserTaken != "also X" {
		t.Errorf("user_taken = %+v", evs)
	}
	if evs := m.mapEvent(werk.Line{T: werk.LineNotice, Text: "careful"}); len(evs) != 1 || evs[0].Notice != "careful" {
		t.Errorf("notice = %+v", evs)
	}
	for _, quiet := range []werk.Line{{T: werk.LineUser, Text: "hi"}, {T: werk.LineReasoning, Text: "thinking"}} {
		if evs := m.mapEvent(quiet); len(evs) != 0 {
			t.Errorf("%s mapped to %+v", quiet.T, evs)
		}
	}

	// An open batch is finished by the turn end, which names its reason.
	m.mapEvent(werk.Line{T: werk.LineToolStart, Turn: 2, Call: "c9", Name: "read"})
	evs = m.mapEvent(werk.Line{T: werk.LineTurnEnd, Turn: 2, Reason: werk.ReasonAborted})
	last := evs[len(evs)-1]
	if len(evs) != 3 || !last.TurnEnd || last.TurnReason != wslive.TurnAborted || last.TurnError != "" || !evs[1].RetryEnd {
		t.Errorf("aborted turn end = %+v", evs)
	}
	evs = m.mapEvent(werk.Line{T: werk.LineTurnEnd, Turn: 3, Reason: werk.ReasonError, Error: "the model response failed (server_error)"})
	if last := evs[len(evs)-1]; !last.TurnEnd || last.TurnError != "the model response failed (server_error)" {
		t.Errorf("error turn end = %+v", evs)
	}
	evs = m.mapEvent(werk.Line{T: werk.LineTurnEnd, Turn: 4, Reason: werk.ReasonDone})
	if last := evs[len(evs)-1]; !last.TurnEnd || last.TurnError != "" || last.TurnReason != "" {
		t.Errorf("done turn end = %+v", evs)
	}
}

func TestWerkWakeResumesTheConversationFromItsLog(t *testing.T) {
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		werkMessage("first answer"),
		werkMessage("second answer"),
	}}
	h, _, got := werkHarness(t, model)
	id := spawnWerk(t, h, "workdir-write")
	eventually(t, "the first report", func() bool { return len(got.list()) == 1 })
	session := werkRecord(t, h, id).SessionID
	if session == "" {
		t.Fatal("the process reported no session id")
	}
	if _, err := h.runner.Stop(id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the process to end", func() bool { return !h.state(id).Process })

	h.send(id, "and now?")
	eventually(t, "the second report", func() bool { return len(got.list()) == 2 })
	if rep := got.list()[1]; rep.Result.Status != TurnDone || rep.Result.Message != "second answer" {
		t.Fatalf("second report = %+v", rep.Result)
	}
	if woken := werkRecord(t, h, id).SessionID; woken != session || h.holders.opCount("spawn") != 0 {
		t.Fatal("the woken daemon loop changed sessions or launched a holder")
	}
	assertWerkRequestInstructions(t, model, werkRecord(t, h, id))
	model.mu.Lock()
	second := model.reqs[1].Input
	model.mu.Unlock()
	if len(second) != 3 || !strings.Contains(string(second[1]), "first answer") {
		t.Errorf("the second request lost the first turn: %s", second)
	}
}

func TestWerkReconcilesSettlementAcrossDaemonReplacement(t *testing.T) {
	for _, tc := range []struct {
		name               string
		beforeCommit, held bool
	}{
		{name: "before terminal commit", beforeCommit: true},
		{name: "after terminal commit"},
		{name: "before held commit", beforeCommit: true, held: true},
		{name: "after held release commit", held: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, finish := make(chan struct{}), make(chan struct{})
			const answer = "the written turn result"
			model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
				func(ctx context.Context, req werk.Request) (*werk.Response, error) {
					close(started)
					select {
					case <-finish:
						return werkMessage(answer)(ctx, req)
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			}}
			h, fleet, reports := werkHarness(t, model)
			jobs := h.execJobs.(*fakeExecJobs)
			h.runner.Close()
			var attempts atomic.Int32
			h.opts.beforeDurableWrite = func(rec Record) error {
				if tc.beforeCommit && rec.DurableTurn == nil && (rec.ReportID != "" || rec.DurableHeldReportID != "") {
					attempts.Add(1)
					return errors.New("fixture lost before turn settlement")
				}
				return nil
			}
			stoppedBeforeReport := make(chan Record, 1)
			if !tc.beforeCommit {
				h.opts.suppressReportDelivery = func(rec Record) bool {
					select {
					case stoppedBeforeReport <- rec:
					default:
					}
					return true
				}
			}
			fleet.start(h)
			id := spawnWerk(t, h, "read-only")
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("the first turn did not reach the model")
			}
			original := werkRecord(t, h, id)
			if original.DurableTurn == nil {
				t.Fatal("the model started without the admitted turn")
			}
			turnID := original.DurableTurn.ID
			if tc.held {
				jobs.set(id, 1)
				h.runner.ExecJobsChanged()
			}
			close(finish)
			if tc.beforeCommit {
				eventually(t, "an unsuccessful settlement", func() bool {
					return attempts.Load() >= 2 && werkRecord(t, h, id).DurableTurn != nil
				})
			} else if tc.held {
				eventually(t, "the durable report hold", func() bool {
					return werkRecord(t, h, id).DurableHeldReportID == turnID && h.state(id).Holding
				})
				if len(reports.list()) != 0 {
					t.Fatal("the runner reported before the held job ended")
				}
				jobs.set(id, 0)
				h.runner.ExecJobsChanged()
			}
			var settled Record
			if !tc.beforeCommit {
				select {
				case settled = <-stoppedBeforeReport:
				case <-time.After(5 * time.Second):
					t.Fatal("the settled turn did not reach report delivery")
				}
				if settled.DurableTurn != nil || settled.HeldTurn != nil || settled.DurableHeldReportID != "" ||
					settled.ReportID != turnID || settled.LastReportedTurn != "" ||
					settled.LastTurn == nil || settled.LastTurn.Result.Message != answer {
					t.Fatal("report delivery reached the wrong settlement boundary")
				}
			}
			if len(reports.list()) != 0 {
				t.Fatal("the first runner delivered a report")
			}
			h.runner.Close()
			h.opts.beforeDurableWrite = nil
			h.opts.suppressReportDelivery = nil
			fleet.start(h)
			if tc.held && tc.beforeCommit {
				eventually(t, "the successor's report hold", func() bool {
					r := werkRecord(t, h, id)
					return r.DurableHeldReportID == turnID && h.state(id).Holding
				})
				if len(reports.list()) != 0 {
					t.Fatal("the successor reported before the held job ended")
				}
				jobs.set(id, 0)
				h.runner.ExecJobsChanged()
			}
			eventually(t, "one original report", func() bool {
				return werkRecord(t, h, id).LastReportedTurn == turnID
			})
			restored := werkRecord(t, h, id)
			if restored.ReportID != turnID || restored.LastTurn == nil {
				t.Fatal("replacement changed the report identity")
			}
			if settled.LastTurn != nil {
				// Compare the persisted fields, excluding the loop's local turn counters.
				before, err := json.Marshal(settled.LastTurn)
				if err != nil {
					t.Fatal(err)
				}
				after, err := json.Marshal(restored.LastTurn)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) {
					t.Fatal("replacement changed the committed turn result")
				}
			}
			h.runner.Close()
			got := reports.list()
			if len(got) != 1 || got[0].Result.Message != answer || model.requestCount() != 1 {
				t.Fatal("replacement repeated the finished model call or changed its report")
			}
		})
	}
}
