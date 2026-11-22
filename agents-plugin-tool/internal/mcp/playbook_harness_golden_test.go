package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// harnessWordingGoldenDir holds the Claude, Codex, and Pi hosted leads'
// workflow_manual, playbook.read, and playbook.render outputs, captured from
// a base without the PreferNativeSubagent option: a change to the agy lead's
// wording must leave every one of them byte-identical.
const harnessWordingGoldenDir = "testdata/harness-wording"

// envRegenHarnessWordingGolden rewrites the goldens from the current tree
// instead of comparing against them.
const envRegenHarnessWordingGolden = "WS_REGEN_HARNESS_WORDING_GOLDEN"

// harnessWordingOutputs are one hosted lead's outputs, keyed by golden file
// stem, with the fixture's temporary root and session key replaced by fixed
// placeholders.
func harnessWordingOutputs(t *testing.T, backend string) map[string]string {
	t.Helper()
	s, f := hostedBackendConnection(t, backend, wsagent.BuiltinBackendRegistrations())
	normalize := func(text string) string {
		text = strings.ReplaceAll(text, f.root, "<root>")
		return strings.ReplaceAll(text, f.hosted, "<key>")
	}
	out := map[string]string{}

	resp := s.renderWorkflowManual(nil, map[string]any{"session_key": f.hosted}, false)
	result, _ := resp.Result.(map[string]any)
	content, _ := result["content"].([]map[string]string)
	if responseIsError(resp) || len(content) == 0 {
		t.Fatalf("%s workflow manual: %+v", backend, resp)
	}
	out["workflow-manual"] = normalize(content[0]["text"])

	for _, name := range []string{"lead-run", "lead-discuss"} {
		text, err := s.readPlaybook(name, f.hosted, nil)
		if err != nil {
			t.Fatalf("%s playbook.read %s: %v", backend, name, err)
		}
		out["read-"+name] = normalize(text)
	}

	rsrcRoot, err := s.resolveRsrcRoot("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"explore", "ticket-reviewer-design"} {
		// No mint root: a minted child key would differ per render.
		path, _, tier, err := renderPlaybookWithPlan(s, f.hosted, "", rsrcRoot, f.root, name, nil, s.configOptions(), "", "", "", nil, "", "")
		if err != nil {
			t.Fatalf("%s playbook.render %s: %v", backend, name, err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out["render-"+name] = "recommended-tier: " + tier + "\n" + normalize(string(body))
	}
	return out
}

// TestHarnessWordingGolden: the Claude, Codex, and Pi hosted leads'
// workflow_manual, playbook.read (lead-run, lead-discuss), and
// playbook.render (explore, ticket-reviewer-design) outputs equal the
// goldens captured from the base before PreferNativeSubagent.
func TestHarnessWordingGolden(t *testing.T) {
	regen := os.Getenv(envRegenHarnessWordingGolden) == "1"
	for _, backend := range []string{wsagent.BackendClaude, wsagent.BackendCodex, wsagent.BackendPi} {
		for stem, got := range harnessWordingOutputs(t, backend) {
			path := filepath.Join(harnessWordingGoldenDir, backend+"-"+stem+".golden")
			if regen {
				if err := os.MkdirAll(harnessWordingGoldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v (regenerate with %s=1)", path, err, envRegenHarnessWordingGolden)
			}
			if got != string(want) {
				t.Errorf("%s %s differs from %s:\n--- got\n%s\n--- want\n%s", backend, stem, path, got, want)
			}
		}
	}
}
