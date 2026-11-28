package wsdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// DaemonDOMMaxBytes is the hard serialized byte cap on ws/daemon.dom
	// responses ((1756)).
	DaemonDOMMaxBytes = 64 << 10 // 64 KiB
	// DaemonDOMMaxNodes is the hard node cap on ws/daemon.dom responses ((1756)).
	DaemonDOMMaxNodes = 1500
	// DaemonUIMaxFillBytes bounds text values passed to fill_ref.
	DaemonUIMaxFillBytes = 64 << 10

	daemonBridgeOpTimeout = 15 * time.Second
)

// DaemonDOMRequest is the structured input to ws/daemon.dom.
type DaemonDOMRequest struct {
	TabID           string `json:"tab_id,omitempty"`
	Scope           string `json:"scope,omitempty"` // "all" (default) | "shell" | "frame" | "settings"
	InteractiveOnly bool   `json:"interactive_only,omitempty"`
	MaxNodes        int    `json:"max_nodes,omitempty"`
}

// DaemonDOMFrameState describes the currently framed session document inside
// the top-level dashboard shell.
type DaemonDOMFrameState struct {
	Path    string `json:"path,omitempty"`
	Hash    string `json:"hash,omitempty"`
	ReplyID string `json:"reply_id,omitempty"`
	Title   string `json:"title,omitempty"`
	Ready   bool   `json:"ready"`
}

// DaemonDOMModalState describes open overlays/modals in the targeted tab.
type DaemonDOMModalState struct {
	SettingsOpen bool     `json:"settings_open"`
	SettingsHref string   `json:"settings_href,omitempty"`
	RailOpen     bool     `json:"rail_open"`
	Dialogs      []string `json:"dialogs,omitempty"`
}

// DaemonDOMNode is one semantic/accessibility entry in a ws/daemon.dom snapshot.
type DaemonDOMNode struct {
	Ref       string            `json:"ref,omitempty"`
	Scope     string            `json:"scope"`
	Tag       string            `json:"tag"`
	Role      string            `json:"role,omitempty"`
	Name      string            `json:"name,omitempty"`
	Text      string            `json:"text,omitempty"`
	Value     string            `json:"value,omitempty"`
	InputType string            `json:"input_type,omitempty"`
	Masked    bool              `json:"masked,omitempty"`
	Href      string            `json:"href,omitempty"`
	Disabled  bool              `json:"disabled,omitempty"`
	Checked   *bool             `json:"checked,omitempty"`
	Pressed   string            `json:"pressed,omitempty"`
	Expanded  *bool             `json:"expanded,omitempty"`
	Selected  *bool             `json:"selected,omitempty"`
	DataWS    map[string]string `json:"data_ws,omitempty"`
	Depth     int               `json:"depth,omitempty"`
}

// DaemonDOMSnapshot is the bounded, redacted semantic DOM snapshot returned by
// ws/daemon.dom.
type DaemonDOMSnapshot struct {
	TabID      string               `json:"tab_id"`
	Generation uint64               `json:"generation"`
	Route      string               `json:"route"`
	Hash       string               `json:"hash,omitempty"`
	ReplyID    string               `json:"reply_id,omitempty"`
	Title      string               `json:"title,omitempty"`
	Frame      *DaemonDOMFrameState `json:"frame,omitempty"`
	Modals     DaemonDOMModalState  `json:"modals"`
	Nodes      []DaemonDOMNode      `json:"nodes"`
	NodeCount  int                  `json:"node_count"`
	ByteSize   int                  `json:"byte_size"`
	Truncated  bool                 `json:"truncated"`
}

