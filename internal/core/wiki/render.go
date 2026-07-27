package wiki

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Renderer converts markdown content to HTML.
//
// There is no sanitiser in this path, and the docstring used to claim there
// was one. What actually keeps author-supplied markup out of the output is
// goldmark's own default: the HTML renderer's Unsafe option is false unless
// html.WithUnsafe() is passed, and with it false goldmark drops raw HTML
// blocks and raw inline HTML rather than passing them through. Nothing else
// downstream escapes this output, so that default IS the safety boundary.
//
// Because the protection is a library default rather than an explicit call,
// it is invisible at this call site — adding html.WithUnsafe() here would
// silently turn every wiki page into a stored-XSS sink and no line in this
// file would look wrong. TestRenderHTML_RawHTMLIsNotPassedThrough pins the
// behaviour so that change fails the suite instead.
type Renderer struct {
	md goldmark.Markdown
}

// NewRenderer creates a Renderer configured with common goldmark extensions
// (tables, strikethrough, autolinks, task lists).
//
// Do not add html.WithUnsafe() to the renderer options. See the type docstring.
func NewRenderer() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // GitHub Flavored Markdown
		),
		goldmark.WithRendererOptions(
			// Deliberately no html.WithUnsafe(): raw HTML must stay dropped.
			html.WithHardWraps(),
			html.WithXHTML(),
		),
	)
	return &Renderer{md: md}
}

// RenderHTML converts markdown text to HTML.
func (r *Renderer) RenderHTML(markdown string) (string, error) {
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(markdown), &buf); err != nil {
		return "", fmt.Errorf("rendering markdown: %w", err)
	}
	return buf.String(), nil
}

// RenderPage is a convenience that renders a page's content from the service.
func (s *Service) RenderPage(markdown string) (string, error) {
	return s.renderer.RenderHTML(markdown)
}
