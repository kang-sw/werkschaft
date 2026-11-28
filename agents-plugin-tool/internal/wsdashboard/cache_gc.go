// cache_gc.go bounds the growth of the cache root's large artifacts
// (dashboard/rollback, dashboard/bin, proj/*) and the daemon's maintenance
// pass that runs it. Ticket 261129-feat-ws-cache-large-growth-gc.
//
// Everything here is background and periodic: nothing runs on an interactive
// path, and a pass stops between entries when the daemon stops.
package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

const (
	// rollbackDirName is the legacy swap directory under the daemon state
	// directory. No live code reads it.
	rollbackDirName = "rollback"
	// stagedMetadataSuffix names a staged build's launcher sidecar.
	stagedMetadataSuffix = ".metadata.json"
	// macStableExecutable is the mutable executable macOS launches from.
	macStableExecutable = "ws-mcp-dashboard"

	cacheGCGrace             = 24 * time.Hour
	cacheGCRollbackMaxAge    = 7 * 24 * time.Hour
	cacheGCRollbackKeep      = 3
	cacheGCBinMaxAge         = 7 * 24 * time.Hour
	cacheGCBinKeep           = 5
	cacheGCInterval          = 6 * time.Hour
	cacheGCStartDelay        = 45 * time.Second
	sessionPruneEveryDefault = time.Hour
)

// CacheGCConfig holds the cache GC bounds. The defaults are fixed constants;
// the environment can override them (CacheGCConfigFromEnv) and tests inject
// their own.
type CacheGCConfig struct {
	// Enabled is false when WS_CACHE_GC is off.
	Enabled bool
	// StartDelay is the wait before the first pass; Interval separates later ones.
	StartDelay time.Duration
	Interval   time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// Grace is the age under which nothing is removed. It is a constant, not
	// an override.
	Grace time.Duration

	RollbackKeep   int
	RollbackMaxAge time.Duration
	BinKeep        int
	BinMaxAge      time.Duration

	ProjTTL         time.Duration
	SharedDocsTTL   time.Duration
	ProjMaxBytes    int64
	DeadWorktreeAge time.Duration
}

// DefaultCacheGCConfig returns the fixed defaults.
func DefaultCacheGCConfig() CacheGCConfig {
	return CacheGCConfig{
		Enabled:         true,
		StartDelay:      cacheGCStartDelay,
		Interval:        cacheGCInterval,
		Grace:           cacheGCGrace,
		RollbackKeep:    cacheGCRollbackKeep,
		RollbackMaxAge:  cacheGCRollbackMaxAge,
		BinKeep:         cacheGCBinKeep,
		BinMaxAge:       cacheGCBinMaxAge,
		ProjTTL:         wsstate.GCProjTTL,
		SharedDocsTTL:   wsstate.GCSharedDocsTTL,
		ProjMaxBytes:    wsstate.GCMaxBytes,
		DeadWorktreeAge: wsstate.GCDeadWorktreeAge,
	}
}

