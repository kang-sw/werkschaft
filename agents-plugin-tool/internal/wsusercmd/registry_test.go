package wsusercmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func openTest(t *testing.T, changed func(string)) (*Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), StoreName)
	r, err := Open(path, changed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, path
}

func in(id string) Input {
	return Input{ID: id, Label: "Label " + id, Description: "desc", Line: "echo " + id}
}

func TestRegisterReplaceWithdraw(t *testing.T) {
	var got []string
	r, _ := openTest(t, func(reply string) { got = append(got, reply) })
	c1, out, err := r.Set("lead", "/w", in("a"))
	if err != nil || out != Registered || c1.Revision == 0 {
		t.Fatalf("register: %+v %v %v", c1, out, err)
	}
	// An unchanged definition commits nothing and keeps its revision.
	ops := r.Ops()
	c2, out, err := r.Set("lead", "/w", in("a"))
	if err != nil || out != Unchanged || c2.Revision != c1.Revision || r.Ops() != ops || len(got) != 1 {
		t.Fatalf("unchanged: %+v %v %v ops %d->%d notified %d", c2, out, err, ops, r.Ops(), len(got))
	}
	// A changed one advances the revision, in one commit, keeping its place.
	if _, _, err := r.Set("lead", "/w", in("b")); err != nil {
		t.Fatal(err)
	}
	ops = r.Ops()
	changed := in("a")
	changed.Line = "echo changed"
	c3, out, err := r.Set("lead", "/w", changed)
	if err != nil || out != Replaced || c3.Revision <= c1.Revision || r.Ops() != ops+1 {
		t.Fatalf("replace: %+v %v %v ops %d->%d", c3, out, err, ops, r.Ops())
	}
	list := r.List("lead", "/w")
	if len(list) != 2 || list[0].ID != "a" || list[0].Line != "echo changed" || list[1].ID != "b" {
		t.Fatalf("list: %+v", list)
	}
	ops = r.Ops()
	if err := r.Remove("lead", "/w", "a"); err != nil || r.Ops() != ops+1 {
		t.Fatalf("remove: %v ops %d->%d", err, ops, r.Ops())
	}
	if err := r.Remove("lead", "/w", "a"); !errors.Is(err, ErrUnknown) || r.Ops() != ops+1 {
		t.Fatalf("second remove: %v ops %d", err, r.Ops())
	}
	if _, ok := r.Get("lead", "/w", "a"); ok {
		t.Fatal("withdrawn command still served")
	}
	// A re-registration after a withdrawal never repeats an earlier revision.
	c4, _, err := r.Set("lead", "/w", in("a"))
	if err != nil || c4.Revision <= c3.Revision {
		t.Fatalf("reregister: %+v %v", c4, err)
	}
	if len(got) != 5 {
		t.Fatalf("notified %d times, want one per change", len(got))
	}
}

func TestReadsTouchNoStore(t *testing.T) {
	r, _ := openTest(t, nil)
	for i := range 5 {
		if _, _, err := r.Set("lead", "/w", in(fmt.Sprint("c", i))); err != nil {
			t.Fatal(err)
		}
	}
	ops := r.Ops()
	for range 100 {
		r.List("lead", "/w")
		r.Get("lead", "/w", "c1")
		r.Revision("lead")
	}
	if r.Ops() != ops {
		t.Fatalf("reads ran %d store operations", r.Ops()-ops)
	}
}

