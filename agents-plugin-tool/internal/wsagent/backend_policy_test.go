package wsagent

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// policyBackend is a backend that declares its differences through the
// registration seam only: it registers under a name no common code knows.
type policyBackend struct {
	fakeBackend
	resume string
}

func (b *policyBackend) terminalResume(rec Record, _ string) string {
	if rec.SessionID == "" {
		return ""
	}
	return b.resume + " " + rec.SessionID
}
func (*policyBackend) checkTerminalSession(string) error { return nil }
func (*policyBackend) adoptTerminalSession(rec *Record, session string) {
	rec.SessionID = session
}
func (*policyBackend) admitNativeMode(_ Record, mode string, user bool) error {
	if mode == "yolo" && !user {
		return &requestError{status: http.StatusForbidden, msg: "only the user may choose yolo"}
	}
	return nil
}

func policyRunner(backends map[string]BackendRegistration) *Runner {
	return &Runner{opts: Options{Backends: backends}}
}

// TestBuiltinBackendPolicyTable pins what each built-in backend declares, so a
// parity change shows as a table edit. A backend that declares nothing keeps
// the zero policy.
func TestBuiltinBackendPolicyTable(t *testing.T) {
	regs := BuiltinBackendRegistrations()
	for _, tc := range []struct {
		backend   string
		lifecycle LifecyclePolicy
		terminal  bool
		admitter  bool
		live      bool
		modes     map[PermissionLevel]string
		legacy    map[string]PermissionLevel
		fallback  PermissionLevel
	}{
		{backend: BackendClaude, terminal: true,
			lifecycle: LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, LeadStdioFallback: true, RestoreUsage: true,
				NoWorkerCompactionSteer: true, WorkerSessionState: true, ExecOptIn: ExecOptIn{Key: "claude.exec_cutover_enabled", Label: "Claude"}},
			modes: map[PermissionLevel]string{PermissionReadOnly: "plan", PermissionWorkdirWrite: "acceptEdits", PermissionFull: "bypassPermissions"},
			legacy: map[string]PermissionLevel{"plan": PermissionReadOnly, "bypassPermissions": PermissionFull, "": PermissionWorkdirWrite,
				"auto": PermissionWorkdirWrite, "default": PermissionWorkdirWrite, "acceptEdits": PermissionWorkdirWrite, "dontAsk": PermissionWorkdirWrite}},
		{backend: BackendCodex, live: true,
			lifecycle: LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, ExecReadTools: true},
			modes:     map[PermissionLevel]string{PermissionReadOnly: "read-only", PermissionWorkdirWrite: "workspace-write", PermissionFull: "danger-full-access"},
			legacy:    map[string]PermissionLevel{"read-only": PermissionReadOnly, "workspace-write": PermissionWorkdirWrite, "danger-full-access": PermissionFull}},
		{backend: BackendAgy, terminal: true, admitter: true,
			lifecycle: LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, TurnActivity: true, InternalTurns: true, WorkerLog: true, SingleHardCompactionSteer: true, EphemeralIsolatedStateDir: true}},
		{backend: BackendPiRPC,
			lifecycle: LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, ExecReadTools: true, RestartLeadOnPermissionChange: true},
			fallback:  PermissionWorkdirWrite},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			reg, ok := regs[tc.backend]
			if !ok {
				t.Fatalf("%s is not a built-in registration", tc.backend)
			}
			if reg.Lifecycle != tc.lifecycle {
				t.Errorf("lifecycle = %+v, want %+v", reg.Lifecycle, tc.lifecycle)
			}
			p := reg.Permission
			if !reflect.DeepEqual(p.NativeModes, tc.modes) || !reflect.DeepEqual(p.LegacyModes, tc.legacy) || p.LegacyDefault != tc.fallback || p.LiveModeProjection != tc.live {
				t.Errorf("permission = %+v, want modes %v legacy %v default %q live %v", p, tc.modes, tc.legacy, tc.fallback, tc.live)
			}
			if _, got := reg.Backend.(terminalSession); got != tc.terminal {
				t.Errorf("terminalSession = %v, want %v", got, tc.terminal)
			}
			if _, got := reg.Backend.(nativeModeAdmitter); got != tc.admitter {
				t.Errorf("nativeModeAdmitter = %v, want %v", got, tc.admitter)
			}
		})
	}
}

