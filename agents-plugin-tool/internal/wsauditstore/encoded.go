package wsauditstore

import (
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// normalizeIDs applies wsaudit.NormalizeID once, at ingestion, to every
// identifier field of e. An absent optional identifier stays absent.
func normalizeIDs(e *wsaudit.Envelope) {
	e.ID = wsaudit.NormalizeID(e.ID)
	e.Session = wsaudit.NormalizeID(e.Session)
	e.Harness = wsaudit.NormalizeID(e.Harness)
	e.Subkind = wsaudit.NormalizeID(e.Subkind)
	e.Turn = wsaudit.NormalizeID(e.Turn)
	e.CallID = wsaudit.NormalizeID(e.CallID)
	e.ParentID = wsaudit.NormalizeID(e.ParentID)
	e.AgentID = wsaudit.NormalizeID(e.AgentID)
}

// normalizeSourceID bounds the free-form parts of a source identity the same
// way.
func normalizeSourceID(id wsaudit.SourceID) wsaudit.SourceID {
	id.Source = wsaudit.NormalizeID(id.Source)
	id.Key = wsaudit.NormalizeID(id.Key)
	return id
}

// encodedLen is the byte length of s inside a JSON string as encoding/json
// writes it (HTML escaping on), without the surrounding quotes: an invalid
// UTF-8 byte becomes �, U+2028, U+2029 and the HTML characters become
// \u escapes, and control characters escape.
func encodedLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		w, size := encodedRuneLen(s[i:])
		n += w
		i += size
	}
	return n
}

// encodedRuneLen is the encoded length of the first rune of s and that rune's
// byte size in s.
func encodedRuneLen(s string) (encoded, size int) {
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
