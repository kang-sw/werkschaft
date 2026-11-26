package mcp

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wswake"
)

// fakePiAgents is AgentHost over an in-memory list of children.
type fakePiAgents struct {
	mu       sync.Mutex
	n        int
	specs    []wsagent.SpawnSpec
	states   map[string]wsagent.State
	records  map[string]wsagent.Record
	order    []string
	sent     map[string][]string
	watchers map[int]func(wsagent.State)
	watchN   int
	waited   []time.Duration
	waitRes  wsagent.TurnResult
	// agent is the agent.* side (agent_tools_test.go).
	agent fakeAgentSide
}

func newFakePiAgents() *fakePiAgents {
	return &fakePiAgents{states: map[string]wsagent.State{}, records: map[string]wsagent.Record{}, sent: map[string][]string{}, watchers: map[int]func(wsagent.State){}}
}

func (f *fakePiAgents) RecordByReplyID(replyID string) (wsagent.Record, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[replyID]
	return rec, ok
}

func (f *fakePiAgents) Spawn(spec wsagent.SpawnSpec) (wsagent.State, error) {
	f.mu.Lock()
	f.n++
	id := fmt.Sprintf("child%d", f.n)
	parent := *spec.Parent
	st := wsagent.State{ID: id, Profile: spec.Profile, Root: spec.Root, Status: wsagent.StatusRunning, Model: spec.Model, Effort: spec.Effort, Label: spec.Label, Parent: &parent}
	if spec.Cwd != spec.Root {
		st.Cwd = spec.Cwd
	}
	f.specs = append(f.specs, spec)
	f.states[id] = st
	f.order = append(f.order, id)
	f.mu.Unlock()
	f.emit(st)
	return st, nil
}

func (f *fakePiAgents) Deliver(id string, d wsagent.Delivery) (wsagent.State, error) {
	return f.Send(id, d.Text)
}

func (f *fakePiAgents) Send(id, text string) (wsagent.State, error) {
	f.mu.Lock()
	f.sent[id] = append(f.sent[id], text)
	st := f.states[id]
	f.mu.Unlock()
	return st, nil
}

func (f *fakePiAgents) Get(id string) (wsagent.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[id]
	if !ok {
		return wsagent.State{}, errors.New("unknown agent conversation")
	}
	return st, nil
}

func (f *fakePiAgents) Lineage(id string) (wsagent.State, error) { return f.Get(id) }

func (f *fakePiAgents) Children(parent wsagent.ParentRef) []wsagent.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []wsagent.State
	for _, id := range f.order {
		if st := f.states[id]; st.Parent != nil && *st.Parent == parent {
			out = append(out, st)
		}
	}
	return out
}

func (f *fakePiAgents) CloseConversation(id string) (wsagent.State, error) {
	return f.set(id, func(st *wsagent.State) { st.Status = wsagent.StatusClosed }), nil
}

func (f *fakePiAgents) Watch(fn func(wsagent.State)) func() {
	f.mu.Lock()
	f.watchN++
	n := f.watchN
	f.watchers[n] = fn
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.watchers, n)
		f.mu.Unlock()
	}
}

func (f *fakePiAgents) Transcript(id string) ([]wslive.RingRecord, error) {
	at := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	return []wslive.RingRecord{
		{RingEntry: wslive.RingEntry{Kind: wslive.RingUser, Text: "do it"}, At: at},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{Calls: 3, Tools: []wslive.ToolTally{{Name: "bash", Calls: 3}}}}, At: at},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingReply, Text: "done it"}, At: at},
	}, nil
}

// set changes a child's state and tells the watchers, as the runner does.
func (f *fakePiAgents) set(id string, change func(*wsagent.State)) wsagent.State {
	f.mu.Lock()
	st := f.states[id]
	change(&st)
	f.states[id] = st
	f.mu.Unlock()
	f.emit(st)
	return st
}

func (f *fakePiAgents) emit(st wsagent.State) {
	f.mu.Lock()
	fns := make([]func(wsagent.State), 0, len(f.watchers))
	for _, fn := range f.watchers {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

func (f *fakePiAgents) watcherCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.watchers)
}

// piFixture is a lead session on a root whose launcher loaded manifests,
// with a server built over agents (nil: the direct path).
type piFixture struct {
	root   string
	key    string
	server *Server
}

