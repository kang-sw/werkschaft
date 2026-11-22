package wsdashboard

import "strings"

// companionExtendedPath is the extended-length (\\?\) form of p, an
// absolute, cleaned Windows path, when p is too long for a plain Win32 open:
// the conversion Go's os package applies to its own opens (fixLongPath), which
// the companion root's direct CreateFile would otherwise skip, so a deeply
// nested config home loses its local packs where MAX_PATH is enforced. A short
// path, one already extended, and a device path are returned unchanged.
func companionExtendedPath(p string) string {
	// 248 is MAX_PATH minus room for an 8.3 name, Go's threshold too.
	if len(p) < 248 {
		return p
	}
	switch {
	case strings.HasPrefix(p, `\\?\`), strings.HasPrefix(p, `\??\`), strings.HasPrefix(p, `\\.\`):
		return p
	case strings.HasPrefix(p, `\\`):
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}
