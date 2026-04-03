package wiki

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Renderer converts markdown content to sanitised HTML.
type Renderer struct {
	md goldmark.Markdown
}

// NewRenderer creates a Renderer configured with common goldmark extensions
// (tables, strikethrough, autolinks, task lists).
func NewRenderer() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // GitHub Flavored Markdown
		),
		goldmark.WithRendererOptions(
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