func newPiFixture(t *testing.T, manifests string, agents AgentHost) piFixture {
	t.Helper()
	setupMailboxTestEnv(t)
	t.Setenv(envConfigManifests, manifests)
	root := t.TempDir()
	initGit(t, root)
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: agents})
	t.Cleanup(s.Close)
	return piFixture{root: root, key: mailboxLogin(t, s, 1, root), server: s}
}

func wsManifests() string {
	pkg := dashboardPackages[0]
	return pkg.own + ";" + pkg.dashboard
}

func listedPiTools(s *Server) []string {
	var out []string
	for _, tool := range s.filteredTools() {
		if name, _ := tool["name"].(string); isPiTool(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func TestAgentTurnReporterMailsTheParent(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key := mailboxLogin(t, s, 1, root)
	sub := wswake.Mailbox.Subscribe()
	defer sub.Close()

	report := AgentTurnReporter(io.Discard)
	report(wsagent.TurnReport{
		Parent: wsagent.ParentRef{Kind: "pi", ReplyID: wsmailbox.ReplyID(key)},
		Result: wsagent.TurnResult{ID: "childone", Status: wsagent.TurnDone},
	})
	select {
	case <-sub.Hard():
		t.Fatalf("a parent that is no lead gets no mail")
	default:
	}
	report(wsagent.TurnReport{
		Parent: wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: wsmailbox.ReplyID(key)},
		Child:  wsagent.State{ID: "childone", Label: "worker", Status: wsagent.StatusClosed},
		Result: wsagent.TurnResult{ID: "childone", Status: wsagent.TurnDone, Message: "all green"},
	})
	select {
	case <-sub.Hard():
	case <-time.After(time.Second):
		t.Fatalf("the report did not wake the mailbox waits")
	}
	report(wsagent.TurnReport{
		Parent: wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: wsmailbox.ReplyID(key)},
		Result: wsagent.TurnResult{ID: "childone", Status: wsagent.TurnInterrupted, Error: "the dashboard daemon exited while the turn ran"},
	})
	out := callToolWithKey(t, s, 2, key, "mailbox.recv", nil)
	for _, want := range []string{`agent:childone: Reply with mailbox.send(to: "id:childone").` + "\n" + `[subagent id:childone "worker"] turn done`, "all green", "[subagent id:childone] turn interrupted: the dashboard daemon exited while the turn ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("mailbox.recv lacks %q: %q", want, out)
		}
	}
	if strings.Contains(out, "pi.send") || strings.Count(out, "[subagent") != 2 {
		t.Fatalf("one mail per lead report: %q", out)
	}
	if entry, _ := replyEntryFor(t, key); len(entry.Contacts) != 0 {
		t.Fatalf("display labels must not become contacts: %+v", entry.Contacts)
	}
}

func TestAgentTurnReporterCustomTextPreservesFirstLine(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	key := mailboxLogin(t, s, 1, root)
	sub := wswake.Mailbox.Subscribe()
	defer sub.Close()

	custom := "QUOTA EXHAUSTED: worker (id:childone)\nbackend: claude\nmodel: claude-large\nerror: usage limit\nresets_at: unknown\nskipped_ancestors: none\naction:\n- the worker keeps its conversation: resume it with mailbox.send(to: \"id:childone\") once its quota is back\n"
	rep := wsagent.TurnReport{
		Parent: wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: wsmailbox.ReplyID(key)},
		Child:  wsagent.State{ID: "childone", Label: "worker"},
		Result: wsagent.TurnResult{ID: "childone", Status: wsagent.TurnFailed, Error: "usage limit"},
		Text:   custom,
	}
	if got := wsagent.ReportText(rep); got != custom {
		t.Fatalf("ReportText(custom) = %q, want %q", got, custom)
	}
	report := AgentTurnReporter(io.Discard)
	report(rep)
	select {
	case <-sub.Hard():
	case <-time.After(time.Second):
		t.Fatalf("custom report did not wake mailbox waits")
	}
	out := callToolWithKey(t, s, 2, key, "mailbox.recv", nil)
	if !strings.Contains(out, "agent:childone: QUOTA EXHAUSTED: worker (id:childone)\n") {
		t.Fatalf("custom report must start with QUOTA EXHAUSTED header, got: %q", out)
	}
	if strings.Contains(out, "Reply with mailbox.send(to: \"id:childone\").\nQUOTA EXHAUSTED") {
		t.Fatalf("custom report must not get generic Reply prefix: %q", out)
	}
	if strings.Contains(out, "[subagent") {
		t.Fatalf("custom report must replace generic [subagent] report: %q", out)
	}
}

// liveAgents is the agents of key's live state in s.

