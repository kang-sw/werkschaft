package wsdashboard

import (
	"html/template"
	"strconv"
	"strings"
)

// mailboxContext is the viewing session's directory and git branch, used to
// shorten <mailbox ...> badge labels relative to where the reader sits.
type mailboxContext struct {
	Dir    string
	Branch string
}

// parsedMailbox is one validated <mailbox .../> or <mailbox ...>...</mailbox> tag.
type parsedMailbox struct {
	ID      string
	Dir     string
	Root    string
	Branch  string
	Harness string
	Label   string
}

func (v SessionView) viewingMailboxContext() mailboxContext {
	var branch string
	if v.Git != nil && !v.Git.Detached {
		branch = v.Git.Branch
	}
	return mailboxContext{Dir: v.Project, Branch: branch}
}

func entryHasMailbox(e ConvEntry) bool {
	if strings.Contains(e.Text, "<mailbox ") {
		return true
	}
	for _, p := range e.Content {
		if !p.Plain && strings.Contains(p.Text, "<mailbox ") {
			return true
		}
	}
	for _, b := range e.UserBlocks {
		if entryHasMailbox(b) {
			return true
		}
	}
	if e.TurnSummary != nil {
		for _, g := range e.TurnSummary.Gaps {
			if g.Mail != nil && entryHasMailbox(*g.Mail) {
				return true
			}
		}
	}
	return false
}

// skipTextref skips a valid byte-framed <textref id="#n" bytes="N">...</textref>
// envelope so inline hydration never scans inside its opaque body.
func skipTextref(s string, start int) (int, bool) {
	const prefix = `<textref id="#`
	if !strings.HasPrefix(s[start:], prefix) {
		return 0, false
	}
	p := start + len(prefix)
	idStart := p
	for p < len(s) && s[p] >= '0' && s[p] <= '9' {
		p++
	}
	if p == idStart || s[idStart] == '0' {
		return 0, false
	}
	const mid = `" bytes="`
	if !strings.HasPrefix(s[p:], mid) {
		return 0, false
	}
	p += len(mid)
	bStart := p
	for p < len(s) && s[p] >= '0' && s[p] <= '9' {
		p++
	}
	if p == bStart || !strings.HasPrefix(s[p:], `">`) {
		return 0, false
	}
	n, err := strconv.Atoi(s[bStart:p])
	if err != nil || n < 0 {
		return 0, false
	}
	bodyStart := p + 2
	bodyEnd := bodyStart + n
	const closing = "</textref>"
	if bodyEnd < bodyStart || bodyEnd+len(closing) > len(s) || !strings.HasPrefix(s[bodyEnd:], closing) {
		return 0, false
	}
	return bodyEnd + len(closing), true
}

func isMailboxAttrNameByte(b byte, first bool) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	return !first && ((b >= '0' && b <= '9') || b == '_' || b == '-')
}

