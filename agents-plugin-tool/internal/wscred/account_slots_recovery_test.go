package wscred

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// A stale .staged generation left behind after the registry already recovered
// must fail one write, not wedge every later write on EEXIST.
func TestAccountSlotRegistryWriteFailureRearmsRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AccountSlotsFileName)
	reg := NewAccountSlotRegistry(path)
	t.Cleanup(reg.Close)

	if _, err := reg.AddSlot(AccountSlot{ID: "a", Harness: "codex", ConfigDir: filepath.Join(dir, "a")}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	_, _, staged := GenerationPaths(path)
	if err := os.WriteFile(staged, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddSlot(AccountSlot{ID: "b", Harness: "codex", ConfigDir: filepath.Join(dir, "b")}); err == nil {
		t.Fatal("write succeeded despite a stale staged generation")
	}
	if _, err := reg.AddSlot(AccountSlot{ID: "c", Harness: "codex", ConfigDir: filepath.Join(dir, "c")}); err != nil {
		t.Fatalf("write after a failed write stayed wedged: %v", err)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatalf("stale staged generation survived recovery: %v", err)
	}
	ids := map[string]bool{}
	for _, v := range reg.Slots("codex") {
		ids[v.ID] = true
	}
	if !ids["a"] || ids["b"] || !ids["c"] {
		t.Fatalf("slots after recovery = %v", ids)
	}
}

// A post-publish scrub failure leaves the new generation committed, so the
// change callback must still fire and a later mutation must succeed.
func TestAccountSlotRegistryPublishedWithScrubErrorStillNotifies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("removing an open retiring generation is not portable on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, AccountSlotsFileName)
	reg := NewAccountSlotRegistry(path)
	t.Cleanup(reg.Close)
	var changes atomic.Int32
	reg.SetOnChange(func() { changes.Add(1) })

	if _, err := reg.AddSlot(AccountSlot{ID: "a", Harness: "codex", ConfigDir: filepath.Join(dir, "a")}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if changes.Load() != 1 {
		t.Fatalf("changes after first add = %d", changes.Load())
	}
	_, retiring, _ := GenerationPaths(path)
	revision := reg.Revision()
	reg.writeStep = func(step credentialWriteStep) {
		if step == credentialTruncated {
			// Make the final unlink of the retiring generation fail.
			if err := os.Remove(retiring); err != nil {
				t.Errorf("remove retiring: %v", err)
			}
		}
	}
	if _, err := reg.AddSlot(AccountSlot{ID: "b", Harness: "codex", ConfigDir: filepath.Join(dir, "b")}); err == nil {
		t.Fatal("expected the post-publish scrub error")
	}
	reg.writeStep = nil
	if changes.Load() != 2 {
		t.Fatalf("onChange not invoked for a published change: %d", changes.Load())
	}
	if reg.Revision() == revision {
		t.Fatal("revision did not advance for a published change")
	}
	found := false
	for _, v := range reg.Slots("codex") {
		found = found || v.ID == "b"
	}
	if !found {
		t.Fatal("published slot is missing from memory")
	}
	if err := reg.SetActive("codex", "b"); err != nil {
		t.Fatalf("mutation after a post-publish error: %v", err)
	}
	if changes.Load() != 3 {
		t.Fatalf("changes after follow-up = %d", changes.Load())
	}
}

func TestMergeEntryClearsStaleOpenAIAccountID(t *testing.T) {
	old := json.RawMessage(`{"client_id":"c","account_id":"stale","future_field":1}`)
	merged, err := mergeEntry(old, openAIAccount{ClientID: "c", AccountID: ""})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(merged, &fields); err != nil {
		t.Fatal(err)
	}
	if got := string(fields["account_id"]); got != `""` {
		t.Fatalf("account_id = %s, want \"\" (stale value must be cleared)", got)
	}
	if string(fields["future_field"]) != "1" {
		t.Fatalf("unknown field not preserved: %s", merged)
	}
}
