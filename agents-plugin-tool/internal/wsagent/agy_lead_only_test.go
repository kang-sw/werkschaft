package wsagent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// shippedAgyRegistration is the daemon's own agy registration over backend:
// the lead-only posture with the worker profile still registered.
func shippedAgyRegistration(backend Backend) BackendRegistration {
	reg := BuiltinBackendRegistrations()[BackendAgy]
	reg.Backend = backend
	return reg
}

func TestShippedAgyRegistrationIsLeadOnly(t *testing.T) {
	reg := BuiltinBackendRegistrations()[BackendAgy]
	if reg.WorkerCapable || reg.leadOnlyReason != agyLeadOnlyReason || reg.LeadProfile != LeadProfileAgy {
		t.Fatalf("agy registration: worker %v, reason %q, lead %q", reg.WorkerCapable, reg.leadOnlyReason, reg.LeadProfile)
	}
	if !strings.Contains(agyLeadOnlyReason, "hosted lead only") || !strings.Contains(agyLeadOnlyReason, "Antigravity") {
		t.Fatalf("reason does not say why: %q", agyLeadOnlyReason)
	}
	for _, name := range []string{BackendClaude, BackendCodex, BackendPi} {
		if other := BuiltinBackendRegistrations()[name]; !other.WorkerCapable || other.leadOnlyReason != "" {
			t.Errorf("%s lost its worker role: %+v", name, other.WorkerCapable)
		}
	}
}

// Every spawn route that ends on agy is refused with the agy reason, and
// agent.tiers names it under Unavailable and on each alias, route, and
// bundle that ends there; an agy lead still spawns a Claude worker.
func TestAgyLeadOnlyRefusesEverySpawnRouteAndListsWhy(t *testing.T) {
	h, _ := agentHarness(t, func(o *Options) {
		o.TierRoles = func() ([]wsconfig.TierRole, error) { return testPackagedRoles, nil }
		o.Backends[BackendAgy] = shippedAgyRegistration(o.Backends[BackendClaude].Backend)
	})
	// agy has slots, so a refusal is the lead-only reason, not a missing model.
	for _, size := range []string{"small", "large"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, size, "", "gemini-"+size, BackendAgy, "high"); err != nil {
			t.Fatal(err)
		}
	}
	setTierAliases(t, h.config.Config, "far=agy.large")
	setTierRouting(t, h.config.Config, `{"nodes":{"review":{"*":"agy.*"}},"bundles":{"agy-deep":{"harness":"agy","model":"gemini-deep"}}}`)
	lead := h.leadCaller()
	agyLead := LeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	for _, tc := range []struct {
		name   string
		caller AgentCaller
		tier   string
	}{
		{"explicit", lead, "agy.small"},
		{"bare tier of an agy caller", agyLead, "small"},
		{"alias", lead, "far"},
		{"graph route", lead, "review:large"},
		{"bundle", lead, "agy-deep"},
	} {
		_, err := h.runner.AgentSpawn(tc.caller, AgentSpawnRequest{Prompt: "x", Tier: tc.tier, Authority: "read-only"})
		if err == nil {
			t.Fatalf("%s: %q spawned", tc.name, tc.tier)
		}
		agentRefused(t, err, http.StatusBadRequest, agyLeadOnlyReason)
	}
	if n := len(h.runner.AgentList(lead)); n != 0 {
		t.Fatalf("a refused agy route spawned %d children", n)
	}
	if h.backend.startCount() != 0 {
		t.Fatal("a refused agy route started a process")
	}

	tiers, err := h.runner.AgentTiers(lead)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tiers.Unavailable, "agy: "+agyLeadOnlyReason) {
		t.Fatalf("unavailable = %v", tiers.Unavailable)
	}
	for _, e := range tiers.Ext {
		if strings.HasPrefix(e.Tier, "agy.") {
			t.Fatalf("ext lists an agy tier: %+v", tiers.Ext)
		}
	}
	if len(tiers.Aliases) != 1 || tiers.Aliases[0].Alias != "far" || tiers.Aliases[0].Error != agyLeadOnlyReason || tiers.Aliases[0].Model != "" {
		t.Fatalf("aliases = %+v", tiers.Aliases)
	}
	if tiers.Routing == nil {
		t.Fatal("no routing section")
	}
	route := slices.IndexFunc(tiers.Routing.Routes, func(e AgentRouteEntry) bool { return e.Tier == "review:large" })
	if route < 0 || tiers.Routing.Routes[route].Error != agyLeadOnlyReason || tiers.Routing.Routes[route].Harness != BackendAgy {
		t.Fatalf("routes = %+v", tiers.Routing.Routes)
	}
	bundle := slices.IndexFunc(tiers.Routing.Bundles, func(e AgentRouteEntry) bool { return e.Tier == "agy-deep" })
	if bundle < 0 || tiers.Routing.Bundles[bundle].Error != agyLeadOnlyReason {
		t.Fatalf("bundles = %+v", tiers.Routing.Bundles)
	}
	if why, known := h.runner.HarnessAvailability(BackendAgy); why != agyLeadOnlyReason || !known {
		t.Fatalf("availability = %q (known %v)", why, known)
	}

	own, err := h.runner.AgentTiers(agyLead)
	if err != nil || own.Harness != BackendAgy || len(own.Native) != 0 {
		t.Fatalf("an agy caller's tiers = %+v, %v", own, err)
	}
	if got := h.spawnBinding(agyLead, "claude.small"); got.Backend != BackendClaude || got.Model != "claude-small" {
		t.Fatalf("an agy lead's claude worker = %+v", got)
	}
}

