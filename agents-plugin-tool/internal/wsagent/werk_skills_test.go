package wsagent

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/werk"
)

func TestWerkDiscoverSkillsRootsAndCollisions(t *testing.T) {
	spec, home, codex, claude := werkSkillFixture(t)
	spec.Cwd = filepath.Join(spec.Root, "app")
	writeWerkSkillFixture(t, filepath.Join(spec.Root, ".git"), "gitdir: fixture")
	writeWerkSkill(t, filepath.Join(spec.Cwd, ".agents", "skills", "choice"), "choice", "nearest description", "nearest body\n")
	writeWerkSkill(t, filepath.Join(spec.Cwd, ".claude", "skills", "choice"), "choice", "Claude description", "Claude body\n")
	writeWerkSkill(t, filepath.Join(spec.Root, ".agents", "skills", "choice"), "choice", "root description", "root body\n")
	writeWerkSkill(t, filepath.Join(spec.Root, ".agents", "skills", "root-only"), "root-only", "root description", "root-only body\n")
	writeWerkSkill(t, filepath.Join(home, ".agents", "skills", "user"), "personal", "personal description", "personal body\n")
	writeWerkSkill(t, filepath.Join(codex, "skills", "legacy"), "legacy", "legacy description", "legacy body\n")
	writeWerkSkill(t, filepath.Join(codex, "skills", ".system", "bundled"), "bundled", "system description", "bundled body\n")
	writeWerkSkill(t, filepath.Join(claude, "skills", "private"), "claude-user", "user description", "claude-user body\n")
	writeWerkSkillFixture(t, filepath.Join(spec.Root, ".claude", "skills", "claude-project", "SKILL.md"),
		"\ufeff---\r\ndescription: >-\r\n  A folded\r\n  description.\r\n---\r\nclaude-project body\r\n")
	writeWerkSkillFixture(t, filepath.Join(claude, "skills", "user-only", "SKILL.md"),
		"---\nname: user-only\ndescription: Explicit user invocation only.\ndisable-model-invocation: true\n---\nbody\n")
	writeWerkSkill(t, filepath.Join(filepath.Dir(spec.Root), ".agents", "skills", "unrelated"), "unrelated", "unrelated description", "unrelated body\n")
	writeWerkSkill(t, filepath.Join(spec.Cwd, ".codex", "skills", "unsupported"), "unsupported", "unsupported description", "unsupported body\n")

	skills := discoverWerkSkills(spec)
	var names []string
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	want := []string{"bundled", "choice", "claude-project", "claude-user", "legacy", "personal", "root-only"}
	if !slices.Equal(names, want) {
		t.Fatalf("indexed %v, want %v", names, want)
	}
	byName := werkSkillsByName(skills)
	if skill := byName["choice"]; skill.Description != "nearest description" {
		t.Errorf("collision selected %q", skill.Description)
	}
	assertWerkSkillBody(t, byName["choice"], "nearest body\n")
	if skill := byName["claude-project"]; skill.Description != "A folded description." {
		t.Errorf("folded description = %q", skill.Description)
	}
	assertWerkSkillBody(t, byName["claude-project"], "claude-project body\r\n")
}

