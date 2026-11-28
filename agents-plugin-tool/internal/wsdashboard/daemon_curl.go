package wsdashboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kang-sw/devenv/internal/wscred"
)

const (
	daemonCurlMaxReqBytes    = 256 << 10 // 256 KiB
	daemonCurlMaxRespBytes   = 512 << 10 // 512 KiB
	daemonCurlDefaultTimeout = 30 * time.Second
)

// DaemonCurlRequest is the structured input to ws/daemon.curl (ticket (1753)).
type DaemonCurlRequest struct {
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          map[string]string `json:"query,omitempty"`
	Body           string            `json:"body,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	TabID          string            `json:"tab_id,omitempty"`
	CompanionPause bool              `json:"companion_pause,omitempty"`
}

// DaemonCurlResponse is the bounded, credential-redacted response of ws/daemon.curl.
type DaemonCurlResponse struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body"`
	Truncated bool              `json:"truncated,omitempty"`
}

// daemonCurlAccessExemptPolicy classifies every route/prefix exempted in
// localhost_auth.go's accessExempt function as allowed (true) or forbidden
// (false) for ws/daemon.curl. TestDaemonCurlAccessExemptClassificationComplete
// parses accessExempt via go/ast and fails if any exempt route is missing here.
var daemonCurlAccessExemptPolicy = map[string]bool{
	"/auth/status":              false,
	"/auth/exchange":            false,
	"/auth/link":                false,
	"/auth/cookie":              false,
	"/auth/clipboard":           false,
	"/health":                   true,
	ProxyPath:                   false,
	"/session-hire/roster":      true,
	ProxyHTTPPath:               false,
	"/shutdown":                 false,
	FullStopPath:                false,
	"/nudge":                    true,
	companionChangedPath:        false,
	wscred.TokenRoutePath:       false,
	credentialChangePath:        false,
	EndpointChangePath:          false,
	MetaAgentRecordPath:         false,
	MetaHostedDescendantPath:    false,
	MetaUnregisterCheckPath:     false,
	MetaPrunePath:               false,
	"/agents/by-token/":         false,
	pwaPrefix:                   true,
	"publicAsset":               true,
	"previewCapability":         true,
}

// isDaemonCurlForbiddenPath rejects every credential-bearing, auth, shutdown,
// machine-bearer, and bridge-reply path for ALL HTTP methods (ticket (1753)).
func isDaemonCurlForbiddenPath(cleanPath string) bool {
	lower := strings.ToLower(cleanPath)
	if allowed, classified := daemonCurlAccessExemptPolicy[cleanPath]; classified && !allowed {
		return true
	}
	if allowed, classified := daemonCurlAccessExemptPolicy[lower]; classified && !allowed {
		return true
	}
	switch {
	case lower == DaemonBridgeReplyPath || strings.HasPrefix(lower, DaemonBridgeReplyPath+"/"):
		return true
	case lower == "/settings" || strings.HasPrefix(lower, "/settings/"):
		return true
	case lower == "/auth" || strings.HasPrefix(lower, "/auth/"):
		return true
	case lower == strings.TrimSuffix(accountPrefix, "/") || strings.HasPrefix(lower, accountPrefix):
		// Account routes expose live OAuth authorize_url/verify_url/user_code and
		// mutate account credentials.
		return true
	case lower == "/api/config" || strings.HasPrefix(lower, "/api/config/"):
		return true
	case lower == strings.TrimSuffix(pprofPrefix, "/") || strings.HasPrefix(lower, pprofPrefix):
		return true
	case lower == "/shutdown" || strings.HasPrefix(lower, "/shutdown/"):
		return true
	case lower == strings.ToLower(FullStopPath) || strings.HasPrefix(lower, strings.ToLower(FullStopPath)+"/"):
		return true
	case lower == strings.ToLower(ProxyPath) || strings.HasPrefix(lower, strings.ToLower(ProxyPath)+"/"):
		return true
	case lower == strings.ToLower(ProxyHTTPPath) || strings.HasPrefix(lower, strings.ToLower(ProxyHTTPPath)+"/"):
		return true
	case lower == "/agents/by-token" || strings.HasPrefix(lower, "/agents/by-token/"):
		return true
	case lower == "/mcp" || strings.HasPrefix(lower, "/mcp/"):
		return true
	case strings.HasPrefix(lower, "/meta/"):
		return true
	case lower == strings.ToLower(wscred.TokenRoutePath) || strings.HasPrefix(lower, strings.ToLower(wscred.TokenRoutePath)+"/"):
		return true
	case lower == strings.ToLower(credentialChangePath) || strings.HasPrefix(lower, strings.ToLower(credentialChangePath)+"/"):
		return true
	case lower == strings.ToLower(EndpointChangePath) || strings.HasPrefix(lower, strings.ToLower(EndpointChangePath)+"/"):
		return true
	case lower == strings.ToLower(companionChangedPath) || strings.HasPrefix(lower, strings.ToLower(companionChangedPath)+"/"):
		return true
	}
	return false
}

