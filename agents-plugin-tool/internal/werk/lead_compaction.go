package werk

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsfs"
)

const rereadBytes = 40 << 10

func (a *Agent) compactRereads(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	root, err := filepath.EvalSymlinks(a.cfg.Root)
	if err != nil {
		root = ""
	}
	files := wsfs.Files{Root: a.cfg.Root, Cwd: a.cfg.Cwd}
	type candidate struct {
		name, body string
		ok, taken  bool
	}
	all := make([]candidate, 0, len(paths))
	seen := make(map[string]bool)
	for _, name := range paths {
		if seen[name] {
			continue
		}
		seen[name] = true
		path, err := files.ResolvePath(name)
		var text string
		var ok bool
		if err == nil && root != "" {
			text, ok = compactRead(root, path)
		}
		all = append(all, candidate{
			name: Scrub(name, a.cfg.SummarySecrets...),
			body: Scrub(text, a.cfg.SummarySecrets...), ok: ok,
		})
	}
	ordered := make([]*candidate, 0, len(all))
	for i := range all {
		if all[i].ok {
			ordered = append(ordered, &all[i])
		}
	}
	slices.SortStableFunc(ordered, func(a, b *candidate) int { return len(a.body) - len(b.body) })
	left := rereadBytes
	for _, item := range ordered {
		if len(item.body) > left {
			break
		}
		item.taken = true
		left -= len(item.body)
	}
	var inlined, deferred strings.Builder
	for _, item := range all {
		if item.taken {
			// A tag in file text must not terminate the enclosing record.
			body := strings.ReplaceAll(item.body, "</file>", "&lt;/file&gt;")
			fmt.Fprintf(&inlined, "<file path=%q>\n%s\n</file>\n", html.EscapeString(item.name), strings.TrimSuffix(body, "\n"))
		} else {
			fmt.Fprintf(&deferred, "- %s\n", item.name)
		}
	}
	var out strings.Builder
	if inlined.Len() > 0 {
		out.WriteString("\n## Required re-reads (inlined)\n")
		out.WriteString(inlined.String())
	}
	if deferred.Len() > 0 {
		out.WriteString("\n## Required re-reads (to read first)\n")
		out.WriteString(deferred.String())
	}
	return out.String()
}

func compactRead(root, path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > rereadBytes {
		return "", false
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, rereadBytes+1))
	if err != nil || len(raw) > rereadBytes || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", false
	}
	return string(raw), true
}
