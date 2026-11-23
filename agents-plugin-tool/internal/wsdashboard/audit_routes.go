// audit_routes.go serves the read-only audit query API under a session's
// prefix: /s/<reply-id>/audit/events, /audit/event/<event id>, and
// /audit/search. Every answer is JSON read from the audit store's in-memory
// snapshot of the session's ring (wsauditstore.View); no route writes.

package wsdashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
)

// auditViewer reads one session's retained audit events: the audit store,
// or a test's fixture.
type auditViewer interface {
	View(ctx context.Context, session string) (wsauditstore.View, error)
}

// auditViewerFor is the reader the audit routes use, nil when the audit
// store is unavailable.
func (d *Daemon) auditViewerFor() auditViewer {
	if d.auditRead != nil {
		return d.auditRead
	}
	if d.audit == nil {
		return nil
	}
	return d.audit.store
}

// auditError writes a JSON error answer.
func auditError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": msg})
}

// auditBadRequest is a request the audit routes refuse as malformed.
type auditBadRequest string

func (e auditBadRequest) Error() string { return string(e) }

// handleAudit serves /s/<reply-id>/audit/<rest> for a read request (the
// session routes refuse every other method before this). The access guard
// already admitted the viewer, exactly as for /s/<reply-id>/history. A
// session without audit data (not hosted, its ring evicted or expired, or
// nothing captured yet) answers an empty result, not an error, so the
// session need not be in the registry: an audit ring can outlive its
// registration.
func (d *Daemon) handleAudit(w http.ResponseWriter, r *http.Request, replyID, rest string) {
	if !ValidReplyID(replyID) {
		http.NotFound(w, r)
		return
	}
	var serve func(http.ResponseWriter, *http.Request, wsauditstore.View) error
	switch {
	case rest == "events":
		serve = serveAuditEvents
	case rest == "search":
		serve = serveAuditSearch
	case strings.HasPrefix(rest, "event/") && len(rest) > len("event/"):
		// The path is already unescaped: an event ID's '/' and '#' arrive
		// percent-encoded and are taken whole from here to the end.
		id := strings.TrimPrefix(rest, "event/")
		serve = func(w http.ResponseWriter, r *http.Request, v wsauditstore.View) error {
			return serveAuditEvent(w, r, v, id)
		}
	default:
		http.NotFound(w, r)
		return
	}
	viewer := d.auditViewerFor()
	if viewer == nil {
		auditError(w, http.StatusServiceUnavailable, "audit store unavailable")
		return
	}
	v, err := viewer.View(r.Context(), replyID)
	if err != nil {
		if r.Context().Err() == nil {
			fmt.Fprintf(d.log, "dashboard: audit %s: %v\n", replyID, err)
		}
		auditError(w, http.StatusServiceUnavailable, "audit data temporarily unavailable")
		return
	}
	err = serve(w, r, v)
	var bad auditBadRequest
	switch {
	case err == nil:
	case errors.As(err, &bad):
		auditError(w, http.StatusBadRequest, bad.Error())
	case errors.Is(err, errStaleCursor):
		auditError(w, http.StatusGone, errStaleCursor.Error())
	case errors.Is(err, errAuditNotFound):
		auditError(w, http.StatusNotFound, err.Error())
	default:
		fmt.Fprintf(d.log, "dashboard: audit %s: %v\n", replyID, err)
		auditError(w, http.StatusInternalServerError, "audit answer failed")
	}
}

var errAuditNotFound = errors.New("event not retained")

func writeAuditJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}

// auditQuery returns the request's query, refusing a repeated parameter or
// a query string that does not parse.
func auditQuery(r *http.Request) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, auditBadRequest("malformed query string")
	}
	for key, vals := range q {
		if len(vals) > 1 {
			return nil, auditBadRequest(key + " is given more than once")
		}
	}
	return q, nil
}

// auditCount parses a positive count, defaulting def and clamped to max.
func auditCount(q url.Values, key string, def, maxN int) (int, error) {
	raw, ok := q[key]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(raw[0])
	if err != nil || n < 1 {
		return 0, auditBadRequest(key + " must be a positive count")
	}
	return min(n, maxN), nil
}

// auditKinds parses a comma-separated kinds list; empty selects every kind.
func auditKinds(q url.Values) (map[wsaudit.Kind]bool, string, error) {
	raw := q.Get("kinds")
	if raw == "" {
		return nil, "", nil
	}
	kinds := map[wsaudit.Kind]bool{}
	for _, k := range strings.Split(raw, ",") {
		kind := wsaudit.Kind(strings.TrimSpace(k))
		if !kind.Valid() {
			return nil, "", auditBadRequest(fmt.Sprintf("unknown kind %q", k))
		}
		kinds[kind] = true
	}
	return kinds, raw, nil
}

