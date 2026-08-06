// Package tags is the entity tag model: org-scoped tags, and the association
// between an entity — a page, a ticket, or a project item — and the tags it
// carries (migrations 040, 055).
//
// # Two ways an entity acquires a tag, and only one of them is authoritative
//
// A tag on an entity is metadata. It is set explicitly, it is stored in
// `entity_tags`, and setting the list replaces it — removing a tag from the
// list removes it from the entity.
//
// An inline `#tag` token is a Codex shortcut. It lives in a page's document
// body, and publishing a page whose body contains one ensures the page carries
// that tag. It is deliberately one-directional: deleting the last `#foo` from
// a page's body does NOT remove the page-level tag. The alternative — inline
// tokens owning the tag set — would mean an author who tags a page explicitly
// and then rewords a sentence silently loses the tag, and there would be no
// way to tag a page without writing the tag into its prose. Tickets and
// project items have plain-text descriptions, no document model, and therefore
// no inline form at all: their tags come only from the explicit list.
//
// So: [Service.EnsureOnEntity] adds and never removes; [Service.SetEntityTags]
// replaces. The publish path uses the first semantic; the tag editors call the
// second.
//
// # Identity
//
// The slug is the identity and it is immutable (migration 040 says why). The
// association stores the tag's id, so a rename — when there is a surface for
// one — rewrites nothing at all.
package tags

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
)

// Errors returned by this package.
var (
	// ErrNotFound is returned when no tag with the given slug exists in the org.
	ErrNotFound = errors.New("tag not found")

	// ErrEntityNotFound is returned when the entity a tag write named is not in
	// the space the caller was authorised against — which is also what a caller
	// is told about an entity that does not exist at all. One error for both,
	// deliberately: two distinguishable answers would be an existence oracle.
	ErrEntityNotFound = errors.New("entity not found")

	// ErrInvalidName is returned when a label slugifies to nothing — "!!!" or a
	// string of punctuation. The DB CHECK would reject it too, but with an
	// opaque constraint violation instead of a sentence.
	ErrInvalidName = errors.New("tag name must contain at least one letter or digit")

	// ErrTooManyTags is returned when an entity is given more tags than
	// [MaxTagsPerEntity].
	ErrTooManyTags = errors.New("too many tags on one entity")
)

// EntityType names a kind of entity that can carry tags. The values are the
// same strings migration 055's CHECK constraint permits — the vocabulary
// migration 015 established for comments and entity_relations.
type EntityType string

// The three taggable entity kinds.
const (
	EntityPage        EntityType = "page"
	EntityTicket      EntityType = "ticket"
	EntityProjectItem EntityType = "project_item"
)

// Valid reports whether this is one of the three known kinds. Everything in
// this package fails closed on an unknown kind — an entity the model cannot
// resolve to a space is an entity nothing may be written for.
func (e EntityType) Valid() bool {
	switch e {
	case EntityPage, EntityTicket, EntityProjectItem:
		return true
	default:
		return false
	}
}

// EntityRef names one taggable entity together with the space the caller was
// authorised against. The space is not a hint: every association read and
// write reconciles the entity against it in SQL, so a ref naming a space the
// entity is not in reaches nothing.
type EntityRef struct {
	Type    EntityType
	ID      uuid.UUID
	SpaceID uuid.UUID
}

// MaxTagsPerEntity bounds an explicit tag set. An entity with more than this
// many tags is not being categorised, and the cap keeps one request from
// minting an unbounded number of org-scoped tag rows.
const MaxTagsPerEntity = 50

// maxLabelLength bounds one tag label before slugification. Slugify collapses
// runs of punctuation, so a very long label is not a very long slug — but it is
// still stored verbatim as the display name.
const maxLabelLength = 64

// Tag is an org-scoped tag.
type Tag struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// MaxTaggedEntities bounds one page of the tag browse. The query asks for one
// more than this — across the whole three-kind union, not per kind — so the
// service can report that there WERE more rather than quietly returning a
// short answer.
const MaxTaggedEntities = 200

// TagEntities is the tag browse's answer: the tag, the readable entities
// carrying it, and whether there were more than fit.
type TagEntities struct {
	Tag      Tag            `json:"tag"`
	Entities []TaggedEntity `json:"entities"`
	// Truncated says the answer was cut short. A caller that ignores it shows a
	// list that looks complete and is not.
	Truncated bool `json:"truncated"`
}

