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
  -- Queues (migration 039) are space-bound saved views and are listed by the
  -- space-scoped queue endpoint, whose middleware has already established that
  -- the caller can read the space. Including them here would mean re-deriving
  -- that audience per row against the caller's readable set.
  AND sv.space_id IS NULL
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
       s.key  AS space_key,
       s.name AS space_name,
       au.display_name AS assignee_name,
       k.sort_key
FROM tickets tk
JOIN spaces s ON s.id = tk.space_id AND s.deleted_at IS NULL
-- One LEFT JOIN, not a per-row lookup. Resolving the assignee's name by
-- fetching each row's user separately is the shape spec §2.5 case 23 forbids
-- outright, and the case-23 tracer would catch it; joining it here keeps the
-- per-request query count constant regardless of how many rows come back.
-- Deliberately unfiltered on the user's deleted_at: assignee_id is already on
-- the wire, so the name reveals nothing further, and a deactivated assignee's
-- work still needs to say who holds it.
LEFT JOIN users au ON au.id = tk.assignee_id
CROSS JOIN LATERAL (
    SELECT CAST(saved_view_sort_key(@sort_field, tk.updated_at, tk.created_at,
                               tk.due_at, tk.resolved_at, tk.priority,
                               tk.title) AS text) COLLATE "C" AS sort_key
) k
WHERE tk.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (tk.space_id = ANY(@readable_space_ids::uuid[])
       OR tk.id = ANY(@shared_ticket_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0
       OR (tk.space_id = ANY(@space_ids::uuid[])) <> @not_space_ids::boolean)
  AND (cardinality(@statuses::text[]) = 0
       OR (tk.status = ANY(@statuses::text[])) <> @not_statuses::boolean)
  AND (cardinality(@priorities::text[]) = 0
       OR (tk.priority = ANY(@priorities::text[])) <> @not_priorities::boolean)
  -- COALESCE is load-bearing here and nowhere else in this block. assignee_id
  -- is the only NULLABLE column the ticket half negates (verified against the
  -- database, not the migrations: space_id, status and priority are NOT NULL on
  -- both tables). For a row with no assignee, `assignee_id = ANY(...)` is NULL
  -- rather than false, and `NULL <> true` is NULL — so without the COALESCE an
  -- unassigned row would be dropped from "not assigned to Alice", which is
  -- exactly the set it belongs to.
  AND (NOT @filter_assignee::boolean
       OR (COALESCE(tk.assignee_id = ANY(@assignee_ids::uuid[]), false)
           OR (@include_unassigned::boolean AND tk.assignee_id IS NULL)
          ) <> @not_assignees::boolean)
  -- The pattern arrives already escaped by access.EscapeLike. It is not
  -- escaped again here: repeating the replace(replace(replace(...))) idiom
  -- from users.sql and tickets.sql would be a third copy of the same escape,
  -- and the caller's `%` or `_` must match itself either way.
  AND (@text_pattern::text = '' OR tk.title ILIKE @text_pattern::text)
  -- The four v2 date ranges. Half-open: `after` is inclusive, `before` is
  -- exclusive, so two adjacent ranges partition the timeline and no row is
  -- counted in both. (audit_log.sql's date filter is closed at both ends; that
  -- one is a report window a person reads, this one is a range that has to
  -- compose.) Both bounds are already resolved to instants by the caller —
  -- relative tokens never reach SQL, so every gadget in one request compares
  -- against the same moment.
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR tk.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR tk.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR tk.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR tk.updated_at < sqlc.narg(updated_before)::timestamptz)
  -- due_at and resolved_at are NULLABLE. A row with no due date matches NO
  -- due_at range, in either direction — it is not "due before X" and not "due
  -- after X" either. That is the intended reading and it is why there is no
  -- COALESCE here: a null due date is an absent fact, not an early or late one.
  AND (sqlc.narg(due_after)::timestamptz IS NULL OR tk.due_at >= sqlc.narg(due_after)::timestamptz)
  AND (sqlc.narg(due_before)::timestamptz IS NULL OR tk.due_at < sqlc.narg(due_before)::timestamptz)
  AND (sqlc.narg(resolved_after)::timestamptz IS NULL OR tk.resolved_at >= sqlc.narg(resolved_after)::timestamptz)
  AND (sqlc.narg(resolved_before)::timestamptz IS NULL OR tk.resolved_at < sqlc.narg(resolved_before)::timestamptz)
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
       pi.resolved_at, pi.kind, pi.sprint_id, pi.item_key,
       s.key  AS space_key,
       s.name AS space_name,
       au.display_name AS assignee_name,
       k.sort_key
