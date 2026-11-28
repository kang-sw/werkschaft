package wsdashboard

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// Authoring write API for pack clips (actions, sentinels, calibration). Every
// write is local-viewer and same-origin only, carries If-Match with the pack
// revision, takes installMu then scanMu, refuses when pack.json changed on
// disk since the last scan, writes through pinned directory handles, and
// publishes a copy-on-write snapshot rebuilt from memory (no rescan).

const (
	companionClipBodyOverhead = 64 << 10
	companionCalibrationCap   = 64 << 10
	companionRemoteAuthorMsg  = "packs installed from a source are replaced on update; import the .vrm as a local pack to author clips"
	companionDriftMsg         = "pack.json changed on disk; reload the pack"
)

func (d *Daemon) registerCompanionClipRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionPacksRoute+"/{id}/clips/{name}", d.handleCompanionClip)
	// A literal segment is more specific than {name}; keys never hold "-".
	mux.HandleFunc(companionPacksRoute+"/{id}/clips/import-vrma", d.handleCompanionClipImport)
	mux.HandleFunc(companionPacksRoute+"/{id}/calibration", d.handleCompanionCalibration)
}

// companionClipError is an authoring failure with an HTTP status; rev, when
// set, is reported as X-Companion-Current-Rev.
type companionClipError struct {
	status  int
	message string
	rev     string
}

func (e *companionClipError) Error() string { return e.message }

func companionClipRefuse(status int, format string, args ...any) error {
	return &companionClipError{status: status, message: fmt.Sprintf(format, args...)}
}

func companionClipFail(w http.ResponseWriter, err error) {
	var ce *companionClipError
	if errors.As(err, &ce) {
		if ce.rev != "" {
			w.Header().Set("X-Companion-Current-Rev", ce.rev)
		}
		companionWriteError(w, ce.status, ce.message)
		return
	}
	companionOperationError(w, err)
}

// companionClipChange is the planned effect of one write: a new pack.json
// (nil leaves it), one clip file to write, and clip files to unlink after
// pack.json commits.
type companionClipChange struct {
	manifest []byte
	write    string
	data     []byte
	remove   []string
}

type companionClipResult struct {
	Pack     companionPackInfo `json:"pack"`
	Warnings []string          `json:"warnings,omitempty"`
}

// --- HTTP handlers ---

// companionAuthorGate answers 404 for a non-local viewer and 403 for a
// cross-origin request, after the caller's method check, then requires
// If-Match (428).
func companionAuthorGate(w http.ResponseWriter, r *http.Request) (string, bool) {
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return "", false
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return "", false
	}
	rev, ok := companionIfMatch(r)
	if !ok {
		companionWriteError(w, http.StatusPreconditionRequired, `If-Match with the pack revision ("<rev>") is required`)
		return "", false
	}
	return rev, true
}

func companionIfMatch(r *http.Request) (string, bool) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 {
		return "", false
	}
	v := strings.TrimSpace(values[0])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	if v == "" || v == "*" || strings.ContainsAny(v, `", `) {
		return "", false
	}
	return v, true
}

// companionReadBody reads at most limit bytes, answering 413 or 400 itself.
func companionReadBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			companionWriteError(w, http.StatusRequestEntityTooLarge, "the request body is too large")
			return nil, false
		}
		companionWriteError(w, http.StatusBadRequest, "the request body could not be read")
		return nil, false
	}
	return data, true
}

