package wsagent

import (
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestCompanionWorkBrainLeadInstructionsAndCompaction(t *testing.T) {
	dir := t.TempDir()
	workRec := Record{
		WSKey:                 "brain-ws-key",
		Backend:               BackendClaude,
		Lead:                  true,
		CompanionRole:         companionRoleWork,
		CompanionSystemPrompt: "## Companion Brain Role\nPersona voice: Aria.",
	}
	path, err := composeLeadInstructions(dir, workRec, "# Full Lead Workflow Manual\nFollow tickets.", "Use ws/agent.spawn.", true)
	if err != nil {
		t.Fatalf("composeLeadInstructions: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read instructions.md: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"brain-ws-key",
		"Use ws/agent.spawn.",
		"# Full Lead Workflow Manual",
		"## Companion Brain Role",
		"Persona voice: Aria.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("companion-work instructions.md missing %q:\n%s", want, text)
		}
	}

	combined := combineLeadManualAndAddendum("# Full Lead Workflow Manual", "## Companion Brain Role")
	if !strings.Contains(combined, "# Full Lead Workflow Manual") || !strings.Contains(combined, "## Companion Brain Role") {
		t.Fatalf("combineLeadManualAndAddendum = %q", combined)
	}
	if got := combineLeadManualAndAddendum("", "addendum only"); got != "addendum only" {
		t.Fatalf("combineLeadManualAndAddendum empty manual = %q", got)
	}
	if got := combineLeadManualAndAddendum("manual only", ""); got != "manual only" {
		t.Fatalf("combineLeadManualAndAddendum empty addendum = %q", got)
	}

	// companion-work receives normal Full Lead compaction nudges and state instructions
	// (unlike companion-body, which has no ws/compact tool).
	root := t.TempDir()
	cfg := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir(), RepoRoot: root}
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.Set("compaction.lead_advisory", "110k", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(Options{
		Config:   cfg,
		Keys:     compactionRootKeys{root: root},
		Backends: map[string]BackendRegistration{BackendClaude: {Catalog: ClaudeCatalog()}},
	})
	c := &conv{r: r, rec: Record{Root: root, WSKey: "brain-key", Backend: BackendClaude, Lead: true, CompanionRole: companionRoleWork}}
	c.loadCompaction()
	v := int64(150_000)
	c.observeCompaction(wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &v, Window: 1_000_000}})
	c.compactIdle()
	if c.compactNudge.text == "" {
		t.Fatalf("companion-work Brain unexpectedly suppressed ws/compact advisory")
	}
	if got := c.compactionStateInstruction(); got == "" {
		t.Fatalf("companion-work Brain compactionStateInstruction = empty, want lead guidance")
	}
}
