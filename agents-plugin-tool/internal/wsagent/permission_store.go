package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
	"github.com/kang-sw/devenv/internal/wsstate"
)

type permissionSelection struct {
	mu sync.Mutex
	// admission orders permission changes with file and exec admission and
	// with descendant creation/start checks that use this session as parent.
	admission sync.RWMutex

	loaded    bool
	level     PermissionLevel
	explicit  bool
	delegated bool
	// delegatedSet distinguishes a new generic worker's explicit false from
	// a pre-provenance sidecar, whose missing member must retain old behavior.
	delegatedSet bool
	// rawBody preserves unknown JSON members across SQLite permission updates.
	rawBody []byte
	err     error
	// info is permission.json's stat taken before the last read, infoAt the
	// clock before that stat, and absent that neither the file nor a link
	// existed then; revalidatePermissionSelection compares them.
	info   os.FileInfo
	infoAt time.Time
	absent bool
}

// permissionRacyWindow is how recent a selection's mtime may be, relative to
// the stat that recorded it, and still be distrusted: a same-size rewrite
// within one coarse timestamp tick keeps the stat unchanged.
const permissionRacyWindow = 2 * time.Second

var permissionSelections sync.Map // session directory -> *permissionSelection

func permissionSlot(dir string) *permissionSelection {
	v, _ := permissionSelections.LoadOrStore(dir, new(permissionSelection))
	return v.(*permissionSelection)
}

