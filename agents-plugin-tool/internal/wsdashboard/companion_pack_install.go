package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

const (
	companionSidecarName  = ".ws-install.json"
	companionSidecarCap   = 16 << 10
	companionStagingName  = ".companion-staging"
	companionPacksDirName = "companions"
)

var (
	companionCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	companionRevision  = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// companionSidecar is the install provenance kept beside an installed pack
// (<config-home>/companions/<id>/.ws-install.json, a plain Type 5 file). The
// scan reads it only during a scan or reload and exposes it as the catalog
// entry's origin; a malformed sidecar is ignored and the pack still loads.
type companionSidecar struct {
	Source        string `json:"source"`
	Kind          string `json:"kind"`
	Ref           string `json:"ref,omitempty"`
	Commit        string `json:"commit,omitempty"`
	ArchiveSHA256 string `json:"archiveSha256,omitempty"`
	PackSHA256    string `json:"packSha256,omitempty"`
	Version       string `json:"version"`
	InstalledAt   string `json:"installedAt"`
	Official      bool   `json:"official"`
}

func readCompanionSidecar(open companionPackOpen, dir companionPackFile) *companionSidecar {
	raw, err := readCompanionLocalFile(open, dir, companionSidecarName, companionSidecarCap)
	if err != nil {
		return nil
	}
	return parseCompanionSidecar(raw)
}

func parseCompanionSidecar(raw []byte) *companionSidecar {
	if !utf8.Valid(raw) {
		return nil
	}
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return nil
	}
	var s companionSidecar
	if obj.fields(map[string]any{
		"source": &s.Source, "kind": &s.Kind, "ref": &s.Ref, "commit": &s.Commit,
		"archiveSha256": &s.ArchiveSHA256, "packSha256": &s.PackSHA256, "version": &s.Version,
		"installedAt": &s.InstalledAt, "official": &s.Official,
	}) != nil {
		return nil
	}
	switch s.Kind {
	case "git", "zip", "local":
	default:
		return nil
	}
	if companionText("source", s.Source, companionURLMax, true) != nil || companionText("ref", s.Ref, 256, false) != nil ||
		companionText("version", s.Version, 32, false) != nil || companionText("installedAt", s.InstalledAt, 64, false) != nil {
		return nil
	}
	if (s.Commit != "" && !companionCommitSHA.MatchString(s.Commit)) ||
		(s.ArchiveSHA256 != "" && !companionSHA256.MatchString(s.ArchiveSHA256)) ||
		(s.PackSHA256 != "" && !companionRevision.MatchString(s.PackSHA256)) {
		return nil
	}
	return &s
}

// companionInstallOrigin is where an install operation's archive came from.
// Official is set only for the embedded official source.
type companionInstallOrigin struct {
	Source        string
	Kind          string // git | zip | local
	Ref           string
	Commit        string
	ArchiveSHA256 string // the source zip's hash (zip and local kinds)
	Official      bool
}

// companionInstallPlan is one install, update, or dry run over an archive
// already opened and checked at the archive level.
type companionInstallPlan struct {
	archive *companionArchive
	market  companionMarketplace
	origin  companionInstallOrigin
	// external fetches and verifies the zip of a url entry; nil refuses url
	// entries.
	external func(entry companionMarketplaceEntry, inflate *companionInflate) (*companionArchive, error)
}

// Listing status of a marketplace pack against the local catalog.
const (
	companionStatusAvailable = "available" // not installed
	companionStatusInstalled = "installed" // installed from this source, unchanged
	companionStatusUpdate    = "update"    // installed from this source, content changed
	companionStatusOther     = "other"     // installed from another source; an install replaces it
	companionStatusManual    = "manual"    // a hand-made directory holds the ID; install refused
	companionStatusBuiltin   = "builtin"   // the embedded default pack in the official listing
)

type companionListing struct {
	Source   string                     `json:"source"`
	Kind     string                     `json:"kind"`
	Official bool                       `json:"official"`
	Commit   string                     `json:"commit,omitempty"`
	Name     string                     `json:"name"`
	Owner    *companionMarketplaceOwner `json:"owner,omitempty"`
	Packs    []companionListingPack     `json:"packs"`
	Errors   []companionPackError       `json:"errors"`
}

