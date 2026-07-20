-- Space grants and permission resolution (v0.3 spec §4 migration 023, §5,
-- ADR-0007). subject_id is polymorphic (user | team) with no FK; the store
-- layer owns integrity — grants are deleted with their user, and grants to
-- non-org-members are rejected before insert.

-- name: CreateSpaceGrant :one
INSERT INTO space_grants (id, org_id, space_id, subject_type, subject_id, role, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSpaceGrant :one
SELECT * FROM space_grants WHERE id = $1;

-- name: UpdateSpaceGrantRole :one
UPDATE space_grants SET role = $2 WHERE id = $1
RETURNING *;

-- name: DeleteSpaceGrant :exec
DELETE FROM space_grants WHERE id = $1;

-- name: ListGrantsBySpace :many
-- subject_type comparisons here are polymorphism discriminators, not
-- role-name checks — permission decisions never read this query.
SELECT g.id, g.org_id, g.space_id, g.subject_type, g.subject_id, g.role,
       g.created_at, g.created_by,
       COALESCE(CASE WHEN g.subject_type = 'user' THEN u.display_name ELSE t.name END, '')::text AS subject_name,
       ((g.subject_type = 'team' AND t.id IS NULL) OR
        (g.subject_type = 'user' AND u.id IS NULL))::boolean AS subject_missing
FROM space_grants g
LEFT JOIN users u ON g.subject_type = 'user' AND u.id = g.subject_id AND u.deleted_at IS NULL
LEFT JOIN teams t ON g.subject_type = 'team' AND t.id = g.subject_id AND t.deleted_at IS NULL
WHERE g.space_id = $1
ORDER BY g.created_at ASC;

-- name: DeleteGrantsBySubjectUser :exec
DELETE FROM space_grants WHERE subject_type = 'user' AND subject_id = $1;

-- name: DeleteGrantsBySubjectUserInOrg :exec
-- Removal from one org drops that org's grants only; the user's grants in
-- other orgs are untouched.
DELETE FROM space_grants WHERE subject_type = 'user' AND subject_id = $1 AND org_id = $2;

-- name: ListTeamGrantsByOrg :many
-- The access matrix (P2.5 W6): every team-subject grant in the org in one
-- query. Cells are (team, space) pairs; user-subject grants are not matrix
-- cells. Also the base state for bulk diff computation — loaded FOR UPDATE
-- inside the bulk-apply transaction so the diff cannot shift under it.
SELECT g.id, g.space_id, g.subject_id AS team_id, g.role, g.created_at, g.created_by
FROM space_grants g
JOIN spaces s ON s.id = g.space_id AND s.deleted_at IS NULL
WHERE g.org_id = $1 AND g.subject_type = 'team'
ORDER BY g.created_at ASC;

-- name: ListTeamGrantsByOrgForUpdate :many
SELECT g.id, g.space_id, g.subject_id AS team_id, g.role
FROM space_grants g
JOIN spaces s ON s.id = g.space_id AND s.deleted_at IS NULL
WHERE g.org_id = $1 AND g.subject_type = 'team'
ORDER BY g.id ASC
FOR UPDATE OF g;

-- name: DeleteGrantsBySubjectTeam :exec
DELETE FROM space_grants WHERE subject_type = 'team' AND subject_id = $1;

-- name: ResolveAccessRows :many
-- The once-per-request resolution (spec §5). One round trip returning
-- (space_id, role) rows: every grant matching the user directly or any team
-- in the user's effective set — direct teams plus all descendants via the
-- GIN-indexed path overlap — plus a viewer row for each org-visible space.
-- The caller reduces rows to highest-role-per-space; row count is bounded by
-- matching grants, never by list-result size.
WITH direct_teams AS (
    SELECT tm.team_id
    FROM team_members tm
    JOIN teams dt ON dt.id = tm.team_id AND dt.deleted_at IS NULL
    WHERE tm.user_id = sqlc.arg(user_id) AND tm.org_id = sqlc.arg(org_id)
),
effective_teams AS (
    SELECT t.id
    FROM teams t
    WHERE t.org_id = sqlc.arg(org_id)
      AND t.deleted_at IS NULL
      AND t.path && (SELECT COALESCE(array_agg(team_id), '{}')::uuid[] FROM direct_teams)
)
SELECT g.space_id, g.role
FROM space_grants g
JOIN spaces s ON s.id = g.space_id AND s.deleted_at IS NULL
WHERE g.org_id = sqlc.arg(org_id)
  AND ((g.subject_type = 'user' AND g.subject_id = sqlc.arg(user_id))
       OR (g.subject_type = 'team' AND g.subject_id IN (SELECT id FROM effective_teams)))
UNION ALL
SELECT s.id AS space_id, 'viewer'::text AS role
FROM spaces s
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL AND s.visibility = 'org';

-- name: ListMatchingGrantsForSpace :many
-- Backs the effective-access explanation: every grant on the space that
-- reaches the target user, with the granted team's path so the caller can
-- name which direct team matched and at what depth.
WITH direct_teams AS (
    SELECT tm.team_id
    FROM team_members tm
    JOIN teams dt ON dt.id = tm.team_id AND dt.deleted_at IS NULL
    WHERE tm.user_id = sqlc.arg(user_id) AND tm.org_id = sqlc.arg(org_id)
)
SELECT g.id, g.subject_type, g.subject_id, g.role, g.created_at, g.created_by,
       t.name AS team_name, t.path AS team_path
FROM space_grants g
LEFT JOIN teams t ON g.subject_type = 'team' AND t.id = g.subject_id AND t.deleted_at IS NULL
WHERE g.space_id = sqlc.arg(space_id)
  AND ((g.subject_type = 'user' AND g.subject_id = sqlc.arg(user_id))
       OR (g.subject_type = 'team'
           AND t.path && (SELECT COALESCE(array_agg(team_id), '{}')::uuid[] FROM direct_teams)))
ORDER BY g.created_at ASC;
