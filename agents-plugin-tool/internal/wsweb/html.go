package wsweb

import (
	"bytes"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlToText parses HTML and extracts the page title plus clean readable text,
// discarding non-content elements (scripts, styles, SVG, templates, embeds).
func htmlToText(raw []byte) (title, text string) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", normalizePlainText(string(raw))
	}
	var w htmlWalker
	w.walk(doc, false)
	return collapseInline(w.title.String()), cleanExtractedLines(w.body.String())
}

type htmlWalker struct {
	title strings.Builder
	body  strings.Builder
	inPre int
}

func (w *htmlWalker) walk(n *html.Node, inTitle bool) {
	switch n.Type {
	case html.ElementNode:
		if skipElement(n.DataAtom) {
			return
		}
		if n.DataAtom == atom.Title {
			if w.title.Len() == 0 {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					w.walk(c, true)
				}
			}
			return
		}
		if n.DataAtom == atom.Head {
			// Still inspect <head> solely to extract <title>.
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.DataAtom == atom.Title {
					w.walk(c, true)
				}
			}
			return
		}
		if n.DataAtom == atom.Br {
			w.writeBreak(1)
			return
		}
		if n.DataAtom == atom.Hr {
			w.writeBreak(2)
			w.body.WriteString("---")
			w.writeBreak(2)
			return
		}
		if isParagraphBlock(n.DataAtom) {
			w.writeBreak(2)
		} else if isLineBlock(n.DataAtom) {
			w.writeBreak(1)
		}
		if n.DataAtom == atom.Li {
			w.body.WriteString("- ")
		}
		if n.DataAtom == atom.Pre {
			w.inPre++
		}
		firstCell := true
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if n.DataAtom == atom.Tr && c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
				if !firstCell {
					w.body.WriteString(" | ")
				}
				firstCell = false
			}
			w.walk(c, inTitle)
		}
		if n.DataAtom == atom.Pre {
			w.inPre--
		}
		if isParagraphBlock(n.DataAtom) {
			w.writeBreak(2)
		} else if isLineBlock(n.DataAtom) {
			w.writeBreak(1)
		}
	case html.TextNode:
		if inTitle {
			w.title.WriteString(n.Data)
			return
		}
		if w.inPre > 0 {
			w.body.WriteString(strings.ReplaceAll(n.Data, "\r\n", "\n"))
			return
		}
		collapsed := collapseInline(n.Data)
		if collapsed == "" {
			return
		}
		if w.needsSpaceBefore(collapsed) {
			w.body.WriteByte(' ')
		}
		w.body.WriteString(collapsed)
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.walk(c, inTitle)
		}
	}
}

func (w *htmlWalker) writeBreak(n int) {
	if w.body.Len() == 0 {
		return
	}
	s := w.body.String()
	trailing := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\n'; i-- {
		trailing++
	}
	for trailing < n {
		w.body.WriteByte('\n')
		trailing++
	}
}

func (w *htmlWalker) needsSpaceBefore(next string) bool {
	if w.body.Len() == 0 || len(next) == 0 {
		return false
	}
	s := w.body.String()
	last := s[len(s)-1]
	if last == '\n' || last == ' ' || last == '\t' || last == '(' || last == '[' {
		return false
	}
	if strings.HasSuffix(s, "- ") || strings.HasSuffix(s, " | ") {
		return false
	}
	switch next[0] {
	case '.', ',', ';', ':', '!', '?', ')', ']':
		return false
	}
	return true
}

func skipElement(a atom.Atom) bool {
	switch a {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg,
		atom.Canvas, atom.Iframe, atom.Object, atom.Embed, atom.Audio, atom.Video:
		return true
	}
	return false
}

func isParagraphBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Blockquote, atom.Pre, atom.Section, atom.Article, atom.Main,
		atom.Header, atom.Footer, atom.Nav, atom.Aside, atom.Table,
		atom.Ul, atom.Ol, atom.Dl, atom.Figure, atom.Figcaption:
		return true
	}
	return false
}

func isLineBlock(a atom.Atom) bool {
	switch a {
	case atom.Div, atom.Li, atom.Tr, atom.Dt, atom.Dd, atom.Address, atom.Details, atom.Summary:
		return true
	}
	return false
}

func collapseInline(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	return strings.TrimSpace(b.String())
}

func cleanExtractedLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
		if strings.TrimSpace(trimmed) == "" {
			if len(out) > 0 && blanks == 0 {
				out = append(out, "")
				blanks++
			}
			continue
		}
		blanks = 0
		out = append(out, trimmed)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func normalizePlainText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}
