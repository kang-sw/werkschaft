package wsdashboard

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of the pack install pipeline. The store's own caps (manifest, asset,
// pack, cache) apply on top of these through loadCompanionPack.
const (
	companionMarketplaceName   = "marketplace.json"
	companionMarketplaceSchema = "ws-companion-marketplace/v1"
	companionMarketplaceCap    = 256 << 10
	companionZipEntryMax       = 4096
	companionPackEntryMax      = 256
	companionZipRatioMax       = 100
	companionOperationCap      = 64 << 20
	companionImageSideMax      = 4096
	companionURLMax            = 2048
	companionArchivePathMax    = 512
)

var companionSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Error makes a pack error usable as a pipeline error. ID is empty for an
// archive-level or marketplace-level failure, which rejects the whole zip.
func (e companionPackError) Error() string { return e.Message }

func companionFail(id, where, format string, args ...any) error {
	return companionPackError{ID: id, Path: where, Message: fmt.Sprintf(format, args...)}
}

// companionInflate counts the bytes one install operation decompresses,
// across the marketplace, every pack, and every external pack zip.
type companionInflate struct{ left int }

func newCompanionInflate() *companionInflate { return &companionInflate{left: companionOperationCap} }

// companionArchive is an in-memory zip whose entries all passed the
// archive-level checks. Entries are addressed by their exact stored name; a
// directory entry is kept by its name without the trailing slash.
type companionArchive struct {
	files   map[string]*zip.File
	dirs    map[string]struct{}
	names   []string // every entry name (directories without the slash), sorted
	inflate *companionInflate
}

var errCompanionNotZip = errors.New("not a zip archive")

// openCompanionArchive opens data and applies the archive-level rules to
// every entry, including entries no pack lists: the entry cap, UTF-8 names
// without NUL, backslash, absolute, drive-letter, dot-dot or non-clean forms,
// unique names, and only unencrypted Store or Deflate file entries.
func openCompanionArchive(data []byte, inflate *companionInflate) (*companionArchive, error) {
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) && !bytes.HasPrefix(data, []byte("PK\x05\x06")) {
		return nil, errCompanionNotZip
	}
	// Refuse an oversized central directory before archive/zip allocates a
	// record for every entry it declares.
	if count, ok := companionZipDeclaredEntries(data); !ok {
		return nil, companionFail("", "", "not a valid zip archive")
	} else if count > companionZipEntryMax {
		return nil, companionFail("", "", "archive holds more than %d entries", companionZipEntryMax)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if errors.Is(err, zip.ErrInsecurePath) {
		return nil, companionFail("", "", "archive contains an unsafe entry name")
	}
	if err != nil {
		return nil, companionFail("", "", "not a valid zip archive")
	}
	if len(reader.File) > companionZipEntryMax {
		return nil, companionFail("", "", "archive holds more than %d entries", companionZipEntryMax)
	}
	a := &companionArchive{files: make(map[string]*zip.File), dirs: make(map[string]struct{}), inflate: inflate}
	for _, f := range reader.File {
		name, directory, err := companionEntryName(f.Name)
		if err != nil {
			return nil, companionFail("", companionDisplayName(f.Name), "%s", err.Error())
		}
		if _, seen := a.files[name]; seen {
			return nil, companionFail("", name, "duplicate entry name")
		}
		if _, seen := a.dirs[name]; seen {
			return nil, companionFail("", name, "duplicate entry name")
		}
		a.names = append(a.names, name)
		if directory {
			a.dirs[name] = struct{}{}
			continue
		}
		if f.Flags&0x1 != 0 {
			return nil, companionFail("", name, "encrypted entries are not allowed")
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, companionFail("", name, "only stored or deflated entries are allowed")
		}
		a.files[name] = f
	}
	sort.Strings(a.names)
	return a, nil
}

