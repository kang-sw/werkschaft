// agent_spawn_test.go holds agent spawn depth, width, and tier tests.
package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestAgentSpawnDepthCapThroughTheTokenRoute(t *testing.T) {
	h, _ := agentHarness(t, nil)
	a, _ := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "depth 1", Tier: "medium", Authority: "workdir-write"})
	token := h.token(0)
	if code, body := h.tokenCall(token, "agent.tiers", nil); code != http.StatusOK || !strings.Contains(body, `"harness":"claude"`) {
		t.Fatalf("agent.tiers by token = %d %s", code, body)
	}
	last := a
	for depth := 2; depth <= MaxSpawnDepth; depth++ {
		n := h.backend.startCount()
		code, body := h.tokenCall(token, "agent.spawn", map[string]any{"prompt": fmt.Sprintf("depth %d", depth), "tier": "small", "authority": "workdir-write"})
		var res AgentSpawnResult
		if code != http.StatusCreated || json.Unmarshal([]byte(body), &res) != nil {
			t.Fatalf("depth-%d spawn = %d %s", depth, code, body)
		}
		id := agentID(res.Address)
		if st := h.state(id); st.Parent == nil || *st.Parent != (ParentRef{Kind: ParentConversation, ReplyID: last}) {
			t.Fatalf("depth-%d child's parent = %+v", depth, st.Parent)
		}
		h.backend.waitProc(t, n).nextSent(t)
		if token = h.token(n); token == "" {
			t.Fatalf("a depth-%d child holds no token", depth)
		}
		last = id
	}
	code, body := h.tokenCall(token, "agent.spawn", map[string]any{"prompt": "too deep", "tier": "small", "authority": "read-only"})
	if code != http.StatusForbidden || !strings.Contains(body, "agent.spawn: the spawn depth cap is 4") || !strings.Contains(body, "at depth 4") {
		t.Fatalf("a depth-5 spawn = %d %s", code, body)
	}
	if n := len(h.runner.Children(ParentRef{Kind: ParentConversation, ReplyID: last})); n != 0 {
		t.Fatalf("a refused spawn spawned %d", n)
	}
}

// The width cap counts awake direct children: the 100th awake one is
// refused, a sleeping one does not count, and a delivery wakes a sleeping
// child past the cap.
func TestAgentSpawnWidthCapCountsAwakeChildren(t *testing.T) {
	h, _ := agentHarness(t, nil)
	lead := h.leadCaller()
	var ids []string
	for i := range MaxSpawnWidth {
		ids = append(ids, h.agentSpawn(lead, AgentSpawnRequest{Prompt: fmt.Sprintf("job %d", i), Tier: "small", Authority: "read-only"}))
	}
	eventually(t, "every child's process", func() bool { return h.backend.startCount() == MaxSpawnWidth })
	if n := h.runner.awakeChildren(leadParent); n != MaxSpawnWidth {
		t.Fatalf("awake children = %d", n)
	}
	_, err := h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "one too many", Tier: "small", Authority: "read-only"})
	agentRefused(t, err, http.StatusForbidden, "the spawn width cap is 99 awake direct children")
	if n := len(h.runner.AgentList(lead)); n != MaxSpawnWidth {
		t.Fatalf("a refused spawn spawned: %d children", n)
	}

	sleeper := ids[0]
	h.asleep(sleeper)
	eventually(t, "the sleeper not counted", func() bool { return h.runner.awakeChildren(leadParent) == MaxSpawnWidth-1 })
	h.agentSpawn(lead, AgentSpawnRequest{Prompt: "the hundredth", Tier: "small", Authority: "read-only"})
	eventually(t, "the parent at the cap", func() bool { return h.runner.awakeChildren(leadParent) == MaxSpawnWidth })
	starts := h.backend.startCount()
	if _, err := h.runner.Deliver(sleeper, Delivery{From: "senderreplyid", Text: "wake past the cap"}); err != nil {
		t.Fatalf("a wake at the cap = %v", err)
	}
	eventually(t, "the sleeper's process", func() bool { return h.state(sleeper).Process })
	if h.backend.startCount() <= starts {
		t.Fatal("the wake started no process")
	}
	if n := h.runner.awakeChildren(leadParent); n != MaxSpawnWidth+1 {
		t.Fatalf("awake children after the wake = %d", n)
	}
}

