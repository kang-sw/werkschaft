// audit_query.go holds the audit query API's selection, paging, summary,
// search, and byte-range logic over one session's retained audit events
// (wsauditstore.View). It is pure: the routes in audit_routes.go parse the
// request and write the answer.

package wsdashboard

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

const (
	// auditPageDefault and auditPageMax bound an events page's n.
	auditPageDefault = 100
	auditPageMax     = 500
	// auditBodyMax caps each body the full-envelope route returns; a longer
	// one is served as its head, with the range follow-up for the rest.
	auditBodyMax = 8 << 20
	// auditRangeDefault and auditRangeMax bound a range follow-up's len.
	auditRangeDefault = 1 << 20
	auditRangeMax     = 8 << 20
	// auditSearchDefault and auditSearchMax bound a search page's n.
	auditSearchDefault = 100
	auditSearchMax     = 500
	// auditLineMax bounds a search hit's line; a longer line is returned as
	// an excerpt around its first match, marked clipped.
	auditLineMax = 1024
	// auditTitleMax bounds a summary's title.
	auditTitleMax = 160
	// auditIDMax bounds an event or call ID a request may carry: stored IDs
	// are normalized to at most wsaudit.IdentifierMaxBytes encoded bytes or
	// a digest.
	auditIDMax = 4 * wsaudit.IdentifierMaxBytes
)

// auditSearchBudget bounds the body bytes one search request scans; a page
// that reaches it returns the hits so far with a next cursor (a variable so
// tests can reach it with small bodies).
var auditSearchBudget = 64 << 20

// errStaleCursor answers an event or search cursor whose event is no longer
// retained while the session still has audit data.
var errStaleCursor = errors.New("stale_cursor")

// auditSummary is one event of an events page or a call lookup.
type auditSummary struct {
	ID        string       `json:"id"`
	Seq       uint64       `json:"seq"`
	At        string       `json:"at"`
	Kind      wsaudit.Kind `json:"kind"`
	Subkind   string       `json:"subkind"`
	CallID    string       `json:"call_id"`
	ParentID  string       `json:"parent_id"`
	AgentID   string       `json:"agent_id"`
	ModelSeen bool         `json:"model_seen"`
	Title     string       `json:"title"`
	InBytes   int          `json:"in_bytes"`
	OutBytes  int          `json:"out_bytes"`
	Attached  []string     `json:"attached"`
	// The tool_call fields: present (null when unknown) on a tool_call
	// summary only, absent on every other kind.
	*auditCallFields
}

// auditCallFields are a tool_call summary's additive fields: the call's
// duration and its request usage (meta.request).
type auditCallFields struct {
	DurationMS *int64  `json:"duration_ms"`
	RequestID  *string `json:"request_id"`
	CtxBefore  *int64  `json:"ctx_before"`
	CtxAfter   *int64  `json:"ctx_after"`
}

// auditPage is the events route's answer.
type auditPage struct {
	Events []auditSummary `json:"events"`
	Before string         `json:"before"`
	After  string         `json:"after"`
	End    bool           `json:"end"`
	Cut    bool           `json:"cut"`
}

// auditCallAnswer is the call lookup's answer: the call's own summary (null
// when it is not retained) and its attached events, oldest first.
type auditCallAnswer struct {
	Call   *auditSummary  `json:"call"`
	Events []auditSummary `json:"events"`
}

// auditFilter is an events request's selection: kinds (all when empty) and
// the UTC interval since <= at < until (each bound optional).
type auditFilter struct {
	kinds map[wsaudit.Kind]bool
	since *time.Time
	until *time.Time
}

func (f auditFilter) match(ev *wsaudit.Envelope) bool {
	if len(f.kinds) > 0 && !f.kinds[ev.Kind] {
		return false
	}
	if f.since != nil && ev.At.Before(*f.since) {
		return false
	}
	if f.until != nil && !ev.At.Before(*f.until) {
		return false
	}
	return true
}

// auditCursor is an events request's event-ID cursor.
type auditCursor struct {
	before, after string
}

