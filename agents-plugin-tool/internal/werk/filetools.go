package werk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// filetools.go is the harness's own file tools (Decision 5): read, edit
// (exact string replacement), write, grep and glob. Shell is not here: it
// goes through the ws exec tools.
//
// Authority (the worker's permission level) is enforced here, in the tool,
// not requested of the daemon:
//   - read-only refuses edit and write;
//   - workdir-write confines edit and write to the session's worktree root,
//     symbolic links resolved, so a link inside the tree cannot carry a write
//     out of it;
//   - full is unrestricted.
//
// Reads (read, grep, glob) are not confined at any level, as for the other
// harnesses.

// Files serves the file tools for one session.
type Files struct {
	// Root is the session's worktree root, the confinement of workdir-write.
	Root string
	// Cwd is the directory a relative path resolves against.
	Cwd       string
	Authority Authority
}

// Tool limits.
const (
	// ReadDefaultLines is how many lines a read returns without a limit.
	ReadDefaultLines = 2000
	readMaxLineRunes = 2000
	grepDefaultLimit = 100
	grepMaxFileBytes = 4 << 20
	globLimit        = 500
	diffContext      = 3
	// diffMaxCells bounds the line diff's table; past it the changed region
	// is shown as one replacement.
	diffMaxCells = 4_000_000
)

// Tool is a local tool: its definition, whether it only reads, and its run
// function. A run's error is the tool's failure text, which the model sees.
type Tool struct {
	Def      ToolDef
	ReadOnly bool
	Run      func(ctx context.Context, args json.RawMessage) (string, error)
}

// Tools returns the file tools, in the order the model is offered them.
// allow, when not empty, lists the tool names the session's profile offers.
func (f *Files) Tools(allow []string) []Tool {
	all := []Tool{
		{Def: readDef, ReadOnly: true, Run: f.read},
		{Def: editDef, Run: f.edit},
		{Def: writeDef, Run: f.write},
		{Def: grepDef, ReadOnly: true, Run: f.grep},
		{Def: globDef, ReadOnly: true, Run: f.glob},
	}
	if len(allow) == 0 {
		return all
	}
	var out []Tool
	for _, t := range all {
		for _, name := range allow {
			if strings.EqualFold(strings.TrimSpace(name), t.Def.Name) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

var readDef = ToolDef{
	Name:        "read",
	Description: "Read a text file. Returns numbered lines. Use offset (1-based line) and limit to page through a large file.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path, absolute or relative to the working directory."},` +
		`"offset":{"type":"integer","description":"First line to return, 1-based."},` +
		`"limit":{"type":"integer","description":"Maximum number of lines to return."}},` +
		`"required":["path"]}`),
}

var editDef = ToolDef{
	Name: "edit",
	Description: "Edit a file by exact string replacement. Each edit's oldText must match exactly one place in the original file and the edits must not overlap; " +
		"all edits of one call are matched against the file as it was before the call. Returns a unified diff of the change.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path, absolute or relative to the working directory."},` +
		`"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{` +
		`"oldText":{"type":"string","description":"Exact text to replace; must occur exactly once."},` +
		`"newText":{"type":"string","description":"Replacement text."}},"required":["oldText","newText"]}}},` +
		`"required":["path","edits"]}`),
}

var writeDef = ToolDef{
	Name:        "write",
	Description: "Write a file with the given content, creating it and any missing parent directories, or replacing it whole.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path, absolute or relative to the working directory."},` +
		`"content":{"type":"string","description":"The file's whole new content."}},` +
		`"required":["path","content"]}`),
}

var grepDef = ToolDef{
	Name:        "grep",
	Description: "Search file contents with a regular expression (Go syntax). Returns path:line:text matches. Skips .git and binary files.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"pattern":{"type":"string","description":"Regular expression."},` +
		`"path":{"type":"string","description":"File or directory to search; defaults to the working directory."},` +
		`"glob":{"type":"string","description":"Only search files whose path matches this glob, such as **/*.go."},` +
		`"ignoreCase":{"type":"boolean"},` +
		`"limit":{"type":"integer","description":"Maximum matches to return."}},` +
		`"required":["pattern"]}`),
}

