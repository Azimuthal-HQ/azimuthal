package teams

import "errors"

// Domain errors. The API layer maps them onto status codes.
var (
	// ErrNotFound — team absent or soft-deleted (404).
	ErrNotFound = errors.New("team not found")
	// ErrNameRequired — empty team name (400).
	ErrNameRequired = errors.New("team name is required")
	// ErrInvalidSlug — slug fails the lowercase-kebab shape (400).
	ErrInvalidSlug = errors.New("team slug must be lowercase letters, digits, and hyphens")
	// ErrSlugTaken — slug already used in the org (409).
	ErrSlugTaken = errors.New("a team with this slug already exists in the organisation")
	// ErrCycle — reparenting under the team's own subtree (or itself) (400).
	ErrCycle = errors.New("reparenting would create a cycle")
	// ErrDepthExceeded — depth(new_parent) + height(subtree) > 5 (400).
	ErrDepthExceeded = errors.New("reparenting would exceed the maximum team depth of 5")
	// ErrHasChildren — deletion blocked by child teams (409).
	ErrHasChildren = errors.New("team has child teams and cannot be deleted")
	// ErrOwnsSpaces — deletion blocked by owned spaces (409).
	ErrOwnsSpaces = errors.New("team owns spaces and cannot be deleted")
	// ErrDefaultTeam — the org default team cannot be deleted or reparented (400).
	ErrDefaultTeam = errors.New("the org default team cannot be deleted or reparented")
	// ErrInvalidMemberRole — team_members.role outside member|lead (400).
	ErrInvalidMemberRole = errors.New("team member role must be 'member' or 'lead'")
	// ErrNotOrgMember — enrolling a user who is not in the org (400).
	ErrNotOrgMember = errors.New("user is not a member of this organisation")
	// ErrMemberNotFound — membership row absent (404).
	ErrMemberNotFound = errors.New("team member not found")
	// ErrParentNotFound — prospective parent absent/deleted/foreign-org (400).
	ErrParentNotFound = errors.New("parent team not found in this organisation")
)
