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

// Relation direction, relative to the entity whose panel is being rendered.
const (
	// DirectionOutgoing means the viewed entity is the relation's from side.
	DirectionOutgoing = "outgoing"
	// DirectionIncoming means the viewed entity is the relation's to side.
	// Incoming rows exist because the read query unions the reverse direction;
	// no inverse row is ever stored.
	DirectionIncoming = "incoming"
)

// ValidEntityTypes are the entity kinds a relation may point at. It mirrors
// the entity_relations to_type/from_type CHECK constraint from migration 015 —
// a value outside this set reaches the database only to be rejected by the
// constraint, which surfaces to the caller as a 500 rather than a 400.
var ValidEntityTypes = map[string]bool{
	EntityTypeTicket:      true,
	EntityTypeProjectItem: true,
	EntityTypePage:        true,
}

// Entity type constants for polymorphic relations.
const (
	EntityTypeTicket      = "ticket"
	EntityTypeProjectItem = "project_item"
	EntityTypePage        = "page"
)

// Relation is one link touching an entity, as one particular VIEWER may see it.
//
// The shape is deliberately per-viewer rather than a stored global (D61). Only
// the FAR side is described — the near side is the entity whose panel is being
// rendered, and the caller already has it — and every far field is nullable
// because a relation may legitimately point somewhere this viewer cannot read.
//
// When FarReadable is false the far side is not merely hidden, it is absent:
// no id, no type, no title, no status. That is the D82 no-container-identity
// rule. The row is still returned so the panel can say "a link exists here",
// which is the whole point of surfacing the incoming direction — an item needs
// to know it is blocked even when it cannot see what is blocking it — but the
// placeholder carries nothing that identifies the far entity or its space.
//
// The predecessor of this struct carried FromID/ToID/FromType/ToType/CreatedBy
// unconditionally. Those are gone rather than gated: on an incoming relation
// from an unreadable space, a raw to_id is a valid entity UUID and CreatedBy
// names someone who acted inside that space, so returning them would have
// defeated the redaction that the query does.
type Relation struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Direction   string     `json:"direction"`
	FarReadable bool       `json:"far_readable"`
	FarID       *uuid.UUID `json:"far_id"`
	FarType     *string    `json:"far_type"`
	FarTitle    *string    `json:"far_title"`
	FarStatus   *string    `json:"far_status"`
}

// NewRelation is a request to create a link. It is a separate type from
// Relation because the two directions of the wire are genuinely different: a
// writer names both endpoints, a reader is told about one.
type NewRelation struct {
	FromID    uuid.UUID
	FromType  string
	ToID      uuid.UUID
	ToType    string
	Kind      string
	CreatedBy uuid.UUID
}

// RelationRepository defines the data access contract for entity relations.
//
// Every read carries readableSpaceIDs. That is not a convention — it is the
// reason the interface was reshaped. The previous contract offered ListByItem
// and ListByEntity, neither of which took the caller's access into account, so
// the only way to read relations was the unauthorised way. There is now no
// ungated read method to call by mistake.
type RelationRepository interface {
	// Create persists a new polymorphic relation under the given id.
	Create(ctx context.Context, id uuid.UUID, rel *NewRelation) error

	// TargetIsReadable reports whether the target entity both exists and sits
	// in a space the caller may read. It answers with a single bool precisely
	// so that "no such entity" and "exists but forbidden" cannot be told apart
	// by any caller — see EntityRelationTargetIsReadable in items.sql.
	TargetIsReadable(ctx context.Context, targetID uuid.UUID, targetType string, readableSpaceIDs []uuid.UUID) (bool, error)

	// ListForEntity returns every relation touching the entity, in both
	// directions, with far sides resolved only where readable.
	ListForEntity(ctx context.Context, entityID uuid.UUID, entityType string, readableSpaceIDs []uuid.UUID) ([]*Relation, error)

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

// CreateRelation validates the request, resolves the target against the
// caller's readable spaces, and persists the link.
//
// The resolution step is the fix for a write path that previously validated
// only that the kind was known and that from and to differed. Any UUID at all
// was accepted and stored — there are no foreign keys to catch it, migration
// 015 dropped them on purpose — so a caller could link their own item to an
// entity in an organization they have nothing to do with, and then read its
// title back out of the relations panel.
//
// An unresolvable target and a forbidden one both return ErrRelationTargetNotFound,
// which the API maps to 404. They are the same error value because the
// repository gives this function a single bool: there is no branch here that
// could drift into reporting them differently.
func (s *RelationService) CreateRelation(ctx context.Context, rel *NewRelation, readableSpaceIDs []uuid.UUID) (*Relation, error) {
	if err := validateNewRelation(rel); err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}

	readable, err := s.repo.TargetIsReadable(ctx, rel.ToID, rel.ToType, readableSpaceIDs)
	if err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}
	if !readable {
		return nil, fmt.Errorf("creating relation: %w", ErrRelationTargetNotFound)
	}

	id := uuid.New()
	if err := s.repo.Create(ctx, id, rel); err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}

	// The target was just proven readable, so naming it here discloses nothing
	// the caller did not already supply. Title and status are left unset rather
	// than fetched: resolving them would need a second query whose only purpose
	// is a response body the client replaces by refetching the list.
	toType := rel.ToType
	return &Relation{
		ID:          id,
		Kind:        rel.Kind,
		Direction:   DirectionOutgoing,
		FarReadable: true,
		FarID:       &rel.ToID,
		FarType:     &toType,
	}, nil
}