// TestBuiltinRegistrationPolicyIsNotShared: the floor's policy maps are
// copied on every read, so a caller cannot edit a built-in's behavior.
func TestBuiltinRegistrationPolicyIsNotShared(t *testing.T) {
	first := BuiltinBackendRegistrations()
	first[BackendClaude].Permission.NativeModes[PermissionFull] = "mutated"
	first[BackendClaude].Permission.LegacyModes["plan"] = PermissionFull
	again := BuiltinBackendRegistrations()[BackendClaude].Permission
	if again.nativeMode(PermissionFull) != "bypassPermissions" || again.legacyLevel("plan") != PermissionReadOnly {
		t.Fatalf("a mutated copy reached the floor: %+v", again)
	}
	runner := policyRunner(first)
	copied := runner.BackendRegistrations()
	copied[BackendClaude].Permission.NativeModes[PermissionReadOnly] = "mutated"
	if runner.permissionPolicy(BackendClaude).nativeMode(PermissionReadOnly) == "mutated" {
		t.Fatal("Runner.BackendRegistrations shares the policy maps")
	}
}

// TestRegisteredPolicyDrivesCommonCode: a backend registered under any name
// gets the behavior it declares, and a built-in name carries none that its
// registration does not declare.
func TestRegisteredPolicyDrivesCommonCode(t *testing.T) {
	fixture := &policyBackend{resume: "fixture --resume"}
	runner := policyRunner(map[string]BackendRegistration{
		"fixture": {
			Backend: fixture, LeadProfile: "fixture-lead",
			Permission: PermissionPolicy{
				NativeModes:        map[PermissionLevel]string{PermissionReadOnly: "locked", PermissionFull: "yolo"},
				LegacyModes:        map[string]PermissionLevel{"locked": PermissionReadOnly, "yolo": PermissionFull},
				LegacyDefault:      PermissionWorkdirWrite,
				LiveModeProjection: true,
			},
			Lifecycle: LifecyclePolicy{TerminalSummaries: true, LeadHTTPMCP: true},
		},
		// A built-in name with no declaration is a plain backend.
		BackendClaude: {Backend: &fakeBackend{}, LeadProfile: LeadProfileClaude},
	})

	rec := Record{Lead: true, Backend: "fixture", PermissionMode: "locked"}
	if got := runner.legacyLeadPermission(rec); got != PermissionReadOnly {
		t.Errorf("legacy level = %q, want read-only", got)
	}
	if got := runner.legacyLeadPermission(Record{Lead: true, Backend: "fixture", PermissionMode: "other"}); got != PermissionWorkdirWrite {
		t.Errorf("unlisted mode = %q, want the declared default", got)
	}
	p := runner.permissionPolicy("fixture")
	if got := p.nativeMode(PermissionFull); got != "yolo" {
		t.Errorf("native mode = %q", got)
	}
	if got := p.quality(PermissionReadOnly, "locked"); got != "partial" {
		t.Errorf("quality under its read-only mode = %q, want partial", got)
	}
	if got := p.quality(PermissionReadOnly, ""); got != "advisory" {
		t.Errorf("quality before the harness reports = %q, want advisory", got)
	}
	if got := runner.projectLiveMode("", rec, "legacy-derived", "yolo", true); got != PermissionFull {
		t.Errorf("live projection = %q, want full", got)
	}
	for name, args := range map[string]struct {
		level          PermissionLevel
		source, active string
		process, lead  bool
	}{
		"explicit selection": {"", "explicit", "yolo", true, true},
		"process not live":   {"", "legacy-derived", "yolo", false, true},
		"not a lead":         {"", "legacy-derived", "yolo", true, false},
		"already derived":    {PermissionReadOnly, "legacy-derived", "yolo", true, true},
	} {
		r := rec
		r.Lead = args.lead
		want := args.level
		if got := runner.projectLiveMode(args.level, r, args.source, args.active, args.process); got != want {
			t.Errorf("%s: projection = %q, want %q", name, got, want)
		}
	}
	if !runner.lifecycle("fixture").TerminalSummaries {
		t.Error("the declared lifecycle was not read")
	}
	if got := runner.resumeCommand(Record{Lead: true, Backend: "fixture", SessionID: "s1"}, ""); got != "fixture --resume s1" {
		t.Errorf("resume = %q", got)
	}
	if got := runner.resumeCommand(Record{Backend: "fixture", SessionID: "s1"}, ""); got != "" {
		t.Errorf("a worker resumed in a terminal: %q", got)
	}

	// Registered under a built-in name, a backend that declares nothing gets
	// none of that harness's behavior: common code reads data, not names.
	plain := Record{Lead: true, Backend: BackendClaude, PermissionMode: "plan"}
	if got := runner.legacyLeadPermission(plain); got != "" {
		t.Errorf("an undeclared claude registration derived %q", got)
	}
	if got := runner.permissionPolicy(BackendClaude).nativeMode(PermissionReadOnly); got != "" {
		t.Errorf("an undeclared claude registration mapped %q", got)
	}
	if runner.lifecycle(BackendClaude) != (LifecyclePolicy{}) || runner.lifecycle("absent") != (LifecyclePolicy{}) {
		t.Error("an undeclared registration has lifecycle behavior")
	}
	if got := runner.resumeCommand(Record{Lead: true, Backend: BackendClaude, SessionID: "s1"}, ""); got != "" {
		t.Errorf("a backend with no terminal session resumed: %q", got)
	}
	if runner.terminalSessionOf("absent") != nil {
		t.Error("an unregistered backend has a terminal session")
	}
}

