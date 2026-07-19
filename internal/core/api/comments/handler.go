// Package comments provides HTTP handlers for polymorphic entity comment endpoints.
package comments

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// NotificationEnqueuer is the subset of jobs.Queue used by the comments handler.
type NotificationEnqueuer interface {
	EnqueueNotification(ctx context.Context, args jobs.NotificationArgs) error
}

// Handler holds the dependencies for comment HTTP handlers.
type Handler struct {
	queries  *generated.Queries
	auditLog audit.Logger
	notifs   NotificationEnqueuer
}

// NewHandler creates a comment Handler.
func NewHandler(queries *generated.Queries) *Handler {
	return &Handler{queries: queries, auditLog: audit.NewLogger(), notifs: jobs.NoopNotificationEnqueuer{}}
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithNotificationEnqueuer attaches a notification enqueuer to the handler.
func (h *Handler) WithNotificationEnqueuer(n NotificationEnqueuer) *Handler {
	h.notifs = n
	return h
}

// Comment routes are registered per resource subtree (tickets, project
// items, wiki pages) so URLs hang off the resource's own path under the
// single org+space scoping convention. Each wrapper fixes the entity type
// and the URL parameter carrying the entity ID.

// ListTicketComments lists comments on a ticket.
//
// @Summary      List ticket comments
// @Description  Returns all top-level comments on a ticket.
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200  {array}   api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/comments [get]
func (h *Handler) ListTicketComments(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "ticket", "ticketID")
}

// CreateTicketComment adds a comment to a ticket.
//
// @Summary      Create ticket comment
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string                          true  "Organization ID (UUID)"
// @Param        spaceID   path      string                          true  "Space ID (UUID)"
// @Param        ticketID  path      string                          true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerCreateCommentRequest true  "Comment content"
// @Success      201  {object}  api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/comments [post]
func (h *Handler) CreateTicketComment(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, "ticket", "ticketID")
}

// ListItemComments lists comments on a project item.
//
// @Summary      List project item comments
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Project item ID (UUID)"
// @Success      200  {array}   api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments [get]
func (h *Handler) ListItemComments(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "project_item", "itemID")
}

// CreateItemComment adds a comment to a project item.
//
// @Summary      Create project item comment
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                          true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        itemID   path      string                          true  "Project item ID (UUID)"
// @Param        body     body      api.SwaggerCreateCommentRequest true  "Comment content"
// @Success      201  {object}  api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/comments [post]
func (h *Handler) CreateItemComment(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, "project_item", "itemID")
}

// ListPageComments lists comments on a wiki page.
//
// @Summary      List wiki page comments
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Wiki page ID (UUID)"
// @Success      200  {array}   api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments [get]
func (h *Handler) ListPageComments(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "page", "pageID")
}

// CreatePageComment adds a comment to a wiki page.
//
// @Summary      Create wiki page comment
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                          true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        pageID   path      string                          true  "Wiki page ID (UUID)"
// @Param        body     body      api.SwaggerCreateCommentRequest true  "Comment content"
// @Success      201  {object}  api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/comments [post]
func (h *Handler) CreatePageComment(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, "page", "pageID")
}

type createCommentRequest struct {
	Content  string     `json:"content"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
}

type commentResponse struct {
	ID         uuid.UUID  `json:"id"`
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	ParentID   *uuid.UUID `json:"parent_id,omitempty"`
	AuthorID   uuid.UUID  `json:"author_id"`
	AuthorName string     `json:"author_name"`
	Body       string     `json:"body"`
	Content    string     `json:"content"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at"`
}

// list returns all top-level comments for the entity whose ID is carried by
// the idParam URL parameter.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, entityType, idParam string) {
	entityID, err := uuid.Parse(chi.URLParam(r, idParam))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}

	rows, err := h.queries.ListCommentsByEntity(r.Context(), generated.ListCommentsByEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list comments")
		return
	}

	result := make([]commentResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToResponse(row))
	}
	respond.JSON(w, http.StatusOK, result)
}

// create adds a new comment to the entity whose ID is carried by the idParam
// URL parameter.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, entityType, idParam string) { //nolint:funlen // HTTP handler; validation + author lookup + notification dispatch requires length
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	entityID, err := uuid.Parse(chi.URLParam(r, idParam))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapComment, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req createCommentRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Content == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "content is required")
		return
	}

	parentID := pgtype.UUID{}
	if req.ParentID != nil {
		parentID = pgtype.UUID{Bytes: *req.ParentID, Valid: true}
	}

	comment, err := h.queries.CreateComment(r.Context(), generated.CreateCommentParams{
		ID:         uuid.New(),
		EntityType: entityType,
		EntityID:   entityID,
		ParentID:   parentID,
		AuthorID:   claims.UserID,
		Body:       req.Content,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create comment")
		return
	}

	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypeCommentCreated, ActorID: claims.UserID.String(),
		ResourceType: "comment", ResourceID: comment.ID.String(),
		Metadata: map[string]string{"entity_type": entityType, "entity_id": entityID.String()},
	})

	user, err := h.queries.GetUserByID(r.Context(), claims.UserID)
	authorName := ""
	if err == nil {
		authorName = user.DisplayName
	}

	var parentIDPtr *uuid.UUID
	if comment.ParentID.Valid {
		id := uuid.UUID(comment.ParentID.Bytes)
		parentIDPtr = &id
	}

	respond.JSON(w, http.StatusCreated, commentResponse{
		ID:         comment.ID,
		EntityType: comment.EntityType,
		EntityID:   comment.EntityID,
		ParentID:   parentIDPtr,
		AuthorID:   comment.AuthorID,
		AuthorName: authorName,
		Body:       comment.Body,
		Content:    comment.Body,
		CreatedAt:  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  comment.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	})
}

func rowToResponse(row generated.ListCommentsByEntityRow) commentResponse {
	var parentIDPtr *uuid.UUID
	if row.ParentID.Valid {
		id := uuid.UUID(row.ParentID.Bytes)
		parentIDPtr = &id
	}
	return commentResponse{
		ID:         row.ID,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		ParentID:   parentIDPtr,
		AuthorID:   row.AuthorID,
		AuthorName: row.AuthorName,
		Body:       row.Body,
		Content:    row.Body,
		CreatedAt:  row.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  row.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
}
