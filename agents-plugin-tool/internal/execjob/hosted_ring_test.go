package execjob

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/textreader"
)

func TestParseStream(t *testing.T) {
	cases := []struct {
		in      string
		want    Stream
		name    string
		wantErr bool
	}{
		{"", StreamStdout, "stdout", false},
		{"  ", StreamStdout, "stdout", false},
		{"stdout", StreamStdout, "stdout", false},
		{"stderr", StreamStderr, "stderr", false},
		{"combined", StreamCombined, "combined", false},
		{"both", 0, "", true},
	}
	for _, tc := range cases {
		got, name, err := ParseStream(tc.in)
		if tc.wantErr {
			if err == nil || !strings.Contains(err.Error(), `invalid stream "both"`) {
				t.Fatalf("ParseStream(%q) err = %v", tc.in, err)
			}
			continue
		}
		if err != nil || got != tc.want || name != tc.name {
			t.Fatalf("ParseStream(%q) = %v %q %v", tc.in, got, name, err)
		}
	}
}

func TestOutputRingsInterleavedOffsets(t *testing.T) {
	r := NewOutputRings(0, 0)
	j := r.NewJob()
	r.Append(j, StreamStdout, []byte("out1\n"))
	r.Append(j, StreamStderr, []byte("err1\n"))
	r.Append(j, StreamStdout, []byte("out2\n"))

	cases := []struct {
		stream  Stream
		written int64
		all     string
	}{
		{StreamCombined, 15, "out1\nerr1\nout2\n"},
		{StreamStdout, 10, "out1\nout2\n"},
		{StreamStderr, 5, "err1\n"},
	}
	for _, tc := range cases {
		if got := j.Written(tc.stream); got != tc.written {
			t.Fatalf("%v Written = %d, want %d", tc.stream, got, tc.written)
		}
		sl := j.Read(tc.stream, 0, 0)
		if string(sl.Data) != tc.all || sl.Start != 0 || sl.End != tc.written || sl.Evicted != 0 {
			t.Fatalf("%v Read = %+v", tc.stream, sl)
		}
	}
	if sl := j.Read(StreamCombined, 5, 5); string(sl.Data) != "err1\n" || sl.Start != 5 || sl.End != 10 {
		t.Fatalf("combined middle Read = %+v", sl)
	}
	if sl := j.Read(StreamStdout, 5, 3); string(sl.Data) != "out" || sl.End != 8 {
		t.Fatalf("stdout second-record Read = %+v", sl)
	}
	if got := j.TailLines(StreamCombined, 2); got != "err1\nout2\n" {
		t.Fatalf("TailLines = %q", got)
	}
	if r.Used() != 15 {
		t.Fatalf("Used = %d", r.Used())
	}
}

func TestOutputRingsPerJobEvictionTrimsFrontRecord(t *testing.T) {
	r := NewOutputRings(10, 0)
	j := r.NewJob()
	r.Append(j, StreamStdout, []byte("AAAAAA")) // combined [0,6), stdout [0,6)
	r.Append(j, StreamStderr, []byte("BBBB"))   // combined [6,10), stderr [0,4)
	r.Append(j, StreamStdout, []byte("CCC"))    // combined [10,13), stdout [6,9): evict 3 of A

	if got := j.Evicted(StreamCombined); got != 3 {
		t.Fatalf("combined Evicted = %d, want 3", got)
	}
	if got := j.Evicted(StreamStdout); got != 3 {
		t.Fatalf("stdout Evicted = %d, want 3", got)
	}
	if got := j.Evicted(StreamStderr); got != 0 {
		t.Fatalf("stderr Evicted = %d, want 0", got)
	}
	if sl := j.Read(StreamCombined, 0, 0); string(sl.Data) != "AAABBBBCCC" || sl.Start != 3 || sl.Evicted != 3 || sl.Written != 13 {
		t.Fatalf("combined Read = %+v", sl)
	}
	if sl := j.Read(StreamStdout, 0, 0); string(sl.Data) != "AAACCC" || sl.Start != 3 || sl.End != 9 {
		t.Fatalf("stdout Read = %+v", sl)
	}

	// Dropping the rest of A and part of B moves stderr's mark too.
	r.Append(j, StreamStdout, []byte("DDDDD"))
	if got := j.Evicted(StreamCombined); got != 8 {
		t.Fatalf("combined Evicted = %d, want 8", got)
	}
	if got := j.Evicted(StreamStdout); got != 6 {
		t.Fatalf("stdout Evicted = %d, want 6", got)
	}
	if got := j.Evicted(StreamStderr); got != 2 {
		t.Fatalf("stderr Evicted = %d, want 2", got)
	}
	if sl := j.Read(StreamStderr, 0, 0); string(sl.Data) != "BB" || sl.Start != 2 {
		t.Fatalf("stderr Read = %+v", sl)
	}
	if r.Used() != 10 {
		t.Fatalf("Used = %d, want 10", r.Used())
	}
}

