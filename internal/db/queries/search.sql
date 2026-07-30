-- Cross-module search (P6, spec §5, §7; ADR-0009, ADR-0010).
--
-- Three queries, one per module, merged in the API layer. ADR-0009 requires the
-- fan-out shape — "fan out per module, merge in the API layer" — so this is
-- deliberately NOT a three-way UNION in SQL. The merge, its ordering and its
-- cursor live in internal/core/search.
--
--
-- THE ACCESS PREDICATE IS THE POINT OF THIS FILE
-- ----------------------------------------------
-- Every query filters against arrays the caller resolved once per request. They
-- are the access control, not a hint. None may be widened, and no query here may
-- be run with the whole access set empty — the service short-circuits first,
-- exactly as tickets.SuggestionService does, because `= ANY('{}')` is false for
-- every row and a query that returns nothing is indistinguishable from a query
-- that was never permitted.
--
-- The arms are parenthesised as ONE group, and that grouping is load-bearing.
-- If the outer parens are lost, the share and subtree arms are promoted above
-- `deleted_at IS NULL` and above the `@@` match, and the query returns every
-- soft-deleted descendant of every cascade root the viewer holds, for every
-- query, whether or not it matches. Correct behaviour on the readable-space arm
-- survives, so a leak test and a two-viewer test both still pass. The test that
-- catches it is a search whose term matches NOTHING, run by a viewer who holds
-- both a readable space and a share, asserting zero rows.
--
--
-- WHY `deleted_at IS NULL` IS SPELLED OUT AND NOT INHERITED
-- --------------------------------------------------------
-- All three GIN indexes are PARTIAL (`WHERE deleted_at IS NULL`, from 009 and
-- 049). The planner can only use a partial index when it can prove the query
-- implies the predicate, so the literal must appear in each query or the index
-- is silently skipped and search degrades to a sequential scan with no error.
--
--
-- WHY THE TSQUERY EXPRESSION IS WRITTEN TWICE PER QUERY
-- ----------------------------------------------------
-- Once in the WHERE for the `@@` match, once inside the ranking lateral. The
-- saved-views fan-out computes its sort key once in a lateral precisely to stop
-- copies drifting, and that reasoning is right — but a `@@` whose right-hand
-- side comes from a lateral is not reliably recognised as an indexable clause,
-- and losing the GIN index is the worse failure. The two copies are the same
-- deterministic expression over the same parameter; the risk is bounded and
-- named here rather than left for a reader to wonder about.
--
--
-- THE SORT KEY
-- ------------
-- search_sort_key (049) turns ts_rank's `real` into fixed-width zero-padded
-- text. Text, because the merge in Go must order the three halves EXACTLY as
-- PostgreSQL ordered each one — that is what COLLATE "C" buys, byte order on
-- both sides — and because a float that has round-tripped through a JSON cursor
-- is not a value to test for equality. Normalization flag 32 (rank/(rank+1))
-- bounds rank to [0,1) without changing any relative order, which is what makes
-- a fixed width safe.
--
-- Ranking is always descending — search has no user-chosen sort — so unlike the
-- saved-view queries there is no @descending arm, and the cursor comparison is
-- the descending one only. It is written out as (key <, or key = and id <)
-- rather than as a row constructor because sqlc cannot parse a COLLATE inside a
-- row constructor.
--
-- Columns are ENUMERATED, never `SELECT *`. Two of the three shipped per-space
-- search queries are `SELECT *`, which puts the raw lexeme vector on the wire as
-- `search_vector`; entity_shares_integration_test.go already asserts that key's
-- absence as a leak.

-- name: GlobalSearchPages :many
-- The Codex half, and the only one with a subtree problem.
--
-- Pages are the entity cascade applies to (026 constrains cascade to pages), so
-- a share of a page subtree must make DESCENDANTS searchable for the recipient.
-- That is the D46 shape: each cascade root's space and LIKE pattern stay PAIRED
-- through unnest, so the space pin is per-root. Two independent arrays would
-- match the cartesian product and a page in one root's space whose path sits
-- under another root's subtree would leak across the space boundary.
--
-- The root page itself is covered by the direct arm: every share's entity id,
-- cascade roots included, is in SharedEntities.direct, and the patterns from
-- CascadeSubtreeArrays are strict-descendant.
--
-- `tag:` narrows to tagged pages by joining page_tags — never by adding a term
-- to the tsquery. A tag's slug does not survive tokenization as one lexeme
-- (`#design_docs` becomes 'design' and 'doc'), page-level tags set through the
-- tags endpoint never touch the pages row at all, and a generated column cannot
-- reference another table. So the tag model is reachable only as a join.
SELECT p.id, p.space_id, p.parent_id, p.title, p.path, p.version, p.author_id,
       p.created_at, p.updated_at,
       s.key  AS space_key,
       s.name AS space_name,
       k.sort_key
