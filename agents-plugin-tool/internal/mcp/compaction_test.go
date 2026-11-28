package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

type compactAccessRunner struct {
	AgentHost
	caller string
}

func (*compactAccessRunner) Watch(func(wsagent.State)) func() { return func() {} }

func (r *compactAccessRunner) Compact(caller, _ string, _ *bool) wsagent.CompactResult {
	r.caller = caller
	return wsagent.CompactResult{Status: "queued"}
}

func TestCompactionToolIsListedAndCallableForBuiltinProfiles(t *testing.T) {
	setupMailboxTestEnv(t)
	for name, raw := range wsagent.BuiltinProfiles() {
		profile, err := wsagent.ParseProfile(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, authority := range []wsagent.PermissionLevel{wsagent.PermissionReadOnly, wsagent.PermissionWorkdirWrite, wsagent.PermissionFull} {
			t.Run(name+"/"+string(authority), func(t *testing.T) {
				root := t.TempDir()
				runner := &compactAccessRunner{}
				env := append(os.Environ(), envAllowedTools+"="+strings.Join(profile.WSTools, ","), wsagent.EnvPermissionLevel+"="+string(authority))
				s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Env: env, Agents: runner})
				t.Cleanup(s.Close)
				role := roleDelegate
				if profile.Lead {
					role = roleLead
				} else if len(profile.WSTools) == 0 {
					t.Fatal("worker profile has no ws attachment")
				}
				key, err := s.sessions.mint(root, role, "")
				if err != nil {
					t.Fatal(err)
				}
				if !listedToolNames(t, s)["compact"] {
					t.Fatal("tools/list omitted compact")
				}
				resp := keyedCall(t, s, 1, key, "compact", nil)
				if toolIsError(t, resp) || runner.caller != key {
					t.Fatalf("compact did not reach the runner: %s", resp)
				}
			})
		}
	}
}

func TestCompactionRegistryManifestParity(t *testing.T) {
	legacy, _ := loadDeclaredConfig(claudeManifestPath).lookup("claude.compaction_advisory_percent")
	if legacy.Pattern != wsconfig.ThresholdPattern {
		t.Fatal("threshold grammar drifted from shipped manifests")
	}
	for key, def := range wsconfig.CompactionDefaults {
		e, ok := resolveConfigEntryForKey(key)
		if !ok || e.HarnessBucketed() || !e.HarnessStored() || !e.RepoScoped() {
			t.Fatal(key, e)
		}
		if builtinConfigDefaults()[key] != def {
			t.Fatal(key)
		}
		if key == wsconfig.ItemCompactionLeadMode || key == wsconfig.ItemCompactionWorkerMode {
			if def != wsconfig.CompactionModeWS {
				t.Fatalf("%s default = %q, want %q", key, def, wsconfig.CompactionModeWS)
			}
			for _, v := range []string{wsconfig.CompactionModeNative, wsconfig.CompactionModeWS} {
				if _, err := normalizeTuneScalar(e, v, true); err != nil {
					t.Fatalf("%s rejected %s: %v", key, v, err)
				}
			}
			for _, v := range []string{"auto", "off", "50%", ""} {
				if _, err := normalizeTuneScalar(e, v, true); err == nil {
					t.Fatalf("%s accepted %s", key, v)
				}
			}
			continue
		}
		for _, v := range thresholdAccepted {
			if _, err := e.Normalize(v); err != nil {
				t.Fatalf("%s rejected %s", key, v)
			}
		}
		for _, v := range thresholdRejected {
			if _, err := e.Normalize(v); err == nil {
				t.Fatalf("%s accepted %s", key, v)
			}
		}
	}
}