func cachedPermissionSelection(dir string) (PermissionLevel, bool, error) {
	slot := permissionSlot(dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.loaded {
		slot.level, slot.explicit, slot.delegated, slot.delegatedSet, slot.err = readPermissionSelectionState(dir)
		slot.loaded = true
	}
	return slot.level, slot.explicit, slot.err
}

// refreshPermissionSelection is for explicit API/admission reads, not spawn
// projection. A new launch must never trust an earlier cached permission.
func refreshPermissionSelection(dir string) (PermissionLevel, bool, error) {
	slot := permissionSlot(dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.loadLocked(dir)
	return slot.level, slot.explicit, slot.err
}

// revalidatePermissionSelection is the hosted page's explicit state read
// (Runner.Get): it re-reads the selection only when permission.json's stat
// moved since the last read, so a page polling an unchanged lead opens no
// file. Another process may still edit the file: any change of identity,
// size, mode or mtime, or a recent mtime (permissionRacyWindow), re-reads.
// Launch admission keeps refreshPermissionSelection's unconditional read.
func revalidatePermissionSelection(dir string) {
	slot := permissionSlot(dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.loaded {
		info, absent := statPermissionSelection(dir)
		if absent && slot.absent {
			return
		}
		if info != nil && wsstate.SameFileMetadata(slot.info, info) && info.ModTime().Before(slot.infoAt.Add(-permissionRacyWindow)) {
			return
		}
	}
	slot.loadLocked(dir)
}

// loadLocked reads the selection, recording the stat taken before the read:
// a write between the two leaves a stat that the next revalidation finds
// moved. Caller holds slot.mu.
func (slot *permissionSelection) loadLocked(dir string) {
	slot.infoAt = time.Now()
	slot.info, slot.absent = statPermissionSelection(dir)
	slot.level, slot.explicit, slot.delegated, slot.delegatedSet, slot.err = readPermissionSelectionState(dir)
	slot.loaded = true
}

// statPermissionSelection stats permission.json. absent reports that
// neither the file nor a dangling link exists (readPermissionMembers treats a
// dangling link as an unreadable selection, not omission).
func statPermissionSelection(dir string) (info os.FileInfo, absent bool) {
	path := filepath.Join(dir, wsdashboard.SessionPermissionFileName)
	info, err := wsstate.Stat(path)
	if err == nil {
		return info, false
	}
	if errors.Is(err, os.ErrNotExist) {
		if _, lerr := wsstate.Lstat(path); errors.Is(lerr, os.ErrNotExist) {
			return nil, true
		}
	}
	return nil, false
}

// readPermissionMembers distinguishes absence from an invalid existing selection.
func readPermissionMembers(dir string) (map[string]json.RawMessage, PermissionLevel, bool, error) {
	path := filepath.Join(dir, wsdashboard.SessionPermissionFileName)
	raw, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// A dangling link is an existing, unreadable selection, not omission.
		if _, statErr := wsstate.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			return nil, "", false, nil
		}
	}
	if err != nil {
		return nil, "", true, fmt.Errorf("read permission selection: %w", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, "", true, fmt.Errorf("parse permission selection: %w", err)
	}
	var level PermissionLevel
	if members == nil || json.Unmarshal(members["permission_level"], &level) != nil || level.rank() == 0 {
		return nil, "", true, fmt.Errorf("invalid permission_level in %s", path)
	}
	return members, level, true, nil
}

func readPermissionSelection(dir string) (level PermissionLevel, explicit bool, err error) {
	level, explicit, _, _, err = readPermissionSelectionState(dir)
	return
}

func readPermissionSelectionState(dir string) (level PermissionLevel, explicit, delegated, delegatedSet bool, err error) {
	members, level, explicit, err := readPermissionMembers(dir)
	if err != nil || !explicit {
		return level, explicit, false, false, err
	}
	if raw, ok := members["delegated_authority"]; ok {
		delegatedSet = true
		if err := json.Unmarshal(raw, &delegated); err != nil {
			return "", true, false, true, fmt.Errorf("invalid delegated_authority in %s", filepath.Join(dir, wsdashboard.SessionPermissionFileName))
		}
	}
	return level, true, delegated, delegatedSet, nil
}

// delegatedPermission reports immutable agent.spawn provenance from the same
// cached sidecar read that resolved the explicit worker level.
func delegatedPermission(dir string) bool {
	delegated, _ := delegatedPermissionState(dir)
	return delegated
}

func delegatedPermissionState(dir string) (delegated, set bool) {
	slot := permissionSlot(dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.loaded {
		slot.loadLocked(dir)
	}
	if slot.err != nil || !slot.explicit {
		return false, false
	}
	return slot.delegated, slot.delegatedSet
}

// writePermissionSelection persists an already authorized choice. The caller's
// conversation loop and admission lock serialize authorization, selection
// updates and descendant admission.
func writePermissionSelection(dir string, level PermissionLevel) error {
	return writePermissionSelectionAs(dir, level, false, false)
}

// writeDelegatedPermissionSelection persists agent.spawn provenance beside
// its explicit authority. Generic controls retain but cannot create this bit.
func writeDelegatedPermissionSelection(dir string, level PermissionLevel) error {
	return writePermissionSelectionAs(dir, level, true, true)
}

// writeGenericPermissionSelection records an explicit non-agent.spawn origin,
// so absence remains available as the pre-provenance compatibility signal.
func writeGenericPermissionSelection(dir string, level PermissionLevel) error {
	return writePermissionSelectionAs(dir, level, false, true)
}

func writePermissionSelectionAs(dir string, level PermissionLevel, delegated, setDelegated bool) error {
	slot := permissionSlot(dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.loaded = false
	if level.rank() == 0 {
		return fmt.Errorf("invalid permission level %q", level)
	}
	members, _, _, err := readPermissionMembers(dir)
	if err != nil {
		return err
	}
	if members == nil {
		members = make(map[string]json.RawMessage)
	}
	members["permission_level"], _ = json.Marshal(level)
	storedDelegated, storedDelegatedSet := false, false
	if raw, ok := members["delegated_authority"]; ok {
		storedDelegatedSet = true
		if err := json.Unmarshal(raw, &storedDelegated); err != nil {
			return fmt.Errorf("invalid delegated_authority in %s", filepath.Join(dir, wsdashboard.SessionPermissionFileName))
		}
	}
	if setDelegated {
		members["delegated_authority"], _ = json.Marshal(delegated)
		storedDelegated, storedDelegatedSet = delegated, true
	}
	raw, err := json.MarshalIndent(members, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, wsdashboard.SessionPermissionFileName), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	slot.delegated = storedDelegated
	slot.delegatedSet = storedDelegatedSet
	slot.level, slot.explicit, slot.err, slot.loaded = level, true, nil, true
	slot.info, slot.absent = nil, false // the next revalidation re-reads once
	return nil
}

func (r *Runner) permissionSlot(id, dir string) *permissionSelection {
	if r == nil || r.meta == nil {
		return permissionSlot(dir)
	}
	v, _ := r.perms.LoadOrStore(id, &permissionSelection{loaded: true})
	return v.(*permissionSelection)
}

func (r *Runner) cachedPermissionSelection(id, dir string) (PermissionLevel, bool, error) {
	if r == nil || r.meta == nil {
		return cachedPermissionSelection(dir)
	}
	slot := r.permissionSlot(id, dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.level, slot.explicit, slot.err
}

func (r *Runner) refreshPermissionSelection(id, dir string) (PermissionLevel, bool, error) {
	if r == nil || r.meta == nil {
		return refreshPermissionSelection(dir)
	}
	// Under SQLite authority, this daemon is the sole writer and keeps r.perms
	// synchronized with SQLite without reading permission.json on disk.
	return r.cachedPermissionSelection(id, dir)
}

func (r *Runner) revalidatePermissionSelection(id, dir string) {
	if r == nil || r.meta == nil {
		revalidatePermissionSelection(dir)
	}
}

func (r *Runner) delegatedPermissionState(id, dir string) (delegated, set bool) {
	if r == nil || r.meta == nil {
		return delegatedPermissionState(dir)
	}
	slot := r.permissionSlot(id, dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.err != nil || !slot.explicit {
		return false, false
	}
	return slot.delegated, slot.delegatedSet
}

func (r *Runner) detachedWorkerFallback(id, dir, source string) bool {
	if source == "legacy-derived" {
		return true
	}
	delegated, set := r.delegatedPermissionState(id, dir)
	return delegated || !set
}

func (r *Runner) childLevel(id, dir string) PermissionLevel {
	level, explicit, err := r.cachedPermissionSelection(id, dir)
	if err != nil || !explicit {
		return ""
	}
	return level
}

func (r *Runner) writePermissionSelection(id, dir string, level PermissionLevel) error {
	return r.writePermissionSelectionAs(id, dir, level, false, false)
}

func (r *Runner) writeDelegatedPermissionSelection(id, dir string, level PermissionLevel) error {
	return r.writePermissionSelectionAs(id, dir, level, true, true)
}

func (r *Runner) writeGenericPermissionSelection(id, dir string, level PermissionLevel) error {
	return r.writePermissionSelectionAs(id, dir, level, false, true)
}

func (r *Runner) writePermissionSelectionAs(id, dir string, level PermissionLevel, delegated, setDelegated bool) error {
	if r == nil || r.meta == nil {
		return writePermissionSelectionAs(dir, level, delegated, setDelegated)
	}
	slot := r.permissionSlot(id, dir)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if level.rank() == 0 {
		return fmt.Errorf("invalid permission level %q", level)
	}
	if slot.err != nil {
		return slot.err
	}
	var members map[string]json.RawMessage
	if len(slot.rawBody) > 0 {
		if err := json.Unmarshal(slot.rawBody, &members); err != nil {
			return fmt.Errorf("parse permission selection: %w", err)
		}
	}
	if members == nil {
		members = make(map[string]json.RawMessage)
	}
	members["permission_level"], _ = json.Marshal(level)
	storedDelegated, storedDelegatedSet := slot.delegated, slot.delegatedSet
	if setDelegated {
		members["delegated_authority"], _ = json.Marshal(delegated)
		storedDelegated, storedDelegatedSet = delegated, true
	} else if storedDelegatedSet {
		if _, ok := members["delegated_authority"]; !ok {
			members["delegated_authority"], _ = json.Marshal(storedDelegated)
		}
	}
	raw, err := json.Marshal(members)
	if err != nil {
		return err
	}
	if err := r.meta.PutPermission(context.Background(), id, wsmeta.Permission{
		State:        "valid",
		Level:        string(level),
		DelegatedSet: storedDelegatedSet,
		Delegated:    storedDelegated,
		Body:         raw,
	}); err != nil {
		return err
	}
	slot.delegated = storedDelegated
	slot.delegatedSet = storedDelegatedSet
	slot.rawBody = bytes.Clone(raw)
	slot.level, slot.explicit, slot.err, slot.loaded = level, true, nil, true
	return nil
}
