package wsdashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconvidx"
	"github.com/kang-sw/devenv/internal/wslive"
)

const (
	DefaultConvSessionsLimit = 20
	MaxConvSessionsLimit     = 100
)

// ConvSessionsRequest configures Daemon.ConvSessions.
type ConvSessionsRequest struct {
	CallerWorkspace string
	Workspace       string // "" -> CallerWorkspace; "all" -> all workspaces; else path
	Status          string // "active" | "archived" | "closed" | "all" (default "all")
	IncludeChildren bool
	Query           string
	SinceMs         int64
	UntilMs         int64
	Limit           int
	Offset          int
}

// ConvSessionItem is one metadata-only row returned by ConvSessions.
type ConvSessionItem struct {
	SessionID    string `json:"session_id"`
	Label        string `json:"label,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	Status       string `json:"status"` // "active" | "archived" | "closed"
	Harness      string `json:"harness,omitempty"`
	Model        string `json:"model,omitempty"`
	Parent       string `json:"parent,omitempty"`
	IsChild      bool   `json:"is_child,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
	EntryCount   int    `json:"entry_count"`
}

// ConvSessionsResponse is returned by Daemon.ConvSessions.
type ConvSessionsResponse struct {
	Sessions []ConvSessionItem `json:"sessions"`
	Total    int               `json:"total"`
	Offset   int               `json:"offset"`
	Limit    int               `json:"limit"`
	Indexing string            `json:"indexing,omitempty"` // "partial" when any matching session is not yet indexed
}

// ConvSearchRequest configures Daemon.ConvSearch.
type ConvSearchRequest struct {
	CallerWorkspace string
	Query           string
	SessionID       string
	Workspace       string // "" -> CallerWorkspace; "all" -> all workspaces; else path
	Status          string // "active" | "archived" | "closed" | "all" (default "all")
	Kinds           []string
	SinceMs         int64
	UntilMs         int64
	Order           string // "relevance" | "time"
	Limit           int
	SnippetChars    int
}

// ConvSearchHit enriches a wsconvidx.SearchHit with session label and status.
type ConvSearchHit struct {
	SessionID     string `json:"session_id"`
	SessionLabel  string `json:"session_label,omitempty"`
	SessionStatus string `json:"session_status,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	EntryID       string `json:"entry_id"`
	Kind          string `json:"kind"`
	TS            int64  `json:"ts"`
	Snippet       string `json:"snippet"`
	Bytes         int    `json:"bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	Direction     string `json:"direction,omitempty"`
	Peer          string `json:"peer,omitempty"`
	Title         string `json:"title,omitempty"`
}

// ConvSearchResponse is returned by Daemon.ConvSearch.
type ConvSearchResponse struct {
	Hits     []ConvSearchHit `json:"hits"`
	Indexing string          `json:"indexing,omitempty"` // "partial" when any candidate session is not yet indexed
}

// ConvHistoryRequest configures Daemon.ConvHistory.
type ConvHistoryRequest struct {
	SessionID    string
	Kinds        []string
	Before       string
	After        string
	SinceMs      int64
	UntilMs      int64
	Limit        int
	SnippetChars int
}

// ConvHistoryResponse is returned by Daemon.ConvHistory.
type ConvHistoryResponse struct {
	SessionID string                  `json:"session_id"`
	Items     []wsconvidx.HistoryItem `json:"items"`
	HasMore   bool                    `json:"has_more"`
}

// ConvReadRequest configures Daemon.ConvRead.
type ConvReadRequest struct {
	SessionID     string
	EntryID       string
	AroundEntryID string
	ContextBefore int
	ContextAfter  int
	HasBefore     bool
	HasAfter      bool
	ByteOffset    int
	MaxBytes      int
}

// ConvReadResponse is returned by Daemon.ConvRead (either single Entry or Window).
type ConvReadResponse struct {
	SessionID     string                  `json:"session_id"`
	Mode          string                  `json:"mode"` // "entry" | "window"
	Entry         *wsconvidx.EntryRecord  `json:"entry,omitempty"`
	ByteOffset    int                     `json:"byte_offset,omitempty"`
	SliceBytes    int                     `json:"slice_bytes,omitempty"`
	NextOffset    int                     `json:"next_offset,omitempty"`
	HasMore       bool                    `json:"has_more,omitempty"`
	TargetEntryID string                  `json:"target_entry_id,omitempty"`
	Entries       []wsconvidx.EntryRecord `json:"entries,omitempty"`
	TotalBytes    int                     `json:"total_bytes,omitempty"`
	WindowClipped bool                    `json:"window_clipped,omitempty"`
}