FROM project_items pi
JOIN spaces s ON s.id = pi.space_id AND s.deleted_at IS NULL
-- See the note on ListViewTickets: one join, never a per-row lookup.
LEFT JOIN users au ON au.id = pi.assignee_id
CROSS JOIN LATERAL (
    SELECT CAST(saved_view_sort_key(@sort_field, pi.updated_at, pi.created_at,
                               pi.due_at, pi.resolved_at, pi.priority,
                               pi.title) AS text) COLLATE "C" AS sort_key
) k
WHERE pi.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (pi.space_id = ANY(@readable_space_ids::uuid[])
       OR pi.id = ANY(@shared_item_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0
       OR (pi.space_id = ANY(@space_ids::uuid[])) <> @not_space_ids::boolean)
  AND (cardinality(@statuses::text[]) = 0
       OR (pi.status = ANY(@statuses::text[])) <> @not_statuses::boolean)
  AND (cardinality(@priorities::text[]) = 0
       OR (pi.priority = ANY(@priorities::text[])) <> @not_priorities::boolean)
  -- See the note on the ticket half: COALESCE guards the NULLABLE columns, so
  -- an unassigned row survives "not assigned to Alice" instead of being dropped
  -- by three-valued logic.
  AND (NOT @filter_assignee::boolean
       OR (COALESCE(pi.assignee_id = ANY(@assignee_ids::uuid[]), false)
           OR (@include_unassigned::boolean AND pi.assignee_id IS NULL)
          ) <> @not_assignees::boolean)
  AND (cardinality(@kinds::text[]) = 0
       OR (pi.kind = ANY(@kinds::text[])) <> @not_kinds::boolean)
  -- sprint_id is the second nullable column, so it needs the COALESCE for the
  -- same reason: a backlog item in no sprint belongs in "not in sprint 4".
  AND (cardinality(@sprint_ids::uuid[]) = 0
       OR COALESCE(pi.sprint_id = ANY(@sprint_ids::uuid[]), false) <> @not_sprint_ids::boolean)
  AND (@text_pattern::text = '' OR pi.title ILIKE @text_pattern::text)
  -- The four v2 date ranges — half-open, and identical to the ticket half. Read
  -- the note there; the two blocks must stay the same predicate or a count
  -- gadget and the list it counts would disagree.
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR pi.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR pi.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR pi.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR pi.updated_at < sqlc.narg(updated_before)::timestamptz)
  AND (sqlc.narg(due_after)::timestamptz IS NULL OR pi.due_at >= sqlc.narg(due_after)::timestamptz)
  AND (sqlc.narg(due_before)::timestamptz IS NULL OR pi.due_at < sqlc.narg(due_before)::timestamptz)
  AND (sqlc.narg(resolved_after)::timestamptz IS NULL OR pi.resolved_at >= sqlc.narg(resolved_after)::timestamptz)
  AND (sqlc.narg(resolved_before)::timestamptz IS NULL OR pi.resolved_at < sqlc.narg(resolved_before)::timestamptz)
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

-- name: ListEffectiveTeamIDs :many
-- The caller's ADR-0007 effective team set, for one request. Delegates to the
-- named schema function (migration 038) rather than restating the expansion,
-- so this and space-grant resolution cannot drift into granting differently.
-- Selecting teams.id rather than the function output directly gives sqlc a
-- real column to type: it cannot infer the type of a set-returning
-- function's output column and falls back to interface{}.
SELECT t.id FROM teams t
WHERE t.id IN (SELECT e.team_id FROM effective_team_ids(@org_id, @user_id) AS e(team_id));

