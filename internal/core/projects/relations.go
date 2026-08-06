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

// ValidEntityTypes are the entity kinds a relation may point at — and, since
// the entity-generic routes, originate from. It mirrors the entity_relations
// to_type/from_type CHECK constraint from migration 015 — a value outside this
// set reaches the database only to be rejected by the constraint, which
// surfaces to the caller as a 500 rather than a 400.
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
	// FarStatus is nil for an unreadable far side like every other far field —
	// and ALSO for a readable page, which has no status to report. A client
	// must key on FarReadable, never on a non-nil status.
	FarStatus *string `json:"far_status"`
	// FarSpaceID is what makes a resolved far side navigable: relations link
	// across spaces by design, so the near entity's space cannot be used to
	// build the far entity's URL. Populated only when FarReadable — it names a
	// space already in the viewer's own readable set, never a new fact.
	FarSpaceID *uuid.UUID `json:"far_space_id"`
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

	// DeleteInSpace removes a relation one of whose endpoints lives in
	// spaceID. There is deliberately no unscoped Delete: the reads here were
	// reshaped so that no ungated method survived to be called by mistake, and
	// the delete taking a bare id was the one that got left behind.
	DeleteInSpace(ctx context.Context, id, spaceID uuid.UUID) error
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
func (s *RelationService) CreateRelation(
	ctx context.Context, rel *NewRelation, spaceID uuid.UUID, readableSpaceIDs []uuid.UUID,
) (*Relation, error) {
	if err := validateNewRelation(rel); err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}

	// The NEAR side first. It is the {itemID} in the URL, and the middleware
	// authorised {spaceID} beside it without ever reconciling the two — so the
	// far side was resolved carefully while the entity the relation hangs off
	// was taken on trust. A contributor in one space could attach a relation to
	// an item in another, and it renders in that item's own panel through the
	// reciprocal-direction union.
	//
	// It is checked against the URL's space alone, not the caller's whole
	// readable set: this is the space they claimed to be acting in, and a wider
	// set would let read access somewhere else authorise a write here. The far
	// side keeps the readable set, because linking ACROSS spaces is the feature.
	near, err := s.repo.TargetIsReadable(ctx, rel.FromID, rel.FromType, []uuid.UUID{spaceID})
	if err != nil {
		return nil, fmt.Errorf("creating relation: %w", err)
	}
	if !near {
		return nil, fmt.Errorf("creating relation: %w", ErrNotFound)
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

// ListRelationsInSpace returns every relation touching the entity — both the
// ones it declares and the ones declared about it, far sides resolved only
// where the caller may read them — after reconciling the entity itself against
// the space the route named.
//
// The reconciliation is the same TargetIsReadable the write path runs on its
// near side, against the URL's space alone, for the same reason: the route
// proved the caller may read {spaceID} and proved nothing at all about
// {entityID}, so without it an entity anywhere in the installation answered
// through whichever space the caller could name. This replaced an unreconciled
// ListRelations rather than joining it, so no ungated variant survives to be
// called by mistake — the same reshaping the repository interface had.
//
// A miss is an empty list, not an error: a collection hanging off an entity
// outside this space answers exactly as one hanging off an entity that never
// existed, which is the no-oracle shape the scoping battery pins for every
// list route in the family.
func (s *RelationService) ListRelationsInSpace(ctx context.Context, entityID uuid.UUID, entityType string, spaceID uuid.UUID, readableSpaceIDs []uuid.UUID) ([]*Relation, error) {
	near, err := s.repo.TargetIsReadable(ctx, entityID, entityType, []uuid.UUID{spaceID})
	if err != nil {
		return nil, fmt.Errorf("listing relations: %w", err)
	}
	if !near {
		return []*Relation{}, nil
	}
	rels, err := s.repo.ListForEntity(ctx, entityID, entityType, readableSpaceIDs)
	if err != nil {
		return nil, fmt.Errorf("listing relations: %w", err)
	}
	return rels, nil
}

// DeleteRelation removes a relation the caller's space touches.
//
// spaceID is the space the route named and proved readable. Without it this
// took a bare relation id and deleted whatever it named — the same gap
// CreateRelation had on the write side, in a method that looked too small to
// have one. Neither endpoint is constrained by a foreign key (migration 015),
// so nothing below this refuses a relation belonging to another organisation.
//
// A relation outside the space is not an error: it is simply not deleted, and
// the caller is told the same thing they would be told about an id that never
// existed. See the query for why that shape rather than a 404.
func (s *RelationService) DeleteRelation(ctx context.Context, id, spaceID uuid.UUID) error {
	if err := s.repo.DeleteInSpace(ctx, id, spaceID); err != nil {
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
//
// FromType gets the same enumeration and the same sentinel as ToType. For as
// long as the only mount hardcoded EntityTypeProjectItem the check could not
// fire, which made it look unnecessary — but "the handler constrains it" is a
// property of one call site, not of this function, and the entity-generic
// routes are exactly the change that stops it being true. An unvalidated
// FromType reaches the CHECK constraint and comes back as an unmapped 500.
func validateNewRelation(rel *NewRelation) error {
	if !ValidRelationKinds[rel.Kind] {
		return ErrInvalidRelationKind
	}
	if !ValidEntityTypes[rel.FromType] {
		return ErrInvalidEntityType
	}
	if !ValidEntityTypes[rel.ToType] {
		return ErrInvalidEntityType
	}
	// A self-relation is the same (type, id) PAIR on both ends, not the same
	// id. Ids are unique only within an entity type's own table, so a ticket
	// and a page sharing a UUID are two different entities — comparing ids
	// alone would refuse that pair while asserting something it never checked.
	if rel.FromID == rel.ToID && rel.FromType == rel.ToType {
		return ErrSelfRelation
	}
	return nil
}
