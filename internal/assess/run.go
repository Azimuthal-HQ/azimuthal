package assess

import (
	"errors"
	"fmt"
	"io"

	"github.com/Azimuthal-HQ/azimuthal/internal/assess/archive"
	"github.com/Azimuthal-HQ/azimuthal/internal/assess/jql"
)

// EntitiesFile is the entry basename both export formats carry their data in.
// Jira and Confluence chose the same name independently; the root element is
// what tells them apart.
const EntitiesFile = "entities.xml"

// ErrNoInput reports that neither export path was given.
var ErrNoInput = errors.New("assess: no export given")

// Input names the exports to assess. Either may be empty; at least one must not
// be.
//
// There is deliberately no DSN field, no connection string, and no credential.
// The assessor cannot write, and it cannot read a database either — see the
// package doc and TestNoDatabaseReachability.
type Input struct {
	// JiraPath is a Jira Cloud backup zip.
	JiraPath string
	// ConfluencePath is a Confluence space export zip.
	ConfluencePath string
}

// SourceReport describes one export that was read.
type SourceReport struct {
	// Kind is "jira" or "confluence".
	Kind string `json:"kind"`
	// Path is the archive as given.
	Path string `json:"path"`
	// Rows is how many entities the parser counted.
	Rows int `json:"rows"`
	// EntryCount and AttachmentBytes describe the attachment tree, read from
	// the zip's own directory without decompressing anything.
	Attachments     int    `json:"attachments"`
	AttachmentBytes uint64 `json:"attachment_bytes"`
	// Truncated reports that the export could not be read to the end.
	Truncated bool `json:"truncated,omitempty"`
	// TruncationReason is why.
	TruncationReason string `json:"truncation_reason,omitempty"`
	// UnknownTypes lists entity or object types this build does not classify.
	UnknownTypes []string `json:"unknown_types,omitempty"`
}

// Result is a complete assessment.
type Result struct {
	// Sources describes what was read.
	Sources []SourceReport `json:"sources"`
	// Ledger is the bucketed assessment.
	Ledger *Ledger `json:"ledger"`
	// Collisions are contended item-key prefixes.
	Collisions []Collision `json:"key_collisions,omitempty"`
	// Coercions are keys that had to change shape.
	Coercions []KeyOrigin `json:"key_coercions,omitempty"`
	// Filters are the classified JQL saved filters.
	Filters []jql.Query `json:"filters,omitempty"`
	// Assumptions are the things this build had to assume about the formats.
	// They are part of the deliverable, not a footnote: the export formats are
	// undocumented, and a reader deciding whether to trust the numbers needs to
	// know which of them rest on an inference.
	Assumptions []string `json:"assumptions"`
}

// Run assesses the given exports.
//
// Both may be supplied at once, and doing so is what makes cross-export key
// collision detection possible — a Jira project and a Confluence space can
// claim the same space key, and neither export can reveal that alone.
func Run(in Input) (*Result, error) {
	if in.JiraPath == "" && in.ConfluencePath == "" {
		return nil, fmt.Errorf("%w: give --jira, --confluence, or both", ErrNoInput)
	}

	res := &Result{Ledger: &Ledger{}, Assumptions: formatAssumptions()}
	keys := NewKeyRegistry()

	if in.JiraPath != "" {
		if err := runJira(in.JiraPath, res, keys); err != nil {
			return nil, err
		}
	}
	if in.ConfluencePath != "" {
		if err := runConfluence(in.ConfluencePath, res, keys); err != nil {
			return nil, err
		}
	}

	res.Collisions = keys.Collisions()
	res.Coercions = keys.Coercions()

	if err := res.Ledger.Reconcile(); err != nil {
		return nil, err
	}
	if err := res.Ledger.ReconcileRows(res.totalRows()); err != nil {
		return nil, err
	}
	return res, nil
}

func (r *Result) totalRows() int {
	n := 0
	for _, s := range r.Sources {
		n += s.Rows
	}
	return n
}

func runJira(path string, res *Result, keys *KeyRegistry) error {
	a, err := archive.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	entry, err := a.OpenBase(EntitiesFile)
	if err != nil {
		return fmt.Errorf("reading the Jira export: %w", err)
	}
	defer func() { _ = entry.Close() }()

	census, filters, err := AssessJira(entry, res.Ledger, keys)
	if err != nil {
		return err
	}
	res.Filters = append(res.Filters, filters...)

	count, bytes := a.CountUnder("attachments")
	res.Sources = append(res.Sources, SourceReport{
		Kind: "jira", Path: path, Rows: census.Rows,
		Attachments: count, AttachmentBytes: bytes,
		Truncated: census.Truncated, TruncationReason: census.TruncationReason,
		UnknownTypes: census.SortedEntityNames(),
	})
	return nil
}

