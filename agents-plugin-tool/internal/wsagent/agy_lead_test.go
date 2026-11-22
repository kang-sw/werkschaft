package wsagent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// agyFake is an ACP agent on a fake holder's child: it answers the
// handshake and the controls ws writes, and reaches the ws MCP route with
// the token the session request carries, as agy does before it answers
// that request, unless refuseMCP is set (agy drops a refused server
// silently). Prompts it leaves unanswered: a test writes the turn.
type agyFake struct {
	h         *leadHarness
	mu        sync.Mutex
	refuseMCP bool
	tokens    []string
	// replyID names the session whose registration a session request
	// reads; rowPIDs are the host_pid it named at each one.
	replyID string
	rowPIDs []int
	// model is the session's model: a Claude model offers no thought
	// level, as in agy.
	model string
}

func newAgyLeadHarness(t *testing.T) (*leadHarness, *agyFake) {
	t.Helper()
	h := newLeadHarness(t)
	h.opts.Backends[BackendAgy] = BackendRegistration{Backend: AgyBackend{Command: "/opt/harness/agy"}, LeadProfile: LeadProfileAgy, PermissionModes: AgyPermissionModes, Catalog: AgyCatalog()}
	h.opts.WSMCP = func(root string) (MCPCommand, error) {
		return MCPCommand{Command: "python3", Env: map[string]string{"WS_MCP_PROJECT_ROOT": root}}, nil
	}
	f := &agyFake{h: h}
	h.holders.acp = f.answer
	return h, f
}

