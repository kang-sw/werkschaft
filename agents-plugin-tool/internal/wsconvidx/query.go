package wsconvidx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultSearchLimit        = 20
	MaxSearchLimit            = 50
	DefaultSearchSnippetChars = 200
	MaxSearchSnippetChars     = 500

	DefaultHistoryLimit        = 20
	MaxHistoryLimit            = 100
	DefaultHistorySnippetChars = 160
	MaxHistorySnippetChars     = 400

	DefaultReadMaxBytes = 16 << 10
	MaxReadMaxBytes     = MaxEntryBytes // 64 KiB
	DefaultWindowBefore = 2
	DefaultWindowAfter  = 2
	MaxWindowContext    = 10
)

// SearchParams configures Store.Search.
type SearchParams struct {
	Query        string
	SessionID    string
	SessionIDs   []string // optional scope filter (e.g. sessions matching workspace/status)
	Workspace    string   // optional direct workspace filter when SessionIDs is nil
	Kinds        []string
	SinceMs      int64
	UntilMs      int64
	Order        string // "relevance" (default) or "time"
	Limit        int
	SnippetChars int
}

// SearchHit is one FTS5 match returned by Store.Search.
type SearchHit struct {
	SessionID string  `json:"session_id"`
	EntryID   string  `json:"entry_id"`
	Workspace string  `json:"workspace,omitempty"`
	Kind      string  `json:"kind"`
	TS        int64   `json:"ts"`
	Snippet   string  `json:"snippet"`
	Bytes     int     `json:"bytes"`
	Truncated bool    `json:"truncated,omitempty"`
	Direction string  `json:"direction,omitempty"`
	Peer      string  `json:"peer,omitempty"`
	Title     string  `json:"title,omitempty"`
	Score     float64 `json:"-"`
}

// Search runs an FTS5 search over indexed ring entries.
func (s *Store) Search(ctx context.Context, p SearchParams) ([]SearchHit, error) {
	rawQuery := strings.TrimSpace(p.Query)
	if rawQuery == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	snippetChars := p.SnippetChars
	if snippetChars <= 0 {
		snippetChars = DefaultSearchSnippetChars
	}
	if snippetChars > MaxSearchSnippetChars {
		snippetChars = MaxSearchSnippetChars
	}
	order := strings.ToLower(strings.TrimSpace(p.Order))
	if order == "" {
		order = "relevance"
	}
	if order != "relevance" && order != "time" {
		return nil, fmt.Errorf("invalid order %q: expected relevance or time", p.Order)
	}
	// An explicit empty non-nil SessionIDs slice means zero candidate sessions.
	if p.SessionIDs != nil && len(p.SessionIDs) == 0 {
		return []SearchHit{}, nil
	}

	ftsExpr := buildFTSQuery(rawQuery)
	if ftsExpr == "" {
		return []SearchHit{}, nil
	}

	hits, err := s.runSearchQuery(ctx, ftsExpr, rawQuery, p, order, limit, snippetChars)
	if err != nil && isFTSSyntaxError(err) {
		fallback := quoteAllTokensFTS(rawQuery)
		if fallback == "" {
			return []SearchHit{}, nil
		}
		hits, err = s.runSearchQuery(ctx, fallback, rawQuery, p, order, limit, snippetChars)
	}
	return hits, err
}

