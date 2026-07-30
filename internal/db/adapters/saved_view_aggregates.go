package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// The P5 half of SavedViewAdapter: the grouped fan-outs behind count and
// breakdown gadgets, and the audience-blind batch view lookup a dashboard uses
// to resolve its gadgets in one query.
//
// It lives beside the row and result seams rather than in a package of its own
// because it reads the same three tables through the same access union. A
// second adapter would be a second place for that union to be assembled.

// Compile-time proof that the adapter satisfies the aggregate seam too.
var _ views.AggregateStore = (*SavedViewAdapter)(nil)

// GetMany returns the live, non-space-bound views among ids, with no audience
// filter. See views.Store.GetMany and the query header for why it is
// audience-blind and why that is safe: the caller applies views.Audience
// before anything reaches a response.
func (a *SavedViewAdapter) GetMany(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]views.View, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := a.q.GetSavedViewsByIDs(ctx, generated.GetSavedViewsByIDsParams{
		OrgID: orgID, Ids: nonNilUUIDs(ids),
	})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter get many: %w", err)
	}
	out := make([]views.View, 0, len(rows))
	for _, r := range rows {
		v, err := viewFromRow(generated.SavedView{
			ID: r.ID, OrgID: r.OrgID, OwnerID: r.OwnerID, Name: r.Name,
			Description: r.Description, Query: r.Query, Visibility: r.Visibility,
			VisibilityTeamID: r.VisibilityTeamID, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt, DeletedAt: r.DeletedAt,
		}, r.OwnerName, r.TeamName)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// CountTickets counts the Beacon half of a resolved view.
func (a *SavedViewAdapter) CountTickets(ctx context.Context, p views.FanoutParams) (int64, error) {
	n, err := a.q.CountViewTickets(ctx, generated.CountViewTicketsParams{
		OrgID:             p.OrgID,
		ReadableSpaceIds:  nonNilUUIDs(p.ReadableSpaceIDs),
		SharedTicketIds:   nonNilUUIDs(p.SharedIDs),
		SpaceIds:          nonNilUUIDs(p.SpaceIDs),
		Statuses:          nonNilStrings(p.Statuses),
		Priorities:        nonNilStrings(p.Priorities),
		FilterAssignee:    p.FilterAssignee,
		AssigneeIds:       nonNilUUIDs(p.AssigneeIDs),
		IncludeUnassigned: p.IncludeUnassigned,
		NotSpaceIds:       p.NotSpaceIDs,
		NotStatuses:       p.NotStatuses,
		NotPriorities:     p.NotPriorities,
		NotAssignees:      p.NotAssignees,
		CreatedAfter:      pgTimestampPtr(p.CreatedAfter),
		CreatedBefore:     pgTimestampPtr(p.CreatedBefore),
		UpdatedAfter:      pgTimestampPtr(p.UpdatedAfter),
		UpdatedBefore:     pgTimestampPtr(p.UpdatedBefore),
		DueAfter:          pgTimestampPtr(p.DueAfter),
		DueBefore:         pgTimestampPtr(p.DueBefore),
		ResolvedAfter:     pgTimestampPtr(p.ResolvedAfter),
		ResolvedBefore:    pgTimestampPtr(p.ResolvedBefore),
		TextPattern:       p.TextPattern,
	})
	if err != nil {
		return 0, fmt.Errorf("saved view adapter count tickets: %w", err)
	}
	return n, nil
}

// CountProjectItems counts the Vector half of a resolved view.
func (a *SavedViewAdapter) CountProjectItems(ctx context.Context, p views.FanoutParams) (int64, error) {
	n, err := a.q.CountViewProjectItems(ctx, generated.CountViewProjectItemsParams{
		OrgID:             p.OrgID,
		ReadableSpaceIds:  nonNilUUIDs(p.ReadableSpaceIDs),
		SharedItemIds:     nonNilUUIDs(p.SharedIDs),
		SpaceIds:          nonNilUUIDs(p.SpaceIDs),
		Statuses:          nonNilStrings(p.Statuses),
		Priorities:        nonNilStrings(p.Priorities),
		FilterAssignee:    p.FilterAssignee,
		AssigneeIds:       nonNilUUIDs(p.AssigneeIDs),
		IncludeUnassigned: p.IncludeUnassigned,
		Kinds:             nonNilStrings(p.Kinds),
		SprintIds:         nonNilUUIDs(p.SprintIDs),
		NotKinds:          p.NotKinds,
		NotSprintIds:      p.NotSprintIDs,
		NotSpaceIds:       p.NotSpaceIDs,
		NotStatuses:       p.NotStatuses,
		NotPriorities:     p.NotPriorities,
		NotAssignees:      p.NotAssignees,
		CreatedAfter:      pgTimestampPtr(p.CreatedAfter),
		CreatedBefore:     pgTimestampPtr(p.CreatedBefore),
		UpdatedAfter:      pgTimestampPtr(p.UpdatedAfter),
		UpdatedBefore:     pgTimestampPtr(p.UpdatedBefore),
		DueAfter:          pgTimestampPtr(p.DueAfter),
		DueBefore:         pgTimestampPtr(p.DueBefore),
		ResolvedAfter:     pgTimestampPtr(p.ResolvedAfter),
		ResolvedBefore:    pgTimestampPtr(p.ResolvedBefore),
		TextPattern:       p.TextPattern,
	})
	if err != nil {
		return 0, fmt.Errorf("saved view adapter count items: %w", err)
	}
	return n, nil
}

// BreakdownTickets groups the Beacon half of a resolved view.
func (a *SavedViewAdapter) BreakdownTickets(ctx context.Context, p views.FanoutParams) ([]views.Bucket, error) {
	rows, err := a.q.BreakdownViewTickets(ctx, generated.BreakdownViewTicketsParams{
		GroupBy:           p.GroupBy,
		OrgID:             p.OrgID,
		ReadableSpaceIds:  nonNilUUIDs(p.ReadableSpaceIDs),
		SharedTicketIds:   nonNilUUIDs(p.SharedIDs),
		SpaceIds:          nonNilUUIDs(p.SpaceIDs),
		Statuses:          nonNilStrings(p.Statuses),
		Priorities:        nonNilStrings(p.Priorities),
		FilterAssignee:    p.FilterAssignee,
		AssigneeIds:       nonNilUUIDs(p.AssigneeIDs),
		IncludeUnassigned: p.IncludeUnassigned,
		NotSpaceIds:       p.NotSpaceIDs,
		NotStatuses:       p.NotStatuses,
		NotPriorities:     p.NotPriorities,
		NotAssignees:      p.NotAssignees,
		CreatedAfter:      pgTimestampPtr(p.CreatedAfter),
		CreatedBefore:     pgTimestampPtr(p.CreatedBefore),
		UpdatedAfter:      pgTimestampPtr(p.UpdatedAfter),
		UpdatedBefore:     pgTimestampPtr(p.UpdatedBefore),
		DueAfter:          pgTimestampPtr(p.DueAfter),
		DueBefore:         pgTimestampPtr(p.DueBefore),
		ResolvedAfter:     pgTimestampPtr(p.ResolvedAfter),
		ResolvedBefore:    pgTimestampPtr(p.ResolvedBefore),
		TextPattern:       p.TextPattern,
	})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter breakdown tickets: %w", err)
	}
	out := make([]views.Bucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, views.Bucket{Key: r.BucketKey, Label: r.BucketLabel, Count: r.BucketCount})
	}
	return out, nil
}