// indexOfID is the position of the retained event whose ID is id, or -1.
func indexOfID(evs []wsaudit.Envelope, id string) int {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].ID == id {
			return i
		}
	}
	return -1
}

// pageEvents selects the events page: the filter first, then the cursor and
// n, as /s/<id>/history pages its entries. A cursor naming an event no
// longer retained is stale while the session still has events; with none,
// the page is empty.
func pageEvents(evs []wsaudit.Envelope, cut bool, f auditFilter, c auditCursor, n int) (auditPage, error) {
	page := auditPage{Events: []auditSummary{}}
	var pos int
	switch {
	case c.before != "":
		pos = indexOfID(evs, c.before)
	case c.after != "":
		pos = indexOfID(evs, c.after)
	}
	if pos < 0 {
		if len(evs) > 0 {
			return page, errStaleCursor
		}
		page.End, page.Cut = true, cut
		return page, nil
	}
	var sel []int
	for i := range evs {
		if f.match(&evs[i]) {
			sel = append(sel, i)
		}
	}
	var start, stop int
	switch {
	case c.before != "":
		stop = sort.SearchInts(sel, pos) // first selected at or after the cursor
		start = max(stop-n, 0)
	case c.after != "":
		start = sort.SearchInts(sel, pos+1) // first selected after the cursor
		stop = min(start+n, len(sel))
	default:
		stop = len(sel)
		start = max(stop-n, 0)
	}
	idx := sel[start:stop]
	page.Events = summarize(evs, idx)
	if len(idx) > 0 {
		page.Before = evs[idx[0]].ID
		page.After = evs[idx[len(idx)-1]].ID
	}
	page.End = start == 0
	page.Cut = page.End && cut
	return page, nil
}

// lookupCall answers ?call=<call id>: the oldest retained call event with
// that call ID, and every other event tied to it (its call ID or its parent
// ID), oldest first.
func lookupCall(evs []wsaudit.Envelope, callID string) auditCallAnswer {
	ans := auditCallAnswer{Events: []auditSummary{}}
	call := -1
	var tied []int
	for i := range evs {
		ev := &evs[i]
		if ev.CallID != callID && ev.ParentID != callID {
			continue
		}
		if call < 0 && ev.CallID == callID && isAuditCall(ev) {
			call = i
			continue
		}
		tied = append(tied, i)
	}
	if call >= 0 {
		s := summarize(evs, []int{call})[0]
		ans.Call = &s
	}
	ans.Events = summarize(evs, tied)
	return ans
}

// isAuditCall reports whether ev is a tool call others attach to: a
// tool_call, or a native subagent's own tool use.
func isAuditCall(ev *wsaudit.Envelope) bool {
	if ev.CallID == "" {
		return false
	}
	switch ev.Kind {
	case wsaudit.KindToolCall:
		return true
	case wsaudit.KindSubagent:
		switch ev.Subkind {
		case "tool_use", "server_tool_use", "mcp_tool_use":
			return true
		}
	}
	return false
}

// isAuditResult reports whether ev is a call's result.
func isAuditResult(ev *wsaudit.Envelope) bool {
	return ev.CallID != "" && (ev.Kind == wsaudit.KindToolResult ||
		(ev.Kind == wsaudit.KindSubagent && ev.Subkind == "tool_result"))
}

// auditRequest reads an event's meta.request object: its id, prev_id and
// ctx, each reported only when present and well formed.
func auditRequest(ev *wsaudit.Envelope) (id, prev string, ctx int64, hasCtx bool) {
	m, ok := ev.Meta[wsaudit.MetaRequest].(map[string]any)
	if !ok {
		return "", "", 0, false
	}
	id, _ = m["id"].(string)
	prev, _ = m["prev_id"].(string)
	ctx, hasCtx = metaInt(m["ctx"])
	return id, prev, ctx, hasCtx
}

