package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// SavedViewAdapter implements both views.Store (the view rows) and
// views.ResultStore (the two cross-space result fan-outs) over the
// saved_views, tickets and project_items tables.
//
// One adapter for both seams because they share nothing but the pool and are
// always wired together; splitting them would mean two constructors in
// main.go for one feature.
type SavedViewAdapter struct {
	q *generated.Queries
}

// NewSavedViewAdapter creates a SavedViewAdapter backed by the given pool.
func NewSavedViewAdapter(pool *pgxpool.Pool) *SavedViewAdapter {
	return &SavedViewAdapter{q: generated.New(pool)}
}

// Compile-time proof that the adapter satisfies both seams. Without these the
// first sign of a drifted signature is a nil collaborator at boot, which
// TestHarness_NoDarkDependencies would catch but only after the fact.
var (
	_ views.Store       = (*SavedViewAdapter)(nil)
	_ views.ResultStore = (*SavedViewAdapter)(nil)
)

func viewFromRow(r generated.SavedView, ownerName string, teamName *string) (views.View, error) {
	q, err := views.ParseQuery(r.Query)
	if err != nil {
		// A stored document this build cannot parse is a real problem and
		// must surface, not be silently replaced by an empty filter that
		// would match everything the viewer can read.
		return views.View{}, fmt.Errorf("saved view %s holds an unreadable filter document: %w", r.ID, err)
	}
	return views.View{
		ID:               r.ID,
		OrgID:            r.OrgID,
		OwnerID:          r.OwnerID,
		Name:             r.Name,
		Description:      r.Description,
		Query:            q,
		Visibility:       views.Visibility(r.Visibility),
		VisibilityTeamID: goUUIDPtr(r.VisibilityTeamID),
		CreatedAt:        goTime(r.CreatedAt),
		UpdatedAt:        goTime(r.UpdatedAt),
		OwnerName:        ownerName,
		TeamName:         derefStr(teamName),
	}, nil
}

// Create inserts a view.
func (a *SavedViewAdapter) Create(ctx context.Context, v views.View) (views.View, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return views.View{}, fmt.Errorf("encoding the view's filter document: %w", err)
	}
	row, err := a.q.CreateSavedView(ctx, generated.CreateSavedViewParams{
		OrgID:            v.OrgID,
		OwnerID:          v.OwnerID,
		Name:             v.Name,
		Description:      v.Description,
		Query:            raw,
		Visibility:       string(v.Visibility),
		VisibilityTeamID: pgUUID(v.VisibilityTeamID),
	})
	if err != nil {
		return views.View{}, fmt.Errorf("saved view adapter create: %w", err)
	}
	return viewFromRow(row, "", nil)
}

// Get returns one view by id, or views.ErrNotFound.
func (a *SavedViewAdapter) Get(ctx context.Context, orgID, id uuid.UUID) (views.View, error) {
	row, err := a.q.GetSavedView(ctx, generated.GetSavedViewParams{ID: id, OrgID: orgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return views.View{}, views.ErrNotFound
		}
		return views.View{}, fmt.Errorf("saved view adapter get: %w", err)
	}
	return viewFromRow(row, "", nil)
}

// Update writes the whole mutable surface back.
func (a *SavedViewAdapter) Update(ctx context.Context, v views.View) (views.View, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return views.View{}, fmt.Errorf("encoding the view's filter document: %w", err)
	}
	row, err := a.q.UpdateSavedView(ctx, generated.UpdateSavedViewParams{
		ID:               v.ID,
		OrgID:            v.OrgID,
		Name:             v.Name,
		Description:      v.Description,
		Query:            raw,
		Visibility:       string(v.Visibility),
		VisibilityTeamID: pgUUID(v.VisibilityTeamID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return views.View{}, views.ErrNotFound
		}
		return views.View{}, fmt.Errorf("saved view adapter update: %w", err)
	}
	return viewFromRow(row, "", nil)
}

// SoftDelete marks a view deleted and reports how many rows it touched.
func (a *SavedViewAdapter) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error) {
	n, err := a.q.SoftDeleteSavedView(ctx, generated.SoftDeleteSavedViewParams{ID: id, OrgID: orgID})
	if err != nil {
		return 0, fmt.Errorf("saved view adapter delete: %w", err)
	}
	return n, nil
}

