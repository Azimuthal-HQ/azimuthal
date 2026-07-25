package doc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// The node types the markdown on-ramp emits for constructs it cannot represent
// natively. None of them is in schema.json, deliberately: they are unknown
// content like any other, so [Shield] captures them through the same path as an
// imported Confluence macro, and there is exactly one preservation mechanism to
// get right.
const (
	// LegacyHTMLBlock preserves a block of raw HTML from markdown.
	LegacyHTMLBlock = "legacyHtmlBlock"

	// LegacyHTMLInline preserves raw HTML inside a paragraph. Codex's markdown
	// editor serialises text colour and highlight as inline <span>, so existing
	// pages in this repository contain these.
	LegacyHTMLInline = "legacyHtmlInline"

	// LegacyImage preserves an image whose URL is not a plain http(s) or
	// site-relative reference. Converting it to a first-class image node would
	// put an unvetted URL scheme into the reading surface; dropping it would
	// lose the image.
	LegacyImage = "legacyImage"

	// LegacyBlock and LegacyInline are the catch-alls. Reaching them means the
	// markdown parser produced a construct this converter has not been taught,
	// and the answer is to preserve it verbatim rather than to skip it.
	LegacyBlock  = "legacyBlock"
	LegacyInline = "legacyInline"
)

// markdownParser is configured with the same GFM extension the server-side
// renderer uses (internal/core/wiki/render.go), so the converter reads a page
// the way the rest of the system already reads it.
var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

// FromMarkdown converts markdown to a document.
//
// This is the legacy on-ramp: it runs the first time an existing page is opened
// in the document editor, and never as a bulk migration (migration 036 says
// why). It is a pure, deterministic function of its input, which is load-bearing
// — publish re-derives the base document from the same markdown to recover the
// preservation ids handed out at read time, and a converter that produced a
// different document the second time would break that.
//
// Every branch either represents a construct natively or preserves it verbatim
// as one of the Legacy* node types above. There is no branch that discards.
func FromMarkdown(markdown string) (json.RawMessage, error) {
	source := []byte(markdown)
	root := markdownParser.Parser().Parse(text.NewReader(source))

	c := &converter{source: source}
	children, err := c.blocks(root, 0)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return Empty(), nil
	}
	return buildNode("doc", nil, children, nil, nil)
}

type converter struct {
	source []byte
}

// blocks converts a container's block children.
func (c *converter) blocks(parent ast.Node, depth int) ([]json.RawMessage, error) {
	if depth > maxDepth {
		return nil, ErrTooDeep
	}
	out := make([]json.RawMessage, 0)
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		converted, err := c.block(child, depth+1)
		if err != nil {
			return nil, err
		}
		if converted != nil {
			out = append(out, converted)
		}
	}
	return out, nil
}

// block converts one markdown block, trying each family of construct in turn.
// The families are separate functions rather than one long switch so that each
// stays readable; the ordering between them carries no meaning.
func (c *converter) block(n ast.Node, depth int) (json.RawMessage, error) {
	if depth > maxDepth {
		return nil, ErrTooDeep
	}
	if out, handled, err := c.inlineBearingBlock(n, depth); handled || err != nil {
		return out, err
	}
	if out, handled := c.literalBlock(n); handled {
		return out, nil
	}
	return c.containerBlock(n, depth)
}

// inlineBearingBlock handles the blocks whose content is inline.
func (c *converter) inlineBearingBlock(n ast.Node, depth int) (json.RawMessage, bool, error) {
	var (
		nodeType = "paragraph"
		attrs    map[string]any
	)
	switch node := n.(type) {
	case *ast.Heading:
		nodeType = "heading"
		attrs = map[string]any{"level": node.Level}
	case *ast.Paragraph:
	case *ast.TextBlock:
		// A tight list item's body: markdown gives it no paragraph, the
		// document model wants one.
	default:
		return nil, false, nil
	}

	inline, err := c.inline(n, nil, depth)
	if err != nil {
		return nil, true, err
	}
	out, err := buildNode(nodeType, attrs, inline, nil, nil)
	return out, true, err
}