// metaInt reads an integral meta value: as the adapter wrote it (in memory)
// or as JSON decoded it (recovered from disk).
func metaInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n != math.Trunc(n) || n < math.MinInt64 || n >= math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// summarize builds the summaries of evs[idx...], in idx order. One pass over
// the whole snapshot finds what a page's calls need from outside the page:
// their attached events, their results' times, and the request that follows
// each call's request.
func summarize(evs []wsaudit.Envelope, idx []int) []auditSummary {
	out := make([]auditSummary, len(idx))
	calls := map[string]bool{}
	requests := map[string]bool{}
	for k, i := range idx {
		ev := &evs[i]
		out[k] = auditSummary{
			ID: ev.ID, Seq: ev.Seq, At: ev.At.UTC().Format(wsaudit.TimestampLayout),
			Kind: ev.Kind, Subkind: ev.Subkind, CallID: ev.CallID, ParentID: ev.ParentID, AgentID: ev.AgentID,
			ModelSeen: ev.ModelSeen, Title: auditTitle(ev), InBytes: len(ev.In), OutBytes: len(ev.Out),
			Attached: []string{},
		}
		if isAuditCall(ev) {
			calls[ev.CallID] = true
		}
		if ev.Kind == wsaudit.KindToolCall {
			if id, _, _, _ := auditRequest(ev); id != "" {
				requests[id] = true
			}
		}
	}
	attached := map[string][]int{}
	resultAt := map[string]time.Time{}
	// ctxAfter maps a request to the ctx of the first event naming it as
	// prev_id, nil when that event carries no ctx.
	ctxAfter := map[string]*int64{}
	// The scan is skipped when the page has no call or request to resolve;
	// its tool_calls still carry their (null) call fields below.
	scan := len(calls) > 0 || len(requests) > 0
	for i := 0; scan && i < len(evs); i++ {
		ev := &evs[i]
		// An event is tied to the call its call ID names (its result, an
		// approval request) and to the call its parent ID names (a native
		// subagent's events under its spawning call).
		if ev.CallID != "" && calls[ev.CallID] {
			attached[ev.CallID] = append(attached[ev.CallID], i)
			if _, ok := resultAt[ev.CallID]; !ok && isAuditResult(ev) {
				resultAt[ev.CallID] = ev.At
			}
		}
		if ev.ParentID != "" && ev.ParentID != ev.CallID && calls[ev.ParentID] {
			attached[ev.ParentID] = append(attached[ev.ParentID], i)
		}
		if len(requests) > 0 {
			if _, prev, ctx, ok := auditRequest(ev); prev != "" && requests[prev] {
				if _, seen := ctxAfter[prev]; !seen {
					if ok {
						ctxAfter[prev] = &ctx
					} else {
						ctxAfter[prev] = nil
					}
				}
			}
		}
	}
	for k, i := range idx {
		ev := &evs[i]
		if isAuditCall(ev) {
			for _, j := range attached[ev.CallID] {
				if j != i && evs[j].ID != "" {
					out[k].Attached = append(out[k].Attached, evs[j].ID)
				}
			}
		}
		if ev.Kind != wsaudit.KindToolCall {
			continue
		}
		cf := &auditCallFields{}
		if at, ok := resultAt[ev.CallID]; ok && ev.CallID != "" {
			ms := at.Sub(ev.At).Milliseconds()
			cf.DurationMS = &ms
		}
		if id, _, ctx, hasCtx := auditRequest(ev); id != "" {
			cf.RequestID = &id
			if hasCtx {
				cf.CtxBefore = &ctx
			}
			cf.CtxAfter = ctxAfter[id]
		} else if hasCtx {
			cf.CtxBefore = &ctx
		}
		out[k].auditCallFields = cf
	}
	return out
}

// auditTitle is a summary's one-line label: the first non-blank line of the
// event's primary body (a call's input, else its output, input or raw line),
// cut to auditTitleMax bytes on a rune boundary.
func auditTitle(ev *wsaudit.Envelope) string {
	bodies := []string{ev.Out, ev.In, ev.Raw}
	if ev.Kind == wsaudit.KindToolCall {
		bodies = []string{ev.In, ev.Out}
	}
	for _, body := range bodies {
		for body != "" {
			line, rest, _ := strings.Cut(body, "\n")
			body = rest
			if line = strings.TrimSpace(line); line != "" {
				return clipUTF8(line, auditTitleMax)
			}
		}
	}
	return ""
}

