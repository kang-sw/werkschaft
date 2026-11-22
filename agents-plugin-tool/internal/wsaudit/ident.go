package wsaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const (
	// IdentifierMaxBytes bounds an identifier kept verbatim, in encoded bytes.
	IdentifierMaxBytes = 256
	// DigestPrefix starts every digested identifier. A raw identifier that
	// itself begins with it is always digested, so the kept and digested
	// forms are disjoint.
	DigestPrefix = "sha256:"
)

// NormalizeID is the one shared identifier normalization (ring-store
// Decision 3): a raw value of at most IdentifierMaxBytes encoded bytes that
// does not begin with DigestPrefix is kept verbatim; any other non-empty
// value becomes DigestPrefix plus the hex sha256 of its raw bytes.
//
// It is applied once, to raw harness input: by the store's ingestion and by
// every adapter's ring call ID (wslive ToolCallArgs.ID) and audit call_id. A
// consumer that already holds a normalized key never normalizes it again.
// It runs whatever the surrounding metadata size, so a call and its result
// always normalize the same way. The empty string stays empty.
func NormalizeID(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, DigestPrefix) && encodedLen(raw) <= IdentifierMaxBytes {
		return raw
	}
	sum := sha256.Sum256([]byte(raw))
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// encodedLen is the length of s as the envelope's JSON encoder writes it,
// without the surrounding quotes.
func encodedLen(s string) int {
	// A plain ASCII string without characters JSON escapes encodes as itself;
	// skip the encoder for that common case.
	plain := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x7f || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			plain = false
			break
		}
	}
	if plain {
		return len(s)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return len(s)
	}
	return len(raw) - 2
}
