// Package confluence parses a Confluence space export (the XML zip) far enough
// to assess it. It reads; it never writes anything anywhere.
package confluence

import (
	"encoding/xml"
	"errors"
	"io"
	"sort"
	"strings"
)

// maxDistinctNames bounds how many distinct construct names one scan remembers.
//
// The census is a map, and a map keyed by content-controlled strings is the one
// place a streaming parser can still use unbounded memory: a body with a million
// distinct macro names would hold a million keys however carefully the bytes are
// streamed. Past the cap, names are counted into OverflowName instead, so the
// totals stay exact even when the naming does not.
const maxDistinctNames = 512

// OverflowName collects constructs seen after the distinct-name cap. It is
// reported like any other name so the report can say the cap was reached rather
// than quietly under-listing.
const OverflowName = "(other — distinct-name cap reached)"

// UnnamedMacro is how a structured macro with no ac:name attribute is counted.
// It is still a construct an import would have to decide about, so it is named
// rather than skipped.
const UnnamedMacro = "(macro with no ac:name)"

// Namespace identity for the two Confluence storage-format prefixes.
//
// Both forms have to be recognised. The storage-format fragment stored inside
// entities.xml uses the ac: and ri: prefixes but carries no xmlns declarations
// of its own — they live on the page template, not in the exported body — and
// Go reports an undeclared prefix by leaving the literal prefix in Name.Space.
// A body that *does* declare them (some tooling re-serialises it that way)
// arrives with Name.Space resolved to the namespace URI instead. Matching only
// one form silently reclassifies every macro in the other as plain HTML, which
// would report a page full of macros as mapping cleanly.
const (
	prefixAC = "ac"
	prefixRI = "ri"

	// Matched as substrings so a version-specific URI still resolves.
	uriMarkerAC = "atlassian.com/content"
	uriMarkerRI = "resource/identifier"
)

// ns is the normalised namespace of an element.
type ns int

const (
	nsHTML ns = iota
	nsAC
	nsRI
)

// classifyNS normalises Name.Space, which is either a literal prefix or a
// resolved URI depending on whether the body declared its namespaces.
func classifyNS(space string) ns {
	switch {
	case space == prefixAC, strings.Contains(space, uriMarkerAC):
		return nsAC
	case space == prefixRI, strings.Contains(space, uriMarkerRI):
		return nsRI
	default:
		return nsHTML
	}
}

// syntheticOpen wraps a body so the decoder sees one document.
//
// A storage-format body is a fragment with any number of top-level elements,
// and encoding/xml stops after the first one closes. Wrapping is what lets a
// whole page be counted rather than just its opening paragraph. The name is
// deliberately one no content could collide with, and it is excluded from the
// census by depth rather than by name.
//
// The matching close tag is deliberately NOT appended, because it would destroy
// the only reliable truncation signal: a non-strict decoder silently pops every
// element still open when it meets the close tag, so a body cut off inside
// three nested elements would balance to zero and read as well-formed. Leaving
// the root open instead means a clean body ends at depth exactly 1 — see scan.
const syntheticOpen = "<azimuthal-assess-root>"

// syntheticFlush is appended after the body so a trailing void element gets its
// synthesised end tag before EOF.
//
// encoding/xml emits the end tag for an auto-closed element only when the next
// token is read, so a body ending in <hr> or <img> would otherwise finish one
// level deep and be misreported as truncated. A single space is enough to flush
// it and cannot itself change the census, which counts only elements.
const syntheticFlush = " "

// balancedDepth is the token depth a well-formed fragment ends at: the
// synthetic root, still open, and nothing else.
const balancedDepth = 1

