package wsconfig

import (
	"testing"
)

func TestWerkWebSearchDefaultScopeAndValues(t *testing.T) {
	for _, key := range []string{
		ItemWerkWebSearch,
		ItemWerkWebSearchMaxUses,
		ItemWerkWebSearchMaxResults,
		ItemWerkWebSearchEngine,
		ItemWerkWebSearchSIWC,
	} {
		if got := DefaultScope(key); got != ScopeGlobal {
			t.Errorf("DefaultScope(%q) = %q, want %q", key, got, ScopeGlobal)
		}
		if GlobalOnly(key) {
			t.Errorf("%q must not be global-only so repo/project/session scopes can override it", key)
		}
	}

	opts := Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir(), RepoRoot: t.TempDir()}
	r := NewResolver(opts, WerkWebSearchDefaults(), nil, nil)
	got := ResolveWerkWebSearch(&r, "")
	want := DefaultWerkWebSearchConfig()
	if got != want {
		t.Fatalf("ResolveWerkWebSearch default = %+v, want %+v", got, want)
	}
	if !got.Enabled || got.MaxUses != 2 || got.MaxResults != 5 || got.Engine != "exa" || got.SIWC {
		t.Fatalf("unexpected builtin defaults: %+v", got)
	}
}

func TestWerkWebSearchLayeredResolutionAndInvalidFallback(t *testing.T) {
	opts := Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir(), RepoRoot: t.TempDir()}
	r := NewResolver(opts, WerkWebSearchDefaults(), nil, nil)

	if err := r.Set(ItemWerkWebSearchEngine, "native", SetOptions{ExplicitScope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ItemWerkWebSearchMaxUses, "4", SetOptions{ExplicitScope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ItemWerkWebSearchMaxResults, "8", SetOptions{ExplicitScope: ScopeRepo}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ItemWerkWebSearchSIWC, "true", SetOptions{ExplicitScope: ScopeProject}); err != nil {
		t.Fatal(err)
	}

	got := ResolveWerkWebSearch(&r, "")
	if !got.Enabled || got.Engine != "native" || got.MaxUses != 4 || got.MaxResults != 8 || !got.SIWC {
		t.Fatalf("layered resolution = %+v", got)
	}

	// Project scope overrides repo and global.
	if err := r.Set(ItemWerkWebSearch, "false", SetOptions{ExplicitScope: ScopeProject}); err != nil {
		t.Fatal(err)
	}
	got = ResolveWerkWebSearch(&r, "")
	if got.Enabled {
		t.Fatalf("expected project Enabled=false to win, got %+v", got)
	}

	// Hand-edited invalid values fall back to builtin defaults cleanly.
	if err := r.Set(ItemWerkWebSearchEngine, "bogus", SetOptions{ExplicitScope: ScopeProject}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ItemWerkWebSearchMaxUses, "0", SetOptions{ExplicitScope: ScopeProject}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ItemWerkWebSearchMaxResults, "-3", SetOptions{ExplicitScope: ScopeProject}); err != nil {
		t.Fatal(err)
	}
	got = ResolveWerkWebSearch(&r, "")
	if got.Engine != DefaultWerkWebSearchEngine || got.MaxUses != DefaultWerkWebSearchMaxUses || got.MaxResults != DefaultWerkWebSearchMaxResults {
		t.Fatalf("invalid overrides should fall back to defaults, got %+v", got)
	}
}
