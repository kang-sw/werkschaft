package wsdashboard

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestCompanionVrmAssetBundleAndRoute(t *testing.T) {
	name, data := CompanionVrmAsset()
	if !regexp.MustCompile(`^companion-vrm-[0-9a-f]{24}\.mjs$`).MatchString(name) {
		t.Fatalf("CompanionVrmAsset name = %q, want content-hashed companion-vrm-<24hex>.mjs", name)
	}
	if len(data) == 0 {
		t.Fatal("CompanionVrmAsset embedded bytes are empty")
	}
	// Hard acceptance gate: gzipped embedded runtime must stay <= 300 KiB.
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	const maxGzipBytes = 300 * 1024
	if buf.Len() > maxGzipBytes {
		t.Fatalf("companion-vrm-runtime.mjs gzip size = %d bytes, exceeds %d bytes", buf.Len(), maxGzipBytes)
	}
	// Verify required exports are present in the ESM output.
	src := string(data)
	for _, sym := range []string{"GLTFLoader", "VRMLoaderPlugin", "VRMUtils", "VRMAnimationLoaderPlugin", "createVRMAnimationClip"} {
		if !strings.Contains(src, sym) {
			t.Fatalf("companion-vrm-runtime.mjs missing expected symbol %q", sym)
		}
	}

	// Route headers, content, and auth gate (not publicAsset).
	f := startPageFixture(t)
	res, err := testClient().Get(f.base + "/s/" + name)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK ||
		body != src ||
		res.Header.Get("Content-Type") != "text/javascript; charset=utf-8" ||
		res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("GET /s/%s: status=%d headers=%v len=%d", name, res.StatusCode, res.Header, len(body))
	}

	// Unknown companion-vrm hash returns 404.
	if code, _ := get(t, f.base+"/s/companion-vrm-000000000000000000000000.mjs"); code != http.StatusNotFound {
		t.Fatalf("unknown companion-vrm hash returned %d, want 404", code)
	}

	// Cookieless loopback request is refused (401); cookie request succeeds (200).
	local := authRequest("GET", "/s/"+name, "")
	local.Header.Set("Sec-Fetch-Mode", "cors")
	if got := authServe(f.d, local); got.Code != http.StatusUnauthorized {
		t.Fatalf("cookieless loopback /s/%s = %d, want 401", name, got.Code)
	}
	local.AddCookie(authCookie(t, f.d))
	if got := authServe(f.d, local); got.Code != http.StatusOK {
		t.Fatalf("cookie loopback /s/%s = %d, want 200", name, got.Code)
	}

	// Unauthenticated LAN request is refused (not 200); LAN token cookie succeeds (200).
	lan := startPageFixture(t, func(o *DaemonOptions) { o.LAN = true })
	token, err := lan.d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	lanReq := authRequest("GET", "/s/"+name, "")
	lanReq.Host, lanReq.RemoteAddr = fmt.Sprintf("192.0.2.1:%d", lan.d.endpoint.Port), "192.0.2.2:12345"
	lanReq.Header.Set("Sec-Fetch-Mode", "cors")
	if got := authServe(lan.d, lanReq); got.Code == http.StatusOK {
		t.Fatalf("unauthenticated LAN /s/%s returned 200", name)
	}
	lanReq.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
	if got := authServe(lan.d, lanReq); got.Code != http.StatusOK {
		t.Fatalf("authenticated LAN /s/%s = %d, want 200", name, got.Code)
	}

	// Outer shell HTML exposes only the URL string and never inlines the runtime.
	shellRes, err := testClient().Get(f.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	shellHTML := readBody(t, shellRes)
	wantURL := `window.wsCompanionVrmRuntime = "/s/` + name + `";`
	if !strings.Contains(shellHTML, wantURL) {
		t.Fatalf("shell HTML missing %q", wantURL)
	}
	mid := len(data) / 2
	if strings.Contains(shellHTML, string(data[:128])) || strings.Contains(shellHTML, string(data[mid:mid+128])) {
		t.Fatal("shell HTML must not inline the VRM runtime bundle")
	}
}