func TestOutputRingsOversizedAppendKeepsOnlyTail(t *testing.T) {
	r := NewOutputRings(4, 0)
	j := r.NewJob()
	r.Append(j, StreamStderr, []byte("ee"))
	r.Append(j, StreamStdout, []byte("a\nb\ncdef"))
	if sl := j.Read(StreamCombined, 0, 0); string(sl.Data) != "cdef" || sl.Start != 6 || sl.Evicted != 6 || sl.Written != 10 {
		t.Fatalf("combined Read = %+v", sl)
	}
	if got := j.Evicted(StreamStderr); got != 2 {
		t.Fatalf("stderr Evicted = %d", got)
	}
	if sl := j.Read(StreamStdout, 0, 0); string(sl.Data) != "cdef" || sl.Start != 4 || sl.Written != 8 {
		t.Fatalf("stdout Read = %+v", sl)
	}
	gr, err := j.Grep(StreamStdout, "cd", 0, 0, 0, false)
	if err != nil || len(gr.Matches) != 1 || gr.Matches[0].Line != 3 {
		t.Fatalf("Grep = %+v, %v", gr, err)
	}
}

func TestOutputRingsAggregateEvictionAndRelease(t *testing.T) {
	r := NewOutputRings(100, 10)
	a := r.NewJob()
	b := r.NewJob()
	r.Append(a, StreamStdout, []byte("aaaaaa")) // oldest
	r.Append(b, StreamStdout, []byte("bbbb"))
	r.Append(b, StreamStderr, []byte("BBB")) // total 13: evict 3 of a's oldest bytes

	if got := a.Evicted(StreamCombined); got != 3 {
		t.Fatalf("a combined Evicted = %d, want 3", got)
	}
	if got := a.Evicted(StreamStdout); got != 3 {
		t.Fatalf("a stdout Evicted = %d, want 3", got)
	}
	if got := b.Evicted(StreamCombined); got != 0 {
		t.Fatalf("b combined Evicted = %d, want 0", got)
	}
	if r.Used() != 10 {
		t.Fatalf("Used = %d, want 10", r.Used())
	}

	r.Append(b, StreamStdout, []byte("xxxxx")) // a's remaining 3 go, then 2 of b's oldest
	if sl := a.Read(StreamCombined, 0, 0); len(sl.Data) != 0 || sl.Evicted != 6 || sl.Written != 6 {
		t.Fatalf("a Read = %+v", sl)
	}
	if sl := b.Read(StreamCombined, 0, 0); string(sl.Data) != "bbBBBxxxxx" || sl.Start != 2 {
		t.Fatalf("b Read = %+v", sl)
	}

	// Released jobs stop counting and accept no more bytes.
	r.Release(b)
	if r.Used() != 0 {
		t.Fatalf("Used after Release = %d", r.Used())
	}
	r.Append(b, StreamStdout, []byte("ignored"))
	if r.Used() != 0 || b.Written(StreamCombined) != 12 || b.Evicted(StreamCombined) != 12 {
		t.Fatalf("released b: used=%d written=%d evicted=%d", r.Used(), b.Written(StreamCombined), b.Evicted(StreamCombined))
	}
	r.Append(a, StreamStdout, []byte("0123456789"))
	if sl := a.Read(StreamStdout, 0, 0); string(sl.Data) != "0123456789" || sl.Start != 6 {
		t.Fatalf("a after Release of b = %+v", sl)
	}
}

