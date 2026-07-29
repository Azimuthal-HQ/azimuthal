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

// ListRevisions returns all revisions for a page, ordered newest first.
func (s *Service) ListRevisions(ctx context.Context, pageID uuid.UUID) ([]generated.ListPageRevisionsRow, error) {
	revisions, err := s.store.ListPageRevisions(ctx, pageID)
	if err != nil {
		return nil, fmt.Errorf("listing revisions: %w", err)
	}
	return revisions, nil
}

// GetRevision retrieves a specific revision by page ID and version number.
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

// DiffRevisions computes a unified diff between two revisions of the same page.
func (s *Service) DiffRevisions(ctx context.Context, pageID uuid.UUID, fromVersion, toVersion int32) (RevisionDiff, error) {
	from, err := s.GetRevision(ctx, pageID, fromVersion)
	if err != nil {
		return RevisionDiff{}, fmt.Errorf("getting from-revision: %w", err)
	}

	to, err := s.GetRevision(ctx, pageID, toVersion)
	if err != nil {
		return RevisionDiff{}, fmt.Errorf("getting to-revision: %w", err)
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
