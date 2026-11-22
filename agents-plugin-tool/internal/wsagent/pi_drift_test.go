package wsagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The Pi workflow bundles and the native first-party payload are generated
// from the plugin trees, config manifests included, and nothing but CI's
// Linux-only Python steps checked them: a manifest edit shipped a stale bundle
// until a later change regenerated it. This runs the same two producer checks,
// so a Go run catches the drift too. A regeneration is
//
//	python3 agents-plugin-pi/scripts/prepare-workflow-bundles.py
//	python3 agents-plugin-pi/scripts/prepare-native-payload.py --first-party-only
//
// from the repository root. When the Pi harness is retired, this test's Pi
// script calls go with the scripts.
func TestPiGeneratedBundlesMatchTheirSources(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bundle checks are Linux-only in CI; their Python producers target POSIX paths")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; CI runs these checks")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(python, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("python3 %v: %v\n%s\nRegenerate with agents-plugin-pi/scripts/prepare-workflow-bundles.py and prepare-native-payload.py --first-party-only", args, err, out)
		}
	}
	run(filepath.Join("agents-plugin-pi", "scripts", "prepare-workflow-bundles.py"), "--check")
	for _, product := range []string{"agents-plugin", "agents-plugin-wsflow"} {
		t.Run(product, func(t *testing.T) {
			run(filepath.Join(product, "bin", "ws-pi-payload.py"), "--plugin", product, "--check-source", ".")
		})
	}
}
