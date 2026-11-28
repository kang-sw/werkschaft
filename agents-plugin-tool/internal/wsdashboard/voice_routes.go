package wsdashboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

const (
	voicePrefix = "/api/voice/"

	// Global settings keys declared in dashboard-config-manifest.json.
	VoiceBackendKey             = "dashboard.voice_backend"
	VoiceBackendOrderKey        = "dashboard.voice_backend_order"
	VoiceOpenRouterSharedKeyKey = "dashboard.voice_openrouter_shared_key"
	VoiceModelKey               = "dashboard.voice_model"
	VoiceLanguageKey            = "dashboard.voice_language"

	// VoiceBackendNative is the backend ID for OS-native desktop STT.
	VoiceBackendNative = "native"

	// Provider upload limits: 25 MiB audio payload cap plus modest multipart
	// framing overhead; everything stays in memory (zero audio disk writes).
	maxVoiceAudioBytes   = 25 << 20
	maxVoiceRequestBytes = maxVoiceAudioBytes + (256 << 10)
	maxVoiceFieldBytes   = 4 << 10
	maxVoiceResponse     = 4 << 20
	maxVoiceSSELine      = 1 << 20

	voiceUpstreamTimeout = 65 * time.Second
)

// VoiceModelCapability describes one transcription model a backend exposes.
type VoiceModelCapability struct {
	ID            string `json:"id"`
	Streaming     bool   `json:"streaming"`
	LanguageField string `json:"language_field"`
}

// VoiceBackendCapability describes one voice backend in GET /api/voice/capabilities.
type VoiceBackendCapability struct {
	ID        string                 `json:"id"`
	Label     string                 `json:"label"`
	Kind      string                 `json:"kind"`
	Available bool                   `json:"available"`
	Reason    string                 `json:"reason,omitempty"`
	Models    []VoiceModelCapability `json:"models"`
}

// VoiceSelection describes the active backend, model, and language when a
// selected backend is usable.
type VoiceSelection struct {
	Backend  string `json:"backend"`
	Model    string `json:"model"`
	Language string `json:"language,omitempty"`
}

type voiceCapabilitiesResponse struct {
	Backends            []VoiceBackendCapability `json:"backends"`
	Order               []string                 `json:"order"`
	Selected            *VoiceSelection          `json:"selected"`
	OpenRouterSharedKey bool                     `json:"openrouter_shared_key"`
	Language            string                   `json:"language,omitempty"`
	LegacyBackend       string                   `json:"legacy_backend,omitempty"`
	LegacyModel         string                   `json:"legacy_model,omitempty"`
	Error               string                   `json:"error,omitempty"`
}

type voiceSettings struct {
	Backend             string
	BackendOrder        string
	Model               string
	Language            string
	OpenRouterSharedKey bool
	OpenRouterSharedSet bool
}

func (s voiceSettings) effectiveOrder() []string {
	if fields := strings.Fields(s.BackendOrder); len(fields) > 0 {
		seen := make(map[string]bool, len(fields))
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
		return out
	}
	if b := strings.TrimSpace(s.Backend); b != "" {
		return []string{b}
	}
	return []string{VoiceBackendNative}
}

// effectiveOpenRouterSharedKey reports whether OpenRouter voice transcription
// may reuse the top-level OpenRouter LLM key. An explicit global setting
// (including false) always wins; when unset, legacy installs with no order key
// and voice_backend == "openrouter" keep working (T0 migration).
func (s voiceSettings) effectiveOpenRouterSharedKey() bool {
	if s.OpenRouterSharedSet {
		return s.OpenRouterSharedKey
	}
	if s.OpenRouterSharedKey {
		return true
	}
	return strings.TrimSpace(s.BackendOrder) == "" && strings.TrimSpace(s.Backend) == string(wscred.ProviderOpenRouter)
}

type voiceProviderSpec struct {
	Provider     wscred.ProviderID
	Label        string
	DefaultModel string
	Models       []VoiceModelCapability
}

