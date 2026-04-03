package projects

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Relation kind constants for cross-tool linking.
const (
	RelationBlocks      = "blocks"
	RelationIsBlockedBy = "is_blocked_by"
	RelationDuplicates  = "duplicates"
	RelationRelatesTo   = "relates_to"
	RelationWikiLink    = "wiki_link"
)

// ValidRelationKinds contains all allowed relation kind values.
var ValidRelationKinds = map[string]bool{
	RelationBlocks:      true,
	RelationIsBlockedBy: true,
	RelationDuplicates:  true,
	RelationRelatesTo:   true,
	RelationWikiLink:    true,
}

// Relation represents a link between two items (or an item and a wiki page).
type Relation struct {
	ID        uuid.UUID
	FromID    uuid.UUID
	ToID      uuid.UUID
	Kind      string
	CreatedBy uuid.UUID
	ToTitle   string
	ToStatus  string
	ToKind    string
}

// RelationRepository defines the data access contract for item relations.
type RelationRepository interface {
	// Create persists a new relation.
	Create(ctx context.Context, rel *Relation) error
	// ListByItem returns all relations originating from a given item,
	// with joined target item metadata.
	ListByItem(ctx context.Context, fromID uuid.UUID) ([]*Relation, error)
	// Delete removes a relation by ID.
	Delete(ctx context.Context, id uuid.UUID) error
}

// RelationService handles cross-tool item linking.
type RelationService struct {
	repo RelationRepository
}

// NewRelationService creates a RelationService backed by the given repository.
func NewRelationService(repo RelationRepository) *RelationService {
	return &RelationService{repo: repo}
}

// CreateRelation validates and persists a new cross-item link.
func (s *RelationService) CreateRelation(ctx context.Context, rel *Relation) (*Relation, error) {
	if err := validateRelation(rel); err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}

	rel.ID = uuid.New()
	if err := s.repo.Create(ctx, rel); err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}
	return rel, nil
}

// ListRelations returns all relations from a given item.
func (s *RelationService) ListRelations(ctx context.Context, fromID uuid.UUID) ([]*Relation, error) {
	rels, err := s.repo.ListByItem(ctx, fromID)
	if err != nil {
		return nil, fmt.Errorf("listing relations: %w", err)
	}
	return rels, nil
}

// DeleteRelation removes a relation by ID.
func (s *RelationService) DeleteRelation(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting relation: %w", err)
	}
	return nil
}

// GetBlockers returns all items that block the given item.
func (s *RelationService) GetBlockers(ctx context.Context, itemID uuid.UUID) ([]*Relation, error) {
	rels, err := s.repo.ListByItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("getting blockers: %w", err)
	}

	blockers := make([]*Relation, 0)
	for _, rel := range rels {
		if rel.Kind == RelationIsBlockedBy {
			blockers = append(blockers, rel)
		}
	}
	return blockers, nil
}

// GetBlocking returns all items that the given item blocks.
func (s *RelationService) GetBlocking(ctx context.Context, itemID uuid.UUID) ([]*Relation, error) {
	rels, err := s.repo.ListByItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("getting blocking items: %w", err)
	}

	blocking := make([]*Relation, 0)
	for _, rel := range rels {
		if rel.Kind == RelationBlocks {
			blocking = append(blocking, rel)
		}
	}
	return blocking, nil
}

// validateRelation checks that a relation has valid fields.
func validateRelation(rel *Relation) error {
	if !ValidRelationKinds[rel.Kind] {
		return ErrInvalidRelationKind
	}
	if rel.FromID == rel.ToID {
		return ErrSelfRelation
	}
	return nil
}
