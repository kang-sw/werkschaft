package wsweb

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Default bounds for bounded public HTTP(S) page retrieval.
const (
	DefaultTimeout          = 20 * time.Second
	DefaultMaxResponseBytes = 2 << 20 // 2 MiB raw body cap
	DefaultMaxRedirects     = 5
	DefaultMaxOutputBytes   = 48 << 10 // 48 KiB (matches werk.ToolOutputBytes)
	DefaultUserAgent        = "ws-web-fetch/1.0"
)

// Sentinel errors for callers and tests to assert on refusal causes.
var (
	ErrInvalidURL       = errors.New("invalid URL")
	ErrPrivateAddress   = errors.New("refused non-public network address")
	ErrTooManyRedirects = errors.New("too many redirects")
	ErrResponseTooLarge = errors.New("response body exceeds size limit")
	ErrBinaryContent    = errors.New("binary or non-text content is not supported")
)

// IPResolver looks up IP addresses for a hostname. Nil uses net.DefaultResolver.
type IPResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// DialFunc establishes a TCP connection to a verified IP:port.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Options configures Fetch. Zero fields take the package defaults.
type Options struct {
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRedirects     int
	MaxOutputBytes   int
	UserAgent        string
	Resolver         IPResolver
	DialContext      DialFunc
	TLSClientConfig  *tls.Config
}

// Result is one fetched page reduced to bounded readable text.
type Result struct {
	URL         string
	StatusCode  int
	ContentType string
	Title       string
	Text        string
	Truncated   bool
}

var nonPublicPrefixes = mustParsePrefixes(
	// IPv4 special-purpose ranges (RFC 6890 / IANA).
	"0.0.0.0/8",       // "This host on this network"
	"10.0.0.0/8",      // Private-use (RFC 1918)
	"100.64.0.0/10",   // Shared Address Space / CGNAT (RFC 6598)
	"127.0.0.0/8",     // Loopback
	"169.254.0.0/16",  // Link-local / cloud metadata
	"172.16.0.0/12",   // Private-use (RFC 1918)
	"192.0.0.0/24",    // IETF Protocol Assignments
	"192.0.2.0/24",    // Documentation TEST-NET-1
	"192.168.0.0/16",  // Private-use (RFC 1918)
	"198.18.0.0/15",   // Benchmarking
	"198.51.100.0/24", // Documentation TEST-NET-2
	"203.0.113.0/24",  // Documentation TEST-NET-3
	"224.0.0.0/4",     // Multicast
	"240.0.0.0/4",     // Reserved and limited broadcast (255.255.255.255/32)
	// IPv6 special-purpose ranges.
	"::/128",          // Unspecified
	"::1/128",         // Loopback
	"::/96",           // Deprecated IPv4-compatible
	"64:ff9b::/96",    // NAT64 well-known prefix
	"64:ff9b:1::/48",  // NAT64 local-use
	"100::/64",        // Discard-only
	"2001::/32",       // Teredo
	"2001:2::/48",     // Benchmarking
	"2001:10::/28",    // ORCHID
	"2001:20::/28",    // ORCHIDv2
	"2001:db8::/32",   // Documentation
	"2002::/16",       // 6to4
	"fc00::/7",        // Unique local (ULA)
	"fe80::/10",       // Link-local unicast
	"fec0::/10",       // Deprecated site-local
	"ff00::/8",        // Multicast
)

func mustParsePrefixes(raw ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// isPublicAddr reports whether addr is a globally routable public IP address.
// IPv4-mapped IPv6 addresses are unmapped before checking so ::ffff:127.0.0.1
// and ::ffff:10.0.0.1 are refused identically to their IPv4 equivalents.
func isPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// validateURL parses and checks an outbound fetch target: HTTP/HTTPS only,
// non-empty host, no embedded credentials, no control characters, valid port.
func validateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: URL is required", ErrInvalidURL)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return nil, fmt.Errorf("%w: control characters are not allowed", ErrInvalidURL)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: only http and https URLs are supported (got %q)", ErrInvalidURL, u.Scheme)
	}
	u.Scheme = scheme
	if u.User != nil {
		return nil, fmt.Errorf("%w: embedded credentials are not allowed", ErrInvalidURL)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrInvalidURL)
	}
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("%w: IPv6 zone identifiers are not allowed", ErrPrivateAddress)
	}
	if portStr := u.Port(); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("%w: invalid port %q", ErrInvalidURL, portStr)
		}
	}
	return u, nil
}