// literalBlock handles the blocks with no inline children to walk.
func (c *converter) literalBlock(n ast.Node) (json.RawMessage, bool) {
	switch node := n.(type) {
	case *ast.ThematicBreak:
		return mustNode(buildNode("horizontalRule", nil, nil, nil, nil)), true
	case *ast.FencedCodeBlock:
		return mustNode(c.codeBlock(node, string(node.Language(c.source)))), true
	case *ast.CodeBlock:
		return mustNode(c.codeBlock(node, "")), true
	default:
		return nil, false
	}
}

// containerBlock handles the blocks that hold other blocks, and owns the
// preserve-rather-than-drop default.
func (c *converter) containerBlock(n ast.Node, depth int) (json.RawMessage, error) {
	switch node := n.(type) {
	case *ast.Blockquote:
		children, err := c.blocks(node, depth)
		if err != nil {
			return nil, err
		}
		return buildNode("blockquote", nil, children, nil, nil)
	case *ast.List:
		return c.list(node, depth)
	case *ast.HTMLBlock:
		return c.legacyHTMLBlock(node)
	case *extast.Table:
		return c.table(node, depth)
	default:
		// Not a construct this converter knows. Preserve the source lines
		// verbatim rather than skipping the node.
		return c.legacyBlock(n)
	}
}

func (c *converter) codeBlock(n ast.Node, language string) (json.RawMessage, error) {
	code := c.lines(n)
	var content []json.RawMessage
	if code != "" {
		textNode, err := buildNode("text", nil, nil, nil, &code)
		if err != nil {
			return nil, err
		}
		content = []json.RawMessage{textNode}
	}
	return buildNode("codeBlock", map[string]any{"language": language}, content, nil, nil)
}

// list converts a markdown list. A list whose items carry checkboxes becomes a
// task list, which is how GFM task lists reach the document model.
func (c *converter) list(n *ast.List, depth int) (json.RawMessage, error) {
	tasks := listIsTaskList(n)

	items := make([]json.RawMessage, 0)
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		children, err := c.blocks(item, depth)
		if err != nil {
			return nil, err
		}
		if tasks {
			items = append(items, mustNode(buildNode(
				"taskItem", map[string]any{"checked": itemIsChecked(item)}, children, nil, nil)))
			continue
		}
		items = append(items, mustNode(buildNode("listItem", nil, children, nil, nil)))
	}

	switch {
	case tasks:
		return buildNode("taskList", nil, items, nil, nil)
	case n.IsOrdered():
		return buildNode("orderedList", map[string]any{"start": n.Start}, items, nil, nil)
	default:
		return buildNode("bulletList", nil, items, nil, nil)
	}
}

// listIsTaskList reports whether any item in the list carries a GFM checkbox.
// Any is the right test rather than all: GFM allows a mixed list, and rendering
// the checked items as plain bullets would lose their state.
func listIsTaskList(n *ast.List) bool {
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		if firstCheckbox(item) != nil {
			return true
		}
	}
	return false
}

func itemIsChecked(item ast.Node) bool {
	box := firstCheckbox(item)
	return box != nil && box.IsChecked
}

// firstCheckbox finds a list item's task checkbox. GFM puts it as the first
// inline child of the item's first block.
func firstCheckbox(item ast.Node) *extast.TaskCheckBox {
	for block := item.FirstChild(); block != nil; block = block.NextSibling() {
		for inline := block.FirstChild(); inline != nil; inline = inline.NextSibling() {
			if box, ok := inline.(*extast.TaskCheckBox); ok {
				return box
			}
		}
	}
	return nil
}

// table converts a GFM table. The header row becomes a row of tableHeader
// cells; every cell holds a paragraph, matching the editor's content model.
func (c *converter) table(n *extast.Table, depth int) (json.RawMessage, error) {
	rows := make([]json.RawMessage, 0)
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		header := false
		if _, ok := row.(*extast.TableHeader); ok {
			header = true
		}
		cells := make([]json.RawMessage, 0)
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			inline, err := c.inline(cell, nil, depth)
			if err != nil {
				return nil, err
			}
			paragraph, err := buildNode("paragraph", nil, inline, nil, nil)
			if err != nil {
				return nil, err
			}
			cellType := "tableCell"
			if header {
				cellType = "tableHeader"
			}
			cellNode, err := buildNode(cellType, nil, []json.RawMessage{paragraph}, nil, nil)
			if err != nil {
				return nil, err
			}
			cells = append(cells, cellNode)
		}
		rowNode, err := buildNode("tableRow", nil, cells, nil, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, rowNode)
	}
	return buildNode("table", nil, rows, nil, nil)
}

