// Package wsusercmd holds the user commands ("command slots"): bash lines an
// open hosted lead registers (session-scoped, source "agent") or a user saves
// per project worktree root (root-scoped, source "user"). The registry serves
// every read from memory, and the SQLite store (storage type 2) is written once
// per changed command and read once at start.
package wsusercmd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The registration limits and scope constants.
const (
	// MaxPerScope is the most commands kept in one scope (per session+worktree
	// for agent commands, and per worktree root for user commands).
	MaxPerScope = 32
	// MaxLabel and MaxDescription bound texts in characters; MaxLine, MaxPrompt
	// and MaxCwd bound their fields in bytes.
	MaxLabel       = 64
	MaxDescription = 256
	MaxLine        = 4 << 10
	MaxPrompt      = 4 << 10
	MaxCwd         = 512

	// SourceAgent identifies session-scoped commands registered by the agent.
	SourceAgent = "agent"
	// SourceUser identifies project-root-persistent commands saved by the user.
	SourceUser = "user"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidID reports whether id is a well-formed command ID (it is also a URL
// path segment of the dashboard's routes).
func ValidID(id string) bool { return idPattern.MatchString(id) }

// NormalizeSource defaults an empty source to SourceAgent and validates it.
func NormalizeSource(source string) (string, error) {
	switch strings.TrimSpace(source) {
	case "", SourceAgent:
		return SourceAgent, nil
	case SourceUser:
		return SourceUser, nil
	default:
		return "", fmt.Errorf("%w: source must be %q or %q", ErrInvalid, SourceAgent, SourceUser)
	}
}

// Command is one registered user command. Revision identifies the exact
// definition shown to the user: it advances with every change, and never
// repeats for a command ID, even across a withdrawal and a restart.
type Command struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Line        string `json:"line"`
	Prompt      string `json:"prompt,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Source      string `json:"source"`
	Revision    int64  `json:"revision"`
}

// Input is a registration or creation payload.
type Input struct {
	ID, Label, Description, Line, Prompt, Cwd, Source string
}

// Patch updates selected template fields atomically under the writer lock.
// Nil fields keep the existing command's value.
type Patch struct {
	Label       *string
	Description *string
	Line        *string
	Prompt      *string
	Cwd         *string
}

// Outcome says what a Set did.
type Outcome int

const (
	// Registered: a new command.
	Registered Outcome = iota
	// Replaced: an existing command's definition changed; its revision advanced.
	Replaced
	// Unchanged: the definition was already stored; nothing was committed.
	Unchanged
)

func (o Outcome) String() string {
	switch o {
	case Replaced:
		return "replaced"
	case Unchanged:
		return "unchanged"
	default:
		return "registered"
	}
}

// Limit and state errors, each raised before anything is written.
var (
	ErrInvalid = errors.New("invalid user command")
	ErrFull    = fmt.Errorf("a scope keeps at most %d user commands: remove one first", MaxPerScope)
	ErrExists  = errors.New("a user command with this id already exists")
	ErrUnknown = errors.New("no such user command")
	// ErrStale: a conditional update or removal named a revision that is no
	// longer the command's.
	ErrStale = errors.New("the user command changed since it was displayed")
)

type scopeKey struct{ reply, root string }

type entry struct {
	Command
	seq int64
}

// Registry is the in-memory read model over the store. Reads (List, Snapshot,
// Get, GetBySource, Revision, RevisionFor) never touch a file; a change commits
// to the store first, then updates memory. Safe for concurrent use.
type Registry struct {
	store *Store
	// wmu serializes the writers (so the cap check, the commit and the memory
	// update are one step among writers) and is never held by a reader; mu
	// guards the maps and is held only for memory work.
	wmu        sync.Mutex
	mu         sync.RWMutex
	scopes     map[scopeKey]map[string]*entry
	rootScopes map[string]map[string]*entry
	rev        map[string]uint64
	rootRev    map[string]uint64
	changeN    uint64
	epoch      string
	nextRev    int64
	nextSeq    int64
	onChange   func(reply string)
	closed     bool
	clock      func() int64
}

// Open opens the store at path and loads every command into memory. onChange,
// when not nil, runs after each committed change with the conversation it
// touched (or "" for a root-scoped change); it must not call back into the
// registry's writers.
func Open(path string, onChange func(reply string)) (*Registry, error) {
	return openClock(path, onChange, func() int64 { return time.Now().UnixMilli() })
}

// openClock is Open with the clock that seeds the revisions (a test seam).
func openClock(path string, onChange func(reply string), clock func() int64) (*Registry, error) {
	st, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	agentRows, rootRows, err := st.load()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	high, err := st.highWater()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	r := &Registry{
		store:      st,
		scopes:     map[scopeKey]map[string]*entry{},
		rootScopes: map[string]map[string]*entry{},
		rev:        map[string]uint64{},
		rootRev:    map[string]uint64{},
		onChange:   onChange,
		clock:      clock,
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		b = [6]byte{byte(time.Now().UnixNano())}
	}
	r.epoch = hex.EncodeToString(b[:])
	// A revision is seeded from the stored high-water (the highest ever issued,
	// kept across withdrawals) and the surviving rows, and from the clock as
	// well so that a store that was deleted still starts above what an old page
	// held. The clock only raises the seed: a clock set back never reuses one.
	r.nextRev = max(r.clock(), high)
	for _, row := range agentRows {
		k := scopeKey{row.Reply, row.Root}
		if r.scopes[k] == nil {
			r.scopes[k] = map[string]*entry{}
		}
		row.Command.Source = SourceAgent
		r.scopes[k][row.ID] = &entry{Command: row.Command, seq: row.Seq}
		r.nextRev = max(r.nextRev, row.Revision)
		r.nextSeq = max(r.nextSeq, row.Seq)
	}
	for _, row := range rootRows {
		if r.rootScopes[row.Root] == nil {
			r.rootScopes[row.Root] = map[string]*entry{}
		}
		row.Command.Source = SourceUser
		r.rootScopes[row.Root][row.ID] = &entry{Command: row.Command, seq: row.Seq}
		r.nextRev = max(r.nextRev, row.Revision)
		r.nextSeq = max(r.nextSeq, row.Seq)
	}
	return r, nil
}

// Close releases the store; later changes fail.
func (r *Registry) Close() error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	r.closed = true
	return r.store.Close()
}

// Ops is the store's database operation count (tests and measurements).
func (r *Registry) Ops() int64 { return r.store.Ops() }

// normRoot is the scope's worktree key: the cleaned path, compared without
// case on Windows.
func normRoot(root string) string {
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	return root
}

// check validates an Input and normalizes the label, description, and cwd.
func check(in Input) (Input, error) {
	if !ValidID(in.ID) {
		return in, fmt.Errorf("%w: id must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit", ErrInvalid)
	}
	return checkFields(in)
}

func checkFields(in Input) (Input, error) {
	in.Label = strings.TrimSpace(in.Label)
	in.Description = strings.TrimSpace(in.Description)
	in.Cwd = strings.TrimSpace(in.Cwd)
	if in.Label == "" {
		return in, fmt.Errorf("%w: label is required", ErrInvalid)
	}
	if n := utf8.RuneCountInString(in.Label); n > MaxLabel {
		return in, fmt.Errorf("%w: label is %d characters, at most %d", ErrInvalid, n, MaxLabel)
	}
	if n := utf8.RuneCountInString(in.Description); n > MaxDescription {
		return in, fmt.Errorf("%w: description is %d characters, at most %d", ErrInvalid, n, MaxDescription)
	}
	for name, text := range map[string]string{"label": in.Label, "description": in.Description} {
		if !utf8.ValidString(text) || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return in, fmt.Errorf("%w: %s must be one line of text", ErrInvalid, name)
		}
	}
	if err := ValidateLine(in.Line); err != nil {
		return in, err
	}
	if err := ValidatePrompt(in.Prompt); err != nil {
		return in, err
	}
	if err := ValidateCwdSyntax(in.Cwd); err != nil {
		return in, err
	}
	src, err := NormalizeSource(in.Source)
	if err != nil {
		return in, err
	}
	in.Source = src
	return in, nil
}

// Set registers or replaces the agent command in.ID of conversation reply in
// worktree root. A definition equal to the stored one commits nothing and keeps
// its revision. Replacing an existing command succeeds at the cap.
func (r *Registry) Set(reply, root string, in Input) (Command, Outcome, error) {
	in.Source = SourceAgent
	in, err := check(in)
	if err != nil {
		return Command{}, 0, err
	}
	root = normRoot(root)
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.closed {
		return Command{}, 0, ErrStoreClosed
	}
	k := scopeKey{reply, root}
	r.mu.RLock()
	old := r.scopes[k][in.ID]
	count := len(r.scopes[k])
	r.mu.RUnlock()
	if old != nil && old.Label == in.Label && old.Description == in.Description && old.Line == in.Line && old.Prompt == in.Prompt && old.Cwd == in.Cwd {
		return old.Command, Unchanged, nil
	}
	if old == nil && count >= MaxPerScope {
		return Command{}, 0, ErrFull
	}
	next := row{
		Reply: reply,
		Root:  root,
		Command: Command{
			ID:          in.ID,
			Label:       in.Label,
			Description: in.Description,
			Line:        in.Line,
			Prompt:      in.Prompt,
			Cwd:         in.Cwd,
			Source:      SourceAgent,
		},
	}
	outcome := Replaced
	if old != nil {
		next.Seq = old.seq
	} else {
		outcome = Registered
		next.Seq = r.nextSeq + 1
	}
	next.Revision = r.nextRev + 1
	if err := r.store.put(next); err != nil {
		return Command{}, 0, err
	}
	r.nextRev, r.nextSeq = next.Revision, max(r.nextSeq, next.Seq)
	r.mu.Lock()
	if r.scopes[k] == nil {
		r.scopes[k] = map[string]*entry{}
	}
	r.scopes[k][in.ID] = &entry{Command: next.Command, seq: next.Seq}
	r.bumpReplyLocked(reply)
	r.mu.Unlock()
	r.notify(reply)
	return next.Command, outcome, nil
}

// CreateUser creates a new project-root-persistent user command in worktree
// root. When in.ID is empty, a unique valid slug ID is generated.
func (r *Registry) CreateUser(root string, in Input) (Command, error) {
	in.Source = SourceUser
	in.ID = strings.TrimSpace(in.ID)
	in, err := checkFields(in)
	if err != nil {
		return Command{}, err
	}
	if in.ID != "" && !ValidID(in.ID) {
		return Command{}, fmt.Errorf("%w: id must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit", ErrInvalid)
	}
	root = normRoot(root)
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.closed {
		return Command{}, ErrStoreClosed
	}
	r.mu.RLock()
	existing := r.rootScopes[root]
	count := len(existing)
	if in.ID != "" && existing[in.ID] != nil {
		r.mu.RUnlock()
		return Command{}, ErrExists
	}
	if count >= MaxPerScope {
		r.mu.RUnlock()
		return Command{}, ErrFull
	}
	if in.ID == "" {
		in.ID = generateSlugID(in.Label, existing)
	}
	r.mu.RUnlock()

	next := row{
		Root: root,
		Command: Command{
			ID:          in.ID,
			Label:       in.Label,
			Description: in.Description,
			Line:        in.Line,
			Prompt:      in.Prompt,
			Cwd:         in.Cwd,
			Source:      SourceUser,
		},
		Seq: r.nextSeq + 1,
	}
	next.Revision = r.nextRev + 1
	if err := r.store.putRoot(next); err != nil {
		return Command{}, err
	}
	r.nextRev, r.nextSeq = next.Revision, max(r.nextSeq, next.Seq)
	r.mu.Lock()
	if r.rootScopes[root] == nil {
		r.rootScopes[root] = map[string]*entry{}
	}
	r.rootScopes[root][in.ID] = &entry{Command: next.Command, seq: next.Seq}
	r.bumpRootLocked(root)
	r.mu.Unlock()
	r.notify("")
	return next.Command, nil
}

// PatchIf updates command (source, id) only while its revision equals rev.
// Preserves Source and seq. Returns ErrUnknown when absent, ErrStale on
// revision mismatch.
func (r *Registry) PatchIf(reply, root, source, id string, rev int64, patch Patch) (Command, error) {
	if rev <= 0 {
		return Command{}, ErrStale
	}
	src, err := NormalizeSource(source)
	if err != nil {
		return Command{}, err
	}
	if !ValidID(id) {
		return Command{}, fmt.Errorf("%w: invalid command id", ErrInvalid)
	}
	root = normRoot(root)
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.closed {
		return Command{}, ErrStoreClosed
	}
	r.mu.RLock()
	var old *entry
	if src == SourceUser {
		old = r.rootScopes[root][id]
	} else {
		old = r.scopes[scopeKey{reply, root}][id]
	}
	var cur entry
	found := old != nil
	if found {
		cur = *old
	}
	r.mu.RUnlock()
	if !found {
		return Command{}, ErrUnknown
	}
	if cur.Revision != rev {
		return Command{}, ErrStale
	}
	merged := Input{
		ID:          cur.ID,
		Label:       cur.Label,
		Description: cur.Description,
		Line:        cur.Line,
		Prompt:      cur.Prompt,
		Cwd:         cur.Cwd,
		Source:      src,
	}
	if patch.Label != nil {
		merged.Label = *patch.Label
	}
	if patch.Description != nil {
		merged.Description = *patch.Description
	}
	if patch.Line != nil {
		merged.Line = *patch.Line
	}
	if patch.Prompt != nil {
		merged.Prompt = *patch.Prompt
	}
	if patch.Cwd != nil {
		merged.Cwd = *patch.Cwd
	}
	merged, err = check(merged)
	if err != nil {
		return Command{}, err
	}
	if merged.Label == cur.Label && merged.Description == cur.Description && merged.Line == cur.Line && merged.Prompt == cur.Prompt && merged.Cwd == cur.Cwd {
		return cur.Command, nil
	}
	next := row{
		Reply: reply,
		Root:  root,
		Command: Command{
			ID:          cur.ID,
			Label:       merged.Label,
			Description: merged.Description,
			Line:        merged.Line,
			Prompt:      merged.Prompt,
			Cwd:         merged.Cwd,
			Source:      src,
			Revision:    r.nextRev + 1,
		},
		Seq: cur.seq,
	}
	if src == SourceUser {
		if err := r.store.putRoot(next); err != nil {
			return Command{}, err
		}
		r.nextRev = next.Revision
		r.mu.Lock()
		r.rootScopes[root][cur.ID] = &entry{Command: next.Command, seq: next.Seq}
		r.bumpRootLocked(root)
		r.mu.Unlock()
		r.notify("")
	} else {
		if err := r.store.put(next); err != nil {
			return Command{}, err
		}
		r.nextRev = next.Revision
		r.mu.Lock()
		r.scopes[scopeKey{reply, root}][cur.ID] = &entry{Command: next.Command, seq: next.Seq}
		r.bumpReplyLocked(reply)
		r.mu.Unlock()
		r.notify(reply)
	}
	return next.Command, nil
}

// Remove withdraws agent command id; ErrUnknown when there is none.
func (r *Registry) Remove(reply, root, id string) error {
	return r.remove(reply, root, SourceAgent, id, 0)
}

// RemoveIf withdraws agent command id only while its revision is rev.
func (r *Registry) RemoveIf(reply, root, id string, rev int64) error {
	return r.RemoveIfSource(reply, root, SourceAgent, id, rev)
}

// RemoveIfSource withdraws command (source, id) only while its revision is rev,
// decided under the writers' lock together with the removal.
func (r *Registry) RemoveIfSource(reply, root, source, id string, rev int64) error {
	if rev <= 0 {
		return ErrStale
	}
	src, err := NormalizeSource(source)
	if err != nil {
		return err
	}
	return r.remove(reply, root, src, id, rev)
}

func (r *Registry) remove(reply, root, source, id string, rev int64) error {
	root = normRoot(root)
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.closed {
		return ErrStoreClosed
	}
	k := scopeKey{reply, root}
	r.mu.RLock()
	var e *entry
	var ok bool
	if source == SourceUser {
		e, ok = r.rootScopes[root][id]
	} else {
		e, ok = r.scopes[k][id]
	}
	var cur int64
	if ok {
		cur = e.Revision
	}
	r.mu.RUnlock()
	if !ok {
		return ErrUnknown
	}
	if rev != 0 && cur != rev {
		return ErrStale
	}
	// The withdrawal consumes a revision too: the high-water never falls.
	issued := r.nextRev + 1
	if source == SourceUser {
		if err := r.store.delRoot(root, id, issued); err != nil {
			return err
		}
		r.nextRev = issued
		r.mu.Lock()
		delete(r.rootScopes[root], id)
		if len(r.rootScopes[root]) == 0 {
			delete(r.rootScopes, root)
		}
		r.bumpRootLocked(root)
		r.mu.Unlock()
		r.notify("")
		return nil
	}
	if err := r.store.del(reply, root, id, issued); err != nil {
		return err
	}
	r.nextRev = issued
	r.mu.Lock()
	delete(r.scopes[k], id)
	if len(r.scopes[k]) == 0 {
		delete(r.scopes, k)
	}
	r.bumpReplyLocked(reply)
	r.mu.Unlock()
	r.notify(reply)
	return nil
}

// List returns the merged commands (agent commands for reply+root and user
// commands for root) in registration order. It reads memory only.
func (r *Registry) List(reply, root string) []Command {
	nr := normRoot(root)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.listLocked(reply, nr)
}

func (r *Registry) listLocked(reply, nr string) []Command {
	k := scopeKey{reply, nr}
	es := make([]*entry, 0, len(r.scopes[k])+len(r.rootScopes[nr]))
	for _, e := range r.scopes[k] {
		es = append(es, e)
	}
	for _, e := range r.rootScopes[nr] {
		es = append(es, e)
	}
	sortEntries(es)
	out := make([]Command, len(es))
	for i, e := range es {
		out[i] = e.Command
	}
	return out
}

// Snapshot returns the merged commands of conversation reply in worktree root
// and the combined opaque revision from one read, so the list and revision
// always belong together. It reads memory only.
func (r *Registry) Snapshot(reply, root string) ([]Command, string) {
	nr := normRoot(root)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.listLocked(reply, nr), fmt.Sprintf("%s.%d", r.epoch, r.combinedRevLocked(reply, nr))
}

// Get returns one agent command. It reads memory only.
func (r *Registry) Get(reply, root, id string) (Command, bool) {
	return r.GetBySource(reply, root, SourceAgent, id)
}

// GetBySource returns one scope-qualified command ("agent" or "user"). It reads
// memory only.
func (r *Registry) GetBySource(reply, root, source, id string) (Command, bool) {
	src, err := NormalizeSource(source)
	if err != nil {
		return Command{}, false
	}
	nr := normRoot(root)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var e *entry
	if src == SourceUser {
		e = r.rootScopes[nr][id]
	} else {
		e = r.scopes[scopeKey{reply, nr}][id]
	}
	if e == nil {
		return Command{}, false
	}
	return e.Command, true
}

// Revision is the opaque revision of conversation reply's agent commands.
func (r *Registry) Revision(reply string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fmt.Sprintf("%s.%d", r.epoch, r.rev[reply])
}

// RevisionFor is the combined opaque revision of conversation reply and
// worktree root: it strictly advances on every change to either the session's
// agent commands or the worktree's user commands. It reads memory only.
func (r *Registry) RevisionFor(reply, root string) string {
	nr := normRoot(root)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fmt.Sprintf("%s.%d", r.epoch, r.combinedRevLocked(reply, nr))
}

func (r *Registry) combinedRevLocked(reply, nr string) uint64 {
	return max(r.rev[reply], r.rootRev[nr])
}

// Reconcile drops the agent commands of every conversation known reports
// unknown, in one commit (nothing is committed when there are none). Root user
// commands are never touched.
func (r *Registry) Reconcile(known func(reply string) bool) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.closed {
		return ErrStoreClosed
	}
	seen := map[string]bool{}
	r.mu.RLock()
	for k := range r.scopes {
		seen[k.reply] = true
	}
	r.mu.RUnlock()
	var gone []string
	for reply := range seen {
		if !known(reply) {
			gone = append(gone, reply)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	if err := r.store.delReplies(gone); err != nil {
		return err
	}
	r.mu.Lock()
	for k := range r.scopes {
		for _, reply := range gone {
			if k.reply == reply {
				delete(r.scopes, k)
			}
		}
	}
	for _, reply := range gone {
		r.bumpReplyLocked(reply)
	}
	r.mu.Unlock()
	for _, reply := range gone {
		r.notify(reply)
	}
	return nil
}

func (r *Registry) bumpReplyLocked(reply string) {
	r.changeN++
	r.rev[reply] = r.changeN
}

func (r *Registry) bumpRootLocked(nr string) {
	r.changeN++
	r.rootRev[nr] = r.changeN
}

func (r *Registry) notify(reply string) {
	if r.onChange != nil {
		r.onChange(reply)
	}
}

func sortEntries(es []*entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].seq != es[j].seq {
			return es[i].seq < es[j].seq
		}
		if es[i].Source != es[j].Source {
			return es[i].Source < es[j].Source
		}
		return es[i].ID < es[j].ID
	})
}

func generateSlugID(label string, existing map[string]*entry) string {
	var b strings.Builder
	prevDash := false
	for _, ch := range strings.ToLower(label) {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9'):
			b.WriteRune(ch)
			prevDash = false
		case b.Len() > 0 && !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "cmd"
	}
	if ValidID(base) && existing[base] == nil {
		return base
	}
	for i := 0; i < 64; i++ {
		var rnd [3]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			rnd[0] = byte(i)
		}
		cand := base + "-" + hex.EncodeToString(rnd[:])
		if len(cand) > 64 {
			cand = cand[:64]
		}
		if ValidID(cand) && existing[cand] == nil {
			return cand
		}
	}
	return fmt.Sprintf("cmd-%x", time.Now().UnixNano())
}