func TestAgyActivityCallbackOnSettledHostedLeadTurn(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	var calls atomic.Int64
	h.opts.AgyActivity = func() { calls.Add(1) }
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`)
	h.idle(id)
	if got := calls.Load(); got != 1 {
		t.Fatalf("settled hosted lead raised %d activity callbacks, want 1", got)
	}
}

// config is the session's configOptions for its model, with option id's
// current value set to value.
func (f *agyFake) config(id, value string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "model" {
		f.model = value
	}
	if f.model == "" {
		f.model = "gemini-3.8-flash"
	}
	opts := []map[string]any{{"id": "model", "currentValue": f.model, "options": []map[string]string{{"value": "gemini-3.8-flash"}, {"value": "claude-sonnet-4-6"}}}}
	if !strings.HasPrefix(f.model, "claude-") {
		level := "medium"
		if id == "thought_level" {
			level = value
		}
		opts = append(opts, map[string]any{"id": "thought_level", "currentValue": level, "options": []map[string]string{{"value": "low"}, {"value": "medium"}, {"value": "high"}}})
	}
	raw, _ := json.Marshal(opts)
	return string(raw)
}

func (f *agyFake) answer(_, method string, params json.RawMessage) (string, string, bool) {
	switch method {
	case "initialize":
		return `{"protocolVersion":1,"agentInfo":{"name":"Antigravity ACP Server","version":"1.5.10"},"agentCapabilities":{"sessionCapabilities":{"resume":{}}}}`, "", true
	case "session/new", "session/resume":
		var p struct {
			SessionID  string `json:"sessionId"`
			MCPServers []struct {
				Headers []struct {
					Value string `json:"value"`
				} `json:"headers"`
			} `json:"mcpServers"`
		}
		_ = json.Unmarshal(params, &p)
		token := strings.TrimPrefix(p.MCPServers[0].Headers[0].Value, "Bearer ")
		f.mu.Lock()
		f.tokens = append(f.tokens, token)
		refuse := f.refuseMCP
		if row, err := wsdashboard.ReadRegistration(f.h.dir, f.replyID); err == nil {
			f.rowPIDs = append(f.rowPIDs, row.HostPID)
		} else {
			f.rowPIDs = append(f.rowPIDs, -1)
		}
		f.mu.Unlock()
		if !refuse {
			f.h.runner.ResolveMCPToken(token)
		}
		sid := p.SessionID
		if sid == "" {
			sid = "agy-s1"
		}
		return `{"sessionId":"` + sid + `","modes":{"currentModeId":"default","availableModes":[{"id":"default"},{"id":"accept-edits"},{"id":"auto"},{"id":"plan"}]},"configOptions":` + f.config("", "") + `}`, "", true
	case "session/set_config_option":
		var p struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		_ = json.Unmarshal(params, &p)
		return `{"configOptions":` + f.config(p.ConfigID, p.Value) + `}`, "", true
	case "session/set_mode":
		return `{}`, "", true
	}
	return "", "", false
}

func acpField(t *testing.T, raw json.RawMessage, path ...string) any {
	t.Helper()
	return jsonAt(raw, path...)
}

// promptIDs are the ids of the prompts written to the holder's child.
func agyPromptIDs(h *leadHarness, holder string) []string {
	var out []string
	for _, line := range h.holders.inputs(holder, "") {
		var d struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(line), &d) == nil && d.Method == "session/prompt" {
			out = append(out, d.ID)
		}
	}
	return out
}

// TestAgyLeadOverACP: a hosted agy lead starts `agy acp
// --process-per-session` under a holder, opens its session with the ws
// MCP server and its token in the session request (never in argv or the
// environment), applies the conversation's model, effort and mode, sends
// the turn as a prompt, takes an approval by its tool call, and ends the
// turn at the prompt's answer; a later daemon rejoins the process without
// a new handshake and its next turn goes to the same session.
func TestAgyLeadOverACP(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	const endpoint = "http://127.0.0.1:1"
	h.startAt(endpoint)
	id := h.lead(BackendAgy, LeadOptions{Model: "claude-sonnet-4-6", Effort: "low", PermissionMode: "auto"}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })

	spawn := h.holders.spawned(holder)
	if n := len(spawn.Argv); n < 3 || !slices.Equal(spawn.Argv[n-2:], []string{"acp", "--process-per-session"}) {
		t.Fatalf("argv %q", spawn.Argv)
	}
	rec := h.record(id)
	token := rec.MCPToken
	if len(token) != 64 || strings.Contains(strings.Join(spawn.Argv, " "), token) {
		t.Fatalf("a token is drawn and stays out of argv: %q", token)
	}
	for name, value := range spawn.Env {
		if strings.Contains(value, token) || strings.HasPrefix(name, "WS_MCP_") || strings.HasPrefix(name, "CLAUDE") || name == "WS_MAILBOX" {
			t.Fatalf("the lead's environment carries %s=%s", name, value)
		}
	}
	if spawn.Env[EnvHostedSession] != "1" || spawn.Env[EnvHostedSessionKey] != rec.WSKey {
		t.Fatalf("the hosted-session variables: %v", spawn.Env)
	}
	news := h.holders.acpInputs(holder, "session/new")
	if len(news) != 1 || h.holders.acpInputs(holder, "initialize") == nil {
		t.Fatalf("one handshake: %d session/new", len(news))
	}
	if acpField(t, news[0], "mcpServers") == nil || !sameDir(acpField(t, news[0], "cwd").(string), h.root) {
		t.Fatalf("session/new %s", news[0])
	}
	var server struct {
		MCPServers []struct {
			Type, Name, URL string
			Headers         []struct{ Name, Value string }
		} `json:"mcpServers"`
	}
	_ = json.Unmarshal(news[0], &server)
	if s := server.MCPServers; len(s) != 1 || s[0].Type != "http" || s[0].Name != ReservedMCPServer || s[0].URL != endpoint+wsdashboard.ProxyHTTPPath ||
		len(s[0].Headers) != 1 || s[0].Headers[0].Name != "Authorization" || s[0].Headers[0].Value != "Bearer "+token {
		t.Fatalf("the ws MCP server entry: %+v", s)
	}
	var settings []string
	for _, p := range h.holders.acpInputs(holder, "session/set_config_option") {
		settings = append(settings, acpField(t, p, "configId").(string)+"="+acpField(t, p, "value").(string))
	}
	for _, p := range h.holders.acpInputs(holder, "session/set_mode") {
		settings = append(settings, "mode="+acpField(t, p, "modeId").(string))
	}
	// A Claude model offers no thought level: the effort waits for one
	// that does.
	if !slices.Equal(settings, []string{"model=claude-sonnet-4-6", "mode=auto"}) {
		t.Fatalf("settings applied at start: %v", settings)
	}
	prompt := h.holders.acpInputs(holder, "session/prompt")[0]
	if acpField(t, prompt, "sessionId") != "agy-s1" || acpField(t, prompt, "prompt") == nil {
		t.Fatalf("the prompt: %s", prompt)
	}
	var text struct {
		Prompt []struct{ Type, Text string } `json:"prompt"`
	}
	_ = json.Unmarshal(prompt, &text)
	if len(text.Prompt) != 1 || text.Prompt[0].Text != h.envelopes("hello")[0] {
		t.Fatalf("the prompt text: %+v", text.Prompt)
	}
	eventually(t, "the session id recorded", func() bool { return h.record(id).SessionID == "agy-s1" })
	if hello, _, ok := h.runner.ResolveMCPToken(token); !ok || hello.Harness != BackendAgy || !sameDir(hello.ProjectRoot, h.root) || hello.Env[EnvHostedSessionKey] != rec.WSKey {
		t.Fatalf("the token resolves to the lead's ws-mcp context: %+v %v", hello, ok)
	}
	f.mu.Lock()
	tokens, rowPIDs := slices.Clone(f.tokens), slices.Clone(f.rowPIDs)
	f.mu.Unlock()
	if len(tokens) != 1 || tokens[0] != token {
		t.Fatalf("agy got the recorded token: %v", tokens)
	}
	// ws-mcp confirms the hosted session by the row's host_pid while agy
	// connects inside its session request.
	if len(rowPIDs) != 1 || rowPIDs[0] != rec.HostPID || rec.HostPID == 0 {
		t.Fatalf("the row named %v at the session request; the process is %d", rowPIDs, rec.HostPID)
	}

	// An approval, keyed by its tool call, answered by option kind with
	// the request's raw id.
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"working"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call","toolCallId":"c1","name":"run_command","status":"in_progress","args":{"CommandLine":"ls"}}}}`,
		`{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"c1","name":"run_command","title":"ls","args":{"CommandLine":"ls"}},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}]},"id":"1"}`,
	)
	eventually(t, "the approval", func() bool {
		a := h.state(id).Approvals
		return len(a) == 1 && a[0].ID == "c1" && a[0].Tool == "run_command"
	})
	if _, err := h.runner.AnswerApproval(id, "c1", true, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the answer", func() bool {
		return slices.ContainsFunc(h.holders.inputs(holder, ""), func(line string) bool {
			return strings.Contains(line, `"id":"1"`) && strings.Contains(line, `"optionId":"allow"`)
		})
	})
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","name":"run_command","status":"completed"}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`,
	)
	h.idle(id)
	if st := h.state(id); len(st.Approvals) != 0 || st.Error != "" {
		t.Fatalf("after the turn: %+v", st)
	}

	// Back on a model with a thought level, the effort is applied again,
	// in place.
	gemini := "gemini-3.8-flash"
	if _, err := h.runner.SetSettings(id, &gemini, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the switch", func() bool { return len(h.holders.acpInputs(holder, "session/set_config_option")) == 3 })
	var switched []string
	for _, p := range h.holders.acpInputs(holder, "session/set_config_option")[1:] {
		switched = append(switched, acpField(t, p, "configId").(string)+"="+acpField(t, p, "value").(string))
	}
	if !slices.Equal(switched, []string{"model=gemini-3.8-flash", "thought_level=low"}) || h.holders.opCount("spawn") != 1 {
		t.Fatalf("the model switch: %v %v", switched, h.holders.opList())
	}
	eventually(t, "the switch settled", func() bool { st := h.state(id); return st.Model == gemini && st.Error == "" })

	// The next daemon rejoins: no new process, no new handshake, the token
	// restored, the next turn on the same session.
	h.runner.Close()
	h.holders.restart()
	h.startAt(endpoint)
	eventually(t, "the token restored", func() bool {
		hello, _, ok := h.runner.ResolveMCPToken(token)
		return ok && hello.Harness == BackendAgy
	})
	h.send(id, "again")
	eventually(t, "the second prompt", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
	if h.holders.opCount("spawn") != 1 || len(h.holders.acpInputs(holder, "initialize")) != 1 {
		t.Fatalf("a rejoin starts nothing: %v", h.holders.opList())
	}
	if got := h.holders.acpInputs(holder, "session/prompt")[1]; acpField(t, got, "sessionId") != "agy-s1" {
		t.Fatalf("the rejoined prompt: %s", got)
	}
	ids := agyPromptIDs(h, holder)
	if ids[0] == ids[1] {
		t.Fatalf("a later daemon's prompt ids are its own: %v", ids)
	}
	h.holders.out(holder, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+ids[1]+`"}`)
	h.idle(id)
	st, err := h.runner.HandOff(id)
	if err != nil || st.Status != StatusHandedOff || st.ResumeCommand != "cd "+shellQuote(h.root)+" && agy --conversation agy-s1" {
		t.Fatalf("agy handoff = %+v, %v", st, err)
	}
}

// TestAgyLeadRejoinOfAnUnfinishedStartRestarts: a daemon that died between
// Spawned naming the process and the start's end left a holder in the
// record with no token. agy has no stdio fallback, so a later daemon does
// not run that process on without ws tools: it restarts it with a new
// token before it takes input.
func TestAgyLeadRejoinOfAnUnfinishedStartRestarts(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	const endpoint = "http://127.0.0.1:1"
	h.startAt(endpoint)
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`)
	h.idle(id)
	token := h.record(id).MCPToken

	h.runner.Close()
	h.holders.restart()
	h.editRecord(id, func(m map[string]any) {
		for _, k := range []string{"mcp_token_secret", "mcp_url", "mcp_env", "mcp_holder_id"} {
			delete(m, k)
		}
	})
	h.startAt(endpoint)
	eventually(t, "the restart", func() bool { return h.holders.opCount("stop "+holder) == 1 && h.holders.opCount("spawn") == 2 })
	eventually(t, "the new process", func() bool { st := h.state(id); return st.Process && h.holder(id) != holder })
	fresh := h.holder(id)
	eventually(t, "the new session request", func() bool { return len(h.holders.acpInputs(fresh, "session/resume")) == 1 })
	if tok := h.record(id).MCPToken; tok == "" || tok == token {
		t.Fatalf("the new process has a new token: %q", tok)
	}
	if _, _, ok := h.runner.ResolveMCPToken(token); ok {
		t.Fatal("the unrecorded token is not restored")
	}
}

// TestAgyLeadRejoinOfMovedRouteRestartsWithSessionResume: when a replacement
// daemon listens on a different MCP route than the rejoined agy process was
// started with, the runner marks the process mcpStale and restarts it at
// replay completion as a fresh process that opens with session/resume on the
// recorded session ID and a new token for the new route.
func TestAgyLeadRejoinOfMovedRouteRestartsWithSessionResume(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	const (
		endpoint1 = "http://127.0.0.1:1"
		endpoint2 = "http://127.0.0.1:2"
	)
	h.startAt(endpoint1)
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`)
	h.idle(id)

	rec1 := h.record(id)
	token1 := rec1.MCPToken
	if rec1.SessionID != "agy-s1" || rec1.MCPURL != endpoint1+wsdashboard.ProxyHTTPPath {
		t.Fatalf("before move: session=%q url=%q", rec1.SessionID, rec1.MCPURL)
	}

	// Rejoin under a daemon whose MCP HTTP route moved to a different endpoint.
	h.runner.Close()
	h.holders.restart()
	h.startAt(endpoint2)

	eventually(t, "stale route restart", func() bool {
		return h.holders.opCount("stop "+holder) == 1 && h.holders.opCount("spawn") == 2
	})
	eventually(t, "fresh agy process", func() bool {
		st := h.state(id)
		return st.Process && h.holder(id) != holder
	})
	fresh := h.holder(id)
	eventually(t, "fresh process opens with session/resume", func() bool {
		return len(h.holders.acpInputs(fresh, "session/resume")) == 1
	})
	if news := h.holders.acpInputs(fresh, "session/new"); len(news) != 0 {
		t.Fatalf("fresh process sent %d session/new requests, want 0", len(news))
	}
	resumed := h.holders.acpInputs(fresh, "session/resume")[0]
	if gotSID := acpField(t, resumed, "sessionId"); gotSID != "agy-s1" {
		t.Fatalf("session/resume sessionId = %v, want agy-s1", gotSID)
	}
	rec2 := h.record(id)
	token2 := rec2.MCPToken
	if token2 == "" || token2 == token1 {
		t.Fatalf("fresh process must mint a new token: old=%q new=%q", token1, token2)
	}
	if rec2.MCPURL != endpoint2+wsdashboard.ProxyHTTPPath {
		t.Fatalf("record MCPURL = %q, want %q", rec2.MCPURL, endpoint2+wsdashboard.ProxyHTTPPath)
	}
	var resumeParams struct {
		MCPServers []struct {
			Type, Name, URL string
			Headers         []struct{ Name, Value string }
		} `json:"mcpServers"`
	}
	_ = json.Unmarshal(resumed, &resumeParams)
	if s := resumeParams.MCPServers; len(s) != 1 || s[0].Type != "http" || s[0].Name != ReservedMCPServer ||
		s[0].URL != endpoint2+wsdashboard.ProxyHTTPPath || len(s[0].Headers) != 1 ||
		s[0].Headers[0].Name != "Authorization" || s[0].Headers[0].Value != "Bearer "+token2 {
		t.Fatalf("session/resume ws MCP server = %+v", s)
	}
	if _, _, ok := h.runner.ResolveMCPToken(token1); ok {
		t.Fatal("stale route token must not resolve")
	}
	if hello, _, ok := h.runner.ResolveMCPToken(token2); !ok || hello.Harness != BackendAgy {
		t.Fatalf("fresh token resolve = (%+v, %v), want agy", hello, ok)
	}

	// Next turn runs on the resumed session in the fresh process.
	h.send(id, "after move")
	eventually(t, "prompt on fresh process", func() bool { return len(agyPromptIDs(h, fresh)) == 1 })
	if got := h.holders.acpInputs(fresh, "session/prompt")[0]; acpField(t, got, "sessionId") != "agy-s1" {
		t.Fatalf("post-move prompt sessionId = %s", got)
	}
	h.holders.out(fresh, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, fresh)[0]+`"}`)
	h.idle(id)
	if st := h.state(id); st.Error != "" {
		t.Fatalf("post-move state error = %q", st.Error)
	}
}

