package adapters

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/spaces"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// SpaceCreateAdapter implements spaces.CreateTxStore: a space and the rows
// that must exist the moment it does, in one transaction.
//
// It follows the ContentTxAdapter shape — owns a pool, opens the transaction
// itself, does the whole unit of work through it.
type SpaceCreateAdapter struct {
	pool   *pgxpool.Pool
	q      *generated.Queries
	access *AccessAdapter
}

// NewSpaceCreateAdapter creates a SpaceCreateAdapter.
func NewSpaceCreateAdapter(pool *pgxpool.Pool) *SpaceCreateAdapter {
	return &SpaceCreateAdapter{
		pool:   pool,
		q:      generated.New(pool),
		access: NewAccessAdapter(pool),
	}
}

// CreateSpaceTx writes the space row, the creator's legacy space_members row
// and — for a non-org-admin creator — the creator's space_admin grant, as one
// transaction. Any failure rolls back all of them, so the caller's 500 leaves
// nothing behind and the slug and key stay free for the retry.
//
// The grant is not written by hand here. It goes through the real
// access.GrantService, bound to this transaction's queries, so the subject
// rules it enforces (spec §4) apply to the creator's grant exactly as they do
// to every other grant — one implementation, not two.
//
// A unique violation is returned unwrapped enough for the caller's
// errors.As(*pgconn.PgError) to still see the constraint name: the caller
// distinguishes a key collision (which it retries with a suffix) from a slug
// collision (which is the client's conflict), and it needs the name to do it.
//
// What is NOT in here, deliberately:
//
//   - The space.created audit row. With the writes atomic, the trail is back
//     on the ordinary convention A footing — the handler writes it after the
//     mutation succeeds. Convention B exists for trails that are part of an
//     atomicity contract; this one is a record of a completed creation, and
//     there is now no state in which the space half-exists for it to describe.
//   - The default workflow assignment, which is explicitly best-effort and
//     non-fatal: a space with no workflow assigned is usable and fixable, so
//     failing the creation over it would be the worse outcome.
func (a *SpaceCreateAdapter) CreateSpaceTx(ctx context.Context, in spaces.CreateInput) (generated.Space, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return generated.Space{}, fmt.Errorf("create space: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	space, err := qtx.CreateSpace(ctx, in.Space)
	if err != nil {
		// Wrapped with %w, which matters: the caller reads the constraint name
		// off this with errors.As to tell a key collision (retried with a
		// suffix) from a slug collision (the client's conflict). It never shows
		// the message — both arms answer with their own text.
		return generated.Space{}, fmt.Errorf("create space: %w", err)
	}

	if _, err := qtx.AddSpaceMember(ctx, generated.AddSpaceMemberParams{
		ID:      in.MemberRowID,
		SpaceID: space.ID,
		UserID:  in.CreatorID,
		Role:    "admin",
	}); err != nil {
		return generated.Space{}, fmt.Errorf("create space: adding creator as member: %w", err)
	}

	if in.CreatorNeedsGrant {
		grants := access.NewGrantService(a.access.withTx(tx))
		if _, err := grants.Create(ctx, space.OrgID, space.ID,
			access.SubjectUser, in.CreatorID, access.RoleSpaceAdmin, in.CreatorID); err != nil {
			return generated.Space{}, fmt.Errorf("create space: granting creator access: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return generated.Space{}, fmt.Errorf("create space: commit: %w", err)
	}
	return space, nil
}

// withTx returns an AccessAdapter whose reads and writes run through tx. It
// exists so a service that owns a rule — access.GrantService and the grant
// subject rules — can be reused inside another package's transaction instead
// of having that rule reimplemented against qtx.
func (a *AccessAdapter) withTx(tx pgx.Tx) *AccessAdapter {
	return &AccessAdapter{pool: a.pool, q: a.q.WithTx(tx)}
}
