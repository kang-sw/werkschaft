// Package wsusercmd holds the user commands ("command slots"): bash lines an
// open hosted lead registers so the user can run them with a click in that
// lead's chat. A command is scoped to the lead's conversation (reply ID) and
// the worktree the lead is bound to; the registry serves every read from
// memory, and the SQLite store (storage type 2) is written once per changed
// command and read once at start.
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

// The registration limits.
const (
	// MaxPerScope is the most commands one lead keeps in one worktree.
	MaxPerScope = 32
	// MaxLabel and MaxDescription bound the texts in characters; MaxLine
	// bounds the shell line in bytes.
	MaxLabel       = 64
	MaxDescription = 256
	MaxLine        = 4 << 10
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidID reports whether id is a well-formed command ID (it is also a URL
// path segment of the dashboard's routes).
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Command is one registered user command. Revision identifies the exact
// definition shown to the user: it advances with every change, and never
// repeats for a command ID, even across a withdrawal and a restart.
type Command struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Line        string `json:"line"`
	Revision    int64  `json:"revision"`
}

// Input is a registration: the stable ID, the label, the optional description
// and the raw shell line.
type Input struct {
	ID, Label, Description, Line string
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

// Limit errors, each raised before anything is written.
var (
	ErrFull    = fmt.Errorf("a lead keeps at most %d user commands: unregister one first", MaxPerScope)
	ErrUnknown = errors.New("no such user command")
)

type scopeKey struct{ reply, root string }

type entry struct {
	Command
	seq int64
}

// Registry is the in-memory read model over the store. Reads (List, Get,
// Revision) never touch a file; a change commits to the store first, then
// updates memory. Safe for concurrent use.
type Registry struct {
	store *Store
	// wmu serializes the writers (so the cap check, the commit and the memory
	// update are one step among writers) and is never held by a reader; mu
	// guards the maps and is held only for memory work.
	wmu      sync.Mutex
	mu       sync.RWMutex
	scopes   map[scopeKey]map[string]*entry
	rev      map[string]uint64
	changeN  uint64
	epoch    string
	nextRev  int64
	nextSeq  int64
	onChange func(reply string)
	closed   bool
}

// Open opens the store at path and loads every command into memory. onChange,
// when not nil, runs after each committed change with the conversation it
// touched (the dashboard's refresh signal); it must not call back into the
// registry's writers.
func Open(path string, onChange func(reply string)) (*Registry, error) {
	st, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	rows, err := st.load()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	r := &Registry{store: st, scopes: map[scopeKey]map[string]*entry{}, rev: map[string]uint64{}, onChange: onChange}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		b = [6]byte{byte(time.Now().UnixNano())}
	}
	r.epoch = hex.EncodeToString(b[:])
	// A revision is seeded from the clock as well as from the stored ones, so
	// that one issued after a withdrawal and a restart is still above any
	// revision a page may hold for the withdrawn definition.
	r.nextRev = time.Now().UnixMilli()
	for _, row := range rows {
		k := scopeKey{row.Reply, row.Root}
		if r.scopes[k] == nil {
			r.scopes[k] = map[string]*entry{}
		}
		r.scopes[k][row.ID] = &entry{Command: row.Command, seq: row.Seq}
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

// check validates an Input and returns it with its texts trimmed.
func check(in Input) (Input, error) {
	if !ValidID(in.ID) {
		return in, errors.New("id must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	in.Label = strings.TrimSpace(in.Label)
	in.Description = strings.TrimSpace(in.Description)
	if in.Label == "" {
		return in, errors.New("label is required")
	}
	if n := utf8.RuneCountInString(in.Label); n > MaxLabel {
		return in, fmt.Errorf("label is %d characters, at most %d", n, MaxLabel)
	}
	if n := utf8.RuneCountInString(in.Description); n > MaxDescription {
		return in, fmt.Errorf("description is %d characters, at most %d", n, MaxDescription)
	}
	for name, text := range map[string]string{"label": in.Label, "description": in.Description} {
		if !utf8.ValidString(text) || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return in, fmt.Errorf("%s must be one line of text", name)
		}
	}
	if strings.TrimSpace(in.Line) == "" {
		return in, errors.New("command line is required")
	}
	if len(in.Line) > MaxLine {
		return in, fmt.Errorf("command line is %d bytes, at most %d", len(in.Line), MaxLine)
	}
	if !utf8.ValidString(in.Line) || strings.ContainsRune(in.Line, 0) {
		return in, errors.New("command line is not valid text")
	}
	return in, nil
}

// Set registers or replaces the command in.ID of conversation reply in the
// worktree root. A definition equal to the stored one commits nothing and keeps
// its revision. Replacing an existing command succeeds at the cap.
func (r *Registry) Set(reply, root string, in Input) (Command, Outcome, error) {
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
	if old != nil && old.Label == in.Label && old.Description == in.Description && old.Line == in.Line {
		return old.Command, Unchanged, nil
	}
	if old == nil && count >= MaxPerScope {
		return Command{}, 0, ErrFull
	}
	next := row{Reply: reply, Root: root, Command: Command{ID: in.ID, Label: in.Label, Description: in.Description, Line: in.Line}}
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
	r.bumpLocked(reply)
	r.mu.Unlock()
	r.notify(reply)
	return next.Command, outcome, nil
}

// Remove withdraws command id; ErrUnknown when there is none.
func (r *Registry) Remove(reply, root, id string) error {
	root = normRoot(root)
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.closed {
		return ErrStoreClosed
	}
	k := scopeKey{reply, root}
	r.mu.RLock()
	_, ok := r.scopes[k][id]
	r.mu.RUnlock()
	if !ok {
		return ErrUnknown
	}
	if err := r.store.del(reply, root, id); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.scopes[k], id)
	if len(r.scopes[k]) == 0 {
		delete(r.scopes, k)
	}
	r.bumpLocked(reply)
	r.mu.Unlock()
	r.notify(reply)
	return nil
}

// List returns the commands of conversation reply in worktree root, in
// registration order. It reads memory only.
func (r *Registry) List(reply, root string) []Command {
	k := scopeKey{reply, normRoot(root)}
	r.mu.RLock()
	defer r.mu.RUnlock()
	es := make([]*entry, 0, len(r.scopes[k]))
	for _, e := range r.scopes[k] {
		es = append(es, e)
	}
	sortEntries(es)
	out := make([]Command, len(es))
	for i, e := range es {
		out[i] = e.Command
	}
	return out
}

// Get returns one command. It reads memory only.
func (r *Registry) Get(reply, root, id string) (Command, bool) {
	k := scopeKey{reply, normRoot(root)}
	r.mu.RLock()
	defer r.mu.RUnlock()
	e := r.scopes[k][id]
	if e == nil {
		return Command{}, false
	}
	return e.Command, true
}

// Revision is the opaque revision of conversation reply's commands: it differs
// after every change to any of them, and across daemon instances. It reads
// memory only.
func (r *Registry) Revision(reply string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fmt.Sprintf("%s.%d", r.epoch, r.rev[reply])
}

// Reconcile drops the commands of every conversation known reports unknown, in
// one commit (nothing is committed when there are none). The daemon calls it
// once the runner knows its conversations.
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
		r.bumpLocked(reply)
	}
	r.mu.Unlock()
	return nil
}

func (r *Registry) bumpLocked(reply string) {
	r.changeN++
	r.rev[reply] = r.changeN
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
		return es[i].ID < es[j].ID
	})
}
