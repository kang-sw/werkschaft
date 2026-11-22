package wscred

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// The credential file (storage type 5, a plain file): one JSON document in
// the per-user cache root, never in a project tree, owner-only (POSIX mode
// 0600; on Windows a protected DACL granting the current user alone). It is
// a file, not a row in the daemon's SQLite stores, because the offline CLI
// writes it with no daemon running, and so that nothing which copies those
// stores ever copies a secret.
//
//	{
//	  "schema_version": 1,
//	  "providers": {
//	    "openrouter": {"kind": "api_key", "api_key": "...", "updated_at": "..."}
//	  }
//	}
//
// Compatibility: a writer keeps every member it does not know - top-level
// members, other providers' entries, and unknown fields of the entry it
// rewrites - so a newer version's additions survive an older writer. A file
// whose schema_version is newer than this binary's is read for the entries
// it understands and never rewritten.
const (
	// FileName is the credential file's name, in the cache root's auth
	// directory (DefaultPath).
	FileName      = "credentials.json"
	dirName       = "auth"
	schemaVersion = 1
	// maxKeyBytes bounds an API key; real keys are well under it.
	maxKeyBytes = 4096
)

// DefaultPath is the credential file under cacheHome's cache root ("" for
// the default root, wsstate.CacheRoot).
func DefaultPath(cacheHome string) (string, error) {
	root, err := wsstate.CacheRoot(wsstate.Options{CacheHome: cacheHome})
	if err != nil {
		return "", err
	}
	return filepath.Join(root, dirName, FileName), nil
}

// Store is the credential file's in-memory owner. It reads the file once, at
// its first use, and serves every later read from memory; a change writes
// the whole file by atomic replace and only then updates memory, so memory
// never holds what the file does not. While a daemon runs, its Store is the
// file's only writer (see the package doc).
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	loaded  bool
	loadErr error
	// failedAt identifies the file a failed load read (see loadLocked).
	failedAt fileStamp
	doc      document
	// keys are the API-key entries of doc, decoded.
	keys map[ProviderID]apiKeyEntry
}

// NewStore returns the store for the credential file at path. It touches
// no file until its first use.
func NewStore(path string) *Store { return &Store{path: path, now: time.Now} }

// Path is the credential file's path.
func (s *Store) Path() string { return s.path }

// ChangeOp is a credential change's kind.
type ChangeOp string

const (
	// OpSet stores Change.APIKey as the provider's key.
	OpSet ChangeOp = "set"
	// OpRemove forgets the provider's credential.
	OpRemove ChangeOp = "remove"
)

// Change is one credential change, from the CLI (or, later, the dashboard).
type Change struct {
	Op       ChangeOp
	Provider ProviderID
	// APIKey is the key OpSet stores; surrounding whitespace is trimmed.
	APIKey Secret
}

// ChangeResult is a change's outcome: Changed is false when the file
// already held the requested state (nothing was written).
type ChangeResult struct {
	Changed bool           `json:"changed"`
	Status  ProviderStatus `json:"status"`
}

// ProviderStatus is a provider's display state; it never carries a secret,
// only its masked form.
type ProviderStatus struct {
	Provider   ProviderID `json:"provider"`
	Name       string     `json:"name"`
	Kind       Kind       `json:"kind"`
	Configured bool       `json:"configured"`
	Masked     string     `json:"masked,omitempty"`
	UpdatedAt  string     `json:"updated_at,omitempty"`
}

