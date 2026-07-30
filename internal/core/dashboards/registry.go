// Package dashboards implements dashboards and the gadget registry (P5,
// ADR-0009, migration 048): composable grids of gadgets whose data always
// comes from the saved-view layer, resolved per viewer on every render.
//
// This file is the single place the gadget vocabulary is defined. It is the
// server half of a contract with web/src/lib/dashboards/registry.ts, and
// web/src/lib/dashboards/registry.test.ts reads THIS FILE and fails in both
// directions when the two disagree — the same drift guard the filter
// vocabulary carries.
//
// # Why this is a registry and not a switch
//
// ADR-0009 decision 5: "First-party gadgets ship through the same registry a
// third-party gadget would use. No switch statement over gadget type anywhere
// in the render path." A switch closes the extension seam permanently and,
// more immediately, scatters the answer to "what may this gadget carry" across
// every function that has to ask. Every lookup here is a map read.
//
// # Why the set is closed
//
// The same reasoning migration 038 and internal/core/views/filter.go give for
// the filter vocabulary, and ADR-0011 gives for workflow scripting. A gadget
// kind this build cannot reason about statically is one it cannot validate,
// cannot bound, and cannot migrate. Spec §1 puts third-party and user-authored
// gadgets out of scope for v0.3 explicitly: "the registry seam is built now,
// hosting and sandboxing deferred". So the seam is real and the set is fixed.
//
// # Strict on write, tolerant on read
//
// A key this build does not know is refused at the API boundary. A key this
// build does not know that is ALREADY STORED — written by an older or newer
// build — must still load, as an inert labelled placeholder (decision log C5:
// "unknown gadget_key — placeholder tile, never crashes"). That is why
// migration 048 puts no CHECK on gadget_key and why Gadget.Key is a string
// rather than a GadgetKey.
package dashboards

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// GadgetKey names a gadget kind.
type GadgetKey string

// The v1 gadget set. Closed, enumerable, statically reasoned.
const (
	// GadgetViewResults renders a saved view's first N results, through the
	// identical path /views/{id}/results takes.
	GadgetViewResults GadgetKey = "view_results"
	// GadgetViewCount renders one number: how many rows the view resolves to
	// for this viewer, counted in the database.
	GadgetViewCount GadgetKey = "view_count"
	// GadgetBreakdown renders counts grouped by one filter-vocabulary field
	// over a view's results.
	GadgetBreakdown GadgetKey = "breakdown"
	// GadgetMyWork renders work assigned to the viewer across both modules. It
	// takes no saved view: the registry supplies a `me`-token query, which is
	// the same query the saved-view layer resolves for everything else.
	GadgetMyWork GadgetKey = "my_work"
	// GadgetRecentWork renders the most recently updated work in the viewer's
	// readable containers. Registry-supplied query, as My work is.
	GadgetRecentWork GadgetKey = "recent_work"
	// GadgetNote renders a markdown annotation. No query, no data, no fetch.
	GadgetNote GadgetKey = "note"
)

// Module is the product surface a dashboard belongs to.
//
// Deliberately NOT views.Module. A views.Module names which TABLE a query
// reads; this names which part of the product lists a dashboard, and 'home' is
// an answer to the second question and not to the first. Migration 048's
// dashboards_module_valid holds the same three values.
type Module string

// The three dashboard modules. Codex is absent: a saved view cannot query
// pages (see the note on views.Resolve), so a Codex dashboard would have
// nothing but notes on it.
const (
	ModuleHome   Module = "home"
	ModuleBeacon Module = "beacon"
	ModuleVector Module = "vector"
)

// ValidModule reports whether m is one of the three.
func ValidModule(m Module) bool {
	return m == ModuleHome || m == ModuleBeacon || m == ModuleVector
}

// RenderMode tells the client how to draw a gadget's data. It is part of the
// definition rather than of the client's own table so that "what shape is this
// gadget" has one answer, and so a gadget kind cannot be added on one side
// alone.
type RenderMode string