// clipUTF8 cuts s to at most n bytes, ending on a rune boundary.
func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// auditPart is the stored body a range follow-up reads.
func auditPart(ev *wsaudit.Envelope, part string) (string, bool) {
	var body string
	switch part {
	case wsaudit.FieldIn:
		body = ev.In
	case wsaudit.FieldOut:
		body = ev.Out
	case wsaudit.FieldRaw:
		body = ev.Raw
	default:
		return "", false
	}
	return body, body != ""
}

// auditRange is the range follow-up's answer.
type auditRange struct {
	Part   string `json:"part"`
	Offset int    `json:"offset"`
	Len    int    `json:"len"`
	Total  int    `json:"total"`
	Data   string `json:"data"`
	Next   int    `json:"next"`
	End    bool   `json:"end"`
}

// sliceBody reads the stored bytes of body from offset for n bytes: the
// offset moves forward out of a UTF-8 sequence, and the end extends to the
// next character boundary, so the slice is whole characters.
func sliceBody(part, body string, offset, n int) auditRange {
	total := len(body)
	for offset < total && !utf8.RuneStart(body[offset]) {
		offset++
	}
	end := min(offset+n, total)
	for end < total && !utf8.RuneStart(body[end]) {
		end++
	}
	return auditRange{Part: part, Offset: offset, Len: end - offset, Total: total, Data: body[offset:end], Next: end, End: end == total}
}

// headUTF8 is the longest whole-character prefix of body within n bytes.
func headUTF8(body string, n int) string {
	return clipUTF8(body, n)
}

// auditEnvelopeExtra is what the full-envelope route adds to the stored
// envelope: the stored body lengths, and for each body longer than one
// response its range follow-up start.
type auditEnvelopeExtra struct {
	InBytes   int                         `json:"in_bytes"`
	OutBytes  int                         `json:"out_bytes"`
	RawBytes  int                         `json:"raw_bytes"`
	Truncated bool                        `json:"truncated"`
	Range     map[string]auditRangeFollow `json:"range,omitempty"`
	// Matches is each stored body's match spans, present only when the
	// request carried a query (see serveAuditEvent).
	Matches map[string]auditBodyMatches `json:"matches,omitempty"`
}

