package wspiruntime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	archiveTopPrefix = "pi"
	maxFileBytes     = 512 << 20 // 512 MiB per file (covers Bun standalone binary + assets)
	maxTotalBytes    = 1 << 30   // 1 GiB total extracted budget
)

// extractArchive unpacks archivePath (of kind ArchiveTarGz or ArchiveZip) into
// stagingDir, stripping the single top-level "pi/" directory and enforcing
// strict path-traversal, symlink-escape, and special-node defenses.
func extractArchive(archivePath string, kind ArchiveKind, stagingDir, binaryName string) error {
	switch kind {
	case ArchiveTarGz:
		if err := extractTarGz(archivePath, stagingDir); err != nil {
			return err
		}
	case ArchiveZip:
		if err := extractZip(archivePath, stagingDir); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported archive kind %q", kind)
	}

	binPath := filepath.Join(stagingDir, binaryName)
	info, err := os.Lstat(binPath)
	if err != nil {
		return fmt.Errorf("extracted archive is missing %s: %w", binaryName, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("extracted %s is not a regular file", binaryName)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(binPath, 0o755); err != nil {
			return fmt.Errorf("chmod %s: %w", binaryName, err)
		}
	}
	return nil
}

func extractTarGz(archivePath, stagingDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var totalBytes int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeGNULongName || hdr.Typeflag == tar.TypeGNULongLink {
			continue
		}

		rel, skip, err := normalizeArchiveEntryPath(hdr.Name)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		dest, err := safeJoin(stagingDir, rel)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", rel, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 || hdr.Size > maxFileBytes {
				return fmt.Errorf("archive entry %q size %d exceeds limit", hdr.Name, hdr.Size)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir parent for %s: %w", rel, err)
			}
			n, err := writeLimitedFile(dest, tr, sanitizedFileMode(os.FileMode(hdr.Mode)), maxFileBytes)
			if err != nil {
				return fmt.Errorf("extract %s: %w", rel, err)
			}
			totalBytes += n
			if totalBytes > maxTotalBytes {
				return fmt.Errorf("archive exceeds total extraction limit (%d bytes)", maxTotalBytes)
			}
		case tar.TypeSymlink:
			if err := validateSymlinkTarget(stagingDir, dest, hdr.Linkname); err != nil {
				return fmt.Errorf("unsafe symlink %q -> %q: %w", hdr.Name, hdr.Linkname, err)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir parent for symlink %s: %w", rel, err)
			}
			_ = os.Remove(dest)
			if err := os.Symlink(hdr.Linkname, dest); err != nil {
				return fmt.Errorf("create symlink %s: %w", rel, err)
			}
		case tar.TypeLink:
			linkRel, linkSkip, err := normalizeArchiveEntryPath(hdr.Linkname)
			if err != nil || linkSkip {
				return fmt.Errorf("unsafe hardlink %q -> %q", hdr.Name, hdr.Linkname)
			}
			target, err := safeJoin(stagingDir, linkRel)
			if err != nil {
				return fmt.Errorf("unsafe hardlink %q -> %q: %w", hdr.Name, hdr.Linkname, err)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir parent for hardlink %s: %w", rel, err)
			}
			_ = os.Remove(dest)
			if err := os.Link(target, dest); err != nil {
				return fmt.Errorf("create hardlink %s: %w", rel, err)
			}
		default:
			return fmt.Errorf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)
		}
	}
	return nil
}

func extractZip(archivePath, stagingDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	var totalBytes int64
	for _, f := range zr.File {
		rel, skip, err := normalizeArchiveEntryPath(f.Name)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		dest, err := safeJoin(stagingDir, rel)
		if err != nil {
			return err
		}

		mode := f.Mode()
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", rel, err)
			}
			continue
		}
		if mode&os.ModeSymlink != 0 {
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("open zip symlink %s: %w", rel, err)
			}
			raw, readErr := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			if readErr != nil {
				return fmt.Errorf("read zip symlink %s: %w", rel, readErr)
			}
			linkTarget := strings.TrimSpace(string(raw))
			if err := validateSymlinkTarget(stagingDir, dest, linkTarget); err != nil {
				return fmt.Errorf("unsafe zip symlink %q -> %q: %w", f.Name, linkTarget, err)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir parent for symlink %s: %w", rel, err)
			}
			_ = os.Remove(dest)
			if err := os.Symlink(linkTarget, dest); err != nil {
				return fmt.Errorf("create symlink %s: %w", rel, err)
			}
			continue
		}
		if mode&(os.ModeDevice|os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
			return fmt.Errorf("unsupported special zip entry %q", f.Name)
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("mkdir parent for %s: %w", rel, err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", rel, err)
		}
		n, writeErr := writeLimitedFile(dest, rc, sanitizedFileMode(mode), maxFileBytes)
		_ = rc.Close()
		if writeErr != nil {
			return fmt.Errorf("extract %s: %w", rel, writeErr)
		}
		totalBytes += n
		if totalBytes > maxTotalBytes {
			return fmt.Errorf("archive exceeds total extraction limit (%d bytes)", maxTotalBytes)
		}
	}
	return nil
}