func TestCompactionConfigGetTuneAndLegacy(t *testing.T) {
	s, _, key := manifestServer(t)
	s.observeHarness("test", "agy")
	tune := func(k, v, harness, scope string) {
		t.Helper()
		r := keyedCall(t, s, 1, key, "config.tune", map[string]any{"key": k, "value": v, "harness": harness, "scope": scope})
		if toolIsError(t, r) {
			t.Fatal(r)
		}
	}
	// New harness values remain scalar overrides, including agy.
	tune("compaction.lead_advisory", "70%", "agy", "global")
	tune("compaction.lead_advisory", "40%", "*", "project")
	result, err := s.resolveConfigGet(key, "compaction.lead_advisory")
	if err != nil || result.Value != "40%" || result.Scope != "project" {
		t.Fatal(result, err)
	}
	tune("compaction.worker_hard", "90%", "agy", "project")
	resolver := s.sessionResolver(key, builtinConfigDefaults())
	rv, err := resolver.Get(key, "compaction.worker_hard.agy")
	if err != nil || rv.Value != "90%" {
		t.Fatal(rv, err)
	}
	// Unknown tool refusal remains the older-daemon contract (no dispatch).
	r := keyedCall(t, s, 2, key, "compact", map[string]any{})
	if toolIsError(t, r) || !strings.Contains(toolText(t, r), "unsupported") {
		t.Fatal(r)
	}
}

type compactionLiveBackend struct {
	mu     sync.Mutex
	starts int
	sends  []string
	proc   *lifecycleProcess
}

func (b *compactionLiveBackend) Start(context.Context, wsagent.StartSpec) (wsagent.Process, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts++
	p := &lifecycleProcess{
		backend: &lifecycleBackend{},
		id:      fmt.Sprintf("compact-holder-%d", b.starts),
		events:  make(chan wsagent.Event, 32),
	}
	b.proc = p
	return &compactionLiveProcess{lifecycleProcess: p, parent: b}, nil
}

func (b *compactionLiveBackend) counts() (int, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starts, append([]string(nil), b.sends...)
}

func (b *compactionLiveBackend) emit(ev wsagent.Event) {
	b.mu.Lock()
	p := b.proc
	b.mu.Unlock()
	p.events <- ev
}

type compactionLiveProcess struct {
	*lifecycleProcess
	parent *compactionLiveBackend
}

func (p *compactionLiveProcess) Send(text string) error {
	p.parent.mu.Lock()
	p.parent.sends = append(p.parent.sends, text)
	p.parent.mu.Unlock()
	return nil
}

func startCompactionTestRunner(t *testing.T, s *Server) (*wsagent.Runner, *compactionLiveBackend) {
	t.Helper()
	keys := NewAgentKeyStore()
	backend := &compactionLiveBackend{}
	runner := wsagent.NewRunner(wsagent.Options{
		Dir:      t.TempDir(),
		Config:   s.configOptions(),
		Keys:     keys,
		LeadKeys: keys,
		Holders:  lifecycleHolders{},
		Backends: map[string]wsagent.BackendRegistration{
			wsagent.BackendClaude: {
				Backend:         backend,
				Permission:      wsagent.BuiltinBackendRegistrations()[wsagent.BackendClaude].Permission,
				Lifecycle:       wsagent.BuiltinBackendRegistrations()[wsagent.BackendClaude].Lifecycle,
				Catalog:         wsagent.ClaudeCatalog(),
				LeadProfile:     wsagent.LeadProfileClaude,
				PermissionModes: wsagent.LeadPermissionModes,
			},
		},
	})
	runner.Start(nil)
	t.Cleanup(runner.Close)
	return runner, backend
}

