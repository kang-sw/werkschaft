package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// mailbox_history.go serves each session's history window: the last
// mailboxHistoryCap messages it sent or drained, full bodies included, so
// mail a conversation lost (a tangled session, a mis-aimed rewind) can be
// recovered with mailbox.history. The window lives in the memory-first
// mailbox store (wsmailbox.Store.History); a send or a recv appends to it in
// the same transaction that moves the mail (Tx.AppendHistory).

const (
	// mailboxHistoryCap is the window's capacity, received and sent together.
	mailboxHistoryCap = wsmailbox.HistoryCap
	// mailboxHistoryDefaultLimit is the default page size when limit is omitted
	// or non-positive, keeping compact output stable while HistoryCap is 100.
	mailboxHistoryDefaultLimit = 10
	// mailboxHistoryTitleMax bounds a title in runes; a longer first line is
	// cut there with an ellipsis.
	mailboxHistoryTitleMax = 120

	mailboxHistoryRecv = wsmailbox.HistoryRecv
	mailboxHistorySent = wsmailbox.HistorySent
)

// mailboxHistoryEntry is one entry to append. Its time is not stored: the
// entry name's stamp is the single source of the exchange time.
type mailboxHistoryEntry struct {
	Direction    string
	Counterparty string
	Body         string
}

// mailboxHistoryRecord is a read entry: the stored fields plus the exchange
// time (RFC3339 UTC, from the name's stamp), the derived title, and the
// entry's name (the key a conversation ring mail line carries).
type mailboxHistoryRecord struct {
	mailboxHistoryEntry
	Time  string
	Title string
	Name  string
	at    time.Time
}

// mailboxHistoryStore is the mailbox store of the cache home cacheHome
// (empty: this process's).
func mailboxHistoryStore(cacheHome string) wsmailbox.Store {
	return wsmailbox.Paths{State: wsstate.Options{CacheHome: cacheHome}}.Store()
}

// appendMailboxHistoryTx appends entries, in order, to sessionKey's window
// inside tx, stamped from mailboxNow, and returns their names.
func appendMailboxHistoryTx(tx *wsmailbox.Tx, sessionKey string, entries []mailboxHistoryEntry) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]wsmailbox.HistoryEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, wsmailbox.HistoryEntry{Direction: e.Direction, Counterparty: e.Counterparty, Body: e.Body})
	}
	return tx.AppendHistory(sessionKey, mailboxNow(), out...)
}

