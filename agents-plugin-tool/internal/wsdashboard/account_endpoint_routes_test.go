package wsdashboard

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

func TestEndpointMachineRouteAndRedaction(t *testing.T) {
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	configPath, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	local := net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), "1234")
	remote := net.JoinHostPort(net.IPv4(192, 0, 2, 10).String(), "1234")
	store := wscred.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	d := &Daemon{credentials: store, endpoint: Endpoint{Token: "fixture-token"}, config: &ConfigBackend{Options: opts}}
	for _, test := range []struct {
		body, remote, origin string
		wrongConfig          bool
		want                 int
	}{
		{body: `{"op":"set","name":"desk","base_url":"http://example.invalid/v1","kind":"ollama","key":"fake-endpoint-secret"}`, remote: local, want: 200},
		{body: `{"op":"set","name":"desk","base_url":"http://secret:password@example.invalid","kind":"ollama","key":"fake-endpoint-secret"}`, remote: local, want: 400},
		{body: `{"op":"remove","name":"desk"} {}`, remote: local, want: 400},
		{body: `{"op":"remove","name":"desk"}`, remote: remote, want: 401},
		{body: `{"op":"remove","name":"desk"}`, remote: local, origin: "http://localhost", want: 401},
		{body: `{"op":"remove","name":"desk"}`, remote: local, wrongConfig: true, want: 409},
		{body: `{"op":"remove","name":"desk"}`, remote: local, want: 200},
	} {
		req := httptest.NewRequest("POST", "http://localhost"+EndpointChangePath, strings.NewReader(test.body))
		req.RemoteAddr = test.remote
		req.Header.Set("Authorization", "Bearer fixture-token")
		if !test.wrongConfig {
			req.Header.Set(endpointConfigHeader, companionConfigID(configPath))
		}
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
	local := net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), "1234")
	remote := net.JoinHostPort(net.IPv4(192, 0, 2, 1).String(), "1234")
	for _, test := range []struct {
		method, path, remote string
		want                 bool
	}{
		{"POST", EndpointChangePath, local, true},
		{"GET", EndpointChangePath, local, false},
		{"POST", EndpointChangePath + "/extra", local, false},
		{"POST", EndpointChangePath, remote, false},
	} {
		req := httptest.NewRequest(test.method, "http://localhost"+test.path, nil)
		req.RemoteAddr = test.remote
		if got := accessExempt(req); got != test.want {
			t.Fatalf("%v = %v", test, got)
		}
	}
}
