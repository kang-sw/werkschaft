package wsdashboard

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

const (
	companionDefaultID    = "default"
	companionManifestName = "pack.json"
	companionMaxPacks     = 32
	companionManifestCap  = 512 << 10
	companionAssetCap     = 2 << 20
	companionPackCap      = 16 << 20
	companionCacheCap     = 64 << 20
)

//go:embed assets/companion/default/*
var companionDefaultAssets embed.FS

// CompanionPackStore loads one immutable catalog on demand. Reloads are
// serialized with first use, and readers of a loaded catalog never wait on disk.
type CompanionPackStore struct {
	active atomic.Pointer[companionSnapshot]
	scanMu sync.Mutex
	open   companionPackOpen // nil selects the disk implementation
}

type companionPackOpen func(parent companionPackFile, name string, directory bool) (companionPackFile, error)

type companionPackFile interface {
	io.ReadCloser
	Stat() (fs.FileInfo, error)
	ReadDir(int) ([]fs.DirEntry, error)
	Fd() uintptr
	Name() string
}

type companionPackInfo struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Schema    string             `json:"schema"`
	Source    string             `json:"source"`
	Rev       string             `json:"rev"`
	Presets   []string           `json:"presets"`
	AutoMatch companionAutoMatch `json:"autoMatch"`
}

type companionPackError struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type companionPack struct {
	info     companionPackInfo
	manifest []byte
	assets   map[string][]byte
	size     int
}

type companionSnapshot struct {
	packs  []companionPackInfo
	errors []companionPackError
	byID   map[string]*companionPack
}

type companionAutoResolution struct {
	PackID    string `json:"packId"`
	MatchedBy string `json:"matchedBy"`
}

type companionCatalog struct {
	Packs        []companionPackInfo     `json:"packs"`
	Errors       []companionPackError    `json:"errors"`
	ResolvedAuto companionAutoResolution `json:"resolvedAuto"`
}

func (s *CompanionPackStore) snapshot(opts wsconfig.Options) *companionSnapshot {
	if current := s.active.Load(); current != nil {
		return current
	}
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if current := s.active.Load(); current != nil {
		return current
	}
	return s.load(opts)
}

func (s *CompanionPackStore) reload(opts wsconfig.Options) *companionSnapshot {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	return s.load(opts)
}

// load is called with scanMu held. It finishes the full snapshot before the
// atomic swap. The embedded pack is reused across local reloads.
func (s *CompanionPackStore) load(opts wsconfig.Options) *companionSnapshot {
	next := &companionSnapshot{
		packs:  []companionPackInfo{},
		errors: []companionPackError{},
		byID:   make(map[string]*companionPack),
	}
	var embedded *companionPack
	if current := s.active.Load(); current != nil {
		embedded = current.byID[companionDefaultID]
	}
	if embedded == nil {
		pack, path, err := loadCompanionPack(companionDefaultID, "embedded", companionPackCap, companionEmbeddedFile)
		if err != nil {
			next.addError(companionDefaultID, path, err.Error())
		}
		embedded = pack
	}
	if embedded != nil {
		next.add(embedded)
	}
	s.scanLocal(next, opts)
	s.active.Store(next)
	return next
}

func companionEmbeddedFile(name string, limit int) ([]byte, error) {
	data, err := companionDefaultAssets.ReadFile("assets/companion/default/" + name)
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded file")
	}
	if len(data) > limit {
		return nil, errCompanionFileLimit
	}
	return data, nil
}

func (s *CompanionPackStore) scanLocal(snapshot *companionSnapshot, opts wsconfig.Options) {
	configPath, err := wsconfig.GlobalPath(opts)
	if err != nil {
		snapshot.addError("", ".", "cannot resolve the companion pack directory")
		return
	}
	open := s.open
	if open == nil {
		open = openCompanionPackEntry
	}
	root, err := open(nil, filepath.Join(filepath.Dir(configPath), "companions"), true)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		snapshot.addError("", ".", "cannot open the companion pack directory")
		return
	}
	defer root.Close()
	if !companionDirectory(root) {
		snapshot.addError("", ".", "companion pack directory must be a plain directory")
		return
	}
	entries, err := root.ReadDir(-1)
	if err != nil {
		snapshot.addError("", ".", "cannot list the companion pack directory")
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var visited, cached int
	for _, entry := range entries {
		id := entry.Name()
		if entry.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			snapshot.addError(id, id, "pack directories must be plain directories")
			continue
		}
		if !entry.IsDir() {
			continue
		}
		switch {
		case id == companionDefaultID || id == "auto":
			snapshot.addError(id, id, "pack ID is reserved")
		case !companionPackID.MatchString(id):
			snapshot.addError(id, id, "invalid pack ID")
		case visited >= companionMaxPacks:
			snapshot.addError(id, id, "local pack directory limit exceeded")
		default:
			visited++
			pack, name, err := loadCompanionLocal(open, root, id, companionCacheCap-cached)
			if err != nil {
				snapshot.addError(id, name, err.Error())
				continue
			}
			cached += pack.size
			snapshot.add(pack)
		}
	}
}

