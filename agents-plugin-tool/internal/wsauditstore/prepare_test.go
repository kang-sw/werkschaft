package wsauditstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

func testEnvelope(t *testing.T, kind wsaudit.Kind) wsaudit.Envelope {
	t.Helper()
	ev, err := wsaudit.NewEnvelope("sess-1", "claude", time.Date(2026, 11, 22, 1, 2, 3, 4, time.UTC), kind)
	if err != nil {
		t.Fatal(err)
	}
	ev.ID = "ev-1"
	return ev
}

func intp(v int) *int { return &v }

// TestPrepareRedactsKeepingRuleAndLength pins Decision 2: bodies are masked,
// each redaction keeps the rule name and original length, and the original
// is not in the stored record.
func TestPrepareRedactsKeepingRuleAndLength(t *testing.T) {
	ev := testEnvelope(t, wsaudit.KindToolCall)
	secret := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	ev.In = "gh auth " + secret
	ev.Out = "PASSWORD=hunter2"
	got, err := prepare(ev, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got.In != "gh auth ***" || got.Out != "PASSWORD=***" {
		t.Fatalf("bodies = %q, %q", got.In, got.Out)
	}
	want := []wsaudit.Redaction{
		{Field: "in", Offset: 8, Rule: "github_token", Length: len(secret)},
		{Field: "out", Offset: 9, Rule: "secret_assignment", Length: len("hunter2")},
	}
	if fmt.Sprint(got.Redactions) != fmt.Sprint(want) {
		t.Fatalf("redactions = %+v", got.Redactions)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "hunter2") {
		t.Fatalf("stored record holds the original: %s", raw)
	}
}

// TestPrepareSecretStraddlingCutIsMaskedBeforeCap pins the order: redaction
// runs on the full body before the per-body cap, so a secret across the cut
// leaves no unredacted fragment, and its offset refers to the pre-cap body.
func TestPrepareSecretStraddlingCutIsMaskedBeforeCap(t *testing.T) {
	lim := DefaultLimits()
	lim.BodyBytes = 8 * kib
	secret := "ghp_" + strings.Repeat("Z", 60)
	head := strings.Repeat("a", 4*1024-21) + " " // the secret starts just before the head share ends
	body := head + secret + " " + strings.Repeat("b", 20*1024)
	ev := testEnvelope(t, wsaudit.KindToolResult)
	ev.Out = body
	got, err := prepare(ev, lim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Out, "ghp_") || strings.Contains(got.Out, "ZZZZ") {
		t.Fatalf("a fragment of the secret survived the cut")
	}
	if got.StoredTrunc == nil || got.StoredTrunc.Out == nil {
		t.Fatal("no stored_trunc marker")
	}
	if len(got.Redactions) != 1 || got.Redactions[0].Offset != len(head) || got.Redactions[0].Length != len(secret) {
		t.Fatalf("redactions = %+v", got.Redactions)
	}
	tr := got.StoredTrunc.Out
	if tr.OriginalBytes != len(body)-len(secret)+3 {
		t.Fatalf("original bytes %d is not the redacted pre-cap length", tr.OriginalBytes)
	}
}

// TestPrepareCapKeepsHeadTailAndMarker pins Decision 9: an oversized body
// keeps its head and tail with a marker; an under-cap body stays whole.
func TestPrepareCapKeepsHeadTailAndMarker(t *testing.T) {
	lim := DefaultLimits()
	lim.BodyBytes = 8 * kib
	ev := testEnvelope(t, wsaudit.KindToolResult)
	ev.Out = "HEAD" + strings.Repeat("m", 20*1024) + "TAIL"
	ev.In = strings.Repeat("s", 8*1024)
	got, err := prepare(ev, lim)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Out, "HEAD") || !strings.HasSuffix(got.Out, "TAIL") || len(got.Out) != 8*1024 {
		t.Fatalf("stored out has %d bytes", len(got.Out))
	}
	tr := got.StoredTrunc.Out
	if tr.HeadBytes != 4096 || tr.TailBytes != 4096 || tr.OriginalBytes != len(ev.Out) {
		t.Fatalf("marker = %+v", tr)
	}
	if got.In != ev.In || got.StoredTrunc.In != nil {
		t.Fatal("an under-cap body was cut")
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestPrepareNULHeavyBodyFitsEncodedShares pins (2129)=a (1): the cap
// measures encoded bytes, so a NUL body (six bytes each in JSON) is cut so
// each half fits its encoded share.
func TestPrepareNULHeavyBodyFitsEncodedShares(t *testing.T) {
	lim := DefaultLimits()
	ev := testEnvelope(t, wsaudit.KindToolResult)
	ev.Out = strings.Repeat("\x00", 1<<20) + "<&>" + strings.Repeat(" ", 1000)
	got, err := prepare(ev, lim)
	if err != nil {
		t.Fatal(err)
	}
	tr := got.StoredTrunc.Out
	head, tail := got.Out[:tr.HeadBytes], got.Out[tr.HeadBytes:]
	if h := wsaudit.EncodedLen(head); h > int(lim.BodyBytes/2) {
		t.Fatalf("head encodes to %d bytes", h)
	}
	if n := wsaudit.EncodedLen(tail); n > int(lim.BodyBytes/2) || len(tail) != tr.TailBytes {
		t.Fatalf("tail encodes to %d bytes", n)
	}
	raw, _ := json.Marshal(got.Out)
	if len(raw)-2 > int(lim.BodyBytes) {
		t.Fatalf("stored body encodes to %d bytes", len(raw)-2)
	}
}

// TestPrepareSeenBoundaryThroughMaskAndCut pins Decision 9: a model-seen
// boundary is carried through masking (one inside a masked span maps to the
// mask's end) and through the cut into the stored body.
func TestPrepareSeenBoundaryThroughMaskAndCut(t *testing.T) {
	ev := testEnvelope(t, wsaudit.KindToolResult)
	ev.Out = "ab PASSWORD=hunter2 cd ef"
	ev.Seen = &wsaudit.SeenBounds{Out: intp(15)} // inside "hunter2"
	got, err := prepare(ev, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got.Out != "ab PASSWORD=*** cd ef" || got.Seen == nil || *got.Seen.Out != 15 {
		t.Fatalf("out %q seen %+v", got.Out, got.Seen)
	}
	ev.Seen = &wsaudit.SeenBounds{Out: intp(21)} // " cd" → after the mask
	got, _ = prepare(ev, DefaultLimits())
	if *got.Seen.Out != 17 {
		t.Fatalf("seen after mask = %d", *got.Seen.Out)
	}
	ev.Seen = &wsaudit.SeenBounds{Out: intp(len(ev.Out))}
	got, _ = prepare(ev, DefaultLimits())
	if got.Seen != nil {
		t.Fatalf("a fully seen body kept a bound: %+v", got.Seen)
	}

	// Through the cut: a bound in the tail shifts by the omitted bytes, one
	// in the omitted middle lands on the cut point.
	lim := DefaultLimits()
	lim.BodyBytes = 8 * kib
	body := strings.Repeat("h", 10*1024) + " token=SECRETVALUE " + strings.Repeat("t", 10*1024)
	ev.Out = body
	ev.Seen = &wsaudit.SeenBounds{Out: intp(len(body) - 100)}
	got, _ = prepare(ev, lim)
	tr := got.StoredTrunc.Out
	omitted := tr.OriginalBytes - tr.HeadBytes - tr.TailBytes
	if want := len(body) - 100 - (len("SECRETVALUE") - 3) - omitted; *got.Seen.Out != want {
		t.Fatalf("tail bound = %d, want %d", *got.Seen.Out, want)
	}
	ev.Seen = &wsaudit.SeenBounds{Out: intp(10*1024 + 10)} // inside the masked value, in the middle
	got, _ = prepare(ev, lim)
	if *got.Seen.Out != tr.HeadBytes {
		t.Fatalf("middle bound = %d, want the cut point %d", *got.Seen.Out, tr.HeadBytes)
	}
}

// TestPrepareThousandsOfMatchesCollapseOverflow pins (2131)=a: an under-cap
// body with thousands of masked matches keeps its masked text unchanged,
// entries stay in offset order up to the bound, and the rest collapse to one
// summary per (field, rule).
func TestPrepareThousandsOfMatchesCollapseOverflow(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "token=v%04d ", i)
		if i%2 == 0 {
			fmt.Fprintf(&b, "AKIAABCDEFGHIJKL%04d ", i)
		}
	}
	ev := testEnvelope(t, wsaudit.KindToolResult)
	ev.Out = b.String()
	whole, matches := redactForTest(ev.Out)
	got, err := prepare(ev, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got.Out != whole || got.StoredTrunc != nil {
		t.Fatal("masked body changed")
	}
	size, _ := metadataSize(got)
	if size > int(MetadataBound) {
		t.Fatalf("metadata is %d bytes", size)
	}
	var kept, summaries []wsaudit.Redaction
	for _, r := range got.Redactions {
		if r.Count > 0 {
			summaries = append(summaries, r)
		} else {
			if len(summaries) > 0 {
				t.Fatal("a per-span entry follows a summary")
			}
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 || len(kept) >= matches {
		t.Fatalf("kept %d of %d entries", len(kept), matches)
	}
	for i := 1; i < len(kept); i++ {
		if kept[i].Offset <= kept[i-1].Offset {
			t.Fatal("kept entries are not in offset order")
		}
	}
	rules := map[string]bool{}
	total := len(kept)
	for _, s := range summaries {
		if rules[s.Rule] || s.Field != "out" {
			t.Fatalf("duplicate or misfiled summary %+v", s)
		}
		rules[s.Rule] = true
		total += s.Count
	}
	if total != matches {
		t.Fatalf("entries account for %d of %d matches", total, matches)
	}
	// The summary's total length is the sum of its spans' lengths.
	var want int
	for _, r := range allRedactions(t, ev)[len(kept):] {
		if r.Rule == summaries[0].Rule {
			want += r.Length
		}
	}
	if summaries[0].Length != want {
		t.Fatalf("summary length %d, want %d", summaries[0].Length, want)
	}
}

func redactForTest(s string) (string, int) {
	ev := wsaudit.Envelope{Out: s}
	out, _, _, reds := prepareBody("out", ev.Out, nil, 1<<40, nil)
	return out, len(reds)
}

func allRedactions(t *testing.T, ev wsaudit.Envelope) []wsaudit.Redaction {
	t.Helper()
	_, _, _, reds := prepareBody("out", ev.Out, nil, 1<<40, nil)
	return reds
}

// TestPrepareCallIDNormalization pins (2138): a 300-byte call_id is stored in
// the same digest form whatever the metadata size, IDs sharing a prefix stay
// distinct, and a literal digest-shaped ID gets a different key.
func TestPrepareCallIDNormalization(t *testing.T) {
	long := strings.Repeat("c", 300)
	small := testEnvelope(t, wsaudit.KindToolCall)
	small.CallID = long
	big := small
	big.Meta = map[string]any{}
	for i := 0; i < 3000; i++ {
		big.Meta[fmt.Sprintf("k%04d", i)] = strings.Repeat("v", 30)
	}
	a, err := prepare(small, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	b, err := prepare(big, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := wsaudit.NormalizeID(long)
	if a.CallID != key || b.CallID != key {
		t.Fatalf("call IDs %q / %q, want %q", a.CallID, b.CallID, key)
	}
	other := small
	other.CallID = long[:299] + "d"
	c, _ := prepare(other, DefaultLimits())
	if c.CallID == key {
		t.Fatal("long IDs sharing a prefix merged")
	}
	literal := small
	literal.CallID = key
	d, _ := prepare(literal, DefaultLimits())
	if d.CallID == key {
		t.Fatal("a literal digest-shaped ID kept the other call's key")
	}
}

// TestPrepareMetaOverflowKeepsRequiredAndMarks pins (2138): with
// non-redaction metadata over the bound, meta.estimated survives, optional
// entries stay in key order, one marker counts the dropped keys and bytes,
// and the bodies are stored.
func TestPrepareMetaOverflowKeepsRequiredAndMarks(t *testing.T) {
	ev := testEnvelope(t, wsaudit.KindCompaction)
	ev.Out = "summary body"
	ev.Meta = map[string]any{wsaudit.MetaEstimated: true, "zzz_last": "x"}
	for i := 0; i < 2000; i++ {
		ev.Meta[fmt.Sprintf("k%04d", i)] = strings.Repeat("v", 60)
	}
	got, err := prepare(ev, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta[wsaudit.MetaEstimated] != true {
		t.Fatal("meta.estimated was dropped")
	}
	if got.Out != ev.Out {
		t.Fatal("body changed")
	}
	marker, ok := got.Meta[MetaTruncated].(map[string]any)
	if !ok {
		t.Fatalf("no truncation marker in %d keys", len(got.Meta))
	}
	keptOptional := len(got.Meta) - 2
	if marker["dropped_keys"] != 2001-keptOptional {
		t.Fatalf("marker = %+v with %d optional kept", marker, keptOptional)
	}
	for i := 0; i < keptOptional; i++ {
		if _, ok := got.Meta[fmt.Sprintf("k%04d", i)]; !ok {
			t.Fatalf("optional keys not kept in key order: k%04d missing", i)
		}
	}
	if _, ok := got.Meta["zzz_last"]; ok {
		t.Fatal("a later key was kept over earlier ones")
	}
	perKey := len(`"k0000":`) + len(`"`+strings.Repeat("v", 60)+`"`) + 1
	if want := (2000-keptOptional)*perKey + len(`"zzz_last":"x",`); marker["dropped_bytes"] != want {
		t.Fatalf("dropped bytes = %v, want %d", marker["dropped_bytes"], want)
	}
	if size, _ := metadataSize(got); size > int(MetadataBound) {
		t.Fatalf("metadata is %d bytes", size)
	}
	// The cut still finds the compaction and its estimate.
	if cut := wsaudit.FindModelSeenCut([]wsaudit.Envelope{got}); !cut.Estimated {
		t.Fatal("cut lost the estimate")
	}
}

// TestPrepareSmallestConfigMaxRecordFitsSegment pins (2129)=a (3): at the
// 4 MiB effective cap, one record with three capped bodies, the full metadata
// bound and framing fits a segment.
func TestPrepareSmallestConfigMaxRecordFitsSegment(t *testing.T) {
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 4 * mib, BodyBytes: 256 * kib, Retention: MinRetention}
	if err := lim.Validate(); err != nil {
		t.Fatal(err)
	}
	ev := testEnvelope(t, wsaudit.KindOther)
	ev.In = strings.Repeat("\x00", 1<<20)
	ev.Out = strings.Repeat("\"", 1<<20)
	ev.Raw = strings.Repeat("<", 1<<20)
	ev.Meta = map[string]any{}
	for i := 0; i < 2000; i++ {
		ev.Meta[fmt.Sprintf("k%04d", i)] = strings.Repeat("\x01", 20)
	}
	ev.Kind = wsaudit.KindOther
	got, err := prepare(ev, lim)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := encodeLine(line{T: lineEvent, Event: &got})
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(ln)) > lim.MaxRecordBytes() || lim.MaxRecordBytes() > lim.SegmentBytes() || lim.SegmentBytes() > lim.EffectiveSessionBytes()/4 {
		t.Fatalf("record %d, bound %d, segment %d", len(ln), lim.MaxRecordBytes(), lim.SegmentBytes())
	}
}