// The four render modes.
const (
	RenderList      RenderMode = "list"
	RenderStat      RenderMode = "stat"
	RenderBreakdown RenderMode = "breakdown"
	RenderNote      RenderMode = "note"
)

// Bounds on a gadget's configuration. Not security boundaries — every value is
// a bound parameter — they stop one gadget from becoming an unbounded request.
const (
	MaxGadgetLimit = 25
	MinGadgetLimit = 1
	MaxTitleLen    = 80
	MaxNoteLen     = 4000
	// MaxGadgets bounds one dashboard. A dashboard renders every gadget in one
	// pass, so the cap is what stops a layout from becoming a request storm.
	MaxGadgets = 40
)

// DefaultGadgetLimit is how many rows a list gadget shows when its config does
// not say. Small on purpose: a gadget is a glance, not a page.
const DefaultGadgetLimit = 5

// Definition is one gadget kind.
//
// It carries no render function. The server's half of the registry answers
// "may this be written, what may it carry, and what query does it run"; the
// drawing lives in web/src/lib/dashboards/registry.ts. Splitting it that way
// is what lets the server validate a gadget it will never draw.
type Definition struct {
	Key  GadgetKey
	Name string
	// DefaultSpan is the column span a freshly added gadget takes, in the
	// four-column grid migration 048's col_span CHECK bounds.
	DefaultSpan int32
	// Modules is which dashboard modules may host this gadget.
	Modules []Module
	// RequiresSavedView is true for the gadgets that take their query from a
	// saved view. A gadget of such a kind whose saved_view_id is NULL renders
	// ADR-0009's recoverable "pick another view" state rather than an error.
	RequiresSavedView bool
	// Query supplies the gadget's own query when the registry owns it, and is
	// nil otherwise. This is how My work stays "a me-token view under the
	// hood" without forking the vocabulary: the document is built here, from
	// the same structs the filter builder produces, and resolved by the same
	// views.Resolve.
	//
	// ADR-0009 decision 2 says a gadget never EMBEDS a query. Nothing here is
	// embedded: the stored row carries a key, and the query is looked up from
	// it. A build that renamed a gadget changes what it means for everyone,
	// which is the property a stored copy would lose.
	Query func() views.Query
	// Render is the drawing mode the client dispatches on.
	Render RenderMode
	// ConfigKeys is exactly which configuration keys this kind may carry.
	// Anything else on a write is refused rather than dropped.
	ConfigKeys []ConfigKey
}

// ConfigKey names one configuration key.
type ConfigKey string

// The closed configuration vocabulary. Four keys across six gadget kinds; a
// fifth needs a line here, a line on the struct, and a line in the client's
// mirror, which is the point.
const (
	// CfgTitle overrides the tile heading. Every kind may carry it.
	CfgTitle ConfigKey = "title"
	// CfgLimit is how many rows a list gadget shows.
	CfgLimit ConfigKey = "limit"
	// CfgGroupBy is the breakdown field, from views.GroupField.
	CfgGroupBy ConfigKey = "group_by"
	// CfgBody is a note's markdown source.
	CfgBody ConfigKey = "body"
)

// allModules is the every-surface list, spelled once.
var allModules = []Module{ModuleHome, ModuleBeacon, ModuleVector}

// registry is the lookup table. Unexported, so the only ways in are
// registerGadget below and Lookup — there is no path by which a caller adds a
// gadget kind at runtime, which is what "closed set" means here.
var registry = map[GadgetKey]Definition{}

// registerGadget is the one way a definition enters the registry. It is the
// server twin of the client's registerGadget (spec §7) and exists so that
// first-party gadgets go through the same door a third-party gadget would.
//
// It panics on a duplicate key rather than overwriting: two definitions for
// one key is a programming error that would otherwise resolve differently
// depending on file order.
func registerGadget(d Definition) {
	if _, dup := registry[d.Key]; dup {
		panic(fmt.Sprintf("dashboards: gadget %q registered twice", d.Key))
	}
	registry[d.Key] = d
}

