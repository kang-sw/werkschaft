package wsagent

import (
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestSubagentSteerFollowsPolicyAndCatalog: the native-subagent line goes
// to a session under the removal policy (a lead, an agent.spawn worker)
// whose backend's catalog names its native subagent tool for that role,
// whatever the harness can physically remove; a tracker support
// conversation, a backend with no catalog, and a role the catalog names no
// tool for (agy's lead) get none.
func TestSubagentSteerFollowsPolicyAndCatalog(t *testing.T) {
	r := &Runner{opts: Options{Backends: map[string]BackendRegistration{
		BackendClaude: {Catalog: ClaudeCatalog()},
		BackendCodex:  {Catalog: CodexCatalog()},
		BackendAgy:    {Catalog: AgyCatalog()},
		BackendPi:     {},
	}}}
	steer := func(tool string) string {
		return "Never use the native `" + tool + "` tool. Start a subagent only with the ws `agent.spawn` tool, and continue one with the ws `mailbox.send` tool."
	}
	for _, tc := range []struct {
		name string
		rec  Record
		want string
	}{
		{"claude lead", Record{Backend: BackendClaude, Lead: true}, steer("Agent")},
		{"claude worker", Record{Backend: BackendClaude, Profile: AgentWorkerProfile(BackendClaude)}, steer("Agent")},
		{"codex lead", Record{Backend: BackendCodex, Lead: true}, steer("spawn_agent")},
		{"agy lead names no tool", Record{Backend: BackendAgy, Lead: true}, ""},
		{"tracker support is outside the policy", Record{Backend: BackendClaude, Profile: "tracker-support"}, ""},
		{"no catalog", Record{Backend: BackendPi, Lead: true}, ""},
		{"unregistered backend", Record{Backend: "other", Lead: true}, ""},
	} {
		if got := r.subagentSteer(tc.rec); got != tc.want {
			t.Errorf("%s: steer = %q, want %q", tc.name, got, tc.want)
		}
	}
	var none *HarnessProfile
	if got := none.NativeSubagentTool(RoleLead); got != "" {
		t.Fatalf("a nil catalog names %q", got)
	}
}

// TestNativeFirstSteerForPreferNativeSubagentLead: the registered agy
// backend sets PreferNativeSubagent, and nothing else does, so an agy hosted
// lead's composed instructions carry the native-first line (native
// exploration, external-harness spawns, the size-prefix rule, the one-shot
// note) and no never-use line, while Claude and Codex leads keep theirs
// byte for byte. An agy worker is outside the option (it is the lead's).
func TestNativeFirstSteerForPreferNativeSubagentLead(t *testing.T) {
	regs := BuiltinBackendRegistrations()
	for name, reg := range regs {
		if reg.PreferNativeSubagent != (name == BackendAgy) {
			t.Fatalf("%s PreferNativeSubagent = %v", name, reg.PreferNativeSubagent)
		}
	}
	r := &Runner{opts: Options{Backends: regs}}
	agyLead := Record{Backend: BackendAgy, Lead: true, WSKey: "k-agy"}
	steer := r.subagentSteer(agyLead)
	want := "Use your native subagent tool for exploration and research that needs no ws tools; it runs at your harness's default model, so when a question needs a specific tier, use a read-only ws `agent.spawn` subagent at an external-harness tier instead. " +
		"Run rendered ws playbooks and any work that needs ws tools on an external harness: start it with the ws `agent.spawn` tool at a `claude.<size>`, `codex.<size>`, or configured route target, and continue it with the ws `mailbox.send` tool. " +
		"When a render recommends a bare size or a role tier that resolves to your own harness, put `claude.` or `codex.` before the size instead of passing it as returned. " +
		"A native subagent is one-shot: ask a follow-up of a new one; `mailbox.send` continues only agents started with `agent.spawn`."
	if steer != want {
		t.Fatalf("agy lead steer =\n%s\nwant\n%s", steer, want)
	}
	path, err := composeLeadInstructions(t.TempDir(), agyLead, "# Workflow Manual\nrendered", steer, regs[BackendAgy].PlaybookCommands)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, leadKeySentence("k-agy")+want+" ") || strings.Contains(text, "Never use the native") {
		t.Fatalf("agy lead instructions =\n%s", text)
	}

	never := func(tool string) string {
		return "Never use the native `" + tool + "` tool. Start a subagent only with the ws `agent.spawn` tool, and continue one with the ws `mailbox.send` tool."
	}
	for _, tc := range []struct {
		rec  Record
		want string
	}{
		{Record{Backend: BackendClaude, Lead: true}, never("Agent")},
		{Record{Backend: BackendCodex, Lead: true}, never("spawn_agent")},
		{Record{Backend: BackendPi, Lead: true}, ""},
	} {
		if got := r.subagentSteer(tc.rec); got != tc.want {
			t.Errorf("%s lead=%v steer = %q, want %q", tc.rec.Backend, tc.rec.Lead, got, tc.want)
		}
	}
	if got := r.subagentSteer(Record{Backend: BackendAgy, Profile: AgentWorkerProfile(BackendAgy)}); strings.Contains(got, "Use your native") {
		t.Fatalf("an agy worker takes the lead-only native-first line: %q", got)
	}
	// A catalog that names the tool gets it named; the wording is otherwise
	// the same.
	if got := nativeFirstSteer("invoke_subagent"); !strings.HasPrefix(got, "Use your native `invoke_subagent` tool for exploration") {
		t.Fatalf("named native-first steer = %q", got)
	}
}