// ConvRevealRequest configures Daemon.ConvRevealSession.
type ConvRevealRequest struct {
	SessionID string
	EntryID   string
	Revive    bool
	Focus     bool
	Reason    string
}

// ConvRevealResponse is returned by Daemon.ConvRevealSession.
type ConvRevealResponse struct {
	SessionID string `json:"session_id"`
	EntryID   string `json:"entry_id,omitempty"`
	Status    string `json:"status"`
	Revived   bool   `json:"revived,omitempty"`
	Focused   bool   `json:"focused,omitempty"`
	Emitted   bool   `json:"emitted"`
}

type convRingDelta struct {
	sessionID string
	added     []wslive.RingRecord
	evicted   []string
}

// ConvIndex returns the daemon's held SQLite FTS5 conversation index (or nil).
func (d *Daemon) ConvIndex() *wsconvidx.Store {
	if d == nil {
		return nil
	}
	return d.convIdx
}

func (d *Daemon) openConvIndex(logw io.Writer) {
	store, err := wsconvidx.Open(d.dir, wsconvidx.Options{
		Logf: func(format string, args ...any) {
			if logw != nil {
				fmt.Fprintf(logw, format+"\n", args...)
			}
		},
	})
	if err != nil {
		if logw != nil {
			fmt.Fprintf(logw, "dashboard: open conversation index: %v\n", err)
		}
		return
	}
	d.convIdx = store
	d.convDeltaWake = make(chan struct{}, 1)
	d.convDeltaFlush = make(chan chan struct{})
	d.convDeltaStop = make(chan struct{})
	d.convDeltaDone = make(chan struct{})
	d.convBackfillInFlight = map[string]struct{}{}
	d.convNonHostedReconciled = map[string]bool{}
	d.stopRingObserver = wslive.RegisterRingObserver(d.onRingDelta)
	go d.convDeltaLoop()
}

func (d *Daemon) closeConvIndex() {
	if d == nil || d.convIdx == nil {
		return
	}
	if d.stopRingObserver != nil {
		d.stopRingObserver()
		d.stopRingObserver = nil
	}
	if d.convDeltaStop != nil {
		close(d.convDeltaStop)
		<-d.convDeltaDone
	}
	d.convBackfillWG.Wait()
	if err := d.convIdx.Close(); err != nil && d.log != nil {
		fmt.Fprintf(d.log, "dashboard: conversation index close: %v\n", err)
	}
}

func (d *Daemon) replyIDFromRingDir(ringDir string) (string, bool) {
	sessionsRoot := filepath.Clean(filepath.Join(d.dir, sessionsDirName))
	cleaned := filepath.Clean(ringDir)
	rel, err := filepath.Rel(sessionsRoot, cleaned)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 || parts[1] != "ring" || !ValidReplyID(parts[0]) {
		return "", false
	}
	return parts[0], true
}

func (d *Daemon) onRingDelta(ringDir string, added []wslive.RingRecord, evicted []string) {
	if d == nil || d.convIdx == nil || (len(added) == 0 && len(evicted) == 0) {
		return
	}
	sessionID, ok := d.replyIDFromRingDir(ringDir)
	if !ok {
		return
	}
	// Copy slices so the caller's buffers cannot mutate after returning.
	addedCopy := append([]wslive.RingRecord(nil), added...)
	evictedCopy := append([]string(nil), evicted...)
	d.convDeltaMu.Lock()
	d.convDeltaQueue = append(d.convDeltaQueue, convRingDelta{
		sessionID: sessionID,
		added:     addedCopy,
		evicted:   evictedCopy,
	})
	d.convDeltaMu.Unlock()
	select {
	case d.convDeltaWake <- struct{}{}:
	default:
	}
}

func (d *Daemon) convDeltaLoop() {
	defer close(d.convDeltaDone)
	drain := func() {
		for {
			d.convDeltaMu.Lock()
			if len(d.convDeltaQueue) == 0 {
				d.convDeltaMu.Unlock()
				return
			}
			batch := d.convDeltaQueue
			d.convDeltaQueue = nil
			d.convDeltaMu.Unlock()
			for _, item := range batch {
				ws := d.sessionWorkspace(item.sessionID)
				if err := d.convIdx.ApplyDelta(context.Background(), item.sessionID, ws, item.added, item.evicted); err != nil {
					if !errors.Is(err, wsconvidx.ErrClosed) && d.log != nil {
						fmt.Fprintf(d.log, "dashboard: conv index delta %s: %v\n", item.sessionID, err)
					}
				}
			}
		}
	}
	for {
		select {
		case <-d.convDeltaWake:
			drain()
		case done := <-d.convDeltaFlush:
			drain()
			close(done)
		case <-d.convDeltaStop:
			drain()
			return
		}
	}
}

