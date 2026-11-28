package wsdashboard

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

const expectedPreTicketVrmRuntimeSHA256 = "5145d8be88c31867819dfba084ccee1308e4bc93f2f69eb3cc84194af5309b75"

const (
	maxRigStudioRuntimeGzipBytes = 360 * 1024
	maxSharedPlaybackBytes       = 24 * 1024
)

func TestCompanionRigStudioBundleIsolationAndCaps(t *testing.T) {
	// 1. Main dashboard VRM runtime must stay byte-identical to pre-ticket SHA-256.
	_, vrmBytes := CompanionVrmAsset()
	sum := sha256.Sum256(vrmBytes)
	gotSHA := hex.EncodeToString(sum[:])
	if gotSHA != expectedPreTicketVrmRuntimeSHA256 {
		t.Fatalf("companion-vrm-runtime.mjs SHA-256 drifted:\ngot:  %s\nwant: %s", gotSHA, expectedPreTicketVrmRuntimeSHA256)
	}

	// 2. Studio runtime bundle exists, has content-hashed name, and stays <= 360 KiB gzip.
	studioName, studioBytes := CompanionRigStudioAsset()
	if !strings.HasPrefix(studioName, "companion-rig-studio-") || !strings.HasSuffix(studioName, ".mjs") {
		t.Fatalf("unexpected studio runtime asset name: %q", studioName)
	}
	var gzBuf bytes.Buffer
	gzw, err := gzip.NewWriterLevel(&gzBuf, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gzw.Write(studioBytes); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	if gzBuf.Len() > maxRigStudioRuntimeGzipBytes {
		t.Fatalf("companion-rig-studio-runtime.mjs gzip size %d exceeds %d bytes", gzBuf.Len(), maxRigStudioRuntimeGzipBytes)
	}

	// 3. No rig-studio file may appear in companionScriptFiles or dashboard.tmpl.
	for _, f := range companionScriptFiles {
		if strings.Contains(f, "rig-studio") || strings.Contains(f, "rig_studio") {
			t.Fatalf("studio-only file %q leaked into companionScriptFiles", f)
		}
	}
	dashTmpl, err := embeddedDashboardAsset("dashboard.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dashTmpl), "rig-studio") || strings.Contains(string(dashTmpl), "rig_studio") {
		t.Fatalf("studio-only reference leaked into dashboard.tmpl")
	}

	// 4. Shared playback modules (vrm-clip + vrm-ik) stay <= 24 KiB combined unminified.
	clipBytes, err := embeddedDashboardAsset("dashboard-companion-vrm-clip.js")
	if err != nil {
		t.Fatal(err)
	}
	ikBytes, err := embeddedDashboardAsset("dashboard-companion-vrm-ik.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := len(clipBytes) + len(ikBytes)
	if combined > maxSharedPlaybackBytes {
		t.Fatalf("vrm-clip.js (%d) + vrm-ik.js (%d) = %d bytes exceeds %d byte cap",
			len(clipBytes), len(ikBytes), combined, maxSharedPlaybackBytes)
	}
	t.Logf("studio runtime gzip=%d bytes (cap %d), shared playback=%d bytes (cap %d)",
		gzBuf.Len(), maxRigStudioRuntimeGzipBytes, combined, maxSharedPlaybackBytes)
}

func TestCompanionRigStudioRouteSecurityAndRendering(t *testing.T) {
	SetDevAssets(nil)
	d := authFixture(t)
	d.lan = true
	cookie := authCookie(t, d)
	token, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Unauthenticated request -> 401.
	if w := authServe(d, authRequest(http.MethodGet, companionRigStudioRoute+"?pack=demo", "")); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", w.Code)
	}

	// 2. Non-local viewer (authenticated via LAN token) -> 404.
	reqRemote := authRequest(http.MethodGet, companionRigStudioRoute+"?pack=demo", "")
	reqRemote.Host, reqRemote.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:51234"
	reqRemote.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
	if w := authServe(d, reqRemote); w.Code != http.StatusNotFound {
		t.Fatalf("non-local viewer status = %d, want 404", w.Code)
	}

	// 3. Local viewer -> 200 (even for unknown pack: renders in-page error state),
	//    CSP has frame-ancestors 'none' and unique per-response nonce.
	nonceRe := regexp.MustCompile(`script-src 'nonce-([0-9a-f]{32})' 'self'`)
	var nonces []string
	studioAssetName, studioAssetData := CompanionRigStudioAsset()
	for i := 0; i < 2; i++ {
		req := authRequest(http.MethodGet, companionRigStudioRoute+"?pack=nonexistent_pack", "")
		req.AddCookie(cookie)
		rec := authServe(d, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("local viewer (unknown pack) status = %d, want 200", rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("CSP missing frame-ancestors 'none': %q", csp)
		}
		m := nonceRe.FindStringSubmatch(csp)
		if len(m) != 2 {
			t.Fatalf("CSP missing script-src nonce: %q", csp)
		}
		if !strings.Contains(csp, "style-src 'nonce-"+m[1]+"'") {
			t.Fatalf("CSP missing matching style-src nonce: %q", csp)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "/s/"+studioAssetName) {
			t.Fatalf("studio page missing hashed runtime URL /s/%s", studioAssetName)
		}
		if !strings.Contains(body, `nonce="`+m[1]+`"`) {
			t.Fatalf("studio page scripts/styles missing matching nonce %s", m[1])
		}
		nonces = append(nonces, m[1])
	}
	if nonces[0] == nonces[1] {
		t.Fatalf("expected unique per-response CSP nonces, got identical %q", nonces[0])
	}

	// 4. Studio runtime served at hashed /s/companion-rig-studio-<hash>.mjs path.
	reqAsset := authRequest(http.MethodGet, "/s/"+studioAssetName, "")
	reqAsset.AddCookie(cookie)
	recAsset := authServe(d, reqAsset)
	if recAsset.Code != http.StatusOK {
		t.Fatalf("studio runtime status = %d, want 200", recAsset.Code)
	}
	if ct := recAsset.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("studio runtime Content-Type = %q, want javascript", ct)
	}
	if !bytes.Equal(recAsset.Body.Bytes(), studioAssetData) {
		t.Fatalf("studio runtime body mismatch (%d vs %d bytes)", recAsset.Body.Len(), len(studioAssetData))
	}
}