// TestAgyLeadPromptCarriesTheWorkflowManual: a hosted agy lead's session
// request appends its key sentence and the manual the daemon rendered for
// agy, its directory, and its key to agy's system prompt
// (_meta.antigravity.systemPrompt.append); with no manual it appends the
// key sentence and the call to fetch it.
func TestAgyLeadPromptCarriesTheWorkflowManual(t *testing.T) {
	for name, manual := range map[string]*leadManualRender{
		"manual":    {text: "# Workflow Manual\n\n## Session Key\nrendered"},
		"no manual": nil,
	} {
		t.Run(name, func(t *testing.T) {
			h, f := newAgyLeadHarness(t)
			if manual != nil {
				h.opts.LeadManual = manual.render
			}
			h.startAt("http://127.0.0.1:1")
			id := h.lead(BackendAgy, LeadOptions{}).ID
			f.replyID = id
			h.send(id, "hello")
			holder := h.holder(id)
			eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
			key := h.record(id).WSKey
			news := h.holders.acpInputs(holder, "session/new")
			if len(news) != 1 {
				t.Fatalf("one session request: %d", len(news))
			}
			text, _ := acpField(t, news[0], "_meta", "antigravity", "systemPrompt", "append").(string)
			want := strings.TrimRight(leadKeySentence(key)+leadManualOnly, "\n") + "\n\n" + agyLeadBgShellRule + "\n"
			if manual != nil {
				want = leadKeySentence(key) + "The workflow manual below is binding and stays in this prompt across compaction. " +
					"Use the session state delivered with your first turn or in a restore bundle. For fresh session state, call the workflow_state tool with this key.\n\n" +
					manual.text + "\n\n" + agyLeadBgShellRule + "\n"
				if calls := manual.list(); len(calls) != 1 || calls[0][0] != BackendAgy || !sameDir(calls[0][1], h.root) || calls[0][2] != key {
					t.Fatalf("the manual renders once, for agy, the lead's directory, and its key: %v", calls)
				}
			}
			if text != want {
				t.Fatalf("session/new appends %q, want %q", text, want)
			}
		})
	}
}

