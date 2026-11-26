package wsmeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// LegacyTrackerHooksConfigName is the retired cache-root hooks config path.
	LegacyTrackerHooksConfigName = "tracker-hooks.json"

	// TrackerHooksWitnessName is the new-daemon-only hooks config path in the
	// dashboard directory. Cutover publishes legacy tracker-hooks.json here
	// before deleting the legacy path (2125).
	TrackerHooksWitnessName = "tracker-hooks-v2.json"

	// ChildCutoverFencedProfile is the profile name stamped onto legacy
	// agent.json files of open non-lead child records at cutover. It fails
	// wsagent's profile-name grammar before any config lookup, so no configured
	// profile can resolve it (2125).
	ChildCutoverFencedProfile = "!cutover-fenced"

	// PermissionCutoverFencedLevel is the invalid permission_level stamped onto
	// every session's legacy permission.json at cutover so an older binary
	// refuses fresh launches while keeping held-process rejoin (2125).
	PermissionCutoverFencedLevel = "!cutover-fenced"

	defaultCutoverRetries     = 12
	defaultCutoverUnreadDelay = 100 * time.Millisecond
	maxCutoverUnreadDelay     = 2 * time.Second
	fallbackFenceClosedAt     = "1970-01-01T00:00:01Z"
)

var permissionFenceBytes = []byte("{\"cutover_fenced\":true,\"permission_level\":\"" + PermissionCutoverFencedLevel + "\"}\n")

// CutoverOptions configures Store.Cutover.
type CutoverOptions struct {
	// Validate carries the legacy owners' whole-document validators
	// (RequiredValidators).
	Validate map[string]LegacyValidator
	// MaxRetries bounds retries on transient cutover races (ErrFenceStale,
	// ErrFenceInvalidated, ErrFencePending, ErrLegacyUnread). Zero uses
	// defaultCutoverRetries.
	MaxRetries int
	// UnreadDelay is the initial retry delay on ErrLegacyUnread (backs off
	// exponentially up to maxCutoverUnreadDelay). Zero uses
	// defaultCutoverUnreadDelay.
	UnreadDelay time.Duration
	// BeforeFence, when set, runs after Stage and before fences are applied
	// (tests only).
	BeforeFence func(attempt int, captures []Capture) error
	// AfterFence, when set, runs after each fence call succeeds (tests only).
	AfterFence func(rel string) error
}

// Cutover drives the one-time legacy cutover under dashboard.lock (2125, 2135,
// 2136): stage all bundled legacy files, apply the four one-time fences in
// order (removal fence on tracker-hooks.json first so ErrRemovalUnreadable
// remains a clean ErrCutoverUnsupported fallback), and commit the imported
// state together with the completion marker.
//
// Once the completion marker is present, Cutover returns AlreadyComplete
// without reading any legacy path.
func (s *Store) Cutover(ctx context.Context, opts CutoverOptions) (ImportResult, error) {
	done, err := s.ImportComplete(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	if done {
		return ImportResult{AlreadyComplete: true}, nil
	}
	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultCutoverRetries
	}
	unreadDelay := opts.UnreadDelay
	if unreadDelay <= 0 {
		unreadDelay = defaultCutoverUnreadDelay
	}
	curDelay := unreadDelay
	for attempt := 0; attempt <= maxRetries; attempt++ {
		res, retryable, isUnread, err := s.cutoverAttempt(ctx, attempt, opts)
		if err == nil {
			return res, nil
		}
		if !retryable || attempt == maxRetries {
			return res, err
		}
		if isUnread {
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(curDelay):
			}
			curDelay *= 2
			if curDelay > maxCutoverUnreadDelay {
				curDelay = maxCutoverUnreadDelay
			}
		} else {
			curDelay = unreadDelay
		}
	}
	return ImportResult{}, fmt.Errorf("wsmeta: cutover exhausted retries")
}

