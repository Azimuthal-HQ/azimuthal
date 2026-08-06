package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/search"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// SearchAdapter implements search.Store over the sqlc-generated cross-module
// queries (P6, spec §5 and §7; ADR-0009, ADR-0010).
//
// It translates rows into search.Result and nothing else. In particular it does
// NOT decide what a viewer may see: the access arrays arrive already resolved
// and are passed through untouched, and the disclosure rule for share-only hits
// is applied once in the service. An adapter that filtered as well would be a
// second implementation of the same rule, and the two would drift.
type SearchAdapter struct {
	q *generated.Queries
}

// NewSearchAdapter creates a SearchAdapter.
func NewSearchAdapter(pool *pgxpool.Pool) *SearchAdapter {
	return &SearchAdapter{q: generated.New(pool)}
}

// ParsedQuery returns PostgreSQL's own parse of the search text.
//
// This exists because websearch_to_tsquery has no error path: stopword-only
// input, pure punctuation and a single 3000-character token all yield an EMPTY
// tsquery and at most a NOTICE, which pgx does not surface. Asking PostgreSQL
// what it made of the text is the only way to distinguish "nothing matched"
// from "there was nothing to match" — and the difference matters, because an
// empty tsquery makes every "the unreadable row does not appear" assertion pass
// with the access filter deleted.
func (a *SearchAdapter) ParsedQuery(ctx context.Context, text string) (string, error) {
	parsed, err := a.q.ParseSearchQuery(ctx, text)
	if err != nil {
		return "", fmt.Errorf("parsing the search query: %w", err)
	}
	return parsed, nil
}