// TestOutputRingsAppendAtKeepsAbsoluteOffsets: records placed at a
// holder's offsets keep absolute offsets after a replay that starts past
// evicted bytes; a gap in one stream leaves the other's retained bytes,
// and a later gap drops only what came before it.
func TestOutputRingsAppendAtKeepsAbsoluteOffsets(t *testing.T) {
	r := NewOutputRings(1<<20, 1<<20)
	j := r.NewJob()
	// The replay's first record: 100 stdout bytes and 50 stderr bytes left
	// the holder's ring before it.
	r.AppendAt(j, StreamStdout, []byte("out-a\n"), 100, 150)
	r.AppendAt(j, StreamStderr, []byte("err-a\n"), 50, 156)
	r.AppendAt(j, StreamStdout, []byte("out-b\n"), 106, 162)
	if got := j.Read(StreamStdout, 0, 100); got.Start != 100 || got.Evicted != 100 || got.Written != 112 || string(got.Data) != "out-a\nout-b\n" {
		t.Fatalf("stdout after the replay = %+v (%q)", got, got.Data)
	}
	if got := j.Read(StreamStdout, 106, 100); got.Start != 106 || string(got.Data) != "out-b\n" {
		t.Fatalf("stdout from a saved cursor = %+v (%q)", got, got.Data)
	}
	if got := j.Read(StreamStderr, 0, 100); got.Start != 50 || got.Written != 56 || string(got.Data) != "err-a\n" {
		t.Fatalf("stderr after the replay = %+v (%q)", got, got.Data)
	}
	if got := j.Read(StreamCombined, 0, 100); got.Start != 150 || got.Written != 168 || string(got.Data) != "out-a\nerr-a\nout-b\n" {
		t.Fatalf("combined after the replay = %+v (%q)", got, got.Data)
	}
	// A reader that fell behind missed stdout and combined bytes: what came
	// before the gap goes, the offsets stay absolute.
	r.AppendAt(j, StreamStdout, []byte("out-c\n"), 200, 300)
	if got := j.Read(StreamStdout, 0, 100); got.Start != 200 || got.Evicted != 200 || string(got.Data) != "out-c\n" {
		t.Fatalf("stdout after a gap = %+v (%q)", got, got.Data)
	}
	if got := j.Read(StreamCombined, 0, 100); got.Start != 300 || got.Written != 306 || string(got.Data) != "out-c\n" {
		t.Fatalf("combined after a gap = %+v (%q)", got, got.Data)
	}
	// Offsets already written append as Append does.
	r.AppendAt(j, StreamStdout, []byte("out-d\n"), 206, 306)
	if got := j.Read(StreamStdout, 200, 100); string(got.Data) != "out-c\nout-d\n" || got.Written != 212 {
		t.Fatalf("stdout after contiguous records = %+v (%q)", got, got.Data)
	}
}

// TestOutputRingsAppendAtSetsTheSilentStream: a replay of one stream sets
// the other stream's offset from the combined one, so a saved cursor of a
// stream whose bytes all left the holder's ring reports the gap.
func TestOutputRingsAppendAtSetsTheSilentStream(t *testing.T) {
	r := NewOutputRings(1<<20, 1<<20)
	j := r.NewJob()
	// 40 stderr and 100 stdout bytes left the holder's ring; the replay
	// holds stdout only.
	r.AppendAt(j, StreamStdout, []byte("out-a\n"), 100, 140)
	if got := j.Read(StreamStderr, 30, 100); got.Start != 40 || got.Evicted != 40 || got.Written != 40 || len(got.Data) != 0 {
		t.Fatalf("stderr after a stdout-only replay = %+v (%q)", got, got.Data)
	}
	if got := j.Read(StreamStdout, 0, 100); got.Start != 100 || got.Written != 106 || string(got.Data) != "out-a\n" {
		t.Fatalf("stdout after the replay = %+v (%q)", got, got.Data)
	}
	// stderr resumes at its absolute offset; stdout keeps its bytes.
	r.AppendAt(j, StreamStderr, []byte("err-a\n"), 40, 146)
	if got := j.Read(StreamStderr, 40, 100); got.Start != 40 || got.Written != 46 || string(got.Data) != "err-a\n" {
		t.Fatalf("stderr after it resumed = %+v (%q)", got, got.Data)
	}
	if got := j.Read(StreamCombined, 0, 100); got.Start != 140 || got.Written != 152 || string(got.Data) != "out-a\nerr-a\n" {
		t.Fatalf("combined = %+v (%q)", got, got.Data)
	}
}

