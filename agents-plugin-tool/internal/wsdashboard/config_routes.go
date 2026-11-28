package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
)

// ConfigBackend connects the dashboard to the daemon-owned catalog and
// session store. Session keys returned internally are never response fields.
type ConfigBackend struct {
	Catalog            *wsconfigservice.Catalog
	Options            wsconfig.Options
	Structured         map[string]json.RawMessage
	Session            func(Registration) (wsconfigservice.SessionLayer, wsconfig.SessionWriter, string, bool)
	OrchestrateWarning func(string) string
	IsLead             func(Registration) bool
	Changed            func(string)
	LoadCompanion      func() (CompanionConfig, error)
	// LoadAuditLimits reads the audit store knobs once at daemon start,
	// clamped to a valid set with one warning per change.
	LoadAuditLimits func() (wsauditstore.Limits, []string, error)
	// TierRouting describes the agent.tier_routing graph for draft (nil: the
	// stored value) with traces for the caller harness, from the daemon's
	// own graph and resolver. Nil on a backend without the graph.
	TierRouting func(draft json.RawMessage, harness string) (any, error)
}

type configContext struct {
	Worktree string `json:"worktree"`
	Session  string `json:"session"`
}

type configSelection struct {
	Scope   wsconfig.Scope `json:"scope"`
	Target  string         `json:"target"`
	Context configContext  `json:"context"`
}

type configSnapshot struct {
	configSelection
	Path           *string                 `json:"path"`
	SessionHarness string                  `json:"session_harness,omitempty"`
	Entries        []wsconfigservice.Entry `json:"entries"`
	Companion      CompanionConfig         `json:"companion"`
}

type configRequest struct {
	configSelection
	Key       string            `json:"key"`
	Selectors map[string]string `json:"selectors"`
	Value     json.RawMessage   `json:"value"`
	// Revision is the agent.tier_routing revision the edited graph was read
	// at (the graph view's revision); required for that key, ignored for
	// every other key.
	Revision *string `json:"revision,omitempty"`
}

type configWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type configResult struct {
	Entry    wsconfigservice.Entry   `json:"entry"`
	Affected []wsconfigservice.Entry `json:"affected_entries"`
	Warnings []configWarning         `json:"warnings"`
}

type configError struct {
	status  int
	code    string
	message string
}

var (
	badConfigRequest    = configError{400, "invalid_request", "Invalid setting, value, scope or selector."}
	badConfigContext    = configError{400, "invalid_context", "The selected inspection context is unavailable or does not match."}
	configTargetMissing = configError{404, "target_not_found", "The selected settings target is unavailable."}
	configForbidden     = configError{403, "forbidden", "Settings require local dashboard access."}
	configUnsupported   = configError{409, "scope_unsupported", "This setting cannot be changed in the selected scope."}
	configUnavailable   = configError{500, "config_error", "The configuration could not be loaded or saved."}
	// The graph editor saves a whole graph built from the copy it loaded;
	// these refuse a save that does not name that copy's revision or whose
	// copy is no longer the stored one, so a concurrent edit is never lost.
	tierRoutingNoRevision = configError{400, "invalid_request", "Saving agent.tier_routing needs the revision of the graph it edits. Reload the graph and apply the edit again."}
	tierRoutingStale      = configError{409, "stale_revision", "The tier routing graph changed since it was loaded. Nothing was saved; the graph has been reloaded, so apply the edit again."}
)

func writeConfigError(w http.ResponseWriter, failure configError) {
	writeJSON(w, failure.status, map[string]configWarning{"error": {Code: failure.code, Message: failure.message}})
}

func configRouteMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == method || (method == http.MethodGet && r.Method == http.MethodHead) {
		return true
	}
	w.Header().Set("Allow", method)
	writeConfigError(w, configError{405, "method_not_allowed", "HTTP method not allowed."})
	return false
}

func queryConfigSelection(r *http.Request) (configSelection, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return configSelection{}, false
	}
	for _, key := range []string{"scope", "target", "worktree", "session"} {
		if len(query[key]) > 1 {
			return configSelection{}, false
		}
	}
	scope := wsconfig.Scope(query.Get("scope"))
	if scope == "" {
		scope = wsconfig.ScopeGlobal
	}
	return configSelection{Scope: scope, Target: query.Get("target"), Context: configContext{
		Worktree: query.Get("worktree"), Session: query.Get("session"),
	}}, true
}

type configResolved struct {
	opts          wsconfig.Options
	path          *string
	session       Registration
	targetSession Registration
	targetRoot    string
}

type configWorktree struct {
	path, workspace string
}

