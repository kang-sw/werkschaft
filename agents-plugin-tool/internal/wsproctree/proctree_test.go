package wsproctree

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestParsePS(t *testing.T) {
	out := "    1     0   12345   1:02.50 /sbin/launchd\n" +
		"  501     1    2048   0:00.01 /Applications/Some App.app/Contents/MacOS/Some App\n" +
		"  777   501  100000 01:02:03 node\n" +
		"  778   501       4 2-01:00:00 sleeper\n" +
		"  bad line\n" +
		"  900   777     xx   0:00.00 broken\n" +
		"\n"
	got := parsePS(out)
	want := []Proc{
		{PID: 1, PPID: 0, Name: "launchd", RSS: 12345 * 1024, CPU: 62500 * time.Millisecond},
		{PID: 501, PPID: 1, Name: "Some App", RSS: 2048 * 1024, CPU: 10 * time.Millisecond},
		{PID: 777, PPID: 501, Name: "node", RSS: 100000 * 1024, CPU: 3723 * time.Second},
		{PID: 778, PPID: 501, Name: "sleeper", RSS: 4 * 1024, CPU: (2*86400 + 3600) * time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePS:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseCPUTimeRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "12", "a:b", "1:2:3:4", "-1:00", "x-1:00"} {
		if _, err := parseCPUTime(s); err == nil {
			t.Errorf("parseCPUTime(%q) accepted", s)
		}
	}
}

func TestParseStat(t *testing.T) {
	// comm holds spaces and parentheses; only the last ')' ends it.
	line := "4242 (my (odd) proc) S 4200 4242 4242 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 3 0 12345 104857600 2560 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0\n"
	p, err := parseStat(line, 100, 4096)
	if err != nil {
		t.Fatal(err)
	}
	want := Proc{PID: 4242, PPID: 4200, Name: "my (odd) proc", RSS: 2560 * 4096, CPU: 3 * time.Second}
	if p != want {
		t.Fatalf("parseStat = %+v, want %+v", p, want)
	}
	for _, bad := range []string{"", "12 no parens", "x (c) S 1", "12 (c) S 1 2 3"} {
		if _, err := parseStat(bad, 100, 4096); err == nil {
			t.Errorf("parseStat(%q) accepted", bad)
		}
	}
}

func TestDescendantsAndKeep(t *testing.T) {
	all := []Proc{
		{PID: 1, PPID: 0}, {PID: 10, PPID: 1}, {PID: 11, PPID: 10}, {PID: 12, PPID: 10},
		{PID: 13, PPID: 11}, {PID: 20, PPID: 1}, {PID: 21, PPID: 20},
		// A cycle and a self-parent never loop.
		{PID: 30, PPID: 31}, {PID: 31, PPID: 30}, {PID: 40, PPID: 40},
	}
	pids := func(ps []Proc) (out []int) {
		for _, p := range ps {
			out = append(out, p.PID)
		}
		return
	}
	if got := pids(Descendants(all, 10)); !reflect.DeepEqual(got, []int{10, 11, 12, 13}) {
		t.Fatalf("Descendants(10) = %v", got)
	}
	if got := pids(Descendants(all, 30)); !reflect.DeepEqual(got, []int{30, 31}) {
		t.Fatalf("Descendants(30) = %v", got)
	}
	if got := pids(Descendants(all, 40)); !reflect.DeepEqual(got, []int{40}) {
		t.Fatalf("Descendants(40) = %v", got)
	}
	// A gone root still reaches its orphaned children's records by ppid.
	if got := pids(Descendants(all[1:], 1)); !reflect.DeepEqual(got, []int{10, 20, 11, 12, 21, 13}) {
		t.Fatalf("Descendants(gone 1) = %v", got)
	}
	if got := pids(keep(all, []int{11, 20})); !reflect.DeepEqual(got, []int{11, 13, 20, 21}) {
		t.Fatalf("keep = %v", got)
	}
}

// BenchmarkSnapshot is one metrics sample's cost (Darwin: one `ps`).
func BenchmarkSnapshot(b *testing.B) {
	if !Supported() {
		b.Skip("no collector on this OS")
	}
	roots := []int{os.Getpid()}
	for i := 0; i < b.N; i++ {
		if _, err := Snapshot(roots); err != nil {
			b.Fatal(err)
		}
	}
}

// TestSnapshotReadsThisProcess reads the test process itself where a
// collector exists.
func TestSnapshotReadsThisProcess(t *testing.T) {
	if !Supported() {
		if _, err := Snapshot([]int{os.Getpid()}); err != ErrUnsupported {
			t.Fatalf("unsupported Snapshot err = %v", err)
		}
		return
	}
	ps, err := Snapshot([]int{os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) == 0 || ps[0].PID != os.Getpid() {
		t.Fatalf("Snapshot(self) = %+v", ps)
	}
	if ps[0].RSS == 0 || ps[0].Name == "" {
		t.Fatalf("Snapshot(self) lacks rss or name: %+v", ps[0])
	}
	if ps, err := Snapshot(nil); err != nil || ps != nil {
		t.Fatalf("Snapshot(nil) = %v, %v", ps, err)
	}
}
