package access

import (
	"regexp"
	"testing"
)

// Capability constants must be exhaustively partitioned (S10).
//
// The org-admin bypass in Resolution.Can is by design and untouched: an org
// admin holds every capability on every space they can read, including
// org-only ones no space role grants. That is ADR-0007.
//
// The gap S10 closes is narrower and different in kind. CanOrgWide used to
// derive its org-level set by complement — "not in minRoleFor, therefore
// org-level" — and end in a bare `return res.IsOrgAdmin`. A capability that
// appeared in NEITHER table was therefore granted org-wide to every org admin
// with no space check whatsoever. A retired constant, a typo at a call site,
// or a new constant somebody forgot to place in a table would all land in
// that hole, and nothing would say so: the permission simply widened.
//
// Role.Grants has always failed closed on the same input (capability.go), so
// the two halves of the model disagreed about unknown capabilities. They no
// longer do.
//
// These tests are in package `access` rather than `access_test` because the
// partition they enforce is over two unexported maps.

// allCapabilities is every Capability constant declared in capability.go,
// listed by hand.
//
// Hand-maintained on purpose. Go has no way to enumerate the members of a
// const block at runtime, so a test that derived this list from the maps it
// checks would be circular — it would agree with whatever the maps said and
// assert nothing. The cost is that adding a constant means adding a line
// here; that cost IS the mechanism. A new constant that is not placed in a
// table fails TestCapabilityConstants_AreExhaustivelyPartitioned, and one
// that is not listed here fails TestCapabilityConstants_ListIsComplete.
var allCapabilities = []Capability{
	CapReadItems,
	CapReadAggregates,
	CapCreateItems,
	CapEditOwnItems,
	CapComment,
	CapEditAnyItem,
	CapTransitionAnyItem,
	CapManageQueue,
	CapManageSpace,
	CapManageGrants,
	CapManageShares,
	CapManageWorkflow,
	CapSetVisibility,
}

// TestCapabilityConstants_AreExhaustivelyPartitioned is the build-time guard.
// Every capability is space-scoped or org-level, never both and never neither.
func TestCapabilityConstants_AreExhaustivelyPartitioned(t *testing.T) {
	for _, c := range allCapabilities {
		_, spaceScoped := minRoleFor[c]
		_, orgLevel := orgLevelCaps[c]

		switch {
		case spaceScoped && orgLevel:
			t.Errorf("capability %q is in BOTH minRoleFor and orgLevelCaps; "+
				"it must be one or the other", c)
		case !spaceScoped && !orgLevel:
			t.Errorf("capability %q is in NEITHER minRoleFor nor orgLevelCaps. "+
				"Add it to minRoleFor with its minimum role, or to orgLevelCaps if "+
				"it is granted by the org-admin bypass alone. Leaving it unplaced "+
				"is not a no-op — before S10 it made the capability org-wide.", c)
		}
	}
}

// TestCapabilityConstants_ListIsComplete catches the other direction: a
// constant added to the const block and to a map, but never listed in
// allCapabilities, which would leave it outside every test in this file.
func TestCapabilityConstants_ListIsComplete(t *testing.T) {
	listed := make(map[Capability]struct{}, len(allCapabilities))
	for _, c := range allCapabilities {
		listed[c] = struct{}{}
	}

	for c := range minRoleFor {
		if _, ok := listed[c]; !ok {
			t.Errorf("capability %q is in minRoleFor but missing from allCapabilities "+
				"in this file — add it so the validity tests cover it", c)
		}
	}
	for c := range orgLevelCaps {
		if _, ok := listed[c]; !ok {
			t.Errorf("capability %q is in orgLevelCaps but missing from allCapabilities "+
				"in this file — add it so the validity tests cover it", c)
		}
	}

	if got, want := len(minRoleFor)+len(orgLevelCaps), len(allCapabilities); got != want {
		t.Errorf("partition size %d != %d capabilities; the two maps must together "+
			"cover every constant exactly once", got, want)
	}
}

// TestCanOrgWide_RefusesUnknownCapability is the regression pair for the
// fail-open defect. It fails against the pre-S10 CanOrgWide, which answered
// true here for any org admin.
func TestCanOrgWide_RefusesUnknownCapability(t *testing.T) {
	orgAdmin := &Resolution{IsOrgAdmin: true}

	unknown := []Capability{
		"typo_capabilty",       // a misspelling of a real one
		"manage_queues",        // a plausible near-miss of CapManageQueue
		"retired_capability",   // a constant deleted from the model
		"",                     // the zero value
		"SET_VISIBILITY",       // right capability, wrong case — the wire is snake_case
		"set_visibility_extra", // a prefix match must not be enough
	}

	for _, c := range unknown {
		if orgAdmin.CanOrgWide(c) {
			t.Errorf("CanOrgWide(%q) = true for an org admin; an unrecognised "+
				"capability must fail closed, not become an org-wide permission", c)
		}
	}
}

// TestCanOrgWide_StillGrantsTheRealOrgLevelCapability is the negative guard.
// Without it, everything above would be satisfied by a CanOrgWide that always
// returned false.
func TestCanOrgWide_StillGrantsTheRealOrgLevelCapability(t *testing.T) {
	orgAdmin := &Resolution{IsOrgAdmin: true}
	if !orgAdmin.CanOrgWide(CapSetVisibility) {
		t.Error("CanOrgWide(CapSetVisibility) = false for an org admin; " +
			"the org-admin bypass must still grant the org-level capability")
	}

	nonAdmin := &Resolution{IsOrgAdmin: false}
	if nonAdmin.CanOrgWide(CapSetVisibility) {
		t.Error("CanOrgWide(CapSetVisibility) = true for a non-admin")
	}
}

// TestCapabilityWireValues_AreUniqueAndSnakeCase pins the wire contract. Two
// constants sharing a string would make the maps silently collide, and the
// wire format is lowercase snake_case without exception (spec section 10).
func TestCapabilityWireValues_AreUniqueAndSnakeCase(t *testing.T) {
	snakeCase := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)

	seen := make(map[Capability]bool, len(allCapabilities))
	for _, c := range allCapabilities {
		if seen[c] {
			t.Errorf("duplicate capability wire value %q — two constants sharing a "+
				"string collide in minRoleFor and orgLevelCaps", c)
		}
		seen[c] = true

		if !snakeCase.MatchString(string(c)) {
			t.Errorf("capability %q is not lowercase snake_case", c)
		}
	}
}