-- name: ListQueuesForSpace :many
-- A space's queues in display order. The route that reaches this is
-- space-read guarded, so membership of the audience is already established --
-- that is what lets a queue carry visibility 'space' without this query
-- re-deriving who may see it.
SELECT sv.*, u.display_name AS owner_name
FROM saved_views sv
JOIN users u ON u.id = sv.owner_id
WHERE sv.org_id = @org_id
  AND sv.space_id = @space_id
  AND sv.deleted_at IS NULL
ORDER BY sv.position ASC;

-- name: GetQueue :one
SELECT * FROM saved_views
WHERE id = @id AND org_id = @org_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: NextQueuePosition :one
-- The position a new queue takes: one past the last live queue in the space.
-- COALESCE over the empty case gives the first queue position 0.
SELECT COALESCE(MAX(sv.position) + 1, 0)::int AS next_position
FROM saved_views sv
WHERE sv.space_id = @space_id AND sv.deleted_at IS NULL;

-- name: CreateQueue :one
INSERT INTO saved_views (
    org_id, owner_id, space_id, position, name, description, query, visibility
) VALUES (
    @org_id, @owner_id, @space_id, @position, @name, @description, @query, 'space'
)
RETURNING *;

-- name: CreateQueueIfAbsent :execrows
-- The idempotent half of "create the default queues". ON CONFLICT DO NOTHING
-- against saved_views_space_name_key means pressing the button twice, or two
-- agents pressing it at once, cannot produce duplicates -- the guarantee is a
-- constraint rather than a check-then-insert race.
INSERT INTO saved_views (
    org_id, owner_id, space_id, position, name, description, query, visibility
) VALUES (
    @org_id, @owner_id, @space_id, @position, @name, @description, @query, 'space'
)
ON CONFLICT (space_id, name) WHERE space_id IS NOT NULL AND deleted_at IS NULL
DO NOTHING;

-- name: UpdateQueue :one
UPDATE saved_views
SET name = @name, description = @description, query = @query
WHERE id = @id AND org_id = @org_id AND space_id = @space_id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteQueue :execrows
UPDATE saved_views
SET deleted_at = now()
WHERE id = @id AND org_id = @org_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: SetQueuePosition :execrows
-- One step of a reorder. Callers run several of these inside ONE transaction:
-- saved_views_space_position_key is DEFERRABLE INITIALLY DEFERRED, so the
-- intermediate states where two queues briefly share a position are legal
-- until COMMIT. Renumbering through temporary slots to dodge the constraint
-- is exactly what the deferral exists to avoid.
UPDATE saved_views
SET position = @position
WHERE id = @id AND org_id = @org_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: ListSpaceWorkflowStatuses :many
-- A space's workflow state names with their categories, in board order.
-- Backs the default-queue set: "open" and "resolved" are not literals in this
-- product, they are whichever states the space's workflow puts in the
-- todo/in_progress and done categories.
SELECT ws.name, ws.category
FROM workflow_states ws
JOIN spaces s ON s.workflow_id = ws.workflow_id AND s.deleted_at IS NULL
WHERE s.id = @space_id
ORDER BY ws.position ASC;

-- name: GetSavedViewsByIDs :many
-- The live views among a set of ids, with NO audience filter.
--
-- Audience-blind on purpose, and the one query in this file that is. Its
-- caller is the P5 dashboard loader, which has to tell "this gadget's view was
-- deleted" from "this gadget's view is not yours to see" — two different tiles
-- under ADR-0009 — and filtering by audience here would collapse both into
-- "absent". The audience is applied in Go by views.Audience.Reaches, which is
-- the same rule ListSavedViewsForViewer's WHERE clause spells in SQL.
--
-- Space-bound rows (queues) are excluded for the same reason the generic list
-- excludes them: their audience is enforced by the space-read guard on the
-- route that serves them, and nothing outside that route may widen it. A queue
-- can therefore never be attached to a gadget.
--
-- One query for a whole dashboard, whatever its gadget count. A dashboard that
-- resolved one view per gadget is exactly the per-item shape spec Â§2.5 case 23
-- forbids.
SELECT sv.*,
       u.display_name AS owner_name,
       tm.name        AS team_name