func (s *Store) cutoverAttempt(ctx context.Context, attempt int, opts CutoverOptions) (res ImportResult, retryable, isUnread bool, err error) {
	stageRes, err := s.Stage(ctx)
	if errors.Is(err, ErrImportComplete) {
		return ImportResult{AlreadyComplete: true}, false, false, nil
	}
	if err != nil {
		return ImportResult{}, false, false, err
	}
	if len(stageRes.Unread) > 0 {
		return ImportResult{Stage: stageRes}, true, true, fmt.Errorf("%w: %s", ErrLegacyUnread, strings.Join(stageRes.Unread, ", "))
	}
	captures, err := s.Captures(ctx)
	if err != nil {
		return ImportResult{}, false, false, err
	}
	if opts.BeforeFence != nil {
		if err := opts.BeforeFence(attempt, captures); err != nil {
			return ImportResult{}, false, false, err
		}
	}
	missing := make(map[string]bool, len(stageRes.Missing))
	for _, id := range stageRes.Missing {
		missing[id] = true
	}

	// 1. Removal fence FIRST (edition b72e720c4): an unreadable
	// tracker-hooks.json refuses before any replacement fence is recorded, so
	// the refusal stays a clean ErrCutoverUnsupported legacy fallback.
	if err := s.ApplyRemovalFence(ctx, LegacyTrackerHooksConfigName, TrackerHooksWitnessName); err != nil {
		return classifyCutoverErr(err)
	}
	if opts.AfterFence != nil {
		if err := opts.AfterFence(LegacyTrackerHooksConfigName); err != nil {
			return ImportResult{}, false, false, err
		}
	}

	// 2. Replacement fences over active (non-missing) sessions.
	validateRecord := opts.Validate[StoreAgentRecord]
	for _, c := range captures {
		if c.ReplyID != "" && missing[c.ReplyID] {
			continue
		}
		var fenced []byte
		var apply bool
		switch c.Store {
		case StorePermission:
			// Every discovered session gets a present, invalid permission.json,
			// including sessions whose legacy file was absent or unreadable.
			fenced, apply = permissionFenceBytes, true
		case StoreHiring:
			if c.State == CapturePresent {
				fenced, apply = buildHiringFence(c.Bytes)
			}
		case StoreAgentRecord:
			if c.State == CapturePresent {
				fenced, apply = buildAgentRecordFence(c.ReplyID, c.Bytes, validateRecord)
			}
		}
		if !apply {
			continue
		}
		if err := s.ApplyFence(ctx, c.Path, fenced); err != nil {
			return classifyCutoverErr(err)
		}
		if opts.AfterFence != nil {
			if err := opts.AfterFence(c.Path); err != nil {
				return ImportResult{}, false, false, err
			}
		}
	}

	// 3. Commit with BeforeMarker verifying every required fence landed in the
	// commit pass's own snapshot (catches sessions created or mutated by a
	// racing legacy writer between Stage and Commit).
	res, err = s.Commit(ctx, CommitOptions{
		Validate: opts.Validate,
		BeforeMarker: func(committed []Capture) error {
			return verifyCutoverFences(committed, validateRecord)
		},
	})
	if err != nil {
		r, retry, unread, cerr := classifyCutoverErr(err)
		if r.AlreadyComplete {
			return r, false, false, nil
		}
		return res, retry, unread, cerr
	}
	return res, false, false, nil
}

func classifyCutoverErr(err error) (res ImportResult, retryable, isUnread bool, out error) {
	switch {
	case errors.Is(err, ErrImportComplete):
		return ImportResult{AlreadyComplete: true}, false, false, nil
	case errors.Is(err, ErrLegacyUnread):
		return ImportResult{}, true, true, err
	case errors.Is(err, ErrFenceStale), errors.Is(err, ErrFenceInvalidated), errors.Is(err, ErrFencePending):
		return ImportResult{}, true, false, err
	default:
		return ImportResult{}, false, false, err
	}
}