// CacheGCConfigFromEnv applies the WS_CACHE_GC* overrides to the defaults:
//
//	WS_CACHE_GC=off                  disable the periodic pass
//	WS_CACHE_GC_INTERVAL             pass period (6h)
//	WS_CACHE_GC_ROLLBACK_KEEP / _MAX_AGE
//	WS_CACHE_GC_BIN_KEEP / _MAX_AGE
//	WS_CACHE_GC_PROJ_TTL / _SHARED_DOCS_TTL / _DEAD_WORKTREE_AGE
//	WS_CACHE_GC_PROJ_MAX_BYTES
//
// Durations take Go syntax or an "Nd" day count. A malformed value keeps the
// default. Ages are floored at the 24h grace and counts at 1.
func CacheGCConfigFromEnv(getenv func(string) string) CacheGCConfig {
	cfg := DefaultCacheGCConfig()
	switch strings.ToLower(strings.TrimSpace(getenv("WS_CACHE_GC"))) {
	case "off", "0", "false", "no", "disabled":
		cfg.Enabled = false
	}
	dur := func(name string, into *time.Duration, floor time.Duration) {
		if d, ok := parseGCDuration(getenv(name)); ok && d >= floor {
			*into = d
		} else if ok && d > 0 {
			*into = floor
		}
	}
	count := func(name string, into *int) {
		if n, err := strconv.Atoi(strings.TrimSpace(getenv(name))); err == nil && n >= 1 {
			*into = n
		}
	}
	dur("WS_CACHE_GC_INTERVAL", &cfg.Interval, time.Minute)
	count("WS_CACHE_GC_ROLLBACK_KEEP", &cfg.RollbackKeep)
	dur("WS_CACHE_GC_ROLLBACK_MAX_AGE", &cfg.RollbackMaxAge, cfg.Grace)
	count("WS_CACHE_GC_BIN_KEEP", &cfg.BinKeep)
	dur("WS_CACHE_GC_BIN_MAX_AGE", &cfg.BinMaxAge, cfg.Grace)
	dur("WS_CACHE_GC_PROJ_TTL", &cfg.ProjTTL, cfg.Grace)
	dur("WS_CACHE_GC_SHARED_DOCS_TTL", &cfg.SharedDocsTTL, cfg.Grace)
	dur("WS_CACHE_GC_DEAD_WORKTREE_AGE", &cfg.DeadWorktreeAge, cfg.Grace)
	if n, err := strconv.ParseInt(strings.TrimSpace(getenv("WS_CACHE_GC_PROJ_MAX_BYTES")), 10, 64); err == nil && n > 0 {
		cfg.ProjMaxBytes = n
	}
	return cfg
}

func parseGCDuration(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, false
		}
		return time.Duration(n) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

func (c CacheGCConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c CacheGCConfig) grace() time.Duration {
	if c.Grace <= 0 {
		return cacheGCGrace
	}
	return c.Grace
}

// PruneRollbackDir removes legacy swap entries under <dir>/rollback: anything
// past maxAge or beyond the newest keep, but never an entry younger than the
// grace. It returns how many it removed. Entries are never read.
func PruneRollbackDir(dir string, cfg CacheGCConfig) int {
	root := filepath.Join(dir, rollbackDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	type item struct {
		path string
		mod  time.Time
	}
	var items []item
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join(root, entry.Name()), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].mod.Equal(items[j].mod) {
			return items[i].mod.After(items[j].mod)
		}
		return items[i].path < items[j].path
	})
	keep, maxAge, now := cfg.RollbackKeep, cfg.RollbackMaxAge, cfg.now()
	if keep <= 0 {
		keep = cacheGCRollbackKeep
	}
	if maxAge <= 0 {
		maxAge = cacheGCRollbackMaxAge
	}
	removed := 0
	for rank, it := range items {
		age := now.Sub(it.mod)
		if age < cfg.grace() || (rank < keep && age <= maxAge) {
			continue
		}
		// A symlink is removed as a link, never followed.
		if os.RemoveAll(it.path) == nil {
			removed++
		}
	}
	return removed
}

// stagedBuildName matches the only names the staging code writes
// (stagedExecutableName): ws-mcp-dashboard-<16 hex>, plus ".exe" on Windows.
var stagedBuildName = regexp.MustCompile(`^ws-mcp-dashboard-([0-9a-f]{16})(\.exe)?$`)

// PruneStagedExecutables removes old staged daemon builds from <dir>/bin with
// the default bounds. exe is the running daemon's executable.
func PruneStagedExecutables(dir, exe string) {
	pruneStagedExecutables(dir, exe, "", DefaultCacheGCConfig(), nil)
}

