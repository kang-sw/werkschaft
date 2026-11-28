package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// ws/daemon.highlight: a non-mutating visual overlay (spotlight, pulse, or
// ring plus an optional guide comment) the targeted local dashboard tab draws
// over one element. It changes no product state, so it runs without a
// confirmation banner.
const (
	daemonHighlightSelectorMax     = 256
	daemonHighlightTextMaxRunes    = 200
	daemonHighlightCommentMaxRunes = 280
	daemonHighlightDefaultID       = "default"
	daemonHighlightDefaultStyle    = "spotlight"
	daemonHighlightBridgeOp        = "guide.highlight"
)

var (
	daemonHighlightIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	daemonHighlightStyles    = map[string]bool{"spotlight": true, "pulse": true, "ring": true}
	daemonHighlightScopes    = map[string]bool{"shell": true, "frame": true, "settings": true}
)

// DaemonHighlightTarget names the element to highlight: exactly one of Ref,
// Selector, or Text. Generation (ref only) asserts the snapshot generation.
type DaemonHighlightTarget struct {
	Ref        string `json:"ref,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Text       string `json:"text,omitempty"`
}

// DaemonHighlightRequest is the structured input to ws/daemon.highlight.
type DaemonHighlightRequest struct {
	Action  string                 `json:"action"`
	ID      string                 `json:"id,omitempty"`
	TabID   string                 `json:"tab_id,omitempty"`
	Target  *DaemonHighlightTarget `json:"target,omitempty"`
	Comment string                 `json:"comment,omitempty"`
	Style   string                 `json:"style,omitempty"`
}

// DaemonHighlightRect is the highlighted element's box in top-level viewport
// CSS pixels.
type DaemonHighlightRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// DaemonHighlightResponse is the structured result of ws/daemon.highlight.
type DaemonHighlightResponse struct {
	OK         bool                 `json:"ok"`
	Action     string               `json:"action"`
	TabID      string               `json:"tab_id"`
	ID         string               `json:"id,omitempty"`
	Style      string               `json:"style,omitempty"`
	TargetKind string               `json:"target_kind,omitempty"`
	Scope      string               `json:"scope,omitempty"`
	Visible    bool                 `json:"visible"`
	Rect       *DaemonHighlightRect `json:"rect,omitempty"`
	Cleared    int                  `json:"cleared,omitempty"`
}

func stripGuideControls(s string, keepNewline bool) string {
	return strings.Map(func(r rune) rune {
		if keepNewline && r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// DaemonHighlight shows or clears a guide highlight in the targeted local tab.
func (d *Daemon) DaemonHighlight(ctx context.Context, req DaemonHighlightRequest) (DaemonHighlightResponse, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	id := strings.TrimSpace(req.ID)
	style := strings.ToLower(strings.TrimSpace(req.Style))
	tabID := strings.TrimSpace(req.TabID)
	resp := DaemonHighlightResponse{Action: action}
	params := map[string]any{"action": action}
	switch action {
	case "clear_all":
		if id != "" || req.Target != nil || req.Comment != "" || style != "" {
			return resp, guideInvalid("clear_all takes no id, target, comment, or style")
		}
	case "clear":
		if req.Target != nil || req.Comment != "" || style != "" {
			return resp, guideInvalid("clear takes only id and tab_id")
		}
	case "show":
	default:
		return resp, guideInvalid("unsupported daemon.highlight action %q (allowed: show, clear, clear_all)", req.Action)
	}
	if action != "clear_all" {
		if id == "" {
			id = daemonHighlightDefaultID
		}
		if !daemonHighlightIDPattern.MatchString(id) {
			return resp, guideInvalid("highlight id must match ^[A-Za-z0-9_-]{1,32}$")
		}
		params["id"] = id
		resp.ID = id
	}
	if action == "show" {
		if style == "" {
			style = daemonHighlightDefaultStyle
		}
		if !daemonHighlightStyles[style] {
			return resp, guideInvalid("invalid highlight style %q (allowed: spotlight, pulse, ring)", req.Style)
		}
		if utf8.RuneCountInString(req.Comment) > daemonHighlightCommentMaxRunes {
			return resp, guideInvalid("highlight comment exceeds %d characters", daemonHighlightCommentMaxRunes)
		}
		target, kind, pinnedTab, err := d.highlightTarget(tabID, req.Target)
		if err != nil {
			return resp, err
		}
		tabID = pinnedTab
		params["style"] = style
		if c := strings.TrimSpace(stripGuideControls(req.Comment, true)); c != "" {
			params["comment"] = d.redactDaemonSecrets(c)
		}
		params["target"] = target
		resp.Style, resp.TargetKind = style, kind
	}
	raw, sentTab, err := d.SendDaemonBridgeRequest(ctx, tabID, daemonHighlightBridgeOp, params, daemonBridgeOpTimeout)
	if err != nil {
		if dce, ok := IsDaemonControlError(err); ok {
			// A bare invalid_request is a pre-guide shell answering an unknown
			// op; a guide-aware client always names its reason.
			if dce.Code == "invalid_request" {
				return resp, &DaemonControlError{Code: "guide_unavailable", Reason: "client_unsupported", Message: "the dashboard tab does not support guide highlights yet (reload the dashboard)"}
			}
			if reason, found := strings.CutPrefix(dce.Code, "invalid_request:"); found {
				reason = sanitizeGuideField(reason, guideMonitorFieldMax)
				return resp, &DaemonControlError{Code: "invalid_request", Reason: reason, Message: "the dashboard rejected the highlight request (" + reason + ")"}
			}
		}
		return resp, err
	}
	resp.OK, resp.TabID = true, sentTab
	if len(raw) > 0 {
		var parsed struct {
			Scope   string               `json:"scope"`
			Visible bool                 `json:"visible"`
			Rect    *DaemonHighlightRect `json:"rect"`
			Cleared int                  `json:"cleared"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			if daemonHighlightScopes[parsed.Scope] {
				resp.Scope = parsed.Scope
			}
			resp.Visible = parsed.Visible
			if action == "show" {
				resp.Rect = parsed.Rect
			}
			if parsed.Cleared > 0 {
				resp.Cleared = parsed.Cleared
			}
		}
	}
	return resp, nil
}