// FlushConvIndexDeltas waits for all currently queued ring deltas to be
// committed into convIdx. Intended for tests and deterministic checkpoints.
func (d *Daemon) FlushConvIndexDeltas() {
	if d == nil || d.convIdx == nil || d.convDeltaFlush == nil {
		return
	}
	done := make(chan struct{})
	select {
	case d.convDeltaFlush <- done:
		<-done
	case <-d.convDeltaDone:
	}
}

func (d *Daemon) deleteConvIndexSession(replyID string) {
	if d == nil || d.convIdx == nil || !ValidReplyID(replyID) {
		return
	}
	d.convBackfillMu.Lock()
	delete(d.convNonHostedReconciled, replyID)
	d.convBackfillMu.Unlock()
	if err := d.convIdx.DeleteSession(context.Background(), replyID); err != nil && !errors.Is(err, wsconvidx.ErrClosed) && d.log != nil {
		fmt.Fprintf(d.log, "dashboard: delete conv index %s: %v\n", replyID, err)
	}
}

func (d *Daemon) sessionWorkspace(replyID string) string {
	if d.index != nil {
		if sess, ok := d.index.Session(replyID); ok && sess.Project != "" {
			return filepath.Clean(sess.Project)
		}
	}
	if snap, ok := d.agentRecord(replyID); ok && snap.Root != "" {
		return filepath.Clean(snap.Root)
	}
	return ""
}

// scheduleConvBackfill starts the one-time daemon-startup background backfill
// for sessions with no high-water mark, and removes any orphaned conv_sessions
// rows whose session directories were pruned while no daemon was running.
func (d *Daemon) scheduleConvBackfill() {
	if d == nil || d.convIdx == nil {
		return
	}
	d.convBackfillOnce.Do(func() {
		d.convBackfillWG.Add(1)
		go func() {
			defer d.convBackfillWG.Done()
			d.runStartupConvBackfill()
		}()
	})
}

func (d *Daemon) runStartupConvBackfill() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-d.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Collect known sessions from the index (plus directory scan fallback).
	candidates := map[string]string{} // sessionID -> workspace
	if d.index != nil {
		for _, s := range d.index.Sessions() {
			candidates[s.ReplyID] = normalizeWorkspace(s.Project)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(d.dir, sessionsDirName)); err == nil {
		for _, ent := range entries {
			if !ent.IsDir() || !ValidReplyID(ent.Name()) {
				continue
			}
			if _, ok := candidates[ent.Name()]; !ok {
				candidates[ent.Name()] = d.sessionWorkspace(ent.Name())
			}
		}
	}

	stats, err := d.convIdx.SessionStats(ctx, nil)
	if err != nil {
		return
	}
	// Clean up orphaned index rows for directories pruned offline.
	for id := range stats {
		if _, exists := candidates[id]; !exists {
			if _, statErr := os.Stat(SessionDir(d.dir, id)); errors.Is(statErr, os.ErrNotExist) {
				_ = d.convIdx.DeleteSession(ctx, id)
			}
		}
	}

	for id, ws := range candidates {
		if ctx.Err() != nil {
			return
		}
		if st, ok := stats[id]; ok && st.Indexed {
			continue
		}
		d.reconcileConvSession(ctx, id, ws)
	}
}

func (d *Daemon) triggerAsyncBackfill(targets []convSessionSnapshot) {
	if d == nil || d.convIdx == nil || len(targets) == 0 {
		return
	}
	var toRun []convSessionSnapshot
	d.convBackfillMu.Lock()
	for _, t := range targets {
		if _, busy := d.convBackfillInFlight[t.id]; busy {
			continue
		}
		d.convBackfillInFlight[t.id] = struct{}{}
		toRun = append(toRun, t)
	}
	d.convBackfillMu.Unlock()
	if len(toRun) == 0 {
		return
	}
	d.convBackfillWG.Add(1)
	go func() {
		defer d.convBackfillWG.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-d.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		for _, t := range toRun {
			if ctx.Err() == nil {
				d.reconcileConvSession(ctx, t.id, t.workspace)
			}
			d.convBackfillMu.Lock()
			delete(d.convBackfillInFlight, t.id)
			d.convBackfillMu.Unlock()
		}
	}()
}

