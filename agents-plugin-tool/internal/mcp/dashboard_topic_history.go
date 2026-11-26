package mcp

import (
	"fmt"
	"html/template"
	"io/fs"
	"maps"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsdoc"
	"github.com/kang-sw/devenv/internal/wsstate"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// The served board's lazy history (ReadTopicHistory) answers from memory
// while nothing it shows changed. Each session and language keeps one view
// record: the board it was built from (identity and content revision), the
// ticket statuses its build resolved, the topic names it held, and every
// topic fragment rendered from it with that fragment's complete revision
// and the image versions it embedded.
//
//   - Fast path: the board is unchanged, every recorded ticket status and
//     image version still reads the same, and each named topic was rendered
//     or revalidated under this record: the stored fragments answer. No view
//     is built, nothing renders, and the only file reads are the ticket
//     directory listings and image stats, each shared for historyValidation.
//   - Slow path: the view is built again (with the same shared ticket and
//     image reads), and each named topic whose revision and image versions
//     still match its stored fragment reuses it; only changed topics render.
//
// Invalidation: any board write moves the board key; a ticket move or an
// image edit is seen within historyValidation (at once after a session
// nudge, invalidateSession); ForgetSession drops the session's records; the
// memo holds at most topicHistoryMaxViews records and topicHistoryMaxBytes
// of fragment HTML, evicting the least recently used session first. A
// stored record is never mutated after it is published, so readers use it
// outside the lock.

var _ wsdashboard.TopicHistorySource = (*DashboardSessionSource)(nil)

// historyValidation is how long one ticket listing or image stat serves
// history reads, matching the board watch stamps' window
// (wsdashboard.StampWatched).
const historyValidation = time.Second

const (
	topicHistoryMaxViews = 64
	topicHistoryMaxBytes = 48 << 20
	ticketDirRootsMax    = 64
	imageStatMax         = 4096
)

// ReadTopicHistory renders the named topics, or the one topic holding
// target, with their complete history (wswhiteboard.RenderTopicFragments)
// under the served page's link rule: what the served board's lazy history
// leaves out, fetched when the viewer expands it. false when the session is
// gone or has no board.
func (src *DashboardSessionSource) ReadTopicHistory(reg wsdashboard.Registration, lang string, names []string, target string) ([]wswhiteboard.TopicFragment, bool) {
	key := reg.ReplyID + "\x00" + lang
	tickets := func(root string) func(string) (string, bool) {
		return func(stem string) (string, bool) { return src.ticketDirs.status(root, stem) }
	}
	// With dev assets on, the templates change between renders.
	memo := !wswhiteboard.DevAssetsOn()
	// The fast path keys the board without reading the record's parts or
	// cloning the board (Snapshot marshals the whole board twice).
	if memo && target == "" {
		if root, board, ok := src.peekHistoryBoard(reg); ok {
			if frags, hit := src.history.lookup(key, board, names, tickets(root), src.historyImageSrc(root, reg.ReplyID)); hit {
				return frags, true
			}
		}
	}
	_, _, record, ok, err := src.lookupRecord(reg)
	if !ok || err != nil || record.Whiteboard == nil {
		return nil, false
	}
	if target != "" {
		name, found := record.Whiteboard.TargetTopic(target)
		if !found {
			return nil, true
		}
		names = []string{name}
	}
	board := topicHistoryBoard(record.Root, record.boardInstance, record.boardRevision)
	imageSrc := src.historyImageSrc(record.Root, reg.ReplyID)
	if memo && target != "" {
		if frags, hit := src.history.lookup(key, board, names, tickets(record.Root), imageSrc); hit {
			return frags, true
		}
	}
	resolver := &recordedTickets{status: tickets(record.Root), seen: map[string]ticketDep{}}
	in := servedViewInput(record, reg.ReplyID)
	in.Tickets = resolver
	var used *[]imageDep
	in.ImageSrc = func(path string) string {
		s := imageSrc(path)
		if used != nil {
			*used = append(*used, imageDep{path: path, src: s})
		}
		return s
	}
	view := wswhiteboard.BuildView(record.Whiteboard, in)
	view.Lang = lang
	prev := src.history.built(key)
	rendered := map[string]topicHistoryEntry{}
	var reuse wswhiteboard.FragmentReuse
	if memo {
		reuse = func(name, revision string, render func() (template.HTML, error)) (template.HTML, error) {
			if prev != nil {
				if old, found := prev.topics[name]; found && old.revision == revision && imagesCurrent(old.images, imageSrc) {
					rendered[name] = old
					return old.html, nil
				}
			}
			var deps []imageDep
			used = &deps
			html, err := render()
			used = nil
			src.history.rendered(name)
			if err == nil {
				rendered[name] = topicHistoryEntry{revision: revision, html: html, images: deps}
			}
			return html, err
		}
	}
	topics, err := wswhiteboard.RenderTopicFragmentsReuse(view, names, reuse)
	if err != nil {
		appendDebugEvent("dashboard.page_render_failed", map[string]any{"error": err.Error()})
		return nil, false
	}
	if memo {
		known := make(map[string]bool, len(view.Topics)+len(view.DeferredTopics))
		for _, t := range view.Topics {
			known[t.Name] = true
		}
		for _, t := range view.DeferredTopics {
			known[t.Name] = true
		}
		src.history.store(key, board, resolver.seen, known, rendered)
	}
	return topics, true
}

// topicHistoryBoard keys the board a history render reads: its root, the
// open board instance (wswhiteboard.BoardDB.Instance: a reopened or
// recreated board gets a new one), and its content revision, which every
// board commit moves.
func topicHistoryBoard(root string, instance, revision uint64) string {
	return fmt.Sprintf("%s\x00%d\x00%d", root, instance, revision)
}

// peekHistoryBoard resolves reg's session as lookupRecord does (a known
// reply-id whose key record is a top-level lead rooted at reg.Project) and
// keys its held board (topicHistoryBoard), renewing the board's idle lease,
// without reading the record's other parts or cloning the board. false
// sends the caller to lookupRecord, which also rescans keys, imports a
// legacy board, and reports a missing session.
func (src *DashboardSessionSource) peekHistoryBoard(reg wsdashboard.Registration) (root, board string, ok bool) {
	if src.store.boards == nil {
		return "", "", false
	}
	d, err := src.store.dirs()
	if err != nil {
		return "", "", false
	}
	keys, err := src.keysFor(d, reg.ReplyID, false)
	if err != nil || len(keys) != 1 {
		return "", "", false
	}
	identity, _, err := loadRecordWith(d, keys[0], partKey, nil, src.readRecordFile)
	if err != nil || identity.Root != reg.Project || identity.Scope != string(roleLead) || identity.Parent != "" {
		return "", "", false
	}
	var instance, revision uint64
	if !src.store.boards.peekHeld(identity.Root, keys[0], func(b *wswhiteboard.BoardDB) {
		instance, revision = b.Instance(), b.Revision()
	}) {
		return "", "", false
	}
	return identity.Root, topicHistoryBoard(identity.Root, instance, revision), true
}

// historyImageSrc is servedViewInput's ImageSrc with the file's version read
// through the source's image stat cache.
func (src *DashboardSessionSource) historyImageSrc(root, replyID string) func(string) string {
	return func(path string) string {
		s := wsdashboard.ImageSrc(root, replyID, path)
		if s == "" {
			return ""
		}
		if v, ok := src.images.version(docImagePath(root, path)); ok {
			s += "&v=" + v
		}
		return s
	}
}

type topicHistoryMemo struct {
	mu    sync.Mutex
	views map[string]*topicHistoryView
	tick  uint64
	// builds counts view builds and hits fast-path answers (test probes);
	// renders, when not nil, counts fragment renders by topic.
	builds, hits int
	renders      map[string]int
}

// topicHistoryView is immutable once published, apart from used, which only
// the memo's lock guards.
type topicHistoryView struct {
	board   string
	gen     uint64
	tickets map[string]ticketDep
	known   map[string]bool
	topics  map[string]topicHistoryEntry
	bytes   int
	used    uint64
}

type topicHistoryEntry struct {
	// gen is the record generation the fragment was rendered or
	// revalidated under; the fast path uses only the current one.
	gen      uint64
	revision string
	html     template.HTML
	images   []imageDep
}

type imageDep struct{ path, src string }

type ticketDep struct {
	status string
	ok     bool
}

// lookup answers names from the record for key when the board, the
// recorded ticket statuses, and each named fragment's image versions are
// unchanged; false sends the caller to the slow path.
func (m *topicHistoryMemo) lookup(key, board string, names []string, tickets func(string) (string, bool), imageSrc func(string) string) ([]wswhiteboard.TopicFragment, bool) {
	m.mu.Lock()
	v := m.views[key]
	if v != nil {
		m.tick++
		v.used = m.tick
	}
	m.mu.Unlock()
	if v == nil || v.board != board {
		return nil, false
	}
	var out []wswhiteboard.TopicFragment
	var entries []topicHistoryEntry
	seen := map[string]bool{}
	for _, name := range names {
		// RenderTopicFragments skips repeated and unknown names.
		if seen[name] || !v.known[name] {
			continue
		}
		seen[name] = true
		e, ok := v.topics[name]
		if !ok || e.gen != v.gen {
			return nil, false
		}
		entries = append(entries, e)
		out = append(out, wswhiteboard.TopicFragment{Name: name, HTML: e.html})
	}
	for stem, dep := range v.tickets {
		if status, ok := tickets(stem); status != dep.status || ok != dep.ok {
			return nil, false
		}
	}
	for _, e := range entries {
		if !imagesCurrent(e.images, imageSrc) {
			return nil, false
		}
	}
	m.mu.Lock()
	m.hits++
	m.mu.Unlock()
	return out, true
}

// built returns the current record for key, if any, and counts a build.
func (m *topicHistoryMemo) built(key string) *topicHistoryView {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builds++
	return m.views[key]
}

func (m *topicHistoryMemo) rendered(name string) {
	m.mu.Lock()
	if m.renders != nil {
		m.renders[name]++
	}
	m.mu.Unlock()
}

// store publishes a new record for key: the previous record's fragments
// carry over for revision reuse, and the ones this read rendered or
// revalidated join it under the record's generation, which moves when the
// board or the resolved ticket statuses differ from the previous record's.
func (m *topicHistoryMemo) store(key, board string, tickets map[string]ticketDep, known map[string]bool, rendered map[string]topicHistoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.views == nil {
		m.views = map[string]*topicHistoryView{}
	}
	old := m.views[key]
	next := &topicHistoryView{board: board, gen: 1, tickets: tickets, known: known, topics: map[string]topicHistoryEntry{}}
	if old != nil {
		next.gen = old.gen
		if old.board != board || !maps.Equal(old.tickets, tickets) {
			next.gen++
		}
		maps.Copy(next.topics, old.topics)
	}
	for name, e := range rendered {
		e.gen = next.gen
		next.topics[name] = e
	}
	next.measure()
	m.tick++
	next.used = m.tick
	m.views[key] = next
	m.bound(key)
}

func (v *topicHistoryView) measure() {
	v.bytes = 0
	for _, e := range v.topics {
		v.bytes += len(e.html)
	}
}

// bound evicts least recently used records other than key past the record
// and byte caps; a record over the byte cap alone keeps only its current
// generation's fragments.
func (m *topicHistoryMemo) bound(key string) {
	total := 0
	for _, v := range m.views {
		total += v.bytes
	}
	for total > topicHistoryMaxBytes || len(m.views) > topicHistoryMaxViews {
		victim, oldest := "", ^uint64(0)
		for k, v := range m.views {
			if k != key && v.used < oldest {
				victim, oldest = k, v.used
			}
		}
		if victim == "" {
			break
		}
		total -= m.views[victim].bytes
		delete(m.views, victim)
	}
	if v := m.views[key]; v != nil && v.bytes > topicHistoryMaxBytes {
		trimmed := *v
		trimmed.topics = map[string]topicHistoryEntry{}
		for name, e := range v.topics {
			if e.gen == v.gen {
				trimmed.topics[name] = e
			}
		}
		trimmed.measure()
		m.views[key] = &trimmed
	}
}

// forget drops the records of session replyID, in every language.
func (m *topicHistoryMemo) forget(replyID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.views {
		if strings.HasPrefix(k, replyID+"\x00") {
			delete(m.views, k)
		}
	}
}

func imagesCurrent(deps []imageDep, imageSrc func(string) string) bool {
	for _, d := range deps {
		if imageSrc(d.path) != d.src {
			return false
		}
	}
	return true
}

// recordedTickets resolves ticket statuses for one view build through the
// shared listing cache, recording each answer: the record validates them,
// and one build sees one answer per stem.
type recordedTickets struct {
	status func(string) (string, bool)
	seen   map[string]ticketDep
}

func (r *recordedTickets) TicketStatus(stem string) (string, bool) {
	if dep, ok := r.seen[stem]; ok {
		return dep.status, dep.ok
	}
	status, ok := r.status(stem)
	r.seen[stem] = ticketDep{status: status, ok: ok}
	return status, ok
}

// ticketDirCache answers wsdoc.TicketStatus from one listing of each ticket
// status directory per root, reused for historyValidation: a board with N
// linked stems costs five directory listings, not up to 5N stats.
type ticketDirCache struct {
	mu    sync.Mutex
	roots map[string]ticketListing
	// lists counts listing passes (a test probe).
	lists int
}

type ticketListing struct {
	at     time.Time
	status map[string]string
}

// status is wsdoc.TicketStatus over the cached listing: the first status
// directory, in wsdoc.TicketStatusDirs order, holding a non-directory
// <stem>.md.
func (c *ticketDirCache) status(root, stem string) (string, bool) {
	if !wsdoc.ValidTicketStem(stem) {
		return "", false
	}
	c.mu.Lock()
	l, ok := c.roots[root]
	c.mu.Unlock()
	if !ok || time.Since(l.at) >= historyValidation {
		// Taken before the listing, so a move during it is looked at again.
		l = ticketListing{at: time.Now(), status: listTicketStatuses(root)}
		c.mu.Lock()
		c.lists++
		if _, had := c.roots[root]; c.roots == nil || !had && len(c.roots) >= ticketDirRootsMax {
			c.roots = map[string]ticketListing{}
		}
		c.roots[root] = l
		c.mu.Unlock()
	}
	status, found := l.status[ticketNameKey(stem)]
	return status, found
}

func (c *ticketDirCache) invalidate(root string) {
	c.mu.Lock()
	delete(c.roots, root)
	c.mu.Unlock()
}

// ticketNameKey folds case where the filesystem's stat does, so the
// listing resolves a stem as wsdoc.TicketStatus's stat would.
func ticketNameKey(name string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(name)
	}
	return name
}

