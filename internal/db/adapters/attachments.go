package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/attachments"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// AttachmentAdapter implements attachments.Store over sqlc-generated queries.
type AttachmentAdapter struct {
	q *generated.Queries
}

// NewAttachmentAdapter creates an AttachmentAdapter.
func NewAttachmentAdapter(pool *pgxpool.Pool) *AttachmentAdapter {
	return &AttachmentAdapter{q: generated.New(pool)}
}

// CreateAttachment inserts an attachment row.
func (a *AttachmentAdapter) CreateAttachment(ctx context.Context, att attachments.Attachment) (attachments.Attachment, error) {
	row, err := a.q.CreateAttachment(ctx, generated.CreateAttachmentParams{
		ID:          att.ID,
		OrgID:       att.OrgID,
		EntityType:  att.EntityType,
		EntityID:    att.EntityID,
		Filename:    att.Filename,
		ContentType: att.ContentType,
		SizeBytes:   att.SizeBytes,
		ObjectKey:   att.ObjectKey,
		CreatedBy:   att.CreatedBy,
	})
	if err != nil {
		return attachments.Attachment{}, fmt.Errorf("creating attachment: %w", err)
	}
	return dbAttachmentToDomain(row), nil
}

// GetAttachment returns one live attachment.
func (a *AttachmentAdapter) GetAttachment(ctx context.Context, id uuid.UUID) (attachments.Attachment, error) {
	row, err := a.q.GetAttachment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return attachments.Attachment{}, attachments.ErrNotFound
	}
	if err != nil {
		return attachments.Attachment{}, fmt.Errorf("getting attachment: %w", err)
	}
	return dbAttachmentToDomain(row), nil
}

// ListAttachmentsByEntity returns the entity's live attachments (metadata).
func (a *AttachmentAdapter) ListAttachmentsByEntity(ctx context.Context, entityType string, entityID uuid.UUID) ([]attachments.Attachment, error) {
	rows, err := a.q.ListAttachmentsByEntity(ctx, generated.ListAttachmentsByEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", err)
	}
	out := make([]attachments.Attachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, attachments.Attachment{
			ID:          r.ID,
			OrgID:       r.OrgID,
			EntityType:  r.EntityType,
			EntityID:    r.EntityID,
			Filename:    r.Filename,
			ContentType: r.ContentType,
			SizeBytes:   r.SizeBytes,
			CreatedBy:   r.CreatedBy,
			CreatedAt:   goTime(r.CreatedAt),
		})
	}
	return out, nil
}

// SoftDeleteAttachment marks an attachment deleted.
func (a *AttachmentAdapter) SoftDeleteAttachment(ctx context.Context, id uuid.UUID) error {
	if err := a.q.SoftDeleteAttachment(ctx, id); err != nil {
		return fmt.Errorf("deleting attachment: %w", err)
	}
	return nil
}

// dbAttachmentToDomain converts a generated.Attachment.
func dbAttachmentToDomain(a generated.Attachment) attachments.Attachment {
	return attachments.Attachment{
		ID:          a.ID,
		OrgID:       a.OrgID,
		EntityType:  a.EntityType,
		EntityID:    a.EntityID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		SizeBytes:   a.SizeBytes,
		ObjectKey:   a.ObjectKey,
		CreatedBy:   a.CreatedBy,
		CreatedAt:   goTime(a.CreatedAt),
	}
}