func settingsID(kind, root string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + filepath.Clean(root)))
	return hex.EncodeToString(sum[:])
}

func (w RailWorkspace) SettingsID() string { return settingsID("workspace", w.Path) }
func (w RailWorktree) SettingsID() string  { return settingsID("worktree", w.Path) }

// SettingsWorktree is the settings ID of the worktree or plain folder the
// rail placed the session under, the bound worktree its gear opens with.
func (r RailRow) SettingsWorktree() string {
	if r.settingsWorktree != "" {
		return r.settingsWorktree
	}
	return settingsID("worktree", r.Project)
}

// settingsTargets indexes the rail inventory: workspaces and worktrees by
// settings ID, and each listed session's bound worktree ID by reply ID, from
// the same membership the rail's gears are rendered from.
func (d *Daemon) settingsTargets() (map[string]string, map[string]configWorktree, map[string]string) {
	workspaces := make(map[string]string)
	worktrees := make(map[string]configWorktree)
	sessions := make(map[string]string)
	// Use the rail's daemon-maintained inventory. No browser input is
	// interpreted as a path or used to enumerate the filesystem.
	view := d.railBaseView(d.pagesIn("en"))
	for _, workspace := range view.Workspaces {
		id := workspace.SettingsID()
		workspaces[id] = workspace.Path
		for _, worktree := range workspace.Worktrees {
			worktrees[worktree.SettingsID()] = configWorktree{worktree.Path, id}
		}
		if !workspace.Repo {
			worktrees[settingsID("worktree", workspace.Path)] = configWorktree{workspace.Path, id}
		}
		for _, row := range workspace.AllSessions() {
			sessions[row.ReplyID] = row.SettingsWorktree()
		}
	}
	return workspaces, worktrees, sessions
}

func (d *Daemon) settingsSession(id string) (Registration, bool) {
	if d.index == nil || !ValidReplyID(id) {
		return Registration{}, false
	}
	session, ok := d.index.Session(id)
	return session.Registration, ok && session.Live && !session.Unlisted
}

func (d *Daemon) resolveConfig(selection configSelection) (configResolved, configError) {
	var result configResolved
	if d.config == nil || d.config.Catalog == nil {
		return result, configUnavailable
	}
	result.opts = d.config.Options
	var workspaces map[string]string
	var worktrees map[string]configWorktree
	var sessionWorktrees map[string]string
	if selection.Context.Worktree != "" || (selection.Scope == wsconfig.ScopeProject && selection.Target != "") || selection.Scope == wsconfig.ScopeRepo {
		workspaces, worktrees, sessionWorktrees = d.settingsTargets()
	}
	var path string
	var err error
	switch selection.Scope {
	case wsconfig.ScopeBuiltin:
		if selection.Target != "" {
			return result, badConfigRequest
		}
	case wsconfig.ScopeGlobal:
		if selection.Target != "" {
			return result, badConfigRequest
		}
		path, err = wsconfig.GlobalPath(result.opts)
	case wsconfig.ScopeProject:
		if selection.Target != "" {
			if _, ok := workspaces[selection.Target]; !ok {
				return result, configTargetMissing
			}
		}
		path, err = wsconfig.Path(result.opts)
	case wsconfig.ScopeRepo:
		worktree, ok := worktrees[selection.Target]
		if !ok {
			return result, configTargetMissing
		}
		result.targetRoot = worktree.path
		target := result.opts
		target.RepoRoot = worktree.path
		path, _ = wsconfig.RepoPath(target)
	case wsconfig.ScopeSession:
		var ok bool
		result.targetSession, ok = d.settingsSession(selection.Target)
		if !ok {
			return result, configTargetMissing
		}
	default:
		return result, badConfigRequest
	}
	if err != nil {
		return result, configUnavailable
	}
	if path != "" {
		result.path = &path
	}
	ctx := selection.Context
	if ctx.Worktree != "" {
		worktree, ok := worktrees[ctx.Worktree]
		if !ok ||
			(selection.Scope == wsconfig.ScopeProject && selection.Target != "" && worktree.workspace != selection.Target) ||
			(selection.Scope == wsconfig.ScopeRepo && ctx.Worktree != selection.Target) {
			return result, badConfigContext
		}
		result.opts.RepoRoot = worktree.path
	}
	if ctx.Session != "" {
		session, ok := d.settingsSession(ctx.Session)
		if !ok || ctx.Worktree == "" || sessionWorktrees[ctx.Session] != ctx.Worktree {
			return result, badConfigContext
		}
		result.session = session
	}
	if selection.Scope == wsconfig.ScopeSession && (ctx.Session != selection.Target || ctx.Worktree == "") {
		return result, badConfigContext
	}
	return result, configError{}
}

