package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// composerFake is a ComposerExecService whose launches the test steers.
type composerFake struct {
	mu       sync.Mutex
	launches []ComposerLaunch
	hands    []composerHand
	// launch answers a launch; nil answers an immediate success.
	launch  func(ComposerLaunch) (ComposerJob, error)
	jobs    map[string]ComposerJob
	handErr error
	// open answers Open: the open generation per session, or the error the
	// session is refused with; a session it does not list is "livesession"
	// at generation 4.
	open map[string]composerOpen
}

type composerOpen struct {
	gen uint64
	err error
}

func (f *composerFake) Open(reply string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.open[reply]; ok {
		return o.gen, o.err
	}
	return 4, nil
}

type composerHand struct {
	reply, execID, text string
	gen                 uint64
}

func (f *composerFake) Launch(_ context.Context, _ <-chan struct{}, req ComposerLaunch) (ComposerJob, error) {
	f.mu.Lock()
	f.launches = append(f.launches, req)
	fn := f.launch
	f.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	zero := 0
	return ComposerJob{ExecID: "exec_a_b_c", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: 3, Output: []byte("hi\n")}, nil
}

func (f *composerFake) Job(reply, id string) (ComposerJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return ComposerJob{}, errors.New("exec job not found")
	}
	return j, nil
}

func (f *composerFake) Hand(reply string, gen uint64, execID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handErr != nil {
		return f.handErr
	}
	f.hands = append(f.hands, composerHand{reply, execID, text, gen})
	return nil
}

func (f *composerFake) launchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.launches)
}

func (f *composerFake) handList() []composerHand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]composerHand(nil), f.hands...)
}

type composerFixture struct {
	*pageFixture
	svc *composerFake
}

func startComposerFixture(t *testing.T) *composerFixture {
	t.Helper()
	f := startPageFixture(t)
	reg, err := ReadRegistration(f.dir, "livesession")
	if err != nil {
		t.Fatal(err)
	}
	reg.Hosted = true
	if err = WriteRegistration(f.dir, reg); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	waitFor(t, "the hosted registration", func() bool {
		s, ok := f.d.Index().Session("livesession")
		return ok && s.Hosted
	})
	svc := &composerFake{jobs: map[string]ComposerJob{}}
	// The background loop settles async cards quickly in tests; its retention
	// sweeps are driven by the tests that need them.
	f.d.composer.settleEvery, f.d.composer.sweepFirst, f.d.composer.sweepEvery = 20*time.Millisecond, time.Hour, time.Hour
	f.d.SetComposerExec(svc)
	return &composerFixture{pageFixture: f, svc: svc}
}

func composerHTTP(d *Daemon, method, path, body, origin, remote string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	req.RemoteAddr = remote
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	out := httptest.NewRecorder()
	localhostTestHandler(nil, d).ServeHTTP(out, req)
	return out
}

func composerPost(d *Daemon, id, text string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"submission_id": id, "text": text})
	return composerHTTP(d, http.MethodPost, "/s/livesession/composer-exec", string(body), "http://127.0.0.1", "127.0.0.1:1234", nil)
}

func composerGet(d *Daemon, path string) *httptest.ResponseRecorder {
	return composerHTTP(d, http.MethodGet, path, "", "", "127.0.0.1:1234", nil)
}

func decodeCard(t *testing.T, res *httptest.ResponseRecorder) composerCard {
	t.Helper()
	var c composerCard
	if err := json.Unmarshal(res.Body.Bytes(), &c); err != nil {
		t.Fatalf("card %q: %v", res.Body.String(), err)
	}
	return c
}

func waitCard(t *testing.T, d *Daemon, id string, phase string) composerCard {
	t.Helper()
	var card composerCard
	waitFor(t, "card "+id+" phase "+phase, func() bool {
		res := composerGet(d, "/s/livesession/composer-exec/"+id)
		if res.Code != 200 {
			return false
		}
		card = decodeCard(t, res)
		return card.Phase == phase
	})
	return card
}

// TestComposerGrammarFirstByteOnly: the server owns the `!` grammar.
func TestComposerGrammarFirstByteOnly(t *testing.T) {
	f := startComposerFixture(t)
	for _, c := range []struct {
		name, text string
		code       int
	}{
		{"ordinary", "hello", 400},
		{"leading space", " !ls", 400},
		{"space then bang", "  !ls", 400},
		{"blank payload", "!", 400},
		{"blank spaces", "!   \n", 400},
	} {
		res := composerPost(f.d, "sub-"+strings.ReplaceAll(c.name, " ", "-")+"-01", c.text)
		if res.Code != c.code {
			t.Fatalf("%s: %d %s", c.name, res.Code, res.Body)
		}
	}
	if n := f.svc.launchCount(); n != 0 {
		t.Fatalf("a refused grammar launched %d jobs", n)
	}
	// One byte is stripped: a second `!` and a leading space stay, a
	// multiline source runs as one script.
	script := "!! echo hi\n echo two\n"
	res := composerPost(f.d, "sub-script-01", script)
	if res.Code != http.StatusAccepted {
		t.Fatalf("script: %d %s", res.Code, res.Body)
	}
	waitFor(t, "the launch", func() bool { return f.svc.launchCount() == 1 })
	if got := f.svc.launches[0].Line; got != "! echo hi\n echo two\n" || f.svc.launches[0].ReplyID != "livesession" || f.svc.launches[0].Window != ComposerWindow {
		t.Fatalf("launch = %+v", f.svc.launches[0])
	}
}