// highlightTarget validates t and returns the bridge target object, its kind,
// and the tab to pin (a ref resolves to the tab holding its snapshot).
func (d *Daemon) highlightTarget(tabID string, t *DaemonHighlightTarget) (map[string]any, string, string, error) {
	if t == nil {
		return nil, "", "", guideInvalid("show requires target with exactly one of ref, selector, or text")
	}
	ref, selector, text := strings.TrimSpace(t.Ref), strings.TrimSpace(t.Selector), strings.TrimSpace(t.Text)
	n := 0
	for _, v := range []string{ref, selector, text} {
		if v != "" {
			n++
		}
	}
	if n != 1 {
		return nil, "", "", guideInvalid("target needs exactly one of ref, selector, or text")
	}
	if t.Generation != 0 && ref == "" {
		return nil, "", "", guideInvalid("target.generation applies only to a ref target")
	}
	switch {
	case ref != "":
		pinned, gen, _, err := d.daemonBridge.lookupRef(tabID, t.Generation, ref)
		if err != nil {
			return nil, "", "", err
		}
		return map[string]any{"kind": "ref", "ref": ref, "generation": gen}, "ref", pinned, nil
	case selector != "":
		if len(selector) > daemonHighlightSelectorMax || strings.IndexFunc(selector, unicode.IsControl) >= 0 {
			return nil, "", "", guideInvalid("target.selector must be at most %d bytes with no control characters", daemonHighlightSelectorMax)
		}
		lower := strings.ToLower(selector)
		if strings.Contains(lower, "daemon-confirm") || strings.Contains(lower, "data-ws-dom-exclude") {
			return nil, "", "", &DaemonControlError{Code: "denied", Reason: "excluded_element", Message: "target.selector names an excluded control"}
		}
		return map[string]any{"kind": "selector", "selector": selector}, "selector", tabID, nil
	default:
		if utf8.RuneCountInString(text) > daemonHighlightTextMaxRunes {
			return nil, "", "", guideInvalid("target.text exceeds %d characters", daemonHighlightTextMaxRunes)
		}
		text = strings.TrimSpace(stripGuideControls(text, false))
		if text == "" {
			return nil, "", "", guideInvalid("target.text is empty")
		}
		return map[string]any{"kind": "text", "text": text}, "text", tabID, nil
	}
}

// DaemonMonitor starts, stops, or reports the caller's guide monitor.
func (d *Daemon) DaemonMonitor(_ context.Context, req DaemonMonitorRequest) (DaemonMonitorStatus, error) {
	return d.guideMonitors().handle(req)
}

// guideMonitors returns the daemon's monitor registry, creating it on first
// use with production seams (real timers, mailbox delivery, registration
// liveness, bridge broadcast).
func (d *Daemon) guideMonitors() *guideMonitorRegistry {
	b := &d.daemonBridge
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.guide == nil {
		b.guide = newGuideMonitorRegistry(guideMonitorDeps{
			clock:   realGuideClock{},
			deliver: d.deliverGuideMonitor,
			live:    d.guideOwnerLive,
			push:    d.broadcastGuidePush,
		})
		if b.guideClosed {
			b.guide.close()
		}
	}
	return b.guide
}

// closeGuideMonitors tears every monitor down at Daemon.Close.
func (d *Daemon) closeGuideMonitors() {
	b := &d.daemonBridge
	b.mu.Lock()
	b.guideClosed = true
	g := b.guide
	b.mu.Unlock()
	if g != nil {
		g.close()
	}
}

func (d *Daemon) deliverGuideMonitor(owner, body string) error {
	if d.dir == "" {
		return errors.New("guide monitor: no daemon state directory")
	}
	// Redact at delivery (never at ingest, which stays I/O-free): the route
	// is client-observed and may carry a credential-like fragment, as
	// daemon.dom redacts its own route.
	body = d.redactDaemonSecrets(body)
	store := (wsmailbox.Paths{State: wsstate.Options{CacheHome: filepath.Dir(d.dir)}}).Store()
	if err := store.PostSystem(owner, guideMonitorLabel, body, time.Now()); err != nil {
		return err
	}
	if d.mailWake != nil {
		d.mailWake.Wake()
	}
	return nil
}

func (d *Daemon) guideOwnerLive(owner string) bool {
	if d.dir == "" {
		return false
	}
	reg, err := ReadRegistration(d.dir, owner)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// An unreadable row is not proof the session ended; keep the
			// monitor and let TTL bound it.
			return true
		}
		return false
	}
	return d.regLive(reg, time.Now())
}

func (d *Daemon) broadcastGuidePush(event string, data []byte) {
	b := &d.daemonBridge
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tabs {
		if t.closed || !t.registered {
			continue
		}
		t.enqueue(event, data)
	}
}
