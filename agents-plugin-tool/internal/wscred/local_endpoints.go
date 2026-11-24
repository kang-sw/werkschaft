package wscred

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// LoadLocalEndpoints replaces the machine-scoped endpoint registry. The
// daemon calls it before recovery and on global configuration changes.
func (s *Store) LoadLocalEndpoints(opts wsconfig.Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	overrides, err := wsconfig.GlobalOverrides(opts)
	if err != nil {
		return err
	}
	endpoints, err := wsconfig.ParseWerkEndpoints(overrides[wsconfig.ItemWerkEndpoints])
	if err != nil {
		return err
	}
	return s.reconcileLocalEndpointsLocked(endpoints, nil)
}

func (s *Store) reconcileLocalEndpointsLocked(endpoints map[string]wsconfig.WerkEndpoint, change *LocalEndpointChange) error {
	providers := make([]Provider, 0, len(endpoints))
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		endpoint := endpoints[name]
		providers = append(providers, Provider{
			ID: ProviderID(wsconfig.LocalWerkProviderID(name)), Name: name,
			Kind: KindAPIKey, BaseURL: endpoint.BaseURL, Dialect: endpoint.Kind,
		})
	}
	previous := s.localProviders.Swap(&providers)
	if previous == nil || !slices.EqualFunc(*previous, providers, func(a, b Provider) bool {
		return a.ID == b.ID && a.BaseURL == b.BaseURL && a.Dialect == b.Dialect
	}) {
		s.localRevision.Add(1)
	}
	// Recover against the configured endpoints during startup and global
	// writes. Publish the routing even if credential cleanup must be retried.
	if err := s.prepareWriteLocked(); err != nil {
		return err
	}
	next := s.doc.clone()
	valid := map[string]bool{}
	for _, p := range providers {
		valid[string(p.keyID())] = true
	}
	dirty := false
	for id := range next.providers {
		if strings.HasPrefix(id, "local/") && !valid[id] {
			delete(next.providers, id)
			dirty = true
		}
	}
	if change != nil && change.Key.Reveal() != "" && change.Op == OpSet {
		for _, p := range providers {
			if string(p.ID) != wsconfig.LocalWerkProviderID(change.Name) || p.BaseURL != change.BaseURL {
				continue
			}
			id := p.keyID()
			key := trimKey(change.Key.Reveal())
			if cur, ok := s.keys[id]; ok && cur.APIKey == key {
				continue
			}
			entry, err := mergeEntry(next.providers[string(id)], apiKeyEntry{Kind: KindAPIKey, APIKey: key, UpdatedAt: s.now().UTC().Format(time.RFC3339)})
			if err != nil {
				return err
			}
			next.providers[string(id)] = entry
			dirty = true
		}
	}
	if dirty {
		// Invalidate even when publication succeeds but retirement reports an error.
		s.localRevision.Add(1)
		if err := s.writeLocked(next); err != nil {
			return err
		}
	}
	return nil
}

// LocalRevision changes when an endpoint definition or API key changes.
func (s *Store) LocalRevision() uint64 { return s.localRevision.Load() }

// ProviderRevision also invalidates model catalogs when the OpenAI account or grant changes.
func (s *Store) ProviderRevision() uint64 { return s.LocalRevision() + s.openAIRevision.Load() }

// LocalProviders returns the configured custom API-key providers.
func (s *Store) LocalProviders() []Provider {
	if providers := s.localProviders.Load(); providers != nil {
		return slices.Clone(*providers)
	}
	return nil
}

func (s *Store) lookupProvider(id ProviderID) (Provider, bool) {
	if p, ok := Lookup(id); ok {
		return p, true
	}
	if providers := s.localProviders.Load(); providers != nil {
		for _, p := range *providers {
			if p.ID == id {
				return p, true
			}
		}
	}
	return Provider{}, false
}

// keyID binds a named endpoint key to its API root. Editing a URL cannot
// send the name's previous credential to the new destination.
func (p Provider) keyID() ProviderID {
	if p.Dialect == "" {
		return p.ID
	}
	return ProviderID(fmt.Sprintf("%s:%x", p.ID, sha256.Sum256([]byte(p.BaseURL))))
}

// LocalEndpointChange carries a structured endpoint edit; Key is never returned.
type LocalEndpointChange struct {
	Op                  ChangeOp
	Name, BaseURL, Kind string
	Key                 Secret
}

type LocalEndpointStatus struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Kind    string `json:"kind"`
}

var ErrInvalidEndpoint = errors.New("invalid endpoint")

// ApplyLocalEndpoint serializes endpoint edits and key cleanup with key writes.
func (s *Store) ApplyLocalEndpoint(opts wsconfig.Options, change LocalEndpointChange) ([]LocalEndpointStatus, error) {
	if !wsconfig.IsLocalWerkProvider(wsconfig.LocalWerkProviderID(change.Name)) {
		return nil, ErrInvalidEndpoint
	}
	var endpoint *wsconfig.WerkEndpoint
	if change.Op == OpSet {
		raw, _ := json.Marshal(map[string]wsconfig.WerkEndpoint{change.Name: {BaseURL: change.BaseURL, Kind: change.Kind}})
		parsed, err := wsconfig.ParseWerkEndpoints(string(raw))
		if err != nil {
			return nil, ErrInvalidEndpoint
		}
		value := parsed[change.Name]
		endpoint = &value
		if change.Key.Reveal() != "" {
			if validateKey(trimKey(change.Key.Reveal())) != nil {
				return nil, ErrInvalidEndpoint
			}
		}
	} else if change.Op != OpRemove {
		return nil, ErrInvalidEndpoint
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Finish any prior failed cleanup against the still-current config before
	// accepting another edit. This prevents restoring a retired URL's key.
	endpoints, err := wsconfig.UpdateWerkEndpoint(opts, change.Name, endpoint, func(current map[string]wsconfig.WerkEndpoint) error {
		return s.reconcileLocalEndpointsLocked(current, nil)
	})
	if err != nil {
		return nil, err
	}
	if endpoint != nil {
		change.BaseURL = endpoint.BaseURL
	}
	if err := s.reconcileLocalEndpointsLocked(endpoints, &change); err != nil {
		return nil, err
	}
	result := []LocalEndpointStatus{}
	for _, p := range s.LocalProviders() {
		result = append(result, LocalEndpointStatus{Name: p.Name, BaseURL: p.BaseURL, Kind: p.Dialect})
	}
	return result, nil
}