func TestWerkDiscoverSkillsPluginRootsByRole(t *testing.T) {
	spec, _, codex, claude := werkSkillFixture(t)
	profilePlugin := filepath.Join(spec.Root, "profile-plugin")
	spec.Profile.Plugins = []string{"profile-plugin"}
	writeWerkSkillFixture(t, filepath.Join(profilePlugin, ".claude-plugin", "plugin.json"), `{"name":"profile-plugin", "skills":["./custom", "./../outside", "sub/../skills"]}`)
	writeWerkSkill(t, filepath.Join(profilePlugin, "skills", "default"), "default", "profile skill", "profile body\n")
	writeWerkSkill(t, filepath.Join(profilePlugin, "custom"), "custom", "custom skill", "custom body\n")
	writeWerkSkill(t, filepath.Join(spec.Root, "outside", "private"), "private", "other skill", "outside body\n")

	writeWerkSkillFixture(t, filepath.Join(codex, "config.toml"), `example = """
[plugins."ghost@market"]
enabled = true
"""
[plugins."codex-tools@market"] # active plugin
enabled = true
[plugins."disabled@market"]
enabled = false
[plugins."ws@market"]
enabled = true
[plugins."../outside@market"]
enabled = true
[[skills.config]]
name = "irrelevant"
enabled = false
[skills.bundled]
enabled = false
`)
	writeWerkSkill(t, filepath.Join(codex, "skills", ".system", "bundled"), "bundled", "bundled description", "bundled body\n")
	for _, plugin := range []struct{ name, version string }{
		{"codex-tools", "1.0.9"}, {"codex-tools", "1.0.10"}, {"codex-tools", "1.0.10-preview"},
		{"disabled", "1.0.0"}, {"ghost", "1.0.0"}, {"ws", "1.0.0"},
	} {
		root := filepath.Join(codex, "plugins", "cache", "market", plugin.name, plugin.version)
		writeWerkSkillFixture(t, filepath.Join(root, ".codex-plugin", "plugin.json"), fmt.Sprintf(`{"name":%q,"skills":"./custom"}`, plugin.name))
		writeWerkSkill(t, filepath.Join(root, "custom", "deploy"), "deploy", "deploy description", plugin.version)
	}
	settings := `{"enabledPlugins":{"claude-tools@market":true,"ws@market":true,"wsflow@market":true,"inactive@market":true}}`
	writeWerkSkillFixture(t, filepath.Join(claude, "settings.json"), settings)
	writeWerkSkillFixture(t, filepath.Join(spec.Root, ".claude", "settings.local.json"), `{"enabledPlugins":{"inactive@market":false}}`)
	registry := map[string][]map[string]string{}
	for _, name := range []string{"claude-tools", "inactive", "ws", "wsflow"} {
		root := filepath.Join(claude, "plugins", "cache", "market", name, "1.0.0")
		registry[name+"@market"] = []map[string]string{{"installPath": root, "scope": "user"}}
		writeWerkSkillFixture(t, filepath.Join(root, ".claude-plugin", "plugin.json"), fmt.Sprintf(`{"name":%q}`, name))
		writeWerkSkill(t, filepath.Join(root, "skills", "deploy"), "deploy", "deploy description", name)
	}
	registry["claude-tools@market"] = append([]map[string]string{{"scope": "project", "projectPath": filepath.Dir(spec.Root), "installPath": profilePlugin}}, registry["claude-tools@market"]...)
	data, err := json.Marshal(map[string]any{"version": 2, "plugins": registry})
	if err != nil {
		t.Fatal(err)
	}
	writeWerkSkillFixture(t, filepath.Join(claude, "plugins", "installed_plugins.json"), string(data))

	worker := werkSkillsByName(discoverWerkSkills(spec))
	if got, want := slices.Sorted(maps.Keys(worker)), []string{"codex-tools:deploy", "profile-plugin:custom", "profile-plugin:default", "ws:deploy"}; !slices.Equal(got, want) {
		t.Errorf("worker skills = %v, want %v", got, want)
	}
	assertWerkSkillBody(t, worker["codex-tools:deploy"], "1.0.10")
	assertWerkSkillBody(t, worker["profile-plugin:custom"], "custom body\n")

	spec.Lead = &LeadSpec{}
	lead := werkSkillsByName(discoverWerkSkills(spec))
	if got, want := slices.Sorted(maps.Keys(lead)), []string{"claude-tools:deploy", "codex-tools:deploy"}; !slices.Equal(got, want) {
		t.Errorf("lead skills = %v, want %v", got, want)
	}
	assertWerkSkillBody(t, lead["claude-tools:deploy"], "claude-tools")
}