FROM pages p
JOIN spaces s ON s.id = p.space_id AND s.deleted_at IS NULL
CROSS JOIN LATERAL (
    SELECT CAST(search_sort_key(
               ts_rank(p.search_vector,
                       websearch_to_tsquery('english', sqlc.arg(query)::text), 32)
           ) AS text) COLLATE "C" AS sort_key
) k
WHERE p.deleted_at IS NULL
  AND s.org_id = sqlc.arg(org_id)
  AND p.search_vector @@ websearch_to_tsquery('english', sqlc.arg(query)::text)
  AND (p.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])
       OR p.id = ANY(sqlc.arg(shared_page_ids)::uuid[])
       OR EXISTS (
           -- The two unnests are in one SELECT list on purpose. Since PG10
           -- multiple set-returning functions there expand in LOCKSTEP — row i
           -- of one pairs with row i of the other — which is the same pairing
           -- `unnest(a, b) AS root(x, y)` gives, and which sqlc's analyzer can
           -- resolve where the two-argument form defeats it. Both arrays come
           -- from one call (CascadeSubtreeArrays) so they are always the same
           -- length, and lockstep is therefore exact rather than NULL-padded.
           SELECT 1
           FROM (SELECT unnest(sqlc.arg(subtree_space_ids)::uuid[]) AS space_id,
                        unnest(sqlc.arg(subtree_patterns)::text[])  AS pattern) AS root
           WHERE p.space_id = root.space_id
             AND p.path LIKE root.pattern
       ))
  AND (NOT sqlc.arg(filter_tag)::boolean
       OR EXISTS (SELECT 1 FROM page_tags pt
                  WHERE pt.page_id = p.id AND pt.tag_id = sqlc.arg(tag_id)::uuid))
  AND (sqlc.arg(cursor_key)::text = ''
       OR k.sort_key < sqlc.arg(cursor_key)::text
       OR (k.sort_key = sqlc.arg(cursor_key)::text AND p.id < sqlc.arg(cursor_id)::uuid))
ORDER BY k.sort_key DESC, p.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GlobalSearchTickets :many
-- The Beacon half. Flat: 026 constrains cascade to pages, so a ticket share is
-- always exactly one entity and the direct id set is complete — there is no
-- subtree arm and there must not be one.
--
-- A ticket's human reference (SD-42) is composed from spaces.key and number by
-- tickets.ComposeRef and is deliberately NOT in the search vector: a generated
-- column may only reference its own row, so spaces.key is unreachable from it.
-- Reference lookup is the /ticketref resolver's job, which is exact.
SELECT t.id, t.space_id, t.number, t.title, t.status, t.priority,
       t.assignee_id, t.created_at, t.updated_at,
       s.key  AS space_key,
       s.name AS space_name,
       k.sort_key
FROM tickets t
JOIN spaces s ON s.id = t.space_id AND s.deleted_at IS NULL
CROSS JOIN LATERAL (
    SELECT CAST(search_sort_key(
               ts_rank(t.search_vector,
                       websearch_to_tsquery('english', sqlc.arg(query)::text), 32)
           ) AS text) COLLATE "C" AS sort_key
) k
WHERE t.deleted_at IS NULL
  AND s.org_id = sqlc.arg(org_id)
  AND t.search_vector @@ websearch_to_tsquery('english', sqlc.arg(query)::text)
  AND (t.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])
       OR t.id = ANY(sqlc.arg(shared_ticket_ids)::uuid[]))
  AND (sqlc.arg(cursor_key)::text = ''
       OR k.sort_key < sqlc.arg(cursor_key)::text
       OR (k.sort_key = sqlc.arg(cursor_key)::text AND t.id < sqlc.arg(cursor_id)::uuid))
ORDER BY k.sort_key DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GlobalSearchProjectItems :many
-- The Vector half. Flat, for the same reason as tickets.
--
-- item_key is selected rather than composed: project items carry their own key
-- column (031), and 049 puts it in the search vector at weight A, so searching
-- "VEC-14" finds the item by its key.
SELECT i.id, i.space_id, i.number, i.item_key, i.kind, i.title, i.status,
       i.priority, i.assignee_id, i.created_at, i.updated_at,
       s.key  AS space_key,
       s.name AS space_name,
       k.sort_key
