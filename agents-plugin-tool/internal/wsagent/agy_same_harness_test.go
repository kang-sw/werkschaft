package wsagent

import (
	"encoding/json"
	"errors"
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

// A held cross-harness agy worker whose holder kept running is not
// reattached: the first scan ends its holder through the ordinary stop, its
// open turn settles failed with the cross-harness text, and the closed
// historical record stays listed with the refusal.
func TestAgyWorkerRejoinEndsTheHolderAndKeepsTheRecord(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	h.startAt("http://127.0.0.1:1")

	eventually(t, "the holder ended", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	eventually(t, "the failed report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("report = %+v", rep.Result)
	}
	eventually(t, "the record to forget the holder", func() bool { rec := h.record(id); return rec.HolderID == "" && !rec.TurnOpen && rec.ClosedAt != "" })
	st := h.state(id)
	if st.Process || st.Status != StatusClosed || st.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("state = %+v", st)
	}
	caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
	if child := findAgent(t, h.runner.AgentList(caller), id); child.State != AgentClosed || child.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("listed child = %+v", child)
	}
	if h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("a cross-harness rejoin launched a process: %v", h.holders.opList())
	}
	if rec := h.record(id); rec.MCPToken != "" || rec.Capability != "" {
		t.Fatalf("the ended worker kept its credentials: %+v", rec)
	}
	if len(got.list()) != 1 {
		t.Fatalf("more than one report: %+v", got.list())
	}
}

// A refused holder stop keeps the holder in the record, says why, publishes
// nothing, and starts no process. The same daemon's heartbeat retries the
// stop until it succeeds; the turn then settles failed with the agy reason,
// reported once.
func TestAgyWorkerRejoinRetriesAFailedStopFromTheHeartbeat(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	h.holders.set(func() { h.holders.stopErr = errors.New("holder busy") })
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder scan", h.runner.rejoined)

	// The heartbeat retries while the stop keeps failing.
	eventually(t, "a heartbeat retry", func() bool { return h.holders.opCount("stop "+holder) >= 2 })
	if rec := h.record(id); rec.HolderID != holder || !rec.TurnOpen {
		t.Fatalf("a failed stop forgot the holder: %+v", rec)
	}
	// The process still runs under its holder (Process), detached: nothing
	// is submitted to it.
	if st := h.state(id); !strings.HasPrefix(st.Error, wantAgyCrossHarnessRefusal+"; ") || !strings.HasSuffix(st.Error, "holder busy") {
		t.Fatalf("state = %+v", st)
	}
	if len(got.list()) != 0 || h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("a failed stop published or launched: reports %+v, ops %v", got.list(), h.holders.opList())
	}

	h.holders.set(func() { h.holders.stopErr = nil })
	eventually(t, "the failed report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("report = %+v", rep.Result)
	}
	eventually(t, "the record to forget the holder", func() bool { rec := h.record(id); return rec.HolderID == "" && !rec.TurnOpen && rec.ClosedAt != "" })
	stops := h.holders.opCount("stop " + holder)
	time.Sleep(200 * time.Millisecond) // several heartbeats
	if st := h.state(id); st.Error != wantAgyCrossHarnessRefusal || st.Process || st.Status != StatusClosed {
		t.Fatalf("state = %+v", st)
	}
	if h.holders.opCount("stop "+holder) != stops || len(got.list()) != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("retired twice or relaunched: stops %d (was %d), reports %+v, ops %v", h.holders.opCount("stop "+holder), stops, got.list(), h.holders.opList())
	}
}