// TestComposerRefusesAttachmentsAndBadIDs: a submission with attachments is
// refused whole (the client keeps the draft); a submission id is required.
func TestComposerRefusesAttachmentsAndBadIDs(t *testing.T) {
	f := startComposerFixture(t)
	body := `{"submission_id":"sub-att-0001","text":"!ls","images":[{"name":"a.png"}]}`
	if res := composerHTTP(f.d, http.MethodPost, "/s/livesession/composer-exec", body, "http://127.0.0.1", "127.0.0.1:1", nil); res.Code != 400 || !strings.Contains(res.Body.String(), "attachments") {
		t.Fatalf("attachments: %d %s", res.Code, res.Body)
	}
	body = `{"submission_id":"sub-att-0002","text":"!ls","refs":[{"x":1}]}`
	if res := composerHTTP(f.d, http.MethodPost, "/s/livesession/composer-exec", body, "http://127.0.0.1", "127.0.0.1:1", nil); res.Code != 400 {
		t.Fatalf("refs: %d %s", res.Code, res.Body)
	}
	for _, id := range []string{"", "short", "has space in it", strings.Repeat("a", 65), "../../etc/passwd"} {
		if res := composerPost(f.d, id, "!ls"); res.Code != 400 {
			t.Fatalf("id %q: %d", id, res.Code)
		}
	}
	if n := f.svc.launchCount(); n != 0 {
		t.Fatalf("launches = %d", n)
	}
}

// TestComposerAuthority: only a local, same-origin POST on an open hosted
// session runs; LAN, cross-origin, missing service, unknown or non-hosted
// sessions never launch.
func TestComposerAuthority(t *testing.T) {
	f := startComposerFixture(t)
	body := `{"submission_id":"sub-auth-001","text":"!ls"}`
	post := "/s/livesession/composer-exec"
	for _, c := range []struct {
		name, origin, remote string
		code                 int
	}{
		{"LAN peer", "http://127.0.0.1", "192.0.2.1:9", 401},
		{"no origin", "", "127.0.0.1:1", 403},
		{"cross origin", "http://evil.test", "127.0.0.1:1", 403},
	} {
		if res := composerHTTP(f.d, http.MethodPost, post, body, c.origin, c.remote, nil); res.Code != c.code {
			t.Fatalf("%s: %d %s", c.name, res.Code, res.Body)
		}
	}
	if res := composerHTTP(f.d, http.MethodPost, "/s/boardsession/composer-exec", body, "http://127.0.0.1", "127.0.0.1:1", nil); res.Code != 404 {
		t.Fatalf("non-hosted session: %d", res.Code)
	}
	if res := composerHTTP(f.d, http.MethodPost, "/s/nosuchsession/composer-exec", body, "http://127.0.0.1", "127.0.0.1:1", nil); res.Code != 404 {
		t.Fatalf("unknown session: %d", res.Code)
	}
	// Reads: a cross-site fetch and a LAN peer see nothing.
	if res := composerHTTP(f.d, http.MethodGet, post, "", "", "127.0.0.1:1", map[string]string{"Sec-Fetch-Site": "cross-site"}); res.Code != 405 {
		t.Fatalf("cross-site read: %d", res.Code)
	}
	if res := composerHTTP(f.d, http.MethodGet, post, "", "", "192.0.2.1:9", nil); res.Code != 401 {
		t.Fatalf("LAN read: %d", res.Code)
	}
	if res := composerHTTP(f.d, http.MethodPut, post, body, "http://127.0.0.1", "127.0.0.1:1", nil); res.Code == 200 || res.Code == 202 {
		t.Fatalf("PUT answered %d", res.Code)
	}
	if n := f.svc.launchCount(); n != 0 {
		t.Fatalf("a refused request launched %d jobs", n)
	}
	f.d.SetComposerExec(nil)
	if res := composerPost(f.d, "sub-auth-002", "!ls"); res.Code != 503 {
		t.Fatalf("unbound service: %d", res.Code)
	}
}