// companionEntryName returns the checked entry name, without the trailing
// slash of a directory entry.
func companionEntryName(raw string) (string, bool, error) {
	if !utf8.ValidString(raw) {
		return "", false, errors.New("entry names must be UTF-8")
	}
	if strings.ContainsRune(raw, 0) {
		return "", false, errors.New("entry names must not contain NUL")
	}
	if strings.ContainsRune(raw, '\\') {
		return "", false, errors.New("entry names must not contain backslashes")
	}
	directory := strings.HasSuffix(raw, "/")
	name := strings.TrimSuffix(raw, "/")
	if err := companionRelativePath(name); err != nil {
		return "", false, err
	}
	return name, directory, nil
}

// companionRelativePath accepts a clean, relative, slash-separated path.
func companionRelativePath(name string) error {
	switch {
	case name == "" || strings.HasPrefix(name, "/"):
		return errors.New("absolute entry names are not allowed")
	case len(name) >= 2 && name[1] == ':' && (name[0]|0x20) >= 'a' && (name[0]|0x20) <= 'z':
		return errors.New("drive-letter entry names are not allowed")
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return errors.New("entry names must not contain .. segments")
		}
	}
	if path.Clean(name) != name {
		return errors.New("entry names must be clean paths")
	}
	return nil
}

func companionDisplayName(raw string) string {
	if !utf8.ValidString(raw) {
		return strings.ToValidUTF8(raw, "?")
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, raw)
}

// companionZipDeclaredEntries reads the entry count from the end-of-central-
// directory record, located the way archive/zip locates it (the last
// signature whose comment fits before the end of the data), following the
// zip64 locator when the classic record holds the zip64 marker.
func companionZipDeclaredEntries(data []byte) (uint64, bool) {
	const endLen, locatorLen = 22, 20
	start := max(0, len(data)-(endLen+0xffff))
	block := data[start:]
	at := -1
	for i := len(block) - endLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(block[i:]) == 0x06054b50 {
			comment := int(binary.LittleEndian.Uint16(block[i+20:]))
			if i+endLen+comment <= len(block) {
				at = start + i
				break
			}
		}
	}
	if at < 0 {
		return 0, false
	}
	count := uint64(binary.LittleEndian.Uint16(data[at+10:]))
	size := binary.LittleEndian.Uint32(data[at+12:])
	offset := binary.LittleEndian.Uint32(data[at+16:])
	if count != 0xffff && size != 0xffffffff && offset != 0xffffffff {
		return count, true
	}
	locator := at - locatorLen
	if locator < 0 || binary.LittleEndian.Uint32(data[locator:]) != 0x07064b50 {
		return count, true
	}
	record := binary.LittleEndian.Uint64(data[locator+8:])
	if record > uint64(len(data)) || uint64(len(data))-record < 56 ||
		binary.LittleEndian.Uint32(data[record:]) != 0x06064b50 {
		return 0, false
	}
	return binary.LittleEndian.Uint64(data[record+32:]), true
}

// root finds the directory holding marker: the archive root, or the single
// top-level directory host archives wrap a repository in. It returns the
// prefix ("" or "<top>/") for entry names.
func (a *companionArchive) root(marker string) (string, bool) {
	if _, ok := a.files[marker]; ok {
		return "", true
	}
	top := ""
	for _, name := range a.names {
		first, _, nested := strings.Cut(name, "/")
		if top == "" {
			top = first
		}
		if first != top {
			return "", false
		}
		if !nested {
			if _, isDir := a.dirs[name]; !isDir {
				return "", false // a root-level file beside the directory
			}
		}
	}
	if top == "" {
		return "", false
	}
	if _, ok := a.files[top+"/"+marker]; !ok {
		return "", false
	}
	return top + "/", true
}

// read decompresses one file entry, counting the bytes actually produced:
// at most limit, at most companionZipRatioMax times its compressed size, and
// at most what is left of the operation's total.
func (a *companionArchive) read(f *zip.File, limit int) ([]byte, error) {
	ratioCap := uint64(companionZipRatioMax) * f.CompressedSize64
	allowed := min(uint64(limit), uint64(a.inflate.left), ratioCap)
	rc, err := f.Open()
	if err != nil {
		return nil, errors.New("cannot read archive entry")
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, int64(allowed)+1))
	if uint64(len(data)) > allowed {
		switch {
		case allowed == uint64(limit):
			return nil, errCompanionFileLimit
		case allowed == ratioCap:
			return nil, fmt.Errorf("compression ratio exceeds %d:1", companionZipRatioMax)
		default:
			return nil, errors.New("install operation byte limit exceeded")
		}
	}
	if err != nil {
		return nil, errors.New("corrupt archive entry")
	}
	a.inflate.left -= len(data)
	return data, nil
}

