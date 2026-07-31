package projects

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DefaultLabelColor is used when no color is specified.
const DefaultLabelColor = "#6b7280"

// Label represents an organization-level tag that can be applied to items.
type Label struct {
	ID    uuid.UUID `json:"id"`
	OrgID uuid.UUID `json:"org_id"`
	Name  string    `json:"name"`
	Color string    `json:"color"`
}

// LabelRepository defines the data access contract for labels.
type LabelRepository interface {
	// Create persists a new label. Returns ErrLabelDuplicate if the name exists in the org.
	Create(ctx context.Context, label *Label) error
	// ListByOrg returns all labels for an organization, ordered by name.
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*Label, error)
	// DeleteInOrg removes a label belonging to orgID. There is deliberately no
	// unscoped variant: the route this serves is open to any org member, so
	// the organisation is the only boundary the delete has.
	DeleteInOrg(ctx context.Context, id, orgID uuid.UUID) error
}

// LabelService handles label management for an organization.
type LabelService struct {
	repo LabelRepository
}

// NewLabelService creates a LabelService backed by the given repository.
func NewLabelService(repo LabelRepository) *LabelService {
	return &LabelService{repo: repo}
}

// CreateLabel validates and persists a new label.
func (s *LabelService) CreateLabel(ctx context.Context, label *Label) (*Label, error) {
	if label.Name == "" {
		return nil, fmt.Errorf("creating label: %w", ErrNameRequired)
	}
	if label.Color == "" {
		label.Color = DefaultLabelColor
	}

	label.ID = uuid.New()
	if err := s.repo.Create(ctx, label); err != nil {
		return nil, fmt.Errorf("creating label: %w", err)
	}
	return label, nil
}

// ListLabels returns all labels for an organization.
func (s *LabelService) ListLabels(ctx context.Context, orgID uuid.UUID) ([]*Label, error) {
	labels, err := s.repo.ListByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing labels: %w", err)
	}
	return labels, nil
}

// DeleteLabel removes a label belonging to orgID.
//
// orgID is the boundary. The route is deliberately open to any org member —
// labels are shared metadata, not an administered resource — so there is no
// space and no admin guard above this, and without the org predicate the id
// alone reached every label in the installation.
//
// A label in another organisation is not an error; it is simply not deleted,
// and the caller is told what they would be told about an id that never
// existed.
func (s *LabelService) DeleteLabel(ctx context.Context, id, orgID uuid.UUID) error {
	if err := s.repo.DeleteInOrg(ctx, id, orgID); err != nil {
		return fmt.Errorf("deleting label: %w", err)
	}
	return nil
}
