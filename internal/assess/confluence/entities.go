package confluence

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RootElement is the element a Confluence space export is wrapped in.
//
// Atlassian's own documentation never names it; it is established by the
// parsers that read this format in production. Treat unknown attributes on it
// as ignorable rather than asserting there are none.
const RootElement = "hibernate-generic"

// maxDistinctClasses bounds the distinct object-class names one scan remembers.
const maxDistinctClasses = 2048

// OverflowClass collects object classes seen after the distinct-name cap.
const OverflowClass = "(other — distinct-class cap reached)"

// ContentStatusCurrent is the contentStatus of a live page.
//
// This matters more than it looks. A space export contains one object per
// historical revision as well as the live page, plus trashed content, so a
// parser that counts every Page object reports several times the real page
// count and resurrects deleted pages. Counting is therefore split: every object
// is counted, and the live subset is counted separately.
const ContentStatusCurrent = "current"

// ErrNotASpaceExport reports that the stream is not a Confluence space export.
var ErrNotASpaceExport = errors.New("confluence: not a space export")

// Object is one exported entity.
//
// A property is either a scalar or a reference to another object, discriminated
// by whether it carries a class attribute and a nested id. Both are kept, in
// separate maps, so a caller asking for a scalar never silently receives an
// object id.
type Object struct {
	// Class is the bare class name (Page, BodyContent, Attachment, ...). The
	// package attribute is deliberately ignored: it is not needed to
	// disambiguate anything in practice.
	Class string
	// ID is the object's own id, as text. Confluence ids are Java longs and
	// production instances reach ten digits, so they are never parsed into an
	// int here — nothing in the assessor does arithmetic on them.
	ID string
	// Props holds scalar properties by name.
	Props map[string]string
	// Refs holds reference properties by name, valued by the referenced id.
	Refs map[string]string
	// Collections holds collection members by collection name, valued by the
	// referenced ids in document order.
	Collections map[string][]string
}

// Prop returns a scalar property, trimmed.
func (o Object) Prop(name string) string { return strings.TrimSpace(o.Props[name]) }

// Census is what a scan of a Confluence entities.xml observed.
type Census struct {
	// Objects counts every object by class, revisions and trash included.
	Objects map[string]int `json:"objects"`
	// Total is the number of objects seen.
	Total int `json:"total"`
	// Truncated is set when the stream ended or faulted mid-document.
	Truncated bool `json:"truncated,omitempty"`
	// TruncationReason is why, kept for the report.
	TruncationReason string `json:"truncation_reason,omitempty"`
	// NameCapReached records that maxDistinctClasses was hit.
	NameCapReached bool `json:"name_cap_reached,omitempty"`
}

// NewCensus returns an empty census.
func NewCensus() *Census { return &Census{Objects: make(map[string]int)} }

func (c *Census) count(class string) {
	c.Total++
	if _, seen := c.Objects[class]; !seen && len(c.Objects) >= maxDistinctClasses {
		c.NameCapReached = true
		c.Objects[OverflowClass]++
		return
	}
	c.Objects[class]++
}

