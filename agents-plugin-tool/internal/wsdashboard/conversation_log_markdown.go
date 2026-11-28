package wsdashboard

import (
	"bytes"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	conversationLogMDOnce sync.Once
	conversationLogMD     goldmark.Markdown

	conversationBoardRefPrefix = regexp.MustCompile(`^\{\{(p[1-9][0-9]*|[0-9]+)\}\}`)
)

func getConversationLogMarkdown() goldmark.Markdown {
	conversationLogMDOnce.Do(func() {
		// Inline-only pipeline: ParagraphParser alone keeps list markers,
		// headings, blockquotes, fences, and raw HTML literal and escaped
		// (no RawHTMLParser, no html.WithUnsafe).
		p := parser.NewParser(
			parser.WithBlockParsers(util.Prioritized(parser.NewParagraphParser(), 100)),
			parser.WithInlineParsers(
				util.Prioritized(parser.NewCodeSpanParser(), 100),
				util.Prioritized(parser.NewLinkParser(), 200),
				util.Prioritized(parser.NewAutoLinkParser(), 300),
				util.Prioritized(boardRefParser{}, 400),
				util.Prioritized(parser.NewEmphasisParser(), 500),
			),
		)
		conversationLogMD = goldmark.New(
			goldmark.WithParser(p),
			goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
			goldmark.WithRendererOptions(
				renderer.WithNodeRenderers(util.Prioritized(&conversationLogRenderer{}, 100)),
			),
		)
	})
	return conversationLogMD
}

var kindBoardRef = ast.NewNodeKind("ConversationBoardRef")

type boardRefNode struct {
	ast.BaseInline
	Label       string
	Target      string
	TopicTarget string
}

func (n *boardRefNode) Kind() ast.NodeKind { return kindBoardRef }

func (n *boardRefNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Label": n.Label, "Target": n.Target}, nil)
}

type boardRefParser struct{}

func (boardRefParser) Trigger() []byte { return []byte{'{'} }

func (boardRefParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	m := conversationBoardRefPrefix.FindSubmatch(line)
	if m == nil {
		return nil
	}
	ref := string(m[1])
	target, label := ref, ref
	if !strings.HasPrefix(ref, "p") {
		id, err := strconv.Atoi(ref)
		if err != nil || id < 1 || id > 999999 {
			return nil
		}
		label = "(" + strconv.Itoa(id) + ")"
		target = "item-" + strconv.Itoa(id)
	}
	block.Advance(len(m[0]))
	return &boardRefNode{Label: label, Target: target}
}

// renderConversationLogMarkdown renders a committed .wbupdate-log string with
// safe inline Markdown (code spans, strong, emphasis, strikethrough, safe links,
// alt-text-only images) and interactive .wbref board references while escaping
// any raw HTML tags.
func renderConversationLogMarkdown(s string, targets map[string]string) template.HTML {
	if s == "" {
		return ""
	}
	md := getConversationLogMarkdown()
	src := []byte(s)
	doc := md.Parser().Parse(text.NewReader(src))
	if len(targets) > 0 {
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering && n.Kind() == kindBoardRef {
				ref := n.(*boardRefNode)
				ref.TopicTarget = targets[ref.Target]
			}
			return ast.WalkContinue, nil
		})
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(strings.TrimRight(buf.String(), "\n"))
}

type conversationLogRenderer struct{}

func (r *conversationLogRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindParagraph, r.renderParagraph)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(kindBoardRef, r.renderBoardRef)
}

func (r *conversationLogRenderer) renderParagraph(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering && n.PreviousSibling() != nil {
		_ = w.WriteByte(' ')
	}
	return ast.WalkContinue, nil
}

func decodeLogDestination(raw []byte) string {
	v := util.UnescapePunctuations(raw)
	v = util.ResolveNumericReferences(v)
	return string(util.ResolveEntityNames(v))
}

func (r *conversationLogRenderer) renderLink(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	href, ok := safePreviewHref(decodeLogDestination(n.Destination))
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

func (r *conversationLogRenderer) renderAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	label := string(n.Label(source))
	rawURL := string(n.URL(source))
	if n.AutoLinkType == ast.AutoLinkEmail {
		rawURL = "mailto:" + rawURL
	}
	if href, ok := safePreviewHref(rawURL); ok {
		_, _ = w.WriteString(`<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(label) + `</a>`)
	} else {
		_, _ = w.WriteString(html.EscapeString(label))
	}
	return ast.WalkSkipChildren, nil
}

func (r *conversationLogRenderer) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	alt := extractPlainText(source, node)
	_, _ = w.WriteString(html.EscapeString(alt))
	return ast.WalkSkipChildren, nil
}

func (r *conversationLogRenderer) renderBoardRef(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*boardRefNode)
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == ast.KindLink {
			if _, ok := safePreviewHref(decodeLogDestination(p.(*ast.Link).Destination)); ok {
				_, _ = w.WriteString(html.EscapeString(n.Label))
				return ast.WalkSkipChildren, nil
			}
			break
		}
	}
	_, _ = w.WriteString(`<span class="wbref" role="link" tabindex="0" data-wb-nav="`)
	_, _ = w.WriteString(html.EscapeString(n.Target))
	_, _ = w.WriteString(`" data-wb-topic="`)
	_, _ = w.WriteString(html.EscapeString(n.TopicTarget))
	_, _ = w.WriteString(`">`)
	_, _ = w.WriteString(html.EscapeString(n.Label))
	_, _ = w.WriteString(`</span>`)
	return ast.WalkSkipChildren, nil
}
