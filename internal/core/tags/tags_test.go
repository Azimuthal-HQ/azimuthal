package tags_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
)

// fakeRepo is an in-memory implementation of the narrow tags.Repository
// interface. It is deliberately NOT a database mock: nothing here stands in for
// PostgreSQL, and no assertion below depends on SQL, constraints or casing. It
// exists so the service's own rules — dedupe-before-write, the additive/
// authoritative split, probe-before-mint, and what is handed down to
// persistence — can be observed directly, which a database cannot show you.
// CLAUDE.md §2 forbids mocking the database, and the real persistence behaviour
// of these same paths is covered against a real PostgreSQL in
// internal/core/api/wiki_tags_integration_test.go.
//
// The call log is the point of the fake. Several of the rules under test are
// statements about *how many* writes happen and *which* arguments reach the
// repository, and those are invisible in the returned values.
type fakeRepo struct {
	// bySlug is keyed orgID|slug and models the one repository rule this fake
	// has to reproduce faithfully for the service's dedupe to be observable:
	// Upsert returns the existing row and the first spelling of a slug wins.
	bySlug map[string]tags.Tag

	entitiesWithTag []tags.TaggedEntity
	getErr          error
	// unreachable makes EntityInSpace answer false, standing in for an entity
	// that does not exist or sits in a space the caller cannot reach — the
	// repository's one bool deliberately cannot say which.
	unreachable bool

	calls []repoCall
}

// repoCall is one recorded repository invocation. Only the arguments an
// assertion needs are kept; the zero value of the rest is never read.
type repoCall struct {
	method           string
	slug             string
	name             string
	ref              tags.EntityRef
	tagIDs           []uuid.UUID
	readableSpaceIDs []uuid.UUID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{bySlug: map[string]tags.Tag{}}
}

func slugKey(orgID uuid.UUID, slug string) string { return orgID.String() + "|" + slug }