// A user's Stop while a failed retirement retries goes through the holder's
// own stop, not the detached stream's Close. A failed stop keeps the holder,
// shows the error, and leaves the heartbeat retry armed; a successful one
// clears everything and reports the turn once.
func TestAgyWorkerUserStopDuringAFailedRetirementStopsTheHolder(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	h.opts.heartbeat = time.Hour // the test drives each retry
	h.holders.set(func() { h.holders.stopErr = errors.New("holder busy") })
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder scan", h.runner.rejoined)
	eventually(t, "the failed retirement stop", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	c := h.runner.conv(id)
	armed := func() bool {
		ok := false
		c.do(func() { ok = c.crossHarnessRetiring && c.replaying && c.proc != nil })
		return ok
	}
	stops := func() int { return h.holders.opCount("stop " + holder) }

	st, err := h.runner.Stop(id)
	if err != nil {
		t.Fatal(err)
	}
	if stops() != 2 {
		t.Fatalf("the user's Stop did not stop the holder: %v", h.holders.opList())
	}
	if rec := h.record(id); rec.HolderID != holder || !rec.TurnOpen {
		t.Fatalf("a failed user Stop forgot the holder: %+v", rec)
	}
	if !strings.HasPrefix(st.Error, wantAgyCrossHarnessRefusal+"; ") || !strings.HasSuffix(st.Error, "holder busy") {
		t.Fatalf("state error = %q", st.Error)
	}
	if !armed() || len(got.list()) != 0 {
		t.Fatalf("a failed user Stop disarmed the retry or published: armed %v, reports %+v", armed(), got.list())
	}
	c.do(c.heartbeat)
	if stops() != 3 {
		t.Fatalf("the heartbeat no longer retries: %v", h.holders.opList())
	}

	h.holders.set(func() { h.holders.stopErr = nil })
	if _, err := h.runner.Stop(id); err != nil {
		t.Fatal(err)
	}
	if stops() != 4 {
		t.Fatalf("the user's Stop did not stop the holder: %v", h.holders.opList())
	}
	if rec := h.record(id); rec.HolderID != "" || rec.TurnOpen || rec.MCPToken != "" || rec.Capability != "" {
		t.Fatalf("the stopped worker kept its holder or turn: %+v", rec)
	}
	eventually(t, "the failed report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("report = %+v", rep.Result)
	}
	if armed() {
		t.Fatal("the retry is still armed after the holder ended")
	}
	c.do(c.heartbeat)
	if _, err := h.runner.Stop(id); !errors.Is(err, errClosed) {
		t.Fatalf("Stop after retirement = %v", err)
	}
	if stops() != 4 || len(got.list()) != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("retired twice or relaunched: stops %d, reports %+v, ops %v", stops(), got.list(), h.holders.opList())
	}
	if st := h.state(id); st.Error != wantAgyCrossHarnessRefusal || st.Process || st.Status != StatusClosed {
		t.Fatalf("state = %+v", st)
	}
}

// A close while a failed retirement retries stops the holder first. A failed
// stop refuses the close: the conversation stays open in the runner with its
// holder and the heartbeat retry, so the live holder keeps both. A later
// close that ends the holder closes the conversation once.
func TestAgyWorkerCloseDuringAFailedRetirementKeepsTheConversation(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	h.opts.heartbeat = time.Hour // the test drives each retry
	h.holders.set(func() { h.holders.stopErr = errors.New("holder busy") })
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder scan", h.runner.rejoined)
	eventually(t, "the failed retirement stop", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	c := h.runner.conv(id)
	armed := func() bool {
		ok := false
		c.do(func() { ok = c.crossHarnessRetiring && c.replaying && c.proc != nil && !c.closed })
		return ok
	}
	stops := func() int { return h.holders.opCount("stop " + holder) }

	_, err := h.runner.CloseConversation(id)
	if !errors.Is(err, errHolderNotEnded) || !strings.HasSuffix(err.Error(), "close it again to retry") || !strings.Contains(err.Error(), "holder busy") {
		t.Fatalf("close error = %v", err)
	}
	if stops() != 2 {
		t.Fatalf("the close did not stop the holder: %v", h.holders.opList())
	}
	if h.runner.conv(id) == nil {
		t.Fatal("a refused close dropped the conversation from the runner")
	}
	if rec := h.record(id); rec.HolderID != holder || !rec.TurnOpen || rec.ClosedAt != "" {
		t.Fatalf("a refused close forgot the holder or closed: %+v", rec)
	}
	if !armed() || len(got.list()) != 0 {
		t.Fatalf("a refused close disarmed the retry or published: armed %v, reports %+v", armed(), got.list())
	}
	if st := h.state(id); st.Status == StatusClosed || !strings.HasSuffix(st.Error, "holder busy") {
		t.Fatalf("state = %+v", st)
	}
	c.do(c.heartbeat)
	if stops() != 3 {
		t.Fatalf("the heartbeat no longer retries: %v", h.holders.opList())
	}

	h.holders.set(func() { h.holders.stopErr = nil })
	st, err := h.runner.CloseConversation(id)
	if err != nil {
		t.Fatal(err)
	}
	if stops() != 4 || st.Status != StatusClosed || h.runner.conv(id) != nil {
		t.Fatalf("the close did not end the holder and close: stops %v, state %+v", h.holders.opList(), st)
	}
	if rec := h.record(id); rec.HolderID != "" || rec.TurnOpen || rec.ClosedAt == "" || rec.MCPToken != "" || rec.Capability != "" {
		t.Fatalf("the closed worker kept its holder or turn: %+v", rec)
	}
	eventually(t, "the failed report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("report = %+v", rep.Result)
	}
	if _, err := h.runner.CloseConversation(id); err == nil {
		t.Fatal("a closed conversation closed again")
	}
	if stops() != 4 || len(got.list()) != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("closed twice or relaunched: stops %d, reports %+v, ops %v", stops(), got.list(), h.holders.opList())
	}
}

