package wsaudit

import (
	"cmp"
	"context"
)

// SourceID is the durable identity of the source record an envelope came
// from, so a store can recognize a replayed record after a restart.
//
// Capture adapters fill it as: Source "holder:<generation>" with Seq the ring
// sequence for a sequenced line; the same Source with Seq the anchor and
// Index the 1-based interval index for a tee line; or a deterministic Source
// key for a daemon-synthesized event. Ordinal numbers the envelopes one
// source record yields. Within one Source, identities order by (Seq, Index,
// Ordinal).
type SourceID struct {
	Source  string `json:"src"`
	Seq     uint64 `json:"seq,omitempty"`
	Index   uint64 `json:"idx,omitempty"`
	Ordinal uint64 `json:"ord,omitempty"`
}

// Compare orders two identities of the same Source by (Seq, Index, Ordinal).
func (a SourceID) Compare(b SourceID) int {
	if c := cmp.Compare(a.Seq, b.Seq); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Index, b.Index); c != 0 {
		return c
	}
	return cmp.Compare(a.Ordinal, b.Ordinal)
}

// SourceState is what a store recovered for one source.
type SourceState struct {
	// Last is the highest retained (or retention-evicted) source identity.
	Last SourceID `json:"last"`
	// Boundary is the finalized boundary: the highest source sequence the
	// writer finalized, whether kept or dropped. A replayed record at or below
	// it that is not retained counts as loss and is never appended late.
	Boundary uint64 `json:"boundary"`
}

// Recovery is the state a store recovered for one session, which capture
// resumes from after a restart.
type Recovery struct {
	// MaxSeq is the highest envelope Seq the session's store has written.
	// Capture assigns new envelope sequences above it.
	MaxSeq uint64 `json:"max_seq"`
	// Sources holds the last retained identity and the finalized boundary of
	// each source, keyed by SourceID.Source.
	Sources map[string]SourceState `json:"sources,omitempty"`
	// Loss counts source records finalized without being retained (dropped on
	// write error, or replayed at or below the boundary).
	Loss uint64 `json:"loss"`
}

// RecoverableSink is a Sink that also carries the recovery contract capture
// needs across daemon restarts and holder replay.
//
// Envelopes reaching Append carry raw identifier fields; the store applies
// NormalizeID once at ingestion. An envelope whose Source identity is
// already retained, or at or below its source's finalized boundary, is
// skipped rather than appended late.
type RecoverableSink interface {
	Sink
	// Recover returns the session's recovered state, opening its ring on
	// first use.
	Recover(ctx context.Context, session string) (Recovery, error)
	// Finalize advances a source's finalized boundary to seq (never lowers
	// it). Capture calls it when a source interval closes, kept or dropped.
	Finalize(ctx context.Context, session, source string, seq uint64) error
}
