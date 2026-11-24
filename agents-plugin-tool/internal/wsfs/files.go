// Package wsfs implements the daemon's filesystem mutation and recovery rules.
package wsfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Authority is a worker's permission level, in the daemon's spelling.
type Authority string

const (
	AuthorityReadOnly     Authority = "read-only"
	AuthorityWorkdirWrite Authority = "workdir-write"
	AuthorityFull         Authority = "full"
)

// Valid reports whether a is one of the three levels.
func (a Authority) Valid() bool {
	switch a {
	case AuthorityReadOnly, AuthorityWorkdirWrite, AuthorityFull:
		return true
	}
	return false
}

// Files serves the file tools for one session.
type Files struct {
	// Root is the session's worktree root, the confinement of workdir-write.
	Root string
	// Cwd is the directory a relative path resolves against.
	Cwd       string
	Authority Authority
	// Protection gives the caller a fresh path and handle policy for each
	// inspection and again immediately before mutation.
	Protection func() FileProtection
	// RecordMutation persists the admitted target and content states before
	// the file is touched.
	RecordMutation func(context.Context, FileIntent) error
}

// FileProtection checks the selected path and the actual handles used for it.
type FileProtection interface {
	CheckPath(string) error
	CheckOpened(*os.File, os.FileInfo) error
}

