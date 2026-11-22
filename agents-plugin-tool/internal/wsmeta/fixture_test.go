package wsmeta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func testOpts() Options {
	return Options{Now: func() time.Time { return testNow }, CheckpointIdle: -1}
}

// testValidate stands in for the owners' legacy decoders (the real ones are
// wired in validators_wiring_test.go): known record members keep their
// types, and every history sample needs a positive asOf. Each checks the
// identity it is handed.
func testValidate() map[string]LegacyValidator {
	record := func(rel, replyID string, raw []byte) error {
		if rel != "sessions/"+replyID+"/agent.json" {
			return fmt.Errorf("record validator handed %q for %q", rel, replyID)
		}
		var r struct {
			ReplyID   string `json:"reply_id"`
			WSKey     string `json:"ws_session_key"`
			Root      string `json:"root"`
			ClosedAt  string `json:"closed_at"`
			HolderSeq uint64 `json:"holder_seq"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.ReplyID != replyID || r.WSKey == "" || r.Root == "" {
			return errors.New("not a complete agent record")
		}
		return nil
	}
	history := func(rel, replyID string, raw []byte) error {
		if replyID != "" || strings.Contains(rel, "/") || !strings.HasSuffix(rel, "history.json") {
			return fmt.Errorf("history validator handed %q, %q", rel, replyID)
		}
		var h struct {
			Samples []struct {
				AsOf int64 `json:"asOf"`
			} `json:"samples"`
		}
		if err := json.Unmarshal(raw, &h); err != nil {
			return err
		}
		for _, v := range h.Samples {
			if v.AsOf <= 0 {
				return errors.New("invalid usage history sample")
			}
		}
		return nil
	}
	return map[string]LegacyValidator{
		StoreAgentRecord:       record,
		StoreUsageHistory:      history,
		StoreUsageHistoryCodex: history,
		StoreUsageHistoryAgy:   history,
	}
}

func testCommit() CommitOptions { return CommitOptions{Validate: testValidate()} }

func withMarker(f func([]Capture) error) CommitOptions {
	o := testCommit()
	o.BeforeMarker = f
	return o
}

func openTest(t *testing.T, dir string, opts Options) *Store {
	t.Helper()
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const memoHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// legacyFixture writes a dashboard directory with every bundled store in
// its legacy form, plus adjacent files discovery must never touch.
func legacyFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dashboard")
	// A hosted lead: record with unknown members and a holder cursor,
	// selection with provenance and an unknown member, label, hiring, ack.
	put(t, dir, "sessions/hostedlead/agent.json", `{"schema_version":1,"reply_id":"hostedlead","profile":"lead","backend":"claude","root":"/repo","ws_session_key":"key-1","created_at":"2026-11-01T00:00:00Z","lead":true,"holder_id":"h1","holder_seq":7,"future_member":{"x":1}}`)
	put(t, dir, "sessions/hostedlead/permission.json", `{"permission_level":"workdir-write","delegated_authority":false,"future_perm":"keep"}`)
	put(t, dir, "sessions/hostedlead/label.json", `{"label":"Lead A","note":"extra"}`)
	put(t, dir, "sessions/hostedlead/hiring.json", `{"enabled":true,"hired":true,"request_id":"req-1"}`)
	put(t, dir, "sessions/hostedlead/ack-check.json", `{"through":12,"interruptedAt":34,"later":true}`)
	put(t, dir, "sessions/hostedlead/registration.json", `{"schema_version":1,"reply_id":"hostedlead","project":"/repo","hosted":true}`)
	put(t, dir, "sessions/hostedlead/holder-h1.json", `{"pid":1}`)
	put(t, dir, "sessions/hostedlead/system_prompt.md", "prompt")
	// A CLI-only session: registration, label and hiring, no record.
	put(t, dir, "sessions/clionly/registration.json", `{"schema_version":1,"reply_id":"clionly","project":"/cli"}`)
	put(t, dir, "sessions/clionly/label.json", `{"label":"CLI lead"}`)
	put(t, dir, "sessions/clionly/hiring.json", `{"enabled":false,"hired":false}`)
	// A worker with an invalid selection, a cleared label, its own holder.
	put(t, dir, "sessions/worker/agent.json", `{"schema_version":1,"reply_id":"worker","profile":"pi","backend":"pi","root":"/repo","ws_session_key":"key-2","created_at":"2026-11-02T00:00:00Z","parent":{"kind":"lead","reply_id":"hostedlead"},"holder_id":"h2","holder_seq":3}`)
	put(t, dir, "sessions/worker/permission.json", `{"permission_level":"root"}`)
	put(t, dir, "sessions/worker/label.json", `{"label":""}`)
	// A record from a newer writer and one naming another reply-id.
	put(t, dir, "sessions/futurerec/agent.json", `{"schema_version":2,"reply_id":"futurerec","root":"/r","ws_session_key":"k"}`)
	put(t, dir, "sessions/wrongid/agent.json", `{"schema_version":1,"reply_id":"hostedlead","root":"/r","ws_session_key":"k"}`)
	put(t, dir, "sessions/wrongid/permission.json", `not json`)
	// Not a session directory name.
	put(t, dir, "sessions/Not-A-Reply/agent.json", `{}`)

	put(t, dir, "support.json", `{"schema_version":1,"last":{"harness":"pi","model":"m","effort":"high"},"stores":{"store-a":"conv1","store-b":"conv2"},"later":[1]}`)
	put(t, dir, "rail-layout.json", `{"schema_version":1,"workspace_order":["/w1","/w2"],"workspaces":{"/w1":{"order":["a","b"],"children":{"a":["c"]},"stars":["b"],"ws_extra":true},"/w2":null},"doc_extra":"x"}`)
	put(t, dir, "plan-usage.json", `{"claude":{"harness":"claude","windows":[]},"gpt":{"harness":"codex","windows":[{"kind":"5h","percent":1,"asOf":2}]},"later":{"k":1}}`)
	put(t, dir, "plan-usage-agy.json", `{"schema_version":1,"groups":[{"harness":"agy","group":"g1","windows":[]},{"harness":"agy","group":"g2","windows":[]}]}`)
	put(t, dir, "plan-usage-history.json", `{"schema_version":1,"samples":[{"harness":"claude","kind":"5h","source":"s","resetsAt":0,"asOf":10,"percent":5,"ext":"keep"}],"doc_ext":1}`)
	put(t, dir, "plan-usage-codex-history.json", `{"schema_version":1,"samples":[{"harness":"codex","kind":"5h","source":"s","asOf":10,"percent":5}]}`)
	put(t, dir, "plan-usage-agy-history.json", `{"schema_version":2,"samples":[]}`)
	put(t, dir, "pi-probes-v1/fp1-models.json", `{"schema_version":1,"kind":"models","identity":{"kind":"models","files":[]},"observed_at":"2026-11-20T00:00:00Z","value":[]}`)
	put(t, dir, "pi-probes-v1/fp1-version.json", `{"schema_version":9,"kind":"version"}`)
	put(t, dir, "pi-probes-v1/odd.json", `{}`)
	put(t, dir, "pi-probes-v1/fp1-models.json.123.tmp", `partial`)
	put(t, dir, "memos/"+memoHash+".json", `{"schema_version":1,"memos":[{"id":"m1","text":"one","pin":true},{"id":"m2","text":"two"}],"doc":"x"}`)
	put(t, dir, "tracker-hooks/store1.json", `{"v":1,"nonce":"n1","cursor":{"seq":4},"processed":{"e1":{"at":"2026-11-20T00:00:00Z","ts":"2026-11-20T00:00:00Z"}},"due":{"i1":"2026-11-21T00:00:00Z"}}`)
	put(t, dir, "tracker-hooks.json", `{"hooks":[{"command":["notify"]}]}`)
	// Adjacent dashboard files outside the bundle.
	put(t, dir, "endpoint.json", `{"port":1}`)
	put(t, dir, "visibility.json", `{}`)
	put(t, dir, "hire-proposals.json", `{}`)
	if runtime.GOOS != "windows" {
		// A dangling selection link: existing and unreadable, not absent.
		if err := os.MkdirAll(filepath.Join(dir, "sessions", "worker2"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "missing-target"), filepath.Join(dir, "sessions", "worker2", "permission.json")); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type fileState struct {
	digest string
	mode   fs.FileMode
	mtime  time.Time
}

// treeState fingerprints every file under dir except the store's own.
func treeState(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		// The store's own files, and the directory entry that holds them.
		if rel == "." || strings.HasPrefix(rel, DBName) {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := fileState{mode: info.Mode(), mtime: info.ModTime()}
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			st.digest = hex.EncodeToString(sum[:])
		}
		out[filepath.ToSlash(rel)] = st
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func captureMap(t *testing.T, s *Store) map[string]Capture {
	t.Helper()
	list, err := s.Captures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Capture{}
	for _, c := range list {
		out[c.Path] = c
	}
	return out
}

func mkdirWrite(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o600)
}