func (r *fakeRepo) ListByOrg(_ context.Context, orgID uuid.UUID) ([]tags.Tag, error) {
	r.calls = append(r.calls, repoCall{method: "ListByOrg"})
	out := make([]tags.Tag, 0, len(r.bySlug))
	for _, t := range r.bySlug {
		if t.OrgID == orgID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (r *fakeRepo) GetByOrgSlug(_ context.Context, orgID uuid.UUID, slug string) (tags.Tag, error) {
	r.calls = append(r.calls, repoCall{method: "GetByOrgSlug", slug: slug})
	if r.getErr != nil {
		return tags.Tag{}, r.getErr
	}
	if t, ok := r.bySlug[slugKey(orgID, slug)]; ok {
		return t, nil
	}
	return tags.Tag{}, tags.ErrNotFound
}

func (r *fakeRepo) Upsert(_ context.Context, orgID uuid.UUID, slug, name string) (tags.Tag, error) {
	r.calls = append(r.calls, repoCall{method: "Upsert", slug: slug, name: name})
	if t, ok := r.bySlug[slugKey(orgID, slug)]; ok {
		// The first spelling wins — a later name for the same slug does not
		// overwrite it, exactly as the interface's doc comment promises.
		return t, nil
	}
	t := tags.Tag{ID: uuid.New(), OrgID: orgID, Slug: slug, Name: name}
	r.bySlug[slugKey(orgID, slug)] = t
	return t, nil
}

func (r *fakeRepo) ForEntity(_ context.Context, ref tags.EntityRef) ([]tags.Tag, error) {
	r.calls = append(r.calls, repoCall{method: "ForEntity", ref: ref})
	return nil, nil
}

func (r *fakeRepo) EntityInSpace(_ context.Context, ref tags.EntityRef) (bool, error) {
	r.calls = append(r.calls, repoCall{method: "EntityInSpace", ref: ref})
	return !r.unreachable, nil
}

func (r *fakeRepo) ReplaceEntityTags(_ context.Context, ref tags.EntityRef, tagIDs []uuid.UUID) error {
	r.calls = append(r.calls, repoCall{method: "ReplaceEntityTags", ref: ref, tagIDs: slices.Clone(tagIDs)})
	return nil
}

func (r *fakeRepo) AddEntityTags(_ context.Context, ref tags.EntityRef, tagIDs []uuid.UUID) error {
	r.calls = append(r.calls, repoCall{method: "AddEntityTags", ref: ref, tagIDs: slices.Clone(tagIDs)})
	return nil
}

func (r *fakeRepo) EntitiesWithTag(_ context.Context, tagID uuid.UUID, readableSpaceIDs []uuid.UUID) ([]tags.TaggedEntity, error) {
	// readableSpaceIDs is recorded by reference-copy rather than being
	// normalised, because the whole point of the assertion downstream is that
	// the service did not rewrite it in transit.
	r.calls = append(r.calls, repoCall{method: "EntitiesWithTag", tagIDs: []uuid.UUID{tagID}, readableSpaceIDs: readableSpaceIDs})
	return r.entitiesWithTag, nil
}

// countCalls returns how many times a repository method was invoked.
func (r *fakeRepo) countCalls(method string) int {
	n := 0
	for _, c := range r.calls {
		if c.method == method {
			n++
		}
	}
	return n
}

// callsOf returns every recorded invocation of one method, in order.
func (r *fakeRepo) callsOf(method string) []repoCall {
	var out []repoCall
	for _, c := range r.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

// pageRef builds the page-flavoured EntityRef the historical page tests use.
func pageRef(pageID, spaceID uuid.UUID) tags.EntityRef {
	return tags.EntityRef{Type: tags.EntityPage, ID: pageID, SpaceID: spaceID}
}

// TestSlugify_UsesTheUnderscoreConvention pins the slug shape, which is the one
// thing about tags that cannot be changed later: migration 040's CHECK
// constraint is written against this form and the slug is the tag's immutable
// identity.
//
// The explicit hyphen assertion is the load-bearing one. Azimuthal has two slug
// helpers — the underscore form used by item types and custom fields, and the
// hyphen form used by spaces and teams — and swapping one for the other here is
// a plausible, tidy-looking edit that would silently orphan every existing tag.
// A test that only asserted "non-empty" or "lowercase" would wave it through.
func TestSlugify_UsesTheUnderscoreConvention(t *testing.T) {
	cases := map[string]string{
		"Design Docs":      "design_docs",
		"design docs":      "design_docs",
		"design_docs":      "design_docs",
		"  Design  Docs  ": "design_docs",
		"Spike / Research": "spike_research",
		"weird---dashes":   "weird_dashes",
		"epic2":            "epic2",
		"!!!":              "",
	}
	for in, want := range cases {
		got := tags.Slugify(in)
		if got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(got, "-") {
			t.Errorf("Slugify(%q) = %q: tag slugs are underscore-separated; a hyphen means the space/team slug helper has been substituted", in, got)
		}
	}
	// Stated once more on its own, because this single pair is the regression
	// the whole table exists for.
	if kebab := tags.Slugify("Design Docs"); kebab == "design-docs" {
		t.Fatal(`Slugify("Design Docs") produced the kebab-case form; tag slugs must be "design_docs"`)
	}
}

// TestService_Resolve_CollapsesLabelsThatSlugifyAlike proves the dedupe happens
// BEFORE the write, not after.
//
// Asserting only len(result) == 1 would pass just as well if the service
// upserted three rows and deduplicated the returned slice, which is a different
// and worse behaviour: three round trips per save, and — against a real
// database — a race that can mint duplicate rows. The Upsert count is what
// distinguishes the two.
func TestService_Resolve_CollapsesLabelsThatSlugifyAlike(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()

	got, err := svc.Resolve(context.Background(), org, []string{"Design Docs", "design docs", "design_docs"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected three spellings to collapse to one tag, got %d: %+v", len(got), got)
	}
	if got[0].Slug != "design_docs" {
		t.Errorf("resolved slug = %q, want %q", got[0].Slug, "design_docs")
	}
	if n := repo.countCalls("Upsert"); n != 1 {
		t.Errorf("Upsert called %d times; the dedupe must happen before the write, not after it", n)
	}
	// The first spelling is the one offered as the display name, so the chip
	// reads as the author first wrote it rather than as whoever last typed it
	// in lower case.
	if up := repo.callsOf("Upsert"); len(up) == 1 && up[0].name != "Design Docs" {
		t.Errorf("Upsert name = %q, want the first spelling %q", up[0].name, "Design Docs")
	}
}

// TestService_Resolve_RejectsUnslugifiableLabel checks that a label which
// slugifies to nothing is refused rather than dropped.
//
// The mixed list matters: if the service silently skipped "!!!" the call would
// return one tag and a nil error, so asserting on the error alone would not
// separate "refused" from "quietly discarded". Both are asserted.
func TestService_Resolve_RejectsUnslugifiableLabel(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)

	got, err := svc.Resolve(context.Background(), uuid.New(), []string{"design", "!!!"})
	if !errors.Is(err, tags.ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v (result %+v)", err, got)
	}
	if got != nil {
		t.Errorf("a refused Resolve must return no tags, got %+v", got)
	}
}

// TestService_Resolve_RefusesMoreThanMaxTagsPerEntity checks the cap, and
// checks that it is enforced before any row is minted.
//
// Without the zero-Upsert assertion the test would pass on a version that
// created fifty-one tag rows and then complained, which defeats the reason the
// cap exists: keeping one request from minting an unbounded number of
// org-scoped rows. The exactly-at-the-limit case is there because a `>=` typo
// in the guard is otherwise indistinguishable from the correct `>`.
func TestService_Resolve_RefusesMoreThanMaxTagsPerEntity(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()

	tooMany := make([]string, 0, tags.MaxTagsPerEntity+1)
	for i := range tags.MaxTagsPerEntity + 1 {
		tooMany = append(tooMany, fmt.Sprintf("tag%d", i))
	}

	if _, err := svc.Resolve(context.Background(), org, tooMany); !errors.Is(err, tags.ErrTooManyTags) {
		t.Fatalf("expected ErrTooManyTags for %d labels, got %v", len(tooMany), err)
	}
	if n := len(repo.calls); n != 0 {
		t.Errorf("a refused Resolve wrote to the repository %d times; the cap must be checked before anything is created", n)
	}

	atLimit := tooMany[:tags.MaxTagsPerEntity]
	got, err := svc.Resolve(context.Background(), org, atLimit)
	if err != nil {
		t.Fatalf("exactly MaxTagsPerEntity labels must be allowed, got %v", err)
	}
	if len(got) != tags.MaxTagsPerEntity {
		t.Errorf("resolved %d tags at the limit, want %d", len(got), tags.MaxTagsPerEntity)
	}
}

// TestService_Resolve_NormalisesLabel observes normaliseLabel — which is
// unexported — through the name that reaches the repository, since the stored
// display name is the only place its work is visible.
func TestService_Resolve_NormalisesLabel(t *testing.T) {
	t.Run("leading hash is stripped and does not make a second tag", func(t *testing.T) {
		repo := newFakeRepo()
		svc := tags.NewService(repo)

		got, err := svc.Resolve(context.Background(), uuid.New(), []string{"#design", "design"})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// An author typing "#design" into the tags field means the tag
		// "design". If the hash survived normalisation the two labels would
		// still slugify alike (Slugify drops leading punctuation) and collapse
		// to one tag — so the tag COUNT proves nothing here and the stored name
		// is the only assertion that catches it. A chip reading "##design" is
		// the visible symptom.
		if len(got) != 1 {
			t.Fatalf("expected one tag, got %d: %+v", len(got), got)
		}
		if got[0].Name != "design" {
			t.Errorf("stored name = %q, want %q with the hash removed", got[0].Name, "design")
		}
		if up := repo.callsOf("Upsert"); len(up) == 1 && strings.HasPrefix(up[0].name, "#") {
			t.Errorf("Upsert name = %q: the leading hash must not be persisted", up[0].name)
		}
	})

	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		repo := newFakeRepo()
		svc := tags.NewService(repo)

		got, err := svc.Resolve(context.Background(), uuid.New(), []string{"  Design Docs \t "})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got[0].Name != "Design Docs" {
			t.Errorf("stored name = %q, want it trimmed to %q", got[0].Name, "Design Docs")
		}
	})

	t.Run("an over-long multi-byte label is cut at a rune boundary", func(t *testing.T) {
		repo := newFakeRepo()
		svc := tags.NewService(repo)

		// "xy" keeps the label slugifiable — Slugify discards non-ASCII, so a
		// purely CJK label would resolve to the empty slug and be refused
		// before truncation ever ran. The three-byte runes then place the
		// 64-byte cut in the middle of a character, which is exactly the case a
		// naive s[:64] gets wrong.
		long := "xy" + strings.Repeat("日", 30)
		got, err := svc.Resolve(context.Background(), uuid.New(), []string{long})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		name := got[0].Name

		// The three assertions are independent. Valid-UTF-8 fails on a byte
		// cut; shorter-than-the-input fails if truncation were removed
		// altogether (the input is valid UTF-8, so validity alone would still
		// pass); prefix-of-the-input fails if the label were mangled some other
		// way, e.g. re-encoded or replaced with a slug.
		if !utf8.ValidString(name) {
			t.Errorf("stored name is not valid UTF-8 (% x); the cut must land on a rune boundary", name)
		}
		if len(name) >= len(long) {
			t.Errorf("stored name is %d bytes and the input was %d; an over-long label must be truncated", len(name), len(long))
		}
		if !strings.HasPrefix(long, name) {
			t.Errorf("stored name %q is not a prefix of the input; truncation must not rewrite the label", name)
		}
	})
}

// TestService_SetEntityTags_IsAuthoritative checks the explicit path: the list
// given IS the entity's tag set, so leaving a label out removes it.
//
// Expressing a removal is the assertion that matters. A service that only ever
// added would satisfy "the tags I asked for are on the page" in every case;
// what it could not do is the second call here, where a shorter list must reach
// ReplaceEntityTags as a shorter id set. The page kind carries the historical
// assertions; the ticket and item kinds are asserted as siblings at the end,
// because the service must not treat any kind specially.
func TestService_SetEntityTags_IsAuthoritative(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()
	ref := pageRef(uuid.New(), uuid.New())

	first, err := svc.SetEntityTags(context.Background(), org, ref, []string{"design", "architecture", "rfc"})
	if err != nil {
		t.Fatalf("SetEntityTags: %v", err)
	}
	replaced := repo.callsOf("ReplaceEntityTags")
	if len(replaced) != 1 {
		t.Fatalf("expected one ReplaceEntityTags call, got %d", len(replaced))
	}
	if replaced[0].ref != ref {
		t.Errorf("ReplaceEntityTags ref = %+v, want %+v", replaced[0].ref, ref)
	}
	// Exact ids, in order: the association stores the tag's id, so a set that
	// merely has the right LENGTH would still associate the wrong rows.
	wantIDs := []uuid.UUID{first[0].ID, first[1].ID, first[2].ID}
	if !slices.Equal(replaced[0].tagIDs, wantIDs) {
		t.Errorf("ReplaceEntityTags ids = %v, want the resolved ids %v", replaced[0].tagIDs, wantIDs)
	}

	// Dropping two labels must be expressible as a shorter id set.
	second, err := svc.SetEntityTags(context.Background(), org, ref, []string{"design"})
	if err != nil {
		t.Fatalf("SetEntityTags (shortened): %v", err)
	}
	replaced = repo.callsOf("ReplaceEntityTags")
	if len(replaced) != 2 {
		t.Fatalf("expected two ReplaceEntityTags calls, got %d", len(replaced))
	}
	if !slices.Equal(replaced[1].tagIDs, []uuid.UUID{second[0].ID}) {
		t.Errorf("shortened set reached the repository as %v, want exactly %v", replaced[1].tagIDs, []uuid.UUID{second[0].ID})
	}
	// "design" is the same tag as before — a removal changes the association,
	// never the tag row, because the slug is the immutable identity.
	if second[0].ID != first[0].ID {
		t.Errorf("re-resolving %q minted a new tag (%v, was %v)", "design", second[0].ID, first[0].ID)
	}

	// Clearing every tag is a write, not a no-op: this is how the last tag
	// comes off an entity.
	if _, err := svc.SetEntityTags(context.Background(), org, ref, nil); err != nil {
		t.Fatalf("SetEntityTags (cleared): %v", err)
	}
	replaced = repo.callsOf("ReplaceEntityTags")
	if len(replaced) != 3 {
		t.Fatalf("clearing the list must still call ReplaceEntityTags; saw %d calls", len(replaced))
	}
	if len(replaced[2].tagIDs) != 0 {
		t.Errorf("cleared set reached the repository as %v, want empty", replaced[2].tagIDs)
	}
	// The authoritative path must never reach for the additive one.
	if n := repo.countCalls("AddEntityTags"); n != 0 {
		t.Errorf("SetEntityTags called AddEntityTags %d times; it must replace, not add", n)
	}

	// The sibling kinds: the same authoritative write, with the ref reaching
	// the repository verbatim. A service that special-cased pages — the only
	// kind that existed before migration 055 — would pass everything above and
	// fail here.
	for _, kind := range []tags.EntityType{tags.EntityTicket, tags.EntityProjectItem} {
		repo := newFakeRepo()
		svc := tags.NewService(repo)
		ref := tags.EntityRef{Type: kind, ID: uuid.New(), SpaceID: uuid.New()}
		resolved, err := svc.SetEntityTags(context.Background(), org, ref, []string{"runbooks"})
		if err != nil {
			t.Fatalf("SetEntityTags(%s): %v", kind, err)
		}
		replaced := repo.callsOf("ReplaceEntityTags")
		if len(replaced) != 1 || replaced[0].ref != ref {
			t.Fatalf("SetEntityTags(%s) reached the repository as %+v, want one call with ref %+v", kind, replaced, ref)
		}
		if !slices.Equal(replaced[0].tagIDs, []uuid.UUID{resolved[0].ID}) {
			t.Errorf("SetEntityTags(%s) ids = %v, want %v", kind, replaced[0].tagIDs, []uuid.UUID{resolved[0].ID})
		}
	}
}

// TestService_SetEntityTags_UnreachableTargetRefusesBeforeMinting checks the
// write path's probe: a target that does not exist — or sits in a space the
// caller was not authorised against, which must be indistinguishable — is
// ErrEntityNotFound, and the refusal happens BEFORE any tag row is minted.
//
// The zero-Upsert assertion is the load-bearing half. A version that resolved
// labels first would answer the same 404 while leaving freshly created
// org-scoped tag rows behind as a side effect of a request that failed.
func TestService_SetEntityTags_UnreachableTargetRefusesBeforeMinting(t *testing.T) {
	for _, kind := range []tags.EntityType{tags.EntityPage, tags.EntityTicket, tags.EntityProjectItem} {
		repo := newFakeRepo()
		repo.unreachable = true
		svc := tags.NewService(repo)
		ref := tags.EntityRef{Type: kind, ID: uuid.New(), SpaceID: uuid.New()}

		_, err := svc.SetEntityTags(context.Background(), uuid.New(), ref, []string{"design"})
		if !errors.Is(err, tags.ErrEntityNotFound) {
			t.Fatalf("SetEntityTags(%s) on an unreachable target: want ErrEntityNotFound, got %v", kind, err)
		}
		if n := repo.countCalls("Upsert"); n != 0 {
			t.Errorf("SetEntityTags(%s) minted %d tag row(s) for an unreachable target; the probe must run first", kind, n)
		}
		if n := repo.countCalls("ReplaceEntityTags"); n != 0 {
			t.Errorf("SetEntityTags(%s) wrote associations for an unreachable target", kind)
		}
	}

	// An unknown entity kind is the same refusal, without ever reaching the
	// repository: a kind the model cannot resolve to a space is a target
	// nothing may be written for.
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	_, err := svc.SetEntityTags(context.Background(), uuid.New(),
		tags.EntityRef{Type: "sprint", ID: uuid.New(), SpaceID: uuid.New()}, []string{"design"})
	if !errors.Is(err, tags.ErrEntityNotFound) {
		t.Fatalf("an unknown entity kind must be ErrEntityNotFound, got %v", err)
	}
	if len(repo.calls) != 0 {
		t.Errorf("an unknown entity kind reached the repository: %+v", repo.calls)
	}
}

// TestService_EnsureOnEntity_IsAdditive checks the inline-token path, and the
// never-Replace assertion is the load-bearing half.
//
// The entity-level tag set is the authority and an inline `#foo` is a shortcut,
// so deleting the last "#foo" from a document body must NOT untag the page. If
// this path used ReplaceEntityTags, publishing a page would silently reduce its
// tags to whatever its prose happened to mention — an author who tagged the
// page explicitly and then reworded a sentence would lose the tag with no
// action that looks like a removal.
func TestService_EnsureOnEntity_IsAdditive(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()
	ref := pageRef(uuid.New(), uuid.New())

	// The page already carries a tag set somebody chose deliberately.
	explicit, err := svc.SetEntityTags(context.Background(), org, ref, []string{"design", "architecture"})
	if err != nil {
		t.Fatalf("SetEntityTags: %v", err)
	}
	before := repo.countCalls("ReplaceEntityTags")

	// A publish whose body mentions only "#rfc" must add it and disturb nothing.
	resolved, err := svc.EnsureOnEntity(context.Background(), org, ref, []string{"#rfc"})
	if err != nil {
		t.Fatalf("EnsureOnEntity: %v", err)
	}
	added := repo.callsOf("AddEntityTags")
	if len(added) != 1 {
		t.Fatalf("expected one AddEntityTags call, got %d", len(added))
	}
	if added[0].ref != ref {
		t.Errorf("AddEntityTags ref = %+v, want %+v", added[0].ref, ref)
	}
	if !slices.Equal(added[0].tagIDs, []uuid.UUID{resolved[0].ID}) {
		t.Errorf("AddEntityTags ids = %v, want exactly the resolved %v", added[0].tagIDs, []uuid.UUID{resolved[0].ID})
	}
	if n := repo.countCalls("ReplaceEntityTags"); n != before {
		t.Fatalf("EnsureOnEntity issued %d ReplaceEntityTags call(s); the inline path must never replace the entity's tag set", n-before)
	}
	// The explicitly-set tags are untouched by construction — nothing removed
	// them — and the deliberate tag rows are still the same rows.
	if len(explicit) != 2 {
		t.Fatalf("setup: expected two explicit tags, got %d", len(explicit))
	}
}

// TestService_EnsureOnEntity_NoLabelsWritesNothing checks that publishing a
// body with no inline tokens is completely inert.
//
// Without the early return the service would probe and write on every publish
// of every untagged page. That is pointless work, and against a real database
// a pointless transaction.
func TestService_EnsureOnEntity_NoLabelsWritesNothing(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)

	got, err := svc.EnsureOnEntity(context.Background(), uuid.New(), pageRef(uuid.New(), uuid.New()), nil)
	if err != nil {
		t.Fatalf("EnsureOnEntity: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no tags, got %+v", got)
	}
	if n := len(repo.calls); n != 0 {
		t.Errorf("EnsureOnEntity with no labels made %d repository call(s): %+v", n, repo.calls)
	}
}

