// Package projects provides HTTP handlers for project tracking endpoints.
package projects

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// Handler holds the dependencies for project HTTP handlers.
type Handler struct {
	items     *projects.ItemService
	sprints   *projects.SprintService
	backlog   *projects.BacklogService
	roadmap   *projects.RoadmapService
	relations *projects.RelationService
	labels    *projects.LabelService
}

// NewHandler creates a project Handler.
func NewHandler(
	items *projects.ItemService,
	sprints *projects.SprintService,
	backlog *projects.BacklogService,
	roadmap *projects.RoadmapService,
	relations *projects.RelationService,
	labels *projects.LabelService,
) *Handler {
	return &Handler{
		items:     items,
		sprints:   sprints,
		backlog:   backlog,
		roadmap:   roadmap,
		relations: relations,
		labels:    labels,
	}
}

// Routes returns a chi.Router with all project endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Items
	r.Get("/items", h.ListItems)
	r.Post("/items", h.CreateItem)
	r.Get("/items/search", h.SearchItems)
	r.Get("/items/{itemID}", h.GetItem)
	r.Put("/items/{itemID}", h.UpdateItem)
	r.Delete("/items/{itemID}", h.DeleteItem)
	r.Post("/items/{itemID}/status", h.UpdateItemStatus)
	r.Post("/items/{itemID}/sprint", h.AssignToSprint)

	// Relations
	r.Get("/items/{itemID}/relations", h.ListRelations)
	r.Post("/items/{itemID}/relations", h.CreateRelation)
	r.Delete("/relations/{relationID}", h.DeleteRelation)

	// Sprints
	r.Get("/sprints", h.ListSprints)
	r.Post("/sprints", h.CreateSprint)
	r.Get("/sprints/active", h.GetActiveSprint)
	r.Get("/sprints/{sprintID}", h.GetSprint)
	r.Put("/sprints/{sprintID}", h.UpdateSprint)
	r.Post("/sprints/{sprintID}/start", h.StartSprint)
	r.Post("/sprints/{sprintID}/complete", h.CompleteSprint)
	r.Get("/sprints/{sprintID}/items", h.ListSprintItems)

	// Backlog
	r.Get("/backlog", h.GetBacklog)
	r.Post("/backlog/move-to-sprint", h.MoveToSprint)
	r.Post("/backlog/move-to-backlog", h.MoveToBacklog)

	// Roadmap
	r.Get("/roadmap", h.GetRoadmap)
	r.Get("/roadmap/overdue", h.GetOverdueItems)
	r.Get("/roadmap/sprints", h.GetSprintRoadmap)

	return r
}

// --- Request/response types ---

type createItemRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Kind        string     `json:"kind"`
	Priority    string     `json:"priority"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	SprintID    *uuid.UUID `json:"sprint_id,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

type updateItemRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    string     `json:"priority"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

type statusRequest struct {
	Status string `json:"status"`
}

type sprintAssignRequest struct {
	SprintID *uuid.UUID `json:"sprint_id"`
}

type moveToSprintRequest struct {
	ItemID   uuid.UUID `json:"item_id"`
	SprintID uuid.UUID `json:"sprint_id"`
}

type moveToBacklogRequest struct {
	ItemID uuid.UUID `json:"item_id"`
}

type createSprintRequest struct {
	Name     string     `json:"name"`
	Goal     string     `json:"goal"`
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
}

type updateSprintRequest struct {
	Name     string     `json:"name"`
	Goal     string     `json:"goal"`
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
}

type createRelationRequest struct {
	ToID uuid.UUID `json:"to_id"`
	Kind string    `json:"kind"`
}

type createLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// --- Item handlers ---

// ListItems returns all items in a space.
func (h *Handler) ListItems(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	items, err := h.items.ListItemsBySpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list items")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// CreateItem creates a new project item.
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {
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

	var req createItemRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	item := &projects.Item{
		SpaceID:     spaceID,
		Kind:        req.Kind,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		ReporterID:  claims.UserID,
		AssigneeID:  req.AssigneeID,
		SprintID:    req.SprintID,
		Labels:      req.Labels,
		DueAt:       req.DueAt,
	}

	created, err := h.items.CreateItem(r.Context(), item)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// GetItem returns a single item by ID.
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	item, err := h.items.GetItem(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, item)
}

// UpdateItem modifies an existing item.
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	var req updateItemRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	existing, err := h.items.GetItem(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}

	existing.Title = req.Title
	existing.Description = req.Description
	existing.Priority = req.Priority
	existing.AssigneeID = req.AssigneeID
	existing.Labels = req.Labels
	existing.DueAt = req.DueAt

	updated, err := h.items.UpdateItem(r.Context(), existing)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, updated)
}

// DeleteItem soft-deletes an item.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	if err := h.items.DeleteItem(r.Context(), id); err != nil {
		handleProjectError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateItemStatus changes the status of an item.
func (h *Handler) UpdateItemStatus(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	var req statusRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	item, err := h.items.UpdateItemStatus(r.Context(), id, req.Status)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, item)
}

// AssignToSprint assigns an item to a sprint.
func (h *Handler) AssignToSprint(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	var req sprintAssignRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if err := h.items.AssignToSprint(r.Context(), id, req.SprintID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item assigned to sprint"})
}

// SearchItems performs full-text search on items.
func (h *Handler) SearchItems(w http.ResponseWriter, r *http.Request) {
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

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		n, parseErr := strconv.Atoi(l)
		if parseErr == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	items, err := h.items.SearchItems(r.Context(), spaceID, query, limit)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// --- Relation handlers ---

// ListRelations returns all relations for an item.
func (h *Handler) ListRelations(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	rels, err := h.relations.ListRelations(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, rels)
}

// CreateRelation creates a new relation from an item.
func (h *Handler) CreateRelation(w http.ResponseWriter, r *http.Request) {
	fromID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req createRelationRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	rel := &projects.Relation{
		FromID:    fromID,
		ToID:      req.ToID,
		Kind:      req.Kind,
		CreatedBy: claims.UserID,
	}

	created, err := h.relations.CreateRelation(r.Context(), rel)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// DeleteRelation removes a relation.
func (h *Handler) DeleteRelation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "relationID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid relation ID")
		return
	}

	if err := h.relations.DeleteRelation(r.Context(), id); err != nil {
		handleProjectError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Sprint handlers ---

// ListSprints returns all sprints in a space.
func (h *Handler) ListSprints(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	sprints, err := h.sprints.ListSprintsBySpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list sprints")
		return
	}
	respond.JSON(w, http.StatusOK, sprints)
}

// CreateSprint creates a new sprint.
func (h *Handler) CreateSprint(w http.ResponseWriter, r *http.Request) {
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

	var req createSprintRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	sprint := &projects.Sprint{
		SpaceID:   spaceID,
		Name:      req.Name,
		Goal:      req.Goal,
		StartsAt:  req.StartsAt,
		EndsAt:    req.EndsAt,
		CreatedBy: claims.UserID,
	}

	created, err := h.sprints.CreateSprint(r.Context(), sprint)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// GetSprint returns a single sprint.
func (h *Handler) GetSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}

	sprint, err := h.sprints.GetSprint(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// UpdateSprint modifies an existing sprint.
func (h *Handler) UpdateSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}

	var req updateSprintRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	existing, err := h.sprints.GetSprint(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}

	existing.Name = req.Name
	existing.Goal = req.Goal
	existing.StartsAt = req.StartsAt
	existing.EndsAt = req.EndsAt

	updated, err := h.sprints.UpdateSprint(r.Context(), existing)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, updated)
}

// StartSprint transitions a sprint to active.
func (h *Handler) StartSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}

	sprint, err := h.sprints.StartSprint(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// CompleteSprint transitions a sprint to completed.
func (h *Handler) CompleteSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}

	sprint, err := h.sprints.CompleteSprint(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// ListSprintItems returns items assigned to a sprint.
func (h *Handler) ListSprintItems(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}

	items, err := h.backlog.GetSprintBacklog(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// GetActiveSprint returns the active sprint for a space.
func (h *Handler) GetActiveSprint(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	sprint, err := h.sprints.GetActiveSprint(r.Context(), spaceID)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// --- Backlog handlers ---

// GetBacklog returns the unassigned backlog for a space.
func (h *Handler) GetBacklog(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	items, err := h.backlog.GetBacklog(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get backlog")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// MoveToSprint moves an item from backlog to a sprint.
func (h *Handler) MoveToSprint(w http.ResponseWriter, r *http.Request) {
	var req moveToSprintRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if err := h.backlog.MoveToSprint(r.Context(), req.ItemID, req.SprintID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item moved to sprint"})
}

// MoveToBacklog moves an item from a sprint back to the backlog.
func (h *Handler) MoveToBacklog(w http.ResponseWriter, r *http.Request) {
	var req moveToBacklogRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if err := h.backlog.MoveToBacklog(r.Context(), req.ItemID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item moved to backlog"})
}

// --- Roadmap handlers ---

// GetRoadmap returns items with due dates in a range.
func (h *Handler) GetRoadmap(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	from, to, err := parseDateRange(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}

	items, err := h.roadmap.GetItemsDueInRange(r.Context(), spaceID, from, to)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get roadmap")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// GetOverdueItems returns items past their due date.
func (h *Handler) GetOverdueItems(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	items, err := h.roadmap.GetOverdueItems(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get overdue items")
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// GetSprintRoadmap returns sprints with their items for roadmap view.
func (h *Handler) GetSprintRoadmap(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	roadmap, err := h.roadmap.GetSprintRoadmap(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get sprint roadmap")
		return
	}
	respond.JSON(w, http.StatusOK, roadmap)
}

// --- Label handlers ---

// ListLabels returns all labels for an organization.
func (h *Handler) ListLabels(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	labels, err := h.labels.ListLabels(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list labels")
		return
	}
	respond.JSON(w, http.StatusOK, labels)
}

// CreateLabel creates a new label.
func (h *Handler) CreateLabel(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	var req createLabelRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	label := &projects.Label{
		OrgID: orgID,
		Name:  req.Name,
		Color: req.Color,
	}

	created, err := h.labels.CreateLabel(r.Context(), label)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// DeleteLabel removes a label.
func (h *Handler) DeleteLabel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "labelID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid label ID")
		return
	}

	if err := h.labels.DeleteLabel(r.Context(), id); err != nil {
		handleProjectError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---

func itemIDFromURL(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "itemID"))
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "spaceID"))
}

func orgIDFromURL(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "orgID"))
}

func parseDateRange(r *http.Request) (time.Time, time.Time, error) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	if fromStr == "" || toStr == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("'from' and 'to' query parameters are required (format: YYYY-MM-DD)")
	}

	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid 'from' date format, expected YYYY-MM-DD")
	}

	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid 'to' date format, expected YYYY-MM-DD")
	}

	return from, to, nil
}

func handleProjectError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, projects.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, projects.ErrInvalidTransition):
		respond.Error(w, r, http.StatusConflict, respond.CodeInvalidTransition, err.Error())
	case errors.Is(err, projects.ErrSprintActive):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, projects.ErrTitleRequired),
		errors.Is(err, projects.ErrNameRequired),
		errors.Is(err, projects.ErrInvalidPriority),
		errors.Is(err, projects.ErrInvalidKind),
		errors.Is(err, projects.ErrInvalidRelationKind),
		errors.Is(err, projects.ErrSelfRelation):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, projects.ErrLabelDuplicate):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("project operation failed: %v", err))
	}
}