func (d *Daemon) configLayers(resolved configResolved) (wsconfigservice.Layers, wsconfig.SessionWriter, string, configError) {
	files, err := wsconfig.FileLayers(resolved.opts)
	if err != nil {
		return wsconfigservice.Layers{}, nil, "", configUnavailable
	}
	layers := wsconfigservice.Layers{Files: files, Structured: d.config.Structured}
	var writer wsconfig.SessionWriter
	var key string
	if resolved.session.ReplyID != "" {
		if d.config.Session != nil {
			layers.Session, writer, key, layers.SessionAvailable = d.config.Session(resolved.session)
		}
		if !layers.SessionAvailable {
			failure := badConfigContext
			if resolved.targetSession.ReplyID != "" {
				failure = configTargetMissing
			}
			return layers, nil, "", failure
		}
	}
	return layers, writer, key, configError{}
}

func (d *Daemon) handleSettings(w http.ResponseWriter, r *http.Request) {
	if !configRouteMethod(w, r, http.MethodGet) {
		return
	}
	if !localBrowseViewer(r) {
		writeConfigError(w, configForbidden)
		return
	}
	sel, ok := queryConfigSelection(r)
	failure := badConfigRequest
	if ok {
		_, failure = d.resolveConfig(sel)
	}
	if failure.status != 0 && !loginNavigationRequest(r) {
		writeConfigError(w, failure)
		return
	}
	if framedRequest(r) {
		lang, _ := requestLang(r)
		_, hasAccount := d.credentials.(browserCredentialStore)
		html, err := renderSettingsPage(lang, requestTheme(r), hasAccount)
		if err != nil {
			writeConfigError(w, configUnavailable)
			return
		}
		admitShellFrame(w, sessionPageCSP(true))
		status := http.StatusOK
		if failure.status != 0 {
			status = failure.status
		}
		writeHTMLStatus(w, status, html)
		return
	}
	status := http.StatusOK
	if failure.status != 0 {
		status = failure.status
	}
	d.serveShellStatus(w, r, nil, "/?frame=1", status)
}

func (d *Daemon) handleConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	if !configRouteMethod(w, r, http.MethodGet) {
		return
	}
	if !localBrowseViewer(r) {
		writeConfigError(w, configForbidden)
		return
	}
	sel, ok := queryConfigSelection(r)
	if !ok {
		writeConfigError(w, badConfigRequest)
		return
	}
	resolved, failure := d.resolveConfig(sel)
	if failure.status != 0 {
		writeConfigError(w, failure)
		return
	}
	layers, _, _, failure := d.configLayers(resolved)
	if failure.status != 0 {
		writeConfigError(w, failure)
		return
	}
	writeJSON(w, http.StatusOK, configSnapshot{
		configSelection: sel,
		Path:            resolved.path,
		SessionHarness:  strings.ToLower(strings.TrimSpace(resolved.session.Harness)),
		Entries:         d.config.Catalog.Entries(sel.Scope, layers),
		Companion:       d.companion.current().config,
	})
}

func decodeConfigRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	return decoder.Decode(target) == nil && decoder.Decode(new(any)) == io.EOF
}

