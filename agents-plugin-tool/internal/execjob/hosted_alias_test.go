package execjob

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wstestutil"
)

// aliasPlaces makes, under one resolved temp directory, a plain directory,
// a stand-in pooled checkout, and a launch root holding the symlink alias
// pointing at target (plain or wt).
func aliasPlaces(t *testing.T, root string) (plain, wt, alias string) {
	t.Helper()
	base := wstestutil.CanonicalTempDir(t)
	plain, wt = filepath.Join(base, "plain"), filepath.Join(base, "wt")
	for _, d := range []string{plain, wt} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias = filepath.Join(root, "alias")
	return plain, wt, alias
}

// pointAlias (re)points alias at target, skipping where symlinks are not
// available.
func pointAlias(t *testing.T, alias, target string) {
	t.Helper()
	_ = os.Remove(alias)
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(alias) })
}

func TestReadOnlyHostedLaunchChecksResolvedCwd(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	base := wstestutil.CanonicalTempDir(t)
	root := filepath.Join(base, "root")
	inside := filepath.Join(root, "sub")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{inside, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	insideLink := filepath.Join(root, "inside-link")
	outsideLink := filepath.Join(root, "outside-link")
	pointAlias(t, insideLink, inside)
	pointAlias(t, outsideLink, outside)

	for name, build := range map[string]func(cwd string) (HostedLaunch, error){
		"exec.run": func(cwd string) (HostedLaunch, error) {
			return ReadOnlyArgvLaunch("lead", "exec.run", root, cwd, []string{"git", "status"}, os.Environ(), nil)
		},
		"exec.shell": func(cwd string) (HostedLaunch, error) {
			return ReadOnlyShellLaunch("lead", "exec.shell", root, cwd, "git status", os.Environ(), nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			outsideReq, err := build(outsideLink)
			if err != nil {
				t.Fatal(err)
			}
			v, err := fx.launch(context.Background(), nil, outsideReq)
			var refusal *ErrReadOnlyLaunch
			if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "outside the worktree root") || v.ID != "" {
				t.Fatalf("outside cwd: job %q, error %v, want read-only refusal before admission", v.ID, err)
			}

			insideReq, err := build(insideLink)
			if err != nil {
				t.Fatal(err)
			}
			insideReq.Async = true
			v, err = fx.launch(context.Background(), nil, insideReq)
			if err != nil || v.RealCwd != inside {
				t.Fatalf("inside cwd: resolved to %q, error %v, want %q", v.RealCwd, err, inside)
			}
		})
	}
}

// A hosted launch whose working directory is an alias resolved into a plain
// directory starts its process there even when the alias is repointed into
// a worktree while the holder starts: the fence an acquisition takes on the
// worktree meanwhile stays consistent (the job is not in it), and the job
// does not run inside it.
func TestHostedSpawnsInAdmittedPlaceAfterAliasRepointed(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	root := wstestutil.CanonicalTempDir(t)
	plain, wt, alias := aliasPlaces(t, root)
	pointAlias(t, alias, plain)

	entered, release := make(chan struct{}), make(chan struct{})
	fx.holders.mu.Lock()
	fx.holders.beforeLaunch = func() { close(entered); <-release }
	fx.holders.mu.Unlock()
	type result struct {
		v   HostedView
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := fx.launch(context.Background(), nil, HostedLaunch{Root: root, Cwd: alias, Async: true})
		done <- result{v, err}
	}()
	<-entered
	pointAlias(t, alias, wt)
	unfence, err := fx.h.FenceRootAs(wt, FenceAcquiring)
	if err != nil {
		t.Fatalf("the worktree's fence refused, though the admitted job is not in it: %v", err)
	}
	defer unfence()
	close(release)
	r := <-done
	if r.err != nil {
		t.Fatalf("launch: %v", r.err)
	}
	if r.v.Cwd != alias || r.v.RealCwd != plain {
		t.Fatalf("row cwd = %q real %q, want %q real %q", r.v.Cwd, r.v.RealCwd, alias, plain)
	}
	fx.holders.mu.Lock()
	spec := fx.holders.spawned[r.v.HolderID]
	fx.holders.mu.Unlock()
	if spec.Cwd != plain {
		t.Fatalf("the process started in %q, want the admitted %q (alias now points at %s)", spec.Cwd, plain, wt)
	}
	if n := fx.h.LiveIn(wt); n != 0 {
		t.Fatalf("LiveIn(worktree) = %d, want 0", n)
	}
}

