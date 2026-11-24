package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

// fullQuitHolderGate holds the in-process dashboard inside the holder stop
// so the test can put a concurrent creation and wake against its fence.
type fullQuitHolderGate struct {
	entered     chan struct{}
	proceed     chan struct{}
	runs        atomic.Int32
	resumptions atomic.Int32
}

func (*fullQuitHolderGate) Run()                   {}
func (*fullQuitHolderGate) Close()                 {}
func (*fullQuitHolderGate) List() wsholder.Listing { return wsholder.Listing{} }
func (*fullQuitHolderGate) Stop(string) error      { return wsholder.ErrUnknownHolder }
func (g *fullQuitHolderGate) StopAllFenced() (wsholder.StopAllResult, func()) {
	if g.runs.Add(1) == 1 {
		close(g.entered)
		<-g.proceed
	}
	return wsholder.StopAllResult{Stopped: []string{}, Remaining: []wsholder.StopFailure{}}, func() { g.resumptions.Add(1) }
}

func TestFullQuitDrainsAdmittedWakeBeforeReturningFence(t *testing.T) {
	starting, permitStart := make(chan struct{}), make(chan struct{})
	var paused atomic.Bool
	h, _, submitted := durableTurnHarness(t, func(rec Record) error {
		if rec.SessionID != "" && paused.CompareAndSwap(false, true) {
			close(starting)
			<-permitStart
		}
		return nil
	})
	releaseStart := sync.OnceFunc(func() { close(permitStart) })
	defer releaseStart()
	idle := h.create("chat")
	if _, err := h.runner.Send(idle.ID, "wake from the inbox"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-starting:
	case <-time.After(5 * time.Second):
		t.Fatal("the wake never reached the backend's session-identity write")
	}
	fenced := make(chan func(), 1)
	go func() { fenced <- h.runner.FenceDurableStarts() }()
	eventually(t, "durable starts are stopping", h.runner.durableStopping.Load)
	select {
	case resume := <-fenced:
		resume()
		t.Fatal("the fence returned while an admitted wake was starting")
	case <-time.After(75 * time.Millisecond):
	}
	releaseStart()
	var resume func()
	select {
	case resume = <-fenced:
	case <-time.After(5 * time.Second):
		t.Fatal("the fence did not drain the admitted wake")
	}
	// The backend start already had permission. Its prompt submission is a
	// separate transition and must wait for partial-stop recovery.
	c := h.runner.conv(idle.ID)
	c.do(func() {})
	if h.backend.startCount() != 1 {
		t.Fatal("the backend did not start before the fence returned")
	}
	select {
	case <-submitted:
		t.Fatal("the wake submitted a turn after the fence was installed")
	default:
	}
	resume()
	if input := nextDurableInput(t, submitted); !strings.Contains(input.Text, "wake from the inbox") {
		t.Fatal("the partial stop lost the inbox input")
	}
}

func TestFullQuitRefusesConcurrentDurableReopen(t *testing.T) {
	h, _, _ := werkHarness(t, &werkModel{})
	st := h.lead(BackendWerk, LeadOptions{Model: "vendor/some-model"})
	if _, err := h.runner.CloseConversation(st.ID); err != nil {
		t.Fatal(err)
	}
	// Hold an admitted transition so Full Quit stays inside its fence
	// while the concurrent reopen reaches the same admission check.
	release, err := h.runner.admitDurableTransition()
	if err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(release)
	defer release()
	fenced := make(chan func(), 1)
	go func() { fenced <- h.runner.FenceDurableStarts() }()
	eventually(t, "Full Quit has fenced durable starts", h.runner.durableStopping.Load)
	if _, err := h.runner.Reopen(st.ID); !errors.Is(err, errDurableStopping) {
		t.Errorf("reopen during Full Quit = %v, want a refusal", err)
	}
	release()
	resume := <-fenced
	if sessions := h.runner.DurableSessions(); len(sessions) != 0 {
		t.Errorf("Full Quit saw an open durable session: %+v", sessions)
	}
	resume()
	if reopened, err := h.runner.Reopen(st.ID); err != nil || reopened.ID != st.ID {
		t.Fatalf("reopen after lifting the fence = %+v, %v", reopened, err)
	}
}