// TestAgyLeadRefusesAStartWithoutWSTools: a start with no HTTP MCP route,
// or one whose session never reaches the route with its token (agy only
// drops a refused server), fails loudly instead of running a lead without
// ws tools.
func TestAgyLeadRefusesAStartWithoutWSTools(t *testing.T) {
	restore := agyMCPConnectWait
	agyMCPConnectWait = 200 * time.Millisecond
	t.Cleanup(func() { agyMCPConnectWait = restore })

	t.Run("no route", func(t *testing.T) {
		h, _ := newAgyLeadHarness(t)
		h.start()
		id := h.lead(BackendAgy, LeadOptions{}).ID
		if _, err := h.runner.Send(id, "hello"); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the refused start", func() bool { return strings.Contains(h.state(id).Error, "HTTP MCP route") })
		if n := h.holders.opCount("spawn"); n != 0 {
			t.Fatalf("no process starts: %v", h.holders.opList())
		}
	})
	t.Run("route not reached", func(t *testing.T) {
		h, f := newAgyLeadHarness(t)
		f.refuseMCP = true
		h.startAt("http://127.0.0.1:1")
		id := h.lead(BackendAgy, LeadOptions{}).ID
		f.replyID = id
		if _, err := h.runner.Send(id, "hello"); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the refused start", func() bool { return strings.Contains(h.state(id).Error, "did not reach the ws MCP route") })
		h.quiesce(id)
		if rec := h.record(id); rec.MCPToken != "" || rec.HolderID != "" || rec.HostPID != 0 {
			t.Fatalf("the failed start leaves no token or holder: %+v", rec)
		}
		if row, err := wsdashboard.ReadRegistration(h.dir, id); err != nil || row.HostPID != 0 || !row.HostStopped {
			t.Fatalf("the row the start named its process in says stopped: %+v %v", row, err)
		}
		h.runner.mu.Lock()
		live := len(h.runner.mcpTokens)
		h.runner.mu.Unlock()
		if live != 0 {
			t.Fatalf("%d live tokens after the failed start", live)
		}
	})
}

