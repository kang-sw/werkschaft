package wswhiteboard

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MockupMaxFiles caps how many allowlisted files a single mockup snapshot
	// bundle may contain.
	MockupMaxFiles = 256
	// MockupMaxBundleBytes caps the total uncompressed byte size of a single
	// mockup snapshot bundle (both at capture and on decode).
	MockupMaxBundleBytes int64 = 8 << 20
	// MockupLRUMaxBytes is the default memory cap for the dashboard's decoded
	// mockup bundle LRU cache.
	MockupLRUMaxBytes int64 = 64 << 20

	// mockupMaxZipBytes caps raw compressed zip bytes before DecodeMockupZip
	// enforces MockupMaxFiles and MockupMaxBundleBytes on inflated members.
	// 1 MiB of headroom covers worst-case zip headers, long relative paths,
	// and deflate stored-block framing across 256 incompressible files.
	mockupMaxZipBytes = MockupMaxBundleBytes + (1 << 20)

	mockupArchivePrefix = "_mockups/"
)

// MockupMeta identifies an immutable multi-file HTML mockup snapshot stored in
// the board's mockup_bundles table alongside a panel entry.
type MockupMeta struct {
	Root   string `json:"root"`
	SHA256 string `json:"sha256"`
}

// MockupBundle is one captured or stored mockup snapshot zip and its metadata.
type MockupBundle struct {
	EntryID   string
	RootFile  string
	SHA256    string
	Zip       []byte
	ByteSize  int64
	FileCount int
	CreatedAt string
}

var mockupContentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".md":    "text/plain; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
}

// MockupContentType returns the served Content-Type for a mockup bundle member
// filename if its extension is on the static allowlist.
func MockupContentType(name string) (string, bool) {
	ct, ok := mockupContentTypes[strings.ToLower(path.Ext(name))]
	return ct, ok
}

// ValidMockupRel reports whether rel is a safe relative slash path inside a
// mockup bundle (no traversal, no backslashes or colons, no dot-prefixed
// segments, and clean canonical form).
func ValidMockupRel(rel string) bool {
	if rel == "" || strings.ContainsAny(rel, `\:`+"\x00") || strings.HasPrefix(rel, "/") || strings.HasSuffix(rel, "/") {
		return false
	}
	if path.Clean(rel) != rel || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}

// isHTMLSourceExt reports whether a panel source path ends in .html or .htm.
func isHTMLSourceExt(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".html" || ext == ".htm"
}