// DaemonUIRequest is the structured input to ws/daemon.ui (strictly 6
// allowlisted semantic actions; zero arbitrary JS eval).
type DaemonUIRequest struct {
	TabID          string `json:"tab_id,omitempty"`
	Action         string `json:"action"`
	Path           string `json:"path,omitempty"`
	ReplyID        string `json:"reply_id,omitempty"`
	EntryID        string `json:"entry_id,omitempty"`
	Scope          string `json:"scope,omitempty"`
	Section        string `json:"section,omitempty"`
	Target         string `json:"target,omitempty"`
	Worktree       string `json:"worktree,omitempty"`
	Session        string `json:"session,omitempty"`
	Panel          string `json:"panel,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Generation     uint64 `json:"generation,omitempty"`
	Value          string `json:"value,omitempty"`
	CompanionPause bool   `json:"companion_pause,omitempty"`
}

// DaemonUIResponse is the structured result of a ws/daemon.ui action.
type DaemonUIResponse struct {
	OK          bool   `json:"ok"`
	Action      string `json:"action"`
	TabID       string `json:"tab_id"`
	Route       string `json:"route,omitempty"`
	Hash        string `json:"hash,omitempty"`
	ReplyID     string `json:"reply_id,omitempty"`
	Panel       string `json:"panel,omitempty"`
	Ref         string `json:"ref,omitempty"`
	TargetLabel string `json:"target_label,omitempty"`
	Masked      bool   `json:"masked,omitempty"`
}

var (
	daemonRefPattern       = regexp.MustCompile(`^g([0-9]+)-r([0-9]+)$`)
	daemonReplyIDPattern   = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	sensitiveDOMAttrRe     = regexp.MustCompile(`(?i)(?:password|passwd|secret|token|api[_\s-]?key|credential|cookie|bearer|private[_\s-]?key|auth[_\s-]?key)`)
	allowedUISettingsScope = map[string]bool{
		"": true, "global": true, "project": true, "repo": true, "session": true,
	}
	allowedUISettingsSection = map[string]bool{
		"": true, "workflow": true, "agents": true, "tier_routing": true, "account": true,
		"compaction": true, "adapters": true, "companion": true, "profiles": true, "prompts": true,
	}
	allowedUIPanels = map[string]string{
		"convo":        "convo",
		"conversation": "convo",
		"board":        "board",
		"files":        "files",
		"term":         "term",
		"terminal":     "term",
		"git":          "git",
		"overview":     "overview",
		"ticketmap":    "ticketmap",
		"rail":         "rail",
		"search":       "search",
	}
)

func (s *daemonBridgeStream) invalidateSnapshotLocked() {
	s.navVersion++
	if s.snapshot != nil {
		s.snapshot.invalidated = true
		s.snapshot.refs = nil
	}
}

func (b *daemonBridge) beginSnapshot(explicitTabID string) (string, uint64, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tab, err := b.selectTabLocked(strings.TrimSpace(explicitTabID), false)
	if err != nil {
		return "", 0, 0, err
	}
	tab.nextSnapGen++
	return tab.tabID, tab.nextSnapGen, tab.navVersion, nil
}

func (b *daemonBridge) commitSnapshot(tabID string, gen, navVer uint64, route, replyID string, refs map[string]daemonRefInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tab := b.tabs[tabID]
	if tab == nil || tab.closed || !tab.registered {
		return
	}
	if tab.nextSnapGen != gen || tab.navVersion != navVer {
		return
	}
	tab.snapshot = &daemonSnapshotState{
		generation: gen,
		navVersion: navVer,
		route:      route,
		replyID:    replyID,
		refs:       refs,
	}
}

func (b *daemonBridge) invalidateTabSnapshot(tabID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tab := b.tabs[tabID]
	if tab == nil || tab.closed {
		return
	}
	tab.invalidateSnapshotLocked()
}

func parseDaemonRefGeneration(ref string) (uint64, bool) {
	m := daemonRefPattern.FindStringSubmatch(ref)
	if len(m) != 3 {
		return 0, false
	}
	g, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil || g == 0 {
		return 0, false
	}
	return g, true
}

func (b *daemonBridge) lookupRef(explicitTabID string, explicitGen uint64, ref string) (string, uint64, daemonRefInfo, error) {
	ref = strings.TrimSpace(ref)
	b.mu.Lock()
	defer b.mu.Unlock()
	tab, err := b.selectTabLocked(strings.TrimSpace(explicitTabID), false)
	if err != nil {
		return "", 0, daemonRefInfo{}, err
	}
	snap := tab.snapshot
	if snap == nil || snap.generation == 0 || snap.invalidated {
		return tab.tabID, 0, daemonRefInfo{}, &DaemonControlError{
			Code:    "stale_ref",
			Reason:  "snapshot_invalidated",
			Message: "no active DOM snapshot for tab (take a fresh daemon.dom snapshot)",
		}
	}
	if explicitGen != 0 && explicitGen != snap.generation {
		return tab.tabID, snap.generation, daemonRefInfo{}, &DaemonControlError{
			Code:    "stale_ref",
			Reason:  "generation_mismatch",
			Message: fmt.Sprintf("ref generation %d is stale (active generation is %d)", explicitGen, snap.generation),
		}
	}
	if refGen, ok := parseDaemonRefGeneration(ref); ok && refGen != snap.generation {
		return tab.tabID, snap.generation, daemonRefInfo{}, &DaemonControlError{
			Code:    "stale_ref",
			Reason:  "generation_mismatch",
			Message: fmt.Sprintf("ref %q belongs to stale generation %d (active generation is %d)", ref, refGen, snap.generation),
		}
	}
	info, exists := snap.refs[ref]
	if !exists {
		return tab.tabID, snap.generation, daemonRefInfo{}, &DaemonControlError{
			Code:    "ref_not_found",
			Reason:  "ref_not_found",
			Message: fmt.Sprintf("ref %q was not found in snapshot generation %d", ref, snap.generation),
		}
	}
	if info.Excluded {
		return tab.tabID, snap.generation, daemonRefInfo{}, &DaemonControlError{
			Code:    "denied",
			Reason:  "excluded_element",
			Message: fmt.Sprintf("ref %q targets an excluded control", ref),
		}
	}
	return tab.tabID, snap.generation, info, nil
}

func isExcludedDOMNode(n DaemonDOMNode) bool {
	for k, v := range n.DataWS {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "data-ws-dom-exclude" && strings.TrimSpace(v) == "1" {
			return true
		}
		if lk == "data-daemon-confirm" || lk == "data-daemon-confirm-action" {
			return true
		}
	}
	return false
}

func isSensitiveDOMNode(n DaemonDOMNode) bool {
	if n.Masked {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(n.InputType), "password") {
		return true
	}
	tag := strings.ToLower(strings.TrimSpace(n.Tag))
	role := strings.ToLower(strings.TrimSpace(n.Role))
	isInputLike := tag == "input" || tag == "textarea" || role == "textbox" || role == "searchbox"
	if !isInputLike {
		return false
	}
	if sensitiveDOMAttrRe.MatchString(n.Name) || sensitiveDOMAttrRe.MatchString(n.InputType) {
		return true
	}
	for k, v := range n.DataWS {
		if sensitiveDOMAttrRe.MatchString(k) || sensitiveDOMAttrRe.MatchString(v) {
			return true
		}
	}
	return false
}

func (d *Daemon) sanitizeAndCapDOMSnapshot(raw DaemonDOMSnapshot, tabID string, gen uint64, maxNodes int) (DaemonDOMSnapshot, map[string]daemonRefInfo) {
	if maxNodes <= 0 || maxNodes > DaemonDOMMaxNodes {
		maxNodes = DaemonDOMMaxNodes
	}
	snap := DaemonDOMSnapshot{
		TabID:      tabID,
		Generation: gen,
		Route:      d.redactDaemonSecrets(raw.Route),
		Hash:       d.redactDaemonSecrets(raw.Hash),
		ReplyID:    clampString(strings.TrimSpace(raw.ReplyID), daemonReplyIDMaxLen),
		Title:      d.redactDaemonSecrets(clampString(raw.Title, 512)),
		Modals: DaemonDOMModalState{
			SettingsOpen: raw.Modals.SettingsOpen,
			SettingsHref: d.redactDaemonSecrets(raw.Modals.SettingsHref),
			RailOpen:     raw.Modals.RailOpen,
		},
		Truncated: raw.Truncated,
	}
	if raw.Frame != nil {
		snap.Frame = &DaemonDOMFrameState{
			Path:    d.redactDaemonSecrets(raw.Frame.Path),
			Hash:    d.redactDaemonSecrets(raw.Frame.Hash),
			ReplyID: clampString(strings.TrimSpace(raw.Frame.ReplyID), daemonReplyIDMaxLen),
			Title:   d.redactDaemonSecrets(clampString(raw.Frame.Title, 512)),
			Ready:   raw.Frame.Ready,
		}
	}
	if len(raw.Modals.Dialogs) > 0 {
		dialogs := make([]string, 0, len(raw.Modals.Dialogs))
		for _, dlg := range raw.Modals.Dialogs {
			if len(dialogs) >= 32 {
				snap.Truncated = true
				break
			}
			dialogs = append(dialogs, d.redactDaemonSecrets(clampString(dlg, 256)))
		}
		snap.Modals.Dialogs = dialogs
	}

	nodes := make([]DaemonDOMNode, 0, min(len(raw.Nodes), maxNodes))
	for _, n := range raw.Nodes {
		if isExcludedDOMNode(n) {
			continue
		}
		if len(nodes) >= maxNodes {
			snap.Truncated = true
			break
		}
		clean := n
		clean.Scope = strings.TrimSpace(clean.Scope)
		if clean.Scope == "" {
			clean.Scope = "shell"
		}
		clean.Tag = strings.ToLower(strings.TrimSpace(clean.Tag))
		clean.Role = strings.ToLower(strings.TrimSpace(clean.Role))
		clean.Name = d.redactDaemonSecrets(clean.Name)
		clean.Text = d.redactDaemonSecrets(clean.Text)
		clean.Href = d.redactDaemonSecrets(clean.Href)
		if len(clean.DataWS) > 0 {
			redacted := make(map[string]string, len(clean.DataWS))
			for k, v := range clean.DataWS {
				redacted[k] = d.redactDaemonSecrets(v)
			}
			clean.DataWS = redacted
		}
		if isSensitiveDOMNode(clean) {
			clean.Masked = true
			if clean.Value != "" {
				clean.Value = "[REDACTED]"
			}
		} else {
			clean.Value = d.redactDaemonSecrets(clean.Value)
		}
		nodes = append(nodes, clean)
	}
	snap.Nodes = nodes
	snap.NodeCount = len(snap.Nodes)

	// Enforce the 64 KiB serialized byte cap ((1756)) by binary-searching the
	// largest prefix of Nodes that fits within DaemonDOMMaxBytes.
	encodeSize := func(s *DaemonDOMSnapshot) int {
		s.NodeCount = len(s.Nodes)
		// Reserve a stable 5-digit ByteSize estimate first, then refine once.
		s.ByteSize = DaemonDOMMaxBytes
		b, _ := json.Marshal(s)
		s.ByteSize = len(b)
		b, _ = json.Marshal(s)
		s.ByteSize = len(b)
		return len(b)
	}

	if encodeSize(&snap) > DaemonDOMMaxBytes {
		snap.Truncated = true
		lo, hi := 0, len(snap.Nodes)
		allNodes := snap.Nodes
		for lo < hi {
			mid := (lo + hi + 1) / 2
			snap.Nodes = allNodes[:mid]
			if encodeSize(&snap) <= DaemonDOMMaxBytes {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		snap.Nodes = allNodes[:lo]
		encodeSize(&snap)
	}

	refs := make(map[string]daemonRefInfo, len(snap.Nodes))
	for _, n := range snap.Nodes {
		if n.Ref == "" {
			continue
		}
		label := n.Name
		if label == "" {
			label = n.Text
		}
		if label == "" {
			label = n.Role
		}
		if label == "" {
			label = n.Tag
		}
		refs[n.Ref] = daemonRefInfo{
			Ref:       n.Ref,
			Scope:     n.Scope,
			Role:      n.Role,
			Tag:       n.Tag,
			Label:     clampString(label, 120),
			Sensitive: n.Masked,
			InputType: n.InputType,
		}
	}
	return snap, refs
}

// DaemonDOM requests a bounded semantic/accessibility DOM snapshot from the
// targeted local dashboard tab, enforces server-side exclusion, secret
// redaction, password masking, and 64 KiB / 1500-node hard caps, and records
// the generation's ephemeral ref map for subsequent ws/daemon.ui actions.
func (d *Daemon) DaemonDOM(ctx context.Context, req DaemonDOMRequest) (DaemonDOMSnapshot, error) {
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "all", "shell", "frame", "settings":
	default:
		return DaemonDOMSnapshot{}, &DaemonControlError{
			Code:    "invalid_request",
			Message: fmt.Sprintf("invalid scope %q (must be all, shell, frame, or settings)", req.Scope),
		}
	}
	maxNodes := req.MaxNodes
	if maxNodes <= 0 || maxNodes > DaemonDOMMaxNodes {
		maxNodes = DaemonDOMMaxNodes
	}

	tabID, gen, navVer, err := d.daemonBridge.beginSnapshot(req.TabID)
	if err != nil {
		return DaemonDOMSnapshot{}, err
	}

	params := map[string]any{
		"generation":       gen,
		"scope":            scope,
		"interactive_only": req.InteractiveOnly,
		"max_nodes":        maxNodes,
		"max_bytes":        DaemonDOMMaxBytes,
	}
	rawPayload, _, err := d.SendDaemonBridgeRequest(ctx, tabID, "dom.snapshot", params, daemonBridgeOpTimeout)
	if err != nil {
		return DaemonDOMSnapshot{}, err
	}
	var rawSnap DaemonDOMSnapshot
	if len(rawPayload) > 0 {
		if err := json.Unmarshal(rawPayload, &rawSnap); err != nil {
			return DaemonDOMSnapshot{}, &DaemonControlError{
				Code:    "invalid_response",
				Message: "tab returned malformed DOM snapshot payload",
			}
		}
	}

	snap, refs := d.sanitizeAndCapDOMSnapshot(rawSnap, tabID, gen, maxNodes)
	d.daemonBridge.commitSnapshot(tabID, gen, navVer, snap.Route, snap.ReplyID, refs)
	return snap, nil
}

func validateDaemonNavigatePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", &DaemonControlError{
			Code:    "invalid_request",
			Message: "navigate path must be a daemon-relative path starting with a single '/'",
		}
	}
	if strings.ContainsAny(raw, "\\\r\n") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", &DaemonControlError{
			Code:    "invalid_request",
			Message: "navigate path contains disallowed characters",
		}
	}
	if strings.Contains(raw, "://") {
		return "", &DaemonControlError{
			Code:    "invalid_request",
			Message: "navigate path must not contain a URL scheme or host",
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Opaque != "" {
		return "", &DaemonControlError{
			Code:    "invalid_request",
			Message: "navigate path must be relative with no scheme, host, or userinfo",
		}
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == ".." {
			return "", &DaemonControlError{
				Code:    "invalid_request",
				Message: "navigate path must not contain '..' traversal segments",
			}
		}
	}
	return u.RequestURI() + func() string {
		if u.Fragment != "" {
			return "#" + u.Fragment
		}
		return ""
	}(), nil
}

// DaemonUI executes one of the 6 strictly allowlisted semantic UI actions
// (navigate, focus_session, open_settings, select_panel, click_ref, fill_ref)
// against the targeted local dashboard tab. Mutating actions (click_ref,
// fill_ref) are server-classified and gated behind RequestDaemonConfirmation.
func (d *Daemon) DaemonUI(ctx context.Context, req DaemonUIRequest) (DaemonUIResponse, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "navigate":
		navPath, err := validateDaemonNavigatePath(req.Path)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		raw, tabID, err := d.SendDaemonBridgeRequest(ctx, req.TabID, "ui.navigate", map[string]any{
			"path": navPath,
		}, daemonBridgeOpTimeout)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		d.daemonBridge.invalidateTabSnapshot(tabID)
		return d.decodeDaemonUIResponse(raw, action, tabID), nil

	case "focus_session":
		replyID := strings.TrimSpace(req.ReplyID)
		if !daemonReplyIDPattern.MatchString(replyID) {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: "focus_session requires a valid reply_id",
			}
		}
		entryID := strings.TrimSpace(req.EntryID)
		if len(entryID) > 128 || strings.IndexFunc(entryID, unicode.IsControl) >= 0 {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: "focus_session entry_id is invalid",
			}
		}
		params := map[string]any{"reply_id": replyID}
		if entryID != "" {
			params["entry_id"] = entryID
		}
		raw, tabID, err := d.SendDaemonBridgeRequest(ctx, req.TabID, "ui.focus_session", params, daemonBridgeOpTimeout)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		d.daemonBridge.invalidateTabSnapshot(tabID)
		return d.decodeDaemonUIResponse(raw, action, tabID), nil

	case "open_settings":
		scope := strings.ToLower(strings.TrimSpace(req.Scope))
		if !allowedUISettingsScope[scope] {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: fmt.Sprintf("invalid settings scope %q", req.Scope),
			}
		}
		section := strings.ToLower(strings.TrimSpace(req.Section))
		if !allowedUISettingsSection[section] {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: fmt.Sprintf("invalid settings section %q", req.Section),
			}
		}
		params := map[string]any{}
		if scope != "" {
			params["scope"] = scope
		}
		if section != "" {
			params["section"] = section
		}
		if t := strings.TrimSpace(req.Target); t != "" {
			params["target"] = clampString(t, 256)
		}
		if wt := strings.TrimSpace(req.Worktree); wt != "" {
			params["worktree"] = clampString(wt, 256)
		}
		if sess := strings.TrimSpace(req.Session); sess != "" {
			params["session"] = clampString(sess, daemonReplyIDMaxLen)
		}
		raw, tabID, err := d.SendDaemonBridgeRequest(ctx, req.TabID, "ui.open_settings", params, daemonBridgeOpTimeout)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		return d.decodeDaemonUIResponse(raw, action, tabID), nil

	case "select_panel":
		panel, ok := allowedUIPanels[strings.ToLower(strings.TrimSpace(req.Panel))]
		if !ok {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: fmt.Sprintf("invalid panel %q (allowed: convo, board, files, term, git, overview, ticketmap, rail, search)", req.Panel),
			}
		}
		raw, tabID, err := d.SendDaemonBridgeRequest(ctx, req.TabID, "ui.select_panel", map[string]any{
			"panel": panel,
		}, daemonBridgeOpTimeout)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		resp := d.decodeDaemonUIResponse(raw, action, tabID)
		if resp.Panel == "" {
			resp.Panel = panel
		}
		return resp, nil

	case "click_ref":
		ref := strings.TrimSpace(req.Ref)
		if ref == "" {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: "click_ref requires a non-empty ref",
			}
		}
		targetTabID, activeGen, info, err := d.daemonBridge.lookupRef(req.TabID, req.Generation, ref)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		label := info.Label
		if label == "" {
			label = ref
		}
		confirm := d.RequestDaemonConfirmation(ctx, DaemonConfirmRequest{
			TabID:          targetTabID,
			Tool:           "daemon.ui",
			Action:         "click_ref",
			TargetLabel:    label,
			Summary:        fmt.Sprintf("click_ref %s (%s)", ref, label),
			CompanionPause: req.CompanionPause,
		})
		if !confirm.Approved {
			code := confirm.Code
			if req.CompanionPause || code == "" {
				code = "denied"
			}
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    code,
				Reason:  confirm.Reason,
				Message: fmt.Sprintf("confirmation denied (%s) for click_ref %s", confirm.Reason, ref),
			}
		}
		// Re-verify ref is still valid after the confirmation wait.
		if _, _, _, err := d.daemonBridge.lookupRef(targetTabID, activeGen, ref); err != nil {
			return DaemonUIResponse{}, err
		}
		raw, tabID, err := d.SendDaemonBridgeRequest(ctx, targetTabID, "ui.click_ref", map[string]any{
			"ref":        ref,
			"generation": activeGen,
		}, daemonBridgeOpTimeout)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		resp := d.decodeDaemonUIResponse(raw, action, tabID)
		resp.Ref = ref
		if resp.TargetLabel == "" {
			resp.TargetLabel = label
		}
		return resp, nil

	case "fill_ref":
		ref := strings.TrimSpace(req.Ref)
		if ref == "" {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "invalid_request",
				Message: "fill_ref requires a non-empty ref",
			}
		}
		if len(req.Value) > DaemonUIMaxFillBytes {
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    "payload_too_large",
				Message: fmt.Sprintf("fill_ref value exceeds %d bytes", DaemonUIMaxFillBytes),
			}
		}
		targetTabID, activeGen, info, err := d.daemonBridge.lookupRef(req.TabID, req.Generation, ref)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		label := info.Label
		if label == "" {
			label = ref
		}
		fillSummary := "[REDACTED]"
		if !info.Sensitive {
			fillSummary = clampString(d.redactDaemonSecrets(req.Value), 120)
		}
		confirm := d.RequestDaemonConfirmation(ctx, DaemonConfirmRequest{
			TabID:          targetTabID,
			Tool:           "daemon.ui",
			Action:         "fill_ref",
			TargetLabel:    label,
			FillValue:      fillSummary,
			Summary:        fmt.Sprintf("fill_ref %s (%s) = %s", ref, label, fillSummary),
			CompanionPause: req.CompanionPause,
		})
		if !confirm.Approved {
			code := confirm.Code
			if req.CompanionPause || code == "" {
				code = "denied"
			}
			return DaemonUIResponse{}, &DaemonControlError{
				Code:    code,
				Reason:  confirm.Reason,
				Message: fmt.Sprintf("confirmation denied (%s) for fill_ref %s", confirm.Reason, ref),
			}
		}
		if _, _, _, err := d.daemonBridge.lookupRef(targetTabID, activeGen, ref); err != nil {
			return DaemonUIResponse{}, err
		}
		raw, tabID, err := d.SendDaemonBridgeRequest(ctx, targetTabID, "ui.fill_ref", map[string]any{
			"ref":        ref,
			"generation": activeGen,
			"value":      req.Value,
		}, daemonBridgeOpTimeout)
		if err != nil {
			return DaemonUIResponse{}, err
		}
		resp := d.decodeDaemonUIResponse(raw, action, tabID)
		resp.Ref = ref
		if resp.TargetLabel == "" {
			resp.TargetLabel = label
		}
		if info.Sensitive {
			resp.Masked = true
		}
		return resp, nil

	default:
		return DaemonUIResponse{}, &DaemonControlError{
			Code:    "invalid_request",
			Message: fmt.Sprintf("unsupported daemon.ui action %q (allowed: navigate, focus_session, open_settings, select_panel, click_ref, fill_ref)", req.Action),
		}
	}
}

func (d *Daemon) decodeDaemonUIResponse(raw json.RawMessage, action, tabID string) DaemonUIResponse {
	resp := DaemonUIResponse{
		OK:     true,
		Action: action,
		TabID:  tabID,
	}
	if len(raw) > 0 {
		var parsed DaemonUIResponse
		if json.Unmarshal(raw, &parsed) == nil {
			resp.Route = d.redactDaemonSecrets(parsed.Route)
			resp.Hash = d.redactDaemonSecrets(parsed.Hash)
			resp.ReplyID = clampString(strings.TrimSpace(parsed.ReplyID), daemonReplyIDMaxLen)
			resp.Panel = parsed.Panel
			resp.Ref = parsed.Ref
			resp.TargetLabel = d.redactDaemonSecrets(parsed.TargetLabel)
			resp.Masked = parsed.Masked
		}
	}
	return resp
}
