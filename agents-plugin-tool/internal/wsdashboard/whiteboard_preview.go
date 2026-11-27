package wsdashboard

import (
	"bytes"
	"html"
	"html/template"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	panelPreviewMaxNonEmptyLines = 12
	panelPreviewMaxRunes         = 720
	panelPreviewMaxAltRunes      = 40
	panelPreviewMaxSrcRunes      = 120

	previewSentinelOpen  = "\uE000WSB:"
	previewSentinelClose = "\uE001"
)

type previewLabels struct {
	html, svg, image, diagram, mockup string
}

func loadPreviewLabels(lang string) previewLabels {
	return previewLabels{
		html:    wsi18n.T(lang, "entry.whiteboard.preview.html"),
		svg:     wsi18n.T(lang, "entry.whiteboard.preview.svg"),
		image:   wsi18n.T(lang, "entry.whiteboard.preview.image"),
		diagram: wsi18n.T(lang, "entry.whiteboard.preview.diagram"),
		mockup:  wsi18n.T(lang, "entry.whiteboard.preview.mockup"),
	}
}

var (
	mockupSnapshotLine = regexp.MustCompile("^Interactive HTML mockup snapshot from `([^`]+)`(?: \\(`([^`]+)`\\))?\\.?$")
	previewImageMeta   = regexp.MustCompile(`\x{E000}WSB:img:([^\x{E001}]*)\x{E001}`)

	previewVoidTags = map[string]bool{
		"area": true, "base": true, "br": true, "col": true, "embed": true,
		"hr": true, "img": true, "input": true, "link": true, "meta": true,
		"source": true, "track": true, "wbr": true,
	}
	previewRawOpaqueTags = map[string]bool{
		"script": true, "style": true, "canvas": true, "iframe": true,
		"object": true, "embed": true, "textarea": true, "xmp": true,
		"title": true, "noscript": true, "noembed": true, "template": true,
		"wb:raw": true,
	}
	previewDroppedHTMLTags = map[string]bool{
		"script": true, "style": true, "svg": true, "canvas": true, "iframe": true,
		"frame": true, "frameset": true, "object": true, "embed": true, "applet": true,
		"img": true, "picture": true, "source": true, "video": true, "audio": true,
		"track": true, "form": true, "textarea": true, "select": true, "button": true,
		"noscript": true, "template": true, "meta": true, "link": true, "base": true,
		"head": true, "xmp": true,
	}
	previewKeptHTMLTags = map[string]bool{
		"p": true, "br": true, "hr": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"ul": true, "ol": true, "li": true,
		"blockquote": true, "pre": true, "code": true,
		"strong": true, "em": true, "del": true, "b": true, "i": true, "s": true,
		"a": true, "span": true,
		"table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true,
	}
)

var (
	previewParserOnce sync.Once
	previewParser     goldmark.Markdown
)

func getPreviewMarkdown() goldmark.Markdown {
	previewParserOnce.Do(func() {
		// Standard block & inline parsers excluding HTML blocks/inline HTML and mermaid;
		// html.WithUnsafe() is intentionally omitted as defense-in-depth.
		blocks := []util.PrioritizedValue{
			util.Prioritized(parser.NewSetextHeadingParser(), 100),
			util.Prioritized(parser.NewThematicBreakParser(), 200),
			util.Prioritized(parser.NewListParser(), 300),
			util.Prioritized(parser.NewListItemParser(), 400),
			util.Prioritized(parser.NewCodeBlockParser(), 500),
			util.Prioritized(parser.NewATXHeadingParser(), 600),
			util.Prioritized(parser.NewFencedCodeBlockParser(), 700),
			util.Prioritized(parser.NewBlockquoteParser(), 800),
			util.Prioritized(parser.NewParagraphParser(), 1000),
		}
		inlines := []util.PrioritizedValue{
			util.Prioritized(parser.NewCodeSpanParser(), 100),
			util.Prioritized(parser.NewLinkParser(), 200),
			util.Prioritized(parser.NewAutoLinkParser(), 300),
			util.Prioritized(parser.NewEmphasisParser(), 500),
		}
		previewParser = goldmark.New(
			goldmark.WithParser(parser.NewParser(
				parser.WithBlockParsers(blocks...),
				parser.WithInlineParsers(inlines...),
				parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
			)),
			goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
			goldmark.WithRendererOptions(
				renderer.WithNodeRenderers(util.Prioritized(&panelPreviewRenderer{}, 100)),
			),
		)
	})
	return previewParser
}

