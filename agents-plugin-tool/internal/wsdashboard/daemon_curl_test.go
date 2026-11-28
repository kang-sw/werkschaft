package wsdashboard

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

// TestDaemonCurlAccessExemptClassificationComplete parses localhost_auth.go's
// accessExempt function and fails if any exempt route, prefix, or helper is not
// explicitly classified in daemonCurlAccessExemptPolicy (ticket (1753)).
func TestDaemonCurlAccessExemptClassificationComplete(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "localhost_auth.go", nil, 0)
	if err != nil {
		t.Fatalf("parse localhost_auth.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "accessExempt" {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("func accessExempt not found in localhost_auth.go")
	}

	constValues := map[string]string{
		"ProxyPath":                ProxyPath,
		"ProxyHTTPPath":            ProxyHTTPPath,
		"FullStopPath":             FullStopPath,
		"companionChangedPath":     companionChangedPath,
		"wscred.TokenRoutePath":    wscred.TokenRoutePath,
		"credentialChangePath":     credentialChangePath,
		"EndpointChangePath":       EndpointChangePath,
		"MetaAgentRecordPath":      MetaAgentRecordPath,
		"MetaHostedDescendantPath": MetaHostedDescendantPath,
		"MetaUnregisterCheckPath":  MetaUnregisterCheckPath,
		"MetaPrunePath":            MetaPrunePath,
		"pwaPrefix":                pwaPrefix,
	}

	resolveExpr := func(expr ast.Expr) (string, bool) {
		switch e := expr.(type) {
		case *ast.BasicLit:
			if e.Kind == token.STRING {
				s, err := strconv.Unquote(e.Value)
				return s, err == nil
			}
		case *ast.Ident:
			v, ok := constValues[e.Name]
			if ok {
				return v, true
			}
			return e.Name, true
		case *ast.SelectorExpr:
			if x, ok := e.X.(*ast.Ident); ok {
				key := x.Name + "." + e.Sel.Name
				if v, ok := constValues[key]; ok {
					return v, true
				}
				return key, true
			}
		}
		return "", false
	}

	var discovered []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CaseClause:
			for _, expr := range node.List {
				if key, ok := resolveExpr(expr); ok {
					discovered = append(discovered, key)
				}
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "strings" && sel.Sel.Name == "HasPrefix" && len(node.Args) == 2 {
					if key, ok := resolveExpr(node.Args[1]); ok {
						discovered = append(discovered, key)
					}
				}
			}
			if id, ok := node.Fun.(*ast.Ident); ok {
				if id.Name != "loopbackExemptCaller" {
					discovered = append(discovered, id.Name)
				}
			}
		}
		return true
	})

	if len(discovered) == 0 {
		t.Fatal("discovered no routes in accessExempt")
	}
	for _, key := range discovered {
		allowed, ok := daemonCurlAccessExemptPolicy[key]
		if !ok {
			t.Errorf("accessExempt entry %q is missing from daemonCurlAccessExemptPolicy; classify it as allowed or forbidden", key)
			continue
		}
		if !allowed && strings.HasPrefix(key, "/") {
			sample := key
			if strings.HasSuffix(sample, "/") {
				sample += "probe"
			}
			if !isDaemonCurlForbiddenPath(sample) {
				t.Errorf("classified-forbidden route %q (%q) is NOT rejected by isDaemonCurlForbiddenPath", key, sample)
			}
		}
	}
}

