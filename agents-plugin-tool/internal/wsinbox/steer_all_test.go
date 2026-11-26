package wsinbox

import (
	"errors"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestSteerAllWithAdmissionAtomicAndFiltering(t *testing.T) {
	dir := Dir(t.TempDir())
	now := time.Now()

	// Seed: already-steered user entry, un-steered user entries, a command entry,
	// an exec notice, and an interrupt entry.
	alreadyName, _, err := Send(dir, Entry{Parts: []string{"already steered"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	alreadySteered, err := Steer(dir, alreadyName)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Send(dir, Entry{Parts: []string{"first held"}}, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Send(dir, Entry{EventID: "exec:job42:exit", Parts: []string{"exec finished"}}, now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Send(dir, Entry{Command: &Command{Name: "compact", Args: "now"}}, now.Add(3*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Send(dir, Entry{Parts: []string{"second held"}}, now.Add(4*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := writeEntry(dir, Entry{SchemaVersion: SchemaVersion, Interrupt: true, SentAt: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}

	var admittedCount int
	n, err := SteerAllWithAdmission(dir, func(entries []Entry) error {
		admittedCount = len(entries)
		return nil
	})
	if err != nil {
		t.Fatalf("SteerAllWithAdmission error: %v", err)
	}
	if n != 3 || admittedCount != 3 {
		t.Fatalf("promoted = %d (admitted %d), want 3", n, admittedCount)
	}

	held, err := Held(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Queue order: steered first (alreadySteered, first held, /compact now, second held),
	// then un-steered (exec notice). Interrupt is excluded from Held.
	if len(held) != 5 {
		t.Fatalf("len(held) = %d, want 5: %+v", len(held), held)
	}
	if held[0].Name != alreadySteered || !held[0].Entry.Steer {
		t.Fatalf("held[0] = %+v, want untouched already-steered %s", held[0], alreadySteered)
	}
	wantSteered := []string{"first held", "/compact now", "second held"}
	for i, want := range wantSteered {
		rec := held[i+1]
		if !rec.Entry.Steer {
			t.Fatalf("held[%d] not steered: %+v", i+1, rec)
		}
		if rec.Entry.Command != nil {
			t.Fatalf("held[%d] still has Command after steer: %+v", i+1, rec.Entry.Command)
		}
		if len(rec.Entry.Parts) != 1 || rec.Entry.Parts[0] != want {
			t.Fatalf("held[%d].Parts = %v, want [%q]", i+1, rec.Entry.Parts, want)
		}
	}
	if id, ok := held[4].Entry.ExecNotice(); !ok || id != "job42" || held[4].Entry.Steer {
		t.Fatalf("held[4] = %+v, want un-steered exec notice job42", held[4])
	}
	interrupts, err := InterruptNames(dir)
	if err != nil || len(interrupts) != 1 {
		t.Fatalf("interrupts = %v, %v; want 1 untouched interrupt", interrupts, err)
	}
}

func TestSteerAllWithAdmissionRefusalLeavesZeroPartialWrites(t *testing.T) {
	dir := Dir(t.TempDir())
	now := time.Now()

	n1, _, err := Send(dir, Entry{Parts: []string{"one"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	n2, _, err := Send(dir, Entry{Parts: []string{"two"}, Chips: []Chip{{ID: "01", Answer: "ok"}}}, now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	refused := errors.New("stale board")
	calls := 0
	count, err := SteerAllWithAdmission(dir, func(entries []Entry) error {
		calls++
		if len(entries) != 2 {
			t.Fatalf("admit got %d entries, want 2", len(entries))
		}
		return refused
	})
	if !errors.Is(err, refused) || count != 0 || calls != 1 {
		t.Fatalf("got count=%d err=%v calls=%d; want 0, refused, 1", count, err, calls)
	}

	held, err := Held(dir)
	if err != nil || len(held) != 2 {
		t.Fatalf("held after refusal = %+v, %v", held, err)
	}
	if held[0].Name != n1 || held[0].Entry.Steer || held[1].Name != n2 || held[1].Entry.Steer {
		t.Fatalf("refusal altered entries: %+v", held)
	}
}

func TestSteerAllWithAdmissionZeroEligibleDoesZeroWrites(t *testing.T) {
	// Missing directory.
	missingDir := Dir(t.TempDir())
	beforeTemp := wsstate.FileIOCounters()["file_io_create_temp"]
	beforeOpen := wsstate.FileIOCounters()["file_io_open_file"]
	n, err := SteerAllWithAdmission(missingDir, func([]Entry) error {
		t.Fatal("admit must not be called when 0 entries qualify")
		return nil
	})
	if err != nil || n != 0 {
		t.Fatalf("missing dir: n=%d err=%v", n, err)
	}
	if got := wsstate.FileIOCounters()["file_io_create_temp"] - beforeTemp; got != 0 {
		t.Fatalf("missing dir created %d temp files", got)
	}
	if got := wsstate.FileIOCounters()["file_io_open_file"] - beforeOpen; got != 0 {
		t.Fatalf("missing dir opened %d files", got)
	}

	// Directory with only exec notice + already-steered entry.
	dir := Dir(t.TempDir())
	now := time.Now()
	sName, _, err := Send(dir, Entry{Parts: []string{"steered"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Steer(dir, sName); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Send(dir, Entry{EventID: "exec:j1:exit", Parts: []string{"done"}}, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	beforeTemp = wsstate.FileIOCounters()["file_io_create_temp"]
	beforeOpen = wsstate.FileIOCounters()["file_io_open_file"]
	n, err = SteerAllWithAdmission(dir, func([]Entry) error {
		t.Fatal("admit must not be called when 0 entries qualify")
		return nil
	})
	if err != nil || n != 0 {
		t.Fatalf("0 eligible: n=%d err=%v", n, err)
	}
	if got := wsstate.FileIOCounters()["file_io_create_temp"] - beforeTemp; got != 0 {
		t.Fatalf("0 eligible created %d temp files", got)
	}
	if got := wsstate.FileIOCounters()["file_io_open_file"] - beforeOpen; got != 0 {
		t.Fatalf("0 eligible opened %d files", got)
	}
}