// whiteboardPanelPreview builds a bounded, sanitized HTML preview of a committed
// whiteboard panel payload for chat update cards. No heavy or unsafe elements
// (<img>, <svg>, <canvas>, <script>, <iframe>, <style>, <pre class="mermaid">)
// are ever emitted.
func whiteboardPanelPreview(payload, lang string) template.HTML {
	clean := strings.NewReplacer(
		"\r\n", "\n",
		"\r", "\n",
		previewSentinelOpen, "",
		previewSentinelClose, "",
		"\uE000", "",
		"\uE001", "",
		"\x00", "",
	).Replace(payload)
	clean = trimLeadingBlankLines(clean)
	if strings.TrimSpace(clean) == "" {
		return ""
	}
	labels := loadPreviewLabels(lang)

	var prefixHTML strings.Builder
	firstLine, rest, hasMore := strings.Cut(clean, "\n")
	if m := mockupSnapshotLine.FindStringSubmatch(strings.TrimSpace(firstLine)); m != nil {
		src := strings.TrimSpace(m[1])
		prefixHTML.WriteString(`<p><span class="wbupdate-preview-badge">`)
		prefixHTML.WriteString(html.EscapeString(labels.mockup))
		prefixHTML.WriteString(`</span> <code>`)
		prefixHTML.WriteString(html.EscapeString(truncateRunes(src, panelPreviewMaxSrcRunes)))
		prefixHTML.WriteString(`</code></p>`)
		if !hasMore || strings.TrimSpace(rest) == "" {
			return template.HTML(prefixHTML.String())
		}
		clean = trimLeadingBlankLines(rest)
	}

	sliced := preSlicePanelPayload(clean, panelPreviewMaxNonEmptyLines, panelPreviewMaxRunes)
	prepared := collapseAdjacentSentinels(replacePreviewFragments(sliced))

	var rawBuf bytes.Buffer
	if err := getPreviewMarkdown().Convert([]byte(prepared), &rawBuf); err != nil {
		return template.HTML(prefixHTML.String())
	}

	rendered := substituteSentinels(rawBuf.String(), labels)
	if prefixHTML.Len() > 0 {
		rendered = prefixHTML.String() + "\n" + rendered
	}
	return sanitizePreviewHTML(rendered)
}

func trimLeadingBlankLines(s string) string {
	for {
		line, rest, hasLF := strings.Cut(s, "\n")
		if strings.TrimSpace(line) != "" {
			return s
		}
		if !hasLF {
			return ""
		}
		s = rest
	}
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	count := 0
	for idx := range s {
		if count == maxRunes {
			if maxRunes > 1 {
				// Rewind one rune to keep total display width <= maxRunes.
				prev := idx
				for i := range s[:idx] {
					prev = i
				}
				return strings.TrimRightFunc(s[:prev], unicode.IsSpace) + "…"
			}
			return "…"
		}
		count++
	}
	return s
}

