// Package comments provides HTTP handlers for item comment endpoints.
package comments

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Handler holds the dependencies for comment HTTP handlers.
type Handler struct {
	queries *generated.Queries
}

// NewHandler creates a comment Handler.
func NewHandler(queries *generated.Queries) *Handler {
	return &Handler{queries: queries}
}

// Routes returns a chi.Router with comment endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	return r
}

type createCommentRequest struct {
	Content string `json:"content"`
}

type commentResponse struct {
	ID         uuid.UUID `json:"id"`
	ItemID     string    `json:"item_id,omitempty"`
	AuthorID   uuid.UUID `json:"author_id"`
	AuthorName string    `json:"author_name"`
	Body       string    `json:"body"`
	Content    string    `json:"content"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
}

// List returns all comments for an item.
//
// @Summary      List comments
// @Description  Returns all comments for the specified item.
// @Tags         comments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200      {array}   api.SwaggerCommentResponse  "List of comments"
// @Failure      400      {object}  api.SwaggerErrorResponse    "Invalid item ID"
// @Failure      401      {object}  api.SwaggerErrorResponse    "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse    "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	itemID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	rows, err := h.queries.ListCommentsByItem(r.Context(), pgtype.UUID{Bytes: itemID, Valid: true})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list comments")
		return
	}

	result := make([]commentResponse, 0, len(rows))
	for _, row := range rows {
		itemIDStr := ""
		if row.ItemID.Valid {
			itemIDStr = uuid.UUID(row.ItemID.Bytes).String()
		}
		result = append(result, commentResponse{
			ID:         row.ID,
			ItemID:     itemIDStr,
			AuthorID:   row.AuthorID,
			AuthorName: row.AuthorName,
			Body:       row.Body,
			Content:    row.Body,
			CreatedAt:  row.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:  row.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
		})
	}

	respond.JSON(w, http.StatusOK, result)
}

// Create adds a new comment to an item.
//
// @Summary      Create comment
// @Description  Adds a new comment to the specified item. Author is set from the JWT.
// @Tags         comments
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                          true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        itemID   path      string                          true  "Item ID (UUID)"
// @Param        body     body      api.SwaggerCreateCommentRequest true  "Comment content"
// @Success      201      {object}  api.SwaggerCommentResponse      "Comment created"
// @Failure      400      {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
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

	comment, err := h.queries.CreateComment(r.Context(), generated.CreateCommentParams{
		ID:       uuid.New(),
		ItemID:   pgtype.UUID{Bytes: itemID, Valid: true},
		AuthorID: claims.UserID,
		Body:     req.Content,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create comment")
		return
	}

	// Fetch the author name for the response.
	user, err := h.queries.GetUserByID(r.Context(), claims.UserID)
	authorName := ""
	if err == nil {
		authorName = user.DisplayName
	}

	itemIDStr := ""
	if comment.ItemID.Valid {
		itemIDStr = uuid.UUID(comment.ItemID.Bytes).String()
	}

	respond.JSON(w, http.StatusCreated, commentResponse{
		ID:         comment.ID,
		ItemID:     itemIDStr,
		AuthorID:   comment.AuthorID,
		AuthorName: authorName,
		Body:       comment.Body,
		Content:    comment.Body,
		CreatedAt:  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  comment.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	})
}

func itemIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing item ID: %w", err)
	}
	return id, nil
}
