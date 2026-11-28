package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
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
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Version        string              `json:"version"`
	Author         string              `json:"author,omitempty"`
	License        string              `json:"license,omitempty"`
	Description    string              `json:"description,omitempty"`
	Format         string              `json:"format,omitempty"`
	VroidSourceURL string              `json:"vroidSourceUrl,omitempty"`
	URL            string              `json:"url,omitempty"`
	Rev            string              `json:"rev,omitempty"`
	Status         string              `json:"status"`
	Installed      bool                `json:"installed,omitempty"` // set by an install that wrote this pack
	Error          *companionPackError `json:"error,omitempty"`
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

// companionStrandedError reports that a previous pack could not be moved back
// after a failed swap. Its copy stays in the staging run, which the caller
// must keep so recoverStaging can restore it.
type companionStrandedError struct{ message string }

func (e *companionStrandedError) Error() string { return e.message }

func (s *CompanionPackStore) renameFile(oldPath, newPath string) error {
	s.fsop("rename")
	if s.rename != nil {
		return s.rename(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (s *CompanionPackStore) removeTree(path string) error {
	s.fsop("remove")
	if s.removeAll != nil {
		return s.removeAll(path)
	}
	return os.RemoveAll(path)
}

// checkPacksRoot refuses a pack root that is a link, as the scan does: an
// install or uninstall through it would act outside the config home.
func (s *CompanionPackStore) checkPacksRoot(paths companionInstallPaths) error {
	s.fsop("lstat")
	info, err := os.Lstat(paths.packs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return companionRefuse(http.StatusInternalServerError, "cannot inspect the companion pack directory")
	case info.Mode().Type() != fs.ModeDir:
		return companionRefuse(http.StatusConflict, "the companion pack directory must be a plain directory")
	}
	return nil
}

// Moved-aside names in a staging run: "." + id is a previous pack displaced by
// an install swap, and recoverStaging may put it back; "~" + id is a pack an
// uninstall removed, which must stay removed, so recoverStaging never touches
// it. Neither prefix can start a pack ID.
const (
	companionSwapAside      = "."
	companionUninstallAside = "~"
)

// recoverStaging keeps one invariant: a "."-named backup is recoverable only
// while its target is absent, and none survives an operation. A backup whose
// target exists is the leftover of a successful update (its deferred run
// removal failed) and is removed here; if that removal fails the operation is
// refused, because a later uninstall of the target would otherwise make the
// stale backup look like a pack to restore. With the invariant, at most one
// backup per ID has a missing target (the swap that failed its rollback); more
// than one is refused rather than guessed between. A restored backup is
// deleted only by prepareStaging's removal of the run, after its target is
// confirmed in place. An uninstall's "~" copy is never touched. Callers hold
// installMu and run it before prepareStaging. Any restore that succeeded is
// published before returning, even when a later one fails.
func (s *CompanionPackStore) recoverStaging(opts wsconfig.Options, paths companionInstallPaths) (err error) {
	s.fsop("lstat")
	if info, err := os.Lstat(paths.staging); err != nil || info.Mode().Type() != fs.ModeDir {
		return nil // absent, or refused later by prepareStaging
	}
	fail := func() error {
		return companionRefuse(http.StatusInternalServerError, "cannot restore a previous companion pack left in staging")
	}
	s.fsop("readdir")
	runs, err := os.ReadDir(paths.staging)
	if err != nil {
		return fail()
	}
	type backup struct{ id, path string }
	var missing []backup
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		runDir := filepath.Join(paths.staging, run.Name())
		s.fsop("readdir")
		children, err := os.ReadDir(runDir)
		if err != nil {
			return fail()
		}
		for _, child := range children {
			id, dotted := strings.CutPrefix(child.Name(), companionSwapAside)
			if !dotted || !child.IsDir() || !companionPackID.MatchString(id) {
				continue
			}
			path := filepath.Join(runDir, child.Name())
			s.fsop("lstat")
			if _, err := os.Lstat(filepath.Join(paths.packs, id)); err == nil {
				if s.removeTree(path) != nil {
					return companionRefuse(http.StatusInternalServerError, "cannot remove a stale companion pack backup left in staging")
				}
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fail()
			}
			missing = append(missing, backup{id, path})
		}
	}
	seen := map[string]bool{}
	for _, b := range missing {
		if seen[b.id] {
			return companionRefuse(http.StatusInternalServerError, "several previous copies of a companion pack are left in staging; remove the staging directory yourself after choosing one")
		}
		seen[b.id] = true
	}
	restored := false
	defer func() {
		if restored {
			s.reload(opts)
		}
	}()
	for _, b := range missing {
		s.fsop("mkdir")
		if os.MkdirAll(paths.packs, 0o755) != nil || s.renameFile(b.path, filepath.Join(paths.packs, b.id)) != nil {
			return fail()
		}
		restored = true
	}
	return nil
}

// prepareStaging removes staging runs a crash or a failed removal left
// behind, then creates a fresh run directory. Callers hold installMu and have
// run recoverStaging.
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
			_ = s.removeTree(filepath.Join(paths.staging, entry.Name()))
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
	// folded maps a lower-cased entry name to its on-disk spelling, for a
	// case-insensitive volume where "Foo" and "foo" are one directory.
	folded map[string]string
}

func (s *CompanionPackStore) localDirs(paths companionInstallPaths) (companionLocalDirs, error) {
	dirs := companionLocalDirs{plain: map[string]bool{}, folded: map[string]string{}}
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
		dirs.folded[strings.ToLower(id)] = id
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
	if err := s.checkPacksRoot(paths); err != nil {
		return result, err
	}
	if !dryRun {
		if err := s.recoverStaging(opts, paths); err != nil {
			return result, err
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
			License: entry.License, Description: entry.Description, Format: entry.Format,
			VroidSourceURL: entry.VroidSourceURL, URL: entry.URL, Status: companionStatusAvailable}
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
		// A local pack carrying Studio-authored content is never replaced by
		// an install; decided from the snapshot, without touching a file.
		if exists && installedPack.info.Origin != nil && installedPack.info.Origin.Kind == "local" &&
			installedPack.catalog.authored(installedPack.clips) {
			item.Error = &companionPackError{ID: entry.ID, Message: "this pack has Studio-authored clips; export them and uninstall it first"}
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
	stranded := false
	defer func() {
		if stranded {
			return // the run holds the only copy of a previous pack
		}
		_ = s.removeTree(run)
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
				var lost *companionStrandedError
				stranded = stranded || errors.As(err, &lost)
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
	if spelled, clash := dirs.folded[strings.ToLower(entry.ID)]; clash && spelled != entry.ID {
		return fail(errors.New("a pack directory whose name differs only by case already uses this pack ID; remove it yourself to install this one"))
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
// renamed into the staging run, and restored when the second rename fails; if
// that restore fails too, the copy stays in the run and a
// companionStrandedError tells the caller not to delete the run. The caller
// holds scanMu through swapAndLoad.
func (s *CompanionPackStore) swapIn(paths companionInstallPaths, run, id string) error {
	// A pack ID never starts with a dot, so the moved-aside name cannot meet
	// another staged pack. recoverStaging restores only this "." name.
	target := filepath.Join(paths.packs, id)
	old := filepath.Join(run, companionSwapAside+id)
	s.fsop("lstat")
	_, err := os.Lstat(target)
	replacing := err == nil
	if replacing {
		if err := s.renameFile(target, old); err != nil {
			return errors.New("cannot move the installed pack aside")
		}
	}
	if err := s.renameFile(filepath.Join(run, id), target); err != nil {
		if replacing && s.renameFile(old, target) != nil {
			return &companionStrandedError{"cannot move the staged pack into place, and the previous pack could not be restored; it is kept aside and the next install or uninstall restores it"}
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
	if err := s.checkPacksRoot(paths); err != nil {
		return nil, err
	}
	if err := s.recoverStaging(opts, paths); err != nil {
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
		_ = s.removeTree(run)
	}()
	var moved error
	snapshot := s.swapAndLoad(opts, func() {
		moved = s.renameFile(target, filepath.Join(run, companionUninstallAside+id))
	})
	if moved != nil {
		return snapshot, companionRefuse(http.StatusInternalServerError, "cannot remove the pack directory")
	}
	return snapshot, nil
}

const (
	companionVRMUploadFallback = "upload.vrm"
	companionVRMModelName      = "model.vrm"
	companionVRMDefaultDisplay = "VRM Avatar"
	companionVRMDefaultSlug    = "vrm-avatar"
)

func companionStripVRMExt(name string) string {
	if len(name) >= 4 && strings.EqualFold(name[len(name)-4:], ".vrm") {
		return name[:len(name)-4]
	}
	return name
}

func companionUploadVRMName(raw string) string {
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || !utf8.ValidString(raw) {
		return companionVRMUploadFallback
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return companionVRMUploadFallback
		}
	}
	if utf8.RuneCountInString(raw) > companionUploadNameMax {
		raw = string([]rune(raw)[:companionUploadNameMax])
	}
	return raw
}

func companionVRMDisplayName(displayName, filename string) (string, error) {
	if displayName != "" {
		trimmed := strings.TrimSpace(displayName)
		if err := companionText("name", trimmed, 64, true); err != nil {
			return "", err
		}
		return trimmed, nil
	}
	base := companionStripVRMExt(companionUploadVRMName(filename))
	var b strings.Builder
	for _, r := range base {
		if !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(cleaned) > 64 {
		cleaned = strings.TrimSpace(string([]rune(cleaned)[:64]))
	}
	if cleaned == "" {
		return companionVRMDefaultDisplay, nil
	}
	return cleaned, nil
}

func companionVRMSlug(raw string) string {
	raw = companionStripVRMExt(strings.TrimSpace(raw))
	var b strings.Builder
	prevDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
		case r == '_':
			b.WriteByte('_')
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-_")
	if len(slug) > 64 {
		slug = strings.TrimRight(slug[:64], "-_")
	}
	if slug == "" || !companionPackID.MatchString(slug) {
		slug = companionVRMDefaultSlug
	}
	if slug == companionDefaultID || slug == "auto" || companionReservedName(slug) {
		slug = "vrm-" + slug
		if len(slug) > 64 {
			slug = strings.TrimRight(slug[:64], "-_")
		}
	}
	return slug
}

// importVRM validates a single uploaded .vrm file, synthesizes a minimal
// ws-companion-vrm/v1 pack with an install sidecar, stages and atomically
// installs it, and reloads the in-memory snapshot.
func (s *CompanionPackStore) importVRM(opts wsconfig.Options, data []byte, explicitID, displayName, filename string) (companionPackInfo, error) {
	if len(data) == 0 {
		return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "the uploaded VRM file is empty")
	}
	if len(data) > companionVrmAssetCap {
		return companionPackInfo{}, companionRefuse(http.StatusRequestEntityTooLarge, "the uploaded VRM file exceeds the %s limit", companionByteSize(companionVrmAssetCap))
	}
	if _, err := inspectCompanionVRM(data); err != nil {
		return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "invalid VRM file: %s", err.Error())
	}
	explicitID = strings.TrimSpace(explicitID)
	if explicitID != "" {
		switch {
		case !companionPackID.MatchString(explicitID):
			return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "id must be 1-64 lowercase letters, digits, - or _")
		case explicitID == companionDefaultID || explicitID == "auto":
			return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "pack ID is reserved")
		case companionReservedName(explicitID):
			return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "pack ID is a reserved device name")
		}
	}
	name, err := companionVRMDisplayName(displayName, filename)
	if err != nil {
		return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "%s", err.Error())
	}

	s.installMu.Lock()
	defer s.installMu.Unlock()

	paths, err := companionPaths(opts)
	if err != nil {
		return companionPackInfo{}, err
	}
	if err := s.checkPacksRoot(paths); err != nil {
		return companionPackInfo{}, err
	}
	if err := s.recoverStaging(opts, paths); err != nil {
		return companionPackInfo{}, err
	}
	snapshot := s.snapshot(opts)
	dirs, err := s.localDirs(paths)
	if err != nil {
		return companionPackInfo{}, err
	}

	occupied := func(candidate string) bool {
		if candidate == companionDefaultID || candidate == "auto" || companionReservedName(candidate) {
			return true
		}
		if _, exists := snapshot.byID[candidate]; exists {
			return true
		}
		if _, exists := dirs.plain[candidate]; exists {
			return true
		}
		if _, clash := dirs.folded[strings.ToLower(candidate)]; clash {
			return true
		}
		return companionHasError(snapshot, candidate)
	}

	var id string
	if explicitID != "" {
		if occupied(explicitID) {
			return companionPackInfo{}, companionRefuse(http.StatusConflict, "pack ID %q is already installed; uninstall it first to replace it", explicitID)
		}
		id = explicitID
	} else {
		slugSource := displayName
		if strings.TrimSpace(slugSource) == "" {
			slugSource = companionUploadVRMName(filename)
		}
		base := companionVRMSlug(slugSource)
		if !occupied(base) {
			id = base
		} else {
			for n := 2; n <= 1000; n++ {
				suffix := "-" + strconv.Itoa(n)
				prefix := base
				if len(prefix)+len(suffix) > 64 {
					prefix = strings.TrimRight(prefix[:64-len(suffix)], "-_")
					if prefix == "" {
						prefix = "vrm"
					}
				}
				candidate := prefix + suffix
				if !occupied(candidate) {
					id = candidate
					break
				}
			}
			if id == "" {
				return companionPackInfo{}, companionRefuse(http.StatusConflict, "could not allocate a free pack ID")
			}
		}
	}

	if dirs.visited >= companionMaxPacks {
		return companionPackInfo{}, companionRefuse(http.StatusConflict, "the local pack limit of %d is reached", companionMaxPacks)
	}
	budget := companionCacheCap
	for _, pack := range snapshot.byID {
		if pack.info.Source == "local" {
			budget -= pack.size
		}
	}
	if budget <= 0 {
		return companionPackInfo{}, companionRefuse(http.StatusConflict, "total local pack cache byte limit exceeded")
	}

	manifestObj := struct {
		Schema string `json:"schema"`
		ID     string `json:"id"`
		Name   string `json:"name"`
		Model  string `json:"model"`
	}{
		Schema: companionPackVRMSchema,
		ID:     id,
		Name:   name,
		Model:  companionVRMModelName,
	}
	manifestBytes, err := json.MarshalIndent(manifestObj, "", "  ")
	if err != nil {
		return companionPackInfo{}, companionRefuse(http.StatusInternalServerError, "cannot build the VRM pack manifest")
	}
	manifestBytes = append(manifestBytes, '\n')

	pack, _, err := loadCompanionPack(id, "local", budget, func(file string, limit int) ([]byte, error) {
		switch file {
		case companionManifestName:
			if len(manifestBytes) > limit {
				return nil, errCompanionFileLimit
			}
			return manifestBytes, nil
		case companionVRMModelName:
			if len(data) > limit {
				return nil, errCompanionFileLimit
			}
			return data, nil
		default:
			return nil, errors.New("file is missing")
		}
	})
	if err != nil {
		if strings.Contains(err.Error(), "total local pack cache byte limit") {
			return companionPackInfo{}, companionRefuse(http.StatusConflict, "%s", err.Error())
		}
		return companionPackInfo{}, companionRefuse(http.StatusBadRequest, "%s", err.Error())
	}

	sum := sha256.Sum256(data)
	sourceName := companionUploadVRMName(filename)
	if filename == "" && displayName != "" {
		sourceName = companionUploadVRMName(displayName)
	}
	sidecar := companionSidecar{
		Source:        sourceName,
		Kind:          "local",
		ArchiveSHA256: hex.EncodeToString(sum[:]),
		PackSHA256:    pack.info.Rev,
		Version:       "1.0.0",
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		Official:      false,
	}

	run, err := s.prepareStaging(paths)
	if err != nil {
		return companionPackInfo{}, err
	}
	stranded := false
	defer func() {
		if stranded {
			return
		}
		_ = s.removeTree(run)
	}()
	if err := s.writeStaged(filepath.Join(run, id), pack, sidecar); err != nil {
		return companionPackInfo{}, companionRefuse(http.StatusInternalServerError, "%s", err.Error())
	}
	s.fsop("mkdir")
	if err := os.MkdirAll(paths.packs, 0o755); err != nil {
		return companionPackInfo{}, companionRefuse(http.StatusInternalServerError, "cannot create the companion pack directory")
	}
	var swapErr error
	snapshot = s.swapAndLoad(opts, func() {
		swapErr = s.swapIn(paths, run, id)
		var lost *companionStrandedError
		stranded = errors.As(swapErr, &lost)
	})
	if swapErr != nil {
		return companionPackInfo{}, companionRefuse(http.StatusInternalServerError, "%s", swapErr.Error())
	}
	loaded := snapshot.byID[id]
	if loaded == nil || loaded.info.Rev != pack.info.Rev {
		return companionPackInfo{}, companionRefuse(http.StatusInternalServerError, "the installed VRM pack did not load")
	}
	return loaded.info, nil
}
