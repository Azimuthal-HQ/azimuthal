package people

import "errors"

var (
	// ErrNotMember means the target user is not a member of the org.
	ErrNotMember = errors.New("user is not a member of this organization")
	// ErrLastAdmin blocks removing, demoting, or deactivating the last
	// active org admin — enforced in the store layer under row locks.
	ErrLastAdmin = errors.New("cannot remove the last active organization admin")
	// ErrNotActive means the account is already deactivated.
	ErrNotActive = errors.New("account is already deactivated")
	// ErrAlreadyActive means the account is already active.
	ErrAlreadyActive = errors.New("account is already active")
	// ErrInvalidOrgRole rejects roles outside member|admin.
	ErrInvalidOrgRole = errors.New("org role must be member or admin")
	// ErrCannotChangeOwner blocks changing an owner's role through the
	// admin surface — owners are assigned at provisioning only.
	ErrCannotChangeOwner = errors.New("the owner role cannot be changed here")
	// ErrTeamNotFound means the team is not a live team of the org.
	ErrTeamNotFound = errors.New("team not found in this organization")
	// ErrInvalidDisplayName rejects an empty or over-long display name.
	ErrInvalidDisplayName = errors.New("display name must be between 1 and 255 characters")
)
