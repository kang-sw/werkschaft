package werk

import (
	"bytes"
	"encoding/json"
	"errors"
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
const maxRereads = 12

// LeadSummary holds validated carry-forward prose for the next checkpoint.
type LeadSummary struct {
	prose []string
	paths []string
}

// LeadSummarySchema is the optional compact tool argument seen at discovery.
func LeadSummarySchema() map[string]any {
	properties := map[string]any{
		"reread": map[string]any{
			"type": "array", "maxItems": maxRereads, "items": map[string]any{"type": "string"},
			"description": "Text files in this worktree to read again after compaction. The daemon inlines whole files within its byte budget.",
		},
	}
	required := make([]string, 0, len(compactProse))
	for _, field := range compactProse {
		properties[field.key] = map[string]any{"type": "string", "description": field.heading + ". Use an empty string when no details need carrying forward."}
		required = append(required, field.key)
	}
	return map[string]any{
		"type": "object", "properties": properties, "required": required, "additionalProperties": false,
		"description": "A hosted builtin lead compacting itself may supply these seven carry-forward fields and optional re-read paths.",
	}
}

// ParseLeadSummary refuses malformed or excessive input before a turn is queued.
func ParseLeadSummary(raw json.RawMessage) (*LeadSummary, error) {
	var fields map[string]json.RawMessage
	if len(raw) > maxSummaryResponseBytes || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errors.New("lead_summary must be a bounded object")
	}
	out := &LeadSummary{prose: make([]string, len(compactProse))}
	for i, field := range compactProse {
		var text *string
		if json.Unmarshal(fields[field.key], &text) != nil || text == nil || len(*text) > proseFieldBytes {
			return nil, fmt.Errorf("lead_summary.%s requires a string of at most %d bytes", field.key, proseFieldBytes)
		}
		out.prose[i] = strings.TrimSpace(*text)
		delete(fields, field.key)
	}
	if raw, ok := fields["reread"]; ok {
		if json.Unmarshal(raw, &out.paths) != nil || len(out.paths) > maxRereads {
			return nil, fmt.Errorf("lead_summary.reread takes at most %d paths", maxRereads)
		}
		for _, path := range out.paths {
			if strings.TrimSpace(path) == "" || len(path) > 1024 || strings.ContainsAny(path, "\r\n\x00") {
				return nil, errors.New("lead_summary.reread contains an invalid path")
			}
		}
		delete(fields, "reread")
	}
	if len(fields) != 0 {
		return nil, errors.New("lead_summary contains unknown fields")
	}
	return out, nil
}

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
