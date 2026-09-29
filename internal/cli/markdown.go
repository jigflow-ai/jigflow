package cli

import (
	"bytes"
	"html"
	"html/template"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
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
	h, _, err := renderLinkingMockups(source, "")
	return h, err
}

// renderLinkingMockups renders source as renderMarkdown does, and returns
// the paths, inside the Mockup folder mockups, of the Mockups it links to,
// once each in the order it first does. A link to one, by its path from
// the project's root or at the Dashboard's /mockups/, opens it in the
// Dashboard. With no Mockup folder, it links none.
func renderLinkingMockups(source, mockups string) (template.HTML, []string, error) {
	src := []byte(source)
	doc := markdown.Parser().Parse(text.NewReader(src))
	var linked []string
	if mockups != "" {
		err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			var dest *[]byte
			switch l := n.(type) {
			case *ast.Link:
				dest = &l.Destination
			case *ast.Image:
				dest = &l.Destination
			default:
				return ast.WalkContinue, nil
			}
			if p, ok := mockupPath(string(*dest), mockups); ok {
				*dest = []byte(mockupURL(p))
				if !slices.Contains(linked, p) {
					linked = append(linked, p)
				}
			}
			return ast.WalkContinue, nil
		})
		if err != nil {
			return "", nil, err
		}
	}
	var buf bytes.Buffer
	if err := markdown.Renderer().Render(&buf, src, doc); err != nil {
		return "", nil, err
	}
	return template.HTML(buf.String()), linked, nil
}

// mockupPath returns the path inside the Mockup folder mockups of the file
// the link dest names, when it names one: by its path from the project's
// root, such as .jigflow/mockups/REQ-1/checkout.html, or at the
// Dashboard's /mockups/.
func mockupPath(dest, mockups string) (string, bool) {
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return "", false
	}
	p := u.Path
	if rest, ok := strings.CutPrefix(p, "/mockups/"); ok {
		p = rest
	} else {
		p = strings.TrimPrefix(path.Clean("/"+p), "/")
		var ok bool
		if p, ok = strings.CutPrefix(p, mockups+"/"); !ok {
			return "", false
		}
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
		return "", false
	}
	return p, true
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