// TestAgyLeadHarnessQualifiedTierStartsClaudeChild: the explore render's
// recommendation for an agy lead is a bare size ("small", pinned by mcp's
// TestNativeFirstLeavesPlaybookRenderUnchanged). Passed as returned, it
// resolves to the lead's own harness; with the native-first line's size
// prefix, agent.spawn admits `claude.small` and starts a Claude child. No
// external route is configured.
// Role-tier coverage (a role tier resolving to agy, qualified to an
// external harness) is omitted: the base carries no role routing graph.
func TestAgyLeadHarnessQualifiedTierStartsClaudeChild(t *testing.T) {
	h, _ := agentHarness(t, func(o *Options) {
		claude := o.Backends[BackendClaude]
		o.Backends[BackendAgy] = BackendRegistration{Backend: claude.Backend, LeadProfile: LeadProfileAgy, PreferNativeSubagent: true,
			WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendAgy)}
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(o.Config, "small", "", "agy-small", BackendAgy, "high"); err != nil {
			t.Fatal(err)
		}
	})
	caller := LeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	const recommended = "small"

	bare, _ := h.agentRunning(caller, AgentSpawnRequest{Prompt: "explore", Tier: recommended, Authority: "read-only"})
	if rec := h.record(bare); rec.Backend != BackendAgy {
		t.Fatalf("a bare size from an agy lead resolves to %s, want agy (the case the prefix rule avoids)", rec.Backend)
	}

	qualified, _ := h.agentRunning(caller, AgentSpawnRequest{Prompt: "explore", Tier: "claude." + recommended, Authority: "read-only"})
	if rec := h.record(qualified); rec.Backend != BackendClaude || rec.Profile != AgentWorkerProfile(BackendClaude) || rec.Model != "claude-small" {
		t.Fatalf("claude.%s child record: %+v", recommended, rec)
	}
}

// TestPlaybookCommandTurnText: a rendered playbook rides in the turn with
// the command's arguments, so the session needs no playbook.read; an empty
// body points at that read instead.
func TestPlaybookCommandTurnText(t *testing.T) {
	got := playbookCommandTurn(wsinbox.Command{Name: "lead-run", Args: " ticket-x "}, "# Lead Run\nbody")
	want := "The user ran the `/lead-run` command with these arguments:\n\nticket-x\n\n" +
		"Run the `lead-run` playbook below; it is the whole playbook, so it needs no `playbook.read`.\n\n" +
		"<playbook name=\"lead-run\">\n# Lead Run\nbody\n</playbook>"
	if got != want {
		t.Fatalf("turn =\n%s\nwant\n%s", got, want)
	}
	got = playbookCommandTurn(wsinbox.Command{Name: "lead-review"}, "")
	want = "The user ran the `/lead-review` command with no arguments.\n\n" +
		"Read the `lead-review` playbook with the ws `playbook.read` tool and run it."
	if got != want {
		t.Fatalf("pointer turn =\n%s\nwant\n%s", got, want)
	}
}