type apiKeyEntry struct {
	Kind      Kind   `json:"kind"`
	APIKey    string `json:"api_key"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// document is the file's content: the members this version knows, decoded,
// and every other member raw.
type document struct {
	version   int
	extra     map[string]json.RawMessage
	providers map[string]json.RawMessage
}

func (d document) clone() document {
	out := document{version: d.version, extra: make(map[string]json.RawMessage, len(d.extra)), providers: make(map[string]json.RawMessage, len(d.providers))}
	for k, v := range d.extra {
		out.extra[k] = v
	}
	for k, v := range d.providers {
		out.providers[k] = v
	}
	return out
}

var errInvalidFile = errors.New("invalid credential file")

// decodeDocument parses the file. Its error never quotes the content.
func decodeDocument(raw []byte) (document, error) {
	doc := document{version: schemaVersion, extra: map[string]json.RawMessage{}, providers: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return doc, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return document{}, errInvalidFile
	}
	for name, value := range top {
		switch name {
		case "schema_version":
			if err := json.Unmarshal(value, &doc.version); err != nil {
				return document{}, errInvalidFile
			}
		case "providers":
			var providers map[string]json.RawMessage
			if err := json.Unmarshal(value, &providers); err != nil {
				return document{}, errInvalidFile
			}
			for id, entry := range providers {
				doc.providers[id] = entry
			}
		default:
			doc.extra[name] = value
		}
	}
	return doc, nil
}

func (d document) encode() ([]byte, error) {
	top := make(map[string]any, len(d.extra)+2)
	for k, v := range d.extra {
		top[k] = v
	}
	top["schema_version"] = d.version
	top["providers"] = d.providers
	raw, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// apiKeys decodes the API-key entries of the registered API-key providers.
// An entry of another kind (a newer version's) is not this version's to
// read and counts as absent.
func (d document) apiKeys() map[ProviderID]apiKeyEntry {
	out := map[ProviderID]apiKeyEntry{}
	for _, p := range providers {
		raw, ok := d.providers[string(p.ID)]
		if !ok || p.Kind != KindAPIKey {
			continue
		}
		var entry apiKeyEntry
		if json.Unmarshal(raw, &entry) != nil || entry.Kind != KindAPIKey || entry.APIKey == "" {
			continue
		}
		out[p.ID] = entry
	}
	return out
}

// fileStamp is what a stat says about a file: enough to tell that a broken
// file was edited, replaced, or removed since it was read.
type fileStamp struct {
	exists  bool
	size    int64
	modTime int64 // UnixNano: comparable with ==, unlike time.Time
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: info.Size(), modTime: info.ModTime().UnixNano()}
}

// loadLocked reads the file at the first use. A failed read is kept while the
// file stays as it was, so a broken file costs a stat, not a read and a
// parse, per request; once the user fixes, replaces, or removes it, the next
// use reads it again. retry (a change) rereads it regardless.
func (s *Store) loadLocked(retry bool) error {
	if s.loaded && s.loadErr == nil {
		return nil
	}
	if s.loaded && !retry && stampOf(s.path) == s.failedAt {
		return s.loadErr
	}
	s.loaded, s.loadErr = true, nil
	s.failedAt = stampOf(s.path)
	raw, err := wsstate.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		raw = nil
	case err != nil:
		s.loadErr = fmt.Errorf("read the credential file: %w", err)
		return s.loadErr
	}
	doc, err := decodeDocument(raw)
	if err != nil {
		s.loadErr = fmt.Errorf("the credential file %s is not valid JSON of the expected shape; fix or remove it", s.path)
		return s.loadErr
	}
	s.doc, s.keys = doc, doc.apiKeys()
	return nil
}

// Credential returns req.Provider's current credential (Source).
func (s *Store) Credential(_ context.Context, req Request) (Credential, error) {
	p, ok := Lookup(req.Provider)
	if !ok {
		return Credential{}, &ProviderError{Provider: req.Provider, Err: ErrUnknownProvider}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(false); err != nil {
		return Credential{}, err
	}
	entry, ok := s.keys[p.ID]
	if !ok {
		return Credential{}, &ProviderError{Provider: p.ID, Err: ErrNotConfigured}
	}
	return Credential{Provider: p.ID, Kind: KindAPIKey, Secret: NewSecret(entry.APIKey)}, nil
}

// Status returns every registered provider's display state, in registry
// order.
func (s *Store) Status() ([]ProviderStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(false); err != nil {
		return nil, err
	}
	out := make([]ProviderStatus, 0, len(providers))
	for _, p := range providers {
		out = append(out, s.statusLocked(p))
	}
	return out, nil
}

func (s *Store) statusLocked(p Provider) ProviderStatus {
	status := ProviderStatus{Provider: p.ID, Name: p.Name, Kind: p.Kind}
	if entry, ok := s.keys[p.ID]; ok {
		status.Configured, status.Masked, status.UpdatedAt = true, Mask(p, entry.APIKey), entry.UpdatedAt
	}
	return status
}

// Apply makes one change: it writes the file and then updates memory, or
// writes nothing when the file already holds the requested state. Its
// errors never quote the key.
func (s *Store) Apply(change Change) (ChangeResult, error) {
	p, ok := Lookup(change.Provider)
	if !ok {
		return ChangeResult{}, &ProviderError{Provider: change.Provider, Err: ErrUnknownProvider}
	}
	if p.Kind != KindAPIKey {
		return ChangeResult{}, fmt.Errorf("provider %q does not take an API key", p.ID)
	}
	key := ""
	if change.Op == OpSet {
		key = trimKey(change.APIKey.Reveal())
		if err := validateKey(key); err != nil {
			return ChangeResult{}, err
		}
	} else if change.Op != OpRemove {
		return ChangeResult{}, fmt.Errorf("unknown credential change %q", change.Op)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(true); err != nil {
		return ChangeResult{}, err
	}
	if s.doc.version > schemaVersion {
		return ChangeResult{}, fmt.Errorf("the credential file was written by a newer ws version (schema %d); update ws to change it", s.doc.version)
	}
	next := s.doc.clone()
	switch change.Op {
	case OpSet:
		if cur, ok := s.keys[p.ID]; ok && cur.APIKey == key {
			return ChangeResult{Status: s.statusLocked(p)}, nil
		}
		entry, err := mergeEntry(next.providers[string(p.ID)], apiKeyEntry{Kind: KindAPIKey, APIKey: key, UpdatedAt: s.now().UTC().Format(time.RFC3339)})
		if err != nil {
			return ChangeResult{}, err
		}
		next.providers[string(p.ID)] = entry
	case OpRemove:
		if _, ok := next.providers[string(p.ID)]; !ok {
			return ChangeResult{Status: s.statusLocked(p)}, nil
		}
		delete(next.providers, string(p.ID))
	}
	next.version = schemaVersion
	data, err := next.encode()
	if err != nil {
		return ChangeResult{}, err
	}
	if err := writePrivateFile(s.path, data); err != nil {
		return ChangeResult{}, fmt.Errorf("write the credential file: %w", err)
	}
	s.doc, s.keys = next, next.apiKeys()
	return ChangeResult{Changed: true, Status: s.statusLocked(p)}, nil
}

// mergeEntry lays entry's fields over the raw entry it replaces, keeping
// the fields this version does not know.
func mergeEntry(old json.RawMessage, entry apiKeyEntry) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if len(old) > 0 {
		// An entry that is not an object is replaced whole.
		_ = json.Unmarshal(old, &fields)
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(raw, &known); err != nil {
		return nil, err
	}
	for k, v := range known {
		fields[k] = v
	}
	return json.Marshal(fields)
}

func trimKey(key string) string {
	start, end := 0, len(key)
	for start < end && isSpace(key[start]) {
		start++
	}
	for end > start && isSpace(key[end-1]) {
		end--
	}
	return key[start:end]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// validateKey checks a key's shape without quoting it.
func validateKey(key string) error {
	switch {
	case key == "":
		return errors.New("the API key is empty")
	case len(key) > maxKeyBytes:
		return fmt.Errorf("the API key is longer than %d bytes", maxKeyBytes)
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c <= ' ' || c >= 0x7f {
			return errors.New("the API key holds whitespace, control, or non-ASCII characters")
		}
	}
	return nil
}

// String and GoString keep a printed Store from showing the keys it holds
// (fmt prints unexported fields raw).
func (s *Store) String() string   { return "wscred.Store(" + s.path + ")" }
func (s *Store) GoString() string { return s.String() }
