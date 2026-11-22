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
	leadOnlyAgyNextDaemon(t, h)
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

// A refused holder stop keeps the holder in the record, says why, publishes
// nothing, and starts no process. The same daemon's heartbeat retries the
// stop until it succeeds; the turn then settles failed with the agy reason,
// reported once.
func TestAgyWorkerRejoinRetriesAFailedStopFromTheHeartbeat(t *testing.T) {
	h, got := workerRecoveryHarness(t, BackendAgy)
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	leadOnlyAgyNextDaemon(t, h)
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
	if st := h.state(id); !strings.HasPrefix(st.Error, agyLeadOnlyReason+"; ") || !strings.HasSuffix(st.Error, "holder busy") {
		t.Fatalf("state = %+v", st)
	}
	if len(got.list()) != 0 || h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("a failed stop published or launched: reports %+v, ops %v", got.list(), h.holders.opList())
	}

	h.holders.set(func() { h.holders.stopErr = nil })
	eventually(t, "the failed report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != agyLeadOnlyReason {
		t.Fatalf("report = %+v", rep.Result)
	}
	eventually(t, "the record to forget the holder", func() bool { rec := h.record(id); return rec.HolderID == "" && !rec.TurnOpen })
	stops := h.holders.opCount("stop " + holder)
	time.Sleep(200 * time.Millisecond) // several heartbeats
	if st := h.state(id); st.Error != agyLeadOnlyReason || st.Process || st.Status == StatusClosed {
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
	leadOnlyAgyNextDaemon(t, h)
	h.opts.heartbeat = time.Hour // the test drives each retry
	h.holders.set(func() { h.holders.stopErr = errors.New("holder busy") })
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder scan", h.runner.rejoined)
	eventually(t, "the failed retirement stop", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	c := h.runner.conv(id)
	armed := func() bool {
		ok := false
		c.do(func() { ok = c.leadOnlyRetiring && c.replaying && c.proc != nil })
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
	if !strings.HasPrefix(st.Error, agyLeadOnlyReason+"; ") || !strings.HasSuffix(st.Error, "holder busy") {
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
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != agyLeadOnlyReason {
		t.Fatalf("report = %+v", rep.Result)
	}
	if armed() {
		t.Fatal("the retry is still armed after the holder ended")
	}
	c.do(c.heartbeat)
	if _, err := h.runner.Stop(id); err != nil {
		t.Fatal(err)
	}
	if stops() != 4 || len(got.list()) != 1 || h.holders.opCount("launch") != 1 {
		t.Fatalf("retired twice or relaunched: stops %d, reports %+v, ops %v", stops(), got.list(), h.holders.opList())
	}
	if st := h.state(id); st.Error != agyLeadOnlyReason || st.Process || st.Status == StatusClosed {
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
	leadOnlyAgyNextDaemon(t, h)
	h.opts.heartbeat = time.Hour // the test drives each retry
	h.holders.set(func() { h.holders.stopErr = errors.New("holder busy") })
	h.startAt("http://127.0.0.1:1")
	wait(t, "holder scan", h.runner.rejoined)
	eventually(t, "the failed retirement stop", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	c := h.runner.conv(id)
	armed := func() bool {
		ok := false
		c.do(func() { ok = c.leadOnlyRetiring && c.replaying && c.proc != nil && !c.closed })
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
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != agyLeadOnlyReason {
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
	leadOnlyAgyNextDaemon(t, h)
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
	if rep := got.list()[0]; rep.Result.ID != id || rep.Result.Status != TurnFailed || rep.Result.Error != agyLeadOnlyReason {
		t.Fatalf("report = %+v", rep.Result)
	}
	if st := h.state(id); st.Error != agyLeadOnlyReason || st.Process || st.Status == StatusClosed {
		t.Fatalf("state = %+v", st)
	}
	if rec := h.record(id); rec.TurnOpen || rec.LastTurn == nil || rec.LastTurn.Result.Status != TurnFailed {
		t.Fatalf("record = %+v", rec)
	}
}

// leadOnlyAgyNextDaemon ends the daemon, its held processes running on, and
// gives the next one the shipped lead-only agy posture.
func leadOnlyAgyNextDaemon(t *testing.T, h *leadHarness) {
	t.Helper()
	h.runner.Close()
	if h.holders.opCount("stop") != 0 {
		t.Fatalf("runner shutdown stopped a worker: %v", h.holders.opList())
	}
	reg := h.opts.Backends[BackendAgy]
	reg.WorkerCapable, reg.leadOnlyReason = false, agyLeadOnlyReason
	h.opts.Backends[BackendAgy] = reg
	h.holders.restart()
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

// A lead-only rejoin runs the ownership match every held worker passes. It
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
			leadOnlyAgyNextDaemon(t, h)
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
			eventually(t, "the association dropped", func() bool { return h.record(id).HolderID == "" })
			eventually(t, "the agy reason", func() bool { return h.state(id).Error == agyLeadOnlyReason })
			if h.holders.opCount("stop") != 0 {
				t.Fatalf("a holder that failed ownership was stopped: %v", h.holders.opList())
			}
			if st := h.state(id); st.Process || st.Status == StatusClosed {
				t.Fatalf("state = %+v", st)
			}
			caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
			if child := findAgent(t, h.runner.AgentList(caller), id); child.State == AgentClosed || child.Error != agyLeadOnlyReason {
				t.Fatalf("listed child = %+v", child)
			}
			if h.holders.opCount("spawn") != 1 || h.holders.opCount("launch") != 1 {
				t.Fatalf("a lead-only rejoin launched a process: %v", h.holders.opList())
			}
		})
	}
}

// Retirement stamps the record's activity, so a record whose last activity
// is older than the whole child expiry interval is not deleted at the next
// sweep: it stays one interval with the agy reason visible.
func TestAgyWorkerRetirementRestartsTheExpiryClock(t *testing.T) {
	h, _ := workerRecoveryHarness(t, BackendAgy)
	id, holder := idleHeldAgyWorker(t, h)
	leadOnlyAgyNextDaemon(t, h)
	old := time.Now().Add(-(time.Duration(DefaultChildWaitingDays)*24*time.Hour + time.Hour)).UTC()
	rewriteRecord(t, h, id, func(rec *Record) { rec.LastActiveAt = old.Format(time.RFC3339) })
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the holder ended", func() bool { return h.holders.opCount("stop "+holder) == 1 })
	eventually(t, "the record to forget the holder", func() bool { return h.record(id).HolderID == "" })
	c := h.runner.conv(id)
	c.do(func() {}) // the retirement's loop step has finished
	if at := lastActive(h.record(id)); time.Since(at) > time.Hour {
		t.Fatalf("last activity %v was not stamped at retirement", at)
	}
	expiring := true
	c.do(func() {
		c.expireIfDue()
		expiring = c.expiring
	})
	if expiring {
		t.Fatal("the retired record is due for deletion at once")
	}
	if st := h.state(id); st.Status == StatusClosed || st.Error != agyLeadOnlyReason {
		t.Fatalf("state = %+v", st)
	}
}

// The agy reason is derived from the record's backend on every load, with no
// stored field, so it survives the restart after the retirement. This holds
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
			leadOnlyAgyNextDaemon(t, h)
			if parentless {
				rewriteRecord(t, h, id, func(rec *Record) { rec.Parent, rec.TurnOpen, rec.LastTurn = nil, false, nil })
				if h.record(id).TurnStartedAt == 0 {
					t.Fatal("fixture: no running user turn")
				}
			}
			h.startAt("http://127.0.0.1:1")
			eventually(t, "the holder ended", func() bool { return h.holders.opCount("stop "+holder) == 1 })
			eventually(t, "the retirement", func() bool { return h.record(id).HolderID == "" })
			if st := h.state(id); st.Error != agyLeadOnlyReason {
				t.Fatalf("after retirement: state error = %q", st.Error)
			}
			h.runner.Close()
			h.holders.restart()
			h.startAt("http://127.0.0.1:1")
			if st := h.state(id); st.Error != agyLeadOnlyReason || st.Status == StatusClosed {
				t.Fatalf("after the next restart: state = %+v", st)
			}
		})
	}
}

