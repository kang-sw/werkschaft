package wsdashboard

import (
	"html/template"
	"strings"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// renderUserCommandCardHTML renders a parsed <ws:ucmd> block as a compact,
// borderless tonal Material card inside a user bubble.
func renderUserCommandCardHTML(lang string, cmd *wsinbox.UserCommand) template.HTML {
	if cmd == nil {
		return ""
	}
	esc := template.HTMLEscapeString
	if cmd.Add {
		return renderUserCommandAddHTML(lang, cmd)
	}
	label := cmd.Label
	if label == "" {
		label = wsi18n.T(lang, "js.ucmd.ask.header")
	}
	var b strings.Builder
	b.WriteString(`<div class="ucmd-req">`)
	b.WriteString(`<div class="ucmd-req-hd">`)
	b.WriteString(`<span class="ucmd-req-ico" aria-hidden="true"><svg class="li" aria-hidden="true"><use href="#l-square-terminal"/></svg></span>`)
	b.WriteString(`<span class="ucmd-req-label">` + esc(label) + `</span>`)
	if cmd.Edited {
		editedLabel := strings.Trim(strings.TrimSpace(wsi18n.T(lang, "js.ucmd.ask.edited")), "()")
		if editedLabel != "" {
			b.WriteString(`<span class="ucmd-req-edited">` + esc(editedLabel) + `</span>`)
		}
	}
	if cmd.Cwd != "" {
		b.WriteString(`<span class="ucmd-req-cwd" title="` + esc(cmd.Cwd) + `">` + esc(cmd.Cwd) + `</span>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<pre class="ucmd-req-cmd"><code>` + esc(cmd.Cmd) + `</code></pre>`)
	if cmd.Prompt != "" {
		b.WriteString(`<div class="ucmd-req-prompt">` + esc(cmd.Prompt) + `</div>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// renderUserCommandAddHTML renders a <ws:ucmd-add> request as the tonal
// .ucmd-req.is-add bubble: badge, tool pill and the user's request text. The
// agent-facing guidance is not shown.
func renderUserCommandAddHTML(lang string, cmd *wsinbox.UserCommand) template.HTML {
	esc := template.HTMLEscapeString
	var b strings.Builder
	b.WriteString(`<div class="ucmd-req is-add">`)
	b.WriteString(`<div class="ucmd-req-hd">`)
	b.WriteString(`<span class="ucmd-req-ico" aria-hidden="true"><svg class="li" aria-hidden="true"><use href="#l-square-terminal"/></svg></span>`)
	b.WriteString(`<span class="ucmd-req-badge">` + esc(wsi18n.T(lang, "js.ucmd.agent.badge")) + `</span>`)
	b.WriteString(`<code class="ucmd-req-tool-pill">session.register_user_command</code>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="ucmd-req-prompt">` + esc(cmd.Request) + `</div>`)
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// formatUserCommandPreview replaces valid <ws:ucmd> XML blocks in a user
// message with a compact single-line preview (e.g. "[label] cmd" or "cmd") for
// the session rail. It also unwraps <ws:user> envelopes when present so rail
// previews never leak raw XML tags, and strips image attachment lines only
// outside <ws:ucmd> blocks so literal command lines stay intact.
func formatUserCommandPreview(text string) string {
	if !strings.Contains(text, "<ws:ucmd") {
		return withoutImageLines(text)
	}
	formatSegment := func(seg string) string {
		parts := wsinbox.ParseUserCommands(seg)
		var out []string
		for _, p := range parts {
			if p.Command != nil {
				if p.Command.Add {
					out = append(out, strings.Join(strings.Fields(p.Command.Request), " "))
					continue
				}
				cmdLine := strings.Join(strings.Fields(p.Command.Cmd), " ")
				switch {
				case p.Command.Label != "" && cmdLine != "":
					out = append(out, "["+p.Command.Label+"] "+cmdLine)
				case cmdLine != "":
					out = append(out, cmdLine)
				case p.Command.Label != "":
					out = append(out, "["+p.Command.Label+"]")
				}
				continue
			}
			if trimmed := strings.TrimSpace(withoutImageLines(p.Text)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return strings.Join(out, " ")
	}
	if blocks, ok := wsinbox.ParseUserMessages(text); ok && hasUserEnvelope(blocks) {
		var out []string
		for _, b := range blocks {
			if s := formatSegment(b.Text); s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, " ")
	}
	return formatSegment(text)
}
