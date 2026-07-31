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

	// ErrInvalidNextSprint is returned when completing a sprint with a carry-over
	// target that does not exist, is in another space, is the sprint being
	// completed, or is already completed.
	ErrInvalidNextSprint = errors.New("next sprint must be an open sprint in the same space")

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

	// ErrInvalidEntityType is returned when a relation endpoint names an entity
	// kind outside the entity_relations CHECK constraint set. Without this the
	// value reaches Postgres and the constraint violation surfaces as a 500.
	ErrInvalidEntityType = errors.New("entity type must be ticket, project_item, or page")

	// ErrRelationTargetNotFound is returned when a relation's target cannot be
	// resolved against the caller's readable spaces.
	//
	// It is deliberately ONE error covering two situations — the target does
	// not exist, and the target exists in a space the caller may not read. They
	// are not told apart anywhere below this line either: the repository answers
	// with a single bool, so no branch exists that could drift into reporting
	// them differently. A distinguishable "exists but forbidden" would be the
	// same disclosure as returning the title, in a different shape.
	ErrRelationTargetNotFound = errors.New("relation target not found")

	// ErrLabelDuplicate is returned when a label with the same name already
	// exists in the organization.
	ErrLabelDuplicate = errors.New("label with this name already exists")

	// ErrKeyRequired is returned when resolving an item by key with an empty key.
	ErrKeyRequired = errors.New("item key is required")

	// ErrColumnNameRequired is returned when creating or renaming a board
	// column with an empty name.
	ErrColumnNameRequired = errors.New("column name is required")

	// ErrColumnNameDuplicate is returned when a board column name is already
	// taken in the same space.
	ErrColumnNameDuplicate = errors.New("a column with this name already exists")

	// ErrInvalidWIPLimit is returned when a WIP limit is zero or negative. A
	// limit of zero would close the column rather than limit it; no limit is
	// expressed by omitting the value.
	ErrInvalidWIPLimit = errors.New("wip limit must be greater than zero")

	// ErrStatusUnmapped is returned when a board configuration would leave an
	// item status with no column to appear in.
	ErrStatusUnmapped = errors.New("every status must be mapped to a column")

	// ErrLastColumn is returned when deleting the only remaining board column,
	// which would leave its statuses nowhere to go.
	ErrLastColumn = errors.New("cannot remove the last column")

	// ErrInvalidRemapTarget is returned when a column deletion names a
	// re-mapping target that does not exist, is in another space, or is the
	// column being deleted.
	ErrInvalidRemapTarget = errors.New("remap target must be another column in the same space")
)
