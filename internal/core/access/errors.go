package access

import "errors"

// ErrNotOrgMember is returned by Resolve when the user has no membership in
// the org. Callers must translate it to 404 — org existence is never leaked
// to non-members.
var ErrNotOrgMember = errors.New("user is not a member of this organisation")

// ErrSubjectNotOrgMember rejects a grant whose user subject is not an org
// member (spec §4 referential obligation — 400 at the API layer).
var ErrSubjectNotOrgMember = errors.New("grant subject is not a member of this organisation")

// ErrSubjectTeamNotFound rejects a grant whose team subject does not exist
// (live) in the org (400 at the API layer).
var ErrSubjectTeamNotFound = errors.New("grant subject team not found in this organisation")

// ErrDuplicateGrant is returned when a grant for the same (space, subject)
// already exists (409 at the API layer).
var ErrDuplicateGrant = errors.New("a grant for this subject already exists on this space")

// ErrGrantNotFound is returned for operations on a missing grant.
var ErrGrantNotFound = errors.New("grant not found")

// ErrBulkEmpty rejects a bulk change with no cells.
var ErrBulkEmpty = errors.New("bulk change contains no cells")

// ErrBulkTooLarge rejects a bulk change over the batch size bound.
var ErrBulkTooLarge = errors.New("bulk change exceeds the maximum batch size")

// ErrBulkDuplicateCell rejects two changes targeting the same (team, space)
// cell in one batch — the outcome would depend on ordering.
var ErrBulkDuplicateCell = errors.New("bulk change targets the same cell twice")

// ErrBulkUnknownTeam rejects a bulk change naming a team that is not a live
// team of the org (400 at the API layer; the whole batch fails).
var ErrBulkUnknownTeam = errors.New("bulk change references a team not in this organisation")

// ErrBulkUnknownSpace rejects a bulk change naming a space that is not a
// live space of the org (400 at the API layer; the whole batch fails).
var ErrBulkUnknownSpace = errors.New("bulk change references a space not in this organisation")

// ErrInvalidShareEntityType rejects a share naming an unknown entity type
// (400 at the API layer).
var ErrInvalidShareEntityType = errors.New("unknown shareable entity type")

// ErrSharedEntityNotFound is returned when a share's target entity does not
// exist live in the org (404 at the API layer — existence is never leaked).
var ErrSharedEntityNotFound = errors.New("shared entity not found")

// ErrInvalidShareAudience rejects an audience outside org|team (400).
var ErrInvalidShareAudience = errors.New("share audience must be 'org' or 'team'")

// ErrShareAudienceIDRequired rejects a team-audience share without a team
// (400) — mirrors the entity_shares_audience_id_present CHECK.
var ErrShareAudienceIDRequired = errors.New("team-audience shares must name a team")

// ErrShareAudienceIDForbidden rejects an org-audience share carrying a team
// (400) — the other half of the same CHECK.
var ErrShareAudienceIDForbidden = errors.New("org-audience shares must not name a team")

// ErrShareAudienceTeamNotFound rejects a team audience that is not a live
// team of the org (400, matching the grant-subject rule).
var ErrShareAudienceTeamNotFound = errors.New("share audience team not found in this organisation")

// ErrShareCascadeNotPage rejects cascade on a non-page entity (400) —
// mirrors the entity_shares_cascade_pages_only CHECK.
var ErrShareCascadeNotPage = errors.New("cascade shares are only available for pages")

// ErrShareExpiryNotFuture rejects an expiry at or before now (400).
var ErrShareExpiryNotFuture = errors.New("share expiry must be in the future")

// ErrDuplicateShare is returned when an active share for the same
// (entity, audience) cell already exists (409 at the API layer).
var ErrDuplicateShare = errors.New("an active share for this audience already exists on this entity")

// ErrShareNotFound is returned for operations on a missing share (404).
var ErrShareNotFound = errors.New("share not found")

// ErrShareAlreadyRevoked is returned when revoking a share that was already
// revoked (410 at the API layer — the row exists but is gone).
var ErrShareAlreadyRevoked = errors.New("share already revoked")
