package werk

import (
	"fmt"
	"html"
	"slices"
	"strings"
)

const rereadBytes = 40 << 10

func (a *Agent) compactRereads(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var files map[string]string
	if a.cfg.RereadFiles != nil {
		files = a.cfg.RereadFiles(paths, rereadBytes)
	}
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
		text, ok := files[name]
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