// auditRangeFollow names where a truncated body's range follow-up starts.
type auditRangeFollow struct {
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

// envelopeAnswer encodes the full envelope, each body capped at
// auditBodyMax with its range follow-up, store-level stored_trunc unchanged.
func envelopeAnswer(ev wsaudit.Envelope, matches map[string]auditBodyMatches) ([]byte, error) {
	extra := auditEnvelopeExtra{InBytes: len(ev.In), OutBytes: len(ev.Out), RawBytes: len(ev.Raw), Matches: matches}
	for _, p := range []struct {
		name string
		body *string
	}{{wsaudit.FieldIn, &ev.In}, {wsaudit.FieldOut, &ev.Out}, {wsaudit.FieldRaw, &ev.Raw}} {
		if len(*p.body) <= auditBodyMax {
			continue
		}
		total := len(*p.body)
		*p.body = headUTF8(*p.body, auditBodyMax)
		if extra.Range == nil {
			extra.Range = map[string]auditRangeFollow{}
		}
		extra.Range[p.name] = auditRangeFollow{Offset: len(*p.body), Total: total}
		extra.Truncated = true
	}
	head, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	tail, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	// Splice the two objects: the envelope's fields, then the extras.
	out := make([]byte, 0, len(head)+len(tail)+1)
	out = append(out, head[:len(head)-1]...)
	out = append(out, ',')
	out = append(out, tail[1:]...)
	return out, nil
}

// auditHit is one search hit.
type auditHit struct {
	EventID  string       `json:"event_id"`
	Kind     wsaudit.Kind `json:"kind"`
	Field    string       `json:"field"`
	LineNo   int          `json:"line_no"`
	Line     string       `json:"line"`
	Clipped  bool         `json:"clipped,omitempty"`
	CallID   string       `json:"call_id"`
	ParentID string       `json:"parent_id"`
}

// auditSearchAnswer is the search route's answer.
type auditSearchAnswer struct {
	Hits []auditHit `json:"hits"`
	Next *string    `json:"next"`
}

// auditSearch is one search request: its matcher, kinds, page size, and the
// fingerprint a cursor carries, so a cursor is only followed with the query
// it was issued for.
type auditSearch struct {
	match func(line string) (start, end int, ok bool)
	// seamed matches a line holding a stored_trunc seam with a pattern that
	// holds a zero-width assertion (^ $ \A \z \b \B), whose answer depends
	// on the text around a position; nil for a pattern without one.
	seamed *seamMatcher
	// re is the compiled expression (nil for a case-sensitive literal, which
	// is lit); the span extraction runs the same expression as match.
	re    *regexp.Regexp
	lit   string
	kinds map[wsaudit.Kind]bool
	n     int
	query string
}

// newAuditSearch compiles q: a regular expression (RE2, linear time) when
// regex is set, else a literal; case-insensitive unless caseSensitive.
func newAuditSearch(q string, regex, caseSensitive bool, kinds map[wsaudit.Kind]bool, kindsRaw string, n int) (auditSearch, error) {
	s := auditSearch{kinds: kinds, n: n}
	sum := sha256.Sum256([]byte(strings.Join([]string{q, boolDigit(regex), boolDigit(caseSensitive), kindsRaw}, "\x00")))
	s.query = hex.EncodeToString(sum[:8])
	if !regex && caseSensitive {
		s.lit = q
		s.match = func(line string) (int, int, bool) {
			i := strings.Index(line, q)
			return i, i + len(q), i >= 0
		}
		return s, nil
	}
	pattern := q
	if !regex {
		pattern = regexp.QuoteMeta(q)
	}
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return s, err
	}
	s.re = re
	if tree, err := syntax.Parse(pattern, syntax.Perl); err == nil && hasAssertion(tree) {
		// The parsed expression's own spelling, not the source: a source can
		// end inside a construct that runs to the end of the pattern (\Q
		// with no \E), which would swallow the wrapping.
		if s.seamed, err = newSeamMatcher(tree.String()); err != nil {
			return s, err
		}
	}
	s.match = func(line string) (int, int, bool) {
		loc := re.FindStringIndex(line)
		if loc == nil {
			return 0, 0, false
		}
		return loc[0], loc[1], true
	}
	return s, nil
}

// hasAssertion reports whether a parsed pattern holds a zero-width
// assertion.
func hasAssertion(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return true
	}
	for _, sub := range re.Sub {
		if hasAssertion(sub) {
			return true
		}
	}
	return false
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// searchPos is where a search resumes: the event (by seq, checked against
// its ID), the field (0 in, 1 out), and the 1-based line number.
type searchPos struct {
	Seq   uint64 `json:"s"`
	ID    string `json:"i"`
	Field int    `json:"f"`
	Line  int    `json:"l"`
	Query string `json:"q"`
}