// A daemon that died after the retirement cleared the holder but before the
// open turn settled leaves an open turn and no holder. The next load settles
// it failed with the agy reason, not the generic interrupted one.
func TestAgyWorkerRetirementCrashSettlesWithTheAgyReason(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, _ := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	rewriteRecord(t, h, id, func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq, rec.HandedOffAt = "", 0, 0, ""
		rec.Capability = ""
		rec.forgetMCP()
	})
	if !h.record(id).TurnOpen {
		t.Fatal("fixture: the turn is not open")
	}
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the settled report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("report = %+v", rep.Result)
	}
	if st := h.state(id); st.Error != wantAgyCrossHarnessRefusal || st.Process || st.Status != StatusClosed {
		t.Fatalf("state = %+v", st)
	}
	if rec := h.record(id); rec.TurnOpen || rec.LastTurn == nil || rec.LastTurn.Result.Status != TurnFailed {
		t.Fatalf("record = %+v", rec)
	}
}

func TestAgyWorkerRejoinWaitsForCompleteHolderAbsence(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	crossHarnessAgyNextDaemon(t, h, id)
	h.holders.vanish(holder)
	h.holders.set(func() { h.holders.incomplete = true })
	h.opts.heartbeat = time.Hour
	h.startAt("http://127.0.0.1:1")
	wait(t, "incomplete holder scan", h.runner.rejoined)
	if rec := h.record(id); rec.ClosedAt != "" || rec.HolderID != holder || !rec.TurnOpen {
		t.Fatalf("incomplete scan changed the holder record: %+v", rec)
	}
	if len(got.list()) != 0 {
		t.Fatalf("incomplete scan settled a turn: %+v", got.list())
	}

	h.holders.set(func() { h.holders.incomplete = false })
	c := h.runner.conv(id)
	if c == nil || !c.do(c.heartbeat) {
		t.Fatal("worker did not survive the incomplete scan")
	}
	if st := h.state(id); st.Status != StatusClosed || st.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("complete holder scan left state %+v", st)
	}
	if rec := h.record(id); rec.HolderID != "" || rec.ClosedAt == "" || rec.TurnOpen {
		t.Fatalf("complete holder scan left record %+v", rec)
	}
	if reports := got.list(); len(reports) != 1 || reports[0].Result.Status != TurnFailed || reports[0].Result.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("reports = %+v", reports)
	}
	if h.holders.opCount("stop") != 0 {
		t.Fatalf("an absent holder was stopped: %v", h.holders.opList())
	}
}

// crossHarnessAgyNextDaemon ends the daemon, its held processes running on,
// and makes agy worker id a cross-harness one for the next daemon: its
// record now names a Claude spawner, as a Claude lead's spawn under an older
// daemon would. That older daemon also detached a finished-idle worker at
// shutdown rather than putting it to sleep. The registration keeps the
// shipped cross-harness refusal.
func crossHarnessAgyNextDaemon(t *testing.T, h *leadHarness, id string) {
	t.Helper()
	detachFinishedAtShutdown(h)
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
	// The idle parent's holder survives the shutdown, as a crash leaves it;
	// rejoined, it waits for its child's running turn instead of sleeping.
	detachFinishedAtShutdown(h)
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

// A cross-harness rejoin runs the ownership match every held worker passes. It
// never stops a holder that fails the match: one whose descriptor names
// another directory, one that is a hosted lead's holder, or one already
// rejected. The record still drops the association and shows the agy
// reason.
func TestAgyWorkerRejoinNeverStopsAHolderThatFailsOwnership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*holderSession)
		reject bool
	}{
		{name: "foreign directory", edit: func(m *holderSession) { m.Cwd = "/foreign" }},
		{name: "lead holder", edit: func(m *holderSession) { m.Kind = holderKindLead }},
		{name: "rejected holder", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := workerRecoveryHarness(t, BackendAgy)
			id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
			crossHarnessAgyNextDaemon(t, h, id)
			if tc.edit != nil {
				h.holders.set(func() {
					var meta holderSession
					if err := json.Unmarshal(h.holders.holders[holder].session, &meta); err != nil {
						t.Fatal(err)
					}
					tc.edit(&meta)
					raw, _ := json.Marshal(meta)
					h.holders.holders[holder].session = raw
				})
			}
			entered, release := h.gateScan()
			defer release()
			h.startAt("http://127.0.0.1:1")
			wait(t, "blocked scan", entered)
			if tc.reject {
				h.runner.holders.reject(holder)
			}
			release()
			wait(t, "holder scan", h.runner.rejoined)
			eventually(t, "the association dropped", func() bool { rec := h.record(id); return rec.HolderID == "" && rec.ClosedAt != "" })
			eventually(t, "the agy reason", func() bool { return h.state(id).Error == wantAgyCrossHarnessRefusal })
			if h.holders.opCount("stop") != 0 {
				t.Fatalf("a holder that failed ownership was stopped: %v", h.holders.opList())
			}
			if st := h.state(id); st.Process || st.Status != StatusClosed {
				t.Fatalf("state = %+v", st)
			}
			caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
			if child := findAgent(t, h.runner.AgentList(caller), id); child.State != AgentClosed || child.Error != wantAgyCrossHarnessRefusal {
				t.Fatalf("listed child = %+v", child)
			}
			if h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
				t.Fatalf("a cross-harness rejoin launched a process: %v", h.holders.opList())
			}
		})
	}
}

