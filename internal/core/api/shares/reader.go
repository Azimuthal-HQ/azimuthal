package shares

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// PageReader reads a page's presentational fields and renders markdown. The
// wiki Service satisfies it.
type PageReader interface {
	GetPage(ctx context.Context, id uuid.UUID) (generated.Page, error)
	RenderPage(markdown string) (string, error)
}

// TicketReader reads a ticket. The ticket Service satisfies it.
type TicketReader interface {
	Get(ctx context.Context, id uuid.UUID) (*tickets.Ticket, error)
}

// ItemReader reads a project item. The item Service satisfies it.
type ItemReader interface {
	GetItem(ctx context.Context, id uuid.UUID) (*projects.Item, error)
}

// serviceReader implements EntityReader over the three module services. It
// projects each entity into a SharedEntityView carrying ONLY presentational
// fields — the mapping is where container stripping is enforced, by simply
// never copying space_id, parent_id, path, reporter/assignee, or any tree
// reference into the view.
type serviceReader struct {
	pages   PageReader
	tickets TicketReader
	items   ItemReader
}

// NewServiceReader builds an EntityReader over the module services.
func NewServiceReader(pages PageReader, tkts TicketReader, items ItemReader) EntityReader {
	return &serviceReader{pages: pages, tickets: tkts, items: items}
}

// ReadSharedEntity dispatches on entity type and returns the container-free
// view. A missing entity maps to access.ErrSharedEntityNotFound so the read
// route 404s without leaking existence.
func (s *serviceReader) ReadSharedEntity(ctx context.Context, entityType string, id uuid.UUID) (SharedEntityView, error) {
	switch entityType {
	case access.ShareEntityPage:
		return s.readPage(ctx, id)
	case access.ShareEntityTicket:
		return s.readTicket(ctx, id)
	case access.ShareEntityProjectItem:
		return s.readItem(ctx, id)
	default:
		return SharedEntityView{}, access.ErrInvalidShareEntityType
	}
}

func (s *serviceReader) readPage(ctx context.Context, id uuid.UUID) (SharedEntityView, error) {
	page, err := s.pages.GetPage(ctx, id)
	if errors.Is(err, wiki.ErrPageNotFound) {
		return SharedEntityView{}, access.ErrSharedEntityNotFound
	}
	if err != nil {
		return SharedEntityView{}, fmt.Errorf("reading shared entity: %w", err)
	}
	view := SharedEntityView{
		ID:         page.ID,
		EntityType: access.ShareEntityPage,
		Title:      page.Title,
		Body:       page.Content,
		Version:    page.Version,
		UpdatedAt:  formatTS(page.UpdatedAt),
	}
	if html, err := s.pages.RenderPage(page.Content); err == nil {
		view.RenderedHTML = html
	} else {
		// Render failure degrades to raw body — never an error page, and
		// never a leak of anything about the container.
		slog.Warn("shares: rendering shared page failed", "page_id", id, "error", err)
	}
	return view, nil
}

func (s *serviceReader) readTicket(ctx context.Context, id uuid.UUID) (SharedEntityView, error) {
	t, err := s.tickets.Get(ctx, id)
	if errors.Is(err, tickets.ErrNotFound) {
		return SharedEntityView{}, access.ErrSharedEntityNotFound
	}
	if err != nil {
		return SharedEntityView{}, fmt.Errorf("reading shared entity: %w", err)
	}
	return SharedEntityView{
		ID:         t.ID,
		EntityType: access.ShareEntityTicket,
		Title:      t.Title,
		Body:       t.Description,
		Status:     string(t.Status),
		Priority:   string(t.Priority),
		UpdatedAt:  t.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func (s *serviceReader) readItem(ctx context.Context, id uuid.UUID) (SharedEntityView, error) {
	item, err := s.items.GetItem(ctx, id)
	if errors.Is(err, projects.ErrNotFound) {
		return SharedEntityView{}, access.ErrSharedEntityNotFound
	}
	if err != nil {
		return SharedEntityView{}, fmt.Errorf("reading shared entity: %w", err)
	}
	return SharedEntityView{
		ID:         item.ID,
		EntityType: access.ShareEntityProjectItem,
		Title:      item.Title,
		Body:       item.Description,
		Status:     item.Status,
		Priority:   item.Priority,
		UpdatedAt:  item.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// formatTS renders a pgtype.Timestamptz as RFC3339, or "" when NULL.
func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
