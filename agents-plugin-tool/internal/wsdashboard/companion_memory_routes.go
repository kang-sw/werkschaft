package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	companionSoulRoute           = "/api/companion/soul"
	companionSoulExportRoute     = "/api/companion/soul/export"
	companionSoulImportRoute     = "/api/companion/soul/import"
	companionMemoryPendingRoute  = "/api/companion/memory/pending"
	companionMemoryEpisodesRoute = "/api/companion/memory/episodes"
)

func (d *Daemon) registerCompanionMemoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionSoulRoute, d.handleCompanionSoul)
	mux.HandleFunc(companionSoulExportRoute, d.handleCompanionSoulExport)
	mux.HandleFunc(companionSoulImportRoute, d.handleCompanionSoulImport)
	mux.HandleFunc(companionMemoryPendingRoute, d.handleCompanionMemoryPending)
	mux.HandleFunc(companionMemoryPendingRoute+"/{id}/approve", d.handleCompanionMemoryPendingApprove)
	mux.HandleFunc(companionMemoryPendingRoute+"/{id}/discard", d.handleCompanionMemoryPendingDiscard)
	mux.HandleFunc(companionMemoryEpisodesRoute, d.handleCompanionMemoryEpisodes)
	mux.HandleFunc(companionMemoryEpisodesRoute+"/prune", d.handleCompanionMemoryEpisodesPrune)
}

func (d *Daemon) companionMemoryStore() *CompanionMemory {
	d.companionMemMu.Lock()
	defer d.companionMemMu.Unlock()
	if d.companionMem == nil {
		d.companionMem = NewCompanionMemory(d.dir, d.home, nil, func(format string, args ...any) {
			if d.log != nil {
				fmt.Fprintf(d.log, "dashboard: "+format+"\n", args...)
			}
		})
	}
	return d.companionMem
}

func (d *Daemon) closeCompanionMemory() {
	d.companionMemMu.Lock()
	mem := d.companionMem
	d.companionMem = nil
	d.companionMemMu.Unlock()
	if mem != nil {
		_ = mem.Close()
	}
}

func (d *Daemon) activeProjectNames() []string {
	var names []string
	seen := map[string]struct{}{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		base := filepath.Base(p)
		if base == "" || base == "." || base == "/" {
			return
		}
		key := strings.ToLower(base)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		names = append(names, base)
	}
	if d.index != nil {
		for _, s := range d.index.Sessions() {
			add(s.Registration.Project)
			if s.Registration.GitCommonDir != "" {
				add(filepath.Dir(filepath.Clean(s.Registration.GitCommonDir)))
			}
		}
	}
	if d.workspaces != nil {
		for _, ws := range d.workspaces.workspaces() {
			add(ws.Path)
		}
	}
	return names
}

