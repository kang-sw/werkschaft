package wsagent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// shippedAgyRegistration is the daemon's own agy registration over backend:
// worker-capable, with the cross-harness refusal.
func shippedAgyRegistration(backend Backend) BackendRegistration {
	reg := BuiltinBackendRegistrations()[BackendAgy]
	reg.Backend = backend
	return reg
}

// verifiedLeadCaller is the agent.* caller of a lead verified to run on
// backend, as a daemon-hosted lead's server builds it from the lead's record.
func verifiedLeadCaller(replyID, root, backend string, cfg wsconfig.Options) AgentCaller {
	caller := LeadCaller(replyID, root, backend, cfg)
	caller.VerifiedBackend = backend
	return caller
}

// The Decision 8 text of 261122-feat-ws-agy-same-harness-workers.
const wantAgyCrossHarnessRefusal = `harness "agy" accepts workers only from an agy session: Antigravity's consumer terms do not support running agy under another agent`

func TestShippedAgyRegistrationAdmitsSameHarnessWorkersOnly(t *testing.T) {
	reg := BuiltinBackendRegistrations()[BackendAgy]
	if !reg.WorkerCapable || reg.WorkerProfile != AgentWorkerProfile(BackendAgy) || reg.crossHarnessRefusal != wantAgyCrossHarnessRefusal || reg.LeadProfile != LeadProfileAgy {
		t.Fatalf("agy registration: worker %v %q, refusal %q, lead %q", reg.WorkerCapable, reg.WorkerProfile, reg.crossHarnessRefusal, reg.LeadProfile)
	}
	for _, name := range []string{BackendClaude, BackendCodex} {
		if other := BuiltinBackendRegistrations()[name]; !other.WorkerCapable || other.crossHarnessRefusal != "" {
			t.Errorf("%s: worker %v, refusal %q", name, other.WorkerCapable, other.crossHarnessRefusal)
		}
	}
}

// agyRoutingHarness registers the shipped agy posture over the fake backend
// with agy slots, an alias, a graph route, and a bundle that all end on agy.
func agyRoutingHarness(t *testing.T) *harness {
	t.Helper()
	h, _ := agentHarness(t, func(o *Options) {
		o.TierRoles = func() ([]wsconfig.TierRole, error) { return testPackagedRoles, nil }
		o.Backends[BackendAgy] = shippedAgyRegistration(o.Backends[BackendClaude].Backend)
	})
	// agy has slots, so a refusal is the cross-harness one, not a missing model.
	for _, size := range []string{"small", "large"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, size, "", "gemini-"+size, BackendAgy, "high"); err != nil {
			t.Fatal(err)
		}
	}
	setTierAliases(t, h.config.Config, "far=agy.large")
	setTierRouting(t, h.config.Config, `{"nodes":{"review":{"*":"agy.*"}},"bundles":{"agy-deep":{"harness":"agy","model":"gemini-deep"}}}`)
	return h
}

