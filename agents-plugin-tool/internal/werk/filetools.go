package werk

import (
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

	"github.com/kang-sw/devenv/internal/wsfs"
)

// Files hosts the Phase 1 local tool adapter. Its mutations are implemented
// by the same handlers the daemon uses.
type Files wsfs.Files

// Tool limits.
const (
	// ReadDefaultLines is how many lines a read returns without a limit.
	ReadDefaultLines = 2000
	readMaxLineRunes = 2000
	grepDefaultLimit = 100
	grepMaxFileBytes = 4 << 20
	globLimit        = 500
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
		{Def: editDef, Run: (*wsfs.Files)(f).Edit},
		{Def: writeDef, Run: (*wsfs.Files)(f).Write},
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
	return (*wsfs.Files)(f).ResolvePath(path)
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
	if wsfs.IsBinary(data) {
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
		if err != nil || wsfs.IsBinary(data) {
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
