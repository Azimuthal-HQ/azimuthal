package projects

import "errors"

// Sentinel errors for the projects package.
var (
	// ErrNotFound is returned when a project item, sprint, or label cannot be located.
	ErrNotFound = errors.New("not found")

	// ErrInvalidTransition is returned when a sprint status change is not allowed
	// by the lifecycle state machine (planned → active → completed).
	ErrInvalidTransition = errors.New("invalid status transition")

	// ErrSprintActive is returned when trying to start a sprint while another
	// sprint in the same space is already active.
	ErrSprintActive = errors.New("another sprint is already active in this space")

	// ErrTitleRequired is returned when creating or updating an item with an empty title.
	ErrTitleRequired = errors.New("title is required")

	// ErrNameRequired is returned when creating or updating a sprint or label with an empty name.
	ErrNameRequired = errors.New("name is required")

	// ErrInvalidPriority is returned when a priority value is not one of
	// urgent, high, medium, or low.
	ErrInvalidPriority = errors.New("priority must be urgent, high, medium, or low")

	// ErrInvalidKind is returned when an item kind is not one of
	// ticket, task, story, epic, or bug.
	ErrInvalidKind = errors.New("kind must be ticket, task, story, epic, or bug")

	// ErrInvalidRelationKind is returned when a relation kind is not one of
	// blocks, is_blocked_by, duplicates, relates_to, or wiki_link.
	ErrInvalidRelationKind = errors.New("relation kind must be blocks, is_blocked_by, duplicates, relates_to, or wiki_link")

	// ErrSelfRelation is returned when attempting to create a relation from
	// an item to itself.
	ErrSelfRelation = errors.New("cannot create a relation from an item to itself")

	// ErrLabelDuplicate is returned when a label with the same name already
	// exists in the organization.
	ErrLabelDuplicate = errors.New("label with this name already exists")
)