// normalizeArchiveEntryPath rejects absolute paths, Windows drive prefixes,
// and ".." traversal segments, then strips the required single top-level "pi/"
// directory. It returns skip=true for the bare "pi" / "pi/" directory entry.
func normalizeArchiveEntryPath(raw string) (rel string, skip bool, err error) {
	if raw == "" {
		return "", true, nil
	}
	if strings.ContainsRune(raw, 0) {
		return "", false, fmt.Errorf("archive entry contains NUL byte: %q", raw)
	}
	norm := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(norm, "/") || hasWindowsDrivePrefix(norm) {
		return "", false, fmt.Errorf("archive entry has absolute or drive-prefixed path: %q", raw)
	}
	for strings.HasPrefix(norm, "./") {
		norm = strings.TrimPrefix(norm, "./")
	}
	norm = strings.TrimSuffix(norm, "/")
	if norm == "" || norm == "." {
		return "", true, nil
	}
	parts := strings.Split(norm, "/")
	for _, seg := range parts {
		if seg == ".." {
			return "", false, fmt.Errorf("archive entry escapes target directory: %q", raw)
		}
		if hasWindowsDrivePrefix(seg) {
			return "", false, fmt.Errorf("archive entry has drive prefix segment: %q", raw)
		}
	}
	if parts[0] != archiveTopPrefix {
		return "", false, fmt.Errorf("archive entry %q is outside expected %q/ top-level directory", raw, archiveTopPrefix)
	}
	if len(parts) == 1 {
		return "", true, nil
	}
	trimmed := strings.Join(parts[1:], "/")
	if trimmed == "" || trimmed == "." {
		return "", true, nil
	}
	return trimmed, false, nil
}

func hasWindowsDrivePrefix(s string) bool {
	if len(s) >= 2 && s[1] == ':' {
		c := s[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return filepath.VolumeName(s) != ""
}

func safeJoin(root, rel string) (string, error) {
	cleanRoot := filepath.Clean(root)
	dest := filepath.Join(cleanRoot, filepath.FromSlash(rel))
	if !isWithinDir(cleanRoot, dest) {
		return "", fmt.Errorf("archive entry %q escapes staging root", rel)
	}
	return dest, nil
}

func isWithinDir(root, candidate string) bool {
	cleanRoot := filepath.Clean(root)
	cleanCand := filepath.Clean(candidate)
	r, err := filepath.Rel(cleanRoot, cleanCand)
	if err != nil {
		return false
	}
	if r == "." {
		return false
	}
	if r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return false
	}
	return true
}

func validateSymlinkTarget(stagingDir, linkPath, target string) error {
	if target == "" {
		return errors.New("empty symlink target")
	}
	norm := strings.ReplaceAll(target, `\`, "/")
	if strings.HasPrefix(norm, "/") || hasWindowsDrivePrefix(norm) {
		return errors.New("absolute symlink target")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), filepath.FromSlash(norm)))
	if !isWithinDir(stagingDir, resolved) {
		return errors.New("symlink target escapes staging directory")
	}
	return nil
}

func sanitizedFileMode(mode os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o644
	}
	// Strip setuid/setgid/sticky bits and ensure owner read/write.
	out := (perm & 0o755) | 0o600
	if perm&0o111 != 0 {
		out |= 0o100
	}
	return out
}

func writeLimitedFile(dest string, r io.Reader, mode os.FileMode, limit int64) (int64, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return 0, copyErr
	}
	if n > limit {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("file exceeds maximum allowed size (%d bytes)", limit)
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return 0, closeErr
	}
	return n, nil
}
