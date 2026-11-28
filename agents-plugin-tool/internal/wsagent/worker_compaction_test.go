package wsagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func TestWorkerCompactionModeResolution(t *testing.T) {
	cfg := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir(), RepoRoot: t.TempDir()}
	r := NewRunner(Options{Config: cfg, Backends: BuiltinBackendRegistrations()})
	c := &conv{r: r, rec: Record{Root: cfg.RepoRoot, WSKey: "key", Backend: BackendClaude}}

	NotifyCompactionConfigChanged()
	if got := c.workerCompactionMode(c.record()); got != wsconfig.CompactionModeWS {
		t.Fatalf("default worker mode = %q, want ws", got)
	}
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.Set(wsconfig.ItemCompactionWorkerMode+"."+BackendClaude, wsconfig.CompactionModeNative, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	NotifyCompactionConfigChanged()
	if got := c.workerCompactionMode(c.record()); got != wsconfig.CompactionModeNative {
		t.Fatalf("per-harness native override = %q, want native", got)
	}
	// The lead key is independent.
	if got := c.leadCompactionMode(c.record()); got != wsconfig.CompactionModeWS {
		t.Fatalf("lead mode leaked from worker override: %q", got)
	}
	// A different harness keeps the default.
	other := &conv{r: r, rec: Record{Root: cfg.RepoRoot, WSKey: "key2", Backend: BackendCodex}}
	if got := other.workerCompactionMode(other.record()); got != wsconfig.CompactionModeWS {
		t.Fatalf("codex worker mode = %q, want ws", got)
	}
}

func TestWerkWorkerCompactionFixedWS(t *testing.T) {
	if !WorkerCompactionWSOnly(BackendWerk) {
		t.Fatal("werk must force ws worker compaction")
	}
	if WorkerCompactionWSOnly(BackendClaude) {
		t.Fatal("claude must not be ws-only")
	}
	if got := EffectiveWorkerCompactionMode(BackendWerk, wsconfig.CompactionModeNative); got != wsconfig.CompactionModeWS {
		t.Fatalf("werk native = %q, want ws", got)
	}
	if got := EffectiveWorkerCompactionMode(BackendClaude, wsconfig.CompactionModeNative); got != wsconfig.CompactionModeNative {
		t.Fatalf("claude native = %q", got)
	}
	if got := EffectiveWorkerCompactionMode(BackendClaude, "bogus"); got != wsconfig.CompactionModeWS {
		t.Fatalf("invalid raw falls back to default ws, got %q", got)
	}
}

func TestEmptyWorkerSummaryShape(t *testing.T) {
	s := emptyWorkerSummary()
	if s == nil || len(s.Prose) == 0 || len(s.Paths) != 0 {
		t.Fatalf("empty worker summary = %+v", s)
	}
}

// wsWorkerHarness runs a claude worker under compaction.worker_mode=ws.
func wsWorkerHarness(t *testing.T) (*leadHarness, string, string) {
	t.Helper()
	return wsWorkerHarnessWith(t, nil)
}

