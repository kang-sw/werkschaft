package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// companionUploadCap bounds one uploaded zip body, and companionVrmImportCap
// bounds one uploaded .vrm file. Both are variables so tests can lower them.
var (
	companionUploadCap    int64 = 64 << 20
	companionVrmImportCap int64 = companionVrmAssetCap
)

const (
	companionUploadNameMax      = 128
	companionUploadNameFallback = "upload.zip"
	companionMultipartOverhead  = 64 << 10
	companionMultipartFieldCap  = 1 << 10
)

func (d *Daemon) registerCompanionInstallRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionPacksRoute+"/validate", d.handleCompanionValidate)
	mux.HandleFunc(companionPacksRoute+"/import", d.handleCompanionImport)
	mux.HandleFunc(companionPacksRoute+"/import-vrm", d.handleCompanionImportVRM)
	mux.HandleFunc(companionPacksRoute+"/{id}/uninstall", d.handleCompanionUninstall)
	d.registerCompanionSourceRoutes(mux)
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
	// An import click may download the external packs its marketplace lists,
	// even while companion display is off so users can prepare packs beforehand.
	plan.external = func(entry companionMarketplaceEntry, inflate *companionInflate) (*companionArchive, error) {
		return d.companionFetch.fetchPack(r.Context(), entry, inflate)
	}
	result, err := d.companionPacks.install(d.companionOptions(), plan, ids, false)
	if err != nil {
		companionOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleCompanionImportVRM(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		companionWriteError(w, http.StatusUnsupportedMediaType, "content type must be model/gltf-binary, application/octet-stream, or multipart/form-data")
		return
	}
	var (
		data        []byte
		explicitID  string
		displayName string
		filename    string
	)
	switch mediaType {
	case "model/gltf-binary", "application/octet-stream":
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, companionVrmImportCap))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				companionWriteError(w, http.StatusRequestEntityTooLarge, "the uploaded VRM file is too large")
				return
			}
			companionWriteError(w, http.StatusBadRequest, "the uploaded VRM file could not be read")
			return
		}
		data = raw
		explicitID = r.URL.Query().Get("id")
		displayName = r.URL.Query().Get("name")
		filename = r.URL.Query().Get("filename")
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			companionWriteError(w, http.StatusBadRequest, "multipart boundary is missing")
			return
		}
		body := http.MaxBytesReader(w, r.Body, companionVrmImportCap+companionMultipartOverhead)
		mr := multipart.NewReader(body, boundary)
		hasFile := false
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					companionWriteError(w, http.StatusRequestEntityTooLarge, "the uploaded VRM file is too large")
					return
				}
				companionWriteError(w, http.StatusBadRequest, "the multipart upload could not be read")
				return
			}
			field := part.FormName()
			switch field {
			case "file":
				if hasFile {
					part.Close()
					companionWriteError(w, http.StatusBadRequest, "only one file part is allowed")
					return
				}
				raw, readErr := io.ReadAll(io.LimitReader(part, companionVrmImportCap+1))
				part.Close()
				if readErr != nil {
					var tooLarge *http.MaxBytesError
					if errors.As(readErr, &tooLarge) {
						companionWriteError(w, http.StatusRequestEntityTooLarge, "the uploaded VRM file is too large")
						return
					}
					companionWriteError(w, http.StatusBadRequest, "the uploaded VRM file could not be read")
					return
				}
				if int64(len(raw)) > companionVrmImportCap {
					companionWriteError(w, http.StatusRequestEntityTooLarge, "the uploaded VRM file is too large")
					return
				}
				hasFile = true
				data = raw
				filename = part.FileName()
			case "id", "name":
				raw, readErr := io.ReadAll(io.LimitReader(part, companionMultipartFieldCap+1))
				part.Close()
				if readErr != nil {
					var tooLarge *http.MaxBytesError
					if errors.As(readErr, &tooLarge) {
						companionWriteError(w, http.StatusRequestEntityTooLarge, "the uploaded VRM file is too large")
						return
					}
					companionWriteError(w, http.StatusBadRequest, "the multipart field could not be read")
					return
				}
				if len(raw) > companionMultipartFieldCap {
					companionWriteError(w, http.StatusBadRequest, "multipart field is too large")
					return
				}
				if field == "id" {
					explicitID = string(raw)
				} else {
					displayName = string(raw)
				}
			default:
				_, _ = io.CopyN(io.Discard, part, companionMultipartFieldCap)
				part.Close()
			}
		}
		if !hasFile {
			companionWriteError(w, http.StatusBadRequest, "file part is required")
			return
		}
		if explicitID == "" {
			explicitID = r.URL.Query().Get("id")
		}
		if displayName == "" {
			displayName = r.URL.Query().Get("name")
		}
	default:
		companionWriteError(w, http.StatusUnsupportedMediaType, "content type must be model/gltf-binary, application/octet-stream, or multipart/form-data")
		return
	}

	info, err := d.companionPacks.importVRM(d.companionOptions(), data, explicitID, displayName, filename)
	if err != nil {
		companionOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
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
