package api_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
)

// The dark-harness guard.
//
// Handlers in this codebase take their optional collaborators through With*
// builders, and a handler whose collaborator is missing does not fail — it
// reports the feature as disabled and answers 404. That is right in
// production, where a deployment without object storage should still boot.
// It is silently wrong in a test harness: every integration test against
// those routes gets a tidy 404, every assertion that expects a 404 still
// passes, and the endpoints look covered while never having been reached.
//
// That is not hypothetical. The board-config endpoints (W4) answered
// 404 "board configuration is not enabled" in every integration test in the
// suite, because newTestServerOn never called WithBoardConfig. Nothing
// announced it. It surfaced only when the coverage floor caught the package.
// The same omission was live for four more collaborators when this test was
// written: the ticket handler's audit logger, notification enqueuer and
// suggestions service, the project handler's audit logger, the comment
// handler's audit logger and notification enqueuer, and the wiki handler's
// audit logger — all wired in cmd/server/main.go, none wired here.
//
// So this stops being a convention people are asked to remember. The harness
// is walked, and any handler collaborator left nil fails the suite by name.

// intentionallyAbsent lists handler dependencies that are legitimately nil in
// the harness, each with the reason. Adding an entry is deliberate; it is the
// act this test exists to force. Key format: "FieldName.dependencyField".
var intentionallyAbsent = map[string]string{}

// dependencyKinds are the field kinds that can be nil and therefore can go
// dark. Value fields (a ticketref.Policy, an int) cannot, and their zero
// value is a real, working configuration rather than an absent one.
func isDependencyKind(k reflect.Kind) bool {
	switch k {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return true
	default:
		return false
	}
}

// TestHarness_NoDarkDependencies walks every handler the test router was
// built from and fails on any nil collaborator.
//
// Verified by deliberate breakage while writing it: removing
// `.WithBoardConfig(...)` from newTestServerOn makes this fail with
// "ProjectHandler.boardConfig is nil", instead of the previous behaviour
// where the board-config endpoints simply 404'd and no test noticed.
// Restored afterwards.
func TestHarness_NoDarkDependencies(t *testing.T) {
	ts := newTestServer(t)

	cfg := reflect.ValueOf(ts.RouterCfg)
	cfgType := cfg.Type()

	var dark []string
	for i := range cfg.NumField() {
		field := cfg.Field(i)
		name := cfgType.Field(i).Name

		// Only the handler pointers carry collaborators. A nil handler means
		// the surface is deliberately unmounted, which the route-accounting
		// sweep already covers; this test is about a handler that IS mounted
		// while missing a dependency.
		if field.Kind() != reflect.Ptr || field.IsNil() {
			continue
		}
		handler := field.Elem()
		if handler.Kind() != reflect.Struct {
			continue
		}

		for j := range handler.NumField() {
			dep := handler.Field(j)
			depName := handler.Type().Field(j).Name
			if !isDependencyKind(dep.Kind()) || !dep.IsNil() {
				continue
			}
			key := name + "." + depName
			if _, ok := intentionallyAbsent[key]; ok {
				continue
			}
			dark = append(dark, key)
		}
	}
	sort.Strings(dark)

	if len(dark) > 0 {
		t.Errorf("handler dependencies left nil in the test harness — every one of these makes a\n"+
			"feature answer 404 \"not enabled\" in every test that touches it, which looks like\n"+
			"passing coverage and is not. Wire the matching With* in newTestServerOn (mirroring\n"+
			"cmd/server/main.go), or add the field to intentionallyAbsent with the reason:\n%s",
			strings.Join(dark, "\n"))
	}

	// A stale exemption is worse than none: it reads as a considered decision
	// about a dependency that no longer exists.
	for key := range intentionallyAbsent {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) != 2 {
			t.Errorf("intentionallyAbsent key %q is not in FieldName.dependencyField form", key)
			continue
		}
		f := cfg.FieldByName(parts[0])
		if !f.IsValid() || f.Kind() != reflect.Ptr || f.IsNil() {
			t.Errorf("intentionallyAbsent names %q, but RouterConfig has no such wired handler — drop it", key)
			continue
		}
		if _, ok := f.Elem().Type().FieldByName(parts[1]); !ok {
			t.Errorf("intentionallyAbsent names %q, but that handler has no such field — drop it", key)
		}
	}
}

// TestHarness_AuditLoggersAreLive is the second half of the same problem.
//
// A nil audit logger cannot happen — every NewHandler installs the package
// default — but that default silently discards every event and reports
// IsAvailable() == false. A handler left on it looks wired and writes
// nothing, so any test asserting "the mutation recorded an audit row" would
// have to be wrong about which handler it was exercising. Since this phase
// makes audit rows load-bearing (they now carry the operator's ticket
// reference), a discarding logger is a dark dependency in the same sense.
func TestHarness_AuditLoggersAreLive(t *testing.T) {
	ts := newTestServer(t)

	cfg := reflect.ValueOf(ts.RouterCfg)
	cfgType := cfg.Type()
	loggerType := reflect.TypeOf((*audit.Logger)(nil)).Elem()
	// The logger the harness actually built. Comparing against this rather
	// than against the no-op's type name keeps the check honest through a
	// rename, and catches any other stand-in a future edit might introduce.
	liveLogger := reflect.TypeOf(ts.AuditLog)

	var discarding []string
	for i := range cfg.NumField() {
		field := cfg.Field(i)
		if field.Kind() != reflect.Ptr || field.IsNil() {
			continue
		}
		handler := field.Elem()
		if handler.Kind() != reflect.Struct {
			continue
		}
		for j := range handler.NumField() {
			dep := handler.Field(j)
			if dep.Kind() != reflect.Interface || dep.Type() != loggerType || dep.IsNil() {
				continue
			}
			if dep.Elem().Type() != liveLogger {
				discarding = append(discarding, cfgType.Field(i).Name+"."+handler.Type().Field(j).Name)
			}
		}
	}
	sort.Strings(discarding)

	if len(discarding) > 0 {
		t.Errorf("handlers still on the no-op audit logger in the test harness — they accept audit\n"+
			"events and discard them, so nothing they write can be asserted. Pass\n"+
			".WithAuditLogger(auditLog) in newTestServerOn:\n%s", strings.Join(discarding, "\n"))
	}
}
