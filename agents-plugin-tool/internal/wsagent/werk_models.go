package wsagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wscred"
)

// werkEfforts are the reasoning effort levels OpenRouter's unified reasoning
// parameter takes, lowest first.
var werkEfforts = []string{"minimal", "low", "medium", "high", "xhigh"}

// maxModelList bounds the model list read.
const maxModelList = 32 << 20

// werkModelsTimeout bounds one model list request.
const werkModelsTimeout = 15 * time.Second

// Choices lists OpenRouter's models for the dashboard's model picker
// (GET /api/v1/models) and the reasoning effort levels. The listing is
// public: it is sent with no credential, so a user can pick a tier's model
// before adding the key, and no token is spent or exposed by it.
//
// A model that declares its supported parameters and does not list tools is
// left out: the harness's loop is a tool loop, so such a model could not run
// a worker. Thinking is true for a model that lists reasoning, and false for
// one that declares its parameters without it; a listing that declares none
// says nothing.
func (b WerkBackend) Choices(ctx context.Context) ([]ModelChoice, []string, error) {
	url := b.modelsURL
	if url == "" {
		provider, ok := wscred.Lookup(werk.Provider)
		if !ok {
			return nil, werkEfforts, fmt.Errorf("the provider registry has no %s entry", werk.Provider)
		}
		url = strings.TrimRight(provider.BaseURL, "/") + "/models"
	}
	ctx, cancel := context.WithTimeout(ctx, werkModelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, werkEfforts, err
	}
	req.Header.Set("Accept", "application/json")
	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, werkEfforts, fmt.Errorf("list OpenRouter models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, werkEfforts, fmt.Errorf("list OpenRouter models: the endpoint answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelList))
	if err != nil {
		return nil, werkEfforts, fmt.Errorf("list OpenRouter models: %w", err)
	}
	models, err := parseWerkModels(raw)
	return models, werkEfforts, err
}

// parseWerkModels reads an OpenRouter model list answer.
func parseWerkModels(raw []byte) ([]ModelChoice, error) {
	var list struct {
		Data []struct {
			ID                  string   `json:"id"`
			SupportedParameters []string `json:"supported_parameters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("the OpenRouter model list is not readable: %w", err)
	}
	out := make([]ModelChoice, 0, len(list.Data))
	seen := map[string]bool{}
	for _, m := range list.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		choice := ModelChoice{ID: id}
		if len(m.SupportedParameters) > 0 {
			if !slices.Contains(m.SupportedParameters, "tools") {
				continue
			}
			thinking := slices.Contains(m.SupportedParameters, "reasoning") || slices.Contains(m.SupportedParameters, "include_reasoning")
			choice.Thinking = &thinking
		}
		seen[id] = true
		out = append(out, choice)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