// A legacy custom agy worker is refused on its record's backend and lead
// flag, before its profile is read: a removed profile, or one that now names
// another backend or a lead, cannot mask the agy reason or fail the turn.
func TestAgyWorkerRecordRefusalIgnoresItsProfile(t *testing.T) {
	for _, tc := range []struct{ name, profile string }{
		{name: "profile removed"},
		{name: "profile on another backend", profile: `{"backend":"claude","model":"haiku","instructions":"agent.md"}`},
		{name: "profile now a lead", profile: `{"backend":"agy","lead":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"agy-custom": `{"backend":"agy","model":"gemini","instructions":"agent.md"}`})
			h.runner.Close()
			h.config.Backends[BackendAgy] = BackendRegistration{Backend: h.backend, WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendAgy)}
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
			if _, err := h.runner.Send(id, "hello"); err != nil {
				t.Fatal(err)
			}
			c := h.runner.conv(id)
			eventually(t, "the refused wake", func() bool {
				refused := false
				c.do(func() { refused = c.wakeRefused })
				return refused
			})
			if st := h.state(id); st.Error != agyLeadOnlyReason || st.Process || st.Status == StatusClosed || h.backend.startCount() != starts {
				t.Fatalf("state = %+v, starts %d (was %d)", st, h.backend.startCount(), starts)
			}
		})
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