func TestOutputRingsReadBounds(t *testing.T) {
	r := NewOutputRings(8, 0)
	j := r.NewJob()
	r.Append(j, StreamStdout, []byte("0123456789ab"))
	cases := []struct {
		name          string
		offset, limit int64
		data          string
		start, end    int64
	}{
		{"below evicted", 0, 3, "456", 4, 7},
		{"negative", -5, 0, "456789ab", 4, 12},
		{"inside", 9, 2, "9a", 9, 11},
		{"at written", 12, 0, "", 12, 12},
		{"past written", 40, 10, "", 12, 12},
	}
	for _, tc := range cases {
		sl := j.Read(StreamStdout, tc.offset, tc.limit)
		if string(sl.Data) != tc.data || sl.Start != tc.start || sl.End != tc.end || sl.Evicted != 4 || sl.Written != 12 {
			t.Fatalf("%s: Read = %+v", tc.name, sl)
		}
	}
}

func TestOutputRingsReadLimitCap(t *testing.T) {
	r := NewOutputRings(0, 0)
	j := r.NewJob()
	r.Append(j, StreamStdout, bytes.Repeat([]byte("x"), textreader.MaxReadLimit+100))
	if sl := j.Read(StreamStdout, 0, textreader.MaxReadLimit+50); len(sl.Data) != textreader.MaxReadLimit {
		t.Fatalf("capped Read len = %d", len(sl.Data))
	}
	if sl := j.Read(StreamStdout, 0, 0); len(sl.Data) != textreader.DefaultReadLimit {
		t.Fatalf("default Read len = %d", len(sl.Data))
	}
}

func TestOutputRingsTailRuneBoundary(t *testing.T) {
	r := NewOutputRings(0, 0)
	j := r.NewJob()
	// "가" is 3 bytes (ea b0 80); "😀" is 4 bytes.
	r.Append(j, StreamStdout, []byte("ab가c😀"))
	cases := []struct {
		max   int
		data  string
		start int64
	}{
		{1, "", 10},       // cut on the emoji's last byte leaves nothing whole
		{4, "😀", 6},       // exact boundary
		{5, "c😀", 5},      // exact boundary
		{6, "c😀", 5},      // cut before 가's last byte: skip 1
		{7, "c😀", 5},      // cut right after 가's lead byte: skip 2
		{8, "가c😀", 2},     // exact boundary
		{100, "ab가c😀", 0}, // whole stream
	}
	for _, tc := range cases {
		sl := j.Tail(StreamStdout, tc.max)
		if string(sl.Data) != tc.data || sl.Start != tc.start || sl.End != 10 {
			t.Fatalf("Tail(%d) = %q start=%d end=%d", tc.max, sl.Data, sl.Start, sl.End)
		}
	}
}

func TestOutputRingsTailRuneBoundaryAfterEviction(t *testing.T) {
	r := NewOutputRings(5, 0)
	j := r.NewJob()
	r.Append(j, StreamStdout, []byte("x가yz")) // x ea b0 80 y z; "x" is never stored
	r.Append(j, StreamStdout, []byte("w"))    // evicts ea; retained b0 80 y z w
	sl := j.Tail(StreamStdout, 10)
	if string(sl.Data) != "yzw" || sl.Start != 4 || sl.Evicted != 2 {
		t.Fatalf("Tail after eviction = %q start=%d evicted=%d", sl.Data, sl.Start, sl.Evicted)
	}
	// Raw invalid bytes that are not a cut rune stay.
	r2 := NewOutputRings(0, 0)
	j2 := r2.NewJob()
	r2.Append(j2, StreamStdout, []byte{0xb0, 0xa1, 'k'})
	if sl := j2.Tail(StreamStdout, 10); !bytes.Equal(sl.Data, []byte{0xb0, 0xa1, 'k'}) || sl.Start != 0 {
		t.Fatalf("Tail of raw bytes = %v start=%d", sl.Data, sl.Start)
	}
}