func setTierAliases(t *testing.T, cfg wsconfig.Options, value string) {
	t.Helper()
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.Set(wsconfig.ItemAgentTierAliases, value, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentSpawnResolvesTiers(t *testing.T) {
	h, _ := agentHarness(t, func(o *Options) {
		o.Backends["codex"] = BackendRegistration{Backend: o.Backends[BackendClaude].Backend}
	})
	setTierAliases(t, h.config.Config, "quick=werk.small big=werk.xlarge far=agy.large")
	lead := h.leadCaller()
	for _, tc := range []struct{ tier, backend, model string }{
		{"medium", BackendClaude, "claude-medium"},
		{"werk.small", BackendWerk, "prov/small-model"},
		{"CLAUDE.Large", BackendClaude, "claude-large"},
		{"quick", BackendWerk, "prov/small-model"},
	} {
		id := h.agentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: tc.tier, Authority: "read-only"})
		if st := h.state(id); st.Backend != tc.backend || st.Model != tc.model {
			t.Fatalf("tier %q spawned %+v", tc.tier, st)
		}
	}
	spawned := len(h.runner.AgentList(lead))
	codex := LeadCaller(leadParent.ReplyID, h.root, "codex", h.config.Config)
	for _, tc := range []struct {
		caller AgentCaller
		tier   string
		why    string
	}{
		{lead, "", "tier is required"},
		{lead, "huge", `tier "huge" is neither a tier, <harness>.<tier>, nor an alias`},
		{lead, "werk.xlarge", "werk.xlarge has no usable model"},
		{lead, "werk.huge", "names no tier"},
		{lead, "agy.large", `harness "agy" is not registered in this daemon`},
		{lead, "big", "werk.xlarge has no usable model"},
		{lead, "far", `harness "agy" is not registered`},
		{codex, "small", `harness "codex" is registered for hosted leads only`},
		{lead, "codex.small", `harness "codex" is registered for hosted leads only`},
	} {
		_, err := h.runner.AgentSpawn(tc.caller, AgentSpawnRequest{Prompt: "x", Tier: tc.tier, Authority: "read-only"})
		agentRefused(t, err, http.StatusBadRequest, tc.why, "valid tiers: ")
		msg := err.Error()
		for _, want := range []string{"claude.small", "claude.large", "werk.small", "werk.large", "quick"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("tier %q: the valid list lacks %s: %s", tc.tier, want, msg)
			}
		}
		if strings.Contains(msg, "big") && tc.tier != "big" || strings.Contains(msg, "far") && tc.tier != "far" || strings.Contains(msg, "codex.") && !strings.Contains(tc.why, "codex") {
			t.Fatalf("tier %q: the valid list names an unusable tier: %s", tc.tier, msg)
		}
		native := strings.Contains(msg, "valid tiers: small, medium, large, xlarge, claude.small")
		if native != (tc.caller.Harness == BackendClaude) {
			t.Fatalf("tier %q: bare tiers listed for harness %s: %s", tc.tier, tc.caller.Harness, msg)
		}
	}
	if n := len(h.runner.AgentList(lead)); n != spawned {
		t.Fatalf("a refused tier spawned: %d children, was %d", n, spawned)
	}
}