// reconcileNonHostedOnRead reconciles an unpinned non-hosted session's ring
// against its high-water mark when ReadSession refreshes it (decision 1645).
// Archived and closed sessions have no writers and are skipped once indexed.
func (d *Daemon) reconcileNonHostedOnRead(replyID string) {
	if d == nil || d.convIdx == nil || !ValidReplyID(replyID) {
		return
	}
	if d.index != nil {
		if s, ok := d.index.Session(replyID); ok {
			status := classifyConvStatus(s, AgentRecordSnapshot{}, false)
			if status != "active" {
				return
			}
		}
	}
	d.convBackfillMu.Lock()
	if _, busy := d.convBackfillInFlight[replyID]; busy {
		d.convBackfillMu.Unlock()
		return
	}
	d.convBackfillInFlight[replyID] = struct{}{}
	d.convBackfillMu.Unlock()
	d.convBackfillWG.Add(1)
	go func() {
		defer d.convBackfillWG.Done()
		defer func() {
			d.convBackfillMu.Lock()
			delete(d.convBackfillInFlight, replyID)
			d.convBackfillMu.Unlock()
		}()
		select {
		case <-d.stop:
			return
		default:
		}
		d.reconcileConvSession(context.Background(), replyID, d.sessionWorkspace(replyID))
	}()
}

func (d *Daemon) reconcileConvSession(ctx context.Context, sessionID, workspace string) error {
	if d == nil || d.convIdx == nil || !ValidReplyID(sessionID) {
		return nil
	}
	if workspace == "" {
		workspace = d.sessionWorkspace(sessionID)
	}
	ringDir := wslive.RingDir(SessionDir(d.dir, sessionID))
	recs, err := wslive.ReadRingRecords(ringDir)
	if err != nil {
		return err
	}
	return d.convIdx.ReconcileSession(ctx, sessionID, workspace, recs)
}

func (d *Daemon) ensureConvSessionIndexed(ctx context.Context, sessionID string) error {
	if d == nil || d.convIdx == nil {
		return errors.New("conversation index is unavailable")
	}
	indexed, _, err := d.convIdx.SessionIndexed(ctx, sessionID)
	if err != nil {
		return err
	}
	ws := d.sessionWorkspace(sessionID)
	if !indexed {
		return d.reconcileConvSession(ctx, sessionID, ws)
	}
	// Decision (1645): unpinned active non-hosted sessions reconcile on their
	// first conv.* touch against the high-water mark, never on every call.
	// Archived and closed sessions have no writers and are not re-reconciled.
	if !d.live.hasOwner(sessionID) {
		if d.isActiveNonHosted(sessionID) {
			d.convBackfillMu.Lock()
			already := d.convNonHostedReconciled[sessionID]
			d.convBackfillMu.Unlock()
			if !already {
				if err := d.reconcileConvSession(ctx, sessionID, ws); err != nil {
					return err
				}
				d.convBackfillMu.Lock()
				d.convNonHostedReconciled[sessionID] = true
				d.convBackfillMu.Unlock()
			}
		}
	}
	return nil
}

func (d *Daemon) isActiveNonHosted(sessionID string) bool {
	if d.live.hasOwner(sessionID) {
		return false
	}
	snap, hasSnap := d.agentRecord(sessionID)
	if hasSnap {
		// Hosted agent conversations are driven through wslive observer hooks.
		return false
	}
	if d.index != nil {
		if s, ok := d.index.Session(sessionID); ok {
			return classifyConvStatus(s, snap, hasSnap) == "active"
		}
	}
	return false
}

// ConvHistoryAccess reports the live persisted conv_history_access flag for
// replyID from memory (zero disk I/O).
func (d *Daemon) ConvHistoryAccess(replyID string) bool {
	if d == nil || !ValidReplyID(replyID) {
		return false
	}
	if host, ok := d.agents.(AgentConvHost); ok {
		return host.ConvHistoryAccess(replyID)
	}
	if snap, ok := d.agentRecord(replyID); ok {
		return snap.Lead && snap.ConvHistoryAccess
	}
	return false
}

type convSessionSnapshot struct {
	id           string
	label        string
	workspace    string
	status       string // "active" | "archived" | "closed"
	harness      string
	model        string
	parent       string
	isChild      bool
	isHosted     bool
	createdAt    string
	lastActiveAt string
	sortMS       int64
}