// Retirement stamps the record's activity, so a record whose last activity
// is older than the whole child expiry interval keeps its history and its
// retirement time, and no longer enters the open-child expiry sweep.
func TestAgyWorkerRetirementRestartsTheExpiryClock(t *testing.T) {
	h, _ := workerRecoveryHarness(t, BackendAgy)
	id, holder := idleHeldAgyWorker(t, h)
	crossHarnessAgyNextDaemon(t, h, id)
	old := time.Now().Add(-(time.Duration(DefaultChildWaitingDays)*24*time.Hour + time.Hour)).UTC()
	rewriteRecord(t, h, id, func(rec *Record) { rec.LastActiveAt = old.Format(time.RFC3339) })
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the holder ended", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	eventually(t, "the record to forget the holder", func() bool {
		rec := h.record(id)
		return rec.HolderID == "" && rec.ClosedAt != "" && h.state(id).Status == StatusClosed
	})
	if at := lastActive(h.record(id)); time.Since(at) > time.Hour {
		t.Fatalf("last activity %v was not stamped at retirement", at)
	}
	// The closed state is published before the conversation leaves r.convs,
	// so a loaded runner can observe the window between the two.
	eventually(t, "the historical record to leave the open-child expiry sweep", func() bool { return h.runner.conv(id) == nil })
	if st := h.state(id); st.Status != StatusClosed || st.Error != wantAgyCrossHarnessRefusal {
		t.Fatalf("state = %+v", st)
	}
}

// The refusal is derived from the record's backend and spawner stamp on every
// load, with no stored reason, so it survives the restart after the
// retirement. This holds
// for a parentless worker retired while a user turn runs (a profile-created
// worker can run one; its record persists the start time but no TurnOpen and
// no last turn) and for an idle parented one (a done last turn).
func TestAgyWorkerRefusalReasonSurvivesARestart(t *testing.T) {
	for _, parentless := range []bool{true, false} {
		t.Run(fmt.Sprintf("parentless=%v", parentless), func(t *testing.T) {
			h, _ := workerRecoveryHarness(t, BackendAgy)
			var id, holder string
			if parentless {
				// The holder's process is mid-turn when the daemon exits.
				id, holder = spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
			} else {
				id, holder = idleHeldAgyWorker(t, h)
			}
			crossHarnessAgyNextDaemon(t, h, id)
			if parentless {
				rewriteRecord(t, h, id, func(rec *Record) { rec.Parent, rec.TurnOpen, rec.LastTurn = nil, false, nil })
				if h.record(id).TurnStartedAt == 0 {
					t.Fatal("fixture: no running user turn")
				}
			}
			h.startAt("http://127.0.0.1:1")
			eventually(t, "the holder ended", func() bool { return h.holders.opCount("stop "+holder) == 1 })
			eventually(t, "the retirement", func() bool { rec := h.record(id); return rec.HolderID == "" && rec.ClosedAt != "" })
			if st := h.state(id); st.Error != wantAgyCrossHarnessRefusal {
				t.Fatalf("after retirement: state error = %q", st.Error)
			}
			h.runner.Close()
			h.holders.restart()
			h.startAt("http://127.0.0.1:1")
			if st := h.state(id); st.Error != wantAgyCrossHarnessRefusal || st.Status != StatusClosed {
				t.Fatalf("after the next restart: state = %+v", st)
			}
		})
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
			if st := h.state(id); st.Process || st.Status != StatusClosed || st.Error != wantAgyCrossHarnessRefusal || h.backend.startCount() != starts {
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
