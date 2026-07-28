package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// Beacon queues (P4 PR-B). Space-scoped, mounted under
// /orgs/{orgID}/spaces/{spaceID}/queues behind the space guards, so READING a
// queue needs only space-readability — which is exactly the audience a queue
// has (visibility 'space').
//
// WHERE CapManageQueue LANDS. Every mutation — create, edit, reorder, delete,
// and the one-click default set — is gated in-handler on
// access.Can(ctx, CapManageQueue, spaceID). ADR-0007 puts manage_queue at the
// `agent` role, so the persona that must be REFUSED to prove the gate is a
// CONTRIBUTOR: a viewer is already refused upstream by the write floor, and a
// test using one would assert the middleware while passing with this gate
// deleted. See queue_capability_integration_test.go.
//
// CapReadAggregates remains unplaced. It most likely belongs to P5 dashboards;
// nothing here uses it, and inventing a use for it to tidy the capability
// table would be the wrong kind of completeness.

// QueueRoutes returns the queue router, mounted at
// /orgs/{orgID}/spaces/{spaceID}/queues. The space guards are applied by the
// router, as they are for every other space-scoped family.
func (h *Handler) QueueRoutes(shareResolver func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.ListQueues)
	r.Post("/", h.CreateQueue)
	r.Post("/defaults", h.CreateDefaultQueues)
	r.Put("/order", h.ReorderQueues)
	r.Patch("/{queueID}", h.UpdateQueue)
	r.Delete("/{queueID}", h.DeleteQueue)
	// Results carry share resolution for the same reason the saved-view
	// results route does: a queue is a saved view, and its results union the
	// caller's shared entities.
	r.With(shareResolver).Get("/{queueID}/results", h.QueueResults)
	return r
}

