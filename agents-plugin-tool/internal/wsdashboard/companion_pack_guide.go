package wsdashboard

import (
	"embed"
	"net/http"
	"strconv"
)

// The pack authoring kit: a guide and two JSON Schemas for pack authors, who
// work outside this binary. They are shipped text and stand alone; the
// canonical rigging contract stays the repository manual, and a drift test
// keeps the kit consistent with the validators and that manual.
//
//go:embed assets/companion/authoring/guide.md assets/companion/authoring/pack.schema.json assets/companion/authoring/marketplace.schema.json
var companionAuthoringFS embed.FS

// Routes of the authoring kit, on the companion route family. The dashboard's
// companion modal links them.
const (
	companionAuthoringGuideRoute             = "/api/companion/authoring/guide.md"
	companionAuthoringPackSchemaRoute        = "/api/companion/authoring/pack.schema.json"
	companionAuthoringMarketplaceSchemaRoute = "/api/companion/authoring/marketplace.schema.json"
)

func (d *Daemon) registerCompanionAuthoringRoutes(mux *http.ServeMux) {
	for _, doc := range []struct{ route, file, contentType string }{
		{companionAuthoringGuideRoute, "guide.md", "text/markdown; charset=utf-8"},
		{companionAuthoringPackSchemaRoute, "pack.schema.json", "application/schema+json"},
		{companionAuthoringMarketplaceSchemaRoute, "marketplace.schema.json", "application/schema+json"},
	} {
		mux.HandleFunc(doc.route, companionAuthoringHandler(doc.file, doc.contentType))
	}
}

// companionAuthoringHandler serves one embedded document as a read route, the
// way the catalog route does: GET and HEAD only, behind the same access
// handling as every other dashboard route, with no write trust required.
func companionAuthoringHandler(file, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Cache-Control", "private, no-store")
		if !companionReadMethod(w, r) {
			return
		}
		data, err := companionAuthoringFS.ReadFile("assets/companion/authoring/" + file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		header.Set("Content-Type", contentType)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	}
}