func (d *Daemon) handleConfigMutation(w http.ResponseWriter, r *http.Request) {
	if !configRouteMethod(w, r, http.MethodPost) {
		return
	}
	if local, own := TrustedWrite(r); !local || !own {
		writeConfigError(w, configForbidden)
		return
	}
	var request configRequest
	if !decodeConfigRequest(w, r, &request) {
		writeConfigError(w, badConfigRequest)
		return
	}
	resolved, failure := d.resolveConfig(request.configSelection)
	if failure.status != 0 {
		writeConfigError(w, failure)
		return
	}
	if request.Scope == wsconfig.ScopeRepo || request.Scope == wsconfig.ScopeBuiltin {
		writeConfigError(w, configUnsupported)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/config/")
	var value any
	if request.Value != nil {
		dec := json.NewDecoder(strings.NewReader(string(request.Value)))
		dec.UseNumber()
		if action == "reset" || dec.Decode(&value) != nil {
			writeConfigError(w, badConfigRequest)
			return
		}
	}
	mutation, err := d.config.Catalog.Validate(request.Scope, wsconfigservice.Operation{Key: request.Key, Selectors: request.Selectors, Action: action, Value: value})
	if err != nil {
		var invalid wsconfigservice.InvalidValue
		switch {
		case errors.Is(err, wsconfigservice.ErrScope):
			writeConfigError(w, configUnsupported)
		case request.Key == wsconfig.ItemAgentTierRouting && errors.As(err, &invalid):
			// The graph editor shows why a save was refused (a cycle, the
			// hop limit, an unknown target) beside the draft.
			writeConfigError(w, configError{400, "invalid_request", invalid.Reason.Error()})
		default:
			writeConfigError(w, badConfigRequest)
		}
		return
	}
	if mutation.Key == wsconfig.ItemAgentTierRouting {
		if request.Revision == nil {
			writeConfigError(w, tierRoutingNoRevision)
			return
		}
		// The shared structured writer compares the revision with the file it
		// read under the config file lock, so no other writer (another save,
		// config.tune in any process) can land between compare and write.
		mutation.Precondition = wsconfig.TierRoutingAt(*request.Revision)
	}
	layers, writer, key, failure := d.configLayers(resolved)
	if failure.status != 0 {
		writeConfigError(w, failure)
		return
	}
	if d.beforeConfigWrite != nil {
		d.beforeConfigWrite(mutation.Key)
	}
	if err := mutation.Apply(request.Scope, resolved.opts, key, writer); err != nil {
		if errors.Is(err, wsconfig.ErrTierRoutingStale) {
			writeConfigError(w, tierRoutingStale)
		} else {
			writeConfigError(w, configUnavailable)
		}
		return
	}
	if d.config.Changed != nil {
		d.config.Changed(mutation.Key)
	}
	if isAgentGlobalConfigKey(mutation.Key) {
		d.reloadAgentGlobalConfig()
	}
	if IsCompanionConfigKey(mutation.Key) || mutation.Key == wsconfig.ItemWorkflowLang {
		if err := d.refreshCompanion(); err != nil {
			fmt.Fprintf(d.log, "dashboard: refresh companion settings: %v\n", err)
		}
	}
	warnings := []configWarning{}
	if refreshed, _, _, failure := d.configLayers(resolved); failure.status == 0 {
		layers = refreshed
	} else {
		layers = mutation.AfterWrite(request.Scope, layers)
		warnings = append(warnings, configWarning{"refresh_failed", "Saved. Reload settings to refresh the resolved layers."})
	}
	if mutation.Key == wsconfig.ItemWorkflowProfile && mutation.Scalar == "orchestrate" && d.config.OrchestrateWarning != nil {
		if message := d.config.OrchestrateWarning(mutation.Selectors["harness"]); message != "" {
			warnings = append(warnings, configWarning{"unverified_harness", strings.TrimSpace(message)})
		}
	}
	entry := d.config.Catalog.Entry(mutation.Key, mutation.Selectors, request.Scope, layers)
	affected := d.config.Catalog.AffectedEntries(mutation, request.Scope, layers)
	writeJSON(w, http.StatusOK, configResult{Entry: entry, Affected: affected, Warnings: warnings})
}

// tierRoutingRequest is a draft preview: the candidate agent.tier_routing
// value and the caller harness its traces resolve for.
type tierRoutingRequest struct {
	Harness string          `json:"harness"`
	Value   json.RawMessage `json:"value"`
}

var configNoTierRouting = configError{404, "upgrade_required", "This daemon has no tier routing graph."}

// handleTierRouting serves the stored agent.tier_routing graph (GET
// ?harness=) and a draft's preview (POST .../preview), both described by the
// daemon's validation and resolver, so the editor's layout, inline errors,
// and traces are what agent.spawn would do. A preview never writes; saving
// goes through /api/config/set, which validates again.
func (d *Daemon) handleTierRouting(w http.ResponseWriter, r *http.Request) {
	preview := strings.HasSuffix(r.URL.Path, "/preview")
	method := http.MethodGet
	if preview {
		method = http.MethodPost
	}
	if !configRouteMethod(w, r, method) {
		return
	}
	if preview {
		if local, own := TrustedWrite(r); !local || !own {
			writeConfigError(w, configForbidden)
			return
		}
	} else if !localBrowseViewer(r) {
		writeConfigError(w, configForbidden)
		return
	}
	if d.config == nil || d.config.TierRouting == nil {
		writeConfigError(w, configNoTierRouting)
		return
	}
	var request tierRoutingRequest
	if preview {
		if !decodeConfigRequest(w, r, &request) || len(request.Value) == 0 {
			writeConfigError(w, badConfigRequest)
			return
		}
	} else {
		request.Harness = r.URL.Query().Get("harness")
	}
	view, err := d.config.TierRouting(request.Value, request.Harness)
	if err != nil {
		writeConfigError(w, configUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