// pruneStagedExecutables keeps the pin set and the newest BinKeep distinct
// builds, and removes builds past BinMaxAge beyond them, with their sidecars.
//
// Pinned, never removed: exe; the macOS stable executable; the executable
// last-running-executable.json names and its hash's build; the build whose
// content hash is exeHash; any build younger than the grace; anything that is
// not a regular file named like a staged build or its .metadata.json sidecar
// (directories, symlinks, temp files, unknown metadata). A record that exists
// but cannot be read stops the pass: the rollback target is then unknown.
// A file the OS will not remove (a locked Windows image) stays.
func pruneStagedExecutables(dir, exe, exeHash string, cfg CacheGCConfig, stop func() bool) int {
	binDir := filepath.Join(dir, stagedBinDir)
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return 0
	}
	pins, ok := stagedPins(dir, exe, exeHash)
	if !ok {
		return 0
	}
	type build struct {
		sum     string
		path    string
		sidecar string
		mod     time.Time
	}
	byName := map[string]fs.DirEntry{}
	for _, entry := range entries {
		byName[entry.Name()] = entry
	}
	var builds []*build
	for _, entry := range entries {
		m := stagedBuildName.FindStringSubmatch(entry.Name())
		if m == nil || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		b := &build{sum: m[1], path: filepath.Join(binDir, entry.Name()), mod: info.ModTime()}
		if side, ok := byName[entry.Name()+stagedMetadataSuffix]; ok && side.Type().IsRegular() {
			b.sidecar = filepath.Join(binDir, side.Name())
		}
		builds = append(builds, b)
	}
	sort.Slice(builds, func(i, j int) bool {
		if !builds[i].mod.Equal(builds[j].mod) {
			return builds[i].mod.After(builds[j].mod)
		}
		return builds[i].path < builds[j].path
	})
	keep, maxAge, now := cfg.BinKeep, cfg.BinMaxAge, cfg.now()
	if keep <= 0 {
		keep = cacheGCBinKeep
	}
	if maxAge <= 0 {
		maxAge = cacheGCBinMaxAge
	}
	removed := 0
	seen := map[string]bool{}
	for _, b := range builds {
		if stop != nil && stop() {
			return removed
		}
		rank := len(seen)
		fresh := !seen[b.sum]
		seen[b.sum] = true
		age := now.Sub(b.mod)
		keepIt := age < cfg.grace() || age <= maxAge || (fresh && rank < keep) || pins.has(b.path, b.sum)
		if keepIt {
			continue
		}
		if err := os.Remove(b.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			// Locked or protected: keep it and its sidecar.
			continue
		}
		removed++
		if b.sidecar != "" {
			if os.Remove(b.sidecar) == nil {
				removed++
			}
		}
	}
	// A sidecar whose build is gone, once it is itself past the grace.
	for _, entry := range entries {
		if stop != nil && stop() {
			return removed
		}
		base, ok := strings.CutSuffix(entry.Name(), stagedMetadataSuffix)
		if !ok || stagedBuildName.FindStringSubmatch(base) == nil || !entry.Type().IsRegular() {
			continue
		}
		// byName is the listing before this pass: a build removed above
		// took its own sidecar, one that stayed keeps it.
		if _, present := byName[base]; present {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < cfg.grace() {
			continue
		}
		if os.Remove(filepath.Join(binDir, entry.Name())) == nil {
			removed++
		}
	}
	return removed
}

type stagedPinSet struct {
	paths []string
	sums  map[string]bool
}

func (p stagedPinSet) has(path, sum string) bool {
	if p.sums[sum] {
		return true
	}
	for _, pin := range p.paths {
		if sameCachePath(pin, path) {
			return true
		}
	}
	return false
}

func sameCachePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// stagedPins collects what must outlive the pass. ok is false when the
// running-executable record exists but is unusable.
func stagedPins(dir, exe, exeHash string) (stagedPinSet, bool) {
	pins := stagedPinSet{sums: map[string]bool{}}
	pinSum := func(sum string) {
		if len(sum) >= 16 {
			pins.sums[strings.ToLower(sum[:16])] = true
		}
	}
	if exe != "" {
		pins.paths = append(pins.paths, exe)
		if m := stagedBuildName.FindStringSubmatch(filepath.Base(exe)); m != nil {
			pins.sums[m[1]] = true
		}
	}
	pinSum(exeHash)
	pins.paths = append(pins.paths, filepath.Join(dir, stagedBinDir, macStableExecutable))
	raw, err := os.ReadFile(filepath.Join(dir, RunningExecutableFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return pins, false
	default:
		var rec runningExecutable
		if json.Unmarshal(raw, &rec) != nil {
			return pins, false
		}
		if rec.Executable != "" {
			pins.paths = append(pins.paths, rec.Executable)
			if m := stagedBuildName.FindStringSubmatch(filepath.Base(rec.Executable)); m != nil {
				pins.sums[m[1]] = true
			}
		}
		pinSum(rec.SHA256)
	}
	return pins, true
}

// cacheRootOf returns the cache root of a daemon state directory, or false for
// a directory that is not <root>/dashboard (a test's scratch directory), where
// no sibling directory is ours to collect.
func cacheRootOf(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	if filepath.Base(dir) != dirName {
		return "", false
	}
	return filepath.Dir(dir), true
}

// runCacheGC runs one pass over the cache root's large areas. It reads a
// session registration only for a dead-worktree candidate.
func (d *Daemon) runCacheGC(cfg CacheGCConfig) {
	root, ok := cacheRootOf(d.dir)
	if !ok {
		return
	}
	stopped := func() bool {
		select {
		case <-d.stop:
			return true
		default:
			return false
		}
	}
	if stopped() {
		return
	}
	removed := PruneRollbackDir(d.dir, cfg)
	if stopped() {
		return
	}
	removed += pruneStagedExecutables(d.dir, d.executable, d.contentHash, cfg, stopped)
	var registered []Registration
	var registeredRead bool
	res := wsstate.GCProjects(root, wsstate.GCOptions{
		Now:             cfg.now,
		Grace:           cfg.grace(),
		TTL:             cfg.ProjTTL,
		SharedDocsTTL:   cfg.SharedDocsTTL,
		MaxBytes:        cfg.ProjMaxBytes,
		DeadWorktreeAge: cfg.DeadWorktreeAge,
		Stop:            stopped,
		SessionActive: func(worktreePath string) bool {
			if !registeredRead {
				registered, _ = ReadRegistrations(d.dir)
				registeredRead = true
			}
			for _, reg := range registered {
				if strings.EqualFold(filepath.Clean(reg.Project), filepath.Clean(worktreePath)) {
					return true
				}
			}
			return false
		},
	})
	if d.log != nil && (removed > 0 || res.Entries > 0) {
		fmt.Fprintf(d.log, "dashboard: cache gc removed %d staged/rollback entries, %d proj entries (%d bytes, %d dead worktrees)\n", removed, res.Entries, res.Bytes, res.DeadWorktrees)
	}
}

// cacheGCLoop runs one pass shortly after startup and then every cfg.Interval.
// It never runs on a request path and ends with the daemon's stop.
func (d *Daemon) cacheGCLoop(cfg CacheGCConfig) {
	defer close(d.gcDone)
	interval := cfg.Interval
	if interval <= 0 {
		interval = cacheGCInterval
	}
	timer := time.NewTimer(cfg.StartDelay)
	defer timer.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-timer.C:
			d.runCacheGC(cfg)
			timer.Reset(interval)
		}
	}
}

// pruneSessionsTick is the hourly registration prune of a long-running daemon,
// the same one a ws-mcp startup requests. PruneRegistrationsFor refuses while
// the daemon drains.
func (d *Daemon) pruneSessionsTick(now time.Time) int {
	pruned, _ := d.PruneRegistrationsFor(d.dir, now, RegistryRetention)
	return pruned
}
