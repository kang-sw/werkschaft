package wsinbox

import (
	"regexp"
	"strings"
)

const (
	ucmdOpenPrefix = "<ws:ucmd"
	ucmdClose      = "</ws:ucmd>"
	ucmdPreamble   = "User requested execution of the following command."
)

// UserCommand is a structured user-command Ask Agent request carried inline
// inside Entry.Parts as a <ws:ucmd> XML block. It is a transient view of
// existing Parts text, never an additional stored JSON field (T0 compatible).
type UserCommand struct {
	Label  string
	Edited bool
	Cwd    string
	Cmd    string
	Prompt string
}

// UserCommandPart is a lossless segment of message text. Text always holds the
// original bytes; Command is non-nil only for a complete valid <ws:ucmd> block.
// Ordinary text and malformed blocks have Command == nil.
type UserCommandPart struct {
	Text    string
	Command *UserCommand
}

var (
	ucmdAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	ucmdTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	// Single-pass unescaper: decodes &#91; alongside XML entities in one scan so
	// literal "&amp;#91;" stays "&#91;" and never double-decodes.
	ucmdUnescaper = strings.NewReplacer(
		"&#91;", "[",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&apos;", "'",
		"&#39;", "'",
		"&amp;", "&",
	)
	ucmdTagPrefix  = regexp.MustCompile(`^</?ws:ucmd(?:[^A-Za-z0-9_.:-]|$)`)
	ucmdAttrRe     = regexp.MustCompile(`^([a-z]+)="([^"<]*)"`)
	ucmdCmdOpenRe  = regexp.MustCompile(`^<cmd(?: cwd="([^"<]*)")?>`)
	ucmdCmdClose   = "</cmd>"
	ucmdPromptOpen = "<prompt>"
	ucmdPromptEnd  = "</prompt>"
)

func escapeUcmdAttr(s string) string {
	return protectSelectionMarkers(ucmdAttrEscaper.Replace(s))
}

func escapeUcmdText(s string) string {
	return protectSelectionMarkers(ucmdTextEscaper.Replace(s))
}

// FormatUserCommand serializes u into the canonical <ws:ucmd> XML block.
func FormatUserCommand(u UserCommand) string {
	var b strings.Builder
	b.WriteString(ucmdOpenPrefix)
	if u.Label != "" {
		b.WriteString(` label="`)
		b.WriteString(escapeUcmdAttr(u.Label))
		b.WriteByte('"')
	}
	if u.Edited {
		b.WriteString(` edited="true"`)
	}
	b.WriteString(">\n")
	b.WriteString(ucmdPreamble)
	b.WriteString("\n<cmd")
	if u.Cwd != "" {
		b.WriteString(` cwd="`)
		b.WriteString(escapeUcmdAttr(u.Cwd))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	b.WriteString(escapeUcmdText(u.Cmd))
	b.WriteString(ucmdCmdClose)
	if u.Prompt != "" {
		b.WriteString("\n")
		b.WriteString(ucmdPromptOpen)
		b.WriteString(escapeUcmdText(u.Prompt))
		b.WriteString(ucmdPromptEnd)
	}
	b.WriteString("\n")
	b.WriteString(ucmdClose)
	return b.String()
}

// ParseUserCommands splits text at complete, line-delimited <ws:ucmd> blocks,
// leaving surrounding prose intact and falling back to raw text on malformed
// tags. Length-framed <textref> bodies remain opaque.
func ParseUserCommands(text string) []UserCommandPart {
	if text == "" {
		return nil
	}
	if !strings.Contains(text, ucmdOpenPrefix) {
		return []UserCommandPart{{Text: text}}
	}
	var parts []UserCommandPart
	appendPart := func(raw string, cmd *UserCommand) {
		if raw == "" {
			return
		}
		if cmd == nil && len(parts) > 0 && parts[len(parts)-1].Command == nil {
			parts[len(parts)-1].Text += raw
			return
		}
		parts = append(parts, UserCommandPart{Text: raw, Command: cmd})
	}
	walkUserCommands(text, appendPart)
	return parts
}

func walkUserCommands(text string, visit func(raw string, cmd *UserCommand)) {
	if strings.Contains(text, textRefOpenPrefix) {
		for _, part := range ParseTextRefs(text) {
			if part.TextRef != nil {
				visit(part.Text, nil)
				continue
			}
			walkUserCommandsPlain(part.Text, visit)
		}
		return
	}
	walkUserCommandsPlain(text, visit)
}

func ucmdTags(text string) [][2]int {
	var tags [][2]int
	depth := 0
	for pos := 0; pos < len(text); {
		n := strings.IndexByte(text[pos:], '<')
		if n < 0 {
			break
		}
		start := pos + n
		body := text[start:]
		terminator := ""
		switch {
		case strings.HasPrefix(body, "<!--"):
			terminator = "-->"
		case strings.HasPrefix(body, "<![CDATA["):
			terminator = "]]>"
		case strings.HasPrefix(body, "<?"):
			terminator = "?>"
		case strings.HasPrefix(body, "<!"):
			return tags
		}
		if terminator != "" {
			end := strings.Index(body, terminator)
			if end < 0 {
				return tags
			}
			pos = start + end + len(terminator)
			continue
		}
		candidate := ucmdTagPrefix.MatchString(body)
		quote := byte(0)
		end := start + 1
		for ; end < len(text); end++ {
			c := text[end]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
			} else if c == '>' || c == '<' {
				break
			}
		}
		if end == len(text) || text[end] != '>' {
			if candidate {
				tags = append(tags, [2]int{start, len(text)})
				return tags
			}
			if depth > 0 {
				return tags
			}
			pos = start + 1
			continue
		}
		pos = end + 1
		if candidate {
			tags = append(tags, [2]int{start, pos})
			if text[start+1] != '/' {
				depth++
			} else if depth > 0 {
				depth--
			}
		}
	}
	return tags
}