// TestHostedLeadPlaybookCommands: a hosted lead of a backend with
// PlaybookCommands offers the lead playbooks as dashboard commands from its
// creation, merged into the harness's own list (a harness command of the
// same name gives way), and its prompt carries the native-subagent line and
// says what a `/lead-*` reference is. Running one sends the rendered
// playbook as the turn (one render per run, none at launch), a render
// failure sends the pointer to playbook.read, and any other command still
// runs as the harness's own; the page's user entry is the typed line. The
// list is read once per runner.
func TestHostedLeadPlaybookCommands(t *testing.T) {
	h := newLeadHarness(t)
	reg := h.opts.Backends[BackendClaude]
	reg.Catalog, reg.PlaybookCommands = ClaudeCatalog(), true
	h.opts.Backends[BackendClaude] = reg
	var loads atomic.Int32
	h.opts.PlaybookCommands = func() ([]wslive.SlashCommand, error) {
		loads.Add(1)
		return []wslive.SlashCommand{{Name: "lead-run", Description: "Run a ticket", Source: "pkg", Layer: "x"}, {Name: "review", Description: "Review playbook"}}, nil
	}
	var mu sync.Mutex
	var expands [][4]string
	h.opts.ExpandCommand = func(backend, dir, key, name string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		expands = append(expands, [4]string{backend, dir, key, name})
		if name == "review" {
			return "partial", errors.New("render broke")
		}
		return "\n# Lead Run\nbody\n", nil
	}
	h.opts.LeadManual = (&leadManualRender{text: "\n# Workflow Manual\nrendered\n"}).render
	h.start()

	playbooks := []wslive.SlashCommand{
		{Name: "lead-run", Description: "Run a ticket", Source: PlaybookCommandSource},
		{Name: "review", Description: "Review playbook", Source: PlaybookCommandSource},
	}
	if got := h.snapshot(h.lead(BackendClaude, LeadOptions{}).ID).Commands; !slices.Equal(got, playbooks) {
		t.Fatalf("a new lead offers its playbook commands before any launch: %+v", got)
	}
	if got := h.snapshot(h.lead(BackendPi, LeadOptions{}).ID).Commands; len(got) != 0 {
		t.Fatalf("a backend without PlaybookCommands offers none: %+v", got)
	}

	h.holders.setControl("initialize", "", `{"commands":[{"name":"compact","description":"Free up context"},{"name":"review","description":"Harness review"}]}`)
	id, holder := h.runLead("one")
	key := h.record(id).WSKey
	merged := append([]wslive.SlashCommand{{Name: "compact", Description: "Free up context"}}, playbooks...)
	eventually(t, "the merged command list", func() bool { return slices.Equal(h.snapshot(id).Commands, merged) })
	wantPrompt := leadKeySentence(key) +
		"Never use the native `Agent` tool. Start a subagent only with the ws `agent.spawn` tool, and continue one with the ws `mailbox.send` tool. " +
		"This session has no ws skills: a `/<name>` reference such as `/lead-run` names a workflow playbook. " +
		"The user runs one as that dashboard command, which sends you its playbook; you run one by reading it with the ws `playbook.read` tool and following it. " +
		"The workflow manual below is binding and stays in this prompt across compaction. " +
		"Use the session state delivered with your first turn or in a restore bundle. " +
		"For fresh session state, and where the manual says to invoke `/lead-revive`, call the ws `workflow_state` tool with this key.\n\n" +
		"# Workflow Manual\nrendered\n"
	if prompt := appendedPrompt(t, h.holders.spawned(holder).Argv); prompt != wantPrompt {
		t.Fatalf("lead prompt =\n%s\nwant\n%s", prompt, wantPrompt)
	}
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)

	h.sendCommand(id, "lead-run", "ticket-x")
	run := playbookCommandTurn(wsinbox.Command{Name: "lead-run", Args: "ticket-x"}, "# Lead Run\nbody")
	eventually(t, "the playbook turn", func() bool { return slices.Equal(h.userInputs(holder), []string{h.envelope("one"), run}) })
	h.holders.out(holder, turnLines("sess-1", "u2", "ran")...)
	h.idle(id)

	h.sendCommand(id, "review", "")
	pointer := playbookCommandTurn(wsinbox.Command{Name: "review"}, "")
	eventually(t, "the pointer turn", func() bool { return slices.Equal(h.userInputs(holder), []string{h.envelope("one"), run, pointer}) })
	h.holders.out(holder, turnLines("sess-1", "u3", "read")...)
	h.idle(id)
	if !strings.Contains(h.log.String(), "/review: playbook not rendered, the turn points at it: render broke") {
		t.Fatalf("the render failure is not logged: %s", h.log.String())
	}

	h.sendCommand(id, "compact", "")
	eventually(t, "the harness command", func() bool {
		return slices.Equal(h.userInputs(holder), []string{h.envelope("one"), run, pointer, "/compact"})
	})
	h.holders.out(holder, turnLines("sess-1", "u4", "compacted")...)
	h.idle(id)

	if got, want := ringTexts(h.ring(id), wslive.RingUser), []string{h.envelope("one"), "/lead-run ticket-x", "/review", "/compact"}; !slices.Equal(got, want) {
		t.Fatalf("ring user entries = %q, want %q", got, want)
	}
	mu.Lock()
	got := slices.Clone(expands)
	mu.Unlock()
	if len(got) != 2 || got[0][0] != BackendClaude || !sameDir(got[0][1], h.root) || got[0][2] != key || got[0][3] != "lead-run" || got[1][3] != "review" {
		t.Fatalf("each playbook command renders once, for the lead's backend, directory, and key: %v", got)
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("the playbook command list is read once per runner: %d reads", n)
	}

	// A rejoined lead offers the new runner's playbook commands, in place of
	// the earlier runner's, beside the harness commands its snapshot kept,
	// without waiting for a new launch.
	h.opts.PlaybookCommands = func() ([]wslive.SlashCommand, error) {
		return []wslive.SlashCommand{{Name: "lead-run", Description: "Run a ticket"}, {Name: "lead-ship", Description: "Ship"}}, nil
	}
	// The holder scan, whose replay would bring the harness's list back, is
	// held: the list read is the one the rejoin itself offers.
	h.runner.Close()
	h.holders.restart()
	entered, release := h.gateScan()
	defer release()
	h.start()
	wait(t, "the first scan", entered)
	var offered []wslive.SlashCommand
	c := h.runner.conv(id)
	c.do(func() { offered = c.live.st.Snapshot(h.opts.Now().UnixMilli()).Commands })
	rejoined := []wslive.SlashCommand{
		{Name: "compact", Description: "Free up context"},
		{Name: "lead-run", Description: "Run a ticket", Source: PlaybookCommandSource}, {Name: "lead-ship", Description: "Ship", Source: PlaybookCommandSource},
	}
	if !slices.Equal(offered, rejoined) {
		t.Fatalf("rejoined command list = %+v, want %+v", offered, rejoined)
	}
}

