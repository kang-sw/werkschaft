package wsdashboard

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

const companionPacksRoute = "/api/companion/packs"

func (d *Daemon) registerCompanionPackRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionPacksRoute, d.handleCompanionCatalog)
	mux.HandleFunc(companionPacksRoute+"/{id}/pack.json", d.handleCompanionManifest)
	mux.HandleFunc(companionPacksRoute+"/{id}/assets/{file}", d.handleCompanionAsset)
	mux.HandleFunc(companionPacksRoute+"/reload", d.handleCompanionReload)
	d.registerCompanionClipRoutes(mux)
	d.registerCompanionInstallRoutes(mux)
	d.registerCompanionAuthoringRoutes(mux)
	d.registerCompanionPhoneRoutes(mux)
	d.registerCompanionMemoryRoutes(mux)
	d.registerCompanionKnockRoute(mux)
	d.registerCompanionAgentsRoute(mux)
	mux.HandleFunc(companionFocusRoute, d.handleCompanionFocus)
}

func (d *Daemon) companionOptions() wsconfig.Options {
	if d.config != nil {
		return d.config.Options
	}
	return wsconfig.Options{}
}

func companionReadMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (d *Daemon) handleCompanionCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !companionReadMethod(w, r) {
		return
	}
	snapshot := d.companionPacks.snapshot(d.companionOptions())
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return
	}
	q := r.URL.Query()
	writeJSON(w, http.StatusOK, snapshot.catalog(q.Get("harness"), q.Get("model")))
}

func (d *Daemon) handleCompanionManifest(w http.ResponseWriter, r *http.Request) {
	d.serveCompanionPackFile(w, r, "", "application/json")
}

func (d *Daemon) handleCompanionAsset(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	var contentType string
	switch {
	case validCompanionRaster(file):
		contentType = "image/webp"
		if filepath.Ext(file) == ".png" {
			contentType = "image/png"
		}
	case validCompanionVRMFile(file), validCompanionVRMAFile(file):
		contentType = "model/gltf-binary"
	default:
		w.Header().Set("Cache-Control", "private, no-store")
		http.NotFound(w, r)
		return
	}
	d.serveCompanionPackFile(w, r, file, contentType)
}

func (d *Daemon) serveCompanionPackFile(w http.ResponseWriter, r *http.Request, file, contentType string) {
	d.serveCompanionPackData(w, r, contentType, file != "", func(pack *companionPack) ([]byte, bool) {
		if file == "" {
			return pack.manifest, true
		}
		data, found := pack.assets[file]
		return data, found
	})
}

// serveCompanionPackData serves bytes pick selects from one snapshot pack,
// with the pack revision as ETag and ?rev= immutable caching; sandbox adds
// the asset CSP.
func (d *Daemon) serveCompanionPackData(w http.ResponseWriter, r *http.Request, contentType string, sandbox bool, pick func(*companionPack) ([]byte, bool)) {
	header := w.Header()
	header.Set("Cache-Control", "private, no-store")
	if !companionReadMethod(w, r) {
		return
	}
	id := r.PathValue("id")
	if !companionPackID.MatchString(id) {
		http.NotFound(w, r)
		return
	}

	// Fix the revision and the bytes to the same snapshot, including during
	// a reload. A removed resource takes precedence over a revision conflict.
	pack := d.companionPacks.snapshot(d.companionOptions()).byID[id]
	if pack == nil {
		http.NotFound(w, r)
		return
	}
	data, found := pick(pack)
	if !found {
		http.NotFound(w, r)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid companion pack query", http.StatusBadRequest)
		return
	}
	if wanted, tagged := query["rev"]; tagged {
		if len(wanted) != 1 || wanted[0] != pack.info.Rev {
			header.Set("X-Companion-Current-Rev", pack.info.Rev)
			http.Error(w, "companion pack revision changed", http.StatusConflict)
			return
		}
		header.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "private, no-cache")
	}
	etag := `"` + pack.info.Rev + `"`
	header.Set("ETag", etag)
	header.Set("Content-Type", contentType)
	header.Set("X-Content-Type-Options", "nosniff")
	if sandbox {
		header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}
	if etagMatches(strings.Join(r.Header.Values("If-None-Match"), ","), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (d *Daemon) handleCompanionReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	snapshot := d.companionPacks.reload(d.companionOptions())
	q := r.URL.Query()
	writeJSON(w, http.StatusOK, snapshot.catalog(q.Get("harness"), q.Get("model")))
}
