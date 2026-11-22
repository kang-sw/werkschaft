package wsaudit

import (
	"context"
	"errors"
	"strconv"
)

// Meta keys capture adapters write on assembled and loss events.
const (
	// MetaIncomplete marks an assembled event some of whose source lines were
	// lost (dropped tee lines, an unfenced interval, a stream that ended
	// mid-block).
	MetaIncomplete = "incomplete"
	// MetaLost counts the source lines known lost on an incomplete event or a
	// loss marker; absent when the count is unknown.
	MetaLost = "lost"
	// MetaLossReason names why a loss marker or incomplete event lost lines.
	MetaLossReason = "loss_reason"
	// MetaAssembled marks an event built from streamed partial lines (tee'd
	// deltas) rather than from one complete harness line.
	MetaAssembled = "assembled"
)

// SubkindLoss is the Subkind of a standalone KindSystem loss marker: lost
// source lines that no assembled event spans.
const SubkindLoss = "audit_loss"

// Loss reasons.
const (
	LossDropped      = "dropped"       // the holder dropped tee lines (no daemon, full buffer)
	LossDisconnect   = "disconnect"    // the holder connection ended before the interval's fence
	LossFenceTimeout = "fence_timeout" // no fence within the bound; the source's tee stream closed
	LossLate         = "late"          // a tee line arrived after its interval was finalized
	LossQueueFull    = "queue_full"    // the daemon's capture queue was full
	LossStreamEnd    = "stream_end"    // the stream ended inside an assembled block
	LossAppendFailed = "append_failed" // finalized at or below the boundary without a retained identity
)

// SourceID is the durable identity of the harness record an envelope came
// from (ticket 261119-feat-ws-audit-capture-harness-adapters Decision 5):
//
//   - a sequenced line: Source (the holder generation) and Seq (its holder
//     ring sequence);
//   - a tee line: Source, Tee, Anchor (the ring sequence of the last line the
//     holder kept before it) and Index (its 1-based place in that interval,
//     dropped lines counted);
//   - a daemon-synthesized event: Source and a deterministic Key.
//
// Ordinal tells apart the envelopes one source record yields.
type SourceID struct {
	Source  string `json:"source"`
	Seq     uint64 `json:"seq,omitempty"`
	Tee     bool   `json:"tee,omitempty"`
	Anchor  uint64 `json:"anchor,omitempty"`
	Index   uint64 `json:"index,omitempty"`
	Key     string `json:"key,omitempty"`
	Ordinal int    `json:"ordinal,omitempty"`
}

// String is a stable text form of id, usable as an envelope ID.
func (id SourceID) String() string {
	s := id.Source + "/"
	switch {
	case id.Key != "":
		s += "k" + id.Key
	case id.Tee:
		s += "t" + strconv.FormatUint(id.Anchor, 10) + "." + strconv.FormatUint(id.Index, 10)
	default:
		s += "s" + strconv.FormatUint(id.Seq, 10)
	}
	return s + "#" + strconv.Itoa(id.Ordinal)
}

// Position is the source sequence id occupies for the finalized-boundary
// rule: a sequenced line's Seq, a tee line's Anchor (it sits after that
// line), 0 for a synthesized event.
func (id SourceID) Position() uint64 {
	if id.Tee {
		return id.Anchor
	}
	return id.Seq
}

// Recovery is what a store retained for one session and source when capture
// (re)opens it.
type Recovery struct {
	// MaxSeq is the highest envelope seq retained for the session; capture
	// assigns seq above it.
	MaxSeq uint64
	// Boundary is the highest source sequence of this source that capture
	// finalized, kept or dropped. A replayed record at or below it whose
	// identity is not retained is recorded as loss, never appended late.
	Boundary uint64
}

// ErrDuplicateSource is returned by SourcedSink.AppendSourced for an
// identity already retained; capture treats it as success.
var ErrDuplicateSource = errors.New("wsaudit: source identity already retained")

// SourcedSink is a Sink that also keeps source identities, so capture can
// resume across a daemon restart and a holder replay without duplicating or
// reordering events. It extends the recovery contract of Sink.Append; like
// Append, every method returns promptly and never waits on disk I/O.
type SourcedSink interface {
	Sink
	// Recover reports what is retained for session and source.
	Recover(session, source string) Recovery
	// Retained reports whether an envelope with identity id is retained for
	// session.
	Retained(session string, id SourceID) bool
	// AppendSourced appends ev under identity id.
	AppendSourced(ctx context.Context, id SourceID, ev Envelope) error
	// Finalize records the source's finalized boundary (Recovery.Boundary).
	Finalize(ctx context.Context, session, source string, boundary uint64) error
	// RecordLoss records that the envelope id was finalized without being
	// retained.
	RecordLoss(ctx context.Context, session string, id SourceID, reason string) error
}
