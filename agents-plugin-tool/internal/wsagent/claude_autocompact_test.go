package wsagent

import (
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func threshold(t *testing.T, value string) wsconfig.Threshold {
	t.Helper()
	th, err := wsconfig.ParseThreshold(value)
	if err != nil && value != "off" {
		t.Fatalf("%q: %v", value, err)
	}
	return th
}

// TestClaudeAutoCompactEnv pins the variables a Claude worker launches with
// for its worker_advisory and worker_hard thresholds.
func TestClaudeAutoCompactEnv(t *testing.T) {
	w, p := claudeAutoCompactWindowEnv, claudeAutoCompactPctEnv
	for _, tc := range []struct {
		advisory, hard string
		want           map[string]string
	}{
		// The reported configuration: 1M models read tokens/320k with the
		// advisory at 160k and the hard point at 200k; Claude compacts at
		// floor(300000*62%) = 186000.
		{"50%|160k", "80%|200k", map[string]string{w: "320000", p: "62"}},
		// Percent-only: no window, the model's own stays rawMaxTokens.
		{"50%", "80%", map[string]string{p: "80"}},
		{"50%|160k", "80%", map[string]string{p: "80"}},
		{"50%", "200k", map[string]string{w: "400000", p: "50"}},
		{"50%", "80%|200k", map[string]string{w: "400000", p: "50"}},
		// A token-only advisory under the hard point needs only W >= both.
		{"150k", "200k", map[string]string{w: "200000"}},
		// Claude reads a window under 100000 as 100000; q covers the rest.
		{"50%|160k", "50k", map[string]string{w: "100000", p: "50"}},
		// A hard point at the whole window is Claude's own default.
		{"50%", "100%", nil},
		// Off or unreadable: Claude keeps its own default.
		{"50%", "off", nil},
	} {
		t.Run(tc.advisory+"/"+tc.hard, func(t *testing.T) {
			got := claudeAutoCompactEnv(threshold(t, tc.advisory), threshold(t, tc.hard))
			if !maps.Equal(got, tc.want) {
				t.Fatalf("env = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClaudeAutoCompactEnvKeepsThresholds: for every model window, the
// window Claude then reports resolves the hard point exactly as the model's
// own window does, and the advisory exactly or at/past the hard point on
// both, so ws's own window never shrinks a configured threshold twice; and
// Claude's trigger, simulated as the 2.1.332 binary computes it, stays at or
// below the hard point.
func TestClaudeAutoCompactEnvKeepsThresholds(t *testing.T) {
	configs := [][2]string{
		{"50%|160k", "80%|200k"}, {"50%", "80%"}, {"50%", "200k"}, {"50%", "80%|200k"},
		{"150k", "200k"}, {"50%|160k", "50k"}, {"30%", "90%|1.5m"}, {"60%|300k", "70%|250k"},
		{"250k", "200k"}, {"90%", "60%"}, {"1.5k|10%", "20%|2.5k"},
	}
	natives := []float64{100_000, 128_000, 200_000, 250_000, 320_000, 400_000, 1_000_000, 2_000_000}
	for _, cfg := range configs {
		advisory, hard := threshold(t, cfg[0]), threshold(t, cfg[1])
		env := claudeAutoCompactEnv(advisory, hard)
		window, q := float64(0), float64(100)
		if v, ok := env[claudeAutoCompactWindowEnv]; ok {
			window, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := env[claudeAutoCompactPctEnv]; ok {
			q, _ = strconv.ParseFloat(v, 64)
		}
		for _, n := range natives {
			w := n
			if window > 0 {
				w = math.Min(n, math.Max(window, claudeAutoCompactWindowFloor))
			}
			hardN, hardW := hard.Point(n), hard.Point(w)
			if hardN != hardW {
				t.Errorf("%v on %.0f: hard resolves to %.0f against the reported %.0f, %.0f against the model's", cfg, n, hardW, w, hardN)
			}
			advN, advW := advisory.Point(n), advisory.Point(w)
			if advN != advW && (advN < hardN || advW < hardN) {
				t.Errorf("%v on %.0f: advisory %.0f against %.0f, %.0f against the model's, hard %.0f", cfg, n, advW, w, advN, hardN)
			}
			if math.IsInf(hardN, 1) {
				continue
			}
			for _, reserve := range []float64{20_000, 4_096} {
				bse := w - reserve
				trigger := math.Min(math.Floor(bse*q/100), bse-13_000)
				if trigger > hardN {
					t.Errorf("%v on %.0f (reserve %.0f): Claude compacts at %.0f, past the hard point %.0f", cfg, n, reserve, trigger, hardN)
				}
			}
		}
	}
}

// TestClaudeWorkerInitAsksContextUsage: a worker's init names its model and
// asks get_context_usage, as a lead's does, and each changed usage reports
// a status at once. Once the answer is in, every status carries the window:
// the meter never reads "342,724 / ?" for the rest of the turn.
func TestClaudeWorkerInitAsksContextUsage(t *testing.T) {
	m := newClaudeMapper(fixtureWorkerSpec(t))
	replies := &recordedReplies{}
	feed := func(line string) []Event { return m.mapLine([]byte(line), replies) }
	init := fixtureInit(t, "claude-worker-turn-2.1.326.jsonl")
	init["model"] = "claude-opus-5-5"
	raw, _ := json.Marshal(init)
	for _, ev := range feed(string(raw)) {
		if ev.Fatal != nil {
			t.Fatal(ev.Fatal)
		}
	}
	if m.model != "claude-opus-5-5" || len(replies.sent) != 1 {
		t.Fatalf("model %q, sent %v", m.model, replies.sent)
	}
	req, _ := replies.sent[0]["request"].(map[string]any)
	id, _ := replies.sent[0]["request_id"].(string)
	if req["subtype"] != "get_context_usage" || !strings.HasPrefix(id, claudeContextAsk) {
		t.Fatalf("ask = %v", replies.sent[0])
	}
	if c := contextOf(t, "usage before the answer", feed(string(usageLine(5_000, "")))); *c.Tokens != 5_000 {
		t.Fatalf("a worker's usage reports at once: %+v", c)
	}
	answer := jsonLine(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id,
		"response": map[string]any{"totalTokens": 5_000, "rawMaxTokens": 320_000, "autocompactSource": "env"}}})
	if c := contextOf(t, "answer", feed(answer)); c.Window != 320_000 || *c.Tokens != 5_000 {
		t.Fatalf("the answer reports the window at once: %+v", c)
	}
	if c := contextOf(t, "a later call", feed(string(usageLine(342_724, "")))); c.Window != 320_000 || *c.Tokens != 342_724 {
		t.Fatalf("mid-turn usage keeps the window: %+v", c)
	}
	limit := `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.4,"resetsAt":2000000000}}}}`
	if c := contextOf(t, "rate limit", feed(limit)); c.Window != 320_000 {
		t.Fatalf("a rate-limit status keeps the window: %+v", c)
	}
	if got := statuses(feed(string(usageLine(342_724, "")))); len(got) != 0 {
		t.Fatalf("the same usage again reports nothing new: %+v", got)
	}
	if got := statuses(feed(string(usageLine(90_000, "toolu_sub")))); len(got) != 0 {
		t.Fatalf("a subagent's usage is not the worker's context: %+v", got)
	}
	feed(string(raw))
	if len(replies.sent) != 1 {
		t.Fatalf("a known window is not asked again: %v", replies.sent)
	}
}

// TestClaudeWorkerInitKeepsCompaction: a worker launched with today's flags
// lists "compact" among its slash commands even under --setting-sources ""
// (Claude 2.1.332 probe), so compaction stays allowed. The empty list of
// the recorded 2.1.322/2.1.326 inits came from the retired
// --disable-slash-commands, under which /compact does not run, and still
// demotes it.
func TestClaudeWorkerInitKeepsCompaction(t *testing.T) {
	init := fixtureInit(t, "claude-worker-turn-2.1.326.jsonl")
	init["slash_commands"] = []string{"agents", "autocompact", "clear", "compact", "context", "model"}
	caps := resolveFixture(t, fixtureWorkerSpec(t), init)
	if slices.Contains(caps.Demoted, ItemCompaction) || !caps.Allows(ItemCompaction) {
		t.Fatalf("a current worker init demoted compaction: %+v", caps)
	}
	init["slash_commands"] = []string{}
	if caps := resolveFixture(t, fixtureWorkerSpec(t), init); !slices.Contains(caps.Demoted, ItemCompaction) {
		t.Fatalf("an init with slash commands disabled keeps compaction: %+v", caps)
	}
}

// TestClaudeWorkerHardCompactionEndToEnd: a Claude worker under
// worker_advisory "50%|160k" and worker_hard "80%|200k" launches with
// Claude's own automatic compaction set (claudeAutoCompactEnv). Its current
// init (2.1.332, "compact" listed) keeps compaction allowed. Its window is
// known from the init's get_context_usage, so the meter reads tokens over
// the window with both markers mid-turn. Usage past the hard point stays
// in the turn (a Claude worker gets no steer) and runs /compact at idle,
// not "the session catalog reports compaction unsupported".
func TestClaudeWorkerHardCompactionEndToEnd(t *testing.T) {
	h, _ := newCodexLeadHarness(t)
	claude := h.opts.Backends[BackendClaude]
	claude.Catalog = ClaudeCatalog()
	h.opts.Backends[BackendClaude] = claude
	h.startAt(codexSyntheticEndpoint)
	h.globalProfile("compact-worker", `{"backend":"claude"}`)
	resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
	for _, kv := range [][2]string{{"compaction.worker_advisory", "50%|160k"}, {"compaction.worker_hard", "80%|200k"}} {
		if err := resolver.Set(kv[0], kv[1], wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	h.holders.setControl("get_context_usage", "", `{"totalTokens":4193,"maxTokens":320000,"rawMaxTokens":320000,"autocompactSource":"env"}`)
	worker, err := h.runner.Spawn(SpawnSpec{Profile: "compact-worker", Root: h.root, Prompt: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	id := worker.ID
	holder := h.holder(id)
	if env := h.holders.spawned(holder).Env; env[claudeAutoCompactWindowEnv] != "320000" || env[claudeAutoCompactPctEnv] != "62" {
		t.Fatalf("launch env: window %q, pct %q", env[claudeAutoCompactWindowEnv], env[claudeAutoCompactPctEnv])
	}
	eventually(t, "initial turn dispatched", func() bool { return len(h.userInputs(holder)) == 1 })
	h.holders.out(holder, `{"type":"system","subtype":"init","session_id":"worker","model":"claude-opus-5-5","permissionMode":"dontAsk","claude_code_version":"2.1.332","slash_commands":["clear","compact","context"]}`)
	eventually(t, "the init's usage question", func() bool {
		return slices.ContainsFunc(h.holders.inputs(holder, "control_request"), func(line string) bool {
			return strings.Contains(line, `"get_context_usage"`) && strings.Contains(line, claudeContextAsk)
		})
	})
	h.holders.out(holder, string(usageLine(342_724, "")))
	c := h.runner.conv(id)
	eventually(t, "the mid-turn meter", func() bool {
		var ok bool
		c.do(func() {
			comp := c.contextCompaction
			ok = c.contextTokens != nil && *c.contextTokens == 342_724 && c.contextWindow == 320_000 &&
				comp != nil && comp.Soft == 160_000 && comp.Hard == 200_000 && c.aboveCompactHard()
		})
		return ok
	})
	if slices.Contains(h.userInputs(holder), "/compact") {
		t.Fatal("compaction dispatched inside the running turn")
	}
	h.holders.out(holder, resultLine())
	eventually(t, "native /compact at idle", func() bool { return slices.Contains(h.userInputs(holder), "/compact") })
	c.do(func() {
		if c.caps == nil || !c.caps.Allows(ItemCompaction) || strings.Contains(c.lastErr, "unsupported") {
			t.Errorf("caps %+v, lastErr %q", c.caps, c.lastErr)
		}
	})
}
