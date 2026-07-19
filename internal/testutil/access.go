package testutil

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
)

// staticAccessStore is an in-memory access.Store for DB-less tests: every
// caller is an org admin and the org contains exactly the given spaces.
type staticAccessStore struct{ spaces []uuid.UUID }

func (s staticAccessStore) OrgRole(context.Context, uuid.UUID, uuid.UUID) (access.OrgRole, error) {
	return access.OrgRole{Name: "owner", Admin: true}, nil
}

func (s staticAccessStore) ResolveAccessRows(context.Context, uuid.UUID, uuid.UUID) ([]access.AccessRow, error) {
	return nil, nil
}

func (s staticAccessStore) ListSpaceIDsByOrg(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return s.spaces, nil
}

// OrgAdminResolution returns the access.Resolution the ResolveAccess
// middleware would compute for an org admin whose org contains exactly the
// given spaces. DB-less handler tests stamp it on the request context so the
// in-handler capability checks — which fail closed without a resolution —
// see the production admin bypass.
func OrgAdminResolution(t *testing.T, spaceIDs ...uuid.UUID) *access.Resolution {
	t.Helper()
	res, err := access.NewResolver(staticAccessStore{spaces: spaceIDs}).
		Resolve(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("building org-admin test resolution: %v", err)
	}
	return res
}