// Every spawn route that ends on agy is refused with the cross-harness text
// for a Claude, Codex, or Pi caller, for an unverified caller (a connection
// that only declared agy), and for no verified binding at all; agent.tiers
// names it under Unavailable and on each alias, route, and bundle that ends
// there.
func TestAgyCrossHarnessCallersAreRefusedOnEveryRoute(t *testing.T) {
	h := agyRoutingHarness(t)
	declaredAgy := LeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	for _, caller := range []struct {
		name   string
		caller AgentCaller
	}{
		{"claude lead", verifiedLeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.config.Config)},
		{"codex lead", verifiedLeadCaller(leadParent.ReplyID, h.root, BackendCodex, h.config.Config)},
		{"pi lead", verifiedLeadCaller(leadParent.ReplyID, h.root, BackendPi, h.config.Config)},
		{"unverified lead", h.leadCaller()},
		{"declared-agy lead without a verified binding", declaredAgy},
	} {
		tiers := []string{"agy.small", "far", "review:large", "agy-deep"}
		if caller.caller.Harness == BackendAgy {
			tiers = append(tiers, "small")
		}
		for _, tier := range tiers {
			_, err := h.runner.AgentSpawn(caller.caller, AgentSpawnRequest{Prompt: "x", Tier: tier, Authority: "read-only"})
			if err == nil {
				t.Fatalf("%s: %q spawned", caller.name, tier)
			}
			agentRefused(t, err, http.StatusBadRequest, wantAgyCrossHarnessRefusal)
		}
		list, err := h.runner.AgentTiers(caller.caller)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(list.Unavailable, "agy: "+wantAgyCrossHarnessRefusal) {
			t.Fatalf("%s: unavailable = %v", caller.name, list.Unavailable)
		}
		if len(list.Native) != 0 && caller.caller.Harness == BackendAgy {
			t.Fatalf("%s: native = %+v", caller.name, list.Native)
		}
		for _, e := range list.Ext {
			if strings.HasPrefix(e.Tier, "agy.") {
				t.Fatalf("%s: ext lists an agy tier: %+v", caller.name, list.Ext)
			}
		}
		if len(list.Aliases) != 1 || list.Aliases[0].Error != wantAgyCrossHarnessRefusal || list.Aliases[0].Model != "" {
			t.Fatalf("%s: aliases = %+v", caller.name, list.Aliases)
		}
		route := slices.IndexFunc(list.Routing.Routes, func(e AgentRouteEntry) bool { return e.Tier == "review:large" })
		if route < 0 || list.Routing.Routes[route].Error != wantAgyCrossHarnessRefusal || list.Routing.Routes[route].Harness != BackendAgy {
			t.Fatalf("%s: routes = %+v", caller.name, list.Routing.Routes)
		}
		bundle := slices.IndexFunc(list.Routing.Bundles, func(e AgentRouteEntry) bool { return e.Tier == "agy-deep" })
		if bundle < 0 || list.Routing.Bundles[bundle].Error != wantAgyCrossHarnessRefusal {
			t.Fatalf("%s: bundles = %+v", caller.name, list.Routing.Bundles)
		}
	}
	if n := len(h.runner.AgentList(h.leadCaller())); n != 0 {
		t.Fatalf("a refused agy route spawned %d children", n)
	}
	if h.backend.startCount() != 0 {
		t.Fatal("a refused agy route started a process")
	}
	for _, caller := range []string{"", BackendClaude, BackendCodex, BackendPi} {
		if why, known := h.runner.HarnessAvailability(caller, BackendAgy); why != wantAgyCrossHarnessRefusal || !known {
			t.Fatalf("availability for %q = %q (known %v)", caller, why, known)
		}
		if why := h.runner.CallerRefusal(caller, BackendAgy); why != wantAgyCrossHarnessRefusal {
			t.Fatalf("caller refusal for %q = %q", caller, why)
		}
	}
	// A backend without the option is unaffected for every caller.
	for _, caller := range []string{"", BackendClaude, BackendAgy, BackendCodex} {
		if why := h.runner.SpawnableHarness(caller, BackendClaude); why != "" {
			t.Fatalf("claude for %q = %q", caller, why)
		}
		c := verifiedLeadCaller(leadParent.ReplyID, h.root, caller, h.config.Config)
		if got := h.spawnBinding(c, "claude.small"); got.Backend != BackendClaude || got.Model != "claude-small" {
			t.Fatalf("claude worker of %q = %+v", caller, got)
		}
	}
}

