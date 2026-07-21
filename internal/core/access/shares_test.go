package access

import (
	"testing"

	"github.com/google/uuid"
)

// TestPathWithinSubtree is the failure-mode-2 guard at the unit level: a
// cascade share on "a.b" must cover "a.b.c" but NOT the sibling "a.bc". The
// naive prefix check (strings.HasPrefix(path, root)) matches both and is the
// exact bug this function exists to avoid.
func TestPathWithinSubtree(t *testing.T) {
	cases := []struct {
		name string
		path string
		root string
		want bool
	}{
		{"root itself is covered", "a.b", "a.b", true},
		{"direct child covered", "a.b.c", "a.b", true},
		{"deep descendant covered", "a.b.c.d.e", "a.b", true},
		{"sibling sharing a name prefix NOT covered", "a.bc", "a.b", false},
		{"sibling sharing a longer prefix NOT covered", "a.bcd", "a.b", false},
		{"unrelated path NOT covered", "x.y.z", "a.b", false},
		{"prefix-of-root NOT covered (root is deeper)", "a", "a.b", false},
		{"same first segment, different second NOT covered", "a.c", "a.b", false},
		// Realistic dotted-UUID segments: one full UUID segment can never be
		// a prefix of a different UUID segment, but the boundary must still
		// hold at the dot.
		{"uuid descendant covered",
			"11111111-1111-1111-1111-111111111111.22222222-2222-2222-2222-222222222222.33333333-3333-3333-3333-333333333333",
			"11111111-1111-1111-1111-111111111111.22222222-2222-2222-2222-222222222222", true},
		{"uuid sibling NOT covered",
			"11111111-1111-1111-1111-111111111111.22222222-2222-2222-2222-222222222223",
			"11111111-1111-1111-1111-111111111111.22222222-2222-2222-2222-222222222222", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PathWithinSubtree(tc.path, tc.root); got != tc.want {
				t.Errorf("PathWithinSubtree(%q, %q) = %v, want %v", tc.path, tc.root, got, tc.want)
			}
		})
	}
}

// TestEscapeLike proves stored LIKE metacharacters are neutralised before
// interpolation, so a path containing '%' or '_' matches itself rather than
// widening the pattern (failure mode 2, defence in depth).
func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"a.b":         "a.b",
		"50%off":      `50\%off`,
		"under_score": `under\_score`,
		`back\slash`:  `back\\slash`,
		`%_\`:         `\%\_\\`,
	}
	for in, want := range cases {
		if got := EscapeLike(in); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSubtreeLikePattern builds the "strict descendants" pattern with the
// required dot boundary — it must exclude the root itself and escape the
// root's metacharacters.
func TestSubtreeLikePattern(t *testing.T) {
	if got, want := SubtreeLikePattern("a.b"), "a.b.%"; got != want {
		t.Errorf("SubtreeLikePattern(a.b) = %q, want %q", got, want)
	}
	if got, want := SubtreeLikePattern("50%off"), `50\%off.%`; got != want {
		t.Errorf("SubtreeLikePattern(50%%off) = %q, want %q", got, want)
	}
}

// TestSharedEntitiesCoverage exercises the resolved coverage set: direct
// shares cover exactly their entity; a cascade share covers its subtree by
// exact-segment prefix and only within the root's own space; a flat entity
// is covered only by a direct share.
func TestSharedEntitiesCoverage(t *testing.T) {
	spaceA := uuid.New()
	spaceB := uuid.New()
	directPage := uuid.New()
	cascadeRoot := uuid.New()
	rootPath := "root.branch"
	ticket := uuid.New()

	rootSpace := spaceA
	rootPathVal := rootPath
	se := NewSharedEntities([]ShareRow{
		{EntityType: ShareEntityPage, EntityID: directPage, Cascade: false},
		{EntityType: ShareEntityPage, EntityID: cascadeRoot, Cascade: true, RootPath: &rootPathVal, RootSpaceID: &rootSpace},
		{EntityType: ShareEntityTicket, EntityID: ticket, Cascade: false},
	})

	if se.Empty() {
		t.Fatal("coverage should not be empty")
	}
	if !se.CoversEntity(ShareEntityPage, directPage) {
		t.Error("direct page share must cover its page")
	}
	if !se.CoversEntity(ShareEntityTicket, ticket) {
		t.Error("direct ticket share must cover its ticket")
	}
	if se.CoversEntity(ShareEntityPage, uuid.New()) {
		t.Error("an unshared page must not be covered")
	}

	// The cascade root itself is covered.
	if !se.CoversPage(cascadeRoot, spaceA, rootPath) {
		t.Error("cascade root must cover itself")
	}
	// A descendant in the same space is covered.
	if !se.CoversPage(uuid.New(), spaceA, "root.branch.leaf") {
		t.Error("descendant in the root's space must be covered")
	}
	// A sibling sharing a name prefix is NOT covered.
	if se.CoversPage(uuid.New(), spaceA, "root.branchX") {
		t.Error("sibling sharing a prefix must NOT be covered")
	}
	// A descendant PATH that coincides across spaces is NOT covered — the
	// candidate must live in the root's own space.
	if se.CoversPage(uuid.New(), spaceB, "root.branch.leaf") {
		t.Error("a path coincidence in another space must NOT be covered")
	}
}

// TestSharedEntitiesFailsClosed proves an empty coverage set denies
// everything — the ResolveShares middleware stamps this on unauthenticated
// share resolution and every route reads it as "no coverage".
func TestSharedEntitiesFailsClosed(t *testing.T) {
	se := NewSharedEntities(nil)
	if !se.Empty() {
		t.Fatal("nil rows must yield an empty coverage set")
	}
	if se.CoversEntity(ShareEntityPage, uuid.New()) {
		t.Error("empty coverage must not cover any entity")
	}
	if se.CoversPage(uuid.New(), uuid.New(), "a.b") {
		t.Error("empty coverage must not cover any page")
	}
}