// TestAgyWorkerProfile keeps the default agent, adds ws's common worker
// tools and child-work instructions, and refuses a launch without HTTP MCP.
func TestAgyWorkerProfile(t *testing.T) {
	if LeadProfileFor(BackendAgy) != LeadProfileAgy {
		t.Fatalf("lead profile %q", LeadProfileFor(BackendAgy))
	}
	p, err := ParseProfile(BuiltinProfiles()[ProfilePrefix+AgentWorkerProfile(BackendAgy)])
	if err != nil || p.Lead || p.Backend != BackendAgy || !slices.Equal(p.WSTools, AgentWSTools) {
		t.Fatalf("worker profile: %+v, %v", p, err)
	}
	text, ok := p.BuiltinInstructions()
	for _, want := range []string{"agent.spawn", "mailbox.send", "Do not use", "invoke_subagent", "define_subagent", "manage_subagents", "Never end your turn", "run_command", "manage_task"} {
		if !ok || !strings.Contains(text, want) {
			t.Errorf("worker instructions lack %q: %q", want, text)
		}
	}
	if _, err := (AgyBackend{}).Start(context.Background(), StartSpec{}); err == nil || !strings.Contains(err.Error(), "HTTP MCP route") {
		t.Fatalf("a worker without ws tools is refused: %v", err)
	}
}

// TestAgyLeadUnsolicitedUpdateDoesNotWedgeTurn: if agy self-resumes after a
// background command finishes with no open session/prompt, its unsolicited
// session/update burst opens a bounded turn that closes cleanly once quiet and
// never wedges the conversation or pollutes the next prompt's reply.
func TestAgyLeadUnsolicitedUpdateDoesNotWedgeTurn(t *testing.T) {
	restoreUsage := agyWakeUsageQuiet
	agyWakeUsageQuiet = 40 * time.Millisecond
	t.Cleanup(func() { agyWakeUsageQuiet = restoreUsage })

	h, f := newAgyLeadHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "first")
	holder := h.holder(id)
	eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"waiting"}}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`,
	)
	h.idle(id)

	// Unsolicited self-resume (recorded shape from agy 1.5.12): no prompt is open.
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"bg1","name":"run_command","status":"completed"}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"background finished"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"usage_update","used":9404,"size":1048576}}}`,
	)
	eventually(t, "unsolicited burst auto-closed", func() bool {
		return h.state(id).Status == StatusIdle && slices.Contains(ringTexts(h.ring(id), "reply"), "background finished")
	})
	// Next turn dispatches cleanly as a fresh prompt (not wedged).
	h.send(id, "second")
	eventually(t, "the second prompt", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"second reply"}}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[1]+`"}`,
	)
	h.idle(id)
	if st := h.state(id); st.Status != StatusIdle || st.Error != "" {
		t.Fatalf("after second turn: %+v", st)
	}
	if !slices.Contains(ringTexts(h.ring(id), "reply"), "second reply") {
		t.Fatalf("second reply missing from ring: %v", ringTexts(h.ring(id), "reply"))
	}
}

// TestAgyLeadReplayUnsolicitedBurstDoesNotWedge (R1 finding 1A): an
// unsolicited/wakeup burst in the holder ring with no session/prompt response
// must not leave the lead wedged in StatusRunning after daemon --replace.
func TestAgyLeadReplayUnsolicitedBurstDoesNotWedge(t *testing.T) {
	restoreUsage, restoreIdle := agyWakeUsageQuiet, agyWakeIdleQuiet
	agyWakeUsageQuiet = 40 * time.Millisecond
	agyWakeIdleQuiet = 80 * time.Millisecond
	t.Cleanup(func() {
		agyWakeUsageQuiet = restoreUsage
		agyWakeIdleQuiet = restoreIdle
	})

	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{
			name: "with usage_update",
			lines: []string{
				`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replayed wakeup"}}}}`,
				`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"usage_update","used":9404,"size":1048576}}}`,
			},
		},
		{
			name: "without usage_update",
			lines: []string{
				`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replayed bare burst"}}}}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f := newAgyLeadHarness(t)
			const endpoint = "http://127.0.0.1:1"
			h.startAt(endpoint)
			id := h.lead(BackendAgy, LeadOptions{}).ID
			f.replyID = id
			h.send(id, "first")
			holder := h.holder(id)
			eventually(t, "first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done1"}}}}`,
				`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`,
			)
			h.idle(id)

			// Write an unsolicited burst with no prompt response into the holder ring, then --replace.
			h.holders.out(holder, tc.lines...)
			h.runner.Close()
			h.holders.restart()
			h.startAt(endpoint)

			eventually(t, "replay finished and burst settled", func() bool {
				c := h.runner.conv(id)
				if c == nil {
					return false
				}
				settled := false
				c.do(func() { settled = c.proc != nil && !c.replaying && !c.turn })
				return settled
			})
			// Subsequent send must not be blocked by a wedged turn.
			h.send(id, "after replay")
			eventually(t, "post-replay prompt dispatched", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"after replay ok"}}}}`,
				`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[1]+`"}`,
			)
			h.idle(id)
		})
	}
}

