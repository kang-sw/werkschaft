// daemon_http.go holds daemon HTTP routing and control endpoints.
package wsdashboard

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// Handler is the daemon's HTTP surface. Every route first passes the access
// guard (localhost cookie, or the separate token for a non-local LAN viewer,
// with explicit machine and frame exemptions; access.go). Loopback mode also
// checks the Host against DNS rebinding; the daemon sends no CORS headers. The pages (/,
// /s/<reply-id>, served as the iframe shell or, with ?frame=1, as the page
// it frames; their SSE or WebSocket streams, the shell's rail stream
// /rail/events, and a session's image, file view, and
// history routes, and the app manifest and its icons, pwa.go) are read-only; a session's message, cancel, steer, and interrupt
// routes (inbox_routes.go), with a tracker host the tracker POST routes
// (tracker_routes.go), and with a support handler the support POST routes
// (a tracker store's support conversation: send, new, select) are the only
// browser writes besides the git drawer's push, pull, and fetch routes
// (gitpush.go) and, with stored workspaces, the rail's workspace routes
// (workspace_routes.go: the stored list, and git worktree add), its layout
// route (rail_layout_route.go: the rows' shared order, tree, and stars), and
// its system-open route (sysopen_route.go) the only route
// that runs a host command beside git and the agent runner's harnesses (with
// a holder manager, the holder stop route, holders.go, ends a holder, and
// the terminal routes, terminal_routes.go, run shells for a browser on this
// machine): a
// session stream keeps its root's git handle read, the git route
// (gitbadge.go) asks for a read at once without running git itself, and the
// git status route (gitdrawer.go) reads the root's changed files. With
// Pprof on, /debug/pprof/ (pprof_route.go) is served behind the guard to
// local viewers only.
func (d *Daemon) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.handleHealth)
	mux.HandleFunc("/shutdown", d.handleShutdown)
	mux.HandleFunc(FullStopPath, d.handleShutdownAll)
	mux.HandleFunc("/auth/status", d.handleAuthStatus)
	mux.HandleFunc("/auth/exchange", d.handleAuthExchange)
	mux.HandleFunc("/auth/link", d.handleAuthLink)
	mux.HandleFunc("/auth/cookie", d.handleAuthCookie)
	mux.HandleFunc("/auth/clipboard", d.handleAuthClipboard)
	if d.proxy != nil {
		mux.HandleFunc(ProxyPath, d.handleProxy)
		if _, ok := d.agents.(AgentMCPTokenHost); ok {
			mux.HandleFunc(ProxyHTTPPath, d.handleProxyHTTP)
		}
	}
	mux.HandleFunc(wscred.TokenRoutePath, d.handleProviderToken)
	mux.HandleFunc(credentialChangePath, d.handleProviderChange)
	mux.HandleFunc(EndpointChangePath, d.handleEndpointChange)
	mux.HandleFunc(MetaAgentRecordPath, d.handleMetaAgentRecord)
	mux.HandleFunc(MetaHostedDescendantPath, d.handleMetaHostedDescendant)
	mux.HandleFunc(MetaUnregisterCheckPath, d.handleMetaUnregisterCheck)
	mux.HandleFunc(MetaPrunePath, d.handleMetaPrune)
	mux.HandleFunc(accountPrefix, d.handleAccount)
	mux.HandleFunc(voicePrefix, d.handleVoice)
	mux.HandleFunc("/nudge", d.handleNudge)
	mux.HandleFunc(companionChangedPath, d.handleCompanionConfigChange)
	mux.HandleFunc("/events", d.handleIndexEvents)
	mux.HandleFunc(pwaPrefix, handlePWA)
	mux.HandleFunc("/s/", d.handleSessionRoutes)
	mux.HandleFunc("/w/", d.handleWorktreePage)
	mux.HandleFunc("/settings", d.handleSettings)
	mux.HandleFunc(companionRigStudioRoute, d.handleCompanionRigStudio)
	mux.HandleFunc("/api/config/snapshot", d.handleConfigSnapshot)
	mux.HandleFunc("/api/config/set", d.handleConfigMutation)
	mux.HandleFunc("/api/config/reset", d.handleConfigMutation)
	mux.HandleFunc("/api/config/proposal", d.handleConfigProposal)
	mux.HandleFunc("/api/config/tier-routing", d.handleTierRouting)
	mux.HandleFunc("/api/config/tier-routing/preview", d.handleTierRouting)
	mux.HandleFunc(DaemonBridgeReplyPath, d.handleDaemonBridgeReply)
	d.registerCompanionPackRoutes(mux)
	mux.HandleFunc(railRoute, d.handleRailRefresh)
	mux.HandleFunc(railLayoutRoute, d.handleRailLayout)
	mux.HandleFunc(railAdmissionRoute, d.handleRailAdmission)
	mux.HandleFunc(hireRoute, d.handleSessionHire)
	mux.HandleFunc(hireRoute+"/", d.handleSessionHire)
	mux.HandleFunc(railEventsRoute, d.handleRailEvents)
	mux.HandleFunc("/rail/memos", d.handleMemos)
	mux.HandleFunc("/rail/usage", d.handlePlanUsage)
	if d.workspaces != nil {
		d.workspaceRoutes(mux)
	}
	if d.tracker != nil {
		mux.HandleFunc("/tracker/", d.handleTracker)
	}
	if d.agents != nil {
		agents := d.agents.Handler()
		mux.Handle(AgentRoutePrefix, agents)
		mux.Handle(AgentRoutePrefix+"/", agents)
	}
	if d.support != nil {
		mux.Handle(SupportRoutePrefix, d.support)
		mux.Handle(SupportRoutePrefix+"/", d.support)
	}
	if d.holders != nil || d.durableSessionHost() != nil {
		mux.HandleFunc("/holders", d.handleHolders)
		mux.HandleFunc("/holders/", d.handleHolders)
	}
	if d.pprof {
		mux.Handle(pprofPrefix, pprofHandler(d.serveStreams))
	}
	mux.HandleFunc("/", d.handleIndex)
	guarded := d.accessGuard(mux)
	if d.lan {
		return guarded
	}
	return hostGuard(guarded)
}

