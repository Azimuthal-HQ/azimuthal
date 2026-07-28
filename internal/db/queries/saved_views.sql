-- Saved views (ADR-0009, migration 038).
--
-- Two kinds of query live here and they answer to different rules:
--
--   * CRUD on the saved_views rows themselves — ordinary org-scoped reads.
--   * The two RESULT fan-outs, ListViewTickets and ListViewProjectItems,
--     which are the first genuinely cross-space reads in the product and the
--     sanctioned ADR-0008 exception. Read the header on ListViewTickets
--     before changing either.

-- name: CreateSavedView :one
INSERT INTO saved_views (
    org_id, owner_id, name, description, query, visibility, visibility_team_id
) VALUES (
    @org_id, @owner_id, @name, @description, @query, @visibility, @visibility_team_id
)
RETURNING *;

-- name: GetSavedView :one
SELECT * FROM saved_views
WHERE id = @id AND org_id = @org_id AND deleted_at IS NULL;

-- name: UpdateSavedView :one
-- The whole mutable surface in one statement. There is no partial-PATCH
-- pointer here on purpose: a single nullable pointer for visibility_team_id
-- would collapse "absent" and "null" into "clear it", which is exactly the
-- defect that silently wiped every item's due_at. The handler reads the row,
-- applies only the fields the request set, and writes the whole result back.
UPDATE saved_views
SET name               = @name,
    description        = @description,
    query              = @query,
    visibility         = @visibility,
    visibility_team_id = @visibility_team_id
WHERE id = @id AND org_id = @org_id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteSavedView :execrows
UPDATE saved_views
SET deleted_at = now()
WHERE id = @id AND org_id = @org_id AND deleted_at IS NULL;

-- name: ListSavedViewsForViewer :many
-- Every view the caller may see: their own, plus org-audience views, plus
-- team-audience views whose team is in the caller's EFFECTIVE team set
-- (already subject-side expanded by the resolver, ADR-0007).
--
-- visibility_team_id IS NOT NULL is load-bearing on the team branch. Migration
-- 038 sets the column NULL when the team is deleted rather than cascading the
-- delete, so a view whose audience team is gone must match nobody. Without the
-- explicit NOT NULL test a degraded view would rely on `= ANY('{}')` being
-- false, which is true today but is SQL trivia rather than intent.
SELECT sv.*,
       u.display_name AS owner_name,
       tm.name        AS team_name
FROM saved_views sv
JOIN users u ON u.id = sv.owner_id
LEFT JOIN teams tm ON tm.id = sv.visibility_team_id
WHERE sv.org_id = @org_id
  AND sv.deleted_at IS NULL
  AND (
        sv.owner_id = @viewer_id
     OR sv.visibility = 'org'
     OR (sv.visibility = 'team'
         AND sv.visibility_team_id IS NOT NULL
         AND sv.visibility_team_id = ANY(@effective_team_ids::uuid[]))
  )
ORDER BY sv.updated_at DESC;

-- name: ListLiveSpaceIDs :many
-- Which of the named spaces still exist. Backs the ADR-0009 case-C1
-- degradation check ("scope unavailable") for a whole page of views in ONE
-- query, so validity stays derived rather than stored and the per-request
-- query budget stays constant regardless of how many views are listed.
SELECT id FROM spaces
WHERE id = ANY(@space_ids::uuid[]) AND org_id = @org_id AND deleted_at IS NULL;

-- name: ListViewTickets :many
-- The Beacon half of a saved view's results.
--
-- THE ADR-0008 EXCEPTION, DELIBERATELY TAKEN
-- ------------------------------------------
-- Space-scoped listings never union shares; that rule stands everywhere else.
-- A saved view is cross-container by nature, so this is the one sanctioned
-- place where the readable-space set is unioned with the caller's directly
-- shared entities:
--
--     tk.space_id = ANY(@readable_space_ids) OR tk.id = ANY(@shared_ticket_ids)
--
-- There is no subtree term here, and its absence is correct rather than an
-- omission. Migration 026 constrains cascade to pages
-- (entity_shares_cascade_pages_only), so a ticket share is always a single
-- entity and DirectIDs() is the complete story for this table. The
-- (space_id, pattern) pair problem the spec §5 warns about is a PAGE problem
-- and cannot arise here. P6 search, which does read pages, still has to solve
-- it.
--
-- Both arrays are the access control, not a hint. Neither may be widened, and
-- the caller must never run this with both empty — the service short-circuits
-- first, exactly as tickets.SuggestionService does.
--
-- THE SORT KEY, AND WHY IT COMES FROM A LATERAL
-- ---------------------------------------------
-- saved_view_sort_key (migration 038) collapses whichever field the view sorts
-- by into one comparable text value, so a single static query serves all six
-- sortable fields in both directions, and so the API layer can merge this
-- module's rows with Vector's by comparing the same key type — which is what
-- ADR-0009's "fan out per module, merge in the API layer" requires.
--
-- The CROSS JOIN LATERAL computes it ONCE and names it, rather than repeating
-- the call in the cursor predicate and twice more in the ORDER BY. Four copies
-- of one expression is four chances for one to drift, and a drifted copy shows
-- up as rows silently missing from page two.
--
-- COLLATE "C" is applied here, in the lateral, so it propagates to every
-- comparison and to the ordering without being restated. It is load-bearing:
-- PostgreSQL orders text by the database collation, Go compares strings
-- bytewise, and for a title sort those disagree — the merge would then
-- interleave two correctly-sorted halves incorrectly. Byte order on both sides
-- makes the SQL order and the Go merge order the same order by construction.
--
-- The cursor comparison is written out as (key <, or key = and id <) rather
-- than as a row constructor. The row form is equivalent and shorter, but sqlc
-- cannot parse a COLLATE inside a row constructor — it reports "edited query
-- syntax is invalid" — so the expanded form is the one that compiles.
SELECT tk.id, tk.number, tk.title, tk.space_id, tk.status, tk.priority,
       tk.assignee_id, tk.created_at, tk.updated_at, tk.due_at, tk.resolved_at,
       tk.labels,
       s.key  AS space_key,
       s.name AS space_name,
       k.sort_key
