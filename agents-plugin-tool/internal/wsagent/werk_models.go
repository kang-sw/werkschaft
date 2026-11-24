package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
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
	providers := b.localProviders()
	if len(providers) == 0 {
		return b.hostedChoices(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, werkModelsTimeout)
	defer cancel()
	type listing struct {
		models []ModelChoice
		err    error
	}
	local := make([]listing, len(providers))
	var requests sync.WaitGroup
	for i, provider := range providers {
		requests.Add(1)
		go func() {
			defer requests.Done()
			local[i].models, local[i].err = b.localChoices(ctx, provider)
		}()
	}
	models, efforts, err := b.hostedChoices(ctx)
	requests.Wait()
	for _, result := range local {
		models = append(models, result.models...)
		err = errors.Join(err, result.err)
	}
	return models, efforts, err
}

func (b WerkBackend) choiceRevision() uint64 {
	if source, ok := b.credentials.(interface{ ProviderRevision() uint64 }); ok {
		return source.ProviderRevision()
	}
	if source, ok := b.credentials.(interface{ LocalRevision() uint64 }); ok {
		return source.LocalRevision()
	}
	return 0
}

func (b WerkBackend) localChoices(ctx context.Context, provider wscred.Provider) ([]ModelChoice, error) {
	models, err := b.listLocalModels(ctx, provider)
	if err != nil {
		return nil, fmt.Errorf("list %s models: %w", provider.ID, err)
	}
	return models, nil
}

func (b WerkBackend) listLocalModels(ctx context.Context, provider wscred.Provider) ([]ModelChoice, error) {
	token, err := b.providerToken(ctx, provider, false)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := clientCopy.Do(req)
	if err != nil {
		return nil, errors.New("could not reach the endpoint")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the endpoint answered %d", resp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelList+1))
	if err != nil || len(raw) > maxModelList || json.Unmarshal(raw, &list) != nil || list.Data == nil {
		return nil, errors.New("the endpoint model list is not readable")
	}
	seen := make(map[string]bool, len(list.Data))
	models := make([]ModelChoice, 0, len(list.Data))
	for _, entry := range list.Data {
		if modelPattern.MatchString(entry.ID) && !seen[entry.ID] {
			seen[entry.ID] = true
			models = append(models, ModelChoice{ID: entry.ID, Provider: string(provider.ID)})
		}
	}
	slices.SortFunc(models, func(a, b ModelChoice) int { return strings.Compare(a.ID, b.ID) })
	return models, nil
}

func (b WerkBackend) hostedChoices(ctx context.Context) ([]ModelChoice, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, werkModelsTimeout)
	defer cancel()
	catalog, ok := b.credentials.(interface {
		OpenAIModels(context.Context, wscred.Request) ([]wscred.OpenAIModel, error)
	})
	if !ok || b.providerGap(wscred.ProviderOpenAI) != "" {
		return b.openRouterChoices(ctx)
	}
	type listing struct {
		models []ModelChoice
		err    error
	}
	router := make(chan listing, 1)
	go func() {
		models, _, err := b.openRouterChoices(ctx)
		router <- listing{models, err}
	}()
	// Listing account models does not start inference or require background
	// consent. The lead's credential request checks consent before every turn.
	account, openAIErr := catalog.OpenAIModels(ctx, wscred.Request{AgentSession: true})
	result := <-router
	if openAIErr != nil {
		if b.openAIWindows != nil {
			b.openAIWindows.Store(nil)
		}
		return result.models, werkEfforts, errors.Join(result.err, openAIErr)
	}
	windows := make(map[string]int64, len(account))
	for _, entry := range account {
		if !modelPattern.MatchString(entry.Slug) {
			continue
		}
		result.models = append(result.models, ModelChoice{
			ID: entry.Slug, Provider: string(wscred.ProviderOpenAI), DisplayName: entry.DisplayName,
		})
		windows[entry.Slug] = max(entry.ContextWindow, 0)
	}
	if b.openAIWindows != nil {
		b.openAIWindows.Store(&windows)
	}
	if len(result.models) > 0 {
		return result.models, werkEfforts, nil
	}
	return result.models, werkEfforts, result.err
}

func (b WerkBackend) openRouterChoices(ctx context.Context) ([]ModelChoice, []string, error) {
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
	if err == nil && b.modelWindows != nil {
		windows := make(map[string]int64, len(models))
		for _, model := range models {
			windows[model.ID] = model.contextWindow
		}
		b.modelWindows.Store(&windows)
	}
	return models, werkEfforts, err
}

func (b WerkBackend) contextWindow(model string) int64 {
	return b.providerContextWindow("", model)
}

func (b WerkBackend) providerContextWindow(provider, model string) int64 {
	cache := b.modelWindows
	if provider == string(wscred.ProviderOpenAI) {
		cache = b.openAIWindows
	}
	if cache != nil {
		if windows := cache.Load(); windows != nil {
			return (*windows)[model]
		}
	}
	return 0
}

// parseWerkModels reads an OpenRouter model list answer.
func parseWerkModels(raw []byte) ([]ModelChoice, error) {
	var list struct {
		Data []struct {
			ID                  string   `json:"id"`
			ContextWindow       int64    `json:"context_length"`
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
		choice := ModelChoice{ID: id, contextWindow: max(m.ContextWindow, 0)}
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