func TestCompactionConfigTuneLiveReloadsRunningConversation(t *testing.T) {
	s, root, key := manifestServer(t)
	s.observeHarness("test", wsagent.BackendClaude)
	tune := func(id int, args map[string]any) {
		t.Helper()
		r := keyedCall(t, s, id, key, "config.tune", args)
		if toolIsError(t, r) {
			t.Fatalf("config.tune(%v) failed: %s", args, r)
		}
	}

	// Keep 60% below advisory so this test exercises the hard threshold.
	tune(1, map[string]any{"key": "compaction.lead_advisory", "value": "70%", "harness": "*", "scope": "global"})
	tune(2, map[string]any{"key": "compaction.lead_hard", "value": "80%", "harness": "*", "scope": "global"})
	runner, backend := startCompactionTestRunner(t, s)
	st, err := runner.CreateLead(wsagent.BackendClaude, root, wsagent.LeadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Send(st.ID, "first"); err != nil {
		t.Fatal(err)
	}
	lifecycleEventually(t, "first turn sent", func() bool { _, sends := backend.counts(); return len(sends) == 1 })

	// Turn 1 ends at 60% usage: below X=80%, so no compaction triggers.
	tokens := int64(60000)
	backend.emit(wsagent.Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens, Window: 100000}}})
	backend.emit(wsagent.Event{TurnEnd: true})
	lifecycleEventually(t, "idle after turn 1", func() bool {
		got, err := runner.Get(st.ID)
		return err == nil && got.Status == wsagent.StatusIdle
	})
	if starts, sends := backend.counts(); starts != 1 || len(sends) != 1 {
		t.Fatalf("unexpected start/compact at 60%% < 80%%: starts=%d sends=%q", starts, sends)
	}

	// Tune global hard threshold to Y = 55% via config.tune without restarting.
	tune(3, map[string]any{"key": "compaction.lead_hard", "value": "55%", "harness": "*", "scope": "global"})

	// Next threshold check uses Y=55% and queues default ws-mode stage-1 compaction on the same running process.
	backend.emit(wsagent.Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens, Window: 100000}}})
	lifecycleEventually(t, "compact sent at tuned threshold Y=55%", func() bool {
		_, sends := backend.counts()
		return len(sends) == 2 && strings.Contains(sends[1], "Call ws/compact directly with lead_summary")
	})
	if starts, _ := backend.counts(); starts != 1 {
		t.Fatalf("conversation restarted on live reload: starts=%d, want 1", starts)
	}
}

func TestCompactionConfigTuneProjectShadowsGlobalLiveReload(t *testing.T) {
	s, root, key := manifestServer(t)
	s.observeHarness("test", wsagent.BackendClaude)
	tune := func(id int, args map[string]any) {
		t.Helper()
		r := keyedCall(t, s, id, key, "config.tune", args)
		if toolIsError(t, r) {
			t.Fatalf("config.tune(%v) failed: %s", args, r)
		}
	}

	// Opt out to native lead compaction and keep 60% below advisory so this test exercises the hard /compact threshold.
	tune(0, map[string]any{"key": "compaction.lead_mode", "value": wsconfig.CompactionModeNative, "harness": "*", "scope": "global"})
	tune(1, map[string]any{"key": "compaction.lead_advisory", "value": "70%", "harness": "*", "scope": "global"})
	tune(2, map[string]any{"key": "compaction.lead_hard", "value": "90%", "harness": "*", "scope": "project"})
	runner, backend := startCompactionTestRunner(t, s)
	st, err := runner.CreateLead(wsagent.BackendClaude, root, wsagent.LeadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Send(st.ID, "first"); err != nil {
		t.Fatal(err)
	}
	lifecycleEventually(t, "first turn sent", func() bool { _, sends := backend.counts(); return len(sends) == 1 })

	tokens60 := int64(60000)
	backend.emit(wsagent.Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens60, Window: 100000}}})
	backend.emit(wsagent.Event{TurnEnd: true})
	lifecycleEventually(t, "idle after turn 1", func() bool {
		got, err := runner.Get(st.ID)
		return err == nil && got.Status == wsagent.StatusIdle
	})

	// Global tune to 55% must still be shadowed by project scope 90%.
	tune(3, map[string]any{"key": "compaction.lead_hard", "value": "55%", "harness": "*", "scope": "global"})
	if _, err := runner.Send(st.ID, "second"); err != nil {
		t.Fatal(err)
	}
	// kick runs compactIdle before draining inbox: receiving "second" instead of
	// "/compact" proves 60% did not trigger under the shadowed global 55%.
	lifecycleEventually(t, "second turn sent without compact", func() bool {
		_, sends := backend.counts()
		return len(sends) == 2
	})
	if starts, sends := backend.counts(); starts != 1 || !strings.Contains(sends[1], "second") {
		t.Fatalf("global tune bypassed project shadow: starts=%d sends=%q", starts, sends)
	}
	backend.emit(wsagent.Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens60, Window: 100000}}})
	backend.emit(wsagent.Event{TurnEnd: true})
	lifecycleEventually(t, "idle after turn 2", func() bool {
		got, err := runner.Get(st.ID)
		return err == nil && got.Status == wsagent.StatusIdle
	})

	// Resetting the project override via config.tune exposes the global 55% live.
	tune(4, map[string]any{"key": "compaction.lead_hard", "reset": true, "harness": "*", "scope": "project"})
	backend.emit(wsagent.Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens60, Window: 100000}}})
	lifecycleEventually(t, "compact sent after project reset exposes global 55%", func() bool {
		_, sends := backend.counts()
		return len(sends) == 3 && sends[2] == "/compact"
	})
	if starts, _ := backend.counts(); starts != 1 {
		t.Fatalf("conversation restarted: starts=%d, want 1", starts)
	}
}

