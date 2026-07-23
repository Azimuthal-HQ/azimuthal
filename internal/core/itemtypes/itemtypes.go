// Package itemtypes manages org-scoped Vector item types (task, story, bug,
// epic, and any org-defined additions). The type's slug is its immutable
// identity, stored on project_items.kind; the display name is mutable. Per
// ADR-0003 the type stays a discriminator on project_items, not a joined entity.
package itemtypes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors for the itemtypes package.
var (
	// ErrNameRequired is returned when creating or renaming a type with an empty name.
	ErrNameRequired = errors.New("name is required")
	// ErrInvalidName is returned when a name has no alphanumeric content to slugify.
	ErrInvalidName = errors.New("name must contain at least one letter or digit")
	// ErrNotFound is returned when a type cannot be located within the org.
	ErrNotFound = errors.New("item type not found")
	// ErrDuplicate is returned when a type with the same slug already exists in the org.
	ErrDuplicate = errors.New("an item type with this name already exists")
	// ErrReferenced is returned when hard-deleting a type that items still use.
	ErrReferenced = errors.New("item type is in use; archive it instead of deleting")
)

// ItemType is an org-scoped item type definition.
type ItemType struct {
	ID         uuid.UUID  `json:"id"`
	OrgID      uuid.UUID  `json:"org_id"`
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Position   int        `json:"position"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Repository is the data-access contract for item types.
type Repository interface {
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*ItemType, error)
	ListActiveByOrg(ctx context.Context, orgID uuid.UUID) ([]*ItemType, error)
	GetByID(ctx context.Context, id uuid.UUID) (*ItemType, error)
	GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (*ItemType, error)
	Create(ctx context.Context, t *ItemType) error
	Rename(ctx context.Context, id uuid.UUID, name string) (*ItemType, error)
	SetArchived(ctx context.Context, id uuid.UUID, archived bool) (*ItemType, error)
	Delete(ctx context.Context, id uuid.UUID) error
	// CountItemsOfType counts items (soft-deleted included) referencing the slug.
	CountItemsOfType(ctx context.Context, orgID uuid.UUID, slug string) (int, error)
	NextPosition(ctx context.Context, orgID uuid.UUID) (int, error)
	// SeedDefaults idempotently seeds the default set for an org.
	SeedDefaults(ctx context.Context, orgID uuid.UUID) error
}

// Service holds item-type business logic.
type Service struct {
	repo Repository
}

// NewService creates an item-types Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns all types for an org (including archived), ordered.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]*ItemType, error) {
	out, err := s.repo.ListByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing item types: %w", err)
	}
	return out, nil
}

// Create defines a new type. The slug is derived from the name once and is
// immutable; the name may later be changed without rewriting item rows.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, name string) (*ItemType, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	slug := Slugify(name)
	if slug == "" {
		return nil, ErrInvalidName
	}

	if _, err := s.repo.GetByOrgSlug(ctx, orgID, slug); err == nil {
		return nil, ErrDuplicate
	} else if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("checking item type slug: %w", err)
	}

	pos, err := s.repo.NextPosition(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("computing item type position: %w", err)
	}

	t := &ItemType{ID: uuid.New(), OrgID: orgID, Slug: slug, Name: name, Position: pos}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("creating item type: %w", err)
	}
	return t, nil
}

// Rename changes a type's display name only; the slug is untouched.
func (s *Service) Rename(ctx context.Context, orgID, id uuid.UUID, name string) (*ItemType, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	if _, err := s.getOwned(ctx, orgID, id); err != nil {
		return nil, err
	}
	t, err := s.repo.Rename(ctx, id, name)
	if err != nil {
		return nil, fmt.Errorf("renaming item type: %w", err)
	}
	return t, nil
}

// SetArchived archives or unarchives a type. Archiving keeps existing items'
// references valid but removes the type from pickers.
func (s *Service) SetArchived(ctx context.Context, orgID, id uuid.UUID, archived bool) (*ItemType, error) {
	if _, err := s.getOwned(ctx, orgID, id); err != nil {
		return nil, err
	}
	t, err := s.repo.SetArchived(ctx, id, archived)
	if err != nil {
		return nil, fmt.Errorf("archiving item type: %w", err)
	}
	return t, nil
}

// Delete hard-deletes a type, but only if no item references it. A referenced
// type must be archived, never silently deletable (ErrReferenced).
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	t, err := s.getOwned(ctx, orgID, id)
	if err != nil {
		return err
	}
	n, err := s.repo.CountItemsOfType(ctx, orgID, t.Slug)
	if err != nil {
		return fmt.Errorf("counting items of type: %w", err)
	}
	if n > 0 {
		return ErrReferenced
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting item type: %w", err)
	}
	return nil
}

// IsActiveType reports whether slug is a defined, non-archived type in the org.
// Item creation validates the chosen type against this.
func (s *Service) IsActiveType(ctx context.Context, orgID uuid.UUID, slug string) (bool, error) {
	t, err := s.repo.GetByOrgSlug(ctx, orgID, slug)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking item type: %w", err)
	}
	return t.ArchivedAt == nil, nil
}

// getOwned fetches a type and asserts it belongs to the org, mapping a
// cross-org id to ErrNotFound so the surface never leaks another org's types.
func (s *Service) getOwned(ctx context.Context, orgID, id uuid.UUID) (*ItemType, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting item type: %w", err)
	}
	if t.OrgID != orgID {
		return nil, ErrNotFound
	}
	return t, nil
}

// Slugify converts a display name into a stable slug matching the DB CHECK
// (^[a-z0-9][a-z0-9_]*$): lowercase, non-alphanumerics collapsed to single
// underscores, leading/trailing underscores trimmed.
func Slugify(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if b.Len() > 0 && !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