// The profile-based creation route refuses the agy worker profile and any
// other non-lead agy profile with the agy reason, before anything is made.
func TestAgyLeadOnlyRefusesProfileCreation(t *testing.T) {
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
		if r.status != http.StatusForbidden || !strings.Contains(r.body, agyLeadOnlyReason) {
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

// An agy worker record made before agy became lead-only stays listable, but
// a message, a continue from its parent, and a resume after a daemon
// restart are each refused with the agy reason instead of waking it.
func TestAgyWorkerRecordRefusesMessageContinueAndResume(t *testing.T) {
	refusals := &wakeRefusals{}
	h, got := agentHarness(t, func(o *Options) {
		o.Backends[BackendAgy] = BackendRegistration{Backend: o.Backends[BackendClaude].Backend, WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendAgy)}
		o.WakeRefused = refusals.add
	})
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, "small", "", "gemini-small", BackendAgy, "medium"); err != nil {
		t.Fatal(err)
	}
	lead := h.leadCaller()
	id, p := h.agentRunning(lead, AgentSpawnRequest{Prompt: "work", Tier: "agy.small", Authority: "read-only"})
	p.reply("done")
	eventually(t, "the agy worker's report", func() bool { return len(got.list()) == 1 })
	h.asleep(id)

	// The daemon now carries the lead-only agy registration.
	h.runner.Close()
	reg := h.config.Backends[BackendAgy]
	reg.WorkerCapable, reg.leadOnlyReason = false, agyLeadOnlyReason
	h.config.Backends[BackendAgy] = reg
	h.start()
	starts := h.backend.startCount()
	refused := func(what string) {
		t.Helper()
		eventually(t, what+" refused", func() bool { return h.state(id).Error == agyLeadOnlyReason })
		st := h.state(id)
		if st.Process || st.Status == StatusClosed || h.backend.startCount() != starts {
			t.Fatalf("%s woke the agy worker: %+v, starts %d (was %d)", what, st, h.backend.startCount(), starts)
		}
		if child := findAgent(t, h.runner.AgentList(lead), id); child.Tier != "agy.small" || child.State == AgentClosed {
			t.Fatalf("%s: the record is not listed as kept: %+v", what, child)
		}
	}

	if _, err := h.runner.Send(id, "message"); err != nil {
		t.Fatal(err)
	}
	refused("a message")

	if _, err := h.runner.Deliver(id, Delivery{From: leadParent.ReplyID, Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	refused("a continue")
	if want := [3]string{leadParent.ReplyID, id, agyLeadOnlyReason}; !slices.Contains(refusals.list(), want) {
		t.Fatalf("wake refusals = %q, want %q", refusals.list(), want)
	}

	h.runner.Close()
	h.start()
	if _, err := h.runner.Send(id, "resume"); err != nil {
		t.Fatal(err)
	}
	refused("a resume")
	if rec := h.record(id); rec.Backend != BackendAgy || rec.Profile != AgentWorkerProfile(BackendAgy) {
		t.Fatalf("record changed: %+v", rec)
	}
}

// A held agy worker whose holder kept running is not reattached once agy is
// lead-only: the first scan ends its holder through the ordinary stop, its
// open turn settles failed with the agy reason, and the record stays listed,
// stopped, with that reason.
func TestAgyWorkerRejoinEndsTheHolderAndKeepsTheRecord(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	h.runner.Close()
	if h.holders.opCount("stop") != 0 {
		t.Fatalf("runner shutdown stopped the worker: %v", h.holders.opList())
	}
	reg := h.opts.Backends[BackendAgy]
	reg.WorkerCapable, reg.leadOnlyReason = false, agyLeadOnlyReason
	h.opts.Backends[BackendAgy] = reg
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")

	eventually(t, "the holder ended", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	eventually(t, "the failed report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != agyLeadOnlyReason {
		t.Fatalf("report = %+v", rep.Result)
	}
	eventually(t, "the record to forget the holder", func() bool { rec := h.record(id); return rec.HolderID == "" && !rec.TurnOpen })
	st := h.state(id)
	if st.Process || st.Status == StatusClosed || st.Error != agyLeadOnlyReason {
		t.Fatalf("state = %+v", st)
	}
	caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
	if child := findAgent(t, h.runner.AgentList(caller), id); child.State == AgentClosed || child.Error != agyLeadOnlyReason {
		t.Fatalf("listed child = %+v", child)
	}
	if h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("a lead-only rejoin launched a process: %v", h.holders.opList())
	}
	if rec := h.record(id); rec.MCPToken != "" || rec.Capability != "" {
		t.Fatalf("the ended worker kept its credentials: %+v", rec)
	}
	if len(got.list()) != 1 {
		t.Fatalf("more than one report: %+v", got.list())
	}
}

// Peer mail still reaches a hosted agy lead under the lead-only posture.
func TestAgyLeadOnlyLeadTakesPeerMail(t *testing.T) {
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