// TestService_ResolveForPublish_DropsUnusableLabels pins the deliberate
// asymmetry between the two entry points, and asserts both halves of it in one
// place so that a later edit cannot "make them consistent" without a failure.
//
// Resolve refuses a label that cannot become a tag, because it serves the tags
// editor: a person is looking at the field they typed into, and telling them
// beats dropping it. ResolveForPublish drops it, because a publish is not the
// moment to reject a whole page over a stray "#!!!" somewhere in its body — the
// author's actual intent was to save the document.
func TestService_ResolveForPublish_DropsUnusableLabels(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()
	mixed := []string{"!!!", "design"}

	ids, err := svc.ResolveForPublish(context.Background(), org, mixed)
	if err != nil {
		t.Fatalf("ResolveForPublish must not fail on an unusable label, got %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected exactly the usable label to survive, got %d ids", len(ids))
	}
	up := repo.callsOf("Upsert")
	if len(up) != 1 || up[0].slug != "design" {
		t.Fatalf("expected one Upsert of %q, got %+v", "design", up)
	}
	// The returned id must be the tag that was actually created, not a fresh
	// or zero uuid — publish writes these straight into entity_tags.
	if ids[0] != repo.bySlug[slugKey(org, "design")].ID {
		t.Errorf("returned id %v is not the resolved tag's id %v", ids[0], repo.bySlug[slugKey(org, "design")].ID)
	}

	// The same input through the editor path is refused. If this ever starts
	// succeeding, the two paths have been unified and the editor has lost its
	// only feedback about an untaggable label.
	if _, err := svc.Resolve(context.Background(), org, mixed); !errors.Is(err, tags.ErrInvalidName) {
		t.Errorf("Resolve on the same labels must return ErrInvalidName, got %v", err)
	}
}

