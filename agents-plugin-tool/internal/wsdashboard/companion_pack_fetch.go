package wsdashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsnetguard"
)

// Response caps of the source fetcher, enforced on the bytes read and early
// on a declared Content-Length. They are variables so a test can lower them.
var (
	companionRefsCap    int64 = 1 << 20  // one ref advertisement
	companionSourceCap  int64 = 64 << 20 // one source zip
	companionPackZipCap int64 = 16 << 20 // one external pack zip
)

const (
	companionFetchTimeout = 60 * time.Second
	companionRedirectMax  = 5
	companionRefMax       = 256
	// companionFetchUserAgent is the one User-Agent every fetch sends. It
	// names no person, machine, or vendor.
	companionFetchUserAgent = "companion-pack-fetch/1"
	companionGitService     = "git-upload-pack"
	companionGitAdvertType  = "application/x-git-upload-pack-advertisement"
	companionPktMax         = 65520
)

// companionFetcher downloads sources and external pack zips over HTTPS
// (Decision 8). The zero value is ready to use; the fields are read once, on
// the first fetch, and must not change afterwards.
type companionFetcher struct {
	timeout time.Duration                         // 0 selects 60 s; the total for one fetchSource or fetchPack call
	refuse  func(netip.Addr) bool                 // nil selects wsnetguard.RefuseLocal
	proxy   func(*http.Request) (*url.URL, error) // nil selects http.ProxyFromEnvironment
	tls     *tls.Config                           // nil selects system roots; tests inject the httptest pool

	once   sync.Once
	client *http.Client
}

// companionFetchedSource is one downloaded source zip, not yet opened.
type companionFetchedSource struct {
	data   []byte
	origin companionInstallOrigin
}

// companionFetchError is a fetch failure whose message is shown to the user
// as is. Unwrap keeps the cause for errors.Is and errors.As.
type companionFetchError struct {
	message string
	cause   error
}

func (e *companionFetchError) Error() string { return e.message }
func (e *companionFetchError) Unwrap() error { return e.cause }

func companionFetchFail(cause error, format string, args ...any) error {
	return &companionFetchError{message: fmt.Sprintf(format, args...), cause: cause}
}

// Causes a caller can test with errors.Is. A zip URL that answers with
// something other than a zip wraps errCompanionNotZip.
var (
	errCompanionTooLarge            = errors.New("the download exceeds its size limit")
	errCompanionNotGit              = errors.New("the URL is not a git repository readable over HTTPS")
	errCompanionNoArchive           = errors.New("no archive URL pattern answered with a zip")
	errCompanionPackSHA256          = errors.New("the pack zip does not match its sha256")
	errCompanionBadRefs             = errors.New("the ref advertisement is malformed")
	errCompanionRedirectScheme      = errors.New("the server redirected to a URL that is not https")
	errCompanionRedirectCount       = fmt.Errorf("the server redirected more than %d times", companionRedirectMax)
	errCompanionRedirectCredentials = errors.New("the server redirected to a URL that carries credentials")
)

// companionStatusError is a response whose status is not 200.
type companionStatusError struct{ code int }

func (e *companionStatusError) Error() string {
	return fmt.Sprintf("the server answered with status %d %s", e.code, http.StatusText(e.code))
}

// companionSource is a parsed source string.
type companionSource struct {
	raw  string // the trimmed source string as given
	kind string // git | zip
	url  string // zip: the URL to download; git: the repository URL without fragment or trailing slash
	site string // git: scheme://host of the repository
	path string // git: the escaped repository path without trailing slash, "" for the host root
	ref  string // git: the ref to resolve, HEAD when none is given
}

