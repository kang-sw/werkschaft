package wsauditstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// RingSchemaVersion is the on-disk audit ring format version, written in
// every segment's header line. A store refuses to write a ring holding a
// newer version (Decision 4).
const RingSchemaVersion = 1

// ErrNewerFormat refuses a write to a ring a newer binary wrote.
var ErrNewerFormat = errors.New("wsauditstore: audit ring has a newer format")

var (
	segmentPattern    = regexp.MustCompile(`^seg-([0-9]{16})\.jsonl$`)
	literalDirPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

func segmentName(seq uint64) string { return fmt.Sprintf("seg-%016d.jsonl", seq) }

// sessionDirName maps a normalized session ID to its ring directory name: the
// ID itself when it is a short portable file name, otherwise "~" and a digest
// ('~' never appears in a literal name, so the two forms cannot collide).
func sessionDirName(session string) string {
	if literalDirPattern.MatchString(session) && session != "." && session != ".." {
		return session
	}
	sum := sha256.Sum256([]byte(session))
	return "~" + hex.EncodeToString(sum[:16])
}

// line is one JSONL record of a segment.
type line struct {
	T string `json:"t"`
	// Header ("h"): the format version, the session, and the recovery state
	// as of the segment's creation, so the oldest retained segment alone
	// seeds recovery after older ones were dropped.
	V       int          `json:"v,omitempty"`
	Session string       `json:"session,omitempty"`
	State   *headerState `json:"state,omitempty"`
	// Event ("e"): a retained envelope and, when it has one, its source
	// identity.
	Event *wsaudit.Envelope `json:"e,omitempty"`
	ID    *wsaudit.SourceID `json:"id,omitempty"`
	// Loss ("loss"): the source record ID finalized without being retained,
	// why, and the session's loss count with it (N), so replay takes the
	// count as a maximum and never counts a line twice — not even one whose
	// identity a later header no longer lists.
	Reason string `json:"reason,omitempty"`
	N      uint64 `json:"n,omitempty"`
	// Boundary ("fin"): a source's finalized boundary (Recovery.Boundary).
	Source string `json:"src,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`
	At     string `json:"at,omitempty"`
}

const (
	lineHeader = "h"
	lineEvent  = "e"
	lineLoss   = "loss"
	lineFin    = "fin"
)

type headerState struct {
	MaxSeq     uint64            `json:"max_seq,omitempty"`
	Boundaries map[string]uint64 `json:"boundaries,omitempty"`
	Loss       uint64            `json:"loss,omitempty"`
	// Lost lists the lost identities already counted in Loss, covered by a
	// boundary or not, so neither a replayed loss line nor a replayed record
	// counts one again once the segment holding its loss line is gone.
	Lost []wsaudit.SourceID `json:"lost,omitempty"`
	// LostFloor is, per source, the sequence at or below which every
	// sequenced identity not retained is already counted lost (see
	// ledger.floor).
	LostFloor map[string]uint64 `json:"lost_floor,omitempty"`
	LastSeen  string            `json:"last_seen,omitempty"`
}

// lostBudget bounds the encoded size of a header's Lost list; past it a
// contiguous accounted prefix folds into LostFloor, and what still does not
// fit is forgotten (see ledger.fold).
const lostBudget = 32 << 10

// sourceBudget bounds the encoded size of a header's per-source maps
// (Boundaries and LostFloor); past it the least recently finalized sources
// are dropped (see ledger.boundSources). With lostBudget and room for the
// session ID and scalars it keeps every header within HeaderBound.
const sourceBudget = int(HeaderBound) - lostBudget - 4<<10

// maxReasonBytes bounds a loss line's reason; a longer one is cut on a rune
// boundary, so a loss line always fits a segment beside its header.
const maxReasonBytes = 256

type segment struct {
	seq    uint64
	size   int64
	events int // retained events that live in this segment
}

// ring is one session's audit segment ring with its in-memory index. The
// store's writer goroutine is its only mutator; reads take the session's mu
// (events) or the ledger's mu (identities).
type ring struct {
	session string
	dir     string

	segs        []segment
	file        *os.File // newest segment, held open for appends
	forceRotate bool
	readOnly    error

	// view guards events and ids, the in-memory index readers copy from. The
	// writer takes it only around in-memory changes, never across disk I/O.
	view   sync.RWMutex
	events []wsaudit.Envelope
	ids    []*wsaudit.SourceID // parallel to events; nil for an unsourced event
	// cut is set (under view) once this ring is known to have dropped older
	// segments to capacity eviction: its oldest segment on disk at recovery
	// was not the first, or it dropped one since. Time retention and session
	// deletion remove the whole directory, so a ring started afterwards
	// begins uncut.
	cut bool
	led *ledger
	// Control lines written ahead of the next record, or when the writer
	// idles: loss lines in order, and the latest boundary per source.
	pendingLoss [][]byte
	pendingFin  map[string]uint64
	lastSeen    time.Time
	bytes       int64
	// evictErr is the first segment deletion that failed since the store
	// last looked; the segment and its bytes stay accounted.
	evictErr error
}

func newRing(session, dir string) *ring {
	return &ring{session: session, dir: dir, led: newLedger(), pendingFin: map[string]uint64{}}
}
