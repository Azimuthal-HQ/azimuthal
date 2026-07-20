-- Teams (v0.3 spec §4 migration 022, ADR-0006/0007).
-- path is always parent.path || id and is constructed inside CreateTeam /
-- ReparentSubtree — callers never hand-assemble it.

-- name: CountTeamMembersByOrg :many
-- Member counts for the access matrix's team rows: one query for the org.
SELECT team_id, count(*)::int AS member_count
FROM team_members
WHERE org_id = $1
GROUP BY team_id;

-- name: CreateTeam :one
-- The service validates that parent_id (when set) is a live team in the same
-- org before calling; the COALESCE fallback only serves the NULL-parent root
-- case. The FK rejects nonexistent parents at the database level.
INSERT INTO teams (id, org_id, parent_id, path, slug, name, description, is_default, source)
SELECT sqlc.arg(id), sqlc.arg(org_id), sqlc.narg(parent_id),
       COALESCE((SELECT t.path FROM teams t
                 WHERE t.id = sqlc.narg(parent_id)
                   AND t.org_id = sqlc.arg(org_id)
                   AND t.deleted_at IS NULL), '{}'::uuid[]) || sqlc.arg(id)::uuid,
       sqlc.arg(slug), sqlc.arg(name), sqlc.arg(description),
       sqlc.arg(is_default), sqlc.arg(source)
RETURNING *;

-- name: GetTeamByID :one
SELECT * FROM teams WHERE id = $1 AND deleted_at IS NULL;

-- name: GetDefaultTeam :one
SELECT * FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL;

-- name: ListTeamsByOrg :many
SELECT * FROM teams WHERE org_id = $1 AND deleted_at IS NULL ORDER BY name ASC;

-- name: UpdateTeam :one
UPDATE teams SET name = $2, description = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTeam :exec
UPDATE teams SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: CountTeamChildren :one
SELECT count(*) FROM teams WHERE parent_id = $1 AND deleted_at IS NULL;

-- name: CountTeamOwnedSpaces :one
SELECT count(*) FROM spaces WHERE owner_team_id = $1 AND deleted_at IS NULL;

-- name: ListSubtreeForUpdate :many
-- Locks the whole subtree (root included) for a reparent so concurrent moves
-- cannot interleave. path @> ARRAY[root] means "root appears in the ancestor
-- chain" — the subtree, by the GIN-indexed materialised path.
SELECT * FROM teams
WHERE org_id = $1 AND path @> ARRAY[sqlc.arg(team_id)::uuid] AND deleted_at IS NULL
ORDER BY array_length(path, 1) ASC
FOR UPDATE;

-- name: GetTeamForUpdate :one
SELECT * FROM teams WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ReparentSubtree :exec
-- Rewrites the moved node and every descendant in one statement: each row's
-- path keeps its tail from the moved root down and gains the new parent's
-- path as prefix. The moved root's parent_id is updated in the same pass.
-- teams_depth_max acts as a database-level backstop to the service's
-- depth(new_parent) + height(subtree) <= 5 pre-check.
UPDATE teams
SET path = sqlc.arg(new_parent_path)::uuid[] || path[array_position(path, sqlc.arg(team_id)::uuid):],
    parent_id = CASE WHEN id = sqlc.arg(team_id)::uuid
                     THEN sqlc.narg(new_parent_id) ELSE parent_id END,
    updated_at = now()
WHERE org_id = sqlc.arg(org_id)
  AND path @> ARRAY[sqlc.arg(team_id)::uuid]
  AND deleted_at IS NULL;

-- name: AddTeamMember :one
-- Re-adding an existing member only updates the metadata role — it never
-- touches is_primary, which is managed by the primary-team queries below.
INSERT INTO team_members (team_id, user_id, org_id, role, is_primary, source)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING *;

-- name: GetTeamMember :one
SELECT * FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: RemoveTeamMember :exec
DELETE FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: ListTeamMembers :many
SELECT tm.team_id, tm.user_id, tm.org_id, tm.role, tm.is_primary, tm.created_at,
       u.email, u.display_name, u.avatar_url
FROM team_members tm
JOIN users u ON u.id = tm.user_id AND u.deleted_at IS NULL
WHERE tm.team_id = $1
ORDER BY u.display_name ASC;

-- name: ListUserTeams :many
SELECT t.* FROM teams t
JOIN team_members tm ON tm.team_id = t.id
WHERE tm.user_id = $1 AND tm.org_id = $2 AND t.deleted_at IS NULL
ORDER BY t.name ASC;

-- name: ListUserDirectTeamIDs :many
SELECT t.id FROM teams t
JOIN team_members tm ON tm.team_id = t.id
WHERE tm.user_id = $1 AND tm.org_id = $2 AND t.deleted_at IS NULL;

-- name: CountUserTeamsInOrg :one
SELECT count(*) FROM team_members tm
JOIN teams t ON t.id = tm.team_id AND t.deleted_at IS NULL
WHERE tm.user_id = $1 AND tm.org_id = $2;

-- name: GetPrimaryTeamMember :one
SELECT * FROM team_members WHERE user_id = $1 AND org_id = $2 AND is_primary;

-- name: ClearPrimaryTeam :exec
UPDATE team_members SET is_primary = false
WHERE user_id = $1 AND org_id = $2 AND is_primary;

-- name: SetPrimaryFlag :exec
UPDATE team_members SET is_primary = true WHERE team_id = $1 AND user_id = $2;

-- name: ListPrimaryUserIDsOfTeam :many
SELECT user_id FROM team_members WHERE team_id = $1 AND is_primary;

-- name: ListTeamMemberUserIDs :many
SELECT user_id FROM team_members WHERE team_id = $1;

-- name: BulkEnrollInTeam :exec
-- Moves every member of src_team into dest_team (deletion flow). Existing
-- dest memberships are preserved untouched.
INSERT INTO team_members (team_id, user_id, org_id, role, is_primary, source)
SELECT sqlc.arg(dest_team_id), tm.user_id, tm.org_id, 'member', false, tm.source
FROM team_members tm
WHERE tm.team_id = sqlc.arg(src_team_id)
ON CONFLICT (team_id, user_id) DO NOTHING;

-- name: DeleteTeamMembers :exec
DELETE FROM team_members WHERE team_id = $1;

-- name: SetPrimaryForUsers :exec
UPDATE team_members SET is_primary = true
WHERE team_id = $1 AND user_id = ANY(sqlc.arg(user_ids)::uuid[]);

-- name: GetFallbackPrimaryTeam :one
-- The team that becomes primary when a user's primary membership goes away:
-- the org default team when they belong to it, else their oldest membership.
SELECT tm.team_id FROM team_members tm
JOIN teams t ON t.id = tm.team_id AND t.deleted_at IS NULL
WHERE tm.user_id = $1 AND tm.org_id = $2
ORDER BY t.is_default DESC, tm.created_at ASC
LIMIT 1;

-- name: ListEffectiveTeams :many
-- Subject-side expansion (ADR-0007): every live team whose ancestor chain
-- touches one of the given direct team ids — the direct teams themselves
-- plus all their descendants. One GIN-indexed overlap, no recursion.
SELECT * FROM teams
WHERE org_id = $1 AND deleted_at IS NULL AND path && sqlc.arg(team_ids)::uuid[]
ORDER BY array_length(path, 1) ASC, name ASC;
