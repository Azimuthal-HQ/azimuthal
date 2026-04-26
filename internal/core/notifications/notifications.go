// Package notifications handles in-app alert delivery.
//
// Phase 1 implements the in-app surface only: notification rows are written
// directly to the notifications table via Recorder, and the API exposes
// list/mark-read endpoints. Email fan-out and mention parsing are reserved
// for later phases — but the kind enum reserves the values now so the schema
// does not need re-migration.
package notifications

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Kind enumerates the notification types known to Phase 1. Mention is reserved
// — it is a valid kind value, but no producer emits it yet (mention parsing
// lands in a later phase).
type Kind string

const (
	// KindAssigned is sent when an item or ticket is assigned to a user.
	KindAssigned Kind = "assigned"
	// KindMentioned is reserved for @mentions in comments and pages. No
	// Phase 1 producer emits this kind.
	KindMentioned Kind = "mentioned"
	// KindCommented is sent when a comment is added to an entity the
	// recipient is the assignee or reporter of.
	KindCommented Kind = "commented"
)

// EntityKind classifies the entity a notification points at. Used by the
// frontend to compute the click-through URL.
type EntityKind string

const (
	// EntityTicket is a service-desk ticket.
	EntityTicket EntityKind = "ticket"
	// EntityItem is a project tracker item.
	EntityItem EntityKind = "item"
	// EntityPage is a wiki page.
	EntityPage EntityKind = "page"
	// EntityComment is a comment on another entity.
	EntityComment EntityKind = "comment"
)

// Notification is the domain projection of a row in the notifications table.
type Notification struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Kind       Kind       `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	EntityKind EntityKind `json:"entity_kind,omitempty"`
	EntityID   uuid.UUID  `json:"entity_id,omitempty"`
	IsRead     bool       `json:"is_read"`
	CreatedAt  string     `json:"created_at"`
	ReadAt     string     `json:"read_at,omitempty"`
}

// CreateInput holds the fields required to create a notification row.
type CreateInput struct {
	UserID     uuid.UUID
	Kind       Kind
	Title      string
	Body       string
	EntityKind EntityKind
	EntityID   uuid.UUID
}

// Recorder writes new in-app notifications. It is owner-scoped at the
// caller layer — Recorder itself trusts the supplied UserID.
type Recorder interface {
	// Create persists a notification row. Returning an error must NOT
	// interrupt normal application flow at the call site; callers are
	// expected to log and continue.
	Create(ctx context.Context, input CreateInput) (*Notification, error)
}

// Reader exposes the read side of the notification surface used by the
// HTTP handler.
type Reader interface {
	List(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]*Notification, error)
	CountUnread(ctx context.Context, userID uuid.UUID) (int64, error)
	MarkRead(ctx context.Context, userID, notificationID uuid.UUID) error
	MarkAllRead(ctx context.Context, userID uuid.UUID) error
}

// Service combines Recorder + Reader behind a single concrete type backed by
// the sqlc-generated queries.
type Service struct {
	queries *generated.Queries
}

// NewService returns a Service backed by the sqlc-generated Queries.
func NewService(queries *generated.Queries) *Service {
	return &Service{queries: queries}
}

// ErrInvalidUserID is returned when a notification operation is invoked with
// a zero UUID for the user.
var ErrInvalidUserID = errors.New("user id must not be empty")

// Create persists a notification row.
func (s *Service) Create(ctx context.Context, input CreateInput) (*Notification, error) {
	if input.UserID == uuid.Nil {
		return nil, ErrInvalidUserID
	}
	if input.Title == "" {
		return nil, fmt.Errorf("creating notification: title required")
	}

	var bodyPtr *string
	if input.Body != "" {
		body := input.Body
		bodyPtr = &body
	}
	var entityKindPtr *string
	if input.EntityKind != "" {
		ek := string(input.EntityKind)
		entityKindPtr = &ek
	}
	var entityID pgtype.UUID
	if input.EntityID != uuid.Nil {
		entityID = pgtype.UUID{Bytes: input.EntityID, Valid: true}
	}

	row, err := s.queries.CreateNotification(ctx, generated.CreateNotificationParams{
		ID:         uuid.New(),
		UserID:     input.UserID,
		Kind:       string(input.Kind),
		Title:      input.Title,
		Body:       bodyPtr,
		EntityKind: entityKindPtr,
		EntityID:   entityID,
	})
	if err != nil {
		return nil, fmt.Errorf("inserting notification: %w", err)
	}
	return toNotification(row), nil
}

// List returns the user's notifications ordered most-recent first.
// limit and offset are clamped at the caller boundary; pass <=0 to use the
// default limit of 50.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]*Notification, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidUserID
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.queries.ListNotificationsByUser(ctx, generated.ListNotificationsByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("listing notifications: %w", err)
	}
	out := make([]*Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, toNotification(row))
	}
	return out, nil
}

// CountUnread returns how many notifications the user has not yet read.
func (s *Service) CountUnread(ctx context.Context, userID uuid.UUID) (int64, error) {
	if userID == uuid.Nil {
		return 0, ErrInvalidUserID
	}
	n, err := s.queries.CountUnreadNotifications(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("counting unread notifications: %w", err)
	}
	return n, nil
}

// MarkRead clears the unread flag on a single notification, scoped to the
// owning user — passing a notificationID owned by another user is a no-op.
func (s *Service) MarkRead(ctx context.Context, userID, notificationID uuid.UUID) error {
	if userID == uuid.Nil {
		return ErrInvalidUserID
	}
	if err := s.queries.MarkNotificationRead(ctx, generated.MarkNotificationReadParams{
		ID:     notificationID,
		UserID: userID,
	}); err != nil {
		return fmt.Errorf("marking notification read: %w", err)
	}
	return nil
}

// MarkAllRead clears the unread flag on every notification owned by the user.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return ErrInvalidUserID
	}
	if err := s.queries.MarkAllNotificationsRead(ctx, userID); err != nil {
		return fmt.Errorf("marking all notifications read: %w", err)
	}
	return nil
}

func toNotification(row generated.Notification) *Notification {
	n := &Notification{
		ID:        row.ID,
		UserID:    row.UserID,
		Kind:      Kind(row.Kind),
		Title:     row.Title,
		IsRead:    row.IsRead,
		CreatedAt: row.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if row.Body != nil {
		n.Body = *row.Body
	}
	if row.EntityKind != nil {
		n.EntityKind = EntityKind(*row.EntityKind)
	}
	if row.EntityID.Valid {
		n.EntityID = uuid.UUID(row.EntityID.Bytes)
	}
	if row.ReadAt.Valid {
		n.ReadAt = row.ReadAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return n
}
