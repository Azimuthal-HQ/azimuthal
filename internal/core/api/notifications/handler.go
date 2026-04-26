// Package notifications provides HTTP handlers for the in-app notification
// surface: list, mark-read, mark-all-read.
package notifications

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
)

// Handler exposes notification routes scoped to the authenticated user.
// All routes are owner-scoped — there is no admin/cross-user view.
type Handler struct {
	svc *notifications.Service
}

// NewHandler creates a notifications Handler backed by svc.
func NewHandler(svc *notifications.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns a chi.Router with all notification endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/read-all", h.MarkAllRead)
	r.Post("/{notificationID}/read", h.MarkRead)
	return r
}

type listResponse struct {
	Notifications []*notifications.Notification `json:"notifications"`
	UnreadCount   int64                         `json:"unread_count"`
}

// List returns the authenticated user's notifications, unread first via
// frontend ordering of the unread_count badge.
//
// @Summary      List notifications
// @Description  Returns the current user's notifications ordered most-recent first, plus an unread count.
// @Tags         notifications
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int  false  "Page size (1-200, default 50)"
// @Param        offset  query     int  false  "Offset (default 0)"
// @Success      200     {object}  api.SwaggerNotificationListResponse  "Notification list"
// @Failure      401     {object}  api.SwaggerErrorResponse             "Not authenticated"
// @Failure      500     {object}  api.SwaggerErrorResponse             "Internal error"
// @Router       /notifications [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	limit := int32(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	offset := int32(0)
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	rows, err := h.svc.List(r.Context(), claims.UserID, limit, offset)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list notifications")
		return
	}
	count, err := h.svc.CountUnread(r.Context(), claims.UserID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to count notifications")
		return
	}
	respond.JSON(w, http.StatusOK, listResponse{Notifications: rows, UnreadCount: count})
}

// MarkRead clears the unread flag on a single notification owned by the
// authenticated user. Returns 204 even when the notification does not
// exist or is owned by another user — the operation is idempotent and
// owner-scoped at the SQL level.
//
// @Summary      Mark notification read
// @Description  Marks a single notification as read for the current user.
// @Tags         notifications
// @Param        notificationID  path  string  true  "Notification ID (UUID)"
// @Security     BearerAuth
// @Success      204             "Marked read"
// @Failure      400             {object}  api.SwaggerErrorResponse  "Invalid id"
// @Failure      401             {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500             {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /notifications/{notificationID}/read [post]
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "notificationID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid notification id")
		return
	}

	if err := h.svc.MarkRead(r.Context(), claims.UserID, id); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to mark read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MarkAllRead clears the unread flag on every notification owned by the
// authenticated user.
//
// @Summary      Mark all notifications read
// @Description  Marks every unread notification for the current user as read.
// @Tags         notifications
// @Security     BearerAuth
// @Success      204  "Marked all read"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /notifications/read-all [post]
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	if err := h.svc.MarkAllRead(r.Context(), claims.UserID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to mark all read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
