package wsdashboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
	"github.com/kang-sw/devenv/internal/wsstate"
)

var errModelDaemonState = errors.New("daemon state is read-only to model file tools")

// NewModelFileWriteProtection protects the daemon's state directory on
// write calls. Read calls keep the provider credential guard alone.
func NewModelFileWriteProtection() *modelFileWriteProtection {
	p := &modelFileWriteProtection{}
	dir, err := Dir("")
	if err != nil {
		p.stateErr = fmt.Errorf("resolve daemon state directory: %w", err)
		return p
	}
	if !filepath.IsAbs(dir) || networkPath(dir) || deviceElement(dir) {
		p.stateErr = errors.New("daemon state directory is not a local path")
		return p
	}
	p.ProviderCredentialProtection = NewProviderCredentialProtection()
	p.stateDir = filepath.Clean(dir)
	p.stateRoot = &denySet{loaded: true, lower: []string{strings.ToLower(p.stateDir)}}
	return p
}

type modelFileWriteProtection struct {
	*ProviderCredentialProtection
	stateDir    string
	stateRoot   *denySet
	rootLoaded  bool
	files       []os.FileInfo
	filesLoaded bool
	stateErr    error
}

func (p *modelFileWriteProtection) CheckPath(path string) error {
	if p.stateErr != nil {
		return p.stateErr
	}
	if p.DeniesName(path) {
		return errProviderCredentialPath
	}
	if p.stateRoot.containsBasePath(strings.ToLower(filepath.Clean(path))) {
		return errModelDaemonState
	}
	if err := p.ProviderCredentialProtection.CheckPath(path); err != nil {
		return err
	}
	if err := p.loadRootIdentity(); err != nil {
		return err
	}
	if len(p.stateRoot.ids) > 0 && p.stateRoot.aboveDenied(filepath.Clean(path), p.stateRoot.ids) {
		return errModelDaemonState
	}
	return nil
}

func (p *modelFileWriteProtection) CheckOpened(file *os.File, info os.FileInfo) error {
	if p.stateErr != nil {
		return p.stateErr
	}
	if err := p.ProviderCredentialProtection.CheckOpened(file, info); err != nil {
		return err
	}
	if info.IsDir() {
		if err := p.loadRootIdentity(); err != nil {
			return err
		}
		if sameDeniedFile(info, p.stateRoot.ids) {
			return errModelDaemonState
		}
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	// Root protection handles paths into the store. Only a destination
	// with another directory entry can be a hard link to a state file.
	linked, err := wsfs.HasMultipleLinks(file, info)
	if err != nil {
		return fmt.Errorf("cannot verify daemon state file identity: check destination link count: %w", err)
	}
	if !linked {
		return nil
	}
	if !p.filesLoaded {
		p.filesLoaded = true
		if err := p.loadFileIdentities(); err != nil {
			p.stateErr = fmt.Errorf("cannot verify daemon state file identity: %w", err)
			return p.stateErr
		}
	}
	if sameDeniedFile(info, p.files) {
		return errModelDaemonState
	}
	return nil
}

func (p *modelFileWriteProtection) loadRootIdentity() error {
	if p.rootLoaded {
		return p.stateErr
	}
	p.rootLoaded = true
	info, err := os.Stat(p.stateDir)
	if errors.Is(err, os.ErrNotExist) {
		// First use can precede creation of the dashboard directory. A
		// dangling final link is an unresolved directory, not an absent one.
		if _, linkErr := os.Lstat(p.stateDir); errors.Is(linkErr, os.ErrNotExist) {
			return nil
		}
	}
	if err == nil && !info.IsDir() {
		err = errors.New("path is not a directory")
	}
	if err != nil {
		p.stateErr = fmt.Errorf("resolve daemon state directory: %w", err)
		return p.stateErr
	}
	p.stateRoot.ids = append(p.stateRoot.ids, info)
	return nil
}

// Retained name check for the persisted journal directory under each session.
func (p *modelFileWriteProtection) loadFileIdentities() error {
	add := func(path string) error {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil && info.Mode().IsRegular() {
			p.files = append(p.files, info)
		}
		return err
	}
	db := filepath.Join(p.stateDir, wsmeta.DBName)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := add(db + suffix); err != nil {
			return err
		}
	}
	sessions, err := os.ReadDir(SessionDir(p.stateDir, ""))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		dir := SessionDir(p.stateDir, session.Name())
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		inbox := wsinbox.Dir(dir)
		for _, entry := range entries {
			switch strings.ToLower(entry.Name()) {
			case AgentRecordFileName, registrationFileName, SessionPermissionFileName,
				SessionSystemPromptFileName, wslive.SnapshotName:
				if err := add(filepath.Join(dir, entry.Name())); err != nil {
					return err
				}
			case wsstate.WerkJournalDirName:
				journalDir := filepath.Join(dir, entry.Name())
				journals, err := os.ReadDir(journalDir)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				for _, journal := range journals {
					if strings.EqualFold(filepath.Ext(journal.Name()), ".jsonl") {
						if err := add(filepath.Join(journalDir, journal.Name())); err != nil {
							return err
						}
					}
				}
			case filepath.Base(inbox):
				if err := wsinbox.VisitTurnInputPaths(inbox, add); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
