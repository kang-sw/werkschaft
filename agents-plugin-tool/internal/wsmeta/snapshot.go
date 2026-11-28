package wsmeta

import (
	"context"
	"encoding/json"
	"fmt"
)

// Snapshot is every imported or live bundled store, read once at daemon
// startup: owners serve their reads from memory afterwards (performance.md
// Rule 1). Raw JSON fields keep every member the documents carried.
type Snapshot struct {
	Records      map[string]AgentRecord
	Permissions  map[string]Permission
	Labels       map[string]string
	AckChecks    map[string]json.RawMessage
	Hiring       map[string]Hiring
	Support      Support
	PiProbes     map[PiProbeKey]PiProbe
	RailLayout   *RailLayout // nil when no layout was imported
	LatestUsage  map[string]LatestUsage
	UsageHistory map[string]UsageHistory
	Memos          map[string]MemoDoc
	TrackerHooks   map[TrackerHookKey]TrackerHookState
	WerkModelUsage map[WerkModelUsageKey]WerkModelUsage
	// ImportIssues lists legacy paths whose import status was invalid or future
	// (keyed by dashboard-relative slash path, e.g. "plan-usage-history.json"),
	// so owners keep schema/error protection without re-reading legacy files.
	ImportIssues map[string]CaptureIssue
}

// CaptureIssue records a legacy document that failed import as invalid or
// newer-schema; its original bytes stay in legacy_captures for offline recovery.
type CaptureIssue struct {
	Store  string
	Status string
	Detail string
}

// AgentRecord is one assembled conversation record.
type AgentRecord struct {
	ReplyID    string
	SessionGen string
	// JSON is the snapshot with its deltas applied and holder_seq advanced
	// to the cursor of the record's own holder.
	JSON          json.RawMessage
	HolderCursors map[string]uint64
	Deltas        int
}

// Permission is one explicit selection. State "invalid" is an existing but
// unusable selection (fail closed), distinct from absence (no entry).
type Permission struct {
	State        string
	Level        string
	DelegatedSet bool
	Delegated    bool
	Body         json.RawMessage
	Error        string
}

// Hiring is one session's imported hiring metadata (the original flags).
type Hiring struct {
	Enabled   bool
	Hired     bool
	RequestID string
	Body      json.RawMessage
}

// Support is the last support selection and the store mappings.
type Support struct {
	Last   json.RawMessage
	Stores map[string]string
	Extra  json.RawMessage
}

// PiProbeKey keys one probe observation.
type PiProbeKey struct{ Fingerprint, Kind string }

// PiProbe is one probe observation record.
type PiProbe struct {
	ObservedAt string
	Body       json.RawMessage
}

// RailLayout is the rail layout: document order and extras, and each
// workspace's complete layout object.
type RailLayout struct {
	WorkspaceOrder []string
	Workspaces     map[string]json.RawMessage
	Extra          json.RawMessage
}

// LatestUsage is one latest-usage source in its stored order.
type LatestUsage struct {
	Extra   json.RawMessage
	Entries []UsageEntry
}

// UsageEntry is one keyed latest-usage projection.
type UsageEntry struct {
	Key  string
	Body json.RawMessage
}

// UsageHistory is one history's samples in stored order.
type UsageHistory struct {
	Extra   json.RawMessage
	Samples []UsageSample
}

// UsageSample is one retained usage sample.
type UsageSample struct {
	Seq                          int64
	Harness, Group, Kind, Source string
	ResetsAt, AsOf               int64
	Percent                      float64
	Body                         json.RawMessage
}

// MemoDoc is one workspace's memo document.
type MemoDoc struct {
	Workspace     string
	SchemaVersion int
	Extra         json.RawMessage
	Items         []Memo
}

// Memo is one memo item in order.
type Memo struct {
	Position int
	ID       string
	Body     json.RawMessage
}

// TrackerHookKey keys hook progress by store and registration nonce.
type TrackerHookKey struct{ StoreID, Nonce string }

// TrackerHookState is one store registration's hook progress.
type TrackerHookState struct {
	Cursor    json.RawMessage
	Extra     json.RawMessage
	Processed map[string]json.RawMessage
	Due       map[string]json.RawMessage
}

