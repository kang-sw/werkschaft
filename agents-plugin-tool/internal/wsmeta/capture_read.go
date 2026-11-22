package wsmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// Reading one bundled legacy file and classifying how the read failed.

// legacyRead is one bundled file as discovery found it.
type legacyRead struct {
	state  string
	bytes  []byte
	digest string
	err    string
	mtime  time.Time // of the file (or link target) discovery stat'ed
	// transient marks a read that failed for a reason a retry may clear;
	// state is then unset and the previous capture row, if any, stands.
	transient bool
	// confirmable marks a transient failure that reads as permanent once it
	// persists (readClass).
	confirmable bool
}

// readLegacy reads one bundled file. A dangling symbolic link, a directory
// in its place or a stable read error is an existing, unreadable input, kept
// apart from absence (permission selection fails closed on it). Any other
// read error (a sharing violation outliving the retry, an I/O error) is
// transient: the pass neither captures nor imports it.
func readLegacy(abs string, readFile func(string) ([]byte, error)) legacyRead {
	info, err := wsstate.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return legacyRead{state: CaptureAbsent, digest: absentDigest}
	}
	if err != nil {
		return readFailure(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		// A link counts by its target: dangling is an existing, unreadable
		// selection; anything but a regular file is never opened.
		target, err := wsstate.Stat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return legacyRead{state: CaptureUnreadable, err: "dangling symbolic link"}
		case err != nil:
			return readFailure(err)
		}
		info = target
	}
	if !info.Mode().IsRegular() {
		return legacyRead{state: CaptureUnreadable, err: "not a regular file"}
	}
	data, err := readFile(abs)
	if err != nil {
		switch {
		case errors.Is(err, errNotRegular):
			return legacyRead{state: CaptureUnreadable, err: "not a regular file"}
		case errors.Is(err, fs.ErrNotExist):
			// Removed (or its link target removed) after the stat: read as
			// changed, and re-read by the next pass.
			return legacyRead{transient: true, err: err.Error()}
		}
		return readFailure(err)
	}
	if data == nil {
		data = []byte{}
	}
	return legacyRead{state: CapturePresent, bytes: data, digest: contentDigest(data), mtime: info.ModTime()}
}

// denialConfirmable reports whether a confirmable denial may become, or stay,
// a row's confirmed unreadable state. A confirmed failure never replaces an
// original read before (a Windows pending delete can outlast the
// confirmation window), nor an absent original under a fence record (the
// live file may be the fence's result). An unreadable original stays
// confirmed even under a fence record: the migration can always read its own
// publication, so a denied live file is still that original.
func denialConfirmable(had bool, prev captureMeta) bool {
	return !had || prev.state == CaptureUnreadable || prev.state == CaptureAbsent && prev.fenceState == FenceNone
}

// readClass sorts a read error: transient errors may clear on a later pass;
// a stable one is the file's state; a confirmable one is transient until it
// persists across UnreadConfirmPasses passes spanning UnreadConfirmAfter,
// then reads as stable.
type readClass int

const (
	classTransient readClass = iota
	classStable
	classConfirmable
)

// classifyReadError is the platform's read error classification (a test
// seam).
var classifyReadError = platformReadErrorClass

// A confirmable read failure (Windows access denied: an ACL, or a pending
// delete) becomes a permanent, unreadable input once it persisted across this
// many staging passes and this long, so a selector its ACL denies fails
// closed for its own session instead of blocking every session's import.
const (
	UnreadConfirmPasses = 3
	UnreadConfirmAfter  = 5 * time.Second
)

func readFailure(err error) legacyRead {
	switch classifyReadError(err) {
	case classStable:
		return legacyRead{state: CaptureUnreadable, err: err.Error()}
	case classConfirmable:
		return legacyRead{transient: true, confirmable: true, err: err.Error()}
	}
	return legacyRead{transient: true, err: err.Error()}
}

// errNotRegular reports a bundled path that opened as something other than a
// regular file.
var errNotRegular = errors.New("not a regular file")

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
