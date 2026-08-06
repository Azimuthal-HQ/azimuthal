// Package relations provides HTTP handlers for the polymorphic entity
// relation endpoints.
//
// The addressing copies the comments routes — the repository's existing answer
// to "one satellite table, several entity types". Each entity subtree mounts
// the same generic core through a wrapper that fixes the entity type and the
// URL parameter carrying the entity id, so the FROM side of a relation comes
// from which route was hit, never from the request body. Registration lives in
// router.go beside the comment routes, under the same spaceGuard +
// readableGuard + writeFloor chain the projects-mounted routes always had —
// moving the mount must not move the write floor.
package relations

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// Handler holds the dependencies for relation HTTP handlers.
type Handler struct {
	svc *projects.RelationService
}

// NewHandler creates a relation Handler.
func NewHandler(svc *projects.RelationService) *Handler {
	return &Handler{svc: svc}
}

type createRelationRequest struct {
	ToID   uuid.UUID `json:"to_id"`
	ToType string    `json:"to_type"`
	Kind   string    `json:"kind"`
}

// ListItemRelations lists relations touching a project item.
//
// @Summary      List project item relations
// @Description  Returns all relations touching a project item, in both directions, with far sides resolved only where the caller may read them.
// @Tags         relations
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Project item ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations [get]
func (h *Handler) ListItemRelations(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, projects.EntityTypeProjectItem, "itemID")
}

// CreateItemRelation creates a relation from a project item.
//
// @Summary      Create project item relation
// @Description  Creates a relation from a project item to another entity (project item, ticket, or page).
// @Tags         relations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                            true  "Organization ID (UUID)"
// @Param        spaceID  path      string                            true  "Space ID (UUID)"
// @Param        itemID   path      string                            true  "Project item ID (UUID)"
// @Param        body     body      api.SwaggerCreateRelationRequest  true  "Relation details"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations [post]
func (h *Handler) CreateItemRelation(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, projects.EntityTypeProjectItem, "itemID")
}

// ListTicketRelations lists relations touching a ticket.
//
// @Summary      List ticket relations
// @Description  Returns all relations touching a ticket, in both directions, with far sides resolved only where the caller may read them.
// @Tags         relations
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {array}   map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/relations [get]
func (h *Handler) ListTicketRelations(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, projects.EntityTypeTicket, "ticketID")
}

// CreateTicketRelation creates a relation from a ticket.
//
// @Summary      Create ticket relation
// @Description  Creates a relation from a ticket to another entity (project item, ticket, or page).
// @Tags         relations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string                            true  "Organization ID (UUID)"
// @Param        spaceID   path      string                            true  "Space ID (UUID)"
// @Param        ticketID  path      string                            true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerCreateRelationRequest  true  "Relation details"
// @Success      201       {object}  map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      404       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/relations [post]
func (h *Handler) CreateTicketRelation(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, projects.EntityTypeTicket, "ticketID")
}

// ListPageRelations lists relations touching a wiki page.
//
// @Summary      List wiki page relations
// @Description  Returns all relations touching a wiki page, in both directions, with far sides resolved only where the caller may read them.
// @Tags         relations
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Wiki page ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/relations [get]
func (h *Handler) ListPageRelations(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, projects.EntityTypePage, "pageID")
}

// CreatePageRelation creates a relation from a wiki page.
//
// @Summary      Create wiki page relation
// @Description  Creates a relation from a wiki page to another entity (project item, ticket, or page).
// @Tags         relations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                            true  "Organization ID (UUID)"
// @Param        spaceID  path      string                            true  "Space ID (UUID)"
// @Param        pageID   path      string                            true  "Wiki page ID (UUID)"
// @Param        body     body      api.SwaggerCreateRelationRequest  true  "Relation details"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/relations [post]
func (h *Handler) CreatePageRelation(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, projects.EntityTypePage, "pageID")
}

