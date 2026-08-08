// Package notifications provides HTTP handlers for in-app notification endpoints.
package notifications

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Handler holds the dependencies for notification HTTP handlers.
type Handler struct {
	queries *generated.Queries
	// resolver supplies the caller's readable space set at render time.
	//
	// It is a constructor argument rather than a With* option on purpose. A
	// nil optional collaborator is exactly the dark-harness shape this
	// repository already got caught by: the tests would still pass and every
	// notification would render, which here means rendering titles nobody
	// checked. Required, so the compiler makes every wiring supply it.
	resolver *access.Resolver
}

// NewHandler creates a notification Handler.
func NewHandler(queries *generated.Queries, resolver *access.Resolver) *Handler {
	return &Handler{queries: queries, resolver: resolver}
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
		respond.Unmapped(w, r, "notification", "listing notifications", err)
		return
	}

	unread, err := h.queries.CountUnreadNotifications(r.Context(), claims.UserID)
	if err != nil {
		respond.Unmapped(w, r, "notification", "counting unread", err)
		return
	}

	items := make([]notificationResponse, 0, len(rows))
	visible := h.visibleSpaces(r, claims)
	for _, n := range rows {
		items = append(items, toResponse(n, visible))
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
		respond.Unmapped(w, r, "notification", "marking notification read", err)
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
		respond.Unmapped(w, r, "notification", "marking all notifications read", err)
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
	// Redacted marks a row whose entity the caller may no longer read. The row
	// survives so the unread count and mark-read keep working; what it named
	// does not.
	Redacted bool `json:"redacted,omitempty"`
}

// visibleSpaces is the caller's readable space set for one request.
//
// A nil map redacts every space-scoped row. That is the fail-closed direction
// and it is deliberate: the alternative on a resolution failure is to render
// titles nobody has checked, which is the defect this closes.
type visibleSpaces map[uuid.UUID]bool

// visibleSpaces resolves the caller's readable set.
//
// The notification routes are user-scoped and mount OUTSIDE /orgs/{orgID}, so
// ResolveAccess never ran and there is no resolution on the context — the org
// comes from the caller's own claims instead. Resolving per request rather than
// caching is what makes revocation immediate, which is the whole point here:
// the title was captured when the actor could see the entity, and nothing since
// asked whether they still can.
func (h *Handler) visibleSpaces(r *http.Request, claims *auth.Claims) visibleSpaces {
	if h.resolver == nil {
		return nil
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		return nil
	}
	res, err := h.resolver.Resolve(r.Context(), orgID, claims.UserID)
	if err != nil {
		// Includes ErrNotOrgMember. Fail closed rather than 500: the bell is a
		// peripheral surface and an unreadable title is a better outcome than
		// an error page, but an UNCHECKED title is not.
		return nil
	}
	ids := res.ReadableSpaceIDs()
	visible := make(visibleSpaces, len(ids))
	for _, id := range ids {
		visible[id] = true
	}
	return visible
}

type listNotificationsResponse struct {
	Notifications []notificationResponse `json:"notifications"`
	UnreadCount   int64                  `json:"unread_count"`
}

// toResponse renders one row, redacting what the caller may no longer read.
//
// The gate is HERE, in the one function every notification passes through,
// rather than in List — a second listing route added later cannot render an
// ungated row without changing this signature.
//
// migration 030 argued that denormalising entity_space_id "creates no
// permission oracle" because clicking navigates to the space-scoped detail
// page, which enforces authz. That is true of the LINK and was never true of
// the TITLE: the title is stored on the notification at enqueue time and was
// rendered from there unconditionally, so a grant revoked afterwards did not
// retract it.
//
// A row with no entity_space_id is NOT redacted. Those are the legacy rows
// migration 030 left unbackfilled and the org-level notifications that never
// named a space; blanking them would destroy the bell's history to close a gap
// they never had.
func toResponse(n generated.Notification, visible visibleSpaces) notificationResponse {
	if n.EntitySpaceID.Valid && !visible[uuid.UUID(n.EntitySpaceID.Bytes)] {
		return notificationResponse{
			ID:        n.ID,
			Kind:      n.Kind,
			Title:     "A notification you no longer have access to",
			IsRead:    n.IsRead,
			CreatedAt: n.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
			Redacted:  true,
		}
	}
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