// preSlicePanelPayload keeps at most maxLines non-empty lines and maxRunes UTF-8
// runes from s, closing any still-open fenced code block cleanly.
func preSlicePanelPayload(s string, maxLines, maxRunes int) string {
	var out strings.Builder
	nonEmpty := 0
	runes := 0
	var fenceChar byte
	fenceLen := 0

	rest := s
	for len(rest) > 0 && nonEmpty < maxLines && runes < maxRunes {
		var line string
		if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
			line = rest[:idx]
			rest = rest[idx+1:]
		} else {
			line = rest
			rest = ""
		}

		isBlank := strings.TrimSpace(line) == ""
		if out.Len() > 0 {
			if runes+1 >= maxRunes {
				break
			}
			out.WriteByte('\n')
			runes++
		}

		lineRunes := utf8.RuneCountInString(line)
		rem := maxRunes - runes
		if lineRunes > rem {
			line = sliceRunes(line, rem)
			lineRunes = rem
		}
		out.WriteString(line)
		runes += lineRunes
		if !isBlank {
			nonEmpty++
		}

		if fenceLen == 0 {
			if ch, n, _, ok := parseFenceOpen(line); ok {
				fenceChar, fenceLen = ch, n
			}
		} else if isFenceClose(line, fenceChar, fenceLen) {
			fenceChar, fenceLen = 0, 0
		}
	}

	if fenceLen > 0 {
		out.WriteByte('\n')
		out.WriteString(strings.Repeat(string(fenceChar), fenceLen))
	}
	return out.String()
}

func sliceRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for idx := range s {
		if count == maxRunes {
			return s[:idx]
		}
		count++
	}
	return s
}

func parseFenceOpen(line string) (ch byte, n int, info string, ok bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, "", false
	}
	ch = line[i]
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	n = j - i
	if n < 3 {
		return 0, 0, "", false
	}
	info = strings.TrimSpace(line[j:])
	if ch == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, "", false
	}
	return ch, n, info, true
}

func isFenceClose(line string, ch byte, minLen int) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	if j-i < minLen {
		return false
	}
	return strings.Trim(line[j:], " \t") == ""
}

func isMermaidInfo(info string) bool {
	fields := strings.Fields(strings.ToLower(info))
	return len(fields) > 0 && fields[0] == "mermaid"
}

func sentinelToken(kind string) string {
	return previewSentinelOpen + kind + previewSentinelClose
}

// replacePreviewFragments preserves non-mermaid fenced code blocks and inline
// backtick spans while replacing mermaid fences and HTML/SVG/script/canvas/<wb:raw>
// fragments with sentinel markers.
func replacePreviewFragments(s string) string {
	var out strings.Builder
	atLineStart := true
	for i := 0; i < len(s); {
		if atLineStart {
			lineStop := i
			for lineStop < len(s) && s[lineStop] != '\n' {
				lineStop++
			}
			line := s[i:lineStop]
			if ch, n, info, ok := parseFenceOpen(line); ok {
				next := lineStop
				if next < len(s) && s[next] == '\n' {
					next++
				}
				endPos := len(s)
				pos := next
				for pos < len(s) {
					nl := strings.IndexByte(s[pos:], '\n')
					var cur string
					var curEnd int
					if nl >= 0 {
						cur = s[pos : pos+nl]
						curEnd = pos + nl + 1
					} else {
						cur = s[pos:]
						curEnd = len(s)
					}
					if isFenceClose(cur, ch, n) {
						endPos = curEnd
						break
					}
					pos = curEnd
				}
				if isMermaidInfo(info) {
					if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
						if !strings.HasSuffix(out.String(), "\n") {
							out.WriteByte('\n')
						}
						out.WriteByte('\n')
					}
					out.WriteString(sentinelToken("diagram"))
					out.WriteString("\n\n")
				} else {
					out.WriteString(s[i:endPos])
				}
				i = endPos
				atLineStart = true
				continue
			}
		}

		b := s[i]
		if b == '\n' {
			out.WriteByte('\n')
			i++
			atLineStart = true
			continue
		}
		if b == '\\' && i+1 < len(s) && isASCIIPunct(s[i+1]) {
			out.WriteString(s[i : i+2])
			i += 2
			atLineStart = false
			continue
		}
		if b == '`' {
			ticks := 0
			for i+ticks < len(s) && s[i+ticks] == '`' {
				ticks++
			}
			if closeEnd, ok := findClosingBackticks(s, i+ticks, ticks); ok {
				out.WriteString(s[i:closeEnd])
				atLineStart = strings.HasSuffix(s[i:closeEnd], "\n")
				i = closeEnd
				continue
			}
			out.WriteString(s[i : i+ticks])
			i += ticks
			atLineStart = false
			continue
		}
		if b == '<' {
			if kind, endIdx, isComment, ok := consumeHTMLFragment(s, i); ok {
				if !isComment {
					out.WriteString(sentinelToken(kind))
				}
				atLineStart = endIdx > 0 && s[endIdx-1] == '\n'
				i = endIdx
				continue
			}
		}
		out.WriteByte(b)
		i++
		atLineStart = false
	}
	return out.String()
}

