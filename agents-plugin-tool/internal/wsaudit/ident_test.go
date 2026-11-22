package wsaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// TestNormalizeIDKeepsShortLiteralsAndDigestsTheRest pins ring-store
// Decision 3's identifier normalization: a value of at most 256 encoded
// bytes not starting with the digest prefix stays verbatim; a longer one, or
// one spelled like a digest, becomes the digest of its raw bytes, so the
// kept and digested forms never collide.
func TestNormalizeIDKeepsShortLiteralsAndDigestsTheRest(t *testing.T) {
	exact := strings.Repeat("a", IdentifierMaxBytes)
	long := strings.Repeat("a", IdentifierMaxBytes+1)
	// 200 bytes raw, but each '<' encodes as < (6 bytes).
	escaped := strings.Repeat("<", 200)
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"short literal", "toolu_01ABC", "toolu_01ABC"},
		{"exactly the bound", exact, exact},
		{"over the bound", long, digestOf(long)},
		{"over the bound once encoded", escaped, digestOf(escaped)},
		{"spelled like a digest", digestOf(long), digestOf(digestOf(long))},
	}
	for _, c := range cases {
		if got := NormalizeID(c.in); got != c.want {
			t.Errorf("%s: NormalizeID = %q, want %q", c.name, got, c.want)
		}
	}
	// The literal spelled as the digest of long gets its own key.
	if NormalizeID(digestOf(long)) == NormalizeID(long) {
		t.Fatal("a literal digest-shaped ID collides with the digest of another ID")
	}
	if got := NormalizeID(long); len(got) != len(DigestPrefix)+64 {
		t.Fatalf("digest form %q has length %d", got, len(got))
	}
}

func TestSourceIDStringAndPosition(t *testing.T) {
	cases := []struct {
		id   SourceID
		str  string
		posn uint64
	}{
		{SourceID{Source: "h1", Seq: 10, Ordinal: 1}, "h1/s10#1", 10},
		{SourceID{Source: "h1", Tee: true, Anchor: 9, Index: 2}, "h1/t9.2#0", 9},
		{SourceID{Source: "h1", Key: "loss-3"}, "h1/kloss-3#0", 0},
	}
	for _, c := range cases {
		if c.id.String() != c.str || c.id.Position() != c.posn {
			t.Errorf("%+v: String %q Position %d, want %q %d", c.id, c.id.String(), c.id.Position(), c.str, c.posn)
		}
	}
}