// TaggedEntity is one row of the tag browse: an entity carrying the tag, with
// enough space context to be navigable and to be told apart from a same-titled
// entity in another space.
type TaggedEntity struct {
	EntityType EntityType `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	SpaceID    uuid.UUID  `json:"space_id"`
	SpaceName  string     `json:"space_name"`
	SpaceKey   string     `json:"space_key"`
	Title      string     `json:"title"`
	// Ref is the kind's one human-readable reference: a page's path, a
	// ticket's composed reference ("BEA-42"), a project item's item_key
	// ("VEC-14"). Composed by the adapter at each kind's single existing
	// composition site — never re-derived past that point.
	Ref       string    `json:"ref"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Repository is the persistence this package needs.
type Repository interface {
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]Tag, error)
	GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (Tag, error)
	// Upsert creates the tag or returns the existing one. The first spelling of
	// a slug wins; a later name for the same slug does not overwrite it.
	Upsert(ctx context.Context, orgID uuid.UUID, slug, name string) (Tag, error)
	// ForEntity reads an entity's tags, reconciled against the space the route
	// named. A tag set says what an entity is about, so reading one across a
	// space boundary describes the subject matter of a thing the caller cannot
	// open — and the route proved {spaceID} readable while proving nothing
	// about the entity id.
	ForEntity(ctx context.Context, ref EntityRef) ([]Tag, error)
	// EntityInSpace reports whether the entity exists, alive, in the space the
	// caller was authorised against. One bool for "no such entity" and "an
	// entity elsewhere" — the write path's no-oracle probe.
	EntityInSpace(ctx context.Context, ref EntityRef) (bool, error)
	// ReplaceEntityTags makes the entity's associations exactly tagIDs.
	ReplaceEntityTags(ctx context.Context, ref EntityRef, tagIDs []uuid.UUID) error
	// AddEntityTags adds associations without removing any.
	AddEntityTags(ctx context.Context, ref EntityRef, tagIDs []uuid.UUID) error
	// EntitiesWithTag lists the entities of every kind carrying the tag,
	// filtered to the caller's readable set, newest first, cut at one past
	// [MaxTaggedEntities] across the union.
	EntitiesWithTag(ctx context.Context, tagID uuid.UUID, readableSpaceIDs []uuid.UUID) ([]TaggedEntity, error)
}

// Service owns the tag rules.
type Service struct {
	repo Repository
}

// NewService creates a Service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Slugify derives a tag's immutable slug from a label.
//
// It is [itemtypes.Slugify], not a second implementation, and the reuse is
// deliberate rather than incidental: that is the repository's one slug helper,
// it is the one migration 040's CHECK constraint is written against, and
// `docs/design/shared-surfaces.md` makes a second implementation of an existing
// thing a defect. The visible consequence is that tag slugs are
// underscore-separated (`#design_docs`) like item-type and custom-field slugs,
// rather than hyphenated like space and team slugs.
//
// Promoting the helper into a neutral package would be tidier than importing a
// Vector domain package from this one; it is not done here because that
// package is shared with concurrent work.
func Slugify(label string) string { return itemtypes.Slugify(label) }

// List returns every tag in the org, ordered by display name.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Tag, error) {
	out, err := s.repo.ListByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	return out, nil
}

// ForEntity returns the tags an entity in the given space carries.
//
// The space is not a filter on the answer, it is the authorisation for asking:
// entity_tags is keyed on the entity alone, so the query reconciles the entity
// id the caller named against the space their request was authorised for. An
// entity elsewhere carries no tags here, which is what an entity that does not
// exist carries.
func (s *Service) ForEntity(ctx context.Context, ref EntityRef) ([]Tag, error) {
	if !ref.Type.Valid() {
		return nil, fmt.Errorf("%w: unknown entity type %q", ErrEntityNotFound, ref.Type)
	}
	out, err := s.repo.ForEntity(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("listing an entity's tags: %w", err)
	}
	return out, nil
}

// Resolve turns labels into tag rows, creating any that do not exist yet.
//
// Tags are created by use — there is no administration surface that would
// create one first — so this is the only constructor there is. Labels that
// slugify to the same thing collapse to one tag, which is the point: "Design
// Docs", "design docs" and "design_docs" are one tag, not three.
//
// A label that slugifies to nothing is an error rather than a silent skip. It
// is the only input a person can type that cannot become a tag, and telling
// them beats dropping it.
func (s *Service) Resolve(ctx context.Context, orgID uuid.UUID, labels []string) ([]Tag, error) {
	wanted, invalid, hadInvalid := distinctTags(labels)
	if hadInvalid {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, invalid)
	}
	// The ceiling applies to the DEDUPED set rather than to what was typed.
	// Refusing fifty-one labels that name three tags would be refusing
	// arithmetic nobody did, and the ceiling exists to bound how many tag ROWS
	// one request can mint — which is what deduping decides.
	if len(wanted) > MaxTagsPerEntity {
		return nil, fmt.Errorf("%w: %d given, %d allowed", ErrTooManyTags, len(wanted), MaxTagsPerEntity)
	}
	return s.upsertAll(ctx, orgID, wanted)
}

