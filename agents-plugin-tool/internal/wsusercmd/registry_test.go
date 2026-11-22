package wsusercmd

import (
	"errors"
	"fmt"
	"path/filepath"
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

func TestRevisionNeverReusedAfterWithdrawAndRestart(t *testing.T) {
	r, path := openTest(t, nil)
	c, _, _ := r.Set("lead", "/w", in("a"))
	if err := r.Remove("lead", "/w", "a"); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	r2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	again, _, err := r2.Set("lead", "/w", Input{ID: "a", Label: "other", Line: "rm -rf x"})
	if err != nil || again.Revision <= c.Revision {
		t.Fatalf("a page holding revision %d would match the new definition %d", c.Revision, again.Revision)
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
