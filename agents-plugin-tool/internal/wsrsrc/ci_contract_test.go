package wsrsrc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Exercise selection with a fake Go command so these guards never run suites.
func TestCITestShardSelection(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci-test-shard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scripts", "ci-test-shard.sh")
	if err := os.WriteFile(path, script, 0644); err != nil {
		t.Fatal(err)
	}
	fakeGo := `#!/usr/bin/env bash
set -eu
if [[ "$1" == list ]]; then
  if [[ "${FAIL_LIST:-}" == 1 ]]; then exit 42; fi
  printf '%s\n' \
    example/cmd/ws-mcp \
    example/internal/mcp \
    example/internal/wsagent \
    example/internal/wsdashboard \
    example/internal/wsfuture \
    example/internal/wsledger \
    example/internal/wsledger/child \
    example/internal/wstrackerhost \
    example/internal/wswhiteboard
elif [[ "$2" == -list ]]; then
  printf '%s\n' TestD TestB TestA TestC ExampleZ FuzzX "ok $4"
else
  printf '%s\n' "$@"
fi
`
	if err := os.WriteFile(filepath.Join(root, "go"), []byte(fakeGo), 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) []string {
		t.Helper()
		cmd := exec.Command(bash, append([]string{path}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{
			[]string{"all"},
			[]string{
				"example/cmd/ws-mcp",
				"example/internal/mcp",
				"example/internal/wsagent",
				"example/internal/wsdashboard",
				"example/internal/wsfuture",
				"example/internal/wsledger/child",
				"example/internal/wswhiteboard",
			},
		},
		{
			[]string{"rest"},
			[]string{
				"example/cmd/ws-mcp",
				"example/internal/wsagent",
				"example/internal/wsfuture",
				"example/internal/wsledger/child",
				"example/internal/wswhiteboard",
			},
		},
		{
			[]string{"long"},
			[]string{"./internal/wsledger", "./internal/wstrackerhost"},
		},
		{
			[]string{"mcp"},
			[]string{"./internal/mcp"},
		},
		{
			[]string{"wsdashboard"},
			[]string{"./internal/wsdashboard"},
		},
	} {
		t.Run(strings.Join(tt.args, "_"), func(t *testing.T) {
			want := append([]string{"test", "-count=1", "-timeout", "30m", "-list", "."}, tt.want...)
			if got := run(append(tt.args, "-list", ".")...); !reflect.DeepEqual(got, want) {
				t.Fatalf("selection = %q, want %q", got, want)
			}
		})
	}

	// 3-way rest shards pin heavy packages to distinct buckets and cover every
	// remaining (including future) package with zero gaps and zero duplicates.
	restSeen := map[string]int{}
	var restUnion []string
	for i := 0; i < 3; i++ {
		got := run("rest", string(rune('0'+i)), "3", "-list", ".")
		pkgs := got[6:] // strip: test -count=1 -timeout 30m -list .
		if len(pkgs) == 0 {
			t.Fatalf("rest %d 3 selected no packages", i)
		}
		for _, pkg := range pkgs {
			restSeen[pkg]++
			restUnion = append(restUnion, pkg)
		}
	}
	sort.Strings(restUnion)
	wantRest := []string{
		"example/cmd/ws-mcp",
		"example/internal/wsagent",
		"example/internal/wsfuture",
		"example/internal/wsledger/child",
		"example/internal/wswhiteboard",
	}
	if !reflect.DeepEqual(restUnion, wantRest) {
		t.Fatalf("rest 0..2 union = %q, want %q", restUnion, wantRest)
	}
	for pkg, n := range restSeen {
		if n != 1 {
			t.Fatalf("rest package %q appeared %d times, want 1", pkg, n)
		}
	}

	// Sorted top-level tests partition without overlap, including examples/fuzz.
	for i, pattern := range []string{"^(ExampleZ|TestC)$", "^(FuzzX|TestD)$", "^(TestA)$", "^(TestB)$"} {
		index := string(rune('0' + i))
		want := []string{"test", "-count=1", "-timeout", "30m", "-run", pattern, "-list", ".", "./internal/mcp"}
		if got := run("mcp", index, "4", "-list", "."); !reflect.DeepEqual(got, want) {
			t.Fatalf("mcp %s = %q, want %q", index, got, want)
		}
	}
	for i, pattern := range []string{"^(ExampleZ|TestA|TestC)$", "^(FuzzX|TestB|TestD)$"} {
		index := string(rune('0' + i))
		want := []string{"test", "-count=1", "-timeout", "30m", "-run", pattern, "-list", ".", "./internal/wsdashboard"}
		if got := run("wsdashboard", index, "2", "-list", "."); !reflect.DeepEqual(got, want) {
			t.Fatalf("wsdashboard %s = %q, want %q", index, got, want)
		}
	}

	// Self-check verify mode must pass on the synthetic module.
	if out := run("verify"); len(out) == 0 || !strings.HasPrefix(out[0], "OK:") {
		t.Fatalf("verify output = %q, want OK summary", out)
	}

	// Verify ws-mcp-ci.yml defines the expected sharded matrix for both OSes.
	workflow, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ws-mcp-ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wfText := string(workflow)
	for _, entry := range []string{
		`{ os: ubuntu-latest, shard: mcp, args: "mcp" }`,
		`{ os: ubuntu-latest, shard: wsdashboard-0, args: "wsdashboard 0 2" }`,
		`{ os: ubuntu-latest, shard: wsdashboard-1, args: "wsdashboard 1 2" }`,
		`{ os: ubuntu-latest, shard: rest-0, args: "rest 0 3" }`,
		`{ os: ubuntu-latest, shard: rest-1, args: "rest 1 3" }`,
		`{ os: ubuntu-latest, shard: rest-2, args: "rest 2 3" }`,
		`{ os: windows-latest, shard: mcp-0, args: "mcp 0 4" }`,
		`{ os: windows-latest, shard: mcp-1, args: "mcp 1 4" }`,
		`{ os: windows-latest, shard: mcp-2, args: "mcp 2 4" }`,
		`{ os: windows-latest, shard: mcp-3, args: "mcp 3 4" }`,
		`{ os: windows-latest, shard: wsdashboard-0, args: "wsdashboard 0 2" }`,
		`{ os: windows-latest, shard: wsdashboard-1, args: "wsdashboard 1 2" }`,
		`{ os: windows-latest, shard: rest-0, args: "rest 0 3" }`,
		`{ os: windows-latest, shard: rest-1, args: "rest 1 3" }`,
		`{ os: windows-latest, shard: rest-2, args: "rest 2 3" }`,
	} {
		if !strings.Contains(wfText, entry) {
			t.Fatalf("ws-mcp-ci.yml missing matrix entry %s", entry)
		}
	}

	t.Setenv("FAIL_LIST", "1")
	cmd := exec.Command(bash, path, "all", "-list", ".")
	cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := cmd.Run(); err == nil {
		t.Fatal("failed package inventory must fail selection, not report a partial pass")
	}
}

func TestCIReleaseLongSkipGate(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq unavailable")
	}
	workflow, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ws-mcp-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var predicate string
	for _, line := range strings.Split(string(workflow), "\n") {
		if strings.Contains(line, "ran=$(gh api") {
			_, tail, ok := strings.Cut(line, "--jq '")
			if ok {
				predicate, _, _ = strings.Cut(tail, "'")
			}
		}
	}
	if predicate == "" {
		t.Fatal("release job predicate missing")
	}
	type job struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	}
	regular := job{"go test (windows-latest, rest)", "success"}
	for _, tt := range []struct {
		name string
		jobs []job
		want string
	}{
		{"empty", nil, "false"},
		{"legacy green", []job{regular}, "true"},
		{"default", []job{regular, {"go test (ubuntu-latest, long)", "skipped"}, {"go test (windows-latest, long)", "skipped"}}, "true"},
		{"long success", []job{regular, {"go test (windows-latest, long)", "success"}}, "true"},
		{"regular skipped", []job{{regular.Name, "skipped"}}, "false"},
		{"regular failure", []job{{regular.Name, "failure"}}, "false"},
		{"unknown skipped", []job{regular, {"go test (other, long)", "skipped"}}, "false"},
		{"long failure", []job{regular, {"go test (windows-latest, long)", "failure"}}, "false"},
		{"long cancelled", []job{regular, {"go test (ubuntu-latest, long)", "cancelled"}}, "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := tt.jobs
			if jobs == nil {
				jobs = []job{}
			}
			input, err := json.Marshal(map[string]any{"jobs": jobs})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(jq, "-r", predicate)
			cmd.Stdin = strings.NewReader(string(input))
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != tt.want {
				t.Fatalf("gate = %s (%v), want %s", out, err, tt.want)
			}
		})
	}
}