// TestAgyLeadIdleRestartUnsolicitedWakeupCloses (R1 finding 1B): after an idle
// daemon --replace, a later live unsolicited wakeup must open and close cleanly
// instead of staying wedged forever.
func TestAgyLeadIdleRestartUnsolicitedWakeupCloses(t *testing.T) {
	restoreUsage := agyWakeUsageQuiet
	agyWakeUsageQuiet = 40 * time.Millisecond
	t.Cleanup(func() { agyWakeUsageQuiet = restoreUsage })

	h, f := newAgyLeadHarness(t)
	const endpoint = "http://127.0.0.1:1"
	h.startAt(endpoint)
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "first")
	holder := h.holder(id)
	eventually(t, "first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"idle1"}}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`,
	)
	h.idle(id)

	// Daemon --replace while idle.
	h.runner.Close()
	h.holders.restart()
	h.startAt(endpoint)
	eventually(t, "idle rejoin complete", func() bool {
		c := h.runner.conv(id)
		if c == nil {
			return false
		}
		ready := false
		c.do(func() { ready = c.proc != nil && !c.replaying && !c.turn })
		return ready
	})

	// Later live unsolicited wakeup arrives with no open prompt.
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"late wakeup"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"usage_update","used":9500,"size":1048576}}}`,
	)
	eventually(t, "late wakeup settled", func() bool {
		return h.state(id).Status == StatusIdle && slices.Contains(ringTexts(h.ring(id), "reply"), "late wakeup")
	})

	h.send(id, "next")
	eventually(t, "next prompt dispatched", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[1]+`"}`,
	)
	h.idle(id)
}

// TestAgyLeadSteeredPromptAcrossReplace (R1 finding 2): if daemon 1 sent a
// prompt and steered a second prompt before --replace, finishing P1 live after
// rejoin must not cause P2's chunks to be dropped; P2 opens, streams its reply,
// and closes on P2's prompt response.
func TestAgyLeadSteeredPromptAcrossReplace(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	const endpoint = "http://127.0.0.1:1"
	h.startAt(endpoint)
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "p1")
	holder := h.holder(id)
	eventually(t, "p1 sent", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"p1 part"}}}}`,
	)
	// Steer a second prompt while P1 is still in flight.
	h.send(id, "p2 steered")
	h.steerHeld(id)
	h.runner.InboxChanged(id)
	eventually(t, "p2 steered into running turn", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
	ids := agyPromptIDs(h, holder)

	// Daemon --replace while both P1 and P2 are in flight.
	h.runner.Close()
	h.holders.restart()
	h.startAt(endpoint)
	eventually(t, "rejoin complete mid-turn", func() bool {
		c := h.runner.conv(id)
		if c == nil {
			return false
		}
		ready := false
		c.do(func() { ready = c.proc != nil && !c.replaying && c.turn })
		return ready
	})

	// P1 finishes live; then P2 streams its reply and finishes live.
	h.holders.out(holder,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+ids[0]+`"}`,
	)
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"p2 steered reply"}}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+ids[1]+`"}`,
	)
	eventually(t, "steered p2 reply recorded and idle", func() bool {
		return h.state(id).Status == StatusIdle && slices.Contains(ringTexts(h.ring(id), "reply"), "p2 steered reply")
	})
}

// TestAgyLeadBackgroundTaskWakeupClosesAutomatically verifies the full ACP
// lifecycle on a hosted agy lead: a run_command left open across end_turn
// raises BackgroundTasks, its later completion drops BackgroundTasks to 0,
// and agy's self-resumed wakeup turn opens and closes automatically once
// usage_update goes quiet.
func TestAgyLeadBackgroundTaskWakeupClosesAutomatically(t *testing.T) {
	restoreUsage := agyWakeUsageQuiet
	agyWakeUsageQuiet = 40 * time.Millisecond
	t.Cleanup(func() { agyWakeUsageQuiet = restoreUsage })

	h, f := newAgyLeadHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "run slow")
	holder := h.holder(id)
	eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call","toolCallId":"bg1","name":"run_command","status":"in_progress","args":{"CommandLine":"sleep 20","IsDaemon":false}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"backgrounded"}}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(h, holder)[0]+`"}`,
	)
	eventually(t, "idle with background task", func() bool {
		st := h.state(id)
		return st.Status == StatusIdle && st.BackgroundTasks == 1
	})
	// Background task completes and agy self-resumes without a new prompt.
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"bg1","name":"run_command","status":"completed"}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"WAKEUP_DONE"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"usage_update","used":9451,"size":1048576}}}`,
	)
	eventually(t, "wakeup turn settled", func() bool {
		st := h.state(id)
		return st.Status == StatusIdle && st.BackgroundTasks == 0 && slices.Contains(ringTexts(h.ring(id), "reply"), "WAKEUP_DONE")
	})
}