func TestAgentTiersListsNativeExtAndAliases(t *testing.T) {
	h, _ := agentHarness(t, nil)
	setTierAliases(t, h.config.Config, "quick=werk.small big=werk.xlarge far=agy.large")
	got, err := h.runner.AgentTiers(h.leadCaller())
	if err != nil {
		t.Fatal(err)
	}
	tierNames := func(es []AgentTierEntry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.Tier)
		}
		return out
	}
	// claude's xlarge is the builtin floor's; the test set the others.
	if got.Harness != BackendClaude || !slices.Equal(tierNames(got.Native), []string{"small", "medium", "large", "xlarge"}) ||
		got.Native[1] != (AgentTierEntry{Tier: "medium", Model: "claude-medium", Effort: "high"}) {
		t.Fatalf("native = %+v", got)
	}
	if !slices.Equal(tierNames(got.Ext), []string{"claude.small", "claude.medium", "claude.large", "claude.xlarge", "werk.small", "werk.medium", "werk.large"}) ||
		got.Ext[4] != (AgentTierEntry{Tier: "werk.small", Model: "prov/small-model", Effort: "low", Provider: "openrouter"}) {
		t.Fatalf("ext = %+v", got.Ext)
	}
	wantAliases := []AgentAliasEntry{
		{Alias: "big", Target: "werk.xlarge", Error: `tier "xlarge" is not set for werk`},
		{Alias: "far", Target: "agy.large", Error: `harness "agy" is not registered in this daemon`},
		{Alias: "quick", Target: "werk.small", Model: "prov/small-model", Effort: "low"},
	}
	if !slices.Equal(got.Aliases, wantAliases) || len(got.Unavailable) != 0 {
		t.Fatalf("aliases = %+v, unavailable = %v", got.Aliases, got.Unavailable)
	}
	// On a werk caller the bare tiers are werk's.
	werk, err := h.runner.AgentTiers(LeadCaller(leadParent.ReplyID, h.root, BackendWerk, h.config.Config))
	if err != nil || !slices.Equal(tierNames(werk.Native), []string{"small", "medium", "large"}) || werk.Native[0].Model != "prov/small-model" {
		t.Fatalf("a werk caller's tiers = %+v, %v", werk, err)
	}

	// A worker harness that cannot spawn now is named, with why, and its
	// tiers leave the lists.
	h.runner.Close()
	reg := h.config.Backends[BackendWerk]
	reg.Backend = &unavailableWerkFixture{h.backend}
	h.config.Backends[BackendWerk] = reg
	h.start()
	got, err = h.runner.AgentTiers(h.leadCaller())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Unavailable, []string{"werk: OpenRouter fixture credential missing"}) || !slices.Equal(tierNames(got.Ext), []string{"claude.small", "claude.medium", "claude.large", "claude.xlarge"}) ||
		got.Aliases[2].Error == "" {
		t.Fatalf("tiers with werk unavailable = %+v", got)
	}
	_, err = h.runner.AgentSpawn(h.leadCaller(), AgentSpawnRequest{Prompt: "x", Tier: "werk.small", Authority: "read-only"})
	agentRefused(t, err, http.StatusBadRequest, "OpenRouter fixture credential missing", "valid tiers: small, medium, large, xlarge, claude.small, claude.medium, claude.large, claude.xlarge")
	if strings.Contains(err.Error(), "werk.small,") || strings.Contains(err.Error(), "quick") {
		t.Fatalf("an unavailable harness's tiers are listed: %v", err)
	}

	// No worker harness installed: nothing is spawnable.
	setLookPath(t, func(file string) (string, error) { return "", errors.New("not found") })
	got, err = h.runner.AgentTiers(h.leadCaller())
	if err != nil || len(got.Native)+len(got.Ext) != 0 || len(got.Unavailable) != 2 || !strings.Contains(got.Unavailable[0], "fake is not installed") {
		t.Fatalf("tiers with nothing installed = %+v, %v", got, err)
	}
	_, err = h.runner.AgentSpawn(h.leadCaller(), AgentSpawnRequest{Prompt: "x", Tier: "small", Authority: "read-only"})
	agentRefused(t, err, http.StatusBadRequest, "no tier is spawnable now")
}