// autoCloseVoidElements is xml.HTMLAutoClose with "link" removed.
//
// This is not a tidy-up; it is a correctness fix. encoding/xml matches
// AutoClose entries against Name.Local and ignores the namespace, so the HTML
// void element <link> also matches Confluence's <ac:link> — one of the most
// common constructs in storage format. With the stock list the decoder closes
// <ac:link> immediately, then fails the whole document on the real </ac:link>,
// and every page containing a link reports as truncated. Dropping "link" is
// safe here because a bare HTML <link> is a document-head element that does not
// occur in Confluence body content.
var autoCloseVoidElements = []string{
	"basefont", "br", "area", "img", "param", "hr",
	"input", "col", "frame", "isindex", "base", "meta",
}

// BodyCensus is what one page body contains, counted by construct.
//
// It is deliberately a census and not a parse tree: the assessor needs to know
// how many of each construct exist and what they are called, not what they nest
// inside. Counting by name is also what lets an unrecognised construct be
// reported rather than dropped, which is the preservation philosophy applied to
// reading rather than writing.
type BodyCensus struct {
	// Macros counts ac:structured-macro by its ac:name attribute.
	Macros map[string]int `json:"macros,omitempty"`
	// Elements counts every other ac:/ri: element, keyed "ac:name"/"ri:name".
	Elements map[string]int `json:"elements,omitempty"`
	// HTMLElements counts the plain XHTML elements (p, h1, table, ...).
	HTMLElements map[string]int `json:"html_elements,omitempty"`
	// Truncated is set when the body ended or faulted mid-document. The census
	// up to that point is still returned and still counted: a page whose last
	// macro is unclosed should report what it did contain, not nothing.
	Truncated bool `json:"truncated,omitempty"`
	// TruncationReason is the decoder's own message, kept so the report can say
	// why rather than only that. Empty unless Truncated.
	TruncationReason string `json:"truncation_reason,omitempty"`
	// NameCapReached records that maxDistinctNames was hit and some constructs
	// were counted under OverflowName.
	NameCapReached bool `json:"name_cap_reached,omitempty"`
}

// NewBodyCensus returns an empty census ready to accumulate.
func NewBodyCensus() *BodyCensus {
	return &BodyCensus{
		Macros:       make(map[string]int),
		Elements:     make(map[string]int),
		HTMLElements: make(map[string]int),
	}
}

// count increments name in m, honouring the distinct-name cap.
func (c *BodyCensus) count(m map[string]int, name string) {
	if name == "" {
		name = UnnamedMacro
	}
	if _, seen := m[name]; !seen && len(m) >= maxDistinctNames {
		c.NameCapReached = true
		m[OverflowName]++
		return
	}
	m[name]++
}

// Total is every construct counted, across all three maps.
func (c *BodyCensus) Total() int {
	return SumCounts(c.Macros) + SumCounts(c.Elements) + SumCounts(c.HTMLElements)
}

// MacroTotal is the number of structured macros counted.
func (c *BodyCensus) MacroTotal() int { return SumCounts(c.Macros) }