// CollectMockupBundle packages the containing subfolder of a full HTML document
// panel source into a deterministic in-memory zip snapshot. It enforces the
// same root-containment, symlink/junction, handle-pinning, and regular-file
// rules as ReadPanelSource, plus:
//   - src must reside inside a strict subdirectory of root (never root itself);
//   - any dot-named file or directory in the scope path or subtree is refused;
//   - non-regular files (symlinks, junctions, FIFOs, sockets, devices) are refused;
//   - non-allowlisted plain regular files are skipped without counting toward caps;
//   - walks stop immediately when MockupMaxFiles or MockupMaxBundleBytes is exceeded.
func CollectMockupBundle(root, src string) (MockupBundle, error) {
	switch {
	case root == "":
		return MockupBundle{}, errors.New("the session has no project root to read from")
	case src == "" || strings.TrimSpace(src) == "":
		return MockupBundle{}, errors.New("the path is empty")
	case strings.ContainsRune(src, 0):
		return MockupBundle{}, errors.New("the path holds a NUL character")
	}
	root = filepath.Clean(root)
	abs := filepath.FromSlash(src)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || !belowRoot(rel) {
		return MockupBundle{}, fmt.Errorf("the path is outside the session root %s; keep the file inside it", root)
	}
	if !isHTMLSourceExt(abs) {
		return MockupBundle{}, errors.New("only .html and .htm files can be a mockup snapshot root")
	}
	scopeRel := filepath.Dir(rel)
	if scopeRel == "." || !belowRoot(scopeRel) {
		return MockupBundle{}, errors.New("a mockup HTML source cannot live directly in the session root; move it into its own subfolder (for example ai-docs/assets/mockups/YYMMDD/<topic>/)")
	}
	rootFile := filepath.Base(abs)
	if strings.HasPrefix(rootFile, ".") {
		return MockupBundle{}, fmt.Errorf("dot-named file %q is not allowed in a mockup bundle", rootFile)
	}
	// Verify every folder component of scopeRel below root is a plain non-dot directory.
	scopeParts := strings.Split(scopeRel, string(filepath.Separator))
	cur := root
	for i, part := range scopeParts {
		if strings.HasPrefix(part, ".") {
			return MockupBundle{}, fmt.Errorf("dot-named folder %q is not allowed in a mockup path", strings.Join(scopeParts[:i+1], "/"))
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return MockupBundle{}, errors.New("no such file")
			}
			return MockupBundle{}, fmt.Errorf("the mockup folder cannot be read: %v", err)
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return MockupBundle{}, fmt.Errorf("%s is a symlink or junction; a mockup folder and its parents below the session root %s must be plain", strings.Join(scopeParts[:i+1], "/"), root)
		}
		if !info.IsDir() {
			return MockupBundle{}, fmt.Errorf("%s is not a folder", strings.Join(scopeParts[:i+1], "/"))
		}
	}
	scopeDir := filepath.Join(root, scopeRel)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return MockupBundle{}, fmt.Errorf("the mockup folder cannot be read: %v", err)
	}
	realScope, err := filepath.EvalSymlinks(scopeDir)
	if err != nil {
		return MockupBundle{}, fmt.Errorf("the mockup folder cannot be read: %v", err)
	}
	realScopeRel, err := filepath.Rel(realRoot, realScope)
	if err != nil || !belowRoot(realScopeRel) {
		return MockupBundle{}, fmt.Errorf("the mockup folder is outside the session root %s; keep it inside it", root)
	}

	type member struct {
		rel  string
		data []byte
	}
	var members []member
	var totalBytes int64
	foundRoot := false

	var walk func(dir, relPrefix string) error
	walk = func(dir, relPrefix string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read mockup folder: %v", err)
		}
		for _, entry := range entries {
			name := entry.Name()
			relSlash := name
			if relPrefix != "" {
				relSlash = relPrefix + "/" + name
			}
			if strings.HasPrefix(name, ".") {
				return fmt.Errorf("dot-named entry %q is not allowed in a mockup bundle", relSlash)
			}
			childPath := filepath.Join(dir, name)
			linfo, err := os.Lstat(childPath)
			if err != nil {
				return fmt.Errorf("stat mockup entry %q: %v", relSlash, err)
			}
			if linfo.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				return fmt.Errorf("%s is a symlink or junction; mockup folders must contain only plain files and subfolders", relSlash)
			}
			if linfo.IsDir() {
				if err := walk(childPath, relSlash); err != nil {
					return err
				}
				continue
			}
			if !linfo.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file", relSlash)
			}
			if _, ok := MockupContentType(name); !ok {
				// Non-allowlisted regular files are skipped without counting toward caps.
				continue
			}
			if !ValidMockupRel(relSlash) {
				return fmt.Errorf("invalid mockup entry path %q", relSlash)
			}
			if len(members)+1 > MockupMaxFiles {
				return fmt.Errorf("mockup bundle exceeds the %d-file limit", MockupMaxFiles)
			}
			if linfo.Size() < 0 || totalBytes+linfo.Size() > MockupMaxBundleBytes {
				return fmt.Errorf("mockup bundle exceeds the %d-byte (8 MiB) limit", MockupMaxBundleBytes)
			}
			data, err := readMockupMemberFile(root, scopeDir, realScope, filepath.Join(scopeRel, filepath.FromSlash(relSlash)), childPath, relSlash, linfo, MockupMaxBundleBytes-totalBytes)
			if err != nil {
				return err
			}
			totalBytes += int64(len(data))
			if relSlash == rootFile {
				foundRoot = true
			}
			members = append(members, member{rel: relSlash, data: data})
		}
		return nil
	}
	if err := walk(scopeDir, ""); err != nil {
		return MockupBundle{}, err
	}
	if !foundRoot {
		return MockupBundle{}, fmt.Errorf("mockup root file %q was not found in %s", rootFile, filepath.ToSlash(scopeRel))
	}
	sort.Slice(members, func(i, j int) bool { return members[i].rel < members[j].rel })

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		hdr := &zip.FileHeader{
			Name:   m.rel,
			Method: zip.Deflate,
		}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return MockupBundle{}, fmt.Errorf("pack mockup %q: %v", m.rel, err)
		}
		if _, err := w.Write(m.data); err != nil {
			return MockupBundle{}, fmt.Errorf("pack mockup %q: %v", m.rel, err)
		}
	}
	if err := zw.Close(); err != nil {
		return MockupBundle{}, fmt.Errorf("finalize mockup bundle: %v", err)
	}
	zipBytes := buf.Bytes()
	sum := sha256.Sum256(zipBytes)
	return MockupBundle{
		RootFile:  rootFile,
		SHA256:    hex.EncodeToString(sum[:]),
		Zip:       zipBytes,
		ByteSize:  totalBytes,
		FileCount: len(members),
	}, nil
}

