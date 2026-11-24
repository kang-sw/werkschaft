package wscred

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// LoadLocalEndpoints reads the machine-scoped registry and warms the credential
// store before routing starts. Only an edit can establish that a key was retired.
func (s *Store) LoadLocalEndpoints(opts wsconfig.Options) error {
	s.localMu.Lock()
	defer s.localMu.Unlock()

	overrides, err := wsconfig.GlobalOverrides(opts)
	if err != nil {
		return err
	}
	endpoints, err := wsconfig.ParseWerkEndpoints(overrides[wsconfig.ItemWerkEndpoints])
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocalEndpointsLocked(endpoints)
	return s.loadLocked(false)
}

func (s *Store) publishLocalEndpointsLocked(endpoints map[string]wsconfig.WerkEndpoint) {
	providers := make([]Provider, 0, len(endpoints))
	for _, name := range slices.Sorted(maps.Keys(endpoints)) {
		providers = append(providers, localProvider(name, endpoints[name]))
	}
	previous := s.localProviders.Swap(&providers)
	if previous == nil || !slices.EqualFunc(*previous, providers, func(a, b Provider) bool {
		return a.ID == b.ID && a.BaseURL == b.BaseURL && a.Dialect == b.Dialect
	}) {
		s.localRevision.Add(1)
	}
}

func localProvider(name string, endpoint wsconfig.WerkEndpoint) Provider {
	return Provider{ID: ProviderID(wsconfig.LocalWerkProviderID(name)), Name: name,
		Kind: KindAPIKey, BaseURL: endpoint.BaseURL, Dialect: endpoint.Kind}
}

func (s *Store) retireLocalEndpointKeysLocked(before, after map[string]wsconfig.WerkEndpoint) (bool, error) {
	if err := s.prepareWriteLocked(); err != nil {
		return false, err
	}
	var ids []string
	for name, prior := range before {
		if current, ok := after[name]; ok && current.BaseURL == prior.BaseURL {
			continue
		}
		id := string(localProvider(name, prior).keyID())
		if _, ok := s.doc.providers[id]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return false, nil
	}
	oldCount := len(s.doc.providers)
	next := s.doc.clone()
	for _, id := range ids {
		delete(next.providers, id)
	}
	s.localRevision.Add(1)
	err := s.writeLocked(next)
	return len(s.doc.providers) < oldCount, err
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

// ErrEndpointKeyRetired means the config change failed after removing its key.
var ErrEndpointKeyRetired = errors.New("endpoint configuration change was not applied, but its previous key was removed")

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
	return s.editLocalEndpoints(true, func(retire func(before, after map[string]wsconfig.WerkEndpoint) error) (map[string]wsconfig.WerkEndpoint, error) {
		return wsconfig.UpdateWerkEndpoint(opts, change.Name, endpoint, retire)
	}, change)
}

// SetLocalEndpointOverride applies the legacy whole-map write or reset through
// the same retirement guard. Edits to other config homes leave routing intact.
func (s *Store) SetLocalEndpointOverride(opts wsconfig.Options, value *string, publish bool) error {
	_, err := s.editLocalEndpoints(publish, func(retire func(before, after map[string]wsconfig.WerkEndpoint) error) (map[string]wsconfig.WerkEndpoint, error) {
		return wsconfig.SetWerkEndpointOverride(opts, value, retire)
	}, LocalEndpointChange{})
	return err
}

func (s *Store) editLocalEndpoints(publish bool, write func(retire func(before, after map[string]wsconfig.WerkEndpoint) error) (map[string]wsconfig.WerkEndpoint, error), change LocalEndpointChange) ([]LocalEndpointStatus, error) {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	locked, retired := false, false
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	endpoints, err := write(func(before, after map[string]wsconfig.WerkEndpoint) error {
		s.mu.Lock()
		locked = true
		var err error
		// Stage config first, then retire keys before publishing it. A crash
		// between those commits can leave the old URL without a key. Publishing
		// config first could allow a subsequent edit to revive a retired key.
		retired, err = s.retireLocalEndpointKeysLocked(before, after)
		return err
	})
	if err != nil {
		if retired {
			return nil, fmt.Errorf("%w: %w", ErrEndpointKeyRetired, err)
		}
		return nil, err
	}
	if !locked {
		s.mu.Lock()
		locked = true
	}
	if s.closed {
		return nil, ErrStoreClosed
	}
	if publish {
		s.publishLocalEndpointsLocked(endpoints)
	}
	if key := trimKey(change.Key.Reveal()); key != "" {
		if err := s.writeLocalEndpointKeyLocked(localProvider(change.Name, endpoints[change.Name]), key); err != nil {
			return nil, err
		}
	}
	result := []LocalEndpointStatus{}
	for _, p := range s.LocalProviders() {
		result = append(result, LocalEndpointStatus{Name: p.Name, BaseURL: p.BaseURL, Kind: p.Dialect})
	}
	return result, nil
}

func (s *Store) writeLocalEndpointKeyLocked(p Provider, key string) error {
	if err := s.prepareWriteLocked(); err != nil {
		return err
	}
	id := p.keyID()
	if cur, ok := s.keys[id]; ok && cur.APIKey == key {
		return nil
	}
	next := s.doc.clone()
	entry, err := mergeEntry(next.providers[string(id)], apiKeyEntry{Kind: KindAPIKey, APIKey: key, UpdatedAt: s.now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	next.providers[string(id)] = entry
	s.localRevision.Add(1)
	return s.writeLocked(next)
}
