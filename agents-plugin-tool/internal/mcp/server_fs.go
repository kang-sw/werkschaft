package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// server_fs.go is the fs.* read tools a cutover Codex session reads with in
// place of its shell: fs.read, fs.grep and fs.list. They are read-only and
// run without approval in every session mode. Their reach matches Codex's
// native read-only sandbox, which reads the whole filesystem: a relative
// path resolves beneath the session's root and an absolute path is read as
// given. The checks are advisory; nothing confines the daemon's own reads.
//
// Every reply is bounded: at most fsResultBudget bytes of text, with what
// was cut said in the reply, and finite work behind it (the caps below).
const (
	// fsResultBudget bounds one reply's text.
	fsResultBudget = 4096
	// fsReadDefaultLines and fsReadMaxLines bound fs.read's returned lines.
	fsReadDefaultLines = 200
	fsReadMaxLines     = 2000
	// fsLineBytes bounds one returned line; a longer one is cut.
	fsLineBytes = 512
	// fsGrepMaxMatches bounds fs.grep's returned matches.
	fsGrepMaxMatches = 100
	// fsListMaxEntries bounds fs.list's returned entries.
	fsListMaxEntries = 500
	// fsBinarySniff is the head a NUL byte in marks a binary file.
	fsBinarySniff = 8192
)

// The scan caps bound the work behind a reply. Variables only so tests can
// exercise them at small sizes; nothing else changes them.
var (
	// fsReadScanBytes bounds the bytes fs.read reads to reach and return
	// its lines.
	fsReadScanBytes int64 = 64 << 20
	// fsGrepMaxFiles and fsGrepScanBytes bound fs.grep's scan: files
	// opened and bytes read across them.
	fsGrepMaxFiles        = 10000
	fsGrepScanBytes int64 = 64 << 20
	// fsGrepScanEntries bounds the directory entries fs.grep's walk visits,
	// whatever its glob or the files' sizes let through.
	fsGrepScanEntries = 100000
	// fsGrepFileBytes skips a file larger than this.
	fsGrepFileBytes int64 = 8 << 20
	// fsListScanEntries bounds the directory entries fs.list reads.
	fsListScanEntries = 10000
)

// fsSize names a byte count for a reply.
func fsSize(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// errFSBinary is fs.read's refusal of a binary file.
var errFSBinary = errors.New("binary file (a NUL byte in its first 8 KiB); fs.read reads text files only")

// callFS runs the fs.* read tools for a cutover Codex session; the caller
// has checked codexReadTools.
func (s *Server) callFS(id json.RawMessage, name string, args, meta map[string]any) response {
	root, err := s.resolveToolRoot(args, meta)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	raw, _ := args["path"].(string)
	if strings.TrimSpace(raw) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: path is required", name))
	}
	path := fsResolvePath(root, raw)
	glob, _ := args["glob"].(string)
	if glob != "" {
		if _, err := filepath.Match(glob, ""); err != nil {
			return toolTextResponse(id, "", fmt.Errorf("%s: bad glob %q: %v", name, glob, err))
		}
	}
	var text string
	switch name {
	case "fs.read":
		text, err = fsRead(path, intFromArgument(args["offset"], 0), intFromArgument(args["limit"], fsReadDefaultLines))
	case "fs.grep":
		pattern, _ := args["pattern"].(string)
		text, err = fsGrep(root, path, pattern, glob)
	case "fs.list":
		text, err = fsList(path, glob)
	default:
		err = fmt.Errorf("unknown fs tool %s", name)
	}
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, err))
	}
	return toolTextResponse(id, text, nil)
}