// wsWorkerHarnessWith is wsWorkerHarness with an optional tweak of the claude
// registration, e.g. to give the worker a steer-capable lifecycle.
func wsWorkerHarnessWith(t *testing.T, tweak func(*BackendRegistration)) (*leadHarness, string, string) {
	t.Helper()
	h := newLeadHarness(t)
	reg := h.opts.Backends[BackendClaude]
	reg.Catalog = ClaudeCatalog()
	if tweak != nil {
		tweak(&reg)
	}
	h.opts.Backends[BackendClaude] = reg
	resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
	for _, kv := range [][2]string{{wsconfig.ItemCompactionWorkerMode, wsconfig.CompactionModeWS}, {"compaction.worker_advisory", "50%|160k"}, {"compaction.worker_hard", "80%|200k"}} {
		if err := resolver.Set(kv[0], kv[1], wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	NotifyCompactionConfigChanged()
	h.start()
	h.globalProfile("compact-worker", `{"backend":"claude"}`)
	h.holders.setControl("get_context_usage", "", `{"totalTokens":4193,"maxTokens":320000,"rawMaxTokens":320000,"autocompactSource":"env"}`)
	worker, err := h.runner.Spawn(SpawnSpec{Profile: "compact-worker", Root: h.root, Prompt: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(worker.ID)
	eventually(t, "initial turn dispatched", func() bool { return len(h.userInputs(holder)) == 1 })
	return h, worker.ID, holder
}

func TestWSModeWorkerDirectCompactRotatesWithoutSummary(t *testing.T) {
	h, id, holder := wsWorkerHarness(t)
	key := h.record(id).WSKey
	// Queued mid-turn like the native worker path: it runs at the idle boundary.
	if res := h.runner.Compact(key, "", nil); res.Status != "queued" {
		t.Fatalf("Compact() = %+v, want queued", res)
	}
	h.holders.out(holder, resultLine())
	eventually(t, "worker holder rotated", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	if slices.Contains(h.userInputs(holder), "/compact") {
		t.Fatal("ws-mode worker must not send native /compact")
	}
	next := h.holder(id)
	eventually(t, "continuation with checkpoint", func() bool {
		in := h.userInputs(next)
		return len(in) >= 1 && strings.Contains(in[0], "<compaction-checkpoint") && strings.Contains(in[0], "Continue the work that was pending.")
	})
	if rec := h.record(id); rec.WSKey != key {
		t.Fatalf("ws key drifted: %q -> %q", key, rec.WSKey)
	}
}

func TestWSModeWorkerHardCompactionRotates(t *testing.T) {
	h, id, holder := wsWorkerHarness(t)
	h.holders.out(holder, `{"type":"system","subtype":"init","session_id":"worker","model":"claude-opus-5-5","permissionMode":"dontAsk","claude_code_version":"2.1.332","slash_commands":["clear","compact","context"]}`)
	h.holders.out(holder, string(usageLine(342_724, "")))
	h.holders.out(holder, resultLine())
	eventually(t, "hard crossing rotates worker without /compact", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	if slices.Contains(h.userInputs(holder), "/compact") {
		t.Fatal("ws-mode worker must not send native /compact")
	}
	for _, in := range h.userInputs(holder) {
		if strings.Contains(in, "lead_summary") {
			t.Fatalf("worker got a Stage 1 prose prompt: %q", in)
		}
	}
}

func TestWSModeWorkerCheckpointPreservesTaskAcrossMultipleRotations(t *testing.T) {
	h := newLeadHarness(t)
	reg := h.opts.Backends[BackendClaude]
	reg.Catalog = ClaudeCatalog()
	h.opts.Backends[BackendClaude] = reg
	resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
	for _, kv := range [][2]string{{wsconfig.ItemCompactionWorkerMode, wsconfig.CompactionModeWS}, {"compaction.worker_advisory", "50%|160k"}, {"compaction.worker_hard", "80%|200k"}} {
		if err := resolver.Set(kv[0], kv[1], wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	NotifyCompactionConfigChanged()
	h.start()
	h.globalProfile("compact-worker", `{"backend":"claude"}`)
	h.holders.setControl("get_context_usage", "", `{"totalTokens":4193,"maxTokens":320000,"rawMaxTokens":320000,"autocompactSource":"env"}`)

	const origPrompt = "Implement 261127-bug-worker-task-anchor and verify all tests."
	const followUp = "Narrow scope to internal/wsagent only."
	worker, err := h.runner.Spawn(SpawnSpec{Profile: "compact-worker", Root: h.root, Prompt: origPrompt})
	if err != nil {
		t.Fatal(err)
	}
	id := worker.ID
	key := h.record(id).WSKey
	holder1 := h.holder(id)
	eventually(t, "initial turn dispatched", func() bool { return len(h.userInputs(holder1)) == 1 })
	h.holders.out(holder1, resultLine())

	// Send a follow-up parent instruction so LatestInstruction is also recorded.
	if _, err := h.runner.Send(id, followUp); err != nil {
		t.Fatal(err)
	}
	eventually(t, "follow-up dispatched", func() bool { return len(h.userInputs(holder1)) == 2 })

	// Compaction #1
	if res := h.runner.Compact(key, "", nil); res.Status != "queued" {
		t.Fatalf("Compact #1 = %+v", res)
	}
	h.holders.out(holder1, resultLine())
	eventually(t, "rotation #1", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder1
	})
	holder2 := h.holder(id)
	eventually(t, "continuation #1 sent", func() bool { return len(h.userInputs(holder2)) >= 1 })

	// Compaction #2 (previously dropped the original prompt & follow-up from ring slice!)
	if res := h.runner.Compact(key, "", nil); res.Status != "queued" {
		t.Fatalf("Compact #2 = %+v", res)
	}
	h.holders.out(holder2, resultLine())
	eventually(t, "rotation #2", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder2
	})
	holder3 := h.holder(id)
	eventually(t, "continuation #2 sent", func() bool { return len(h.userInputs(holder3)) >= 1 })

	cp := h.userInputs(holder3)[0]
	for _, want := range []string{
		"<task>\n## Task",
		"### Original prompt\n" + origPrompt,
		"### Latest instruction\n",
		followUp,
		"- Active ticket: 261127-bug-worker-task-anchor",
	} {
		if !strings.Contains(cp, want) {
			t.Fatalf("2nd-rotation worker checkpoint missing %q:\n%s", want, cp)
		}
	}
	if strings.Contains(cp, "<worker-summary>") {
		t.Fatalf("2nd-rotation worker checkpoint should omit empty <worker-summary>:\n%s", cp)
	}
	rec, err := h.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.InitialPrompt != origPrompt || !strings.Contains(rec.LatestInstruction, followUp) {
		t.Fatalf("persisted record task fields = (%q, %q), want (%q, containing %q)", rec.InitialPrompt, rec.LatestInstruction, origPrompt, followUp)
	}
}
