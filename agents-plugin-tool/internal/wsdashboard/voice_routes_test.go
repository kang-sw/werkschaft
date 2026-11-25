package wsdashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

const (
	voiceFixtureOpenAIKey = "sk-proj-fixture-voice-1111"
	voiceFixtureGroqKey   = "gsk_fixture_voice_key_2222"
	voiceFixtureORKey     = "sk-or-v1-fixture-voice-3333"
)

func voiceMultipartRequest(t *testing.T, urlPath string, fields map[string]string, filename, contentType string, audio []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if audio != nil {
		hdr := make(textproto.MIMEHeader)
		hdr.Set("Content-Disposition", `form-data; name="audio"; filename="`+filename+`"`)
		if contentType != "" {
			hdr.Set("Content-Type", contentType)
		}
		fw, err := mw.CreatePart(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(audio); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost:43210"+urlPath, &buf)
	req.RemoteAddr = "127.0.0.1:41234"
	req.Host = "localhost:43210"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", "http://localhost:43210")
	return req
}

func TestVoiceBoundaryAndEmptyCapabilities(t *testing.T) {
	d := authFixture(t)
	d.lan = true
	lanToken, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	store := wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenAIAPI, APIKey: wscred.NewSecret(voiceFixtureOpenAIKey)}); err != nil {
		t.Fatal(err)
	}
	d.credentials = store
	d.voiceConfig = func() voiceSettings {
		return voiceSettings{Backend: "openai_api"}
	}

	localPeer := authRequest(http.MethodGet, "/", "").RemoteAddr
	lanPeer := net.JoinHostPort(net.IPv4(192, 0, 2, 8).String(), "8888")

	// Non-loopback peer or Host on GET /api/voice/capabilities must be rejected
	// by the shared guard and return an empty capability payload.
	for _, v := range []viewer{
		{peer: lanPeer, host: "dashboard.example", cookie: lanToken},
		{peer: lanPeer, host: "localhost:43210", cookie: lanToken},
		{peer: localPeer, host: "dashboard.example", cookie: lanToken},
	} {
		w := serve(d, v, http.MethodGet, voicePrefix+"capabilities", "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("LAN voice capabilities status = %d, want 404 (%s)", w.Code, w.Body.String())
		}
		var resp voiceCapabilitiesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("LAN capabilities body not JSON: %v (%s)", err, w.Body.String())
		}
		if len(resp.Backends) != 0 || resp.Selected != nil {
			t.Fatalf("LAN capabilities not empty: %+v", resp)
		}
	}

	// Bad Origin on capabilities must be rejected with empty capabilities.
	badOrigin := authRequest(http.MethodGet, voicePrefix+"capabilities", "")
	badOrigin.AddCookie(authCookie(t, d))
	badOrigin.Header.Set("Origin", "https://evil.example")
	w := authServe(d, badOrigin)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bad Origin capabilities = %d, want 403", w.Code)
	}
	var capResp voiceCapabilitiesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil || len(capResp.Backends) != 0 || capResp.Selected != nil {
		t.Fatalf("bad Origin capabilities not empty: %+v (%s)", capResp, w.Body.String())
	}

	// Missing cookie must be rejected with 401.
	noCookie := authRequest(http.MethodGet, voicePrefix+"capabilities", "")
	if w := authServe(d, noCookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing cookie capabilities = %d, want 401", w.Code)
	}

	// POST /api/voice/transcribe rejects bad Origin and LAN peer.
	badPost := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{"backend": "openai_api"}, "a.webm", "audio/webm", []byte("RIFF"))
	badPost.AddCookie(authCookie(t, d))
	badPost.Header.Set("Origin", "https://evil.example")
	if w := authServe(d, badPost); w.Code != http.StatusForbidden {
		t.Fatalf("bad Origin transcribe = %d, want 403", w.Code)
	}

	// Local viewer gets capabilities without ever exposing raw or masked secrets.
	okReq := authRequest(http.MethodGet, voicePrefix+"capabilities", "")
	okReq.AddCookie(authCookie(t, d))
	w = authServe(d, okReq)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("local capabilities = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), voiceFixtureOpenAIKey) || strings.Contains(w.Body.String(), "1111") {
		t.Fatalf("capabilities leaked key material: %s", w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil {
		t.Fatal(err)
	}
	if capResp.Selected == nil || capResp.Selected.Backend != "openai_api" || capResp.Selected.Model != "gpt-transcribe" {
		t.Fatalf("selected = %+v, want openai_api/gpt-transcribe", capResp.Selected)
	}
	var sawOpenAI bool
	for _, b := range capResp.Backends {
		if b.ID == "openai_api" {
			sawOpenAI = true
			if !b.Available || b.Kind != "external" || len(b.Models) < 5 {
				t.Fatalf("openai_api backend = %+v", b)
			}
			if b.Models[0].ID != "gpt-transcribe" || !b.Models[0].Streaming || b.Models[0].LanguageField != "languages" {
				t.Fatalf("gpt-transcribe model spec = %+v", b.Models[0])
			}
		}
	}
	if !sawOpenAI {
		t.Fatalf("openai_api missing from capabilities: %+v", capResp.Backends)
	}

	// When voice_backend is unset, selected is nil.
	d.voiceConfig = func() voiceSettings { return voiceSettings{} }
	okReq2 := authRequest(http.MethodGet, voicePrefix+"capabilities", "")
	okReq2.AddCookie(authCookie(t, d))
	w = authServe(d, okReq2)
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil || capResp.Selected != nil {
		t.Fatalf("unset backend selected = %+v, want nil", capResp.Selected)
	}
}

func TestAccountVoiceKeysGroupingAndMasking(t *testing.T) {
	d := authFixture(t)
	store := &accountTestStore{Store: wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))}
	d.credentials = store

	w := requestAccount(t, d, http.MethodPost, "key", `{"op":"set","provider":"openai_api","key":"`+voiceFixtureOpenAIKey+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"masked":"sk-...1111"`) || strings.Contains(w.Body.String(), voiceFixtureOpenAIKey) {
		t.Fatalf("set openai_api = %d %s", w.Code, w.Body.String())
	}
	w = requestAccount(t, d, http.MethodPost, "key", `{"op":"set","provider":"groq","key":"`+voiceFixtureGroqKey+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"masked":"gsk_...2222"`) || strings.Contains(w.Body.String(), voiceFixtureGroqKey) {
		t.Fatalf("set groq = %d %s", w.Code, w.Body.String())
	}

	w = requestAccount(t, d, http.MethodGet, "status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	var st accountStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.LocalKeys) != 0 {
		t.Fatalf("voice keys leaked into local_keys: %+v", st.LocalKeys)
	}
	if len(st.VoiceKeys) != 2 || st.VoiceKeys[0].Provider != wscred.ProviderOpenAIAPI || !st.VoiceKeys[0].Configured ||
		st.VoiceKeys[1].Provider != wscred.ProviderGroq || !st.VoiceKeys[1].Configured {
		t.Fatalf("voice_keys = %+v", st.VoiceKeys)
	}

	w = requestAccount(t, d, http.MethodPost, "key", `{"op":"remove","provider":"groq"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("remove groq = %d %s", w.Code, w.Body.String())
	}
}

func TestVoiceTranscribeOpenAIStreamingLanguagesAndZeroDiskWrites(t *testing.T) {
	tmpSpy := t.TempDir()
	t.Setenv("TMPDIR", tmpSpy)
	t.Setenv("TEMP", tmpSpy)
	t.Setenv("TMP", tmpSpy)

	var gotAuth, gotFileName, gotFileCT, gotModel, gotStream, gotRespFmt string
	var gotLanguage, gotLanguages []string
	var gotAudioLen int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotFileName, gotFileCT, gotModel, gotStream, gotRespFmt = "", "", "", "", ""
		gotLanguage, gotLanguages = nil, nil
		gotAudioLen = 0

		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "file":
				gotFileName = part.FileName()
				gotFileCT = part.Header.Get("Content-Type")
				gotAudioLen = len(data)
			case "model":
				gotModel = string(data)
			case "stream":
				gotStream = string(data)
			case "response_format":
				gotRespFmt = string(data)
			case "language":
				gotLanguage = append(gotLanguage, string(data))
			case "languages":
				gotLanguages = append(gotLanguages, string(data))
			}
		}

		if gotStream == "true" {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"Hello \"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"world\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"Hello world\"}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"Final only whisper"}`)
	}))
	defer srv.Close()

	d := authFixture(t)
	store := wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenAIAPI, APIKey: wscred.NewSecret(voiceFixtureOpenAIKey)}); err != nil {
		t.Fatal(err)
	}
	d.credentials = store
	d.voiceBaseURLs = map[wscred.ProviderID]string{wscred.ProviderOpenAIAPI: srv.URL}

	// 1) gpt-transcribe: streaming SSE + languages array (never language) + zero disk writes.
	payload := bytes.Repeat([]byte("A"), 2<<20) // 2 MiB audio
	req := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{
		"backend":  "openai_api",
		"model":    "gpt-transcribe",
		"language": "en, ko",
	}, "clip.bin", "audio/webm;codecs=opus", payload)
	req.AddCookie(authCookie(t, d))
	w := authServe(d, req)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("gpt-transcribe status=%d ct=%q body=%s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if gotAuth != "Bearer "+voiceFixtureOpenAIKey {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotFileName != "clip.webm" || gotFileCT != "audio/webm" || gotAudioLen != len(payload) {
		t.Fatalf("file part = name:%q ct:%q len:%d", gotFileName, gotFileCT, gotAudioLen)
	}
	if gotModel != "gpt-transcribe" || gotStream != "true" || len(gotLanguage) != 0 ||
		len(gotLanguages) != 2 || gotLanguages[0] != "en" || gotLanguages[1] != "ko" {
		t.Fatalf("gpt-transcribe fields: model=%q stream=%q language=%v languages=%v", gotModel, gotStream, gotLanguage, gotLanguages)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: transcript.text.delta\ndata: {\"delta\":\"Hello \"}") ||
		!strings.Contains(body, "event: transcript.text.done\ndata: {\"text\":\"Hello world\"}") ||
		strings.Contains(body, "[DONE]") {
		t.Fatalf("SSE relay body unexpected:\n%s", body)
	}
	if entries, err := os.ReadDir(tmpSpy); err != nil || len(entries) != 0 {
		t.Fatalf("transcribe wrote temp files to disk: entries=%v err=%v", entries, err)
	}

	// 2) gpt-4o-transcribe: streaming SSE + single language (never languages).
	req2 := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{
		"backend":  "openai_api",
		"model":    "gpt-4o-transcribe",
		"language": "ko",
	}, "clip.m4a", "audio/mp4", []byte("MP4AUDIO"))
	req2.AddCookie(authCookie(t, d))
	w2 := authServe(d, req2)
	if w2.Code != http.StatusOK || len(gotLanguages) != 0 || len(gotLanguage) != 1 || gotLanguage[0] != "ko" || gotFileName != "clip.m4a" {
		t.Fatalf("gpt-4o-transcribe: code=%d language=%v languages=%v file=%q", w2.Code, gotLanguage, gotLanguages, gotFileName)
	}

	// 3) whisper-1: final-only JSON (no stream).
	req3 := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{
		"backend": "openai_api",
		"model":   "whisper-1",
	}, "clip.wav", "audio/wav", []byte("RIFF"))
	req3.AddCookie(authCookie(t, d))
	w3 := authServe(d, req3)
	if w3.Code != http.StatusOK || gotStream != "" || gotRespFmt != "json" || !strings.Contains(w3.Body.String(), `"text":"Final only whisper"`) {
		t.Fatalf("whisper-1: code=%d stream=%q fmt=%q body=%s", w3.Code, gotStream, gotRespFmt, w3.Body.String())
	}
}