var voiceExternalSpecs = []voiceProviderSpec{
	{
		Provider:     wscred.ProviderOpenAIAPI,
		Label:        "OpenAI Platform",
		DefaultModel: "gpt-transcribe",
		Models: []VoiceModelCapability{
			{ID: "gpt-transcribe", Streaming: true, LanguageField: "languages"},
			{ID: "gpt-4o-transcribe", Streaming: true, LanguageField: "language"},
			{ID: "gpt-4o-mini-transcribe", Streaming: true, LanguageField: "language"},
			{ID: "gpt-4o-transcribe-diarize", Streaming: true, LanguageField: "language"},
			{ID: "whisper-1", Streaming: false, LanguageField: "language"},
		},
	},
	{
		Provider:     wscred.ProviderGroq,
		Label:        "Groq",
		DefaultModel: "whisper-large-v3-turbo",
		Models: []VoiceModelCapability{
			{ID: "whisper-large-v3-turbo", Streaming: false, LanguageField: "language"},
			{ID: "whisper-large-v3", Streaming: false, LanguageField: "language"},
		},
	},
	{
		Provider:     wscred.ProviderOpenRouter,
		Label:        "OpenRouter",
		DefaultModel: "openai/whisper-1",
		Models: []VoiceModelCapability{
			{ID: "openai/whisper-1", Streaming: false, LanguageField: "language"},
		},
	},
}

func voiceLocalModels() []VoiceModelCapability {
	return []VoiceModelCapability{
		{ID: "whisper-1", Streaming: false, LanguageField: "language"},
	}
}

func voiceError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

func voiceEmptyCapabilities(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, voiceCapabilitiesResponse{
		Backends: []VoiceBackendCapability{},
		Order:    []string{},
		Selected: nil,
		Error:    code,
	})
}