// TestComposerInlineAndFileClassification: exactly 4096 raw combined bytes
// are inline and 4097 a file, by the raw byte count, not the decoded text.
func TestComposerInlineAndFileClassification(t *testing.T) {
	f := startComposerFixture(t)
	zero := 0
	var start int64
	run := func(id string, written int, out []byte) composerCard {
		f.svc.mu.Lock()
		f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
			return ComposerJob{ExecID: "exec_" + id, Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: int64(written), Start: start, Output: out}, nil
		}
		f.svc.mu.Unlock()
		if res := composerPost(f.d, id, "!cmd"); res.Code != 202 {
			t.Fatalf("%s: %d %s", id, res.Code, res.Body)
		}
		return waitCard(t, f.d, id, "done")
	}
	inline := run("sub-4096-ok", 4096, []byte(strings.Repeat("a", 4096)))
	if inline.File != "" || len(inline.Output) != 4096 || inline.OutputBytes != 4096 {
		t.Fatalf("4096 bytes: %+v", inline)
	}
	file := run("sub-4097-file", 4097, []byte(strings.Repeat("a", 4097)))
	if file.File == "" || file.Output != "" || file.FileURL != "/s/livesession/composer-exec/sub-4097-file/output" {
		t.Fatalf("4097 bytes: %+v", file)
	}
	if got, err := os.ReadFile(file.File); err != nil || len(got) != 4097 {
		t.Fatalf("file: %d bytes, %v", len(got), err)
	}
	if info, err := os.Stat(file.File); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v, %v", info, err)
	}
	// Binary/invalid UTF-8: 2000 bytes of 0xff decode to 2000 replacement
	// characters (6000 UTF-8 bytes), still classified inline by raw bytes.
	bin := run("sub-binary-01", 2000, []byte(strings.Repeat("\xff", 2000)))
	if bin.File != "" || !bin.Replaced || strings.Count(bin.Output, "�") != 2000 {
		t.Fatalf("binary: replaced=%v file=%q", bin.Replaced, bin.File)
	}
	// 3000 raw bytes of 0xff decode longer than 4096 but are still inline's
	// raw count... 5000 raw is a file.
	if big := run("sub-binary-big", 5000, []byte(strings.Repeat("\xff", 5000))); big.File == "" {
		t.Fatalf("5000 raw bytes were not a file: %+v", big)
	}
	// The retained snapshot over 1 MiB keeps its tail, marked truncated.
	huge := []byte(strings.Repeat("z", 1<<20))
	start = 500
	trunc := run("sub-truncate-1", (1<<20)+500, huge)
	if trunc.File == "" || trunc.Truncated != 500 {
		t.Fatalf("truncate: %+v", trunc)
	}
	if got, _ := os.ReadFile(trunc.File); len(got) != 1<<20 {
		t.Fatalf("a retained 1 MiB range wrote %d bytes", len(got))
	}
	// A snapshot over the per-run cap keeps its last MiB and says how many
	// leading bytes it dropped on top of the ring's own eviction.
	start = 0
	over := run("sub-over-cap-01", (1<<20)+4096, []byte(strings.Repeat("o", (1<<20)+4096)))
	if got, _ := os.ReadFile(over.File); len(got) != 1<<20 || over.Truncated != 4096 {
		t.Fatalf("over cap: %d bytes, truncated %d", len(got), over.Truncated)
	}
	if hands := f.svc.handList(); len(hands) == 0 {
		t.Fatal("no receipts")
	}
	// stderr-only output is just output: no special case.
	if e := run("sub-stderr-001", 12, []byte("only stderr\n")); e.Output != "only stderr\n" {
		t.Fatalf("stderr only: %+v", e)
	}
	// Every inline/file outcome handed the lead one receipt under the job's
	// id and generation.
	hands := f.svc.handList()
	if len(hands) != 7 {
		t.Fatalf("hands = %d", len(hands))
	}
	for _, h := range hands {
		if h.reply != "livesession" || h.gen != 4 || !strings.Contains(h.text, "[user ran from the dashboard]") {
			t.Fatalf("hand = %+v", h)
		}
	}
	var fileHand string
	for _, h := range hands {
		if h.execID == "exec_sub-4097-file" {
			fileHand = h.text
		}
	}
	if !strings.Contains(fileHand, "is in the file: "+file.File) || strings.Contains(fileHand, "aaaaaaaaaa") {
		t.Fatalf("file receipt = %q", fileHand)
	}
}

// TestComposerIdempotency: a retry with the same submission ID returns the
// same job; concurrent identical submissions map to one job; a fresh ID is a
// new run; an ID reused for another command conflicts.
func TestComposerIdempotency(t *testing.T) {
	f := startComposerFixture(t)
	release := make(chan struct{})
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		<-release
		zero := 0
		return ComposerJob{ExecID: "exec_one", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: 1, Output: []byte("x")}, nil
	}
	f.svc.mu.Unlock()
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = composerPost(f.d, "sub-same-0001", "!sleep 1").Code
		}()
	}
	wg.Wait()
	accepted := 0
	for _, c := range codes {
		if c == http.StatusAccepted {
			accepted++
		} else if c != http.StatusOK {
			t.Fatalf("codes = %v", codes)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d submissions were accepted as new: %v", accepted, codes)
	}
	// The lost-response retry while it still runs returns the card, no relaunch.
	if res := composerPost(f.d, "sub-same-0001", "!sleep 1"); res.Code != 200 || decodeCard(t, res).Phase != "running" {
		t.Fatalf("retry: %d %s", res.Code, res.Body)
	}
	if res := composerPost(f.d, "sub-same-0001", "!other"); res.Code != http.StatusConflict {
		t.Fatalf("reused id for another command: %d", res.Code)
	}
	close(release)
	waitCard(t, f.d, "sub-same-0001", "done")
	if n := f.svc.launchCount(); n != 1 {
		t.Fatalf("launches = %d, want 1", n)
	}
	if res := composerPost(f.d, "sub-same-0001", "!sleep 1"); res.Code != 200 || decodeCard(t, res).Phase != "done" || decodeCard(t, res).ExecID != "exec_one" {
		t.Fatalf("retry after done: %d %s", res.Code, res.Body)
	}
	// A deliberate rerun uses a fresh ID and is a new run.
	if res := composerPost(f.d, "sub-fresh-0001", "!sleep 1"); res.Code != 202 {
		t.Fatalf("fresh id: %d", res.Code)
	}
	waitFor(t, "the second launch", func() bool { return f.svc.launchCount() == 2 })
	// Another session's claim never answers for this one's id.
	if res := composerGet(f.d, "/s/boardsession/composer-exec/sub-same-0001"); res.Code == 200 {
		t.Fatalf("a claim crossed sessions: %s", res.Body)
	}
	// After a restart the map is empty: the card endpoint says unknown and
	// nothing re-runs by itself.
	f.d.composer.mu.Lock()
	f.d.composer.subs, f.d.composer.order = nil, nil
	f.d.composer.mu.Unlock()
	if res := composerGet(f.d, "/s/livesession/composer-exec/sub-same-0001"); res.Code != 404 {
		t.Fatalf("after restart: %d", res.Code)
	}
	if n := f.svc.launchCount(); n != 2 {
		t.Fatalf("an unknown submission was re-run: %d launches", n)
	}
}

