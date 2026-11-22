package wsagent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The model list for the dashboard picker: OpenRouter's public list, minus the
// models that declare their parameters without tools.
func TestWerkChoicesListOpenRouterModels(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"data":[
			{"id":"vendor/b-thinker","supported_parameters":["tools","reasoning","temperature"]},
			{"id":"vendor/a-plain","supported_parameters":["tools","temperature"]},
			{"id":"vendor/c-no-tools","supported_parameters":["temperature"]},
			{"id":"vendor/d-undeclared"},
			{"id":"vendor/a-plain","supported_parameters":["tools"]},
			{"id":" "}]}`)
	}))
	defer srv.Close()
	b := WerkBackend{modelsURL: srv.URL + "/api/v1/models", httpClient: srv.Client()}
	models, efforts, err := b.Choices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Errorf("the public model list was requested with credentials: %q", auth)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "vendor/a-plain,vendor/b-thinker,vendor/d-undeclared" {
		t.Fatalf("models = %v", ids)
	}
	if models[0].Thinking == nil || *models[0].Thinking || models[1].Thinking == nil || !*models[1].Thinking || models[2].Thinking != nil {
		t.Errorf("thinking flags = %v %v %v", models[0].Thinking, models[1].Thinking, models[2].Thinking)
	}
	if !slices.Equal(efforts, []string{"minimal", "low", "medium", "high", "xhigh"}) {
		t.Errorf("efforts = %v", efforts)
	}
}

func TestWerkChoicesReportAFailedListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusBadGateway) }))
	defer srv.Close()
	_, efforts, err := WerkBackend{modelsURL: srv.URL, httpClient: srv.Client()}.Choices(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") || len(efforts) == 0 {
		t.Errorf("err = %v, efforts = %v", err, efforts)
	}
	if _, err := parseWerkModels([]byte("{")); err == nil {
		t.Error("a broken list was accepted")
	}
}

// The picker lists werk's models beside the support harnesses without making
// it a support or lead choice, and says why it is unavailable until a key is
// set, while still listing the public models.
func TestWerkIsListedForThePickerOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"vendor/m","supported_parameters":["tools"]}]}`)
	}))
	defer srv.Close()
	configured := false
	sh := newSupportHarness(t)
	_, reg := WerkBackend{Command: fakeHarness("ws-mcp"), modelsURL: srv.URL, httpClient: srv.Client(), credentialStatus: func(string) (bool, error) { return configured, nil }}.registration()
	sh.runner.opts.Backends[BackendWerk] = reg

	answer := choicesWithPickers(t, sh.support)
	for _, h := range answer.Harnesses {
		if h.Harness == BackendWerk {
			t.Fatalf("werk is a support/lead choice: %+v", h)
		}
	}
	if len(answer.Pickers) != 1 || answer.Pickers[0].Harness != BackendWerk {
		t.Fatalf("pickers = %+v", answer.Pickers)
	}
	p := answer.Pickers[0]
	if p.Available || !strings.Contains(p.Error, "credential set openrouter") || len(p.Models) != 1 || p.Models[0].ID != "vendor/m" || len(p.Efforts) == 0 {
		t.Errorf("without a key: %+v", p)
	}
	if p.Lead || p.Profile != "" {
		t.Errorf("a picker entry claims a lead or a profile: %+v", p)
	}

	configured = true
	answer = choicesWithPickers(t, sh.support)
	if p := answer.Pickers[0]; !p.Available || p.Error != "" || len(p.Models) != 1 {
		t.Errorf("with a key: %+v", p)
	}
}

// choicesWithPickers asks again until the background listing of the picker
// harnesses has landed.
func choicesWithPickers(t *testing.T, s *Support) ChoicesAnswer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		answer := s.Choices(context.Background())
		if !answer.PickersPending {
			return answer
		}
		if time.Now().After(deadline) {
			t.Fatalf("the picker listing never landed: %+v", answer)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A slow model endpoint holds no choices request: the answer comes at once
// without the picker (reported pending), the other harnesses are listed, and
// once the listing lands the picker is served from the cache.
func TestSlowModelListDoesNotHoldChoices(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"data":[{"id":"vendor/m","supported_parameters":["tools"]}]}`)
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	sh := newSupportHarness(t)
	_, reg := WerkBackend{Command: fakeHarness("ws-mcp"), modelsURL: srv.URL, httpClient: srv.Client(), credentialStatus: func(string) (bool, error) { return true, nil }}.registration()
	sh.runner.opts.Backends[BackendWerk] = reg

	start := time.Now()
	answer := sh.support.Choices(context.Background())
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("choices waited %v on the model list", took)
	}
	if !answer.PickersPending || len(answer.Pickers) != 0 {
		t.Errorf("pickers = %+v pending = %v, want none and pending", answer.Pickers, answer.PickersPending)
	}
	if len(answer.Harnesses) == 0 {
		t.Error("the other harnesses were not listed")
	}
	// A second request while the listing runs starts no second fetch.
	for deadline := time.Now().Add(5 * time.Second); hits.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	sh.support.Choices(context.Background())
	time.Sleep(50 * time.Millisecond)
	if n := hits.Load(); n != 1 {
		t.Errorf("model list requests = %d, want 1", n)
	}

	close(release)
	answer = choicesWithPickers(t, sh.support)
	if len(answer.Pickers) != 1 || len(answer.Pickers[0].Models) != 1 || answer.Pickers[0].Models[0].ID != "vendor/m" {
		t.Fatalf("pickers after the listing = %+v", answer.Pickers)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("model list requests = %d after landing, want 1 (served from the cache)", n)
	}
}