// parseCompanionSource classifies a source string. A URL whose path ends in
// .zip is a zip source and may carry a query (share links use one) but no
// fragment. Anything else is a git repository: the fragment names the ref and
// a query is refused.
func parseCompanionSource(raw string) (companionSource, error) {
	raw = strings.TrimSpace(raw)
	if err := companionHTTPSURL(raw); err != nil {
		return companionSource{}, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return companionSource{}, errors.New("URL must be an absolute https URL")
	}
	hasFragment := strings.Contains(raw, "#")
	if strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		if hasFragment {
			return companionSource{}, errors.New("a zip URL must not carry a #fragment")
		}
		return companionSource{raw: raw, kind: "zip", url: raw}, nil
	}
	if u.RawQuery != "" || u.ForceQuery {
		return companionSource{}, errors.New("a git repository URL must not carry a query; name a branch or tag after # instead")
	}
	ref := u.Fragment
	switch {
	case ref == "":
		ref = "HEAD"
	case len(ref) > companionRefMax:
		return companionSource{}, fmt.Errorf("the ref after # exceeds %d characters", companionRefMax)
	case !utf8.ValidString(ref):
		return companionSource{}, errors.New("the ref after # must be UTF-8")
	}
	for _, r := range ref {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return companionSource{}, errors.New("the ref after # must not contain spaces or control characters")
		}
	}
	site, repoPath := u.Scheme+"://"+u.Host, strings.TrimRight(u.EscapedPath(), "/")
	return companionSource{raw: raw, kind: "git", url: site + repoPath, site: site, path: repoPath, ref: ref}, nil
}