//nolint:gochecknoinits // the registry is populated once at startup; an explicit builder would only move the same list
func init() {
	registerGadget(Definition{
		Key: GadgetViewResults, Name: "View results",
		DefaultSpan: 2, Modules: allModules, RequiresSavedView: true,
		Render: RenderList, ConfigKeys: []ConfigKey{CfgTitle, CfgLimit},
	})
	registerGadget(Definition{
		Key: GadgetViewCount, Name: "View count",
		DefaultSpan: 1, Modules: allModules, RequiresSavedView: true,
		Render: RenderStat, ConfigKeys: []ConfigKey{CfgTitle},
	})
	registerGadget(Definition{
		Key: GadgetBreakdown, Name: "Breakdown",
		DefaultSpan: 2, Modules: allModules, RequiresSavedView: true,
		Render: RenderBreakdown, ConfigKeys: []ConfigKey{CfgTitle, CfgGroupBy},
	})
	registerGadget(Definition{
		Key: GadgetMyWork, Name: "My work",
		DefaultSpan: 2, Modules: allModules,
		Query: MyWorkQuery, Render: RenderList,
		ConfigKeys: []ConfigKey{CfgTitle, CfgLimit},
	})
	registerGadget(Definition{
		Key: GadgetRecentWork, Name: "Recently updated",
		DefaultSpan: 2, Modules: allModules,
		Query: RecentWorkQuery, Render: RenderList,
		ConfigKeys: []ConfigKey{CfgTitle, CfgLimit},
	})
	registerGadget(Definition{
		Key: GadgetNote, Name: "Note",
		DefaultSpan: 2, Modules: allModules,
		Render: RenderNote, ConfigKeys: []ConfigKey{CfgTitle, CfgBody},
	})
}

// MyWorkQuery is the `me`-token document behind the My work gadget.
//
// It is built from views.Filter rather than written as JSON so that a change
// to the vocabulary breaks this at compile time. The `me` token is stored
// verbatim and resolved per request against the CALLING user
// (views.buildParams), which is what makes one shared dashboard mean each
// viewer's own work.
func MyWorkQuery() views.Query {
	return views.Query{
		V: views.Version,
		Filter: views.Filter{
			Modules:   []views.Module{views.ModuleBeacon, views.ModuleVector},
			Assignees: []string{views.AssigneeMe},
		},
		Sort: views.Sort{Field: "updated_at", Dir: "desc"},
	}
}

// RecentWorkQuery is "everything I can read, most recently touched first".
//
// An empty filter is not "match none" — the vocabulary says an absent field is
// not a filter at all — so this resolves to whatever the viewer can read, and
// nothing more. The access union inside the fan-out is what bounds it.
func RecentWorkQuery() views.Query {
	return views.Query{
		V: views.Version,
		Filter: views.Filter{
			Modules: []views.Module{views.ModuleBeacon, views.ModuleVector},
		},
		Sort: views.Sort{Field: "updated_at", Dir: "desc"},
	}
}

// ErrUnknownGadget reports a gadget key the registry does not define. It is
// returned on WRITE only: a stored unknown key is a placeholder tile, not an
// error (decision log C5).
var ErrUnknownGadget = errors.New("unknown gadget")

// Lookup returns a definition by key.
func Lookup(k GadgetKey) (Definition, bool) {
	d, ok := registry[k]
	return d, ok
}

// Definitions returns every registered gadget, ordered by key so the output is
// stable between runs.
func Definitions() []Definition {
	out := make([]Definition, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// AllowsModule reports whether this gadget may sit on a dashboard of module m.
func (d Definition) AllowsModule(m Module) bool {
	for _, got := range d.Modules {
		if got == m {
			return true
		}
	}
	return false
}

// AllowsConfigKey reports whether this gadget may carry key k.
func (d Definition) AllowsConfigKey(k ConfigKey) bool {
	for _, got := range d.ConfigKeys {
		if got == k {
			return true
		}
	}
	return false
}