func isASCIIPunct(b byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", b) >= 0
}

func findClosingBackticks(s string, start, count int) (int, bool) {
	pos := start
	for pos < len(s) {
		if s[pos] == '\n' {
			// A blank line ends the paragraph so inline code cannot cross it.
			after := pos + 1
			for after < len(s) && (s[after] == ' ' || s[after] == '\t') {
				after++
			}
			if after < len(s) && s[after] == '\n' {
				return 0, false
			}
		}
		if s[pos] == '`' {
			n := 0
			for pos+n < len(s) && s[pos+n] == '`' {
				n++
			}
			if n == count {
				return pos + n, true
			}
			pos += n
			continue
		}
		pos++
	}
	return 0, false
}

// consumeHTMLFragment matches raw HTML / SVG / <wb:raw> / comments starting at s[pos] == '<',
// excluding CommonMark autolinks and literal "<" comparisons.
func consumeHTMLFragment(s string, pos int) (kind string, end int, isComment, ok bool) {
	if strings.HasPrefix(s[pos:], "<!--") {
		if closeIdx := strings.Index(s[pos+4:], "-->"); closeIdx >= 0 {
			return "", pos + 4 + closeIdx + 3, true, true
		}
		return "", len(s), true, true
	}
	if strings.HasPrefix(s[pos:], "<!") && pos+2 < len(s) && isASCIILetter(s[pos+2]) {
		tagEnd, closed := findTagEnd(s, pos+2)
		if !closed {
			return "", 0, false, false
		}
		return "html", tagEnd, false, true
	}
	if isAutolinkAt(s, pos) {
		return "", 0, false, false
	}
	closing, tagName, tagEnd, selfClosing, matched := parseHTMLTagAt(s, pos)
	if !matched {
		return "", 0, false, false
	}
	lower := strings.ToLower(tagName)
	kind = "html"
	if lower == "svg" {
		kind = "svg"
	}
	if closing || selfClosing || previewVoidTags[lower] {
		return kind, tagEnd, false, true
	}
	if previewRawOpaqueTags[lower] {
		closeIdx, found := findClosingRawTag(s, tagEnd, lower)
		if !found {
			return kind, len(s), false, true
		}
		if lower == "wb:raw" {
			innerStart := tagEnd
			// Extract inner before </wb:raw> to detect wrapped <svg>.
			inner := s[innerStart:closeIdx]
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(inner)), "<svg") {
				kind = "svg"
			}
		}
		closeEnd, _ := findTagEnd(s, closeIdx+2+len(lower))
		return kind, closeEnd, false, true
	}
	if lower == "svg" {
		endIdx, found := findBalancedTagEnd(s, tagEnd, "svg")
		if !found {
			return "svg", len(s), false, true
		}
		return "svg", endIdx, false, true
	}
	if endIdx, found := findBalancedTagEnd(s, tagEnd, lower); found {
		return "html", endIdx, false, true
	}
	return "html", tagEnd, false, true
}

func isAutolinkAt(s string, pos int) bool {
	if pos+1 >= len(s) || s[pos] != '<' {
		return false
	}
	gt := strings.IndexAny(s[pos+1:], "<>\r\n \t")
	if gt <= 0 || s[pos+1+gt] != '>' {
		return false
	}
	inner := s[pos+1 : pos+1+gt]
	lower := strings.ToLower(inner)
	if strings.HasPrefix(lower, "wb:") {
		return false
	}
	for _, scheme := range []string{"http://", "https://", "mailto:", "ftp://", "ws://", "wss://"} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	if at := strings.IndexByte(inner, '@'); at > 0 && at < len(inner)-1 && strings.Contains(inner[at+1:], ".") && !strings.Contains(inner, ":") {
		return true
	}
	return false
}

