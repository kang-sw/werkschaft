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

// sameIncarnation reports whether two entry identity tokens (ident_*.go)
// name the same incarnation of a file, strictly: the tokens are equal and
// carry evidence that tells one incarnation from a later one at the same
// device and inode. That is the Windows file index, whose NTFS sequence
// number changes when the record is reused, or a POSIX birth time. Device
// and inode alone can be reused by a new file once the old one is deleted,
// so they never prove a witness (re-review fd1cff5e).
func sameIncarnation(a, b string) bool {
	return a == b && hasIncarnation(a)
}

// hasIncarnation reports whether an entry identity token carries
// incarnation evidence (sameIncarnation).
func hasIncarnation(id string) bool {
	return strings.HasPrefix(id, "id:") || strings.Contains(id, btimeTag)
}