// fsResolvePath resolves path: beneath root when relative, as given when
// absolute.
func fsResolvePath(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

// fsBudget collects one reply's text up to fsResultBudget bytes.
type fsBudget struct {
	b    strings.Builder
	full bool
}

// add appends line and a newline when it fits, and reports whether it did;
// once a line does not fit, nothing more is added.
func (f *fsBudget) add(line string, reserve int) bool {
	if f.full || f.b.Len()+len(line)+1 > fsResultBudget-reserve {
		f.full = true
		return false
	}
	f.b.WriteString(line)
	f.b.WriteByte('\n')
	return true
}

// fsCutLine is line cut to fsLineBytes on a UTF-8 boundary, marked.
func fsCutLine(line string) string {
	if len(line) <= fsLineBytes {
		return line
	}
	cut := fsLineBytes
	for cut > 0 && cut < len(line) && line[cut]&0xC0 == 0x80 {
		cut--
	}
	return line[:cut] + fmt.Sprintf(" …[line cut at %d of %d bytes]", cut, len(line))
}

// fsIsBinary reports whether head marks a binary file.
func fsIsBinary(head []byte) bool { return bytes.IndexByte(head, 0) >= 0 }

// fsTruncNote is the reserve kept for a reply's closing truncation note.
const fsTruncNote = 160

// fsRead returns the text lines of file path after skipping offset lines,
// at most limit of them, each numbered from one.
func fsRead(path string, offset, limit int) (string, error) {
	if offset < 0 {
		return "", errors.New("offset must not be negative")
	}
	if limit <= 0 {
		limit = fsReadDefaultLines
	}
	if limit > fsReadMaxLines {
		limit = fsReadMaxLines
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use fs.list", path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(io.LimitReader(f, fsReadScanBytes), 64<<10)
	head, _ := reader.Peek(fsBinarySniff)
	if fsIsBinary(head) {
		return "", errFSBinary
	}
	var out fsBudget
	lineNo, shown, scanned := 0, 0, int64(0)
	more, cutByBudget := false, false
	for {
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			break
		}
		scanned += int64(len(line))
		lineNo++
		if lineNo <= offset {
			continue
		}
		if shown == limit {
			more = true
			break
		}
		text := fsCutLine(strings.TrimRight(line, "\r\n"))
		if !out.add(fmt.Sprintf("%d\t%s", lineNo, text), fsTruncNote) {
			more, cutByBudget = true, true
			break
		}
		shown++
		if err != nil {
			break
		}
	}
	scanCapped := scanned >= fsReadScanBytes && info.Size() > fsReadScanBytes
	switch {
	case shown == 0 && lineNo <= offset && !scanCapped:
		return fmt.Sprintf("(no lines after offset %d; the file has %d lines)\n", offset, lineNo), nil
	case cutByBudget:
		fmt.Fprintf(&out.b, "[truncated at the %d-byte reply budget after line %d; continue with offset=%d]\n", fsResultBudget, offset+shown, offset+shown)
	case more:
		fmt.Fprintf(&out.b, "[more lines follow line %d; continue with offset=%d]\n", offset+shown, offset+shown)
	case scanCapped:
		fmt.Fprintf(&out.b, "[stopped after reading %s of the file; lines beyond it are not reachable with fs.read]\n", fsSize(fsReadScanBytes))
	}
	return out.b.String(), nil
}

// fsGlobMatch reports whether name passes glob; an empty glob passes all.
func fsGlobMatch(glob, name string) bool {
	if glob == "" {
		return true
	}
	ok, _ := filepath.Match(glob, name)
	return ok
}

// fsDisplayPath is path as a reply names it: relative to root when beneath
// it, else as given.
func fsDisplayPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return path
}

