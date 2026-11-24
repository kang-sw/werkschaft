package wsdashboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func providerCredentialBases(cacheHome string) []string {
	dirs, files, _ := providerCredentialPaths(cacheHome)
	return append(dirs, files...)
}

func providerCredentialPaths(cacheHome string) (dirs, files []string, err error) {
	path, err := wscred.DefaultPath(cacheHome)
	if err != nil {
		return nil, nil, err
	}
	current := filepath.Dir(path)
	legacy := filepath.Join(filepath.Dir(filepath.Dir(current)), filepath.Base(current))
	committed, retiring, staged := wscred.GenerationPaths(path)
	return []string{current, legacy}, []string{committed, retiring, staged, filepath.Join(legacy, wscred.FileName)}, nil
}

var errProviderCredentialPath = errors.New("the provider credential store is protected")

// ProviderCredentialProtection holds one file operation's view of the daemon's
// credential store and the default and legacy locations it may have used.
type ProviderCredentialProtection struct {
	deny            *denySet
	credentialFiles []string
	identityErr     error
}

func NewProviderCredentialProtection() *ProviderCredentialProtection {
	deny := &denySet{loaded: true}
	bases, files, err := providerCredentialPaths("")
	p := &ProviderCredentialProtection{deny: deny, identityErr: err}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		homeDirs, homeFiles, homeErr := providerCredentialPaths(filepath.Join(home, ".cache", wsstate.DefaultCacheDirName))
		if homeErr != nil && p.identityErr == nil {
			p.identityErr = homeErr
		}
		bases = append(bases, homeDirs...)
		files = append(files, homeFiles...)
	}
	fileSet := make(map[string]bool)
	for _, path := range files {
		if !fileSet[path] {
			p.credentialFiles = append(p.credentialFiles, path)
			fileSet[path] = true
			if (networkPath(path) || deviceElement(path)) && p.identityErr == nil {
				p.identityErr = errors.New("credential location is not a local file path")
			}
		}
	}
	deny.stat = func(path string) (os.FileInfo, error) {
		if !fileSet[path] {
			return os.Stat(path)
		}
		if filepath.Base(path) == wscred.FileName {
			file, info, err := wscred.OpenCredentialFileForRead(path)
			if err != nil {
				if p.identityErr == nil {
					p.identityErr = err
				}
				return nil, err
			}
			if file == nil {
				return nil, os.ErrNotExist
			}
			_ = file.Close()
			return info, nil
		}
		file, err := wscred.OpenCredentialFile(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return file.Stat()
	}
	seen := make(map[string]bool)
	for _, base := range append(bases, p.credentialFiles...) {
		if !seen[base] {
			seen[base] = true
			deny.bases = append(deny.bases, base)
			deny.lower = append(deny.lower, strings.ToLower(base))
		}
	}
	return p
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
	return p.checkIdentityError()
}

func (p *ProviderCredentialProtection) CheckOpened(info os.FileInfo) error {
	if p.deny.opened(info) {
		return errProviderCredentialPath
	}
	return p.checkIdentityError()
}

func (p *ProviderCredentialProtection) checkIdentityError() error {
	if p.identityErr != nil {
		return fmt.Errorf("cannot verify provider credential identity: %w", p.identityErr)
	}
	return nil
}

// CheckGitWorktreeCredentials checks for links Git's own worktree reads
// could follow, including links to a generation being retired.
func (p *ProviderCredentialProtection) CheckGitWorktreeCredentials() error {
	unverified := errors.New("cannot verify provider credential hard links before reading the worktree; retry when the credential store is accessible")
	if p.identityErr != nil {
		return unverified
	}
	for _, path := range p.credentialFiles {
		linked, err := providerCredentialLinkType(path)
		if err != nil {
			return unverified
		}
		switch linked {
		case credentialNameLink:
			return errors.New("worktree search refused because a provider credential filename is a symbolic link or reparse point; restore a regular credential file, then retry")
		case credentialHardLink:
			return errors.New("worktree search refused because a provider credential file has an extra hard link; remove the extra link while keeping the store's file, then retry")
		}
	}
	return nil
}

type credentialLinkType int

const (
	credentialNoLink credentialLinkType = iota
	credentialNameLink
	credentialHardLink
)

func providerCredentialLinkType(path string) (credentialLinkType, error) {
	name, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return credentialNoLink, nil
	}
	if err != nil {
		return credentialNoLink, err
	}
	if wscred.CredentialNameIsLink(name) {
		return credentialNameLink, nil
	}
	file, err := wscred.OpenCredentialFile(path)
	if err != nil {
		return credentialNoLink, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return credentialNoLink, err
	}
	if wscred.CredentialNameIsLink(info) {
		return credentialNameLink, nil
	}
	if !info.Mode().IsRegular() || !os.SameFile(name, info) {
		return credentialNoLink, errors.New("credential file identity could not be verified")
	}
	if linked, err := wsfs.HasMultipleLinks(file, info); linked || err != nil {
		return credentialHardLink, err
	}
	return credentialNoLink, nil
}