// HarnessAvailability is SpawnableHarness for the dashboard view: the same
// refusals, but the Pi child check answers only from ChildCheckKnown and is
// unknown (never run) while that needs a probe.
func TestAgentSystemPromptReachesEveryStart(t *testing.T) {
	h, _ := agentHarness(t, nil)
	lead := h.leadCaller()
	a, ap := h.agentRunning(lead, AgentSpawnRequest{Prompt: "go", Tier: "small", Authority: "read-only", SystemPrompt: "You are the cache probe."})
	if text := instructionsOf(t, h.backend.start(0)); !strings.Contains(text, "You are the cache probe.") {
		t.Fatalf("the first start's instructions = %q", text)
	}
	if raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(h.dir, a), systemPromptFile)); err != nil || string(raw) != "You are the cache probe." {
		t.Fatalf("the session copy = %q, %v", raw, err)
	}
	if rec := h.record(a); !rec.SystemPrompt {
		t.Fatalf("the record does not mark the system prompt: %+v", rec)
	}
	ap.reply("done")
	eventually(t, "a's turn settled", func() bool { return h.state(a).Status == StatusIdle })
	h.asleep(a)
	// A new segment after sleep re-injects it.
	if _, err := h.runner.Deliver(a, Delivery{Text: "again"}); err != nil {
		t.Fatal(err)
	}
	h.backend.waitProc(t, 1).nextSent(t)
	if text := instructionsOf(t, h.backend.start(1)); !strings.Contains(text, "You are the cache probe.") {
		t.Fatalf("the start after sleep = %q", text)
	}
	// After a daemon restart it is read from the session directory.
	h.runner.Close()
	h.start()
	if _, err := h.runner.Deliver(a, Delivery{Text: "after the restart"}); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 2)
	if text := p.nextSent(t); !strings.Contains(text, "after the restart") {
		t.Fatalf("the turn after the restart = %q", text)
	}
	if text := instructionsOf(t, h.backend.start(2)); !strings.Contains(text, "You are the cache probe.") {
		t.Fatalf("the start after the restart = %q", text)
	}

	// system_prompt_path is read once, at spawn, relative to the caller's
	// directory: deleting the source does not matter.
	src := filepath.Join(h.root, "prompts", "probe.md")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("Path-sourced prompt."), 0o644); err != nil {
		t.Fatal(err)
	}
	n := h.backend.startCount()
	h.agentSpawn(lead, AgentSpawnRequest{Prompt: "go", Tier: "small", Authority: "read-only", SystemPromptPath: "prompts/probe.md"})
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	h.backend.waitProc(t, n).nextSent(t)
	if text := instructionsOf(t, h.backend.start(n)); !strings.Contains(text, "Path-sourced prompt.") {
		t.Fatalf("the path prompt's start = %q", text)
	}

	_, err := h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: "small", Authority: "read-only", SystemPrompt: "a", SystemPromptPath: "b"})
	agentRefused(t, err, http.StatusBadRequest, "not both")
	_, err = h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: "small", Authority: "read-only", SystemPromptPath: "prompts/probe.md"})
	agentRefused(t, err, http.StatusBadRequest, "system_prompt_path")
}

func TestWerkWorkerAdmissionAsksForConsentOnlyOnStart(t *testing.T) {
	prompt := make(chan struct{}, 1)
	r := &Runner{opts: Options{Backends: map[string]BackendRegistration{
		BackendWerk: {Backend: WerkBackend{credentials: cronPlanStatus{prompt: prompt}}},
	}}}
	provider := string(wscred.ProviderOpenAI)
	if why := r.workerProviderGap(BackendWerk, provider); why == "" || len(prompt) != 0 {
		t.Fatal("worker discovery did not report consent-required without prompting")
	}
	if why := r.workerStartProviderGap(BackendWerk, provider); why == "" || len(prompt) != 1 {
		t.Fatal("worker admission did not report consent-required and prompt")
	}
}

type unavailableWerkFixture struct{ *fakeBackend }

func (*unavailableWerkFixture) workerCredentialGap() string {
	return "OpenRouter fixture credential missing"
}

func (b *unavailableWerkFixture) workerProviderGap(string) string {
	return b.workerCredentialGap()
}

func (*unavailableWerkFixture) workerTierGap(map[string]wsconfig.AgentTier) string { return "" }