// TestComposerRefusedLaunchShowsRefused: a launch the service refuses leaves
// a refused card, never a receipt.
func TestComposerRefusedLaunchShowsRefused(t *testing.T) {
	f := startComposerFixture(t)
	f.svc.mu.Lock()
	f.svc.launch = func(ComposerLaunch) (ComposerJob, error) {
		return ComposerJob{}, errors.New("exec: the owning session reopened since this request was made")
	}
	f.svc.mu.Unlock()
	if res := composerPost(f.d, "sub-refused-01", "!ls"); res.Code != 202 {
		t.Fatal(res.Body)
	}
	card := waitCard(t, f.d, "sub-refused-01", "refused")
	if !strings.Contains(card.Error, "reopened") || card.ExecID != "" {
		t.Fatalf("card = %+v", card)
	}
	if n := len(f.svc.handList()); n != 0 {
		t.Fatalf("a refused launch handed %d receipts", n)
	}
}

// TestComposerAsyncConvertedCardFinishesOnRefresh: a run that converted keeps
// an async card until its job ends; the refresh then classifies its output
// once, and no receipt comes from the dashboard (the exec notice's job).
func TestComposerAsyncConvertedCardFinishesOnRefresh(t *testing.T) {
	f := startComposerFixture(t)
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		return ComposerJob{ExecID: "exec_slow", Gen: 4, State: "running", Command: req.Line, Async: true}, nil
	}
	f.svc.mu.Unlock()
	if res := composerPost(f.d, "sub-async-0001", "!sleep 60"); res.Code != 202 {
		t.Fatal(res.Body)
	}
	card := waitCard(t, f.d, "sub-async-0001", "async")
	if card.ExecID != "exec_slow" || !card.Async {
		t.Fatalf("card = %+v", card)
	}
	// Still running: the refresh keeps it async.
	f.svc.mu.Lock()
	f.svc.jobs["exec_slow"] = ComposerJob{ExecID: "exec_slow", Gen: 4, State: "running", Async: true}
	f.svc.mu.Unlock()
	if c := decodeCard(t, composerGet(f.d, "/s/livesession/composer-exec/sub-async-0001")); c.Phase != "async" {
		t.Fatalf("running card = %+v", c)
	}
	zero := 0
	f.svc.mu.Lock()
	f.svc.jobs["exec_slow"] = ComposerJob{ExecID: "exec_slow", Gen: 4, State: "exited", ExitCode: &zero, Async: true, Written: 5000, Output: []byte(strings.Repeat("y", 5000))}
	f.svc.mu.Unlock()
	card = waitCard(t, f.d, "sub-async-0001", "done")
	if card.File == "" || !card.Async {
		t.Fatalf("finished card = %+v", card)
	}
	if n := len(f.svc.handList()); n != 0 {
		t.Fatalf("the dashboard handed %d receipts for an async completion", n)
	}
	// The job aged out: the card says it is no longer queryable.
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		return ComposerJob{ExecID: "exec_pruned", Gen: 4, State: "running", Async: true}, nil
	}
	f.svc.mu.Unlock()
	composerPost(f.d, "sub-async-0002", "!sleep 60")
	waitCard(t, f.d, "sub-async-0002", "async")
	// Nobody polls: the loop retires the card itself, as finished.
	waitFor(t, "the pruned card retires without a poll", func() bool {
		c := decodeCard(t, composerGet(f.d, "/s/livesession/composer-exec/sub-async-0002"))
		return c.Phase == "done" && strings.Contains(c.Error, "no longer queryable")
	})
}

// TestComposerOutputRouteIsOwnerChecked: the artifact route serves a file by
// its submission ID to that session only, never by a browser-supplied path,
// and shows an expired file explicitly.
func TestComposerOutputRouteIsOwnerChecked(t *testing.T) {
	f := startComposerFixture(t)
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		zero := 0
		return ComposerJob{ExecID: "exec_big", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: 5000, Output: []byte(strings.Repeat("q", 5000))}, nil
	}
	f.svc.mu.Unlock()
	composerPost(f.d, "sub-output-001", "!big")
	card := waitCard(t, f.d, "sub-output-001", "done")
	res := composerGet(f.d, card.FileURL)
	if res.Code != 200 || res.Body.Len() != 5000 || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/plain") || res.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("output: %d len %d %v", res.Code, res.Body.Len(), res.Header())
	}
	for _, path := range []string{
		"/s/boardsession/composer-exec/sub-output-001/output",
		"/s/livesession/composer-exec/" + "..%2f..%2fetc%2fpasswd" + "/output",
		"/s/livesession/composer-exec/sub-output-001/output/x",
		"/s/livesession/composer-exec/" + card.File + "/output",
	} {
		if res := composerGet(f.d, path); res.Code == 200 {
			t.Fatalf("%s served %d bytes", path, res.Body.Len())
		}
	}
	if res := composerHTTP(f.d, http.MethodGet, card.FileURL, "", "", "192.0.2.1:1", nil); res.Code != 401 {
		t.Fatalf("LAN output read: %d", res.Code)
	}
	// Retention removed the file: shown explicitly.
	if err := os.Remove(card.File); err != nil {
		t.Fatal(err)
	}
	if res := composerGet(f.d, card.FileURL); res.Code != http.StatusGone || !strings.Contains(res.Body.String(), "gone") {
		t.Fatalf("expired output: %d %s", res.Code, res.Body)
	}
}

