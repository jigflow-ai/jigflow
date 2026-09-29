package cli

import (
	"bytes"
	"html"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// markdown renders the prose anyone edits, such as an Artifact's body, as
// the Dashboard shows it: with GitHub's tables and task lists, and with any
// HTML of its own shown as text, so that a body can't put a script on a
// page that carries decision forms (ADR 0027).
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(htmlAsText{}, 100))),
)

// renderMarkdown renders source as HTML safe to put on a page.
func renderMarkdown(source string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(source), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// htmlAsText renders the HTML written in Markdown, blocks and inline, as
// the text it is.
type htmlAsText struct{}

// RegisterFuncs renders both kinds of HTML node as escaped text, in place
// of goldmark's own renderers, which drop them.
func (htmlAsText) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHTMLBlock, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		b := n.(*ast.HTMLBlock)
		if entering {
			_, _ = w.WriteString("<p>")
			lines := b.Lines()
			for i := range lines.Len() {
				s := lines.At(i)
				_, _ = w.WriteString(html.EscapeString(string(s.Value(source))))
			}
			if b.HasClosure() {
				_, _ = w.WriteString(html.EscapeString(string(b.ClosureLine.Value(source))))
			}
		} else {
			_, _ = w.WriteString("</p>\n")
		}
		return ast.WalkContinue, nil
	})
	reg.Register(ast.KindRawHTML, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segs := n.(*ast.RawHTML).Segments
			for i := range segs.Len() {
				s := segs.At(i)
				_, _ = w.WriteString(html.EscapeString(string(s.Value(source))))
			}
		}
		return ast.WalkSkipChildren, nil
	})
}
