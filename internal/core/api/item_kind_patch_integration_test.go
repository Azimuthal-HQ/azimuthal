package api_test

// T1: `kind` on the item PATCH contract.
//
// Two things make this surface unusually easy to get wrong, and every test here
// is shaped around them.
//
// First, persistence. The handler change alone — updateItemRequest.Kind, the
// shared validateItemKind, applyItemPatch — produced a 200 whose body showed the
// new type while the write path never carried the column down to the row, so the
// stored slug did not move. The response body is built from the in-memory item
// the handler just patched, never from a re-read, so it was right while the
// database was wrong and a body-only assertion passed against the defect.
// Nothing below trusts that body alone: every outcome is re-read over HTTP and
// again straight out of PostgreSQL.
//
// Second, integrity. Migration 032 dropped project_items_kind_check (D49) because
// the type vocabulary became org-editable, and referential integrity for types is
// deliberately a service rule rather than a foreign key. The database will
// therefore store a typo, an archived slug or "" without complaint.
// Handler.validateItemKind is the only thing standing between a bad request and a
// permanently invalid row, so each rejection test asserts not just the 400 but
// that the stored slug is still the original.
//
// The absent-kind case is the #68 shape (a field the body never carried must not
// be applied) on the newest field, and reintroducing it breaks every partial
// PATCH the same way #68 did — see the mutation note on that test.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// itemKindPatchOriginalKind is the type every fixture item is created with, so
// "unchanged" has one name across the file.
const itemKindPatchOriginalKind = "task"

type itemKindPatchFixture struct {
	ts        *testServer
	itemsBase string
	typesBase string
}

func newItemKindPatchFixture(t *testing.T) *itemKindPatchFixture {
	t.Helper()
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Kind Patch Proj", "kind-patch-proj", "vector")
	return &itemKindPatchFixture{
		ts:        ts,
		itemsBase: fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, spaceID),
		typesBase: fmt.Sprintf("/api/v1/orgs/%s/item-types", ts.OrgID),
	}
}