func TestReloadScopedToLeadAndWorktree(t *testing.T) {
	r, path := openTest(t, nil)
	cmds := map[[2]string]string{{"lead", "/w1"}: "one", {"lead", "/w2"}: "two", {"other", "/w1"}: "three"}
	for k, id := range cmds {
		if _, _, err := r.Set(k[0], k[1], in(id)); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := r.Get("lead", "/w1", "one")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	for k, id := range cmds {
		list := r2.List(k[0], k[1])
		if len(list) != 1 || list[0].ID != id {
			t.Fatalf("%v: %+v", k, list)
		}
	}
	if got := r2.List("lead", "/elsewhere"); len(got) != 0 {
		t.Fatalf("another worktree sees %+v", got)
	}
	if got := r2.List("stranger", "/w1"); len(got) != 0 {
		t.Fatalf("another lead sees %+v", got)
	}
	after, _ := r2.Get("lead", "/w1", "one")
	if after != before {
		t.Fatalf("reload changed the definition: %+v -> %+v", before, after)
	}
	// A revision issued after the reload is above every stored one.
	c, _, err := r2.Set("lead", "/w1", Input{ID: "one", Label: "x", Line: "true"})
	if err != nil || c.Revision <= before.Revision {
		t.Fatalf("revision after reload: %+v %v", c, err)
	}
}

// TestRevisionNeverReusedAfterWithdrawAndRestart: a withdrawn command's
// revision is never issued again, whatever the clock does across the restart
// (frozen at the same instant, or set back): the store keeps the highest
// revision ever issued.
func TestRevisionNeverReusedAfterWithdrawAndRestart(t *testing.T) {
	for name, restartClock := range map[string]int64{"same instant": 1_000_000, "set back": 5, "forward": 9_000_000} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), StoreName)
			r, err := openClock(path, nil, func() int64 { return 1_000_000 })
			if err != nil {
				t.Fatal(err)
			}
			c, _, _ := r.Set("lead", "/w", in("a"))
			// Several issued and withdrawn: the last withdrawal is the highest.
			c2, _, _ := r.Set("lead", "/w", in("b"))
			if err := r.Remove("lead", "/w", "a"); err != nil {
				t.Fatal(err)
			}
			if err := r.Remove("lead", "/w", "b"); err != nil {
				t.Fatal(err)
			}
			_ = r.Close()
			r2, err := openClock(path, nil, func() int64 { return restartClock })
			if err != nil {
				t.Fatal(err)
			}
			defer r2.Close()
			again, _, err := r2.Set("lead", "/w", Input{ID: "a", Label: "other", Line: "rm -rf x"})
			if err != nil || again.Revision <= c.Revision || again.Revision <= c2.Revision {
				t.Fatalf("a page holding revision %d/%d would match the new definition %d (%v)", c.Revision, c2.Revision, again.Revision, err)
			}
		})
	}
}

// TestSnapshotPairsTheListWithItsRevision: a list and the revision it is
// stamped with come from one read, so a concurrent change never leaves an old
// list carrying the new revision.
func TestSnapshotPairsTheListWithItsRevision(t *testing.T) {
	r, _ := openTest(t, nil)
	if _, _, err := r.Set("lead", "/w", in("seed")); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprint("c", i%5)
			in := in(id)
			in.Line = fmt.Sprint("echo ", i)
			if i%3 == 2 {
				_ = r.Remove("lead", "/w", id)
			} else {
				_, _, _ = r.Set("lead", "/w", in)
			}
		}
	}()
	// Each snapshot's revision names the change count that produced its list: the
	// same revision must always carry the same list.
	seen := map[string]string{}
	for range 4000 {
		cmds, rev := r.Snapshot("lead", "/w")
		var b strings.Builder
		for _, c := range cmds {
			fmt.Fprintf(&b, "%s@%d;", c.ID, c.Revision)
		}
		if prior, ok := seen[rev]; ok && prior != b.String() {
			close(stop)
			wg.Wait()
			t.Fatalf("revision %s carried two lists: %q and %q", rev, prior, b.String())
		}
		seen[rev] = b.String()
	}
	close(stop)
	wg.Wait()
}