// TestNativeModeAdmission: a backend reserves a native mode to the user
// through nativeModeAdmitter; agy keeps its bypass rule and ignores the rest.
func TestNativeModeAdmission(t *testing.T) {
	fixture := &policyBackend{}
	for _, tc := range []struct {
		name    string
		backend nativeModeAdmitter
		rec     Record
		mode    string
		user    bool
		refused bool
	}{
		{"fixture agent", fixture, Record{Lead: true}, "yolo", false, true},
		{"fixture user", fixture, Record{Lead: true}, "yolo", true, false},
		{"fixture other mode", fixture, Record{Lead: true}, "locked", false, false},
		{"agy agent enabling bypass", AgyBackend{}, Record{Lead: true, Backend: BackendAgy}, "bypass", false, true},
		{"agy user enabling bypass", AgyBackend{}, Record{Lead: true, Backend: BackendAgy}, "bypass", true, false},
		{"agy agent keeping bypass", AgyBackend{}, Record{Lead: true, Backend: BackendAgy, AgyBypass: true, PermissionMode: "default"}, "bypass", false, false},
		{"agy agent other mode", AgyBackend{}, Record{Lead: true, Backend: BackendAgy}, "plan", false, false},
	} {
		err := tc.backend.admitNativeMode(tc.rec, tc.mode, tc.user)
		if got := err != nil; got != tc.refused {
			t.Errorf("%s: err = %v, refused %v", tc.name, err, tc.refused)
			continue
		}
		if err != nil {
			if re, ok := err.(*requestError); !ok || re.status != http.StatusForbidden {
				t.Errorf("%s: refusal = %v, want a 403", tc.name, err)
			}
		}
	}
}

// TestTerminalSessionAdoption: what a takeover carries over is the backend's
// own rule, table-driven across the built-in terminal backends.
func TestTerminalSessionAdoption(t *testing.T) {
	for _, tc := range []struct {
		backend string
		want    Record
	}{
		{BackendClaude, Record{PermissionMode: "acceptEdits", SessionID: "s1"}},
		{BackendAgy, Record{SessionID: "s1"}},
	} {
		ts, ok := BuiltinBackendRegistrations()[tc.backend].Backend.(terminalSession)
		if !ok {
			t.Fatalf("%s has no terminal session", tc.backend)
		}
		rec := Record{PermissionMode: "acceptEdits"}
		ts.adoptTerminalSession(&rec, "s1")
		if !reflect.DeepEqual(rec, tc.want) {
			t.Errorf("%s adopted %+v, want %+v", tc.backend, rec, tc.want)
		}
	}
	if _, ok := BuiltinBackendRegistrations()[BackendCodex].Backend.(terminalSession); ok {
		t.Error("codex must not adopt a terminal session")
	}
}

// builtinLifecycle is the production lifecycle policy a built-in backend
// registers, for fixtures that stand a fake process behind that backend's name.
func builtinLifecycle(backend string) LifecyclePolicy {
	return builtinRegistration(backend).Lifecycle
}