// distinctTags normalises labels and drops duplicates by SLUG rather than by
// text, reporting the first label that cannot become a tag.
//
// Deduping on the slug is the load-bearing part. "Design Docs", "design docs"
// and "design_docs" are one tag; a caller that deduped on the text would call
// Upsert three times for it and would count it three times against the
// ceiling. The first spelling in order is the one carried forward, matching
// what the table itself does on conflict.
func distinctTags(labels []string) (wanted []SlugLabel, invalid string, hadInvalid bool) {
	out := make([]SlugLabel, 0, len(labels))
	seen := make(map[string]bool, len(labels))
	for _, raw := range labels {
		label := normaliseLabel(raw)
		slug := Slugify(label)
		if slug == "" {
			return nil, label, true
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, SlugLabel{Slug: slug, Label: label})
	}
	return out, "", false
}

func (s *Service) upsertAll(ctx context.Context, orgID uuid.UUID, wanted []SlugLabel) ([]Tag, error) {
	out := make([]Tag, 0, len(wanted))
	for _, w := range wanted {
		tag, err := s.repo.Upsert(ctx, orgID, w.Slug, w.Label)
		if err != nil {
			return nil, fmt.Errorf("creating tag %q: %w", w.Slug, err)
		}
		out = append(out, tag)
	}
	return out, nil
}

// requireEntity answers the write path's reachability question before anything
// is resolved or written. It runs FIRST so an unreachable target cannot mint
// org-scoped tag rows as a side effect of a request that then fails — and the
// association statements carry the same predicate themselves, so a caller that
// somehow skipped this probe still cannot write across a space boundary.
func (s *Service) requireEntity(ctx context.Context, ref EntityRef) error {
	if !ref.Type.Valid() {
		return fmt.Errorf("%w: unknown entity type %q", ErrEntityNotFound, ref.Type)
	}
	ok, err := s.repo.EntityInSpace(ctx, ref)
	if err != nil {
		return fmt.Errorf("resolving a tag target: %w", err)
	}
	if !ok {
		return ErrEntityNotFound
	}
	return nil
}

// SetEntityTags makes the entity's tags exactly these labels. This is the
// authoritative path: a tag left out of the list is removed from the entity.
func (s *Service) SetEntityTags(ctx context.Context, orgID uuid.UUID, ref EntityRef, labels []string) ([]Tag, error) {
	if err := s.requireEntity(ctx, ref); err != nil {
		return nil, err
	}
	resolved, err := s.Resolve(ctx, orgID, labels)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceEntityTags(ctx, ref, idsOf(resolved)); err != nil {
		return nil, fmt.Errorf("setting an entity's tags: %w", err)
	}
	return resolved, nil
}

// EnsureOnEntity adds these labels to the entity's tags without removing any.
//
// This is the publish-path semantic for the `#tag` tokens in a page's document
// body. It never removes, because the entity-level set is the authority and an
// inline token is a shortcut — see the package comment.
func (s *Service) EnsureOnEntity(ctx context.Context, orgID uuid.UUID, ref EntityRef, labels []string) ([]Tag, error) {
	// Nothing to ensure is completely inert — not even the reachability probe
	// runs. Publishing an untagged page is the overwhelmingly common case, and
	// it must cost nothing.
	if len(labels) == 0 {
		return []Tag{}, nil
	}
	if err := s.requireEntity(ctx, ref); err != nil {
		return nil, err
	}
	resolved, err := s.Resolve(ctx, orgID, labels)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return resolved, nil
	}
	if err := s.repo.AddEntityTags(ctx, ref, idsOf(resolved)); err != nil {
		return nil, fmt.Errorf("adding an entity's tags: %w", err)
	}
	return resolved, nil
}