// appendMailboxHistory appends entries to sessionKey's window in a
// transaction of its own and returns their names.
func appendMailboxHistory(cacheHome, sessionKey string, entries []mailboxHistoryEntry) ([]string, error) {
	var names []string
	err := mailboxHistoryStore(cacheHome).Update("history", func(tx *wsmailbox.Tx) error {
		var err error
		names, err = appendMailboxHistoryTx(tx, sessionKey, entries)
		return err
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// readMailboxHistory returns sessionKey's window, newest first. An entry
// whose name carries no valid stamp is skipped.
func readMailboxHistory(cacheHome, sessionKey string) ([]mailboxHistoryRecord, error) {
	entries, err := mailboxHistoryStore(cacheHome).History(sessionKey)
	if err != nil {
		return nil, err
	}
	records := make([]mailboxHistoryRecord, 0, len(entries))
	for _, e := range entries {
		if rec, ok := mailboxHistoryRecordOf(e); ok {
			records = append(records, rec)
		}
	}
	return records, nil
}

func mailboxHistoryRecordOf(e wsmailbox.HistoryEntry) (mailboxHistoryRecord, bool) {
	at, ok := wsmailbox.HistoryTime(e.Name)
	if !ok {
		return mailboxHistoryRecord{}, false
	}
	return mailboxHistoryRecord{
		mailboxHistoryEntry: mailboxHistoryEntry{Direction: e.Direction, Counterparty: e.Counterparty, Body: e.Body},
		Time:                at.Format(time.RFC3339),
		Title:               mailboxHistoryTitle(e.Body),
		Name:                e.Name,
		at:                  at,
	}, true
}

func mailboxHistoryID(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ".json")
}

func parseMailboxHistorySince(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	if len(s) == len("2006-01-02") {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("since must be RFC3339 or YYYY-MM-DD")
}

// mailboxHistoryTitle is the one title rule for a ring entry: the first line
// the sender wrote. A leading peer-lead note line is skipped, an empty body
// gives an empty title, and a first line longer than mailboxHistoryTitleMax
// runes is cut there with an ellipsis.
func mailboxHistoryTitle(body string) string {
	if hasPeerLeadNote(body) {
		_, rest, ok := strings.Cut(body, "\n")
		if !ok {
			return ""
		}
		body = rest
	}
	line, _, _ := strings.Cut(body, "\n")
	line = strings.TrimSuffix(line, "\r")
	if runes := []rune(line); len(runes) > mailboxHistoryTitleMax {
		return string(runes[:mailboxHistoryTitleMax]) + "…"
	}
	return line
}

// mailboxHistoryOut is mailbox.history's per-entry JSON shape. Body is a
// pointer so body:false omits it while body:true keeps an empty body.
type mailboxHistoryOut struct {
	ID           string  `json:"id"`
	Direction    string  `json:"direction"`
	Counterparty string  `json:"counterparty"`
	Time         string  `json:"time"`
	Title        string  `json:"title"`
	Body         *string `json:"body,omitempty"`
}

type mailboxReadOut struct {
	ID           string `json:"id"`
	Direction    string `json:"direction"`
	Counterparty string `json:"counterparty"`
	Time         string `json:"time"`
	Title        string `json:"title"`
	Body         string `json:"body"`
}

func (s *Server) handleMailboxHistory(id json.RawMessage, args map[string]any) response {
	const tool = "mailbox.history"
	sessionKey, err := sessionStateKey(tool, args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	if _, found := s.lookupSession(sessionKey); !found {
		return toolTextResponse(id, "", mailboxUnknownSessionError(tool))
	}
	limit := intFromArgument(args["limit"], mailboxHistoryDefaultLimit)
	offset, err := exactIntegerArgument(args, "offset", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if offset < 0 {
		return toolTextResponse(id, "", fmt.Errorf("%s: offset must be 0 or greater", tool))
	}
	withBody := boolArgument(args["body"])
	direction, _ := args["direction"].(string)
	direction = strings.TrimSpace(direction)
	switch direction {
	case "", "all":
		direction = ""
	case mailboxHistoryRecv, mailboxHistorySent:
	default:
		return toolTextResponse(id, "", fmt.Errorf(`%s: direction must be "recv", "sent", or "all"`, tool))
	}
	peer, _ := args["peer"].(string)
	peerLower := strings.ToLower(strings.TrimSpace(peer))
	sinceRaw, _ := args["since"].(string)
	sinceAt, err := parseMailboxHistorySince(sinceRaw)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}

	records, err := readMailboxHistory(s.getenv(wsstate.EnvCacheHome), sessionKey)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	out := []mailboxHistoryOut{}
	skipped := 0
	for _, r := range records {
		if direction != "" && r.Direction != direction {
			continue
		}
		if peerLower != "" && !strings.Contains(strings.ToLower(r.Counterparty), peerLower) {
			continue
		}
		if !sinceAt.IsZero() && r.at.Before(sinceAt) {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		item := mailboxHistoryOut{
			ID:           mailboxHistoryID(r.Name),
			Direction:    r.Direction,
			Counterparty: r.Counterparty,
			Time:         r.Time,
			Title:        r.Title,
		}
		if withBody {
			body := r.Body
			item.Body = &body
		}
		out = append(out, item)
		if len(out) == limit {
			break
		}
	}

	if wantsJSON(args) {
		return toolJSONResponse(id, out, nil)
	}
	if len(out) == 0 {
		return toolTextResponse(id, "no mailbox history\n", nil)
	}
	var b strings.Builder
	for _, item := range out {
		preposition := "from"
		if item.Direction == mailboxHistorySent {
			preposition = "to"
		}
		fmt.Fprintf(&b, "[%s] %s %s %s (%s): %s\n", item.Time, item.Direction, preposition, item.Counterparty, item.ID, item.Title)
		if item.Body != nil {
			b.WriteString(*item.Body)
			if !strings.HasSuffix(*item.Body, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("---\n")
		}
	}
	return toolTextResponse(id, b.String(), nil)
}

func (s *Server) handleMailboxRead(id json.RawMessage, args map[string]any) response {
	const tool = "mailbox.read"
	sessionKey, err := sessionStateKey(tool, args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	if _, found := s.lookupSession(sessionKey); !found {
		return toolTextResponse(id, "", mailboxUnknownSessionError(tool))
	}
	rawID, _ := args["id"].(string)
	targetID := mailboxHistoryID(rawID)
	if targetID == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: id is required", tool))
	}
	records, err := readMailboxHistory(s.getenv(wsstate.EnvCacheHome), sessionKey)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	var matched *mailboxHistoryRecord
	var prefixMatches []mailboxHistoryRecord
	for _, r := range records {
		candID := mailboxHistoryID(r.Name)
		if candID == targetID {
			cp := r
			matched = &cp
			break
		}
		if strings.HasPrefix(candID, targetID) {
			prefixMatches = append(prefixMatches, r)
		}
	}
	if matched == nil {
		switch len(prefixMatches) {
		case 0:
			return toolTextResponse(id, "", fmt.Errorf("%s: message %q not found in session history", tool, strings.TrimSpace(rawID)))
		case 1:
			matched = &prefixMatches[0]
		default:
			preview := make([]string, 0, min(3, len(prefixMatches)))
			for _, m := range prefixMatches[:min(3, len(prefixMatches))] {
				preview = append(preview, mailboxHistoryID(m.Name))
			}
			return toolTextResponse(id, "", fmt.Errorf("%s: message id %q is ambiguous (%d matches, e.g. %s)", tool, strings.TrimSpace(rawID), len(prefixMatches), strings.Join(preview, ", ")))
		}
	}
	out := mailboxReadOut{
		ID:           mailboxHistoryID(matched.Name),
		Direction:    matched.Direction,
		Counterparty: matched.Counterparty,
		Time:         matched.Time,
		Title:        matched.Title,
		Body:         matched.Body,
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, out, nil)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\ndirection: %s\ncounterparty: %s\ntime: %s\n\n%s", out.ID, out.Direction, out.Counterparty, out.Time, out.Body)
	if !strings.HasSuffix(out.Body, "\n") {
		b.WriteString("\n")
	}
	return toolTextResponse(id, b.String(), nil)
}