// readMockupMemberFile opens and reads one verified regular file inside a
// mockup subfolder with the same handle-pinning guarantees as ReadPanelSource.
func readMockupMemberFile(root, scopeDir, realScope, relFromRoot, childPath, relSlash string, linfo os.FileInfo, remaining int64) ([]byte, error) {
	if !os.SameFile(linfo, linfo) {
		return nil, fmt.Errorf("mockup file %s changed while opening; keep the folder inside the session root %s without symlinks or junctions", relSlash, root)
	}
	if panelSrcBeforeOpen != nil {
		panelSrcBeforeOpen()
	}
	f, err := openPanelSourceFile(childPath)
	if err != nil {
		if isSymlinkOpenErr(err) {
			return nil, fmt.Errorf("%s is a symlink or junction; mockup folders must contain only plain files and subfolders", relSlash)
		}
		return nil, fmt.Errorf("mockup file %s cannot be read: %v", relSlash, err)
	}
	defer f.Close()

	finfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("mockup file %s cannot be read: %v", relSlash, err)
	}
	if !finfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", relSlash)
	}
	if finfo.Size() < 0 || finfo.Size() > remaining {
		return nil, fmt.Errorf("mockup bundle exceeds the %d-byte (8 MiB) limit", MockupMaxBundleBytes)
	}
	if !os.SameFile(linfo, finfo) {
		return nil, fmt.Errorf("mockup file %s changed while opening; keep the folder inside the session root %s without symlinks or junctions", relSlash, root)
	}
	if err := verifyPanelSourceHandle(f, scopeDir); err != nil {
		return nil, err
	}
	if err := verifyPanelSourceHandle(f, root); err != nil {
		return nil, err
	}
	afterInfo, err := checkPanelSourceComponents(root, relFromRoot)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(finfo, afterInfo) {
		return nil, fmt.Errorf("mockup file %s changed while opening; keep the folder inside the session root %s without symlinks or junctions", relSlash, root)
	}
	realPath, err := filepath.EvalSymlinks(childPath)
	if err != nil {
		return nil, fmt.Errorf("mockup file %s cannot be read: %v", relSlash, err)
	}
	realRel, err := filepath.Rel(realScope, realPath)
	if err != nil || !belowRoot(realRel) {
		return nil, fmt.Errorf("mockup file %s resolves outside its mockup folder", relSlash)
	}
	realInfo, err := os.Lstat(realPath)
	if err != nil {
		return nil, fmt.Errorf("mockup file %s cannot be read: %v", relSlash, err)
	}
	if !os.SameFile(finfo, realInfo) {
		return nil, fmt.Errorf("mockup file %s changed while opening; keep the folder inside the session root %s without symlinks or junctions", relSlash, root)
	}
	data, err := io.ReadAll(io.LimitReader(f, remaining+1))
	if err != nil {
		return nil, fmt.Errorf("mockup file %s cannot be read: %v", relSlash, err)
	}
	if int64(len(data)) > remaining {
		return nil, fmt.Errorf("mockup bundle exceeds the %d-byte (8 MiB) limit", MockupMaxBundleBytes)
	}
	return data, nil
}