func TestWerkDiscoverSkillsLoadsLazilyWithLimits(t *testing.T) {
	spec, _, _, _ := werkSkillFixture(t)
	root := filepath.Join(spec.Root, ".agents", "skills")
	path := writeWerkSkill(t, filepath.Join(root, "lazy"), "lazy", "a lazy skill", strings.Repeat("x", werkSkillBodyLimit+1))
	writeWerkSkill(t, filepath.Join(root, "malformed"), "malformed", strings.Repeat("d", werkSkillFrontmatterLimit), "small body")
	writeWerkSkill(t, filepath.Join(root, "bad-name"), "../escape", "bad name", "small body")
	byName := werkSkillsByName(discoverWerkSkills(spec))
	if got := slices.Sorted(maps.Keys(byName)); !slices.Equal(got, []string{"lazy"}) {
		t.Fatalf("lazy index = %v", got)
	}
	lazy := byName["lazy"]
	if body, err := lazy.Load(); err == nil || body != "" {
		t.Fatal("load accepted an oversized body")
	}
	limitBody := strings.Repeat("x", werk.ToolOutputBytes)
	writeWerkSkill(t, filepath.Dir(path), "lazy", "a lazy skill", limitBody)
	assertWerkSkillBody(t, lazy, limitBody)
	body := "  preserve indentation\n"
	writeWerkSkill(t, filepath.Dir(path), "lazy", "a lazy skill", body)
	assertWerkSkillBody(t, lazy, body)

	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	writeWerkSkill(t, filepath.Dir(path), "lazy", "a replacement", "replacement content\n")
	if body, err := lazy.Load(); err == nil || body != "" {
		t.Fatal("load accepted a replacement file")
	}
}

func TestWerkDiscoverSkillsRejectsSymlinks(t *testing.T) {
	spec, _, codex, _ := werkSkillFixture(t)
	root := filepath.Join(spec.Root, ".agents", "skills")
	outside := t.TempDir()
	outsidePath := writeWerkSkill(t, outside, "outside", "external skill", "must stay outside")
	path := writeWerkSkill(t, filepath.Join(root, "indexed"), "indexed", "an indexed skill", "original body")
	linkWerkSkillFixture(t, outside, filepath.Join(root, "linked-directory"))
	linkWerkSkillFixture(t, outsidePath, filepath.Join(root, "linked-file", "SKILL.md"))
	linkWerkSkillFixture(t, outside, filepath.Join(codex, "skills"))
	byName := werkSkillsByName(discoverWerkSkills(spec))
	if got := slices.Sorted(maps.Keys(byName)); !slices.Equal(got, []string{"indexed"}) {
		t.Fatalf("indexed symlink fixture = %v", got)
	}
	if err := os.Rename(filepath.Dir(path), filepath.Join(root, ".moved")); err != nil {
		t.Fatal(err)
	}
	linkWerkSkillFixture(t, outside, filepath.Dir(path))
	if body, err := byName["indexed"].Load(); err == nil || body != "" {
		t.Fatal("load accepted a symlink substituted for its parent directory")
	}
}

func werkSkillFixture(t *testing.T) (StartSpec, string, string, string) {
	t.Helper()
	base := t.TempDir()
	root, home := filepath.Join(base, "project"), filepath.Join(base, "home")
	codex, claude := filepath.Join(home, "codex-settings"), filepath.Join(home, "claude-settings")
	for _, dir := range []string{root, codex, claude} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return StartSpec{Root: root, Env: []string{"HOME=" + home, "USERPROFILE=" + home, "CODEX_HOME=" + codex, "CLAUDE_CONFIG_DIR=" + claude}}, home, codex, claude
}

func writeWerkSkill(t *testing.T, dir, name, description, body string) string {
	t.Helper()
	path := filepath.Join(dir, "SKILL.md")
	writeWerkSkillFixture(t, path, fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s", name, description, body))
	return path
}

func writeWerkSkillFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func linkWerkSkillFixture(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("creating a symlink: %v", err)
	}
}

func werkSkillsByName(skills []werk.Skill) map[string]werk.Skill {
	byName := make(map[string]werk.Skill, len(skills))
	for _, skill := range skills {
		byName[skill.Name] = skill
	}
	return byName
}

func assertWerkSkillBody(t *testing.T, skill werk.Skill, want string) {
	t.Helper()
	if skill.Load == nil {
		t.Fatal("missing indexed skill")
	}
	body, err := skill.Load()
	if err != nil || body != want {
		t.Errorf("load %q = %q, %v, want %q", skill.Name, body, err, want)
	}
}