FROM tickets tk
JOIN spaces s ON s.id = tk.space_id AND s.deleted_at IS NULL
CROSS JOIN LATERAL (
    SELECT CAST(saved_view_sort_key(@sort_field, tk.updated_at, tk.created_at,
                               tk.due_at, tk.resolved_at, tk.priority,
                               tk.title) AS text) COLLATE "C" AS sort_key
) k
WHERE tk.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (tk.space_id = ANY(@readable_space_ids::uuid[])
       OR tk.id = ANY(@shared_ticket_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0 OR tk.space_id = ANY(@space_ids::uuid[]))
  AND (cardinality(@statuses::text[]) = 0 OR tk.status = ANY(@statuses::text[]))
  AND (cardinality(@priorities::text[]) = 0 OR tk.priority = ANY(@priorities::text[]))
  AND (NOT @filter_assignee::boolean
       OR tk.assignee_id = ANY(@assignee_ids::uuid[])
       OR (@include_unassigned::boolean AND tk.assignee_id IS NULL))
  -- The pattern arrives already escaped by access.EscapeLike. It is not
  -- escaped again here: repeating the replace(replace(replace(...))) idiom
  -- from users.sql and tickets.sql would be a third copy of the same escape,
  -- and the caller's `%` or `_` must match itself either way.
  AND (@text_pattern::text = '' OR tk.title ILIKE @text_pattern::text)
  AND (@cursor_key::text = ''
       OR (@descending::boolean
           AND (k.sort_key < @cursor_key::text
                OR (k.sort_key = @cursor_key::text AND tk.id < @cursor_id::uuid)))
       OR (NOT @descending::boolean
           AND (k.sort_key > @cursor_key::text
                OR (k.sort_key = @cursor_key::text AND tk.id > @cursor_id::uuid))))
ORDER BY
    CASE WHEN @descending::boolean THEN k.sort_key END DESC,
    CASE WHEN @descending::boolean THEN tk.id END DESC,
    CASE WHEN NOT @descending::boolean THEN k.sort_key END ASC,
    CASE WHEN NOT @descending::boolean THEN tk.id END ASC
LIMIT @row_limit;

-- name: ListViewProjectItems :many
-- The Vector half. Structurally identical to ListViewTickets — read that
-- header first; only the differences are commented here.
--
-- project_items carries columns tickets does not have, and the filter
-- vocabulary treats two of them as Vector-only: kind and sprint_id. Verified
-- against the database, not the migrations. Both are rejected at write time
-- when Beacon is in the module set, so reaching this query with either set
-- means the view is Vector-only.
--
-- item_key is selected rather than composed: project items carry their own key
-- column (migration 031), whereas a ticket's reference is composed from the
-- space key and number by tickets.ComposeRef. One spelling each, and neither
-- is re-derived in the API layer.
SELECT pi.id, pi.number, pi.title, pi.space_id, pi.status, pi.priority,
       pi.assignee_id, pi.created_at, pi.updated_at, pi.due_at,
       pi.resolved_at, pi.labels, pi.kind, pi.sprint_id, pi.item_key,
       s.key  AS space_key,
       s.name AS space_name,
       k.sort_key
FROM project_items pi
JOIN spaces s ON s.id = pi.space_id AND s.deleted_at IS NULL
CROSS JOIN LATERAL (
    SELECT CAST(saved_view_sort_key(@sort_field, pi.updated_at, pi.created_at,
                               pi.due_at, pi.resolved_at, pi.priority,
                               pi.title) AS text) COLLATE "C" AS sort_key
) k
WHERE pi.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (pi.space_id = ANY(@readable_space_ids::uuid[])
       OR pi.id = ANY(@shared_item_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0 OR pi.space_id = ANY(@space_ids::uuid[]))
  AND (cardinality(@statuses::text[]) = 0 OR pi.status = ANY(@statuses::text[]))
  AND (cardinality(@priorities::text[]) = 0 OR pi.priority = ANY(@priorities::text[]))
  AND (NOT @filter_assignee::boolean
       OR pi.assignee_id = ANY(@assignee_ids::uuid[])
       OR (@include_unassigned::boolean AND pi.assignee_id IS NULL))
  AND (cardinality(@kinds::text[]) = 0 OR pi.kind = ANY(@kinds::text[]))
  AND (cardinality(@sprint_ids::uuid[]) = 0 OR pi.sprint_id = ANY(@sprint_ids::uuid[]))
  AND (@text_pattern::text = '' OR pi.title ILIKE @text_pattern::text)
  AND (@cursor_key::text = ''
       OR (@descending::boolean
           AND (k.sort_key < @cursor_key::text
                OR (k.sort_key = @cursor_key::text AND pi.id < @cursor_id::uuid)))
       OR (NOT @descending::boolean
           AND (k.sort_key > @cursor_key::text
                OR (k.sort_key = @cursor_key::text AND pi.id > @cursor_id::uuid))))
ORDER BY
    CASE WHEN @descending::boolean THEN k.sort_key END DESC,
    CASE WHEN @descending::boolean THEN pi.id END DESC,
    CASE WHEN NOT @descending::boolean THEN k.sort_key END ASC,
    CASE WHEN NOT @descending::boolean THEN pi.id END ASC
LIMIT @row_limit;
