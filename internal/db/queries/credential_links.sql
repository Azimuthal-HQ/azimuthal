-- name: InvalidateOutstandingCredentialLinks :execrows
-- Issue-supersedes-outstanding: minting a new link for a (user, purpose) retires
-- every live link that user already holds for that purpose, so at most one is
-- ever redeemable. Runs in the same transaction as CreateCredentialLink (see
-- CredentialLinkAdapter.Issue), exactly as the portal's CreateMagicLink does.
UPDATE credential_links
SET invalidated_at = now()
WHERE user_id = $1 AND purpose = $2
  AND consumed_at IS NULL AND invalidated_at IS NULL;

-- name: CreateCredentialLink :one
INSERT INTO credential_links (user_id, purpose, token_hash, new_email, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: InspectCredentialLink :one
-- A NON-consuming validity check for the redemption page, so it can render the
-- right form (set-password for signin/reset, confirm for email_change) without
-- burning the single-use link. The three-guard predicate matches
-- ConsumeCredentialLink exactly, so inspect and consume never disagree about
-- whether a link is still redeemable.
SELECT user_id, purpose, new_email
FROM credential_links
WHERE token_hash = $1
  AND consumed_at IS NULL
  AND invalidated_at IS NULL
  AND expires_at > now();

-- name: ConsumeCredentialLink :one
-- Single use enforced by a guarded UPDATE, never a pre-check — copied from the
-- portal's ConsumeMagicLink. consumed_at, invalidated_at and expires_at are all
-- tested in the WHERE; zero rows returned is the one answer for consumed,
-- superseded, expired and never-existed alike, so a caller can never tell which.
UPDATE credential_links
SET consumed_at = now()
WHERE token_hash = $1
  AND consumed_at IS NULL
  AND invalidated_at IS NULL
  AND expires_at > now()
RETURNING user_id, purpose, new_email;