// A verified agy lead spawns agy workers on every route, sees agy tiers in
// agent.tiers, and each child records its spawner and stays keyed under its
// parent (listed, counted toward width).
func TestAgyLeadSpawnsAgyWorkersOnEveryRoute(t *testing.T) {
	h := agyRoutingHarness(t)
	agyLead := verifiedLeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	for _, tc := range []struct{ tier, model string }{
		{"agy.small", "gemini-small"},
		{"small", "gemini-small"},
		{"far", "gemini-large"},
		{"review:large", "gemini-large"},
		{"agy-deep", "gemini-deep"},
	} {
		if got := h.spawnBinding(agyLead, tc.tier); got.Backend != BackendAgy || got.Model != tc.model {
			t.Fatalf("%q = %+v", tc.tier, got)
		}
	}
	list, err := h.runner.AgentTiers(agyLead)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range list.Unavailable {
		if strings.HasPrefix(u, "agy:") {
			t.Fatalf("unavailable = %v", list.Unavailable)
		}
	}
	if !slices.ContainsFunc(list.Native, func(e AgentTierEntry) bool { return e.Tier == "small" && e.Model == "gemini-small" }) || !slices.ContainsFunc(list.Ext, func(e AgentTierEntry) bool { return e.Tier == "agy.large" && e.Model == "gemini-large" }) {
		t.Fatalf("native %+v, ext %+v", list.Native, list.Ext)
	}
	if len(list.Aliases) != 1 || list.Aliases[0].Error != "" || list.Aliases[0].Model != "gemini-large" {
		t.Fatalf("aliases = %+v", list.Aliases)
	}
	route := slices.IndexFunc(list.Routing.Routes, func(e AgentRouteEntry) bool { return e.Tier == "review:large" })
	if route < 0 || list.Routing.Routes[route].Error != "" || list.Routing.Routes[route].Model != "gemini-large" {
		t.Fatalf("routes = %+v", list.Routing.Routes)
	}
	if why, known := h.runner.HarnessAvailability(BackendAgy, BackendAgy); why != "" || !known {
		t.Fatalf("availability = %q (known %v)", why, known)
	}
	id := h.agentSpawn(agyLead, AgentSpawnRequest{Prompt: "x", Tier: "agy.small", Authority: "read-only"})
	rec := h.record(id)
	if rec.SpawnerBackend != BackendAgy || rec.Parent == nil || *rec.Parent != leadParent {
		t.Fatalf("record = spawner %q, parent %+v", rec.SpawnerBackend, rec.Parent)
	}
	if child := findAgent(t, h.runner.AgentList(LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.config.Config)), id); child.Tier != "agy.small" {
		t.Fatalf("listed child = %+v", child)
	}
	h.runner.mu.Lock()
	awake := h.runner.awakeChildrenLocked(leadParent)
	h.runner.mu.Unlock()
	if awake == 0 {
		t.Fatal("a stamped agy child does not count toward its parent's width")
	}
}

// An agy worker spawns a nested agy child (its token route caller is its own
// verified record); a Claude worker of the same agy lead does not.
func TestAgyWorkerSpawnsNestedAgyChild(t *testing.T) {
	h := agyRoutingHarness(t)
	agyLead := verifiedLeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	agyWorker, _ := h.agentRunning(agyLead, AgentSpawnRequest{Prompt: "work", Tier: "agy.small", Authority: "read-only"})
	nested := h.agentSpawn(h.holderOf(agyWorker), AgentSpawnRequest{Prompt: "nested", Tier: "agy.small", Authority: "read-only"})
	if rec := h.record(nested); rec.Backend != BackendAgy || rec.SpawnerBackend != BackendAgy || rec.Parent.Kind != ParentConversation || rec.Parent.ReplyID != agyWorker {
		t.Fatalf("nested record = %+v", rec)
	}
	claudeWorker, _ := h.agentRunning(agyLead, AgentSpawnRequest{Prompt: "work", Tier: "claude.small", Authority: "read-only"})
	_, err := h.runner.AgentSpawn(h.holderOf(claudeWorker), AgentSpawnRequest{Prompt: "nested", Tier: "agy.small", Authority: "read-only"})
	agentRefused(t, err, http.StatusBadRequest, wantAgyCrossHarnessRefusal)
}

// resolveSpawnerBackends derives a legacy record's spawner from its parent's
// record whatever order the records were read in; a record with no parent
// record, or on a backend without the option, keeps none.
func TestResolveSpawnerBackendsIgnoresLoadOrder(t *testing.T) {
	r := &Runner{opts: Options{Backends: map[string]BackendRegistration{
		BackendAgy:    {crossHarnessRefusal: wantAgyCrossHarnessRefusal},
		BackendClaude: {},
	}}}
	child := Record{ReplyID: "child", Backend: BackendAgy, Parent: &ParentRef{Kind: ParentConversation, ReplyID: "agyparent"}}
	nested := Record{ReplyID: "nested", Backend: BackendAgy, Parent: &ParentRef{Kind: ParentConversation, ReplyID: "claudeparent"}}
	orphan := Record{ReplyID: "orphan", Backend: BackendAgy, Parent: &ParentRef{Kind: ParentLead, ReplyID: "gone"}}
	claude := Record{ReplyID: "claudechild", Backend: BackendClaude, Parent: &ParentRef{Kind: ParentConversation, ReplyID: "agyparent"}}
	stamped := Record{ReplyID: "stamped", Backend: BackendAgy, SpawnerBackend: BackendClaude, Parent: &ParentRef{Kind: ParentConversation, ReplyID: "agyparent"}}
	agyLead := Record{ReplyID: "agyparent", Backend: BackendAgy, Lead: true}
	claudeParent := Record{ReplyID: "claudeparent", Backend: BackendClaude}
	for _, order := range [][]Record{
		{child, nested, orphan, claude, stamped, agyLead, claudeParent},
		{agyLead, claudeParent, child, nested, orphan, claude, stamped},
	} {
		all := slices.Clone(order)
		r.resolveSpawnerBackends(all)
		got := map[string]string{}
		for _, rec := range all {
			got[rec.ReplyID] = rec.SpawnerBackend
		}
		want := map[string]string{"child": BackendAgy, "nested": BackendClaude, "orphan": "", "claudechild": "", "stamped": BackendClaude, "agyparent": "", "claudeparent": ""}
		for id, w := range want {
			if got[id] != w {
				t.Fatalf("order %v: %s spawner = %q, want %q", order[0].ReplyID, id, got[id], w)
			}
		}
		if r.admitWorkerRecord(all[slices.IndexFunc(all, func(rec Record) bool { return rec.ReplyID == "child" })]) != nil {
			t.Fatal("the legacy child of an agy parent is refused")
		}
	}
}