// The legacy launch does the same: an alias repointed into a worktree after
// admission does not move the process into it.
func TestLegacySpawnsInAdmittedPlaceAfterAliasRepointed(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	root := gitRoot(t)
	plain, wt, alias := aliasPlaces(t, root)
	pointAlias(t, alias, plain)
	var unfence func()
	afterLegacyAdmission = func() {
		pointAlias(t, alias, wt)
		var err error
		if unfence, err = FenceLegacyRootAs(wt, FenceAcquiring); err != nil {
			t.Errorf("the worktree's fence refused, though the admitted job is not in it: %v", err)
		}
	}
	t.Cleanup(func() { afterLegacyAdmission = nil })
	res, err := Launch(LaunchOptions{Root: root, WorkingDir: "alias", Cmd: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "flow"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}})
	afterLegacyAdmission = nil
	if unfence != nil {
		unfence()
	}
	if err != nil {
		t.Fatal(err)
	}
	wd, _, _ := strings.Cut(res.Stdout, "\n")
	if real, err := filepath.EvalSymlinks(wd); err != nil || real != plain {
		t.Fatalf("the process ran in %q (%q, %v), want the admitted %q (alias now points at %s)", wd, real, err, plain, wt)
	}
}

// A replaced daemon restoring a running hosted job keeps the place the job
// started in, from the stored row, after the alias it was launched through
// is repointed: the worktree's fence still refuses.
func TestHostedRestoreKeepsAdmittedPlaceAfterAliasRepointed(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	root := wstestutil.CanonicalTempDir(t)
	plain, wt, alias := aliasPlaces(t, root)
	pointAlias(t, alias, wt)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Root: root, Cwd: alias, Async: true})
	if err != nil {
		t.Fatal(err)
	}
	if row, ok := fx.store.row(v.ID); !ok || row.RealCwd != wt {
		t.Fatalf("stored row = %+v, want RealCwd %q", row, wt)
	}
	_ = fx.h.Close()
	pointAlias(t, alias, plain)
	fx.open(time.Second)
	t.Cleanup(func() { fx.h.Close() })
	if n := fx.h.LiveIn(wt); n != 1 {
		t.Fatalf("LiveIn(worktree) after restore = %d, want 1", n)
	}
	if unfence, err := fx.h.FenceRootAs(wt, FenceAcquiring); err == nil {
		unfence()
		t.Fatalf("the worktree's fence was granted over the restored job started in it")
	}
}

// A launch whose working directory cannot be resolved at admission is
// refused, hosted and legacy alike, before anything asks for approval or
// starts: its spelling was checked only lexically, and could appear as a
// link into a worktree fenced before the process starts.
func TestLaunchRefusesAnUnresolvablePlace(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	root := gitRoot(t)
	missing := filepath.Join(root, "missing")
	const want = "cannot be resolved, so the launch is refused"

	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Root: root, Cwd: missing, Async: true}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("hosted launch in an unresolvable cwd = %v, want %q", err, want)
	}
	fx.holders.mu.Lock()
	launched := len(fx.holders.launched)
	fx.holders.mu.Unlock()
	fx.sessions.mu.Lock()
	approvals := len(fx.sessions.approvals)
	fx.sessions.mu.Unlock()
	fx.store.mu.Lock()
	rows := len(fx.store.rows)
	fx.store.mu.Unlock()
	if launched != 0 || approvals != 0 || rows != 0 {
		t.Fatalf("a refused hosted launch left %d holder(s), %d approval(s), %d row(s)", launched, approvals, rows)
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Root: filepath.Join(root, "gone"), Async: true}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("hosted launch under an unresolvable root = %v, want %q", err, want)
	}

	if _, err := Launch(LaunchOptions{Root: root, WorkingDir: "missing", Cmd: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "ok"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("legacy launch in an unresolvable cwd = %v, want %q", err, want)
	}
}

