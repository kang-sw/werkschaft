package wsdashboard

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// topicHistoryMax bounds the topics one history fetch may name.
const topicHistoryMax = 512

// topicTarget is a page target the history fetch locates: an entry, a
// panel, or an item.
var topicTarget = regexp.MustCompile(`^(?:[pe][1-9][0-9]{0,9}|item-[1-9][0-9]{0,9})$`)

// handleTopicHistory serves /s/<reply-id>/topic?name=<topic>[&name=...] or
// ?target=<eN|pN|item-N>: the named topics, or the one topic holding the
// target, rendered with the complete history the served board leaves out
// (TopicHistorySource), as {"topics":[{"name","html"}]} with each body
// frame's theme pinned like the board's. The page fetches it when the
// viewer expands lazy history. It is read-only and serves what the session
// page shows, so it takes the page's own access rule. The answer carries a
// weak ETag over its exact bytes and must be revalidated (private,
// no-cache): a matching If-None-Match answers 304 with no body. A large
// answer is gzip-compressed for a client that accepts it.
func (d *Daemon) handleTopicHistory(w http.ResponseWriter, r *http.Request, replyID string) {
	query := r.URL.Query()
	names, target := query["name"], query.Get("target")
	if (len(names) == 0) == (target == "") || len(names) > topicHistoryMax || target != "" && !topicTarget.MatchString(target) {
		http.Error(w, "name or target required", http.StatusBadRequest)
		return
	}
	src, has := d.source.(TopicHistorySource)
	var s Session
	ok := has && d.index != nil && ValidReplyID(replyID)
	if ok {
		s, ok = d.index.Session(replyID)
	}
	type fragment struct {
		Name string `json:"name"`
		HTML string `json:"html"`
	}
	out := struct {
		Topics []fragment `json:"topics"`
	}{Topics: []fragment{}}
	if ok {
		lang, _ := requestLang(r)
		topics, read := src.ReadTopicHistory(s.Registration, lang, names, target)
		ok = read
		theme := requestTheme(r)
		for _, t := range topics {
			out.Topics = append(out.Topics, fragment{Name: t.Name, HTML: string(wswhiteboard.ThemeFrames(t.HTML, theme))})
		}
	}
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	raw, _ := json.Marshal(out)
	sum := sha256.Sum256(raw)
	etag := `W/"` + hex.EncodeToString(sum[:16]) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Vary", "Accept-Encoding")
	h.Set("X-Content-Type-Options", "nosniff")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	if len(raw) < topicHistoryGzipMin || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		h.Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw)
		return
	}
	h.Set("Content-Encoding", "gzip")
	zw := topicHistoryGzip.Get().(*gzip.Writer)
	zw.Reset(w)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	zw.Reset(nil)
	topicHistoryGzip.Put(zw)
}

// topicHistoryGzipMin is the smallest answer worth compressing: below it
// the gzip framing costs about what it saves.
const topicHistoryGzipMin = 1024

// topicHistoryGzip pools fastest-level writers: history HTML compresses
// well even at level 1, and the answer is on the interactive path.
var topicHistoryGzip = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
	return zw
}}

// acceptsGzip reports whether an Accept-Encoding header accepts gzip: a
// gzip or x-gzip coding with a non-zero q, or else a * coding with one.
func acceptsGzip(header string) bool {
	explicit, star := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		switch coding {
		case "gzip", "x-gzip":
			explicit = max(explicit, q)
		case "*":
			star = max(star, q)
		}
	}
	if explicit >= 0 {
		return explicit > 0
	}
	return star > 0
}