func TestCIReleaseTauriVersion(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	workflow, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ws-mcp-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, step, ok := strings.Cut(string(workflow), "      - name: Select the Tauri build version\n")
	if !ok {
		t.Fatal("desktop version selector missing")
	}
	step, _, ok = strings.Cut(step, "\n\n")
	if !ok {
		t.Fatal("desktop version selector boundary missing")
	}
	_, body, ok := strings.Cut(step, "        run: |\n")
	if !ok {
		t.Fatal("desktop version selector shell missing")
	}
	for _, tt := range []struct {
		name, ref, refName, dryRun, dispatch, version string
		invalid                                       bool
	}{
		{name: "stable tag", ref: "refs/tags/v0.52.0", refName: "v0.52.0", version: "0.52.0"},
		{name: "canary tag", ref: "refs/tags/v0.52.0-canary.0", refName: "v0.52.0-canary.0", version: "0.52.0-canary.0"},
		{name: "other tag", ref: "refs/tags/v0.52.0-rc.1", refName: "v0.52.0-rc.1"},
		{name: "normal dispatch", ref: "refs/heads/develop", refName: "develop", dryRun: "false", dispatch: "0.52.0"},
		{name: "pull request", ref: "refs/pull/1/merge"},
		{name: "dry run stable", dryRun: "true", dispatch: "0.52.0", version: "0.52.0"},
		{name: "dry run canary", dryRun: "true", dispatch: "v0.52.0-canary.19", version: "0.52.0-canary.19"},
		{name: "dry run default", dryRun: "true", dispatch: "0.52.0-dev", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(output, nil, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, "-c", body)
			cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+output, "GITHUB_REF="+tt.ref,
				"GITHUB_REF_NAME="+tt.refName, "TAURI_DRY_RUN="+tt.dryRun, "DISPATCH_VERSION="+tt.dispatch)
			log, err := cmd.CombinedOutput()
			if tt.invalid {
				if err == nil || !strings.Contains(string(log), "::error::Set version") {
					t.Fatalf("selector accepted invalid dry-run version or failed unexpectedly: %v\n%s", err, log)
				}
			} else if err != nil {
				t.Fatalf("selector failed: %v\n%s", err, log)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tt.version != "" {
				want = "tauri_version=" + tt.version + "\n"
			}
			if string(data) != want {
				t.Fatalf("selector output = %q, want %q", data, want)
			}
		})
	}
}