// EntitiesWithSlug returns the readable entities of every kind carrying a tag.
//
// readableSpaceIDs is the caller's resolved readable set (ADR-0010: every
// cross-space endpoint filters against it). An empty set matches no entities,
// which is the fail-closed answer — never "no filter".
func (s *Service) EntitiesWithSlug(ctx context.Context, orgID uuid.UUID, slug string, readableSpaceIDs []uuid.UUID) (TagEntities, error) {
	tag, err := s.repo.GetByOrgSlug(ctx, orgID, slug)
	if err != nil {
		// Wrapped, but with the sentinel intact: the handler dispatches on
		// errors.Is(err, ErrNotFound) to answer 404, and a laundered error
		// would turn an unknown tag slug into a 500.
		return TagEntities{}, fmt.Errorf("getting tag %q: %w", slug, err)
	}
	entities, err := s.repo.EntitiesWithTag(ctx, tag.ID, readableSpaceIDs)
	if err != nil {
		return TagEntities{}, fmt.Errorf("listing entities with a tag: %w", err)
	}

	// The query asks for one more row than a page holds — across the union of
	// all three kinds, never per kind. Its presence is the only way the caller
	// can tell a full answer from a cut-off one — a bare LIMIT returns a
	// truncated list that looks exactly like a complete one, and because the
	// order is most-recent-first the entities that disappear are the oldest,
	// so the reader is shown the wrong nothing and told nothing.
	truncated := len(entities) > MaxTaggedEntities
	if truncated {
		entities = entities[:MaxTaggedEntities]
	}
	return TagEntities{Tag: tag, Entities: entities, Truncated: truncated}, nil
}

// ResolveForPublish turns a document's inline tag labels into tag rows, without
// touching any association.
//
// It is split from [Service.EnsureOnEntity] because publish needs the tag rows
// BEFORE its transaction and the associations INSIDE it. Creating a tag row for
// a publish that then fails leaves an unused tag, which is indistinguishable
// from a tag somebody made and stopped using; failing to write an association
// for a publish that succeeded leaves a page missing a chip it should have. The
// harmless failure goes outside the transaction.
func (s *Service) ResolveForPublish(ctx context.Context, orgID uuid.UUID, labels []string) ([]uuid.UUID, error) {
	// Labels that cannot become a tag are dropped rather than refused — a
	// publish is not the moment to reject a whole page over a stray `#!!!` in
	// its body — and the lenient normalisation is the shared helper, so this
	// path and the workflow applier cannot drift apart. The dedupe runs before
	// the ceiling there, and the ordering matters: counting raw labels would
	// drop everything past the fiftieth spelling even when they named far
	// fewer tags, silently, since a publish reports no truncation. The
	// document walker's own ceiling (doc.maxInlineTagsPerDocument) bounds the
	// input to the same number, so after deduping the cap cannot bite at all;
	// it stays as the belt to that walker's braces, because the two live in
	// different packages and only one of them is the one a hostile paste
	// reaches first.
	resolved, err := s.upsertAll(ctx, orgID, LenientLabelSlugs(labels))
	if err != nil {
		return nil, err
	}
	return idsOf(resolved), nil
}

// SlugLabel pairs a tag's identity with the display spelling that produced it.
type SlugLabel struct {
	Slug  string
	Label string
}

// LenientLabelSlugs normalises a label list the way the lenient consumers do —
// the workflow set_field:tags applier today, the same shape ResolveForPublish
// uses inside its transaction split. Labels are slugified with the one helper,
// deduplicated by slug with the first spelling kept, capped at
// [MaxTagsPerEntity], and a label that cannot become a tag is DROPPED rather
// than refused: the values reaching this path are stored configuration or
// document content, and the person triggering them is not the person who can
// fix them. The strict path for people looking at the field they typed into is
// [Service.Resolve].
func LenientLabelSlugs(labels []string) []SlugLabel {
	usable := make([]string, 0, len(labels))
	for _, label := range labels {
		if Slugify(normaliseLabel(label)) != "" {
			usable = append(usable, label)
		}
	}
	wanted, _, hadInvalid := distinctTags(usable)
	if hadInvalid {
		// Unreachable: every label was slug-checked above.
		return nil
	}
	if len(wanted) > MaxTagsPerEntity {
		wanted = wanted[:MaxTagsPerEntity]
	}
	return wanted
}

// normaliseLabel trims a label and bounds its stored length. A tag's display
// name is whatever a person typed; it is not a place for a paragraph.
func normaliseLabel(label string) string {
	label = strings.TrimSpace(label)
	// Trim a leading hash: an author typing "#design" into the tags field means
	// the tag "design", and storing the hash would make the chip read "##design".
	label = strings.TrimPrefix(label, "#")
	label = strings.TrimSpace(label)
	if len(label) > maxLabelLength {
		label = strings.TrimSpace(truncateRunes(label, maxLabelLength))
	}
	return label
}

// truncateRunes cuts at a rune boundary, so a multi-byte label stays valid
// UTF-8 rather than ending in half a character.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

func idsOf(list []Tag) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(list))
	for _, t := range list {
		out = append(out, t.ID)
	}
	return out
}

// SlugsOf reduces a tag list to its slugs — the identity form the workflow
// guard snapshot carries.
func SlugsOf(list []Tag) []string {
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Slug)
	}
	return out
}