// hostGuard refuses a request whose Host header is not a loopback name. A
// DNS-rebinding page reaches 127.0.0.1 under its own host name, which this
// refuses.
func hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			// The native clipboard route is deliberately undiscoverable to
			// a non-local Host, in both serving modes. It remains refused.
			if r.URL.Path == "/auth/clipboard" {
				http.NotFound(w, r)
			} else {
				http.Error(w, "forbidden host", http.StatusForbidden)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackHost reports whether a Host header names the loopback interface:
// localhost, 127.0.0.1, or ::1, with or without a port.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, HealthInfo{
		Service:     healthService,
		Version:     d.endpoint.Version,
		PID:         d.endpoint.PID,
		Port:        d.endpoint.Port,
		StartedAt:   d.endpoint.StartedAt,
		LAN:         d.lan,
		BuildTime:   d.buildTime,
		ContentHash: d.contentHash,
	})
}

// handleShutdown stops the daemon when the request carries the endpoint
// file's token as a bearer credential. Only a process that can read the
// endpoint file (the same OS user) holds that token. The holders outlive
// it (holders.go); handleShutdownAll ends them too.
func (d *Daemon) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.endpointBearer(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"shutting_down": true})
	fmt.Fprintf(d.log, "dashboard: shutdown requested\n")
	go d.Close()
}

// endpointBearer reports whether r carries the endpoint file's token as its
// bearer credential.
func (d *Daemon) endpointBearer(r *http.Request) bool {
	got, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return bearer && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(d.endpoint.Token)) == 1
}

// FullStopPath is the full stop route (handleShutdownAll).
const FullStopPath = "/shutdown/all"

// FullStopResult lists each stopped or remaining session under the holder
// response shape. ShuttingDown is true only after all sessions have ended.
type FullStopResult struct {
	ShuttingDown bool `json:"shutting_down"`
	wsholder.StopAllResult
}