// TestService_EntitiesWithSlug_PropagatesNotFound checks that the repository's
// ErrNotFound reaches the caller unwrapped-enough to be recognised.
//
// The handler maps this to a 404. If EntitiesWithSlug wrapped it in a generic
// "listing entities" error — as its second call site does, correctly, for a
// real failure — an unknown tag slug would surface as a 500.
func TestService_EntitiesWithSlug_PropagatesNotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)

	_, err := svc.EntitiesWithSlug(context.Background(), uuid.New(), "no_such_tag", []uuid.UUID{uuid.New()})
	if !errors.Is(err, tags.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown slug, got %v", err)
	}
	// A lookup that failed must not go on to query entities.
	if n := repo.countCalls("EntitiesWithTag"); n != 0 {
		t.Errorf("EntitiesWithTag was called %d time(s) after the tag lookup failed", n)
	}

	// A repository error that is not ErrNotFound must not be laundered into
	// one, or a transient database failure would read to the caller as "this
	// tag does not exist".
	repo.getErr = errors.New("connection reset")
	if _, err := svc.EntitiesWithSlug(context.Background(), uuid.New(), "design", nil); errors.Is(err, tags.ErrNotFound) {
		t.Errorf("a transport error was reported as ErrNotFound: %v", err)
	}
}