func encodeSearchCursor(p searchPos) string {
	raw, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// errBadCursor refuses a malformed search cursor, or one issued for another
// query.
var errBadCursor = errors.New("cursor is malformed or belongs to another query")

func decodeSearchCursor(s, query string) (searchPos, error) {
	var p searchPos
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Field < 0 || p.Field > 1 || p.Line < 1 || p.Query != query {
		return p, errBadCursor
	}
	return p, nil
}

// run scans the stored in and out bodies of evs newest first (each body's
// lines in order, in before out), from the cursor when one is given, until n
// hits or the scan budget. Line numbers count the stored body's lines; in a
// stored_trunc body the line holding the seam is matched as its head piece
// and its tail piece apart, so no hit spans the omitted middle. After the
// n-th hit the scan looks ahead for one more, so next is null once the
// oldest hit is returned. done reports cancellation (the request ended).
func (s auditSearch) run(evs []wsaudit.Envelope, cursor *searchPos, done func() bool) (auditSearchAnswer, error) {
	ans := auditSearchAnswer{Hits: []auditHit{}}
	i := len(evs) - 1
	field, fromLine := 0, 1
	if cursor != nil {
		k := sort.Search(len(evs), func(k int) bool { return evs[k].Seq >= cursor.Seq })
		if k == len(evs) || evs[k].Seq != cursor.Seq || evs[k].ID != cursor.ID {
			if len(evs) == 0 {
				return ans, nil
			}
			return ans, errStaleCursor
		}
		i, field, fromLine = k, cursor.Field, cursor.Line
	}
	// pending is the cursor after the n-th hit, issued once the lookahead
	// finds another hit.
	var pending *string
	// stopAt ends the scan early (budget, cancellation) at the first
	// unexamined position: the lookahead found no hit between the n-th hit
	// and here, so resuming here repeats no work.
	stopAt := func(ev *wsaudit.Envelope, field, line int) (auditSearchAnswer, error) {
		next := encodeSearchCursor(searchPos{Seq: ev.Seq, ID: ev.ID, Field: field, Line: line, Query: s.query})
		ans.Next = &next
		return ans, nil
	}
	scanned := 0
	for ; i >= 0; i, field, fromLine = i-1, 0, 1 {
		ev := &evs[i]
		if done() {
			if pending != nil || len(ans.Hits) > 0 {
				return stopAt(ev, field, fromLine)
			}
			return ans, nil
		}
		if len(s.kinds) > 0 && !s.kinds[ev.Kind] {
			continue
		}
		for ; field <= 1; field, fromLine = field+1, 1 {
			name, body, trunc := wsaudit.FieldIn, ev.In, storedTrunc(ev, wsaudit.FieldIn)
			if field == 1 {
				name, body, trunc = wsaudit.FieldOut, ev.Out, storedTrunc(ev, wsaudit.FieldOut)
			}
			if body == "" {
				continue // nothing left to search here, whatever the budget
			}
			if scanned >= auditSearchBudget {
				return stopAt(ev, field, fromLine)
			}
			scanned += len(body)
			stop := false
			eachLine(body, trunc, func(no int, line string, seam int) bool {
				if no < fromLine {
					return true
				}
				start, end, ok := s.matchLine(line, seam)
				if !ok {
					return true
				}
				if pending != nil {
					ans.Next, stop = pending, true
					return false
				}
				text, clipped := excerpt(line, start, end)
				ans.Hits = append(ans.Hits, auditHit{
					EventID: ev.ID, Kind: ev.Kind, Field: name, LineNo: no, Line: text, Clipped: clipped,
					CallID: ev.CallID, ParentID: ev.ParentID,
				})
				if len(ans.Hits) == s.n {
					next := encodeSearchCursor(searchPos{Seq: ev.Seq, ID: ev.ID, Field: field, Line: no + 1, Query: s.query})
					pending = &next
				}
				return true
			})
			if stop {
				return ans, nil
			}
		}
	}
	return ans, nil
}

// matchLine matches one stored line. In a line holding a stored_trunc seam
// (seam >= 0) no match consumes text from both sides of the omitted middle.
// A pattern without zero-width assertions is matched against the head piece,
// then the tail piece. One with assertions is matched by seamMatcher, so ^ $
// \b see the line's real boundaries and neighbours, not the pieces' ends.
// Offsets are in the whole line.
func (s auditSearch) matchLine(line string, seam int) (int, int, bool) {
	if seam < 0 {
		return s.match(line)
	}
	if s.seamed != nil {
		return s.seamed.match(line, seam)
	}
	if start, end, ok := s.match(line[:seam]); ok {
		return start, end, true
	}
	start, end, ok := s.match(line[seam:])
	return start + seam, end + seam, ok
}

// seamMatcher matches a pattern holding zero-width assertions inside a
// window [lo, hi) of one stored line, evaluating every assertion against the
// whole line. The window's outside neighbours are stood in for by a sentinel
// of the same word class (the only context ^ $ \A \z \b \B read in a line
// without line breaks): none at the line's own start or end. A match is kept
// inside the window by the shape of the expression: sentinel, lazy prefix,
// the pattern's group, the rest, sentinel. Matching allocates the window
// copy and the submatch indexes, however many matches the window holds. Its
// pattern is a parsed expression's String(), which closes every construct it
// opens.
type seamMatcher struct {
	pattern string
	// re is the compiled form by the left and right sentinel kind (see
	// sentinelKind), made on first use; a request is served by one goroutine.
	re map[[2]int]*regexp.Regexp
	// bounded is the program of the scan that ends a window's matches at its
	// edge; stats counts the scan's branches.
	bounded *boundedProg
	stats   seamStats
}

// seamSentinels are the sentinel characters by kind: 0 none (a line edge),
// 1 a non-word character, 2 a word character.
var seamSentinels = [3]string{"", "\x00", "_"}

func newSeamMatcher(pattern string) (*seamMatcher, error) {
	m := &seamMatcher{pattern: pattern, re: map[[2]int]*regexp.Regexp{}}
	if _, err := m.compiled(0, 0); err != nil {
		return nil, err
	}
	var err error
	if m.bounded, err = newBoundedProg(pattern); err != nil {
		return nil, err
	}
	return m, nil
}

// compiled is the expression for a window whose left and right neighbours
// are of the given sentinel kinds.
func (m *seamMatcher) compiled(left, right int) (*regexp.Regexp, error) {
	k := [2]int{left, right}
	if re := m.re[k]; re != nil {
		return re, nil
	}
	// Group 1 is the pattern's match; the sentinels end and start the text.
	re, err := regexp.Compile(`\A` + regexp.QuoteMeta(seamSentinels[left]) + `(?s:.*?)((?:` + m.pattern + `))(?s:.*)` + regexp.QuoteMeta(seamSentinels[right]) + `\z`)
	if err != nil {
		return nil, err
	}
	m.re[k] = re
	return re, nil
}

// sentinelKind is the sentinel kind standing for the byte next to a window
// edge: none past the line.
func sentinelKind(line string, i int) int {
	if i < 0 || i >= len(line) {
		return 0
	}
	return 1 + wordClass(line[i])
}

// first is the first match inside the window [lo, hi) of line, in line
// offsets.
func (m *seamMatcher) first(line string, lo, hi int) (int, int, bool) {
	left, right := sentinelKind(line, lo-1), sentinelKind(line, hi)
	re, err := m.compiled(left, right)
	if err != nil {
		return 0, 0, false
	}
	loc := re.FindStringSubmatchIndex(seamSentinels[left] + line[lo:hi] + seamSentinels[right])
	if loc == nil {
		return 0, 0, false
	}
	return loc[2] - len(seamSentinels[left]) + lo, loc[3] - len(seamSentinels[left]) + lo, true
}

// match is the first match on a line with a seam: on the head, else the tail.
func (m *seamMatcher) match(line string, seam int) (int, int, bool) {
	if start, end, ok := m.first(line, 0, seam); ok {
		return start, end, true
	}
	return m.first(line, seam, len(line))
}

// wordClass is 1 for an ASCII word byte (what \b counts as a word
// character), else 0.
func wordClass(b byte) int {
	if b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' {
		return 1
	}
	return 0
}