func (s *Store) runSearchQuery(ctx context.Context, ftsExpr, rawQuery string, p SearchParams, order string, limit, snippetChars int) ([]SearchHit, error) {
	var where []string
	var args []any

	where = append(where, `conv_entries_fts MATCH ?`)
	args = append(args, ftsExpr)

	if p.SessionID != "" {
		where = append(where, `e.session_id = ?`)
		args = append(args, p.SessionID)
	} else if len(p.SessionIDs) > 0 {
		ph := strings.Repeat("?,", len(p.SessionIDs))
		where = append(where, fmt.Sprintf(`e.session_id IN (%s)`, ph[:len(ph)-1]))
		for _, id := range p.SessionIDs {
			args = append(args, id)
		}
	}
	if p.Workspace != "" {
		where = append(where, `e.workspace = ?`)
		args = append(args, p.Workspace)
	}
	if len(p.Kinds) > 0 {
		ph := strings.Repeat("?,", len(p.Kinds))
		where = append(where, fmt.Sprintf(`e.kind IN (%s)`, ph[:len(ph)-1]))
		for _, k := range p.Kinds {
			args = append(args, k)
		}
	}
	if p.SinceMs > 0 {
		where = append(where, `e.ts >= ?`)
		args = append(args, p.SinceMs)
	}
	if p.UntilMs > 0 {
		where = append(where, `e.ts <= ?`)
		args = append(args, p.UntilMs)
	}

	orderClause := `bm25(conv_entries_fts) ASC, e.ts DESC, e.entry_id DESC`
	if order == "time" {
		orderClause = `e.ts DESC, e.entry_id DESC`
	}

	sqlStr := fmt.Sprintf(`
		SELECT e.session_id, e.entry_id, e.workspace, e.kind, e.ts, e.text,
		       e.full_bytes, e.truncated, e.direction, e.peer, e.title,
		       bm25(conv_entries_fts)
		FROM conv_entries_fts
		JOIN conv_entries e ON e.rowid = conv_entries_fts.rowid
		WHERE %s
		ORDER BY %s
		LIMIT ?`, strings.Join(where, " AND "), orderClause)
	args = append(args, limit)

	terms := extractHighlightTerms(rawQuery)
	hits := []SearchHit{}
	err := s.read(ctx, func(db *sql.DB) error {
		// s.read may retry the closure; drop rows from a failed attempt.
		hits = hits[:0]
		rows, err := db.QueryContext(ctx, sqlStr, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h SearchHit
			var text string
			var truncInt int
			if err := rows.Scan(&h.SessionID, &h.EntryID, &h.Workspace, &h.Kind, &h.TS, &text, &h.Bytes, &truncInt, &h.Direction, &h.Peer, &h.Title, &h.Score); err != nil {
				return err
			}
			h.Truncated = truncInt != 0
			h.Snippet = makeSnippet(text, terms, snippetChars)
			hits = append(hits, h)
		}
		return rows.Err()
	})
	return hits, err
}

// HistoryParams configures Store.History.
type HistoryParams struct {
	SessionID    string
	Kinds        []string
	Before       string // exclusive upper entry_id cursor
	After        string // exclusive lower entry_id cursor
	SinceMs      int64
	UntilMs      int64
	Limit        int
	SnippetChars int
}

