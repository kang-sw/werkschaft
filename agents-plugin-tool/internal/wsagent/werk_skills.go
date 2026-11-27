package wsagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const (
	werkSkillFrontmatterLimit = 8 << 10
	werkSkillBodyLimit        = werk.ToolOutputBytes
	werkSkillConfigLimit      = 256 << 10
	werkSkillEntryLimit       = 4096
	werkSkillDirEntryLimit    = 512
	werkSkillCountLimit       = 128
	werkSkillListingLimit     = 32 << 10
	werkSkillDepthLimit       = 6
	werkSkillPluginLimit      = 64
)

var errWerkSkillPath = errors.New("the indexed skill is no longer available at its original path")

type werkSkillLocation struct {
	base string
	rel  string
}

func (p werkSkillLocation) join(parts ...string) werkSkillLocation {
	return werkSkillLocation{p.base, filepath.Join(append([]string{p.rel}, parts...)...)}
}

func (p werkSkillLocation) path() string { return filepath.Join(p.base, p.rel) }

type werkSkillRoot struct {
	werkSkillLocation
	namespace string
	claude    bool
}

// discoverWerkSkills captures an inventory for one loop. The loader captures
// an exact file identity, so a name never resolves again against the roots.
func discoverWerkSkills(spec StartSpec) []werk.Skill {
	if spec.Env == nil {
		spec.Env = environ()
	}
	env := claudeWorkerEnv(spec.Env)
	if spec.Lead != nil {
		env = claudeLeadEnv(spec)
	}
	vars := werkSkillEnv(env)
	cwd := werkSkillAnchor(spec.workDir())
	homeKey := "HOME"
	if runtime.GOOS == "windows" {
		homeKey = "USERPROFILE"
	}
	home := vars[homeKey]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	home = werkSkillAnchor(home)
	// Intentionally ambient/default-home only: werk discovers user skills from
	// the process's ambient CODEX_HOME / CLAUDE_CONFIG_DIR rather than external
	// CLI account slots.
	codex := werkSkillConfigDir(vars["CODEX_HOME"], ".codex", home, cwd)
	plugins, bundled := readWerkCodexConfig(codex.join("config.toml"))
	bundledRoot := werkSkillRoot{codex.join("skills", ".system"), "", false}
	if spec.Lead != nil && spec.Lead.PluginIsolation == "isolated" {
		if !bundled {
			return nil
		}
		return indexWerkSkillRoots([]werkSkillRoot{bundledRoot})
	}
	project := werkSkillProjectDirs(cwd, werkSkillAnchor(spec.Root))
	claude := werkSkillConfigDir(vars["CLAUDE_CONFIG_DIR"], ".claude", home, cwd)
	managed := werkClaudeManagedDir(vars)

	var roots []werkSkillRoot
	for _, dir := range project {
		roots = append(roots, werkSkillRoot{werkSkillLocation{dir, filepath.Join(".agents", "skills")}, "", false},
			werkSkillRoot{werkSkillLocation{dir, filepath.Join(".claude", "skills")}, "", true})
	}
	roots = append(roots,
		werkSkillRoot{codex.join("skills"), "", false},
		werkSkillRoot{werkSkillLocation{home, filepath.Join(".agents", "skills")}, "", false},
		werkSkillRoot{claude.join("skills"), "", true})
	if spec.Lead == nil {
		for _, dir := range spec.Profile.PluginPaths(spec.Root) {
			roots = append(roots, werkPluginSkillRoots(werkSkillAbsolute(dir, cwd), "", true)...)
		}
	} else {
		roots = append(roots, werkClaudeInstalledSkills(claude, managed, project)...)
	}
	roots = append(roots, werkCodexInstalledSkills(codex, plugins, spec.Lead != nil)...)
	if runtime.GOOS != "windows" {
		roots = append(roots, werkSkillRoot{werkSkillLocation{werkSkillAnchor("/etc"), filepath.Join("codex", "skills")}, "", false})
	}
	roots = append(roots, werkSkillRoot{managed.join(".claude", "skills"), "", true})
	if bundled {
		roots = append(roots, bundledRoot)
	}
	return indexWerkSkillRoots(roots)
}

func werkSkillEnv(env []string) map[string]string {
	values := map[string]string{}
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(key)
			}
			values[key] = value
		}
	}
	return values
}