func (d *Daemon) collectConvSessions() []convSessionSnapshot {
	if d == nil {
		return nil
	}
	var indexed []Session
	if d.index != nil {
		indexed = d.index.Sessions()
	}
	metaHost, _ := d.agents.(AgentSessionMetaHost)
	out := make([]convSessionSnapshot, 0, len(indexed))
	for _, s := range indexed {
		snap, hasSnap := d.agentRecord(s.ReplyID)
		meta := AgentSessionMeta{}
		hasMeta := false
		if metaHost != nil {
			meta, hasMeta = metaHost.SessionMeta(s.ReplyID)
		}
		item := convSessionSnapshot{
			id:           s.ReplyID,
			label:        s.Label,
			workspace:    normalizeWorkspace(s.Project),
			status:       classifyConvStatus(s, snap, hasSnap),
			harness:      s.Harness,
			createdAt:    s.RegisteredAt,
			lastActiveAt: latestTimestamp(s.LastCallAt, s.HeartbeatAt, s.EndedAt, s.RegisteredAt),
			isHosted:     s.Hosted || s.Agent || hasSnap || d.live.hasOwner(s.ReplyID),
		}
		if item.workspace == "" && hasSnap {
			item.workspace = normalizeWorkspace(snap.Root)
		}
		if hasSnap {
			if snap.Backend != "" {
				item.harness = snap.Backend
			}
			if snap.Model != "" {
				item.model = snap.Model
			}
			if snap.Parent != nil {
				item.parent = snap.Parent.ReplyID
				item.isChild = true
			} else if !snap.Lead {
				item.isChild = true
			}
		} else if s.Agent {
			item.isChild = true
		}
		if d.cardBound != nil && d.cardBound(s.ReplyID) {
			item.isChild = true
		}
		if hasMeta {
			if meta.Backend != "" {
				item.harness = meta.Backend
			}
			if meta.Model != "" {
				item.model = meta.Model
			}
			if meta.IsChild {
				item.isChild = true
			}
			if meta.LastActiveAt != "" {
				item.lastActiveAt = latestTimestamp(item.lastActiveAt, meta.LastActiveAt)
			}
		}
		if !s.SnapshotUpdatedAt.IsZero() {
			item.lastActiveAt = latestTimestamp(item.lastActiveAt, s.SnapshotUpdatedAt.UTC().Format(time.RFC3339))
		}
		if t, ok := parseAnyTimestamp(item.lastActiveAt); ok {
			item.sortMS = t.UnixMilli()
		} else if t, ok := parseAnyTimestamp(item.createdAt); ok {
			item.sortMS = t.UnixMilli()
		}
		out = append(out, item)
	}
	return out
}

func classifyConvStatus(s Session, snap AgentRecordSnapshot, hasSnap bool) string {
	if s.EndedAt != "" || s.Lifecycle == SessionPendingDeletion || (hasSnap && snap.ClosedAt != "") {
		return "closed"
	}
	if s.Hidden || s.Lifecycle == SessionArchived {
		return "archived"
	}
	return "active"
}