func parseHTMLTagAt(s string, pos int) (closing bool, name string, end int, selfClosing, ok bool) {
	i := pos + 1
	if i < len(s) && s[i] == '/' {
		closing = true
		i++
	}
	if i >= len(s) || !isASCIILetter(s[i]) {
		return false, "", 0, false, false
	}
	nameStart := i
	for i < len(s) && isTagNameChar(s[i]) {
		i++
	}
	name = s[nameStart:i]
	if closing {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
			i++
		}
		if i < len(s) && s[i] == '>' {
			return true, name, i + 1, false, true
		}
		if i == len(s) {
			return true, name, len(s), false, true
		}
		return false, "", 0, false, false
	}
	if i < len(s) && s[i] != '>' && s[i] != '/' && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' {
		return false, "", 0, false, false
	}
	lower := strings.ToLower(name)
	tagEnd, closed := findTagEnd(s, i)
	if !closed && !previewRawOpaqueTags[lower] && !previewDroppedHTMLTags[lower] {
		return false, "", 0, false, false
	}
	// Self-closing check: character immediately before '>' is '/'.
	if closed && tagEnd >= pos+2 && tagEnd <= len(s) && s[tagEnd-1] == '>' && s[tagEnd-2] == '/' {
		selfClosing = true
	}
	return false, name, tagEnd, selfClosing, true
}

func findTagEnd(s string, i int) (int, bool) {
	var quote byte
	for i < len(s) {
		ch := s[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			i++
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			i++
			continue
		}
		if ch == '>' {
			return i + 1, true
		}
		i++
	}
	return len(s), false
}

func findClosingRawTag(s string, pos int, lowerTag string) (int, bool) {
	for i := pos; i < len(s); i++ {
		if s[i] == '<' && i+2+len(lowerTag) <= len(s) && s[i+1] == '/' {
			if strings.EqualFold(s[i+2:i+2+len(lowerTag)], lowerTag) {
				after := i + 2 + len(lowerTag)
				if after == len(s) || s[after] == '>' || s[after] == ' ' || s[after] == '\t' || s[after] == '\n' {
					return i, true
				}
			}
		}
	}
	return 0, false
}

func findBalancedTagEnd(s string, pos int, lowerTarget string) (int, bool) {
	depth := 1
	for i := pos; i < len(s); {
		if strings.HasPrefix(s[i:], "<!--") {
			if closeIdx := strings.Index(s[i+4:], "-->"); closeIdx >= 0 {
				i += 4 + closeIdx + 3
				continue
			}
			return 0, false
		}
		if s[i] == '<' {
			closing, name, tagEnd, selfClosing, matched := parseHTMLTagAt(s, i)
			if matched {
				lower := strings.ToLower(name)
				if lower == lowerTarget {
					if closing {
						depth--
						if depth == 0 {
							return tagEnd, true
						}
					} else if !selfClosing {
						depth++
					}
					i = tagEnd
					continue
				}
				if !closing && !selfClosing && previewRawOpaqueTags[lower] {
					if closeIdx, found := findClosingRawTag(s, tagEnd, lower); found {
						i, _ = findTagEnd(s, closeIdx+2+len(lower))
						continue
					}
					return 0, false
				}
				i = tagEnd
				continue
			}
		}
		i++
	}
	return 0, false
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isTagNameChar(b byte) bool {
	return isASCIILetter(b) || (b >= '0' && b <= '9') || b == '-' || b == ':'
}

// collapseAdjacentSentinels collapses runs of identical HTML or SVG sentinels
// separated solely by whitespace into a single badge token.
func collapseAdjacentSentinels(s string) string {
	for _, kind := range []string{"html", "svg"} {
		tok := sentinelToken(kind)
		for {
			changed := false
			var out strings.Builder
			pos := 0
			for pos < len(s) {
				idx := strings.Index(s[pos:], tok)
				if idx < 0 {
					out.WriteString(s[pos:])
					break
				}
				out.WriteString(s[pos : pos+idx])
				out.WriteString(tok)
				after := pos + idx + len(tok)
				for {
					ws := after
					for ws < len(s) && (s[ws] == ' ' || s[ws] == '\t' || s[ws] == '\n') {
						ws++
					}
					if strings.HasPrefix(s[ws:], tok) {
						after = ws + len(tok)
						changed = true
						continue
					}
					break
				}
				pos = after
			}
			s = out.String()
			if !changed {
				break
			}
		}
	}
	return s
}

func substituteSentinels(rendered string, labels previewLabels) string {
	badge := func(label string) string {
		return `<span class="wbupdate-preview-badge">` + html.EscapeString(label) + `</span>`
	}
	out := strings.NewReplacer(
		sentinelToken("html"), badge(labels.html),
		sentinelToken("svg"), badge(labels.svg),
		sentinelToken("diagram"), badge(labels.diagram),
	).Replace(rendered)

	return previewImageMeta.ReplaceAllStringFunc(out, func(match string) string {
		alt := strings.TrimPrefix(match, previewSentinelOpen+"img:")
		alt = strings.TrimSuffix(alt, previewSentinelClose)
		res := badge(labels.image)
		if alt != "" {
			res += ` <span class="wbupdate-preview-alt">` + html.EscapeString(alt) + `</span>`
		}
		return res
	})
}

// panelPreviewRenderer overrides image, fenced mermaid, and link rendering
// so heavy/unsafe elements become clean sentinel markers or plain text.
type panelPreviewRenderer struct{}

func (r *panelPreviewRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCode)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
}

