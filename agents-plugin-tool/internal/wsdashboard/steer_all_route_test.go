package wsdashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func TestSteerAllRouteAtomicAndSingleWake(t *testing.T) {
	wake := &countingMailWake{}
	f := startPageFixture(t, func(o *DaemonOptions) { o.MailWake = wake })

	// Page renders the bottom-left steer-all button next to #convo-bottom-btn.
	_, page := get(t, f.base+"/s/livesession?frame=1")
	if !strings.Contains(page, `id="convo-steer-all-btn"`) {
		t.Fatal("session page lacks #convo-steer-all-btn")
	}

	// Idempotent 0-eligible call returns 200 {ok:true, count:0} and does not wake.
	code, _, raw := postJSON(t, f.base+"/s/livesession/steer-all", f.base, `{}`)
	if code != http.StatusOK {
		t.Fatalf("steer-all empty = %d: %s", code, raw)
	}
	var res struct {
		OK    bool `json:"ok"`
		Count int  `json:"count"`
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil || !res.OK || res.Count != 0 {
		t.Fatalf("steer-all empty answer = %q (%v)", raw, err)
	}
	if wakes, _ := wake.counts(); wakes != 0 {
		t.Fatalf("steer-all with 0 eligible woke %d times, want 0", wakes)
	}

	// Queue two user messages plus an exec notice.
	f.send(t, `{"text":"first queued"}`)
	f.send(t, `{"text":"second queued"}`)
	if _, _, err := wsinbox.Send(f.inbox("livesession"), wsinbox.Entry{
		EventID: "exec:bg1:exit",
		Parts:   []string{"exec done"},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	wakesBefore, _ := wake.counts()
	code, _, raw = postJSON(t, f.base+"/s/livesession/steer-all", f.base, `{}`)
	if code != http.StatusOK {
		t.Fatalf("steer-all = %d: %s", code, raw)
	}
	res = struct {
		OK    bool `json:"ok"`
		Count int  `json:"count"`
	}{}
	if err := json.Unmarshal([]byte(raw), &res); err != nil || !res.OK || res.Count != 2 {
		t.Fatalf("steer-all answer = %q (%v), want ok:true count:2", raw, err)
	}
	wakesAfter, _ := wake.counts()
	if wakesAfter-wakesBefore != 1 {
		t.Fatalf("steer-all woke %d times, want exactly 1", wakesAfter-wakesBefore)
	}

	held := f.held(t)
	if len(held) != 3 {
		t.Fatalf("len(held) = %d, want 3", len(held))
	}
	if !held[0].Entry.Steer || inboxBody(t, held[0].Entry) != "first queued" {
		t.Fatalf("held[0] = %+v, want steered first queued", held[0])
	}
	if !held[1].Entry.Steer || inboxBody(t, held[1].Entry) != "second queued" {
		t.Fatalf("held[1] = %+v, want steered second queued", held[1])
	}
	if _, isExec := held[2].Entry.ExecNotice(); !isExec || held[2].Entry.Steer {
		t.Fatalf("held[2] = %+v, want un-steered exec notice", held[2])
	}

	// Second call is idempotent no-op (count 0, no additional wake).
	code, _, raw = postJSON(t, f.base+"/s/livesession/steer-all", f.base, ``)
	if code != http.StatusOK {
		t.Fatalf("steer-all repeat = %d: %s", code, raw)
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil || !res.OK || res.Count != 0 {
		t.Fatalf("steer-all repeat answer = %q (%v)", raw, err)
	}
	if wakesNow, _ := wake.counts(); wakesNow != wakesAfter {
		t.Fatalf("idempotent steer-all woke %d extra times", wakesNow-wakesAfter)
	}

	// Malformed JSON returns 400.
	if code, _, _ := postJSON(t, f.base+"/s/livesession/steer-all", f.base, `{bad`); code != http.StatusBadRequest {
		t.Fatalf("malformed steer-all = %d, want 400", code)
	}
}

func TestSteerAllBoardIncarnationAdmission(t *testing.T) {
	stale := "stale"
	for _, tc := range []struct {
		name  string
		chip  bool
		token *string
		want  int
	}{
		{"plain with stale token", false, &stale, http.StatusOK},
		{"board-linked with stale token", true, &stale, http.StatusConflict},
		{"board-linked without token", true, nil, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			reg := Registration{ReplyID: "conversation"}
			d := &Daemon{
				log: io.Discard,
				index: &Index{opts: IndexOptions{Now: time.Now}, sessions: map[string]*indexEntry{
					reg.ReplyID: {session: Session{Registration: reg, HasBoard: true}},
				}},
				source: incarnationSource{folder: wswhiteboard.FolderStore{Dir: filepath.Join(root, "unopened")}},
			}
			dir := sessionInbox(root, reg.ReplyID)
			n1, _, err := wsinbox.Send(dir, wsinbox.Entry{Parts: []string{"plain"}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			second := wsinbox.Entry{Parts: []string{"second"}}
			if tc.chip {
				second.Chips = []wsinbox.Chip{{ID: "01", Answer: "ok"}}
			}
			n2, _, err := wsinbox.Send(dir, second, time.Now().Add(time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			body := `{}`
			if tc.token != nil {
				body = `{"boardIncarnation":"` + *tc.token + `"}`
			}
			d.inboxMu.Lock()
			status, answer := d.inboxWrite(dir, reg.ReplyID, "steer-all", []byte(body), time.Now())
			d.inboxMu.Unlock()
			if status != tc.want {
				t.Fatalf("steer-all = %d, %v; want %d", status, answer, tc.want)
			}
			held, err := wsinbox.Held(dir)
			if err != nil || len(held) != 2 {
				t.Fatalf("held = %+v, %v", held, err)
			}
			if tc.want == http.StatusConflict {
				if answer != "board-replaced" {
					t.Fatalf("answer = %v, want board-replaced", answer)
				}
				if held[0].Entry.Steer || held[1].Entry.Steer || held[0].Name != n1 || held[1].Name != n2 {
					t.Fatalf("stale refusal left partial writes: %+v", held)
				}
			} else if !held[0].Entry.Steer || !held[1].Entry.Steer {
				t.Fatalf("accepted steer-all did not promote all entries: %+v", held)
			}
		})
	}
}
