// Package comments provides HTTP handlers for polymorphic entity comment endpoints.
package comments

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// Visibility is "internal" or "public", and OMITTING IT MEANS INTERNAL.
	//
	// A pointer rather than a string so that absent and "" are the same
	// thing and both mean internal. This is the safe direction and it is the
	// direction an old client — one written before the customer portal
	// existed — necessarily takes: it sends no visibility, and its comments
	// stay internal. Had the zero value meant public, shipping this would
	// have retroactively published every comment posted by a stale tab.
	Visibility *string `json:"visibility,omitempty"`
}

type commentResponse struct {
	ID         uuid.UUID  `json:"id"`
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	ParentID   *uuid.UUID `json:"parent_id,omitempty"`
	// AuthorID is null for a comment written by an external requester, who
	// has no users row by design (migration 044). AuthorName carries their
	// name in both cases.
	AuthorID   *uuid.UUID `json:"author_id"`
	AuthorName string     `json:"author_name"`
	// FromRequester marks a customer's own message, so the agent thread can
	// distinguish it without the client having to reason about which of two
	// author columns is populated.
	FromRequester bool `json:"from_requester"`
	// Visibility is "internal" or "public". Present on the AGENT surface
	// only: the portal's own serialiser has no such field, because the portal
	// query returns only public rows and there is nothing to disambiguate.
	Visibility string `json:"visibility"`
	Body       string `json:"body"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Comment visibility values. Mirrors migration 045's comments_visibility_valid.
const (
	visibilityInternal = "internal"
	visibilityPublic   = "public"
)

// resolveVisibility maps the request's optional visibility onto the stored
// value, defaulting to internal and refusing anything else.
//
// AN AGENT'S COMMENT IS INTERNAL UNLESS THEY SAY OTHERWISE. Going public is
// the explicit act, because the two mistakes are not symmetrical: an internal
// note the customer never sees is a delay, and a public note the agent
// thought was private is a disclosure that cannot be recalled.
func resolveVisibility(v *string) (string, bool) {
	if v == nil || *v == "" {
		return visibilityInternal, true
	}
	switch *v {
	case visibilityInternal, visibilityPublic:
		return *v, true
	default:
		return "", false
	}
}

// list returns all top-level comments for the entity whose ID is carried by
// the idParam URL parameter, reconciled against the space the URL names.
//
// THE SPACE IS PASSED TO THE QUERY, not assumed from the route. The middleware
// proved {spaceID} readable for this caller and proved nothing whatever about
// {entityID}, so on a bare entity id this returned the entire thread — internal
// notes included — on any item, ticket or page in any other space and any other
// organization. An entity outside the space now matches no row, which is the
// same empty list an entity that never existed has always produced.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, entityType, idParam string) {
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

	rows, err := h.queries.ListCommentsByEntity(r.Context(), generated.ListCommentsByEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
		SpaceID:    spaceID,
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
// URL parameter, after reconciling that entity against the space the URL names
// — see entityInSpace for why the capability check alone does not do it.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, entityType, idParam string) { //nolint:funlen,cyclop // HTTP handler; polymorphic entity dispatch + validation + capability check + notification dispatch
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
	// The capability was asked about the space; nothing has yet been asked
	// about the entity. Without this the same id that leaked a foreign thread
	// on the read side accepted a write onto it — a comment posted into a space
	// the author cannot see, visible to everyone who can.
	inSpace, err := h.entityInSpace(r.Context(), entityType, entityID, spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create comment")
		return
	}
	if !inSpace {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "entity not found")
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

	visibility, ok := resolveVisibility(req.Visibility)
	if !ok {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "visibility must be \"internal\" or \"public\"")
		return
	}
	// Only a ticket has a customer who could read a public comment.
	// Migration 045's comments_public_ticket_only would refuse this anyway;
	// catching it here turns a constraint violation into an answer that says
	// what was wrong.
	if visibility == visibilityPublic && entityType != "ticket" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "only ticket comments can be public")
		return
	}

	parentID := pgtype.UUID{}
	if req.ParentID != nil {
		// The entity above was reconciled against the space; the parent had
		// nothing checking it at all. Its only constraint is a bare foreign key
		// to the whole comments table, so a reply could be grafted onto a thread
		// on another entity in another organisation — and, worse, the difference
		// between a real parent id and an invented one was 201 versus a
		// foreign-key 500, which made the route an existence oracle over every
		// comment in the installation.
		belongs, berr := h.queries.CommentBelongsToEntity(r.Context(), generated.CommentBelongsToEntityParams{
			CommentID:  *req.ParentID,
			EntityType: entityType,
			EntityID:   entityID,
		})
		if berr != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create comment")
			return
		}
		if !belongs {
			// One answer for "no such comment" and "a comment somewhere you
			// cannot see", which is the whole point of checking it here rather
			// than letting the foreign key decide.
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "parent comment not found")
			return
		}
		parentID = pgtype.UUID{Bytes: *req.ParentID, Valid: true}
	}

	comment, err := h.queries.CreateComment(r.Context(), generated.CreateCommentParams{
		ID:         uuid.New(),
		EntityType: entityType,
		EntityID:   entityID,
		ParentID:   parentID,
		AuthorID:   pgtype.UUID{Bytes: claims.UserID, Valid: true},
		Body:       req.Content,
		Visibility: visibility,
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
		AuthorID:   goUUIDPtr(comment.AuthorID),
		AuthorName: authorName,
		Visibility: comment.Visibility,
		Body:       comment.Body,
		Content:    comment.Body,
		CreatedAt:  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  comment.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	})
}

// entityInSpace reports whether the entity a comment hangs off lives in the
// space the URL named.
//
// The route proves {spaceID} readable and proves nothing at all about
// {entityID}: RequireSpaceReadable authorises the space, and access.CapComment
// asks about the space too, so both checks are satisfied by a member of the
// space in the URL regardless of where the entity actually lives. Comments
// carry no space of their own — they are reachable exactly when the thing they
// are attached to is — so the reconciliation has to run against whichever table
// entity_type names, which is why this dispatches rather than taking one id.
//
// A miss is reported as absent, never as forbidden. The caller answers its
// ordinary 404, because a distinguishable "this exists but is not yours"
// discloses the same fact in a different shape.
func (h *Handler) entityInSpace(ctx context.Context, entityType string, entityID, spaceID uuid.UUID) (bool, error) {
	var err error
	switch entityType {
	case "ticket":
		_, err = h.queries.GetTicketInSpace(ctx, generated.GetTicketInSpaceParams{TicketID: entityID, SpaceID: spaceID})
	case "project_item":
		_, err = h.queries.GetProjectItemInSpace(ctx, generated.GetProjectItemInSpaceParams{ItemID: entityID, SpaceID: spaceID})
	case "page":
		_, err = h.queries.GetPageInSpace(ctx, generated.GetPageInSpaceParams{PageID: entityID, SpaceID: spaceID})
	default:
		// Unreachable through the router: entityType is a literal fixed by the
		// wrapper the route is bound to. A new wrapper that forgot to add its
		// arm here would otherwise be reconciled against nothing, so this fails
		// closed rather than returning true.
		return false, fmt.Errorf("comments: unknown entity type %q", entityType)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reconcile %s %s against space %s: %w", entityType, entityID, spaceID, err)
	}
	return true, nil
}

func rowToResponse(row generated.ListCommentsByEntityRow) commentResponse {
	var parentIDPtr *uuid.UUID
	if row.ParentID.Valid {
		id := uuid.UUID(row.ParentID.Bytes)
		parentIDPtr = &id
	}
	return commentResponse{
		ID:            row.ID,
		EntityType:    row.EntityType,
		EntityID:      row.EntityID,
		ParentID:      parentIDPtr,
		AuthorID:      goUUIDPtr(row.AuthorID),
		AuthorName:    row.AuthorName,
		FromRequester: row.AuthorRequesterID.Valid,
		Visibility:    row.Visibility,
		Body:          row.Body,
		Content:       row.Body,
		CreatedAt:     row.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     row.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
}

// goUUIDPtr converts a nullable database UUID to a Go pointer. author_id is
// nullable since migration 045 — a requester's message has no users row.
func goUUIDPtr(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := uuid.UUID(v.Bytes)
	return &id
}