func (d *Daemon) handleCompanionClip(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		d.serveCompanionClip(w, r)
		return
	case http.MethodPut, http.MethodDelete:
	default:
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Allow", "GET, HEAD, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	rev, ok := companionAuthorGate(w, r)
	if !ok {
		return
	}
	id, key := r.PathValue("id"), r.PathValue("name")
	if !validCompanionClipKey(key) {
		companionWriteError(w, http.StatusBadRequest, "clip keys are 1-32 lowercase letters, digits or _, starting with a letter, and not a device name")
		return
	}
	var plan func(*companionPack) (*companionClipChange, error)
	if r.Method == http.MethodDelete {
		plan = planCompanionClipDelete(key)
	} else {
		body, ok := companionReadBody(w, r, companionClipFileCap+companionClipBodyOverhead)
		if !ok {
			return
		}
		var err error
		if plan, err = planCompanionClipPut(key, body); err != nil {
			companionClipFail(w, err)
			return
		}
	}
	result, err := d.companionPacks.editPack(d.companionOptions(), id, rev, plan)
	if err != nil {
		companionClipFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleCompanionClipImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rev, ok := companionAuthorGate(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	key := q.Get("key")
	if !validCompanionClipKey(key) {
		companionWriteError(w, http.StatusBadRequest, "clip keys are 1-32 lowercase letters, digits or _, starting with a letter, and not a device name")
		return
	}
	data, ok := companionReadBody(w, r, companionVrmaAssetCap)
	if !ok {
		return
	}
	if err := validateCompanionVRMA(data); err != nil {
		companionWriteError(w, http.StatusBadRequest, "invalid .vrma file: "+err.Error())
		return
	}
	plan := planCompanionClipImport(key, data, q.Get("replace") == "1", q.Get("expose") == "1", q.Get("sentinel") == "1")
	result, err := d.companionPacks.editPack(d.companionOptions(), r.PathValue("id"), rev, plan)
	if err != nil {
		companionClipFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleCompanionCalibration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rev, ok := companionAuthorGate(w, r)
	if !ok {
		return
	}
	body, ok := companionReadBody(w, r, companionCalibrationCap)
	if !ok {
		return
	}
	if err := validateCompanionCalibration(body); err != nil {
		companionWriteError(w, http.StatusBadRequest, "calibration: "+err.Error())
		return
	}
	result, err := d.companionPacks.editPack(d.companionOptions(), r.PathValue("id"), rev, planCompanionCalibration(body))
	if err != nil {
		companionClipFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// serveCompanionClip serves a loaded clip from snapshot memory with the
// pack's revision, ETag and sandbox CSP.
func (d *Daemon) serveCompanionClip(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("name")
	match := companionClipFileName.FindStringSubmatch(file)
	if match == nil {
		w.Header().Set("Cache-Control", "private, no-store")
		http.NotFound(w, r)
		return
	}
	contentType := "application/json"
	if match[2] == "vrma" {
		contentType = "model/gltf-binary"
	}
	d.serveCompanionPackData(w, r, contentType, true, func(pack *companionPack) ([]byte, bool) {
		data, ok := pack.clips[companionClipsDirName+"/"+file]
		return data, ok
	})
}

// --- write plans (pure; they read the published pack only) ---

func companionManifestObject(pack *companionPack) (*companionOrderedObject, error) {
	obj, err := parseCompanionOrderedObject(pack.manifest)
	if err != nil {
		return nil, companionClipRefuse(http.StatusConflict, "pack.json cannot be rewritten safely (%s); fix it by hand", err)
	}
	return obj, nil
}

func companionActionItems(obj *companionOrderedObject) ([]json.RawMessage, error) {
	raw, ok := obj.values["actions"]
	if !ok {
		return nil, nil
	}
	var items []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &items) != nil {
		return nil, companionClipRefuse(http.StatusConflict, "pack.json actions is not an array; fix it by hand")
	}
	return items, nil
}

func companionSentinelObject(obj *companionOrderedObject) (*companionOrderedObject, error) {
	raw, ok := obj.values["sentinels"]
	if !ok {
		return &companionOrderedObject{values: map[string]json.RawMessage{}}, nil
	}
	sentinels, err := parseCompanionOrderedObject(raw)
	if err != nil {
		return nil, companionClipRefuse(http.StatusConflict, "pack.json sentinels is not an object; fix it by hand")
	}
	return sentinels, nil
}

func companionEncodeManifest(obj *companionOrderedObject) []byte {
	return append(obj.encode(""), '\n')
}

// companionCatalogEntry finds the parsed entry for key.
func companionCatalogEntry(pack *companionPack, key string) (companionClipEntry, bool) {
	if pack.catalog == nil {
		return companionClipEntry{}, false
	}
	if e, ok := pack.catalog.sentinels[key]; ok {
		return e, true
	}
	for _, e := range pack.catalog.actions {
		if e.Key == key {
			return e, true
		}
	}
	return companionClipEntry{}, false
}

func companionOtherKind(kind string) string {
	if kind == companionClipKindVRMA {
		return companionClipKindClip
	}
	return companionClipKindVRMA
}

// companionSetEntry writes entry (already validated) for key into the
// manifest. It reports false when the stored entry is already equal.
func companionSetEntry(obj *companionOrderedObject, key string, entry []byte) (bool, error) {
	value, err := companionIndentValue(entry, "    ")
	if err != nil {
		return false, companionClipRefuse(http.StatusBadRequest, "entry must be JSON")
	}
	if companionSentinelKey(key) {
		sentinels, err := companionSentinelObject(obj)
		if err != nil {
			return false, err
		}
		if old, ok := sentinels.values[key]; ok && companionSameJSON(old, entry) {
			return false, nil
		}
		sentinels.set(key, value)
		obj.set("sentinels", sentinels.encode("  "))
		return true, nil
	}
	items, err := companionActionItems(obj)
	if err != nil {
		return false, err
	}
	if idx := companionActionIndices(items, key); len(idx) > 0 {
		if len(idx) == 1 && companionSameJSON(items[idx[0]], entry) {
			return false, nil
		}
		items[idx[0]] = value
		items = companionActionsWithout(items, idx[1:])
	} else {
		if len(items) >= companionActionsMax {
			return false, companionClipRefuse(http.StatusRequestEntityTooLarge, "a pack holds at most %d actions", companionActionsMax)
		}
		items = append(items, value)
	}
	obj.set("actions", companionEncodeArray(items, "  "))
	return true, nil
}

// companionExistingEntry returns the raw stored entry for key, if any.
func companionExistingEntry(obj *companionOrderedObject, key string) (json.RawMessage, error) {
	if companionSentinelKey(key) {
		sentinels, err := companionSentinelObject(obj)
		if err != nil {
			return nil, err
		}
		return sentinels.values[key], nil
	}
	items, err := companionActionItems(obj)
	if err != nil {
		return nil, err
	}
	if idx := companionActionIndices(items, key); len(idx) > 0 {
		return items[idx[0]], nil
	}
	return nil, nil
}

// planCompanionClipPut validates a PUT body {clip?, entry} up front and
// returns the plan that applies it to the published pack.
func planCompanionClipPut(key string, body []byte) (func(*companionPack) (*companionClipChange, error), error) {
	req, err := parseCompanionObject(body)
	if err != nil {
		return nil, companionClipRefuse(http.StatusBadRequest, "the body must be a JSON object {clip?, entry}")
	}
	entryRaw, ok := req["entry"]
	if !ok {
		return nil, companionClipRefuse(http.StatusBadRequest, "entry is required")
	}
	sentinelKey := ""
	if companionSentinelKey(key) {
		sentinelKey = key
	}
	entry, err := parseCompanionClipEntry(entryRaw, sentinelKey)
	if err != nil {
		return nil, companionClipRefuse(http.StatusBadRequest, "entry: %s", err)
	}
	if entry.Key != key {
		return nil, companionClipRefuse(http.StatusBadRequest, "entry.key must match the clip key in the URL")
	}
	if sentinelKey == "" {
		if err := companionDescriptionText(entry.Description); err != nil {
			return nil, companionClipRefuse(http.StatusBadRequest, "entry: %s", err)
		}
	}
	var data []byte
	if clipRaw, ok := req["clip"]; ok && !bytes.Equal(bytes.TrimSpace(clipRaw), []byte("null")) {
		if entry.Kind != companionClipKindClip {
			return nil, companionClipRefuse(http.StatusBadRequest, `clip is only accepted with entry kind "clip"`)
		}
		var b bytes.Buffer
		if err := json.Indent(&b, clipRaw, "", "  "); err != nil {
			return nil, companionClipRefuse(http.StatusBadRequest, "clip must be JSON")
		}
		b.WriteByte('\n')
		if b.Len() > companionClipFileCap {
			return nil, companionClipRefuse(http.StatusRequestEntityTooLarge, "clip exceeds the %s limit", companionByteSize(companionClipFileCap))
		}
		data = b.Bytes()
		if _, err := validateCompanionClip(data, key); err != nil {
			return nil, companionClipRefuse(http.StatusBadRequest, "clip: %s", err)
		}
	}
	return func(pack *companionPack) (*companionClipChange, error) {
		change := &companionClipChange{}
		if data != nil {
			if old, ok := pack.clips[entry.File]; !ok || !bytes.Equal(old, data) {
				change.write, change.data = entry.File, data
			}
		} else if _, ok := pack.clips[entry.File]; !ok {
			if entry.Kind == companionClipKindVRMA {
				return nil, companionClipRefuse(http.StatusBadRequest, "import the .vrma file for this key first")
			}
			return nil, companionClipRefuse(http.StatusBadRequest, "clip is required: this key has no loaded clip file")
		}
		obj, err := companionManifestObject(pack)
		if err != nil {
			return nil, err
		}
		changed, err := companionSetEntry(obj, key, entryRaw)
		if err != nil {
			return nil, err
		}
		if changed {
			change.manifest = companionEncodeManifest(obj)
		}
		other := companionClipFile(key, companionOtherKind(entry.Kind))
		prev, had := companionCatalogEntry(pack, key)
		if _, loaded := pack.clips[other]; loaded || (had && prev.Kind != entry.Kind) {
			change.remove = append(change.remove, other)
		}
		return change, nil
	}, nil
}

func planCompanionClipDelete(key string) func(*companionPack) (*companionClipChange, error) {
	return func(pack *companionPack) (*companionClipChange, error) {
		obj, err := companionManifestObject(pack)
		if err != nil {
			return nil, err
		}
		if companionSentinelKey(key) {
			sentinels, err := companionSentinelObject(obj)
			if err != nil {
				return nil, err
			}
			if _, ok := sentinels.values[key]; !ok {
				return nil, companionClipRefuse(http.StatusNotFound, "clip key not found")
			}
			sentinels.set(key, nil)
			obj.set("sentinels", sentinels.encode("  "))
		} else {
			items, err := companionActionItems(obj)
			if err != nil {
				return nil, err
			}
			idx := companionActionIndices(items, key)
			if len(idx) == 0 {
				return nil, companionClipRefuse(http.StatusNotFound, "clip key not found")
			}
			// Every element with the key goes, so a shadowed duplicate
			// cannot become live pointing at the unlinked files.
			items = companionActionsWithout(items, idx)
			obj.set("actions", companionEncodeArray(items, "  "))
		}
		return &companionClipChange{
			manifest: companionEncodeManifest(obj),
			remove:   []string{companionClipFile(key, companionClipKindClip), companionClipFile(key, companionClipKindVRMA)},
		}, nil
	}
}

func planCompanionClipImport(key string, data []byte, replace, expose, sentinel bool) func(*companionPack) (*companionClipChange, error) {
	return func(pack *companionPack) (*companionClipChange, error) {
		isSentinel := companionSentinelKey(key)
		switch {
		case isSentinel && !sentinel:
			return nil, companionClipRefuse(http.StatusBadRequest, "%s is a sentinel key; set sentinel=1 to override it", key)
		case !isSentinel && sentinel:
			return nil, companionClipRefuse(http.StatusBadRequest, "%s is not a sentinel key", key)
		}
		obj, err := companionManifestObject(pack)
		if err != nil {
			return nil, err
		}
		old, err := companionExistingEntry(obj, key)
		if err != nil {
			return nil, err
		}
		if old != nil && !replace {
			return nil, companionClipRefuse(http.StatusConflict, "clip key %s already exists; set replace=1 to replace its file", key)
		}
		file := companionClipFile(key, companionClipKindVRMA)
		var entry []byte
		if old == nil {
			if isSentinel {
				entry, _ = json.Marshal(map[string]any{"kind": companionClipKindVRMA, "file": file})
			} else {
				entry, _ = json.Marshal(struct {
					Key         string `json:"key"`
					Kind        string `json:"kind"`
					File        string `json:"file"`
					Description string `json:"description"`
					Expose      bool   `json:"expose_to_agent"`
				}{key, companionClipKindVRMA, file, "", false})
			}
		} else {
			fields, err := parseCompanionOrderedObject(old)
			if err != nil {
				return nil, companionClipRefuse(http.StatusConflict, "the existing entry for %s is not a JSON object; fix pack.json by hand", key)
			}
			kind, _ := json.Marshal(companionClipKindVRMA)
			path, _ := json.Marshal(file)
			fields.set("kind", kind)
			fields.set("file", path)
			if !isSentinel {
				// An inherited description reaches the agent only through an
				// explicit expose.
				flag, _ := json.Marshal(expose)
				fields.set("expose_to_agent", flag)
			}
			entry = fields.encode("")
			sentinelKey := ""
			if isSentinel {
				sentinelKey = key
			}
			parsed, err := parseCompanionClipEntry(entry, sentinelKey)
			if err != nil {
				return nil, companionClipRefuse(http.StatusConflict, "the existing entry for %s is invalid (%s); delete it first", key, err)
			}
			if !isSentinel && expose {
				if err := companionDescriptionText(parsed.Description); err != nil {
					return nil, companionClipRefuse(http.StatusBadRequest, "cannot expose: %s", err)
				}
			}
		}
		change := &companionClipChange{}
		if prev, ok := pack.clips[file]; !ok || !bytes.Equal(prev, data) {
			change.write, change.data = file, data
		}
		changed, err := companionSetEntry(obj, key, entry)
		if err != nil {
			return nil, err
		}
		if changed {
			change.manifest = companionEncodeManifest(obj)
		}
		other := companionClipFile(key, companionClipKindClip)
		prev, had := companionCatalogEntry(pack, key)
		if _, loaded := pack.clips[other]; loaded || (had && prev.Kind != companionClipKindVRMA) {
			change.remove = append(change.remove, other)
		}
		return change, nil
	}
}

func planCompanionCalibration(body []byte) func(*companionPack) (*companionClipChange, error) {
	return func(pack *companionPack) (*companionClipChange, error) {
		obj, err := companionManifestObject(pack)
		if err != nil {
			return nil, err
		}
		if old, ok := obj.values["calibration"]; ok && companionSameJSON(old, body) {
			return &companionClipChange{}, nil
		}
		value, err := companionIndentValue(body, "  ")
		if err != nil {
			return nil, companionClipRefuse(http.StatusBadRequest, "calibration must be JSON")
		}
		obj.set("calibration", value)
		return &companionClipChange{manifest: companionEncodeManifest(obj)}, nil
	}
}

// --- store write path ---

// editPack applies one authoring write to a loaded, authorable VRM pack.
// File operations are traced through fsop; a single-clip save stays within
// the 16-operation budget and lists no directory.
func (s *CompanionPackStore) editPack(opts wsconfig.Options, id, rev string, plan func(*companionPack) (*companionClipChange, error)) (companionClipResult, error) {
	var result companionClipResult
	if !companionPackID.MatchString(id) || id == "auto" {
		return result, companionClipRefuse(http.StatusNotFound, "pack not found")
	}
	paths, err := companionPaths(opts)
	if err != nil {
		return result, err
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	s.snapshot(opts)
	// scanMu is held from the drift read through the swap, so a concurrent
	// reload neither publishes a scan from before the write nor interleaves.
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	snapshot := s.active.Load()
	pack := snapshot.byID[id]
	switch {
	case id == companionDefaultID:
		return result, companionClipRefuse(http.StatusConflict, "the built-in pack cannot be authored")
	case pack == nil:
		return result, companionClipRefuse(http.StatusNotFound, "pack not found")
	case pack.info.Source != "local" || pack.info.Schema != companionPackVRMSchema:
		return result, companionClipRefuse(http.StatusConflict, "only local VRM packs carry clips")
	case pack.info.Origin != nil && pack.info.Origin.Kind != "local":
		return result, companionClipRefuse(http.StatusConflict, companionRemoteAuthorMsg)
	}
	if rev != pack.info.Rev {
		return result, &companionClipError{status: http.StatusPreconditionFailed, message: "the pack changed since it was loaded; reload it and retry", rev: pack.info.Rev}
	}
	change, err := plan(pack)
	if err != nil {
		return result, err
	}
	if change.manifest == nil && change.write == "" && len(change.remove) == 0 {
		result.Pack = pack.info
		return result, nil
	}

	// Caps are checked before any file is touched.
	manifest := pack.manifest
	if change.manifest != nil {
		manifest = change.manifest
		if len(manifest) > companionManifestCap {
			return result, companionClipRefuse(http.StatusRequestEntityTooLarge, "pack.json would exceed the %s limit", companionByteSize(companionManifestCap))
		}
	}
	clips := make(map[string][]byte, len(pack.clips)+1)
	for name, data := range pack.clips {
		clips[name] = data
	}
	if change.write != "" {
		clips[change.write] = change.data
	}
	for _, name := range change.remove {
		delete(clips, name)
	}
	size := len(manifest)
	for _, name := range pack.files {
		size += len(pack.assets[name])
	}
	for _, data := range clips {
		size += len(data)
	}
	others := 0
	for otherID, other := range snapshot.byID {
		if otherID != id && other.info.Source == "local" {
			others += other.size
		}
	}
	if size > companionVrmPackCap {
		return result, companionClipRefuse(http.StatusRequestEntityTooLarge, "the pack would exceed the %s limit", companionByteSize(companionVrmPackCap))
	}
	if others+size > companionCacheCap {
		return result, companionClipRefuse(http.StatusRequestEntityTooLarge, "total local pack cache byte limit exceeded")
	}
	// The next pack is built from memory before any file is touched, so a
	// rebuild failure leaves both the disk and the snapshot unchanged.
	next, err := rebuildCompanionPack(pack, manifest, clips, companionCacheCap-others)
	if err != nil {
		return result, companionClipRefuse(http.StatusConflict, "the change would not validate: %s", err)
	}

	s.fsop("open")
	root, err := openCompanionDirAt(nil, paths.packs)
	if err != nil {
		return result, companionClipRefuse(http.StatusConflict, "the companion pack directory must be a plain directory")
	}
	defer root.Close()
	s.fsop("open")
	dir, err := openCompanionDirAt(root, id)
	if err != nil {
		return result, companionClipRefuse(http.StatusConflict, "the pack directory must be a plain directory")
	}
	defer dir.Close()
	s.fsop("read")
	current, perm, err := readCompanionLocalFileMode(openCompanionPackEntry, dir, companionManifestName, companionManifestCap)
	if err != nil || !bytes.Equal(current, pack.manifest) {
		return result, companionClipRefuse(http.StatusConflict, companionDriftMsg)
	}
	// Rewritten files keep pack.json's mode (a 0600 pack stays private).
	perm |= 0o600

	var clipsDir *os.File
	if change.write != "" || len(change.remove) > 0 {
		clipsDir, err = s.openClipsDir(dir, change.write != "")
		if err != nil {
			return result, err
		}
		if clipsDir != nil {
			defer clipsDir.Close()
		}
	}
	base := func(name string) string { return strings.TrimPrefix(name, companionClipsDirName+"/") }
	if change.write != "" {
		if err := s.writeFileAt(clipsDir, base(change.write), change.data, perm); err != nil {
			return result, companionClipRefuse(http.StatusInternalServerError, "cannot write the clip file")
		}
	}
	if change.manifest != nil {
		if err := s.writeFileAt(dir, companionManifestName, change.manifest, perm); err != nil {
			msg := "cannot write pack.json; the change was rolled back"
			if change.write != "" {
				// Put the previous clip back (its bytes are in memory), or
				// remove the new one, so the pack on disk matches the
				// snapshot. A previous copy that never loaded (invalid or
				// over budget) was already replaced and cannot be restored.
				if prev, ok := pack.clips[change.write]; ok {
					_ = s.writeFileAt(clipsDir, base(change.write), prev, perm)
				} else {
					_ = s.removeAt(clipsDir, base(change.write))
					msg += "; any previous unloaded " + change.write + " on disk was replaced and is gone"
				}
			}
			return result, companionClipRefuse(http.StatusInternalServerError, "%s", msg)
		}
	}
	for _, name := range change.remove {
		if clipsDir == nil {
			break
		}
		if err := s.removeAt(clipsDir, base(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			result.Warnings = append(result.Warnings, "could not remove "+name+"; the pack no longer references it")
		}
	}

	s.active.Store(snapshot.replace(next))
	result.Pack = next.info
	return result, nil
}

// openClipsDir pins clips/ under the pack directory, creating it on the
// first save. It returns nil, nil when clips/ is absent and create is false.
func (s *CompanionPackStore) openClipsDir(dir *os.File, create bool) (*os.File, error) {
	s.fsop("open")
	clipsDir, err := openCompanionDirAt(dir, companionClipsDirName)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return nil, nil
		}
		s.fsop("mkdir")
		if err := mkdirCompanionAt(dir, companionClipsDirName); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, companionClipRefuse(http.StatusInternalServerError, "cannot create the clips directory")
		}
		s.fsop("open")
		clipsDir, err = openCompanionDirAt(dir, companionClipsDirName)
	}
	if err != nil {
		return nil, companionClipRefuse(http.StatusConflict, "the pack's clips folder must be a plain directory")
	}
	return clipsDir, nil
}

// writeFileAt replaces name in dir with data: exclusive temp create, write,
// fsync, rename relative to the pinned directory, close.
func (s *CompanionPackStore) writeFileAt(dir *os.File, name string, data []byte, perm os.FileMode) error {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmpName := "." + name + ".tmp-" + hex.EncodeToString(suffix[:])
	s.fsop("create")
	tmp, err := createCompanionTempAt(dir, tmpName, perm)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = tmp.Close()
			_ = removeCompanionAt(dir, tmpName)
		}
	}()
	s.fsop("write")
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	s.fsop("fsync")
	if err := tmp.Sync(); err != nil {
		return err
	}
	s.fsop("rename")
	if s.fault != nil {
		if err := s.fault("rename", name); err != nil {
			return err
		}
	}
	if err := renameCompanionAt(dir, tmp, tmpName, name); err != nil {
		return err
	}
	published = true
	s.fsop("close")
	return tmp.Close()
}

func (s *CompanionPackStore) removeAt(dir *os.File, name string) error {
	s.fsop("remove")
	if s.fault != nil {
		if err := s.fault("remove", name); err != nil {
			return err
		}
	}
	return removeCompanionAt(dir, name)
}

// rebuildCompanionPack builds the updated pack from bytes already in memory:
// the shared flat assets, the new manifest and the new clip set.
func rebuildCompanionPack(old *companionPack, manifest []byte, clips map[string][]byte, budget int) (*companionPack, error) {
	obj, err := parseCompanionObject(manifest)
	if err != nil {
		return nil, err
	}
	m, files, err := validateCompanionVRMManifest(obj, old.info.ID)
	if err != nil {
		return nil, err
	}
	if strings.Join(files, "\x00") != strings.Join(old.files, "\x00") {
		return nil, errors.New("the flat asset list changed")
	}
	next := &companionPack{manifest: manifest, assets: old.assets, files: old.files, size: len(manifest)}
	for _, name := range old.files {
		next.size += len(old.assets[name])
	}
	info := old.info
	info.Name, info.Persona = m.Name, m.Persona
	info.Rev = companionPackRevision(manifest, old.files, old.assets, nil)
	next.info = info
	next.catalog, next.info.ClipWarnings = parseCompanionClipCatalog(obj)
	attachCompanionClips(next, budget, func(base string, limit int) ([]byte, error) {
		data, ok := clips[companionClipsDirName+"/"+base]
		switch {
		case !ok:
			return nil, errors.New("cannot open file (only plain files are allowed)")
		case len(data) > limit:
			return nil, errCompanionFileLimit
		}
		return data, nil
	})
	return next, nil
}

// replace returns a copy of the snapshot with one pack swapped; every other
// pack pointer is shared.
func (s *companionSnapshot) replace(pack *companionPack) *companionSnapshot {
	next := &companionSnapshot{
		packs:  append([]companionPackInfo(nil), s.packs...),
		errors: append([]companionPackError(nil), s.errors...),
		byID:   make(map[string]*companionPack, len(s.byID)),
	}
	for i := range next.packs {
		if next.packs[i].ID == pack.info.ID {
			next.packs[i] = pack.info
		}
	}
	for id, p := range s.byID {
		next.byID[id] = p
	}
	next.byID[pack.info.ID] = pack
	return next
}