// The profile-based creation route has no spawning session, so it refuses
// the agy worker profile and any other non-lead agy profile with the
// cross-harness text, before anything is made.
func TestAgyCrossHarnessRefusesProfileCreation(t *testing.T) {
	h := newHarness(t, map[string]string{"agy-chat": `{"backend":"agy","model":"gemini","instructions":"agent.md"}`})
	h.runner.Close()
	h.config.Backends[BackendAgy] = shippedAgyRegistration(h.backend)
	h.runner = NewRunner(h.config)
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: h.dir, Port: 0, Sessions: leadSessions{}, Agents: h.runner, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	dh := &daemonHarness{harness: h, base: fmt.Sprintf("http://127.0.0.1:%d", d.Endpoint().Port), cookie: daemonCookieForTest(t, d)}
	for _, profile := range []string{AgentWorkerProfile(BackendAgy), "agy-chat"} {
		r := dh.post("/agents", map[string]string{"profile": profile, "root": h.root})
		if r.status != http.StatusForbidden || !strings.Contains(r.body, wantAgyCrossHarnessRefusal) {
			t.Fatalf("POST /agents %s = %d %s", profile, r.status, r.body)
		}
	}
	var list struct {
		Conversations []State `json:"conversations"`
	}
	if err := json.Unmarshal([]byte(dh.do(http.MethodGet, "/agents", "", "", nil).body), &list); err != nil || len(list.Conversations) != 0 {
		t.Fatalf("a refused creation left %+v (%v)", list, err)
	}
}

// A permanent cross-harness refusal closes the historical row at startup.
func TestAgyWorkerRecordClosesAtStartup(t *testing.T) {
	h, got := agentHarness(t, func(o *Options) {
		o.Backends[BackendAgy] = BackendRegistration{Backend: o.Backends[BackendClaude].Backend, Permission: builtinPermission(BackendAgy), Lifecycle: builtinLifecycle(BackendAgy), WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendAgy)}
	})
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, "small", "", "gemini-small", BackendAgy, "medium"); err != nil {
		t.Fatal(err)
	}
	lead := h.leadCaller()
	id, p := h.agentRunning(lead, AgentSpawnRequest{Prompt: "work", Tier: "agy.small", Authority: "read-only"})
	p.reply("retained reply")
	eventually(t, "the agy worker's report", func() bool { return len(got.list()) == 1 })
	h.asleep(id)
	h.runner.Close()
	reg := h.config.Backends[BackendAgy]
	reg.crossHarnessRefusal = agyCrossHarnessRefusal
	h.config.Backends[BackendAgy] = reg
	h.start()
	starts := h.backend.startCount()
	if st := h.state(id); st.Process || st.Status != StatusClosed {
		t.Fatalf("dead worker = %+v", st)
	}
	if child := findAgent(t, h.runner.AgentList(lead), id); child.Tier != "agy.small" || child.State != AgentClosed {
		t.Fatalf("listed closed child = %+v", child)
	}
	if _, err := h.runner.Send(id, "resume"); err != errClosed || h.backend.startCount() != starts {
		t.Fatalf("closed send = %v, starts = %d, want %d", err, h.backend.startCount(), starts)
	}
	if rec := h.record(id); rec.ClosedAt == "" || rec.Backend != BackendAgy || rec.LastTurn == nil || rec.LastTurn.Result.Message != "retained reply" {
		t.Fatalf("closed history = %+v", rec)
	}
}

