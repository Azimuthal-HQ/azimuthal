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