// TestBuiltinDashboardTraits pins every built-in's dashboard declaration with
// literal expectations; wsdashboard's own fixture (testHarnessTraits) mirrors
// these because that package cannot import this one.
func TestBuiltinDashboardTraits(t *testing.T) {
	want := wsdashboard.HarnessTraitSet{
		BackendClaude: {PushesClaudeUsage: true, UntaggedRowsNative: true, TerminalTakeover: true, ReplayClientName: "claude-code"},
		BackendCodex:  {SeparateFiveHourHistory: true, ReplayClientName: "codex"},
		BackendAgy:    {ReplayClientName: "agy"},
		BackendWerk:   {ReplayClientName: "ws-werk"},
		BackendPiRPC:  {ReplayClientName: pirpcBridgeClientName},
	}
	runner := policyRunner(BuiltinBackendRegistrations())
	if got := runner.DashboardHarnesses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("dashboard traits = %+v, want %+v", got, want)
	}
	if got := BuiltinDashboardHarnesses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("builtin dashboard traits = %+v, want %+v", got, want)
	}
	// An extension that declares nothing is registered with zero traits.
	regs := BuiltinBackendRegistrations()
	regs["fixture"] = BackendRegistration{Backend: ClaudeBackend{}, LeadProfile: "fixture-lead"}
	got := policyRunner(regs).DashboardHarnesses()
	if zero, ok := got["fixture"]; !ok || zero != (wsdashboard.HarnessTraits{}) {
		t.Fatalf("an undeclared extension = %+v, present %v", zero, ok)
	}
}

// TestBuiltinConnectionPolicy pins every built-in's connection declaration.
func TestBuiltinConnectionPolicy(t *testing.T) {
	type conn struct {
		named        bool
		bridge       string
		daemonAgents bool
		delegates    bool
	}
	want := map[string]conn{
		BackendClaude: {named: true, delegates: true},
		BackendCodex:  {named: true},
		BackendAgy:    {},
		BackendWerk:   {},
		BackendPiRPC:  {bridge: pirpcBridgeClientName},
	}
	got := map[string]conn{}
	for _, named := range BuiltinConnections() {
		c := named.Connection
		got[named.Name] = conn{c.NamedClient, c.BridgeClientName, c.ClientDaemonAgents, c.DelegatesThroughDaemon != nil}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connection policy = %+v, want %+v", got, want)
	}
	if names := BuiltinHarnessNames(); len(BuiltinConnections()) != len(names) {
		t.Fatalf("BuiltinConnections lists %d backends, want %d", len(BuiltinConnections()), len(names))
	}
	if c := BuiltinConnection("not-built-in"); c.NamedClient || c.DelegatesThroughDaemon != nil {
		t.Fatalf("an unknown name declares %+v", c)
	}
	if l := BuiltinLifecycle(BackendPi); l.TerminalSummaries || BuiltinLifecycle("not-built-in") != (LifecyclePolicy{}) {
		t.Fatalf("BuiltinLifecycle = %+v", l)
	}
}

// TestDelegatesThroughDaemon: the Claude declaration follows RemovesSubagents
// and the Pi one follows its launched profile (PiMCPOnly plus the spawn and
// mailbox tools), reading the snapshot first and the resolver only for a
// non-lead record that has none.
func TestDelegatesThroughDaemon(t *testing.T) {
	claude := BuiltinConnection(BackendClaude).DelegatesThroughDaemon
	if !claude(Record{Backend: BackendClaude, Lead: true}, nil) || !claude(Record{Backend: BackendClaude, Profile: AgentWorkerProfile(BackendClaude)}, nil) {
		t.Error("a hosted Claude lead or agent.spawn child keeps no native subagent tool")
	}
	if claude(Record{Backend: BackendClaude, Profile: SupportProfileClaude}, nil) {
		t.Error("the Claude support profile keeps its native subagent tool")
	}

}