// A stamped agy-parented record wakes after a restart, and an unstamped one
// wakes from its agy parent's record (resolveSpawnerBackends).
func TestAgyParentedWorkerRecordWakesStampedOrFromItsParent(t *testing.T) {
	h := agyRoutingHarness(t)
	agyLead := verifiedLeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	parent, pp := h.agentRunning(agyLead, AgentSpawnRequest{Prompt: "work", Tier: "agy.small", Authority: "read-only"})
	child, cp := h.agentRunning(h.holderOf(parent), AgentSpawnRequest{Prompt: "nested", Tier: "agy.small", Authority: "read-only"})
	pp.reply("parent done")
	cp.reply("child done")
	// The child's report reaches its parent, as a turn of its own, before
	// either sleeps.
	eventually(t, "the nested report delivered", func() bool {
		rec := h.record(child)
		return rec.LastTurn != nil && len(rec.PendingReports) == 0
	})
	pp.nextSent(t)
	pp.reply("report read")
	h.asleep(child)
	h.asleep(parent)
	h.runner.Close()
	rec := h.record(child)
	rec.SpawnerBackend = ""
	if err := writeRecord(wsdashboard.SessionDir(h.dir, child), rec); err != nil {
		t.Fatal(err)
	}
	h.start()
	for _, id := range []string{parent, child} {
		starts := h.backend.startCount()
		if _, err := h.runner.Send(id, "again"); err != nil {
			t.Fatal(err)
		}
		h.backend.waitProc(t, starts)
		if st := h.state(id); st.Error != "" {
			t.Fatalf("%s: state = %+v", id, st)
		}
	}
	if got := h.record(child).SpawnerBackend; got != BackendAgy {
		t.Fatalf("the legacy child's derived spawner = %q", got)
	}
}

// Startup closes the cross-harness row without stopping or rejoining its holder.
func TestAgyWorkerCrossHarnessCloseRetainsTheHolder(t *testing.T) {
	h, _ := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder reconciliation", h.runner.rejoined)
	if st := h.state(id); st.Process || st.Status != StatusClosed {
		t.Fatalf("state = %+v", st)
	}
	if rec, ok := h.runner.recordOf(id); !ok || rec.HolderID != holder || rec.ClosedAt == "" || rec.TurnOpen || rec.MCPToken != "" || rec.Capability != "" {
		t.Fatalf("in-memory closed record = %+v, found %v", rec, ok)
	}
	if rec := h.record(id); rec.ClosedAt != "" || rec.HolderID != holder {
		t.Fatalf("the old daemon's holder ownership record must stay open: %+v", rec)
	}
	if h.holders.opCount("stop "+holder) != 0 || h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("an automatic close altered a live holder: %v", h.holders.opList())
	}

	h.runner.Close()
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder reconciliation after restart", h.runner.rejoined)
	if st := h.state(id); st.Status != StatusClosed || h.holders.opCount("stop "+holder) != 0 {
		t.Fatalf("the next daemon must re-derive the closed row without stopping the holder: %+v, %v", st, h.holders.opList())
	}

	h.runner.Close()
	h.holders.vanish(holder)
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	wait(t, "complete holder inventory", h.runner.rejoined)
	if rec := h.record(id); rec.ClosedAt == "" || rec.HolderID != "" || rec.TurnOpen {
		t.Fatalf("the finished holder's closure must now be durable: %+v", rec)
	}
	if h.holders.opCount("stop "+holder) != 0 {
		t.Fatalf("startup signalled the holder: %v", h.holders.opList())
	}
}

// crossHarnessAgyNextDaemon ends the daemon, its held processes running on,
// and makes agy worker id a cross-harness one for the next daemon: its
// record now names a Claude spawner, as a Claude lead's spawn under an older
// daemon would. The registration keeps the shipped cross-harness refusal.
func crossHarnessAgyNextDaemon(t *testing.T, h *leadHarness, id string) {
	t.Helper()
	h.runner.Close()
	if h.holders.opCount("stop") != 0 {
		t.Fatalf("runner shutdown stopped a worker: %v", h.holders.opList())
	}
	if reg := h.opts.Backends[BackendAgy]; reg.crossHarnessRefusal != agyCrossHarnessRefusal {
		t.Fatalf("fixture: agy registration lost the cross-harness refusal: %+v", reg)
	}
	rewriteRecord(t, h, id, func(rec *Record) { rec.SpawnerBackend = BackendClaude })
	h.holders.restart()
}