func TestDecodeText(t *testing.T) {
	rep := string(utf8.RuneError)
	cases := []struct {
		name     string
		in       []byte
		text     string
		replaced bool
	}{
		{"ascii", []byte("ok\n"), "ok\n", false},
		{"utf8", []byte("가나"), "가나", false},
		{"literal replacement char", []byte("a" + rep + "b"), "a" + rep + "b", false},
		{"0xff", []byte{'a', 0xff, 'b'}, "a" + rep + "b", true},
		{"cp949", []byte{0xb0, 0xa1, 0xb3, 0xaa}, strings.Repeat(rep, 4), true}, // "가나" in cp949
		{"truncated rune", []byte{'x', 0xea, 0xb0}, "x" + rep + rep, true},
	}
	for _, tc := range cases {
		text, replaced := DecodeText(tc.in)
		if text != tc.text || replaced != tc.replaced {
			t.Fatalf("%s: DecodeText = %q %v", tc.name, text, replaced)
		}
	}

	// Raw bytes stay raw in the ring.
	r := NewOutputRings(0, 0)
	j := r.NewJob()
	raw := []byte{0xc7, 0xd1, '\n', 0xff}
	r.Append(j, StreamStderr, raw)
	sl := j.Tail(StreamStderr, 0)
	if !bytes.Equal(sl.Data, raw) {
		t.Fatalf("Tail Data = %v, want raw %v", sl.Data, raw)
	}
	if _, replaced := DecodeText(sl.Data); !replaced {
		t.Fatal("DecodeText of raw cp949 bytes reported no replacement")
	}
}

func TestOutputRingsGrepAbsoluteLinesAfterEviction(t *testing.T) {
	r := NewOutputRings(20, 0)
	j := r.NewJob()
	for i := 1; i <= 10; i++ {
		r.Append(j, StreamStdout, []byte(fmt.Sprintf("line%d\n", i)))
		if i%3 == 0 {
			r.Append(j, StreamStderr, []byte("e\n"))
		}
	}
	gr, err := j.Grep(StreamStdout, "line10", 1, 0, 0, false)
	if err != nil || len(gr.Matches) != 1 || gr.Matches[0].Line != 10 || gr.Matches[0].Text != "line10" {
		t.Fatalf("stdout Grep = %+v, %v", gr, err)
	}
	if gr.Matches[0].Path != "" || len(gr.Matches[0].Before) != 1 || gr.Matches[0].Before[0] != "line9" {
		t.Fatalf("stdout Grep context = %+v", gr.Matches[0])
	}
	// Combined: 10 stdout lines + 3 stderr lines; line10 is combined line 13.
	gr, err = j.Grep(StreamCombined, `^line1\d$`, 0, 0, 0, true)
	if err != nil || len(gr.Matches) != 1 || gr.Matches[0].Line != 13 {
		t.Fatalf("combined Grep = %+v, %v", gr, err)
	}
	if _, err := j.Grep(StreamStdout, "(", 0, 0, 0, true); err == nil {
		t.Fatal("invalid regex returned nil error")
	}
	if _, err := j.Grep(StreamStdout, "", 0, 0, 0, false); err == nil {
		t.Fatal("empty pattern returned nil error")
	}
	if got := j.TailLines(StreamStdout, 2); got != "line9\nline10\n" {
		t.Fatalf("TailLines = %q", got)
	}
}

