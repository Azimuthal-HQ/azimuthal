package invites

import "errors"

var (
	// ErrNotFound covers unknown invite ids and unknown tokens.
	ErrNotFound = errors.New("invite not found")
	// ErrInvalidEmail rejects unparseable addresses.
	ErrInvalidEmail = errors.New("invalid email address")
	// ErrInvalidOrgRole rejects org roles outside member|admin.
	ErrInvalidOrgRole = errors.New("org role must be member or admin")
	// ErrDuplicateInvite means an active invite for the email already exists.
	ErrDuplicateInvite = errors.New("an active invite for this email already exists")
	// ErrAlreadyMember means the email belongs to an account that is
	// already a member of the org.
	ErrAlreadyMember = errors.New("this email already belongs to a member of the organization")
	// ErrTeamNotFound means the initial team is not a live team of the org.
	ErrTeamNotFound = errors.New("team not found in this organization")
	// ErrRevoked means the invite was revoked.
	ErrRevoked = errors.New("invite has been revoked")
	// ErrAlreadyAccepted means the invite was already used.
	ErrAlreadyAccepted = errors.New("invite has already been accepted")
	// ErrExpired means the invite is past its expiry.
	ErrExpired = errors.New("invite has expired")
	// ErrAccountInactive means the invited email belongs to a deactivated
	// account; an admin must reactivate it before the invite can be used.
	ErrAccountInactive = errors.New("the account for this email has been deactivated")
	// ErrDisplayNameAndPasswordRequired means acceptance needs registration
	// fields because the email has no account yet.
	ErrDisplayNameAndPasswordRequired = errors.New("display_name and password are required")
	// ErrPasswordTooShort rejects passwords under 8 characters.
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
)