// ResolveTagSlug maps an org-scoped tag slug to its id.
//
// It reuses GetTagByOrgSlug rather than adding a second lookup — one spelling
// of "find this org's tag", per shared-surfaces.
func (a *SearchAdapter) ResolveTagSlug(ctx context.Context, orgID uuid.UUID, slug string) (uuid.UUID, error) {
	tag, err := a.q.GetTagByOrgSlug(ctx, generated.GetTagByOrgSlugParams{OrgID: orgID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, search.ErrTagNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("looking up tag %q: %w", slug, err)
	}
	return tag.ID, nil
}

// SearchPages runs the Codex half, including the D46 cascade-subtree arm.
func (a *SearchAdapter) SearchPages(ctx context.Context, p search.FanoutParams) ([]search.Result, error) {
	rows, err := a.q.GlobalSearchPages(ctx, generated.GlobalSearchPagesParams{
		Query:            p.Query,
		OrgID:            p.OrgID,
		ReadableSpaceIds: p.ReadableSpaceIDs,
		SharedPageIds:    p.SharedPageIDs,
		SubtreeSpaceIds:  p.SubtreeSpaceIDs,
		SubtreePatterns:  p.SubtreePatterns,
		FilterTag:        p.FilterTag,
		TagID:            p.TagID,
		CursorKey:        p.CursorKey,
		CursorID:         p.CursorID,
		RowLimit:         p.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching pages: %w", err)
	}
	out := make([]search.Result, 0, len(rows))
	for _, r := range rows {
		space := r.SpaceID
		out = append(out, search.Result{
			Module:    search.ModuleCodex,
			ID:        r.ID,
			Title:     r.Title,
			SortKey:   sortKeyText(r.SortKey),
			SpaceID:   &space,
			SpaceKey:  r.SpaceKey,
			SpaceName: r.SpaceName,
			Path:      r.Path,
			UpdatedAt: r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// SearchTickets runs the Beacon half. Flat: a ticket share is always exactly one
// entity, so there is no subtree arm.
func (a *SearchAdapter) SearchTickets(ctx context.Context, p search.FanoutParams) ([]search.Result, error) {
	rows, err := a.q.GlobalSearchTickets(ctx, generated.GlobalSearchTicketsParams{
		Query:            p.Query,
		OrgID:            p.OrgID,
		ReadableSpaceIds: p.ReadableSpaceIDs,
		SharedTicketIds:  p.SharedTicketIDs,
		FilterTag:        p.FilterTag,
		TagID:            p.TagID,
		CursorKey:        p.CursorKey,
		CursorID:         p.CursorID,
		RowLimit:         p.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching tickets: %w", err)
	}
	out := make([]search.Result, 0, len(rows))
	for _, r := range rows {
		space := r.SpaceID
		out = append(out, search.Result{
			Module:    search.ModuleBeacon,
			ID:        r.ID,
			Title:     r.Title,
			SortKey:   sortKeyText(r.SortKey),
			SpaceID:   &space,
			SpaceKey:  r.SpaceKey,
			SpaceName: r.SpaceName,
			Number:    r.Number,
			Status:    r.Status,
			Priority:  r.Priority,
			UpdatedAt: r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// SearchProjectItems runs the Vector half.
func (a *SearchAdapter) SearchProjectItems(ctx context.Context, p search.FanoutParams) ([]search.Result, error) {
	rows, err := a.q.GlobalSearchProjectItems(ctx, generated.GlobalSearchProjectItemsParams{
		Query:            p.Query,
		OrgID:            p.OrgID,
		ReadableSpaceIds: p.ReadableSpaceIDs,
		SharedItemIds:    p.SharedItemIDs,
		FilterTag:        p.FilterTag,
		TagID:            p.TagID,
		CursorKey:        p.CursorKey,
		CursorID:         p.CursorID,
		RowLimit:         p.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching project items: %w", err)
	}
	out := make([]search.Result, 0, len(rows))
	for _, r := range rows {
		space := r.SpaceID
		out = append(out, search.Result{
			Module:    search.ModuleVector,
			ID:        r.ID,
			Title:     r.Title,
			SortKey:   sortKeyText(r.SortKey),
			SpaceID:   &space,
			SpaceKey:  r.SpaceKey,
			SpaceName: r.SpaceName,
			Number:    r.Number,
			ItemKey:   r.ItemKey,
			Kind:      r.Kind,
			Status:    r.Status,
			Priority:  r.Priority,
			UpdatedAt: r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// sortKeyText coerces the generated sort key to its string form.
//
// sqlc types the column as `interface{}` because the COLLATE "C" defeats its
// inference, but the SQL casts it to text and pgx therefore hands back a string.
// The zero value on an unexpected type is deliberate and safe: an empty key
// sorts LAST in a descending order, so a row whose key failed to decode falls to
// the bottom of the page rather than silently displacing correctly-ranked
// results at the top.
func sortKeyText(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

// Snippets returns ts_headline excerpts for the ids of one module.
//
// The highlight delimiters are STX and ETX (U+0002 / U+0003), not markup.
// ts_headline escapes nothing — it returns the source text with the delimiters
// inserted — so HTML delimiters over a body containing a script tag would
// produce a snippet carrying that script, and any client rendering it as HTML
// would execute it. Control characters cannot occur in ordinary prose, so the
// client can split on them and wrap the pieces in real elements: highlighting
// without ever interpreting stored content as markup.
func (a *SearchAdapter) Snippets(ctx context.Context, m search.Module, query string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]string{}, nil
	}
	switch m {
	case search.ModuleCodex:
		return a.pageSnippets(ctx, query, ids)
	case search.ModuleBeacon:
		return a.ticketSnippets(ctx, query, ids)
	case search.ModuleVector:
		return a.itemSnippets(ctx, query, ids)
	default:
		return nil, fmt.Errorf("search: unknown module %q", m)
	}
}

func (a *SearchAdapter) pageSnippets(ctx context.Context, query string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := a.q.HeadlinePages(ctx, generated.HeadlinePagesParams{Query: query, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("headlining pages: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = string(r.Snippet)
	}
	return out, nil
}

func (a *SearchAdapter) ticketSnippets(ctx context.Context, query string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := a.q.HeadlineTickets(ctx, generated.HeadlineTicketsParams{Query: query, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("headlining tickets: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = string(r.Snippet)
	}
	return out, nil
}

func (a *SearchAdapter) itemSnippets(ctx context.Context, query string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := a.q.HeadlineProjectItems(ctx, generated.HeadlineProjectItemsParams{Query: query, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("headlining project items: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = string(r.Snippet)
	}
	return out, nil
}