FROM saved_views sv
JOIN users u ON u.id = sv.owner_id
LEFT JOIN teams tm ON tm.id = sv.visibility_team_id
WHERE sv.org_id = @org_id
  AND sv.deleted_at IS NULL
  AND sv.space_id IS NULL
  AND sv.id = ANY(@ids::uuid[]);

-- name: CountViewTickets :one
-- The Beacon half of a saved view's count (P5).
--
-- THE SAME PREDICATES AS ListViewTickets, MINUS THE PAGE. Read that query's
-- header first: the access union, the two arrays that ARE the access control,
-- and the ADR-0008 exception all apply here unchanged and for the same
-- reasons. What is gone is the sort key, the cursor and the limit, because a
-- count has no position and no page.
--
-- It exists so a count gadget never becomes fetch-all-then-count in the
-- client. That form is bounded by MaxPageSize and would silently under-report
-- any view with more than two hundred results, which is precisely the view
-- somebody puts a count on.
SELECT count(*)
FROM tickets tk
JOIN spaces s ON s.id = tk.space_id AND s.deleted_at IS NULL
WHERE tk.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (tk.space_id = ANY(@readable_space_ids::uuid[])
       OR tk.id = ANY(@shared_ticket_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0
       OR (tk.space_id = ANY(@space_ids::uuid[])) <> @not_space_ids::boolean)
  AND (cardinality(@statuses::text[]) = 0
       OR (tk.status = ANY(@statuses::text[])) <> @not_statuses::boolean)
  AND (cardinality(@priorities::text[]) = 0
       OR (tk.priority = ANY(@priorities::text[])) <> @not_priorities::boolean)
  -- COALESCE is load-bearing here and nowhere else in this block. assignee_id
  -- is the only NULLABLE column the ticket half negates (verified against the
  -- database, not the migrations: space_id, status and priority are NOT NULL on
  -- both tables). For a row with no assignee, `assignee_id = ANY(...)` is NULL
  -- rather than false, and `NULL <> true` is NULL — so without the COALESCE an
  -- unassigned row would be dropped from "not assigned to Alice", which is
  -- exactly the set it belongs to.
  AND (NOT @filter_assignee::boolean
       OR (COALESCE(tk.assignee_id = ANY(@assignee_ids::uuid[]), false)
           OR (@include_unassigned::boolean AND tk.assignee_id IS NULL)
          ) <> @not_assignees::boolean)
  AND (@text_pattern::text = '' OR tk.title ILIKE @text_pattern::text)
  -- The four v2 date ranges. Half-open: `after` is inclusive, `before` is
  -- exclusive, so two adjacent ranges partition the timeline and no row is
  -- counted in both. (audit_log.sql's date filter is closed at both ends; that
  -- one is a report window a person reads, this one is a range that has to
  -- compose.) Both bounds are already resolved to instants by the caller —
  -- relative tokens never reach SQL, so every gadget in one request compares
  -- against the same moment.
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR tk.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR tk.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR tk.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR tk.updated_at < sqlc.narg(updated_before)::timestamptz)
  -- due_at and resolved_at are NULLABLE. A row with no due date matches NO
  -- due_at range, in either direction — it is not "due before X" and not "due
  -- after X" either. That is the intended reading and it is why there is no
  -- COALESCE here: a null due date is an absent fact, not an early or late one.
  AND (sqlc.narg(due_after)::timestamptz IS NULL OR tk.due_at >= sqlc.narg(due_after)::timestamptz)
  AND (sqlc.narg(due_before)::timestamptz IS NULL OR tk.due_at < sqlc.narg(due_before)::timestamptz)
  AND (sqlc.narg(resolved_after)::timestamptz IS NULL OR tk.resolved_at >= sqlc.narg(resolved_after)::timestamptz)
  AND (sqlc.narg(resolved_before)::timestamptz IS NULL OR tk.resolved_at < sqlc.narg(resolved_before)::timestamptz);