func TestAgyWorkerReattachWaitsForPromptResponse(t *testing.T) {
	const usageQuiet = 20 * time.Millisecond
	const idleQuiet = 80 * time.Millisecond
	restoreUsage, restoreIdle := agyWakeUsageQuiet, agyWakeIdleQuiet
	agyWakeUsageQuiet, agyWakeIdleQuiet = usageQuiet, idleQuiet
	t.Cleanup(func() { agyWakeUsageQuiet, agyWakeIdleQuiet = restoreUsage, restoreIdle })

	for _, tc := range []struct {
		name  string
		usage bool
		quiet time.Duration
	}{
		{"usage quiet", true, usageQuiet},
		{"idle quiet", false, idleQuiet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, got := workerRecoveryHarness(t, BackendAgy)
			atSend := observeAgyWorkerPromptStarts(h)
			id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
			before := h.record(id)
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t1","name":"read_file","status":"in_progress"}}}`,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}}}`,
			)
			eventually(t, "the first tool call completed", func() bool { return h.record(id).HolderSeq >= before.HolderSeq+2 })
			rejoinAgyWorker(t, h, id)
			if start := <-atSend; !start.TurnOpen || start.TurnStartedAt == 0 || start.TurnStartedAt != h.record(id).TurnStartedAt || start.TurnStartSeq != h.record(id).TurnStartSeq {
				t.Fatalf("first prompt boundary at send: open=%v time=%d seq=%d", start.TurnOpen, start.TurnStartedAt, start.TurnStartSeq)
			}

			if tc.usage {
				h.holders.out(holder, `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"usage_update","used":9404,"size":1048576}}}`)
				eventually(t, "the resumed worker applied usage", func() bool {
					used := h.state(id).ContextTokens
					return used != nil && *used == 9404
				})
			}
			time.Sleep(3 * tc.quiet)
			if reports := got.list(); len(reports) != 0 || !h.record(id).TurnOpen {
				t.Fatalf("worker turn ended during %s: reports=%+v open=%v", tc.name, reports, h.record(id).TurnOpen)
			}
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t2","name":"read_file","status":"completed"}}}`,
			)
			completeRecoveryWorker(t, h, BackendAgy, holder)
			eventually(t, "one worker result after the prompt response", func() bool { return len(got.list()) == 1 })
			if result := got.list()[0].Result; result.Status != TurnDone || result.Message != "held result" || h.record(id).TurnOpen {
				t.Fatalf("worker turn did not settle on the prompt response: %+v", result)
			}

			// A later wakeup has no prompt response and still closes on quiet.
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"unsolicited wakeup"}}}}`,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"usage_update","used":9500,"size":1048576}}}`,
			)
			eventually(t, "the unsolicited worker wakeup settled", func() bool { return len(got.list()) == 2 })
			if result := got.list()[1].Result; result.Status != TurnDone || result.Message != "unsolicited wakeup" {
				t.Fatalf("unsolicited wakeup result: %+v", result)
			}
		})
	}
}

// Observe the durable record from the fake holder's synchronous ACP write.
func observeAgyWorkerPromptStarts(h *leadHarness) <-chan Record {
	starts := make(chan Record, 8)
	h.holders.set(func() {
		answer := h.holders.acp
		h.holders.acp = func(holder, method string, params json.RawMessage) (string, string, bool) {
			if method == "session/prompt" {
				h.holders.mu.Lock()
				var meta holderSession
				_ = json.Unmarshal(h.holders.holders[holder].session, &meta)
				h.holders.mu.Unlock()
				rec, _ := ReadRecord(h.dir, meta.ReplyID)
				starts <- rec
			}
			return answer(holder, method, params)
		}
	})
	return starts
}

func rejoinAgyWorker(t *testing.T, h *leadHarness, id string) {
	t.Helper()
	h.runner.Close()
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the worker rejoined its holder", func() bool {
		c := h.runner.conv(id)
		if c == nil {
			return false
		}
		ready := false
		c.do(func() { ready = c.proc != nil && !c.replaying })
		return ready
	})
}

func TestAgyWorkerCompactionPromptSurvivesReattach(t *testing.T) {
	const quiet = 40 * time.Millisecond
	restoreUsage := agyWakeUsageQuiet
	agyWakeUsageQuiet = quiet
	t.Cleanup(func() { agyWakeUsageQuiet = restoreUsage })

	for _, tc := range []struct {
		name        string
		reportable  bool
		prepare     func(*conv)
		wantReports int
	}{
		{"advisory", false, func(c *conv) {
			c.compactNudge = compactionNudge{text: "write agenda before compacting", level: 1, standalone: true}
			c.compactUsageFresh = true
			c.kickInput()
		}, 1},
		{"native command", false, func(c *conv) {
			c.compactPending = &compactRequest{}
			c.kick()
		}, 1},
		{"continuation", true, func(c *conv) {
			c.compactNote, c.compactResume = "restored agenda", true
			c.kick()
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, got := workerRecoveryHarness(t, BackendAgy)
			atSend := observeAgyWorkerPromptStarts(h)
			id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
			<-atSend
			completeRecoveryWorker(t, h, BackendAgy, holder)
			eventually(t, "the original result", func() bool { return len(got.list()) == 1 })
			c := h.runner.conv(id)
			c.do(func() { tc.prepare(c) })
			var start Record
			select {
			case start = <-atSend:
			default:
				t.Fatal("the standalone compaction prompt did not reach the holder")
			}

			rejoinAgyWorker(t, h, id)
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"quiet","name":"read_file","status":"completed"}}}`,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"usage_update","used":9404,"size":1048576}}}`,
			)
			eventually(t, "the worker applied usage", func() bool {
				used := h.state(id).ContextTokens
				return used != nil && *used == 9404
			})
			time.Sleep(3 * quiet)
			if count, status := len(got.list()), h.state(id).Status; count != 1 || status != StatusRunning {
				t.Fatalf("compaction prompt settled during quiet: reports=%d status=%s", count, status)
			}
			if rec := h.record(id); start.TurnOpen != tc.reportable || start.TurnStartedAt == 0 || rec.TurnStartedAt != start.TurnStartedAt || rec.TurnStartSeq != start.TurnStartSeq {
				t.Fatalf("compaction boundary at send: open=%v time=%d seq=%d", start.TurnOpen, start.TurnStartedAt, start.TurnStartSeq)
			}
			prompt := agyPromptIDs(h, holder)[1]
			h.holders.out(holder,
				`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"continued"}}}}`,
				jsonLine(map[string]any{"jsonrpc": "2.0", "id": prompt, "result": map[string]any{"stopReason": "end_turn"}}),
			)
			eventually(t, "the prompt response cleared the boundary", func() bool {
				rec := h.record(id)
				return !rec.TurnOpen && rec.TurnStartedAt == 0 && h.state(id).Status == StatusIdle && len(got.list()) == tc.wantReports
			})
			if tc.reportable && got.list()[1].Result.Message != "continued" {
				t.Fatalf("continuation result: %+v", got.list()[1].Result)
			}
		})
	}
}