func companionLocalGet(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !localViewer(r) {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (d *Daemon) handleCompanionSoul(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	switch r.Method {
	case http.MethodGet:
		if !localViewer(r) {
			http.NotFound(w, r)
			return
		}
		d.handleCompanionSoulGet(w, r)
	case http.MethodPost:
		if !companionTrustedPost(w, r) {
			return
		}
		d.handleCompanionSoulPost(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type companionSoulResponse struct {
	SoulSnapshot
	PendingCount int `json:"pendingCount"`
	EpisodeCount int `json:"episodeCount"`
	// Presets is the embedded official persona preset catalog, default first.
	Presets []CompanionPersonaPresetView `json:"presets"`
	// ActivePresetID is the Soul's picked preset when it names a known
	// preset, else CompanionPersonaPresetDefault.
	ActivePresetID string `json:"activePresetId"`
}

func (d *Daemon) handleCompanionSoulGet(w http.ResponseWriter, r *http.Request) {
	mem := d.companionMemoryStore()
	cfg := d.companion.current().config
	soulID := strings.TrimSpace(r.URL.Query().Get("soul"))
	if soulID == "" {
		soulID = cfg.Soul
	}
	limit := companionSoulRecentDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	snap, err := mem.Soul().Load(cfg.SoulDir, soulID, limit)
	if err != nil {
		companionWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Read-only: an uncustomized Soul shows the live preset, pack, or default
	// persona it inherits; nothing is written here.
	if snap.PersonaInherited {
		resolved := d.resolveCompanionPersonaFor("", snap.PersonaPreset)
		snap.Persona = resolved.Markdown
		snap.PersonaSource = resolved.Source
	}
	if souls, err := mem.Soul().ListSouls(cfg.SoulDir, snap.SoulID); err == nil {
		snap.Souls = souls
	}
	resp := companionSoulResponse{
		SoulSnapshot:   snap,
		Presets:        CompanionPersonaPresetViews(),
		ActivePresetID: CompanionPersonaPresetDefault,
	}
	if _, ok := companionPersonaPreset(snap.PersonaPreset); ok {
		resp.ActivePresetID = snap.PersonaPreset
	}
	if ep, err := mem.Episodes(); err == nil {
		if n, err := ep.CountPending(r.Context(), snap.SoulID); err == nil {
			resp.PendingCount = n
		}
		if n, err := ep.CountEpisodes(r.Context()); err == nil {
			resp.EpisodeCount = n
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (d *Daemon) handleCompanionSoulPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		SoulID  string `json:"soulId"`
		Target  string `json:"target"`
		Content string `json:"content"`
		// PresetID names the official preset for action "preset".
		PresetID string `json:"presetId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, companionSoulDocMaxBytes+16<<10)).Decode(&body); err != nil {
		companionWriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mem := d.companionMemoryStore()
	cfg := d.companion.current().config
	soulID := strings.TrimSpace(body.SoulID)
	if soulID == "" {
		soulID = cfg.Soul
	}

	if body.Action == "create" {
		snap, err := mem.Soul().CreateSoul(cfg.SoulDir, soulID)
		if err != nil {
			companionWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if souls, err := mem.Soul().ListSouls(cfg.SoulDir, snap.SoulID); err == nil {
			snap.Souls = souls
		}
		writeJSON(w, http.StatusOK, snap)
		return
	}

	if body.Action == "reset" {
		// Reset to default: drop both the user's persona.md and the picked
		// preset, so the Soul inherits the pack or default persona again.
		if SoulPendingTarget(strings.TrimSpace(body.Target)) != SoulPendingTargetPersona {
			companionWriteError(w, http.StatusBadRequest, "only persona.md can be reset")
			return
		}
		// persona.md goes first: it is the visible tier, so a failure on the
		// second step never leaves a half-applied pick hidden behind it.
		removed, err := mem.Soul().ResetPersona(cfg.SoulDir, soulID)
		if err != nil {
			companionWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := mem.Soul().SetPersonaPreset(cfg.SoulDir, soulID, ""); err != nil {
			companionWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "reset", "removed": removed})
		return
	}
	if body.Action == "preset" {
		// Picking an official preset replaces the user's persona.md (the UI
		// confirms first); the default preset clears the pick so a pack persona
		// wins.
		presetID := strings.TrimSpace(body.PresetID)
		if _, ok := companionPersonaPreset(presetID); !ok {
			companionWriteError(w, http.StatusBadRequest, "unknown persona preset")
			return
		}
		removed, err := mem.Soul().ResetPersona(cfg.SoulDir, soulID)
		if err != nil {
			companionWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := mem.Soul().SetPersonaPreset(cfg.SoulDir, soulID, presetID); err != nil {
			companionWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "preset", "presetId": presetID, "removed": removed})
		return
	}
	if body.Action != "" && body.Action != "save" {
		companionWriteError(w, http.StatusBadRequest, "unknown soul action")
		return
	}

	target := SoulPendingTarget(strings.TrimSpace(body.Target))
	res, err := mem.SaveUserDoc(r.Context(), cfg.SoulDir, soulID, target, body.Content, d.activeProjectNames())
	if err != nil {
		companionWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusOK
	if res.Status == "held_pending" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, res)
}

func (d *Daemon) handleCompanionSoulExport(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	mem := d.companionMemoryStore()
	cfg := d.companion.current().config
	soulID := strings.TrimSpace(r.URL.Query().Get("soul"))
	if soulID == "" {
		soulID = cfg.Soul
	}
	data, filename, err := mem.Soul().ExportZIP(cfg.SoulDir, soulID)
	if err != nil {
		companionWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (d *Daemon) handleCompanionSoulImport(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, companionSoulZipMaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			companionWriteError(w, http.StatusRequestEntityTooLarge, "soul archive exceeds maximum size")
			return
		}
		companionWriteError(w, http.StatusBadRequest, "failed to read soul archive")
		return
	}
	mem := d.companionMemoryStore()
	cfg := d.companion.current().config
	soulID := strings.TrimSpace(r.URL.Query().Get("soul"))
	overwrite := r.URL.Query().Get("overwrite") == "1" || r.URL.Query().Get("overwrite") == "true"

	res, err := mem.Soul().ImportZIP(cfg.SoulDir, soulID, data, overwrite)
	if err != nil {
		companionWriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (d *Daemon) handleCompanionMemoryPending(w http.ResponseWriter, r *http.Request) {
	if !companionLocalGet(w, r) {
		return
	}
	mem := d.companionMemoryStore()
	ep, err := mem.Episodes()
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	soulID := strings.TrimSpace(r.URL.Query().Get("soul"))
	items, err := ep.ListPending(r.Context(), soulID)
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d *Daemon) handleCompanionMemoryPendingApprove(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		companionWriteError(w, http.StatusBadRequest, "invalid pending id")
		return
	}
	var body struct {
		Content *string `json:"content"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, companionSoulDocMaxBytes+16<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			companionWriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	mem := d.companionMemoryStore()
	cfg := d.companion.current().config
	approved, err := mem.ApprovePending(r.Context(), cfg.SoulDir, id, body.Content)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			companionWriteError(w, http.StatusNotFound, "pending entry not found")
			return
		}
		companionWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": approved})
}

func (d *Daemon) handleCompanionMemoryPendingDiscard(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		companionWriteError(w, http.StatusBadRequest, "invalid pending id")
		return
	}
	mem := d.companionMemoryStore()
	ep, err := mem.Episodes()
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deleted, err := ep.DeletePending(r.Context(), id)
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		companionWriteError(w, http.StatusNotFound, "pending entry not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"discarded": id})
}

func (d *Daemon) handleCompanionMemoryEpisodes(w http.ResponseWriter, r *http.Request) {
	if !companionLocalGet(w, r) {
		return
	}
	mem := d.companionMemoryStore()
	ep, err := mem.Episodes()
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := r.URL.Query()
	limit := companionEpisodesDefaultLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	episodes, err := ep.SearchEpisodes(r.Context(), CompanionEpisodeQuery{
		ProjectKey: q.Get("project"),
		Query:      q.Get("q"),
		Limit:      limit,
	})
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projects, err := ep.ListProjects(r.Context())
	if err != nil {
		projects = []string{}
	}
	total, _ := ep.CountEpisodes(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"episodes": episodes,
		"projects": projects,
		"total":    total,
	})
}

func (d *Daemon) handleCompanionMemoryEpisodesPrune(w http.ResponseWriter, r *http.Request) {
	if !companionTrustedPost(w, r) {
		return
	}
	var opts CompanionEpisodePruneOptions
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&opts); err != nil {
		companionWriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mem := d.companionMemoryStore()
	ep, err := mem.Episodes()
	if err != nil {
		companionWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deleted, err := ep.PruneEpisodes(r.Context(), opts)
	if err != nil {
		companionWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}