func TestDaemonCurlValidationForbiddenPathsAndRedaction(t *testing.T) {
	f := startPageFixture(t)

	// Invalid methods.
	for _, method := range []string{"", "GET POST", "G3T", "GET\r\n"} {
		_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{Method: method, Path: "/health"})
		dce, ok := IsDaemonControlError(err)
		if !ok || dce.Code != "invalid_request" {
			t.Errorf("method %q -> (%v), want invalid_request", method, err)
		}
	}

	// Invalid paths (scheme, host, protocol-relative, traversal, query/fragment, backslash).
	for _, p := range []string{
		"", "health", "//127.0.0.1/health", "http://127.0.0.1/health",
		"https://example.com/", "/health?a=1", "/health#frag",
		"/a/../settings", "/a/%2e%2e/settings", `/a\b`, "/health\r\nX: 1",
		// Double-encoded paths must be rejected up front: a second decode would
		// otherwise resolve them to forbidden routes or traversal.
		"/%2573ettings", "/%2561uth/status", "/%2561uth/link",
		"/a/%252e%252e/settings", "/%2561pi/daemon/bridge-reply",
	} {
		_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{Method: "GET", Path: p})
		dce, ok := IsDaemonControlError(err)
		if !ok || dce.Code != "invalid_request" {
			t.Errorf("path %q -> (%v), want invalid_request", p, err)
		}
	}

	// Disallowed headers (auth, cookie, origin, method-override).
	for _, hdr := range []string{
		"Authorization", "Cookie", "Origin", "Host",
		"X-HTTP-Method-Override", "X-Method-Override", "X-HTTP-Method", "X-Custom",
	} {
		_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
			Method:  "GET",
			Path:    "/health",
			Headers: map[string]string{hdr: "val"},
		})
		dce, ok := IsDaemonControlError(err)
		if !ok || dce.Code != "invalid_request" {
			t.Errorf("header %q -> (%v), want invalid_request", hdr, err)
		}
	}

	// GET/HEAD with body rejected.
	for _, method := range []string{"GET", "HEAD", "get", "head"} {
		_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{Method: method, Path: "/health", Body: "x"})
		dce, ok := IsDaemonControlError(err)
		if !ok || dce.Code != "invalid_request" {
			t.Errorf("%s with body -> (%v), want invalid_request", method, err)
		}
	}

	// Request body > 256 KiB rejected as payload_too_large.
	_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
		Method: "POST",
		Path:   "/rail/memos",
		Body:   strings.Repeat("a", daemonCurlMaxReqBytes+1),
	})
	if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "payload_too_large" {
		t.Fatalf("oversize body -> %v, want payload_too_large", err)
	}

	// Forbidden paths rejected for EVERY method (including GET and POST without even asking confirmation).
	forbidden := []string{
		DaemonBridgeReplyPath,
		DaemonBridgeReplyPath + "/extra",
		"/settings",
		"/settings/",
		"/Settings",
		"/%73ettings",
		"/auth/cookie",
		"/auth/link",
		"/auth/exchange",
		"/auth/clipboard",
		"/auth/status",
		"/shutdown",
		FullStopPath,
		wscred.TokenRoutePath,
		credentialChangePath,
		EndpointChangePath,
		ProxyPath,
		ProxyHTTPPath,
		MetaAgentRecordPath,
		"/agents/by-token/secret-tok",
		"/api/account",
		"/api/account/status",
		"/api/account/slots/sign-in/status",
		"/api/account/key",
		"/API/Account/status",
		"/%61pi/account/status",
		"/api/config/snapshot",
		"/api/config/set",
		"/debug/pprof",
		"/debug/pprof/",
		"/debug/pprof/goroutine",
	}
	for _, p := range forbidden {
		for _, method := range []string{"GET", "HEAD", "POST", "DELETE"} {
			_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{Method: method, Path: p})
			dce, ok := IsDaemonControlError(err)
			if !ok || dce.Code != "forbidden_path" {
				t.Errorf("%s %s -> %v, want forbidden_path", method, p, err)
			}
		}
	}

	// Redaction scrubs daemon secrets, LAN tokens, query tokens, bearer tokens, and cookies.
	lanPath := filepath.Join(f.d.dir, LANTokenFileName)
	if err := os.WriteFile(lanPath, []byte("lan-secret-token-xyz123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cookieVal, err := f.d.localhostCookieValue()
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Join([]string{
		"ep=" + f.d.endpoint.Token,
		"ck=" + cookieVal,
		"lan=lan-secret-token-xyz123",
		"url=http://192.168.1.2:9000/?token=abcd12345678&ok=1",
		"login=http://127.0.0.1:9000/?login=oneTimeCode999",
		"auth=Bearer superSecretBearerToken999",
		"cookie=ws_dashboard_localhost=abc999def; Path=/",
	}, " ")
	redacted := f.d.redactDaemonSecrets(raw)
	for _, leak := range []string{
		f.d.endpoint.Token, cookieVal, "lan-secret-token-xyz123",
		"abcd12345678", "oneTimeCode999", "superSecretBearerToken999", "abc999def",
	} {
		if leak != "" && strings.Contains(redacted, leak) {
			t.Errorf("redacted output leaked secret %q: %s", leak, redacted)
		}
	}
}

func TestDaemonCurlImmediateReadsAndGatedWrites(t *testing.T) {
	f := startPageFixture(t)

	// 1. GET /health and HEAD /health succeed immediately even with zero tabs connected.
	getResp, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
		Method:  "get",
		Path:    "/health",
		Headers: map[string]string{"accept": "application/json"},
	})
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	if getResp.Status != http.StatusOK || !strings.Contains(getResp.Body, healthService) {
		t.Fatalf("GET /health = %+v", getResp)
	}
	if _, hasCT := getResp.Headers["content-type"]; !hasCT || len(getResp.Headers) != 1 {
		t.Fatalf("GET /health headers = %+v, want content-type only", getResp.Headers)
	}

	headResp, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
		Method: "HEAD",
		Path:   "/health",
	})
	if err != nil {
		t.Fatalf("HEAD /health: %v", err)
	}
	if headResp.Status != http.StatusOK || headResp.Body != "" {
		t.Fatalf("HEAD /health = %+v, want 200 with empty body", headResp)
	}

	// 2. Mutating verb with no connected tab fails closed immediately with no_active_tab.
	start := time.Now()
	_, err = f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
		Method:  "POST",
		Path:    "/rail/memos",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{}`,
	})
	if time.Since(start) > time.Second {
		t.Fatalf("no_active_tab curl waited too long: %v", time.Since(start))
	}
	if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "no_active_tab" {
		t.Fatalf("POST with no tab -> %v, want no_active_tab", err)
	}

	// 3. Connect a local tab: test approve, deny, dismiss, and timeout for POST /nudge.
	tab := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-curl&display=1", f.base, true)
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool { return len(tabs) == 1 })

	// Approve -> executes in-process as local trusted viewer.
	type curlRes struct {
		resp DaemonCurlResponse
		err  error
	}
	resCh := make(chan curlRes, 1)
	go func() {
		bodyBytes, _ := json.Marshal(NudgeBody{ReplyID: "livesession", Project: f.project})
		r, e := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
			Method:  "POST",
			Path:    "/nudge",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    string(bodyBytes),
		})
		resCh <- curlRes{r, e}
	}()

	ev := waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	var pushed confirmPushPayload
	if err := json.Unmarshal([]byte(ev.data), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed.Method != "POST" || pushed.Path != "/nudge" {
		t.Fatalf("unexpected confirm push: %+v", pushed)
	}
	reply := postBridgeReply(t, f, map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      pushed.Nonce,
		"tab_id":     "tab-curl",
		"decision":   "approve",
	})
	reply.Body.Close()

	got := <-resCh
	if got.err != nil {
		t.Fatalf("approved POST /nudge failed: %v", got.err)
	}
	if got.resp.Status != http.StatusNoContent && got.resp.Status != http.StatusOK {
		t.Fatalf("approved POST /nudge status = %d, want 200/204", got.resp.Status)
	}

	// Deny -> returns denied without executing request.
	resCh2 := make(chan curlRes, 1)
	go func() {
		r, e := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
			Method: "DELETE",
			Path:   "/nudge",
		})
		resCh2 <- curlRes{r, e}
	}()
	ev2 := waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	var pushed2 confirmPushPayload
	_ = json.Unmarshal([]byte(ev2.data), &pushed2)
	reply2 := postBridgeReply(t, f, map[string]any{
		"request_id": pushed2.RequestID,
		"nonce":      pushed2.Nonce,
		"tab_id":     "tab-curl",
		"decision":   "deny",
	})
	reply2.Body.Close()
	got2 := <-resCh2
	if dce, ok := IsDaemonControlError(got2.err); !ok || dce.Code != "denied" {
		t.Fatalf("denied DELETE -> %v, want denied", got2.err)
	}

	// Timeout -> returns timeout.
	f.d.daemonConfirmTimeout = 60 * time.Millisecond
	_, err = f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
		Method: "PATCH",
		Path:   "/nudge",
	})
	if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "timeout" {
		t.Fatalf("timed-out PATCH -> %v, want timeout", err)
	}
}

func TestDaemonCurlResponseTruncationCap(t *testing.T) {
	rec := newBoundedCurlRecorder(16)
	rec.Header().Set("Content-Type", "text/plain")
	rec.Header().Set("Set-Cookie", "ws_dashboard_localhost=secret")
	_, _ = rec.Write([]byte("0123456789abcdef"))
	if rec.truncated {
		t.Fatal("exact-fit write should not mark truncated")
	}
	_, _ = rec.Write([]byte("EXTRA"))
	if !rec.truncated || rec.buf.String() != "0123456789abcdef" {
		t.Fatalf("truncated=%v buf=%q, want true and 16-byte prefix", rec.truncated, rec.buf.String())
	}
}