func TestVoiceTranscribeGroqOpenRouterAndLocalEndpoint(t *testing.T) {
	var lastAuth, lastModel, lastStream, lastRespFmt, lastLang string
	var returnStatus int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth = r.Header.Get("Authorization")
		lastModel, lastStream, lastRespFmt, lastLang = "", "", "", ""
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "model":
				lastModel = string(data)
			case "stream":
				lastStream = string(data)
			case "response_format":
				lastRespFmt = string(data)
			case "language":
				lastLang = string(data)
			}
		}
		if returnStatus != 0 {
			w.WriteHeader(returnStatus)
			_, _ = io.WriteString(w, `{"error":"secret-upstream-detail"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"transcribed ok"}`)
	}))
	defer srv.Close()

	d := authFixture(t)
	configHome := t.TempDir()
	store := wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderGroq, APIKey: wscred.NewSecret(voiceFixtureGroqKey)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(voiceFixtureORKey)}); err != nil {
		t.Fatal(err)
	}
	opts := wsconfig.Options{ConfigHome: configHome}
	if _, err := store.ApplyLocalEndpoint(opts, wscred.LocalEndpointChange{
		Op:      wscred.OpSet,
		Name:    "whisper-local",
		BaseURL: srv.URL,
		Kind:    "vllm",
	}); err != nil {
		t.Fatal(err)
	}
	d.credentials = store
	d.voiceBaseURLs = map[wscred.ProviderID]string{
		wscred.ProviderGroq:       srv.URL,
		wscred.ProviderOpenRouter: srv.URL,
	}

	// Groq: default whisper-large-v3-turbo, final-only json, single language.
	req := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{
		"backend":  "groq",
		"language": "ja",
	}, "a.webm", "audio/webm", []byte("WEBM"))
	req.AddCookie(authCookie(t, d))
	w := authServe(d, req)
	if w.Code != http.StatusOK || lastAuth != "Bearer "+voiceFixtureGroqKey || lastModel != "whisper-large-v3-turbo" ||
		lastStream != "" || lastRespFmt != "json" || lastLang != "ja" || !strings.Contains(w.Body.String(), "transcribed ok") {
		t.Fatalf("groq: code=%d auth=%q model=%q stream=%q fmt=%q lang=%q body=%s", w.Code, lastAuth, lastModel, lastStream, lastRespFmt, lastLang, w.Body.String())
	}

	// OpenRouter: final-only json, omitted language when empty.
	req2 := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{
		"backend": "openrouter",
	}, "a.webm", "audio/webm", []byte("WEBM"))
	req2.AddCookie(authCookie(t, d))
	w2 := authServe(d, req2)
	if w2.Code != http.StatusOK || lastAuth != "Bearer "+voiceFixtureORKey || lastStream != "" || lastRespFmt != "json" || lastLang != "" {
		t.Fatalf("openrouter: code=%d auth=%q stream=%q fmt=%q lang=%q body=%s", w2.Code, lastAuth, lastStream, lastRespFmt, lastLang, w2.Body.String())
	}

	// OpenRouter 504 timeout maps cleanly without reflecting upstream text.
	returnStatus = http.StatusGatewayTimeout
	req3 := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{"backend": "openrouter"}, "a.webm", "audio/webm", []byte("WEBM"))
	req3.AddCookie(authCookie(t, d))
	w3 := authServe(d, req3)
	if w3.Code != http.StatusGatewayTimeout || !strings.Contains(w3.Body.String(), `"upstream_timeout"`) || strings.Contains(w3.Body.String(), "secret-upstream-detail") {
		t.Fatalf("openrouter timeout: code=%d body=%s", w3.Code, w3.Body.String())
	}
	returnStatus = 0

	// Custom local endpoint with no API key succeeds without Authorization header.
	req4 := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{"backend": "local/whisper-local"}, "a.wav", "audio/wav", []byte("RIFF"))
	req4.AddCookie(authCookie(t, d))
	w4 := authServe(d, req4)
	if w4.Code != http.StatusOK || lastAuth != "" || lastModel != "whisper-1" || lastRespFmt != "json" {
		t.Fatalf("local endpoint: code=%d auth=%q model=%q fmt=%q body=%s", w4.Code, lastAuth, lastModel, lastRespFmt, w4.Body.String())
	}

	// Client timeout maps to upstream_timeout.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer slowSrv.Close()
	d.voiceBaseURLs[wscred.ProviderOpenRouter] = slowSrv.URL
	d.voiceHTTPClient = &http.Client{Timeout: 40 * time.Millisecond}
	req5 := voiceMultipartRequest(t, voicePrefix+"transcribe", map[string]string{"backend": "openrouter"}, "a.webm", "audio/webm", []byte("WEBM"))
	req5.AddCookie(authCookie(t, d))
	w5 := authServe(d, req5)
	if w5.Code != http.StatusGatewayTimeout || !strings.Contains(w5.Body.String(), `"upstream_timeout"`) {
		t.Fatalf("slow upstream: code=%d body=%s", w5.Code, w5.Body.String())
	}
}

