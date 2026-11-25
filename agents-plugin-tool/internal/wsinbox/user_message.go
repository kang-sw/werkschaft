package wsinbox

import (
	"regexp"
	"strings"
	"time"
)

const userTimeLayout = "2006-01-02T15:04-07:00"

var userBodyDelimiter = regexp.MustCompile(`</ws:user|&(?:amp;)*lt;/ws:user`)
var userOpen = regexp.MustCompile(`^<ws:user at="([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}[+-][0-9]{2}:[0-9]{2})">`)

// Only delimiter-like entities gain a level: escaping every ampersand would
// change unrelated delivered text. Decode each match once, never recursively.
func escapeUserBody(text string) string {
	return userBodyDelimiter.ReplaceAllStringFunc(text, func(s string) string {
		if s[0] == '<' {
			return "&lt;/ws:user"
		}
		return "&amp;" + s[1:]
	})
}

func unescapeUserBody(text string) string {
	return userBodyDelimiter.ReplaceAllStringFunc(text, func(s string) string {
		if strings.HasPrefix(s, "&lt;") {
			return "</ws:user"
		}
		if strings.HasPrefix(s, "&amp;") {
			return "&" + s[len("&amp;"):]
		}
		return s
	})
}

// Text wraps each nonempty dashboard item once, regardless of delivery host.
// Commands remain native command lines; interrupt controls carry no text. A
// system notice of a background exec job (exec.go) is wrapped as <ws:exec>.
func (e Entry) Text() string {
	if e.Command != nil {
		return e.Command.Line()
	}
	if e.Cron != nil {
		return e.cronText()
	}
	if _, ok := e.ExecNotice(); ok {
		return e.execText()
	}
	body := e.bodyText()
	if body == "" {
		return ""
	}
	at := time.UnixMilli(e.SentAt).In(time.Local)
	if e.UTCOffsetMinutes != nil {
		at = at.In(time.FixedZone("", *e.UTCOffsetMinutes*60))
	}
	return `<ws:user at="` + at.Format(userTimeLayout) + `">` + "\n" + escapeUserBody(body) + "\n</ws:user>"
}

// UserMessage is an envelope's decoded content and exact timestamp, or text
// outside envelopes (At empty). Outside text keeps its position in the batch.
type UserMessage struct{ Text, At string }

// envelope is one span of a delivered batch: text outside any envelope
// (kind ""), a <ws:user> block (its body unescaped in text), or a verbatim
// <ws:cron> / <ws:exec> block. raw is the span as delivered.
type envelope struct {
	kind, raw, text, at string
}

const (
	kindUser = "user"
	kindCron = "cron"
	kindExec = "exec"
)

// verbatimOpens maps each verbatim envelope kind to its opening-tag pattern.
func verbatimOpen(kind string) *regexp.Regexp {
	if kind == kindExec {
		return execOpen
	}
	return cronOpen
}

// scanEnvelopes splits a delivered batch into its envelopes before any
// leading-anchored content parser runs. A scheduled prompt and an exec
// completion notice are each one verbatim block: their bodies are escaped,
// so the first closing delimiter ends them and nothing inside is read as an
// envelope. A malformed delimiter or time rejects the whole scan.
func scanEnvelopes(text string) ([]envelope, bool) {
	var out []envelope
	plain := func(s string) {
		if s = strings.Trim(s, "\r\n"); strings.TrimSpace(s) != "" {
			out = append(out, envelope{raw: s, text: s})
		}
	}
	for text != "" {
		start := strings.Index(text, "<ws:user")
		verbatim, vstart := "", -1
		for _, kind := range []string{kindCron, kindExec} {
			if at := strings.Index(text, "<ws:"+kind); at >= 0 && (vstart < 0 || at < vstart) {
				verbatim, vstart = kind, at
			}
		}
		if vstart >= 0 && (start < 0 || vstart < start) {
			if strings.Contains(text[:vstart], "</ws:user") {
				return nil, false
			}
			plain(text[:vstart])
			text = text[vstart:]
			if verbatimOpen(verbatim).FindString(text) == "" {
				return nil, false
			}
			closing := "</ws:" + verbatim
			end := strings.Index(text, closing)
			if end < 0 || !strings.HasPrefix(text[end:], closing+">") {
				return nil, false
			}
			end += len(closing + ">")
			out = append(out, envelope{kind: verbatim, raw: text[:end], text: text[:end]})
			text = text[end:]
			continue
		}
		prefix := text
		if start >= 0 {
			prefix = text[:start]
		}
		if strings.Contains(prefix, "</ws:user") {
			return nil, false
		}
		plain(prefix)
		if start < 0 {
			break
		}
		text = text[start:]
		open := userOpen.FindStringSubmatch(text)
		if open == nil {
			return nil, false
		}
		at, err := time.Parse(userTimeLayout, open[1])
		if err != nil {
			return nil, false
		}
		_, offset := at.Zone()
		if offset < -720*60 || offset > 840*60 {
			return nil, false
		}
		rest := text[len(open[0]):]
		end := strings.Index(rest, "</ws:user")
		if end < 0 || !strings.HasPrefix(rest[end:], "</ws:user>") {
			return nil, false
		}
		body := strings.TrimSuffix(strings.TrimPrefix(rest[:end], "\n"), "\n")
		raw := text[:len(open[0])+end+len("</ws:user>")]
		out = append(out, envelope{kind: kindUser, raw: raw, text: unescapeUserBody(body), at: open[1]})
		text = rest[end+len("</ws:user>"):]
	}
	return out, true
}

// ParseUserMessages splits envelopes before any leading-anchored content
// parser runs. Legacy text is one plain block. A malformed delimiter or time
// rejects the whole parse so callers can display the original turn verbatim.
// A scheduled prompt or an exec notice is one plain block, kept verbatim.
func ParseUserMessages(text string) ([]UserMessage, bool) {
	spans, ok := scanEnvelopes(text)
	if !ok {
		return nil, false
	}
	blocks := make([]UserMessage, len(spans))
	for i, s := range spans {
		blocks[i] = UserMessage{Text: s.text, At: s.at}
	}
	return blocks, true
}