func normalizeWorkspace(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func workspaceMatches(sessionWS, filterWS string) bool {
	if filterWS == "" || strings.EqualFold(filterWS, "all") {
		return true
	}
	return normalizeWorkspace(sessionWS) == normalizeWorkspace(filterWS)
}

func parseAnyTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func latestTimestamp(values ...string) string {
	var best string
	var bestT time.Time
	for _, v := range values {
		t, ok := parseAnyTimestamp(v)
		if !ok {
			continue
		}
		if best == "" || t.After(bestT) {
			best = t.UTC().Format(time.RFC3339)
			bestT = t
		}
	}
	return best
}

// ConvSessions implements the backend for conv.sessions.
func (d *Daemon) ConvSessions(ctx context.Context, req ConvSessionsRequest) (ConvSessionsResponse, error) {
	if d == nil || d.convIdx == nil {
		return ConvSessionsResponse{}, errors.New("conversation index is unavailable")
	}
	statusFilter := strings.ToLower(strings.TrimSpace(req.Status))
	if statusFilter == "" {
		statusFilter = "all"
	}
	switch statusFilter {
	case "active", "archived", "closed", "all":
	default:
		return ConvSessionsResponse{}, fmt.Errorf("invalid status %q: expected active, archived, closed, or all", req.Status)
	}
	wsFilter := strings.TrimSpace(req.Workspace)
	if wsFilter == "" {
		wsFilter = strings.TrimSpace(req.CallerWorkspace)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultConvSessionsLimit
	}
	if limit > MaxConvSessionsLimit {
		limit = MaxConvSessionsLimit
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	q := strings.ToLower(strings.TrimSpace(req.Query))

	all := d.collectConvSessions()
	matched := make([]convSessionSnapshot, 0, len(all))
	ids := make([]string, 0, len(all))
	for _, s := range all {
		if !req.IncludeChildren && s.isChild {
			continue
		}
		if !workspaceMatches(s.workspace, wsFilter) {
			continue
		}
		if statusFilter != "all" && s.status != statusFilter {
			continue
		}
		if q != "" {
			if !strings.Contains(strings.ToLower(s.id), q) &&
				!strings.Contains(strings.ToLower(s.label), q) &&
				!strings.Contains(strings.ToLower(s.workspace), q) &&
				!strings.Contains(strings.ToLower(s.harness), q) &&
				!strings.Contains(strings.ToLower(s.model), q) {
				continue
			}
		}
		if req.SinceMs > 0 && s.sortMS > 0 && s.sortMS < req.SinceMs {
			continue
		}
		if req.UntilMs > 0 && s.sortMS > 0 && s.sortMS > req.UntilMs {
			continue
		}
		matched = append(matched, s)
		ids = append(ids, s.id)
	}

	stats := map[string]wsconvidx.SessionStat{}
	if len(ids) > 0 {
		var err error
		stats, err = d.convIdx.SessionStats(ctx, ids)
		if err != nil {
			return ConvSessionsResponse{}, err
		}
	}

	var unindexed []convSessionSnapshot
	for i := range matched {
		st, ok := stats[matched[i].id]
		if !ok || !st.Indexed {
			unindexed = append(unindexed, matched[i])
		} else if st.LastTS > matched[i].sortMS {
			matched[i].sortMS = st.LastTS
			matched[i].lastActiveAt = time.UnixMilli(st.LastTS).UTC().Format(time.RFC3339)
		}
	}
	indexing := ""
	if len(unindexed) > 0 {
		indexing = "partial"
		d.triggerAsyncBackfill(unindexed)
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].sortMS != matched[j].sortMS {
			return matched[i].sortMS > matched[j].sortMS
		}
		return matched[i].id < matched[j].id
	})

	total := len(matched)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := matched[offset:end]
	items := make([]ConvSessionItem, 0, len(page))
	for _, s := range page {
		items = append(items, ConvSessionItem{
			SessionID:    s.id,
			Label:        s.label,
			Workspace:    s.workspace,
			Status:       s.status,
			Harness:      s.harness,
			Model:        s.model,
			Parent:       s.parent,
			IsChild:      s.isChild,
			CreatedAt:    s.createdAt,
			LastActiveAt: s.lastActiveAt,
			EntryCount:   stats[s.id].EntryCount,
		})
	}
	return ConvSessionsResponse{
		Sessions: items,
		Total:    total,
		Offset:   offset,
		Limit:    limit,
		Indexing: indexing,
	}, nil
}

// ConvSearch implements the backend for conv.search.
func (d *Daemon) ConvSearch(ctx context.Context, req ConvSearchRequest) (ConvSearchResponse, error) {
	if d == nil || d.convIdx == nil {
		return ConvSearchResponse{}, errors.New("conversation index is unavailable")
	}
	if req.SessionID != "" && !ValidReplyID(req.SessionID) {
		return ConvSearchResponse{}, fmt.Errorf("invalid session_id %q", req.SessionID)
	}
	statusFilter := strings.ToLower(strings.TrimSpace(req.Status))
	if statusFilter == "" {
		statusFilter = "all"
	}
	switch statusFilter {
	case "active", "archived", "closed", "all":
	default:
		return ConvSearchResponse{}, fmt.Errorf("invalid status %q: expected active, archived, closed, or all", req.Status)
	}
	wsFilter := strings.TrimSpace(req.Workspace)
	if wsFilter == "" && req.SessionID == "" {
		wsFilter = strings.TrimSpace(req.CallerWorkspace)
	}

	all := d.collectConvSessions()
	byID := make(map[string]convSessionSnapshot, len(all))
	var candidateIDs []string
	var candidates []convSessionSnapshot
	for _, s := range all {
		byID[s.id] = s
		if req.SessionID != "" {
			if s.id != req.SessionID {
				continue
			}
		} else {
			if !workspaceMatches(s.workspace, wsFilter) {
				continue
			}
			if statusFilter != "all" && s.status != statusFilter {
				continue
			}
		}
		candidateIDs = append(candidateIDs, s.id)
		candidates = append(candidates, s)
	}

	indexing := ""
	if len(candidateIDs) > 0 {
		stats, err := d.convIdx.SessionStats(ctx, candidateIDs)
		if err != nil {
			return ConvSearchResponse{}, err
		}
		var unindexed []convSessionSnapshot
		for _, c := range candidates {
			st, ok := stats[c.id]
			if !ok || !st.Indexed {
				unindexed = append(unindexed, c)
			} else if !c.isHosted && c.status == "active" {
				// First conv.* touch of an active non-hosted session reconciles
				// receiver-process writes against the high-water mark once.
				d.convBackfillMu.Lock()
				already := d.convNonHostedReconciled[c.id]
				d.convBackfillMu.Unlock()
				if !already {
					if err := d.reconcileConvSession(ctx, c.id, c.workspace); err == nil {
						d.convBackfillMu.Lock()
						d.convNonHostedReconciled[c.id] = true
						d.convBackfillMu.Unlock()
					}
				}
			}
		}
		if len(unindexed) > 0 {
			indexing = "partial"
			d.triggerAsyncBackfill(unindexed)
		}
	} else if req.SessionID != "" {
		// Session not in index; check if it's indexed directly (e.g. unit test).
		indexed, _, err := d.convIdx.SessionIndexed(ctx, req.SessionID)
		if err == nil && !indexed {
			if _, statErr := os.Stat(SessionDir(d.dir, req.SessionID)); statErr == nil {
				indexing = "partial"
				d.triggerAsyncBackfill([]convSessionSnapshot{{id: req.SessionID, workspace: d.sessionWorkspace(req.SessionID)}})
			}
		}
	}

	params := wsconvidx.SearchParams{
		Query:        req.Query,
		SessionID:    req.SessionID,
		Kinds:        req.Kinds,
		SinceMs:      req.SinceMs,
		UntilMs:      req.UntilMs,
		Order:        req.Order,
		Limit:        req.Limit,
		SnippetChars: req.SnippetChars,
	}
	if req.SessionID == "" {
		if candidateIDs == nil {
			candidateIDs = []string{}
		}
		params.SessionIDs = candidateIDs
	}

	rawHits, err := d.convIdx.Search(ctx, params)
	if err != nil {
		return ConvSearchResponse{}, err
	}
	hits := make([]ConvSearchHit, 0, len(rawHits))
	for _, h := range rawHits {
		sess := byID[h.SessionID]
		ws := h.Workspace
		if ws == "" {
			ws = sess.workspace
		}
		hits = append(hits, ConvSearchHit{
			SessionID:     h.SessionID,
			SessionLabel:  sess.label,
			SessionStatus: sess.status,
			Workspace:     ws,
			EntryID:       h.EntryID,
			Kind:          h.Kind,
			TS:            h.TS,
			Snippet:       h.Snippet,
			Bytes:         h.Bytes,
			Truncated:     h.Truncated,
			Direction:     h.Direction,
			Peer:          h.Peer,
			Title:         h.Title,
		})
	}
	return ConvSearchResponse{Hits: hits, Indexing: indexing}, nil
}

