package access

import (
	"context"

	"github.com/google/uuid"
)

// BulkChange is one requested cell state in a bulk grant change: set the
// (team, space) grant to Role, or revoke it when Role is nil.
type BulkChange struct {
	TeamID  uuid.UUID
	SpaceID uuid.UUID
	// Role nil means revoke.
	Role *Role
}

// BulkAction is one itemised line of a bulk diff: what applying a change
// does to the existing grant state.
type BulkAction struct {
	TeamID  uuid.UUID
	SpaceID uuid.UUID
	// Action is "create", "update", "revoke", or "noop".
	Action string
	// FromRole is the pre-existing role ("" for creates).
	FromRole string
	// ToRole is the requested role ("" for revokes).
	ToRole string
}

// BulkResult is a computed diff, and — after Apply — the identity of the
// batch that applied it.
type BulkResult struct {
	// BatchID is set by Apply only: the one batch_id shared by the
	// transaction's grant rows and audit events.
	BatchID uuid.UUID
	Creates int
	Updates int
	Revokes int
	Noops   int
	Actions []BulkAction
}

// BulkStore is the persistence contract. Preview and Apply compute the diff
// with the same code path; Apply runs it inside one transaction under row
// locks, applies it, and writes one audit event per action with one shared
// batch_id — a mid-batch failure rolls the whole thing back, audit rows
// included.
type BulkStore interface {
	// MatrixData loads the matrix in a constant number of queries.
	MatrixData(ctx context.Context, orgID uuid.UUID) (MatrixData, error)
	// PreviewBulk computes the diff without applying. Validation errors
	// (dead team, dead space, cross-org ids) fail the whole request.
	PreviewBulk(ctx context.Context, orgID uuid.UUID, changes []BulkChange) (BulkResult, error)
	// ApplyBulk applies the diff atomically. ticketRef travels onto every
	// audit event of the batch.
	ApplyBulk(ctx context.Context, orgID, actorID uuid.UUID, changes []BulkChange, ticketRef string) (BulkResult, error)
}

// MatrixTeam is one team row of the access matrix.
type MatrixTeam struct {
	ID          uuid.UUID
	ParentID    *uuid.UUID
	Path        []uuid.UUID
	Name        string
	IsDefault   bool
	MemberCount int
}

// MatrixSpace is one space column of the access matrix.
type MatrixSpace struct {
	ID         uuid.UUID
	Name       string
	Type       string
	Visibility string
}

// MatrixGrant is one direct team-subject grant (a solid cell).
type MatrixGrant struct {
	ID      uuid.UUID
	TeamID  uuid.UUID
	SpaceID uuid.UUID
	Role    string
}

// MatrixData is everything the matrix renders from. Inherited (ghosted)
// cells are derived client-side from team paths: team T inherits access on
// a space when a DESCENDANT of T holds a grant there (ADR-0007 subject-side
// expansion). They correspond to no grant row and are not editable.
type MatrixData struct {
	Teams  []MatrixTeam
	Spaces []MatrixSpace
	Grants []MatrixGrant
}

// BulkService serves the access matrix and its bulk editing path.
type BulkService struct {
	store BulkStore
}

// NewBulkService creates a BulkService.
func NewBulkService(store BulkStore) *BulkService { return &BulkService{store: store} }

// Matrix loads the matrix data.
func (s *BulkService) Matrix(ctx context.Context, orgID uuid.UUID) (MatrixData, error) {
	return s.store.MatrixData(ctx, orgID)
}

// Preview computes the diff a bulk change would apply.
func (s *BulkService) Preview(ctx context.Context, orgID uuid.UUID, changes []BulkChange) (BulkResult, error) {
	if err := validateBulkSize(changes); err != nil {
		return BulkResult{}, err
	}
	return s.store.PreviewBulk(ctx, orgID, changes)
}

// Apply applies a bulk change as one transaction with one batch_id.
func (s *BulkService) Apply(ctx context.Context, orgID, actorID uuid.UUID, changes []BulkChange, ticketRef string) (BulkResult, error) {
	if err := validateBulkSize(changes); err != nil {
		return BulkResult{}, err
	}
	return s.store.ApplyBulk(ctx, orgID, actorID, changes, ticketRef)
}

// maxBulkChanges bounds one batch. Generous — a 50-team × 100-space matrix
// fits — while keeping a runaway request from holding locks indefinitely.
const maxBulkChanges = 10000

func validateBulkSize(changes []BulkChange) error {
	if len(changes) == 0 {
		return ErrBulkEmpty
	}
	if len(changes) > maxBulkChanges {
		return ErrBulkTooLarge
	}
	seen := make(map[[2]uuid.UUID]struct{}, len(changes))
	for _, c := range changes {
		key := [2]uuid.UUID{c.TeamID, c.SpaceID}
		if _, dup := seen[key]; dup {
			// Two changes targeting one cell make the outcome order-defined;
			// reject rather than guess intent.
			return ErrBulkDuplicateCell
		}
		seen[key] = struct{}{}
	}
	return nil
}
