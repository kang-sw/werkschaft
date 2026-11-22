package wsagent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
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

	answer := sh.support.Choices(context.Background())
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
	answer = sh.support.Choices(context.Background())
	if p := answer.Pickers[0]; !p.Available || p.Error != "" || len(p.Models) != 1 {
		t.Errorf("with a key: %+v", p)
	}
}