// HistoryItem is one compact turn/event row returned by Store.History.
type HistoryItem struct {
	EntryID   string `json:"entry_id"`
	TS        int64  `json:"ts"`
	Kind      string `json:"kind"`
	Preview   string `json:"preview"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
	Direction string `json:"direction,omitempty"`
	Peer      string `json:"peer,omitempty"`
	Title     string `json:"title,omitempty"`
}

// HistoryResult is the paginated response from Store.History.
type HistoryResult struct {
	Items   []HistoryItem
	HasMore bool
}

// History returns a bounded chronological window of entries for one session
// directly from the SQLite index without touching ring files.
func (s *Store) History(ctx context.Context, p HistoryParams) (HistoryResult, error) {
	if p.SessionID == "" {
		return HistoryResult{}, fmt.Errorf("session_id is required")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
	}
	snippetChars := p.SnippetChars
	if snippetChars <= 0 {
		snippetChars = DefaultHistorySnippetChars
	}
	if snippetChars > MaxHistorySnippetChars {
		snippetChars = MaxHistorySnippetChars
	}

	var where []string
	var args []any
	where = append(where, `session_id = ?`)
	args = append(args, p.SessionID)

	if len(p.Kinds) > 0 {
		ph := strings.Repeat("?,", len(p.Kinds))
		where = append(where, fmt.Sprintf(`kind IN (%s)`, ph[:len(ph)-1]))
		for _, k := range p.Kinds {
			args = append(args, k)
		}
	}
	if p.Before != "" {
		where = append(where, `entry_id < ?`)
		args = append(args, p.Before)
	}
	if p.After != "" {
		where = append(where, `entry_id > ?`)
		args = append(args, p.After)
	}
	if p.SinceMs > 0 {
		where = append(where, `ts >= ?`)
		args = append(args, p.SinceMs)
	}
	if p.UntilMs > 0 {
		where = append(where, `ts <= ?`)
		args = append(args, p.UntilMs)
	}

	// Forward paging when After is set without Before; otherwise newest window.
	forward := p.After != "" && p.Before == ""
	dirSQL := `DESC`
	if forward {
		dirSQL = `ASC`
	}
	sqlStr := fmt.Sprintf(`
		SELECT entry_id, ts, kind, text, full_bytes, truncated, direction, peer, title
		FROM conv_entries
		WHERE %s
		ORDER BY entry_id %s
		LIMIT ?`, strings.Join(where, " AND "), dirSQL)
	args = append(args, limit+1)

	var items []HistoryItem
	err := s.read(ctx, func(db *sql.DB) error {
		// s.read may retry the closure; drop rows from a failed attempt.
		items = items[:0]
		rows, err := db.QueryContext(ctx, sqlStr, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it HistoryItem
			var text string
			var truncInt int
			if err := rows.Scan(&it.EntryID, &it.TS, &it.Kind, &text, &it.Bytes, &truncInt, &it.Direction, &it.Peer, &it.Title); err != nil {
				return err
			}
			it.Truncated = truncInt != 0
			it.Preview = makePreview(text, snippetChars)
			items = append(items, it)
		}
		return rows.Err()
	})
	if err != nil {
		return HistoryResult{}, err
	}
	hasMore := false
	if len(items) > limit {
		hasMore = true
		items = items[:limit]
	}
	if !forward {
		// Present oldest-to-newest within the page so before/after cursors are natural.
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	if items == nil {
		items = []HistoryItem{}
	}
	return HistoryResult{Items: items, HasMore: hasMore}, nil
}

// EntryRecord is a full indexed entry returned by Read / ReadWindow.
type EntryRecord struct {
	SessionID string `json:"session_id"`
	EntryID   string `json:"entry_id"`
	Kind      string `json:"kind"`
	TS        int64  `json:"ts"`
	Text      string `json:"text"`
	FullBytes int    `json:"bytes"`
	// StoredTruncated is true when the indexed ring entry itself was clipped
	// (either by the ring writer or by the 64 KiB index cap).
	StoredTruncated bool   `json:"truncated,omitempty"`
	Direction       string `json:"direction,omitempty"`
	Peer            string `json:"peer,omitempty"`
	Title           string `json:"title,omitempty"`
}

// ReadResult is a byte-bounded slice of one indexed entry.
type ReadResult struct {
	Entry      EntryRecord
	ByteOffset int
	SliceBytes int
	NextOffset int
	HasMore    bool
}

// ReadWindowResult is a byte-bounded window around one target entry.
type ReadWindowResult struct {
	TargetEntryID string
	Entries       []EntryRecord
	TotalBytes    int
	WindowClipped bool
}

// AmbiguousPrefixError lists candidate entry_ids when a prefix matches more than one entry.
type AmbiguousPrefixError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousPrefixError) Error() string {
	return fmt.Sprintf("ambiguous entry_id prefix %q (matches %s)", e.Prefix, strings.Join(e.Candidates, ", "))
}

func (e *AmbiguousPrefixError) Unwrap() error { return ErrAmbiguousPrefix }

// ReadEntry resolves entryIDOrPrefix (exact or unambiguous prefix) in sessionID
// and returns a UTF-8 safe slice bounded by maxBytes starting at byteOffset.
func (s *Store) ReadEntry(ctx context.Context, sessionID, entryIDOrPrefix string, byteOffset, maxBytes int) (ReadResult, error) {
	if sessionID == "" {
		return ReadResult{}, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(entryIDOrPrefix) == "" {
		return ReadResult{}, fmt.Errorf("entry_id is required")
	}
	if byteOffset < 0 {
		byteOffset = 0
	}
	maxBytes = clampReadMaxBytes(maxBytes)

	var rec EntryRecord
	err := s.read(ctx, func(db *sql.DB) error {
		var rerr error
		rec, rerr = resolveEntryLocked(ctx, db, sessionID, strings.TrimSpace(entryIDOrPrefix))
		return rerr
	})
	if err != nil {
		return ReadResult{}, err
	}
	return sliceEntry(rec, byteOffset, maxBytes), nil
}

// ReadWindow resolves aroundIDOrPrefix and returns up to contextBefore entries
// before it and contextAfter entries after it, bounded in aggregate by maxBytes.
func (s *Store) ReadWindow(ctx context.Context, sessionID, aroundIDOrPrefix string, contextBefore, contextAfter, maxBytes int) (ReadWindowResult, error) {
	if sessionID == "" {
		return ReadWindowResult{}, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(aroundIDOrPrefix) == "" {
		return ReadWindowResult{}, fmt.Errorf("around_entry_id is required")
	}
	if contextBefore < 0 {
		contextBefore = 0
	}
	if contextBefore > MaxWindowContext {
		contextBefore = MaxWindowContext
	}
	if contextAfter < 0 {
		contextAfter = 0
	}
	if contextAfter > MaxWindowContext {
		contextAfter = MaxWindowContext
	}
	maxBytes = clampReadMaxBytes(maxBytes)

	var target EntryRecord
	var beforeList, afterList []EntryRecord
	err := s.read(ctx, func(db *sql.DB) error {
		var rerr error
		target, rerr = resolveEntryLocked(ctx, db, sessionID, strings.TrimSpace(aroundIDOrPrefix))
		if rerr != nil {
			return rerr
		}
		if contextBefore > 0 {
			rows, err := db.QueryContext(ctx, `
				SELECT session_id, entry_id, kind, ts, text, full_bytes, truncated, direction, peer, title
				FROM conv_entries
				WHERE session_id = ? AND entry_id < ?
				ORDER BY entry_id DESC
				LIMIT ?`, sessionID, target.EntryID, contextBefore)
			if err != nil {
				return err
			}
			beforeList, err = scanEntryRecords(rows)
			if err != nil {
				return err
			}
			for i, j := 0, len(beforeList)-1; i < j; i, j = i+1, j-1 {
				beforeList[i], beforeList[j] = beforeList[j], beforeList[i]
			}
		}
		if contextAfter > 0 {
			rows, err := db.QueryContext(ctx, `
				SELECT session_id, entry_id, kind, ts, text, full_bytes, truncated, direction, peer, title
				FROM conv_entries
				WHERE session_id = ? AND entry_id > ?
				ORDER BY entry_id ASC
				LIMIT ?`, sessionID, target.EntryID, contextAfter)
			if err != nil {
				return err
			}
			afterList, err = scanEntryRecords(rows)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ReadWindowResult{}, err
	}

	all := make([]EntryRecord, 0, len(beforeList)+1+len(afterList))
	all = append(all, beforeList...)
	all = append(all, target)
	all = append(all, afterList...)

	remaining := maxBytes
	clipped := false
	out := make([]EntryRecord, 0, len(all))
	totalBytes := 0
	for _, rec := range all {
		if remaining <= 0 {
			clipped = true
			break
		}
		if len(rec.Text) > remaining {
			trimmed, _ := clipUTF8(rec.Text, remaining)
			rec.Text = trimmed
			rec.StoredTruncated = true
			clipped = true
		}
		totalBytes += len(rec.Text)
		remaining -= len(rec.Text)
		out = append(out, rec)
	}
	return ReadWindowResult{
		TargetEntryID: target.EntryID,
		Entries:       out,
		TotalBytes:    totalBytes,
		WindowClipped: clipped,
	}, nil
}

func resolveEntryLocked(ctx context.Context, db *sql.DB, sessionID, idOrPrefix string) (EntryRecord, error) {
	// Exact match first.
	var rec EntryRecord
	var truncInt int
	err := db.QueryRowContext(ctx, `
		SELECT session_id, entry_id, kind, ts, text, full_bytes, truncated, direction, peer, title
		FROM conv_entries
		WHERE session_id = ? AND entry_id = ?`, sessionID, idOrPrefix).
		Scan(&rec.SessionID, &rec.EntryID, &rec.Kind, &rec.TS, &rec.Text, &rec.FullBytes, &truncInt, &rec.Direction, &rec.Peer, &rec.Title)
	if err == nil {
		rec.StoredTruncated = truncInt != 0
		return rec, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EntryRecord{}, err
	}
	// Prefix match (escape LIKE wildcards).
	escaped := escapeLike(idOrPrefix) + "%"
	rows, err := db.QueryContext(ctx, `
		SELECT session_id, entry_id, kind, ts, text, full_bytes, truncated, direction, peer, title
		FROM conv_entries
		WHERE session_id = ? AND entry_id LIKE ? ESCAPE '\'
		ORDER BY entry_id ASC
		LIMIT 3`, sessionID, escaped)
	if err != nil {
		return EntryRecord{}, err
	}
	matches, err := scanEntryRecords(rows)
	if err != nil {
		return EntryRecord{}, err
	}
	switch len(matches) {
	case 0:
		return EntryRecord{}, fmt.Errorf("%w: %s", ErrNotFound, idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.EntryID
		}
		return EntryRecord{}, &AmbiguousPrefixError{Prefix: idOrPrefix, Candidates: ids}
	}
}

func scanEntryRecords(rows *sql.Rows) ([]EntryRecord, error) {
	defer rows.Close()
	var out []EntryRecord
	for rows.Next() {
		var rec EntryRecord
		var truncInt int
		if err := rows.Scan(&rec.SessionID, &rec.EntryID, &rec.Kind, &rec.TS, &rec.Text, &rec.FullBytes, &truncInt, &rec.Direction, &rec.Peer, &rec.Title); err != nil {
			return nil, err
		}
		rec.StoredTruncated = truncInt != 0
		out = append(out, rec)
	}
	return out, rows.Err()
}

func sliceEntry(rec EntryRecord, byteOffset, maxBytes int) ReadResult {
	storedLen := len(rec.Text)
	if byteOffset >= storedLen {
		rec.Text = ""
		return ReadResult{
			Entry:      rec,
			ByteOffset: byteOffset,
			SliceBytes: 0,
			NextOffset: storedLen,
			HasMore:    false,
		}
	}
	// Align start forward to a valid UTF-8 rune start so we never emit broken UTF-8.
	start := byteOffset
	for start < storedLen && !utf8.RuneStart(rec.Text[start]) {
		start++
	}
	end := start + maxBytes
	if end > storedLen {
		end = storedLen
	} else {
		for end > start && !utf8.RuneStart(rec.Text[end]) {
			end--
		}
	}
	rec.Text = rec.Text[start:end]
	hasMore := end < storedLen
	return ReadResult{
		Entry:      rec,
		ByteOffset: start,
		SliceBytes: len(rec.Text),
		NextOffset: end,
		HasMore:    hasMore,
	}
}

func clampReadMaxBytes(maxBytes int) int {
	if maxBytes <= 0 {
		return DefaultReadMaxBytes
	}
	if maxBytes < utf8.UTFMax {
		return utf8.UTFMax
	}
	if maxBytes > MaxReadMaxBytes {
		return MaxReadMaxBytes
	}
	return maxBytes
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func isFTSSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "fts5: syntax error") || strings.Contains(msg, "unterminated string") || strings.Contains(msg, "no such column")
}

// buildFTSQuery translates a user query into safe FTS5 MATCH syntax while
// preserving quoted phrases and explicit boolean operators (AND, OR, NOT).
func buildFTSQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var tokens []string
	i := 0
	for i < len(raw) {
		for i < len(raw) {
			r, size := utf8.DecodeRuneInString(raw[i:])
			if !unicode.IsSpace(r) {
				break
			}
			i += size
		}
		if i >= len(raw) {
			break
		}
		if raw[i] == '"' {
			end := i + 1
			for end < len(raw) && raw[end] != '"' {
				end++
			}
			phrase := strings.TrimSpace(raw[i+1 : end])
			if end < len(raw) {
				i = end + 1
			} else {
				i = end
			}
			if phrase != "" {
				tokens = append(tokens, `"`+strings.ReplaceAll(phrase, `"`, `""`)+`"`)
			}
			continue
		}
		start := i
		for i < len(raw) {
			r, size := utf8.DecodeRuneInString(raw[i:])
			if unicode.IsSpace(r) {
				break
			}
			i += size
		}
		tok := raw[start:i]
		if tok == "AND" || tok == "OR" || tok == "NOT" {
			tokens = append(tokens, tok)
			continue
		}
		if needsFTSQuoting(tok) {
			tokens = append(tokens, `"`+strings.ReplaceAll(tok, `"`, `""`)+`"`)
		} else {
			tokens = append(tokens, tok)
		}
	}
	if len(tokens) == 0 {
		return ""
	}
	// Guard against leading/trailing boolean operators.
	for len(tokens) > 0 && isFTSBoolOp(tokens[0]) {
		tokens = tokens[1:]
	}
	for len(tokens) > 0 && isFTSBoolOp(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

func quoteAllTokensFTS(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, `"`)
		if f == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

func isFTSBoolOp(tok string) bool {
	return tok == "AND" || tok == "OR" || tok == "NOT"
}

func needsFTSQuoting(tok string) bool {
	for _, r := range tok {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			return true
		}
	}
	return false
}