// TestRemoveIfComparesTheRevisionUnderTheWriterLock: a conditional removal
// removes only the revision the user saw; a replacement that landed since is
// kept, a missing revision is refused, and the by-ID removal is unchanged.
func TestRemoveIfComparesTheRevisionUnderTheWriterLock(t *testing.T) {
	r, _ := openTest(t, nil)
	first, _, _ := r.Set("lead", "/w", in("a"))
	if err := r.RemoveIf("lead", "/w", "a", 0); !errors.Is(err, ErrStale) {
		t.Fatalf("zero revision: %v", err)
	}
	repl := in("a")
	repl.Line = "echo replaced"
	second, _, _ := r.Set("lead", "/w", repl)
	if err := r.RemoveIf("lead", "/w", "a", first.Revision); !errors.Is(err, ErrStale) {
		t.Fatalf("predecessor's revision: %v", err)
	}
	if got, ok := r.Get("lead", "/w", "a"); !ok || got != second {
		t.Fatalf("the replacement was removed: %+v %v", got, ok)
	}
	if err := r.RemoveIf("lead", "/w", "nope", 1); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
	// Racing a replacement against removals that name the replaced revision:
	// whichever order they run in, the surviving command is never one the
	// removal's revision did not name.
	for i := range 200 {
		cur, _ := r.Get("lead", "/w", "a")
		next := in("a")
		next.Line = fmt.Sprint("echo ", i)
		var wg sync.WaitGroup
		wg.Add(2)
		var rmErr error
		go func() { defer wg.Done(); _, _, _ = r.Set("lead", "/w", next) }()
		go func() { defer wg.Done(); rmErr = r.RemoveIf("lead", "/w", "a", cur.Revision) }()
		wg.Wait()
		got, ok := r.Get("lead", "/w", "a")
		switch {
		case rmErr == nil && ok && got.Revision == cur.Revision:
			t.Fatalf("round %d: removed yet still there", i)
		case rmErr == nil && ok && got.Line != next.Line:
			t.Fatalf("round %d: the survivor is not the replacement", i)
		case errors.Is(rmErr, ErrStale) && (!ok || got.Line != next.Line):
			t.Fatalf("round %d: stale removal lost the replacement", i)
		}
		if !ok {
			_, _, _ = r.Set("lead", "/w", in("a"))
		}
	}
	if err := r.Remove("lead", "/w", "a"); err != nil {
		t.Fatalf("by-ID removal: %v", err)
	}
}

func TestLimits(t *testing.T) {
	r, _ := openTest(t, nil)
	for i := range MaxPerScope {
		if _, _, err := r.Set("lead", "/w", in(fmt.Sprint("c", i))); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
	}
	if _, _, err := r.Set("lead", "/w", in("one-more")); !errors.Is(err, ErrFull) {
		t.Fatalf("33rd command: %v", err)
	}
	// Replacing one at the cap succeeds, and another worktree has its own cap.
	repl := in("c3")
	repl.Line = "echo replaced"
	if _, out, err := r.Set("lead", "/w", repl); err != nil || out != Replaced {
		t.Fatalf("replace at cap: %v %v", out, err)
	}
	if _, _, err := r.Set("lead", "/w2", in("one-more")); err != nil {
		t.Fatalf("other worktree: %v", err)
	}
	long := func(n int) string { return strings.Repeat("x", n) }
	for name, bad := range map[string]Input{
		"label":       {ID: "a", Label: long(MaxLabel + 1), Line: "true"},
		"description": {ID: "a", Label: "l", Description: long(MaxDescription + 1), Line: "true"},
		"line":        {ID: "a", Label: "l", Line: long(MaxLine + 1)},
		"blank line":  {ID: "a", Label: "l", Line: " \n"},
		"blank label": {ID: "a", Label: " ", Line: "true"},
		"newline":     {ID: "a", Label: "two\nlines", Line: "true"},
		"bad id":      {ID: "a b", Label: "l", Line: "true"},
		"nul":         {ID: "a", Label: "l", Line: "true\x00"},
		"utf8":        {ID: "a", Label: "l", Line: "\xff"},
	} {
		if _, _, err := r.Set("lead", "/w3", bad); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// Exactly at the limits is fine; the label counts characters, not bytes.
	if _, _, err := r.Set("lead", "/w3", Input{ID: "a", Label: strings.Repeat("가", MaxLabel), Description: long(MaxDescription), Line: long(MaxLine)}); err != nil {
		t.Fatalf("at the limits: %v", err)
	}
	if got := r.List("lead", "/w3"); len(got) != 1 {
		t.Fatalf("refused registrations left %+v", got)
	}
}

func TestRevisionChangesPerChange(t *testing.T) {
	r, _ := openTest(t, nil)
	rev0 := r.Revision("lead")
	_, _, _ = r.Set("lead", "/w", in("a"))
	rev1 := r.Revision("lead")
	_, _, _ = r.Set("lead", "/w", in("a")) // unchanged
	if r.Revision("lead") != rev1 || rev1 == rev0 {
		t.Fatalf("revisions %q %q %q", rev0, rev1, r.Revision("lead"))
	}
	_ = r.Remove("lead", "/w", "a")
	if r.Revision("lead") == rev1 {
		t.Fatal("a removal left the revision")
	}
	if r.Revision("other") != rev0 {
		t.Fatalf("another lead's revision moved: %q", r.Revision("other"))
	}
}

func TestReconcileDropsUnknownLeads(t *testing.T) {
	r, path := openTest(t, nil)
	_, _, _ = r.Set("live", "/w", in("a"))
	_, _, _ = r.Set("dead", "/w", in("a"))
	ops := r.Ops()
	if err := r.Reconcile(func(string) bool { return true }); err != nil || r.Ops() != ops {
		t.Fatalf("nothing to drop committed: %v ops %d->%d", err, ops, r.Ops())
	}
	if err := r.Reconcile(func(id string) bool { return id == "live" }); err != nil || r.Ops() != ops+1 {
		t.Fatalf("reconcile: %v ops %d->%d", err, ops, r.Ops())
	}
	_ = r.Close()
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if len(r2.List("live", "/w")) != 1 || len(r2.List("dead", "/w")) != 0 {
		t.Fatal("reconcile did not persist")
	}
}

func TestConcurrentWriters(t *testing.T) {
	r, _ := openTest(t, nil)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 10 {
				_, _, _ = r.Set("lead", "/w", in(fmt.Sprintf("c%d-%d", i, j)))
				r.List("lead", "/w")
			}
		}()
	}
	wg.Wait()
	if n := len(r.List("lead", "/w")); n != MaxPerScope {
		t.Fatalf("%d commands, the cap is %d", n, MaxPerScope)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreName)
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`PRAGMA user_version=99`); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if _, err := Open(path, nil); !errors.Is(err, ErrStoreNewer) {
		t.Fatalf("newer schema: %v", err)
	}
}

