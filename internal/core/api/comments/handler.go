// Package comments provides HTTP handlers for polymorphic entity comment endpoints.
package comments

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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

// Routes returns a chi.Router with comment endpoints mounted.
// The router is mounted under paths that include {entityType}/{entityID}.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	return r
}

type createCommentRequest struct {
	Content  string    `json:"content"`
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

// entityTypeFromURL extracts {entityType} URL param and maps to internal entity_type value.
// URL param values: "tickets" → "ticket", "project-items" → "project_item", "wiki" → "page".
func entityTypeFromURL(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "entityType")
	switch raw {
	case "tickets":
		return "ticket", nil
	case "project-items":
		return "project_item", nil
	case "wiki":
		return "page", nil
	default:
		return "", fmt.Errorf("unknown entity type %q", raw)
	}
}

// entityIDFromURL extracts {entityID} from the URL.
func entityIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "entityID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing entity ID: %w", err)
	}
	return id, nil
}

// itemIDFromURL extracts {itemID} — retained for the legacy deprecated route.
func itemIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing item ID: %w", err)
	}
	return id, nil
}

// List returns all top-level comments for the entity.
//
// @Summary      List comments
// @Description  Returns all top-level comments for a ticket, project item, or wiki page.
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID      path      string  true  "Organization ID (UUID)"
// @Param        spaceID    path      string  true  "Space ID (UUID)"
// @Param        entityType path      string  true  "Entity type (tickets, project-items, wiki)"
// @Param        entityID   path      string  true  "Entity ID (UUID)"
// @Success      200  {array}   api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/{entityType}/{entityID}/comments [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityType, entityID, ok := h.extractEntityFromURL(w, r)
	if !ok {
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

// ListLegacy is the deprecated item-scoped comment list endpoint.
// Returns comments with a Deprecation header pointing to the new route.
//
// @Summary      List comments (deprecated)
// @Description  Deprecated: use /{entityType}/{entityID}/comments instead.
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200  {array}   api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments [get]
func (h *Handler) ListLegacy(w http.ResponseWriter, r *http.Request) {
	itemID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `</api/v1/orgs/{orgID}/spaces/{spaceID}/project-items/{itemID}/comments>; rel="successor-version"`)

	// Try project_item first, then ticket.
	rows, err := h.queries.ListCommentsByEntity(r.Context(), generated.ListCommentsByEntityParams{
		EntityType: "project_item",
		EntityID:   itemID,
	})
	if err != nil || len(rows) == 0 {
		rows, err = h.queries.ListCommentsByEntity(r.Context(), generated.ListCommentsByEntityParams{
			EntityType: "ticket",
			EntityID:   itemID,
		})
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list comments")
			return
		}
	}

	result := make([]commentResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToResponse(row))
	}
	respond.JSON(w, http.StatusOK, result)
}

// Create adds a new comment to an entity.
//
// @Summary      Create comment
// @Description  Adds a new comment to a ticket, project item, or wiki page.
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID      path      string                          true  "Organization ID (UUID)"
// @Param        spaceID    path      string                          true  "Space ID (UUID)"
// @Param        entityType path      string                          true  "Entity type (tickets, project-items, wiki)"
// @Param        entityID   path      string                          true  "Entity ID (UUID)"
// @Param        body       body      api.SwaggerCreateCommentRequest true  "Comment content"
// @Success      201  {object}  api.SwaggerCommentResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/{entityType}/{entityID}/comments [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	entityType, entityID, ok := h.extractEntityFromURL(w, r)
	if !ok {
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

// CreateLegacy is the deprecated item-scoped create comment endpoint.
func (h *Handler) CreateLegacy(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	itemID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
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

	w.Header().Set("Deprecation", "true")

	// Detect entity type by checking which table the item belongs to.
	entityType := "project_item"
	if _, terr := h.queries.GetTicketByID(r.Context(), itemID); terr == nil {
		entityType = "ticket"
	}

	comment, err := h.queries.CreateComment(r.Context(), generated.CreateCommentParams{
		ID:         uuid.New(),
		EntityType: entityType,
		EntityID:   itemID,
		AuthorID:   claims.UserID,
		Body:       req.Content,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create comment")
		return
	}

	user, _ := h.queries.GetUserByID(r.Context(), claims.UserID) //nolint:errcheck
	authorName := ""
	if user.ID != uuid.Nil {
		authorName = user.DisplayName
	}

	respond.JSON(w, http.StatusCreated, commentResponse{
		ID:         comment.ID,
		EntityType: comment.EntityType,
		EntityID:   comment.EntityID,
		AuthorID:   comment.AuthorID,
		AuthorName: authorName,
		Body:       comment.Body,
		Content:    comment.Body,
		CreatedAt:  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  comment.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	})
}

func (h *Handler) extractEntityFromURL(w http.ResponseWriter, r *http.Request) (string, uuid.UUID, bool) {
	entityType, err := entityTypeFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity type")
		return "", uuid.Nil, false
	}
	entityID, err := entityIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return "", uuid.Nil, false
	}
	return entityType, entityID, true
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
