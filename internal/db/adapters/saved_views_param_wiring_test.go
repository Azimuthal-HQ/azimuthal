package adapters

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TestSavedViewAdapters_AssignEveryGeneratedParam is the guard the six
// hand-written fan-out adapters have never had.
//
// WHY THIS EXISTS. sqlc generates a Params STRUCT per query; the adapters fill
// it with a named-field composite literal. Adding a field to the struct
// therefore does NOT break compilation of the adapters — Go zero-values what
// the literal omits. A forgotten field is a `false` flag or a NULL bound,
// which reads to SQL as "this filter is absent". The query runs, the rows come
// back, and the filter the caller asked for was quietly not applied.
//
// This is not hypothetical. Filter v2 shipped its first draft with the four
// shared negation flags and all eight date bounds wired into the three TICKET
// adapters and missing from the three ITEM adapters, because a bulk edit
// anchored on two adjacent lines that the item literals separate with `Kinds`
// and `SprintIds`. It compiled. sqlc regenerated cleanly. The whole suite
// passed — including the count-versus-list parity test, because the list and
// the count for that module had BOTH lost the same parameters and so went on
// agreeing with each other about the wrong set of rows.
//
// So the check is structural rather than behavioural: parse the adapter
// sources, and for each of the six composite literals require that every field
// of the corresponding generated struct is assigned by name. It cannot be
// satisfied by a test that happens to exercise the right path, and it fails at
// the moment a parameter is added rather than whenever someone notices.
func TestSavedViewAdapters_AssignEveryGeneratedParam(t *testing.T) {
	t.Parallel()

	// The six fan-outs, and nothing else: these are the queries whose
	// parameters are the filter vocabulary.
	want := map[string]reflect.Type{
		"ListViewTicketsParams":           reflect.TypeOf(generated.ListViewTicketsParams{}),
		"ListViewProjectItemsParams":      reflect.TypeOf(generated.ListViewProjectItemsParams{}),
		"CountViewTicketsParams":          reflect.TypeOf(generated.CountViewTicketsParams{}),
		"CountViewProjectItemsParams":     reflect.TypeOf(generated.CountViewProjectItemsParams{}),
		"BreakdownViewTicketsParams":      reflect.TypeOf(generated.BreakdownViewTicketsParams{}),
		"BreakdownViewProjectItemsParams": reflect.TypeOf(generated.BreakdownViewProjectItemsParams{}),
	}

	assigned := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, src := range []string{"saved_views.go", "saved_view_aggregates.go"} {
		f, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", src, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if _, tracked := want[name]; !tracked {
				return true
			}
			if assigned[name] == nil {
				assigned[name] = map[string]bool{}
			}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					assigned[name][key.Name] = true
				}
			}
			return true
		})
	}

	for name, typ := range want {
		got := assigned[name]
		if got == nil {
			t.Errorf("no composite literal for generated.%s found in the adapters — "+
				"either the query is no longer used or this guard has stopped matching", name)
			continue
		}
		var missing []string
		for i := range typ.NumField() {
			if field := typ.Field(i).Name; !got[field] {
				missing = append(missing, field)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("generated.%s has %d field(s) the adapter never assigns: %v\n"+
				"A missing field is a ZERO VALUE, not a compile error: a false flag or a NULL bound "+
				"reads to SQL as \"this filter is absent\", so the query runs and the filter is "+
				"silently not applied.", name, len(missing), missing)
		}
	}
}

// TestSavedViewAdapters_TicketAndItemParamsAgree is the cross-module half.
//
// The two modules differ by exactly the two Vector-only columns. Anything else
// present on one side and absent from the other means a cross-module view would
// return a filtered half and an unfiltered half — the shape that makes a result
// list look plausible and be wrong.
func TestSavedViewAdapters_TicketAndItemParamsAgree(t *testing.T) {
	t.Parallel()

	vectorOnly := map[string]struct{}{
		"Kinds": {}, "NotKinds": {}, "SprintIds": {}, "NotSprintIds": {},
		// The shared-entity array is named for its own entity type on each side.
		"SharedTicketIds": {}, "SharedItemIds": {},
	}

	for _, pair := range []struct{ ticket, item reflect.Type }{
		{reflect.TypeOf(generated.ListViewTicketsParams{}), reflect.TypeOf(generated.ListViewProjectItemsParams{})},
		{reflect.TypeOf(generated.CountViewTicketsParams{}), reflect.TypeOf(generated.CountViewProjectItemsParams{})},
		{reflect.TypeOf(generated.BreakdownViewTicketsParams{}), reflect.TypeOf(generated.BreakdownViewProjectItemsParams{})},
	} {
		names := func(typ reflect.Type) map[string]bool {
			out := map[string]bool{}
			for i := range typ.NumField() {
				out[typ.Field(i).Name] = true
			}
			return out
		}
		tk, pi := names(pair.ticket), names(pair.item)
		for f := range tk {
			if _, ok := vectorOnly[f]; ok {
				continue
			}
			if !pi[f] {
				t.Errorf("%s filters on %q; %s does not", pair.ticket.Name(), f, pair.item.Name())
			}
		}
		for f := range pi {
			if _, ok := vectorOnly[f]; ok {
				continue
			}
			if !tk[f] {
				t.Errorf("%s filters on %q; %s does not", pair.item.Name(), f, pair.ticket.Name())
			}
		}
	}
}

// sanity keeps the AST walk honest: if the adapters were ever renamed or moved,
// the guard above would find no literals and pass vacuously for the wrong
// reason. This asserts the files it reads still contain what it expects.
func TestSavedViewAdapters_GuardReadsTheRealFiles(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	found := 0
	for _, src := range []string{"saved_views.go", "saved_view_aggregates.go"} {
		f, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", src, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "ViewTicketsParams") {
				found++
			}
			return true
		})
	}
	if found < 3 {
		t.Fatalf("found %d ticket fan-out param literals, expected at least 3 — the guard above "+
			"is no longer reading the adapters it thinks it is", found)
	}
}