// ResolvePath normalizes a path relative to the caller's working directory.
func (f *Files) ResolvePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(path) {
		base := f.Cwd
		if base == "" {
			base = f.Root
		}
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

// realPath resolves symbolic links of the deepest existing ancestor of p and
// appends the rest, so a path that does not exist yet is judged where it
// would land. Link targets are never cleaned lexically here: EvalSymlinks
// resolves them as the operating system does, so a relative link through a
// directory link followed by ".." is judged where it really lands. A symbolic
// link whose target is missing (dangling, or a loop) is refused: a write
// through it would create the target where the link points, and judging that
// place would mean re-implementing the resolver.
func realPath(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	var rest []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		// EvalSymlinks reports a link with a missing target as a missing
		// path; the link itself is there.
		if info, lerr := os.Lstat(cur); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symbolic link whose target does not exist; it is not followed for a write", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// writable resolves path for a write and enforces the authority.
func (f *Files) writable(path string, protection FileProtection) (string, error) {
	switch f.Authority {
	case AuthorityReadOnly:
		return "", errors.New("this session is read-only: it may not edit or write files")
	case AuthorityWorkdirWrite, AuthorityFull:
	default:
		return "", fmt.Errorf("this session has no write authority (%q)", string(f.Authority))
	}
	p, err := f.ResolvePath(path)
	if err != nil {
		return "", err
	}
	if protection != nil {
		if err := protection.CheckPath(p); err != nil {
			return "", err
		}
	}
	realTarget, err := realPath(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %v", path, err)
	}
	if protection != nil && realTarget != p {
		if err := protection.CheckPath(realTarget); err != nil {
			return "", err
		}
	}
	if f.Authority == AuthorityFull {
		return realTarget, nil
	}
	root := f.Root
	if root == "" {
		return "", errors.New("workdir-write needs the session's worktree root, and none is set")
	}
	realRoot, err := realPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve the worktree root: %v", err)
	}
	if !within(realRoot, realTarget) {
		return "", fmt.Errorf("%s is outside the session's worktree; workdir-write may write inside it only", path)
	}
	return realTarget, nil
}

// within reports whether target is root or lies under it.
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// IsBinary reports whether the first 8 KiB contain a NUL byte.
func IsBinary(b []byte) bool {
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// Edit applies unique, non-overlapping replacements to one text file.
func (f *Files) Edit(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path  string `json:"path"`
		Edits []struct {
			Old string  `json:"oldText"`
			New *string `json:"newText"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("edit arguments: %v", err)
	}
	if len(a.Edits) == 0 {
		return "", errors.New("edits is empty")
	}
	target, protection, err := f.pinForWrite(a.Path)
	if err != nil {
		return "", err
	}
	defer target.close()
	prior, data, err := target.inspect(true, protection)
	if err != nil {
		return "", err
	}
	if prior.info == nil {
		return "", &os.PathError{Op: "read", Path: target.path, Err: fs.ErrNotExist}
	}
	if IsBinary(data) {
		return "", fmt.Errorf("%s is a binary file", a.Path)
	}
	orig := string(data)
	text, crlfOffsets := editMatchText(orig)
	type span struct{ start, end int }
	spans := make([]span, len(a.Edits))
	for i, e := range a.Edits {
		if e.New == nil {
			return "", fmt.Errorf("edit %d: newText is required", i+1)
		}
		old := e.Old
		if len(crlfOffsets) != 0 {
			old = strings.ReplaceAll(old, "\r\n", "\n")
		}
		if old == "" {
			return "", fmt.Errorf("edit %d: oldText is empty", i+1)
		}
		at := strings.Index(text, old)
		if at < 0 {
			return "", fmt.Errorf("edit %d: oldText was not found in %s; it must match the file exactly, whitespace included", i+1, a.Path)
		}
		if n := countMatches(text[at:], old); n > 1 {
			return "", fmt.Errorf("edit %d: oldText matches %d places in %s; add surrounding text so it matches exactly one", i+1, n, a.Path)
		}
		spans[i] = span{at, at + len(old)}
	}
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(x, y int) bool { return spans[order[x]].start < spans[order[y]].start })
	for k := 1; k < len(order); k++ {
		if spans[order[k]].start < spans[order[k-1]].end {
			return "", fmt.Errorf("edits %d and %d overlap; merge them into one edit", order[k-1]+1, order[k]+1)
		}
	}
	var out strings.Builder
	last := 0
	for _, i := range order {
		match := spans[i]
		start := match.start + sort.SearchInts(crlfOffsets, match.start)
		end := match.end + sort.SearchInts(crlfOffsets, match.end)
		out.WriteString(orig[last:start])
		replacement := *a.Edits[i].New
		if len(crlfOffsets) != 0 {
			replacement = strings.ReplaceAll(replacement, "\r\n", "\n")
			if replacement == text[match.start:match.end] {
				replacement = orig[start:end]
			} else if editLineUsesCRLF(orig, start) {
				replacement = strings.ReplaceAll(replacement, "\n", "\r\n")
			}
		}
		out.WriteString(replacement)
		last = end
	}
	out.WriteString(orig[last:])
	result := out.String()
	diff := unifiedDiff(a.Path, orig, result)
	if err := f.commitFile(ctx, target, prior, []byte(result)); err != nil {
		return "", err
	}
	if diff == "" {
		return fmt.Sprintf("edited %s (no change)", a.Path), nil
	}
	return diff, nil
}

// editMatchText normalizes CRLF for matching and records each removed CR at
// its corresponding LF's offset in the normalized text. The number of removed
// CRs strictly before a match boundary maps it back to the original bytes.
func editMatchText(orig string) (string, []int) {
	var offsets []int
	for start := 0; ; {
		index := strings.Index(orig[start:], "\r\n")
		if index < 0 {
			break
		}
		index += start
		offsets = append(offsets, index-len(offsets))
		start = index + 2
	}
	if len(offsets) == 0 {
		return orig, nil
	}
	return strings.ReplaceAll(orig, "\r\n", "\n"), offsets
}

// New lines use the edited line's terminator, or the preceding one for an
// unterminated last line.
func editLineUsesCRLF(orig string, start int) bool {
	end := strings.IndexByte(orig[start:], '\n')
	if end >= 0 {
		end += start
	} else {
		end = strings.LastIndexByte(orig[:start], '\n')
	}
	return end > 0 && orig[end-1] == '\r'
}

func countMatches(text, match string) int {
	count := 0
	for {
		i := strings.Index(text, match)
		if i < 0 {
			return count
		}
		count++
		text = text[i+1:]
	}
}

// Write creates or replaces one file, creating missing parent directories.
func (f *Files) Write(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("write arguments: %v", err)
	}
	target, protection, err := f.pinForWrite(a.Path)
	if err != nil {
		return "", err
	}
	defer target.close()
	prior, _, err := target.inspect(false, protection)
	if err != nil {
		return "", err
	}
	if err := f.commitFile(ctx, target, prior, []byte(a.Content)); err != nil {
		return "", err
	}
	verb := "created"
	if prior.info != nil {
		verb = "overwrote"
	}
	return fmt.Sprintf("%s %s (%d bytes)", verb, a.Path, len(a.Content)), nil
}
