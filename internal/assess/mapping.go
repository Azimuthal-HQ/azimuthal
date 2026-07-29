package assess

import (
	"regexp"
	"sort"
	"strings"
)

// This file is the mapping matrix: what Azimuthal can actually hold, stated
// once, with the source of each fact named. Every claim below is checked
// against the repository by mapping_test.go, so a schema change breaks a test
// rather than quietly making the report wrong.

// SpaceKeyPattern is the space key format, from validKey in
// internal/core/api/spaces/handler.go.
//
// It is the tightest constraint either export meets: uppercase alphanumeric,
// at most ten characters, no separators. Jira project keys mostly fit; a
// Confluence space key routinely does not, because Confluence allows lowercase
// and longer keys.
var SpaceKeyPattern = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)

// SlugPattern is the item-type and custom-field slug format, from
// item_types_slug_format (migration 032) and custom_field_defs_slug_format
// (migration 033).
//
// Note the absence of a hyphen. Jira's "Sub-task" cannot keep its name.
var SlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

// SeededItemKinds are the item types every org is created with (migration 032).
// An admin can add more at runtime, so a Jira issue type outside this set is an
// approximation rather than a loss — provided the slug can be coerced.
var SeededItemKinds = []string{"task", "story", "bug", "epic"}

// SeededTicketStates are the default Beacon workflow states, and their
// categories, from migration 016.
var SeededTicketStates = map[string]string{
	"open": "todo", "in_progress": "in_progress", "resolved": "done", "closed": "done",
}

// Priorities is the closed four-value set CHECK-constrained on both tickets and
// project_items, and validated again in internal/core/views/filter.go.
var Priorities = []string{"urgent", "high", "medium", "low"}

// CustomFieldTypes is the complete implemented set, from the field_type CHECK
// in migration 033. Four types against Jira's twenty-odd is the single largest
// source of approximation in an issue import.
var CustomFieldTypes = []string{"text", "number", "date", "single_select"}

// AttachmentEntityTypes is what an attachment may hang off, from the CHECK in
// migration 027.
var AttachmentEntityTypes = []string{"page", "ticket", "project_item"}

// jiraCustomFieldMap maps Jira's customfieldtypes keys onto Azimuthal's four.
//
// The keys are the suffix after "com.atlassian.jira.plugin.system.customfieldtypes:".
// A type absent from this map is unmappable, and the map's own shortness is the
// finding: Jira's cascading and calculated types have no representation at all,
// and the multi-valued pickers collapse to a single select or to text.
var jiraCustomFieldMap = map[string]struct {
	target  string
	verdict Verdict
	reason  string
}{
	"textfield":            {"text", VerdictClean, "a single-line text field maps directly onto the text type"},
	"textarea":             {"text", VerdictClean, "a multi-line text field maps onto the text type"},
	"url":                  {"text", VerdictApproximated, "there is no url type; the value is kept as text and stops being validated as a URL"},
	"readonlyfield":        {"text", VerdictApproximated, "there is no read-only type; the value is kept as text and becomes editable"},
	"importid":             {"text", VerdictApproximated, "an import id is kept as text; it carries no meaning in Azimuthal"},
	"float":                {"number", VerdictClean, "a numeric field maps onto the number type"},
	"datepicker":           {"date", VerdictClean, "a date field maps onto the date type"},
	"datetime":             {"date", VerdictApproximated, "the date type holds a date; the time-of-day component is lost"},
	"select":               {"single_select", VerdictClean, "a single-choice select maps onto single_select"},
	"radiobuttons":         {"single_select", VerdictClean, "radio buttons are a single choice and map onto single_select"},
	"multiselect":          {"text", VerdictApproximated, "there is no multi-value type; the selected values collapse into one text value and stop being options"},
	"multicheckboxes":      {"text", VerdictApproximated, "there is no multi-value type; the checked values collapse into one text value"},
	"labels":               {"text", VerdictApproximated, "labels collapse into one text value; project_items.labels exists but is not reachable through a custom field"},
	"userpicker":           {"text", VerdictApproximated, "there is no user-reference field type; the value is kept as text and stops resolving to a person"},
	"multiuserpicker":      {"text", VerdictApproximated, "there is no user-reference field type; the values are kept as one text value"},
	"grouppicker":          {"text", VerdictApproximated, "there is no group-reference field type; the value is kept as text"},
	"multigrouppicker":     {"text", VerdictApproximated, "there is no group-reference field type; the values are kept as one text value"},
	"project":              {"text", VerdictApproximated, "there is no project-reference field type; the value is kept as text"},
	"version":              {"text", VerdictApproximated, "Azimuthal has no versions; the value is kept as text"},
	"multiversion":         {"text", VerdictApproximated, "Azimuthal has no versions; the values are kept as one text value"},
	"cascadingselect":      {"", VerdictUnmappable, "a cascading select is two dependent choices; single_select holds one flat option list and there is no dependency model"},
	"multicascadingselect": {"", VerdictUnmappable, "a cascading select is two dependent choices with no flat equivalent"},
}