type compactAgendaTestRunner struct {
	AgentHost
	result      wsagent.CompactResult
	calls       int
	calledWith  *wscompact.LeadSummary
	calledNoArg bool
}

func (*compactAgendaTestRunner) Watch(func(wsagent.State)) func() { return func() {} }
func (r *compactAgendaTestRunner) Compact(string, string, *bool) wsagent.CompactResult {
	r.calls++
	r.calledNoArg = true
	return r.result
}
func (r *compactAgendaTestRunner) CompactWithSummary(_ string, _ string, _ *bool, lead *wscompact.LeadSummary) wsagent.CompactResult {
	r.calls++
	r.calledWith = lead
	return r.result
}

func TestCompactAgendaUpdatesBatchAndRestoreBundle(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	runner := &compactAgendaTestRunner{result: wsagent.CompactResult{Status: "queued", Reason: "ws-managed compaction waits for the idle boundary"}}
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: runner})
	t.Cleanup(s.Close)

	key, err := s.sessions.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	// Seed initial agenda blobs.
	for k, raw := range map[string]string{
		"keep":       `{"a":1}`,
		"merge_me":   `{"keep":1,"drop":2,"nested":{"x":10,"y":20},"big":9007199254740993}`,
		"replace_me": `{"old":true}`,
		"delete_me":  `{"gone":true}`,
	} {
		if err := s.sessions.setAgenda(key, k, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}

	summaryArg := map[string]any{
		"user_preferences": "concise", "working_practices": "", "unrecorded_decisions": "",
		"current_work": "agenda_updates", "next_step": "verify", "mood_rapport": "", "residual_details": "",
	}
	resp := keyedCall(t, s, 1, key, "compact", map[string]any{
		"target":       key,
		"lead_summary": summaryArg,
		"agenda_updates": map[string]any{
			"merge_me": map[string]any{
				"merge": true,
				"value": map[string]any{
					"drop":   nil,
					"added":  "ok",
					"nested": map[string]any{"y": nil, "z": 30},
				},
			},
			"replace_me":     map[string]any{"value": map[string]any{"new": 42}},
			"created":        map[string]any{"value": []any{1, 2, 3}},
			"delete_me":      nil,
			"missing_delete": nil,
		},
	})
	if toolIsError(t, resp) {
		t.Fatalf("compact with agenda_updates failed: %s", resp)
	}
	if runner.calls != 1 || runner.calledWith == nil {
		t.Fatalf("runner not invoked with lead_summary: calls=%d calledWith=%v", runner.calls, runner.calledWith)
	}

	rec, ok := s.sessions.readState(key, partAgenda)
	if !ok {
		t.Fatal("failed to read session agenda")
	}
	if _, exists := rec.Agenda["delete_me"]; exists {
		t.Fatalf("delete_me was not cleared: %s", rec.Agenda["delete_me"])
	}
	if _, exists := rec.Agenda["missing_delete"]; exists {
		t.Fatalf("missing_delete should remain absent: %s", rec.Agenda["missing_delete"])
	}
	if string(rec.Agenda["keep"]) != `{"a":1}` {
		t.Fatalf("keep mutated: %s", rec.Agenda["keep"])
	}
	if string(rec.Agenda["replace_me"]) != `{"new":42}` {
		t.Fatalf("replace_me = %s, want {\"new\":42}", rec.Agenda["replace_me"])
	}
	if string(rec.Agenda["created"]) != `[1,2,3]` {
		t.Fatalf("created = %s, want [1,2,3]", rec.Agenda["created"])
	}
	mergedStr := string(rec.Agenda["merge_me"])
	for _, want := range []string{`"keep":1`, `"added":"ok"`, `"x":10`, `"z":30`, `"big":9007199254740993`} {
		if !strings.Contains(mergedStr, want) {
			t.Fatalf("merge_me = %s, missing %s", mergedStr, want)
		}
	}
	for _, forbidden := range []string{`"drop"`, `"y"`} {
		if strings.Contains(mergedStr, forbidden) {
			t.Fatalf("merge_me = %s, unexpectedly kept %s", mergedStr, forbidden)
		}
	}

	// RenderHostedRestoreBundle reads the updated agenda.
	bundle, err := RenderHostedRestoreBundle(os.Environ(), root, HostedRender{Harness: wsagent.BackendClaude}, key, "compaction", nil)
	if err != nil {
		t.Fatalf("RenderHostedRestoreBundle: %v", err)
	}
	for _, want := range []string{"### agenda: merge_me", `"added": "ok"`, "### agenda: replace_me", "### agenda: created"} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("restore bundle missing %q:\n%s", want, bundle)
		}
	}
	if strings.Contains(bundle, "delete_me") {
		t.Fatalf("restore bundle still contains cleared key delete_me:\n%s", bundle)
	}

	// compactToolWithoutLeadSummary preserves agenda_updates and full compactTool fits cap.
	full := compactTool()
	rawFull, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawFull) > toolSchemaByteCap {
		t.Fatalf("compactTool() = %d bytes, exceeds cap %d", len(rawFull), toolSchemaByteCap)
	}
	t.Logf("compactTool() serialized size = %d bytes (cap %d)", len(rawFull), toolSchemaByteCap)
	stripped := compactToolWithoutLeadSummary(full)
	props := stripped["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, hasLead := props["lead_summary"]; hasLead {
		t.Fatal("compactToolWithoutLeadSummary did not strip lead_summary")
	}
	if _, hasAgenda := props["agenda_updates"]; !hasAgenda {
		t.Fatal("compactToolWithoutLeadSummary dropped agenda_updates")
	}
}