// SortedClassNames returns the observed classes in a stable order.
func (c *Census) SortedClassNames() []string {
	out := make([]string, 0, len(c.Objects))
	for k := range c.Objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ObjectFunc is called for each object of a class the caller asked to collect.
type ObjectFunc func(Object) error

// Scanner streams a Confluence entities.xml, counting every object and
// materialising only the classes a caller asked for.
//
// Bounded memory works the same way as the Jira scanner: objects are decoded
// one at a time and released. That matters most for BodyContent, whose body
// property holds a whole page of storage format — bodies are censused as they
// stream past and never accumulated.
type Scanner struct {
	handlers map[string]ObjectFunc
}

// NewScanner returns a scanner that counts everything and collects nothing.
func NewScanner() *Scanner { return &Scanner{handlers: make(map[string]ObjectFunc)} }

// On registers a handler for one object class, matched case-insensitively.
func (s *Scanner) On(class string, fn ObjectFunc) *Scanner {
	s.handlers[strings.ToLower(class)] = fn
	return s
}

// Scan streams the export, filling the census and dispatching handlers.
func (s *Scanner) Scan(r io.Reader) (*Census, error) {
	if r == nil {
		return nil, errors.New("confluence: nil reader")
	}
	census := NewCensus()

	dec := xml.NewDecoder(r)
	dec.CharsetReader = passthroughCharset

	if err := expectRoot(dec, census); err != nil {
		return census, err
	}
	s.scanObjects(dec, census)
	return census, nil
}

func expectRoot(dec *xml.Decoder, census *Census) error {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: stream ended before any element", ErrNotASpaceExport)
		}
		if err != nil {
			census.Truncated = true
			census.TruncationReason = err.Error()
			return fmt.Errorf("%w: %w", ErrNotASpaceExport, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !strings.EqualFold(start.Name.Local, RootElement) {
			return fmt.Errorf("%w: root element is %q, expected %q",
				ErrNotASpaceExport, start.Name.Local, RootElement)
		}
		return nil
	}
}

func (s *Scanner) scanObjects(dec *xml.Decoder, census *Census) {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			census.Truncated = true
			census.TruncationReason = err.Error()
			return
		}
		start, ok := tok.(xml.StartElement)
		if !ok || !strings.EqualFold(start.Name.Local, "object") {
			continue
		}
		if !s.consumeObject(dec, census, start) {
			return
		}
	}
}

// consumeObject counts one object and dispatches it. It reports whether the
// scan should continue.
func (s *Scanner) consumeObject(dec *xml.Decoder, census *Census, start xml.StartElement) bool {
	class := attrValue(start, "class")
	if class == "" {
		class = "(object with no class)"
	}
	census.count(class)

	fn, wanted := s.handlers[strings.ToLower(class)]
	if !wanted {
		if err := dec.Skip(); err != nil {
			census.Truncated = true
			census.TruncationReason = err.Error()
			return false
		}
		return true
	}

	obj, err := decodeObject(dec, class)
	if err != nil {
		census.Truncated = true
		census.TruncationReason = err.Error()
		return false
	}
	if err := fn(obj); err != nil {
		census.Truncated = true
		census.TruncationReason = err.Error()
		return false
	}
	return true
}

// decodeObject reads one object's id, properties and collections.
func decodeObject(dec *xml.Decoder, class string) (Object, error) {
	obj := Object{
		Class:       class,
		Props:       make(map[string]string),
		Refs:        make(map[string]string),
		Collections: make(map[string][]string),
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return obj, nil
		}
		if err != nil {
			return obj, fmt.Errorf("reading %s object: %w", class, err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			if err := obj.readMember(dec, el); err != nil {
				return obj, err
			}
		case xml.EndElement:
			if strings.EqualFold(el.Name.Local, "object") {
				return obj, nil
			}
		}
	}
}

// readMember dispatches one direct child of an object.
func (o *Object) readMember(dec *xml.Decoder, el xml.StartElement) error {
	switch strings.ToLower(el.Name.Local) {
	case "id":
		text, err := readElementText(dec)
		if err != nil {
			return err
		}
		o.ID = strings.TrimSpace(text)
	case "property":
		return o.readProperty(dec, el)
	case "collection":
		return o.readCollection(dec, el)
	default:
		if err := dec.Skip(); err != nil {
			return fmt.Errorf("skipping %s: %w", el.Name.Local, err)
		}
	}
	return nil
}

