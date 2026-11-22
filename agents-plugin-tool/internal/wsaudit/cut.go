package wsaudit

// Cut describes the active model-seen context boundary of a chronological
// audit event sequence.
type Cut struct {
	// Index is the 0-based index where the model's current context begins:
	// the latest KindCompaction event when one exists, 0 when no compaction
	// boundary is present in a non-empty sequence (conversation start), or -1
	// for an empty sequence.
	Index int
	// EventID is the ID of the compaction event that established the cut, or
	// empty when the cut fell back to the conversation start or the sequence
	// is empty.
	EventID string
	// Seq is the sequence number of the cut compaction event, or 0 when there
	// is no compaction boundary.
	Seq uint64
	// HasCompaction reports whether an explicit or inferred compaction event
	// established the cut. When false, the cut falls back to the conversation
	// start.
	HasCompaction bool
	// Estimated reports whether the cut compaction event carries
	// meta["estimated"] == true.
	Estimated bool
}

// FindModelSeenCut locates the latest compaction cut in events (ordered oldest
// first) without allocating or mutating events.
func FindModelSeenCut(events []Envelope) Cut {
	if len(events) == 0 {
		return Cut{Index: -1}
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == KindCompaction {
			estimated, _ := events[i].Meta[MetaEstimated].(bool)
			return Cut{
				Index:         i,
				EventID:       events[i].ID,
				Seq:           events[i].Seq,
				HasCompaction: true,
				Estimated:     estimated,
			}
		}
	}
	return Cut{Index: 0}
}

// ApplyModelSeenCut returns a copy of events (ordered oldest first) with each
// event's ModelSeen set according to the latest compaction boundary: every
// event strictly before the cut is marked false, and the cut event itself plus
// every later event is marked true. Without a compaction boundary, every event
// is marked true and the cut falls back to the conversation start.
//
// ApplyModelSeenCut is pure and does not mutate the input slice.
func ApplyModelSeenCut(events []Envelope) ([]Envelope, Cut) {
	cut := FindModelSeenCut(events)
	if len(events) == 0 {
		return nil, cut
	}
	out := make([]Envelope, len(events))
	copy(out, events)
	for i := range out {
		out[i].ModelSeen = i >= cut.Index
	}
	return out, cut
}