func TestComposerPruneByAgeAndTotal(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write := func(name string, size int, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		mod := now.Add(-age)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write("old.out", 10, 8*24*time.Hour)
	a := write("a.out", 40<<20, 3*time.Hour)
	b := write("b.out", 40<<20, 2*time.Hour)
	c := write("c.out", 40<<20, time.Hour)
	other := write("keep.txt", 10, 30*24*time.Hour)
	present, ok := composerPrune(dir, now)
	if _, has := present["b.out"]; !ok || len(present) != 2 || !has {
		t.Fatalf("the surviving names = %v (ok %v)", present, ok)
	}
	gone := func(p string) bool { _, err := os.Stat(p); return os.IsNotExist(err) }
	if !gone(old) || !gone(a) || gone(b) || gone(c) || gone(other) {
		t.Fatalf("old=%v a=%v b=%v c=%v other=%v", gone(old), gone(a), gone(b), gone(c), gone(other))
	}
}

// TestComposerLaunchSurvivesTheClosedTab: the run is the daemon's, not the
// request's: the acknowledgement returns before the launch ends and the
// request context never reaches it.
func TestComposerLaunchSurvivesTheClosedTab(t *testing.T) {
	f := startComposerFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var ctxErr error
	f.svc.mu.Lock()
	f.svc.launch = func(ComposerLaunch) (ComposerJob, error) {
		close(started)
		<-release
		zero := 0
		return ComposerJob{ExecID: "exec_tab", Gen: 4, State: "exited", ExitCode: &zero, Written: 0}, ctxErr
	}
	f.svc.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	body, _ := json.Marshal(map[string]any{"submission_id": "sub-tab-00001", "text": "!sleep 5"})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/s/livesession/composer-exec", strings.NewReader(string(body))).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:5"
	req.Header.Set("Origin", "http://127.0.0.1")
	out := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { localhostTestHandler(nil, f.d).ServeHTTP(out, req); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the route waited for the launch")
	}
	<-started
	cancel() // the tab closes
	close(release)
	card := waitCard(t, f.d, "sub-tab-00001", "done")
	if card.ExecID != "exec_tab" {
		t.Fatalf("card = %+v", card)
	}
}

// TestComposerClaimEvictsFinishedCardsBehindAnActiveOne: an active card is
// never dropped, and it does not make the finished ones behind it pile up.
func TestComposerClaimEvictsFinishedCardsBehindAnActiveOne(t *testing.T) {
	f := startComposerFixture(t)
	first, _ := f.d.composerAdmit("livesession", "claim-active-0", "sleep 60", 4, false)
	for i := 1; i < composerKeepCards+5; i++ {
		sub, how := f.d.composerAdmit("livesession", fmt.Sprintf("claim-done-%02d", i), "true", 4, false)
		if how != composerFresh {
			t.Fatalf("admission %d was not fresh", i)
		}
		sub.mu.Lock()
		sub.phase, sub.finalized = "done", true
		sub.mu.Unlock()
	}
	c := &f.d.composer
	c.mu.Lock()
	n, kept := len(c.order["livesession"]), c.subs["livesession\x00claim-active-0"]
	c.mu.Unlock()
	if kept != first {
		t.Fatal("the active card was dropped")
	}
	if n > composerKeepCards+1 {
		t.Fatalf("%d cards kept behind one active card, want at most %d", n, composerKeepCards+1)
	}
}

func composerPostAs(d *Daemon, reply, id, text string, retry bool) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"submission_id": id, "text": text, "retry": retry})
	return composerHTTP(d, http.MethodPost, "/s/"+reply+"/composer-exec", string(body), "http://127.0.0.1", "127.0.0.1:1234", nil)
}

