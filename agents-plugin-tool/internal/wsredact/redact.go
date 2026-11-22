// Package wsredact masks credentials in short display text before a browser
// or an agent sees it. Every rule replaces the secret part of a match with
// Mask and leaves the surrounding text, so a reader can still tell what was
// there.
//
// It is best-effort, not a guarantee: the rules catch well-known token shapes
// and secret-named values, and a secret in any other shape passes through.
// A false positive only hides a harmless value, so the rules lean toward
// masking where a shape is distinctive.
package wsredact

import (
	"regexp"
	"strings"
)

// Mask replaces each secret part.
const Mask = "***"

// urlUserinfo matches the userinfo of an http(s) URL, where a token or
// password sits; an ssh user (git@host) carries none. The userinfo runs to
// the last '@' before the host's path, since a password may hold an
// unencoded '@' (user:p@ss@host).
var urlUserinfo = regexp.MustCompile(`(?i)(https?://)[^/\s]*@`)

// urlQuery matches the query string of an http(s) URL, where a token can sit
// too (private_token=, access_token=): group 1 is everything up to the "?",
// group 2 the query, which ends at a fragment, whitespace, or quote.
var urlQuery = regexp.MustCompile(`(?i)(https?://[^\s?#'"]*\?)([^\s#'"]*)`)

// Rule names reported by Redact for the URL rules.
const (
	RuleURLUserinfo = "url_userinfo"
	RuleURLQuery    = "url_query"
)

// URLCredentials hides the userinfo and every query value of each http(s)
// URL in s. Query names stay.
func URLCredentials(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}"+Mask+"@")
	return urlQuery.ReplaceAllStringFunc(s, func(url string) string {
		m := urlQuery.FindStringSubmatch(url)
		params := strings.Split(m[2], "&")
		for i, p := range params {
			if name, _, ok := strings.Cut(p, "="); ok {
				params[i] = name + "=" + Mask
			} else if p != "" {
				params[i] = Mask
			}
		}
		return m[1] + strings.Join(params, "&")
	})
}

// secretKey is a secret-named key: one of the names, case-insensitive,
// optionally after word_ or word- prefixes (db_password, GITHUB_TOKEN,
// aws_secret_access_key). A longer word that only starts with a name
// (tokenizer, tokens, authorization) is not one.
const secretKey = `(?:[A-Za-z0-9]+[_-])*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret|auth)`

// secretValue is the value after a secret key: the inside of a double- or
// single-quoted string (to its closing quote, or to the end when cut), or a
// bare run up to whitespace or a delimiter.
const secretValue = `(?:"((?:[^"\\]|\\.)+)|'([^']+)|([^\s"',;&]+))`

// rule is one named masking pattern. A pattern with capture groups masks each
// group that matched; one without masks its whole match.
type rule struct {
	name string
	re   *regexp.Regexp
}