// TestAgentChildPromptCarriesTheSteer: an agent.spawn child of a backend
// whose catalog names its native subagent tool gets the parent return address
// and the never-use line right after its key sentence.
func TestAgentChildPromptCarriesTheSteer(t *testing.T) {
	h, _ := agentHarness(t, func(o *Options) {
		reg := o.Backends[BackendClaude]
		reg.Catalog = ClaudeCatalog()
		o.Backends[BackendClaude] = reg
	})
	a, _ := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "go", Tier: "small", Authority: "read-only", SystemPrompt: "You are the cache probe."})
	text := instructionsOf(t, h.backend.start(0))
	want := "You are the cache probe.\n\nYour ws session key is `" + h.record(a).WSKey + "`. Pass it as the `session_key` argument of every ws tool call. " +
		"This key is bound to worktree `" + h.record(a).Root + "`; if work moves to another worktree, use a key minted for that destination (from a direct spawn there or a legitimate acquisition that returns one), because this key is never rebound. " +
		"Your direct parent's mailbox reply address is `id:" + leadParent.ReplyID + "`; send it reports or questions with `mailbox.send`. " +
		"Never use the native `Agent` tool. Start a subagent only with the ws `agent.spawn` tool, and continue one with the ws `mailbox.send` tool.\n"
	if text != want {
		t.Fatalf("the child's appended prompt = %q, want %q", text, want)
	}
}

// TestPlaybookCommandsFailedReadOffersNone: a failed list read is logged
// once and never retried, and a list a snapshot kept drops the playbook
// commands this runner cannot run; a backend without PlaybookCommands keeps
// its list as it is.
func TestPlaybookCommandsFailedReadOffersNone(t *testing.T) {
	log := &logBuffer{}
	reads := 0
	r := &Runner{opts: Options{Log: log, Backends: map[string]BackendRegistration{BackendClaude: {PlaybookCommands: true}, BackendPi: {}},
		PlaybookCommands: func() ([]wslive.SlashCommand, error) { reads++; return nil, errors.New("package broke") }}}
	kept := []wslive.SlashCommand{{Name: "compact"}, {Name: "lead-run", Source: PlaybookCommandSource}}
	for range 3 {
		if got := r.withPlaybookCommands(Record{Backend: BackendClaude, Lead: true}, kept); !slices.Equal(got, kept[:1]) {
			t.Fatalf("offered %+v, want the harness commands alone", got)
		}
	}
	if reads != 1 || strings.Count(log.String(), "no playbook commands: package broke") != 1 {
		t.Fatalf("a failed read is tried and logged once: %d reads, log %q", reads, log.String())
	}
	if got := r.withPlaybookCommands(Record{Backend: BackendPi, Lead: true}, kept); !slices.Equal(got, kept) {
		t.Fatalf("a backend without playbook commands changed its list: %+v", got)
	}
	if got := r.withPlaybookCommands(Record{Backend: BackendClaude}, kept); !slices.Equal(got, kept) {
		t.Fatalf("a worker's list changed: %+v", got)
	}
}
