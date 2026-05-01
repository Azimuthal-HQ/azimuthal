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

// RevisionDiff holds a unified diff between two page revisions.
type RevisionDiff struct {
	FromVersion int32  `json:"from_version"`
	ToVersion   int32  `json:"to_version"`
	TitleDiff   string `json:"title_diff"`
	ContentDiff string `json:"content_diff"`
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

	titleDiffs := dmp.DiffMain(from.Title, to.Title, false)
	titlePatch := dmp.DiffPrettyText(titleDiffs)

	contentDiffs := dmp.DiffMain(from.Content, to.Content, true)
	contentDiffs = dmp.DiffCleanupSemantic(contentDiffs)
	contentPatch := dmp.DiffPrettyText(contentDiffs)

	return RevisionDiff{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		TitleDiff:   titlePatch,
		ContentDiff: contentPatch,
	}, nil
}
