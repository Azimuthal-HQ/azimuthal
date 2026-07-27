// Package spaces holds the space domain types that outlive any one layer.
//
// It is deliberately thin: space policy — creation authority, visibility
// rules, key derivation — lives in the HTTP handler, where it always has. What
// lives here is the shape of a space creation as a *single unit of work*, so
// the handler can describe one and the adapter can execute it without either
// importing the other.
package spaces

import (
	"context"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// CreateInput is one space creation: the row itself plus everything that has
// to exist the moment it does.
type CreateInput struct {
	// Space is the space row to insert. The handler has already settled the
	// key, slug, type, visibility and owning team.
	Space generated.CreateSpaceParams

	// MemberRowID is the id for the creator's legacy space_members row. Passed
	// in rather than minted here so a retry writes a deterministic set of rows.
	MemberRowID uuid.UUID

	// CreatorID is who is creating the space. They become the space_members
	// admin, and — when CreatorNeedsGrant is set — the subject of the grant.
	CreatorID uuid.UUID

	// CreatorNeedsGrant asks for a space_admin grant for the creator, written
	// in the same transaction. It is false for an org admin, who reaches every
	// space through the middleware bypass and must hold zero grant rows
	// (ADR-0007).
	CreatorNeedsGrant bool
}

// CreateTxStore writes a space and its inseparable companions atomically.
//
// Creating a space is up to three writes — the space row, the creator's
// space_members row, and (for a non-org-admin creator) the creator's
// space_admin grant. Run separately, each failure after the first leaves
// wreckage that nothing cleans up and the 500 gives no hint of:
//
//   - a member-row failure leaves an orphaned space in the org directory;
//   - a grant failure leaves a lead the owner of a space they cannot open,
//     and cannot grant themselves into, because granting requires reaching it.
//
// Neither is fixable by retrying the request: the slug and key are taken by
// the orphan. So this follows shared-surfaces convention B for the same reason
// PublishPageTx does — the atomicity is the contract.
type CreateTxStore interface {
	CreateSpaceTx(ctx context.Context, in CreateInput) (generated.Space, error)
}
