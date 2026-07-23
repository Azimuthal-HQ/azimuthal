// Package notifications provides HTTP handlers for in-app notification endpoints.
package notifications

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Handler holds the dependencies for notification HTTP handlers.
type Handler struct {
	queries *generated.Queries
}

// NewHandler creates a notification Handler.
func NewHandler(queries *generated.Queries) *Handler {
	return &Handler{queries: queries}
}

// Routes returns a chi.Router with all notification endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/read-all", h.ReadAll)
	r.Post("/{notificationID}/read", h.MarkRead)
	return r
}

// List returns the current user's notifications, unread first, paginated.
//
// @Summary      List notifications
// @Description  Returns the authenticated user's notifications, unread first. Paginated via limit/offset query params.
// @Tags         notifications
// @Produce      json
// @Param        limit   query  int  false  "Max results (default 20)"
// @Param        offset  query  int  false  "Offset for pagination"
// @Success      200  {object}  listNotificationsResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Security     BearerAuth
// @Router       /notifications [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) { //nolint:cyclop // HTTP handler complexity from query-param parsing and branching
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	limit := int32(20)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = int32(n) //nolint:gosec // G109 — value is bounded to [1,100] by the check above
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = int32(n) //nolint:gosec // G109 — value is non-negative int from strconv; safe for int32 pagination
		}
	}

	rows, err := h.queries.ListNotificationsByUser(r.Context(), generated.ListNotificationsByUserParams{
		UserID: claims.UserID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, fmt.Sprintf("listing notifications: %v", err))
		return
	}

	unread, err := h.queries.CountUnreadNotifications(r.Context(), claims.UserID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, fmt.Sprintf("counting unread: %v", err))
		return
	}

	items := make([]notificationResponse, 0, len(rows))
	for _, n := range rows {
		items = append(items, toResponse(n))
	}

	respond.JSON(w, http.StatusOK, listNotificationsResponse{
		Notifications: items,
		UnreadCount:   unread,
	})
}

// MarkRead marks a single notification as read.
//
// @Summary      Mark notification read
// @Description  Marks the specified notification as read. Only the owning user can mark their own notifications.
// @Tags         notifications
// @Produce      json
// @Param        notificationID  path  string  true  "Notification ID"
// @Success      204
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Security     BearerAuth
// @Router       /notifications/{notificationID}/read [post]
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	idStr := chi.URLParam(r, "notificationID")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid notification ID")
		return
	}

	// user_id scoping in the query ensures users can only mark their own.
	if err := h.queries.MarkNotificationRead(r.Context(), generated.MarkNotificationReadParams{
		ID:     id,
		UserID: claims.UserID,
	}); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, fmt.Sprintf("marking notification read: %v", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ReadAll marks all of the current user's notifications as read.
//
// @Summary      Mark all notifications read
// @Description  Marks every unread notification for the authenticated user as read.
// @Tags         notifications
// @Produce      json
// @Success      204
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Security     BearerAuth
// @Router       /notifications/read-all [post]
func (h *Handler) ReadAll(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	if err := h.queries.MarkAllNotificationsRead(r.Context(), claims.UserID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, fmt.Sprintf("marking all notifications read: %v", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type notificationResponse struct {
	ID            uuid.UUID  `json:"id"`
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	Body          *string    `json:"body,omitempty"`
	EntityKind    *string    `json:"entity_kind,omitempty"`
	EntityID      *uuid.UUID `json:"entity_id,omitempty"`
	EntitySpaceID *uuid.UUID `json:"entity_space_id,omitempty"`
	IsRead        bool       `json:"is_read"`
	CreatedAt     string     `json:"created_at"`
}

type listNotificationsResponse struct {
	Notifications []notificationResponse `json:"notifications"`
	UnreadCount   int64                  `json:"unread_count"`
}

func toResponse(n generated.Notification) notificationResponse {
	r := notificationResponse{
		ID:         n.ID,
		Kind:       n.Kind,
		Title:      n.Title,
		Body:       n.Body,
		EntityKind: n.EntityKind,
		IsRead:     n.IsRead,
		CreatedAt:  n.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}
	if n.EntityID.Valid {
		uid := uuid.UUID(n.EntityID.Bytes)
		r.EntityID = &uid
	}
	if n.EntitySpaceID.Valid {
		sid := uuid.UUID(n.EntitySpaceID.Bytes)
		r.EntitySpaceID = &sid
	}
	return r
}
