package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestCompanionVrmSpeechBrowser verifies Tier-0 procedural 5-vowel lip-sync
// (Hangul + English), speech-time blink modulation, pluggable speechMouthDriver,
// and 30fps->6fps cadence in headless Chromium when WS_PLAYWRIGHT_MODULE is set.
// When WS_VRM0_SAMPLE and/or WS_VRM1_SAMPLE point to real VRM models, it verifies
// live VRM 0.x (a/i/u/e/o) and VRM 1.0 (aa/ih/ou/ee/oh) viseme weights and writes
// close-up comparison screenshots under /tmp/companion-lipsync-qa/ (or
// WS_COMPANION_SPEECH_QA_SHOTS when set).
func TestCompanionVrmSpeechBrowser(t *testing.T) {
	vrm0Sample := os.Getenv("WS_VRM0_SAMPLE")
	vrm1Sample := os.Getenv("WS_VRM1_SAMPLE")
	shots := os.Getenv("WS_COMPANION_SPEECH_QA_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_SPEECH_QA_SHOTS must be absolute")
	}
	if shots == "" && (vrm0Sample != "" || vrm1Sample != "") {
		shots = "/tmp/companion-lipsync-qa"
	}

	fx := startCompanionVrmBrowserFixture(t, "speech")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	real0 := "0"
	if vrm0Sample != "" {
		real0 = "1"
	}
	real1 := "0"
	if vrm1Sample != "" {
		real1 = "1"
	}
	out, err := exec.CommandContext(
		ctx,
		"node",
		"testdata/companion-vrm-speech.browser.cjs",
		fx.base,
		shots,
		fx.vrm0ID,
		fx.vrm1ID,
		real0,
		real1,
	).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM speech verification: %v", err)
	}
}
