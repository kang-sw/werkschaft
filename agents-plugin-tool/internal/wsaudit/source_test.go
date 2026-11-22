package wsaudit

import "testing"

// TestSourceIDCovered pins the finalized-boundary rule capture and a
// SourcedSink writer share: a sequenced record at or below the boundary is
// covered, a tee record only when anchored below it (it sits after its
// anchor), a keyed record never, and nothing is covered by a zero boundary.
func TestSourceIDCovered(t *testing.T) {
	for _, c := range []struct {
		name string
		id   SourceID
		b    uint64
		want bool
	}{
		{"seq below", SourceID{Seq: 4}, 5, true},
		{"seq at", SourceID{Seq: 5}, 5, true},
		{"seq above", SourceID{Seq: 6}, 5, false},
		{"tee anchored below", SourceID{Tee: true, Anchor: 4, Index: 1}, 5, true},
		{"tee anchored at", SourceID{Tee: true, Anchor: 5, Index: 1}, 5, false},
		{"keyed", SourceID{Key: "call.x", Seq: 1}, 5, false},
		{"zero boundary", SourceID{Seq: 1}, 0, false},
	} {
		if got := c.id.Covered(c.b); got != c.want {
			t.Errorf("%s: %+v.Covered(%d) = %v, want %v", c.name, c.id, c.b, got, c.want)
		}
	}
}
