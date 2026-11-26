package wsdashboard

import (
	"bytes"
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

var (
	mockupEntryIDPattern = regexp.MustCompile(`^e[1-9][0-9]*$`)
	mockupSHA256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// BoardMockupSrc returns the cookie-authenticated redirect route URL for a
// stored mockup snapshot panel. Rendered HTML carries this stable path rather
// than an expiring capability URL.
func BoardMockupSrc(replyID, entryID string) string {
	if !ValidReplyID(replyID) || !mockupEntryIDPattern.MatchString(entryID) {
		return ""
	}
	return "/s/" + replyID + "/board/mockup?entry=" + url.QueryEscape(entryID)
}

type mockupCacheKey struct {
	boardDir string
	instance uint64
	entryID  string
	sha256   string
}

type decodedMockup struct {
	files map[string][]byte
	size  int64
}

type mockupLRUEntry struct {
	key mockupCacheKey
	val *decodedMockup
}

// mockupLRU is a bounded in-memory LRU cache of decoded mockup snapshot
// bundles keyed by (boardDir, boardInstance, entryID, sha256).
type mockupLRU struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	ll       *list.List
	items    map[mockupCacheKey]*list.Element
}

func newMockupLRU(maxBytes int64) *mockupLRU {
	if maxBytes <= 0 {
		maxBytes = wswhiteboard.MockupLRUMaxBytes
	}
	return &mockupLRU{
		maxBytes: maxBytes,
		ll:       list.New(),
		items:    map[mockupCacheKey]*list.Element{},
	}
}

func (c *mockupLRU) Get(key mockupCacheKey) (*decodedMockup, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*mockupLRUEntry).val, true
	}
	return nil, false
}

func (c *mockupLRU) Put(key mockupCacheKey, val *decodedMockup) {
	if val == nil || val.size < 0 || val.size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		entry := el.Value.(*mockupLRUEntry)
		c.used -= entry.val.size
		entry.val = val
		c.used += val.size
		c.ll.MoveToFront(el)
	} else {
		el := c.ll.PushFront(&mockupLRUEntry{key: key, val: val})
		c.items[key] = el
		c.used += val.size
	}
	for c.used > c.maxBytes && c.ll.Len() > 0 {
		back := c.ll.Back()
		entry := back.Value.(*mockupLRUEntry)
		c.ll.Remove(back)
		delete(c.items, entry.key)
		c.used -= entry.val.size
	}
}

func (c *mockupLRU) EvictBoard(boardDir string) {
	if boardDir == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		entry := el.Value.(*mockupLRUEntry)
		if entry.key.boardDir == boardDir {
			c.ll.Remove(el)
			delete(c.items, entry.key)
			c.used -= entry.val.size
		}
		el = next
	}
}

func (d *Daemon) getMockupLRU() *mockupLRU {
	d.mockupMu.Do(func() {
		if d.mockupLRU == nil {
			d.mockupLRU = newMockupLRU(wswhiteboard.MockupLRUMaxBytes)
		}
	})
	return d.mockupLRU
}

func fsMockMAC(key []byte, part, replyID, entryID, sha256Hex string, expiry int64) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(part))
	m.Write([]byte{0})
	m.Write([]byte(replyID))
	m.Write([]byte{0})
	m.Write([]byte(entryID))
	m.Write([]byte{0})
	m.Write([]byte(sha256Hex))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func (d *Daemon) mintMockCapability(replyID, entryID, sha256Hex string) string {
	key := d.fsCapKey()
	if key == nil {
		return ""
	}
	expiry := d.fsCapNow().Add(fsCapabilityTTL).Unix()
	return strconv.FormatInt(expiry, 36) + "-" + entryID + "-" + sha256Hex + "-" + fsMockMAC(key, fsMockRoutePart, replyID, entryID, sha256Hex, expiry)
}

func (d *Daemon) parseMockCapability(capability, replyID string) (entryID, sha256Hex string, ok bool) {
	key := d.fsCapKey()
	if key == nil {
		return "", "", false
	}
	parts := strings.Split(capability, "-")
	if len(parts) != 4 {
		return "", "", false
	}
	exp, entryID, sha256Hex, mac := parts[0], parts[1], parts[2], parts[3]
	if len(exp) > 13 || !mockupEntryIDPattern.MatchString(entryID) || !mockupSHA256Pattern.MatchString(sha256Hex) {
		return "", "", false
	}
	expiry, err := strconv.ParseInt(exp, 36, 64)
	if err != nil || strconv.FormatInt(expiry, 36) != exp || expiry <= d.fsCapNow().Unix() {
		return "", "", false
	}
	if subtle.ConstantTimeCompare([]byte(mac), []byte(fsMockMAC(key, fsMockRoutePart, replyID, entryID, sha256Hex, expiry))) != 1 {
		return "", "", false
	}
	return entryID, sha256Hex, true
}