// handleVoice serves the loopback-only voice capability discovery and
// transcription routes using the same viewer/origin boundary as /api/account/*.
func (d *Daemon) handleVoice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	action := strings.TrimPrefix(r.URL.Path, voicePrefix)
	if !localViewer(r) {
		if action == "capabilities" {
			voiceEmptyCapabilities(w, http.StatusNotFound, "not_found")
			return
		}
		voiceError(w, http.StatusNotFound, "not_found")
		return
	}
	var method string
	switch action {
	case "capabilities":
		method = http.MethodGet
	case "transcribe":
		method = http.MethodPost
	default:
		voiceError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		if action == "capabilities" {
			voiceEmptyCapabilities(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		voiceError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	origins := r.Header.Values("Origin")
	if crossSiteFetch(r) || len(origins) > 1 ||
		((method == http.MethodPost || len(origins) == 1) && !sameOrigin(r)) {
		if action == "capabilities" {
			voiceEmptyCapabilities(w, http.StatusForbidden, "origin_required")
			return
		}
		voiceError(w, http.StatusForbidden, "origin_required")
		return
	}
	if action == "capabilities" {
		d.handleVoiceCapabilities(w, r)
		return
	}
	d.handleVoiceTranscribe(w, r)
}

func (d *Daemon) readVoiceSettings() voiceSettings {
	if d.voiceConfig != nil {
		return d.voiceConfig()
	}
	var opts wsconfig.Options
	if d.config != nil {
		opts = d.config.Options
	}
	layers, err := wsconfig.FileLayers(opts)
	if err != nil {
		return voiceSettings{}
	}
	global := layers[wsconfig.ScopeGlobal].Overrides
	rawShared, sharedSet := global[VoiceOpenRouterSharedKeyKey]
	return voiceSettings{
		Backend:             strings.TrimSpace(global[VoiceBackendKey]),
		BackendOrder:        strings.TrimSpace(global[VoiceBackendOrderKey]),
		Model:               strings.TrimSpace(global[VoiceModelKey]),
		Language:            strings.TrimSpace(global[VoiceLanguageKey]),
		OpenRouterSharedKey: strings.EqualFold(strings.TrimSpace(rawShared), "true"),
		OpenRouterSharedSet: sharedSet,
	}
}

func (d *Daemon) voiceLocalProviders() []wscred.Provider {
	if catalog, ok := d.credentials.(interface{ LocalProviders() []wscred.Provider }); ok {
		return catalog.LocalProviders()
	}
	return nil
}

func (d *Daemon) handleVoiceCapabilities(w http.ResponseWriter, _ *http.Request) {
	configured := map[wscred.ProviderID]bool{}
	if store, ok := d.credentials.(interface {
		Status() ([]wscred.ProviderStatus, error)
	}); ok {
		if statuses, err := store.Status(); err == nil {
			for _, st := range statuses {
				configured[st.Provider] = st.Configured
			}
		}
	}
	settings := d.readVoiceSettings()
	sharedEffective := settings.effectiveOpenRouterSharedKey()
	order := settings.effectiveOrder()

	backends := make([]VoiceBackendCapability, 0, len(voiceExternalSpecs)+1)
	// Daemon cannot observe the desktop shell bridge, so native availability is
	// decided client-side from the bridge handshake.
	backends = append(backends, VoiceBackendCapability{
		ID:        VoiceBackendNative,
		Label:     "OS Native STT (Desktop)",
		Kind:      "native",
		Available: false,
		Reason:    "desktop_only",
		Models:    []VoiceModelCapability{},
	})
	for _, spec := range voiceExternalSpecs {
		var avail bool
		if spec.Provider == wscred.ProviderOpenRouter {
			avail = configured[wscred.ProviderOpenRouterVoice] || (sharedEffective && configured[wscred.ProviderOpenRouter])
		} else {
			avail = configured[spec.Provider]
		}
		reason := ""
		if !avail {
			reason = "api_key_missing"
		}
		models := make([]VoiceModelCapability, len(spec.Models))
		copy(models, spec.Models)
		backends = append(backends, VoiceBackendCapability{
			ID:        string(spec.Provider),
			Label:     spec.Label,
			Kind:      "external",
			Available: avail,
			Reason:    reason,
			Models:    models,
		})
	}
	for _, local := range d.voiceLocalProviders() {
		backends = append(backends, VoiceBackendCapability{
			ID:        string(local.ID),
			Label:     local.Name,
			Kind:      "external",
			Available: true,
			Models:    voiceLocalModels(),
		})
	}

	byID := make(map[string]VoiceBackendCapability, len(backends))
	for _, b := range backends {
		byID[b.ID] = b
	}
	var selected *VoiceSelection
	for _, id := range order {
		b, ok := byID[id]
		if !ok || !b.Available {
			continue
		}
		model := ""
		if b.ID == settings.Backend {
			model = settings.Model
		}
		if model == "" && len(b.Models) > 0 {
			model = b.Models[0].ID
		}
		selected = &VoiceSelection{
			Backend:  b.ID,
			Model:    model,
			Language: settings.Language,
		}
		break
	}
	writeJSON(w, http.StatusOK, voiceCapabilitiesResponse{
		Backends:            backends,
		Order:               order,
		Selected:            selected,
		OpenRouterSharedKey: sharedEffective,
		Language:            settings.Language,
		LegacyBackend:       settings.Backend,
		LegacyModel:         settings.Model,
	})
}

type voiceUpload struct {
	audio       []byte
	filename    string
	contentType string
	backend     string
	model       string
	language    string
}

// readVoiceUpload reads the multipart upload strictly into memory using
// MultipartReader (never ParseMultipartForm, which may spill audio to temp
// files on disk).
func readVoiceUpload(w http.ResponseWriter, r *http.Request) (voiceUpload, int, string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceRequestBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return voiceUpload{}, http.StatusBadRequest, "invalid_multipart"
	}
	var up voiceUpload
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return voiceUpload{}, http.StatusRequestEntityTooLarge, "audio_too_large"
			}
			return voiceUpload{}, http.StatusBadRequest, "invalid_multipart"
		}
		name := part.FormName()
		switch name {
		case "audio":
			data, err := readBounded(part, maxVoiceAudioBytes)
			_ = part.Close()
			if errors.Is(err, errPayloadTooLarge) {
				return voiceUpload{}, http.StatusRequestEntityTooLarge, "audio_too_large"
			}
			if err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					return voiceUpload{}, http.StatusRequestEntityTooLarge, "audio_too_large"
				}
				return voiceUpload{}, http.StatusBadRequest, "invalid_multipart"
			}
			up.audio = data
			up.filename = strings.TrimSpace(part.FileName())
			up.contentType = strings.TrimSpace(part.Header.Get("Content-Type"))
		case "backend", "model", "language":
			data, err := readBounded(part, maxVoiceFieldBytes)
			_ = part.Close()
			if err != nil {
				return voiceUpload{}, http.StatusBadRequest, "invalid_request"
			}
			val := strings.TrimSpace(string(data))
			switch name {
			case "backend":
				up.backend = val
			case "model":
				up.model = val
			case "language":
				up.language = val
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, maxVoiceFieldBytes+1))
			_ = part.Close()
		}
	}
	if len(up.audio) == 0 {
		return voiceUpload{}, http.StatusBadRequest, "audio_required"
	}
	up.filename, up.contentType = normalizeAudioFilename(up.filename, up.contentType)
	return up, 0, ""
}