// Load reads the whole store once. It refuses before the import marker
// committed (there is no request-path fallback to legacy files).
func (s *Store) Load(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{
		Records: map[string]AgentRecord{}, Permissions: map[string]Permission{}, Labels: map[string]string{},
		AckChecks: map[string]json.RawMessage{}, Hiring: map[string]Hiring{}, Support: Support{Stores: map[string]string{}},
		PiProbes: map[PiProbeKey]PiProbe{}, LatestUsage: map[string]LatestUsage{}, UsageHistory: map[string]UsageHistory{},
		Memos: map[string]MemoDoc{}, TrackerHooks: map[TrackerHookKey]TrackerHookState{},
		WerkModelUsage: map[WerkModelUsageKey]WerkModelUsage{},
		ImportIssues:   map[string]CaptureIssue{},
	}
	err := s.read(ctx, func(q querier) error {
		if err := s.requireImported(ctx, q); err != nil {
			return err
		}
		return loadSnapshot(ctx, q, snap)
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// each runs query and calls scan per row.
func each(ctx context.Context, q querier, query string, scan func(row interface{ Scan(...any) error }) error) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

type scanner = interface{ Scan(...any) error }

func loadSnapshot(ctx context.Context, q querier, snap *Snapshot) error {
	type recordParts struct {
		gen      string
		snapshot []byte
		deltas   [][]byte
		cursors  map[string]uint64
	}
	parts := map[string]*recordParts{}
	steps := []struct {
		query string
		scan  func(scanner) error
	}{
		{`SELECT reply_id, session_gen, snapshot_json FROM agent_records`, func(r scanner) error {
			var id string
			p := &recordParts{cursors: map[string]uint64{}}
			if err := r.Scan(&id, &p.gen, &p.snapshot); err != nil {
				return err
			}
			parts[id] = p
			return nil
		}},
		{`SELECT reply_id, patch_json FROM agent_record_deltas ORDER BY reply_id, seq`, func(r scanner) error {
			var id string
			var d []byte
			if err := r.Scan(&id, &d); err != nil {
				return err
			}
			if p := parts[id]; p != nil {
				p.deltas = append(p.deltas, d)
			}
			return nil
		}},
		{`SELECT reply_id, holder_id, holder_seq FROM agent_holder_cursors`, func(r scanner) error {
			var id, h string
			var seq int64
			if err := r.Scan(&id, &h, &seq); err != nil {
				return err
			}
			if p := parts[id]; p != nil {
				p.cursors[h] = uint64(seq)
			}
			return nil
		}},
		{`SELECT reply_id, state, level, delegated_set, delegated, body_json, error FROM permissions`, func(r scanner) error {
			var id string
			var p Permission
			var body []byte
			if err := r.Scan(&id, &p.State, &p.Level, &p.DelegatedSet, &p.Delegated, &body, &p.Error); err != nil {
				return err
			}
			p.Body = body
			snap.Permissions[id] = p
			return nil
		}},
		{`SELECT reply_id, label FROM labels`, func(r scanner) error {
			var id, label string
			if err := r.Scan(&id, &label); err != nil {
				return err
			}
			snap.Labels[id] = label
			return nil
		}},
		{`SELECT reply_id, body_json FROM ack_checks`, func(r scanner) error {
			var id string
			var body []byte
			if err := r.Scan(&id, &body); err != nil {
				return err
			}
			snap.AckChecks[id] = body
			return nil
		}},
		{`SELECT reply_id, enabled, hired, request_id, body_json FROM hiring`, func(r scanner) error {
			var id string
			var h Hiring
			var body []byte
			if err := r.Scan(&id, &h.Enabled, &h.Hired, &h.RequestID, &body); err != nil {
				return err
			}
			h.Body = body
			snap.Hiring[id] = h
			return nil
		}},
		{`SELECT last_json, extra_json FROM support_state`, func(r scanner) error {
			var last, extra []byte
			if err := r.Scan(&last, &extra); err != nil {
				return err
			}
			snap.Support.Last, snap.Support.Extra = last, extra
			return nil
		}},
		{`SELECT store, conversation FROM support_mappings`, func(r scanner) error {
			var store, conv string
			if err := r.Scan(&store, &conv); err != nil {
				return err
			}
			snap.Support.Stores[store] = conv
			return nil
		}},
		{`SELECT fingerprint, kind, observed_at, body_json FROM pi_probes`, func(r scanner) error {
			var k PiProbeKey
			var p PiProbe
			var body []byte
			if err := r.Scan(&k.Fingerprint, &k.Kind, &p.ObservedAt, &body); err != nil {
				return err
			}
			p.Body = body
			snap.PiProbes[k] = p
			return nil
		}},
		{`SELECT workspace_order_json, extra_json FROM rail_layout`, func(r scanner) error {
			var order, extra []byte
			if err := r.Scan(&order, &extra); err != nil {
				return err
			}
			l := &RailLayout{Workspaces: map[string]json.RawMessage{}, Extra: extra}
			if err := json.Unmarshal(order, &l.WorkspaceOrder); err != nil {
				return fmt.Errorf("rail layout order: %w", err)
			}
			snap.RailLayout = l
			return nil
		}},
		{`SELECT workspace, body_json FROM rail_layout_workspaces`, func(r scanner) error {
			var ws string
			var body []byte
			if err := r.Scan(&ws, &body); err != nil {
				return err
			}
			if snap.RailLayout != nil {
				snap.RailLayout.Workspaces[ws] = body
			}
			return nil
		}},
		{`SELECT source, extra_json FROM usage_latest_docs`, func(r scanner) error {
			var source string
			var extra []byte
			if err := r.Scan(&source, &extra); err != nil {
				return err
			}
			snap.LatestUsage[source] = LatestUsage{Extra: extra}
			return nil
		}},
		{`SELECT source, entry_key, body_json FROM usage_latest ORDER BY source, position`, func(r scanner) error {
			var source string
			var e UsageEntry
			var body []byte
			if err := r.Scan(&source, &e.Key, &body); err != nil {
				return err
			}
			e.Body = body
			u := snap.LatestUsage[source]
			u.Entries = append(u.Entries, e)
			snap.LatestUsage[source] = u
			return nil
		}},
		{`SELECT history, extra_json FROM usage_history_docs`, func(r scanner) error {
			var h string
			var extra []byte
			if err := r.Scan(&h, &extra); err != nil {
				return err
			}
			snap.UsageHistory[h] = UsageHistory{Extra: extra, Samples: []UsageSample{}}
			return nil
		}},
		{`SELECT history, seq, harness, grp, kind, source, resets_at, as_of, percent, body_json FROM usage_samples ORDER BY history, seq`, func(r scanner) error {
			var h string
			var v UsageSample
			var body []byte
			if err := r.Scan(&h, &v.Seq, &v.Harness, &v.Group, &v.Kind, &v.Source, &v.ResetsAt, &v.AsOf, &v.Percent, &body); err != nil {
				return err
			}
			v.Body = body
			u := snap.UsageHistory[h]
			u.Samples = append(u.Samples, v)
			snap.UsageHistory[h] = u
			return nil
		}},
		{`SELECT workspace_hash, workspace, schema_version, extra_json FROM memo_docs`, func(r scanner) error {
			var hash string
			var d MemoDoc
			var extra []byte
			if err := r.Scan(&hash, &d.Workspace, &d.SchemaVersion, &extra); err != nil {
				return err
			}
			d.Extra, d.Items = extra, []Memo{}
			snap.Memos[hash] = d
			return nil
		}},
		{`SELECT workspace_hash, position, memo_id, body_json FROM memos ORDER BY workspace_hash, position`, func(r scanner) error {
			var hash string
			var m Memo
			var body []byte
			if err := r.Scan(&hash, &m.Position, &m.ID, &body); err != nil {
				return err
			}
			m.Body = body
			d := snap.Memos[hash]
			d.Items = append(d.Items, m)
			snap.Memos[hash] = d
			return nil
		}},
		{`SELECT store_id, nonce, cursor_json, extra_json FROM tracker_hook_state`, func(r scanner) error {
			var k TrackerHookKey
			var cursor, extra []byte
			if err := r.Scan(&k.StoreID, &k.Nonce, &cursor, &extra); err != nil {
				return err
			}
			snap.TrackerHooks[k] = TrackerHookState{Cursor: cursor, Extra: extra, Processed: map[string]json.RawMessage{}, Due: map[string]json.RawMessage{}}
			return nil
		}},
		{`SELECT store_id, nonce, event_id, body_json FROM tracker_hook_processed`, func(r scanner) error {
			var k TrackerHookKey
			var id string
			var body []byte
			if err := r.Scan(&k.StoreID, &k.Nonce, &id, &body); err != nil {
				return err
			}
			snap.TrackerHooks[k].Processed[id] = body
			return nil
		}},
		{`SELECT store_id, nonce, item_id, at_json FROM tracker_hook_due`, func(r scanner) error {
			var k TrackerHookKey
			var id string
			var at []byte
			if err := r.Scan(&k.StoreID, &k.Nonce, &id, &at); err != nil {
				return err
			}
			snap.TrackerHooks[k].Due[id] = at
			return nil
		}},
		{`SELECT path, store, import_status, import_detail FROM legacy_captures WHERE import_status IN ('invalid', 'future')`, func(r scanner) error {
			var p string
			var issue CaptureIssue
			if err := r.Scan(&p, &issue.Store, &issue.Status, &issue.Detail); err != nil {
				return err
			}
			snap.ImportIssues[p] = issue
			return nil
		}},
		{`SELECT provider, account_key, model, requests, input_tokens, output_tokens, cached_tokens, cache_write_tokens, reasoning_tokens, cost_usd, updated_at FROM werk_model_usage`, func(r scanner) error {
			var u WerkModelUsage
			if err := r.Scan(
				&u.Provider, &u.AccountKey, &u.Model,
				&u.Requests, &u.InputTokens, &u.OutputTokens,
				&u.CachedTokens, &u.CacheWriteTokens, &u.ReasoningTokens,
				&u.CostUSD, &u.UpdatedAt,
			); err != nil {
				return err
			}
			snap.WerkModelUsage[u.Key()] = u
			return nil
		}},
	}
	for _, step := range steps {
		if err := each(ctx, q, step.query, step.scan); err != nil {
			return err
		}
	}
	for id, p := range parts {
		assembled, err := mergeRecord(p.snapshot, p.deltas, p.cursors)
		if err != nil {
			return fmt.Errorf("agent record %s: %w", id, err)
		}
		snap.Records[id] = AgentRecord{ReplyID: id, SessionGen: p.gen, JSON: assembled, HolderCursors: p.cursors, Deltas: len(p.deltas)}
	}
	return nil
}