// TestService_EntitiesWithSlug_PassesReadableSetVerbatim asserts the
// fail-closed authorisation input arrives at persistence exactly as the caller
// resolved it.
//
// readableSpaceIDs is the caller's resolved readable set (ADR-0010). It is the
// only thing standing between a tag browse and entities in spaces the requester
// cannot read, so any rewriting in transit — reordering is harmless, but
// deduplicating, appending, or replacing an empty set with "no filter" is not —
// is a cross-space read leak. Asserting the exact slice is the cheapest way to
// notice that someone started editing it.
//
// (This is the browse-path pin previously named
// TestService_PagesWithSlug_PassesReadableSetVerbatim; the symbol moved with
// the browse's generalization and every assertion moved with it.)
func TestService_EntitiesWithSlug_PassesReadableSetVerbatim(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()

	seeded, err := svc.Resolve(context.Background(), org, []string{"design"})
	if err != nil {
		t.Fatalf("seeding the tag: %v", err)
	}
	readable := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	result, err := svc.EntitiesWithSlug(context.Background(), org, "design", readable)
	if err != nil {
		t.Fatalf("EntitiesWithSlug: %v", err)
	}
	if result.Tag.ID != seeded[0].ID {
		t.Errorf("returned tag %v, want the seeded %v", result.Tag.ID, seeded[0].ID)
	}
	// A result that fits is not truncated. Reporting truncation on a short
	// answer would make the UI's "there are more" notice permanent noise.
	if result.Truncated {
		t.Error("a result well under the page size reported itself truncated")
	}
	called := repo.callsOf("EntitiesWithTag")
	if len(called) != 1 {
		t.Fatalf("expected one EntitiesWithTag call, got %d", len(called))
	}
	if !slices.Equal(called[0].readableSpaceIDs, readable) {
		t.Errorf("readable set reached the repository as %v, want the caller's %v verbatim", called[0].readableSpaceIDs, readable)
	}
	// The lookup is by slug but the query is by id — the association stores the
	// id, and a slug reaching EntitiesWithTag would mean the two disagree.
	if !slices.Equal(called[0].tagIDs, []uuid.UUID{seeded[0].ID}) {
		t.Errorf("EntitiesWithTag queried %v, want the resolved tag id %v", called[0].tagIDs, seeded[0].ID)
	}

	// An empty readable set must stay empty. Turning it into "no filter" is the
	// specific failure ADR-0010's fail-closed rule exists to prevent, and it
	// would be invisible in the returned value because the repository is what
	// applies the filter.
	repo.calls = nil
	if _, err := svc.EntitiesWithSlug(context.Background(), org, "design", nil); err != nil {
		t.Fatalf("EntitiesWithSlug (empty readable set): %v", err)
	}
	called = repo.callsOf("EntitiesWithTag")
	if len(called) != 1 {
		t.Fatalf("expected one EntitiesWithTag call, got %d", len(called))
	}
	if len(called[0].readableSpaceIDs) != 0 {
		t.Errorf("an empty readable set was expanded to %v; it must remain empty and match nothing", called[0].readableSpaceIDs)
	}
}

