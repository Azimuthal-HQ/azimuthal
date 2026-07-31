package workflow

import (
	"context"

	"github.com/google/uuid"
)

// TierStore is data access for ADR-0011's three tiers.
//
// It is a SECOND interface rather than seventeen more methods on Repository.
// Repository is mocked in several handler tests, and widening it would force
// every one of those mocks to grow methods their subject never calls — which is
// how a mock stops resembling the thing it stands for.
type TierStore interface {
	// ── Tier 1: guards ──

	// GuardsForTransition returns every guard on a transition, ordered by
	// (position, id).
	//
	// Rows are returned VERBATIM, including any kind this build does not
	// recognise. That is deliberate: refusing to read an unknown guard would
	// turn a rolling deploy into a 500 on every transition through the edge,
	// whereas passing it through lets Evaluate refuse it by name with a
	// sentence a person can act on. The fail-closed decision belongs to the
	// evaluator, which has the context to explain itself; the store's job is to
	// not lose information on the way there.
	GuardsForTransition(ctx context.Context, transitionID uuid.UUID) ([]Guard, error)
	// GuardsForWorkflow returns every guard in a workflow, for the admin
	// surface.
	GuardsForWorkflow(ctx context.Context, workflowID uuid.UUID) ([]Guard, error)
	CreateGuard(ctx context.Context, g Guard) (Guard, error)
	DeleteGuard(ctx context.Context, id uuid.UUID) error

	// ── Tier 3: post-functions ──

	PostFunctionsForTransition(ctx context.Context, transitionID uuid.UUID) ([]PostFunction, error)
	PostFunctionsForWorkflow(ctx context.Context, workflowID uuid.UUID) ([]PostFunction, error)
	CreatePostFunction(ctx context.Context, p PostFunction) (PostFunction, error)
	DeletePostFunction(ctx context.Context, id uuid.UUID) error

	// ── Tier 2: approver configuration ──

	ApproversForTransition(ctx context.Context, transitionID uuid.UUID) ([]Approver, error)
	ApproversForWorkflow(ctx context.Context, workflowID uuid.UUID) ([]Approver, error)
	CreateApprover(ctx context.Context, a Approver) (Approver, error)
	DeleteApprover(ctx context.Context, id uuid.UUID) error

	// ── Tier 2: approval instances ──

	// CreateApproval records a request. A second concurrent request for the
	// same item loses on migration 047's partial unique index and comes back as
	// ErrApprovalPending, never as a duplicate row.
	CreateApproval(ctx context.Context, a Approval) (Approval, error)
	// PendingApprovalForEntity returns the item's outstanding request, or
	// ErrNotFound.
	PendingApprovalForEntity(ctx context.Context, entityType ApprovalEntityType, entityID uuid.UUID) (Approval, error)
	GetApproval(ctx context.Context, id uuid.UUID) (Approval, error)
	// DecideApproval records a decision on a pending request. A request another
	// approver has already decided comes back as ErrApprovalAlreadyDecided
	// rather than being overwritten — the update carries `decided_at IS NULL`,
	// so the race is settled by the database.
	//
	// reason is nil when the approver said nothing, which migration 050 permits
	// alongside a decision but never without one. It is written in the same
	// statement as the decision; see the query's header for why a follow-up
	// UPDATE would be wrong.
	DecideApproval(ctx context.Context, id, decidedBy uuid.UUID, d Decision, reason *string) (Approval, error)
	// ApprovalsForEntity returns every request ever made about an item, newest
	// first, decided and pending alike.
	ApprovalsForEntity(ctx context.Context, entityType ApprovalEntityType, entityID uuid.UUID) ([]Approval, error)
	// PendingApprovalsForSpace powers both the "awaiting a decision" surface
	// and the board's blocked markers.
	PendingApprovalsForSpace(ctx context.Context, spaceID uuid.UUID) ([]Approval, error)
	// PendingApprovalCountForTransition is asked before an administrator
	// deletes a transition, so the delete can refuse rather than orphan
	// in-flight requests.
	PendingApprovalCountForTransition(ctx context.Context, transitionID uuid.UUID) (int64, error)

	// ── Resolution helpers for the chokepoint ──

	// StateByName maps a status string onto the workflow state it names.
	// Returns ErrNotFound when the workflow has no state by that name, which is
	// an ordinary state: statuses are free text and can be renamed out from
	// under an item.
	StateByName(ctx context.Context, workflowID uuid.UUID, name string) (*State, error)
	// TransitionBetween returns the edge between two states, or ErrNotFound.
	TransitionBetween(ctx context.Context, workflowID, fromStateID, toStateID uuid.UUID) (*Transition, error)
	// EffectiveTeamIDs is the actor's ADR-0007 effective team set, delegating to
	// the effective_team_ids() schema function so a guard and a space grant can
	// never disagree about who is in a team.
	EffectiveTeamIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error)
}
