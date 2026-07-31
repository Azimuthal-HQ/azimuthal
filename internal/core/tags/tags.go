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

// MaxTaggedPages bounds one page of the tag browse. The query asks for one more
// than this, so the service can report that there WERE more rather than quietly
// returning a short answer.
const MaxTaggedPages = 200

// TagPages is the tag browse's answer: the tag, the readable pages carrying it,
// and whether there were more than fit.
type TagPages struct {
	Tag   Tag          `json:"tag"`
	Pages []TaggedPage `json:"pages"`
	// Truncated says the answer was cut short. A caller that ignores it shows a
	// list that looks complete and is not.
	Truncated bool `json:"truncated"`
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
	// ForPage reads a page's tags, reconciled against the space the route named.
	// A tag set says what a page is about, so reading one across a space
	// boundary describes the subject matter of a page the caller cannot open —
	// and the route proved {spaceID} readable while proving nothing about
	// {pageID}.
	ForPage(ctx context.Context, pageID, spaceID uuid.UUID) ([]Tag, error)
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

// ForPage returns the tags a page in the given space carries.
//
// The space is not a filter on the answer, it is the authorisation for asking:
// page_tags is keyed on the page alone, so the query joins through the page to
// establish that the page id the caller named really is in the space their
// request was authorised against. A page elsewhere carries no tags here, which
// is what a page that does not exist carries.
func (s *Service) ForPage(ctx context.Context, pageID, spaceID uuid.UUID) ([]Tag, error) {
	out, err := s.repo.ForPage(ctx, pageID, spaceID)
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
	wanted, invalid, hadInvalid := distinctTags(labels)
	if hadInvalid {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, invalid)
	}
	// The ceiling applies to the DEDUPED set rather than to what was typed.
	// Refusing fifty-one labels that name three tags would be refusing
	// arithmetic nobody did, and the ceiling exists to bound how many tag ROWS
	// one request can mint — which is what deduping decides.
	if len(wanted) > MaxTagsPerPage {
		return nil, fmt.Errorf("%w: %d given, %d allowed", ErrTooManyTags, len(wanted), MaxTagsPerPage)
	}
	return s.upsertAll(ctx, orgID, wanted)
}

// labelledSlug pairs a tag's identity with the spelling that produced it.
type labelledSlug struct {
	slug  string
	label string
}

// distinctTags normalises labels and drops duplicates by SLUG rather than by
// text, reporting the first label that cannot become a tag.
//
// Deduping on the slug is the load-bearing part. "Design Docs", "design docs"
// and "design_docs" are one tag; a caller that deduped on the text would call
// Upsert three times for it and would count it three times against the
// ceiling. The first spelling in order is the one carried forward, matching
// what the table itself does on conflict.
func distinctTags(labels []string) (wanted []labelledSlug, invalid string, hadInvalid bool) {
	out := make([]labelledSlug, 0, len(labels))
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
		out = append(out, labelledSlug{slug: slug, label: label})
	}
	return out, "", false
}

func (s *Service) upsertAll(ctx context.Context, orgID uuid.UUID, wanted []labelledSlug) ([]Tag, error) {
	out := make([]Tag, 0, len(wanted))
	for _, w := range wanted {
		tag, err := s.repo.Upsert(ctx, orgID, w.slug, w.label)
		if err != nil {
			return nil, fmt.Errorf("creating tag %q: %w", w.slug, err)
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
func (s *Service) PagesWithSlug(ctx context.Context, orgID uuid.UUID, slug string, readableSpaceIDs []uuid.UUID) (TagPages, error) {
	tag, err := s.repo.GetByOrgSlug(ctx, orgID, slug)
	if err != nil {
		// Wrapped, but with the sentinel intact: the handler dispatches on
		// errors.Is(err, ErrNotFound) to answer 404, and a laundered error
		// would turn an unknown tag slug into a 500.
		return TagPages{}, fmt.Errorf("getting tag %q: %w", slug, err)
	}
	pages, err := s.repo.PagesWithTag(ctx, tag.ID, readableSpaceIDs)
	if err != nil {
		return TagPages{}, fmt.Errorf("listing pages with a tag: %w", err)
	}

	// The query asks for one more row than a page holds. Its presence is the
	// only way the caller can tell a full answer from a cut-off one — a bare
	// LIMIT returns a truncated list that looks exactly like a complete one,
	// and because the order is most-recent-first the pages that disappear are
	// the oldest, so the reader is shown the wrong nothing and told nothing.
	truncated := len(pages) > MaxTaggedPages
	if truncated {
		pages = pages[:MaxTaggedPages]
	}
	return TagPages{Tag: tag, Pages: pages, Truncated: truncated}, nil
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
	// publish is not the moment to reject a whole page over a stray `#!!!` in
	// its body, and Resolve's error exists for the explicit editor path, where a
	// person is looking at the field they typed into.
	usable := make([]string, 0, len(labels))
	for _, label := range labels {
		if Slugify(normaliseLabel(label)) != "" {
			usable = append(usable, label)
		}
	}

	// Deduped BEFORE the ceiling is applied, and the ordering matters. Counting
	// raw labels would have dropped everything past the fiftieth spelling even
	// when they named far fewer tags — and silently, since a publish reports no
	// truncation. The document walker's own ceiling
	// (doc.maxInlineTagsPerDocument) bounds the input to the same number, so
	// after deduping this one cannot bite at all; it stays as the belt to that
	// walker's braces, because the two live in different packages and only one
	// of them is the one a hostile paste reaches first.
	wanted, _, hadInvalid := distinctTags(usable)
	if hadInvalid {
		// Unreachable: every label was slug-checked above. Treated as no tags
		// rather than as a failed publish, because the page's content is not in
		// question either way.
		return nil, nil
	}
	if len(wanted) > MaxTagsPerPage {
		wanted = wanted[:MaxTagsPerPage]
	}

	resolved, err := s.upsertAll(ctx, orgID, wanted)
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
