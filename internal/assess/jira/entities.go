// Package jira parses a Jira backup export far enough to assess it. It reads;
// it never writes anything anywhere.
//
// # The format, and what is actually known about it
//
// A Jira Cloud backup is a zip of XML, not JSON: entities.xml (the core entity
// model) and activeobjects.xml (plugin data, including the Agile sprint and
// board tables). Atlassian publishes the archive layout but explicitly declines
// to document the XML itself — the support article answering "is the format
// documented" says it is "an XML version of the underlying entity model, pulled
// out of the database", changing as fields and entities are added.
//
// That undocumented, drifting shape is the governing design constraint. A
// parser with a fixed struct per entity would silently miss whatever the
// running Jira version added, and silence is the one outcome this tool cannot
// produce. So the reader is generic: it streams rows, counts every entity type
// by its own name, and classification happens afterwards against a table of
// types the assessor understands. An entity nobody anticipated is counted and
// named in the report rather than dropped.
package jira

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RootElement is the element every Jira entity export is wrapped in. Atlassian's
// own restore path fails with a SAX error when it is missing, which makes it the
// one structural marker worth checking before trusting the rest.
const RootElement = "entity-engine-xml"

// maxDistinctEntities bounds the number of distinct entity-type names one scan
// remembers, for the same reason the Confluence census is bounded: the map is
// keyed by strings the archive controls.
const maxDistinctEntities = 2048

// OverflowEntity collects entity types seen after the distinct-name cap.
const OverflowEntity = "(other — distinct-entity cap reached)"

// ErrNotAnEntityExport reports that the stream is not a Jira entity export.
var ErrNotAnEntityExport = errors.New("jira: not an entity export")

// Row is one entity row: its type and its fields.
//
// Fields are gathered from BOTH XML attributes and child elements. Short scalar
// columns are serialised as attributes, and Atlassian's own error messages
// confirm long text such as an issue description arrives that way too. But the
// entity engine is not documented to do so universally, and a value carrying
// newlines cannot portably live in an attribute — so a child element with
// character data is read as a field of the same name. Reading only attributes
// would yield empty descriptions and comment bodies across an entire export,
// which is exactly the failure that looks like success.
type Row struct {
	// Type is the element name, which is the OfBiz entity name (Issue,
	// Project, Action, ...).
	Type string
	// Fields holds the row's values. A child element and an attribute of the
	// same name cannot both be present in practice; if they are, the attribute
	// is kept and the duplicate is not silently merged.
	Fields map[string]string
}

// Get returns a field value, trimmed. Missing fields return "".
func (r Row) Get(name string) string { return strings.TrimSpace(r.Fields[name]) }

// Census is what a scan of entities.xml observed.
type Census struct {
	// Entities counts rows by entity type.
	Entities map[string]int `json:"entities"`
	// Rows is the total number of rows seen, equal to the sum of Entities.
	Rows int `json:"rows"`
	// Truncated is set when the stream ended or faulted mid-document. Whatever
	// was counted before the fault is kept.
	Truncated bool `json:"truncated,omitempty"`
	// TruncationReason is why, kept for the report.
	TruncationReason string `json:"truncation_reason,omitempty"`
	// NameCapReached records that maxDistinctEntities was hit.
	NameCapReached bool `json:"name_cap_reached,omitempty"`
}

// NewCensus returns an empty census.
func NewCensus() *Census { return &Census{Entities: make(map[string]int)} }

func (c *Census) count(name string) {
	c.Rows++
	if _, seen := c.Entities[name]; !seen && len(c.Entities) >= maxDistinctEntities {
		c.NameCapReached = true
		c.Entities[OverflowEntity]++
		return
	}
	c.Entities[name]++
}

