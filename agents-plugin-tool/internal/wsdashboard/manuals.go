package wsdashboard

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdoc"
)

// manualRow is one ai-docs/manuals/*.md entry as the Workpanel Manuals tab
// lists it. Path is project-relative so the client can hand it to the file
// preview; Stem is the file name without ".md".
type manualRow struct {
	Path    string `json:"path"`
	Stem    string `json:"stem"`
	Summary string `json:"summary"`
}

// handleManuals lists the session project's manuals on explicit demand (the
// Manuals tab opening), never as part of the page render or a live tick. The
// project is the session's registered one: the route takes no root parameter.
func (d *Daemon) handleManuals(w http.ResponseWriter, r *http.Request, replyID string) {
	if d.index == nil || !ValidReplyID(replyID) {
		http.NotFound(w, r)
		return
	}
	session, ok := d.index.Session(replyID)
	if !ok || session.Registration.Project == "" {
		http.NotFound(w, r)
		return
	}
	infos, err := wsdoc.ManualsList(session.Registration.Project)
	if err != nil {
		http.Error(w, "manuals read failed", http.StatusInternalServerError)
		return
	}
	rows := make([]manualRow, 0, len(infos))
	for _, info := range infos {
		rows = append(rows, manualRow{
			Path:    info.Path,
			Stem:    strings.TrimSuffix(path.Base(info.Path), ".md"),
			Summary: info.Summary,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(struct {
		Manuals []manualRow `json:"manuals"`
	}{rows})
}