// readProperty reads a scalar or reference property.
//
// The two are discriminated by a nested <id>: a reference property carries a
// class attribute and an <id> child, a scalar carries character data. Modelling
// them as one map would let a caller asking for a title receive an object id.
func (o *Object) readProperty(dec *xml.Decoder, el xml.StartElement) error {
	name := attrValue(el, "name")
	var text strings.Builder
	refID := ""

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading property %q: %w", name, err)
		}
		switch inner := tok.(type) {
		case xml.CharData:
			text.Write(inner)
		case xml.StartElement:
			if strings.EqualFold(inner.Name.Local, "id") {
				id, readErr := readElementText(dec)
				if readErr != nil {
					return readErr
				}
				refID = strings.TrimSpace(id)
				continue
			}
			if err := dec.Skip(); err != nil {
				return fmt.Errorf("skipping inside property %q: %w", name, err)
			}
		case xml.EndElement:
			if name == "" {
				return nil
			}
			if refID != "" {
				o.Refs[name] = refID
				return nil
			}
			o.Props[name] = text.String()
			return nil
		}
	}
}

// readCollection reads a collection's element references.
func (o *Object) readCollection(dec *xml.Decoder, el xml.StartElement) error {
	name := attrValue(el, "name")
	var ids []string

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading collection %q: %w", name, err)
		}
		switch inner := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(inner.Name.Local, "element") {
				id, readErr := readElementID(dec)
				if readErr != nil {
					return readErr
				}
				if id != "" {
					ids = append(ids, id)
				}
				continue
			}
			if err := dec.Skip(); err != nil {
				return fmt.Errorf("skipping inside collection %q: %w", name, err)
			}
		case xml.EndElement:
			if name != "" && len(ids) > 0 {
				o.Collections[name] = ids
			}
			return nil
		}
	}
}

// readElementID reads a collection element's referenced id, tolerating an
// element that carries a scalar instead.
//
// It must consume the element's own end tag before returning. Returning as soon
// as the id was read leaves </element> in the stream, where the enclosing
// readCollection mistakes it for the end of the collection — so every member
// after the first is dropped, and a page's history or attachment list silently
// reads as a single entry.
func readElementID(dec *xml.Decoder) (string, error) {
	id := ""
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return id, nil
		}
		if err != nil {
			return id, fmt.Errorf("reading collection element: %w", err)
		}
		switch inner := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(inner.Name.Local, "id") {
				text, readErr := readElementTextTrimmed(dec)
				if readErr != nil {
					return id, readErr
				}
				if id == "" {
					id = text
				}
				continue
			}
			if err := dec.Skip(); err != nil {
				return id, fmt.Errorf("skipping inside collection element: %w", err)
			}
		case xml.EndElement:
			return id, nil
		}
	}
}

// readElementText consumes to the current element's end tag and returns its
// character data.
func readElementText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return b.String(), nil
		}
		if err != nil {
			return b.String(), fmt.Errorf("reading element text: %w", err)
		}
		switch inner := tok.(type) {
		case xml.CharData:
			b.Write(inner)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth == 0 {
				return b.String(), nil
			}
			depth--
		}
	}
}

func readElementTextTrimmed(dec *xml.Decoder) (string, error) {
	s, err := readElementText(dec)
	return strings.TrimSpace(s), err
}

func attrValue(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// passthroughCharset lets the decoder proceed on a declared encoding it does
// not natively support; see the identical note in the jira package.
func passthroughCharset(_ string, input io.Reader) (io.Reader, error) {
	return input, nil
}

// CDATATerminatorEscape is the sequence Confluence writes when a page body
// itself contained a CDATA terminator.
//
// Nested CDATA is illegal in XML, so a body containing "]]>" is written with a
// space inserted. The rewrite is lossy and ambiguous in exactly one direction:
// content that genuinely contained "]] >" is afterwards indistinguishable from
// content that contained "]]>". The assessor counts occurrences and reports
// them rather than silently un-escaping, because guessing would corrupt the
// bodies that were innocent.
const CDATATerminatorEscape = "]] >"

// CountCDATATerminatorEscapes reports how many ambiguous terminator rewrites a
// body contains.
func CountCDATATerminatorEscapes(body string) int {
	return strings.Count(body, CDATATerminatorEscape)
}