var errPayloadTooLarge = errors.New("payload exceeds maximum size")

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errPayloadTooLarge
	}
	return data, nil
}

func normalizeAudioFilename(filename, contentType string) (string, string) {
	mediaType := ""
	if contentType != "" {
		if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
			mediaType = strings.ToLower(parsed)
		}
	}
	extForMime := map[string]string{
		"audio/webm":  ".webm",
		"audio/mp4":   ".m4a",
		"audio/x-m4a": ".m4a",
		"audio/m4a":   ".m4a",
		"audio/mpeg":  ".mp3",
		"audio/mp3":   ".mp3",
		"audio/wav":   ".wav",
		"audio/x-wav": ".wav",
		"audio/wave":  ".wav",
		"audio/ogg":   ".ogg",
		"audio/flac":  ".flac",
	}
	mimeForExt := map[string]string{
		".webm": "audio/webm",
		".m4a":  "audio/mp4",
		".mp4":  "audio/mp4",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".ogg":  "audio/ogg",
		".flac": "audio/flac",
	}
	base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if base == "" || base == "." || base == "/" {
		base = "recording"
	}
	ext := strings.ToLower(path.Ext(base))
	if mediaType == "" || mediaType == "application/octet-stream" {
		if inferred, ok := mimeForExt[ext]; ok {
			mediaType = inferred
		} else {
			mediaType = "audio/webm"
		}
	}
	wantExt, ok := extForMime[mediaType]
	if !ok {
		wantExt = ".webm"
	}
	if ext != wantExt {
		stem := strings.TrimSuffix(base, path.Ext(base))
		if stem == "" {
			stem = "recording"
		}
		base = stem + wantExt
	}
	return base, mediaType
}

type resolvedVoiceTarget struct {
	providerID    wscred.ProviderID
	baseURL       string
	apiKey        wscred.Secret
	model         string
	streaming     bool
	languageField string
}

func (d *Daemon) resolveOpenRouterVoiceCredential(ctx context.Context, sharedEffective bool) (wscred.Credential, error) {
	if sharedEffective {
		cred, err := d.credentials.Credential(ctx, wscred.Request{Provider: wscred.ProviderOpenRouter})
		if err == nil {
			return cred, nil
		}
		if !errors.Is(err, wscred.ErrNotConfigured) {
			return wscred.Credential{}, err
		}
	}
	return d.credentials.Credential(ctx, wscred.Request{Provider: wscred.ProviderOpenRouterVoice})
}

