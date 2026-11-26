package wscron

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func (e *env) addAt(owner, name, at string) string {
	e.t.Helper()
	id, _, err := e.s.AddAt(owner, name, "prompt "+name, mustTime(e.t, at))
	if err != nil {
		e.t.Fatalf("add at %s: %v", name, err)
	}
	return id
}

// countDeliveries counts every Deliver call, failed ones included.
func (e *env) countDeliveries() *atomic.Int64 {
	var n atomic.Int64
	e.sess.gate = func(string) { n.Add(1) }
	return &n
}

// rowsEventually waits until the store holds n rows: a finished one-shot's
// delete is written by the scheduler's writer goroutine, after the fire.
func (e *env) rowsEventually(n int) {
	e.t.Helper()
	eventually(e.t, func() bool {
		rows, err := e.s.store.Load()
		return err == nil && len(rows) == n
	})
}

// legacyV1Schema is the schedules table as the binaries before run_at
// created it.
const legacyV1Schema = `CREATE TABLE schedules (
	id         TEXT PRIMARY KEY,
	owner      TEXT NOT NULL,
	name       TEXT NOT NULL,
	expr       TEXT NOT NULL,
	tz         TEXT NOT NULL,
	prompt     TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	last_fire  INTEGER NOT NULL
)`

func columnsOf(t *testing.T, path string) map[string]bool {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	cols, err := tableColumns(context.Background(), tx, "schedules")
	if err != nil {
		t.Fatal(err)
	}
	return cols
}

func userVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// An existing schema-1 database made before run_at gains the column on open,
// keeps user_version 1 (no lockout of older binaries) and keeps its rows; a
// second open changes nothing.
func TestStoreAddsRunAtToAnExistingV1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{legacyV1Schema, `PRAGMA user_version=1`,
		`INSERT INTO schedules VALUES ('cron-01', 'lead', 'daily', '@daily', 'UTC', 'p', 100, 0)`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if columnsOf(t, path)["run_at"] {
		t.Fatal("fixture already has run_at")
	}
	for round := 0; round < 2; round++ {
		st, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := st.Load()
		if err != nil || len(rows) != 1 || rows[0].Expr != "@daily" || rows[0].RunAt != 0 {
			t.Fatalf("round %d rows = %+v, %v", round, rows, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if !columnsOf(t, path)["run_at"] {
			t.Fatalf("round %d: run_at was not added", round)
		}
		if v := userVersion(t, path); v != 1 {
			t.Fatalf("round %d: user_version = %d, want 1", round, v)
		}
	}
	// A fresh database gets the column too.
	fresh := filepath.Join(t.TempDir(), StoreName)
	st, err := OpenStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if !columnsOf(t, fresh)["run_at"] || userVersion(t, fresh) != 1 {
		t.Fatal("a fresh store lacks run_at or is not version 1")
	}
}

// T0: a binary that predates run_at selects its explicit columns, sees a
// one-shot as an empty expr and skips the row without rewriting it; its
// last-fire write and its purge of other owners leave the row alone, and
// the new binary restores the one-shot on reopen.
func TestOldBinarySkipsAOneShotAndReopenRestoresIt(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead", "other")
	e.add("lead", "daily", "@daily", "")
	e.addAt("lead", "wake", "2026-03-10T15:00:00+09:00")
	e.add("other", "tick", "@hourly", "")
	before := e.s.List("lead")
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}

	// The old binary's view of the store.
	db, err := sql.Open("sqlite", e.path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id, owner, name, expr, tz, prompt, created_at, last_fire FROM schedules ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	var loaded, skipped []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.Owner, &r.Name, &r.Expr, &r.TZ, &r.Prompt, &r.CreatedAt, &r.LastFire); err != nil {
			t.Fatal(err)
		}
		// The old scheduleFromRow: Parse(r.Expr) first.
		if _, err := Parse(r.Expr); err != nil {
			skipped = append(skipped, r)
			continue
		}
		loaded = append(loaded, r)
	}
	rows.Close()
	if len(skipped) != 1 || skipped[0].Name != "wake" || len(loaded) != 2 {
		t.Fatalf("old binary loaded %+v, skipped %+v", loaded, skipped)
	}
	// The old binary writes last fires for the schedules it loaded and purges
	// an ended owner.
	for _, stmt := range []string{
		`UPDATE schedules SET last_fire = MAX(COALESCE(last_fire, 0), 1773100800) WHERE id = '` + loaded[0].ID + `'`,
		`DELETE FROM schedules WHERE owner = 'other'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	e.open()
	after := e.s.List("lead")
	if len(after) != 2 || after[1] != before[1] || after[1].At == "" || after[1].Expr != "" {
		t.Fatalf("one-shot not restored:\n before %+v\n after  %+v", before, after)
	}
}

func TestAddAtRefusesANonFutureTime(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	for _, at := range []string{"2026-03-10T00:00:30Z", "2026-03-10T00:00:29Z", "2026-03-10T09:00:00+09:00"} {
		if _, _, err := e.s.AddAt("lead", "wake", "p", mustTime(t, at)); !errors.Is(err, ErrAtNotFuture) {
			t.Fatalf("at %s: err = %v, want ErrAtNotFuture", at, err)
		}
	}
	e.storeRowsAre(0)
	id, next, err := e.s.AddAt("lead", "wake", "p", mustTime(t, "2026-03-10T09:30:00+09:00"))
	if err != nil || next != "2026-03-10T09:30:00+09:00" {
		t.Fatalf("add = %q %q %v", id, next, err)
	}
	i := e.info("lead", "wake")
	if i.Expr != "" || i.At != "2026-03-10T09:30:00+09:00" || i.TZ != "+09:00" || i.NextAt != i.At {
		t.Fatalf("info = %+v", i)
	}
	// Names stay unique across recurring and one-shot schedules.
	if _, _, err := e.s.Add("lead", "wake", "@daily", "p", ""); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, _, err := e.s.AddAt("worker", "x", "p", mustTime(t, "2026-03-11T00:00:00Z")); !errors.Is(err, ErrNotLead) {
		t.Fatalf("non-lead = %v", err)
	}
}

func TestOneShotFiresOnceAndIsDeleted(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	id := e.addAt("lead", "wake", "2026-03-10T00:10:15Z")
	e.clock.Advance(9*time.Minute + 44*time.Second)
	if n := len(e.sess.held("lead")); n != 0 {
		t.Fatalf("fired early: %d", n)
	}
	e.clock.Advance(time.Second)
	held := e.sess.held("lead")
	if len(held) != 1 || held[0].Cron == nil || held[0].Cron.ID != id || held[0].Cron.At != "2026-03-10T00:10:15Z" || held[0].Text() == "" {
		t.Fatalf("held = %+v", held)
	}
	if n := len(e.s.List("lead")); n != 0 {
		t.Fatalf("a delivered one-shot is still listed: %d", n)
	}
	e.rowsEventually(0)
	e.clock.Advance(48 * time.Hour)
	if n := len(e.sess.held("lead")); n != 1 {
		t.Fatalf("fired again: %d entries", n)
	}
}

// A slept machine wakes long past the one-shot's time: unlike a recurring
// schedule it fires once, late, and is deleted.
func TestOneShotPastItsTimeAfterSleepFiresOnce(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.add("lead", "tick", "*/5 * * * *", "")
	e.addAt("lead", "wake", "2026-03-10T00:03:00Z")
	e.clock.Jump(mustTime(t, "2026-03-10T02:00:00Z"))
	var once, recurring int
	for _, h := range e.sess.held("lead") {
		switch h.Cron.Name {
		case "wake":
			once++
		case "tick":
			recurring++
		}
	}
	if once != 1 || recurring != 0 {
		t.Fatalf("after the jump: one-shot %d (want 1), recurring %d (want 0, stale)", once, recurring)
	}
	e.rowsEventually(1)
}

// A daemon down across the one-shot's time fires it once at start.
func TestOneShotPastItsTimeAcrossARestartFiresOnce(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T00:03:00Z")
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}
	e.clock.mu.Lock()
	e.clock.now = mustTime(t, "2026-03-10T06:00:00Z")
	e.clock.mu.Unlock()
	e.open()
	if i := e.info("lead", "wake"); i.NextAt != "2026-03-10T06:00:00Z" || i.At != "2026-03-10T00:03:00Z" {
		t.Fatalf("info = %+v", i)
	}
	e.clock.Advance(0)
	if n := len(e.sess.held("lead")); n != 1 {
		t.Fatalf("delivered %d, want 1", n)
	}
	e.rowsEventually(0)
	e.restart()
	e.clock.Advance(time.Hour)
	if n := len(e.sess.held("lead")); n != 1 || len(e.s.List("lead")) != 0 {
		t.Fatalf("after another restart: %d entries, %d schedules", n, len(e.s.List("lead")))
	}
}

// A one-shot not yet due survives a restart unchanged and fires at its time.
func TestOneShotPersistsAcrossARestart(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T08:00:00-05:00")
	before := e.s.List("lead")
	e.restart()
	after := e.s.List("lead")
	if len(after) != 1 || after[0] != before[0] {
		t.Fatalf("changed across restart:\n before %+v\n after  %+v", before, after)
	}
	e.clock.Advance(13 * time.Hour)
	if n := len(e.sess.held("lead")); n != 1 {
		t.Fatalf("delivered %d", n)
	}
}

// Concurrent ticks of the same timer generation deliver a due one-shot once.
func TestOneShotConcurrentTicksFireOnce(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T00:01:00Z")
	calls := e.countDeliveries()
	// Time passes without the armed timer running; the ticks race instead.
	e.clock.mu.Lock()
	e.clock.now = mustTime(t, "2026-03-10T00:01:00Z")
	e.clock.mu.Unlock()
	e.s.mu.Lock()
	gen := e.s.gen
	e.s.mu.Unlock()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e.s.fire(gen)
		}()
	}
	close(start)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("Deliver ran %d times", n)
	}
	if n := len(e.sess.held("lead")); n != 1 {
		t.Fatalf("held %d", n)
	}
}

// The fire path never takes opMu or touches the store: a one-shot delivers
// while both are held elsewhere, and its row's delete waits for the writer.
func TestOneShotDeleteStaysOffTheFirePath(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T00:01:00Z")
	ops := e.s.store.Ops()
	e.s.opMu.Lock()
	e.s.store.mu.Lock()
	done := make(chan struct{})
	go func() { e.clock.Advance(time.Minute); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the fire waited on opMu or the store")
	}
	if n := len(e.sess.held("lead")); n != 1 || len(e.s.List("lead")) != 0 {
		t.Fatalf("held %d, listed %d", n, len(e.s.List("lead")))
	}
	e.s.store.mu.Unlock()
	e.s.opMu.Unlock()
	eventually(t, func() bool { return e.s.store.Ops()-ops >= 1 })
	if got := e.s.store.Ops() - ops; got != 1 {
		t.Fatalf("the one-shot's delivery and delete ran %d store operations, want 1", got)
	}
	e.storeRowsAre(0)
}

// A Delete or ConversationGone racing an admitted one-shot delivery owns the
// removal: it waits for the delivery, and the settled fire queues nothing.
func TestOneShotDeliveryOrderedAgainstDeleteAndConversationGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(e *env) error
	}{
		{"delete", func(e *env) error { return e.s.Delete("lead", "wake") }},
		{"gone", func(e *env) error { e.s.ConversationGone("lead"); return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
			e.addAt("lead", "wake", "2026-03-10T00:01:00Z")
			_, release, fired := blockedFire(t, e, 30*time.Second)
			removed := make(chan error, 1)
			go func() { removed <- tc.remove(e) }()
			eventually(t, func() bool { return len(e.s.List("lead")) == 0 })
			gotRemoved := false
			var removeErr error
			select {
			case removeErr = <-removed:
				gotRemoved = true
				if tc.name == "delete" {
					t.Fatalf("Delete returned (%v) while a delivery was admitted", removeErr)
				}
			default:
			}
			release()
			if !gotRemoved {
				removeErr = <-removed
			}
			if removeErr != nil {
				t.Fatal(removeErr)
			}
			<-fired
			e.s.mu.Lock()
			queued := len(e.s.deletes)
			e.s.mu.Unlock()
			if queued != 0 {
				t.Fatalf("the settled fire queued %d deletes for a removed schedule", queued)
			}
			e.s.Flush()
			e.storeRowsAre(0)
			if n := len(e.sess.held("lead")); n != 1 {
				t.Fatalf("held %d", n)
			}
		})
	}
}

func TestOneShotRetriesADeliverErrorThenDeletes(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T00:01:00Z")
	calls := e.countDeliveries()
	e.sess.mu.Lock()
	e.sess.fail = errors.New("inbox busy")
	e.sess.mu.Unlock()
	e.clock.Advance(30 * time.Second)
	if n := calls.Load(); n != 1 {
		t.Fatalf("attempts after the first fire = %d", n)
	}
	if i := e.info("lead", "wake"); i.NextAt != "2026-03-10T00:02:00Z" || i.Pending {
		t.Fatalf("after a failed attempt: %+v", i)
	}
	e.clock.Advance(59 * time.Second)
	if n := calls.Load(); n != 1 {
		t.Fatalf("retried early: %d", n)
	}
	e.clock.Advance(time.Second)
	if n := calls.Load(); n != 2 || len(e.s.List("lead")) != 1 {
		t.Fatalf("second attempt: %d calls, %d listed", n, len(e.s.List("lead")))
	}
	e.clock.Advance(time.Minute)
	if n := calls.Load(); n != 3 || len(e.s.List("lead")) != 0 {
		t.Fatalf("third attempt: %d calls, %d listed", n, len(e.s.List("lead")))
	}
	e.rowsEventually(0)
	e.clock.Advance(time.Hour)
	if n := calls.Load(); n != 3 {
		t.Fatalf("attempted again after deletion: %d", n)
	}
	eventually(t, func() bool { return strings.Contains(e.log.String(), "not delivered after 3 attempts") })
	if c := strings.Count(e.log.String(), "wake"); c != 1 {
		t.Fatalf("logged %d lines about the schedule: %q", c, e.log.String())
	}
}

func TestOneShotRetrySucceedsOnASecondAttempt(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T00:01:00Z")
	e.sess.mu.Lock()
	e.sess.fail = errors.New("inbox busy")
	e.sess.mu.Unlock()
	e.clock.Advance(30 * time.Second)
	e.sess.mu.Lock()
	e.sess.fail = nil
	e.sess.mu.Unlock()
	e.clock.Advance(time.Minute)
	if n := len(e.sess.held("lead")); n != 1 || len(e.s.List("lead")) != 0 {
		t.Fatalf("held %d, listed %d", n, len(e.s.List("lead")))
	}
	e.rowsEventually(0)
}

// A closed or handed-off target deletes the one-shot at once.
func TestOneShotToAClosedTargetIsDeleted(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	e.addAt("lead", "wake", "2026-03-10T00:01:00Z")
	calls := e.countDeliveries()
	e.sess.mu.Lock()
	e.sess.shut["lead"] = true
	e.sess.mu.Unlock()
	e.clock.Advance(time.Hour)
	if n := calls.Load(); n != 1 || len(e.s.List("lead")) != 0 {
		t.Fatalf("%d attempts, %d listed", n, len(e.s.List("lead")))
	}
	e.rowsEventually(0)
	eventually(t, func() bool { return strings.Contains(e.log.String(), "closed or handed off; deleted") })
}

// A name reused while a binary without one-shot support ran (it skipped the
// one-shot row) loads both schedules; a delete by that name asks for the id.
func TestDuplicateStoredNamesLoadAndDeleteById(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	onceID := e.addAt("lead", "wake", "2026-03-10T03:00:00Z")
	if err := e.s.store.Put(Row{ID: "cron-0a0a0a", Owner: "lead", Name: "wake", Expr: "@hourly", TZ: "UTC", Prompt: "p", CreatedAt: e.clock.Now().Unix() + 1}); err != nil {
		t.Fatal(err)
	}
	e.restart()
	list := e.s.List("lead")
	if len(list) != 2 || list[0].Name != "wake" || list[1].Name != "wake" {
		t.Fatalf("list = %+v", list)
	}
	eventually(t, func() bool { return strings.Contains(e.log.String(), "more than one schedule named") })
	if err := e.s.Delete("lead", "wake"); !errors.Is(err, ErrAmbiguousName) {
		t.Fatalf("delete by name = %v", err)
	}
	if err := e.s.Delete("lead", onceID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Delete("lead", "wake"); err != nil {
		t.Fatalf("delete of the remaining one by name = %v", err)
	}
	e.storeRowsAre(0)
}

// A one-shot whose prompt is still held in its inbox at start was delivered
// before a crash that lost its row's delete: it is deleted, not fired again.
func TestOneShotStillHeldAtStartIsDeletedNotRefired(t *testing.T) {
	e := newEnv(t, "2026-03-10T00:00:30Z", "lead")
	if err := e.s.store.Put(Row{ID: "cron-0b0b0b", Owner: "lead", Name: "wake", TZ: "UTC", Prompt: "p", CreatedAt: 1, RunAt: mustTime(t, "2026-03-10T00:00:00Z").Unix()}); err != nil {
		t.Fatal(err)
	}
	mark := wsinbox.Cron{ID: "cron-0b0b0b", Name: "wake", At: "2026-03-10T00:00:00Z"}
	if _, _, err := wsinbox.Send(e.sess.inbox("lead"), wsinbox.Entry{Parts: []string{"p"}, Cron: &mark}, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.restart()
	if n := len(e.s.List("lead")); n != 0 {
		t.Fatalf("listed %d", n)
	}
	e.storeRowsAre(0)
	e.clock.Advance(time.Hour)
	if n := len(e.sess.held("lead")); n != 1 {
		t.Fatalf("refired: %d entries", n)
	}
}

func TestOneShotZoneFollowsTheTimestampOffset(t *testing.T) {
	for _, tc := range []struct{ at, tz string }{
		{"2026-03-11T00:00:00Z", "UTC"},
		{"2026-03-11T00:00:00+00:00", "UTC"},
		{"2026-03-11T09:00:00+09:00", "+09:00"},
		{"2026-03-11T09:00:00-03:30", "-03:30"},
		{"2026-03-11T09:00:00+14:45", "UTC"}, // beyond ParseTZ's range
	} {
		if name, _ := onceZone(mustTime(t, tc.at)); name != tc.tz {
			t.Errorf("%s: tz %s, want %s", tc.at, name, tc.tz)
		}
	}
}