func werkSkillAnchor(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	location := werkSkillAbsolute(path, "")
	if _, err := statWerkSkillPath(location, true); err != nil {
		return ""
	}
	return location.path()
}

func werkSkillProjectDirs(cwd, root string) []string {
	if cwd == "" {
		return nil
	}
	var dirs []string
	for dir := cwd; len(dirs) < 32; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if git, err := os.Lstat(filepath.Join(dir, ".git")); err == nil && (git.IsDir() || git.Mode().IsRegular()) {
			return dirs
		}
		if dir == root && root != cwd {
			return dirs
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return dirs[:1]
}

func werkSkillConfigDir(value, fallback, home, cwd string) werkSkillLocation {
	if value == "" {
		return werkSkillLocation{home, fallback}
	}
	if !filepath.IsAbs(value) {
		return werkSkillLocation{cwd, value}
	}
	return werkSkillAbsolute(value, cwd)
}

func werkSkillAbsolute(path, cwd string) werkSkillLocation {
	if !filepath.IsAbs(path) {
		return werkSkillLocation{cwd, path}
	}
	// macOS exposes these system paths as compatibility aliases. The remaining
	// components are checked with Lstat like every other configured path.
	if runtime.GOOS == "darwin" {
		for _, alias := range []string{"/etc", "/tmp", "/var"} {
			if path == alias || strings.HasPrefix(path, alias+"/") {
				path = "/private" + path
				break
			}
		}
	}
	base := filepath.VolumeName(path) + string(filepath.Separator)
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return werkSkillLocation{}
	}
	return werkSkillLocation{base, rel}
}

func werkClaudeManagedDir(env map[string]string) werkSkillLocation {
	switch runtime.GOOS {
	case "darwin":
		return werkSkillAbsolute("/Library/Application Support/ClaudeCode", "")
	case "windows":
		if dir := env["PROGRAMFILES"]; filepath.IsAbs(dir) {
			return werkSkillAbsolute(filepath.Join(dir, "ClaudeCode"), "")
		}
	default:
		return werkSkillAbsolute("/etc/claude-code", "")
	}
	return werkSkillLocation{}
}

// Lstat rejects symlinks and Windows reparse points along the path. Check
// the opened handle against both the checked path and the indexed identity
// before reading any bytes, including on a load long after discovery.
func openWerkSkillPath(path werkSkillLocation, dir bool, indexed os.FileInfo) (*os.File, os.FileInfo, error) {
	before, err := statWerkSkillPath(path, dir)
	if err != nil || indexed != nil && !os.SameFile(indexed, before) {
		return nil, nil, errWerkSkillPath
	}
	// A concurrent replacement with a FIFO must also fail without blocking.
	f, err := os.OpenFile(path.path(), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := f.Stat()
	if err == nil && os.SameFile(before, opened) {
		after, err := statWerkSkillPath(path, dir)
		if err == nil && os.SameFile(opened, after) {
			return f, opened, nil
		}
	}
	f.Close()
	return nil, nil, errWerkSkillPath
}

func statWerkSkillPath(path werkSkillLocation, dir bool) (os.FileInfo, error) {
	if path.base == "" || !filepath.IsLocal(path.rel) || strings.Contains(path.rel, ":") {
		return nil, errWerkSkillPath
	}
	info, err := os.Lstat(path.base)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, errWerkSkillPath
	}
	current := path.base
	parts := strings.Split(filepath.Clean(path.rel), string(filepath.Separator))
	for i, part := range parts {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if err != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return nil, errWerkSkillPath
		}
	}
	if dir && info.IsDir() || !dir && info.Mode().IsRegular() {
		return info, nil
	}
	return nil, errWerkSkillPath
}

func readWerkSkillDir(path werkSkillLocation, limit int) ([]fs.DirEntry, int) {
	f, _, err := openWerkSkillPath(path, true, nil)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	entries, err := f.ReadDir(limit + 1)
	if len(entries) > limit || err != nil && !errors.Is(err, io.EOF) {
		return nil, limit
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, len(entries)
}

func readWerkSkillConfig(path werkSkillLocation) []byte {
	f, _, err := openWerkSkillPath(path, false, nil)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, werkSkillConfigLimit+1))
	if err != nil || len(data) > werkSkillConfigLimit {
		return nil
	}
	return data
}

