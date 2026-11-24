package wsagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

const retiredPiHistoryDir = "retired-pi-history"

// The old registry sweeper walks sessions/ and can remove a retired row's
// entire directory. Publish a complete backup beside sessions/ before close
// can make a Pi record eligible for that sweeper.
func (r *Runner) archiveRetiredPiHistory(rec Record) error {
	root := filepath.Join(r.opts.Dir, retiredPiHistoryDir)
	dest := filepath.Join(root, rec.ReplyID)
	if info, err := os.Stat(dest); err == nil {
		if info.IsDir() {
			return nil
		}
		return errors.New("the retired Pi history path is not a directory")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	return stageRetiredPiHistory(wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID), dest)
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
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || !wsdashboard.ValidReplyID(id) {
			continue
		}
		dest := wsdashboard.SessionDir(r.opts.Dir, id)
		if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		source := filepath.Join(root, id)
		rec, err := readRecord(source)
		if err == nil && rec.Backend != BackendPi {
			err = errors.New("expected a retired Pi history record")
		}
		if err == nil {
			err = os.MkdirAll(filepath.Dir(dest), 0o700)
		}
		if err == nil {
			err = stageRetiredPiHistory(source, dest)
		}
		if err != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: restore retired Pi history: %v\n", id, err)
		}
	}
}

func (r *Runner) discardRetiredPiHistory(rec Record) {
	if rec.Backend != BackendPi {
		return
	}
	if err := os.RemoveAll(filepath.Join(r.opts.Dir, retiredPiHistoryDir, rec.ReplyID)); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: delete retired Pi history: %v\n", rec.ReplyID, err)
	}
}

func stageRetiredPiHistory(source, dest string) error {
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".pi-history-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	registrationName := filepath.Base(wsdashboard.RegistrationPath("", filepath.Base(source)))
	for _, name := range []string{recordFileName, registrationName, wslive.SnapshotName} {
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
		if err := copyRetiredPiFile(source, stage, filepath.Join("pi", "session.jsonl"), true); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(stage, dest)
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