func (r *panelPreviewRenderer) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	alt := extractPlainText(source, n)
	alt = strings.Join(strings.Fields(alt), " ")
	alt = truncateRunes(alt, panelPreviewMaxAltRunes)
	alt = strings.NewReplacer(previewSentinelOpen, "", previewSentinelClose, "", "\uE000", "", "\uE001", "").Replace(alt)
	_, _ = w.WriteString(previewSentinelOpen + "img:" + alt + previewSentinelClose)
	return ast.WalkSkipChildren, nil
}

func (r *panelPreviewRenderer) renderFencedCode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := ""
	if n.Language(source) != nil {
		lang = strings.ToLower(string(n.Language(source)))
	}
	if lang == "mermaid" {
		_, _ = w.WriteString("<p>" + sentinelToken("diagram") + "</p>\n")
		return ast.WalkSkipChildren, nil
	}
	_, _ = w.WriteString("<pre><code")
	if lang != "" {
		safeLang := sanitizeCodeLang(lang)
		if safeLang != "" {
			_, _ = w.WriteString(` class="language-` + html.EscapeString(safeLang) + `"`)
		}
	}
	_ = w.WriteByte('>')
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		_, _ = w.Write(util.EscapeHTML(line.Value(source)))
	}
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

func (r *panelPreviewRenderer) renderLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	href, ok := safePreviewHref(string(n.Destination))
	if !ok {
		return ast.WalkContinue, nil
	}
	if entering {
		_, _ = w.WriteString(`<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">`)
	} else {
		_, _ = w.WriteString(`</a>`)
	}
	return ast.WalkContinue, nil
}

