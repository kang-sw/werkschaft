package wsdashboard

import (
	"archive/zip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// companionSourcesRoute is the route family of user and official pack
// sources. Every route is a TrustedWrite POST that acts only on a click.
const companionSourcesRoute = "/api/companion/sources"

// companionOfficialSource names the built-in official source in requests
// and install records. It is not a URL, so no configured entry can equal it.
const companionOfficialSource = "official"

// companionSourceCacheTTL is how long a fetched source zip stays in memory
// for the install click that usually follows a listing.
const companionSourceCacheTTL = 10 * time.Minute

// The official marketplace ships with the binary. It lists only official
// packs; the embedded default pack is shown beside it as built in.
//
//go:embed assets/companion/official/marketplace.json
var companionOfficialMarketplace []byte

// companionSourceCache keeps the last fetched source zip, one entry at a
// time, so an install after a listing needs no second download.
type companionSourceCache struct {
	mu      sync.Mutex
	entry   *companionFetchedSource
	fetched time.Time
	timer   *time.Timer
}

// take returns the cached fetch of source when it is still fresh.
func (c *companionSourceCache) take(source string, now time.Time) (companionFetchedSource, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == nil || c.entry.origin.Source != source || now.Sub(c.fetched) >= companionSourceCacheTTL {
		return companionFetchedSource{}, false
	}
	return *c.entry, true
}

// put replaces the cached entry and drops it after the TTL, so a listing
// never holds up to 64 MiB for longer than that.
func (c *companionSourceCache) put(fetched companionFetchedSource, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
	}
	entry := &fetched
	c.entry, c.fetched = entry, now
	c.timer = time.AfterFunc(companionSourceCacheTTL, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.entry == entry {
			c.entry, c.timer = nil, nil
		}
	})
}

type companionSourceRequest struct {
	Source string   `json:"source"`
	IDs    []string `json:"ids"`
}

func (d *Daemon) registerCompanionSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionSourcesRoute+"/fetch", d.handleCompanionSourceFetch)
	mux.HandleFunc(companionSourcesRoute+"/install", d.handleCompanionSourceInstall)
}