-- name: CountViewProjectItems :one
-- The Vector half. Structurally identical to CountViewTickets plus the two
-- Vector-only terms, exactly as the two list fan-outs differ.
SELECT count(*)
FROM project_items pi
JOIN spaces s ON s.id = pi.space_id AND s.deleted_at IS NULL
WHERE pi.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (pi.space_id = ANY(@readable_space_ids::uuid[])
       OR pi.id = ANY(@shared_item_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0
       OR (pi.space_id = ANY(@space_ids::uuid[])) <> @not_space_ids::boolean)
  AND (cardinality(@statuses::text[]) = 0
       OR (pi.status = ANY(@statuses::text[])) <> @not_statuses::boolean)
  AND (cardinality(@priorities::text[]) = 0
       OR (pi.priority = ANY(@priorities::text[])) <> @not_priorities::boolean)
  -- See the note on the ticket half: COALESCE guards the NULLABLE columns, so
  -- an unassigned row survives "not assigned to Alice" instead of being dropped
  -- by three-valued logic.
  AND (NOT @filter_assignee::boolean
       OR (COALESCE(pi.assignee_id = ANY(@assignee_ids::uuid[]), false)
           OR (@include_unassigned::boolean AND pi.assignee_id IS NULL)
          ) <> @not_assignees::boolean)
  AND (cardinality(@kinds::text[]) = 0
       OR (pi.kind = ANY(@kinds::text[])) <> @not_kinds::boolean)
  -- sprint_id is the second nullable column, so it needs the COALESCE for the
  -- same reason: a backlog item in no sprint belongs in "not in sprint 4".
  AND (cardinality(@sprint_ids::uuid[]) = 0
       OR COALESCE(pi.sprint_id = ANY(@sprint_ids::uuid[]), false) <> @not_sprint_ids::boolean)
  AND (@text_pattern::text = '' OR pi.title ILIKE @text_pattern::text)
  -- The four v2 date ranges — half-open, and identical to the ticket half. Read
  -- the note there; the two blocks must stay the same predicate or a count
  -- gadget and the list it counts would disagree.
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR pi.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR pi.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR pi.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR pi.updated_at < sqlc.narg(updated_before)::timestamptz)
  AND (sqlc.narg(due_after)::timestamptz IS NULL OR pi.due_at >= sqlc.narg(due_after)::timestamptz)
  AND (sqlc.narg(due_before)::timestamptz IS NULL OR pi.due_at < sqlc.narg(due_before)::timestamptz)
  AND (sqlc.narg(resolved_after)::timestamptz IS NULL OR pi.resolved_at >= sqlc.narg(resolved_after)::timestamptz)
  AND (sqlc.narg(resolved_before)::timestamptz IS NULL OR pi.resolved_at < sqlc.narg(resolved_before)::timestamptz);

-- name: BreakdownViewTickets :many
-- Counts grouped by one field, over the Beacon half of a saved view (P5).
--
-- ONE STATIC QUERY FOR EVERY GROUPABLE FIELD, by the same device migration
-- 038's saved_view_sort_key uses for sorting: the choice is collapsed into one
-- expression rather than spread across one query per field. Four fields times
-- two modules would otherwise be eight near-identical queries, which is eight
-- chances for one of them to drift from the access predicate above it — and a
-- drifted copy of THAT is not a display bug.
--
-- The bucket is (key, label) rather than one column because an assignee's key
-- is an opaque id and its label is a name, while a status is its own label.
-- Grouping by both is free: the label is functionally dependent on the key.
--
-- The empty key is a REAL BUCKET, not a null to drop. Unassigned work is
-- exactly what a breakdown is for, and collapsing it away would make the
-- buckets stop summing to the count.
--
-- ELSE '' is unreachable through the API — views.ParseGroupField refuses
-- anything outside the closed set before this runs, and 'kind' is refused
-- alongside Beacon so it never reaches this query at all. It is written as a
-- single bucket rather than as NULL so that a field added on one side only
-- produces one obviously-wrong tile rather than silently dropped rows.
SELECT g.bucket_key::text AS bucket_key,
       g.bucket_label::text AS bucket_label,
       count(*) AS bucket_count