func (d *Daemon) resolveVoiceTarget(ctx context.Context, backend, model string) (resolvedVoiceTarget, int, string) {
	settings := d.readVoiceSettings()
	if backend == "" {
		backend = settings.Backend
	}
	if backend == "" && strings.TrimSpace(settings.BackendOrder) != "" {
		for _, candidate := range settings.effectiveOrder() {
			if candidate != VoiceBackendNative {
				backend = candidate
				break
			}
		}
	}
	if backend == "" {
		return resolvedVoiceTarget{}, http.StatusBadRequest, "backend_required"
	}
	if model == "" && backend == settings.Backend {
		model = settings.Model
	}
	pid := wscred.ProviderID(backend)
	for _, spec := range voiceExternalSpecs {
		if spec.Provider != pid {
			continue
		}
		if d.credentials == nil {
			return resolvedVoiceTarget{}, http.StatusServiceUnavailable, "credential_unavailable"
		}
		var (
			cred wscred.Credential
			err  error
		)
		if pid == wscred.ProviderOpenRouter {
			cred, err = d.resolveOpenRouterVoiceCredential(ctx, settings.effectiveOpenRouterSharedKey())
		} else {
			cred, err = d.credentials.Credential(ctx, wscred.Request{Provider: pid})
		}
		if err != nil {
			if errors.Is(err, wscred.ErrNotConfigured) {
				return resolvedVoiceTarget{}, http.StatusUnprocessableEntity, "backend_unconfigured"
			}
			return resolvedVoiceTarget{}, http.StatusServiceUnavailable, "credential_unavailable"
		}
		if model == "" {
			model = spec.DefaultModel
		}
		streaming := false
		langField := "language"
		for _, m := range spec.Models {
			if m.ID == model {
				streaming = m.Streaming
				langField = m.LanguageField
				break
			}
		}
		p, _ := wscred.Lookup(pid)
		baseURL := p.BaseURL
		if override, ok := d.voiceBaseURLs[pid]; ok && override != "" {
			baseURL = override
		}
		return resolvedVoiceTarget{
			providerID:    pid,
			baseURL:       baseURL,
			apiKey:        cred.Secret,
			model:         model,
			streaming:     streaming,
			languageField: langField,
		}, 0, ""
	}
	if wsconfig.IsLocalWerkProvider(backend) {
		for _, local := range d.voiceLocalProviders() {
			if local.ID != pid {
				continue
			}
			var secret wscred.Secret
			if d.credentials != nil {
				if cred, err := d.credentials.Credential(ctx, wscred.Request{Provider: pid, BaseURL: local.BaseURL}); err == nil {
					secret = cred.Secret
				} else if !errors.Is(err, wscred.ErrNotConfigured) {
					return resolvedVoiceTarget{}, http.StatusServiceUnavailable, "credential_unavailable"
				}
			}
			if model == "" {
				model = "whisper-1"
			}
			baseURL := local.BaseURL
			if override, ok := d.voiceBaseURLs[pid]; ok && override != "" {
				baseURL = override
			}
			return resolvedVoiceTarget{
				providerID:    pid,
				baseURL:       baseURL,
				apiKey:        secret,
				model:         model,
				streaming:     false,
				languageField: "language",
			}, 0, ""
		}
		return resolvedVoiceTarget{}, http.StatusBadRequest, "unknown_backend"
	}
	return resolvedVoiceTarget{}, http.StatusBadRequest, "unknown_backend"
}

