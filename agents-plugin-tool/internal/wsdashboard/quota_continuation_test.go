package wsdashboard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestQuotaContinuationRegionRendering(t *testing.T) {
	now := testNow
	reset := now.Add(2 * 3600e9).UnixMilli()
	resume := now.Add(2*3600e9 + 45e9).UnixMilli()
	s := liveSession("qclead", "/a", now)
	s.Hosted = true

	render := func(host *wslive.Host) (head, live, page string) {
		t.Helper()
		snap := liveSnap(now, wslive.Lead{})
		snap.Host = host
		v := buildSessionView(pageInputs{Session: s, Snapshot: snap, Now: now, Lookup: lookupOf(nil, nil)})
		regions, err := renderSessionRegions(v, nil)
		if err != nil {
			t.Fatal(err)
		}
		html, err := renderSessionPage(v, regions, true, "")
		if err != nil {
			t.Fatal(err)
		}
		return string(regions[regionHead].HTML), string(regions[regionConvLive].HTML), html
	}

	// 1. Usage limit failure with no continuation scheduled: no qc-chip, #qcbox present on hosted page.
	head, live, page := render(&wslive.Host{
		Failure: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "Limit reached", ResetsAt: reset},
	})
	if strings.Contains(head, "qc-chip") {
		t.Errorf("head should not render qc-chip when no continuation is active:\n%s", head)
	}
	if !strings.Contains(live, fmt.Sprintf(`data-qc-fail="usage_limit" data-qc-reset="%d"`, reset)) {
		t.Errorf("convlive lacks quota failure attributes:\n%s", live)
	}
	if !strings.Contains(page, `<div class="qcbox" id="qcbox" hidden></div>`) {
		t.Errorf("hosted session page lacks #qcbox container")
	}

	// 2. Scheduled continuation: read-only static <time data-f="dt"> chip in head, zero buttons in shared regions.
	head, live, _ = render(&wslive.Host{
		Failure: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "Limit reached", ResetsAt: reset},
		QuotaContinuation: &wslive.QuotaContinuation{
			Status:   "scheduled",
			ResumeAt: resume,
		},
	})
	wantChip := fmt.Sprintf(`<span class="chip qc-chip scheduled" data-qc-status="scheduled" data-qc-resume="%d">Resume scheduled · <time class="t" data-ms="%d" data-f="dt">%s</time></span>`, resume, resume, clockText(resume))
	if !strings.Contains(head, wantChip) {
		t.Errorf("head lacks scheduled qc-chip %s:\n%s", wantChip, head)
	}
	if strings.Contains(head, "<button") && strings.Contains(head, "qc-chip") && strings.Contains(head, "quota-continuation") {
		t.Errorf("shared head region must not embed interactive continuation buttons:\n%s", head)
	}
	if !strings.Contains(live, fmt.Sprintf(`data-qc-status="scheduled" data-qc-resume="%d"`, resume)) {
		t.Errorf("convlive lacks scheduled quota attributes:\n%s", live)
	}

	// 3. In-flight continuation: scheduled chip and timestamp hidden.
	head, live, _ = render(&wslive.Host{
		Failure: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "Limit reached", ResetsAt: reset},
		QuotaContinuation: &wslive.QuotaContinuation{
			Status: "in_flight",
		},
	})
	if strings.Contains(head, "qc-chip") {
		t.Errorf("head must hide qc-chip while continuation is in_flight:\n%s", head)
	}
	if !strings.Contains(live, `data-qc-status="in_flight"`) {
		t.Errorf("convlive lacks in_flight attribute:\n%s", live)
	}

	// 4. Exhausted continuation: exhausted chip in head, zero interactive buttons in shared regions.
	head, live, _ = render(&wslive.Host{
		Failure: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "Limit reached"},
		QuotaContinuation: &wslive.QuotaContinuation{
			Status:      "exhausted",
			Attempts:    6,
			MaxAttempts: 6,
		},
	})
	wantExhausted := `<span class="chip qc-chip exhausted" data-qc-status="exhausted">Retries exhausted</span>`
	if !strings.Contains(head, wantExhausted) {
		t.Errorf("head lacks exhausted qc-chip %s:\n%s", wantExhausted, head)
	}
	if !strings.Contains(live, `data-qc-status="exhausted" data-qc-att="6"`) {
		t.Errorf("convlive lacks exhausted quota attributes:\n%s", live)
	}

	// 5. Non-hosted CLI session does not render #qcbox.
	cli := liveSession("clilead", "/a", now)
	cliView := buildSessionView(pageInputs{Session: cli, Snapshot: liveSnap(now, wslive.Lead{}), Now: now, Lookup: lookupOf(nil, nil)})
	cliRegions, err := renderSessionRegions(cliView, nil)
	if err != nil {
		t.Fatal(err)
	}
	cliPage, err := renderSessionPage(cliView, cliRegions, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cliPage, `id="qcbox"`) {
		t.Errorf("non-hosted session page should not render #qcbox")
	}
}