FROM tickets tk
JOIN spaces s ON s.id = tk.space_id AND s.deleted_at IS NULL
LEFT JOIN users au ON au.id = tk.assignee_id
CROSS JOIN LATERAL (
    SELECT
        CASE @group_by::text
            WHEN 'status'   THEN tk.status
            WHEN 'priority' THEN tk.priority
            WHEN 'assignee' THEN COALESCE(tk.assignee_id::text, '')
            ELSE ''
        END AS bucket_key,
        CASE @group_by::text
            WHEN 'status'   THEN tk.status
            WHEN 'priority' THEN tk.priority
            WHEN 'assignee' THEN COALESCE(au.display_name, '')
            ELSE ''
        END AS bucket_label
) g
WHERE tk.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (tk.space_id = ANY(@readable_space_ids::uuid[])
       OR tk.id = ANY(@shared_ticket_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0
       OR (tk.space_id = ANY(@space_ids::uuid[])) <> @not_space_ids::boolean)
  AND (cardinality(@statuses::text[]) = 0
       OR (tk.status = ANY(@statuses::text[])) <> @not_statuses::boolean)
  AND (cardinality(@priorities::text[]) = 0
       OR (tk.priority = ANY(@priorities::text[])) <> @not_priorities::boolean)
  -- COALESCE is load-bearing here and nowhere else in this block. assignee_id
  -- is the only NULLABLE column the ticket half negates (verified against the
  -- database, not the migrations: space_id, status and priority are NOT NULL on
  -- both tables). For a row with no assignee, `assignee_id = ANY(...)` is NULL
  -- rather than false, and `NULL <> true` is NULL — so without the COALESCE an
  -- unassigned row would be dropped from "not assigned to Alice", which is
  -- exactly the set it belongs to.
  AND (NOT @filter_assignee::boolean
       OR (COALESCE(tk.assignee_id = ANY(@assignee_ids::uuid[]), false)
           OR (@include_unassigned::boolean AND tk.assignee_id IS NULL)
          ) <> @not_assignees::boolean)
  AND (@text_pattern::text = '' OR tk.title ILIKE @text_pattern::text)
  -- The four v2 date ranges. Half-open: `after` is inclusive, `before` is
  -- exclusive, so two adjacent ranges partition the timeline and no row is
  -- counted in both. (audit_log.sql's date filter is closed at both ends; that
  -- one is a report window a person reads, this one is a range that has to
  -- compose.) Both bounds are already resolved to instants by the caller —
  -- relative tokens never reach SQL, so every gadget in one request compares
  -- against the same moment.
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR tk.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR tk.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR tk.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR tk.updated_at < sqlc.narg(updated_before)::timestamptz)
  -- due_at and resolved_at are NULLABLE. A row with no due date matches NO
  -- due_at range, in either direction — it is not "due before X" and not "due
  -- after X" either. That is the intended reading and it is why there is no
  -- COALESCE here: a null due date is an absent fact, not an early or late one.
  AND (sqlc.narg(due_after)::timestamptz IS NULL OR tk.due_at >= sqlc.narg(due_after)::timestamptz)
  AND (sqlc.narg(due_before)::timestamptz IS NULL OR tk.due_at < sqlc.narg(due_before)::timestamptz)
  AND (sqlc.narg(resolved_after)::timestamptz IS NULL OR tk.resolved_at >= sqlc.narg(resolved_after)::timestamptz)
  AND (sqlc.narg(resolved_before)::timestamptz IS NULL OR tk.resolved_at < sqlc.narg(resolved_before)::timestamptz)
GROUP BY g.bucket_key, g.bucket_label
ORDER BY count(*) DESC, g.bucket_key ASC;

-- name: BreakdownViewProjectItems :many
-- The Vector half. 'kind' appears here and not in the ticket query because
-- project_items has the column and tickets does not — the same asymmetry the
-- filter vocabulary records, enforced the same way.
SELECT g.bucket_key::text AS bucket_key,
       g.bucket_label::text AS bucket_label,
       count(*) AS bucket_count
