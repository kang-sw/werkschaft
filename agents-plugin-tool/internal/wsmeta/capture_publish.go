package wsmeta

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// Durable publication of a fence: replacing and removing a legacy file so it survives a crash.

// removeDurably deletes p so that the deletion survives a crash: a rename to
// a dot name in the same directory (write-through on Windows), a directory
// sync, then the unlink. A crash leaves at most the dot name, which no
// discovery reads.
func (s *Store) removeDurably(p string) error {
	var b [8]byte
	_, _ = rand.Read(b[:])
	retired := filepath.Join(filepath.Dir(p), ".wsmeta-retired-"+hex.EncodeToString(b[:])+".tmp")
	if err := wsstate.AtomicReplaceFile(p, retired); err != nil {
		return err
	}
	if err := s.syncDir(filepath.Dir(p)); err != nil {
		return err
	}
	if err := wsstate.RemoveFile(retired); err != nil {
		s.logf("wsmeta: leftover %s: %v", retired, err)
	}
	return nil
}

// distinctFenceSentinel picks a sentinel whose second differs from the live
// file's current mtime. Legacy readers cache a file by its stat (mode, size,
// mtime and identity: wsagent/permission_store.go:85 through
// wsstate.SameFileMetadata). The replacement changes the inode on POSIX, but
// the Windows identity is the creation time, which NTFS tunneling can carry
// over to the replacement. So a fence of the same size must never keep the
// previous mtime. The mtime only needs to differ; no legacy reader of a
// fenced path orders mtimes.
func distinctFenceSentinel(abs string) time.Time {
	sentinel := fenceSentinel()
	if info, err := wsstate.Stat(abs); err == nil {
		for sentinel.Unix() == info.ModTime().Unix() {
			sentinel = fenceSentinel()
		}
	}
	return sentinel
}

// fenceSentinel picks a random even second in [2000, 2010). A variable so a
// test can force a collision.
var fenceSentinel = randomFenceSentinel

func randomFenceSentinel() time.Time {
	const lo, span = 946684800, 315532800 // 2000-01-01, ten years
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := int64(binary.LittleEndian.Uint64(b[:])>>1) % (span / 2)
	return time.Unix(lo+2*n, 0)
}

// publishFile durably replaces abs with data stamped mtime. The temporary
// file's data and mtime are synced before the rename, and the directory
// after it (invariant 5).
func (s *Store) publishFile(abs string, data []byte, mtime time.Time) error {
	f, err := wsstate.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".cutover-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmp, abs); err != nil {
		return err
	}
	ok = true
	if err := s.hit("fence-renamed"); err != nil {
		return err
	}
	return s.syncDir(filepath.Dir(abs))
}

// Durability seams: syncFile flushes a file's data and metadata, and
// syncDirectory makes the entries of a directory durable (sync_*.go).
var (
	syncFile      = syncFileData
	syncDirectory = platformSyncDir
)

func syncFileData(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir is the barrier before a publication is marked landed. It refuses
// a directory whose file system does not support it (barrier.go), so landed
// never rests on an unverified barrier. It runs only after a rename, so its
// refusal is ErrCutoverInterrupted, never the fallback result.
func (s *Store) syncDir(dir string) error {
	if reason := barrierRefusal(dir, false); reason != nil {
		return s.interrupted(reason)
	}
	return syncDirectory(dir)
}

// markFenceLanded marks the exact record this application published (the
// cutover lock keeps it current); any other record is a conflict.
func (s *Store) markFenceLanded(ctx context.Context, rel, kind, digest string, mtime int64, witness string) error {
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET fence_state = 'landed'
			WHERE path = ? AND fence_state = 'intent' AND fence_kind = ? AND fence_digest = ? AND fence_mtime = ? AND fence_witness = ?`,
			rel, kind, digest, mtime, witness)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("%w: %s: its record changed during the publication", ErrFenceConflict, rel)
		}
		return nil
	})
}
