package wsaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

const (
	// MaxIDEncodedBytes is the longest JSON-encoded identifier NormalizeID
	// keeps verbatim.
	MaxIDEncodedBytes = 256
	// DigestPrefix marks an identifier NormalizeID replaced with its digest.
	DigestPrefix = "sha256:"
)

// NormalizeID is the one shared identifier normalization: a raw value of at
// most MaxIDEncodedBytes JSON-encoded bytes that does not begin with
// DigestPrefix is kept verbatim; any other raw value becomes
// "sha256:<hex>" of its raw bytes. The kept and digested forms are disjoint,
// so a literal ID spelled like a digest never equals another ID's key.
//
// Apply it exactly once, to raw harness input: the audit store at ingestion
// (id, session, harness, subkind, turn, call_id, parent_id, agent_id) and
// each adapter for the ring's tool call ID. It is deliberately not
// idempotent; a lookup takes an already normalized key and never
// re-normalizes it.
func NormalizeID(raw string) string {
	if !strings.HasPrefix(raw, DigestPrefix) && EncodedLen(raw) <= MaxIDEncodedBytes {
		return raw
	}
	sum := sha256.Sum256([]byte(raw))
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// NormalizeIDs applies NormalizeID to every identifier field of e.
func NormalizeIDs(e *Envelope) {
	e.ID = NormalizeID(e.ID)
	e.Session = NormalizeID(e.Session)
	e.Harness = NormalizeID(e.Harness)
	e.Subkind = normalizeOptional(e.Subkind)
	e.Turn = normalizeOptional(e.Turn)
	e.CallID = normalizeOptional(e.CallID)
	e.ParentID = normalizeOptional(e.ParentID)
	e.AgentID = normalizeOptional(e.AgentID)
	if e.Source != nil {
		src := *e.Source
		src.Source = NormalizeID(src.Source)
		e.Source = &src
	}
}

// normalizeOptional keeps an absent optional identifier absent.
func normalizeOptional(raw string) string {
	if raw == "" {
		return ""
	}
	return NormalizeID(raw)
}

// EncodedLen is the byte length of s inside a JSON string as encoding/json
// writes it (HTML escaping on), without the surrounding quotes: an invalid
// UTF-8 byte becomes �, U+2028 and U+2029 and the HTML characters
// become \u escapes, and control characters escape.
func EncodedLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		w, size := EncodedRuneLen(s[i:])
		n += w
		i += size
	}
	return n
}

// EncodedRuneLen is the encoded length of the first rune of s and that
// rune's byte size in s.
func EncodedRuneLen(s string) (encoded, size int) {
	b := s[0]
	if b < utf8.RuneSelf {
		switch {
		case b == '"' || b == '\\' || b == '\n' || b == '\r' || b == '\t' || b == '\b' || b == '\f':
			return 2, 1
		case b < 0x20 || b == '<' || b == '>' || b == '&':
			return 6, 1
		default:
			return 1, 1
		}
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return 6, 1
	}
	if r == ' ' || r == ' ' {
		return 6, size
	}
	return size, size
}