// DeleteRelation removes a relation the caller's space touches.
//
// One route, not one per entity subtree: a relation is addressed by its own
// id, not through an endpoint, and DeleteEntityRelationInSpace already matches
// EITHER endpoint of any type against the URL's space. Three spellings of the
// same delete would be surface without meaning.
//
// @Summary      Delete a relation
// @Description  Removes a relation one of whose endpoints lives in the named space. Answers 204 whether or not a row matched.
// @Tags         relations
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID (UUID)"
// @Param        spaceID     path      string  true  "Space ID (UUID)"
// @Param        relationID  path      string  true  "Relation ID (UUID)"
// @Success      204         "No Content"
// @Failure      400         {object}  api.SwaggerErrorResponse
// @Failure      401         {object}  api.SwaggerErrorResponse
// @Failure      500         {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/relations/{relationID} [delete]
func (h *Handler) DeleteRelation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "relationID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid relation ID")
		return
	}
	// The route proved {spaceID} readable and {relationID} nothing at all, and
	// this is the only place the two are reconciled: a relation carries no
	// space of its own, and neither of its endpoints carries a foreign key.
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	if err := h.svc.DeleteRelation(r.Context(), id, spaceID); err != nil {
		handleRelationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// list serves the entity whose id the idParam URL parameter carries,
// reconciled against the space the URL names — the service answers an entity
// outside that space with the same empty list an absent entity gets.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, entityType, idParam string) {
	entityID, err := uuid.Parse(chi.URLParam(r, idParam))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	readable, ok := readableSpaceIDs(w, r)
	if !ok {
		return
	}

	rels, err := h.svc.ListRelationsInSpace(r.Context(), entityID, entityType, spaceID, readable)
	if err != nil {
		handleRelationError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, rels)
}

// create makes the entity the idParam URL parameter names the FROM side of a
// new relation. The from type is fixed by the wrapper the route is bound to —
// a caller cannot name it — and the service still validates it, because "the
// handler constrains it" is a property of these call sites, not a guarantee
// the domain may lean on.
//
// The two readability checks keep their deliberate asymmetry: the service
// resolves the near side against the URL's space ALONE — the space the caller
// claimed to be acting in, where a wider set would let read access somewhere
// else authorise a write here — and the far side against the caller's whole
// readable set, because linking across spaces is the feature.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, entityType, idParam string) {
	entityID, err := uuid.Parse(chi.URLParam(r, idParam))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	readable, ok := readableSpaceIDs(w, r)
	if !ok {
		return
	}

	var req createRelationRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// An omitted to_type has meant project_item since before the field
	// existed on the wire; clients that predate page targets still get the
	// relation they always got.
	toType := req.ToType
	if toType == "" {
		toType = projects.EntityTypeProjectItem
	}
	rel := &projects.NewRelation{
		FromID:    entityID,
		FromType:  entityType,
		ToID:      req.ToID,
		ToType:    toType,
		Kind:      req.Kind,
		CreatedBy: claims.UserID,
	}

	created, err := h.svc.CreateRelation(r.Context(), rel, spaceID, readable)
	if err != nil {
		handleRelationError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// handleRelationError maps relation domain errors onto HTTP, rendering each
// error exactly as the projects handler always rendered it so the moved mount
// changes no byte of any response.
func handleRelationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, projects.ErrNotFound),
		errors.Is(err, projects.ErrRelationTargetNotFound):
		// Two sentinels, one rendering, zero distinguishability — the near
		// side the route named and a far target the caller cannot resolve are
		// both an ordinary 404, and within each sentinel "does not exist" and
		// "exists where you cannot read" are already the same value.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, projects.ErrInvalidRelationKind),
		errors.Is(err, projects.ErrInvalidEntityType),
		errors.Is(err, projects.ErrSelfRelation):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	default:
		slog.Error("unmapped handler error",
			"surface", "relation",
			"error", err,
			"request_id", respond.RequestIDFromContext(r.Context()),
		)
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "relation operation failed")
	}
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing space ID: %w", err)
	}
	return id, nil
}

// readableSpaceIDs returns the caller's resolved readable set, or writes a 404
// and reports false when no resolution ran.
//
// A missing resolution denies rather than degrades, exactly as it does in the
// projects handler this moved from: carrying on with an empty set would make
// an unwired route answer 200 with plausible-looking rows instead of
// announcing that its authorization never executed.
func readableSpaceIDs(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool) {
	res := access.FromContext(r.Context())
	if res == nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return nil, false
	}
	return res.ReadableSpaceIDs(), true
}
