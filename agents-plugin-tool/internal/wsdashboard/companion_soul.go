package wsdashboard

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsstate"
)

const (
	companionDefaultSoulID      = "default"
	companionSoulDirName        = "companion-soul"
	companionMachineIDFile      = "companion-machine-id"
	companionSoulSchema         = "ws-companion-soul/v1"
	companionSoulDocMaxBytes    = 256 << 10 // 256 KiB per persona.md / profile.md
	companionSoulEntryMaxBytes  = 8 << 10   // 8 KiB per shard entry
	companionSoulZipMaxBytes    = 16 << 20  // 16 MiB uncompressed ZIP import cap
	companionSoulZipMaxFiles    = 256
	companionSoulZipMaxFileSize = 2 << 20
	companionSoulRecentDefault  = 30
	// companionSoulPresetFile holds the official persona preset ID the user
	// picked for a Soul; it exists only for a non-default pick.
	companionSoulPresetFile     = "persona-preset"
	companionSoulPresetMaxBytes = 256
)

var (
	companionSoulIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	companionMachineIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	companionShardFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-[a-z0-9][a-z0-9-]{0,31}\.jsonl$`)
)

// SoulShardEntry is one append-only line in log/YYYY-MM-<machine-id>.jsonl.
type SoulShardEntry struct {
	ID        string   `json:"id"`
	Timestamp string   `json:"ts"`
	MachineID string   `json:"machine_id"`
	Kind      string   `json:"kind,omitempty"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
}

// SoulSnapshot is the RAM-cached view of a Portable Soul served to callers and Settings UI.
type SoulSnapshot struct {
	SoulID         string           `json:"soulId"`
	SoulDir        string           `json:"soulDir"`
	DefaultSoulDir string           `json:"defaultSoulDir"`
	MachineID      string           `json:"machineId"`
	Souls          []string         `json:"souls"`
	Persona        string           `json:"persona"`
	Profile        string           `json:"profile"`
	RecentEntries  []SoulShardEntry `json:"recentEntries"`
	TotalEntries   int              `json:"totalEntries"`
	ShardCount     int              `json:"shardCount"`
	// PersonaSource is "user" when persona.md holds the user's own text;
	// otherwise the store leaves it empty and the daemon fills in "pack" or
	// "official" with the inherited persona text.
	PersonaSource string `json:"personaSource,omitempty"`
	// PersonaInherited is true when no user persona.md overrides the
	// inherited (preset, pack, or official) persona.
	PersonaInherited bool `json:"personaInherited"`
	// PersonaPreset is the official preset ID the user picked for this Soul
	// ("" when none, which also means "default").
	PersonaPreset string `json:"personaPreset,omitempty"`
}

type soulZipManifest struct {
	Schema     string `json:"schema"`
	SoulID     string `json:"soul_id"`
	ExportedAt string `json:"exported_at"`
}

// SoulImportResult reports what a Portable Soul ZIP import applied.
type SoulImportResult struct {
	SoulID         string `json:"soulId"`
	ImportedShards int    `json:"importedShards"`
	MergedEntries  int    `json:"mergedEntries"`
	UpdatedPersona bool   `json:"updatedPersona"`
	UpdatedProfile bool   `json:"updatedProfile"`
}

// CompanionSoulStore manages Portable Soul files with a daemon RAM hot cache:
// cold load reads persona.md, profile.md, and log/*.jsonl once (or caches
// that the Soul directory is absent); warm reads touch zero files; reads
// never write. Only explicit user actions (Settings save or reset, approving
// a pending item, import, create) and memory appends write files; shard
// writes append a single JSONL line without rewriting existing files or
// rescanning directories. persona.md is user-owned: nothing seeds it, and an
// absent or blank persona.md inherits a picked official preset, the pack, or
// the default preset. The picked preset lives in persona-preset, written only
// for a non-default pick, so a default Soul has no such file.
type CompanionSoulStore struct {
	stateDir string
	homeDir  string
	now      func() time.Time

	mu         sync.RWMutex
	machineID  string
	loaded     bool
	loadedRoot string
	loadedID   string
	dirExists  bool
	persona    string
	preset     string
	profile    string
	entries    []SoulShardEntry
	entryIDs   map[string]struct{}
	shardFiles map[string]struct{}

	// diskReads increments on every cold disk load so tests can assert zero
	// warm disk reads.
	diskReads atomic.Int64
}

// NewCompanionSoulStore creates a Portable Soul store bound to a daemon state dir.
func NewCompanionSoulStore(stateDir, homeDir string, now func() time.Time) *CompanionSoulStore {
	if now == nil {
		now = time.Now
	}
	return &CompanionSoulStore{
		stateDir: stateDir,
		homeDir:  homeDir,
		now:      now,
	}
}

// DiskReadCount returns how many cold disk loads have occurred (for tests).
func (s *CompanionSoulStore) DiskReadCount() int64 {
	return s.diskReads.Load()
}

// DefaultRootDir returns ~/.ws/companion-soul for the configured home directory.
func (s *CompanionSoulStore) DefaultRootDir() (string, error) {
	home := strings.TrimSpace(s.homeDir)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".ws", companionSoulDirName), nil
}

// ResolveRootDir resolves customDir (supporting leading ~/) or falls back to
// ~/.ws/companion-soul. Relative non-tilde paths and control characters are refused.
func (s *CompanionSoulStore) ResolveRootDir(customDir string) (string, error) {
	raw := strings.TrimSpace(customDir)
	if raw == "" {
		return s.DefaultRootDir()
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("soul directory contains control characters")
		}
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
		home := strings.TrimSpace(s.homeDir)
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", err
			}
		}
		if raw == "~" {
			raw = home
		} else {
			raw = filepath.Join(home, raw[2:])
		}
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New("soul directory must be an absolute path")
	}
	return filepath.Clean(raw), nil
}

