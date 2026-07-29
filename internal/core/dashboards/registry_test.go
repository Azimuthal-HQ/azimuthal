package dashboards

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// The registry, and the property ADR-0009 decision 5 and the P5 DoD both ask
// for: no gadget resolves through a switch.

func TestRegistry_HoldsExactlyTheV1Set(t *testing.T) {
	want := []GadgetKey{
		GadgetBreakdown, GadgetMyWork, GadgetNote,
		GadgetRecentWork, GadgetViewCount, GadgetViewResults,
	}
	got := Definitions()
	require.Len(t, got, len(want), "the v1 gadget set is closed — adding one is a deliberate act")
	for i, d := range got {
		require.Equal(t, want[i], d.Key, "Definitions() is ordered by key so output is stable between runs")
	}
}

func TestRegistry_EveryDefinitionIsWellFormed(t *testing.T) {
	for _, d := range Definitions() {
		t.Run(string(d.Key), func(t *testing.T) {
			require.NotEmpty(t, d.Name, "a gadget with no name renders an unlabelled tile")
			require.Contains(t, []int32{1, 2, 4}, d.DefaultSpan,
				"the default span must satisfy dashboard_gadgets_span_valid or the first save 500s")
			require.NotEmpty(t, d.Modules)
			require.NotEmpty(t, d.Render, "the client dispatches on Render; an empty one draws nothing")
			require.True(t, d.AllowsConfigKey(CfgTitle), "every gadget may be retitled")

			// ADR-0009 decision 2: a gadget references a saved view OR the
			// registry supplies its query. Both at once would mean the stored
			// row's view is ignored; neither is legal only for a gadget that
			// has no data at all.
			require.False(t, d.RequiresSavedView && d.Query != nil,
				"a gadget cannot both take a saved view and carry its own query")
			if d.Render != RenderNote {
				require.True(t, d.RequiresSavedView || d.Query != nil,
					"a data gadget needs a source: a saved view or a registry query")
			}
		})
	}
}

// The registry's built-in queries must be documents the vocabulary accepts. If
// one is not, the gadget renders an error tile for everybody, forever — and
// the failure would otherwise only appear at request time.
func TestRegistry_BuiltinQueriesAreValid(t *testing.T) {
	for _, d := range Definitions() {
		if d.Query == nil {
			continue
		}
		q := d.Query()
		require.NoError(t, q.Validate(), "gadget %q carries a query the filter vocabulary refuses", d.Key)
	}
}

// The `me` token is stored verbatim and resolved per request. Substituting a
// user id at any point would freeze a shared gadget to one person — which is
// the whole property the saved-view layer exists to provide.
func TestRegistry_MyWorkUsesTheMeTokenNotAUserID(t *testing.T) {
	q := MyWorkQuery()
	require.Equal(t, []string{views.AssigneeMe}, q.Filter.Assignees)
	require.ElementsMatch(t, []views.Module{views.ModuleBeacon, views.ModuleVector}, q.Filter.Modules,
		"My work spans both modules — that is what makes it 'my work' rather than 'my tickets'")
}

// An empty filter is not "match none": the vocabulary says an absent field is
// not a filter at all, so Recently updated resolves to whatever the viewer can
// read and the access union is what bounds it.
func TestRegistry_RecentWorkFiltersNothingAndSortsByUpdate(t *testing.T) {
	q := RecentWorkQuery()
	require.Empty(t, q.Filter.Statuses)
	require.Empty(t, q.Filter.Assignees)
	require.Empty(t, q.Filter.SpaceIDs)
	require.Equal(t, "updated_at", q.Sort.Field)
	require.Equal(t, "desc", q.Sort.Dir)
}

func TestRegistry_LookupRefusesAnUnknownKey(t *testing.T) {
	_, ok := Lookup("burndown")
	require.False(t, ok, "a key the registry does not define must not resolve")
}

// THE P5 DoD LINE: "a test asserts no gadget resolves via a switch."
//
// It parses this package's own source and fails on any `switch` whose subject
// mentions a gadget key — the shape that closes the extension seam. Dispatch
// must be a map read.
//
// Deliberately narrow: it looks for a switch over the gadget-key type or over
// a value named like one, not for every switch in the package (the service has
// several over gadget STATE, which is a different closed set that no third
// party extends). Written as an AST walk rather than a grep so a switch split
// across lines, or one with an unusual gofmt, cannot slip past.
func TestRegistry_NoSwitchOverGadgetKey(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, sources, "the walk found no source at all — it would pass vacuously")

	fset := token.NewFileSet()
	offenders := []string{}
	checked := 0
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || sw.Tag == nil {
				return true
			}
			if mentionsGadgetKey(sw.Tag) {
				offenders = append(offenders, fset.Position(sw.Pos()).String())
			}
			return true
		})
	}
	require.NotZero(t, checked, "no non-test source was parsed — the guard asserts nothing")
	require.Empty(t, offenders,
		"a switch over a gadget key closes the extension seam permanently (ADR-0009 decision 5).\n"+
			"Dispatch through the registry map instead. Offending switches:\n%v", offenders)
}

func mentionsGadgetKey(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			if v.Name == "GadgetKey" || v.Name == "gadgetKey" || v.Name == "Key" {
				found = true
			}
		case *ast.SelectorExpr:
			if v.Sel != nil && (v.Sel.Name == "GadgetKey" || v.Sel.Name == "Key") {
				found = true
			}
		}
		return true
	})
	return found
}