// fsMockCSP is the capability policy for /fs/mock/: identical to
// fsCapabilityCSP except connect-src allows same-bundle fetches under prefix
// so interactive multi-file HTML mockups can load sibling JSON/assets.
func fsMockCSP(prefix string) string {
	return "sandbox allow-scripts; default-src " + prefix +
		"; script-src " + prefix + " 'unsafe-inline'" +
		"; style-src " + prefix + " 'unsafe-inline'" +
		"; img-src " + prefix + " data: blob:" +
		"; media-src " + prefix + " data: blob:" +
		"; font-src " + prefix + " data:" +
		"; connect-src " + prefix +
		"; object-src 'none'; base-uri 'none'; form-action 'none'"
}

// handleBoardMockup serves GET /s/<reply-id>/board/mockup?entry=<entry-id>:
// behind the normal session access guard, it mints a fresh bundle-scoped
// capability and redirects (302, no-store) to /s/<reply-id>/fs/mock/<cap>/<root>.
func (d *Daemon) handleBoardMockup(w http.ResponseWriter, r *http.Request, replyID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if crossSiteFetch(r) || d.index == nil || !ValidReplyID(replyID) {
		fsNotFound(w)
		return
	}
	entryID := r.URL.Query().Get("entry")
	if !mockupEntryIDPattern.MatchString(entryID) {
		fsNotFound(w)
		return
	}
	s, ok := d.index.Session(replyID)
	if !ok {
		fsNotFound(w)
		return
	}
	src, ok := d.source.(BoardMockupSource)
	if !ok {
		fsNotFound(w)
		return
	}
	meta, found, err := src.LookupMockup(s.Registration, entryID)
	if err != nil {
		http.Error(w, "mockup unavailable", http.StatusInternalServerError)
		return
	}
	if !found || !wswhiteboard.ValidMockupRel(meta.RootFile) || !mockupSHA256Pattern.MatchString(meta.SHA256) {
		fsNotFound(w)
		return
	}
	capToken := d.mintMockCapability(replyID, entryID, meta.SHA256)
	if capToken == "" {
		http.Error(w, "mockup unavailable", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, d.capabilityURL(fsMockRoutePart, capToken, replyID, meta.RootFile), http.StatusFound)
}

// handleFSMockup serves GET /s/<reply-id>/fs/mock/<cap>/<rel>: one member of a
// stored immutable mockup snapshot bundle from the daemon's bounded LRU cache.
func (d *Daemon) handleFSMockup(w http.ResponseWriter, r *http.Request, replyID, rest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fsNotFound(w)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" && !sameOrigin(r) {
		fsNotFound(w)
		return
	}
	capability, rel, ok := strings.Cut(rest, "/")
	if !ok || rel == "" || d.index == nil || !ValidReplyID(replyID) || !capHost.MatchString(r.Host) {
		fsNotFound(w)
		return
	}
	entryID, capSHA, ok := d.parseMockCapability(capability, replyID)
	if !ok || !wswhiteboard.ValidMockupRel(rel) {
		fsNotFound(w)
		return
	}
	ctype, allowed := wswhiteboard.MockupContentType(rel)
	if !allowed {
		fsNotFound(w)
		return
	}
	s, found := d.index.Session(replyID)
	if !found {
		fsNotFound(w)
		return
	}
	src, has := d.source.(BoardMockupSource)
	if !has {
		fsNotFound(w)
		return
	}
	meta, found, err := src.LookupMockup(s.Registration, entryID)
	if err != nil || !found || meta.SHA256 != capSHA {
		fsNotFound(w)
		return
	}
	key := mockupCacheKey{
		boardDir: meta.BoardDir,
		instance: meta.Instance,
		entryID:  entryID,
		sha256:   capSHA,
	}
	decoded, hit := d.getMockupLRU().Get(key)
	if !hit {
		bundle, found, err := src.ReadMockupBundle(s.Registration, entryID)
		if err != nil || !found || bundle.SHA256 != capSHA {
			fsNotFound(w)
			return
		}
		files, totalBytes, err := wswhiteboard.DecodeMockupZip(bundle.Zip)
		if err != nil {
			fsNotFound(w)
			return
		}
		decoded = &decodedMockup{files: files, size: totalBytes}
		d.getMockupLRU().Put(key, decoded)
	}
	data, exists := decoded.files[rel]
	if !exists {
		fsNotFound(w)
		return
	}
	prefix := "http://" + r.Host + "/s/" + replyID + fsMockRoutePart + capability + "/"
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", fsMockCSP(prefix))
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Vary", "Origin")
	if r.Header.Get("Origin") == "null" {
		h.Set("Access-Control-Allow-Origin", "null")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
