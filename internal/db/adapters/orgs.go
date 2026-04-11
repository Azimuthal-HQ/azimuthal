package adapters

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// OrgProvisionerAdapter implements auth.OrgProvisioner using sqlc-generated queries.
// It creates a personal organization and owner membership for newly registered users.
type OrgProvisionerAdapter struct {
	q *generated.Queries
}

// NewOrgProvisionerAdapter creates an OrgProvisionerAdapter.
func NewOrgProvisionerAdapter(q *generated.Queries) *OrgProvisionerAdapter {
	return &OrgProvisionerAdapter{q: q}
}

// ProvisionOrg creates a personal organization with a slug derived from the display name.
// If the slug already exists, it returns the existing org.
func (a *OrgProvisionerAdapter) ProvisionOrg(ctx context.Context, displayName string) (uuid.UUID, string, error) {
	slug := slugifyName(displayName)

	existing, err := a.q.GetOrganizationBySlug(ctx, slug)
	if err == nil {
		return existing.ID, slug, nil
	}

	orgID := uuid.New()
	desc := fmt.Sprintf("Organization for %s", displayName)
	org, err := a.q.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID:          orgID,
		Slug:        slug,
		Name:        displayName,
		Description: &desc,
		Plan:        "free",
	})
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("provisioning org: %w", err)
	}
	return org.ID, slug, nil
}

// CreateMembership adds the user as owner of the given org.
func (a *OrgProvisionerAdapter) CreateMembership(ctx context.Context, orgID, userID uuid.UUID) error {
	_, err := a.q.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:        uuid.New(),
		OrgID:     orgID,
		UserID:    userID,
		Role:      "owner",
		InvitedBy: pgtype.UUID{},
	})
	if err != nil {
		return fmt.Errorf("creating membership: %w", err)
	}
	return nil
}

// slugifyName converts a display name into a URL-safe slug.
func slugifyName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "default"
	}
	return s
}