func TestRetiredPiListUsesCallerOwnership(t *testing.T) {
	f, foreign := piHistoryFixture(t)
	raw := callToolOnce(t, f.server, 2, "pi.list", map[string]any{"session_key": f.key, "format": "json"})
	for _, unwanted := range []string{"child2", "child3", "sleep"} {
		if strings.Contains(raw, unwanted) {
			t.Errorf("pi.list includes %q: %s", unwanted, raw)
		}
	}
	if !strings.Contains(raw, "child1") || !strings.Contains(raw, "closed") {
		t.Fatalf("pi.list omitted ended Pi history: %s", raw)
	}
	other := callToolOnce(t, f.server, 3, "pi.list", map[string]any{"session_key": foreign})
	if !strings.Contains(other, "child3") || strings.Contains(other, "child1") {
		t.Fatalf("pi.list for the other lead: %s", other)
	}
}

func TestRetiredPiTranscriptUsesCallerOwnership(t *testing.T) {
	f, foreign := piHistoryFixture(t)
	read := func(key, id string) string {
		return callToolOnce(t, f.server, 2, "pi.transcript", map[string]any{"session_key": key, "id": id, "limit": 1})
	}
	if raw := read(f.key, "child1"); !strings.Contains(raw, "done it") || strings.Contains(raw, "do it") {
		t.Fatalf("pi.transcript missed the retained reply: %s", raw)
	}
	for name, raw := range map[string]string{
		"foreign":       read(foreign, "child1"),
		"other backend": read(f.key, "child2"),
	} {
		if !strings.Contains(raw, "not one of this session's Pi subagents") || strings.Contains(raw, "done it") {
			t.Errorf("%s transcript lacked ownership refusal: %s", name, raw)
		}
	}
}

func TestRetiredPiExecutionToolsAreHiddenAndRefuse(t *testing.T) {
	f := newPiFixture(t, "", nil)
	if got := listedPiTools(f.server); len(got) != 0 {
		t.Fatalf("legacy Pi tools are visible: %v", got)
	}
	for _, tool := range f.server.filteredTools() {
		if name, _ := tool["name"].(string); strings.HasPrefix(name, "pi.") {
			t.Fatalf("legacy Pi tool %q leaked into tools/list", name)
		}
	}
	for _, tool := range []string{"pi.spawn", "pi.send", "pi.stop", "pi.wait"} {
		raw := callToolOnce(t, f.server, 2, tool, map[string]any{"session_key": f.key})
		if !strings.Contains(raw, "-32602") || !strings.Contains(raw, "unknown tool: "+tool) {
			t.Errorf("%s = %s", tool, raw)
		}
	}
}

func piHistoryFixture(t *testing.T) (piFixture, string) {
	t.Helper()
	agents := newFakePiAgents()
	// The empty manifest list omits the old dashboard opt-in.
	f := newPiFixture(t, "", agents)
	foreign := mailboxLogin(t, f.server, 5, f.root)
	for _, fixture := range []struct{ key, backend string }{
		{f.key, wsagent.BackendPi}, {f.key, wsagent.BackendClaude}, {foreign, wsagent.BackendPi},
	} {
		parent := wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: f.server.mailboxReplyID(fixture.key)}
		child, err := agents.Spawn(wsagent.SpawnSpec{Root: f.root, Parent: &parent})
		if err != nil {
			t.Fatal(err)
		}
		agents.set(child.ID, func(st *wsagent.State) {
			st.Backend, st.Status = fixture.backend, wsagent.StatusClosed
		})
	}
	return f, foreign
}

type refusingHireHost struct{ err error }

func (refusingHireHost) OpenHostedSlot(string) (wsdashboard.HostedSlot, bool) { return nil, false }

func (h refusingHireHost) RequestHire(string, wsdashboard.HireRequest) (wsdashboard.HireProposal, error) {
	return wsdashboard.HireProposal{}, h.err
}

func TestRetiredPiHireRefuses(t *testing.T) {
	f := newPiFixture(t, "", nil)
	f.server.live.host = refusingHireHost{err: errors.New("the requested harness does not support hosted leads")}
	raw := callToolOnce(t, f.server, 2, "session.request_hire", map[string]any{
		"session_key": f.key, "harness": "pi", "reason": "work", "recovery_context": "context",
	})
	if !strings.Contains(raw, "does not support hosted leads") || strings.Contains(raw, "Pi harness is retired") {
		t.Fatalf("session.request_hire = %s", raw)
	}
}