// TestComposerReplayFenceNeverRunsAnIdTwice: a submission id this daemon
// admitted never launches again, whatever became of its card; a retry of an
// id it does not know reports the outcome unknown (a restart), and only a
// fresh submission under an unknown id runs.
func TestComposerReplayFenceNeverRunsAnIdTwice(t *testing.T) {
	f := startComposerFixture(t)
	if res := composerPostAs(f.d, "livesession", "replay-0001", "!echo once", false); res.Code != 202 {
		t.Fatal(res.Body)
	}
	waitCard(t, f.d, "replay-0001", "done")
	// A duplicate and a retry both map to the one card.
	for _, retry := range []bool{false, true} {
		res := composerPostAs(f.d, "livesession", "replay-0001", "!echo once", retry)
		if res.Code != 200 || decodeCard(t, res).Phase != "done" || decodeCard(t, res).Unknown {
			t.Fatalf("resend (retry=%v) = %d %s", retry, res.Code, res.Body)
		}
	}
	if n := f.svc.launchCount(); n != 1 {
		t.Fatalf("launches after resends = %d", n)
	}
	// Evict the card: more finished cards than the daemon keeps.
	for i := 0; i < composerKeepCards+3; i++ {
		id := fmt.Sprintf("replay-fill-%03d", i)
		if res := composerPostAs(f.d, "livesession", id, "!true", false); res.Code != 202 {
			t.Fatal(res.Body)
		}
		waitCard(t, f.d, id, "done")
	}
	if res := composerGet(f.d, "/s/livesession/composer-exec/replay-0001"); res.Code != 404 {
		t.Fatalf("the first card was not evicted: %d", res.Code)
	}
	before := f.svc.launchCount()
	for _, retry := range []bool{true, false} {
		res := composerPostAs(f.d, "livesession", "replay-0001", "!echo once", retry)
		card := decodeCard(t, res)
		if res.Code != 200 || !card.Unknown || card.Phase != "done" {
			t.Fatalf("evicted id (retry=%v) = %d %s", retry, res.Code, res.Body)
		}
	}
	if n := f.svc.launchCount(); n != before {
		t.Fatalf("an evicted id launched again: %d -> %d", before, n)
	}
	// A restart empties the maps: a retry is unknown and runs nothing, a fresh
	// submission under the same id runs (a deliberate resend).
	c := &f.d.composer
	c.mu.Lock()
	c.subs, c.order, c.fence, c.fenceQ = nil, nil, nil, nil
	c.mu.Unlock()
	res := composerPostAs(f.d, "livesession", "replay-0001", "!echo once", true)
	if card := decodeCard(t, res); res.Code != 200 || !card.Unknown {
		t.Fatalf("retry after a restart = %d %s", res.Code, res.Body)
	}
	if n := f.svc.launchCount(); n != before {
		t.Fatalf("a retry after a restart launched: %d -> %d", before, n)
	}
	if res := composerPostAs(f.d, "livesession", "replay-0001", "!echo once", false); res.Code != 202 {
		t.Fatalf("fresh submission after a restart = %d %s", res.Code, res.Body)
	}
	waitFor(t, "the deliberate resend to launch", func() bool { return f.svc.launchCount() == before+1 })
	// The same id for another command is refused, never mapped.
	if res := composerPostAs(f.d, "livesession", "replay-0001", "!echo other", false); res.Code != 409 {
		t.Fatalf("id reused for another command = %d", res.Code)
	}
}

// TestComposerReplayFenceIsBounded: the fence keeps the newest ids only.
func TestComposerReplayFenceIsBounded(t *testing.T) {
	f := startComposerFixture(t)
	for i := 0; i < composerFenceMax+10; i++ {
		sub, how := f.d.composerAdmit("livesession", fmt.Sprintf("bound-%06d", i), "true", 4, false)
		if how != composerFresh {
			t.Fatalf("admission %d = %v", i, how)
		}
		sub.mu.Lock()
		sub.phase, sub.finalized = "done", true
		sub.mu.Unlock()
	}
	c := &f.d.composer
	c.mu.Lock()
	n, oldest := len(c.fence), c.fenceQ[0]
	c.mu.Unlock()
	if n != composerFenceMax || oldest != "livesession\x00bound-000010" {
		t.Fatalf("fence holds %d ids from %q", n, oldest)
	}
}

// TestComposerRouteCapturesTheGenerationBeforeAcknowledging: the launch
// carries the open generation the route read, even when the lead is on
// another one by the time the worker runs.
func TestComposerRouteCapturesTheGenerationBeforeAcknowledging(t *testing.T) {
	f := startComposerFixture(t)
	f.svc.mu.Lock()
	f.svc.open = map[string]composerOpen{"livesession": {gen: 0}}
	release := make(chan struct{})
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		<-release
		return ComposerJob{}, errors.New("exec: the owning session reopened since this request was made")
	}
	f.svc.mu.Unlock()
	if res := composerPostAs(f.d, "livesession", "gen-zero-0001", "!echo hi", false); res.Code != 202 {
		t.Fatal(res.Body)
	}
	// The conversation closes and revives (generation 1) before the worker runs.
	f.svc.mu.Lock()
	f.svc.open["livesession"] = composerOpen{gen: 1}
	f.svc.mu.Unlock()
	close(release)
	waitCard(t, f.d, "gen-zero-0001", "refused")
	f.svc.mu.Lock()
	got := f.svc.launches[0].Gen
	f.svc.mu.Unlock()
	if got != 0 {
		t.Fatalf("the launch carried generation %d, want the 0 the route acknowledged", got)
	}
}

// TestComposerConvertedRunHandsNothingFromTheDashboard: a run the service
// reports as converted is the exec notice's, even when the job it returns
// has ended since; a run that ended inside its window is handed once.
func TestComposerConvertedRunHandsNothingFromTheDashboard(t *testing.T) {
	f := startComposerFixture(t)
	zero := 0
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		if strings.HasPrefix(req.Line, "conv") {
			// Converted at the commit; ended by the time the view is read.
			return ComposerJob{ExecID: "exec_conv", Gen: 4, State: "exited", ExitCode: &zero, Async: true, Written: 3, Output: []byte("hi\n")}, nil
		}
		return ComposerJob{ExecID: "exec_done", Gen: 4, State: "exited", ExitCode: &zero, Written: 3, Output: []byte("hi\n")}, nil
	}
	f.svc.jobs["exec_conv"] = ComposerJob{ExecID: "exec_conv", Gen: 4, State: "exited", ExitCode: &zero, Async: true, Written: 3, Output: []byte("hi\n")}
	f.svc.mu.Unlock()
	composerPostAs(f.d, "livesession", "conv-sub-0001", "!conv", false)
	card := waitCard(t, f.d, "conv-sub-0001", "done")
	if !card.Async || card.Output != "hi\n" {
		t.Fatalf("converted card = %+v", card)
	}
	composerPostAs(f.d, "livesession", "done-sub-0001", "!quick", false)
	waitCard(t, f.d, "done-sub-0001", "done")
	waitFor(t, "the in-window receipt", func() bool { return len(f.svc.handList()) == 1 })
	time.Sleep(100 * time.Millisecond)
	hands := f.svc.handList()
	if len(hands) != 1 || hands[0].execID != "exec_done" {
		t.Fatalf("hands = %+v; only the in-window run is the dashboard's", hands)
	}
}

