package werk

import (
	"regexp"
	"strings"

	"github.com/kang-sw/devenv/internal/wsredact"
)

// bearerToken matches an Authorization-style credential outside a header
// line, which wsredact's rules (header line, key shapes, secret-named values)
// do not cover.
var bearerToken = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{6,}`)

// Scrub masks credentials in text that came from the provider before it
// enters an error, a line, or an audit record: the shapes wsredact knows
// (provider keys, tokens, Authorization values, secret-named values), a
// bearer credential anywhere, and each secret named exactly (the token the
// request itself carried). It is best-effort, as wsredact is.
func Scrub(s string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) >= 8 {
			s = strings.ReplaceAll(s, secret, wsredact.Mask)
		}
	}
	s = wsredact.Secrets(s)
	return bearerToken.ReplaceAllString(s, "${1}"+wsredact.Mask)
}