// fetchSource downloads the zip a source string names. A git source resolves
// its ref through the smart-HTTP ref advertisement and downloads the archive
// pinned to the resolved commit; a zip source is one download.
func (f *companionFetcher) fetchSource(ctx context.Context, raw string) (companionFetchedSource, error) {
	src, err := parseCompanionSource(raw)
	if err != nil {
		return companionFetchedSource{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, f.limit())
	defer cancel()
	if src.kind == "zip" {
		data, err := f.get(ctx, src.url, companionGet{limit: companionSourceCap, zip: true})
		switch {
		case errors.Is(err, errCompanionNotZip):
			return companionFetchedSource{}, companionFetchFail(err,
				"the URL did not return a zip archive; a share link may have returned a web page instead of the file, so download the file and import it locally")
		case err != nil:
			return companionFetchedSource{}, companionFetchStage("the zip could not be downloaded", err)
		}
		sum := sha256.Sum256(data)
		return companionFetchedSource{data: data, origin: companionInstallOrigin{
			Source: src.raw, Kind: "zip", ArchiveSHA256: hex.EncodeToString(sum[:])}}, nil
	}
	commit, err := f.resolveGitRef(ctx, src)
	if err != nil {
		return companionFetchedSource{}, err
	}
	data, err := f.gitArchive(ctx, src, commit)
	if err != nil {
		return companionFetchedSource{}, err
	}
	return companionFetchedSource{data: data, origin: companionInstallOrigin{
		Source: src.raw, Kind: "git", Ref: src.ref, Commit: commit}}, nil
}

// fetchPack downloads an external pack zip, checks its sha256 against the
// marketplace entry before the zip is opened, and opens it under the
// operation's decompression budget.
func (f *companionFetcher) fetchPack(ctx context.Context, entry companionMarketplaceEntry, inflate *companionInflate) (*companionArchive, error) {
	if err := companionHTTPSURL(entry.URL); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, f.limit())
	defer cancel()
	data, err := f.get(ctx, entry.URL, companionGet{limit: companionPackZipCap})
	if err != nil {
		return nil, companionFetchStage("the pack zip could not be downloaded", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != entry.SHA256 {
		return nil, companionFetchFail(errCompanionPackSHA256,
			"the downloaded pack zip does not match the sha256 the marketplace lists (downloaded %s)", got)
	}
	return openCompanionArchive(data, inflate)
}

// companionFetchStage prefixes a status failure with the step that failed;
// every other failure already carries its own message.
func companionFetchStage(stage string, err error) error {
	var status *companionStatusError
	if errors.As(err, &status) {
		return companionFetchFail(err, "%s (%s)", stage, status.Error())
	}
	return err
}

// resolveGitRef reads the repository's ref advertisement and resolves the
// source's ref to a commit.
func (f *companionFetcher) resolveGitRef(ctx context.Context, src companionSource) (string, error) {
	data, err := f.get(ctx, src.url+"/info/refs?service="+companionGitService, companionGet{
		limit: companionRefsCap,
		accept: func(resp *http.Response) error {
			media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if err != nil || media != companionGitAdvertType {
				return companionFetchFail(errCompanionNotGit,
					"%s (the server did not answer with a git ref advertisement)", errCompanionNotGit.Error())
			}
			return nil
		},
	})
	var status *companionStatusError
	if errors.As(err, &status) {
		return "", companionFetchFail(errors.Join(errCompanionNotGit, err), "%s (%s)", errCompanionNotGit.Error(), status.Error())
	}
	if err != nil {
		return "", err
	}
	refs, err := parseCompanionRefs(data)
	if err != nil {
		return "", err
	}
	return resolveCompanionRef(refs, src.ref)
}

// gitArchive downloads the archive of commit from the first of two common
// archive URL patterns that answers 200 with a zip. A status other than 200
// or a body that is not a zip moves on to the next pattern; any other failure
// stops.
func (f *companionFetcher) gitArchive(ctx context.Context, src companionSource, commit string) ([]byte, error) {
	repoPath := strings.TrimSuffix(src.path, ".git")
	name := repoPath[strings.LastIndexByte(repoPath, '/')+1:] // "" for a repository at the host root
	base := src.site + repoPath
	patterns := []string{base + "/archive/" + commit + ".zip"}
	if name != "" {
		patterns = append(patterns, base+"/-/archive/"+commit+"/"+name+"-"+commit+".zip")
	}
	for _, target := range patterns {
		data, err := f.get(ctx, target, companionGet{limit: companionSourceCap, zip: true})
		var status *companionStatusError
		if errors.As(err, &status) || errors.Is(err, errCompanionNotZip) {
			continue
		}
		return data, err
	}
	return nil, companionFetchFail(errCompanionNoArchive,
		"the repository's zip archive could not be downloaded from a known URL; add the repository's zip download URL as a zip source instead")
}

func companionHex40(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !companionIsHex(s[i]) {
			return false
		}
	}
	return true
}

func companionIsHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func companionBadRefs(format string, args ...any) error {
	return companionFetchFail(errCompanionBadRefs, "the repository's ref list is malformed: "+format, args...)
}

// parseCompanionRefs parses a protocol v0 smart-HTTP ref advertisement into
// ref name -> 40-hex object name. The service line and its flush come first;
// ref lines follow up to the final flush, with capabilities after a NUL on
// the first one. An empty repository yields an empty map.
func parseCompanionRefs(data []byte) (map[string]string, error) {
	var lines [][]byte // payloads; nil marks a flush
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, companionBadRefs("a line is truncated")
		}
		for _, c := range data[:4] {
			if !companionIsHex(c) {
				return nil, companionBadRefs("a line length is not hexadecimal")
			}
		}
		parsed, _ := strconv.ParseUint(string(data[:4]), 16, 32)
		size := int(parsed)
		switch {
		case size == 0:
			lines = append(lines, nil)
			data = data[4:]
			continue
		case size == 1 || size == 2:
			return nil, companionBadRefs("the server sent a newer protocol's delimiter")
		case size < 4 || size > companionPktMax:
			return nil, companionBadRefs("a line length is out of range")
		case size > len(data):
			return nil, companionBadRefs("a line is truncated")
		}
		lines = append(lines, data[4:size:size])
		data = data[size:]
	}
	if len(lines) < 2 || lines[0] == nil || strings.TrimSuffix(string(lines[0]), "\n") != "# service="+companionGitService {
		return nil, companionBadRefs("the service line is missing")
	}
	if lines[1] != nil {
		return nil, companionBadRefs("the service line is not followed by a flush")
	}
	lines = lines[2:]
	if len(lines) == 0 || lines[len(lines)-1] != nil {
		return nil, companionBadRefs("the list does not end with a flush")
	}
	lines = lines[:len(lines)-1]
	refs := make(map[string]string, len(lines))
	for i, line := range lines {
		if line == nil {
			return nil, companionBadRefs("a flush appears inside the list")
		}
		text := strings.TrimSuffix(string(line), "\n")
		if i == 0 {
			text, _, _ = strings.Cut(text, "\x00")
		} else if strings.ContainsRune(text, 0) {
			return nil, companionBadRefs("capabilities appear after the first line")
		}
		id, name, ok := strings.Cut(text, " ")
		if ok && len(id) == 64 && companionLowerHex(id) {
			return nil, companionFetchFail(errCompanionBadRefs, "repositories that use SHA-256 object names are not supported")
		}
		if !ok || len(id) != 40 || !companionLowerHex(id) || name == "" || strings.ContainsAny(name, " \n") {
			return nil, companionBadRefs("a ref line is not an object name and a ref name")
		}
		if i == 0 && name == "capabilities^{}" && id == strings.Repeat("0", 40) {
			continue // an empty repository's capabilities line
		}
		if _, seen := refs[name]; !seen {
			refs[name] = id
		}
	}
	return refs, nil
}

func companionLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// resolveCompanionRef resolves HEAD, a branch, or a tag (preferring the
// peeled commit of an annotated tag) to a commit. A 40-hex ref is used as
// given, lowercased.
func resolveCompanionRef(refs map[string]string, ref string) (string, error) {
	if companionHex40(ref) {
		return strings.ToLower(ref), nil
	}
	if len(refs) == 0 {
		return "", errors.New("the repository is empty")
	}
	if ref == "HEAD" {
		if id, ok := refs["HEAD"]; ok {
			return id, nil
		}
		return "", errors.New("the repository does not name a default branch; add a branch or tag after #")
	}
	if id, ok := refs["refs/heads/"+ref]; ok {
		return id, nil
	}
	if id, ok := refs["refs/tags/"+ref+"^{}"]; ok {
		return id, nil
	}
	if id, ok := refs["refs/tags/"+ref]; ok {
		return id, nil
	}
	return "", fmt.Errorf("the repository has no branch or tag named %q", ref)
}

func (f *companionFetcher) limit() time.Duration {
	if f.timeout > 0 {
		return f.timeout
	}
	return companionFetchTimeout
}

// companionGet describes one download.
type companionGet struct {
	limit  int64                      // response byte cap
	zip    bool                       // require the zip magic, checked on the first bytes before the rest is read
	accept func(*http.Response) error // checks a 200 response before its body is read
}

// get downloads target under ctx. A status other than 200 returns
// *companionStatusError without reading the body; a body that misses the zip
// magic returns errCompanionNotZip after reading only its first bytes.
func (f *companionFetcher) get(ctx context.Context, target string, opt companionGet) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("URL must be an absolute https URL")
	}
	req.Header.Set("User-Agent", companionFetchUserAgent)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, f.failure(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &companionStatusError{code: resp.StatusCode}
	}
	if opt.accept != nil {
		if err := opt.accept(resp); err != nil {
			return nil, err
		}
	}
	tooLarge := func() error {
		return companionFetchFail(errCompanionTooLarge, "the download exceeds the %s limit", companionByteSize(opt.limit))
	}
	if resp.ContentLength > opt.limit {
		return nil, tooLarge()
	}
	body := io.LimitReader(resp.Body, opt.limit+1)
	var buf bytes.Buffer
	if resp.ContentLength > 0 {
		buf.Grow(int(resp.ContentLength) + 1)
	}
	if opt.zip {
		if _, err := io.CopyN(&buf, body, 4); err != nil && err != io.EOF {
			return nil, f.failure(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, f.failure(ctx, err)
		}
		if !companionZipMagic(buf.Bytes()) {
			return nil, errCompanionNotZip
		}
	}
	if _, err := buf.ReadFrom(body); err != nil {
		return nil, f.failure(ctx, err)
	}
	// A deadline or cancellation that passed during the read voids the result
	// even when the body ended cleanly: when the client's connection closes as
	// the context ends, the server's terminating chunk can still reach this
	// read as a clean EOF, so a truncated body would look complete.
	if err := ctx.Err(); err != nil {
		return nil, f.failure(ctx, err)
	}
	if int64(buf.Len()) > opt.limit {
		return nil, tooLarge()
	}
	return buf.Bytes(), nil
}

// companionZipMagic reports whether data opens with a local file header or,
// for an empty archive, the end-of-central-directory record, as
// openCompanionArchive requires.
func companionZipMagic(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06"))
}

