package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// TestMacroTargets_AreRealCodexNodes is the drift guard for the Confluence
// half.
//
// Every macro this package claims maps "cleanly" names a Codex node type. If
// that node does not exist in the editor's schema, the claim is false and the
// report tells a self-hoster their pages arrive intact when the content would
// in fact be captured as unknown. doc.KnownNode reads the same schema.json the
// editor's TypeScript mirror is checked against, so this fails the moment a
// node is renamed or removed.
func TestMacroTargets_AreRealCodexNodes(t *testing.T) {
	t.Parallel()

	for macro, m := range confluenceMacroMap {
		require.True(t, doc.KnownNode(m.node),
			"macro %q is mapped to node %q, which is not in the Codex schema", macro, m.node)
	}

	// And the preservation carriers must still exist, since every unmapped
	// macro is reported as landing in one.
	require.True(t, doc.KnownNode(doc.NodeUnknownContent))
	require.True(t, doc.KnownNode(doc.NodeUnknownInline))
	require.True(t, doc.KnownMark(doc.MarkUnknownMark))
}

// TestMacroMap_CoversEveryFirstClassMacroNode asserts the other direction: if
// the editor implements a macro node, this table should have something that
// reaches it. A node with no macro mapped to it means an import would preserve
// content the product could have rendered natively.
func TestMacroMap_CoversEveryFirstClassMacroNode(t *testing.T) {
	t.Parallel()

	targeted := make(map[string]struct{})
	for _, m := range confluenceMacroMap {
		targeted[m.node] = struct{}{}
	}

	// layout and layoutColumn are reached from ac:layout elements rather than
	// from a structured macro, so they are legitimately absent from the macro
	// table; they are asserted in the element mapping instead.
	fromElements := map[string]struct{}{"layout": {}, "layoutColumn": {}}

	for _, node := range doc.SchemaNodes() {
		if !isMacroGroupNode(t, node) {
			continue
		}
		if _, ok := fromElements[node]; ok {
			continue
		}
		require.Contains(t, targeted, node,
			"Codex implements macro node %q but no Confluence macro maps to it — content that could render natively would be preserved as unknown instead", node)
	}
}

// isMacroGroupNode reads schema.json's own grouping rather than hardcoding the
// list, so a macro added there is picked up here.
func isMacroGroupNode(t *testing.T, node string) bool {
	t.Helper()
	return schemaGroup(t, node) == "macro"
}

func schemaGroup(t *testing.T, node string) string {
	t.Helper()
	raw := doc.SchemaJSON()
	// The manifest is small and its shape is fixed; a targeted scan avoids
	// duplicating the doc package's parsing here.
	needle := `"` + node + `":`
	i := strings.Index(string(raw), needle)
	require.Positive(t, i, "node %q not found in schema.json", node)
	rest := string(raw)[i+len(needle):]
	start := strings.Index(rest, `"`)
	end := strings.Index(rest[start+1:], `"`)
	return rest[start+1 : start+1+end]
}

// TestSubstrateFacts_MatchTheMigrations checks the constants that describe the
// database against the migrations themselves.
//
// These are the facts every verdict in the report rests on: the four custom
// field types, the four priorities, the four seeded kinds, the slug format and
// the space key format. Stating them in Go is convenient; letting them drift
// from the schema would make the whole assessment quietly wrong.
func TestSubstrateFacts_MatchTheMigrations(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, "migrations", name))
		require.NoError(t, err)
		return string(b)
	}

	customFields := read("033_custom_fields.sql")
	for _, ft := range CustomFieldTypes {
		require.Contains(t, customFields, "'"+ft+"'",
			"custom field type %q is claimed here but not in migration 033", ft)
	}
	require.Contains(t, customFields, `CHECK (field_type IN ('text', 'number', 'date', 'single_select'))`,
		"the custom-field type set changed; every classification in jiraCustomFieldMap needs revisiting")
	require.Contains(t, customFields, `custom_field_defs_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9_]*$')`,
		"the slug format changed; CoerceSlug and the coercion verdicts need revisiting")

	itemTypes := read("032_item_types.sql")
	for _, k := range SeededItemKinds {
		require.Contains(t, itemTypes, "('"+k+"',",
			"seeded item kind %q is claimed here but not seeded by migration 032", k)
	}
	require.Contains(t, itemTypes, `item_types_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9_]*$')`)

	// D49: kind is validated by the service, not by the database. If a CHECK or
	// FK ever appears, the report's advice about routing importer writes
	// through the service changes.
	require.Contains(t, itemTypes, "ALTER TABLE project_items DROP CONSTRAINT IF EXISTS project_items_kind_check",
		"migration 032 is what makes kind validation service-only (D49)")

	keys := read("031_project_item_keys.sql")
	require.Contains(t, keys, "CREATE UNIQUE INDEX idx_project_items_org_key ON project_items (org_id, item_key)",
		"the item-key uniqueness constraint is what collision detection is about")
	require.Contains(t, keys, "s.key || '-' || pi.number",
		"the item_key shape <SPACE_KEY>-<number> is what collision detection reconstructs")

	workflows := read("016_workflow_engine.sql")
	for state, category := range SeededTicketStates {
		require.Contains(t, workflows, "'"+state+"'",
			"seeded workflow state %q is claimed here but not in migration 016", state)
		require.Contains(t, workflows, "'"+category+"'")
	}

	attachments := read("027_attachments.sql")
	require.Contains(t, attachments, `CHECK (entity_type IN ('page','ticket','project_item'))`,
		"what an attachment may hang off decides where Jira and Confluence attachments can land")
}