// ResolveSoulID normalizes an empty ID to "default" and validates the identifier.
func ResolveSoulID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return companionDefaultSoulID, nil
	}
	if !companionSoulIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid soul id %q", id)
	}
	return id, nil
}

// MachineID returns the persisted random machine identifier ([a-z0-9-]),
// creating and persisting it once in the daemon state directory.
func (s *CompanionSoulStore) MachineID() (string, error) {
	s.mu.RLock()
	if s.machineID != "" {
		id := s.machineID
		s.mu.RUnlock()
		return id, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureMachineIDLocked()
}

func (s *CompanionSoulStore) ensureMachineIDLocked() (string, error) {
	if s.machineID != "" {
		return s.machineID, nil
	}
	if strings.TrimSpace(s.stateDir) == "" {
		id, err := generateCompanionMachineID()
		if err != nil {
			return "", err
		}
		s.machineID = id
		return id, nil
	}
	idPath := filepath.Join(s.stateDir, companionMachineIDFile)
	if raw, err := os.ReadFile(idPath); err == nil {
		sanitized := sanitizeMachineID(string(raw))
		if sanitized != "" {
			s.machineID = sanitized
			return sanitized, nil
		}
	}
	id, err := generateCompanionMachineID()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		return "", err
	}
	if err := writeAtomicPrivateFile(idPath, []byte(id+"\n")); err != nil {
		return "", err
	}
	s.machineID = id
	return id, nil
}

func generateCompanionMachineID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	hexStr := hex.EncodeToString(b[:])
	return "m-" + hexStr[:8] + "-" + hexStr[8:], nil
}

func sanitizeMachineID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 32 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if !companionMachineIDPattern.MatchString(out) {
		return ""
	}
	return out
}

// Invalidate drops the in-memory hot cache so the next access reloads from disk.
func (s *CompanionSoulStore) Invalidate() {
	s.mu.Lock()
	s.loaded = false
	s.loadedRoot = ""
	s.loadedID = ""
	s.mu.Unlock()
}

// Load returns the RAM-cached SoulSnapshot for (customDir, soulID). On cache
// hit (same resolved root and soulID), zero disk reads occur. A cold load
// reads the Soul's files once and writes nothing: an absent Soul yields an
// empty snapshot (cached, so later reads touch no file either).
func (s *CompanionSoulStore) Load(customDir, rawSoulID string, recentLimit int) (SoulSnapshot, error) {
	snap, _, err := s.PeekExisting(customDir, rawSoulID, recentLimit)
	return snap, err
}

// PeekExisting returns the RAM-cached SoulSnapshot for (customDir, rawSoulID)
// and whether that Soul's directory exists. It never creates directories,
// never writes persona.md, and never writes companion-machine-id. Absence is
// cached in RAM like any loaded Soul, so repeated wakes for a Soul that does
// not exist yet touch no file; writes through this store update the cache,
// and Invalidate (Soul config change, import) forces a reread.
func (s *CompanionSoulStore) PeekExisting(customDir, rawSoulID string, recentLimit int) (SoulSnapshot, bool, error) {
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return SoulSnapshot{}, false, err
	}
	defaultRoot, _ := s.DefaultRootDir()
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return SoulSnapshot{}, false, err
	}
	if recentLimit <= 0 {
		recentLimit = companionSoulRecentDefault
	}

	// Fast RAM hot-cache path: zero disk reads.
	s.mu.RLock()
	if s.loaded && s.loadedRoot == root && s.loadedID == soulID {
		snap, exists := s.snapshotLocked(root, defaultRoot, recentLimit), s.dirExists
		s.mu.RUnlock()
		return snap, exists, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return SoulSnapshot{}, false, err
		}
	}
	return s.snapshotLocked(root, defaultRoot, recentLimit), s.dirExists, nil
}

// SearchCached searches the active Soul's RAM-cached shard entries without
// auto-creating Soul files on disk. Warm calls perform zero disk reads. When
// query is empty, returns up to limit newest entries (newest first).
func (s *CompanionSoulStore) SearchCached(customDir, rawSoulID, query string, limit int) ([]SoulShardEntry, bool, error) {
	if limit <= 0 {
		limit = companionRecallDefaultLimit
	}
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return nil, false, err
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	if s.loaded && s.loadedRoot == root && s.loadedID == soulID {
		out, exists := matchSoulEntriesLocked(s.entries, query, limit), s.dirExists
		s.mu.RUnlock()
		return out, exists, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return nil, false, err
		}
	}
	return matchSoulEntriesLocked(s.entries, query, limit), s.dirExists, nil
}

func matchSoulEntriesLocked(entries []SoulShardEntry, query string, limit int) []SoulShardEntry {
	if len(entries) == 0 || limit <= 0 {
		return nil
	}
	tokens := tokenizeCompanionSearchQuery(query)
	if len(tokens) == 0 {
		n := min(limit, len(entries))
		out := make([]SoulShardEntry, 0, n)
		for i := len(entries) - 1; i >= 0 && len(out) < n; i-- {
			out = append(out, cloneSoulShardEntry(entries[i]))
		}
		return out
	}

	type scored struct {
		entry SoulShardEntry
		score int
		idx   int
	}
	var matches []scored
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		haystack := strings.ToLower(e.Content + " " + e.Kind + " " + strings.Join(e.Tags, " "))
		score := 0
		for _, tok := range tokens {
			if strings.Contains(haystack, tok) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{entry: e, score: score, idx: i})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].idx > matches[j].idx
	})
	n := min(limit, len(matches))
	out := make([]SoulShardEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, cloneSoulShardEntry(matches[i].entry))
	}
	return out
}