func walkUserCommandsPlain(text string, visit func(raw string, cmd *UserCommand)) {
	tags := ucmdTags(text)
	last := 0
	for i := 0; i < len(tags); i++ {
		start := tags[i][0]
		if text[start+1] == '/' {
			continue
		}
		if start > last {
			visit(text[last:start], nil)
		}
		depth, end := 1, len(text)
		for i++; i < len(tags); i++ {
			if text[tags[i][0]+1] == '/' {
				depth--
			} else {
				depth++
			}
			if depth == 0 {
				end = tags[i][1]
				break
			}
		}
		raw := text[start:end]
		var cmd *UserCommand
		if (start == 0 || text[start-1] == '\n') &&
			(end == len(text) || text[end] == '\n' || strings.HasPrefix(text[end:], "\r\n")) {
			cmd = parseUserCommand(raw)
		}
		visit(raw, cmd)
		last = end
	}
	if last < len(text) {
		visit(text[last:], nil)
	}
}

func parseUserCommand(raw string) *UserCommand {
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(norm, ucmdOpenPrefix) || !strings.HasSuffix(norm, "\n"+ucmdClose) {
		return nil
	}
	firstNL := strings.IndexByte(norm, '\n')
	if firstNL < 0 || norm[firstNL-1] != '>' {
		return nil
	}
	openTag := norm[:firstNL]
	attrStr := openTag[len(ucmdOpenPrefix) : len(openTag)-1]
	u, ok := parseUcmdOpenAttrs(attrStr)
	if !ok {
		return nil
	}
	body := norm[firstNL+1 : len(norm)-len(ucmdClose)-1]
	cmdIdx := strings.Index(body, "<cmd")
	if cmdIdx < 0 {
		return nil
	}
	// Allow optional plain lead-in prose before <cmd> (e.g. ucmdPreamble),
	// but reject stray tags before <cmd>.
	if cmdIdx > 0 {
		lead := body[:cmdIdx]
		if !strings.HasSuffix(lead, "\n") || strings.Contains(lead, "<") {
			return nil
		}
	}
	rest := body[cmdIdx:]
	m := ucmdCmdOpenRe.FindStringSubmatchIndex(rest)
	if m == nil {
		return nil
	}
	if m[2] >= 0 {
		u.Cwd = ucmdUnescaper.Replace(rest[m[2]:m[3]])
	}
	rest = rest[m[1]:]
	cmdEnd := strings.Index(rest, ucmdCmdClose)
	if cmdEnd < 0 || strings.Contains(rest[:cmdEnd], "<") {
		return nil
	}
	u.Cmd = ucmdUnescaper.Replace(rest[:cmdEnd])
	if strings.TrimSpace(u.Cmd) == "" {
		return nil
	}
	tail := rest[cmdEnd+len(ucmdCmdClose):]
	if tail == "" {
		return &u
	}
	if !strings.HasPrefix(tail, "\n"+ucmdPromptOpen) || !strings.HasSuffix(tail, ucmdPromptEnd) {
		return nil
	}
	promptBody := tail[1+len(ucmdPromptOpen) : len(tail)-len(ucmdPromptEnd)]
	if strings.Contains(promptBody, "<") {
		return nil
	}
	u.Prompt = ucmdUnescaper.Replace(promptBody)
	return &u
}

func parseUcmdOpenAttrs(attrStr string) (UserCommand, bool) {
	var u UserCommand
	seenLabel := false
	seenEdited := false
	rest := attrStr
	for rest != "" {
		if rest[0] != ' ' {
			return UserCommand{}, false
		}
		rest = rest[1:]
		m := ucmdAttrRe.FindStringSubmatchIndex(rest)
		if m == nil {
			return UserCommand{}, false
		}
		key := rest[m[2]:m[3]]
		val := rest[m[4]:m[5]]
		switch key {
		case "label":
			if seenLabel {
				return UserCommand{}, false
			}
			seenLabel = true
			u.Label = ucmdUnescaper.Replace(val)
		case "edited":
			if seenEdited || val != "true" {
				return UserCommand{}, false
			}
			seenEdited = true
			u.Edited = true
		default:
			return UserCommand{}, false
		}
		rest = rest[m[1]:]
	}
	return u, true
}
