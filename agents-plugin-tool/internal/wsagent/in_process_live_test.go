package wsagent

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type inProcessLiveSessions struct{}

func (inProcessLiveSessions) ReadSession(wsdashboard.Registration) (wsdashboard.SessionData, bool, error) {
	return wsdashboard.SessionData{HasBoard: true, Fingerprint: "v1"}, true, nil
}

func (inProcessLiveSessions) ReadPage(wsdashboard.Registration) (wsdashboard.PageContent, bool, error) {
	return wsdashboard.PageContent{HasBoard: true}, true, nil
}

func startInProcessLiveDaemon(t *testing.T, dir string) *wsdashboard.Daemon {
	t.Helper()
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir:          dir,
		Port:         0,
		Sessions:     inProcessLiveSessions{},
		PollInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func getDaemonPage(t *testing.T, d *wsdashboard.Daemon, cookie *http.Cookie, id string) string {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/s/%s?frame=1", d.Endpoint().Port, id)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /s/%s: %v", id, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /s/%s: %v", id, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /s/%s status %d: %s", id, resp.StatusCode, body)
	}
	return string(body)
}

func htmlHeadPreview(page string) string {
	if len(page) > 400 {
		return page[:400]
	}
	return page
}

func TestHostedLeadStaysLiveInDaemonWhileLoopBlockedPastStaleWindow(t *testing.T) {
	// Place the runner's clock 150s behind wall-clock time so snapshots written
	// by the runner carry UpdatedAt older than wslive's 90s stale threshold
	// relative to the daemon's real clock.
	clk := newLockSeamClock(time.Now().Add(-150 * time.Second))
	h := newLeadHarness(t)
	d := startInProcessLiveDaemon(t, h.dir)
	cookie := daemonCookieForTest(t, d)
	h.opts.Now = clk.now
	h.opts.LiveHost = d
	h.opts.heartbeat = time.Hour
	h.start()

	st, err := h.runner.CreateLead(BackendClaude, h.root, LeadOptions{})
	if err != nil {
		t.Fatalf("CreateLead: %v", err)
	}
	leadID := st.ID
	h.quiesce(leadID)
	// Seed one saved ring entry so closing the lead transitions to history mode.
	if err := wslive.AppendRing(wslive.RingDir(wsdashboard.SessionDir(h.dir, leadID)), []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "saved lead prompt"},
	}); err != nil {
		t.Fatal(err)
	}

	d.Index().Refresh()
	d.Index().Settle()

	var diskWrites atomic.Int32
	c := h.runner.conv(leadID)
	c.do(func() {
		c.live.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
			diskWrites.Add(1)
			return writeFileAtomic(path, raw, mode)
		}
	})

	// Park conv.loop() to simulate >90s blocking startup / PC lag.
	blocked, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	go c.do(func() { close(blocked); <-release })
	awaitLockSeam(t, blocked, "lead loop parked")

	page := getDaemonPage(t, d, cookie, leadID)
	if !strings.Contains(page, `data-mode="live"`) || !strings.Contains(page, `id="composer"`) || !strings.Contains(page, `id="convo"`) {
		t.Fatalf("blocked hosted lead dropped out of live mode: %s", htmlHeadPreview(page))
	}
	if got := diskWrites.Load(); got != 0 {
		t.Fatalf("disk snapshot writes during stall = %d, want 0", got)
	}

	// Unblock conv.loop() and close the lead: setAdapter("") + slot.Close()
	// must transition cleanly out of live mode into saved history.
	close(release)
	if _, err := h.runner.CloseConversation(leadID); err != nil {
		t.Fatalf("CloseConversation: %v", err)
	}
	d.Index().Reread(leadID)
	d.Index().Settle()
	closedPage := getDaemonPage(t, d, cookie, leadID)
	if strings.Contains(closedPage, `data-mode="live"`) || !strings.Contains(closedPage, `data-mode="history"`) {
		t.Fatalf("closed hosted lead did not transition from live to history: %s", htmlHeadPreview(closedPage))
	}
}

func TestHostedWorkerStaysLiveInDaemonWhileLoopBlockedPastStaleWindow(t *testing.T) {
	clk := newLockSeamClock(time.Now().Add(-150 * time.Second))
	h, _ := agentHarness(t, func(o *Options) {
		o.Now = clk.now
		o.heartbeat = time.Hour
	})
	d := startInProcessLiveDaemon(t, h.dir)
	cookie := daemonCookieForTest(t, d)
	t.Cleanup(h.runner.Close)
	h.runner.SetLiveHost(d)

	childID, pChild := h.agentRunning(h.leadCaller(), AgentSpawnRequest{
		Prompt:    "work",
		Tier:      "small",
		Authority: "read-only",
	})
	pChild.reply("child ready")
	eventually(t, "child settled", func() bool { return h.state(childID).Status == StatusIdle })

	d.Index().Refresh()
	d.Index().Settle()

	var diskWrites atomic.Int32
	c := h.runner.conv(childID)
	c.do(func() {
		c.live.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
			diskWrites.Add(1)
			return writeFileAtomic(path, raw, mode)
		}
	})

	blocked, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	go c.do(func() { close(blocked); <-release })
	awaitLockSeam(t, blocked, "child loop parked")

	page := getDaemonPage(t, d, cookie, childID)
	if !strings.Contains(page, `data-mode="live"`) || !strings.Contains(page, `id="convo"`) {
		t.Fatalf("blocked hosted worker dropped out of live mode (hasSlot=%v): %s", c.live.currentSlot() != nil, htmlHeadPreview(page))
	}
	if got := diskWrites.Load(); got != 0 {
		t.Fatalf("disk snapshot writes during worker stall = %d, want 0", got)
	}
	close(release)
}
