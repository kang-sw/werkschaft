package wsconfig

import "maps"

// A config key a release shipped stays a wire contract after a later release
// replaces it: an older binary keeps reading and writing the stored string.
// Two hooks let a replacement key stay compatible in both directions without a
// second file touch.
//
//   - A mirror runs inside the locked rewrite of a scalar write or reset, so
//     the replacement key and the legacy key land in one file write and an
//     older reader never sees them disagree.
//   - A global reconcile runs after the global file is read, so a value an
//     older writer stored under the legacy key reaches every reader of the
//     replacement key. It changes the in-memory read only; the next write
//     persists the result.

// OverrideMirror adjusts overrides after key was set (removed false) or reset
// (removed true) in one config file. It edits the map in place; the write that
// triggered it commits the whole map.
type OverrideMirror func(key string, overrides map[string]string, removed bool)

// GlobalReconcile rewrites a freshly read global overrides map in memory.
type GlobalReconcile func(overrides map[string]string)

var (
	overrideMirrors  = map[string][]OverrideMirror{}
	globalReconciles []GlobalReconcile
)

// RegisterOverrideMirror runs mirror after every scalar write or reset of one
// of keys, before the set checks. Call at package init time.
func RegisterOverrideMirror(keys []string, mirror OverrideMirror) {
	for _, key := range keys {
		overrideMirrors[key] = append(overrideMirrors[key], mirror)
	}
}

// RegisterGlobalReconcile runs reconcile on every read of the global config
// file's overrides. Call at package init time.
func RegisterGlobalReconcile(reconcile GlobalReconcile) {
	globalReconciles = append(globalReconciles, reconcile)
}

// mirrorOverride applies the mirrors registered for key to overrides.
func mirrorOverride(key string, overrides map[string]string, removed bool) {
	for _, mirror := range overrideMirrors[key] {
		mirror(key, overrides, removed)
	}
}

func hasOverrideMirror(key string) bool { return len(overrideMirrors[key]) > 0 }

// reconcileGlobal applies every registered reconcile to a global read.
func reconcileGlobal(cfg Config) Config {
	if len(globalReconciles) == 0 || len(cfg.Overrides) == 0 {
		return cfg
	}
	for _, reconcile := range globalReconciles {
		reconcile(cfg.Overrides)
	}
	return cfg
}

// overridesEqual reports whether two override maps hold the same entries.
func overridesEqual(a, b map[string]string) bool { return maps.Equal(a, b) }