func cloneSoulShardEntry(e SoulShardEntry) SoulShardEntry {
	if len(e.Tags) > 0 {
		e.Tags = append([]string(nil), e.Tags...)
	}
	return e
}

func tokenizeCompanionSearchQuery(raw string) []string {
	var tokens []string
	seen := make(map[string]struct{})
	var cur []rune
	flush := func() {
		if len(cur) == 0 {
			return
		}
		tok := strings.ToLower(string(cur))
		cur = cur[:0]
		if _, dup := seen[tok]; !dup {
			seen[tok] = struct{}{}
			tokens = append(tokens, tok)
		}
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// loadColdLocked reads one Soul into the RAM cache without writing anything:
// the machine ID only if already persisted, then one stat of the Soul
// directory; when it exists, persona.md, profile.md, and log/*.jsonl. An
// absent directory caches an empty Soul.
func (s *CompanionSoulStore) loadColdLocked(root, soulID string) error {
	if s.machineID == "" && strings.TrimSpace(s.stateDir) != "" {
		if raw, err := os.ReadFile(filepath.Join(s.stateDir, companionMachineIDFile)); err == nil {
			if sanitized := sanitizeMachineID(string(raw)); sanitized != "" {
				s.machineID = sanitized
			}
		}
	}
	s.diskReads.Add(1)

	soulDir := filepath.Join(root, soulID)
	var (
		persona, profile     string
		preset               string
		entries              = make([]SoulShardEntry, 0)
		entryIDs, shardFiles = map[string]struct{}{}, map[string]struct{}{}
		dirExists            bool
	)
	info, err := os.Stat(soulDir)
	switch {
	case err == nil && info.IsDir():
		dirExists = true
	case err == nil:
		return fmt.Errorf("soul path %s is not a directory", soulID)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if dirExists {
		if persona, _, err = readOptionalRegularFile(filepath.Join(soulDir, "persona.md"), companionSoulDocMaxBytes); err != nil {
			return fmt.Errorf("read persona.md: %w", err)
		}
		// A malformed preset file reads as no pick rather than failing the Soul.
		if raw, _, err := readOptionalRegularFile(filepath.Join(soulDir, companionSoulPresetFile), companionSoulPresetMaxBytes); err == nil {
			preset = sanitizeSoulPersonaPreset(raw)
		}
		if profile, _, err = readOptionalRegularFile(filepath.Join(soulDir, "profile.md"), companionSoulDocMaxBytes); err != nil {
			return fmt.Errorf("read profile.md: %w", err)
		}
		if entries, entryIDs, shardFiles, err = loadSoulShards(filepath.Join(soulDir, "log")); err != nil {
			return fmt.Errorf("load soul shards: %w", err)
		}
	}

	s.loaded = true
	s.loadedRoot = root
	s.loadedID = soulID
	s.dirExists = dirExists
	s.persona = persona
	s.preset = preset
	s.profile = profile
	s.entries = entries
	s.entryIDs = entryIDs
	s.shardFiles = shardFiles
	return nil
}

func (s *CompanionSoulStore) snapshotLocked(root, defaultRoot string, recentLimit int) SoulSnapshot {
	total := len(s.entries)
	start := 0
	if total > recentLimit {
		start = total - recentLimit
	}
	recent := make([]SoulShardEntry, 0, total-start)
	// Return newest-first for UI inspection.
	for i := total - 1; i >= start; i-- {
		recent = append(recent, cloneShardEntry(s.entries[i]))
	}
	snap := SoulSnapshot{
		SoulID:         s.loadedID,
		SoulDir:        root,
		DefaultSoulDir: defaultRoot,
		MachineID:      s.machineID,
		Souls:          []string{s.loadedID},
		Persona:        s.persona,
		PersonaPreset:  s.preset,
		Profile:        s.profile,
		RecentEntries:  recent,
		TotalEntries:   total,
		ShardCount:     len(s.shardFiles),
	}
	if strings.TrimSpace(s.persona) != "" {
		snap.PersonaSource = CompanionPersonaSourceUser
	} else {
		snap.PersonaInherited = true
	}
	return snap
}

// ListSouls returns valid soul folder IDs under root, always including activeID.
func (s *CompanionSoulStore) ListSouls(customDir, activeID string) ([]string, error) {
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return nil, err
	}
	resolvedActive, err := ResolveSoulID(activeID)
	if err != nil {
		resolvedActive = companionDefaultSoulID
	}
	seen := map[string]struct{}{resolvedActive: {}}
	if dirEntries, err := os.ReadDir(root); err == nil {
		for _, de := range dirEntries {
			if !de.IsDir() || de.Type()&fs.ModeSymlink != 0 {
				continue
			}
			name := de.Name()
			if companionSoulIDPattern.MatchString(name) {
				seen[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// CreateSoul initializes a new Soul directory (with its log/ folder) when it
// does not exist yet, on an explicit user request. It writes no persona.md:
// a new Soul inherits the pack or official persona until the user saves one.
func (s *CompanionSoulStore) CreateSoul(customDir, rawSoulID string) (SoulSnapshot, error) {
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return SoulSnapshot{}, err
	}
	defaultRoot, _ := s.DefaultRootDir()
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return SoulSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.ensureMachineIDLocked(); err != nil {
		return SoulSnapshot{}, err
	}
	if err := os.MkdirAll(filepath.Join(root, soulID, "log"), 0o700); err != nil {
		return SoulSnapshot{}, fmt.Errorf("create soul directory: %w", err)
	}
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID || !s.dirExists {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return SoulSnapshot{}, err
		}
	}
	return s.snapshotLocked(root, defaultRoot, companionSoulRecentDefault), nil
}

// AppendShardEntry appends ONE JSONL line to log/YYYY-MM-<machine-id>.jsonl
// without rewriting existing files or rescanning directories, and updates the
// RAM hot cache in-place.
func (s *CompanionSoulStore) AppendShardEntry(customDir, rawSoulID, kind, content string, tags []string) (SoulShardEntry, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return SoulShardEntry{}, errors.New("soul memory content is required")
	}
	if len(content) > companionSoulEntryMaxBytes {
		return SoulShardEntry{}, fmt.Errorf("soul memory content exceeds %d bytes", companionSoulEntryMaxBytes)
	}
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return SoulShardEntry{}, err
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return SoulShardEntry{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return SoulShardEntry{}, err
		}
	}
	machineID, err := s.ensureMachineIDLocked()
	if err != nil {
		return SoulShardEntry{}, err
	}
	now := s.now().UTC()
	entryID, err := newSoulEntryID(now)
	if err != nil {
		return SoulShardEntry{}, err
	}
	entry := SoulShardEntry{
		ID:        entryID,
		Timestamp: now.Format(time.RFC3339),
		MachineID: machineID,
		Kind:      strings.TrimSpace(kind),
		Content:   content,
		Tags:      normalizeCompanionTags(tags),
	}
	if entry.Kind == "" {
		entry.Kind = "note"
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return SoulShardEntry{}, err
	}
	line = append(line, '\n')

	shardName := fmt.Sprintf("%s-%s.jsonl", now.Format("2006-01"), machineID)
	logDir := filepath.Join(root, soulID, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return SoulShardEntry{}, err
	}
	shardPath := filepath.Join(logDir, shardName)
	f, err := os.OpenFile(shardPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return SoulShardEntry{}, err
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return SoulShardEntry{}, err
	}
	if err := f.Close(); err != nil {
		return SoulShardEntry{}, err
	}

	// Update RAM hot cache in-place (zero rescan).
	s.dirExists = true
	s.entries = append(s.entries, cloneShardEntry(entry))
	if s.entryIDs == nil {
		s.entryIDs = map[string]struct{}{}
	}
	s.entryIDs[entry.ID] = struct{}{}
	if s.shardFiles == nil {
		s.shardFiles = map[string]struct{}{}
	}
	s.shardFiles[shardName] = struct{}{}
	return entry, nil
}

// WriteUserDoc writes user-edited persona.md or profile.md atomically and
// updates the RAM cache in-place. Agent tools never call this method (D11).
// A blank persona.md is never written: it resets the persona to inherit the
// pack or official default (ResetPersona).
func (s *CompanionSoulStore) WriteUserDoc(customDir, rawSoulID string, docName SoulPendingTarget, content string) error {
	if docName != SoulPendingTargetPersona && docName != SoulPendingTargetProfile {
		return fmt.Errorf("invalid soul document %q", docName)
	}
	if !utf8.ValidString(content) {
		return errors.New("soul document must be valid UTF-8")
	}
	if len(content) > companionSoulDocMaxBytes {
		return fmt.Errorf("soul document exceeds %d bytes", companionSoulDocMaxBytes)
	}
	if docName == SoulPendingTargetPersona && strings.TrimSpace(content) == "" {
		_, err := s.ResetPersona(customDir, rawSoulID)
		return err
	}
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return err
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return err
		}
	}
	soulDir := filepath.Join(root, soulID)
	if err := os.MkdirAll(soulDir, 0o700); err != nil {
		return err
	}
	targetPath := filepath.Join(soulDir, string(docName))
	if err := writeAtomicPrivateFile(targetPath, []byte(content)); err != nil {
		return err
	}
	s.dirExists = true
	if docName == SoulPendingTargetPersona {
		s.persona = content
	} else {
		s.profile = content
	}
	return nil
}

// ResetPersona removes the user's persona.md override so the Soul inherits
// the live pack or official persona again, and reports whether a file was
// removed. Only the persona.md file is removed; profile.md and shards stay.
func (s *CompanionSoulStore) ResetPersona(customDir, rawSoulID string) (bool, error) {
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return false, err
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return false, err
		}
	}
	// An explicit user action: stat the file even when the cache holds the
	// Soul as absent, so a persona.md written behind the cache still resets.
	personaPath := filepath.Join(root, soulID, "persona.md")
	info, err := os.Lstat(personaPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.persona = ""
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("persona.md is not a regular file")
	}
	if err := os.Remove(personaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	s.persona = ""
	return true, nil
}

