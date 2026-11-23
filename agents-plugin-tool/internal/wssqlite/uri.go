package wssqlite

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FileURI turns a filesystem path into a literal SQLite file: URI. The
// driver splits a plain DSN at its first '?', and SQLite interprets '?',
// '#' and '%' in a URI, so escape those bytes before either parser sees them.
func FileURI(path string) string {
	// Resolve relative paths the same way as the callers' filesystem operations.
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return fileURI(path)
}

func fileURI(path string) string {
	p := path
	// SQLite's Windows VFS expects an extended-length path with native
	// backslashes. Escape them so SQLite decodes the original path verbatim.
	if !strings.HasPrefix(p, `\\?\`) {
		p = filepath.ToSlash(path)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p // C:/x becomes /C:/x, which SQLite's Windows VFS accepts
		} else if strings.HasPrefix(p, "//") {
			p = "//" + p // a UNC path keeps an empty URI authority
		}
	}
	var b strings.Builder
	b.WriteString("file:")
	for i := 0; i < len(p); i++ {
		c := p[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~/:", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