// auditID bounds an event or call ID taken from a request. IDs are opaque
// and compared verbatim: the store keeps a short identifier as given
// (wsaudit.NormalizeID), control characters included, so any ID an answer
// carries must be accepted back.
func auditID(key, id string) error {
	if len(id) > auditIDMax {
		return auditBadRequest(key + " is too long")
	}
	return nil
}

// auditTime parses a UTC RFC3339Nano bound.
func auditTime(q url.Values, key string) (*time.Time, error) {
	raw, ok := q[key]
	if !ok {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw[0])
	if err != nil {
		return nil, auditBadRequest(key + " must be an RFC3339Nano UTC time")
	}
	if _, off := t.Zone(); off != 0 {
		return nil, auditBadRequest(key + " must be in UTC")
	}
	t = t.UTC()
	return &t, nil
}

// serveAuditEvents serves /audit/events: a page of event summaries, or with
// call=<call id> that call's summary and its attached events.
func serveAuditEvents(w http.ResponseWriter, r *http.Request, v wsauditstore.View) error {
	q, err := auditQuery(r)
	if err != nil {
		return err
	}
	if call, ok := q["call"]; ok {
		// The call join is by ID alone: no paging, kinds, or time bounds.
		for _, key := range []string{"before", "after", "n", "kinds", "since", "until"} {
			if _, set := q[key]; set {
				return auditBadRequest("call cannot be combined with " + key)
			}
		}
		if call[0] == "" {
			return auditBadRequest("call must name a call ID")
		}
		if err := auditID("call", call[0]); err != nil {
			return err
		}
		writeAuditJSON(w, lookupCall(v.Events, call[0]))
		return nil
	}
	c := auditCursor{before: q.Get("before"), after: q.Get("after")}
	if c.before != "" && c.after != "" {
		return auditBadRequest("before and after cannot be combined")
	}
	if err := auditID("before", c.before); err != nil {
		return err
	}
	if err := auditID("after", c.after); err != nil {
		return err
	}
	n, err := auditCount(q, "n", auditPageDefault, auditPageMax)
	if err != nil {
		return err
	}
	var f auditFilter
	if f.kinds, _, err = auditKinds(q); err != nil {
		return err
	}
	if f.since, err = auditTime(q, "since"); err != nil {
		return err
	}
	if f.until, err = auditTime(q, "until"); err != nil {
		return err
	}
	if f.since != nil && f.until != nil && f.since.After(*f.until) {
		return auditBadRequest("since is after until")
	}
	page, err := pageEvents(v.Events, v.Cut, f, c, n)
	if err != nil {
		return err
	}
	writeAuditJSON(w, page)
	return nil
}

// auditPattern parses the query parameters the search and the event route's
// match spans share: q (required, non-empty), regex and case as 0|1.
func auditPattern(q url.Values) (text string, regex, caseSensitive bool, err error) {
	text = q.Get("q")
	if text == "" {
		return "", false, false, auditBadRequest("q must not be empty")
	}
	flag := func(key string) (bool, error) {
		switch q.Get(key) {
		case "", "0":
			return false, nil
		case "1":
			return true, nil
		}
		return false, auditBadRequest(key + " must be 0 or 1")
	}
	if regex, err = flag("regex"); err != nil {
		return "", false, false, err
	}
	if caseSensitive, err = flag("case"); err != nil {
		return "", false, false, err
	}
	return text, regex, caseSensitive, nil
}

