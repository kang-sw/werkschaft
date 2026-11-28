package wsdashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func companionAgentsRequest(method, origin string) *http.Request {
	r := httptest.NewRequest(method, companionAgentsRoute, nil)
	r.Host, r.RemoteAddr = "127.0.0.1:19544", "127.0.0.1:40000"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

// The readback route serves the bound readback to a local GET, answers
// upgrade_required with none bound, and refuses foreign origins and writes.
func TestCompanionAgentsRouteServesTheBoundReadback(t *testing.T) {
	d := &Daemon{}
	w := httptest.NewRecorder()
	d.handleCompanionAgents(w, companionAgentsRequest(http.MethodGet, ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unbound readback = %d %s", w.Code, w.Body)
	}
	d.SetCompanionAgents(func() any { return map[string]string{"body": "x"} })
	w = httptest.NewRecorder()
	d.handleCompanionAgents(w, companionAgentsRequest(http.MethodGet, ""))
	var got struct {
		ResolvedAgents map[string]string `json:"resolved_agents"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ResolvedAgents["body"] != "x" {
		t.Fatalf("bound readback = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	d.handleCompanionAgents(w, companionAgentsRequest(http.MethodGet, "https://evil.example"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin = %d", w.Code)
	}
	w = httptest.NewRecorder()
	d.handleCompanionAgents(w, companionAgentsRequest(http.MethodPost, ""))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", w.Code)
	}
	d.SetCompanionAgents(nil)
	w = httptest.NewRecorder()
	d.handleCompanionAgents(w, companionAgentsRequest(http.MethodGet, ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cleared readback = %d", w.Code)
	}
}
