package wsdashboard

import (
	"net/http"
)

// companionAgentsRoute serves the companion tier selectors' readback: what
// the Lightweight (body) and Work companion sessions launch with now, and
// the values the selectors offer.
const companionAgentsRoute = "/api/companion/agents"

// companionAgentsUnavailable answers a daemon whose host bound no readback.
var companionAgentsUnavailable = configError{404, "upgrade_required", "This daemon has no companion agent readback."}

// SetCompanionAgents installs the readback the companion runtime computes;
// nil removes it.
func (d *Daemon) SetCompanionAgents(fn func() any) {
	if fn == nil {
		d.companionAgents.Store(nil)
		return
	}
	d.companionAgents.Store(&fn)
}

func (d *Daemon) registerCompanionAgentsRoute(mux *http.ServeMux) {
	mux.HandleFunc(companionAgentsRoute, d.handleCompanionAgents)
}

func (d *Daemon) handleCompanionAgents(w http.ResponseWriter, r *http.Request) {
	if !configRouteMethod(w, r, http.MethodGet) {
		return
	}
	if !localBrowseViewer(r) {
		writeConfigError(w, configForbidden)
		return
	}
	fn := d.companionAgents.Load()
	if fn == nil || *fn == nil {
		writeConfigError(w, companionAgentsUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved_agents": (*fn)()})
}