func TestFullQuitDurableCloseFailureFencesStartsAndWakes(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var idleID atomic.Pointer[string]
	h, reports, inputs := durableTurnHarness(t, func(rec Record) error {
		if id := idleID.Load(); rec.ClosedAt != "" && fail.Load() && id != nil && rec.ReplyID == *id {
			return errors.New("injected durable close failure")
		}
		return nil
	})
	h.runner.Close()
	h.runner = NewRunner(h.config)
	gate := &fullQuitHolderGate{entered: make(chan struct{}), proceed: make(chan struct{})}
	start := func(holders wsdashboard.HolderHost) *wsdashboard.Daemon {
		t.Helper()
		d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: h.dir, Home: t.TempDir(), Agents: h.runner, Holders: holders})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(d.Close)
		return d
	}
	d := start(gate)
	defer func() {
		select {
		case <-gate.proceed:
		default:
			close(gate.proceed)
		}
	}()
	idle := h.create("chat")
	idleID.Store(&idle.ID)
	busy := h.spawn("start working", leadParent)
	nextDurableInput(t, inputs)
	busyProcess := h.backend.waitProc(t, 0)
	if count := len(h.runner.DurableSessions()); count != 2 {
		t.Fatalf("live count = %d, want idle and busy", count)
	}

	type answer struct {
		code int
		wsdashboard.FullStopResult
		err error
	}
	fullQuit := func() (a answer) {
		ep := d.Endpoint()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", ep.Port, wsdashboard.FullStopPath), nil)
		if err != nil {
			a.err = err
			return
		}
		req.Header.Set("Authorization", "Bearer "+ep.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			a.err = err
			return
		}
		defer resp.Body.Close()
		a.code, a.err = resp.StatusCode, json.NewDecoder(resp.Body).Decode(&a.FullStopResult)
		return
	}
	answerC := make(chan answer, 1)
	go func() { answerC <- fullQuit() }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the holder stop was not reached")
	}
	if _, err := h.runner.Create("chat", h.root); !errors.Is(err, errDurableStopping) {
		t.Fatalf("create during Full Quit = %v, want a refusal", err)
	}
	if _, err := h.runner.Send(idle.ID, "wake after the failed quit"); err != nil {
		t.Fatal(err)
	}
	conv := h.runner.conv(idle.ID)
	conv.do(conv.kick)
	if h.backend.startCount() != 1 {
		t.Fatal("the idle session woke during Full Quit")
	}
	close(gate.proceed)

	a := <-answerC
	if a.err != nil || a.code != http.StatusConflict || a.ShuttingDown || len(a.Remaining) != 1 ||
		a.Remaining[0].ID != "durable."+idle.ID || !slices.Equal(a.Stopped, []string{"durable." + busy.ID}) {
		t.Fatalf("partial quit = %+v", a)
	}
	if gate.resumptions.Load() != 1 || !busyProcess.isClosed() {
		t.Fatal("partial quit did not resume holder admission or end the successfully closed worker")
	}
	if wake := nextDurableInput(t, inputs); !strings.Contains(wake.Text, "wake after the failed quit") {
		t.Fatalf("partial quit lost queued input: %q", wake.Text)
	}
	if _, err := h.runner.Create("chat", h.root); err != nil {
		t.Fatalf("the daemon did not accept another durable session after the partial quit: %v", err)
	}
	fail.Store(false)
	a = fullQuit()
	if a.err != nil || a.code != http.StatusAccepted || !a.ShuttingDown || len(a.Stopped) != 2 || len(a.Remaining) != 0 {
		t.Fatalf("successful quit = %+v", a)
	}
	select {
	case <-d.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not finish Full Quit")
	}
	if len(reports.list()) != 0 {
		t.Fatal("Full Quit reported an abandoned worker turn")
	}
	h.runner = NewRunner(h.config)
	start(nil)
	if sessions := h.runner.DurableSessions(); len(sessions) != 0 {
		t.Fatalf("successor reopened stopped sessions: %+v", sessions)
	}
	for _, id := range []string{idle.ID, busy.ID} {
		rec, ok := h.runner.recordOf(id)
		if !ok || rec.ClosedAt == "" || rec.OpenGen != 1 || rec.TurnOpen || rec.DurableTurn != nil || rec.HeldTurn != nil {
			t.Fatal("successor did not find the durable close boundary")
		}
	}
	if h.backend.startCount() != 2 {
		t.Fatal("successor woke a stopped loop")
	}
}

func TestFullQuitCountsAnUnavailableDurableProjection(t *testing.T) {
	h, _, _ := durableTurnHarness(t, nil)
	id := h.create("chat").ID
	h.runner.Close()
	store, err := wsmeta.Open(h.dir, wsmeta.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDurableAgentRecord(context.Background(), id); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	h.start()
	want := []wsdashboard.DurableSession{{ID: id, Unavailable: true}}
	if sessions := h.runner.DurableSessions(); !slices.Equal(sessions, want) {
		t.Fatalf("unavailable session count = %+v, want %+v", sessions, want)
	}
	if err := h.runner.StopDurableSession(id); !errors.Is(err, errDurableUnavailable) {
		t.Fatalf("stop of unavailable durable record = %v", err)
	}
	if h.backend.startCount() != 0 {
		t.Fatal("the compatibility projection started a process")
	}
}