type companionListingPack struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	Author      string              `json:"author,omitempty"`
	License     string              `json:"license,omitempty"`
	Description string              `json:"description,omitempty"`
	URL         string              `json:"url,omitempty"`
	Rev         string              `json:"rev,omitempty"`
	Status      string              `json:"status"`
	Installed   bool                `json:"installed,omitempty"` // set by an install that wrote this pack
	Error       *companionPackError `json:"error,omitempty"`
}

// companionInstallResult is the outcome of an install operation: the
// listing with per-pack status and errors, and the catalog after a write.
type companionInstallResult struct {
	Listing companionListing  `json:"listing"`
	Catalog *companionCatalog `json:"catalog,omitempty"`
}

// companionInstallError is an operation-level failure with an HTTP status.
type companionInstallError struct {
	status  int
	message string
}

func (e *companionInstallError) Error() string { return e.message }

func companionRefuse(status int, format string, args ...any) error {
	return &companionInstallError{status: status, message: fmt.Sprintf(format, args...)}
}

type companionInstallPaths struct{ home, packs, staging string }

func companionPaths(opts wsconfig.Options) (companionInstallPaths, error) {
	configPath, err := wsconfig.GlobalPath(opts)
	if err != nil {
		return companionInstallPaths{}, companionRefuse(http.StatusInternalServerError, "cannot resolve the companion pack directory")
	}
	home := filepath.Dir(configPath)
	return companionInstallPaths{home: home, packs: filepath.Join(home, companionPacksDirName),
		staging: filepath.Join(home, companionStagingName)}, nil
}

// fsop records one installer file operation for the per-action counts.
func (s *CompanionPackStore) fsop(op string) {
	if s.trace != nil {
		s.trace(op)
	}
}

// swapAndLoad runs swap and the following scan under one scanMu hold, so a
// lazy first scan or a user reload never sees a pack missing mid-swap, and
// on Windows the renames never meet the scan's pinned handles. load is the
// locked variant of reload.
func (s *CompanionPackStore) swapAndLoad(opts wsconfig.Options, swap func()) *companionSnapshot {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	swap()
	return s.load(opts)
}

// prepareStaging removes staging runs a crash or a failed removal left
// behind, then creates a fresh run directory. Callers hold installMu.
func (s *CompanionPackStore) prepareStaging(paths companionInstallPaths) (string, error) {
	s.fsop("lstat")
	info, err := os.Lstat(paths.staging)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.fsop("mkdir")
		if err := os.MkdirAll(paths.staging, 0o755); err != nil {
			return "", companionRefuse(http.StatusInternalServerError, "cannot create the companion staging directory")
		}
	case err != nil || info.Mode().Type() != fs.ModeDir:
		return "", companionRefuse(http.StatusConflict, "the companion staging path must be a plain directory")
	default:
		s.fsop("readdir")
		entries, err := os.ReadDir(paths.staging)
		if err != nil {
			return "", companionRefuse(http.StatusInternalServerError, "cannot list the companion staging directory")
		}
		for _, entry := range entries {
			s.fsop("remove")
			_ = os.RemoveAll(filepath.Join(paths.staging, entry.Name()))
		}
	}
	s.fsop("mkdir")
	run, err := os.MkdirTemp(paths.staging, "run-")
	if err != nil {
		return "", companionRefuse(http.StatusInternalServerError, "cannot create a companion staging directory")
	}
	return run, nil
}

// companionLocalDirs lists the pack root once: the directories the scan
// would visit (valid, unreserved IDs; symlinks excluded) and, per entry name,
// whether it is a plain directory.
type companionLocalDirs struct {
	visited int
	plain   map[string]bool
}

func (s *CompanionPackStore) localDirs(paths companionInstallPaths) (companionLocalDirs, error) {
	dirs := companionLocalDirs{plain: map[string]bool{}}
	s.fsop("readdir")
	entries, err := os.ReadDir(paths.packs)
	if errors.Is(err, fs.ErrNotExist) {
		return dirs, nil
	}
	if err != nil {
		return dirs, companionRefuse(http.StatusInternalServerError, "cannot list the companion pack directory")
	}
	for _, entry := range entries {
		id := entry.Name()
		plain := entry.Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 && entry.IsDir()
		dirs.plain[id] = plain
		if plain && id != companionDefaultID && id != "auto" && companionPackID.MatchString(id) {
			dirs.visited++
		}
	}
	return dirs, nil
}

