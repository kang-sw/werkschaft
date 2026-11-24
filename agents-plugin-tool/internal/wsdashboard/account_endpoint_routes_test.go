package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

func TestEndpointMachineRouteAndRedaction(t *testing.T) {
	t.Setenv(wsconfig.EnvConfigHome, t.TempDir())
	store := wscred.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	d := &Daemon{credentials: store, endpoint: Endpoint{Token: "fixture-token"}}
	for _, test := range []struct {
		body, remote, origin string
		want                 int
	}{
		{body: `{"op":"set","name":"desk","base_url":"http://example.invalid/v1","kind":"ollama","key":"fake-endpoint-secret"}`, remote: "127.0.0.1:1234", want: 200},
		{body: `{"op":"set","name":"desk","base_url":"http://secret:password@example.invalid","kind":"ollama","key":"fake-endpoint-secret"}`, remote: "127.0.0.1:1234", want: 400},
		{body: `{"op":"remove","name":"desk"} {}`, remote: "127.0.0.1:1234", want: 400},
		{body: `{"op":"remove","name":"desk"}`, remote: "192.0.2.10:1234", want: 401},
		{body: `{"op":"remove","name":"desk"}`, remote: "127.0.0.1:1234", origin: "http://localhost", want: 401},
		{body: `{"op":"remove","name":"desk"}`, remote: "127.0.0.1:1234", want: 200},
	} {
		req := httptest.NewRequest("POST", "http://localhost"+EndpointChangePath, strings.NewReader(test.body))
		req.RemoteAddr = test.remote
		req.Header.Set("Authorization", "Bearer fixture-token")
		if test.origin != "" {
			req.Header.Set("Origin", test.origin)
		}
		w := httptest.NewRecorder()
		d.handleEndpointChange(w, req)
		if w.Code != test.want {
			t.Fatalf("status %d want %d: %s", w.Code, test.want, w.Body)
		}
		for _, secret := range []string{"fake-endpoint-secret", "password", "fixture-token"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("response leaked secret: %s", w.Body)
			}
		}
		if !json.Valid(w.Body.Bytes()) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing safe JSON response")
		}
	}
}

func TestEndpointOfflineMutation(t *testing.T) {
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	endpoints, err := ApplyLocalEndpointChange(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "credentials.json"), opts, wscred.LocalEndpointChange{Op: wscred.OpSet, Name: "desk", BaseURL: "http://example.invalid/v1", Kind: "vllm"})
	if err != nil || len(endpoints) != 1 || endpoints[0].Name != "desk" {
		t.Fatalf("offline result: %v %v", endpoints, err)
	}
}

func TestEndpointMachineExemptionIsExact(t *testing.T) {
	for _, test := range []struct {
		method, path, remote string
		want                 bool
	}{
		{"POST", EndpointChangePath, "127.0.0.1:1234", true},
		{"GET", EndpointChangePath, "127.0.0.1:1234", false},
		{"POST", EndpointChangePath + "/extra", "127.0.0.1:1234", false},
		{"POST", EndpointChangePath, "192.0.2.1:1234", false},
	} {
		req := httptest.NewRequest(test.method, "http://localhost"+test.path, nil)
		req.RemoteAddr = test.remote
		if got := accessExempt(req); got != test.want {
			t.Fatalf("%v = %v", test, got)
		}
	}
}