func resolvePublicAddrs(ctx context.Context, resolver IPResolver, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddr(addr) {
			return nil, fmt.Errorf("%w: %s", ErrPrivateAddress, addr)
		}
		return []netip.Addr{addr.Unmap()}, nil
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses returned", host)
	}
	pinned := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if ip.Zone != "" {
			return nil, fmt.Errorf("%w: %s resolved to zoned address", ErrPrivateAddress, host)
		}
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || !isPublicAddr(addr) {
			return nil, fmt.Errorf("%w: %s resolved to %s", ErrPrivateAddress, host, ip.IP)
		}
		pinned = append(pinned, addr.Unmap())
	}
	return pinned, nil
}

func targetPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// Fetch retrieves one public HTTP(S) URL with SSRF protection (DNS validation
// and IP pinning on every hop), manual redirect cap, no cookies or credentials,
// raw size and time caps, HTML-to-text reduction, and output truncation.
func Fetch(ctx context.Context, rawURL string, opts Options) (Result, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBytes := opts.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = DefaultMaxRedirects
	}
	maxOutput := opts.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	baseDial := opts.DialContext
	if baseDial == nil {
		d := &net.Dialer{Timeout: min(timeout, 10*time.Second)}
		baseDial = d.DialContext
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	current, err := validateURL(rawURL)
	if err != nil {
		return Result{}, err
	}

	for hop := 0; ; hop++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		pinned, err := resolvePublicAddrs(ctx, resolver, current.Hostname())
		if err != nil {
			return Result{}, err
		}
		port := targetPort(current)

		tr := &http.Transport{
			// Direct transport only: never inherit environment proxies.
			Proxy:                 nil,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1,
			ResponseHeaderTimeout: timeout,
			TLSHandshakeTimeout:   min(timeout, 10*time.Second),
			DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
				// Dial only the pre-validated IP(s) from this hop's lookup so
				// DNS rebinding cannot redirect the socket to a private IP.
				var lastErr error
				for _, addr := range pinned {
					conn, dialErr := baseDial(dctx, network, net.JoinHostPort(addr.String(), port))
					if dialErr == nil {
						return conn, nil
					}
					lastErr = dialErr
				}
				return nil, lastErr
			},
		}
		if opts.TLSClientConfig != nil {
			tr.TLSClientConfig = opts.TLSClientConfig.Clone()
		}

		client := &http.Client{
			Transport: tr,
			Jar:       nil, // Never store or send cookies.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			tr.CloseIdleConnections()
			return Result{}, err
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, text/markdown, application/json, application/xml;q=0.9, */*;q=0.1")

		resp, err := client.Do(req)
		if err != nil {
			tr.CloseIdleConnections()
			return Result{}, err
		}

		if isRedirectStatus(resp.StatusCode) {
			loc := strings.TrimSpace(resp.Header.Get("Location"))
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			tr.CloseIdleConnections()
			if loc == "" {
				return Result{}, fmt.Errorf("HTTP %d redirect missing Location header", resp.StatusCode)
			}
			if hop >= maxRedirects {
				return Result{}, fmt.Errorf("%w (limit %d)", ErrTooManyRedirects, maxRedirects)
			}
			rel, err := url.Parse(loc)
			if err != nil {
				return Result{}, fmt.Errorf("%w: invalid redirect target: %v", ErrInvalidURL, err)
			}
			nextURL := current.ResolveReference(rel)
			validated, err := validateURL(nextURL.String())
			if err != nil {
				return Result{}, fmt.Errorf("redirect refused: %w", err)
			}
			current = validated
			continue
		}

		res, err := readAndFormatResponse(current.String(), resp, maxBytes, maxOutput)
		_ = resp.Body.Close()
		tr.CloseIdleConnections()
		return res, err
	}
}

func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func readAndFormatResponse(finalURL string, resp *http.Response, maxBytes int64, maxOutput int) (Result, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if resp.ContentLength > maxBytes {
		return Result{}, fmt.Errorf("%w (%d bytes > %d byte cap)", ErrResponseTooLarge, resp.ContentLength, maxBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return Result{}, err
	}
	if int64(len(raw)) > maxBytes {
		return Result{}, fmt.Errorf("%w (exceeded %d byte cap)", ErrResponseTooLarge, maxBytes)
	}

	mediaType := parseMediaType(resp.Header.Get("Content-Type"), raw)
	if !isAllowedTextMedia(mediaType) {
		return Result{}, fmt.Errorf("%w (%s)", ErrBinaryContent, mediaType)
	}
	if hasBinaryNUL(raw) {
		return Result{}, fmt.Errorf("%w (contains NUL bytes)", ErrBinaryContent)
	}

	var title, body string
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		title, body = htmlToText(raw)
	} else {
		body = normalizePlainText(string(raw))
	}

	out := formatOutput(finalURL, resp.StatusCode, mediaType, title, body)
	truncated := false
	if len(out) > maxOutput {
		out = truncateOutput(out, maxOutput)
		truncated = true
	}
	return Result{
		URL:         finalURL,
		StatusCode:  resp.StatusCode,
		ContentType: mediaType,
		Title:       title,
		Text:        out,
		Truncated:   truncated,
	}, nil
}

func parseMediaType(header string, sample []byte) string {
	if header != "" {
		if mt, _, err := mime.ParseMediaType(header); err == nil && mt != "" {
			return strings.ToLower(strings.TrimSpace(mt))
		}
	}
	if len(sample) == 0 {
		return "text/plain"
	}
	mt, _, err := mime.ParseMediaType(http.DetectContentType(sample))
	if err != nil || mt == "" {
		return "application/octet-stream"
	}
	return strings.ToLower(strings.TrimSpace(mt))
}

func isAllowedTextMedia(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/xhtml+xml", "application/xml", "application/json",
		"application/ld+json", "application/javascript", "application/ecmascript",
		"application/rss+xml", "application/atom+xml", "application/yaml", "application/x-yaml":
		return true
	}
	return strings.HasSuffix(mediaType, "+xml") || strings.HasSuffix(mediaType, "+json")
}

func hasBinaryNUL(raw []byte) bool {
	return bytes.IndexByte(raw[:min(len(raw), 8192)], 0) >= 0
}

func formatOutput(finalURL string, status int, mediaType, title, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\nStatus: %d %s\nContent-Type: %s\n", finalURL, status, http.StatusText(status), mediaType)
	if title != "" {
		fmt.Fprintf(&b, "Title: %s\n", title)
	}
	if body != "" {
		b.WriteByte('\n')
		b.WriteString(body)
	}
	return b.String()
}

// truncateOutput bounds s to limit bytes, keeping head and tail on UTF-8 rune
// boundaries with an omission marker matching werk.TruncateOutput.
func truncateOutput(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	omitted := len(s) - limit
	marker := fmt.Sprintf("\n... [%d bytes truncated] ...\n", omitted)
	if len(marker) >= limit {
		return trimRightUTF8(s[:limit])
	}
	keep := limit - len(marker)
	headLen := keep * 2 / 3
	tailLen := keep - headLen
	head := trimRightUTF8(s[:headLen])
	tail := trimLeftUTF8(s[len(s)-tailLen:])
	actualOmitted := len(s) - len(head) - len(tail)
	marker = fmt.Sprintf("\n... [%d bytes truncated] ...\n", actualOmitted)
	if len(head)+len(marker)+len(tail) > limit {
		excess := len(head) + len(marker) + len(tail) - limit
		if excess < len(tail) {
			tail = trimLeftUTF8(tail[excess:])
		} else {
			tail = ""
		}
	}
	return head + marker + tail
}

func trimRightUTF8(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func trimLeftUTF8(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}
