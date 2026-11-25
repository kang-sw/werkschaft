package wsinbox

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	textRefOpenPrefix = `<textref id="#`
	textRefMid        = `" bytes="`
	textRefOpenEnd    = `">`
	textRefClose      = `</textref>`
)

// TextRef is one length-framed pasted text attachment extracted from message
// text: <textref id="#n" bytes="N">...</textref>, where N is the exact UTF-8
// byte length of Body. No body escaping is performed.
type TextRef struct {
	Num  int
	Body string
}

// TextRefPart is a lossless segment of message text. Text always holds the
// original bytes; TextRef is non-nil only for a complete, length-verified
// <textref> block. Malformed, mismatched, or truncated tags remain plain text.
type TextRefPart struct {
	Text    string
	TextRef *TextRef
}

var textMarkerPattern = regexp.MustCompile(`\[Text #([1-9][0-9]{0,5})\]`)

// TextMarker returns the inline marker for text attachment n.
func TextMarker(n int) string {
	return fmt.Sprintf("[Text #%d]", n)
}

// TextMarkers calls fn for each well-formed "[Text #n]" marker in text.
func TextMarkers(text string, fn func(start, end, n int)) {
	for _, m := range textMarkerPattern.FindAllStringSubmatchIndex(text, -1) {
		n, err := strconv.Atoi(text[m[2]:m[3]])
		if err != nil || n < 1 || n > maxImageNum {
			continue
		}
		fn(m[0], m[1], n)
	}
}

// TextRefBlock serializes a length-framed <textref> block for inline T0
// transport inside Parts text. Body is written verbatim with its exact UTF-8
// byte length in bytes="N".
func TextRefBlock(num int, body string) (string, error) {
	if num < 1 || num > maxImageNum {
		return "", fmt.Errorf("%w: textref number %d", ErrInvalid, num)
	}
	return fmt.Sprintf(`<textref id="#%d" bytes="%d">%s</textref>`, num, len(body), body), nil
}

// parseTextRefAt parses a complete length-framed <textref> starting at text[0].
// It returns the parsed ref, the total byte length consumed, and true only when
// the header, exact N-byte body, and immediate </textref> closing tag all match.
func parseTextRefAt(text string) (TextRef, int, bool) {
	if !strings.HasPrefix(text, textRefOpenPrefix) {
		return TextRef{}, 0, false
	}
	pos := len(textRefOpenPrefix)
	numStart := pos
	for pos < len(text) && pos-numStart < 6 && text[pos] >= '0' && text[pos] <= '9' {
		pos++
	}
	if pos == numStart || text[numStart] == '0' {
		return TextRef{}, 0, false
	}
	num, err := strconv.Atoi(text[numStart:pos])
	if err != nil || num < 1 || num > maxImageNum {
		return TextRef{}, 0, false
	}
	if !strings.HasPrefix(text[pos:], textRefMid) {
		return TextRef{}, 0, false
	}
	pos += len(textRefMid)
	byteStart := pos
	for pos < len(text) && text[pos] >= '0' && text[pos] <= '9' {
		pos++
	}
	if pos == byteStart || (pos-byteStart > 1 && text[byteStart] == '0') {
		return TextRef{}, 0, false
	}
	nBytes, err := strconv.Atoi(text[byteStart:pos])
	if err != nil || nBytes < 0 {
		return TextRef{}, 0, false
	}
	if !strings.HasPrefix(text[pos:], textRefOpenEnd) {
		return TextRef{}, 0, false
	}
	bodyStart := pos + len(textRefOpenEnd)
	if nBytes > len(text)-bodyStart-len(textRefClose) {
		return TextRef{}, 0, false
	}
	bodyEnd := bodyStart + nBytes
	if !strings.HasPrefix(text[bodyEnd:], textRefClose) {
		return TextRef{}, 0, false
	}
	return TextRef{Num: num, Body: text[bodyStart:bodyEnd]}, bodyEnd + len(textRefClose), true
}

// ParseTextRefs splits text losslessly into plain segments and valid
// length-framed <textref> blocks. Adjacent plain segments are coalesced.
// Malformed headers, byte-length mismatches, missing closing tags, and
// truncated blocks stay literal plain text without swallowing later valid tags.
func ParseTextRefs(text string) []TextRefPart {
	if text == "" {
		return nil
	}
	if !strings.Contains(text, textRefOpenPrefix) {
		return []TextRefPart{{Text: text}}
	}
	var parts []TextRefPart
	appendPlain := func(s string) {
		if s == "" {
			return
		}
		if len(parts) > 0 && parts[len(parts)-1].TextRef == nil {
			parts[len(parts)-1].Text += s
			return
		}
		parts = append(parts, TextRefPart{Text: s})
	}
	pos := 0
	plainStart := 0
	for pos < len(text) {
		rel := strings.Index(text[pos:], textRefOpenPrefix)
		if rel < 0 {
			break
		}
		tagStart := pos + rel
		ref, consumed, ok := parseTextRefAt(text[tagStart:])
		if !ok {
			pos = tagStart + 1
			continue
		}
		appendPlain(text[plainStart:tagStart])
		r := ref
		parts = append(parts, TextRefPart{Text: text[tagStart : tagStart+consumed], TextRef: &r})
		pos = tagStart + consumed
		plainStart = pos
	}
	appendPlain(text[plainStart:])
	return parts
}

// StripTextRefs extracts all valid length-framed <textref> blocks from text
// and returns the remaining prose with separator blank lines around removed
// blocks tidied, plus the extracted refs in document order. When text contains
// no valid <textref> blocks, text is returned unchanged with nil refs.
func StripTextRefs(text string) (string, []TextRef) {
	if !strings.Contains(text, textRefOpenPrefix) {
		return text, nil
	}
	parts := ParseTextRefs(text)
	var refs []TextRef
	for _, p := range parts {
		if p.TextRef != nil {
			refs = append(refs, *p.TextRef)
		}
	}
	if len(refs) == 0 {
		return text, nil
	}
	var prose strings.Builder
	for i, p := range parts {
		if p.TextRef != nil {
			continue
		}
		seg := p.Text
		prevRef := i > 0 && parts[i-1].TextRef != nil
		nextRef := i+1 < len(parts) && parts[i+1].TextRef != nil
		if prevRef && nextRef && strings.TrimSpace(seg) == "" {
			continue
		}
		if prevRef {
			seg = trimLeadingBlockSep(seg)
		}
		if nextRef {
			seg = trimTrailingBlockSep(seg)
		}
		if seg == "" {
			continue
		}
		if prose.Len() > 0 && !strings.HasSuffix(prose.String(), "\n") && !strings.HasPrefix(seg, "\n") {
			prose.WriteString("\n\n")
		}
		prose.WriteString(seg)
	}
	return prose.String(), refs
}

func trimLeadingBlockSep(s string) string {
	for i := 0; i < 2; i++ {
		if strings.HasPrefix(s, "\r\n") {
			s = s[2:]
		} else if strings.HasPrefix(s, "\n") {
			s = s[1:]
		} else {
			break
		}
	}
	return s
}

func trimTrailingBlockSep(s string) string {
	for i := 0; i < 2; i++ {
		if strings.HasSuffix(s, "\r\n") {
			s = s[:len(s)-2]
		} else if strings.HasSuffix(s, "\n") {
			s = s[:len(s)-1]
		} else {
			break
		}
	}
	return s
}