// companionPackFiles are the file entries of one pack directory, keyed by
// file name, after the pack-level entry rules passed.
type companionPackFiles map[string]*zip.File

// pack applies the pack-level rules to the entries under prefix: at most
// companionPackEntryMax entries, regular files only, no files in nested
// directories (directory entries are tolerated and skipped), no names that
// differ only by case, only pack.json and raster names, and no Windows
// reserved device names.
func (a *companionArchive) pack(id, prefix string) (companionPackFiles, error) {
	files := make(companionPackFiles)
	folded := make(map[string]struct{})
	count := 0
	self := strings.TrimSuffix(prefix, "/")
	for _, name := range a.names {
		if prefix != "" && (name == self || !strings.HasPrefix(name, prefix)) {
			continue
		}
		if count++; count > companionPackEntryMax {
			return nil, companionFail(id, "", "pack directory holds more than %d entries", companionPackEntryMax)
		}
		if _, isDir := a.dirs[name]; isDir {
			continue
		}
		rel := name[len(prefix):]
		f := a.files[name]
		if strings.Contains(rel, "/") {
			return nil, companionFail(id, rel, "files in nested directories are not allowed")
		}
		mode := f.Mode()
		if mode&fs.ModeSymlink != 0 {
			return nil, companionFail(id, rel, "symbolic links are not allowed")
		}
		if !mode.IsRegular() {
			return nil, companionFail(id, rel, "only regular files are allowed")
		}
		lower := strings.ToLower(rel)
		if _, seen := folded[lower]; seen {
			return nil, companionFail(id, rel, "file names must not differ only by case")
		}
		folded[lower] = struct{}{}
		if rel != companionManifestName && !validCompanionRaster(rel) {
			return nil, companionFail(id, rel, "only pack.json and .png or .webp files are allowed")
		}
		if companionReservedName(rel) {
			return nil, companionFail(id, rel, "file name is a reserved device name on Windows")
		}
		files[rel] = f
	}
	return files, nil
}

// companionReservedName reports a Windows reserved device name, with or
// without an extension, in any case.
func companionReservedName(name string) bool {
	base, _, _ := strings.Cut(strings.ToLower(name), ".")
	switch base {
	case "con", "prn", "aux", "nul":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) &&
		base[3] >= '1' && base[3] <= '9'
}

// prepare validates one pack exactly as the scanner would load it, under the
// given cache budget. The referenced file list loadCompanionPack returns is
// the allow-list: any other file fails the pack, and every raster must carry
// the header of its extension within the dimension cap.
func (a *companionArchive) prepare(id string, files companionPackFiles, budget int) (*companionPack, error) {
	if companionReservedName(id) {
		return nil, companionFail(id, "", "pack ID is a reserved device name on Windows")
	}
	pack, where, err := loadCompanionPack(id, "local", budget, func(name string, limit int) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, errors.New("file is missing")
		}
		return a.read(f, limit)
	})
	if err != nil {
		return nil, companionFail(id, where, "%s", err.Error())
	}
	for _, name := range sortedCompanionKeys(files) {
		if _, referenced := pack.assets[name]; name != companionManifestName && !referenced {
			return nil, companionFail(id, name, "file is not referenced by pack.json")
		}
	}
	for _, name := range sortedCompanionKeys(pack.assets) {
		if err := companionCheckRaster(name, pack.assets[name]); err != nil {
			return nil, companionFail(id, name, "%s", err.Error())
		}
	}
	return pack, nil
}