// hasSidecar reports whether an existing pack directory carries an install
// sidecar as a regular file.
func (s *CompanionPackStore) hasSidecar(paths companionInstallPaths, id string) bool {
	s.fsop("lstat")
	info, err := os.Lstat(filepath.Join(paths.packs, id, companionSidecarName))
	return err == nil && info.Mode().IsRegular()
}

// install validates the selected entries of plan (nil ids selects all) and,
// unless dryRun, stages, swaps, and loads the packs that passed. Installs,
// updates, uninstalls, and dry runs are serialized by installMu; budgets and
// guards read the store's snapshot and the pack root inside that section.
func (s *CompanionPackStore) install(opts wsconfig.Options, plan companionInstallPlan, ids []string, dryRun bool) (companionInstallResult, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()

	listing := companionListing{Source: plan.origin.Source, Kind: plan.origin.Kind, Official: plan.origin.Official,
		Commit: plan.origin.Commit, Name: plan.market.Name, Owner: plan.market.Owner,
		Packs: []companionListingPack{}, Errors: []companionPackError{}}
	result := companionInstallResult{Listing: listing}
	paths, err := companionPaths(opts)
	if err != nil {
		return result, err
	}
	var wanted map[string]bool
	if ids != nil {
		wanted = make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}
	}
	snapshot := s.snapshot(opts)
	dirs, err := s.localDirs(paths)
	if err != nil {
		return result, err
	}

	// sizes holds the cache bytes of every loaded local pack; each prepared
	// pack replaces its own ID's entry, so its budget is the cache cap minus
	// every other local pack (later IDs included, which the scan would drop).
	sizes := map[string]int{}
	for id, pack := range snapshot.byID {
		if pack.info.Source == "local" {
			sizes[id] = pack.size
		}
	}
	type staged struct {
		id     string
		pack   *companionPack
		origin companionSidecar
		index  int
	}
	var ready []staged
	for _, entry := range plan.market.Packs {
		item := companionListingPack{ID: entry.ID, Name: entry.Name, Version: entry.Version, Author: entry.Author,
			License: entry.License, Description: entry.Description, URL: entry.URL, Status: companionStatusAvailable}
		if entry.Err != nil {
			fail := companionAsPackError(entry.Err, entry.ID)
			item.Error = &fail
			listing.Packs = append(listing.Packs, item)
			continue
		}
		installedPack, exists := snapshot.byID[entry.ID]
		switch {
		case exists && installedPack.info.Source == "local" && installedPack.info.Origin != nil:
			item.Status = companionStatusOther
			if companionSameSource(*installedPack.info.Origin, plan.origin) {
				item.Status = companionStatusInstalled
			}
		case exists || dirs.plain[entry.ID] || companionHasError(snapshot, entry.ID):
			item.Status = companionStatusManual
			if dirs.plain[entry.ID] && s.hasSidecar(paths, entry.ID) {
				item.Status = companionStatusOther
			}
		}
		// An unselected entry is listed only. A dry run lists a url entry
		// without downloading it: its status follows the entry's sha256, and
		// the pack zip is fetched and checked only by an install click.
		if (wanted != nil && !wanted[entry.ID]) || (dryRun && entry.URL != "") {
			if entry.URL != "" && item.Status == companionStatusInstalled && entry.SHA256 != installedPack.info.Origin.ArchiveSHA256 {
				item.Status = companionStatusUpdate
			}
			listing.Packs = append(listing.Packs, item)
			continue
		}
		pack, fail := s.preparePlanEntry(plan, entry, paths, dirs, sizes)
		if fail != nil {
			item.Error = fail
			listing.Packs = append(listing.Packs, item)
			continue
		}
		item.Rev = pack.info.Rev
		if item.Status == companionStatusInstalled {
			origin := installedPack.info.Origin
			if (entry.URL != "" && entry.SHA256 != origin.ArchiveSHA256) || (entry.URL == "" && pack.info.Rev != origin.PackSHA256) {
				item.Status = companionStatusUpdate
			}
		}
		// A write counts each accepted pack against the next one; a dry run
		// judges every pack alone against the installed packs.
		if !dryRun {
			if _, existed := dirs.plain[entry.ID]; !existed {
				dirs.visited++
				dirs.plain[entry.ID] = true
			}
			sizes[entry.ID] = pack.size
		}
		sidecar := companionSidecar{Source: plan.origin.Source, Kind: plan.origin.Kind, Ref: plan.origin.Ref,
			Commit: plan.origin.Commit, ArchiveSHA256: plan.origin.ArchiveSHA256, PackSHA256: pack.info.Rev,
			Version: entry.Version, Official: plan.origin.Official}
		if entry.URL != "" {
			sidecar.ArchiveSHA256 = entry.SHA256
		}
		ready = append(ready, staged{id: entry.ID, pack: pack, origin: sidecar, index: len(listing.Packs)})
		listing.Packs = append(listing.Packs, item)
	}
	result.Listing = listing
	if dryRun || len(ready) == 0 {
		return result, nil
	}

	run, err := s.prepareStaging(paths)
	if err != nil {
		return result, err
	}
	defer func() {
		s.fsop("remove")
		_ = os.RemoveAll(run)
	}()
	now := time.Now().UTC().Format(time.RFC3339)
	var written []staged
	for _, item := range ready {
		item.origin.InstalledAt = now
		if err := s.writeStaged(filepath.Join(run, item.id), item.pack, item.origin); err != nil {
			fail := companionPackError{ID: item.id, Message: err.Error()}
			result.Listing.Packs[item.index].Error = &fail
			continue
		}
		written = append(written, item)
	}
	if len(written) == 0 {
		return result, nil
	}
	s.fsop("mkdir")
	if err := os.MkdirAll(paths.packs, 0o755); err != nil {
		return result, companionRefuse(http.StatusInternalServerError, "cannot create the companion pack directory")
	}
	failed := map[string]string{}
	snapshot = s.swapAndLoad(opts, func() {
		for _, item := range written {
			if err := s.swapIn(paths, run, item.id); err != nil {
				failed[item.id] = err.Error()
			}
		}
	})
	for _, item := range written {
		message, bad := failed[item.id]
		if !bad {
			if loaded := snapshot.byID[item.id]; loaded == nil || loaded.info.Rev != item.pack.info.Rev {
				message, bad = "the installed pack did not load", true
			}
		}
		if bad {
			fail := companionPackError{ID: item.id, Message: message}
			result.Listing.Packs[item.index].Error = &fail
			continue
		}
		result.Listing.Packs[item.index].Installed = true
		result.Listing.Packs[item.index].Status = companionStatusInstalled
	}
	catalog := snapshot.catalog("", "")
	result.Catalog = &catalog
	return result, nil
}