func normalizeDaemonCurlMethod(raw string) (string, error) {
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", &DaemonControlError{Code: "invalid_request", Message: fmt.Sprintf("invalid HTTP method %q", raw)}
	}
	m := strings.ToUpper(strings.TrimSpace(raw))
	if m == "" || len(m) > 16 {
		return "", &DaemonControlError{Code: "invalid_request", Message: "method must be a valid HTTP verb"}
	}
	for i := 0; i < len(m); i++ {
		c := m[i]
		if c < 'A' || c > 'Z' {
			return "", &DaemonControlError{Code: "invalid_request", Message: fmt.Sprintf("invalid HTTP method %q", raw)}
		}
	}
	return m, nil
}

func validateDaemonCurlPath(raw string) (string, error) {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", &DaemonControlError{Code: "invalid_request", Message: "path must be a daemon-relative path starting with a single '/'"}
	}
	if strings.ContainsAny(raw, "\\?#\r\n") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", &DaemonControlError{Code: "invalid_request", Message: "path must not contain query, fragment, backslash, or control characters (pass query separately)"}
	}
	if strings.Contains(raw, "://") {
		return "", &DaemonControlError{Code: "invalid_request", Message: "path must not contain a URL scheme or host"}
	}
	unescaped, err := url.PathUnescape(raw)
	if err != nil || strings.ContainsAny(unescaped, "\\?#\r\n") || strings.IndexFunc(unescaped, unicode.IsControl) >= 0 {
		return "", &DaemonControlError{Code: "invalid_request", Message: "path contains invalid percent-encoding or control characters"}
	}
	// A '%' surviving one decode means double-encoding (%25xx); a later decode
	// would resolve it to a different path than the one classified here.
	if strings.Contains(unescaped, "%") {
		return "", &DaemonControlError{Code: "invalid_request", Message: "path must not contain double-encoded or literal '%' sequences"}
	}
	for _, candidate := range []string{raw, unescaped} {
		for _, seg := range strings.Split(candidate, "/") {
			if seg == ".." {
				return "", &DaemonControlError{Code: "invalid_request", Message: "path must not contain '..' traversal segments"}
			}
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", &DaemonControlError{Code: "invalid_request", Message: "path must be relative with no scheme, host, or userinfo"}
	}
	clean := path.Clean(unescaped)
	if !strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "//") || clean == "/.." || strings.Contains(clean, "/../") {
		return "", &DaemonControlError{Code: "invalid_request", Message: "normalized path is invalid"}
	}
	// Preserve trailing slash for directory/prefix routes like /agents/ when clean dropped it.
	if strings.HasSuffix(raw, "/") && clean != "/" && !strings.HasSuffix(clean, "/") {
		clean += "/"
	}
	return clean, nil
}

func validateDaemonCurlHeaders(headers map[string]string) (http.Header, error) {
	out := make(http.Header)
	for k, v := range headers {
		name := strings.TrimSpace(k)
		if strings.ContainsAny(v, "\r\n") {
			return nil, &DaemonControlError{Code: "invalid_request", Message: "header value contains invalid line breaks"}
		}
		switch {
		case strings.EqualFold(name, "Accept"):
			out.Set("Accept", v)
		case strings.EqualFold(name, "Content-Type"):
			out.Set("Content-Type", v)
		default:
			return nil, &DaemonControlError{Code: "invalid_request", Message: fmt.Sprintf("header %q is not allowed (only Accept and Content-Type are permitted)", name)}
		}
	}
	return out, nil
}