func TestCompactAgendaUpdatesValidationAtomic(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	runner := &compactAgendaTestRunner{result: wsagent.CompactResult{Status: "queued", Reason: "ok"}}
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: runner})
	t.Cleanup(s.Close)

	key, err := s.sessions.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.sessions.setAgenda(key, "stable", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args map[string]any
	}{
		{name: "non-object string", args: map[string]any{"agenda_updates": "bad"}},
		{name: "non-object array", args: map[string]any{"agenda_updates": []any{"a"}}},
		{name: "non-self target", args: map[string]any{"target": "other-session-key", "agenda_updates": map[string]any{"a": map[string]any{"value": 1}}}},
		{name: "non-self target empty updates", args: map[string]any{"target": "id:child1", "agenda_updates": map[string]any{}}},
		{name: "empty key", args: map[string]any{"agenda_updates": map[string]any{"": map[string]any{"value": 1}}}},
		{name: "whitespace key", args: map[string]any{"agenda_updates": map[string]any{"   ": map[string]any{"value": 1}}}},
		{name: "post-trim duplicate key", args: map[string]any{"agenda_updates": map[string]any{"dup": map[string]any{"value": 1}, " dup ": map[string]any{"value": 2}}}},
		{name: "non-object entry string", args: map[string]any{"agenda_updates": map[string]any{"k": "raw"}}},
		{name: "non-object entry array", args: map[string]any{"agenda_updates": map[string]any{"k": []any{1}}}},
		{name: "missing value", args: map[string]any{"agenda_updates": map[string]any{"k": map[string]any{"merge": true}}}},
		{name: "unknown field", args: map[string]any{"agenda_updates": map[string]any{"k": map[string]any{"value": 1, "extra": true}}}},
		{name: "non-bool merge", args: map[string]any{"agenda_updates": map[string]any{"k": map[string]any{"value": map[string]any{"a": 1}, "merge": "yes"}}}},
		{name: "merge true non-object value", args: map[string]any{"agenda_updates": map[string]any{"k": map[string]any{"value": []any{1}, "merge": true}}}},
		{name: "merge true null value", args: map[string]any{"agenda_updates": map[string]any{"k": map[string]any{"value": nil, "merge": true}}}},
		{name: "mixed valid and invalid batch", args: map[string]any{"agenda_updates": map[string]any{"stable": map[string]any{"value": map[string]any{"v": 999}}, "bad": "nope"}}},
		{name: "non-coercible lead_summary with valid agenda_updates", args: map[string]any{
			"lead_summary":   map[string]any{"user_preferences": map[string]any{"not": "a string"}},
			"agenda_updates": map[string]any{"stable": map[string]any{"value": map[string]any{"v": 999}}},
		}},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner.calls = 0
			resp := keyedCall(t, s, i+1, key, "compact", tc.args)
			if !toolIsError(t, resp) {
				t.Fatalf("expected error for %s, got: %s", tc.name, resp)
			}
			if runner.calls != 0 {
				t.Fatalf("runner called %d times on validation error", runner.calls)
			}
			rec, ok := s.sessions.readState(key, partAgenda)
			if !ok || len(rec.Agenda) != 1 || string(rec.Agenda["stable"]) != `{"v":1}` {
				t.Fatalf("agenda mutated on validation error: %+v", rec.Agenda)
			}
		})
	}

	// Null and empty object agenda_updates succeed and leave agenda untouched.
	for i, args := range []map[string]any{
		{"agenda_updates": nil},
		{"agenda_updates": map[string]any{}},
	} {
		runner.calls = 0
		resp := keyedCall(t, s, 100+i, key, "compact", args)
		if toolIsError(t, resp) || runner.calls != 1 {
			t.Fatalf("expected success for %v, got resp=%s calls=%d", args, resp, runner.calls)
		}
		rec, ok := s.sessions.readState(key, partAgenda)
		if !ok || len(rec.Agenda) != 1 || string(rec.Agenda["stable"]) != `{"v":1}` {
			t.Fatalf("agenda unexpectedly changed: %+v", rec.Agenda)
		}
	}
}