// inline converts a node's inline children, carrying the marks accumulated from
// the enclosing emphasis, link and code spans.
func (c *converter) inline(parent ast.Node, marks []json.RawMessage, depth int) ([]json.RawMessage, error) {
	if depth > maxDepth {
		return nil, ErrTooDeep
	}
	out := make([]json.RawMessage, 0)
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		converted, err := c.inlineNode(child, marks, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	return mergeAdjacentText(out)
}

// mergeAdjacentText joins neighbouring text nodes that carry identical marks.
//
// goldmark splits a run of plain text wherever an inline parser was triggered
// and then backed out, so "Intro paragraph." arrives as two segments. ProseMirror
// normalises that away the moment the editor loads the document, which would
// leave the stored document and the one the editor sends back structurally
// different for no reason — noise in every diff and in the markdown projection.
// Merging here means the converter emits what the editor would.
func mergeAdjacentText(nodes []json.RawMessage) ([]json.RawMessage, error) {
	if len(nodes) < 2 {
		return nodes, nil
	}
	out := make([]json.RawMessage, 0, len(nodes))
	for _, node := range nodes {
		if len(out) == 0 {
			out = append(out, node)
			continue
		}
		merged, ok, err := mergeTextPair(out[len(out)-1], node)
		if err != nil {
			return nil, err
		}
		if ok {
			out[len(out)-1] = merged
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

// mergeTextPair merges two text nodes if both are text and their marks match.
func mergeTextPair(left, right json.RawMessage) (json.RawMessage, bool, error) {
	leftObj, rightObj, mergeable, err := mergeableTextNodes(left, right)
	if err != nil || !mergeable {
		return nil, false, err
	}

	leftText, err := textOf(leftObj)
	if err != nil {
		return nil, false, err
	}
	rightText, err := textOf(rightObj)
	if err != nil {
		return nil, false, err
	}

	textRaw, err := marshalPlain(leftText + rightText)
	if err != nil {
		return nil, false, err
	}
	leftObj["text"] = textRaw
	encoded, err := leftObj.encode()
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// mergeableTextNodes reports whether two nodes are both text with identical
// marks, which is the only case where joining them preserves meaning.
func mergeableTextNodes(left, right json.RawMessage) (object, object, bool, error) {
	leftObj, err := decodeObject(left)
	if err != nil {
		return nil, nil, false, err
	}
	rightObj, err := decodeObject(right)
	if err != nil {
		return nil, nil, false, err
	}
	leftType, err := leftObj.typeOf()
	if err != nil {
		return nil, nil, false, err
	}
	rightType, err := rightObj.typeOf()
	if err != nil {
		return nil, nil, false, err
	}
	if leftType != "text" || rightType != "text" {
		return nil, nil, false, nil
	}
	return leftObj, rightObj, bytesEqual(leftObj["marks"], rightObj["marks"]), nil
}

// textOf reads a text node's payload.
func textOf(obj object) (string, error) {
	var value string
	if err := json.Unmarshal(obj["text"], &value); err != nil {
		return "", fmt.Errorf("decoding text node: %w", err)
	}
	return value, nil
}

func (c *converter) inlineNode(n ast.Node, marks []json.RawMessage, depth int) ([]json.RawMessage, error) {
	if depth > maxDepth {
		return nil, ErrTooDeep
	}
	switch node := n.(type) {
	case *ast.Text:
		return c.textRun(node, marks)
	case *ast.String:
		return c.textValue(string(node.Value), marks)
	}
	if markName, attrs, ok := inlineMarkFor(n); ok {
		return c.inline(n, appendMark(marks, markName, attrs), depth)
	}
	return c.inlineAtom(n, marks)
}

// inlineMarkFor maps the inline constructs that are formatting around other
// inline content to the mark they become. The returned mark is added to the
// enclosing set and the node's children are walked with it.
func inlineMarkFor(n ast.Node) (string, map[string]any, bool) {
	switch node := n.(type) {
	case *ast.CodeSpan:
		return "code", nil, true
	case *ast.Emphasis:
		if node.Level >= 2 {
			return "bold", nil, true
		}
		return "italic", nil, true
	case *extast.Strikethrough:
		return "strike", nil, true
	case *ast.Link:
		attrs := map[string]any{"href": string(node.Destination)}
		if len(node.Title) > 0 {
			attrs["title"] = string(node.Title)
		}
		return "link", attrs, true
	default:
		return "", nil, false
	}
}

// inlineAtom handles the inline constructs that stand alone, and owns the
// preserve-rather-than-drop default.
func (c *converter) inlineAtom(n ast.Node, marks []json.RawMessage) ([]json.RawMessage, error) {
	switch node := n.(type) {
	case *ast.AutoLink:
		url := string(node.URL(c.source))
		return c.textValue(url, appendMark(marks, "link", map[string]any{"href": url}))
	case *ast.Image:
		return c.image(node, marks)
	case *ast.RawHTML:
		return c.legacyHTMLInline(node, marks)
	case *extast.TaskCheckBox:
		// Consumed by the enclosing list, which became a taskList. Emitting it
		// again would put a literal "[x]" in the item's text.
		return nil, nil
	default:
		return c.legacyInline(n, marks)
	}
}

// textRun converts a text segment, turning markdown's two kinds of line break
// into their document equivalents.
func (c *converter) textRun(node *ast.Text, marks []json.RawMessage) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, 2)
	if value := string(node.Segment.Value(c.source)); value != "" {
		converted, err := c.textValue(value, marks)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	switch {
	case node.HardLineBreak():
		br, err := buildNode("hardBreak", nil, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, br)
	case node.SoftLineBreak():
		// A soft break is whitespace in rendered markdown; keeping it as a
		// space is what preserves word separation across the wrapped line.
		space, err := c.textValue(" ", marks)
		if err != nil {
			return nil, err
		}
		out = append(out, space...)
	}
	return out, nil
}

func (c *converter) textValue(value string, marks []json.RawMessage) ([]json.RawMessage, error) {
	if value == "" {
		return nil, nil
	}
	node, err := buildNode("text", nil, nil, marks, &value)
	if err != nil {
		return nil, err
	}
	return []json.RawMessage{node}, nil
}

// image converts a markdown image. A destination that is not plain http(s) or
// site-relative is preserved verbatim instead: the reading surface should not be
// handed an arbitrary URL scheme, and refusing to convert it is not the same as
// throwing it away.
func (c *converter) image(node *ast.Image, marks []json.RawMessage) ([]json.RawMessage, error) {
	src := string(node.Destination)
	if !safeImageURL(src) {
		return c.preserve(LegacyImage, map[string]any{
			"markdown": fmt.Sprintf("![%s](%s)", plainInline(node, c.source), src),
			"src":      src,
		}, marks)
	}
	attrs := map[string]any{"src": src, "alt": plainInline(node, c.source)}
	if len(node.Title) > 0 {
		attrs["title"] = string(node.Title)
	}
	built, err := buildNode("image", attrs, nil, marks, nil)
	if err != nil {
		return nil, err
	}
	return []json.RawMessage{built}, nil
}

// safeImageURL allows http(s) and site-relative references. Everything else —
// data:, javascript:, file:, an unrecognised scheme — is preserved rather than
// rendered.
func safeImageURL(src string) bool {
	switch {
	case strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"):
		return true
	case strings.HasPrefix(src, "/"):
		return !strings.HasPrefix(src, "//") // "//host/x" is scheme-relative
	default:
		return false
	}
}

func (c *converter) legacyHTMLBlock(node *ast.HTMLBlock) (json.RawMessage, error) {
	html := c.lines(node)
	if node.HasClosure() {
		html += string(node.ClosureLine.Value(c.source))
	}
	return buildNode(LegacyHTMLBlock, map[string]any{"html": html}, nil, nil, nil)
}

func (c *converter) legacyHTMLInline(node *ast.RawHTML, marks []json.RawMessage) ([]json.RawMessage, error) {
	var b strings.Builder
	for i := 0; i < node.Segments.Len(); i++ {
		segment := node.Segments.At(i)
		b.Write(segment.Value(c.source))
	}
	return c.preserve(LegacyHTMLInline, map[string]any{"html": b.String()}, marks)
}

// legacyBlock preserves an unrecognised block using its source lines.
func (c *converter) legacyBlock(n ast.Node) (json.RawMessage, error) {
	return buildNode(LegacyBlock, map[string]any{
		"kind":     n.Kind().String(),
		"markdown": c.lines(n),
	}, nil, nil, nil)
}

// legacyInline preserves an unrecognised inline node. Its text is carried too,
// so the content stays searchable even though it is not rendered.
func (c *converter) legacyInline(n ast.Node, marks []json.RawMessage) ([]json.RawMessage, error) {
	return c.preserve(LegacyInline, map[string]any{
		"kind": n.Kind().String(),
		"text": plainInline(n, c.source),
	}, marks)
}

func (c *converter) preserve(nodeType string, attrs map[string]any, marks []json.RawMessage) ([]json.RawMessage, error) {
	built, err := buildNode(nodeType, attrs, nil, marks, nil)
	if err != nil {
		return nil, err
	}
	return []json.RawMessage{built}, nil
}

// lines concatenates a block node's source lines.
func (c *converter) lines(n ast.Node) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		b.Write(segment.Value(c.source))
	}
	return b.String()
}

// plainInline renders an inline subtree's literal text, for alt text and for
// keeping preserved content searchable.
func plainInline(n ast.Node, source []byte) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch node := child.(type) {
		case *ast.Text:
			b.Write(node.Segment.Value(source))
		case *ast.String:
			b.Write(node.Value)
		default:
			b.WriteString(plainInline(child, source))
		}
	}
	return b.String()
}