func runConfluence(path string, res *Result, keys *KeyRegistry) error {
	a, err := archive.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	entry, err := a.OpenBase(EntitiesFile)
	if err != nil {
		return fmt.Errorf("reading the Confluence export: %w", err)
	}
	defer func() { _ = entry.Close() }()

	census, err := AssessConfluence(entry, res.Ledger, keys)
	if err != nil {
		return err
	}

	count, bytes := a.CountUnder("attachments")
	res.Sources = append(res.Sources, SourceReport{
		Kind: "confluence", Path: path, Rows: census.Total,
		Attachments: count, AttachmentBytes: bytes,
		Truncated: census.Truncated, TruncationReason: census.TruncationReason,
		UnknownTypes: census.SortedClassNames(),
	})
	return nil
}

// RunReaders assesses already-opened streams, for tests and for callers that
// have the entities.xml without the surrounding zip.
func RunReaders(jiraXML, confluenceXML io.Reader) (*Result, error) {
	res := &Result{Ledger: &Ledger{}, Assumptions: formatAssumptions()}
	keys := NewKeyRegistry()

	if jiraXML != nil {
		census, filters, err := AssessJira(jiraXML, res.Ledger, keys)
		if err != nil {
			return nil, err
		}
		res.Filters = append(res.Filters, filters...)
		res.Sources = append(res.Sources, SourceReport{
			Kind: "jira", Path: "(stream)", Rows: census.Rows,
			Truncated: census.Truncated, TruncationReason: census.TruncationReason,
			UnknownTypes: census.SortedEntityNames(),
		})
	}
	if confluenceXML != nil {
		census, err := AssessConfluence(confluenceXML, res.Ledger, keys)
		if err != nil {
			return nil, err
		}
		res.Sources = append(res.Sources, SourceReport{
			Kind: "confluence", Path: "(stream)", Rows: census.Total,
			Truncated: census.Truncated, TruncationReason: census.TruncationReason,
			UnknownTypes: census.SortedClassNames(),
		})
	}

	res.Collisions = keys.Collisions()
	res.Coercions = keys.Coercions()

	if err := res.Ledger.Reconcile(); err != nil {
		return nil, err
	}
	if err := res.Ledger.ReconcileRows(res.totalRows()); err != nil {
		return nil, err
	}
	return res, nil
}

// formatAssumptions is the honest ledger the importer inherits.
//
// Neither export format is documented as a contract. Atlassian says so
// explicitly of the Jira one — "an XML version of the underlying entity model"
// that changes as fields are added — and never names the Confluence root
// element at all. Every claim below is one this build had to make in order to
// read anything, and every one of them is a place a real export could differ.
func formatAssumptions() []string {
	return []string{
		"Jira: the entity export is XML (entities.xml with an <entity-engine-xml> root), not JSON. Atlassian publishes the archive layout but explicitly declines to document the XML, so entity and field names here follow the OfBiz entity model and may differ on a Cloud instance, which runs a fork.",
		"Jira: field names are OfBiz field-names, not SQL column names — Action.type not actiontype, Issue.key not pkey. A parser written from the database schema documentation reads nothing.",
		"Jira: fields are read from both XML attributes and child elements, because a value carrying newlines cannot live in an attribute. Which form a given field takes has not been verified against a real export.",
		"Jira: issue type, status and priority are id references, resolved here against the IssueType, Status and Priority rows in the same file; an id with no matching row is reported as the id itself rather than dropped. Resolution is Jira's own separate field and has no Azimuthal equivalent at all.",
		"Jira: comment visibility is read from Action.level (group) and Action.rolelevel (project role). Both empty means unrestricted.",
		"Jira: saved filters are read from SearchRequest rows. The JQL text is taken from a \"request\" field, falling back to \"query\".",
		"Confluence: the root element <hibernate-generic> is established by the parsers that read this format in production, not by Atlassian documentation.",
		"Confluence: a page's body is reached through its bodyContents collection to a BodyContent object. Historical revisions and trashed pages are separate Page objects, separated here by contentStatus.",
		"Confluence: storage-format bodies carry the ac: and ri: prefixes without declaring them, so they are matched by prefix as well as by namespace URI.",
		"Confluence: a body's own \"]]>\" is rewritten to \"]] >\" because nested CDATA is illegal. The rewrite is ambiguous and is counted rather than undone.",
		"Both: attachment blobs are counted from the zip directory by path prefix, not matched to their metadata rows. An attachment whose blob is missing from the archive would not be detected here.",
		"Both: an export is read as bytes whatever encoding it declares, so element and attribute names are trusted to be ASCII. Non-ASCII values from a legacy-encoded export may be mojibake.",
	}
}