type queueResponse struct {
	ID          uuid.UUID       `json:"id"`
	SpaceID     uuid.UUID       `json:"space_id"`
	Position    int32           `json:"position"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Query       json.RawMessage `json:"query"`
	OwnerID     uuid.UUID       `json:"owner_id"`
	OwnerName   string          `json:"owner_name,omitempty"`
	// CanManage tells the UI whether to render the edit and reorder controls,
	// so it does not have to reproduce the capability rule client-side.
	CanManage bool `json:"can_manage"`
}

func toQueueResponse(v views.View, canManage bool) (queueResponse, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return queueResponse{}, fmt.Errorf("encoding the queue's filter document: %w", err)
	}
	q := queueResponse{
		ID: v.ID, Name: v.Name, Description: v.Description, Query: raw,
		OwnerID: v.OwnerID, OwnerName: v.OwnerName, CanManage: canManage,
	}
	if v.SpaceID != nil {
		q.SpaceID = *v.SpaceID
	}
	if v.Position != nil {
		q.Position = *v.Position
	}
	return q, nil
}

// queueContext parses the two path ids. Space readability is already
// established by the middleware chain; this only resolves identifiers.
func queueContext(w http.ResponseWriter, r *http.Request) (orgID, spaceID uuid.UUID, ok bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, uuid.Nil, false
	}
	spaceID, err = uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, spaceID, true
}

// requireManageQueue is the capability gate. It is the only place
// CapManageQueue is checked, so there is one answer to "who may change a
// space's queues".
func requireManageQueue(w http.ResponseWriter, r *http.Request, spaceID uuid.UUID) bool {
	if access.Can(r.Context(), access.CapManageQueue, spaceID) {
		return true
	}
	// 403, not 404: the caller can read the space and can see its queues, so
	// refusing the mutation leaks nothing that reading the list did not.
	respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden,
		"managing this space's queues needs the agent role")
	return false
}

// ListQueues returns a space's queues in order.
//
// @Summary      List a space's queues
// @Description  Queues are saved views bound to the space. Reading needs only space-readability, which is the audience a queue has.
// @Tags         queues
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                    true  "Organization ID (UUID)"
// @Param        spaceID  path      string                    true  "Space ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "Queues in display order"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Space not found or not readable"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues [get]
func (h *Handler) ListQueues(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok {
		return
	}
	rows, err := h.queues.List(r.Context(), orgID, spaceID)
	if err != nil {
		h.fail(w, r, err, "could not load this space's queues")
		return
	}
	canManage := access.Can(r.Context(), access.CapManageQueue, spaceID)
	out := make([]queueResponse, 0, len(rows))
	for _, v := range rows {
		resp, err := toQueueResponse(v, canManage)
		if err != nil {
			h.fail(w, r, err, "could not load this space's queues")
			return
		}
		out = append(out, resp)
	}
	respond.JSON(w, http.StatusOK, map[string]any{"queues": out, "can_manage": canManage})
}

// CreateQueue adds a queue to the space.
//
// @Summary      Create a queue
// @Description  Requires manage_queue on the space (the agent role).
// @Tags         queues
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                    true  "Organization ID (UUID)"
// @Param        spaceID  path      string                    true  "Space ID (UUID)"
// @Param        body     body      map[string]interface{}    true  "Queue"
// @Success      201      {object}  map[string]interface{}    "Created queue"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_queue required"
// @Failure      409      {object}  api.SwaggerErrorResponse  "A queue of that name exists in this space"
// @Failure      422      {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues [post]
func (h *Handler) CreateQueue(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok || !requireManageQueue(w, r, spaceID) {
		return
	}
	draft, ok := h.draft(w, r)
	if !ok {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "not authenticated")
		return
	}
	v, err := h.queues.Create(r.Context(), orgID, spaceID, claims.UserID, draft)
	if err != nil {
		h.fail(w, r, err, "could not create the queue")
		return
	}
	resp, err := toQueueResponse(v, true)
	if err != nil {
		h.fail(w, r, err, "could not create the queue")
		return
	}
	respond.JSON(w, http.StatusCreated, resp)
}

// CreateDefaultQueues creates whichever of the four defaults are missing.
//
// @Summary      Create the default queues
// @Description  Idempotent: creates only the defaults this space does not already have, and reports how many were added. Requires manage_queue.
// @Tags         queues
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                    true  "Organization ID (UUID)"
// @Param        spaceID  path      string                    true  "Space ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "How many queues were created"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_queue required"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues/defaults [post]
func (h *Handler) CreateDefaultQueues(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok || !requireManageQueue(w, r, spaceID) {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "not authenticated")
		return
	}
	n, err := h.queues.CreateDefaults(r.Context(), orgID, spaceID, claims.UserID)
	if err != nil {
		h.fail(w, r, err, "could not create the default queues")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"created": n})
}

// ReorderQueues sets the whole order at once.
//
// @Summary      Reorder a space's queues
// @Description  The body must list every live queue in the space exactly once. Applied in one transaction. Requires manage_queue.
// @Tags         queues
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                    true  "Organization ID (UUID)"
// @Param        spaceID  path      string                    true  "Space ID (UUID)"
// @Param        body     body      map[string]interface{}    true  "Ordered queue ids"
// @Success      204      "Reordered"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_queue required"
// @Failure      422      {object}  api.SwaggerErrorResponse  "Not a permutation of this space's queues"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues/order [put]
func (h *Handler) ReorderQueues(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok || !requireManageQueue(w, r, spaceID) {
		return
	}
	var body struct {
		QueueIDs []uuid.UUID `json:"queue_ids"`
	}
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "could not read the request")
		return
	}
	if err := h.queues.Reorder(r.Context(), orgID, spaceID, body.QueueIDs); err != nil {
		h.fail(w, r, err, "could not reorder the queues")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateQueue changes a queue's name, description and query.
//
// @Summary      Update a queue
// @Description  Requires manage_queue. Position is changed by the reorder endpoint, never here.
// @Tags         queues
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                    true  "Organization ID (UUID)"
// @Param        spaceID  path      string                    true  "Space ID (UUID)"
// @Param        queueID  path      string                    true  "Queue ID (UUID)"
// @Param        body     body      map[string]interface{}    true  "Queue"
// @Success      200      {object}  map[string]interface{}    "Updated queue"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_queue required"
// @Failure      404      {object}  api.SwaggerErrorResponse  "No such queue in this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues/{queueID} [patch]
func (h *Handler) UpdateQueue(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok || !requireManageQueue(w, r, spaceID) {
		return
	}
	queueID, ok := uuidParam(w, r, "queueID")
	if !ok {
		return
	}
	draft, ok := h.draft(w, r)
	if !ok {
		return
	}
	v, err := h.queues.Update(r.Context(), orgID, spaceID, queueID, draft)
	if err != nil {
		h.fail(w, r, err, "could not update the queue")
		return
	}
	resp, err := toQueueResponse(v, true)
	if err != nil {
		h.fail(w, r, err, "could not update the queue")
		return
	}
	respond.JSON(w, http.StatusOK, resp)
}

// DeleteQueue removes a queue from the space.
//
// @Summary      Delete a queue
// @Description  Requires manage_queue.
// @Tags         queues
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        queueID  path  string  true  "Queue ID (UUID)"
// @Success      204      "Deleted"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_queue required"
// @Failure      404      {object}  api.SwaggerErrorResponse  "No such queue in this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues/{queueID} [delete]
func (h *Handler) DeleteQueue(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok || !requireManageQueue(w, r, spaceID) {
		return
	}
	queueID, ok := uuidParam(w, r, "queueID")
	if !ok {
		return
	}
	if err := h.queues.Delete(r.Context(), orgID, spaceID, queueID); err != nil {
		h.fail(w, r, err, "could not delete the queue")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// QueueResults resolves a queue for the calling agent.
//
// @Summary      Run a queue
// @Description  Resolves per viewer, exactly as a saved view does — so an "Assigned to me" queue means each agent's own work.
// @Tags         queues
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                    true   "Organization ID (UUID)"
// @Param        spaceID  path      string                    true   "Space ID (UUID)"
// @Param        queueID  path      string                    true   "Queue ID (UUID)"
// @Param        cursor   query     string                    false  "Opaque cursor from a previous page"
// @Param        limit    query     int                       false  "Page size (default 50, max 200)"
// @Success      200      {object}  map[string]interface{}    "Results, next_cursor and has_more"
// @Failure      404      {object}  api.SwaggerErrorResponse  "No such queue in this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/queues/{queueID}/results [get]
func (h *Handler) QueueResults(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := queueContext(w, r)
	if !ok {
		return
	}
	queueID, ok := uuidParam(w, r, "queueID")
	if !ok {
		return
	}
	q, err := h.queues.Get(r.Context(), orgID, spaceID, queueID)
	if err != nil {
		h.fail(w, r, err, "could not run the queue")
		return
	}
	// The SAME resolution path an ordinary saved view takes. A second one here
	// is the drift shared-surfaces exists to prevent.
	page, err := h.svc.Preview(r.Context(), orgID, q.Query, viewerFrom(r),
		r.URL.Query().Get("cursor"), pageLimit(r))
	if err != nil {
		h.fail(w, r, err, "could not run the queue")
		return
	}
	respondPage(w, page)
}

// failQueue maps the queue-specific errors. Folded into the shared fail() by
// the switch below rather than duplicated.
func queueErrorStatus(err error) (int, respond.ErrorCode, string, bool) {
	switch {
	case errors.Is(err, views.ErrQueueNotInSpace), errors.Is(err, views.ErrNotAQueue):
		return http.StatusNotFound, respond.CodeNotFound, "queue not found in this space", true
	case errors.Is(err, views.ErrQueueNameTaken):
		return http.StatusConflict, respond.CodeConflict, views.ErrQueueNameTaken.Error(), true
	case errors.Is(err, views.ErrReorderMismatch):
		return http.StatusUnprocessableEntity, respond.CodeValidation, views.ErrReorderMismatch.Error(), true
	case errors.Is(err, views.ErrQueueModule):
		return http.StatusUnprocessableEntity, respond.CodeValidation, views.ErrQueueModule.Error(), true
	}
	return 0, "", "", false
}