// DecodeMockupZip inflates and validates a mockup snapshot zip entirely in
// memory, enforcing the file-count, inflated-byte, path, and extension caps.
func DecodeMockupZip(zipBytes []byte) (map[string][]byte, int64, error) {
	r, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, 0, fmt.Errorf("invalid mockup bundle zip: %w", err)
	}
	if len(r.File) == 0 {
		return nil, 0, errors.New("mockup bundle is empty")
	}
	if len(r.File) > MockupMaxFiles {
		return nil, 0, fmt.Errorf("mockup bundle has %d files, over the %d limit", len(r.File), MockupMaxFiles)
	}
	files := make(map[string][]byte, len(r.File))
	var total int64
	for _, zf := range r.File {
		if zf.FileInfo().IsDir() || !zf.Mode().IsRegular() {
			return nil, 0, fmt.Errorf("mockup bundle entry %q is not a regular file", zf.Name)
		}
		if !ValidMockupRel(zf.Name) {
			return nil, 0, fmt.Errorf("mockup bundle entry %q has an invalid path", zf.Name)
		}
		if _, ok := MockupContentType(zf.Name); !ok {
			return nil, 0, fmt.Errorf("mockup bundle entry %q has a disallowed file type", zf.Name)
		}
		if _, dup := files[zf.Name]; dup {
			return nil, 0, fmt.Errorf("mockup bundle has duplicate entry %q", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, 0, fmt.Errorf("open mockup bundle entry %q: %w", zf.Name, err)
		}
		remaining := MockupMaxBundleBytes - total
		data, readErr := io.ReadAll(io.LimitReader(rc, remaining+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, 0, fmt.Errorf("read mockup bundle entry %q: %w", zf.Name, readErr)
		}
		if closeErr != nil {
			return nil, 0, fmt.Errorf("close mockup bundle entry %q: %w", zf.Name, closeErr)
		}
		if int64(len(data)) > remaining {
			return nil, 0, fmt.Errorf("mockup bundle inflates past the %d-byte (8 MiB) limit", MockupMaxBundleBytes)
		}
		total += int64(len(data))
		files[zf.Name] = data
	}
	return files, total, nil
}

// matchMockupBundles matches raw _mockups/<entryID>.zip blobs against the
// board's mockup panel entries. Orphans (no matching mockup entry) and blobs
// whose sha256 does not match the entry's recorded digest are skipped; a
// matching blob that fails DecodeMockupZip or lacks its root file returns an error.
func matchMockupBundles(raw map[string][]byte, state *State) (map[string]MockupBundle, error) {
	if len(raw) == 0 || state == nil {
		return nil, nil
	}
	mockupEntries := map[string]Entry{}
	for _, doc := range state.Topics {
		for _, e := range doc.Entries {
			if e.Kind == EntryPanel && e.Mockup != nil {
				mockupEntries[e.ID] = e
			}
		}
	}
	if len(mockupEntries) == 0 {
		return nil, nil
	}
	out := map[string]MockupBundle{}
	for id, zipBytes := range raw {
		entry, ok := mockupEntries[id]
		if !ok || entry.Mockup == nil {
			continue
		}
		sum := sha256.Sum256(zipBytes)
		gotSHA := hex.EncodeToString(sum[:])
		if gotSHA != entry.Mockup.SHA256 {
			continue
		}
		files, totalBytes, err := DecodeMockupZip(zipBytes)
		if err != nil {
			return nil, fmt.Errorf("mockup bundle %s: %w", id, err)
		}
		if _, hasRoot := files[entry.Mockup.Root]; !hasRoot {
			return nil, fmt.Errorf("mockup bundle %s is missing root file %q", id, entry.Mockup.Root)
		}
		out[id] = MockupBundle{
			EntryID:   id,
			RootFile:  entry.Mockup.Root,
			SHA256:    entry.Mockup.SHA256,
			Zip:       append([]byte(nil), zipBytes...),
			ByteSize:  totalBytes,
			FileCount: len(files),
			CreatedAt: entry.At,
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// readLegacyMockupBundles reads _mockups/<entryID>.zip files from a legacy
// board folder (such as a .bak-* backup) and validates matching bundles.
func readLegacyMockupBundles(dir string, state *State) (map[string]MockupBundle, error) {
	if dir == "" || state == nil {
		return nil, nil
	}
	mockDir := filepath.Join(dir, "_mockups")
	entries, err := os.ReadDir(mockDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	raw := map[string][]byte{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".zip") {
			continue
		}
		id := strings.TrimSuffix(name, ".zip")
		if folderNumber(id, "e") <= 0 {
			continue
		}
		full := filepath.Join(mockDir, name)
		linfo, err := os.Lstat(full)
		if err != nil || !linfo.Mode().IsRegular() {
			continue
		}
		if linfo.Size() > mockupMaxZipBytes {
			return nil, fmt.Errorf("legacy mockup bundle %s exceeds maximum bundle size", name)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		raw[id] = data
	}
	return matchMockupBundles(raw, state)
}

func mockupBundleMap(list []MockupBundle) map[string]MockupBundle {
	if len(list) == 0 {
		return nil
	}
	m := make(map[string]MockupBundle, len(list))
	for _, b := range list {
		if b.EntryID != "" {
			m[b.EntryID] = b
		}
	}
	return m
}