// TestService_EntitiesWithSlug_TruncatesAtTheCap checks the truncation signal:
// one row past MaxTaggedEntities — however the three kinds contribute to it —
// reports Truncated and returns exactly the cap.
func TestService_EntitiesWithSlug_TruncatesAtTheCap(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()
	if _, err := svc.Resolve(context.Background(), org, []string{"design"}); err != nil {
		t.Fatalf("seeding the tag: %v", err)
	}

	// One past the cap, mixing kinds: the limit is a property of the union,
	// not of any one kind's arm.
	kinds := []tags.EntityType{tags.EntityPage, tags.EntityTicket, tags.EntityProjectItem}
	over := make([]tags.TaggedEntity, 0, tags.MaxTaggedEntities+1)
	for i := range tags.MaxTaggedEntities + 1 {
		over = append(over, tags.TaggedEntity{EntityType: kinds[i%3], EntityID: uuid.New()})
	}
	repo.entitiesWithTag = over

	result, err := svc.EntitiesWithSlug(context.Background(), org, "design", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("EntitiesWithSlug: %v", err)
	}
	if !result.Truncated {
		t.Error("a result one past the cap must report Truncated")
	}
	if len(result.Entities) != tags.MaxTaggedEntities {
		t.Errorf("returned %d entities, want exactly the cap %d", len(result.Entities), tags.MaxTaggedEntities)
	}
}