func TestAgyWorkerReattachInterceptsNativeFailureOnce(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	// Replace before the open prompt produces any content.
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	first := agyPromptIDs(h, holder)[0]
	h.runner.Close()
	h.holders.restart()
	timers := &leverTimers{}
	h.opts.Timer = timers.start
	h.opts.AgyModelWaitBackoff = func(int) time.Duration { return 0 }
	h.startAt("http://127.0.0.1:1")
	c := h.runner.conv(id)
	eventually(t, "open worker turn rearmed after replay", func() bool {
		var armed bool
		c.do(func() { armed = !c.replaying && c.turn && c.rec.TurnOpen && c.modelWaitArmed })
		return armed
	})
	if started, _ := timers.counts(); started != 0 || len(got.list()) != 0 {
		t.Fatalf("rejoin started %d timers and reported %d results", started, len(got.list()))
	}
	end := `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"` + first + `"}`
	h.holders.out(holder, agyIdleTimeoutChunk, end)
	eventually(t, "one automatic continue", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
	prompts := h.holders.acpInputs(holder, "session/prompt")
	var params struct{ Prompt []struct{ Text string } }
	if err := json.Unmarshal(prompts[1], &params); err != nil || len(params.Prompt) != 1 || params.Prompt[0].Text != "continue" {
		t.Fatalf("retry prompt params = %s", prompts[1])
	}
	if started, stopped := timers.counts(); started != 1 || stopped != 1 {
		t.Fatalf("fallback timers started = %d, stopped = %d, want one stopped timer", started, stopped)
	}
	timers.fire(t, 0)
	retry := agyPromptIDs(h, holder)[1]
	h.holders.out(holder, end, // Duplicate response to the predecessor prompt.
		`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"retried result"}}}}`,
		`{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+retry+`"}`)
	eventually(t, "one worker result", func() bool { return len(got.list()) == 1 })
	if result := got.list()[0].Result; result.Status != TurnDone || result.Message != "retried result" {
		t.Fatalf("retried result = %+v", result)
	}
	if count := len(agyPromptIDs(h, holder)); count != 2 || len(h.holders.acpInputs(holder, "session/cancel")) != 0 {
		t.Fatalf("reattach sent %d prompts and %d cancels", count, len(h.holders.acpInputs(holder, "session/cancel")))
	}
}

func TestAgyWorkerReattachDoesNotArmCompletedTurn(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	completeRecoveryWorker(t, h, BackendAgy, holder)
	eventually(t, "completed worker result", func() bool { return len(got.list()) == 1 && !h.record(id).TurnOpen })
	h.runner.Close()
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	c := h.runner.conv(id)
	var armed, active bool
	eventually(t, "completed worker rejoined", func() bool {
		var ready bool
		c.do(func() { ready, armed, active = c.proc != nil && !c.replaying, c.modelWaitArmed, c.turn })
		return ready
	})
	if armed || active || len(got.list()) != 1 || len(agyPromptIDs(h, holder)) != 1 {
		t.Fatalf("completed worker rejoined with active=%v, armed=%v, reports=%d, prompts=%d", active, armed, len(got.list()), len(agyPromptIDs(h, holder)))
	}
}

func TestAgyWorkerReattachSuspendsNativeFailureForPendingToolOrApproval(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		tools      int
		approvals  int
	}{
		{"tool", `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t1","name":"run_command","status":"in_progress","args":{"IsDaemon":true}}}}`, 1, 0},
		{"approval", `{"jsonrpc":"2.0","id":"approval-1","method":"session/request_permission","params":{"toolCall":{"toolCallId":"t1","name":"run_command"},"options":[{"optionId":"yes","kind":"allow_once"}]}}`, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, got := workerRecoveryHarness(t, BackendAgy, false)
			id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
			h.holders.out(holder, tc.line)
			c := h.runner.conv(id)
			eventually(t, "pending work before rejoin", func() bool {
				var ready bool
				c.do(func() { ready = c.modelWaitPending == tc.tools && len(c.approvals) == tc.approvals })
				return ready
			})
			h.runner.Close()
			h.holders.restart()
			timers := &leverTimers{}
			h.opts.Timer = timers.start
			h.startAt("http://127.0.0.1:1")
			c = h.runner.conv(id)
			var armed bool
			var tools, approvals int
			eventually(t, "worker rejoined with pending work", func() bool {
				var ready bool
				c.do(func() {
					ready = c.proc != nil && !c.replaying
					armed, tools, approvals = c.modelWaitArmed, c.modelWaitPending, len(c.approvals)
				})
				return ready
			})
			if !armed || tools != tc.tools || approvals != tc.approvals {
				t.Fatalf("reattach watchdog: armed=%v, tools=%d, approvals=%d", armed, tools, approvals)
			}
			h.holders.out(holder, agyIdleTimeoutChunk)
			eventually(t, "native failure excluded while work is pending", func() bool {
				var excluded bool
				c.do(func() { excluded = c.modelWaitFailureExcluded })
				return excluded
			})
			if started, _ := timers.counts(); started != 0 {
				t.Fatalf("failure during pending %s started %d timers", tc.name, started)
			}
			first := agyPromptIDs(h, holder)[0]
			h.holders.out(holder, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+first+`"}`)
			eventually(t, "worker failure settled", func() bool { return len(got.list()) == 1 })
			if result := got.list()[0].Result; result.Status != TurnFailed || !strings.HasPrefix(result.Error, "[stream idle timeout]") {
				t.Fatalf("excluded failure result = %+v", result)
			}
			if len(agyPromptIDs(h, holder)) != 1 || len(h.holders.acpInputs(holder, "session/cancel")) != 0 {
				t.Fatal("failure during pending work sent a cancel or prompt")
			}
		})
	}
}