// SortedEntityNames returns the observed entity types in a stable order.
func (c *Census) SortedEntityNames() []string {
	out := make([]string, 0, len(c.Entities))
	for k := range c.Entities {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RowFunc is called for each row of an entity type the caller asked to collect.
// Returning an error stops the scan and is returned from Scan.
type RowFunc func(Row) error

// Scanner streams entities.xml, counting every row and materialising only the
// ones a caller asked for.
//
// This is the bounded-memory contract. A multi-gigabyte export is decoded token
// by token; a row is built only while it is being handed to a RowFunc and is
// released immediately after. Nothing accumulates except the per-type counters
// and whatever the caller chooses to keep.
type Scanner struct {
	handlers map[string]RowFunc
}

// NewScanner returns a scanner that counts everything and collects nothing.
func NewScanner() *Scanner { return &Scanner{handlers: make(map[string]RowFunc)} }

// On registers a handler for one entity type. The type name is matched
// case-insensitively because the export's casing is not a documented contract.
func (s *Scanner) On(entityType string, fn RowFunc) *Scanner {
	s.handlers[strings.ToLower(entityType)] = fn
	return s
}

// Scan streams the export, filling the census and dispatching handlers.
//
// A malformed or truncated stream is not an error: the census gathered before
// the fault is returned with Truncated set. A stream that is not an entity
// export at all — no entity-engine-xml root — is an error, because reporting
// "0 issues" for a file that was never a Jira export would be a lie the reader
// cannot detect.
func (s *Scanner) Scan(r io.Reader) (*Census, error) {
	if r == nil {
		return nil, errors.New("jira: nil reader")
	}
	census := NewCensus()

	dec := xml.NewDecoder(r)
	// Real exports carry Windows-1252 and other legacy encodings in the XML
	// declaration; without a CharsetReader the decoder refuses them outright.
	dec.CharsetReader = passthroughCharset

	if err := expectRoot(dec, census); err != nil {
		return census, err
	}
	if err := s.scanRows(dec, census); err != nil {
		return census, err
	}
	return census, nil
}

// expectRoot advances to the root element and verifies it.
func expectRoot(dec *xml.Decoder, census *Census) error {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: stream ended before any element", ErrNotAnEntityExport)
		}
		if err != nil {
			census.Truncated = true
			census.TruncationReason = err.Error()
			return fmt.Errorf("%w: %w", ErrNotAnEntityExport, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !strings.EqualFold(start.Name.Local, RootElement) {
			return fmt.Errorf("%w: root element is %q, expected %q",
				ErrNotAnEntityExport, start.Name.Local, RootElement)
		}
		return nil
	}
}

// scanRows consumes the rows under the root.
func (s *Scanner) scanRows(dec *xml.Decoder, census *Census) error {
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			census.Truncated = true
			census.TruncationReason = err.Error()
			return nil
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if err := s.consumeRow(dec, census, start); err != nil {
			return err
		}
	}
}

// consumeRow counts one row and dispatches it if a handler wants it.
func (s *Scanner) consumeRow(dec *xml.Decoder, census *Census, start xml.StartElement) error {
	name := start.Name.Local
	census.count(name)

	fn, wanted := s.handlers[strings.ToLower(name)]
	if !wanted {
		// Not collected: skip the subtree without building anything.
		if err := dec.Skip(); err != nil {
			census.Truncated = true
			census.TruncationReason = err.Error()
		}
		return nil
	}

	row, err := decodeRow(dec, start)
	if err != nil {
		census.Truncated = true
		census.TruncationReason = err.Error()
		return nil
	}
	if err := fn(row); err != nil {
		return fmt.Errorf("handling %s row: %w", name, err)
	}
	return nil
}

// decodeRow materialises one row's fields from attributes and child elements.
func decodeRow(dec *xml.Decoder, start xml.StartElement) (Row, error) {
	row := Row{Type: start.Name.Local, Fields: make(map[string]string, len(start.Attr))}
	for _, attr := range start.Attr {
		row.Fields[attr.Name.Local] = attr.Value
	}
	if err := readChildFields(dec, &row); err != nil {
		return row, err
	}
	return row, nil
}

// readChildFields folds child elements into the row's fields.
func readChildFields(dec *xml.Decoder, row *Row) error {
	depth := 0
	var current string
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s row: %w", row.Type, err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				current = el.Name.Local
				text.Reset()
			}
		case xml.CharData:
			// Accumulated at any depth inside the field, so a value carrying
			// markup (a description with inline HTML) contributes its text
			// rather than reading as empty.
			if depth >= 1 {
				text.Write(el)
			}
		case xml.EndElement:
			if depth == 0 {
				return nil // the row's own end element
			}
			if depth == 1 && current != "" {
				// An attribute of the same name wins; a child element only
				// fills a field the attributes did not already set.
				if _, taken := row.Fields[current]; !taken {
					row.Fields[current] = text.String()
				}
				current = ""
			}
			depth--
		}
	}
}

// passthroughCharset lets the decoder proceed on a declared encoding it does
// not natively support.
//
// Go's encoding/xml handles UTF-8 and refuses everything else unless a
// CharsetReader is supplied. Jira exports from older or non-UTF-8 instances
// declare windows-1252 or iso-8859-1, and refusing the whole file for a
// declaration in its first line would be a worse answer than reading it as
// bytes: the assessor counts structure, and element and attribute names in
// these exports are ASCII. Non-ASCII values may be mojibake, which is recorded
// as an assumption in the report rather than hidden.
func passthroughCharset(_ string, input io.Reader) (io.Reader, error) {
	return input, nil
}
