package tickets

import "errors"

var (
	// ErrNotFound is returned when a ticket cannot be located.
	ErrNotFound = errors.New("ticket not found")

	// ErrInvalidTransition is returned when a status transition is not permitted
	// by the state machine.
	ErrInvalidTransition = errors.New("invalid status transition")

	// ErrInvalidPriority is returned when a priority value is not recognised.
	ErrInvalidPriority = errors.New("invalid priority")

	// ErrInvalidStatus is returned when a status value is not recognised.
	ErrInvalidStatus = errors.New("invalid status")

	// ErrTitleRequired is returned when creating a ticket without a title.
	ErrTitleRequired = errors.New("ticket title is required")

	// ErrSpaceRequired is returned when creating a ticket without a space.
	ErrSpaceRequired = errors.New("space ID is required")

	// ErrReporterRequired is returned when creating a ticket without a reporter.
	ErrReporterRequired = errors.New("reporter ID is required")

	// ErrAlreadyAssigned is returned when re-assigning to the current assignee.
	ErrAlreadyAssigned = errors.New("ticket is already assigned to this user")

	// ErrAssigneeNotOrgMember refuses an assignee who does not belong to the
	// organisation owning the ticket.
	//
	// The obligation is not new: the grants surface already enforces it
	// (access.ErrSubjectNotOrgMember) and so does the share audience
	// (ErrShareAudienceTeamNotFound). Assignment was the one referential write
	// that skipped it, because tickets.assignee_id references the global users
	// table and so the foreign key was satisfied by any user in the
	// installation. The wording follows the grants sentence deliberately.
	ErrAssigneeNotOrgMember = errors.New("assignee is not a member of this organisation")

	// ErrEmailParseFailure is returned when an inbound email cannot be parsed
	// into a valid ticket.
	ErrEmailParseFailure = errors.New("failed to parse inbound email")

	// ErrEmptySearchQuery is returned when a search is attempted with a blank query.
	ErrEmptySearchQuery = errors.New("search query must not be empty")
)