func extractHighlightTerms(raw string) []string {
	var terms []string
	for _, f := range strings.Fields(raw) {
		if isFTSBoolOp(f) {
			continue
		}
		clean := strings.Trim(f, `"'()*`)
		if clean != "" {
			terms = append(terms, strings.ToLower(clean))
		}
	}
	return terms
}

// makeSnippet extracts a single-line, rune-bounded snippet centered around the
// earliest matched term in text.
func makeSnippet(text string, terms []string, maxRunes int) string {
	flat := flattenWhitespace(text)
	if flat == "" {
		return ""
	}
	runes := []rune(flat)
	if len(runes) <= maxRunes {
		return flat
	}
	lowerRunes := make([]rune, len(runes))
	for i, r := range runes {
		lowerRunes[i] = unicode.ToLower(r)
	}
	bestRune := -1
	for _, term := range terms {
		needle := []rune(term)
		if len(needle) == 0 || len(needle) > len(lowerRunes) {
			continue
		}
		for i := range needle {
			needle[i] = unicode.ToLower(needle[i])
		}
		if idx := indexRunes(lowerRunes, needle); idx >= 0 && (bestRune < 0 || idx < bestRune) {
			bestRune = idx
		}
	}
	if bestRune < 0 {
		return string(runes[:maxRunes]) + "..."
	}
	matchRune := bestRune
	start := matchRune - maxRunes/3
	if start < 0 {
		start = 0
	}
	end := start + maxRunes
	if end > len(runes) {
		end = len(runes)
		start = end - maxRunes
		if start < 0 {
			start = 0
		}
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("...")
	}
	b.WriteString(string(runes[start:end]))
	if end < len(runes) {
		b.WriteString("...")
	}
	return b.String()
}

func makePreview(text string, maxRunes int) string {
	flat := flattenWhitespace(text)
	runes := []rune(flat)
	if len(runes) <= maxRunes {
		return flat
	}
	return string(runes[:maxRunes]) + "..."
}

func flattenWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func indexRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	limit := len(haystack) - len(needle)
	for i := 0; i <= limit; i++ {
		if haystack[i] != needle[0] {
			continue
		}
		match := true
		for j := 1; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
