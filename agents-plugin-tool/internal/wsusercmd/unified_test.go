package wsusercmd

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestUserCommandUnifiedWorktreeScope: agent and user commands share one
// worktree list ordered by seq and one cap, an agent registration is visible to
// every conversation of the worktree and survives a restart, an ID is unique
// per worktree (the agent never overwrites a user command), and PatchIf/Remove
// keep Source and seq.
func TestUserCommandUnifiedWorktreeScope(t *testing.T) {
	r, path := openTest(t, nil)

	a, _, err := r.Set("live", "/w", Input{ID: "build", Label: "Agent Build", Line: "make agent", Prompt: "Check agent build", Cwd: "cmd"})
	if err != nil || a.Source != SourceAgent || a.Prompt != "Check agent build" || a.Cwd != "cmd" {
		t.Fatalf("agent set: %+v %v", a, err)
	}
	if _, err := r.CreateUser("/w", Input{ID: "build", Label: "User Build", Line: "make user"}); !errors.Is(err, ErrExists) {
		t.Fatalf("user create over agent id: %v", err)
	}
	u, err := r.CreateUser("/w", Input{ID: "mine", Label: "Mine", Line: "make user", Prompt: "p", Cwd: "pkg"})
	if err != nil || u.Source != SourceUser {
		t.Fatalf("user create: %+v %v", u, err)
	}
	if _, _, err := r.Set("live", "/w", Input{ID: "mine", Label: "Hijack", Line: "true"}); !errors.Is(err, ErrExists) {
		t.Fatalf("agent set over user id: %v", err)
	}
	s1, err := r.CreateUser("/w", Input{Label: "Run Tests!", Line: "go test ./..."})
	if err != nil || s1.ID != "run-tests" {
		t.Fatalf("slug: %+v %v", s1, err)
	}

	if got := r.List("other", "/w"); len(got) != 3 || got[0].Source != SourceAgent || got[1].Source != SourceUser {
		t.Fatalf("other conversation: %+v", got)
	}
	if len(r.List("other", "/other-root")) != 0 {
		t.Fatal("other root leaked commands")
	}
	if _, rev := r.Snapshot("live", "/w"); rev != r.RevisionFor("other", "/w") {
		t.Fatal("conversations of one worktree must share the revision")
	}

	newLine := "make agent-v2"
	a2, err := r.PatchIf("other", "/w", SourceAgent, "build", a.Revision, Patch{Line: &newLine})
	if err != nil || a2.Source != SourceAgent || a2.Line != newLine || a2.Cwd != "cmd" || a2.Revision <= a.Revision {
		t.Fatalf("patch agent: %+v %v", a2, err)
	}
	if _, err := r.PatchIf("other", "/w", SourceAgent, "build", a.Revision, Patch{Line: &newLine}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale patch: %v", err)
	}
	if _, err := r.PatchIf("live", "/w", SourceUser, "build", a2.Revision, Patch{Line: &newLine}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("source mismatch patch: %v", err)
	}
	if got := r.List("live", "/w"); got[0].ID != "build" || got[0].Line != newLine {
		t.Fatalf("order after patch: %+v", got)
	}

	if err := r.Remove("live", "/w", "mine"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("agent remove of user command: %v", err)
	}
	if err := r.RemoveIfSource("live", "/w", SourceUser, "mine", u.Revision); err != nil {
		t.Fatalf("user remove: %v", err)
	}

	_ = r.Close()
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	got := r2.List("anybody", "/w")
	if len(got) != 2 || got[0].ID != "build" || got[0].Source != SourceAgent || got[1].ID != "run-tests" || got[1].Source != SourceUser {
		t.Fatalf("after restart: %+v", got)
	}
}