var (
	redactQuerySecretRe  = regexp.MustCompile(`([?&](?:token|login)=)[^&#\s"'<>]+`)
	redactBearerRe       = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	redactCookieSecretRe = regexp.MustCompile(`(ws_dashboard_(?:localhost|token|screenshot_render)=)[^;\s"'<>]+`)
	redactAPIKeyRe       = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|AIza[A-Za-z0-9_-]{20,})\b`)
)

// RedactCredentials scrubs LAN/login query tokens, bearer-shaped tokens,
// dashboard cookies, and common API key prefixes from text returned to models
// (shared by ws/daemon.curl and Phase 2 ws/daemon.dom).
func RedactCredentials(text string) string {
	if text == "" {
		return ""
	}
	text = redactQuerySecretRe.ReplaceAllString(text, "${1}[REDACTED]")
	text = redactBearerRe.ReplaceAllString(text, "Bearer [REDACTED]")
	text = redactCookieSecretRe.ReplaceAllString(text, "${1}[REDACTED]")
	text = redactAPIKeyRe.ReplaceAllString(text, "[REDACTED]")
	return text
}

func (d *Daemon) redactDaemonSecrets(text string) string {
	if text == "" {
		return ""
	}
	if d.endpoint.Token != "" {
		text = strings.ReplaceAll(text, d.endpoint.Token, "[REDACTED]")
	}
	if cookieVal, err := d.localhostCookieValue(); err == nil && cookieVal != "" {
		text = strings.ReplaceAll(text, cookieVal, "[REDACTED]")
	}
	// Read LAN token passively (never mint one on read).
	if lanTok, err := d.readSecret(LANTokenFileName); err == nil && lanTok != "" {
		text = strings.ReplaceAll(text, lanTok, "[REDACTED]")
	}
	return RedactCredentials(text)
}

func (d *Daemon) curlTimeout() time.Duration {
	if d.daemonCurlTimeout > 0 {
		return d.daemonCurlTimeout
	}
	return daemonCurlDefaultTimeout
}

// DaemonCurl executes a bounded, credential-redacted loopback request directly
// against the daemon's own HTTP handler (ticket (1753)). GET and HEAD run
// immediately; every other HTTP verb blocks behind RequestDaemonConfirmation.
func (d *Daemon) DaemonCurl(ctx context.Context, req DaemonCurlRequest) (DaemonCurlResponse, error) {
	method, err := normalizeDaemonCurlMethod(req.Method)
	if err != nil {
		return DaemonCurlResponse{}, err
	}
	cleanPath, err := validateDaemonCurlPath(req.Path)
	if err != nil {
		return DaemonCurlResponse{}, err
	}
	if isDaemonCurlForbiddenPath(cleanPath) {
		return DaemonCurlResponse{}, &DaemonControlError{
			Code:    "forbidden_path",
			Message: fmt.Sprintf("path %q is forbidden for daemon.curl", cleanPath),
		}
	}
	hdrs, err := validateDaemonCurlHeaders(req.Headers)
	if err != nil {
		return DaemonCurlResponse{}, err
	}
	if (method == http.MethodGet || method == http.MethodHead) && req.Body != "" {
		return DaemonCurlResponse{}, &DaemonControlError{
			Code:    "invalid_request",
			Message: fmt.Sprintf("%s requests must not include a body", method),
		}
	}
	if len(req.Body) > daemonCurlMaxReqBytes {
		return DaemonCurlResponse{}, &DaemonControlError{
			Code:    "payload_too_large",
			Message: fmt.Sprintf("request body exceeds %d bytes", daemonCurlMaxReqBytes),
		}
	}

	// Server-side fail-closed classification: only GET and HEAD are immediate;
	// every other method passes through the top-level shell 1-click gate.
	if method != http.MethodGet && method != http.MethodHead {
		confirm := d.RequestDaemonConfirmation(ctx, DaemonConfirmRequest{
			TabID:          req.TabID,
			Tool:           "daemon.curl",
			Method:         method,
			Path:           cleanPath,
			Summary:        fmt.Sprintf("%s %s", method, cleanPath),
			CompanionPause: req.CompanionPause,
		})
		if !confirm.Approved {
			code := confirm.Code
			if req.CompanionPause || code == "" {
				code = "denied"
			}
			return DaemonCurlResponse{}, &DaemonControlError{
				Code:    code,
				Reason:  confirm.Reason,
				Message: fmt.Sprintf("confirmation denied (%s) for %s %s", confirm.Reason, method, cleanPath),
			}
		}
	}

	// Build in-process loopback request authenticated as the local trusted viewer.
	reqCtx, cancel := context.WithTimeout(ctx, d.curlTimeout())
	defer cancel()

	host := "127.0.0.1:" + strconv.Itoa(d.endpoint.Port)
	// cleanPath is already percent-decoded once by validateDaemonCurlPath. Set it
	// as an explicit URL.Path so no second unescape can reinterpret it (parsing
	// "http://host"+cleanPath would decode %xx again and bypass the forbidden-path
	// classification).
	target := url.URL{Scheme: "http", Host: host, Path: cleanPath}
	if len(req.Query) > 0 {
		q := url.Values{}
		for k, v := range req.Query {
			q.Set(k, v)
		}
		target.RawQuery = q.Encode()
	}
	var bodyReader *bytes.Reader
	if req.Body != "" {
		bodyReader = bytes.NewReader([]byte(req.Body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, method, "http://"+host+"/", bodyReader)
	if err != nil {
		return DaemonCurlResponse{}, &DaemonControlError{Code: "invalid_request", Message: err.Error()}
	}
	httpReq.URL = &target
	httpReq.Host = host
	httpReq.RemoteAddr = "127.0.0.1:1"
	httpReq.Header = hdrs
	httpReq.Header.Set("Origin", "http://"+host)
	httpReq.Header.Set("Sec-Fetch-Site", "same-origin")

	cookieVal, err := d.localhostCookieValue()
	if err != nil {
		return DaemonCurlResponse{}, &DaemonControlError{Code: "denied", Message: "localhost credential unavailable"}
	}
	httpReq.AddCookie(&http.Cookie{Name: localhostCookieName, Value: cookieVal})

	// Defense in depth: re-classify the exact path the handler will route on.
	if isDaemonCurlForbiddenPath(path.Clean(httpReq.URL.Path)) {
		return DaemonCurlResponse{}, &DaemonControlError{
			Code:    "forbidden_path",
			Message: fmt.Sprintf("path %q is forbidden for daemon.curl", cleanPath),
		}
	}

	rec := newBoundedCurlRecorder(daemonCurlMaxRespBytes)
	d.Handler().ServeHTTP(rec, httpReq)

	if errors.Is(reqCtx.Err(), context.DeadlineExceeded) || (reqCtx.Err() != nil && rec.status == 0) {
		return DaemonCurlResponse{}, &DaemonControlError{Code: "timeout", Message: "daemon.curl request timed out"}
	}
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	respHeaders := map[string]string{}
	if ct := rec.header.Get("Content-Type"); ct != "" {
		respHeaders["content-type"] = ct
	}
	body := ""
	if method != http.MethodHead {
		body = d.redactDaemonSecrets(rec.buf.String())
	}
	return DaemonCurlResponse{
		Status:    status,
		Headers:   respHeaders,
		Body:      body,
		Truncated: rec.truncated,
	}, nil
}

// boundedCurlRecorder intentionally omits http.Flusher and http.Hijacker so
// streaming/WS routes fail fast instead of hanging in-process curl calls.
type boundedCurlRecorder struct {
	header    http.Header
	status    int
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedCurlRecorder(limit int) *boundedCurlRecorder {
	return &boundedCurlRecorder{
		header: make(http.Header),
		limit:  limit,
	}
}

func (r *boundedCurlRecorder) Header() http.Header { return r.header }

func (r *boundedCurlRecorder) WriteHeader(statusCode int) {
	if r.status == 0 {
		r.status = statusCode
	}
}

func (r *boundedCurlRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	remaining := r.limit - r.buf.Len()
	if remaining <= 0 {
		if len(p) > 0 {
			r.truncated = true
		}
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = r.buf.Write(p[:remaining])
		r.truncated = true
		return len(p), nil
	}
	_, _ = r.buf.Write(p)
	return len(p), nil
}