func TestCompactAgendaUpdatesNoMutationOnRunnerFailureOrUnsupported(t *testing.T) {
	setupMailboxTestEnv(t)
	for _, tc := range []struct {
		name   string
		runner AgentHost
		want   string
	}{
		{name: "runner unsupported", runner: &compactAgendaTestRunner{result: wsagent.CompactResult{Status: "unsupported", Reason: "no native compact"}}, want: "unsupported"},
		{name: "runner error", runner: &compactAgendaTestRunner{result: wsagent.CompactResult{Status: "error", Reason: "boom"}}, want: "error"},
		{name: "non-hosted server", runner: nil, want: "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: tc.runner})
			t.Cleanup(s.Close)
			key, err := s.sessions.mint(root, roleLead, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.sessions.setAgenda(key, "stable", json.RawMessage(`{"v":1}`)); err != nil {
				t.Fatal(err)
			}
			resp := keyedCall(t, s, 1, key, "compact", map[string]any{
				"agenda_updates": map[string]any{
					"stable": map[string]any{"value": map[string]any{"v": 2}},
					"added":  map[string]any{"value": "nope"},
				},
			})
			if toolIsError(t, resp) || !strings.Contains(toolText(t, resp), tc.want) {
				t.Fatalf("got %s, want %q status", resp, tc.want)
			}
			rec, ok := s.sessions.readState(key, partAgenda)
			if !ok || len(rec.Agenda) != 1 || string(rec.Agenda["stable"]) != `{"v":1}` {
				t.Fatalf("agenda mutated when compact returned %s: %+v", tc.want, rec.Agenda)
			}
		})
	}
}

func TestWhiteboardAgendaProviderDirtiedByCompact(t *testing.T) {
	if !whiteboardDirtyingTool("compact") {
		t.Fatal("whiteboardDirtyingTool(\"compact\") = false, want true")
	}
	for _, p := range whiteboardProviders {
		if p.id == "agenda" {
			if !slices.Contains(p.dirtiedBy, "compact") {
				t.Fatalf("agenda provider dirtiedBy = %v, missing \"compact\"", p.dirtiedBy)
			}
			return
		}
	}
	t.Fatal("agenda provider not found in whiteboardProviders")
}