// appendMark returns marks plus one more, without mutating the caller's slice —
// the same slice is shared by every sibling at this level.
func appendMark(marks []json.RawMessage, markType string, attrs map[string]any) []json.RawMessage {
	built := mustNode(buildNode(markType, attrs, nil, nil, nil))
	out := make([]json.RawMessage, 0, len(marks)+1)
	out = append(out, marks...)
	return append(out, built)
}

// buildNode assembles a node in canonical member order. attrs is marshalled by
// encoding/json, which sorts map keys, so the output is byte-stable across runs
// — which FromMarkdown's determinism contract depends on.
func buildNode(nodeType string, attrs map[string]any, content, marks []json.RawMessage, textValue *string) (json.RawMessage, error) {
	obj := object{}

	typeRaw, err := marshalPlain(nodeType)
	if err != nil {
		return nil, fmt.Errorf("encoding node type: %w", err)
	}
	obj["type"] = typeRaw

	if len(attrs) > 0 {
		attrsRaw, err := marshalPlain(attrs)
		if err != nil {
			return nil, fmt.Errorf("encoding attrs of a %s node: %w", nodeType, err)
		}
		obj["attrs"] = attrsRaw
	}
	if content != nil {
		obj["content"] = encodeArray(content)
	}
	if len(marks) > 0 {
		obj["marks"] = encodeArray(marks)
	}
	if textValue != nil {
		textRaw, err := marshalPlain(*textValue)
		if err != nil {
			return nil, fmt.Errorf("encoding text of a %s node: %w", nodeType, err)
		}
		obj["text"] = textRaw
	}
	return obj.encode()
}

// mustNode is for the build sites whose inputs are literals, where an encoding
// error is impossible rather than merely unlikely.
func mustNode(raw json.RawMessage, err error) json.RawMessage {
	if err != nil {
		panic(fmt.Sprintf("wiki/doc: building a document node from literals: %v", err))
	}
	return raw
}
