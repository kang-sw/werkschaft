package wsaudit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestEncodedLenMatchesEncodingJSON pins EncodedLen against encoding/json for
// escapes, HTML characters, line separators, and invalid UTF-8.
func TestEncodedLenMatchesEncodingJSON(t *testing.T) {
	for _, s := range []string{
		"", "plain", "\x00\x01\x1f", "\"\\\n\r\t\b\f", "<a>&b", "  ", "한글", "\xff\xfe", "a\xc3", "\xe2\x80",
		strings.Repeat("\x00", 1000),
	} {
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := EncodedLen(s), len(raw)-2; got != want {
			t.Errorf("EncodedLen(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestNormalizeIDForms pins the kept and digested forms and their
// disjointness.
func TestNormalizeIDForms(t *testing.T) {
	short := "toolu_01"
	if got := NormalizeID(short); got != short {
		t.Fatalf("short ID = %q", got)
	}
	long := strings.Repeat("x", 300)
	digest := NormalizeID(long)
	if !strings.HasPrefix(digest, DigestPrefix) || len(digest) != len(DigestPrefix)+64 {
		t.Fatalf("long ID = %q", digest)
	}
	if NormalizeID(long) != digest {
		t.Fatal("normalization is not deterministic")
	}
	otherLong := strings.Repeat("x", 299) + "y"
	if NormalizeID(otherLong) == digest {
		t.Fatal("long IDs sharing a prefix merged")
	}
	if got := NormalizeID(digest); got == digest || !strings.HasPrefix(got, DigestPrefix) {
		t.Fatalf("a literal digest-shaped ID kept its spelling: %q", got)
	}
	// 256 encoded bytes is kept; one NUL encodes to six bytes.
	edge := strings.Repeat("a", 256)
	if NormalizeID(edge) != edge {
		t.Fatal("256-byte ID was digested")
	}
	if NormalizeID(strings.Repeat("\x00", 43)) == strings.Repeat("\x00", 43) {
		t.Fatal("ID over 256 encoded bytes was kept")
	}
}

// TestSourceRoundTrip pins the optional source identity on the wire.
func TestSourceRoundTrip(t *testing.T) {
	ev, err := NewEnvelope("s", "claude", time.Unix(1, 0), KindToolCall)
	if err != nil {
		t.Fatal(err)
	}
	ev.Source = &SourceID{Source: "holder:3", Seq: 10, Index: 2, Ordinal: 1}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source":{"src":"holder:3","seq":10,"idx":2,"ord":1}`) {
		t.Fatalf("wire = %s", raw)
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Source == nil || *back.Source != *ev.Source {
		t.Fatalf("source = %+v", back.Source)
	}
	ev.Source = nil
	raw, _ = json.Marshal(ev)
	if strings.Contains(string(raw), "source") {
		t.Fatalf("nil source encoded: %s", raw)
	}
}

func TestSourceIDCompare(t *testing.T) {
	a := SourceID{Source: "h", Seq: 10}
	for _, c := range []struct {
		b    SourceID
		want int
	}{
		{SourceID{Source: "h", Seq: 10}, 0},
		{SourceID{Source: "h", Seq: 11}, -1},
		{SourceID{Source: "h", Seq: 10, Index: 1}, -1},
		{SourceID{Source: "h", Seq: 10, Ordinal: 1}, -1},
		{SourceID{Source: "h", Seq: 9, Index: 5}, 1},
	} {
		if got := a.Compare(c.b); got != c.want {
			t.Errorf("Compare(%+v) = %d, want %d", c.b, got, c.want)
		}
	}
}