FROM project_items pi
JOIN spaces s ON s.id = pi.space_id AND s.deleted_at IS NULL
LEFT JOIN users au ON au.id = pi.assignee_id
CROSS JOIN LATERAL (
    SELECT
        CASE @group_by::text
            WHEN 'status'   THEN pi.status
            WHEN 'priority' THEN pi.priority
            WHEN 'assignee' THEN COALESCE(pi.assignee_id::text, '')
            WHEN 'kind'     THEN COALESCE(pi.kind, '')
            ELSE ''
        END AS bucket_key,
        CASE @group_by::text
            WHEN 'status'   THEN pi.status
            WHEN 'priority' THEN pi.priority
            WHEN 'assignee' THEN COALESCE(au.display_name, '')
            WHEN 'kind'     THEN COALESCE(pi.kind, '')
            ELSE ''
        END AS bucket_label
) g
WHERE pi.deleted_at IS NULL
  AND s.org_id = @org_id
  AND (pi.space_id = ANY(@readable_space_ids::uuid[])
       OR pi.id = ANY(@shared_item_ids::uuid[]))
  AND (cardinality(@space_ids::uuid[]) = 0
       OR (pi.space_id = ANY(@space_ids::uuid[])) <> @not_space_ids::boolean)
  AND (cardinality(@statuses::text[]) = 0
       OR (pi.status = ANY(@statuses::text[])) <> @not_statuses::boolean)
  AND (cardinality(@priorities::text[]) = 0
       OR (pi.priority = ANY(@priorities::text[])) <> @not_priorities::boolean)
  -- See the note on the ticket half: COALESCE guards the NULLABLE columns, so
  -- an unassigned row survives "not assigned to Alice" instead of being dropped
  -- by three-valued logic.
  AND (NOT @filter_assignee::boolean
       OR (COALESCE(pi.assignee_id = ANY(@assignee_ids::uuid[]), false)
           OR (@include_unassigned::boolean AND pi.assignee_id IS NULL)
          ) <> @not_assignees::boolean)
  AND (cardinality(@kinds::text[]) = 0
       OR (pi.kind = ANY(@kinds::text[])) <> @not_kinds::boolean)
  -- sprint_id is the second nullable column, so it needs the COALESCE for the
  -- same reason: a backlog item in no sprint belongs in "not in sprint 4".
  AND (cardinality(@sprint_ids::uuid[]) = 0
       OR COALESCE(pi.sprint_id = ANY(@sprint_ids::uuid[]), false) <> @not_sprint_ids::boolean)
  AND (@text_pattern::text = '' OR pi.title ILIKE @text_pattern::text)
  -- The four v2 date ranges — half-open, and identical to the ticket half. Read
  -- the note there; the two blocks must stay the same predicate or a count
  -- gadget and the list it counts would disagree.
  AND (sqlc.narg(created_after)::timestamptz IS NULL OR pi.created_at >= sqlc.narg(created_after)::timestamptz)
  AND (sqlc.narg(created_before)::timestamptz IS NULL OR pi.created_at < sqlc.narg(created_before)::timestamptz)
  AND (sqlc.narg(updated_after)::timestamptz IS NULL OR pi.updated_at >= sqlc.narg(updated_after)::timestamptz)
  AND (sqlc.narg(updated_before)::timestamptz IS NULL OR pi.updated_at < sqlc.narg(updated_before)::timestamptz)
  AND (sqlc.narg(due_after)::timestamptz IS NULL OR pi.due_at >= sqlc.narg(due_after)::timestamptz)
  AND (sqlc.narg(due_before)::timestamptz IS NULL OR pi.due_at < sqlc.narg(due_before)::timestamptz)
  AND (sqlc.narg(resolved_after)::timestamptz IS NULL OR pi.resolved_at >= sqlc.narg(resolved_after)::timestamptz)
  AND (sqlc.narg(resolved_before)::timestamptz IS NULL OR pi.resolved_at < sqlc.narg(resolved_before)::timestamptz)
GROUP BY g.bucket_key, g.bucket_label
ORDER BY count(*) DESC, g.bucket_key ASC;