func TestOutputRingsTailLinesMatchesTextreader(t *testing.T) {
	inputs := []string{"", "a", "a\nb", "a\r\nb\r\n", "\n\n", "x\ny\nz\n"}
	for _, in := range inputs {
		r := NewOutputRings(0, 0)
		j := r.NewJob()
		// Split across records to exercise the multi-part reader.
		for i := 0; i < len(in); i++ {
			s := StreamStdout
			if i%2 == 1 {
				s = StreamStderr
			}
			r.Append(j, s, []byte{in[i]})
		}
		for _, n := range []int{0, 1, 2} {
			if got, want := j.TailLines(StreamCombined, n), textreader.TailText([]byte(in), n); got != want {
				t.Fatalf("TailLines(%q, %d) = %q, want %q", in, n, got, want)
			}
		}
	}
}

func TestOutputRingsRestoreKeepsOffsets(t *testing.T) {
	r := NewOutputRings(64, 0)
	j := r.NewJob()
	r.Restore(j, [3]int64{1000, 700, 300}, []byte("out-tail"), []byte("err"), []byte("combined-tail"))

	cases := []struct {
		stream  Stream
		written int64
		evicted int64
		data    string
	}{
		{StreamCombined, 1000, 987, "combined-tail"},
		{StreamStdout, 700, 692, "out-tail"},
		{StreamStderr, 300, 297, "err"},
	}
	for _, tc := range cases {
		sl := j.Read(tc.stream, 0, 0)
		if string(sl.Data) != tc.data || sl.Start != tc.evicted || sl.Evicted != tc.evicted || sl.Written != tc.written {
			t.Fatalf("%v restored Read = %+v", tc.stream, sl)
		}
	}
	r.Append(j, StreamStdout, []byte("+new"))
	if sl := j.Read(StreamCombined, 1000, 0); string(sl.Data) != "+new" || sl.Start != 1000 || sl.Written != 1004 {
		t.Fatalf("combined after Restore = %+v", sl)
	}
	if sl := j.Read(StreamStdout, 0, 0); string(sl.Data) != "out-tail+new" || sl.Start != 692 || sl.Written != 704 {
		t.Fatalf("stdout after Restore = %+v", sl)
	}
	if got := j.Written(StreamStderr); got != 300 {
		t.Fatalf("stderr Written = %d", got)
	}
	if r.Used() != int64(len("combined-tail")+len("out-tail")+len("err")+len("+new")) {
		t.Fatalf("Used = %d", r.Used())
	}
}

func TestOutputRingsConcurrentAppendRead(t *testing.T) {
	r := NewOutputRings(4096, 6000)
	jobs := []*JobOutput{r.NewJob(), r.NewJob()}
	var wg sync.WaitGroup
	for ji, j := range jobs {
		wg.Add(1)
		go func(ji int, j *JobOutput) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				s := StreamStdout
				if i%3 == 0 {
					s = StreamStderr
				}
				r.Append(j, s, []byte(fmt.Sprintf("job%d line %d\n", ji, i)))
			}
		}(ji, j)
	}
	for _, j := range jobs {
		wg.Add(1)
		go func(j *JobOutput) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_ = j.Tail(StreamCombined, 512)
				sl := j.Read(StreamStdout, j.Evicted(StreamStdout), 256)
				if sl.Start < sl.Evicted || sl.End > sl.Written {
					t.Errorf("inconsistent slice %+v", sl)
					return
				}
				_ = j.TailLines(StreamStderr, 5)
				if _, err := j.Grep(StreamCombined, "line 1", 0, 0, 5, false); err != nil {
					t.Errorf("Grep: %v", err)
					return
				}
			}
		}(j)
	}
	wg.Wait()
	if used := r.Used(); used > 6000 {
		t.Fatalf("Used = %d exceeds total", used)
	}
	for _, j := range jobs {
		if got := j.Written(StreamCombined); got != j.Written(StreamStdout)+j.Written(StreamStderr) {
			t.Fatalf("combined Written %d != stdout+stderr", got)
		}
		if sl := j.Read(StreamCombined, 0, 0); sl.Start != sl.Evicted || sl.Evicted == 0 {
			t.Fatalf("expected eviction, got %+v", sl)
		}
	}
}