// companionCheckRaster checks the header of a raster against its extension
// and the dimension cap. The browser performs the full decode.
func companionCheckRaster(name string, data []byte) error {
	var width, height int
	if strings.HasSuffix(name, ".png") {
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return errors.New("file is not a PNG image")
		}
		width, height = config.Width, config.Height
	} else {
		var err error
		if width, height, err = companionWebPSize(data); err != nil {
			return err
		}
	}
	if width < 1 || height < 1 || width > companionImageSideMax || height > companionImageSideMax {
		return fmt.Errorf("image dimensions must be between 1 and %d pixels", companionImageSideMax)
	}
	return nil
}

// companionWebPSize reads the canvas size from a WebP RIFF header (VP8, VP8L
// or VP8X chunk) without decoding the image.
func companionWebPSize(data []byte) (int, int, error) {
	invalid := errors.New("file is not a WebP image")
	if len(data) < 25 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, invalid
	}
	switch string(data[12:16]) {
	case "VP8 ":
		if len(data) < 30 || data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, invalid
		}
		return int(binary.LittleEndian.Uint16(data[26:]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:]) & 0x3fff), nil
	case "VP8L":
		if data[20] != 0x2f {
			return 0, 0, invalid
		}
		bits := binary.LittleEndian.Uint32(data[21:])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8X":
		if len(data) < 30 {
			return 0, 0, invalid
		}
		width := 1 + int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		height := 1 + int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return width, height, nil
	}
	return 0, 0, invalid
}

// companionMarketplace is a parsed ws-companion-marketplace/v1 manifest.
// Fields are read by exact key; unknown fields are ignored.
type companionMarketplace struct {
	Name   string                     `json:"name"`
	Owner  *companionMarketplaceOwner `json:"owner,omitempty"`
	Packs  []companionMarketplaceEntry
	prefix string // entry-name prefix of the directory holding marketplace.json
}

type companionMarketplaceOwner struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// companionMarketplaceEntry is one listed pack. Exactly one of Path (a
// directory in the same zip) and URL with SHA256 (an external pack zip) is
// set. Err holds an entry-level failure; the rest of the listing stays usable.
type companionMarketplaceEntry struct {
	ID          string
	Name        string
	Version     string
	Author      string
	License     string
	Description string
	Path        string
	URL         string
	SHA256      string
	Err         error
}

// marketplace reads and validates marketplace.json from the archive root or
// its single top-level directory.
func (a *companionArchive) marketplace() (companionMarketplace, error) {
	prefix, ok := a.root(companionMarketplaceName)
	if !ok {
		return companionMarketplace{}, companionFail("", companionMarketplaceName, "marketplace.json is missing from the archive root")
	}
	raw, err := a.read(a.files[prefix+companionMarketplaceName], companionMarketplaceCap)
	if errors.Is(err, errCompanionFileLimit) {
		err = errors.New("marketplace.json byte limit exceeded")
	}
	if err != nil {
		return companionMarketplace{}, companionFail("", companionMarketplaceName, "%s", err.Error())
	}
	m, err := parseCompanionMarketplace(raw)
	if err != nil {
		return m, companionFail("", companionMarketplaceName, "%s", err.Error())
	}
	m.prefix = prefix
	return m, nil
}

