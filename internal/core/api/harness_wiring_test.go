package api_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/ticketref"
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
var intentionallyAbsent = map[string]string{
	// The portal's email sender is nil in LINK delivery mode, which is the
	// default and what the harness runs. cmd/server/main.go leaves it nil in
	// exactly the same case — `var portalSender portal.Sender` is assigned
	// only when AZIMUTHAL_PORTAL_LINK_DELIVERY=email — so the harness is
	// faithful to production here rather than diverging from it, which is
	// what this test actually polices.
	//
	// Nothing goes dark as a result. portal.Service.deliver checks both the
	// flag and the pointer and reports Delivered=false rather than failing,
	// so the sign-in path is fully exercised in link mode; the tests read the
	// URL out of the response, which is that mode's entire purpose. The email
	// body is covered by adapters.PortalLinkSender's own test, not through
	// the router.
	"PortalService.sender": "nil in link-delivery mode, matching cmd/server/main.go; deliver() checks both the flag and the pointer, and the link path is exercised through the returned URL",
}

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

// unmountedInHarness lists RouterConfig handler fields that are legitimately
// nil in the test harness, with the reason. It should stay empty.
var unmountedInHarness = map[string]string{}

// TestHarness_NoUnmountedSurfaces closes the seam between the two guards
// above and the route-accounting sweep.
//
// TestHarness_NoDarkDependencies deliberately SKIPS a nil handler, on the
// stated grounds that "a nil handler means the surface is deliberately
// unmounted, which the route-accounting sweep already covers". It does not.
// TestReadPathSweep_EveryRouteAccounted walks the router built by
// newTestServerOn — the same router — so a handler left nil there contributes
// no routes to the walk, needs no accounting rows, and is invisible to both
// tests at once.
//
// The failure mode is the dark-harness one a level up: not a mounted handler
// missing a collaborator, but a whole feature that exists in production and
// in no test. P4 walked straight into it — the saved-view routes were added,
// the sweep stayed green, and nothing said the routes were simply not there.
//
// So: every handler field is mounted in the harness, or named here with a
// reason. SPAHandler is not caught by this and does not need to be — it is an
// interface, not a handler struct, and serving the embedded frontend from the
// test harness would be noise.
func TestHarness_NoUnmountedSurfaces(t *testing.T) {
	ts := newTestServer(t)
	cfg := reflect.ValueOf(ts.RouterCfg)
	cfgType := cfg.Type()

	var missing []string
	for i := range cfg.NumField() {
		name := cfgType.Field(i).Name
		if !strings.HasSuffix(name, "Handler") {
			continue
		}
		field := cfg.Field(i)
		// Pointer-to-struct handlers only: SPAHandler is an interface.
		if field.Kind() != reflect.Ptr || field.Type().Elem().Kind() != reflect.Struct {
			continue
		}
		if !field.IsNil() {
			continue
		}
		if _, ok := unmountedInHarness[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("handler surfaces left unmounted in the test harness:\n  %s\n\n"+
			"A nil handler contributes no routes to the router the accounting sweep walks, so its\n"+
			"endpoints are covered by NEITHER that sweep NOR TestHarness_NoDarkDependencies. The\n"+
			"feature reads as tested and has never been reached. Wire it in newTestServerOn\n"+
			"(mirroring cmd/server/main.go), or add it to unmountedInHarness with the reason.",
			strings.Join(missing, "\n  "))
	}

	for name := range unmountedInHarness {
		if _, ok := cfgType.FieldByName(name); !ok {
			t.Errorf("unmountedInHarness names %q, which is no longer a RouterConfig field", name)
		}
	}
}

// TestHarness_PortalGuardIsMounted closes a hole that internal/core/api/router.go
// already claimed was closed.
//
// That file says, of RouterConfig.PortalService: "TestHarness_PortalGuardIsMounted
// fails if the two ever disagree." No such test existed — the name appeared
// exactly once in the repository, in that comment. This is it.
//
// The hole is the dark harness in the portal's own shape. PortalHandler mounts
// the requester routes; PortalService backs the RequirePortalSession middleware
// the ROUTER applies to them. With the handler non-nil and the service nil,
// every structural guard we have still passes:
//
//   - RequirePortalSession(nil) is still present in the middleware chain, so
//     the accounting sweep's carries() check is satisfied — the guard is
//     mounted, it simply cannot authenticate anyone.
//   - TestHarness_NoDarkDependencies skips nil RouterConfig fields; it polices
//     a handler's collaborators, not the config's own.
//   - TestHarness_NoUnmountedSurfaces only inspects fields whose name ends in
//     "Handler", which PortalService does not.
//
// Meanwhile every /my/ route answers 404 at the guard's nil-service branch, so
// a requester-authenticated test would get a tidy 404 and pass any assertion
// that expected one. The whole authenticated half of the portal would read as
// covered while never having been reached.
//
// What this test adds, precisely — because it is easy to overclaim here. The
// portal's functional coverage already exists: the TestPortal_* integration
// tests drive real sessions through this harness, so nilling PortalService
// today breaks a good number of them. This test is not the only thing standing
// between that hole and production, and saying so would be false.
//
// What it adds is a NAMED invariant. Without it the symptom of a nil service is
// a scattering of unexplained 404s across a dozen tests whose subject is
// comment visibility or link expiry, none of which mentions wiring. With it,
// the harness fails once, in the file about harness wiring, saying what is
// actually wrong. It also makes router.go's comment true, which matters
// independently: a guard that names its guarantee after a test nobody wrote is
// worse than one that names nothing, because the next reader believes it.
//
// The invariant is deliberately one-directional. PortalHandler nil is a valid
// deployment — a build that has opted no space into a portal — so the check is
// "handler implies service", not "both set".
func TestHarness_PortalGuardIsMounted(t *testing.T) {
	ts := newTestServer(t)

	cfg := reflect.ValueOf(ts.RouterCfg)
	handler := cfg.FieldByName("PortalHandler")
	service := cfg.FieldByName("PortalService")

	require.True(t, handler.IsValid() && service.IsValid(),
		"RouterConfig must carry both PortalHandler and PortalService")

	if handler.IsNil() {
		t.Skip("portal surface is not mounted in this harness")
	}

	require.False(t, service.IsNil(),
		"PortalHandler is mounted but PortalService is nil, so RequirePortalSession "+
			"cannot authenticate: every requester route answers 404 and reads as covered. "+
			"Wire PortalService in newTestServerOn, mirroring cmd/server/main.go.")
}

// TestHarness_EveryTicketRefHandlerIsUnderTheRequiredPolicy is the structural
// guard for the omission that produced B3.
//
// A ticketref.Policy is a VALUE field, so TestHarness_NoDarkDependencies is
// blind to it by design (isDependencyKind above excludes value kinds, and it
// is right to: the zero policy is a real, working, permissive configuration,
// not an absent collaborator). The consequence is that a handler could gain a
// ticket-reference gate and no test would ever exercise it under the required
// posture — which is exactly what happened to grants and shares. They accepted
// no reference at all, the four required-mode tests passed, and nothing in the
// suite had an opinion.
//
// So the guard is not "is the field set" but "is every handler that HAS one
// also mounted in the required-mode harness with Required true". A new
// reference-accepting handler fails here by name until newTicketRefRequiredServer
// knows about it.
//
// Verified by deliberate breakage: removing `.WithTicketRefPolicy(required)`
// from TeamHandler in newTicketRefRequiredServer fails this with
// "TeamHandler"; leaving GrantHandler out of that harness entirely fails it
// too. Restored afterwards.
func TestHarness_EveryTicketRefHandlerIsUnderTheRequiredPolicy(t *testing.T) {
	production := newTestServer(t)
	required := newTicketRefRequiredServer(t)

	prodCfg := reflect.ValueOf(production.RouterCfg)
	reqCfg := reflect.ValueOf(required.RouterCfg)
	cfgType := prodCfg.Type()

	var missing []string
	var checked []string

	for i := range prodCfg.NumField() {
		name := cfgType.Field(i).Name
		field := prodCfg.Field(i)
		if field.Kind() != reflect.Ptr || field.IsNil() || field.Elem().Kind() != reflect.Struct {
			continue
		}
		if !hasTicketRefPolicy(field.Elem()) {
			continue
		}
		checked = append(checked, name)

		counterpart := reqCfg.Field(i)
		if counterpart.Kind() != reflect.Ptr || counterpart.IsNil() {
			missing = append(missing, name+" (not mounted in newTicketRefRequiredServer)")
			continue
		}
		if !ticketRefPolicyIsRequired(counterpart.Elem()) {
			missing = append(missing, name+" (mounted, but its policy is permissive)")
		}
	}

	sort.Strings(missing)
	require.Empty(t, missing, strings.Join([]string{
		"these handlers accept a ticket reference but are not exercised under the required policy:",
		strings.Join(missing, ", "),
		"— add them to newTicketRefRequiredServer in ticket_ref_audit_integration_test.go.",
	}, "\n"))

	// Guard against the guard passing vacuously. If the reflection stops
	// finding the policy field — renamed, moved, made a pointer — `missing`
	// is empty for the wrong reason and this test would report success while
	// checking nothing.
	require.NotEmpty(t, checked, "no handler was found to carry a ticketref.Policy; the reflection has gone stale")
	require.Contains(t, checked, "GrantHandler")
	require.Contains(t, checked, "ShareHandler")
}

// hasTicketRefPolicy reports whether a handler struct carries a
// ticketref.Policy field, by type rather than by name — a rename of the field
// must not silently drop a handler out of the guard above.
func hasTicketRefPolicy(handler reflect.Value) bool {
	return ticketRefPolicyField(handler).IsValid()
}

// ticketRefPolicyIsRequired reads Required off the handler's policy.
//
// Reading an exported sub-field of an unexported struct field is legal
// through reflect as long as nothing calls Interface() on it — the same
// constraint TestHarness_AuditLoggersAreLive works within.
func ticketRefPolicyIsRequired(handler reflect.Value) bool {
	policy := ticketRefPolicyField(handler)
	if !policy.IsValid() {
		return false
	}
	field := policy.FieldByName("Required")
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}

// ticketRefPolicyField finds the handler's ticketref.Policy field by type.
func ticketRefPolicyField(handler reflect.Value) reflect.Value {
	if handler.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	t := handler.Type()
	for i := range t.NumField() {
		if t.Field(i).Type == reflect.TypeOf(ticketref.Policy{}) {
			return handler.Field(i)
		}
	}
	return reflect.Value{}
}
