package execjob

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func hostedStoreTestOpen(t *testing.T, path string) *SQLiteHostedStore {
	t.Helper()
	s, err := OpenHostedStore(path)
	if err != nil {
		t.Fatalf("OpenHostedStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func hostedStoreFullRow() HostedJobRow {
	zero := 0
	return HostedJobRow{
		ID:         "exec_alpha_bravo_charlie",
		Legacy:     true,
		Owner:      "reply-owner",
		Gen:        1 << 40,
		Tool:       "exec.run",
		Root:       "/repo",
		Cwd:        "/repo/sub",
		Argv:       []string{"sh", "-c", "echo hi"},
		Command:    "echo hi",
		Shell:      "sh",
		HolderID:   "holder-1",
		PID:        4321,
		State:      HostedExited,
		ExitCode:   &zero,
		Signal:     nil,
		Error:      "some error",
		StartedAt:  1700000000000,
		EndedAt:    1700000001234,
		Notify:     true,
		Notice:     NoticeDelivered,
		StdinBytes: 17,
		Written:    [3]int64{StreamCombined: 30, StreamStdout: 20, StreamStderr: 10},
		Tails: [3][]byte{
			StreamCombined: []byte("out\nerr\n"),
			StreamStdout:   []byte("out\n"),
			StreamStderr:   []byte("err\x00\xff\n"),
		},
	}
}

func hostedStoreRunningRow(id string, started int64) HostedJobRow {
	return HostedJobRow{
		ID:        id,
		Owner:     "reply-owner",
		Gen:       3,
		Tool:      "exec.shell",
		Root:      "/repo",
		Cwd:       "/repo",
		Argv:      []string{"sleep", "60"},
		HolderID:  "holder-" + id,
		PID:       99,
		State:     HostedRunning,
		StartedAt: started,
	}
}

func TestHostedStoreRoundTripsEveryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "exec.sqlite")
	s := hostedStoreTestOpen(t, path)
	full := hostedStoreFullRow()
	running := hostedStoreRunningRow("exec_delta_echo_fox", full.StartedAt+1)
	signal := 9
	maxGen := hostedStoreRunningRow("exec_max_gen", full.StartedAt+2)
	maxGen.Gen = ^uint64(0)
	maxGen.State = HostedStopped
	maxGen.Signal = &signal
	for _, row := range []HostedJobRow{running, full, maxGen} {
		if err := s.Put(row); err != nil {
			t.Fatalf("Put %s: %v", row.ID, err)
		}
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []HostedJobRow{full, running, maxGen}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if got[0].ExitCode == nil || *got[0].ExitCode != 0 || got[0].Signal != nil {
		t.Fatalf("exit fields: exit=%v signal=%v", got[0].ExitCode, got[0].Signal)
	}
	if got[1].ExitCode != nil || got[1].Signal != nil {
		t.Fatalf("running row exit fields not nil: exit=%v signal=%v", got[1].ExitCode, got[1].Signal)
	}
	for i, tail := range got[1].Tails {
		if tail != nil {
			t.Fatalf("running row tail %d = %q, want nil", i, tail)
		}
	}
}

func TestHostedStorePutReplacesExistingID(t *testing.T) {
	s := hostedStoreTestOpen(t, filepath.Join(t.TempDir(), "exec.sqlite"))
	row := hostedStoreRunningRow("exec_one", 10)
	if err := s.Put(row); err != nil {
		t.Fatal(err)
	}
	code := 2
	row.State, row.ExitCode, row.EndedAt = HostedExited, &code, 20
	row.Notice = NoticePending
	row.Written[StreamStdout] = 5
	row.Tails[StreamStdout] = []byte("hello")
	if err := s.Put(row); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], row) {
		t.Fatalf("after replace: %#v", got)
	}
}

func TestHostedStoreDeleteSeveralIDs(t *testing.T) {
	s := hostedStoreTestOpen(t, filepath.Join(t.TempDir(), "exec.sqlite"))
	for i := 0; i < 5; i++ {
		if err := s.Put(hostedStoreRunningRow(fmt.Sprintf("exec_%d", i), int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("empty Delete: %v", err)
	}
	if err := s.Delete("exec_1", "exec_3", "exec_missing"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if want := []string{"exec_0", "exec_2", "exec_4"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids after delete = %v, want %v", ids, want)
	}
}

func TestHostedStoreReopenReturnsSameRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec.sqlite")
	s, err := OpenHostedStore(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []HostedJobRow{hostedStoreFullRow(), hostedStoreRunningRow("exec_later", hostedStoreFullRow().StartedAt+5)}
	for _, r := range want {
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := hostedStoreTestOpen(t, path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestHostedStoreRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, hostedStoreSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenHostedStore(path)
	if err == nil {
		_ = s.Close()
		t.Fatal("OpenHostedStore accepted a newer schema")
	}
	if !errors.Is(err, ErrHostedStoreNewer) {
		t.Fatalf("error %v does not wrap ErrHostedStoreNewer", err)
	}
	// The refusal leaves the database untouched: no table was created.
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'exec_jobs'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused open created exec_jobs")
	}
}

func TestHostedStoreClosedAnswers(t *testing.T) {
	s, err := OpenHostedStore(filepath.Join(t.TempDir(), "exec.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrHostedStoreClosed) {
		t.Fatalf("Load after Close: %v", err)
	}
	if err := s.Put(hostedStoreRunningRow("exec_x", 1)); !errors.Is(err, ErrHostedStoreClosed) {
		t.Fatalf("Put after Close: %v", err)
	}
	if err := s.Delete("exec_x"); !errors.Is(err, ErrHostedStoreClosed) {
		t.Fatalf("Delete after Close: %v", err)
	}
	if err := s.Delete(); !errors.Is(err, ErrHostedStoreClosed) {
		t.Fatalf("empty Delete after Close: %v", err)
	}
}

func TestHostedStoreConcurrentPuts(t *testing.T) {
	s := hostedStoreTestOpen(t, filepath.Join(t.TempDir(), "exec.sqlite"))
	const workers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				row := hostedStoreRunningRow(fmt.Sprintf("exec_%d_%d", w, i), int64(w*each+i))
				if err := s.Put(row); err != nil {
					errs <- err
					return
				}
				// Rewrite the same id to exercise the update path too.
				row.Written[StreamCombined] = int64(i)
				if err := s.Put(row); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Put: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != workers*each {
		t.Fatalf("rows = %d, want %d", len(got), workers*each)
	}
}
