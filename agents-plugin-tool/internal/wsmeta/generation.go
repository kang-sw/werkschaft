package wsmeta

import "strings"

// btimeTag introduces the optional birth-time component of a generation
// token (gen_*.go).
const btimeTag = ":btime:"

// sameGeneration reports whether two generation tokens name the same
// incarnation. A token is an identity, optionally followed by a birth time.
// The identities must be equal; the birth times are compared only when both
// tokens carry one, because a pass in an environment that cannot read it
// (Linux without statx) yields the identity alone. Reading such a token as
// another incarnation would discard a session's journal rows, fenced
// originals included.
func sameGeneration(a, b string) bool {
	if a == b {
		return true
	}
	ai, at, aok := strings.Cut(a, btimeTag)
	bi, bt, bok := strings.Cut(b, btimeTag)
	return ai == bi && (!aok || !bok || at == bt)
}