// sanitizeSoulPersonaPreset reads a persona-preset file's content: a
// well-formed non-default preset ID, else "" (no pick).
func sanitizeSoulPersonaPreset(raw string) string {
	id := strings.TrimSpace(raw)
	if id == CompanionPersonaPresetDefault || !companionPersonaPresetID.MatchString(id) {
		return ""
	}
	return id
}

// SetPersonaPreset records the official preset the user picked for a Soul.
// A non-default ID is written to persona-preset; "" or the default ID removes the
// file, so a Soul on the default preset keeps no file. The caller checks the
// ID against the embedded catalog; persona.md is left alone.
func (s *CompanionSoulStore) SetPersonaPreset(customDir, rawSoulID, presetID string) error {
	presetID = strings.TrimSpace(presetID)
	if presetID != "" && presetID != CompanionPersonaPresetDefault && sanitizeSoulPersonaPreset(presetID) == "" {
		return fmt.Errorf("invalid persona preset %q", presetID)
	}
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return err
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded || s.loadedRoot != root || s.loadedID != soulID {
		if err := s.loadColdLocked(root, soulID); err != nil {
			return err
		}
	}
	presetPath := filepath.Join(root, soulID, companionSoulPresetFile)
	if pick := sanitizeSoulPersonaPreset(presetID); pick != "" {
		if err := writeAtomicPrivateFile(presetPath, []byte(pick+"\n")); err != nil {
			return err
		}
		s.dirExists = true
		s.preset = pick
		return nil
	}
	// An explicit user action: stat even when the cache holds no pick, so a
	// file written behind the cache is still cleared.
	info, err := os.Lstat(presetPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.preset = ""
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("persona-preset is not a regular file")
	}
	if err := os.Remove(presetPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.preset = ""
	return nil
}

// ExportZIP packages only the Portable Soul files (manifest.json, persona.md,
// profile.md, and log/YYYY-MM-<machine-id>.jsonl) into an in-memory ZIP.
// Machine-local episodes DB and any unrelated files are strictly excluded.
func (s *CompanionSoulStore) ExportZIP(customDir, rawSoulID string) ([]byte, string, error) {
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return nil, "", err
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return nil, "", err
	}
	// Export reads only what exists; it never creates the Soul. An inherited
	// persona is not exported: it stays live with the pack or official default.
	if _, err := s.Load(customDir, soulID, 1); err != nil {
		return nil, "", err
	}

	soulDir := filepath.Join(root, soulID)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifestBytes, err := json.MarshalIndent(soulZipManifest{
		Schema:     companionSoulSchema,
		SoulID:     soulID,
		ExportedAt: s.now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := writeZipBytes(zw, "manifest.json", append(manifestBytes, '\n')); err != nil {
		return nil, "", err
	}

	for _, docName := range []string{"persona.md", "profile.md"} {
		data, exists, err := readOptionalRegularFile(filepath.Join(soulDir, docName), companionSoulDocMaxBytes)
		if err != nil {
			return nil, "", err
		}
		if exists {
			if err := writeZipBytes(zw, docName, []byte(data)); err != nil {
				return nil, "", err
			}
		}
	}

	logDir := filepath.Join(soulDir, "log")
	if dirEntries, err := os.ReadDir(logDir); err == nil {
		names := make([]string, 0, len(dirEntries))
		for _, de := range dirEntries {
			if de.IsDir() || de.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if companionShardFilePattern.MatchString(de.Name()) {
				names = append(names, de.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			raw, err := os.ReadFile(filepath.Join(logDir, name))
			if err != nil {
				return nil, "", err
			}
			if err := writeZipBytes(zw, "log/"+name, raw); err != nil {
				return nil, "", err
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), fmt.Sprintf("companion-soul-%s.zip", soulID), nil
}

// ImportZIP validates and imports a hardened Portable Soul ZIP archive into
// targetSoulID (or the archive manifest's soul_id when targetSoulID is empty).
// Rejects Zip Slip paths, symlinks, oversize payloads, and non-Soul files
// (such as .sqlite databases). Shards are merged without overwriting existing
// entries; persona.md/profile.md are preserved unless overwrite is true.
func (s *CompanionSoulStore) ImportZIP(customDir, rawTargetSoulID string, zipBytes []byte, overwrite bool) (SoulImportResult, error) {
	if len(zipBytes) == 0 {
		return SoulImportResult{}, errors.New("empty zip payload")
	}
	if int64(len(zipBytes)) > companionSoulZipMaxBytes {
		return SoulImportResult{}, fmt.Errorf("zip archive exceeds %d bytes", companionSoulZipMaxBytes)
	}
	root, err := s.ResolveRootDir(customDir)
	if err != nil {
		return SoulImportResult{}, err
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return SoulImportResult{}, fmt.Errorf("invalid zip archive: %w", err)
	}
	if len(zr.File) > companionSoulZipMaxFiles {
		return SoulImportResult{}, fmt.Errorf("zip archive exceeds %d file limit", companionSoulZipMaxFiles)
	}

	var (
		totalBytes    int64
		manifest      *soulZipManifest
		personaBytes  []byte
		hasPersona    bool
		profileBytes  []byte
		hasProfile    bool
		shardPayloads = map[string][]byte{}
	)

	for _, zf := range zr.File {
		if zf.Mode()&fs.ModeSymlink != 0 {
			return SoulImportResult{}, fmt.Errorf("zip archive must not contain symlinks: %q", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			continue
		}
		name, err := validateSoulZipEntryPath(zf.Name)
		if err != nil {
			return SoulImportResult{}, err
		}
		data, err := readBoundedZipFile(zf, companionSoulZipMaxFileSize, &totalBytes, companionSoulZipMaxBytes)
		if err != nil {
			return SoulImportResult{}, err
		}
		switch {
		case name == "manifest.json":
			var m soulZipManifest
			if err := json.Unmarshal(data, &m); err != nil {
				return SoulImportResult{}, fmt.Errorf("invalid manifest.json: %w", err)
			}
			if m.Schema != "" && m.Schema != companionSoulSchema {
				return SoulImportResult{}, fmt.Errorf("unsupported soul archive schema %q", m.Schema)
			}
			manifest = &m
		case name == "persona.md":
			if !utf8.Valid(data) || len(data) > companionSoulDocMaxBytes {
				return SoulImportResult{}, errors.New("invalid or oversize persona.md in archive")
			}
			personaBytes = data
			hasPersona = true
		case name == "profile.md":
			if !utf8.Valid(data) || len(data) > companionSoulDocMaxBytes {
				return SoulImportResult{}, errors.New("invalid or oversize profile.md in archive")
			}
			profileBytes = data
			hasProfile = true
		case strings.HasPrefix(name, "log/"):
			shardName := strings.TrimPrefix(name, "log/")
			if !companionShardFilePattern.MatchString(shardName) {
				return SoulImportResult{}, fmt.Errorf("invalid shard file name %q in archive", name)
			}
			shardPayloads[shardName] = data
		default:
			return SoulImportResult{}, fmt.Errorf("unexpected file %q in soul archive", name)
		}
	}

	if !hasPersona && !hasProfile && len(shardPayloads) == 0 {
		return SoulImportResult{}, errors.New("soul archive contains no persona, profile, or shard files")
	}

	targetIDRaw := strings.TrimSpace(rawTargetSoulID)
	if targetIDRaw == "" && manifest != nil && manifest.SoulID != "" {
		targetIDRaw = manifest.SoulID
	}
	soulID, err := ResolveSoulID(targetIDRaw)
	if err != nil {
		return SoulImportResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	soulDir := filepath.Join(root, soulID)
	logDir := filepath.Join(soulDir, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return SoulImportResult{}, err
	}

	var res SoulImportResult
	res.SoulID = soulID

	if hasPersona {
		personaPath := filepath.Join(soulDir, "persona.md")
		_, statErr := os.Stat(personaPath)
		if overwrite || errors.Is(statErr, os.ErrNotExist) {
			if err := writeAtomicPrivateFile(personaPath, personaBytes); err != nil {
				return SoulImportResult{}, err
			}
			res.UpdatedPersona = true
		}
	}
	if hasProfile {
		profilePath := filepath.Join(soulDir, "profile.md")
		_, statErr := os.Stat(profilePath)
		if overwrite || errors.Is(statErr, os.ErrNotExist) {
			if err := writeAtomicPrivateFile(profilePath, profileBytes); err != nil {
				return SoulImportResult{}, err
			}
			res.UpdatedProfile = true
		}
	}

	// Merge shard files cleanly: deduplicate by entry ID so importing from
	// another machine or re-importing the same archive is idempotent.
	existingEntries, existingIDs, _, err := loadSoulShards(logDir)
	if err != nil {
		return SoulImportResult{}, err
	}
	_ = existingEntries

	shardNames := make([]string, 0, len(shardPayloads))
	for name := range shardPayloads {
		shardNames = append(shardNames, name)
	}
	sort.Strings(shardNames)

	for _, name := range shardNames {
		parsed := parseShardBytes(shardPayloads[name])
		var toAppend []byte
		added := 0
		for _, entry := range parsed {
			if entry.ID != "" {
				if _, seen := existingIDs[entry.ID]; seen {
					continue
				}
				existingIDs[entry.ID] = struct{}{}
			}
			line, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			toAppend = append(toAppend, line...)
			toAppend = append(toAppend, '\n')
			added++
		}
		if added == 0 {
			continue
		}
		dest := filepath.Join(logDir, name)
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return SoulImportResult{}, err
		}
		_, wErr := f.Write(toAppend)
		cErr := f.Close()
		if wErr != nil {
			return SoulImportResult{}, wErr
		}
		if cErr != nil {
			return SoulImportResult{}, cErr
		}
		res.ImportedShards++
		res.MergedEntries += added
	}

	// Invalidate RAM cache if this soul was cached so the next read reflects the import.
	if s.loaded && s.loadedRoot == root && s.loadedID == soulID {
		s.loaded = false
	}
	return res, nil
}

func validateSoulZipEntryPath(raw string) (string, error) {
	if raw == "" || strings.Contains(raw, `\`) || strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("unsafe zip entry path %q", raw)
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("unsafe zip entry path %q", raw)
	}
	if cleaned == "manifest.json" || cleaned == "persona.md" || cleaned == "profile.md" {
		return cleaned, nil
	}
	if strings.HasPrefix(cleaned, "log/") && !strings.Contains(strings.TrimPrefix(cleaned, "log/"), "/") {
		return cleaned, nil
	}
	return "", fmt.Errorf("disallowed file %q in soul archive", raw)
}

func readBoundedZipFile(zf *zip.File, maxFile int64, total *int64, maxTotal int64) ([]byte, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxFile+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxFile {
		return nil, fmt.Errorf("zip entry %q exceeds %d bytes", zf.Name, maxFile)
	}
	*total += int64(len(data))
	if *total > maxTotal {
		return nil, fmt.Errorf("zip archive uncompressed size exceeds %d bytes", maxTotal)
	}
	return data, nil
}

func writeZipBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func loadSoulShards(logDir string) ([]SoulShardEntry, map[string]struct{}, map[string]struct{}, error) {
	entries := make([]SoulShardEntry, 0)
	entryIDs := make(map[string]struct{})
	shardFiles := make(map[string]struct{})

	dirEntries, err := os.ReadDir(logDir)
	if errors.Is(err, fs.ErrNotExist) {
		return entries, entryIDs, shardFiles, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	names := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() || de.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if companionShardFilePattern.MatchString(de.Name()) {
			names = append(names, de.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(logDir, name))
		if err != nil {
			continue
		}
		shardFiles[name] = struct{}{}
		for _, e := range parseShardBytes(raw) {
			if e.ID != "" {
				if _, dup := entryIDs[e.ID]; dup {
					continue
				}
				entryIDs[e.ID] = struct{}{}
			}
			entries = append(entries, e)
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Timestamp != entries[j].Timestamp {
			return entries[i].Timestamp < entries[j].Timestamp
		}
		if entries[i].MachineID != entries[j].MachineID {
			return entries[i].MachineID < entries[j].MachineID
		}
		return entries[i].ID < entries[j].ID
	})

	return entries, entryIDs, shardFiles, nil
}

func parseShardBytes(raw []byte) []SoulShardEntry {
	var out []SoulShardEntry
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 16<<10), companionSoulEntryMaxBytes*2)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e SoulShardEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if strings.TrimSpace(e.Content) == "" {
			continue
		}
		if e.Tags == nil {
			e.Tags = []string{}
		}
		out = append(out, e)
	}
	return out
}

func readOptionalRegularFile(p string, maxBytes int64) (string, bool, error) {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s is not a regular file", filepath.Base(p))
	}
	if info.Size() > maxBytes {
		return "", false, fmt.Errorf("%s exceeds %d bytes", filepath.Base(p), maxBytes)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

func writeAtomicPrivateFile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmpPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func newSoulEntryID(now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("se-%d-%s", now.UnixMilli(), hex.EncodeToString(b[:])), nil
}

func cloneShardEntry(e SoulShardEntry) SoulShardEntry {
	cp := e
	if len(e.Tags) > 0 {
		cp.Tags = append([]string(nil), e.Tags...)
	} else {
		cp.Tags = []string{}
	}
	return cp
}

// CompanionMemory coordinates the Portable Soul store, lazy Local Episodes
// SQLite FTS5 store, and deterministic 2-layer secret/PII scrubber.
type CompanionMemory struct {
	stateDir string
	homeDir  string
	now      func() time.Time
	logf     func(format string, args ...any)

	soul *CompanionSoulStore

	epMu          sync.Mutex
	episodes      *CompanionEpisodesStore
	epClosed      bool
	epAbsentKnown bool
	epIdleWait    time.Duration
}

// NewCompanionMemory creates the daemon's companion memory coordinator.
// The Local Episodes SQLite handle is opened lazily on first memory access and
// held until Close() during daemon drain.
func NewCompanionMemory(stateDir, homeDir string, now func() time.Time, logf func(string, ...any)) *CompanionMemory {
	if now == nil {
		now = time.Now
	}
	return &CompanionMemory{
		stateDir: stateDir,
		homeDir:  homeDir,
		now:      now,
		logf:     logf,
		soul:     NewCompanionSoulStore(stateDir, homeDir, now),
	}
}

// Soul returns the underlying Portable Soul store.
func (m *CompanionMemory) Soul() *CompanionSoulStore {
	if m == nil {
		return nil
	}
	return m.soul
}

// Episodes opens (lazily, once) and returns the held Local Episodes SQLite store.
func (m *CompanionMemory) Episodes() (*CompanionEpisodesStore, error) {
	if m == nil {
		return nil, errors.New("companion memory unavailable")
	}
	m.epMu.Lock()
	defer m.epMu.Unlock()
	if m.epClosed {
		return nil, errCompanionEpisodesClosed
	}
	if m.episodes != nil {
		return m.episodes, nil
	}
	store, err := OpenCompanionEpisodes(m.stateDir, CompanionEpisodesOptions{
		Now:            m.now,
		Logf:           m.logf,
		CheckpointIdle: m.epIdleWait,
	})
	if err != nil {
		return nil, err
	}
	m.episodes = store
	m.epAbsentKnown = false
	return store, nil
}

// EpisodesIfPresent returns the held Local Episodes store when already open or
// when companion-episodes.sqlite already exists on disk. Unlike Episodes(), it
// never creates a new SQLite file on a read-only wake/recall, and caches
// absence in RAM so repeated warm wakes incur zero disk stat calls.
func (m *CompanionMemory) EpisodesIfPresent() (*CompanionEpisodesStore, bool, error) {
	if m == nil {
		return nil, false, nil
	}
	m.epMu.Lock()
	if m.epClosed {
		m.epMu.Unlock()
		return nil, false, errCompanionEpisodesClosed
	}
	if m.episodes != nil {
		ep := m.episodes
		m.epMu.Unlock()
		return ep, true, nil
	}
	if m.epAbsentKnown || strings.TrimSpace(m.stateDir) == "" {
		m.epMu.Unlock()
		return nil, false, nil
	}
	dbPath := filepath.Join(m.stateDir, companionEpisodesDBName)
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			m.epAbsentKnown = true
			m.epMu.Unlock()
			return nil, false, nil
		}
		m.epMu.Unlock()
		return nil, false, err
	}
	m.epMu.Unlock()
	ep, err := m.Episodes()
	if err != nil {
		return nil, false, err
	}
	return ep, true, nil
}

// Close closes the held SQLite episodes handle (if opened) during daemon drain.
func (m *CompanionMemory) Close() error {
	if m == nil {
		return nil
	}
	m.epMu.Lock()
	m.epClosed = true
	ep := m.episodes
	m.episodes = nil
	m.epMu.Unlock()
	if ep != nil {
		return ep.Close()
	}
	return nil
}

// RememberSoulInput describes a candidate Portable Soul shard write.
type RememberSoulInput struct {
	SoulDir        string
	SoulID         string
	Kind           string
	Content        string
	Tags           []string
	ActiveProjects []string
	ProjectKey     string
	Source         string
}

// RememberSoulResult reports the guardrail outcome for RememberSoul.
type RememberSoulResult struct {
	Status   string            `json:"status"` // "saved" | "held_pending" | "downgraded_episode" | "rejected_secret"
	Entry    *SoulShardEntry   `json:"entry,omitempty"`
	Pending  *SoulPendingItem  `json:"pending,omitempty"`
	Episode  *CompanionEpisode `json:"episode,omitempty"`
	Findings []ScrubFinding    `json:"findings,omitempty"`
}

// RememberSoul runs the deterministic 2-layer scrubber before persisting:
//   - secret-like tokens/keys -> rejected outright and never persisted
//   - paths/emails/URLs -> held in soul_pending approval queue
//   - ticket stems / active project names -> downgraded to Local Episodes
//   - clean -> appended as a single JSONL line to log/YYYY-MM-<machine-id>.jsonl
func (m *CompanionMemory) RememberSoul(ctx context.Context, in RememberSoulInput) (RememberSoulResult, error) {
	scrub := ScrubSoulContent(in.Content, in.ActiveProjects)
	switch scrub.Action {
	case ScrubRejectSecret:
		return RememberSoulResult{
			Status:   "rejected_secret",
			Findings: scrub.Findings,
		}, nil
	case ScrubHoldPending:
		ep, err := m.Episodes()
		if err != nil {
			return RememberSoulResult{}, err
		}
		soulID, err := ResolveSoulID(in.SoulID)
		if err != nil {
			return RememberSoulResult{}, err
		}
		pending, err := ep.AddPending(ctx, SoulPendingInput{
			SoulID:   soulID,
			Target:   SoulPendingTargetShard,
			Category: in.Kind,
			Content:  in.Content,
			Findings: scrub.Findings,
			Source:   in.Source,
		})
		if err != nil {
			return RememberSoulResult{}, err
		}
		return RememberSoulResult{
			Status:   "held_pending",
			Pending:  &pending,
			Findings: scrub.Findings,
		}, nil
	case ScrubDowngradeEpisode:
		ep, err := m.Episodes()
		if err != nil {
			return RememberSoulResult{}, err
		}
		proj := strings.TrimSpace(in.ProjectKey)
		if proj == "" {
			proj = scrub.MatchedProject
		}
		episode, err := ep.AddEpisode(ctx, CompanionEpisodeInput{
			ProjectKey: proj,
			Summary:    in.Content,
			Tags:       append(append([]string(nil), in.Tags...), "downgraded-from-soul"),
			Source:     in.Source,
		})
		if err != nil {
			return RememberSoulResult{}, err
		}
		return RememberSoulResult{
			Status:   "downgraded_episode",
			Episode:  &episode,
			Findings: scrub.Findings,
		}, nil
	default:
		entry, err := m.soul.AppendShardEntry(in.SoulDir, in.SoulID, in.Kind, in.Content, in.Tags)
		if err != nil {
			return RememberSoulResult{}, err
		}
		return RememberSoulResult{
			Status: "saved",
			Entry:  &entry,
		}, nil
	}
}

// SaveUserDocResult reports whether a Settings drawer edit of persona.md or
// profile.md was saved directly or held in the approval queue by the scrubber.
type SaveUserDocResult struct {
	Status   string           `json:"status"` // "saved" | "held_pending"
	Pending  *SoulPendingItem `json:"pending,omitempty"`
	Findings []ScrubFinding   `json:"findings,omitempty"`
}

// SaveUserDoc scrubs a Settings drawer edit of persona.md or profile.md. Per
// ticket constraints, user edits through Settings are held in the approval
// queue on any scrubber hit rather than rejected outright.
func (m *CompanionMemory) SaveUserDoc(ctx context.Context, soulDir, rawSoulID string, target SoulPendingTarget, content string, activeProjects []string) (SaveUserDocResult, error) {
	if target != SoulPendingTargetPersona && target != SoulPendingTargetProfile {
		return SaveUserDocResult{}, fmt.Errorf("invalid soul document target %q", target)
	}
	if !utf8.ValidString(content) {
		return SaveUserDocResult{}, errors.New("soul document must be valid UTF-8")
	}
	if len(content) > companionSoulDocMaxBytes {
		return SaveUserDocResult{}, fmt.Errorf("soul document exceeds %d bytes", companionSoulDocMaxBytes)
	}
	soulID, err := ResolveSoulID(rawSoulID)
	if err != nil {
		return SaveUserDocResult{}, err
	}
	scrub := ScrubSoulContent(content, activeProjects)
	if scrub.Action != ScrubAllow {
		ep, err := m.Episodes()
		if err != nil {
			return SaveUserDocResult{}, err
		}
		pending, err := ep.AddPending(ctx, SoulPendingInput{
			SoulID:   soulID,
			Target:   target,
			Category: string(target),
			Content:  content,
			Findings: scrub.Findings,
			Source:   "settings",
		})
		if err != nil {
			return SaveUserDocResult{}, err
		}
		return SaveUserDocResult{
			Status:   "held_pending",
			Pending:  &pending,
			Findings: scrub.Findings,
		}, nil
	}
	if err := m.soul.WriteUserDoc(soulDir, soulID, target, content); err != nil {
		return SaveUserDocResult{}, err
	}
	return SaveUserDocResult{Status: "saved"}, nil
}

// ApprovePending approves a scrubber-held item (optionally with edited text),
// commits it to its target (persona.md, profile.md, or a JSONL shard), and
// removes it from soul_pending.
func (m *CompanionMemory) ApprovePending(ctx context.Context, soulDir string, id int64, editedContent *string) (SoulPendingItem, error) {
	ep, err := m.Episodes()
	if err != nil {
		return SoulPendingItem{}, err
	}
	item, found, err := ep.GetPending(ctx, id)
	if err != nil {
		return SoulPendingItem{}, err
	}
	if !found {
		return SoulPendingItem{}, os.ErrNotExist
	}
	content := item.Content
	if editedContent != nil {
		content = *editedContent
	}
	switch item.Target {
	case SoulPendingTargetPersona, SoulPendingTargetProfile:
		if err := m.soul.WriteUserDoc(soulDir, item.SoulID, item.Target, content); err != nil {
			return SoulPendingItem{}, err
		}
	case SoulPendingTargetShard:
		if _, err := m.soul.AppendShardEntry(soulDir, item.SoulID, item.Category, content, nil); err != nil {
			return SoulPendingItem{}, err
		}
	default:
		return SoulPendingItem{}, fmt.Errorf("unknown pending target %q", item.Target)
	}
	if _, err := ep.DeletePending(ctx, id); err != nil {
		return SoulPendingItem{}, err
	}
	item.Content = content
	return item, nil
}