func TestVoiceTranscribeBackendOverrideModelDefault(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotModel = ""
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "model" {
				gotModel = string(data)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"ok"}`)
	}))
	defer srv.Close()

	d := authFixture(t)
	configHome := t.TempDir()
	store := wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenAIAPI, APIKey: wscred.NewSecret(voiceFixtureOpenAIKey)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderGroq, APIKey: wscred.NewSecret(voiceFixtureGroqKey)}); err != nil {
		t.Fatal(err)
	}
	opts := wsconfig.Options{ConfigHome: configHome}
	if _, err := store.ApplyLocalEndpoint(opts, wscred.LocalEndpointChange{
		Op:      wscred.OpSet,
		Name:    "whisper-local",
		BaseURL: srv.URL,
		Kind:    "vllm",
	}); err != nil {
		t.Fatal(err)
	}
	d.credentials = store
	d.voiceBaseURLs = map[wscred.ProviderID]string{
		wscred.ProviderOpenAIAPI: srv.URL,
		wscred.ProviderGroq:      srv.URL,
	}
	d.voiceConfig = func() voiceSettings {
		return voiceSettings{
			Backend: "openai_api",
			Model:   "gpt-4o-mini-transcribe",
		}
	}

	cases := []struct {
		name      string
		settings  *voiceSettings
		fields    map[string]string
		wantModel string
	}{
		{
			name:      "omitted backend inherits global model",
			fields:    map[string]string{},
			wantModel: "gpt-4o-mini-transcribe",
		},
		{
			name:      "same backend with empty model inherits global model",
			fields:    map[string]string{"backend": "openai_api"},
			wantModel: "gpt-4o-mini-transcribe",
		},
		{
			name:      "overridden cloud backend with empty model uses target default",
			fields:    map[string]string{"backend": "groq"},
			wantModel: "whisper-large-v3-turbo",
		},
		{
			name:      "overridden local backend with empty model uses whisper-1",
			fields:    map[string]string{"backend": "local/whisper-local"},
			wantModel: "whisper-1",
		},
		{
			name:      "explicit model wins on overridden backend",
			fields:    map[string]string{"backend": "groq", "model": "whisper-large-v3"},
			wantModel: "whisper-large-v3",
		},
		{
			name:      "explicit model wins on same backend",
			fields:    map[string]string{"backend": "openai_api", "model": "whisper-1"},
			wantModel: "whisper-1",
		},
		{
			name:      "empty global backend with stale global model does not leak to explicit backend",
			settings:  &voiceSettings{Backend: "", Model: "gpt-4o-mini-transcribe"},
			fields:    map[string]string{"backend": "groq"},
			wantModel: "whisper-large-v3-turbo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.settings != nil {
				s := *tc.settings
				d.voiceConfig = func() voiceSettings { return s }
			} else {
				d.voiceConfig = func() voiceSettings {
					return voiceSettings{Backend: "openai_api", Model: "gpt-4o-mini-transcribe"}
				}
			}
			req := voiceMultipartRequest(t, voicePrefix+"transcribe", tc.fields, "a.webm", "audio/webm", []byte("WEBM"))
			req.AddCookie(authCookie(t, d))
			w := authServe(d, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
			}
			if gotModel != tc.wantModel {
				t.Fatalf("upstream model = %q, want %q", gotModel, tc.wantModel)
			}
		})
	}
}
