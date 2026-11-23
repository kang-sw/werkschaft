package werk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreAppendsOneLinePerItemAndRecovers(t *testing.T) {
	dir := t.TempDir()
	session := NewSessionID()
	s, items, err := OpenStore(dir, session, func() int64 { return 42 })
	if err != nil || len(items) != 0 {
		t.Fatalf("open = %v, %d items", err, len(items))
	}
	for _, it := range []string{`{"a":1}`, `{"b":"two"}`} {
		if err := s.Append(json.RawMessage(it)); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	raw, _ := os.ReadFile(segmentPath(dir, session))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"k":"head"`) || !strings.Contains(lines[0], `"at":42`) {
		t.Fatalf("file = %q", raw)
	}
	s2, items, err := OpenStore(dir, session, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if len(items) != 2 || string(items[1]) != `{"b":"two"}` {
		t.Errorf("recovered = %s", items)
	}
	// Reopening appends after the recovered lines and writes no second header.
	if err := s2.Append(json.RawMessage(`{"c":3}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(segmentPath(dir, session))
	if strings.Count(string(raw), `"k":"head"`) != 1 || strings.Count(string(raw), "\n") != 4 {
		t.Errorf("file after reopen = %q", raw)
	}
}

func TestStoreTrimsATornLastLine(t *testing.T) {
	dir := t.TempDir()
	session := NewSessionID()
	s, _, _ := OpenStore(dir, session, nil)
	_ = s.Append(json.RawMessage(`{"a":1}`))
	s.Close()
	f, _ := os.OpenFile(segmentPath(dir, session), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"k":"item","item":{"torn`)
	f.Close()

	s2, items, err := OpenStore(dir, session, nil)
	if err != nil || len(items) != 1 {
		t.Fatalf("open = %v, %d items", err, len(items))
	}
	_ = s2.Append(json.RawMessage(`{"b":2}`))
	s2.Close()
	_, items, err = OpenStore(dir, session, nil)
	if err != nil || len(items) != 2 || string(items[1]) != `{"b":2}` {
		t.Fatalf("after the torn line: %v, %s", err, items)
	}
}

func TestStoreRefusesForeignFilesAndBadIDs(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := OpenStore(dir, "../escape", nil); err == nil {
		t.Error("a path-shaped session id was accepted")
	}
	session := NewSessionID()
	p := segmentPath(dir, session)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("not json\n"), 0o600)
	if _, _, err := OpenStore(dir, session, nil); err == nil {
		t.Error("a file that is no segment was accepted")
	}
	other := NewSessionID()
	_ = os.WriteFile(p, []byte(`{"k":"head","v":1,"session":"`+other+`"}`+"\n"), 0o600)
	if _, _, err := OpenStore(dir, session, nil); err == nil {
		t.Error("another session's segment was accepted")
	}
	_ = os.WriteFile(p, []byte(`{"k":"head","v":99,"session":"`+session+`"}`+"\n"), 0o600)
	if _, _, err := OpenStore(dir, session, nil); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("a newer segment version: %v", err)
	}
}

func TestStoreRotateStartsASnapshotSegmentAndDropsTheOldOne(t *testing.T) {
	dir := t.TempDir()
	session := NewSessionID()
	s, _, _ := OpenStore(dir, session, nil)
	for i := 0; i < 5; i++ {
		_ = s.Append(json.RawMessage(`{"old":true}`))
	}
	if err := s.Rotate([]json.RawMessage{json.RawMessage(`{"summary":1}`)}); err != nil {
		t.Fatal(err)
	}
	_ = s.Append(json.RawMessage(`{"after":1}`))
	s.Close()
	segments, err := filepath.Glob(filepath.Join(filepath.Dir(segmentPath(dir, session)), "*.jsonl"))
	if err != nil || len(segments) != 1 {
		t.Errorf("segments left = %d, %v", len(segments), err)
	}
	reopened, items, err := OpenStore(dir, session, nil)
	if err != nil || len(items) != 2 || string(items[0]) != `{"summary":1}` || string(items[1]) != `{"after":1}` {
		t.Fatalf("after rotate: %v, %s", err, items)
	}
	defer reopened.Close()
}

func TestStoreResponseIsRecoveredAsAUnit(t *testing.T) {
	dir, session := t.TempDir(), NewSessionID()
	s, _, err := OpenStore(dir, session, nil)
	must(t, err)
	must(t, s.Append(json.RawMessage(userItem("question"))))
	first := &Response{ID: "committed", Output: []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"r"}`),
		json.RawMessage(callOf("c", "read", "{}")),
	}}
	must(t, s.AppendResponse(first))
	must(t, s.Close())
	tail, err := os.OpenFile(segmentPath(dir, session), os.O_APPEND|os.O_WRONLY, 0)
	must(t, err)
	_, err = tail.WriteString(`{"k":"response","response":{"id":"torn","output":[{"type":"message"}`)
	must(t, err)
	must(t, tail.Close())

	reopened, items, err := OpenStore(dir, session, nil)
	must(t, err)
	defer reopened.Close()
	if len(items) != 3 || string(items[1]) != string(first.Output[0]) || string(items[2]) != string(first.Output[1]) {
		t.Fatalf("replayed %s", items)
	}
}

func TestStoreKeepsTheWriterLeaseAcrossRotation(t *testing.T) {
	dir, session := t.TempDir(), NewSessionID()
	writer, _, err := OpenStore(dir, session, nil)
	must(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	assertBusy := func() {
		t.Helper()
		second, _, err := OpenStore(dir, session, nil)
		if err == nil {
			_ = second.Close()
			t.Fatal("the same session admitted another writer")
		}
		if !strings.Contains(err.Error(), "already has a writer") {
			t.Fatal(err)
		}
	}
	assertBusy()
	must(t, writer.Rotate(nil))
	assertBusy()
	must(t, writer.Append(json.RawMessage(`{"step":"committed"}`)))
	must(t, writer.Close())
	successor, items, err := OpenStore(dir, session, nil)
	must(t, err)
	t.Cleanup(func() { _ = successor.Close() })
	if len(items) != 1 || string(items[0]) != `{"step":"committed"}` {
		t.Errorf("successor's transcript = %s", items)
	}
}