// fsGrep searches file or directory path for lines matching the regular
// expression pattern, in files whose name passes glob, and returns them as
// path:line:text with one-based line numbers. A directory walk skips .git
// directories, symlinked directories and binary files, and stops at the
// scan caps: entries visited, files opened, bytes read.
func fsGrep(root, path, pattern, glob string) (string, error) {
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	var out fsBudget
	matches, files, skippedBinary, skippedLarge := 0, 0, 0, 0
	scanned := int64(0)
	stop := ""
	searchFile := func(file string, size int64) error {
		if size > fsGrepFileBytes {
			skippedLarge++
			return nil
		}
		if files == fsGrepMaxFiles {
			stop = fmt.Sprintf("stopped after %d files", fsGrepMaxFiles)
			return fs.SkipAll
		}
		if scanned+size > fsGrepScanBytes {
			stop = fmt.Sprintf("stopped after reading %s", fsSize(fsGrepScanBytes))
			return fs.SkipAll
		}
		f, err := os.Open(file)
		if err != nil {
			return nil
		}
		defer f.Close()
		files++
		reader := bufio.NewReaderSize(f, 64<<10)
		head, _ := reader.Peek(fsBinarySniff)
		if fsIsBinary(head) {
			skippedBinary++
			return nil
		}
		scanned += size
		name := fsDisplayPath(root, file)
		lineNo := 0
		for {
			line, err := reader.ReadString('\n')
			if line == "" && err != nil {
				return nil
			}
			lineNo++
			text := strings.TrimRight(line, "\r\n")
			if re.MatchString(text) {
				if matches == fsGrepMaxMatches {
					stop = fmt.Sprintf("stopped at %d matches", fsGrepMaxMatches)
					return fs.SkipAll
				}
				if !out.add(fmt.Sprintf("%s:%d:%s", name, lineNo, fsCutLine(text)), fsTruncNote) {
					stop = fmt.Sprintf("stopped at the %d-byte reply budget after %d matches", fsResultBudget, matches)
					return fs.SkipAll
				}
				matches++
			}
			if err != nil {
				return nil
			}
		}
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file or directory", path)
		}
		if err := searchFile(path, info.Size()); err != nil && !errors.Is(err, fs.SkipAll) {
			return "", err
		}
		if skippedBinary > 0 {
			return "", fmt.Errorf("%s is a binary file; fs.grep searches text files only", path)
		}
	} else {
		visited := 0
		walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if visited++; visited > fsGrepScanEntries {
				stop = fmt.Sprintf("stopped after visiting %d directory entries; narrow the path or glob", fsGrepScanEntries)
				return fs.SkipAll
			}
			if err != nil {
				if p == path {
					return err
				}
				return nil
			}
			if d.IsDir() {
				if p != path && d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !fsGlobMatch(glob, d.Name()) {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			return searchFile(p, fi.Size())
		})
		if walkErr != nil && !errors.Is(walkErr, fs.SkipAll) {
			return "", walkErr
		}
	}
	if matches == 0 && stop == "" {
		out.b.WriteString("no matches.\n")
	}
	var notes []string
	if stop != "" {
		notes = append(notes, "truncated: "+stop)
	}
	if skippedBinary > 0 {
		notes = append(notes, fmt.Sprintf("%d binary files skipped", skippedBinary))
	}
	if skippedLarge > 0 {
		notes = append(notes, fmt.Sprintf("%d files over %s skipped", skippedLarge, fsSize(fsGrepFileBytes)))
	}
	if len(notes) > 0 {
		fmt.Fprintf(&out.b, "[%s]\n", strings.Join(notes, "; "))
	}
	return out.b.String(), nil
}

// fsList lists directory path's entries whose name passes glob, sorted,
// with a trailing slash on directories.
func fsList(path, glob string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory; use fs.read", path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(fsListScanEntries)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	scanCapped := false
	if len(entries) == fsListScanEntries {
		if more, _ := dir.ReadDir(1); len(more) > 0 {
			scanCapped = true
		}
	}
	var names []string
	for _, e := range entries {
		if !fsGlobMatch(glob, e.Name()) {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		} else if e.Type()&fs.ModeSymlink != 0 {
			name += "@"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var out fsBudget
	shown := 0
	for _, name := range names {
		if shown == fsListMaxEntries || !out.add(name, fsTruncNote) {
			break
		}
		shown++
	}
	if len(names) == 0 {
		out.b.WriteString("no entries.\n")
	}
	var notes []string
	if shown < len(names) {
		notes = append(notes, fmt.Sprintf("truncated: showing %d of %d entries; narrow with glob", shown, len(names)))
	}
	if scanCapped {
		notes = append(notes, fmt.Sprintf("read only the first %d directory entries", fsListScanEntries))
	}
	if len(notes) > 0 {
		fmt.Fprintf(&out.b, "[%s]\n", strings.Join(notes, "; "))
	}
	return out.b.String(), nil
}