// TestComposerPrivateReadsNeedTheLiveHostedLead: every private read and write
// asks the live authority, so a closed lead (whose registration keeps its
// hosted flag), a token-authenticated LAN peer, and another hosted session
// see nothing of a session's commands, results or files.
func TestComposerPrivateReadsNeedTheLiveHostedLead(t *testing.T) {
	f := startComposerFixture(t)
	if res := composerPostAs(f.d, "livesession", "live-sub-0001", "!echo secret", false); res.Code != 202 {
		t.Fatal(res.Body)
	}
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		zero := 0
		out := []byte(strings.Repeat("s", 5000))
		return ComposerJob{ExecID: "exec_secret", Gen: 4, State: "exited", ExitCode: &zero, Written: 5000, Output: out}, nil
	}
	f.svc.mu.Unlock()
	if res := composerPostAs(f.d, "livesession", "live-sub-0002", "!big", false); res.Code != 202 {
		t.Fatal(res.Body)
	}
	card := waitCard(t, f.d, "live-sub-0002", "done")
	if card.FileURL == "" {
		t.Fatalf("no file: %+v", card)
	}
	reads := []string{"/s/livesession/composer-exec", "/s/livesession/composer-exec/live-sub-0001", card.FileURL}
	for _, path := range reads {
		if res := composerGet(f.d, path); res.Code != 200 {
			t.Fatalf("open lead read %s = %d", path, res.Code)
		}
	}

	// Another hosted session: its own routes never show these cards or files.
	reg, err := ReadRegistration(f.dir, "boardsession")
	if err != nil {
		t.Fatal(err)
	}
	reg.Hosted = true
	if err := WriteRegistration(f.dir, reg); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	waitFor(t, "the second hosted session", func() bool { s, ok := f.d.Index().Session("boardsession"); return ok && s.Hosted })
	if res := composerGet(f.d, "/s/boardsession/composer-exec"); res.Code != 200 || strings.Contains(res.Body.String(), "live-sub") {
		t.Fatalf("another hosted owner lists %d %s", res.Code, res.Body)
	}
	for _, path := range []string{"/s/boardsession/composer-exec/live-sub-0001", "/s/boardsession/composer-exec/live-sub-0002/output"} {
		if res := composerGet(f.d, path); res.Code != 404 {
			t.Fatalf("another hosted owner read %s = %d", path, res.Code)
		}
	}

	// A LAN peer holding the access token still reads and writes nothing.
	f.d.lan = true
	token, err := f.d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range reads {
		req := httptest.NewRequest(http.MethodGet, "http://192.0.2.7"+path, nil)
		req.RemoteAddr = "192.0.2.1:9"
		req.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		out := httptest.NewRecorder()
		f.d.Handler().ServeHTTP(out, req)
		if out.Code != 404 || strings.Contains(out.Body.String(), "echo secret") {
			t.Fatalf("token-authenticated LAN read %s = %d %s", path, out.Code, out.Body)
		}
	}
	body := `{"submission_id":"lan-sub-00001","text":"!id"}`
	req := httptest.NewRequest(http.MethodPost, "http://192.0.2.7/s/livesession/composer-exec", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.1:9"
	req.Header.Set("Origin", "http://192.0.2.7")
	req.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
	out := httptest.NewRecorder()
	f.d.Handler().ServeHTTP(out, req)
	if out.Code != 404 {
		t.Fatalf("token-authenticated LAN submit = %d", out.Code)
	}
	f.d.lan = false

	// A closed lead keeps its registration and its hosted flag, but the live
	// authority refuses: nothing is read or run.
	launches := f.svc.launchCount()
	f.svc.mu.Lock()
	f.svc.open = map[string]composerOpen{"livesession": {err: errors.New("the conversation is closed")}}
	f.svc.mu.Unlock()
	for _, path := range reads {
		if res := composerGet(f.d, path); res.Code != 404 {
			t.Fatalf("closed lead read %s = %d", path, res.Code)
		}
	}
	if res := composerPostAs(f.d, "livesession", "closed-sub-001", "!echo x", false); res.Code != 404 {
		t.Fatalf("closed lead submit = %d", res.Code)
	}
	if n := f.svc.launchCount(); n != launches {
		t.Fatalf("a closed lead launched %d", n-launches)
	}
}