// Read the enablement tables Codex writes when a local plugin is installed.
func readWerkCodexConfig(path werkSkillLocation) (map[string]bool, bool) {
	plugins, bundled := map[string]bool{}, true
	var table, plugin, multiline string
	for _, line := range strings.Split(string(readWerkSkillConfig(path)), "\n") {
		if multiline != "" {
			if strings.Contains(line, multiline) {
				multiline = ""
			}
			continue
		}
		line = werkSettingValue(line)
		if strings.HasPrefix(line, "[") {
			table, plugin = line, ""
			if name, ok := strings.CutPrefix(table, "[plugins."); ok && strings.HasSuffix(name, "]") {
				plugin = werkScalar(strings.TrimSuffix(name, "]"))
			}
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, `"""`) || strings.HasPrefix(value, `'''`) {
			if !strings.Contains(value[3:], value[:3]) {
				multiline = value[:3]
			}
			continue
		}
		if strings.TrimSpace(key) == "enabled" {
			if table == "[skills.bundled]" {
				bundled = value != "false"
			} else if plugin != "" {
				plugins[plugin] = value == "true"
			}
		}
	}
	return plugins, bundled
}

func werkCodexInstalledSkills(codex werkSkillLocation, enabled map[string]bool, lead bool) []werkSkillRoot {
	var roots []werkSkillRoot
	considered := 0
	for _, id := range slices.Sorted(maps.Keys(enabled)) {
		name, marketplace, ok := strings.Cut(id, "@")
		if !ok || !enabled[id] || !validWerkSkillName(name) || !validWerkSkillName(marketplace) || lead && slices.Contains(hostedPluginPackages, name) {
			continue
		}
		if considered == werkSkillPluginLimit {
			break
		}
		considered++
		base := codex.join("plugins", "cache", marketplace, name)
		versions, _ := readWerkSkillDir(base, werkSkillPluginLimit)
		var best string
		for _, version := range versions {
			value := version.Name()
			if !version.IsDir() || !filepath.IsLocal(value) || strings.HasPrefix(value, ".") {
				continue
			}
			if value == "local" {
				best = value
				break
			}
			if best == "" || compareWerkCodexVersions(value, best) > 0 {
				best = value
			}
		}
		if best != "" {
			roots = append(roots, werkPluginSkillRoots(base.join(best), name, false)...)
		}
	}
	return roots
}

func compareWerkCodexVersions(a, b string) int {
	versionA, _, _ := strings.Cut(a, "+")
	versionB, _, _ := strings.Cut(b, "+")
	coreA, _, preA := strings.Cut(versionA, "-")
	coreB, _, preB := strings.Cut(versionB, "-")
	if coreA == coreB && preA != preB {
		if preA {
			return -1
		}
		return 1
	}
	if order := wsdashboard.CompareVersions(versionA, versionB); order != 0 {
		return order
	}
	return strings.Compare(a, b)
}