func loadCompanionLocal(open companionPackOpen, root companionPackFile, id string, budget int) (*companionPack, string, error) {
	dir, err := open(root, id, true)
	if err != nil {
		return nil, id, fmt.Errorf("cannot open pack directory")
	}
	defer dir.Close()
	if !companionDirectory(dir) {
		return nil, id, fmt.Errorf("pack directory must be a plain directory")
	}
	pack, name, err := loadCompanionPack(id, "local", budget, func(name string, limit int) ([]byte, error) {
		return readCompanionLocalFile(open, dir, name, limit)
	})
	if err != nil {
		name = id + "/" + name
	}
	return pack, name, err
}

func companionDirectory(f companionPackFile) bool {
	info, err := f.Stat()
	return err == nil && info.Mode().Type() == fs.ModeDir
}

var errCompanionFileLimit = errors.New("file byte limit exceeded")

func readCompanionLocalFile(open companionPackOpen, parent companionPackFile, name string, limit int) ([]byte, error) {
	file, err := open(parent, name, false)
	if err != nil {
		return nil, fmt.Errorf("cannot open file (only plain files are allowed)")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected a regular file")
	}
	if info.Size() > int64(limit) {
		return nil, errCompanionFileLimit
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read file")
	}
	if len(raw) > limit {
		return nil, errCompanionFileLimit
	}
	return raw, nil
}

func loadCompanionPack(id, source string, budget int, read func(string, int) ([]byte, error)) (*companionPack, string, error) {
	readWithinBudget := func(name string, fileCap, used int) ([]byte, error) {
		cacheLeft, packLeft := budget-used, companionPackCap-used
		data, err := read(name, min(fileCap, cacheLeft, packLeft))
		if errors.Is(err, errCompanionFileLimit) {
			switch {
			case cacheLeft < fileCap && cacheLeft < packLeft:
				err = fmt.Errorf("total local pack cache byte limit exceeded")
			case packLeft < fileCap:
				err = fmt.Errorf("pack byte limit exceeded")
			}
		}
		return data, err
	}

	raw, err := readWithinBudget(companionManifestName, companionManifestCap, 0)
	if err != nil {
		return nil, companionManifestName, err
	}
	manifest, files, err := validateCompanionManifest(raw, id)
	if err != nil {
		return nil, companionManifestName, err
	}
	pack := &companionPack{manifest: raw, assets: make(map[string][]byte, len(files)), size: len(raw)}
	revision := sha256.New()
	hashCompanionPart(revision, raw)
	for _, name := range files {
		data, err := readWithinBudget(name, companionAssetCap, pack.size)
		if err != nil {
			return nil, name, err
		}
		pack.size += len(data)
		pack.assets[name] = data
		hashCompanionPart(revision, []byte(name))
		hashCompanionPart(revision, data)
	}

	auto := manifest.AutoMatch
	if auto.Harnesses == nil {
		auto.Harnesses = []string{}
	}
	if auto.Models == nil {
		auto.Models = []string{}
	}
	pack.info = companionPackInfo{
		ID: id, Name: manifest.Name, Schema: manifest.Schema, Source: source,
		Rev: hex.EncodeToString(revision.Sum(nil)[:16]), Presets: sortedCompanionKeys(manifest.Presets), AutoMatch: auto,
	}
	return pack, "", nil
}

func hashCompanionPart(h hash.Hash, data []byte) {
	// Length framing prevents concatenation ambiguities across files.
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(data)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(data)
}

func (s *companionSnapshot) add(pack *companionPack) {
	s.packs = append(s.packs, pack.info)
	s.byID[pack.info.ID] = pack
}

func (s *companionSnapshot) addError(id, path, message string) {
	s.errors = append(s.errors, companionPackError{ID: id, Path: path, Message: message})
}

func (s *companionSnapshot) catalog(harness, model string) companionCatalog {
	result := companionCatalog{Packs: s.packs, Errors: s.errors,
		ResolvedAuto: companionAutoResolution{PackID: companionDefaultID, MatchedBy: "default"}}
	if harness == "" && model == "" {
		return result
	}
	model = strings.ToLower(model)
	best := 0
	// Local entries were visited in ascending ID order. Equal scores leave
	// the earlier entry in place.
	for _, pack := range s.packs {
		if pack.Source != "local" {
			continue
		}
		var score int
		for _, tag := range pack.AutoMatch.Harnesses {
			if strings.EqualFold(tag, harness) {
				score++
				break
			}
		}
		for _, tag := range pack.AutoMatch.Models {
			if strings.Contains(model, strings.ToLower(tag)) {
				score += 2
				break
			}
		}
		if score > best {
			best = score
			matchedBy := "harness"
			if score >= 2 {
				matchedBy = "model"
			}
			result.ResolvedAuto = companionAutoResolution{PackID: pack.ID, MatchedBy: matchedBy}
		}
	}
	return result
}