func (r *panelPreviewRenderer) renderAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	label := string(n.Label(source))
	url := string(n.URL(source))
	if n.AutoLinkType == ast.AutoLinkEmail {
		url = "mailto:" + url
	}
	if href, ok := safePreviewHref(url); ok {
		_, _ = w.WriteString(`<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(label) + `</a>`)
	} else {
		_, _ = w.WriteString(html.EscapeString(label))
	}
	return ast.WalkSkipChildren, nil
}

func extractPlainText(source []byte, root ast.Node) string {
	var b strings.Builder
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Text:
			_, _ = b.Write(t.Segment.Value(source))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			_, _ = b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func sanitizeCodeLang(lang string) string {
	var b strings.Builder
	for _, r := range lang {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "mermaid" {
		return ""
	}
	return out
}

func safePreviewHref(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	for _, scheme := range []string{"https://", "http://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			return s, true
		}
	}
	return "", false
}

// sanitizePreviewHTML runs a final strict allow-list pass over the rendered HTML
// fragment so no unexpected tags or attributes can survive.
func sanitizePreviewHTML(fragment string) template.HTML {
	ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), ctx)
	if err != nil {
		return template.HTML(template.HTMLEscapeString(fragment))
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	sanitizePreviewChildren(root)
	var buf bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := xhtml.Render(&buf, c); err != nil {
			return template.HTML(template.HTMLEscapeString(fragment))
		}
	}
	return template.HTML(strings.TrimSpace(buf.String()))
}

func sanitizePreviewChildren(parent *xhtml.Node) {
	for c := parent.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case xhtml.TextNode:
		case xhtml.ElementNode:
			sanitizePreviewElement(c)
		default:
			parent.RemoveChild(c)
		}
		c = next
	}
}

func sanitizePreviewElement(n *xhtml.Node) {
	name := strings.ToLower(n.Data)
	parent := n.Parent
	if n.Namespace != "" || previewDroppedHTMLTags[name] {
		parent.RemoveChild(n)
		return
	}
	if name == "input" {
		if !isSafeTaskCheckbox(n) {
			parent.RemoveChild(n)
			return
		}
		keepTaskCheckboxAttrs(n)
		sanitizePreviewChildren(n)
		return
	}
	if !previewKeptHTMLTags[name] {
		sanitizePreviewChildren(n)
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			n.RemoveChild(c)
			parent.InsertBefore(c, n)
			c = next
		}
		parent.RemoveChild(n)
		return
	}
	sanitizePreviewChildren(n)
	keepPreviewAttrs(n, name)
}

func isSafeTaskCheckbox(n *xhtml.Node) bool {
	isCB, disabled := false, false
	for _, a := range n.Attr {
		k := strings.ToLower(a.Key)
		if k == "type" && strings.EqualFold(a.Val, "checkbox") {
			isCB = true
		}
		if k == "disabled" {
			disabled = true
		}
	}
	return isCB && disabled
}

func keepTaskCheckboxAttrs(n *xhtml.Node) {
	attrs := []xhtml.Attribute{
		{Key: "type", Val: "checkbox"},
		{Key: "disabled", Val: ""},
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "checked") {
			attrs = append(attrs, xhtml.Attribute{Key: "checked", Val: ""})
			break
		}
	}
	n.Attr = attrs
}

func keepPreviewAttrs(n *xhtml.Node, name string) {
	var kept []xhtml.Attribute
	switch name {
	case "span":
		for _, a := range n.Attr {
			if a.Namespace == "" && strings.EqualFold(a.Key, "class") {
				if a.Val == "wbupdate-preview-badge" || a.Val == "wbupdate-preview-alt" {
					kept = append(kept, xhtml.Attribute{Key: "class", Val: a.Val})
				}
			}
		}
	case "a":
		var href string
		for _, a := range n.Attr {
			if a.Namespace == "" && strings.EqualFold(a.Key, "href") {
				if safe, ok := safePreviewHref(a.Val); ok {
					href = safe
				}
			}
		}
		if href != "" {
			kept = append(kept,
				xhtml.Attribute{Key: "href", Val: href},
				xhtml.Attribute{Key: "target", Val: "_blank"},
				xhtml.Attribute{Key: "rel", Val: "noopener noreferrer"},
			)
		}
	case "code":
		for _, a := range n.Attr {
			if a.Namespace == "" && strings.EqualFold(a.Key, "class") && strings.HasPrefix(a.Val, "language-") {
				if lang := sanitizeCodeLang(strings.TrimPrefix(a.Val, "language-")); lang != "" {
					kept = append(kept, xhtml.Attribute{Key: "class", Val: "language-" + lang})
				}
			}
		}
	case "th", "td":
		for _, a := range n.Attr {
			if a.Namespace == "" && strings.EqualFold(a.Key, "align") {
				v := strings.ToLower(a.Val)
				if v == "left" || v == "center" || v == "right" {
					kept = append(kept, xhtml.Attribute{Key: "align", Val: v})
				}
			}
		}
	}
	n.Attr = kept
}
