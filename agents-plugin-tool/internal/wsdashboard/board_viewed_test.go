package wsdashboard

import (
	"strings"
	"testing"
)

func TestBoardViewedBoundaryAndBatchWiring(t *testing.T) {
	js, err := dashboardTestAsset("assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`var f = docFrame(e.source), m = e.data;`,
		`if (!f || !m || typeof m !== "object") return;`,
		`if (!key || m.docKey !== key || f.getAttribute("data-doc") !== key) return;`,
		`if (f.wbNext) { postTheme(f); return; }`,
		`if (h >= 0 && h < 100000) queueFrameHeight(f, h);`,
	} {
		if !strings.Contains(string(js), s) {
			t.Errorf("trusted frame boundary changed: %s", s)
		}
	}
	batch := dashboardFuncs(t, "applyFrameHeights")
	capture, write := strings.Index(batch, "boardKeep(Array.from(batch.keys()))"), strings.Index(batch, `f.style.height = to + "px";`)
	if capture < 0 || write < capture || strings.Count(batch, "kept.h.apply()") != 1 {
		t.Error("height batch must pre-capture all actual scrollers and apply once after all writes")
	}
}
