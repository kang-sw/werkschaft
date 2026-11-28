package wsagent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pruneEphemeralStateDir bounds an ephemeral slot's isolated agy state
// directory (ephemeralStatePruner).
func (AgyBackend) pruneEphemeralStateDir(dir, keepSession string, maxKeep int) {
	pruneAgyGeminiDir(dir, keepSession, maxKeep)
}

// pruneAgyGeminiDir bounds growth inside an isolated agy --gemini-dir by
// removing the oldest brain/ session directories and .db session files beyond
// maxKeep, never touching the active keepSession or anything outside geminiDir.
func pruneAgyGeminiDir(geminiDir, keepSession string, maxKeep int) {
	if geminiDir == "" || maxKeep <= 0 {
		return
	}
	type agedPath struct {
		path    string
		modTime time.Time
	}

	// Prune brain/<session-id> subdirectories under <geminiDir>/antigravity-cli/brain
	// and <geminiDir>/brain.
	for _, brainDir := range []string{
		filepath.Join(geminiDir, "antigravity-cli", "brain"),
		filepath.Join(geminiDir, "brain"),
	} {
		entries, err := os.ReadDir(brainDir)
		if err != nil {
			continue
		}
		var dirs []agedPath
		for _, e := range entries {
			if !e.IsDir() || (keepSession != "" && e.Name() == keepSession) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			dirs = append(dirs, agedPath{path: filepath.Join(brainDir, e.Name()), modTime: info.ModTime()})
		}
		if len(dirs) <= maxKeep {
			continue
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].modTime.Before(dirs[j].modTime) })
		for _, victim := range dirs[:len(dirs)-maxKeep] {
			_ = os.RemoveAll(victim.path)
		}
	}

	// Prune excess .db files (and matching -wal/-shm sidecars) inside geminiDir.
	var dbs []agedPath
	_ = filepath.WalkDir(geminiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".db") {
			return nil
		}
		if keepSession != "" && strings.Contains(d.Name(), keepSession) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		dbs = append(dbs, agedPath{path: path, modTime: info.ModTime()})
		return nil
	})
	if len(dbs) > maxKeep {
		sort.Slice(dbs, func(i, j int) bool { return dbs[i].modTime.Before(dbs[j].modTime) })
		for _, victim := range dbs[:len(dbs)-maxKeep] {
			_ = os.Remove(victim.path)
			_ = os.Remove(victim.path + "-wal")
			_ = os.Remove(victim.path + "-shm")
		}
	}
}