// ConvHistory implements the backend for conv.history.
func (d *Daemon) ConvHistory(ctx context.Context, req ConvHistoryRequest) (ConvHistoryResponse, error) {
	if d == nil || d.convIdx == nil {
		return ConvHistoryResponse{}, errors.New("conversation index is unavailable")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if !ValidReplyID(sessionID) {
		return ConvHistoryResponse{}, fmt.Errorf("invalid session_id %q", req.SessionID)
	}
	if !d.sessionExists(ctx, sessionID) {
		return ConvHistoryResponse{}, fmt.Errorf("session %q not found", sessionID)
	}
	if err := d.ensureConvSessionIndexed(ctx, sessionID); err != nil {
		return ConvHistoryResponse{}, err
	}
	res, err := d.convIdx.History(ctx, wsconvidx.HistoryParams{
		SessionID:    sessionID,
		Kinds:        req.Kinds,
		Before:       strings.TrimSpace(req.Before),
		After:        strings.TrimSpace(req.After),
		SinceMs:      req.SinceMs,
		UntilMs:      req.UntilMs,
		Limit:        req.Limit,
		SnippetChars: req.SnippetChars,
	})
	if err != nil {
		return ConvHistoryResponse{}, err
	}
	return ConvHistoryResponse{
		SessionID: sessionID,
		Items:     res.Items,
		HasMore:   res.HasMore,
	}, nil
}

// ConvRead implements the backend for conv.read.
func (d *Daemon) ConvRead(ctx context.Context, req ConvReadRequest) (ConvReadResponse, error) {
	if d == nil || d.convIdx == nil {
		return ConvReadResponse{}, errors.New("conversation index is unavailable")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if !ValidReplyID(sessionID) {
		return ConvReadResponse{}, fmt.Errorf("invalid session_id %q", req.SessionID)
	}
	if !d.sessionExists(ctx, sessionID) {
		return ConvReadResponse{}, fmt.Errorf("session %q not found", sessionID)
	}
	if err := d.ensureConvSessionIndexed(ctx, sessionID); err != nil {
		return ConvReadResponse{}, err
	}
	entryID := strings.TrimSpace(req.EntryID)
	aroundID := strings.TrimSpace(req.AroundEntryID)
	if entryID != "" && aroundID != "" {
		return ConvReadResponse{}, errors.New("specify either entry_id or around_entry_id, not both")
	}
	if entryID == "" && aroundID == "" {
		return ConvReadResponse{}, errors.New("entry_id or around_entry_id is required")
	}
	if aroundID != "" {
		before := wsconvidx.DefaultWindowBefore
		if req.HasBefore {
			before = req.ContextBefore
		}
		after := wsconvidx.DefaultWindowAfter
		if req.HasAfter {
			after = req.ContextAfter
		}
		res, err := d.convIdx.ReadWindow(ctx, sessionID, aroundID, before, after, req.MaxBytes)
		if err != nil {
			return ConvReadResponse{}, err
		}
		return ConvReadResponse{
			SessionID:     sessionID,
			Mode:          "window",
			TargetEntryID: res.TargetEntryID,
			Entries:       res.Entries,
			TotalBytes:    res.TotalBytes,
			WindowClipped: res.WindowClipped,
		}, nil
	}
	res, err := d.convIdx.ReadEntry(ctx, sessionID, entryID, req.ByteOffset, req.MaxBytes)
	if err != nil {
		return ConvReadResponse{}, err
	}
	entry := res.Entry
	return ConvReadResponse{
		SessionID:  sessionID,
		Mode:       "entry",
		Entry:      &entry,
		ByteOffset: res.ByteOffset,
		SliceBytes: res.SliceBytes,
		NextOffset: res.NextOffset,
		HasMore:    res.HasMore,
	}, nil
}

// ConvRevealSession implements the backend for conv.reveal.
func (d *Daemon) ConvRevealSession(ctx context.Context, req ConvRevealRequest) (ConvRevealResponse, error) {
	if d == nil {
		return ConvRevealResponse{}, errors.New("dashboard daemon is unavailable")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if !ValidReplyID(sessionID) {
		return ConvRevealResponse{}, fmt.Errorf("invalid session_id %q", req.SessionID)
	}
	var (
		sess    Session
		hasSess bool
	)
	if d.index != nil {
		sess, hasSess = d.index.Session(sessionID)
	}
	snap, hasSnap := d.agentRecord(sessionID)
	if !hasSess && !hasSnap {
		return ConvRevealResponse{}, fmt.Errorf("session %q not found (it may have been pruned after 7 days)", sessionID)
	}
	isChild := (hasSnap && (!snap.Lead || snap.Parent != nil)) ||
		(!hasSnap && hasSess && sess.Agent) ||
		(d.cardBound != nil && d.cardBound(sessionID))
	status := classifyConvStatus(sess, snap, hasSnap)

	revived := false
	if req.Revive {
		if isChild {
			return ConvRevealResponse{}, fmt.Errorf("cannot revive child/worker session %q: only top-level lead sessions can be revived", sessionID)
		}
		if status == "archived" || status == "closed" {
			host, ok := d.agents.(AgentConvHost)
			if !ok {
				return ConvRevealResponse{}, errors.New("agent runner does not support lead revival")
			}
			if err := host.ReviveLead(sessionID); err != nil {
				return ConvRevealResponse{}, fmt.Errorf("revive session %q: %w", sessionID, err)
			}
			revived = true
			status = "active"
			if d.index != nil {
				d.index.Reread(sessionID)
			}
		}
	} else {
		if status == "archived" {
			return ConvRevealResponse{}, fmt.Errorf("session %q is archived; call conv.reveal with revive: true to unhide and reveal it", sessionID)
		}
		if status == "closed" {
			return ConvRevealResponse{}, fmt.Errorf("session %q is closed; call conv.reveal with revive: true to reopen and reveal it", sessionID)
		}
	}

	emitted := d.PublishConvReveal(ConvReveal{
		ReplyID: sessionID,
		EntryID: strings.TrimSpace(req.EntryID),
		Focus:   req.Focus,
		Reason:  strings.TrimSpace(req.Reason),
	})
	return ConvRevealResponse{
		SessionID: sessionID,
		EntryID:   strings.TrimSpace(req.EntryID),
		Status:    status,
		Revived:   revived,
		Focused:   req.Focus,
		Emitted:   emitted,
	}, nil
}

func (d *Daemon) sessionExists(ctx context.Context, sessionID string) bool {
	if d.index != nil {
		if _, ok := d.index.Session(sessionID); ok {
			return true
		}
	}
	if _, ok := d.agentRecord(sessionID); ok {
		return true
	}
	if d.convIdx != nil {
		if indexed, _, err := d.convIdx.SessionIndexed(ctx, sessionID); err == nil && indexed {
			return true
		}
	}
	if _, err := os.Stat(SessionDir(d.dir, sessionID)); err == nil {
		return true
	}
	return false
}