// auditEventMatches reads the event route's match request: q, regex and case
// as /audit/search takes them (an invalid expression is refused alike), the
// optional through_in, through_out and through_raw (the bytes of a body the
// viewer has loaded, default all of it), and only=matches to answer the spans
// without the envelope. It returns nil matches when the request carries no q.
func auditEventMatches(q url.Values, ev *wsaudit.Envelope) (matches map[string]auditBodyMatches, only bool, err error) {
	if _, has := q["q"]; !has {
		for _, key := range []string{"regex", "case", "only", "through_in", "through_out", "through_raw"} {
			if _, set := q[key]; set {
				return nil, false, auditBadRequest(key + " needs q")
			}
		}
		return nil, false, nil
	}
	text, regex, caseSensitive, err := auditPattern(q)
	if err != nil {
		return nil, false, err
	}
	switch q.Get("only") {
	case "":
	case "matches":
		only = true
	default:
		return nil, false, auditBadRequest("only must be matches")
	}
	through := map[string]int{}
	for _, f := range []string{wsaudit.FieldIn, wsaudit.FieldOut, wsaudit.FieldRaw} {
		raw, set := q["through_"+f]
		if !set {
			continue
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 0 {
			return nil, false, auditBadRequest("through_" + f + " must be a non-negative byte count")
		}
		through[f] = n
	}
	s, err := newAuditSearch(text, regex, caseSensitive, nil, "", 1)
	if err != nil {
		return nil, false, auditBadRequest("q is not a valid regular expression: " + err.Error())
	}
	// Without through_<part> a body is read as the envelope answer returns
	// it: its head within auditBodyMax.
	for _, f := range []struct{ name, body string }{{wsaudit.FieldIn, ev.In}, {wsaudit.FieldOut, ev.Out}, {wsaudit.FieldRaw, ev.Raw}} {
		if _, set := through[f.name]; !set && len(f.body) > auditBodyMax {
			through[f.name] = auditBodyMax
		}
	}
	return eventMatches(s, ev, through), only, nil
}

// serveAuditEvent serves /audit/event/<event id>: the full envelope, or with
// part=in|out|raw a range of one stored body. With q (and regex, case, as
// /audit/search takes them) the envelope also carries "matches": each stored
// body's match spans, in the server's matcher and numbering; only=matches
// answers the spans alone.
func serveAuditEvent(w http.ResponseWriter, r *http.Request, v wsauditstore.View, id string) error {
	if err := auditID("event id", id); err != nil {
		return err
	}
	q, err := auditQuery(r)
	if err != nil {
		return err
	}
	i := indexOfID(v.Events, id)
	part, ranged := q["part"]
	if !ranged {
		for _, key := range []string{"offset", "len"} {
			if _, set := q[key]; set {
				return auditBadRequest(key + " needs part")
			}
		}
		if i < 0 {
			return errAuditNotFound
		}
		matches, only, err := auditEventMatches(q, &v.Events[i])
		if err != nil {
			return err
		}
		if only {
			writeAuditJSON(w, map[string]any{"matches": matches})
			return nil
		}
		body, err := envelopeAnswer(v.Events[i], matches)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(append(body, '\n'))
		return nil
	}
	if i < 0 {
		return errAuditNotFound
	}
	for _, key := range []string{"q", "regex", "case", "only", "through_in", "through_out", "through_raw"} {
		if _, set := q[key]; set {
			return auditBadRequest(key + " cannot be combined with part")
		}
	}
	ev := &v.Events[i]
	body, ok := auditPart(ev, part[0])
	if !ok {
		return auditBadRequest("the event carries no " + strconv.Quote(part[0]) + " body")
	}
	offset := 0
	if raw, set := q["offset"]; set {
		if offset, err = strconv.Atoi(raw[0]); err != nil || offset < 0 {
			return auditBadRequest("offset must be a non-negative byte offset")
		}
	}
	if offset > len(body) {
		return auditBadRequest("offset is past the stored body")
	}
	n := auditRangeDefault
	if raw, set := q["len"]; set {
		if n, err = strconv.Atoi(raw[0]); err != nil || n < 1 {
			return auditBadRequest("len must be a positive byte count")
		}
		n = min(n, auditRangeMax)
	}
	writeAuditJSON(w, sliceBody(part[0], body, offset, n))
	return nil
}

// serveAuditSearch serves /audit/search: the stored in and out bodies'
// matching lines, newest event first, in pages joined by an opaque cursor.
func serveAuditSearch(w http.ResponseWriter, r *http.Request, v wsauditstore.View) error {
	q, err := auditQuery(r)
	if err != nil {
		return err
	}
	text, regex, caseSensitive, err := auditPattern(q)
	if err != nil {
		return err
	}
	kinds, kindsRaw, err := auditKinds(q)
	if err != nil {
		return err
	}
	n, err := auditCount(q, "n", auditSearchDefault, auditSearchMax)
	if err != nil {
		return err
	}
	s, err := newAuditSearch(text, regex, caseSensitive, kinds, kindsRaw, n)
	if err != nil {
		return auditBadRequest("q is not a valid regular expression: " + err.Error())
	}
	var cursor *searchPos
	if raw, ok := q["cursor"]; ok {
		p, err := decodeSearchCursor(raw[0], s.query)
		if err != nil {
			return auditBadRequest(err.Error())
		}
		cursor = &p
	}
	ctx := r.Context()
	ans, err := s.run(v.Events, cursor, func() bool { return ctx.Err() != nil })
	if err != nil {
		return err
	}
	writeAuditJSON(w, ans)
	return nil
}
