package wsagent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

func TestWerkLocalPickerListsModelsPerEndpointAndRefreshes(t *testing.T) {
	const model = "vendor/shared:latest"
	const key = "fixture-local-models-key"
	serve := func(path, wantAuth, data string) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != path {
				t.Errorf("model request = %s %s", r.Method, r.URL.Path)
			}
			if r.Header.Get("Authorization") != wantAuth {
				t.Error("unexpected API key on model request")
			}
			_, _ = io.WriteString(w, data)
		}))
		t.Cleanup(server.Close)
		return server
	}
	localList := fmt.Sprintf(`{"data":[{"id":%q},{"id":%q},{"id":""}]}`, model, model)
	router := serve("/models", "", `{"data":[]}`)
	desk := serve("/desk/v1/models", "", localList)
	lab := serve("/lab/v1/models", "Bearer "+key, localList)
	config := wsconfig.Options{ConfigHome: t.TempDir()}
	store := wscred.NewStore(filepath.Join(t.TempDir(), wscred.FileName))
	resolver := wsconfig.NewResolver(config, nil, nil, nil)
	configure := func(raw string) {
		t.Helper()
		if err := resolver.Set(wsconfig.ItemWerkEndpoints, raw, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := store.LoadLocalEndpoints(config); err != nil {
			t.Fatal(err)
		}
	}
	configure(fmt.Sprintf(`{"desk":{"base_url":%q,"kind":"ollama"},"lab":{"base_url":%q,"kind":"vllm"}}`, desk.URL+"/desk/v1/", lab.URL+"/lab/v1"))
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: "local/lab", APIKey: wscred.NewSecret(key)}); err != nil {
		t.Fatal(err)
	}
	sh := newSupportHarness(t)
	b := WerkBackend{
		modelsURL: router.URL + "/models", credentials: store,
		credentialStatus: func(string) (bool, error) { return false, nil },
	}
	_, reg := b.registration()
	sh.runner.opts.Backends[BackendWerk] = reg
	check := func(ids []string) {
		t.Helper()
		choices := choicesWithPickers(t, sh.support)
		entry := harnessByName(t, choices, BackendWerk)
		if !entry.Available || entry.Error != "" || !slices.Equal(entry.ModelProviders, ids) || len(entry.Models) != len(ids) {
			t.Fatalf("picker = %+v", entry)
		}
		for i, m := range entry.Models {
			if m.ID != model || m.Provider != ids[i] {
				t.Fatalf("model %d = %+v", i, m)
			}
		}
	}
	check([]string{"local/desk", "local/lab"})
	configure(fmt.Sprintf(`{"lab":{"base_url":%q,"kind":"vllm"}}`, lab.URL+"/lab/v1"))
	check([]string{"local/lab"})
}