func unescapeMailboxXML(s string, allowQuot bool) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '&' {
			switch {
			case strings.HasPrefix(s[i:], "&amp;"):
				b.WriteByte('&')
				i += 5
				continue
			case strings.HasPrefix(s[i:], "&lt;"):
				b.WriteByte('<')
				i += 4
				continue
			case strings.HasPrefix(s[i:], "&gt;"):
				b.WriteByte('>')
				i += 4
				continue
			case allowQuot && strings.HasPrefix(s[i:], "&quot;"):
				b.WriteByte('"')
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// parseMailboxTag parses a strict <mailbox .../> or <mailbox ...>...</mailbox>
// tag starting at start. Malformed, unclosed, or missing-id tags return ok=false.
func parseMailboxTag(s string, start int) (parsedMailbox, int, bool) {
	const prefix = "<mailbox "
	if !strings.HasPrefix(s[start:], prefix) {
		return parsedMailbox{}, 0, false
	}
	p := start + len(prefix)
	var tag parsedMailbox
	seen := make(map[string]bool, 5)
	for p < len(s) {
		for p < len(s) && s[p] == ' ' {
			p++
		}
		if p >= len(s) {
			return parsedMailbox{}, 0, false
		}
		if strings.HasPrefix(s[p:], "/>") {
			if tag.ID == "" {
				return parsedMailbox{}, 0, false
			}
			return tag, p + 2, true
		}
		if s[p] == '>' {
			if tag.ID == "" {
				return parsedMailbox{}, 0, false
			}
			bodyStart := p + 1
			closeRel := strings.Index(s[bodyStart:], "</mailbox>")
			if closeRel < 0 {
				return parsedMailbox{}, 0, false
			}
			rawLabel := s[bodyStart : bodyStart+closeRel]
			if strings.ContainsAny(rawLabel, "<>\r\n") {
				return parsedMailbox{}, 0, false
			}
			tag.Label = unescapeMailboxXML(rawLabel, false)
			return tag, bodyStart + closeRel + len("</mailbox>"), true
		}
		nameStart := p
		if !isMailboxAttrNameByte(s[p], true) {
			return parsedMailbox{}, 0, false
		}
		p++
		for p < len(s) && isMailboxAttrNameByte(s[p], false) {
			p++
		}
		name := s[nameStart:p]
		if seen[name] || p+1 >= len(s) || s[p] != '=' || s[p+1] != '"' {
			return parsedMailbox{}, 0, false
		}
		seen[name] = true
		p += 2
		valStart := p
		for p < len(s) && s[p] != '"' {
			if s[p] == '<' || s[p] == '>' || s[p] == '\r' || s[p] == '\n' {
				return parsedMailbox{}, 0, false
			}
			p++
		}
		if p >= len(s) || s[p] != '"' {
			return parsedMailbox{}, 0, false
		}
		val := unescapeMailboxXML(s[valStart:p], true)
		p++
		switch name {
		case "id":
			tag.ID = val
		case "dir":
			tag.Dir = val
		case "root":
			tag.Root = val
		case "branch":
			tag.Branch = val
		case "harness":
			tag.Harness = val
		}
		// After a closing quote, require space, "/>", or ">".
		if p < len(s) && s[p] != ' ' && s[p] != '>' && !strings.HasPrefix(s[p:], "/>") {
			return parsedMailbox{}, 0, false
		}
	}
	return parsedMailbox{}, 0, false
}

func normalizeMailboxDir(dir string) string {
	s := strings.ReplaceAll(dir, `\`, "/")
	for len(s) > 1 && strings.HasSuffix(s, "/") {
		if len(s) == 3 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1] == ':' {
			break
		}
		s = s[:len(s)-1]
	}
	if len(s) >= 2 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1] == ':' {
		s = strings.ToLower(s)
	}
	return s
}

func mailboxDirBase(dir string) string {
	s := strings.ReplaceAll(dir, `\`, "/")
	for len(s) > 1 && strings.HasSuffix(s, "/") {
		s = s[:len(s)-1]
	}
	if idx := strings.LastIndexByte(s, '/'); idx >= 0 && idx+1 < len(s) {
		return s[idx+1:]
	}
	return s
}

func shortMailboxID(id string) string {
	if rest, ok := strings.CutPrefix(id, "id:"); ok {
		return "id:" + shortID(rest)
	}
	return shortID(id)
}

// formatMailboxLabel computes the dynamic badge label relative to the viewing
// session's dir and branch. Keep in lockstep with WsCapsules.formatMailboxLabel.
func formatMailboxLabel(tag parsedMailbox, ctx mailboxContext) string {
	sameDir := tag.Dir == "" || (ctx.Dir != "" && normalizeMailboxDir(tag.Dir) == normalizeMailboxDir(ctx.Dir))
	sameBranch := tag.Branch == ctx.Branch
	var prefix string
	if !sameDir {
		base := mailboxDirBase(tag.Dir)
		if tag.Branch != "" {
			prefix = base + ":" + tag.Branch
		} else {
			prefix = base
		}
	} else if !sameBranch && tag.Branch != "" {
		prefix = tag.Branch
	}
	if prefix != "" && tag.Label != "" {
		return prefix + " · " + tag.Label
	}
	if prefix != "" {
		return prefix
	}
	if tag.Label != "" {
		return tag.Label
	}
	return shortMailboxID(tag.ID)
}

// formatMailboxTooltip formats the hover tooltip exposing full mailbox metadata.
// Keep in lockstep with WsCapsules.formatMailboxTooltip.
func formatMailboxTooltip(tag parsedMailbox) string {
	lines := []string{"id: " + tag.ID}
	if tag.Dir != "" {
		lines = append(lines, "dir: "+tag.Dir)
	}
	if tag.Root != "" {
		lines = append(lines, "root: "+tag.Root)
	}
	if tag.Branch != "" {
		lines = append(lines, "branch: "+tag.Branch)
	}
	if tag.Harness != "" {
		lines = append(lines, "harness: "+tag.Harness)
	}
	return strings.Join(lines, "\n")
}

func renderMailboxCapsule(tag parsedMailbox, ctx mailboxContext) string {
	esc := template.HTMLEscapeString
	label := formatMailboxLabel(tag, ctx)
	title := formatMailboxTooltip(tag)
	return `<span class="icapsule icapsule-mailbox" title="` + esc(title) + `">` + esc(label) + `</span>`
}