// A held agy worker with no spawner stamp whose parent record is an agy
// conversation is admitted from that parent at startup: it rejoins its
// holder, which is never stopped.
func TestAgyWorkerUnstampedRejoinsFromItsAgyParentRecord(t *testing.T) {
	h, _ := workerRecoveryHarness(t, BackendAgy)
	parent, _ := idleHeldAgyWorker(t, h)
	child, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	h.runner.Close()
	if h.holders.opCount("stop") != 0 {
		t.Fatalf("runner shutdown stopped a worker: %v", h.holders.opList())
	}
	rewriteRecord(t, h, child, func(rec *Record) {
		rec.Parent = &ParentRef{Kind: ParentConversation, ReplyID: parent}
		rec.SpawnerBackend = ""
	})
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the unstamped worker rejoined", func() bool {
		c := h.runner.conv(child)
		if c == nil {
			return false
		}
		ready := false
		c.do(func() { ready = c.proc != nil && !c.replaying && !c.crossHarnessRejoin })
		return ready
	})
	if st := h.state(child); st.Error != "" || st.Status == StatusClosed {
		t.Fatalf("state = %+v", st)
	}
	if rec := h.record(child); rec.HolderID != holder {
		t.Fatalf("the rejoin dropped the holder: %+v", rec)
	}
	if h.holders.opCount("stop") != 0 || h.holders.opCount("launch") != 2 {
		t.Fatalf("an agy-parented rejoin stopped or relaunched: %v", h.holders.opList())
	}
}

// idleHeldAgyWorker is a held agy worker whose turn ended, its process idle
// under its holder.
func idleHeldAgyWorker(t *testing.T, h *leadHarness) (string, string) {
	t.Helper()
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	completeRecoveryWorker(t, h, BackendAgy, holder)
	eventually(t, "the turn to settle", func() bool { return !h.record(id).TurnOpen })
	return id, holder
}

// rewriteRecord edits id's record on disk while no daemon runs.
func rewriteRecord(t *testing.T, h *leadHarness, id string, edit func(*Record)) {
	t.Helper()
	rec := h.record(id)
	edit(&rec)
	if err := writeRecord(wsdashboard.SessionDir(h.opts.Dir, id), rec); err != nil {
		t.Fatal(err)
	}
}

func TestAgyWorkerRecordRefusalIgnoresItsProfile(t *testing.T) {
	for _, tc := range []struct{ name, profile string }{
		{name: "profile removed"},
		{name: "profile on another backend", profile: `{"backend":"claude","model":"haiku","instructions":"agent.md"}`},
		{name: "profile now a lead", profile: `{"backend":"agy","lead":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"agy-custom": `{"backend":"agy","model":"gemini","instructions":"agent.md"}`})
			h.runner.Close()
			h.config.Backends[BackendAgy] = BackendRegistration{Backend: h.backend, Permission: builtinPermission(BackendAgy), Lifecycle: builtinLifecycle(BackendAgy), WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendAgy)}
			h.start()
			id := h.create("agy-custom").ID
			h.runner.Close()
			values := map[string]string{}
			if tc.profile != "" {
				values[ProfilePrefix+"agy-custom"] = tc.profile
			}
			writeStructured(t, filepath.Join(h.root, ".ws-workflow", "config.json"), values)
			h.config.Backends[BackendAgy] = shippedAgyRegistration(h.backend)
			h.start()
			starts := h.backend.startCount()
			if _, err := h.runner.Send(id, "hello"); err != errClosed {
				t.Fatalf("send to closed worker: %v", err)
			}
			if st := h.state(id); st.Process || st.Status != StatusClosed || h.backend.startCount() != starts {
				t.Fatalf("state = %+v, starts %d (was %d)", st, h.backend.startCount(), starts)
			}
		})
	}
}

// Peer mail still reaches a hosted agy lead under the shipped agy posture.
func TestAgyLeadTakesPeerMail(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.mail.deliverFrom(h.record(id).WSKey, "peerreplyid", "From peer@x: ping")
	holder := h.holder(id)
	eventually(t, "the mail prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	prompts := h.holders.acpInputs(holder, "session/prompt")
	if len(prompts) != 1 || !strings.Contains(string(prompts[0]), "ping") {
		t.Fatalf("prompt = %s", prompts)
	}
}