// handleShutdownAll uses the endpoint bearer to stop both process holders
// and durable sessions. A partial stop leaves the daemon serving and
// reopens admission. A Close from another source can still cut the response
// short, which the caller treats as an incomplete stop.
func (d *Daemon) handleShutdownAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.endpointBearer(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d.fullStopMu.Lock()
	defer d.fullStopMu.Unlock()
	if d.fullStopAccepted {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shutting_down"})
		return
	}
	select {
	case <-d.stop:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shutting_down"})
		return
	default:
	}
	fmt.Fprintf(d.log, "dashboard: full stop requested\n")
	result := FullStopResult{StopAllResult: wsholder.StopAllResult{Stopped: []string{}, Remaining: []wsholder.StopFailure{}}}
	host := d.durableSessionHost()
	var resumeDurable, resumeHolders func()
	if host != nil {
		resumeDurable = host.FenceDurableStarts()
	}
	if d.holders != nil {
		result.StopAllResult, resumeHolders = d.holders.StopAllFenced()
		if d.terminals != nil {
			// The stopped terminals' viewers learn of it now.
			d.terminals.sync()
		}
	}
	if host != nil {
		sessions := host.DurableSessions()
		failures := make(map[string]error)
		for _, session := range sessions {
			if err := host.StopDurableSession(session.ID); err != nil && !errors.Is(err, ErrUnknownDurableSession) {
				failures[session.ID] = err
			}
		}
		// A cascade or an overlapping individual stop may have ended a
		// session after its earlier attempt failed. Report the survivors.
		remaining := host.DurableSessions()
		live := make(map[string]bool, len(remaining))
		for _, session := range remaining {
			live[session.ID] = true
			message := "the durable session is still open"
			if err := failures[session.ID]; err != nil {
				message = err.Error()
			}
			result.Remaining = append(result.Remaining, wsholder.StopFailure{Info: durableHolderInfo(session), Error: message})
		}
		for _, session := range sessions {
			if !live[session.ID] {
				result.Stopped = append(result.Stopped, durableHolderPrefix+session.ID)
			}
		}
	}
	if len(result.Remaining) > 0 {
		if resumeHolders != nil {
			resumeHolders()
		}
		if resumeDurable != nil {
			resumeDurable()
		}
		fmt.Fprintf(d.log, "dashboard: full stop incomplete: %d session(s) stopped, %d remain; the daemon keeps running\n", len(result.Stopped), len(result.Remaining))
		writeJSON(w, http.StatusConflict, result)
		return
	}
	d.fullStopAccepted = true
	result.ShuttingDown = true
	fmt.Fprintf(d.log, "dashboard: full stop: %d session(s) stopped; shutting down\n", len(result.Stopped))
	writeJSON(w, http.StatusAccepted, result)
	go d.Close()
}

// handleNudge schedules a re-read of the one session the body names
// (Index.Nudge) and answers without waiting for it. It needs no credential:
// non-browser callers omit Origin and fetch metadata. Browser requests must
// be same-origin. A malformed body is 400, an unknown
// session 404, and a scheduled re-read 204; the sender ignores the answer.
func (d *Daemon) handleNudge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if (len(r.Header.Values("Origin")) > 0 && !sameOrigin(r)) || crossSiteFetch(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	d.stats.Nudges.Add(1)
	var body NudgeBody
	if err := json.NewDecoder(io.LimitReader(r.Body, nudgeBodyLimit)).Decode(&body); err != nil {
		http.Error(w, "malformed nudge", http.StatusBadRequest)
		return
	}
	// Before the registration check: a mail sender need not be a
	// registered session, and each woken wait rechecks its own target.
	if d.mailWake != nil {
		d.mailWake.Nudge()
	}
	d.foreign.mailChanged()
	d.foreignWriter(body.ReplyID)
	if d.index == nil || !d.index.Nudge(body.ReplyID, body.Project) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// foreignWriter handles an HTTP nudge naming session replyID. Only another
// process nudges over HTTP (a hosted server notifies in process,
// NotifySessionChange), so for a session this daemon hosts it is a second
// writer whose evidence the hosted copy lacks: the copy stops standing for
// the evidence directory, before the nudge's re-read wakes any page.
func (d *Daemon) foreignWriter(replyID string) {
	d.live.mu.Lock()
	defer d.live.mu.Unlock()
	if _, held := d.live.owners[replyID]; held {
		d.snapshots.shareObserved(d.dir, replyID)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