// ListForViewer returns every view whose definition reaches the caller.
func (a *SavedViewAdapter) ListForViewer(ctx context.Context, orgID, viewerID uuid.UUID, teamIDs []uuid.UUID) ([]views.View, error) {
	if teamIDs == nil {
		teamIDs = []uuid.UUID{}
	}
	rows, err := a.q.ListSavedViewsForViewer(ctx, generated.ListSavedViewsForViewerParams{
		OrgID:            orgID,
		ViewerID:         viewerID,
		EffectiveTeamIds: teamIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter list: %w", err)
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

// LiveSpaceIDs returns which of the given spaces still exist.
func (a *SavedViewAdapter) LiveSpaceIDs(ctx context.Context, orgID uuid.UUID, spaceIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	ids, err := a.q.ListLiveSpaceIDs(ctx, generated.ListLiveSpaceIDsParams{SpaceIds: spaceIDs, OrgID: orgID})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter live spaces: %w", err)
	}
	return ids, nil
}

// EffectiveTeamIDs returns the caller's ADR-0007 team set, expanded by the
// effective_team_ids schema function rather than by a second hand-written copy
// of the rule.
func (a *SavedViewAdapter) EffectiveTeamIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := a.q.ListEffectiveTeamIDs(ctx, generated.ListEffectiveTeamIDsParams{OrgID: orgID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter effective teams: %w", err)
	}
	return ids, nil
}

// nonNilUUIDs and nonNilStrings exist because the fan-out queries test
// cardinality(...) = 0 to mean "this filter is not set". A nil slice reaches
// pgx as NULL, and cardinality(NULL) is NULL rather than 0, which makes the
// whole predicate NULL and drops every row. An empty slice is the difference
// between "no filter" and "no results".
func nonNilUUIDs(in []uuid.UUID) []uuid.UUID {
	if in == nil {
		return []uuid.UUID{}
	}
	return in
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// sortKeyString pulls the lateral-derived sort key out of the interface{}
// sqlc gives it.
//
// sqlc cannot infer the type of a column produced by a CROSS JOIN LATERAL and
// falls back to interface{}; a column override does not reach it either. The
// value is always text — saved_view_sort_key RETURNS TEXT (migration 038) —
// so this assertion holds, and it is written to FAIL rather than to default.
// A silent "" here would collapse every row to the same cursor position and
// break paging in a way that only shows up on page two.
func sortKeyString(v interface{}) (string, error) {
	switch s := v.(type) {
	case string:
		return s, nil
	case []byte:
		return string(s), nil
	case nil:
		return "", errors.New("sort key came back NULL; saved_view_sort_key is declared NOT NULL-returning")
	default:
		return "", fmt.Errorf("sort key came back as %T, want text", v)
	}
}

// ListTickets runs the Beacon half of a view's results.
func (a *SavedViewAdapter) ListTickets(ctx context.Context, p views.FanoutParams) ([]views.Result, error) {
	rows, err := a.q.ListViewTickets(ctx, generated.ListViewTicketsParams{
		SortField:         p.SortField,
		OrgID:             p.OrgID,
		ReadableSpaceIds:  nonNilUUIDs(p.ReadableSpaceIDs),
		SharedTicketIds:   nonNilUUIDs(p.SharedIDs),
		SpaceIds:          nonNilUUIDs(p.SpaceIDs),
		Statuses:          nonNilStrings(p.Statuses),
		Priorities:        nonNilStrings(p.Priorities),
		FilterAssignee:    p.FilterAssignee,
		AssigneeIds:       nonNilUUIDs(p.AssigneeIDs),
		IncludeUnassigned: p.IncludeUnassigned,
		TextPattern:       p.TextPattern,
		CursorKey:         p.CursorKey,
		Descending:        p.Descending,
		CursorID:          p.CursorID,
		RowLimit:          p.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter list tickets: %w", err)
	}
	out := make([]views.Result, 0, len(rows))
	for _, r := range rows {
		key, err := sortKeyString(r.SortKey)
		if err != nil {
			return nil, err
		}
		out = append(out, views.Result{
			Module: views.ModuleBeacon,
			ID:     r.ID,
			// Tickets carry no key column; the human-readable reference is
			// composed from the space key and the number, by the one function
			// that owns that spelling.
			Key:        tickets.ComposeRef(r.SpaceKey, r.Number),
			Number:     r.Number,
			Title:      r.Title,
			SpaceID:    r.SpaceID,
			SpaceKey:   r.SpaceKey,
			SpaceName:  r.SpaceName,
			Status:     r.Status,
			Priority:   r.Priority,
			AssigneeID: goUUIDPtr(r.AssigneeID),
			Labels:     r.Labels,
			CreatedAt:  goTime(r.CreatedAt),
			UpdatedAt:  goTime(r.UpdatedAt),
			DueAt:      goTimePtr(r.DueAt),
			ResolvedAt: goTimePtr(r.ResolvedAt),
			SortKey:    key,
		})
	}
	return out, nil
}

// ListProjectItems runs the Vector half of a view's results.
func (a *SavedViewAdapter) ListProjectItems(ctx context.Context, p views.FanoutParams) ([]views.Result, error) {
	rows, err := a.q.ListViewProjectItems(ctx, generated.ListViewProjectItemsParams{
		SortField:         p.SortField,
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
		TextPattern:       p.TextPattern,
		CursorKey:         p.CursorKey,
		Descending:        p.Descending,
		CursorID:          p.CursorID,
		RowLimit:          p.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("saved view adapter list project items: %w", err)
	}
	out := make([]views.Result, 0, len(rows))
	for _, r := range rows {
		key, err := sortKeyString(r.SortKey)
		if err != nil {
			return nil, err
		}
		kind := r.Kind
		out = append(out, views.Result{
			Module: views.ModuleVector,
			ID:     r.ID,
			// Project items carry their own key column (migration 031).
			Key:        r.ItemKey,
			Number:     r.Number,
			Title:      r.Title,
			SpaceID:    r.SpaceID,
			SpaceKey:   r.SpaceKey,
			SpaceName:  r.SpaceName,
			Status:     r.Status,
			Priority:   r.Priority,
			AssigneeID: goUUIDPtr(r.AssigneeID),
			Labels:     r.Labels,
			Kind:       &kind,
			SprintID:   goUUIDPtr(r.SprintID),
			CreatedAt:  goTime(r.CreatedAt),
			UpdatedAt:  goTime(r.UpdatedAt),
			DueAt:      goTimePtr(r.DueAt),
			ResolvedAt: goTimePtr(r.ResolvedAt),
			SortKey:    key,
		})
	}
	return out, nil
}