// ListRelations returns every relation touching the entity — both the ones it
// declares and the ones declared about it — with far sides resolved only where
// the caller may read them.
func (s *RelationService) ListRelations(ctx context.Context, entityID uuid.UUID, entityType string, readableSpaceIDs []uuid.UUID) ([]*Relation, error) {
	rels, err := s.repo.ListForEntity(ctx, entityID, entityType, readableSpaceIDs)
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

// GetBlockers returns the relations describing what blocks the given entity.
//
// Direction is load-bearing now that the reverse direction is unioned in. An
// entity is blocked either because it declared "is_blocked_by" itself, or
// because something else declared "blocks" against it — and before reciprocal
// visibility existed only the first of those could ever be seen. Filtering on
// kind alone would now report the second case backwards.
func (s *RelationService) GetBlockers(ctx context.Context, entityID uuid.UUID, entityType string, readableSpaceIDs []uuid.UUID) ([]*Relation, error) {
	rels, err := s.repo.ListForEntity(ctx, entityID, entityType, readableSpaceIDs)
	if err != nil {
		return nil, fmt.Errorf("getting blockers: %w", err)
	}
	return filterByDirectedKind(rels, RelationIsBlockedBy, RelationBlocks), nil
}

// GetBlocking returns the relations describing what the given entity blocks.
func (s *RelationService) GetBlocking(ctx context.Context, entityID uuid.UUID, entityType string, readableSpaceIDs []uuid.UUID) ([]*Relation, error) {
	rels, err := s.repo.ListForEntity(ctx, entityID, entityType, readableSpaceIDs)
	if err != nil {
		return nil, fmt.Errorf("getting blocking items: %w", err)
	}
	return filterByDirectedKind(rels, RelationBlocks, RelationIsBlockedBy), nil
}

// filterByDirectedKind keeps outgoing relations of outgoingKind and incoming
// relations of incomingKind — the two ways one directed statement can be
// written down depending on which end wrote it.
func filterByDirectedKind(rels []*Relation, outgoingKind, incomingKind string) []*Relation {
	out := make([]*Relation, 0)
	for _, rel := range rels {
		switch rel.Direction {
		case DirectionOutgoing:
			if rel.Kind == outgoingKind {
				out = append(out, rel)
			}
		case DirectionIncoming:
			if rel.Kind == incomingKind {
				out = append(out, rel)
			}
		}
	}
	return out
}

// validateNewRelation checks that a relation request has valid fields.
func validateNewRelation(rel *NewRelation) error {
	if !ValidRelationKinds[rel.Kind] {
		return ErrInvalidRelationKind
	}
	if !ValidEntityTypes[rel.ToType] {
		return ErrInvalidEntityType
	}
	if rel.FromID == rel.ToID {
		return ErrSelfRelation
	}
	return nil
}