func parseCompanionMarketplace(raw []byte) (companionMarketplace, error) {
	var m companionMarketplace
	if !utf8.Valid(raw) {
		return m, errors.New("marketplace.json must be UTF-8")
	}
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return m, err
	}
	var schema string
	var owner companionObject
	var packs []json.RawMessage
	if err := obj.fields(map[string]any{"schema": &schema, "name": &m.Name, "owner": &owner, "packs": &packs}); err != nil {
		return m, err
	}
	if schema != companionMarketplaceSchema {
		return m, fmt.Errorf("schema must be %s", companionMarketplaceSchema)
	}
	if err := companionText("name", m.Name, 64, true); err != nil {
		return m, err
	}
	if owner != nil {
		m.Owner = &companionMarketplaceOwner{}
		if err := owner.fields(map[string]any{"name": &m.Owner.Name, "url": &m.Owner.URL}); err != nil {
			return m, fmt.Errorf("owner: %w", err)
		}
		if err := companionText("owner.name", m.Owner.Name, 64, false); err != nil {
			return m, err
		}
		if m.Owner.URL != "" {
			if err := companionHTTPSURL(m.Owner.URL); err != nil {
				return m, fmt.Errorf("owner.url: %w", err)
			}
		}
	}
	if _, present := obj["packs"]; !present {
		return m, errors.New("packs is required")
	}
	if len(packs) > companionMaxPacks {
		return m, fmt.Errorf("a marketplace lists at most %d packs", companionMaxPacks)
	}
	seen := make(map[string]struct{}, len(packs))
	for i, raw := range packs {
		entry := parseCompanionMarketplaceEntry(raw)
		if entry.Err == nil {
			if _, dup := seen[entry.ID]; dup {
				return m, fmt.Errorf("pack ID %s is listed more than once", entry.ID)
			}
			seen[entry.ID] = struct{}{}
		}
		if entry.Err != nil {
			entry.Err = companionFail(entry.ID, fmt.Sprintf("packs[%d]", i), "%s", entry.Err.Error())
		}
		m.Packs = append(m.Packs, entry)
	}
	return m, nil
}

func parseCompanionMarketplaceEntry(raw json.RawMessage) companionMarketplaceEntry {
	var e companionMarketplaceEntry
	obj, err := parseCompanionObject(raw)
	if err != nil {
		e.Err = err
		return e
	}
	if e.Err = obj.fields(map[string]any{
		"id": &e.ID, "name": &e.Name, "version": &e.Version, "author": &e.Author, "license": &e.License,
		"description": &e.Description, "path": &e.Path, "url": &e.URL, "sha256": &e.SHA256,
	}); e.Err != nil {
		return e
	}
	if !companionPackID.MatchString(e.ID) {
		e.Err = errors.New("id must be 1-64 lowercase letters, digits, - or _")
		return e
	}
	switch {
	case e.ID == companionDefaultID || e.ID == "auto":
		e.Err = errors.New("pack ID is reserved")
	case companionReservedName(e.ID):
		e.Err = errors.New("pack ID is a reserved device name on Windows")
	}
	if e.Err != nil {
		return e
	}
	for _, field := range []struct {
		name, value string
		max         int
		required    bool
	}{
		{"name", e.Name, 64, true}, {"version", e.Version, 32, true}, {"author", e.Author, 64, false},
		{"license", e.License, 64, false}, {"description", e.Description, 280, false},
	} {
		if e.Err = companionText(field.name, field.value, field.max, field.required); e.Err != nil {
			return e
		}
	}
	switch {
	case (e.Path == "") == (e.URL == ""):
		e.Err = errors.New("exactly one of path and url is required")
	case e.Path != "":
		if e.SHA256 != "" {
			e.Err = errors.New("sha256 applies only to a url entry")
		} else if len(e.Path) > companionArchivePathMax || strings.ContainsAny(e.Path, "\\\x00") || companionRelativePath(e.Path) != nil || e.Path == "." {
			e.Err = errors.New("path must be a clean relative directory inside the archive")
		}
	default:
		if err := companionHTTPSURL(e.URL); err != nil {
			e.Err = fmt.Errorf("url: %w", err)
		} else if !companionSHA256.MatchString(e.SHA256) {
			e.Err = errors.New("a url entry requires sha256 as 64 lowercase hex characters")
		}
	}
	return e
}

// companionText checks one untrusted display string: a code-point cap and no
// control characters.
func companionText(field, value string, limit int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > limit {
		return fmt.Errorf("%s exceeds %d characters", field, limit)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

// companionHTTPSURL accepts an absolute https URL with a host, without
// credentials or control characters, of at most companionURLMax bytes.
func companionHTTPSURL(raw string) error {
	if len(raw) > companionURLMax {
		return fmt.Errorf("URL exceeds %d characters", companionURLMax)
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("URL must not contain spaces or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return errors.New("URL must be an absolute https URL")
	}
	if u.User != nil {
		return errors.New("URL must not carry credentials")
	}
	return nil
}