// preparePlanEntry applies the guards and the full pack validation to one
// entry. It never writes.
func (s *CompanionPackStore) preparePlanEntry(plan companionInstallPlan, entry companionMarketplaceEntry,
	paths companionInstallPaths, dirs companionLocalDirs, sizes map[string]int) (*companionPack, *companionPackError) {
	fail := func(err error) (*companionPack, *companionPackError) {
		e := companionAsPackError(err, entry.ID)
		return nil, &e
	}
	if plain, exists := dirs.plain[entry.ID]; exists {
		if !plain {
			return fail(errors.New("a file or link that is not a pack directory already uses this pack ID"))
		}
		if !s.hasSidecar(paths, entry.ID) {
			return fail(errors.New("a hand-made pack already uses this pack ID; remove it yourself to install this one"))
		}
	} else if dirs.visited >= companionMaxPacks {
		return fail(fmt.Errorf("the local pack limit of %d is reached", companionMaxPacks))
	}
	budget := companionCacheCap
	for id, size := range sizes {
		if id != entry.ID {
			budget -= size
		}
	}
	if budget <= 0 {
		return fail(errors.New("total local pack cache byte limit exceeded"))
	}
	archive, prefix := plan.archive, plan.market.prefix+entry.Path+"/"
	if entry.URL != "" {
		if plan.external == nil {
			return fail(errors.New("external pack URLs need a network source"))
		}
		var err error
		if archive, err = plan.external(entry, plan.archive.inflate); err != nil {
			return fail(err)
		}
		var ok bool
		if prefix, ok = archive.root(companionManifestName); !ok {
			return fail(errors.New("the pack zip must hold pack.json at its root or in its single top-level directory"))
		}
	} else if _, ok := plan.archive.files[prefix+companionManifestName]; !ok {
		return fail(errors.New("path does not hold pack.json"))
	}
	files, err := archive.pack(entry.ID, prefix)
	if err != nil {
		return fail(err)
	}
	pack, err := archive.prepare(entry.ID, files, budget)
	if err != nil {
		return fail(err)
	}
	return pack, nil
}