var globDef = ToolDef{
	Name:        "glob",
	Description: "List files whose path matches a glob (** crosses directories, * does not). Skips .git.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"pattern":{"type":"string","description":"Glob such as src/**/*.go."},` +
		`"path":{"type":"string","description":"Directory the pattern is relative to; defaults to the working directory."}},` +
		`"required":["pattern"]}`),
}

// abs resolves path against Cwd.
func (f *Files) abs(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(path) {
		base := f.Cwd
		if base == "" {
			base = f.Root
		}
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

// realPath resolves symbolic links of the deepest existing ancestor of p and
// appends the rest, so a path that does not exist yet is judged where it
// would land. A symbolic link whose target is missing is followed to where it
// points: a write through it creates the target, so that is where it lands.
func realPath(p string) (string, error) {
	return realPathDepth(p, 0)
}

// maxLinkHops bounds the dangling links one path may chain through.
const maxLinkHops = 40

func realPathDepth(p string, hops int) (string, error) {
	if hops > maxLinkHops {
		return "", errors.New("too many levels of symbolic links")
	}
	p = filepath.Clean(p)
	var rest []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err != nil && errors.Is(err, fs.ErrNotExist) {
			// EvalSymlinks reports a link with a missing target as a missing
			// path; the link itself is there, and a write follows it.
			if info, lerr := os.Lstat(cur); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
				target, rerr := os.Readlink(cur)
				if rerr != nil {
					return "", rerr
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(cur), target)
				}
				real, err = realPathDepth(target, hops+1)
			}
		}
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// writable resolves path for a write and enforces the authority.
func (f *Files) writable(path string) (string, error) {
	switch f.Authority {
	case AuthorityReadOnly:
		return "", errors.New("this session is read-only: it may not edit or write files")
	case AuthorityWorkdirWrite:
	case AuthorityFull:
		return f.abs(path)
	default:
		return "", fmt.Errorf("this session has no write authority (%q)", string(f.Authority))
	}
	p, err := f.abs(path)
	if err != nil {
		return "", err
	}
	root := f.Root
	if root == "" {
		return "", errors.New("workdir-write needs the session's worktree root, and none is set")
	}
	realRoot, err := realPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve the worktree root: %v", err)
	}
	realTarget, err := realPath(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %v", path, err)
	}
	if !within(realRoot, realTarget) {
		return "", fmt.Errorf("%s is outside the session's worktree; workdir-write may write inside it only", path)
	}
	return p, nil
}

// within reports whether target is root or lies under it.
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func isBinary(b []byte) bool {
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

func (f *Files) read(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("read arguments: %v", err)
	}
	p, err := f.abs(a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", a.Path)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s is a binary file (%d bytes)", a.Path, len(data))
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	start := 1
	if a.Offset > 1 {
		start = a.Offset
	}
	limit := a.Limit
	if limit <= 0 {
		limit = ReadDefaultLines
	}
	if start > len(lines) {
		if len(lines) == 0 {
			return "(the file is empty)", nil
		}
		return "", fmt.Errorf("offset %d is past the end of the file (%d lines)", start, len(lines))
	}
	end := min(start-1+limit, len(lines))
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		line := lines[i]
		if r := []rune(line); len(r) > readMaxLineRunes {
			line = string(r[:readMaxLineRunes]) + "…"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "(showing lines %d-%d of %d; pass offset %d to continue)\n", start, end, len(lines), end+1)
	}
	return b.String(), nil
}