// ClassifyJiraCustomFieldType decides one Jira custom-field type.
//
// An unrecognised type — which includes every app-provided field, the
// calculated and scripted ones most often — is unmappable rather than
// approximated, because there is nothing to approximate it with.
func ClassifyJiraCustomFieldType(fieldType string) (target string, v Verdict, reason string) {
	key := jiraCustomFieldKey(fieldType)
	if m, ok := jiraCustomFieldMap[key]; ok {
		return m.target, m.verdict, m.reason
	}
	return "", VerdictUnmappable,
		"no implemented custom-field type covers it; Azimuthal implements text, number, date and single_select, and app-provided types (calculated, scripted) have no equivalent"
}

// jiraCustomFieldKey strips the plugin namespace from a custom-field type key.
func jiraCustomFieldKey(fieldType string) string {
	s := strings.TrimSpace(fieldType)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return strings.ToLower(s)
}

// confluenceMacroMap maps Confluence storage-format macros onto Codex nodes.
//
// The target names are node types from internal/core/wiki/doc/schema.json, and
// mapping_test.go asserts every one of them against doc.KnownNode — so a macro
// mapped to a node the editor does not implement fails a test rather than
// producing a report that promises a clean import.
//
// Everything absent from this map is preserved, not lost: ADR-0012's
// unknownContent carrier keeps it verbatim. That is why the Confluence half of
// an assessment has almost no unmappable rows and a large preserved one.
var confluenceMacroMap = map[string]struct {
	node    string
	verdict Verdict
	reason  string
}{
	"info":            {"panel", VerdictClean, "maps onto the panel node with kind=info"},
	"note":            {"panel", VerdictClean, "maps onto the panel node with kind=note"},
	"warning":         {"panel", VerdictClean, "maps onto the panel node with kind=warning"},
	"tip":             {"panel", VerdictApproximated, "the panel node's kinds are info, note, success, warning and error — a tip has no exact counterpart and becomes success"},
	"panel":           {"panel", VerdictApproximated, "a generic panel keeps its content but loses its custom title and background colour, which the panel node does not model"},
	"expand":          {"expand", VerdictClean, "maps onto the expand node, which carries the title"},
	"status":          {"statusLozenge", VerdictClean, "maps onto the statusLozenge inline node"},
	"toc":             {"tableOfContents", VerdictClean, "maps onto the tableOfContents node"},
	"children":        {"childrenDisplay", VerdictClean, "maps onto the childrenDisplay node"},
	"include":         {"pageInclude", VerdictClean, "maps onto the pageInclude node"},
	"excerpt-include": {"pageInclude", VerdictApproximated, "an excerpt include transcludes part of a page; pageInclude transcludes the whole page"},
	"code":            {"codeBlock", VerdictClean, "maps onto the codeBlock node"},
	"noformat":        {"codeBlock", VerdictApproximated, "becomes a code block, which applies syntax highlighting the original deliberately suppressed"},
}

// ClassifyConfluenceMacro decides one Confluence macro by its ac:name.
func ClassifyConfluenceMacro(macroName string) (node string, v Verdict, reason string) {
	if m, ok := confluenceMacroMap[strings.ToLower(strings.TrimSpace(macroName))]; ok {
		return m.node, m.verdict, m.reason
	}
	return "", VerdictPreserved,
		"no implemented node covers it, so ADR-0012 keeps it verbatim in an unknownContent carrier: it survives a round trip and renders as preserved content, but nothing understands it"
}

// MappedMacroNames lists the macros with a native representation, for the
// report and for the drift guard.
func MappedMacroNames() []string {
	out := make([]string, 0, len(confluenceMacroMap))
	for k := range confluenceMacroMap {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CoerceSlug converts a display name into a slug the schema will accept, and
// reports whether coercion changed anything.
//
// This is where "Sub-task" becomes "sub_task". The change is reported because a
// key or type name silently changing shape is exactly the kind of surprise a
// migration report exists to prevent.
func CoerceSlug(name string) (slug string, changed bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '.', r == '/':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	// A slug must start with an alphanumeric.
	out = strings.TrimLeft(out, "_")
	return out, out != lower
}

// CoerceSpaceKey converts an export's project or space key into one the spaces
// table will accept, and reports whether it changed.
func CoerceSpaceKey(key string) (coerced string, changed bool) {
	upper := strings.ToUpper(strings.TrimSpace(key))
	var b strings.Builder
	for _, r := range upper {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 10 {
		out = out[:10]
	}
	return out, out != upper
}

// IsSeededKind reports whether a slug is one of the four types every org starts
// with.
func IsSeededKind(slug string) bool {
	for _, k := range SeededItemKinds {
		if k == slug {
			return true
		}
	}
	return false
}

// IsPriority reports whether a value is one of the four CHECK-constrained
// priorities.
func IsPriority(v string) bool {
	lower := strings.ToLower(strings.TrimSpace(v))
	for _, p := range Priorities {
		if p == lower {
			return true
		}
	}
	return false
}