func companionAsPackError(err error, id string) companionPackError {
	var fail companionPackError
	if errors.As(err, &fail) {
		if fail.ID == "" {
			fail.ID = id
		}
		return fail
	}
	return companionPackError{ID: id, Message: err.Error()}
}

func companionSameSource(origin companionSidecar, plan companionInstallOrigin) bool {
	if origin.Kind != plan.Kind {
		return false
	}
	return plan.Kind == "local" || origin.Source == plan.Source
}

func companionHasError(snapshot *companionSnapshot, id string) bool {
	for _, e := range snapshot.errors {
		if e.ID == id {
			return true
		}
	}
	return false
}

// writeStaged writes the pack into its staging directory. Files are named
// only from the validated manifest, never from zip paths: pack.json keeps
// its original bytes because the store serves raw bytes.
func (s *CompanionPackStore) writeStaged(dir string, pack *companionPack, origin companionSidecar) error {
	s.fsop("mkdir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		return errors.New("cannot create the staging directory")
	}
	sidecar, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{companionManifestName: pack.manifest, companionSidecarName: append(sidecar, '\n')}
	for name, data := range pack.assets {
		files[name] = data
	}
	for _, name := range sortedCompanionKeys(files) {
		if err := s.writeNew(filepath.Join(dir, name), files[name]); err != nil {
			return errors.New("cannot write the staged pack")
		}
	}
	return nil
}

func (s *CompanionPackStore) writeNew(name string, data []byte) error {
	s.fsop("create")
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	s.fsop("write")
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// swapIn moves a staged pack into place. An existing directory is first
// renamed into the staging run, and restored when the second rename fails.
// The caller holds scanMu through swapAndLoad.
func (s *CompanionPackStore) swapIn(paths companionInstallPaths, run, id string) error {
	// A pack ID never starts with a dot, so the moved-aside name cannot meet
	// another staged pack.
	target := filepath.Join(paths.packs, id)
	old := filepath.Join(run, "."+id)
	s.fsop("lstat")
	_, err := os.Lstat(target)
	replacing := err == nil
	if replacing {
		s.fsop("rename")
		if err := os.Rename(target, old); err != nil {
			return errors.New("cannot move the installed pack aside")
		}
	}
	s.fsop("rename")
	if err := os.Rename(filepath.Join(run, id), target); err != nil {
		if replacing {
			s.fsop("rename")
			_ = os.Rename(old, target)
		}
		return errors.New("cannot move the staged pack into place")
	}
	return nil
}

// uninstall removes an installed pack. Only a directory carrying an install
// sidecar is removed; a hand-made pack belongs to the user.
func (s *CompanionPackStore) uninstall(opts wsconfig.Options, id string) (*companionSnapshot, error) {
	if id == companionDefaultID {
		return nil, companionRefuse(http.StatusConflict, "the built-in pack cannot be uninstalled")
	}
	if !companionPackID.MatchString(id) || id == "auto" {
		return nil, companionRefuse(http.StatusNotFound, "pack not found")
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	paths, err := companionPaths(opts)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(paths.packs, id)
	s.fsop("lstat")
	info, err := os.Lstat(target)
	if err != nil || info.Mode().Type() != fs.ModeDir {
		return nil, companionRefuse(http.StatusNotFound, "pack not found")
	}
	if !s.hasSidecar(paths, id) {
		return nil, companionRefuse(http.StatusConflict, "only installed packs can be uninstalled; remove a hand-made pack yourself")
	}
	run, err := s.prepareStaging(paths)
	if err != nil {
		return nil, err
	}
	defer func() {
		s.fsop("remove")
		_ = os.RemoveAll(run)
	}()
	var moved error
	snapshot := s.swapAndLoad(opts, func() {
		s.fsop("rename")
		moved = os.Rename(target, filepath.Join(run, "."+id))
	})
	if moved != nil {
		return snapshot, companionRefuse(http.StatusInternalServerError, "cannot remove the pack directory")
	}
	return snapshot, nil
}