func (f *Files) edit(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path  string `json:"path"`
		Edits []struct {
			Old string `json:"oldText"`
			New string `json:"newText"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("edit arguments: %v", err)
	}
	p, err := f.writable(a.Path)
	if err != nil {
		return "", err
	}
	if len(a.Edits) == 0 {
		return "", errors.New("edits is empty")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s is a binary file", a.Path)
	}
	orig := string(data)
	crlf := strings.Contains(orig, "\r\n")
	text := orig
	if crlf {
		text = strings.ReplaceAll(orig, "\r\n", "\n")
	}
	type span struct{ start, end int }
	spans := make([]span, len(a.Edits))
	for i, e := range a.Edits {
		old := e.Old
		if crlf {
			old = strings.ReplaceAll(old, "\r\n", "\n")
		}
		if old == "" {
			return "", fmt.Errorf("edit %d: oldText is empty", i+1)
		}
		switch n := strings.Count(text, old); {
		case n == 0:
			return "", fmt.Errorf("edit %d: oldText was not found in %s; it must match the file exactly, whitespace included", i+1, a.Path)
		case n > 1:
			return "", fmt.Errorf("edit %d: oldText matches %d places in %s; add surrounding text so it matches exactly one", i+1, n, a.Path)
		}
		at := strings.Index(text, old)
		spans[i] = span{at, at + len(old)}
	}
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(x, y int) bool { return spans[order[x]].start < spans[order[y]].start })
	for k := 1; k < len(order); k++ {
		if spans[order[k]].start < spans[order[k-1]].end {
			return "", fmt.Errorf("edits %d and %d overlap; merge them into one edit", order[k-1]+1, order[k]+1)
		}
	}
	var out strings.Builder
	last := 0
	for _, i := range order {
		out.WriteString(text[last:spans[i].start])
		new := a.Edits[i].New
		if crlf {
			new = strings.ReplaceAll(new, "\r\n", "\n")
		}
		out.WriteString(new)
		last = spans[i].end
	}
	out.WriteString(text[last:])
	result := out.String()
	diff := unifiedDiff(a.Path, text, result)
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	if err := writeKeepingMode(p, []byte(result)); err != nil {
		return "", err
	}
	if diff == "" {
		return fmt.Sprintf("edited %s (no change)", a.Path), nil
	}
	return diff, nil
}

func (f *Files) write(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("write arguments: %v", err)
	}
	p, err := f.writable(a.Path)
	if err != nil {
		return "", err
	}
	verb := "created"
	if _, err := os.Stat(p); err == nil {
		verb = "overwrote"
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := writeKeepingMode(p, []byte(a.Content)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s (%d bytes)", verb, a.Path, len(a.Content)), nil
}

// writeKeepingMode writes data to p, keeping an existing file's permission.
func writeKeepingMode(p string, data []byte) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", p)
		}
		mode = info.Mode().Perm()
	}
	return os.WriteFile(p, data, mode)
}

func (f *Files) grep(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignoreCase"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("grep arguments: %v", err)
	}
	pat := a.Pattern
	if a.IgnoreCase {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return "", fmt.Errorf("pattern: %v", err)
	}
	var globRe *regexp.Regexp
	if a.Glob != "" {
		if globRe, err = globRegexp(a.Glob); err != nil {
			return "", err
		}
	}
	base := f.Cwd
	if base == "" {
		base = f.Root
	}
	start := base
	if a.Path != "" {
		if start, err = f.abs(a.Path); err != nil {
			return "", err
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = grepDefaultLimit
	}
	var b strings.Builder
	count, truncated := 0, false
	searchFile := func(path, show string) {
		if truncated {
			return
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > grepMaxFileBytes {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return
		}
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if !re.MatchString(line) {
				continue
			}
			if count >= limit {
				truncated = true
				return
			}
			count++
			if r := []rune(line); len(r) > 300 {
				line = string(r[:300]) + "…"
			}
			fmt.Fprintf(&b, "%s:%d:%s\n", show, i+1, line)
		}
	}
	info, err := os.Stat(start)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		searchFile(start, filepath.ToSlash(a.Path))
	} else {
		walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(start, p)
			rel = filepath.ToSlash(rel)
			if globRe != nil && !globRe.MatchString(rel) {
				return nil
			}
			searchFile(p, rel)
			if truncated {
				return fs.SkipAll
			}
			return nil
		})
		if walkErr != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if count == 0 {
		return "no matches", nil
	}
	if truncated {
		fmt.Fprintf(&b, "(stopped at %d matches; narrow the pattern or path)\n", limit)
	}
	return b.String(), nil
}

func (f *Files) glob(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("glob arguments: %v", err)
	}
	re, err := globRegexp(a.Pattern)
	if err != nil {
		return "", err
	}
	start := f.Cwd
	if start == "" {
		start = f.Root
	}
	if a.Path != "" {
		if start, err = f.abs(a.Path); err != nil {
			return "", err
		}
	}
	var hits []string
	truncated := false
	walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(start, p)
		rel = filepath.ToSlash(rel)
		if re.MatchString(rel) {
			if len(hits) >= globLimit {
				truncated = true
				return fs.SkipAll
			}
			hits = append(hits, rel)
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(hits) == 0 {
		return "no files match", nil
	}
	sort.Strings(hits)
	out := strings.Join(hits, "\n") + "\n"
	if truncated {
		out += fmt.Sprintf("(stopped at %d files; narrow the pattern)\n", globLimit)
	}
	return out, nil
}

// globRegexp compiles a glob: ** crosses directories, * and ? do not, and
// {a,b} alternates.
func globRegexp(glob string) (*regexp.Regexp, error) {
	glob = strings.TrimSpace(filepath.ToSlash(glob))
	if glob == "" {
		return nil, errors.New("pattern is required")
	}
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth > 0 {
				depth--
				b.WriteString(")")
			} else {
				b.WriteString(`\}`)
			}
		case ',':
			if depth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if depth != 0 {
		return nil, errors.New("pattern has an unclosed {")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// unifiedDiff is a unified diff of a to b with diffContext lines of context;
// "" when they are equal. It is a line diff: the common head and tail are cut
// away and the middle is diffed by longest common subsequence.
func unifiedDiff(path, a, b string) string {
	if a == b {
		return ""
	}
	al, bl := splitLines(a), splitLines(b)
	pre := 0
	for pre < len(al) && pre < len(bl) && al[pre] == bl[pre] {
		pre++
	}
	suf := 0
	for suf < len(al)-pre && suf < len(bl)-pre && al[len(al)-1-suf] == bl[len(bl)-1-suf] {
		suf++
	}
	am, bm := al[pre:len(al)-suf], bl[pre:len(bl)-suf]
	type op struct {
		kind byte // ' ', '-', '+'
		text string
	}
	var ops []op
	for _, l := range al[:pre] {
		ops = append(ops, op{' ', l})
	}
	if len(am)*len(bm) > diffMaxCells {
		for _, l := range am {
			ops = append(ops, op{'-', l})
		}
		for _, l := range bm {
			ops = append(ops, op{'+', l})
		}
	} else {
		n, m := len(am), len(bm)
		lcs := make([][]int32, n+1)
		for i := range lcs {
			lcs[i] = make([]int32, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if am[i] == bm[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else {
					lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n && j < m {
			switch {
			case am[i] == bm[j]:
				ops = append(ops, op{' ', am[i]})
				i++
				j++
			case lcs[i+1][j] >= lcs[i][j+1]:
				ops = append(ops, op{'-', am[i]})
				i++
			default:
				ops = append(ops, op{'+', bm[j]})
				j++
			}
		}
		for ; i < n; i++ {
			ops = append(ops, op{'-', am[i]})
		}
		for ; j < m; j++ {
			ops = append(ops, op{'+', bm[j]})
		}
	}
	for _, l := range al[len(al)-suf:] {
		ops = append(ops, op{' ', l})
	}
	// Hunks: runs of changes with diffContext lines around them.
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", path, path)
	i := 0
	aLine, bLine := 1, 1
	// aAt and bAt are the line numbers of ops[k]'s position.
	aAt := make([]int, len(ops)+1)
	bAt := make([]int, len(ops)+1)
	for k, o := range ops {
		aAt[k], bAt[k] = aLine, bLine
		if o.kind != '+' {
			aLine++
		}
		if o.kind != '-' {
			bLine++
		}
	}
	aAt[len(ops)], bAt[len(ops)] = aLine, bLine
	for i < len(ops) {
		for i < len(ops) && ops[i].kind == ' ' {
			i++
		}
		if i >= len(ops) {
			break
		}
		from := max(i-diffContext, 0)
		to := i
		for {
			for to < len(ops) && ops[to].kind != ' ' {
				to++
			}
			gap := to
			for gap < len(ops) && ops[gap].kind == ' ' {
				gap++
			}
			if gap < len(ops) && gap-to <= 2*diffContext {
				to = gap
				continue
			}
			to = min(to+diffContext, len(ops))
			break
		}
		aCount, bCount := 0, 0
		for _, o := range ops[from:to] {
			if o.kind != '+' {
				aCount++
			}
			if o.kind != '-' {
				bCount++
			}
		}
		aStart, bStart := aAt[from], bAt[from]
		if aCount == 0 {
			aStart--
		}
		if bCount == 0 {
			bStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		for _, o := range ops[from:to] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		i = to
	}
	return out.String()
}

// splitLines splits text into lines without their terminators; text that ends
// in a newline has no empty last line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