// SumCounts totals a census map.
func SumCounts(m map[string]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

// SortedNames returns the keys of a census map in a stable order, so report
// output does not change between runs over the same input.
func SortedNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ScanBody censuses one page body in Confluence storage format.
//
// # Why the decoder is configured this way
//
// Storage format is XHTML-ish, not XML, and it arrives inside entities.xml as
// escaped character data. Four things break a default encoding/xml decoder on
// real exports, and all four are handled here rather than discovered later:
// undeclared ac:/ri: prefixes (see the namespace constants), HTML entities that
// XML does not define (&nbsp; the common one, hence xml.HTMLEntity), unclosed
// void tags that real content accumulates (hence Strict=false and the
// autoCloseVoidElements list, and see that list's comment for the ac:link
// collision), and the fragment having no single root (hence syntheticRoot).
//
// A malformed body is not an error: the census gathered before the fault is
// returned with Truncated set and the decoder's message kept. Refusing to
// report anything about a page because its last macro was unclosed would lose
// more information than it protects.
func ScanBody(r io.Reader) (*BodyCensus, error) {
	if r == nil {
		return nil, errors.New("confluence: nil body reader")
	}
	census := NewBodyCensus()
	census.scan(io.MultiReader(
		strings.NewReader(syntheticOpen), r, strings.NewReader(syntheticFlush),
	))
	return census, nil
}

// scan drives the decoder and fills the census.
//
// Truncation is decided by depth rather than by the decoder's error text.
// Because the synthetic root is never closed, every scan ends in an error, and
// matching on its wording would couple this package to encoding/xml's internal
// messages. Depth says the same thing without that coupling: a fragment whose
// own elements all closed leaves exactly the root open (balancedDepth), one cut
// off inside three elements leaves four, and a stray end tag leaves zero.
//
// One case is deliberately not detected here: a body cut off in the middle of a
// tag name or attribute ends at balancedDepth and reads as clean. That is a
// truncation of the containing stream rather than of this body, and it is
// caught where it is meaningful — when reading entities.xml itself.
func (c *BodyCensus) scan(r io.Reader) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = autoCloseVoidElements
	dec.Entity = xml.HTMLEntity

	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			if depth != balancedDepth {
				c.Truncated = true
				c.TruncationReason = truncationReason(err, depth)
			}
			return
		}
		switch el := tok.(type) {
		case xml.StartElement:
			depth++
			// balancedDepth is the synthetic root, which is not content.
			if depth > balancedDepth {
				c.record(el)
			}
		case xml.EndElement:
			depth--
		}
	}
}

// truncationReason describes an unbalanced body in terms a report can print.
func truncationReason(err error, depth int) string {
	if unclosed := depth - balancedDepth; unclosed > 0 {
		return "body ended with " + itoa(unclosed) + " element(s) still open: " + err.Error()
	}
	return err.Error()
}

// itoa avoids importing strconv for one call on an error path.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// record classifies one start element into the census.
func (c *BodyCensus) record(el xml.StartElement) {
	switch classifyNS(el.Name.Space) {
	case nsAC:
		if el.Name.Local == "structured-macro" {
			c.count(c.Macros, macroName(el))
			return
		}
		c.count(c.Elements, prefixAC+":"+el.Name.Local)
	case nsRI:
		c.count(c.Elements, prefixRI+":"+el.Name.Local)
	case nsHTML:
		c.count(c.HTMLElements, strings.ToLower(el.Name.Local))
	}
}

// macroName reads the ac:name attribute that identifies a structured macro.
func macroName(el xml.StartElement) string {
	for _, attr := range el.Attr {
		if attr.Name.Local != "name" {
			continue
		}
		if classifyNS(attr.Name.Space) == nsAC || attr.Name.Space == "" {
			if v := strings.TrimSpace(attr.Value); v != "" {
				return v
			}
		}
	}
	return UnnamedMacro
}

// ScanBodyString is ScanBody over an in-memory body, for the common case where
// the body has already been read out of entities.xml.
func ScanBodyString(body string) *BodyCensus {
	// ScanBody's only error is a nil reader, which cannot happen here.
	census, _ := ScanBody(strings.NewReader(body))
	return census
}

// Merge folds another census into this one, for accumulating across pages.
func (c *BodyCensus) Merge(other *BodyCensus) {
	if other == nil {
		return
	}
	c.mergeCounts(c.Macros, other.Macros)
	c.mergeCounts(c.Elements, other.Elements)
	c.mergeCounts(c.HTMLElements, other.HTMLElements)
	if other.Truncated {
		c.Truncated = true
		if c.TruncationReason == "" {
			c.TruncationReason = other.TruncationReason
		}
	}
	c.NameCapReached = c.NameCapReached || other.NameCapReached
}

func (c *BodyCensus) mergeCounts(dst, src map[string]int) {
	for _, name := range SortedNames(src) {
		if _, seen := dst[name]; !seen && len(dst) >= maxDistinctNames {
			c.NameCapReached = true
			dst[OverflowName] += src[name]
			continue
		}
		dst[name] += src[name]
	}
}