// rules apply in order.
var rules = []rule{
	// Well-known token shapes, a subset of gitleaks' (MIT) rules. Minimum
	// lengths are loose where a prefix is distinctive, so a token an
	// adapter's preview cap cut short is still masked.
	{"aws_access_key_id", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{12,}\b`)},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{8,}`)},
	{"github_fine_grained_token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{8,}`)},
	{"gitlab_token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{8,}`)},
	{"slack_token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{8,}`)},
	{"anthropic_key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`)},
	{"openai_key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}`)},
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{16,}`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}(?:\.[A-Za-z0-9_-]*){0,2}`)}, // or its cut head
	// A PEM private key block: its body to the END line, or to the end of
	// the text when the preview cut it.
	{"private_key_block", regexp.MustCompile(`(?s)-----BEGIN[A-Z0-9 ]*PRIVATE KEY-----(.+?)(?:-----END[A-Z0-9 ]*PRIVATE KEY-----|$)`)},
	// An Authorization header's credentials.
	{"authorization_header", regexp.MustCompile(`(?i)\bauthorization["']?\s*[:=]\s*["']?(?:bearer|basic|token)\s+([^\s"',;]+)`)},
	// key=value, key: value, "key": "value".
	{"secret_assignment", regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.-])["']?` + secretKey + `["']?\s*[:=]\s*` + secretValue)},
	// --flag value, --flag=value; a bare value starting with "-" is the next
	// flag, not a value.
	{"secret_flag", regexp.MustCompile(`(?i)(?:^|\s)--` + secretKey + `(?:=|\s+)(?:"((?:[^"\\]|\\.)+)|'([^']+)|([^\s"'-][^\s"']*))`)},
}

// Secrets masks every credential the rules recognize in s: URL userinfo and
// query values, well-known token shapes, PEM private key bodies,
// Authorization credentials, and the values of secret-named keys and flags.
func Secrets(s string) string {
	out, _ := Redact(s)
	return out
}

// Match reports one Mask the rules wrote into Redact's output.
type Match struct {
	// Offset is the byte offset of the Mask in the redacted output.
	Offset int
	// OrigOffset is the byte offset in the input where the masked text began.
	OrigOffset int
	// Length is the byte length of the input text the Mask replaced. It is 0
	// when a rule masked an empty value (a bare "token=" query parameter); the
	// Mask is still written so the output equals Secrets.
	Length int
	// Rule names the rule that wrote the Mask. When a later rule's match
	// swallowed an earlier Mask, the later rule is named and Length covers
	// both.
	Rule string
}

// Redact masks s exactly as Secrets does and reports every Mask it wrote, in
// output order, with the rule name and the original length. It never
// returns the masked text itself.
func Redact(s string) (string, []Match) {
	r := redaction{pieces: []piece{{text: s, origLen: len(s)}}}
	if s == "" {
		r.pieces = nil
	}
	r.apply(RuleURLUserinfo, userinfoSpans)
	r.apply(RuleURLQuery, querySpans)
	for _, ru := range rules {
		re := ru.re
		r.apply(ru.name, func(cur string) [][2]int { return groupSpans(re, cur) })
	}
	return r.result()
}

// userinfoSpans is the span URLCredentials' userinfo rule replaces: between
// the scheme and the last '@'.
func userinfoSpans(s string) [][2]int {
	var spans [][2]int
	for _, m := range urlUserinfo.FindAllStringSubmatchIndex(s, -1) {
		spans = append(spans, [2]int{m[3], m[1] - 1})
	}
	return spans
}

// querySpans are the spans URLCredentials' query rule replaces: every value
// after "=", or a whole parameter without one.
func querySpans(s string) [][2]int {
	var spans [][2]int
	for _, m := range urlQuery.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[4], m[5]
		pos := start
		for pos <= end {
			next := strings.IndexByte(s[pos:end], '&')
			paramEnd := end
			if next >= 0 {
				paramEnd = pos + next
			}
			param := s[pos:paramEnd]
			if eq := strings.IndexByte(param, '='); eq >= 0 {
				spans = append(spans, [2]int{pos + eq + 1, paramEnd})
			} else if param != "" {
				spans = append(spans, [2]int{pos, paramEnd})
			}
			if next < 0 {
				break
			}
			pos = paramEnd + 1
		}
	}
	return spans
}

// groupSpans are the spans of each capture group that matched, or each whole
// match when re has no groups.
func groupSpans(re *regexp.Regexp, s string) [][2]int {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	var spans [][2]int
	for _, m := range matches {
		if len(m) == 2 {
			spans = append(spans, [2]int{m[0], m[1]})
			continue
		}
		for g := 2; g < len(m); g += 2 {
			if m[g] >= 0 && m[g+1] > m[g] {
				spans = append(spans, [2]int{m[g], m[g+1]})
			}
		}
	}
	return spans
}

// piece is a run of the current text. A literal piece copies input bytes
// starting at orig (origLen == len(text)); a synthetic literal is the
// leftover of a Mask a later span cut through (origLen 0); a mask piece is a
// Mask standing for origLen input bytes starting at orig.
type piece struct {
	text      string
	orig      int
	origLen   int
	mask      bool
	synthetic bool
	rule      string
}

type redaction struct {
	pieces []piece
}

func (r *redaction) text() string {
	if len(r.pieces) == 1 {
		return r.pieces[0].text
	}
	var b strings.Builder
	for _, p := range r.pieces {
		b.WriteString(p.text)
	}
	return b.String()
}

// apply replaces each span find reports in the current text with one mask
// piece. Spans are ordered and disjoint.
func (r *redaction) apply(name string, find func(string) [][2]int) {
	if len(r.pieces) == 0 {
		return
	}
	spans := find(r.text())
	if len(spans) == 0 {
		return
	}
	out := make([]piece, 0, len(r.pieces)+2*len(spans))
	si := 0
	var cur *piece
	counted := false // the mask piece being cut already gave its origLen
	pos := 0
	emitZeroAt := func(abs, origAt int) {
		for cur == nil && si < len(spans) && spans[si][0] == abs && spans[si][1] == abs {
			out = append(out, piece{text: Mask, orig: origAt, mask: true, rule: name})
			si++
		}
	}
	for _, p := range r.pieces {
		start, end := pos, pos+len(p.text)
		counted = false
		k := 0
		for {
			abs := start + k
			emitZeroAt(abs, p.origAt(k))
			if k >= len(p.text) {
				break
			}
			if cur != nil {
				n := min(end, spans[si][1]) - abs
				part := p.part(k, k+n)
				if p.mask {
					if !counted {
						cur.origLen += p.origLen
						counted = true
					}
				} else {
					cur.origLen += part.origLen
				}
				k += n
				if abs+n == spans[si][1] {
					out = append(out, *cur)
					cur = nil
					si++
				}
				continue
			}
			if si < len(spans) && spans[si][0] == abs {
				cur = &piece{text: Mask, orig: p.origAt(k), mask: true, rule: name}
				continue
			}
			next := end
			if si < len(spans) && spans[si][0] < next {
				next = spans[si][0]
			}
			n := next - abs
			if k == 0 && n == len(p.text) {
				out = append(out, p)
			} else {
				out = append(out, p.part(k, k+n))
			}
			k += n
		}
		pos = end
	}
	last := piece{}
	if n := len(r.pieces); n > 0 {
		last = r.pieces[n-1]
	}
	emitZeroAt(pos, last.origAt(len(last.text)))
	if cur != nil {
		out = append(out, *cur)
	}
	r.pieces = out
}

// origAt is the input offset standing for byte k of p.
func (p piece) origAt(k int) int {
	switch {
	case p.mask:
		if k >= len(p.text) {
			return p.orig + p.origLen
		}
		return p.orig
	case p.synthetic:
		return p.orig
	default:
		return p.orig + k
	}
}

// part is bytes [i, j) of p as its own piece. Part of a mask piece becomes a
// synthetic literal: the Mask bytes stay in the text while the whole original
// goes to whichever span covers the rest.
func (p piece) part(i, j int) piece {
	switch {
	case p.mask:
		at := p.orig
		if i > 0 {
			at = p.orig + p.origLen
		}
		return piece{text: p.text[i:j], orig: at, synthetic: true}
	case p.synthetic:
		return piece{text: p.text[i:j], orig: p.orig, synthetic: true}
	default:
		return piece{text: p.text[i:j], orig: p.orig + i, origLen: j - i}
	}
}

func (r *redaction) result() (string, []Match) {
	var matches []Match
	pos := 0
	for _, p := range r.pieces {
		if p.mask {
			matches = append(matches, Match{Offset: pos, OrigOffset: p.orig, Length: p.origLen, Rule: p.rule})
		}
		pos += len(p.text)
	}
	return r.text(), matches
}

// MapOffset maps a byte offset in Redact's input to the redacted output,
// given the matches Redact reported. An offset inside a masked span maps to
// the end of its Mask: the reader saw part of what the Mask stands for.
func MapOffset(matches []Match, orig int) int {
	outEnd, origEnd := 0, 0
	for _, m := range matches {
		if orig <= m.OrigOffset {
			break
		}
		if orig < m.OrigOffset+m.Length {
			return m.Offset + len(Mask)
		}
		outEnd, origEnd = m.Offset+len(Mask), m.OrigOffset+m.Length
	}
	return orig + outEnd - origEnd
}
