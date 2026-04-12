// Package tickets provides HTTP handlers for service desk endpoints.
package tickets

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
)

// Handler holds the dependencies for ticket HTTP handlers.
type Handler struct {
	svc *tickets.TicketService
}

// NewHandler creates a ticket Handler.
func NewHandler(svc *tickets.TicketService) *Handler {
	return &Handler{svc: svc}
}

// Routes returns a chi.Router with all ticket endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Get("/search", h.Search)
	r.Get("/kanban", h.Kanban)
	r.Get("/{ticketID}", h.Get)
	r.Patch("/{ticketID}", h.Update)
	r.Delete("/{ticketID}", h.Delete)
	r.Post("/{ticketID}/status", h.TransitionStatus)
	r.Post("/{ticketID}/assign", h.Assign)
	r.Delete("/{ticketID}/assign", h.Unassign)
	return r
}

type createTicketRequest struct {
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Priority    tickets.Priority `json:"priority"`
	AssigneeID  *uuid.UUID       `json:"assignee_id,omitempty"`
	Labels      []string         `json:"labels,omitempty"`
}

type updateTicketRequest struct {
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Priority    tickets.Priority `json:"priority"`
	Labels      []string         `json:"labels,omitempty"`
}

type transitionRequest struct {
	Status tickets.Status `json:"status"`
}

type assignRequest struct {
	AssigneeID uuid.UUID `json:"assignee_id"`
}

// List returns all tickets in a space.
//
// @Summary      List tickets
// @Description  Returns all tickets in the specified space.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   api.SwaggerTicketResponse  "List of tickets"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /spaces/{spaceID}/tickets [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	result, err := h.svc.ListBySpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list tickets")
		return
	}
	respond.JSON(w, http.StatusOK, result)
}

// Create creates a new ticket.
//
// @Summary      Create ticket
// @Description  Creates a new ticket in the specified space. Reporter is set from the JWT.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreateTicketRequest  true  "Ticket details"
// @Success      201      {object}  api.SwaggerTicketResponse       "Ticket created"
// @Failure      400      {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /spaces/{spaceID}/tickets [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req createTicketRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	ticket, err := h.svc.Create(r.Context(), tickets.CreateTicketParams{
		SpaceID:     spaceID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		ReporterID:  claims.UserID,
		AssigneeID:  req.AssigneeID,
		Labels:      req.Labels,
	})
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, ticket)
}

// Get returns a single ticket by ID.
//
// @Summary      Get ticket
// @Description  Returns a single ticket by ID.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {object}  api.SwaggerTicketResponse  "Ticket details"
// @Failure      400       {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401       {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      500       {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /spaces/{spaceID}/tickets/{ticketID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}

	ticket, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, ticket)
}

// Update modifies an existing ticket.
//
// @Summary      Update ticket
// @Description  Updates an existing ticket's title, description, priority, and labels.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID   path      string                          true  "Space ID (UUID)"
// @Param        ticketID  path      string                          true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerUpdateTicketRequest  true  "Updated fields"
// @Success      200       {object}  api.SwaggerTicketResponse       "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401       {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse        "Not found"
// @Failure      500       {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /spaces/{spaceID}/tickets/{ticketID} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}

	var req updateTicketRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}

	existing.Title = req.Title
	existing.Description = req.Description
	existing.Priority = req.Priority
	existing.Labels = req.Labels

	if err := h.svc.Update(r.Context(), existing); err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, existing)
}

