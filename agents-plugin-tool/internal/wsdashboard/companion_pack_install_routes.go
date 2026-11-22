package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// companionUploadCap bounds one uploaded zip body. It is a variable so a
// test can lower it.
var companionUploadCap int64 = 64 << 20

const (
	companionUploadNameMax      = 128
	companionUploadNameFallback = "upload.zip"
)

func (d *Daemon) registerCompanionInstallRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionPacksRoute+"/validate", d.handleCompanionValidate)
	mux.HandleFunc(companionPacksRoute+"/import", d.handleCompanionImport)
	mux.HandleFunc(companionPacksRoute+"/{id}/uninstall", d.handleCompanionUninstall)
}

// companionTrustedPost gates an install-family route: POST only, a local
// viewer, and a same-origin request. It answers and returns false otherwise.
func companionTrustedPost(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return false
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return false
	}
	return true
}

// companionUploadName reduces a browser-supplied file name to the text shown
// as "local file <name>": the part after the last path separator, trimmed,
// without control characters, at most companionUploadNameMax code points.
func companionUploadName(raw string) string {
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || !utf8.ValidString(raw) {
		return companionUploadNameFallback
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return companionUploadNameFallback
		}
	}
	if utf8.RuneCountInString(raw) > companionUploadNameMax {
		raw = string([]rune(raw)[:companionUploadNameMax])
	}
	return raw
}

func companionWriteError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func companionOperationError(w http.ResponseWriter, err error) {
	var op *companionInstallError
	if errors.As(err, &op) {
		companionWriteError(w, op.status, op.message)
		return
	}
	companionWriteError(w, http.StatusInternalServerError, "the companion pack operation failed")
}

// companionReadUpload reads the zip body within companionUploadCap. It
// answers the request itself and returns false when the body is too large or
// unreadable.
func companionReadUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, companionUploadCap))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			companionWriteError(w, http.StatusRequestEntityTooLarge, "the uploaded file is too large")
			return nil, false
		}
		companionWriteError(w, http.StatusBadRequest, "the uploaded file could not be read")
		return nil, false
	}
	return data, true
}

// companionUploadPlan opens an uploaded zip in memory. On an archive- or
// marketplace-level failure it answers 422 with a listing holding that one
// error and returns false.
func companionUploadPlan(w http.ResponseWriter, r *http.Request, data []byte) (companionInstallPlan, bool) {
	sum := sha256.Sum256(data)
	origin := companionInstallOrigin{Source: companionUploadName(r.URL.Query().Get("name")), Kind: "local",
		ArchiveSHA256: hex.EncodeToString(sum[:])}
	fail := func(err error) (companionInstallPlan, bool) {
		packErr := companionAsPackError(err, "")
		if errors.Is(err, errCompanionNotZip) {
			packErr = companionPackError{Message: "the file is not a zip archive; export the packs as a .zip file and upload that"}
		}
		writeJSON(w, http.StatusUnprocessableEntity, companionInstallResult{Listing: companionListing{
			Source: origin.Source, Kind: origin.Kind, Packs: []companionListingPack{},
			Errors: []companionPackError{packErr}}})
		return companionInstallPlan{}, false
	}
	archive, err := openCompanionArchive(data, newCompanionInflate())
	if err != nil {
		return fail(err)
	}
	market, err := archive.marketplace()
	if err != nil {
		return fail(err)
	}
	return companionInstallPlan{archive: archive, market: market, origin: origin}, true
}

func (d *Daemon) handleCompanionValidate(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	data, ok := companionReadUpload(w, r)
	if !ok {
		return
	}
	plan, ok := companionUploadPlan(w, r, data)
	if !ok {
		return
	}
	result, err := d.companionPacks.install(d.companionOptions(), plan, nil, true)
	if err != nil {
		companionOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleCompanionImport(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range r.URL.Query()["id"] {
		if !companionPackID.MatchString(id) {
			companionWriteError(w, http.StatusBadRequest, "a selected pack ID is not valid")
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		companionWriteError(w, http.StatusBadRequest, "select at least one pack to import")
		return
	}
	data, ok := companionReadUpload(w, r)
	if !ok {
		return
	}
	plan, ok := companionUploadPlan(w, r, data)
	if !ok {
		return
	}
	result, err := d.companionPacks.install(d.companionOptions(), plan, ids, false)
	if err != nil {
		companionOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleCompanionUninstall(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	snapshot, err := d.companionPacks.uninstall(d.companionOptions(), r.PathValue("id"))
	if err != nil {
		companionOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.catalog("", ""))
}