// TestStoreMigrationBackfillsTheRevisionHighWater: a version-1 database (rows,
// no high-water) opens at the highest surviving revision, so the counter never
// starts below a revision a page may still hold.
func TestStoreMigrationBackfillsTheRevisionHighWater(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE user_commands (reply TEXT NOT NULL, root TEXT NOT NULL, id TEXT NOT NULL, label TEXT NOT NULL, description TEXT NOT NULL, line TEXT NOT NULL, revision INTEGER NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY (reply, root, id))`,
		`INSERT INTO user_commands VALUES ('lead', '/w', 'a', 'A', '', 'echo a', 7, 1), ('lead', '/w', 'b', 'B', '', 'echo b', 9, 2)`,
		`PRAGMA user_version=1`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.highWater(); err != nil || got != 9 {
		t.Fatalf("high-water after the upgrade = %d, %v; want 9", got, err)
	}
}

// TestStoreV2InPlaceUpgradeKeepsSchemaVersionTwo: an existing schema-v2 DB
// (pre-v2 feature: no root_user_commands table, no prompt/cwd columns on
// user_commands) opens cleanly, preserves PRAGMA user_version == 2 (T0/T1
// patch-safe), preserves existing rows, and performs zero write ops on a
// subsequent reopen.
func TestStoreV2InPlaceUpgradeKeepsSchemaVersionTwo(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE user_commands (reply TEXT NOT NULL, root TEXT NOT NULL, id TEXT NOT NULL, label TEXT NOT NULL, description TEXT NOT NULL, line TEXT NOT NULL, revision INTEGER NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY (reply, root, id))`,
		`CREATE TABLE meta (k TEXT PRIMARY KEY, v INTEGER NOT NULL)`,
		`INSERT INTO meta VALUES ('revision', 12)`,
		`INSERT INTO user_commands VALUES ('lead', '/w', 'a', 'A', 'desc', 'echo a', 12, 1)`,
		`PRAGMA user_version=2`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open pre-extension v2 db: %v", err)
	}
	var ver int
	if err := r.store.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != 2 {
		t.Fatalf("user_version = %d, %v; want 2", ver, err)
	}
	got, ok := r.GetBySource("lead", "/w", SourceAgent, "a")
	if !ok || got.Line != "echo a" || got.Prompt != "" || got.Cwd != "" || got.Source != SourceAgent {
		t.Fatalf("existing row after upgrade: %+v %v", got, ok)
	}
	// Create a user-scoped command and an agent command with prompt+cwd.
	u, err := r.CreateUser("/w", Input{Label: "Lint", Line: "make lint", Prompt: "Fix lint", Cwd: "pkg"})
	if err != nil || u.Source != SourceUser || u.Revision <= 12 || u.Prompt != "Fix lint" || u.Cwd != "pkg" {
		t.Fatalf("create user: %+v %v", u, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Second reopen performs only the two startup reads (load + highWater),
	// zero DDL writes, and restores both scopes.
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r2.Close()
	if r2.Ops() != 2 {
		t.Fatalf("reopen performed %d ops, want 2 (load + highWater only)", r2.Ops())
	}
	list := r2.List("lead", "/w")
	if len(list) != 2 || list[0].Source != SourceAgent || list[0].ID != "a" || list[1].Source != SourceUser || list[1].ID != u.ID || list[1].Prompt != "Fix lint" || list[1].Cwd != "pkg" {
		t.Fatalf("reopened list: %+v", list)
	}
}

// TestDualScopeCoexistencePatchAndReconcile: agent and user commands share
// one root list ordered by seq, may share the same ID without colliding,
// support PatchIf across both scopes without changing Source or seq, and
// Reconcile drops dead sessions' agent commands while leaving root_user_commands
// untouched.
func TestDualScopeCoexistencePatchAndReconcile(t *testing.T) {
	var notified []string
	r, path := openTest(t, func(reply string) { notified = append(notified, reply) })

	a, _, err := r.Set("live", "/w", Input{ID: "build", Label: "Agent Build", Line: "make agent", Prompt: "Check agent build", Cwd: "cmd"})
	if err != nil || a.Source != SourceAgent || a.Prompt != "Check agent build" || a.Cwd != "cmd" {
		t.Fatalf("agent set: %+v %v", a, err)
	}
	u, err := r.CreateUser("/w", Input{ID: "build", Label: "User Build", Line: "make user", Prompt: "Check user build", Cwd: "pkg"})
	if err != nil || u.Source != SourceUser || u.ID != "build" {
		t.Fatalf("user create same id: %+v %v", u, err)
	}
	// Duplicate user ID returns ErrExists.
	if _, err := r.CreateUser("/w", Input{ID: "build", Label: "Dup", Line: "true"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate user id: %v", err)
	}
	// Auto-slug generation and collision suffixing.
	s1, err := r.CreateUser("/w", Input{Label: "Run Tests!", Line: "go test ./..."})
	if err != nil || s1.ID != "run-tests" {
		t.Fatalf("slug 1: %+v %v", s1, err)
	}
	s2, err := r.CreateUser("/w", Input{Label: "Run Tests!", Line: "go test -race ./..."})
	if err != nil || !strings.HasPrefix(s2.ID, "run-tests-") || s2.ID == s1.ID || !ValidID(s2.ID) {
		t.Fatalf("slug 2: %+v %v", s2, err)
	}
	// Non-ASCII label falls back to "cmd" slug.
	s3, err := r.CreateUser("/w", Input{Label: "빌드", Line: "make"})
	if err != nil || s3.ID != "cmd" {
		t.Fatalf("non-ascii slug: %+v %v", s3, err)
	}

	// Merged list for "live" has both "build" entries in seq order; another session
	// on the same root sees user commands only; another root sees none.
	liveList, revLive := r.Snapshot("live", "/w")
	if len(liveList) != 5 || liveList[0].Source != SourceAgent || liveList[1].Source != SourceUser || liveList[1].ID != "build" {
		t.Fatalf("live list: %+v", liveList)
	}
	if got := r.RevisionFor("live", "/w"); got != revLive {
		t.Fatalf("RevisionFor %q != Snapshot rev %q", got, revLive)
	}
	otherList := r.List("other", "/w")
	if len(otherList) != 4 {
		t.Fatalf("other session on same root: %+v", otherList)
	}
	if len(r.List("other", "/other-root")) != 0 {
		t.Fatal("other root leaked user commands")
	}

	// PatchIf updates line and prompt atomically on both scopes, keeping Source and seq.
	newLine := "make agent-v2"
	newPrompt := "Updated prompt"
	a2, err := r.PatchIf("live", "/w", SourceAgent, "build", a.Revision, Patch{Line: &newLine, Prompt: &newPrompt})
	if err != nil || a2.Source != SourceAgent || a2.Line != newLine || a2.Prompt != newPrompt || a2.Cwd != "cmd" || a2.Revision <= a.Revision {
		t.Fatalf("patch agent: %+v %v", a2, err)
	}
	// Stale revision refused.
	if _, err := r.PatchIf("live", "/w", SourceAgent, "build", a.Revision, Patch{Line: &newLine}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale patch: %v", err)
	}
	// Patch user command.
	uLine := "make user-v2"
	u2, err := r.PatchIf("other", "/w", SourceUser, "build", u.Revision, Patch{Line: &uLine})
	if err != nil || u2.Source != SourceUser || u2.Line != uLine || u2.Prompt != "Check user build" {
		t.Fatalf("patch user: %+v %v", u2, err)
	}
	// Order preserved: agent "build" still precedes user "build".
	afterPatch := r.List("live", "/w")
	if afterPatch[0].Source != SourceAgent || afterPatch[0].Line != newLine || afterPatch[1].Source != SourceUser || afterPatch[1].Line != uLine {
		t.Fatalf("order after patch: %+v", afterPatch[:2])
	}

	// RemoveIfSource removes only the named scope.
	if err := r.RemoveIfSource("live", "/w", SourceUser, "build", u.Revision); !errors.Is(err, ErrStale) {
		t.Fatalf("stale user remove: %v", err)
	}
	if err := r.RemoveIfSource("live", "/w", SourceUser, "build", u2.Revision); err != nil {
		t.Fatalf("user remove: %v", err)
	}
	if _, ok := r.GetBySource("live", "/w", SourceAgent, "build"); !ok {
		t.Fatal("removing user command removed same-id agent command")
	}
	if _, ok := r.GetBySource("live", "/w", SourceUser, "build"); ok {
		t.Fatal("removed user command still present")
	}

	// Reconcile drops dead agent session commands but leaves user commands untouched.
	if _, _, err := r.Set("dead", "/w", in("deadcmd")); err != nil {
		t.Fatal(err)
	}
	if err := r.Reconcile(func(id string) bool { return id == "live" }); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if got := r2.List("dead", "/w"); len(got) != 3 { // only the 3 surviving user commands
		t.Fatalf("after reconcile dead session sees %+v", got)
	}
	for _, c := range r2.List("dead", "/w") {
		if c.Source != SourceUser {
			t.Fatalf("dead agent command survived reconcile: %+v", c)
		}
	}
}

func TestValidateWorktreeCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "backend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	if _, err := ValidateWorktreeCwd(root, ""); err != nil {
		t.Fatalf("empty cwd: %v", err)
	}
	if _, err := ValidateWorktreeCwd(root, "backend"); err != nil {
		t.Fatalf("valid subdir: %v", err)
	}
	for name, bad := range map[string]string{
		"escape":   "../escape",
		"missing":  "does-not-exist",
		"file":     "file.txt",
		"absolute": outside,
		"newline":  "backend\n",
		"nul":      "backend\x00",
	} {
		_, err := ValidateWorktreeCwd(root, bad)
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s (%q): got %v, want ErrInvalid", name, bad, err)
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "escape-link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateWorktreeCwd(root, "escape-link"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("symlink escape: got %v, want ErrInvalid", err)
		}
	}
}