func companionByteSize(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// failure turns a transport or body-read error into a message for the user.
func (f *companionFetcher) failure(ctx context.Context, err error) error {
	var refused *wsnetguard.RefusedError
	for _, known := range []error{errCompanionRedirectScheme, errCompanionRedirectCount, errCompanionRedirectCredentials} {
		if errors.Is(err, known) {
			return companionFetchFail(err, "%s", known.Error())
		}
	}
	switch {
	case errors.As(err, &refused):
		return companionFetchFail(err,
			"the download was refused because the server address %s is a loopback, link-local, unspecified, or multicast address", refused.Addr)
	case errors.Is(ctx.Err(), context.Canceled):
		return companionFetchFail(err, "the download was canceled")
	case ctx.Err() != nil:
		return companionFetchFail(err, "the download did not finish within %s", companionDuration(f.limit()))
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return companionFetchFail(err, "the download failed: %v", urlErr.Err)
	}
	return companionFetchFail(err, "the download failed: %v", err)
}

func companionDuration(d time.Duration) string {
	if d >= time.Second && d%time.Second == 0 {
		return fmt.Sprintf("%d seconds", d/time.Second)
	}
	return d.String()
}

// companionCheckRedirect follows at most companionRedirectMax redirects, only
// to https URLs without credentials, and drops the Referer net/http sets
// before calling it.
func companionCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > companionRedirectMax {
		return errCompanionRedirectCount
	}
	if req.URL.Scheme != "https" {
		return errCompanionRedirectScheme
	}
	if req.URL.User != nil {
		return errCompanionRedirectCredentials
	}
	req.Header.Del("Referer")
	return nil
}

// httpClient builds the fetcher's client on first use. It has no cookie jar
// and no timeout of its own: each call bounds itself through its context.
func (f *companionFetcher) httpClient() *http.Client {
	f.once.Do(func() {
		refuse := f.refuse
		if refuse == nil {
			refuse = wsnetguard.RefuseLocal
		}
		proxy := f.proxy
		if proxy == nil {
			proxy = http.ProxyFromEnvironment
		}
		direct := companionTransport(f.tls, wsnetguard.Control(refuse))
		proxied := companionTransport(f.tls, nil)
		proxied.Proxy = func(req *http.Request) (*url.URL, error) {
			if u, ok := req.Context().Value(companionProxyKey{}).(*url.URL); ok && u != nil {
				return u, nil
			}
			// Never fall through to an unguarded direct dial.
			return nil, errors.New("no proxy was selected for this request")
		}
		f.client = &http.Client{
			Transport:     &companionRoundTripper{proxy: proxy, direct: direct, proxied: proxied},
			CheckRedirect: companionCheckRedirect,
		}
	})
	return f.client
}

// companionTransport builds a transport with http.DefaultTransport's
// timeouts, written out rather than cloned so a hook another package sets on
// the shared default (a TLS dial function, for one) cannot bypass the dial
// guard. control is the dialer's Control hook; nil leaves the dial unguarded.
func companionTransport(base *tls.Config, control func(network, address string, c syscall.RawConn) error) *http.Transport {
	config := &tls.Config{}
	if base != nil {
		config = base.Clone()
	}
	if config.MinVersion < tls.VersionTLS12 {
		config.MinVersion = tls.VersionTLS12
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: control}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSClientConfig:       config,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

type companionProxyKey struct{}

// companionRoundTripper sends each request, redirect hops included, through
// the proxy the proxy function names, or else directly through the guarded
// transport. The proxy makes the connection itself, so the address guard
// does not apply to a proxied request.
type companionRoundTripper struct {
	proxy   func(*http.Request) (*url.URL, error)
	direct  *http.Transport
	proxied *http.Transport
}

func (t *companionRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := t.proxy(req)
	if err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, fmt.Errorf("the proxy setting could not be read: %w", err)
	}
	if u == nil {
		return t.direct.RoundTrip(req)
	}
	return t.proxied.RoundTrip(req.WithContext(context.WithValue(req.Context(), companionProxyKey{}, u)))
}
