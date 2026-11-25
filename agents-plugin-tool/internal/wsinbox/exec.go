// The exec completion envelope: an inbox entry whose trusted EventID names a
// background exec job's notice, rendered as <ws:exec> beside the <ws:user>
// family, so the model and the dashboard tell it from the user's own text.
package wsinbox

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// ExecEventPrefix starts the EventID of every exec job notice
// ("exec:<id>:exit", "exec:<id>:stopped-by-user"). Only the runner sets an
// EventID (Runner.AdmitCompletion); the dashboard's send routes never copy
// one from a request, so no user text carries it.
const ExecEventPrefix = "exec:"

// ExecNotice reports whether e is an exec job's notice and names the job.
func (e Entry) ExecNotice() (id string, ok bool) {
	rest, found := strings.CutPrefix(e.EventID, ExecEventPrefix)
	if !found || e.Command != nil || e.Cron != nil {
		return "", false
	}
	if cut := strings.LastIndex(rest, ":"); cut >= 0 {
		rest = rest[:cut]
	}
	return rest, rest != ""
}

var execBodyDelimiter = regexp.MustCompile(`</ws:exec|&(?:amp;)*lt;/ws:exec`)

// escapeExecBody mirrors escapeUserBody for the exec closing delimiter, so
// command output cannot close or forge the envelope. A body that quotes a
// user or cron envelope stays inert too: the parser consumes an exec
// envelope whole.
func escapeExecBody(text string) string {
	return execBodyDelimiter.ReplaceAllStringFunc(text, func(s string) string {
		if s[0] == '<' {
			return "&lt;/ws:exec"
		}
		return "&amp;" + s[1:]
	})
}

func unescapeExecBody(text string) string {
	return execBodyDelimiter.ReplaceAllStringFunc(text, func(s string) string {
		if strings.HasPrefix(s, "&lt;") {
			return "</ws:exec"
		}
		if strings.HasPrefix(s, "&amp;") {
			return "&" + s[len("&amp;"):]
		}
		return s
	})
}

// execText is the entry's <ws:exec> envelope; the body is the entry's text.
func (e Entry) execText() string {
	id, _ := e.ExecNotice()
	body := e.bodyText()
	if body == "" {
		return ""
	}
	at := time.UnixMilli(e.SentAt).In(time.Local)
	if e.UTCOffsetMinutes != nil {
		at = at.In(time.FixedZone("", *e.UTCOffsetMinutes*60))
	}
	return `<ws:exec at="` + at.Format(userTimeLayout) + `" id="` + cronAttrEscaper.Replace(id) + `">` + "\n" + escapeExecBody(body) + "\n</ws:exec>"
}

var execOpen = regexp.MustCompile(`^<ws:exec at="[^"\n]*" id="[^"\n]*">`)
var execOpenAttrs = regexp.MustCompile(`^<ws:exec at="([^"\n]*)" id="([^"\n]*)">`)

var execAttrUnescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&amp;", "&")

// ExecBlock is one parsed <ws:exec> envelope: the job id and delivery time
// its attributes carry, and the notice payload unescaped.
type ExecBlock struct{ ID, At, Body string }

// ParseExecBlock reads text, surrounding whitespace aside, as exactly one
// <ws:exec> envelope. Anything else, including an envelope followed by more
// text, is not one.
func ParseExecBlock(text string) (ExecBlock, bool) {
	spans, ok := scanEnvelopes(strings.TrimSpace(text))
	if !ok || len(spans) != 1 || spans[0].kind != kindExec {
		return ExecBlock{}, false
	}
	raw := spans[0].raw
	attrs := execOpenAttrs.FindStringSubmatch(raw)
	if attrs == nil {
		return ExecBlock{}, false
	}
	body := raw[len(attrs[0]) : len(raw)-len("</ws:exec>")]
	body = strings.TrimSuffix(strings.TrimPrefix(body, "\n"), "\n")
	return ExecBlock{ID: execAttrUnescaper.Replace(attrs[2]), At: attrs[1], Body: unescapeExecBody(body)}, true
}

// Segment is one run of a delivered text: one <ws:exec> envelope (Exec), or
// the other text around it.
type Segment struct {
	Text string
	Exec bool
}

// SplitExecNotices splits a delivered text, such as steers joined for one
// submission, into its <ws:exec> envelopes and the text between them, in
// order, each part's text as delivered. The classification is the
// envelope's own: <ws:exec> inside a user or cron block stays inside it. Text
// that does not scan as envelopes is one non-exec segment.
func SplitExecNotices(text string) []Segment {
	spans, ok := scanEnvelopes(text)
	if !ok {
		return []Segment{{Text: text}}
	}
	if !slices.ContainsFunc(spans, func(s envelope) bool { return s.kind == kindExec }) {
		return []Segment{{Text: text}}
	}
	var out []Segment
	var run []string
	flush := func() {
		if len(run) > 0 {
			out = append(out, Segment{Text: strings.Join(run, "\n\n")})
			run = nil
		}
	}
	for _, s := range spans {
		if s.kind == kindExec {
			flush()
			out = append(out, Segment{Text: s.raw, Exec: true})
			continue
		}
		run = append(run, s.raw)
	}
	flush()
	return out
}
