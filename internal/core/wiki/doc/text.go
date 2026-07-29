package doc

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// maxPlainTextBytes caps the plain-text summary of one node. It bounds both the
// az_text attribute on a placeholder and the search projection's contribution
// from preserved content, so a page carrying a megabyte-sized embedded diagram
// does not carry it twice more.
const maxPlainTextBytes = 2000

// PlainText renders a node subtree as plain text.
//
// For ordinary content that is the concatenation of its text nodes. For content
// this schema does not understand there are no text nodes to find, so it falls
// back to every string leaf in the subtree — which is how a preserved
// Confluence macro body, stored as a string attribute, still reaches the search
// index. ADR-0012: indexing an unknown body as plain text is acceptable;
// pretending it renders is not.
func PlainText(raw json.RawMessage) string {
	var texts []string
	collectTextMembers(raw, &texts, 0)
	if len(texts) == 0 {
		collectStringLeaves(raw, &texts, 0)
	}
	return truncate(strings.TrimSpace(strings.Join(texts, " ")), maxPlainTextBytes)
}

// collectTextMembers gathers ProseMirror text-node payloads.
func collectTextMembers(raw json.RawMessage, out *[]string, depth int) {
	if depth > maxDepth {
		return
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return
	}
	if textRaw, ok := obj["text"]; ok {
		var s string
		if json.Unmarshal(textRaw, &s) == nil && s != "" {
			*out = append(*out, s)
		}
	}
	for _, member := range [...]string{"content", "marks"} {
		items, _, err := obj.array(member)
		if err != nil {
			continue
		}
		for _, item := range items {
			collectTextMembers(item, out, depth+1)
		}
	}
}

// collectStringLeaves gathers every string value in an arbitrary JSON subtree,
// skipping this package's own placeholder bookkeeping so a re-shielded
// placeholder does not index its own id and type name as prose.
func collectStringLeaves(raw json.RawMessage, out *[]string, depth int) {
	if depth > maxDepth {
		return
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return
	}
	collectStringLeavesValue(value, out, depth)
}

func collectStringLeavesValue(value any, out *[]string, depth int) {
	if depth > maxDepth {
		return
	}
	switch v := value.(type) {
	case string:
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			*out = append(*out, trimmed)
		}
	case []any:
		for _, item := range v {
			collectStringLeavesValue(item, out, depth+1)
		}
	case map[string]any:
		collectStringLeavesFromMap(v, out, depth)
	}
}