// BreakdownProjectItems groups the Vector half of a resolved view.
func (a *SavedViewAdapter) BreakdownProjectItems(ctx context.Context, p views.FanoutParams) ([]views.Bucket, error) {
	rows, err := a.q.BreakdownViewProjectItems(ctx, generated.BreakdownViewProjectItemsParams{
		GroupBy:           p.GroupBy,
		OrgID:             p.OrgID,
		ReadableSpaceIds:  nonNilUUIDs(p.ReadableSpaceIDs),
		SharedItemIds:     nonNilUUIDs(p.SharedIDs),
		SpaceIds:          nonNilUUIDs(p.SpaceIDs),
		Statuses:          nonNilStrings(p.Statuses),
		Priorities:        nonNilStrings(p.Priorities),
		FilterAssignee:    p.FilterAssignee,
		AssigneeIds:       nonNilUUIDs(p.AssigneeIDs),
		IncludeUnassigned: p.IncludeUnassigned,
		Kinds:             nonNilStrings(p.Kinds),
		SprintIds:         nonNilUUIDs(p.SprintIDs),
		NotKinds:          p.NotKinds,
		NotSprintIds:      p.NotSprintIDs,
		NotSpaceIds:       p.NotSpaceIDs,
		NotStatuses:       p.NotStatuses,
		NotPriorities:     p.NotPriorities,
		NotAssignees:      p.NotAssignees,
		CreatedAfter:      pgTimestampPtr(p.CreatedAfter),
		CreatedBefore:     pgTimestampPtr(p.CreatedBefore),
		UpdatedAfter:      pgTimestampPtr(p.UpdatedAfter),
		UpdatedBefore:     pgTimestampPtr(p.UpdatedBefore),
		DueAfter:          pgTimestampPtr(p.DueAfter),
		DueBefore:         pgTimestampPtr(p.DueBefore),
		ResolvedAfter:     pgTimestampPtr(p.ResolvedAfter),
		ResolvedBefore:    pgTimestampPtr(p.ResolvedBefore),
		TextPattern:       p.TextPattern,
	})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter breakdown items: %w", err)
	}
	out := make([]views.Bucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, views.Bucket{Key: r.BucketKey, Label: r.BucketLabel, Count: r.BucketCount})
	}
	return out, nil
}
