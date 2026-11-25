package wsdoc

import (
	"os"
	"strings"
)

func frontmatter(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return frontmatterFromText(string(data))
}

// frontmatterFromText is the parser itself, split out from the path wrapper so
// a ticket body that never touched the filesystem — an index blob read through
// ticketScope — parses identically.
func frontmatterFromText(text string) map[string]any {
	if !strings.HasPrefix(text, "---") {
		return nil
	}
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil
	}

	result := map[string]any{}
	current := ""
	for _, line := range lines[1:end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			current = strings.TrimSpace(parts[0])
			value := cleanScalar(parts[1])
			if value == "" || value == "[]" || value == "{}" || value == "null" || value == "~" {
				result[current] = map[string]string{}
			} else {
				result[current] = value
			}
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			list, _ := result[current].([]string)
			result[current] = append(list, item)
			continue
		}
		if strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			m, _ := result[current].(map[string]string)
			if m == nil {
				m = map[string]string{}
			}
			m[strings.TrimSpace(parts[0])] = cleanScalar(parts[1])
			result[current] = m
		}
	}
	return result
}

func cleanScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	// Quoted YAML scalars may contain "#" (e.g. "foo (#1 text ref) #2"); scan
	// to the matching closing quote before looking for a trailing comment.
	if value[0] == '"' {
		var b strings.Builder
		for i := 1; i < len(value); i++ {
			if value[i] == '\\' && i+1 < len(value) {
				next := value[i+1]
				if next == '"' || next == '\\' {
					b.WriteByte(next)
				} else {
					b.WriteByte('\\')
					b.WriteByte(next)
				}
				i++
				continue
			}
			if value[i] == '"' {
				rest := strings.TrimSpace(value[i+1:])
				if rest == "" || strings.HasPrefix(rest, "#") {
					return b.String()
				}
				break
			}
			b.WriteByte(value[i])
		}
	} else if value[0] == '\'' {
		var b strings.Builder
		for i := 1; i < len(value); i++ {
			if value[i] == '\'' {
				if i+1 < len(value) && value[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				rest := strings.TrimSpace(value[i+1:])
				if rest == "" || strings.HasPrefix(rest, "#") {
					return b.String()
				}
				break
			}
			b.WriteByte(value[i])
		}
	}
	if idx := inlineCommentIndex(value); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

func inlineCommentIndex(value string) int {
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return i - 1
		}
	}
	return -1
}