// TestSpaceKeyPattern_MatchesTheHandler keeps the key format in step with the
// validator that actually enforces it.
func TestSpaceKeyPattern_MatchesTheHandler(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "core", "api", "spaces", "handler.go"))
	require.NoError(t, err)
	require.Contains(t, string(b), "regexp.MustCompile(`^[A-Z0-9]{1,10}$`)",
		"the space key format changed; CoerceSpaceKey and every key-collision verdict need revisiting")
	require.Equal(t, "^[A-Z0-9]{1,10}$", SpaceKeyPattern.String())
}

// TestPriorities_MatchTheFilterVocabulary — the same four are CHECK-constrained
// on both tables and validated in views.
func TestPriorities_MatchTheFilterVocabulary(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "core", "views", "filter.go"))
	require.NoError(t, err)
	for _, p := range Priorities {
		require.Contains(t, string(b), `"`+p+`"`, "priority %q is not in the filter vocabulary", p)
	}
	require.Len(t, Priorities, 4)
}

func TestCoerceSlug(t *testing.T) {
	t.Parallel()

	// "changed" means the slug is structurally different, not merely lowercased.
	// Case folding is unsurprising and reporting it would bury the coercions
	// that matter — "Sub-task" losing its hyphen is what a reader needs to see.
	for _, tc := range []struct {
		in, want string
		changed  bool
	}{
		{"Task", "task", false},
		{"task", "task", false},
		{"Sub-task", "sub_task", true},
		{"New Feature", "new_feature", true},
		{"Story Points", "story_points", true},
		{"  Bug  ", "bug", false}, // trimming, like case folding, is not structural
		{"Epic/Theme", "epic_theme", true},
		{"a--b", "a_b", true},
		{"__leading", "leading", true},
	} {
		got, changed := CoerceSlug(tc.in)
		require.Equal(t, tc.want, got, "input %q", tc.in)
		require.Equal(t, tc.changed, changed, "input %q", tc.in)
		require.Regexp(t, SlugPattern, got, "coerced slug %q must satisfy the schema", got)
	}
}

func TestCoerceSpaceKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, want string
		changed  bool
	}{
		{"ABC", "ABC", false},
		{"abc", "ABC", false}, // uppercasing alone is not a reportable change
		{"my-docs", "MYDOCS", true},
		{"VERYLONGSPACEKEY", "VERYLONGSP", true},
		{"a b c", "ABC", true},
	} {
		got, changed := CoerceSpaceKey(tc.in)
		require.Equal(t, tc.want, got, "input %q", tc.in)
		require.Equal(t, tc.changed, changed, "input %q", tc.in)
		require.Regexp(t, SpaceKeyPattern, got, "coerced key %q must satisfy the validator", got)
	}
}

func TestClassifyJiraCustomFieldType(t *testing.T) {
	t.Parallel()

	target, v, reason := ClassifyJiraCustomFieldType(
		"com.atlassian.jira.plugin.system.customfieldtypes:cascadingselect")
	require.Equal(t, VerdictUnmappable, v, "a cascading select has no flat equivalent")
	require.Empty(t, target)
	require.NotEmpty(t, reason)

	target, v, _ = ClassifyJiraCustomFieldType("com.atlassian.jira.plugin.system.customfieldtypes:textarea")
	require.Equal(t, VerdictClean, v)
	require.Equal(t, "text", target)

	target, v, _ = ClassifyJiraCustomFieldType("datetime")
	require.Equal(t, VerdictApproximated, v, "the date type has no time-of-day component")
	require.Equal(t, "date", target)

	// An app-provided field nobody anticipated.
	_, v, reason = ClassifyJiraCustomFieldType("com.onresolve.jira.groovy.groovyrunner:scripted-field")
	require.Equal(t, VerdictUnmappable, v)
	require.Contains(t, reason, "text, number, date and single_select")

	// Every mapped target must be a real implemented type.
	for key, m := range jiraCustomFieldMap {
		if m.target == "" {
			continue
		}
		require.Contains(t, CustomFieldTypes, m.target,
			"custom field %q maps to type %q, which is not implemented", key, m.target)
	}
}

func TestClassifyConfluenceMacro_UnknownIsPreservedNotLost(t *testing.T) {
	t.Parallel()

	node, v, reason := ClassifyConfluenceMacro("drawio")
	require.Equal(t, VerdictPreserved, v)
	require.Empty(t, node)
	require.Contains(t, reason, "unknownContent")

	node, v, _ = ClassifyConfluenceMacro("INFO")
	require.Equal(t, VerdictClean, v, "macro names match case-insensitively")
	require.Equal(t, "panel", node)

	_, v, reason = ClassifyConfluenceMacro("tip")
	require.Equal(t, VerdictApproximated, v)
	require.Contains(t, reason, "success")
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "reached the filesystem root without finding go.mod")
		dir = parent
	}
	t.Fatal("could not locate the module root")
	return ""
}