func listTicketStatuses(root string) map[string]string {
	out := map[string]string{}
	for _, dir := range wsdoc.TicketStatusDirs {
		path := filepath.Join(root, filepath.FromSlash(wsdoc.TicketsDir), dir)
		entries, err := wsstate.ReadDir(path)
		if err != nil {
			continue
		}
		status := strings.TrimPrefix(dir, ".")
		for _, e := range entries {
			stem, md := strings.CutSuffix(ticketNameKey(e.Name()), ".md")
			if !md || stem == "" {
				continue
			}
			if _, taken := out[stem]; taken {
				continue
			}
			if e.Type()&fs.ModeSymlink != 0 {
				// TicketStatus's stat follows the link.
				if info, err := wsstate.Stat(filepath.Join(path, e.Name())); err != nil || info.IsDir() {
					continue
				}
			} else if e.IsDir() {
				continue
			}
			out[stem] = status
		}
	}
	return out
}

// imageStatCache serves an image's version (wsdashboard.ImageVersion) to
// history reads, one stat per path per historyValidation.
type imageStatCache struct {
	mu      sync.Mutex
	entries map[string]imageStat
	// stats counts the stats taken (a test probe).
	stats int
}

type imageStat struct {
	at      time.Time
	version string
	ok      bool
}

func (c *imageStatCache) version(path string) (string, bool) {
	c.mu.Lock()
	e, found := c.entries[path]
	c.mu.Unlock()
	if found && time.Since(e.at) < historyValidation {
		return e.version, e.ok
	}
	e = imageStat{at: time.Now()}
	if info, err := wsstate.Stat(path); err == nil {
		e.version, e.ok = wsdashboard.ImageVersion(info), true
	}
	c.mu.Lock()
	c.stats++
	if _, had := c.entries[path]; c.entries == nil || !had && len(c.entries) >= imageStatMax {
		c.entries = map[string]imageStat{}
	}
	c.entries[path] = e
	c.mu.Unlock()
	return e.version, e.ok
}

// invalidateUnder drops the versions of the files under root.
func (c *imageStatCache) invalidateUnder(root string) {
	if root == "" {
		return
	}
	prefix := filepath.Clean(root) + string(filepath.Separator)
	c.mu.Lock()
	defer c.mu.Unlock()
	for path := range c.entries {
		if strings.HasPrefix(path, prefix) {
			delete(c.entries, path)
		}
	}
}
