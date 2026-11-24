package wsagent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
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
	root := filepath.Join(r.opts.Dir, retiredPiHistoryDir)
	dest := filepath.Join(root, rec.ReplyID)
	source := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	if info, err := os.Stat(dest); err == nil {
		if !info.IsDir() {
			return errors.New("the retired Pi history path is not a directory")
		}
		// An older daemon can add history between two 0.52 runs. Refresh
		// the recovery copy when the surviving session has changed.
		if changed, err := retiredPiHistoryChanged(source, dest); err != nil || !changed {
			return err
		}
		return stageRetiredPiHistory(source, dest, true)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	return stageRetiredPiHistory(source, dest, false)
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
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), previousPiHistorySuffix)
		if e.IsDir() && wsdashboard.ValidReplyID(id) {
			ids[id] = true
		}
	}
	for id := range ids {
		source := filepath.Join(root, id)
		if err := recoverRetiredPiArchive(source); err != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: recover retired Pi archive: %v\n", id, err)
			continue
		}
		dest := wsdashboard.SessionDir(r.opts.Dir, id)
		if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		rec, err := readRecord(source)
		if err == nil && rec.Backend != BackendPi {
			err = errors.New("expected a retired Pi history record")
		}
		if err == nil {
			err = os.MkdirAll(filepath.Dir(dest), 0o700)
		}
		if err == nil {
			err = stageRetiredPiHistory(source, dest, false)
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

func (r *Runner) discardRetiredPiHistory(rec Record) {
	if rec.Backend != BackendPi {
		return
	}
	archive := filepath.Join(r.opts.Dir, retiredPiHistoryDir, rec.ReplyID)
	if err := errors.Join(os.RemoveAll(archive+stagedPiHistorySuffix), os.RemoveAll(archive+previousPiHistorySuffix), os.RemoveAll(archive)); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: delete retired Pi history: %v\n", rec.ReplyID, err)
	}
}

func retiredPiHistoryFiles() []string {
	registrationName := filepath.Base(wsdashboard.RegistrationPath("", "id"))
	return []string{recordFileName, registrationName, wslive.SnapshotName}
}

func retiredPiHistoryChanged(source, archive string) (bool, error) {
	for _, name := range retiredPiHistoryFiles() {
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
	changed := errors.New("retired Pi ring changed")
	ring := wslive.RingDir(source)
	err := filepath.WalkDir(ring, func(path string, entry fs.DirEntry, err error) error {
		if path == ring && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || entry.IsDir() {
			return err
		}
		name, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if differs, err := retiredPiFileChanged(source, archive, name); err != nil {
			return err
		} else if differs {
			return changed
		}
		return nil
	})
	if err == changed {
		return true, nil
	}
	return false, err
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
	if !prior.Mode().IsRegular() || current.Size() != prior.Size() {
		return true, nil
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

func stageRetiredPiHistory(source, dest string, refresh bool) error {
	stage := dest + stagedPiHistorySuffix
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, name := range retiredPiHistoryFiles() {
		if err := copyRetiredPiFile(source, stage, name, false); err != nil {
			return err
		}
	}
	if _, err := os.Stat(recordPath(stage)); err != nil {
		return err
	}
	if ring, err := os.Lstat(wslive.RingDir(source)); err == nil && ring.IsDir() {
		if err := os.CopyFS(wslive.RingDir(stage), os.DirFS(wslive.RingDir(source))); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if pi, err := os.Lstat(filepath.Join(source, "pi")); err == nil && pi.IsDir() {
		if err := copyRetiredPiFile(source, stage, retiredPiSessionFile, true); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !refresh {
		return os.Rename(stage, dest)
	}
	if err := recoverRetiredPiArchive(dest); err != nil {
		return err
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

func copyRetiredPiFile(source, dest, name string, link bool) error {
	from, to := filepath.Join(source, name), filepath.Join(dest, name)
	info, err := os.Lstat(from)
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