func verifyCutoverFences(captures []Capture, validateRecord LegacyValidator) error {
	var missing []string
	sawHooks := false
	for _, c := range captures {
		switch c.Store {
		case StoreTrackerHooksConfig:
			sawHooks = true
			if c.FenceState != FenceLanded || c.FenceWitness != TrackerHooksWitnessName {
				missing = append(missing, c.Path)
			}
		case StorePermission:
			if c.FenceState != FenceLanded {
				missing = append(missing, c.Path)
			}
		case StoreHiring:
			if c.State == CapturePresent {
				if _, needed := buildHiringFence(c.Bytes); needed && c.FenceState != FenceLanded {
					missing = append(missing, c.Path)
				}
			}
		case StoreAgentRecord:
			if c.State == CapturePresent {
				if _, needed := buildAgentRecordFence(c.ReplyID, c.Bytes, validateRecord); needed && c.FenceState != FenceLanded {
					missing = append(missing, c.Path)
				}
			}
		}
	}
	if !sawHooks {
		missing = append(missing, LegacyTrackerHooksConfigName)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrFencePending, strings.Join(missing, ", "))
	}
	return nil
}

// buildHiringFence returns the fenced hiring.json bytes (enabled: false,
// retaining hired, request_id, and unknown fields) when raw decodes as a JSON
// object with enabled=true. Deterministic so retries produce identical digests.
func buildHiringFence(raw []byte) ([]byte, bool) {
	m, err := object(raw)
	if err != nil {
		return nil, false
	}
	var h struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &h); err != nil || !h.Enabled {
		return nil, false
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		if strings.EqualFold(k, "enabled") {
			continue
		}
		out[k] = v
	}
	out["enabled"] = json.RawMessage(`false`)
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return append(encoded, '\n'), true
}

// buildAgentRecordFence returns the deterministic fenced agent.json bytes for
// an open non-lead record (2136 + child fence), or (nil, false) when the
// record needs no fence (invalid, lead, or already closed).
//
//   - Parentless non-lead open record (e.g. tracker support): sets a
//     deterministic nonempty closed_at (created_at, else fallbackFenceClosedAt),
//     preserving ws_session_key, root, and unknown fields.
//   - Child non-lead open record (parent non-null): keeps closed_at empty,
//     saves the original profile in unknown field cutover_profile, and sets
//     profile to ChildCutoverFencedProfile ("!cutover-fenced").
func buildAgentRecordFence(replyID string, raw []byte, validate LegacyValidator) ([]byte, bool) {
	m, err := object(raw)
	if err != nil {
		return nil, false
	}
	if validate != nil {
		if err := validate("sessions/"+replyID+"/agent.json", replyID, raw); err != nil {
			return nil, false
		}
	}
	var rec struct {
		ReplyID   string          `json:"reply_id"`
		WSKey     string          `json:"ws_session_key"`
		Root      string          `json:"root"`
		Lead      bool            `json:"lead"`
		ClosedAt  string          `json:"closed_at"`
		CreatedAt string          `json:"created_at"`
		Profile   string          `json:"profile"`
		Parent    json.RawMessage `json:"parent"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false
	}
	if rec.ReplyID != replyID || rec.WSKey == "" || rec.Root == "" {
		return nil, false
	}
	if rec.Lead || rec.ClosedAt != "" {
		return nil, false
	}
	out := make(map[string]json.RawMessage, len(m)+2)
	hasParent := len(rec.Parent) > 0 && !isNull(rec.Parent)
	if !hasParent {
		for k, v := range m {
			if strings.EqualFold(k, "closed_at") {
				continue
			}
			out[k] = v
		}
		closedAt := rec.CreatedAt
		if closedAt == "" {
			closedAt = fallbackFenceClosedAt
		}
		out["closed_at"], _ = json.Marshal(closedAt)
	} else {
		for k, v := range m {
			if strings.EqualFold(k, "profile") || strings.EqualFold(k, "cutover_profile") {
				continue
			}
			out[k] = v
		}
		out["cutover_profile"], _ = json.Marshal(rec.Profile)
		out["profile"], _ = json.Marshal(ChildCutoverFencedProfile)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return append(encoded, '\n'), true
}