func TestCIReleasePublication(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	workflow, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ws-mcp-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	_, publication, ok := strings.Cut(text, "      - name: Publish GitHub release assets\n")
	if !ok {
		t.Fatal("native publication step missing")
	}
	publication, _, ok = strings.Cut(publication, "\n  windows-smoke:")
	if !ok || !strings.Contains(publication, "if: startsWith(github.ref, 'refs/tags/') && !inputs.tauri_dry_run && (steps.route.outputs.tauri_version == '' || contains(steps.route.outputs.tauri_version, '-canary.'))") {
		t.Fatal("native publication must handle non-Tauri and canary tags outside dry runs")
	}
	_, body, ok := strings.Cut(publication, "        run: |\n")
	if !ok {
		t.Fatal("native publication shell missing")
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if !strings.HasPrefix(line, "          ") {
			t.Fatalf("unexpected native publication indentation: %q", line)
		}
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "native.bin"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	gh := "#!/bin/sh\nif [ \"$2\" = view ]; then exit 1; fi\nprintf '<%s>\\n' \"$@\" >> \"$CALL_LOG\"\nprintf '<end>\\n' >> \"$CALL_LOG\"\n"
	if err := os.WriteFile(filepath.Join(root, "gh"), []byte(gh), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		tag    string
		canary bool
	}{
		{"v0.50.8", false},
		{"v0.50.9-canary.0", true},
		{"v12.3.4-canary.19", true},
		{"v0.50.9-canary.1-extra", false},
		{"v0.50.9-canary.x", false},
		{"v0.50.9-canary.1.2", false},
		{"v0.50.9-rc.1", false},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "calls")
			cmd := exec.Command(bash, "-c", strings.Join(lines, "\n"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "GITHUB_REF_NAME="+tt.tag, "CALL_LOG="+log)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("publication failed: %v\n%s", err, out)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			want := "<release>\n<create>\n<" + tt.tag + ">\n<--title>\n<" + tt.tag + ">\n<--notes>\n<Automated ws-mcp release assets for " + tt.tag + ".>\n"
			if tt.canary {
				want += "<--prerelease>\n<--latest=false>\n"
			}
			want += "<end>\n<release>\n<upload>\n<" + tt.tag + ">\n<dist/native.bin>\n<--clobber>\n<end>\n"
			if string(calls) != want {
				t.Fatalf("calls = %q, want %q", calls, want)
			}
		})
	}
	for _, removed := range []string{"desktop_dry_run", "desktop-build:", "desktop-release:", "tauri_route", "WERKSCHAFT_TAURI_RELEASE"} {
		if strings.Contains(text, removed) {
			t.Fatalf("retired desktop release contract still contains %q", removed)
		}
	}
	_, desktop, ok := strings.Cut(text, "\n  tauri-desktop:")
	if !ok {
		t.Fatal("Tauri desktop release job missing")
	}
	for _, required := range []string{
		"needs: build",
		"if: needs.build.outputs.tauri_version != ''",
		"uses: ./.github/workflows/werkschaft-tauri-build.yml",
		"version: ${{ needs.build.outputs.tauri_version }}",
		"dry_run: ${{ inputs.tauri_dry_run || false }}",
	} {
		if !strings.Contains(desktop, required) {
			t.Fatalf("desktop release contract missing %q", required)
		}
	}
	tauri, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "werkschaft-tauri-build.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"node scripts/assemble-daemon-input.cjs",
		"name: werkschaft-tauri-assets",
		"if: ${{ !inputs.dry_run && github.ref == format('refs/tags/v{0}', inputs.version) }}",
		"feed=\"$RUNNER_TEMP/tauri-release/werkschaft-tauri-update.json\"",
		"files=( \"$RUNNER_TEMP/ws-mcp-assets/\"* \"${desktop[@]}\" \"$feed\" )",
		"gh release upload \"$GITHUB_REF_NAME\" \"${desktop[@]}\" --repo \"$GITHUB_REPOSITORY\" --clobber",
		"gh release upload \"$GITHUB_REF_NAME\" \"$feed\" --repo \"$GITHUB_REPOSITORY\" --clobber",
	} {
		if !strings.Contains(string(tauri), required) {
			t.Fatalf("Tauri workflow contract missing %q", required)
		}
	}
}

func TestBumpWSVersionWritesLF(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "bump-ws-version.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	if !strings.Contains(script, `newline="\n"`) {
		t.Fatal(`bump-ws-version.sh must open files with newline="\n" so Windows runs do not emit CRLF`)
	}
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, ".write_text(") && !strings.Contains(line, `newline="\n"`) {
			t.Fatalf("bump-ws-version.sh has write_text call without explicit LF newline: %s", strings.TrimSpace(line))
		}
	}
}