// A job's admission places are stored even when they equal its recorded
// ones, so a replaced daemon does not resolve them again: a directory the
// job started in, renamed while it runs and replaced by an alias into an
// idle pooled checkout, leaves that checkout reusable after restore.
func TestHostedRestoreOfARenamedPlaceLeavesTheCandidateReusable(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	root := wstestutil.CanonicalTempDir(t)
	_, wt, _ := aliasPlaces(t, root)
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Root: root, Cwd: work, Async: true})
	if err != nil {
		t.Fatal(err)
	}
	if row, ok := fx.store.row(v.ID); !ok || row.RealRoot != root || row.RealCwd != work {
		t.Fatalf("stored row = %+v, want RealRoot %q RealCwd %q", row, root, work)
	}
	_ = fx.h.Close()
	if err := os.Rename(work, work+"-moved"); err != nil {
		t.Fatal(err)
	}
	pointAlias(t, work, wt)
	fx.open(time.Second)
	t.Cleanup(func() { fx.h.Close() })
	if n := fx.h.LiveIn(wt); n != 0 {
		t.Fatalf("LiveIn(candidate) after restore = %d, want 0: the job never ran in it", n)
	}
	unfence, err := fx.h.FenceRootAs(wt, FenceAcquiring)
	if err != nil {
		t.Fatalf("acquire's fence on the candidate refused after restore: %v", err)
	}
	unfence()
}

// The resolved-place columns are a compatible addition to schema version 1:
// a version-1 table an older daemon created gains them on open with its rows
// intact, and an older daemon's upsert (naming only its own columns) leaves
// them as they are.
func TestHostedStoreResolvedPlaceColumnsAreCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec.sqlite")
	oldColumns := strings.TrimSuffix(hostedStoreColumns, ", real_root, real_cwd")
	oldPut := strings.NewReplacer(", real_root, real_cwd)", ")", ", ?, ?)", ")", ", real_root = excluded.real_root,\n\treal_cwd = excluded.real_cwd", "").Replace(hostedStorePut)
	if oldColumns == hostedStoreColumns || strings.Contains(oldPut, "real_") {
		t.Fatalf("could not derive the version-1 statements:\n%s\n%s", oldColumns, oldPut)
	}
	row := hostedStoreRunningRow("exec_old_daemon_row", 1)
	oldArgs := func(r HostedJobRow, pid int) []any {
		return []any{r.ID, r.Legacy, r.Owner, int64(r.Gen), r.Tool, r.Root, r.Cwd, `["sleep","60"]`, r.Command, r.Shell, r.HolderID, pid, r.State, nil, nil, r.Error, r.StartedAt, r.EndedAt, r.Notify, r.Notice, r.StdinBytes, int64(0), int64(0), int64(0), nil, nil, nil}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{hostedStoreSchema, `PRAGMA user_version=1`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(oldPut, oldArgs(row, 1)...); err != nil {
		t.Fatalf("older daemon insert: %v", err)
	}
	_ = db.Close()

	s, err := OpenHostedStore(path)
	if err != nil {
		t.Fatalf("open a version-1 store: %v", err)
	}
	rows, err := s.Load()
	if err != nil || len(rows) != 1 || rows[0].ID != row.ID || rows[0].RealRoot != "" || rows[0].RealCwd != "" {
		t.Fatalf("load the older row = %+v, %v", rows, err)
	}
	row.PID = 1
	row.RealRoot, row.RealCwd = "/real/repo", "/real/repo/sub"
	if err := s.Put(row); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// An older daemon rewrites the row (its holder reported a new pid).
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(oldPut, oldArgs(row, 77)...); err != nil {
		t.Fatalf("older daemon upsert: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM (SELECT ` + oldColumns + ` FROM exec_jobs)`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("older daemon select = %d, %v", n, err)
	}
	_ = db.Close()

	s = hostedStoreTestOpen(t, path)
	rows, err = s.Load()
	if err != nil || len(rows) != 1 || rows[0].PID != 77 || rows[0].RealRoot != "/real/repo" || rows[0].RealCwd != "/real/repo/sub" {
		t.Fatalf("after an older daemon's upsert = %+v, %v", rows, err)
	}
}