// TestResidualPolicyDrivesCommonCode: the compaction, session-state, exec
// cutover, internal-turn, and worker-log differences common code used to read
// from the harness name come from the registration, so a fixture backend under
// any name gets them and a built-in name registered without them gets none.
func TestResidualPolicyDrivesCommonCode(t *testing.T) {
	runner := policyRunner(map[string]BackendRegistration{
		"fixture": {Backend: &policyBackend{}, Lifecycle: LifecyclePolicy{
			NoWorkerCompactionSteer: true, WorkerSessionState: true, ExecReadTools: true,
			InternalTurns: true, WorkerLog: true,
		}},
		BackendAgy: {Backend: &fakeBackend{}},
	})
	if !runner.lifecycle("fixture").WorkerLog || !runner.lifecycle("fixture").NoWorkerCompactionSteer || !runner.lifecycle("fixture").WorkerSessionState {
		t.Fatalf("lifecycle = %+v", runner.lifecycle("fixture"))
	}
	held := Record{Backend: "fixture", Parent: &ParentRef{}, HolderID: "h", HandedOffAt: "t"}
	if !runner.heldInternalWorker(held) {
		t.Error("a held worker of an InternalTurns backend is not held-internal")
	}
	held.Backend = BackendAgy
	if runner.heldInternalWorker(held) || runner.internalPrompt(Record{Backend: BackendAgy, Parent: &ParentRef{}, TurnStartedAt: 1}) {
		t.Error("an agy registration that declares no InternalTurns got agy behavior")
	}
	stamped := Record{Backend: "fixture", Parent: &ParentRef{}, TurnStartedAt: 1}
	if !runner.internalPrompt(stamped) {
		t.Error("a stamped idle worker turn is not an internal prompt")
	}
	stamped.TurnOpen = true
	if runner.internalPrompt(stamped) {
		t.Error("an open turn is an internal prompt")
	}
	if got := cutoverReplacement(runner.lifecycle("fixture")); !slices.Equal(got, append([]string{"exec.shell"}, CodexReadWSTools...)) {
		t.Errorf("replacement = %v", got)
	}
	if got := cutoverReplacement(LifecyclePolicy{}); !slices.Equal(got, []string{"exec.shell"}) {
		t.Errorf("replacement without read tools = %v", got)
	}
}

// TestBuiltinResidualPolicyInterfaces pins which built-in declares the
// extension-bridge compaction and the audit adapter, with literal expectations.
func TestBuiltinResidualPolicyInterfaces(t *testing.T) {
	if h, a := (&claudeMapper{}).newAudit("s"); h != BackendClaude || a == nil {
		t.Errorf("claude audit = %q %v", h, a)
	}
	if h, a := (&codexMapper{}).newAudit("s"); h != BackendCodex || a == nil {
		t.Errorf("codex audit = %q %v", h, a)
	}
}

// TestTierHarnessesLeadWithClaude pins the order the dashboard tier-routing
// view relies on for its default harness (its first entry, which was a Claude
// preference before the view stopped naming a harness).
func TestTierHarnessesLeadWithClaude(t *testing.T) {
	if got := TierHarnesses(); len(got) == 0 || got[0] != BackendClaude {
		t.Fatalf("TierHarnesses = %v, want %s first", got, BackendClaude)
	}
	if got := TierHarnesses(map[string]BackendRegistration{"extension": {}}); got[0] != BackendClaude {
		t.Fatalf("an extension registration displaced the default: %v", got)
	}
}

// TestRunnerPolicyAccessorsMatchTheRegistryCopy pins the per-name accessors to
// the full registry copy they stand in for on hot paths.
func TestRunnerPolicyAccessorsMatchTheRegistryCopy(t *testing.T) {
	r := &Runner{opts: Options{Backends: BuiltinBackendRegistrations()}}
	for name, reg := range r.BackendRegistrations() {
		life, ok := r.BackendLifecycle(name)
		if !ok || life != reg.Lifecycle {
			t.Errorf("%s: BackendLifecycle = %+v, %v; want %+v", name, life, ok, reg.Lifecycle)
		}
		conn, ok := r.BackendConnection(name)
		if !ok || conn.BridgeClientName != reg.Connection.BridgeClientName || conn.NamedClient != reg.Connection.NamedClient {
			t.Errorf("%s: BackendConnection = %+v, %v; want %+v", name, conn, ok, reg.Connection)
		}
	}
	if _, ok := r.BackendLifecycle("unregistered"); ok {
		t.Error("an unregistered name reported a lifecycle")
	}
	if _, ok := r.BackendConnection("unregistered"); ok {
		t.Error("an unregistered name reported a connection")
	}
}