func werkClaudeInstalledSkills(claude, managed werkSkillLocation, project []string) []werkSkillRoot {
	settingsPaths := []werkSkillLocation{claude.join("settings.json")}
	if len(project) > 0 {
		cwd, root := project[0], project[len(project)-1]
		settingsPaths = append(settingsPaths, werkSkillLocation{root, filepath.Join(".claude", "settings.json")})
		if cwd != root {
			settingsPaths = append(settingsPaths, werkSkillLocation{cwd, filepath.Join(".claude", "settings.json")})
		}
		settingsPaths = append(settingsPaths, werkSkillLocation{root, filepath.Join(".claude", "settings.local.json")})
		if cwd != root {
			settingsPaths = append(settingsPaths, werkSkillLocation{cwd, filepath.Join(".claude", "settings.local.json")})
		}
	}
	settingsPaths = append(settingsPaths, managed.join("managed-settings.json"))
	enabled := map[string]bool{}
	for _, path := range settingsPaths {
		var settings struct {
			Plugins map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(readWerkSkillConfig(path), &settings) == nil {
			maps.Copy(enabled, settings.Plugins)
		}
	}
	var registry struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(readWerkSkillConfig(claude.join("plugins", "installed_plugins.json")), &registry) != nil {
		return nil
	}
	var roots []werkSkillRoot
	for i, id := range slices.Sorted(maps.Keys(registry.Plugins)) {
		if i == werkSkillPluginLimit {
			break
		}
		name, _, _ := strings.Cut(id, "@")
		if !enabled[id] || !validWerkSkillName(name) || slices.Contains(hostedPluginPackages, name) {
			continue
		}
		for _, row := range registry.Plugins[id] {
			switch row.Scope {
			case "project", "local":
				if !slices.Contains(project, werkSkillAnchor(row.ProjectPath)) {
					continue
				}
			case "user", "managed":
			default:
				continue
			}
			if filepath.IsAbs(row.InstallPath) {
				roots = append(roots, werkPluginSkillRoots(werkSkillAbsolute(row.InstallPath, ""), name, true)...)
				break
			}
		}
	}
	return roots
}

func werkPluginSkillRoots(plugin werkSkillLocation, expected string, claude bool) []werkSkillRoot {
	manifestDir := ".codex-plugin"
	if claude {
		manifestDir = ".claude-plugin"
	}
	var manifest struct {
		Name   string          `json:"name"`
		Skills json.RawMessage `json:"skills"`
	}
	data := readWerkSkillConfig(plugin.join(manifestDir, "plugin.json"))
	if len(data) == 0 && !claude {
		data = readWerkSkillConfig(plugin.join("plugin.json"))
	}
	if len(data) > 0 && json.Unmarshal(data, &manifest) != nil || !claude && len(data) == 0 {
		return nil
	}
	name := manifest.Name
	if name == "" {
		name = expected
		if name == "" {
			name = filepath.Base(plugin.path())
		}
	}
	if !validWerkSkillName(name) || expected != "" && expected != name {
		return nil
	}
	var paths []string
	if claude || len(manifest.Skills) == 0 {
		paths = append(paths, "skills")
	}
	var custom []string
	if json.Unmarshal(manifest.Skills, &custom) != nil {
		var path string
		if json.Unmarshal(manifest.Skills, &path) == nil {
			custom = []string{path}
		}
	}
	paths = append(paths, custom...)
	var roots []werkSkillRoot
	for i, path := range paths {
		if i == werkSkillPluginLimit {
			break
		}
		parts := strings.Split(path, "/")
		if filepath.IsLocal(filepath.FromSlash(path)) && !strings.ContainsAny(path, `\:`) && !slices.Contains(parts, "..") {
			roots = append(roots, werkSkillRoot{plugin.join(filepath.FromSlash(path)), name, claude})
		}
	}
	return roots
}

func indexWerkSkillRoots(roots []werkSkillRoot) []werk.Skill {
	var skills []werk.Skill
	seen := map[string]bool{}
	rootSeen := map[werkSkillRoot]bool{}
	remaining := werkSkillEntryLimit
	listing := werkSkillListingLimit
	var visit func(werkSkillRoot, int)
	visit = func(root werkSkillRoot, depth int) {
		if remaining <= 0 || listing <= 0 || len(skills) >= werkSkillCountLimit || root.base == "" || rootSeen[root] {
			return
		}
		rootSeen[root] = true
		entries, count := readWerkSkillDir(root.werkSkillLocation, min(remaining, werkSkillDirEntryLimit))
		remaining -= count
		for _, entry := range entries {
			if entry.Name() != "SKILL.md" || !entry.Type().IsRegular() {
				continue
			}
			path := root.join(entry.Name())
			meta, indexed, err := indexWerkSkillFile(path, root.claude)
			if err != nil || meta.disabled {
				break
			}
			name := meta.name
			if root.namespace != "" {
				name = root.namespace + ":" + name
			}
			cost := len(name) + len(meta.description) + 5 // Bullet, colon, spaces, and newline.
			if cost <= listing && !seen[name] {
				listing -= cost
				seen[name] = true
				skills = append(skills, werk.Skill{Name: name, Description: meta.description, Load: func() (string, error) {
					return loadWerkSkillFile(path, indexed, root.claude, meta.name)
				}})
			}
			break
		}
		if depth == werkSkillDepthLimit {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				child := root
				child.werkSkillLocation = root.join(entry.Name())
				visit(child, depth+1)
			}
		}
	}
	for _, root := range roots {
		visit(root, 0)
	}
	slices.SortFunc(skills, func(a, b werk.Skill) int { return strings.Compare(a.Name, b.Name) })
	return skills
}

