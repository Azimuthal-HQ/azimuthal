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
