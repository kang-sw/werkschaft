package wsdashboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsstate"
	"github.com/kang-sw/devenv/internal/wstestutil"
)

// v0510OutsideDenied is v0.51.0's outsideDenied (f800ba30d,
// fs_outside.go:105-113), frozen: the home-relative credential locations
// the released preview always refuses.
var v0510OutsideDenied = []string{
	".ssh", ".gnupg", ".aws", ".config/gh", ".netrc", ".docker",
	".kube", ".git-credentials", ".codex/auth.json", ".claude/.credentials.json",
	".config/gcloud", ".azure", ".npmrc", ".pypirc", ".cargo/credentials", ".cargo/credentials.toml",
	".vault-token", ".password-store", "Library/Keychains",
	".cache/ws/dashboard",
}

// v0510Refuses is v0.51.0's lexical deny verdict (f800ba30d,
// fs_denied.go:14-68 deniedBasePaths for home and the default cache root,
// and denySet.lexical, fs_denied.go:114-141): path at or under a denied base,
// without regard to case. The released preview checks nothing else for a path
// that does not exist yet, and identity checks only add aliases of bases.
func v0510Refuses(path, home, cacheRoot string) bool {
	var bases []string
	for _, name := range v0510OutsideDenied {
		bases = append(bases, filepath.Join(home, filepath.FromSlash(name)))
	}
	// Dir(""): the daemon directory under the (possibly relocated) cache root.
	bases = append(bases, filepath.Join(cacheRoot, "dashboard"))
	lower := strings.ToLower(filepath.Clean(path))
	for _, base := range bases {
		base = strings.ToLower(filepath.Clean(base))
		if lower == base || strings.HasPrefix(lower, base) &&
			(os.IsPathSeparator(base[len(base)-1]) || os.IsPathSeparator(lower[len(base)])) {
			return true
		}
	}
	return false
}

// TestProviderCredentialPathIsRefusedByTheV0510Preview: after a rollback the
// v0.51.0 local file preview, running as the same user, must refuse the
// provider credential file (the 0.51.1 compatibility gate). It runs the
// frozen release rule set against the new path, at the default cache root and
// a relocated one, and against the pre-release location as the control that
// the rule set can fail.
func TestProviderCredentialPathIsRefusedByTheV0510Preview(t *testing.T) {
	home := wstestutil.CanonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for name, cacheHome := range map[string]string{"default": "", "relocated": filepath.Join(wstestutil.CanonicalTempDir(t), "cache")} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(wsstate.EnvCacheHome, cacheHome)
			root, err := wsstate.CacheRoot(wsstate.Options{})
			if err != nil {
				t.Fatal(err)
			}
			path, err := wscred.DefaultPath("")
			if err != nil {
				t.Fatal(err)
			}
			if name == "default" {
				// The frozen list names the default cache directory.
				if root != filepath.Join(home, ".cache", wsstate.DefaultCacheDirName) {
					t.Fatalf("default cache root = %q", root)
				}
			}
			if !v0510Refuses(path, home, root) || !v0510Refuses(filepath.Dir(path), home, root) {
				t.Fatalf("the v0.51.0 preview reads the credential path %q", path)
			}
			legacy := filepath.Join(root, "auth", wscred.FileName)
			if v0510Refuses(legacy, home, root) {
				t.Fatalf("control failed: the v0.51.0 rule set refuses the pre-release location %q, so this test proves nothing", legacy)
			}
			// The candidate refuses both.
			set := newDenySet()
			if !set.denied(path) || !set.denied(legacy) {
				t.Fatal("the candidate preview reads the credential file or the pre-release location")
			}
		})
	}
}

func TestCredentialDirectoryUnderTheDaemonDirectoryConstant(t *testing.T) {
	root := t.TempDir()
	dir, err := Dir(root)
	if err != nil {
		t.Fatal(err)
	}
	path, err := wscred.DefaultPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Fatalf("DefaultPath %q is not under the daemon directory %q", path, dir)
	}
}

func writeLegacyCredentialFile(t *testing.T, cacheHome string) string {
	t.Helper()
	legacy := filepath.Join(cacheHome, "auth", wscred.FileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":1,"providers":{"openrouter":{"kind":"api_key","api_key":"` + credFixtureKey + `"}}}`
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func notExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: want not-exist, got %v", path, err)
	}
}

// TestDaemonStartMovesTheCandidateCredentialFile is the upgrade, rollback,
// re-upgrade sequence for a file a pre-release build wrote beside the daemon
// directory: the daemon moves it under its lock at start, serves its key,
// nothing remains where v0.51.0's preview would read it, and the next start
// (after a v0.51.0 run, which never touches it) finds the file in place.
func TestDaemonStartMovesTheCandidateCredentialFile(t *testing.T) {
	cacheHome := t.TempDir()
	dir, err := Dir(cacheHome)
	if err != nil {
		t.Fatal(err)
	}
	credPath, _ := wscred.DefaultPath(cacheHome)
	legacy := writeLegacyCredentialFile(t, filepath.Dir(dir))

	cd := startCredentialDaemon(t, dir, credPath, nil)
	notExist(t, legacy)
	notExist(t, filepath.Dir(legacy))
	if cred, err := routeCredential(t, cd, tokenPermitted); err != nil || cred.Secret.Reveal() != credFixtureKey {
		t.Fatalf("the moved key is not served = %+v, %v", cred, err)
	}
	cd.d.Close()

	cd = startCredentialDaemon(t, dir, credPath, nil)
	notExist(t, legacy)
	if cred, err := routeCredential(t, cd, tokenPermitted); err != nil || cred.Secret.Reveal() != credFixtureKey {
		t.Fatalf("the key is lost after a restart = %+v, %v", cred, err)
	}
}

// TestOfflineCredentialCLIMovesTheCandidateFileUnderTheLock: a CLI run with
// no daemon moves the legacy file while it holds the daemon's lock, for a
// change and for a read, and leaves it alone beside a running daemon.
func TestOfflineCredentialCLIMovesTheCandidateFileUnderTheLock(t *testing.T) {
	cacheHome := t.TempDir()
	dir, _ := Dir(cacheHome)
	credPath, _ := wscred.DefaultPath(cacheHome)
	legacy := writeLegacyCredentialFile(t, filepath.Dir(dir))

	if err := MigrateCredentialFile(dir, credPath); err != nil {
		t.Fatal(err)
	}
	notExist(t, legacy)
	if st, err := wscred.NewStore(credPath).Status(); err != nil || !st[0].Configured {
		t.Fatalf("after a read-side move = %+v, %v", st, err)
	}

	cacheHome = t.TempDir()
	dir, _ = Dir(cacheHome)
	credPath, _ = wscred.DefaultPath(cacheHome)
	legacy = writeLegacyCredentialFile(t, filepath.Dir(dir))
	locked := false
	beforeOfflineCredentialWrite = func() {
		locked = true
		if _, err := os.Stat(legacy); err != nil {
			t.Errorf("the legacy file moved before the lock was held: %v", err)
		}
	}
	t.Cleanup(func() { beforeOfflineCredentialWrite = nil })
	res, served, err := ApplyCredentialChange(context.Background(), dir, credPath, wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey2)})
	if err != nil || served != CredentialServedByFile || !res.Changed || !locked {
		t.Fatalf("offline set = %+v, %q, %v", res, served, err)
	}
	notExist(t, legacy)
	if raw, err := os.ReadFile(credPath); err != nil || !strings.Contains(string(raw), credFixtureKey2) {
		t.Fatalf("the moved file did not take the change: %v", err)
	}
}