// collectStringLeavesFromMap visits an object's prose-bearing members in a fixed
// order.
//
// Sorted, because Go randomises map iteration and this string ends up in a
// placeholder attribute the client holds. An unstable attribute makes two reads
// of the same page return different bytes, which breaks the determinism the
// read/write id agreement depends on.
func collectStringLeavesFromMap(v map[string]any, out *[]string, depth int) {
	keys := make([]string, 0, len(v))
	for key := range v {
		if isProseMember(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		collectStringLeavesValue(v[key], out, depth+1)
	}
}

// isProseMember reports whether a member's value could be prose. "type" is a
// schema name and the az_* members are this package's own bookkeeping; indexing
// either would put machine identifiers into the search index as English.
func isProseMember(key string) bool {
	return key != "type" && !strings.HasPrefix(key, "az_")
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	// Cut on a rune boundary so the projection stays valid UTF-8.
	cut := limit
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

// utf8Start reports whether b can start a UTF-8 sequence.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// ToMarkdown projects a document to markdown.
//
// This is the value stored in pages.content for a document-backed page, and it
// is derived, never authoritative — see migration 036. It exists for exactly two
// readers: the generated search_vector, which spans title and content, and any
// legacy consumer that has only ever known the markdown column. Nothing reads it
// back into the editor.
//
// Every node type reaches a branch here, and the default branch emits the
// subtree's plain text rather than nothing. That is deliberate: a node type this
// function has not been taught about must still be findable by search, and
// "silently contributes nothing to the index" is the projection's version of
// silent data loss.
func ToMarkdown(document json.RawMessage) (string, error) {
	if err := Validate(document); err != nil {
		return "", err
	}
	var b strings.Builder
	if err := writeBlocks(&b, document, 0); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// writeBlocks writes a container node's children as block-level markdown.
func writeBlocks(b *strings.Builder, raw json.RawMessage, depth int) error {
	if depth > maxDepth {
		return ErrTooDeep
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return err
	}
	children, _, err := obj.array("content")
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := writeBlock(b, child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// blockGap separates two markdown blocks.
const blockGap = "\n\n"

// blockWriter renders one block node. obj is raw already decoded, passed so
// every writer does not decode it again.
type blockWriter func(b *strings.Builder, raw json.RawMessage, obj object, depth int) error

// blockWriters maps a node type to its projection.
//
// A table rather than one long switch, for two reasons. It keeps "which types
// have a projection" answerable at a glance, and it makes the fallback explicit:
// a type with no entry is not skipped, it goes to the plain-text default in
// [writeBlock]. Populated in init() because the writers call back into
// writeBlocks, which would be an initialisation cycle as a package-level literal.
var blockWriters map[string]blockWriter

func init() {
	blockWriters = map[string]blockWriter{
		// Core rich text.
		"paragraph":      writeParagraphBlock,
		"heading":        writeHeadingBlock,
		"codeBlock":      writeCodeBlock,
		"horizontalRule": writeHorizontalRule,
		"bulletList":     writeListBlock,
		"orderedList":    writeListBlock,
		"taskList":       writeListBlock,
		"blockquote":     writeBlockquoteBlock,
		"table":          writeTableBlock,
		"image":          writeImageBlock,

		// ADR-0012 macros.
		"panel":           writePanelBlock,
		"expand":          writeExpandBlock,
		"layout":          writeTransparentBlock,
		"layoutColumn":    writeTransparentBlock,
		"tableOfContents": writeTableOfContentsBlock,
		"childrenDisplay": writeChildrenDisplayBlock,
		"pageInclude":     writePageIncludeBlock,
	}
}

func writeBlock(b *strings.Builder, raw json.RawMessage, depth int) error {
	if depth > maxDepth {
		return ErrTooDeep
	}
	obj, err := decodeObject(raw)
	if err != nil {
		return err
	}
	nodeType, err := obj.typeOf()
	if err != nil {
		return err
	}
	if writer, ok := blockWriters[nodeType]; ok {
		return writer(b, raw, obj, depth)
	}

	// No projection for this type, including every preserved unknown node. Its
	// text still goes in, because "contributes nothing to the search index" is
	// the projection's own version of silent data loss.
	if text := PlainText(raw); text != "" {
		b.WriteString(text)
		b.WriteString(blockGap)
	}
	return nil
}

func writeParagraphBlock(b *strings.Builder, _ json.RawMessage, obj object, depth int) error {
	inline, err := inlineMarkdown(obj, depth)
	if err != nil {
		return err
	}
	b.WriteString(inline)
	b.WriteString(blockGap)
	return nil
}

func writeHeadingBlock(b *strings.Builder, _ json.RawMessage, obj object, depth int) error {
	level := min(max(intAttr(obj, "level", 1), 1), 6)
	inline, err := inlineMarkdown(obj, depth)
	if err != nil {
		return err
	}
	b.WriteString(strings.Repeat("#", level))
	b.WriteByte(' ')
	b.WriteString(inline)
	b.WriteString(blockGap)
	return nil
}

func writeCodeBlock(b *strings.Builder, _ json.RawMessage, obj object, depth int) error {
	language, _ := obj.attrString("language")
	inline, err := inlineMarkdown(obj, depth)
	if err != nil {
		return err
	}
	b.WriteString("```")
	b.WriteString(language)
	b.WriteByte('\n')
	b.WriteString(inline)
	b.WriteString("\n```" + blockGap)
	return nil
}

func writeHorizontalRule(b *strings.Builder, _ json.RawMessage, _ object, _ int) error {
	b.WriteString("---" + blockGap)
	return nil
}

func writeListBlock(b *strings.Builder, _ json.RawMessage, obj object, depth int) error {
	listType, err := obj.typeOf()
	if err != nil {
		return err
	}
	if err := writeList(b, obj, listType, depth); err != nil {
		return err
	}
	b.WriteByte('\n')
	return nil
}

func writeBlockquoteBlock(b *strings.Builder, raw json.RawMessage, _ object, depth int) error {
	var inner strings.Builder
	if err := writeBlocks(&inner, raw, depth); err != nil {
		return err
	}
	writePrefixed(b, inner.String(), "> ")
	b.WriteByte('\n')
	return nil
}

func writeTableBlock(b *strings.Builder, _ json.RawMessage, obj object, depth int) error {
	if err := writeTable(b, obj, depth); err != nil {
		return err
	}
	b.WriteByte('\n')
	return nil
}

func writeImageBlock(b *strings.Builder, _ json.RawMessage, obj object, _ int) error {
	writeImageMarkdown(b, obj)
	b.WriteString(blockGap)
	return nil
}

// writePanelBlock projects an admonition as a labelled blockquote — the closest
// markdown has, and it keeps the panel's kind in the search index.
func writePanelBlock(b *strings.Builder, raw json.RawMessage, obj object, depth int) error {
	kind, ok := obj.attrString("kind")
	if !ok {
		kind = "info"
	}
	var inner strings.Builder
	if err := writeBlocks(&inner, raw, depth); err != nil {
		return err
	}
	fmt.Fprintf(b, "> **%s**\n", strings.ToUpper(kind))
	writePrefixed(b, inner.String(), "> ")
	b.WriteByte('\n')
	return nil
}

func writeExpandBlock(b *strings.Builder, raw json.RawMessage, obj object, depth int) error {
	title, _ := obj.attrString("title")
	fmt.Fprintf(b, "**%s**"+blockGap, title)
	return writeBlocks(b, raw, depth)
}

// writeTransparentBlock contributes a container's children and no framing of its
// own. Markdown has no columns, and inventing one would put noise in the index.
func writeTransparentBlock(b *strings.Builder, raw json.RawMessage, _ object, depth int) error {
	return writeBlocks(b, raw, depth)
}

func writeTableOfContentsBlock(b *strings.Builder, _ json.RawMessage, _ object, _ int) error {
	b.WriteString("[Table of contents]" + blockGap)
	return nil
}

func writeChildrenDisplayBlock(b *strings.Builder, _ json.RawMessage, _ object, _ int) error {
	b.WriteString("[Child pages]" + blockGap)
	return nil
}

func writePageIncludeBlock(b *strings.Builder, _ json.RawMessage, obj object, _ int) error {
	id, _ := obj.attrString("page_id")
	fmt.Fprintf(b, "[Included page: %s]"+blockGap, id)
	return nil
}

// writeImageMarkdown renders an image reference. An uploaded image is addressed
// by its attachment id, which the reading surface turns into an authorised URL;
// a converted legacy image keeps the src it came with.
func writeImageMarkdown(b *strings.Builder, obj object) {
	alt, _ := obj.attrString("alt")
	if id, ok := obj.attrString("attachment_id"); ok && id != "" {
		fmt.Fprintf(b, "![%s](attachment:%s)", alt, id)
		return
	}
	src, _ := obj.attrString("src")
	fmt.Fprintf(b, "![%s](%s)", alt, src)
}

// writeList writes a list and its items. Task items carry their checkbox.
func writeList(b *strings.Builder, obj object, listType string, depth int) error {
	items, _, err := obj.array("content")
	if err != nil {
		return err
	}
	for i, item := range items {
		itemObj, err := decodeObject(item)
		if err != nil {
			return err
		}
		var inner strings.Builder
		if err := writeBlocks(&inner, item, depth+1); err != nil {
			return err
		}
		marker := "- "
		switch listType {
		case "orderedList":
			marker = fmt.Sprintf("%d. ", i+1+intAttr(obj, "start", 1)-1)
		case "taskList":
			marker = "- [ ] "
			if boolAttr(itemObj, "checked") {
				marker = "- [x] "
			}
		}
		writeListItem(b, strings.TrimRight(inner.String(), "\n"), marker)
	}
	return nil
}

// writeListItem writes one item, indenting continuation lines under the marker.
func writeListItem(b *strings.Builder, body, marker string) {
	lines := strings.Split(body, "\n")
	indent := strings.Repeat(" ", len(marker))
	for i, line := range lines {
		if i == 0 {
			b.WriteString(marker)
		} else if line != "" {
			b.WriteString(indent)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// writeTable writes a GFM pipe table. The first row supplies the header.
func writeTable(b *strings.Builder, obj object, depth int) error {
	rows, _, err := obj.array("content")
	if err != nil {
		return err
	}
	for rowIndex, row := range rows {
		rowObj, err := decodeObject(row)
		if err != nil {
			return err
		}
		cells, _, err := rowObj.array("content")
		if err != nil {
			return err
		}
		texts := make([]string, 0, len(cells))
		for _, cell := range cells {
			var inner strings.Builder
			if err := writeBlocks(&inner, cell, depth+1); err != nil {
				return err
			}
			// A cell holds blocks; a pipe table holds one line per cell, so the
			// cell's blocks collapse to a single space-joined line.
			texts = append(texts, collapse(inner.String()))
		}
		b.WriteString("| ")
		b.WriteString(strings.Join(texts, " | "))
		b.WriteString(" |\n")
		if rowIndex == 0 {
			b.WriteString("|")
			for range texts {
				b.WriteString(" --- |")
			}
			b.WriteByte('\n')
		}
	}
	return nil
}

// inlineMarkdown renders a node's inline children with their marks.
func inlineMarkdown(obj object, depth int) (string, error) {
	if depth > maxDepth {
		return "", ErrTooDeep
	}
	children, _, err := obj.array("content")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, child := range children {
		rendered, err := inlineChildMarkdown(child)
		if err != nil {
			return "", err
		}
		b.WriteString(rendered)
	}
	return b.String(), nil
}

// inlineChildMarkdown renders one inline child. The default arm emits the child's
// plain text, so an inline type with no projection — including preserved inline
// content — still reaches the search index.
func inlineChildMarkdown(child json.RawMessage) (string, error) {
	obj, err := decodeObject(child)
	if err != nil {
		return "", err
	}
	childType, err := obj.typeOf()
	if err != nil {
		return "", err
	}

	switch childType {
	case "text":
		text := ""
		if raw, ok := obj["text"]; ok {
			_ = json.Unmarshal(raw, &text)
		}
		return applyMarks(obj, text)
	case "hardBreak":
		// Two trailing spaces then a newline: markdown's hard break.
		return "  \n", nil
	case "statusLozenge":
		label, _ := obj.attrString("text")
		return "`" + label + "`", nil
	case NodeInlineTag:
		// The tag's own text, with the hash, so the projection carries it into
		// the generated search_vector — an inline tag that contributed nothing
		// to the index would be a tag you cannot find by name.
		label, _ := obj.attrString(AttrTagLabel)
		if label == "" {
			return "", nil
		}
		return "#" + label, nil
	case "image":
		var b strings.Builder
		writeImageMarkdown(&b, obj)
		return b.String(), nil
	default:
		return PlainText(child), nil
	}
}

// emphasisWrappers are applied innermost-first, so the projection is
// deterministic whatever order the editor emitted the marks in.
var emphasisWrappers = []struct {
	mark      string
	delimiter string
}{
	{"code", "`"},
	{"bold", "**"},
	{"italic", "*"},
	{"strike", "~~"},
}

// applyMarks wraps text in the markdown for its marks, with the link outermost.
func applyMarks(obj object, text string) (string, error) {
	present, err := markSet(obj)
	if err != nil {
		return "", err
	}
	for _, wrapper := range emphasisWrappers {
		if _, ok := present[wrapper.mark]; ok {
			text = wrapper.delimiter + text + wrapper.delimiter
		}
	}
	if link, ok := present[MarkLink]; ok {
		if target, resolved := linkTarget(link); resolved {
			text = "[" + text + "](" + target + ")"
		} else {
			text += unresolvedLinkSuffix(link, text)
		}
	}
	return text, nil
}

// unresolvedLinkSuffix names the page an unresolved wikilink was aiming at,
// when that is not already the link's own text.
//
// An unresolved link has no destination, so it projects as its own text rather
// than as `[text]()` — a markdown link with an empty target would render as
// broken in every legacy reader and would claim a destination that does not
// exist.
//
// The target title still has to reach the index, though, and for the aliased
// form it is the only place it exists: `[[Runbook|the rota]]` stores "the rota"
// as its text, so a projection of the text alone would make a page that
// explicitly references "Runbook" unfindable by that word. That is the
// projection's own version of silent data loss, which this file refuses
// everywhere else.
//
// The shape follows `[Included page: <id>]` rather than any markdown link
// syntax, for the same reason: it is a label describing a reference, not a
// destination a reader could follow.
func unresolvedLinkSuffix(link object, text string) string {
	target, ok := link.attrString(AttrLinkTargetTitle)
	if !ok {
		return ""
	}
	target = strings.TrimSpace(target)
	if target == "" || strings.EqualFold(target, strings.TrimSpace(text)) {
		return ""
	}
	return " [New page: " + target + "]"
}

// markSet indexes a node's marks by type.
func markSet(obj object) (map[string]object, error) {
	marks, _, err := obj.array("marks")
	if err != nil {
		return nil, err
	}
	present := make(map[string]object, len(marks))
	for _, mark := range marks {
		markObj, err := decodeObject(mark)
		if err != nil {
			return nil, err
		}
		markType, err := markObj.typeOf()
		if err != nil {
			return nil, err
		}
		present[markType] = markObj
	}
	return present, nil
}

// linkTarget renders a link mark's destination, reporting whether it has one.
//
// A link mark can be in three states, and they are not variants of each other:
//
//	href set          an external link, projected as itself
//	page_id set       a resolved internal link. The page id rather than a URL,
//	                  because a page's URL depends on the space it is being read
//	                  in and the document must not bake one in.
//	target_title set  an UNRESOLVED wikilink: the author named a page that does
//	                  not exist yet. There is no destination, so there is nothing
//	                  to project — the caller emits the link's text alone.
//
// The false return covers the third state and a malformed link with no
// destination at all, which projects the same way for the same reason.
func linkTarget(link object) (string, bool) {
	if href, ok := link.attrString(AttrLinkHref); ok && href != "" {
		return href, true
	}
	if pageID, ok := link.attrString(AttrLinkPageID); ok && pageID != "" {
		return "page:" + pageID, true
	}
	return "", false
}

// writePrefixed writes body with prefix on every line, which is how blockquote
// and panel bodies are emitted.
func writePrefixed(b *strings.Builder, body, prefix string) {
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// collapse folds a multi-line block into one space-separated line.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func intAttr(obj object, name string, fallback int) int {
	attrsRaw, ok := obj["attrs"]
	if !ok {
		return fallback
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(attrsRaw, &attrs); err != nil {
		return fallback
	}
	raw, ok := attrs[name]
	if !ok {
		return fallback
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return fallback
	}
	return n
}

func boolAttr(obj object, name string) bool {
	attrsRaw, ok := obj["attrs"]
	if !ok {
		return false
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(attrsRaw, &attrs); err != nil {
		return false
	}
	raw, ok := attrs[name]
	if !ok {
		return false
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return v
}