type werkSkillMetadata struct {
	name, description string
	disabled          bool
}

func indexWerkSkillFile(path werkSkillLocation, claude bool) (werkSkillMetadata, os.FileInfo, error) {
	f, info, err := openWerkSkillPath(path, false, nil)
	if err != nil {
		return werkSkillMetadata{}, nil, err
	}
	defer f.Close()
	meta, err := readWerkSkillFrontmatter(bufio.NewReaderSize(io.LimitReader(f, werkSkillFrontmatterLimit), 512), filepath.Base(filepath.Dir(path.path())), claude)
	return meta, info, err
}

func loadWerkSkillFile(path werkSkillLocation, indexed os.FileInfo, claude bool, name string) (string, error) {
	f, _, err := openWerkSkillPath(path, false, indexed)
	if err != nil {
		return "", errWerkSkillPath
	}
	defer f.Close()
	limited := &io.LimitedReader{R: f, N: werkSkillFrontmatterLimit}
	frontmatter := bufio.NewReaderSize(limited, 512)
	meta, err := readWerkSkillFrontmatter(frontmatter, filepath.Base(filepath.Dir(path.path())), claude)
	if err != nil || meta.name != name || meta.disabled {
		return "", errWerkSkillPath
	}
	limited.N = werkSkillBodyLimit + 1
	body, err := io.ReadAll(io.LimitReader(frontmatter, werkSkillBodyLimit+1))
	if err != nil {
		return "", err
	}
	if len(body) > werkSkillBodyLimit {
		return "", errors.New("skill instructions exceed the tool output limit")
	}
	if !utf8.Valid(body) || strings.ContainsRune(string(body), 0) {
		return "", errors.New("skill instructions must be UTF-8 text")
	}
	return string(body), nil
}

func readWerkSkillFrontmatter(r *bufio.Reader, folder string, claude bool) (werkSkillMetadata, error) {
	var meta werkSkillMetadata
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(strings.TrimPrefix(line, "\ufeff")) != "---" {
		return meta, errWerkSkillPath
	}
	blockDescription := false
	for {
		line, err := r.ReadString('\n')
		if strings.TrimRight(line, " \t\r\n") == "---" && err == nil {
			break
		}
		if err != nil {
			return meta, errWerkSkillPath
		}
		if line[0] == ' ' || line[0] == '\t' {
			if blockDescription {
				meta.description += " " + strings.TrimSpace(line)
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		blockDescription = false
		key, value, _ := strings.Cut(line, ":")
		value = werkSettingValue(value)
		switch key {
		case "name":
			meta.name = werkScalar(value)
		case "description":
			blockDescription = strings.HasPrefix(value, ">") || strings.HasPrefix(value, "|")
			meta.description = ""
			if !blockDescription {
				meta.description = werkScalar(value)
			}
		case "disable-model-invocation":
			meta.disabled = claude && value == "true"
		}
	}
	if claude && meta.name == "" {
		meta.name = folder
	}
	meta.description = strings.Join(strings.Fields(meta.description), " ")
	if !validWerkSkillName(meta.name) || !claude && meta.description == "" || len(meta.description) > 2048 || !utf8.ValidString(meta.description) || strings.ContainsRune(meta.description, 0) {
		return werkSkillMetadata{}, errWerkSkillPath
	}
	return meta, nil
}

func validWerkSkillName(value string) bool {
	if len(value) == 0 || len(value) > 64 || value == "." || value == ".." || !filepath.IsLocal(value) {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-_.", char)) {
			return false
		}
	}
	return true
}

func werkScalar(value string) string {
	if len(value) > 1 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	if len(value) > 1 && value[0] == '"' {
		decoded, _ := strconv.Unquote(value)
		return decoded
	}
	return value
}

func werkSettingValue(line string) string {
	var quote rune
	escaped := false
	for i, char := range line {
		switch {
		case escaped:
			escaped = false
		case quote == '"' && char == '\\':
			escaped = true
		case quote != 0:
			if char == quote {
				quote = 0
			}
		case char == '"' || char == '\'':
			// Quotes within an unquoted description, such as an apostrophe,
			// are ordinary text.
			if i == 0 || strings.ContainsRune(" \t[.=", rune(line[i-1])) {
				quote = char
			}
		case char == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
}