// itemKindPatchCreateItem posts an item of the given kind and returns its id.
// It asserts the created kind so a later "unchanged" assertion cannot pass by
// the item never having had the type in the first place.
func itemKindPatchCreateItem(t *testing.T, f *itemKindPatchFixture, title, kind string) string {
	t.Helper()
	r := f.ts.post(t, f.itemsBase, map[string]any{
		"title":       title,
		"description": "original description",
		"kind":        kind,
		"priority":    "high",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)

	created := decodeJSONMap(t, r.Body)
	require.Equal(t, kind, created["kind"], "premise: the item must start life as %q", kind)
	id, ok := created["id"].(string)
	require.True(t, ok, "created item must carry an id: %s", r.Body)
	return id
}

// itemKindPatchFetchItem re-reads the item over HTTP — a round trip that shares
// no code with the PATCH response the handler already built in memory.
func itemKindPatchFetchItem(t *testing.T, f *itemKindPatchFixture, itemID string) map[string]any {
	t.Helper()
	r := f.ts.get(t, f.itemsBase+"/"+itemID, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "re-read item %s: %s", itemID, r.Body)
	return decodeJSONMap(t, r.Body)
}

// itemKindPatchStoredKind reads project_items.kind straight out of PostgreSQL,
// past every layer that could paper over a write that never happened.
func itemKindPatchStoredKind(t *testing.T, pool *pgxpool.Pool, itemID string) string {
	t.Helper()
	var kind string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT kind FROM project_items WHERE id = $1`, uuid.MustParse(itemID)).Scan(&kind),
		"reading project_items.kind for %s", itemID)
	return kind
}

// itemKindPatchRequireKind asserts the two independent readings of the stored
// type agree with want: a fresh GET, and the raw column.
func itemKindPatchRequireKind(t *testing.T, f *itemKindPatchFixture, itemID, want string) {
	t.Helper()
	require.Equal(t, want, itemKindPatchFetchItem(t, f, itemID)["kind"],
		"a fresh GET of %s must report kind %q", itemID, want)
	require.Equal(t, want, itemKindPatchStoredKind(t, f.ts.DB.Pool, itemID),
		"project_items.kind for %s must be %q", itemID, want)
}

// TestItemKindPatch_ChangesKindAndPersistsIt is the happy path, and it is the
// test the persistence defect fails: the PATCH response was already correct
// while the column was never written, so the 200 and the echoed body prove
// nothing on their own. The GET and the raw column are the real assertions.
func TestItemKindPatch_ChangesKindAndPersistsIt(t *testing.T) {
	f := newItemKindPatchFixture(t)
	id := itemKindPatchCreateItem(t, f, "Retype me", itemKindPatchOriginalKind)

	r := f.ts.patch(t, f.itemsBase+"/"+id, map[string]any{"kind": "bug"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "patching kind: %s", r.Body)
	require.Equal(t, "bug", decodeJSONMap(t, r.Body)["kind"],
		"the PATCH response must echo the new kind")

	itemKindPatchRequireKind(t, f, id, "bug")
}

// TestItemKindPatch_UnknownSlugRejectedAndWritesNothing: validation runs before
// applyItemPatch, so a rejected type leaves the row untouched — including the
// title that rode in on the same request. Without the check the database would
// have accepted "nonsense" (D49: no CHECK since migration 032).
func TestItemKindPatch_UnknownSlugRejectedAndWritesNothing(t *testing.T) {
	f := newItemKindPatchFixture(t)
	id := itemKindPatchCreateItem(t, f, "Keep my type", itemKindPatchOriginalKind)

	r := f.ts.patch(t, f.itemsBase+"/"+id,
		map[string]any{"kind": "nonsense", "title": "Renamed too"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode,
		"an undefined type slug must be refused: %s", r.Body)

	errObj := decodeErrorObject(t, r.Body)
	require.Equal(t, "VALIDATION_ERROR", errObj["code"])
	require.Equal(t, "unknown or archived item type", errObj["message"])

	itemKindPatchRequireKind(t, f, id, itemKindPatchOriginalKind)
	require.Equal(t, "Keep my type", itemKindPatchFetchItem(t, f, id)["title"],
		"a rejected patch must write none of its fields, not just the bad one")
}

// TestItemKindPatch_ArchivedTypeRejectedAndWritesNothing covers the slug that
// exists but is no longer offered. The fixture proves the type is usable while
// active first, otherwise the 400 after archiving would be indistinguishable
// from "spike was never a type at all".
//
// It also pins the other half of the archive rule: an item already carrying an
// archived slug stays editable in every respect that does not mention the type.
// That only holds because an absent kind is never validated.
func TestItemKindPatch_ArchivedTypeRejectedAndWritesNothing(t *testing.T) {
	f := newItemKindPatchFixture(t)

	r := f.ts.post(t, f.typesBase, map[string]any{"name": "Spike"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item type: %s", r.Body)
	spike := decodeJSONMap(t, r.Body)
	require.Equal(t, "spike", spike["slug"])
	spikeID, ok := spike["id"].(string)
	require.True(t, ok, "created type must carry an id: %s", r.Body)

	onArchivedType := itemKindPatchCreateItem(t, f, "Already a spike", "spike")
	target := itemKindPatchCreateItem(t, f, "Wants to be a spike", itemKindPatchOriginalKind)

	// While active, retyping to it works and persists.
	r = f.ts.patch(t, f.itemsBase+"/"+target, map[string]any{"kind": "spike"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "an active custom type must be accepted: %s", r.Body)
	itemKindPatchRequireKind(t, f, target, "spike")

	// Put the target back so the rejection below starts from a known slug.
	r = f.ts.patch(t, f.itemsBase+"/"+target,
		map[string]any{"kind": itemKindPatchOriginalKind}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "restoring the original kind: %s", r.Body)
	itemKindPatchRequireKind(t, f, target, itemKindPatchOriginalKind)

	// Archiving closes the door for new assignments.
	r = f.ts.patch(t, f.typesBase+"/"+spikeID, map[string]any{"archived": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "archive type: %s", r.Body)

	r = f.ts.patch(t, f.itemsBase+"/"+target, map[string]any{"kind": "spike"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode,
		"an archived type must be refused on PATCH exactly as on POST: %s", r.Body)
	require.Equal(t, "unknown or archived item type", decodeErrorObject(t, r.Body)["message"])
	itemKindPatchRequireKind(t, f, target, itemKindPatchOriginalKind)

	// The already-typed item is not frozen by its type being archived.
	r = f.ts.patch(t, f.itemsBase+"/"+onArchivedType, map[string]any{"title": "Still editable"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"an item on an archived type must stay editable: %s", r.Body)
	got := itemKindPatchFetchItem(t, f, onArchivedType)
	require.Equal(t, "Still editable", got["title"])
	itemKindPatchRequireKind(t, f, onArchivedType, "spike")
}

// TestItemKindPatch_EmptyKindRejectedAndWritesNothing: "" is a value the client
// deliberately sent, not an omission, and it names no type. This is the case the
// pointer buys — as a plain string it would be indistinguishable from absent.
//
// Two layers would refuse it, so the assertion names the one that must: the
// error message pins Handler.validateItemKind, not ItemService.validateItem's
// non-empty check further down. The service check only knows "" from non-"" and
// would pass any other bad slug straight through, so a test satisfied by either
// message would not be testing the type vocabulary at all.
func TestItemKindPatch_EmptyKindRejectedAndWritesNothing(t *testing.T) {
	f := newItemKindPatchFixture(t)
	id := itemKindPatchCreateItem(t, f, "Not typeless", itemKindPatchOriginalKind)

	r := f.ts.patch(t, f.itemsBase+"/"+id, map[string]any{"kind": ""}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode,
		"an explicitly empty kind must be refused: %s", r.Body)
	require.Equal(t, "unknown or archived item type", decodeErrorObject(t, r.Body)["message"])

	itemKindPatchRequireKind(t, f, id, itemKindPatchOriginalKind)
}

// TestItemKindPatch_AbsentKindLeavesStoredKindAlone is the #68 property extended
// to the new field: a body that never mentions kind must leave the stored slug
// exactly as it was, while still applying what it did carry.
//
// Verified by mutation: replacing applyItemPatch's `if req.Kind != nil` guard
// with an unconditional assignment makes an absent kind resolve to "". The
// handler's own validateItemKind does not fire on it — nothing was sent to
// validate — so the empty slug travels down to ItemService.validateItem, which
// rejects it. Every kind-omitting PATCH then answers
// 400 "updating item: kind must be ticket, task, story, epic, or bug", which is
// #68 verbatim: the assignee control failing on a body it never had reason to
// think was wrong. Each sub-case below fails on its 200.
//
// That service check is the reason the reintroduced defect surfaces as a 400
// rather than as "" in the column. It is the last line, not the first: it only
// asks whether the slug is non-empty, so it would still wave through a garbage
// slug that got past the handler. The re-reads stay the assertions that matter.
func TestItemKindPatch_AbsentKindLeavesStoredKindAlone(t *testing.T) {
	f := newItemKindPatchFixture(t)
	id := itemKindPatchCreateItem(t, f, "Original Title", itemKindPatchOriginalKind)
	path := f.itemsBase + "/" + id

	// Title only.
	r := f.ts.patch(t, path, map[string]any{"title": "changed"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "title-only patch: %s", r.Body)
	body := decodeJSONMap(t, r.Body)
	require.Equal(t, "changed", body["title"], "the field the body carried must change")
	require.Equal(t, itemKindPatchOriginalKind, body["kind"], "an absent kind must not be applied")
	require.Equal(t, "changed", itemKindPatchFetchItem(t, f, id)["title"])
	itemKindPatchRequireKind(t, f, id, itemKindPatchOriginalKind)

	// Assignee only — the exact body the item-detail assignee control sends,
	// which is the request shape #68 was opened for.
	r = f.ts.patch(t, path, map[string]any{"assignee_id": f.ts.UserID}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "assignee-only patch: %s", r.Body)
	body = decodeJSONMap(t, r.Body)
	require.Equal(t, f.ts.UserID.String(), body["assignee_id"], "the assignee must actually have changed")
	require.Equal(t, itemKindPatchOriginalKind, body["kind"], "an absent kind must not be applied")
	itemKindPatchRequireKind(t, f, id, itemKindPatchOriginalKind)

	// The degenerate body: nothing at all is still a valid no-op, not a blanking.
	r = f.ts.patch(t, path, map[string]any{}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "empty patch: %s", r.Body)
	require.Equal(t, "changed", itemKindPatchFetchItem(t, f, id)["title"],
		"an empty patch must not disturb the title")
	itemKindPatchRequireKind(t, f, id, itemKindPatchOriginalKind)
}

// itemKindPatchCreateItemAs creates an item as an arbitrary persona of the
// shared write-capability fixture and returns its id.
func itemKindPatchCreateItemAs(t *testing.T, f *writeCapFixture, token, title string) string {
	t.Helper()
	r := f.requestAs(t, token, http.MethodPost, f.spacePath("/projects/items"),
		map[string]any{"title": title, "kind": itemKindPatchOriginalKind, "priority": "medium"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item as persona: %s", r.Body)
	id, ok := decodeJSONMap(t, r.Body)["id"].(string)
	require.True(t, ok, "created item must carry an id: %s", r.Body)
	return id
}

// TestItemKindPatch_PermissionsUnchangedOnKindPatch: adding a field to the PATCH
// body changes nothing about who may send it. The write floor still refuses a
// viewer, and above the floor the edit_own/edit_any split still decides.
//
// The contributor rows are the ones that isolate the in-handler gate: a
// contributor is already past RequireWriteFloor(create_items), so the 403 on
// another persona's item can only come from access.CanEditEntity. The
// same-body success on their own item is what makes that denial mean
// "not yours" rather than "kind is unpatchable".
func TestItemKindPatch_PermissionsUnchangedOnKindPatch(t *testing.T) {
	f := newWriteCapFixture(t)

	contribItem := itemKindPatchCreateItemAs(t, f, f.contribTok, "Contributor Item")
	agentItem := itemKindPatchCreateItemAs(t, f, f.agentTok, "Agent Item")
	itemPath := func(id string) string { return f.spacePath("/projects/items/" + id) }
	kindBody := map[string]any{"kind": "bug"}

	// Premise: the viewer can read the item, so the denial below is the write
	// floor rather than an invisible space.
	r := f.requestAs(t, f.viewerTok, http.MethodGet, itemPath(contribItem), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "viewer must be able to read the item: %s", r.Body)
	requireAPIForbidden(t, f.requestAs(t, f.viewerTok, http.MethodPatch, itemPath(contribItem), kindBody))

	// A contributor retypes their own item, and it persists.
	r = f.requestAs(t, f.contribTok, http.MethodPatch, itemPath(contribItem), kindBody)
	require.Equal(t, http.StatusOK, r.StatusCode, "contributor retypes own item: %s", r.Body)
	require.Equal(t, "bug", decodeJSONMap(t, r.Body)["kind"])
	require.Equal(t, "bug", itemKindPatchStoredKind(t, f.ts.DB.Pool, contribItem),
		"the contributor's own retype must reach the column")

	// The same body on the agent's item is refused by edit_any_item…
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPatch, itemPath(agentItem), kindBody))
	require.Equal(t, itemKindPatchOriginalKind, itemKindPatchStoredKind(t, f.ts.DB.Pool, agentItem),
		"a refused patch must write nothing")

	// …and the refusal comes first: a bogus slug from an unauthorised caller
	// still answers 403, never the 400 that would confirm the slug is unknown.
	// This fails if validateItemKind is ever hoisted above the permission check.
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPatch, itemPath(agentItem),
		map[string]any{"kind": "nonsense"}))

	// The agent holds edit_any_item, so the same request lands.
	r = f.requestAs(t, f.agentTok, http.MethodPatch, itemPath(contribItem),
		map[string]any{"kind": "story"})
	require.Equal(t, http.StatusOK, r.StatusCode, "agent retypes the contributor's item: %s", r.Body)
	require.Equal(t, "story", itemKindPatchStoredKind(t, f.ts.DB.Pool, contribItem))
}