// Delete soft-deletes a ticket.
//
// @Summary      Delete ticket
// @Description  Soft-deletes a ticket by ID.
// @Tags         tickets
// @Security     BearerAuth
// @Param        spaceID   path  string  true  "Space ID (UUID)"
// @Param        ticketID  path  string  true  "Ticket ID (UUID)"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/tickets/{ticketID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		handleTicketError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TransitionStatus changes the status of a ticket.
//
// @Summary      Transition ticket status
// @Description  Changes the status of a ticket (e.g. open -> in_progress -> resolved -> closed).
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID   path      string                          true  "Space ID (UUID)"
// @Param        ticketID  path      string                          true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerTransitionRequest    true  "New status"
// @Success      200       {object}  api.SwaggerTicketResponse       "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse        "Invalid status"
// @Failure      401       {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse        "Not found"
// @Failure      409       {object}  api.SwaggerErrorResponse        "Invalid transition"
// @Failure      500       {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /spaces/{spaceID}/tickets/{ticketID}/status [post]
func (h *Handler) TransitionStatus(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}

	var req transitionRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	ticket, err := h.svc.TransitionStatus(r.Context(), id, req.Status)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, ticket)
}

// Assign assigns a ticket to a user.
//
// @Summary      Assign ticket
// @Description  Assigns a ticket to a user by ID.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID   path      string                     true  "Space ID (UUID)"
// @Param        ticketID  path      string                     true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerAssignRequest   true  "Assignee"
// @Success      200       {object}  api.SwaggerTicketResponse  "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse   "Invalid request"
// @Failure      401       {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      409       {object}  api.SwaggerErrorResponse   "Already assigned"
// @Failure      500       {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /spaces/{spaceID}/tickets/{ticketID}/assign [post]
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}

	var req assignRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	ticket, err := h.svc.Assign(r.Context(), id, req.AssigneeID, nil)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, ticket)
}

// Unassign removes the assignee from a ticket.
//
// @Summary      Unassign ticket
// @Description  Removes the current assignee from a ticket.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {object}  api.SwaggerTicketResponse  "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401       {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      500       {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /spaces/{spaceID}/tickets/{ticketID}/assign [delete]
func (h *Handler) Unassign(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}

	ticket, err := h.svc.Unassign(r.Context(), id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, ticket)
}

// Search performs full-text search on tickets.
//
// @Summary      Search tickets
// @Description  Full-text search on tickets in a space. Requires query parameter 'q'.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true   "Space ID (UUID)"
// @Param        q        query     string  true   "Search query"
// @Param        limit    query     int     false  "Max results (1-200, default 50)"
// @Success      200      {array}   api.SwaggerTicketResponse  "Search results"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Missing query"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /spaces/{spaceID}/tickets/search [get]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "query parameter 'q' is required")
		return
	}

	limit := int32(50)
	if l := r.URL.Query().Get("limit"); l != "" {
		n, parseErr := strconv.ParseInt(l, 10, 32)
		if parseErr == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}

	result, err := h.svc.Search(r.Context(), spaceID, query, limit)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, result)
}

// Kanban returns the kanban board view grouped by status.
//
// @Summary      Kanban board
// @Description  Returns tickets grouped by status for kanban board display.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   api.SwaggerKanbanColumn    "Kanban columns"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /spaces/{spaceID}/tickets/kanban [get]
func (h *Handler) Kanban(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	board, err := h.svc.KanbanBoard(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to load kanban board")
		return
	}
	respond.JSON(w, http.StatusOK, board)
}

func ticketIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "ticketID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing ticket ID: %w", err)
	}
	return id, nil
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing space ID: %w", err)
	}
	return id, nil
}

func handleTicketError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tickets.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, tickets.ErrInvalidTransition):
		respond.Error(w, r, http.StatusConflict, respond.CodeInvalidTransition, err.Error())
	case errors.Is(err, tickets.ErrTitleRequired),
		errors.Is(err, tickets.ErrSpaceRequired),
		errors.Is(err, tickets.ErrReporterRequired),
		errors.Is(err, tickets.ErrInvalidPriority),
		errors.Is(err, tickets.ErrInvalidStatus),
		errors.Is(err, tickets.ErrEmptySearchQuery):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, tickets.ErrAlreadyAssigned):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("ticket operation failed: %v", err))
	}
}
