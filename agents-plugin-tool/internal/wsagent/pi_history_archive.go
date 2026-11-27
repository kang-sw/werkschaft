package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

const (
	retiredPiHistoryDir     = "retired-pi-history"
	previousPiHistorySuffix = ".previous"
	stagedPiHistorySuffix   = ".staged"
)

var retiredPiSessionFile = filepath.Join("pi", "session.jsonl")

// The old registry sweeper walks sessions/ and can remove a retired row's
// entire directory. Publish a complete backup beside sessions/ before close
// can make a Pi record eligible for that sweeper.
func (r *Runner) archiveRetiredPiHistory(rec Record) error {
	if r.piArchiveIDs == nil {
		r.piArchiveIDs = make(map[string]bool)
	}
	r.piArchiveIDs[rec.ReplyID] = true
	if err := r.loadRetiredPiPurges(); err != nil {
		return err
	}
	if _, purging := r.piPurges[rec.ReplyID]; purging {
		return nil
	}
	root := filepath.Join(r.opts.Dir, retiredPiHistoryDir)
	dest := filepath.Join(root, rec.ReplyID)
	source := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	var recBytes []byte
	if r.meta != nil {
		normalized := rec
		if normalized.SchemaVersion == 0 {
			normalized.SchemaVersion = recordSchemaVersion
		}
		raw, err := json.MarshalIndent(normalized, "", "  ")
		if err != nil {
			return err
		}
		recBytes = append(raw, '\n')
	}
	if info, err := os.Stat(dest); err == nil {
		if !info.IsDir() {
			return errors.New("the retired Pi history path is not a directory")
		}
		// An older daemon can add history between two 0.52 runs. Refresh
		// the recovery copy when the surviving session has changed.
		if changed, err := retiredPiHistoryChangedWithRecord(source, dest, recBytes); err != nil || !changed {
			return err
		}
		return stageRetiredPiHistory(source, dest, true, recBytes)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	return stageRetiredPiHistory(source, dest, false, recBytes)
}

func (r *Runner) restoreRetiredPiHistory() {
	root := filepath.Join(r.opts.Dir, retiredPiHistoryDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(r.opts.Log, "agent: read retired Pi history: %v\n", err)
		return
	}
	ids := make(map[string]bool, len(entries))
	r.piArchiveIDs = make(map[string]bool, len(entries))
	for _, e := range entries {
		id := strings.TrimSuffix(strings.TrimSuffix(e.Name(), previousPiHistorySuffix), stagedPiHistorySuffix)
		if !wsdashboard.ValidReplyID(id) {
			continue
		}
		r.piArchiveIDs[id] = true
		if e.IsDir() && !strings.HasSuffix(e.Name(), stagedPiHistorySuffix) {
			ids[id] = true
		}
	}
	if err := r.loadRetiredPiPurges(); err != nil {
		fmt.Fprintf(r.opts.Log, "agent: load retired Pi deletions: %v\n", err)
		return
	}
	for id := range r.piPurges {
		if !r.piArchiveIDs[id] {
			r.finishRetiredPiPurge(id)
		}
	}
	for id := range ids {
		if _, purging := r.piPurges[id]; purging {
			continue
		}
		source := filepath.Join(root, id)
		if err := recoverRetiredPiArchive(source); err != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: recover retired Pi archive: %v\n", id, err)
			continue
		}
		dest := wsdashboard.SessionDir(r.opts.Dir, id)
		_, statErr := os.Stat(dest)
		missingDir := errors.Is(statErr, os.ErrNotExist)
		missingMeta := r.meta != nil && (r.metaSnap == nil || r.metaSnap.Records[id].ReplyID == "")
		if !missingDir && !missingMeta {
			continue
		}
		if statErr != nil && !missingDir {
			continue
		}
		raw, err := os.ReadFile(recordPath(source))
		var rec Record
		if err == nil {
			rec, err = decodeRecord(recordPath(source), id, raw)
		}
		if err == nil && rec.Backend != BackendPi {
			err = errors.New("expected a retired Pi history record")
		}
		if err == nil && missingDir {
			err = os.MkdirAll(filepath.Dir(dest), 0o700)
			if err == nil {
				err = stageRetiredPiHistory(source, dest, false, nil)
			}
		}
		if err == nil && r.meta != nil {
			if missingMeta {
				err = r.meta.PutRecord(context.Background(), id, "", raw)
				if err == nil {
					if r.metaSnap != nil {
						if r.metaSnap.Records == nil {
							r.metaSnap.Records = map[string]wsmeta.AgentRecord{}
						}
						r.metaSnap.Records[id] = wsmeta.AgentRecord{ReplyID: id, JSON: bytes.Clone(raw)}
					}
					r.recordBaseMu.Lock()
					if r.recordBase == nil {
						r.recordBase = map[string][]byte{}
					}
					r.recordBase[id] = bytes.Clone(raw)
					r.recordBaseMu.Unlock()
				}
			}
			if err == nil && missingDir {
				_ = os.Remove(recordPath(dest))
			}
		}
		if err != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: restore retired Pi history: %v\n", id, err)
		}
	}
}