// storedTrunc is the stored head length of a body the store kept as head
// plus tail, or -1 for a body stored whole.
func storedTrunc(ev *wsaudit.Envelope, field string) int {
	if ev.StoredTrunc == nil {
		return -1
	}
	var b *wsaudit.BodyTrunc
	switch field {
	case wsaudit.FieldIn:
		b = ev.StoredTrunc.In
	case wsaudit.FieldOut:
		b = ev.StoredTrunc.Out
	}
	if b == nil {
		return -1
	}
	return b.HeadBytes
}

// eachLine calls fn with each line of body and its 1-based number until fn
// returns false. Lines end at '\n' (a trailing '\r' is dropped); a final
// '\n' opens no line. head >= 0 is a stored_trunc body's head length: the
// line the head ends inside, without a line break at the seam, gets the
// seam's offset within it, else seam is -1.
func eachLine(body string, head int, fn func(no int, line string, seam int) bool) {
	for no, at := 1, 0; at < len(body); no++ {
		end := strings.IndexByte(body[at:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += at
		}
		line := strings.TrimSuffix(body[at:end], "\r")
		seam := -1
		if head > at && head < at+len(line) {
			seam = head - at
		}
		if !fn(no, line, seam) {
			return
		}
		at = end + 1
	}
}

// excerpt is line when it fits auditLineMax, else a window of it around the
// match [start, end), on rune boundaries, reported clipped.
func excerpt(line string, start, end int) (string, bool) {
	if len(line) <= auditLineMax {
		return line, false
	}
	from := max(start-auditLineMax/4, 0)
	if end-from > auditLineMax {
		from = start
	}
	for from > 0 && !utf8.RuneStart(line[from]) {
		from--
	}
	return clipUTF8(line[from:], auditLineMax), true
}

// auditSpansMax bounds the spans one body's answer carries; a body with more
// matches is answered with the first ones, marked capped.
const auditSpansMax = 10000

// auditSpan is one match on a stored line: the line's number in the server's
// numbering (a stored_trunc body's head and tail numbered as one text, as in
// search hits) and start/end offsets in UTF-16 code units within the line, so
// a browser slices the line without conversion. Matches are never empty.
type auditSpan struct {
	Line  int `json:"line"`
	Start int `json:"start"`
	End   int `json:"end"`
}

// auditBodyMatches is one body's match spans over its first Through bytes.
type auditBodyMatches struct {
	Through int         `json:"through"`
	Spans   []auditSpan `json:"spans"`
	Capped  bool        `json:"capped,omitempty"`
}

// bodySpans is the non-empty matches of the first through bytes of body, line
// by line, up to auditSpansMax; one more visible match marks the answer
// capped. Empty matches are skipped and never count toward the cap. head is
// the body's stored_trunc head length (-1 for a body stored whole).
func (s auditSearch) bodySpans(body string, head, through int) auditBodyMatches {
	res := auditBodyMatches{Through: through, Spans: []auditSpan{}}
	eachLine(body[:through], head, func(no int, line string, seam int) bool {
		units := utf16Cursor{s: line}
		s.eachLineMatch(line, seam, func(start, end int) bool {
			if start == end {
				return false
			}
			if len(res.Spans) == auditSpansMax {
				res.Capped = true
				return true
			}
			res.Spans = append(res.Spans, auditSpan{Line: no, Start: units.at(start), End: units.at(end)})
			return false
		})
		return !res.Capped
	})
	return res
}

// utf16Cursor converts increasing byte offsets of a string to UTF-16 code
// unit offsets, as a browser string counts them: one unit per rune below
// U+10000 (a byte that is not UTF-8 counts one, as the JSON encoding turns
// it into U+FFFD), two above.
type utf16Cursor struct {
	s       string
	b, unit int
}

func (c *utf16Cursor) at(b int) int {
	for c.b < b {
		r, w := utf8.DecodeRuneInString(c.s[c.b:])
		c.b += w
		c.unit++
		if r >= 0x10000 {
			c.unit++
		}
	}
	return c.unit
}

// throughBytes is how much of body a spans request reads: all of it unless
// the request names a shorter loaded prefix, moved forward to a character
// boundary.
func throughBytes(body string, through int) int {
	through = min(through, len(body))
	for through < len(body) && !utf8.RuneStart(body[through]) {
		through++
	}
	return through
}

// eventMatches is the spans of each stored body the event carries, keyed by
// field. through is a field's loaded prefix in bytes, absent for the whole
// body.
func eventMatches(s auditSearch, ev *wsaudit.Envelope, through map[string]int) map[string]auditBodyMatches {
	out := map[string]auditBodyMatches{}
	for _, f := range []struct{ name, body string }{{wsaudit.FieldIn, ev.In}, {wsaudit.FieldOut, ev.Out}, {wsaudit.FieldRaw, ev.Raw}} {
		if f.body == "" {
			continue
		}
		n := len(f.body)
		if t, ok := through[f.name]; ok {
			n = throughBytes(f.body, t)
		}
		out[f.name] = s.bodySpans(f.body, storedTrunc(ev, f.name), n)
	}
	return out
}
