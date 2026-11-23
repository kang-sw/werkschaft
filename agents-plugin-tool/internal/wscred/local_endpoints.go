package wscred

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// LoadLocalEndpoints replaces the machine-scoped endpoint registry. The
// daemon calls it before recovery and on global configuration changes.
func (s *Store) LoadLocalEndpoints(opts wsconfig.Options) error {
	overrides, err := wsconfig.GlobalOverrides(opts)
	if err != nil {
		return err
	}
	endpoints, err := wsconfig.ParseWerkEndpoints(overrides[wsconfig.ItemWerkEndpoints])
	if err != nil {
		return err
	}
	providers := make([]Provider, 0, len(endpoints))
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		endpoint := endpoints[name]
		providers = append(providers, Provider{
			ID: ProviderID(wsconfig.LocalWerkProviderID(name)), Name: name,
			Kind: KindAPIKey, BaseURL: endpoint.BaseURL, Dialect: endpoint.Kind,
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.localProviders.Swap(&providers)
	if previous == nil || !slices.EqualFunc(*previous, providers, func(a, b Provider) bool {
		return a.ID == b.ID && a.BaseURL == b.BaseURL && a.Dialect == b.Dialect
	}) {
		s.localRevision.Add(1)
	}
	return nil
}

// LocalRevision changes when an endpoint definition or API key changes.
func (s *Store) LocalRevision() uint64 { return s.localRevision.Load() }

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
