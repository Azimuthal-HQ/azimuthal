// Package tags is the Codex tag model: org-scoped tags, and the association
// between a page and the tags it carries (migration 040).
//
// # Two ways a page acquires a tag, and only one of them is authoritative
//
// A page-level tag is metadata. It is set explicitly, it is stored in
// `page_tags`, and setting the list replaces it — removing a tag from the list
// removes it from the page.
//
// An inline `#tag` token is a shortcut. It lives in the document body, and
// publishing a page whose body contains one ensures the page carries that tag.
// It is deliberately one-directional: deleting the last `#foo` from a page's
// body does NOT remove the page-level tag. The alternative — inline tokens
// owning the page's tag set — would mean an author who tags a page explicitly
// and then rewords a sentence silently loses the tag, and there would be no way
// to tag a page without writing the tag into its prose.
//
// So: [Service.EnsureOnPage] adds and never removes; [Service.SetPageTags]
// replaces. The publish path calls the first; the tags editor calls the second.
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

	// ErrInvalidName is returned when a label slugifies to nothing — "!!!" or a
	// string of punctuation. The DB CHECK would reject it too, but with an
	// opaque constraint violation instead of a sentence.
	ErrInvalidName = errors.New("tag name must contain at least one letter or digit")

	// ErrTooManyTags is returned when a page is given more tags than
	// [MaxTagsPerPage].
	ErrTooManyTags = errors.New("too many tags on one page")
)

// MaxTagsPerPage bounds an explicit page-level tag set. A page with more than
// this many tags is not being categorised, and the cap keeps one request from
// minting an unbounded number of org-scoped tag rows.
const MaxTagsPerPage = 50

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

// TaggedPage is one row of the tag browse: a page carrying the tag, with enough
// space context to be navigable and to be told apart from a same-titled page in
// another space.
type TaggedPage struct {
	PageID    uuid.UUID `json:"page_id"`
	SpaceID   uuid.UUID `json:"space_id"`
	SpaceName string    `json:"space_name"`
	SpaceKey  string    `json:"space_key"`
	Title     string    `json:"title"`
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Repository is the persistence this package needs.
type Repository interface {
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]Tag, error)
	GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (Tag, error)
	// Upsert creates the tag or returns the existing one. The first spelling of
	// a slug wins; a later name for the same slug does not overwrite it.
	Upsert(ctx context.Context, orgID uuid.UUID, slug, name string) (Tag, error)
	ForPage(ctx context.Context, pageID uuid.UUID) ([]Tag, error)
	// ReplacePageTags makes the page's associations exactly tagIDs.
	ReplacePageTags(ctx context.Context, pageID uuid.UUID, tagIDs []uuid.UUID) error
	// AddPageTags adds associations without removing any.
	AddPageTags(ctx context.Context, pageID uuid.UUID, tagIDs []uuid.UUID) error
	PagesWithTag(ctx context.Context, tagID uuid.UUID, readableSpaceIDs []uuid.UUID) ([]TaggedPage, error)
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
// thing a defect. The visible consequence is that Codex tag slugs are
// underscore-separated (`#design_docs`) like item-type and custom-field slugs,
// rather than hyphenated like space and team slugs.
//
// Promoting the helper into a neutral package would be tidier than importing a
// Vector domain package from a Codex one; it is not done here because that
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

// ForPage returns the tags a page carries.
func (s *Service) ForPage(ctx context.Context, pageID uuid.UUID) ([]Tag, error) {
	out, err := s.repo.ForPage(ctx, pageID)
	if err != nil {
		return nil, fmt.Errorf("listing a page's tags: %w", err)
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
	if len(labels) > MaxTagsPerPage {
		return nil, fmt.Errorf("%w: %d given, %d allowed", ErrTooManyTags, len(labels), MaxTagsPerPage)
	}
	out := make([]Tag, 0, len(labels))
	seen := make(map[string]bool, len(labels))
	for _, label := range labels {
		label = normaliseLabel(label)
		slug := Slugify(label)
		if slug == "" {
			return nil, fmt.Errorf("%w: %q", ErrInvalidName, label)
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		tag, err := s.repo.Upsert(ctx, orgID, slug, label)
		if err != nil {
			return nil, fmt.Errorf("creating tag %q: %w", slug, err)
		}
		out = append(out, tag)
	}
	return out, nil
}

// SetPageTags makes the page's tags exactly these labels. This is the
// authoritative path: a tag left out of the list is removed from the page.
func (s *Service) SetPageTags(ctx context.Context, orgID, pageID uuid.UUID, labels []string) ([]Tag, error) {
	resolved, err := s.Resolve(ctx, orgID, labels)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplacePageTags(ctx, pageID, idsOf(resolved)); err != nil {
		return nil, fmt.Errorf("setting a page's tags: %w", err)
	}
	return resolved, nil
}

// EnsureOnPage adds these labels to the page's tags without removing any.
//
// This is what publish calls for the `#tag` tokens in a document body. It never
// removes, because the page-level set is the authority and an inline token is a
// shortcut — see the package comment.
func (s *Service) EnsureOnPage(ctx context.Context, orgID, pageID uuid.UUID, labels []string) ([]Tag, error) {
	resolved, err := s.Resolve(ctx, orgID, labels)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return resolved, nil
	}
	if err := s.repo.AddPageTags(ctx, pageID, idsOf(resolved)); err != nil {
		return nil, fmt.Errorf("adding a page's inline tags: %w", err)
	}
	return resolved, nil
}

// PagesWithSlug returns the readable pages carrying a tag.
//
// readableSpaceIDs is the caller's resolved readable set (ADR-0010: every
// cross-space endpoint filters against it). An empty set matches no pages,
// which is the fail-closed answer — never "no filter".
func (s *Service) PagesWithSlug(ctx context.Context, orgID uuid.UUID, slug string, readableSpaceIDs []uuid.UUID) (Tag, []TaggedPage, error) {
	tag, err := s.repo.GetByOrgSlug(ctx, orgID, slug)
	if err != nil {
		return Tag{}, nil, err
	}
	pages, err := s.repo.PagesWithTag(ctx, tag.ID, readableSpaceIDs)
	if err != nil {
		return Tag{}, nil, fmt.Errorf("listing pages with a tag: %w", err)
	}
	return tag, pages, nil
}

// ResolveForPublish turns a document's inline tag labels into tag rows, without
// touching any association.
//
// It is split from [Service.EnsureOnPage] because publish needs the tag rows
// BEFORE its transaction and the associations INSIDE it. Creating a tag row for
// a publish that then fails leaves an unused tag, which is indistinguishable
// from a tag somebody made and stopped using; failing to write an association
// for a publish that succeeded leaves a page missing a chip it should have. The
// harmless failure goes outside the transaction.
func (s *Service) ResolveForPublish(ctx context.Context, orgID uuid.UUID, labels []string) ([]uuid.UUID, error) {
	// Labels that cannot become a tag are dropped here rather than refused. A
	// publish is not the moment to reject a whole page over a `#!!!` in its
	// body, and Resolve's error exists for the explicit editor path where a
	// person is looking at the field they typed into.
	usable := make([]string, 0, len(labels))
	for _, label := range labels {
		if Slugify(normaliseLabel(label)) != "" {
			usable = append(usable, label)
		}
		if len(usable) == MaxTagsPerPage {
			break
		}
	}
	resolved, err := s.Resolve(ctx, orgID, usable)
	if err != nil {
		return nil, err
	}
	return idsOf(resolved), nil
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
