package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestModelFileProtectionSkipsIdentityScanForSingleLinkEdit(t *testing.T) {
	t.Setenv(wsstate.EnvCacheHome, t.TempDir())
	stateDir, err := Dir("")
	if err != nil {
		t.Fatal(err)
	}
	session := SessionDir(stateDir, "fixture")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, AgentRecordFileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, authority := range []wsfs.Authority{wsfs.AuthorityFull, wsfs.AuthorityWorkdirWrite} {
		t.Run(string(authority), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "project.txt")
			if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			var guards []*modelFileWriteProtection
			files := wsfs.Files{
				Root: root, Authority: authority,
				Protection: func() wsfs.FileProtection {
					guard := NewModelFileWriteProtection()
					guards = append(guards, guard)
					return guard
				},
			}
			edit := json.RawMessage(`{"path":"project.txt","edits":[{"oldText":"before","newText":"after"}]}`)
			if _, err := files.Edit(context.Background(), edit); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "after" {
				t.Fatalf("edited project file = %q, %v", got, err)
			}
			if len(guards) != 2 {
				t.Fatalf("expected guards for inspection and apply, got %d", len(guards))
			}
			for _, guard := range guards {
				if guard.filesLoaded {
					t.Fatal("a single-link edit enumerated daemon state files")
				}
			}
		})
	}
}

func TestModelFileProtectionRefusesWerkJournalAlias(t *testing.T) {
	t.Setenv(wsstate.EnvCacheHome, t.TempDir())
	stateDir, err := Dir("")
	if err != nil {
		t.Fatal(err)
	}
	id := werk.NewSessionID()
	dir := SessionDir(stateDir, "fixture")
	store, _, err := werk.OpenStore(dir, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal := filepath.Join(dir, wsstate.WerkJournalDirName, id+".jsonl")
	before, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Link(journal, filepath.Join(root, "alias.jsonl")); err != nil {
		t.Fatal(err)
	}
	for _, authority := range []wsfs.Authority{wsfs.AuthorityFull, wsfs.AuthorityWorkdirWrite} {
		files := wsfs.Files{
			Root: root, Authority: authority,
			Protection: func() wsfs.FileProtection { return NewModelFileWriteProtection() },
		}
		_, err := files.Write(context.Background(), json.RawMessage(`{"path":"alias.jsonl","content":"overwrite journal"}`))
		if !errors.Is(err, errModelDaemonState) {
			t.Errorf("%s Write = %v, want daemon state refusal", authority, err)
		}
	}
	if got, err := os.ReadFile(journal); err != nil || string(got) != string(before) {
		t.Fatalf("journal changed: %q, %v", got, err)
	}
}