// companionSourceGate runs the shared checks of the source routes and
// returns the decoded request. It answers the request itself on failure.
func (d *Daemon) companionSourceGate(w http.ResponseWriter, r *http.Request) (companionSourceRequest, bool) {
	var req companionSourceRequest
	if !companionTrustedPost(w, r) {
		return req, false
	}
	config := d.companion.current().config
	if !config.Enabled {
		companionWriteError(w, http.StatusConflict, "turn the companion on to use pack sources")
		return req, false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(raw, &req) != nil {
		companionWriteError(w, http.StatusBadRequest, "the request body must be a JSON object")
		return req, false
	}
	if req.Source != companionOfficialSource && !companionSourceListed(config.Sources, req.Source) {
		companionWriteError(w, http.StatusBadRequest, "add the source to the source list first")
		return req, false
	}
	return req, true
}

func (d *Daemon) handleCompanionSourceFetch(w http.ResponseWriter, r *http.Request) {
	req, ok := d.companionSourceGate(w, r)
	if !ok {
		return
	}
	// Every listing click downloads again: this is how a user checks for
	// updates, and it refreshes the one-entry cache.
	plan, status, failure := d.companionSourcePlan(r.Context(), req.Source, false)
	if failure != nil {
		writeJSON(w, status, companionInstallResult{Listing: *failure})
		return
	}
	result, err := d.companionPacks.install(d.companionOptions(), plan, nil, true)
	if err != nil {
		companionOperationError(w, err)
		return
	}
	d.companionOfficialBuiltin(&result.Listing, plan.origin)
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleCompanionSourceInstall(w http.ResponseWriter, r *http.Request) {
	req, ok := d.companionSourceGate(w, r)
	if !ok {
		return
	}
	ids, ok := companionSelectedIDs(w, req.IDs)
	if !ok {
		return
	}
	plan, status, failure := d.companionSourcePlan(r.Context(), req.Source, true)
	if failure != nil {
		writeJSON(w, status, companionInstallResult{Listing: *failure})
		return
	}
	result, err := d.companionPacks.install(d.companionOptions(), plan, ids, false)
	if err != nil {
		companionOperationError(w, err)
		return
	}
	d.companionOfficialBuiltin(&result.Listing, plan.origin)
	writeJSON(w, http.StatusOK, result)
}

// companionSelectedIDs checks a selected pack ID list: at least one ID, each
// a valid pack ID, duplicates dropped.
func companionSelectedIDs(w http.ResponseWriter, raw []string) ([]string, bool) {
	var ids []string
	seen := map[string]bool{}
	for _, id := range raw {
		if !companionPackID.MatchString(id) {
			companionWriteError(w, http.StatusBadRequest, "a selected pack ID is not valid")
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		companionWriteError(w, http.StatusBadRequest, "select at least one pack to install")
		return nil, false
	}
	return ids, true
}

// companionSourcePlan opens a source as an install plan. The official
// source is the embedded marketplace and needs no download. A user source
// is downloaded, or, for an install (useCache), taken from the cache when
// the last listing fetched it less than companionSourceCacheTTL ago. On a
// failure it returns the status and a listing holding the error.
func (d *Daemon) companionSourcePlan(ctx context.Context, source string, useCache bool) (companionInstallPlan, int, *companionListing) {
	inflate := newCompanionInflate()
	external := func(entry companionMarketplaceEntry, inflate *companionInflate) (*companionArchive, error) {
		return d.companionFetch.fetchPack(ctx, entry, inflate)
	}
	fail := func(status int, origin companionInstallOrigin, err error) (companionInstallPlan, int, *companionListing) {
		packErr := companionAsPackError(err, "")
		return companionInstallPlan{}, status, &companionListing{Source: origin.Source, Kind: origin.Kind,
			Official: origin.Official, Commit: origin.Commit, Packs: []companionListingPack{}, Errors: []companionPackError{packErr}}
	}
	if source == companionOfficialSource {
		origin := companionInstallOrigin{Source: companionOfficialSource, Kind: "zip", Official: true}
		market, err := parseCompanionMarketplace(companionOfficialMarketplace)
		if err != nil {
			return fail(http.StatusInternalServerError, origin, err)
		}
		archive := &companionArchive{files: map[string]*zip.File{}, dirs: map[string]struct{}{}, inflate: inflate}
		return companionInstallPlan{archive: archive, market: market, origin: origin, external: external}, 0, nil
	}

	now := time.Now()
	fetched, cached := companionFetchedSource{}, false
	if useCache {
		fetched, cached = d.companionSources.take(source, now)
	}
	if !cached {
		var err error
		if fetched, err = d.companionFetch.fetchSource(ctx, source); err != nil {
			return fail(http.StatusBadGateway, companionInstallOrigin{Source: source}, err)
		}
		d.companionSources.put(fetched, now)
	}
	archive, err := openCompanionArchive(fetched.data, inflate)
	if err == nil {
		var market companionMarketplace
		if market, err = archive.marketplace(); err == nil {
			return companionInstallPlan{archive: archive, market: market, origin: fetched.origin, external: external}, 0, nil
		}
	}
	if errors.Is(err, errCompanionNotZip) {
		err = errors.New("the source did not return a zip archive")
	}
	return fail(http.StatusUnprocessableEntity, fetched.origin, err)
}

// companionOfficialBuiltin lists the embedded default pack first in the
// official listing, as built in and installed. It has no install record, so
// no action is offered for it.
func (d *Daemon) companionOfficialBuiltin(listing *companionListing, origin companionInstallOrigin) {
	if !origin.Official {
		return
	}
	item := companionListingPack{ID: companionDefaultID, Name: companionDefaultID, Status: companionStatusBuiltin}
	if pack := d.companionPacks.snapshot(d.companionOptions()).byID[companionDefaultID]; pack != nil {
		item.Name, item.Rev = pack.info.Name, pack.info.Rev
	}
	listing.Packs = append([]companionListingPack{item}, listing.Packs...)
}