func buildVoiceMultipart(up voiceUpload, target resolvedVoiceTarget) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, up.filename))
	hdr.Set("Content-Type", up.contentType)
	fw, err := mw.CreatePart(hdr)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(up.audio); err != nil {
		return nil, "", err
	}
	if err := mw.WriteField("model", target.model); err != nil {
		return nil, "", err
	}
	if target.streaming {
		if err := mw.WriteField("stream", "true"); err != nil {
			return nil, "", err
		}
	} else {
		// OpenRouter rejects text/srt/vtt; Groq and OpenAI whisper-1 accept json.
		if err := mw.WriteField("response_format", "json"); err != nil {
			return nil, "", err
		}
	}
	if langs := parseVoiceLanguages(up.language); len(langs) > 0 {
		if target.languageField == "languages" {
			for _, lang := range langs {
				if err := mw.WriteField("languages", lang); err != nil {
					return nil, "", err
				}
			}
		} else {
			if err := mw.WriteField("language", langs[0]); err != nil {
				return nil, "", err
			}
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

func parseVoiceLanguages(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (d *Daemon) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	up, status, code := readVoiceUpload(w, r)
	if code != "" {
		voiceError(w, status, code)
		return
	}
	settings := d.readVoiceSettings()
	if up.language == "" {
		up.language = settings.Language
	}
	target, status, code := d.resolveVoiceTarget(r.Context(), up.backend, up.model)
	if code != "" {
		voiceError(w, status, code)
		return
	}
	body, contentType, err := buildVoiceMultipart(up, target)
	if err != nil {
		voiceError(w, http.StatusInternalServerError, "encode_failed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), voiceUpstreamTimeout)
	defer cancel()
	endpoint := strings.TrimRight(target.baseURL, "/") + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		voiceError(w, http.StatusInternalServerError, "request_failed")
		return
	}
	req.Header.Set("Content-Type", contentType)
	if target.streaming {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if token := target.apiKey.Reveal(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := d.voiceHTTPClient
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			voiceError(w, http.StatusGatewayTimeout, "upstream_timeout")
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
			return
		}
		voiceError(w, http.StatusBadGateway, "upstream_unreachable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		status, code := mapVoiceUpstreamStatus(resp.StatusCode)
		voiceError(w, status, code)
		return
	}

	respMedia, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if target.streaming && strings.EqualFold(respMedia, "text/event-stream") {
		relayVoiceSSE(w, resp.Body)
		return
	}
	raw, err := readBounded(resp.Body, maxVoiceResponse)
	if err != nil {
		voiceError(w, http.StatusBadGateway, "invalid_upstream_response")
		return
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		voiceError(w, http.StatusBadGateway, "invalid_upstream_response")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Text string `json:"text"`
	}{Text: decoded.Text})
}

func mapVoiceUpstreamStatus(status int) (int, string) {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return http.StatusUnprocessableEntity, "provider_auth_failed"
	case http.StatusRequestEntityTooLarge:
		return http.StatusRequestEntityTooLarge, "audio_too_large"
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests, "rate_limited"
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return http.StatusGatewayTimeout, "upstream_timeout"
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return http.StatusUnprocessableEntity, "provider_rejected"
	default:
		return http.StatusBadGateway, "provider_error"
	}
}

func relayVoiceSSE(w http.ResponseWriter, body io.Reader) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	writeEvent := func(event string, payload any) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		if flusher != nil {
			flusher.Flush()
		}
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxVoiceSSELine)

	var eventName string
	var dataLines []string
	sawDone := false
	sawError := false

	dispatch := func() {
		if len(dataLines) == 0 {
			eventName = ""
			return
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		ev := eventName
		eventName = ""
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			return
		}
		var msg struct {
			Type  string          `json:"type"`
			Delta string          `json:"delta"`
			Text  string          `json:"text"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			return
		}
		kind := msg.Type
		if kind == "" {
			kind = ev
		}
		switch kind {
		case "transcript.text.delta":
			writeEvent("transcript.text.delta", struct {
				Delta string `json:"delta"`
			}{Delta: msg.Delta})
		case "transcript.text.done":
			sawDone = true
			writeEvent("transcript.text.done", struct {
				Text string `json:"text"`
			}{Text: msg.Text})
		case "error":
			sawError = true
			writeEvent("error", struct {
				Code string `json:"code"`
			}{Code: "provider_error"})
		default:
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				sawError = true
				writeEvent("error", struct {
					Code string `json:"code"`
				}{Code: "provider_error"})
			}
		}
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			dispatch()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	dispatch()
	if !sawDone && !sawError {
		code := "stream_incomplete"
		if scanner.Err() != nil {
			code = "upstream_read_failed"
		}
		writeEvent("error", struct {
			Code string `json:"code"`
		}{Code: code})
	}
}