FROM project_items i
JOIN spaces s ON s.id = i.space_id AND s.deleted_at IS NULL
CROSS JOIN LATERAL (
    SELECT CAST(search_sort_key(
               ts_rank(i.search_vector,
                       websearch_to_tsquery('english', sqlc.arg(query)::text), 32)
           ) AS text) COLLATE "C" AS sort_key
) k
WHERE i.deleted_at IS NULL
  AND s.org_id = sqlc.arg(org_id)
  AND i.search_vector @@ websearch_to_tsquery('english', sqlc.arg(query)::text)
  AND (i.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])
       OR i.id = ANY(sqlc.arg(shared_item_ids)::uuid[]))
  AND (sqlc.arg(cursor_key)::text = ''
       OR k.sort_key < sqlc.arg(cursor_key)::text
       OR (k.sort_key = sqlc.arg(cursor_key)::text AND i.id < sqlc.arg(cursor_id)::uuid))
ORDER BY k.sort_key DESC, i.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ParseSearchQuery :one
-- The parsed tsquery, as text, for the service's empty-query guard.
--
-- websearch_to_tsquery never errors — a stopword-only query, 3000 characters of
-- one letter, or pure punctuation all yield an EMPTY tsquery and at most a
-- NOTICE, which pgx does not surface. An empty tsquery `@@` anything is false,
-- so the three queries above return nothing, and "nothing matched" becomes
-- indistinguishable from "there was nothing to match". That matters beyond
-- tidiness: every permission assertion of the form "the unreadable row does not
-- appear" passes vacuously for such inputs, with the access predicate deleted.
--
-- So the service asks first and answers with a distinct state rather than an
-- ordinary empty page. Negation is the other reason to look: `-zebra` parses to
-- `!'zebra'`, which matches every row the viewer can read at rank 0 — an
-- unbounded read and a total collapse of the rank ordering onto the tiebreaker.
SELECT websearch_to_tsquery('english', sqlc.arg(query)::text)::text AS parsed;

-- ── Snippets ─────────────────────────────────────────────────────────────────
--
-- ts_headline runs ONLY over the ids of the page actually being returned, never
-- over the match set. It is the expensive half of a text search — it re-parses
-- the document body per row rather than reading the index — so computing it for
-- rows nobody will see is exactly the cost the fan-out limit exists to avoid.
-- Three queries, one per module, and only for the modules the page contains.
--
-- The ids are already permission-filtered: they come out of the fan-out above,
-- in the same request. These queries deliberately do NOT re-derive access and
-- must never be called with ids from any other source. `deleted_at IS NULL` is
-- still spelled out, so a row soft-deleted between the fan-out and here drops
-- its snippet rather than resurrecting its text.
--
-- THE DELIMITERS ARE CONTROL CHARACTERS, NOT MARKUP.
-- ts_headline escapes nothing. It returns the source text with the delimiters
-- inserted, so `StartSel=<mark>` over a body containing `<script>` produces a
-- snippet carrying that script, and any client rendering the snippet as HTML
-- executes it. STX and ETX (U+0002 / U+0003) cannot occur in ordinary prose and
-- JSON-encode as  and , so the client splits on them and wraps the
-- pieces in real elements — highlighting without ever interpreting stored
-- content as markup.

-- name: HeadlinePages :many
SELECT p.id,
       ts_headline('english', coalesce(p.content, ''),
                   websearch_to_tsquery('english', sqlc.arg(query)::text),
                   'MaxFragments=1, MaxWords=28, MinWords=10, ShortWord=3, StartSel='
                       || chr(2) || ', StopSel=' || chr(3)
       ) AS snippet
FROM pages p
WHERE p.id = ANY(sqlc.arg(ids)::uuid[]) AND p.deleted_at IS NULL;

-- name: HeadlineTickets :many
SELECT t.id,
       ts_headline('english', coalesce(t.description, ''),
                   websearch_to_tsquery('english', sqlc.arg(query)::text),
                   'MaxFragments=1, MaxWords=28, MinWords=10, ShortWord=3, StartSel='
                       || chr(2) || ', StopSel=' || chr(3)
       ) AS snippet
FROM tickets t
WHERE t.id = ANY(sqlc.arg(ids)::uuid[]) AND t.deleted_at IS NULL;

-- name: HeadlineProjectItems :many
SELECT i.id,
       ts_headline('english', coalesce(i.description, ''),
                   websearch_to_tsquery('english', sqlc.arg(query)::text),
                   'MaxFragments=1, MaxWords=28, MinWords=10, ShortWord=3, StartSel='
                       || chr(2) || ', StopSel=' || chr(3)
       ) AS snippet
FROM project_items i
WHERE i.id = ANY(sqlc.arg(ids)::uuid[]) AND i.deleted_at IS NULL;