// TestService_ListAndForEntity_PassThrough covers the two thin delegations,
// since a package's coverage should not be shaped by which functions were
// interesting to write tests for.
func TestService_ListAndForEntity_PassThrough(t *testing.T) {
	repo := newFakeRepo()
	svc := tags.NewService(repo)
	org := uuid.New()

	seeded, err := svc.Resolve(context.Background(), org, []string{"design", "architecture"})
	if err != nil {
		t.Fatalf("seeding tags: %v", err)
	}
	listed, err := svc.List(context.Background(), org)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != len(seeded) {
		t.Errorf("List returned %d tags, want the %d in the org", len(listed), len(seeded))
	}
	// Another org's tags must not appear — List is org-scoped, and the fake
	// holds both orgs' rows in one map so a missing org filter would show up.
	other := uuid.New()
	if _, err := svc.Resolve(context.Background(), other, []string{"unrelated"}); err != nil {
		t.Fatalf("seeding the other org: %v", err)
	}
	listed, err = svc.List(context.Background(), org)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != len(seeded) {
		t.Errorf("List returned %d tags after another org gained one, want %d", len(listed), len(seeded))
	}

	// The space travels down with the entity and is asserted on: it is what
	// stops an entity id alone from reading another space's tag set, so a
	// delegation that dropped it would be the defect rather than a detail.
	// All three kinds, because the ref's Type is part of what must arrive.
	for _, kind := range []tags.EntityType{tags.EntityPage, tags.EntityTicket, tags.EntityProjectItem} {
		repo.calls = nil
		ref := tags.EntityRef{Type: kind, ID: uuid.New(), SpaceID: uuid.New()}
		if _, err := svc.ForEntity(context.Background(), ref); err != nil {
			t.Fatalf("ForEntity(%s): %v", kind, err)
		}
		fe := repo.callsOf("ForEntity")
		if len(fe) != 1 || fe[0].ref != ref {
			t.Errorf("ForEntity(%s) reached the repository as %+v, want one call with ref %+v", kind, fe, ref)
		}
	}

	// An unknown kind never reaches the repository — the read fails closed the
	// same direction as the writes.
	repo.calls = nil
	if _, err := svc.ForEntity(context.Background(), tags.EntityRef{Type: "sprint", ID: uuid.New(), SpaceID: uuid.New()}); !errors.Is(err, tags.ErrEntityNotFound) {
		t.Errorf("ForEntity with an unknown kind must be ErrEntityNotFound, got %v", err)
	}
	if len(repo.calls) != 0 {
		t.Errorf("an unknown entity kind reached the repository: %+v", repo.calls)
	}
}