// A refresh first sets aside the last complete snapshot and then publishes
// the new one. An interrupted refresh can recover either complete copy.
func recoverRetiredPiArchive(dest string) error {
	previous := dest + previousPiHistorySuffix
	if _, err := os.Stat(previous); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
		return os.Rename(previous, dest)
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return errors.New("the retired Pi history path is not a directory")
	}
	return os.RemoveAll(previous)
}

func retiredPiHistoryFiles() []string {
	registrationName := filepath.Base(wsdashboard.RegistrationPath("", "id"))
	return []string{recordFileName, registrationName, wslive.SnapshotName}
}

func retiredPiHistoryChanged(source, archive string) (bool, error) {
	return retiredPiHistoryChangedWithRecord(source, archive, nil)
}

func retiredPiHistoryChangedWithRecord(source, archive string, recBytes []byte) (bool, error) {
	for _, name := range retiredPiHistoryFiles() {
		if name == recordFileName && len(recBytes) > 0 {
			saved, err := os.ReadFile(filepath.Join(archive, name))
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			if err != nil {
				return false, err
			}
			if !bytes.Equal(saved, recBytes) {
				return true, nil
			}
			continue
		}
		if changed, err := retiredPiFileChanged(source, archive, name); changed || err != nil {
			return changed, err
		}
	}
	if pi, err := os.Lstat(filepath.Join(source, "pi")); err == nil && pi.IsDir() {
		if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); changed || err != nil {
			return changed, err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	ring := wslive.RingDir(source)
	info, err := os.Lstat(ring)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := wslive.RingNames(ring)
	if err != nil || len(current) == 0 {
		return false, err
	}
	saved, err := wslive.RingNames(wslive.RingDir(archive))
	if err != nil {
		return false, err
	}
	for _, name := range current {
		if len(saved) == wslive.RingHistoryCap && name < saved[0] {
			continue
		}
		if _, exists := slices.BinarySearch(saved, name); !exists {
			return true, nil
		}
	}
	return false, nil
}

func retiredPiFileChanged(source, archive, name string) (bool, error) {
	from, to := filepath.Join(source, name), filepath.Join(archive, name)
	current, err := os.Lstat(from)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current.Mode().IsRegular() {
		return false, fmt.Errorf("retired Pi history file %s is not regular", name)
	}
	prior, err := os.Lstat(to)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil || os.SameFile(current, prior) {
		return false, err
	}
	if !prior.Mode().IsRegular() {
		return true, nil
	}
	if name == retiredPiSessionFile && current.Size() < prior.Size() {
		return false, nil
	}
	if current.Size() != prior.Size() {
		return true, nil
	}
	// Equal size, and the source untouched since the archive copy was written:
	// the bytes are the ones archived. Skips reading a large session.jsonl on
	// every startup; a later in-place write moves the source mtime past the
	// archive's and falls through to the byte comparison.
	if name == retiredPiSessionFile && !current.ModTime().After(prior.ModTime()) {
		return false, nil
	}
	live, err := os.Open(from)
	if err != nil {
		return false, err
	}
	defer live.Close()
	saved, err := os.Open(to)
	if err != nil {
		return false, err
	}
	defer saved.Close()
	var a, b [32 << 10]byte
	for left := current.Size(); left > 0; {
		n := min(left, int64(len(a)))
		if _, err := io.ReadFull(live, a[:n]); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(saved, b[:n]); err != nil {
			return false, err
		}
		if !bytes.Equal(a[:n], b[:n]) {
			return true, nil
		}
		left -= n
	}
	return false, nil
}

func stageRetiredPiHistory(source, dest string, refresh bool, recBytes []byte) error {
	stage := dest + stagedPiHistorySuffix
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var prior string
	if refresh {
		if err := recoverRetiredPiArchive(dest); err != nil {
			return err
		}
		prior = dest
	}
	for _, name := range retiredPiHistoryFiles() {
		if name == recordFileName && len(recBytes) > 0 {
			if err := os.WriteFile(filepath.Join(stage, name), recBytes, 0o600); err != nil {
				return err
			}
			continue
		}
		if err := copyRetiredPiFile(source, prior, stage, name, false); err != nil {
			return err
		}
	}
	if _, err := os.Stat(recordPath(stage)); err != nil {
		return err
	}
	if err := copyRetiredPiRing(source, prior, stage); err != nil {
		return err
	}
	if err := copyRetiredPiJSONL(source, prior, stage); err != nil {
		return err
	}
	if !refresh {
		return os.Rename(stage, dest)
	}
	previous := dest + previousPiHistorySuffix
	if err := os.Rename(dest, previous); err != nil {
		return err
	}
	if err := os.Rename(stage, dest); err != nil {
		return errors.Join(err, os.Rename(previous, dest))
	}
	return os.RemoveAll(previous)
}

func copyRetiredPiRing(source, prior, dest string) error {
	live, stage := wslive.RingDir(source), wslive.RingDir(dest)
	info, err := os.Lstat(live)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && info.IsDir() {
		if prior != "" {
			return wslive.CopyRingUnion(stage, wslive.RingDir(prior), live)
		}
		return os.CopyFS(stage, os.DirFS(live))
	}
	if prior == "" {
		return nil
	}
	saved := wslive.RingDir(prior)
	if _, err := os.Lstat(saved); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.CopyFS(stage, os.DirFS(saved))
}

func copyRetiredPiJSONL(source, prior, dest string) error {
	pi, err := os.Lstat(filepath.Join(source, "pi"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && pi.IsDir() {
		return copyRetiredPiFile(source, prior, dest, retiredPiSessionFile, true)
	}
	if prior != "" {
		return copyRetiredPiFile(prior, "", dest, retiredPiSessionFile, true)
	}
	return nil
}

func copyRetiredPiFile(source, prior, dest, name string, link bool) error {
	from, to := filepath.Join(source, name), filepath.Join(dest, name)
	info, err := os.Lstat(from)
	// A partial prune can leave valid metadata beside missing history.
	if prior != "" && errors.Is(err, os.ErrNotExist) {
		from = filepath.Join(prior, name)
		info, err = os.Lstat(from)
	} else if prior != "" && err == nil && link {
		// Prefer the complete copy when the live JSONL is shorter.
		saved := filepath.Join(prior, name)
		savedInfo, savedErr := os.Lstat(saved)
		if savedErr == nil && savedInfo.Size() > info.Size() {
			from, info = saved, savedInfo
		} else if savedErr != nil && !errors.Is(savedErr, os.ErrNotExist) {
			return savedErr
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("retired Pi history file %s is not regular", name)
	}
	if link {
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		// A surviving holder can append to this daemon-owned file after
		// retirement. Unlinking its session directory leaves those bytes in
		// the archive. Fall back to the retirement snapshot on filesystems
		// that do not support links.
		if err := os.Link(from, to); err == nil {
			return nil
		}
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(out, in, info.Size())
	return errors.Join(err, out.Close())
}