func TestUserCommandSharedCapAcrossSources(t *testing.T) {
	r, _ := openTest(t, nil)
	for i := 0; i < MaxPerScope/2; i++ {
		if _, _, err := r.Set("lead", "/w", in(fmt.Sprintf("a%d", i))); err != nil {
			t.Fatal(err)
		}
		if _, err := r.CreateUser("/w", Input{ID: fmt.Sprintf("u%d", i), Label: "u", Line: "true"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.Set("lead", "/w", in("extra")); !errors.Is(err, ErrFull) {
		t.Fatalf("agent over shared cap: %v", err)
	}
	if _, err := r.CreateUser("/w", Input{ID: "extra", Label: "x", Line: "true"}); !errors.Is(err, ErrFull) {
		t.Fatalf("user over shared cap: %v", err)
	}
	if _, out, err := r.Set("lead", "/w", Input{ID: "a0", Label: "changed", Line: "true"}); err != nil || out != Replaced {
		t.Fatalf("replace at cap: %v %v", out, err)
	}
}

// seedLegacy builds a version-2 store whose session-scoped user_commands table
// holds rows and whose root_user_commands lacks the source column and the
// migration flag (what the previous build left behind).
func seedLegacy(t *testing.T, path string) {
	t.Helper()
	root := normRoot("/w")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE user_commands (reply TEXT NOT NULL, root TEXT NOT NULL, id TEXT NOT NULL, label TEXT NOT NULL, description TEXT NOT NULL, line TEXT NOT NULL, prompt TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY (reply, root, id))",
		"CREATE TABLE root_user_commands (root TEXT NOT NULL, id TEXT NOT NULL, label TEXT NOT NULL, description TEXT NOT NULL, line TEXT NOT NULL, prompt TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL, seq INTEGER NOT NULL, PRIMARY KEY (root, id))",
		"CREATE TABLE meta (k TEXT PRIMARY KEY, v INTEGER NOT NULL)",
		"INSERT INTO meta VALUES ('revision', 50)",
		"INSERT INTO user_commands VALUES ('s1', 'ROOT', 'only', 'Only', '', 'echo only', '', '', 10, 1)",
		"INSERT INTO user_commands VALUES ('s1', 'ROOT', 'clash', 'S1 clash', '', 'echo s1', '', '', 20, 2)",
		"INSERT INTO user_commands VALUES ('s2', 'ROOT', 'clash', 'S2 clash', '', 'echo s2', '', '', 30, 3)",
		"INSERT INTO user_commands VALUES ('s1', 'ROOT', 'mine', 'Agent mine', '', 'echo agent', '', '', 40, 4)",
		"INSERT INTO root_user_commands VALUES ('ROOT', 'mine', 'User mine', '', 'echo user', '', '', 5, 5)",
		"PRAGMA user_version=2",
	} {
		q = strings.ReplaceAll(q, "ROOT", root)
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestUserCommandEnsureV2ExtensionsSourceColumnAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreName)
	seedLegacy(t, path)
	r, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Command{}
	for _, c := range r.List("any", "/w") {
		got[c.ID] = c
	}
	if len(got) != 3 {
		t.Fatalf("migrated list: %+v", got)
	}
	if got["mine"].Label != "User mine" || got["mine"].Source != SourceUser {
		t.Fatalf("existing root row must win: %+v", got["mine"])
	}
	if got["clash"].Label != "S2 clash" || got["clash"].Source != SourceAgent {
		t.Fatalf("newest revision must win among sessions: %+v", got["clash"])
	}
	if got["only"].Source != SourceAgent {
		t.Fatalf("migrated rows are agent commands: %+v", got["only"])
	}
	if c, _, err := r.Set("s3", "/w", in("fresh")); err != nil || c.Revision <= 50 {
		t.Fatalf("revision after migration: %+v %v", c, err)
	}
	_ = r.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n, version int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_commands`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("legacy table rows %d %v", n, err)
	}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 2 || storeSchemaVersion != 2 {
		t.Fatalf("schema version %d %v", version, err)
	}
	// One time only: a legacy row written afterwards (an older build) is not copied.
	if _, err := db.Exec("INSERT INTO user_commands VALUES ('s9', '" + normRoot("/w") + "', 'late', 'Late', '', 'true', '', '', 99, 9)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if _, ok := r2.GetBySource("x", "/w", SourceAgent, "late"); ok {
		t.Fatal("migration ran twice")
	}
	if len(r2.List("x", "/w")) != 4 {
		t.Fatalf("reopen list: %+v", r2.List("x", "/w"))
	}
}
