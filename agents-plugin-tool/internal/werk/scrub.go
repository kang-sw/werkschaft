package werk

import "github.com/kang-sw/devenv/internal/wscompact"

// Scrub masks credentials in text that came from the provider before it
// enters an error, a line, or an audit record: the shapes wsredact knows
// (provider keys, tokens, Authorization values, secret-named values), a
// bearer credential anywhere, and each secret named exactly (the token the
// request itself carried). It is best-effort, as wsredact is.
func Scrub(s string, secrets ...string) string {
	return wscompact.Scrub(s, secrets...)
}
