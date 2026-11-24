package wsdashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func providerCredentialBases(cacheHome string) []string {
	path, err := wscred.DefaultPath(cacheHome)
	if err != nil {
		return nil
	}
	current := filepath.Dir(path)
	legacy := filepath.Join(filepath.Dir(filepath.Dir(current)), filepath.Base(current))
	return []string{current, path, legacy, filepath.Join(legacy, wscred.FileName)}
}

var errProviderCredentialPath = errors.New("the provider credential store is protected")

// ProviderCredentialProtection holds one file operation's view of the daemon's
// credential store and the default and legacy locations it may have used.
type ProviderCredentialProtection struct{ deny *denySet }

func NewProviderCredentialProtection() *ProviderCredentialProtection {
	deny := &denySet{loaded: true}
	bases := providerCredentialBases("")
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		bases = append(bases, providerCredentialBases(filepath.Join(home, ".cache", wsstate.DefaultCacheDirName))...)
	}
	seen := make(map[string]bool)
	for _, base := range bases {
		if !seen[base] {
			seen[base] = true
			deny.bases = append(deny.bases, base)
			deny.lower = append(deny.lower, strings.ToLower(base))
		}
	}
	return &ProviderCredentialProtection{deny: deny}
}

// DeniesName checks a path without touching the filesystem, for directory walks.
func (p *ProviderCredentialProtection) DeniesName(path string) bool {
	return p.deny.containsBasePath(strings.ToLower(filepath.Clean(path)))
}

func (p *ProviderCredentialProtection) CheckPath(path string) error {
	if p.DeniesName(path) {
		return errProviderCredentialPath
	}
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
		path = real
		if p.DeniesName(path) {
			return errProviderCredentialPath
		}
	}
	if identities := p.deny.identities(); len(identities) > 0 && p.deny.aboveDenied(filepath.Clean(path), identities) {
		return errProviderCredentialPath
	}
	return nil
}

func (p *ProviderCredentialProtection) CheckOpened(info os.FileInfo) error {
	if p.deny.opened(info) {
		return errProviderCredentialPath
	}
	return nil
}