// TestComposerArtifactIsBoundedTextAHarnessReads: the output file is UTF-8
// text within the 1 MiB cap after the substitutions (an invalid byte becomes
// three bytes, a NUL three), with no NUL in it. internal/mcp's fs.read
// refuses a file with a NUL in its first 8192 bytes as binary
// (fsBinarySniff) and reads at most fsReadScanBytes (64 MiB), so the file is
// readable by it; the receipt names the substitutions.
func TestComposerArtifactIsBoundedTextAHarnessReads(t *testing.T) {
	f := startComposerFixture(t)
	zero := 0
	run := func(id string, out []byte) composerCard {
		f.svc.mu.Lock()
		f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
			return ComposerJob{ExecID: "exec_" + id, Gen: 4, State: "exited", ExitCode: &zero, Written: int64(len(out)), Output: out}, nil
		}
		f.svc.mu.Unlock()
		if res := composerPost(f.d, id, "!cmd"); res.Code != 202 {
			t.Fatalf("%s: %d %s", id, res.Code, res.Body)
		}
		return waitCard(t, f.d, id, "done")
	}
	check := func(name string, card composerCard, rawLen int) []byte {
		t.Helper()
		got, err := os.ReadFile(card.File)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) > composerFileMax || !utf8.Valid(got) || bytes.IndexByte(got, 0) >= 0 {
			t.Fatalf("%s: %d bytes (cap %d), valid=%v, has NUL=%v", name, len(got), composerFileMax, utf8.Valid(got), bytes.IndexByte(got, 0) >= 0)
		}
		if int(card.Truncated) <= 0 && rawLen > composerFileMax/3 && strings.Contains(name, "invalid") {
			t.Fatalf("%s: nothing marked truncated though the text outgrew the cap", name)
		}
		return got
	}
	// Invalid UTF-8 past the cap: 600000 bytes of 0xFF would be 1.8 MB of text.
	inv := run("art-invalid-0001", []byte(strings.Repeat("\xff", 600000)))
	got := check("invalid", inv, 600000)
	if len(got) != 3*(composerFileMax/3) || int(inv.Truncated) != 600000-len(got)/3 {
		t.Fatalf("invalid: file %d bytes, truncated %d", len(got), inv.Truncated)
	}
	// NUL-bearing output: the NULs are visible as U+2400, none stay in the file.
	data := bytes.Repeat([]byte("a\x00b\n"), 3000)
	nul := run("art-nul-000001", data)
	text := string(check("nul", nul, len(data)))
	if !strings.Contains(text, "a␀b\n") || !nul.Replaced {
		t.Fatalf("NUL file = %.40q replaced=%v", text, nul.Replaced)
	}
	if head := []byte(text); len(head) > 8192 && bytes.IndexByte(head[:8192], 0) >= 0 {
		t.Fatal("the harness's binary sniff would refuse the file")
	}
	var receipt string
	for _, h := range f.svc.handList() {
		if h.execID == "exec_art-nul-000001" {
			receipt = h.text
		}
	}
	if !strings.Contains(receipt, "U+2400") || !strings.Contains(receipt, nul.File) {
		t.Fatalf("receipt does not name the representation: %q", receipt)
	}
	// Valid text of exactly the cap is kept whole.
	whole := run("art-whole-00001", bytes.Repeat([]byte("x"), composerFileMax))
	if g := check("whole", whole, composerFileMax); len(g) != composerFileMax || whole.Truncated != 0 {
		t.Fatalf("whole: %d bytes, truncated %d", len(g), whole.Truncated)
	}
}

// TestComposerPruneSweepsRaceWithoutOverDeleting: sweeps that overlap (a lost
// remove race counts the file as removed, not as still taking room) never
// delete a file the bound does not require; sparse files stand in for the
// 100 MiB bound.
func TestComposerPruneSweepsRaceWithoutOverDeleting(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var names []string
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("f%d.out", i)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, 40<<20); err != nil { // sparse
			t.Skipf("sparse files unavailable: %v", err)
		}
		mod := now.Add(-time.Duration(6-i) * time.Hour)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); composerPrune(dir, now) }()
	}
	wg.Wait()
	// 240 MiB over a 100 MiB bound: exactly the newest two (80 MiB) stay.
	left, _ := composerPrune(dir, now)
	if len(left) != 2 || func() bool { _, a := left[names[4]]; _, b := left[names[5]]; return !a || !b }() {
		t.Fatalf("kept %v, want the newest two %v", left, names[4:])
	}
}

// TestComposerRetentionRunsWithoutNewOutput: the background loop expires an
// idle file on its own (no oversized write needed) and publishes its absence
// to the card and the output route; no card read or acknowledgement touches
// the directory.
func TestComposerRetentionRunsWithoutNewOutput(t *testing.T) {
	f := startComposerFixture(t)
	zero := 0
	out := []byte(strings.Repeat("r", 6000))
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		return ComposerJob{ExecID: "exec_ret", Gen: 4, State: "exited", ExitCode: &zero, Written: int64(len(out)), Output: out}, nil
	}
	f.svc.mu.Unlock()
	composerPost(f.d, "retention-0001", "!big")
	card := waitCard(t, f.d, "retention-0001", "done")
	if card.File == "" {
		t.Fatalf("no file: %+v", card)
	}
	// The file ages past seven days; nothing else happens.
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(card.File, old, old); err != nil {
		t.Fatal(err)
	}
	// A card read does not touch the file (it is still there).
	composerGet(f.d, "/s/livesession/composer-exec/retention-0001")
	if _, err := os.Stat(card.File); err != nil {
		t.Fatal("a card read removed the file")
	}
	// The loop's retention pass (what the period or a write wakes).
	f.d.composer.kick <- struct{}{}
	waitFor(t, "the expired file to be removed and its absence published", func() bool {
		c := decodeCard(t, composerGet(f.d, "/s/livesession/composer-exec/retention-0001"))
		_, err := os.Stat(card.File)
		return os.IsNotExist(err) && c.File == "" && c.FileURL == "" && strings.Contains(c.FileError, "removed")
	})
	if res := composerGet(f.d, card.FileURL); res.Code != 404 {
		t.Fatalf("the expired file's route = %d", res.Code)
	}
}
