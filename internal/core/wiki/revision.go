package wiki

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ErrRevisionNotFound is returned when a requested revision does not exist.
var ErrRevisionNotFound = errors.New("revision not found")

// DiffOp is what happened to one run of text between two revisions.
//
// The numbers are diffmatchpatch's own (-1 delete, 0 equal, 1 insert), stated
// here as named constants so the wire contract is this package's rather than a
// dependency's incidental encoding.
type DiffOp int8

const (
	// DiffDelete marks text present in the from-revision and gone in the to.
	DiffDelete DiffOp = -1
	// DiffEqual marks text present in both.
	DiffEqual DiffOp = 0
	// DiffInsert marks text added by the to-revision.
	DiffInsert DiffOp = 1
)

// DiffSegment is one run of text and what happened to it.
type DiffSegment struct {
	Op   DiffOp `json:"op"`
	Text string `json:"text"`
}

// RevisionDiff holds the difference between two page revisions.
//
// # Why segments rather than a rendered string
//
// This used to carry `title_diff` and `content_diff` as strings produced by
// diffmatchpatch's DiffPrettyText — which wraps insertions and deletions in
// ANSI terminal colour codes (`\x1b[32m` … `\x1b[0m`). Over a JSON API consumed
// by a browser those are not colour, they are unprintable bytes in the middle
// of the text, and no reader could render them. Nothing consumed the old shape
// correctly: the only client-side binding, `useWikiDiff` in web/src/lib/api.ts,
// declared the response as `{ diff: string }`, which never matched this struct
// at all and was referenced by no component.
//
// Segments put the decision about how to show a change where it belongs — with
// the surface showing it — and keep the text itself intact.
//
// # What is being compared
//
// The markdown projection (`page_revisions.content`), not the document. It is
// derived rather than authoritative (migration 036), which is exactly what
// makes it the right thing to diff here: it is one format across the
// markdown→document boundary, so a page that predates the editor and a page
// that does not can be compared against each other. A true structural diff of
// two rich documents is a different and much larger feature, and the surface
// says "text comparison" rather than implying it has one.
type RevisionDiff struct {
	FromVersion int32 `json:"from_version"`
	ToVersion   int32 `json:"to_version"`
	// TitleSegments is empty when the title did not change, so a caller can
	// show the title row only when it is part of the answer.
	TitleSegments   []DiffSegment `json:"title_segments"`
	ContentSegments []DiffSegment `json:"content_segments"`
}

// ListRevisions returns all revisions of a page in the given space, ordered
// newest first.
//
// The space is carried because page_revisions has no space of its own: the
// ledger is readable exactly when its page is, and the query joins through the
// page to say so. Without it a page id alone returned every version's title and
// author across any space or organisation boundary — the route had proved
// {spaceID} readable and nothing at all about {pageID}. A page in another space
// yields an empty ledger, which is what a page that does not exist yields.
func (s *Service) ListRevisions(ctx context.Context, pageID, spaceID uuid.UUID) ([]generated.ListPageRevisionsRow, error) {
	revisions, err := s.store.ListPageRevisions(ctx, generated.ListPageRevisionsParams{
		PageID:  pageID,
		SpaceID: spaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing revisions: %w", err)
	}
	return revisions, nil
}

// GetRevisionInSpace retrieves one revision of a page that lives in the given
// space.
//
// The page is reconciled first and the revision is read second. `page_revisions`
// is keyed on the page alone — there is no space-scoped revision query to switch
// to — and a revision carries the full historical title and body, so reading one
// by page id was the same disclosure as reading the page. Proving the page is in
// the space is what makes the second read safe.
func (s *Service) GetRevisionInSpace(ctx context.Context, pageID, spaceID uuid.UUID, version int32) (generated.PageRevision, error) {
	if _, err := s.GetPageInSpace(ctx, pageID, spaceID); err != nil {
		return generated.PageRevision{}, err
	}
	return s.GetRevision(ctx, pageID, version)
}

// GetRevision retrieves a specific revision by page ID and version number.
//
// UNSCOPED: it takes no space and reconciles nothing. Space-scoped routes use
// [Service.GetRevisionInSpace].
func (s *Service) GetRevision(ctx context.Context, pageID uuid.UUID, version int32) (generated.PageRevision, error) {
	rev, err := s.store.GetPageRevision(ctx, generated.GetPageRevisionParams{
		PageID:  pageID,
		Version: version,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.PageRevision{}, ErrRevisionNotFound
		}
		return generated.PageRevision{}, fmt.Errorf("getting revision: %w", err)
	}
	return rev, nil
}

// DiffRevisionsInSpace compares two revisions of a page that lives in the given
// space.
//
// Same reason as [Service.GetRevisionInSpace]: the diff is built out of two
// revision bodies, so it discloses everything the revisions do. The page is
// reconciled against the space before either one is read.
func (s *Service) DiffRevisionsInSpace(ctx context.Context, pageID, spaceID uuid.UUID, fromVersion, toVersion int32) (RevisionDiff, error) {
	if _, err := s.GetPageInSpace(ctx, pageID, spaceID); err != nil {
		return RevisionDiff{}, err
	}
	return s.DiffRevisions(ctx, pageID, fromVersion, toVersion)
}

// DiffRevisions computes a unified diff between two revisions of the same page.
//
// UNSCOPED. Space-scoped routes use [Service.DiffRevisionsInSpace].
func (s *Service) DiffRevisions(ctx context.Context, pageID uuid.UUID, fromVersion, toVersion int32) (RevisionDiff, error) {
	// The wrapping names the VERSION, not the call site. handleWikiError passes
	// a not-found error's own text through as the 404 body, so "getting
	// to-revision: revision not found" would put internal phrasing in front of
	// a person — while "revision not found: version 99" tells them which one.
	// RestoreRevision already formats it this way; this is its sibling path.
	from, err := s.GetRevision(ctx, pageID, fromVersion)
	if err != nil {
		return RevisionDiff{}, versionError(err, fromVersion)
	}

	to, err := s.GetRevision(ctx, pageID, toVersion)
	if err != nil {
		return RevisionDiff{}, versionError(err, toVersion)
	}

	dmp := diffmatchpatch.New()

	var titleSegments []DiffSegment
	if from.Title != to.Title {
		titleSegments = segmentsOf(dmp.DiffMain(from.Title, to.Title, false))
	}

	contentDiffs := dmp.DiffMain(from.Content, to.Content, true)
	contentDiffs = dmp.DiffCleanupSemantic(contentDiffs)

	return RevisionDiff{
		FromVersion:     fromVersion,
		ToVersion:       toVersion,
		TitleSegments:   titleSegments,
		ContentSegments: segmentsOf(contentDiffs),
	}, nil
}

// versionError names the version a lookup failed on, keeping the sentinel
// wrapped so errors.Is still recognises it as ErrRevisionNotFound.
func versionError(err error, version int32) error {
	if errors.Is(err, ErrRevisionNotFound) {
		return fmt.Errorf("%w: version %d", ErrRevisionNotFound, version)
	}
	return fmt.Errorf("getting revision %d: %w", version, err)
}

// segmentsOf converts diffmatchpatch's diffs into the wire shape, dropping
// empty runs — the library emits them for an insert-only or delete-only change
// and they carry nothing a reader needs.
func segmentsOf(diffs []diffmatchpatch.Diff) []DiffSegment {
	out := make([]DiffSegment, 0, len(diffs))
	for _, d := range diffs {
		if d.Text == "" {
			continue
		}
		out = append(out, DiffSegment{Op: DiffOp(d.Type), Text: d.Text})
	}
	return out
}
