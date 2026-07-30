// Package workflows provides HTTP handlers for workflow engine endpoints.
package workflows

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/tiergate"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Handler holds dependencies for workflow HTTP handlers.
type Handler struct {
	q    *generated.Queries
	repo workflow.Repository
	eng  workflow.Engine
	// tiers and applier gate and write a transition exactly as the /status
	// routes do. Both engine-backed routes below go through them, so no route
	// is a way around a configured guard. Nil-able, so covered by
	// TestHarness_NoDarkDependencies.
	tiers   *tiergate.Gate
	applier workflow.TransitionApplier
	// tierStore and tierSvc back the ADR-0011 configuration CRUD and the
	// approval decision surface; auditLog records both.
	tierStore workflow.TierStore
	tierSvc   *workflow.TierService
	auditLog  audit.Logger
}

// WithWorkflowTiers attaches the ADR-0011 tier gate and the transactional
// applier.
func (h *Handler) WithWorkflowTiers(
	g *tiergate.Gate, a workflow.TransitionApplier, store workflow.TierStore, svc *workflow.TierService,
) *Handler {
	h.tiers = g
	h.applier = a
	h.tierStore = store
	h.tierSvc = svc
	return h
}

// WithAuditLogger attaches an audit logger. Tier configuration changes and
// approval decisions are recorded through it.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// NewHandler creates a Handler.
func NewHandler(q *generated.Queries, repo workflow.Repository, eng workflow.Engine) *Handler {
	return &Handler{q: q, repo: repo, eng: eng, auditLog: audit.NewLogger()}
}

// OrgRoutes mounts org-scoped workflow CRUD under /orgs/{orgID}/workflows.
func (h *Handler) OrgRoutes(adminGuard func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	// Reads are open to every org member; mutations are the workflow-admin
	// surface and require an org admin.
	r.Get("/", h.ListWorkflows)
	r.With(adminGuard).Post("/", h.CreateWorkflow)
	r.Get("/{workflowID}", h.GetWorkflow)
	r.With(adminGuard).Put("/{workflowID}", h.UpdateWorkflow)
	r.With(adminGuard).Delete("/{workflowID}", h.DeleteWorkflow)
	r.Get("/{workflowID}/states", h.ListStates)
	r.With(adminGuard).Post("/{workflowID}/states", h.CreateState)
	r.With(adminGuard).Delete("/{workflowID}/states/{stateID}", h.DeleteState)
	r.Get("/{workflowID}/transitions", h.ListTransitions)
	r.With(adminGuard).Post("/{workflowID}/transitions", h.CreateTransition)
	r.With(adminGuard).Delete("/{workflowID}/transitions/{transitionID}", h.DeleteTransition)
	h.registerTierOrgRoutes(r, adminGuard)
	return r
}

// SpaceRoutes mounts space-scoped workflow read + transition endpoints.
func (h *Handler) SpaceRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.GetSpaceWorkflow)
	r.Get("/states", h.GetSpaceWorkflowStates)
	h.registerTierSpaceRoutes(r)
	return r
}

// ─── Request / response types ─────────────────────────────────────────────────

type createWorkflowRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	IsDefault   bool    `json:"is_default"`
	AppliesTo   string  `json:"applies_to"`
}

type updateWorkflowRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	IsDefault   bool    `json:"is_default"`
	AppliesTo   string  `json:"applies_to"`
}

type createStateRequest struct {
	Name      string `json:"name"`
	Category  string `json:"category"`
	Color     string `json:"color"`
	Position  int32  `json:"position"`
	IsInitial bool   `json:"is_initial"`
}

type createTransitionRequest struct {
	FromStateID uuid.UUID `json:"from_state_id"`
	ToStateID   uuid.UUID `json:"to_state_id"`
	Name        string    `json:"name"`
}

// ─── Org-scoped handlers ──────────────────────────────────────────────────────

// ListWorkflows lists all workflows for an org.
//
// @Summary      List workflows
// @Description  Returns all workflows belonging to the organization.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID"
// @Success      200    {array}   workflow.Workflow           "Workflows"
// @Failure      400    {object}  api.SwaggerErrorResponse   "Invalid org ID"
// @Failure      401    {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500    {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows [get]
func (h *Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org ID")
		return
	}
	workflows, err := h.repo.ListWorkflows(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list workflows")
		return
	}
	respond.JSON(w, http.StatusOK, workflows)
}

// GetWorkflow returns a single workflow.
//
// @Summary      Get workflow
// @Description  Returns a workflow by ID.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID"
// @Param        workflowID  path      string  true  "Workflow ID"
// @Success      200         {object}  workflow.Workflow           "Workflow"
// @Failure      400         {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401         {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404         {object}  api.SwaggerErrorResponse   "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID} [get]
func (h *Handler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	wf, err := h.repo.GetWorkflow(r.Context(), id)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "workflow not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get workflow")
		return
	}
	respond.JSON(w, http.StatusOK, wf)
}

// CreateWorkflow creates a new workflow for an org.
//
// @Summary      Create workflow
// @Description  Creates a new workflow for the organization.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                 true  "Organization ID"
// @Param        body   body      createWorkflowRequest  true  "Workflow definition"
// @Success      201    {object}  workflow.Workflow           "Created workflow"
// @Failure      400    {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      401    {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500    {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows [post]
func (h *Handler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org ID")
		return
	}
	var req createWorkflowRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.AppliesTo == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name and applies_to are required")
		return
	}
	wf := &workflow.Workflow{
		OrgID:       orgID,
		Name:        req.Name,
		Description: req.Description,
		IsDefault:   req.IsDefault,
		AppliesTo:   req.AppliesTo,
	}
	if err := h.repo.CreateWorkflow(r.Context(), wf); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create workflow")
		return
	}
	respond.JSON(w, http.StatusCreated, wf)
}

// UpdateWorkflow updates a workflow's metadata.
//
// @Summary      Update workflow
// @Description  Updates a workflow's name, description, and default flag.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string                 true  "Organization ID"
// @Param        workflowID  path      string                 true  "Workflow ID"
// @Param        body        body      updateWorkflowRequest  true  "Updated fields"
// @Success      200         {object}  workflow.Workflow           "Updated workflow"
// @Failure      400         {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      401         {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500         {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID} [put]
func (h *Handler) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	var req updateWorkflowRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	wf := &workflow.Workflow{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		IsDefault:   req.IsDefault,
		AppliesTo:   req.AppliesTo,
	}
	if err := h.repo.UpdateWorkflow(r.Context(), wf); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update workflow")
		return
	}
	respond.JSON(w, http.StatusOK, wf)
}

// DeleteWorkflow removes a workflow.
//
// @Summary      Delete workflow
// @Description  Deletes a workflow by ID.
// @Tags         workflows
// @Security     BearerAuth
// @Param        orgID       path  string  true  "Organization ID"
// @Param        workflowID  path  string  true  "Workflow ID"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID} [delete]
func (h *Handler) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	if err := h.repo.DeleteWorkflow(r.Context(), id); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete workflow")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListStates lists states for a workflow.
//
// @Summary      List workflow states
// @Description  Returns all states for a workflow, ordered by position.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID"
// @Param        workflowID  path      string  true  "Workflow ID"
// @Success      200         {array}   workflow.State             "States"
// @Failure      400         {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401         {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500         {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID}/states [get]
func (h *Handler) ListStates(w http.ResponseWriter, r *http.Request) {
	id, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	states, err := h.repo.ListStates(r.Context(), id)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list states")
		return
	}
	respond.JSON(w, http.StatusOK, states)
}

// CreateState adds a state to a workflow.
//
// @Summary      Create workflow state
// @Description  Adds a state to a workflow.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string              true  "Organization ID"
// @Param        workflowID  path      string              true  "Workflow ID"
// @Param        body        body      createStateRequest  true  "State definition"
// @Success      201         {object}  workflow.State             "Created state"
// @Failure      400         {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      401         {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500         {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID}/states [post]
func (h *Handler) CreateState(w http.ResponseWriter, r *http.Request) {
	wfID, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	var req createStateRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Category == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name and category are required")
		return
	}
	color := req.Color
	if color == "" {
		color = "#6b7280"
	}
	s := &workflow.State{
		WorkflowID: wfID,
		Name:       req.Name,
		Category:   req.Category,
		Color:      color,
		Position:   req.Position,
		IsInitial:  req.IsInitial,
	}
	if err := h.repo.CreateState(r.Context(), s); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create state")
		return
	}
	respond.JSON(w, http.StatusCreated, s)
}

// DeleteState removes a state from a workflow.
//
// @Summary      Delete workflow state
// @Description  Removes a state from a workflow.
// @Tags         workflows
// @Security     BearerAuth
// @Param        orgID       path  string  true  "Organization ID"
// @Param        workflowID  path  string  true  "Workflow ID"
// @Param        stateID     path  string  true  "State ID"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID}/states/{stateID} [delete]
func (h *Handler) DeleteState(w http.ResponseWriter, r *http.Request) {
	id, err := stateIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid state ID")
		return
	}
	if err := h.repo.DeleteState(r.Context(), id); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete state")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTransitions lists transitions for a workflow.
//
// @Summary      List workflow transitions
// @Description  Returns all transitions for a workflow.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID"
// @Param        workflowID  path      string  true  "Workflow ID"
// @Success      200         {array}   workflow.Transition         "Transitions"
// @Failure      400         {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401         {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500         {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions [get]
func (h *Handler) ListTransitions(w http.ResponseWriter, r *http.Request) {
	id, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	ts, err := h.repo.ListTransitions(r.Context(), id)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list transitions")
		return
	}
	respond.JSON(w, http.StatusOK, ts)
}

// CreateTransition adds a transition to a workflow.
//
// @Summary      Create workflow transition
// @Description  Adds a transition between two states in a workflow.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string                   true  "Organization ID"
// @Param        workflowID  path      string                   true  "Workflow ID"
// @Param        body        body      createTransitionRequest  true  "Transition definition"
// @Success      201         {object}  workflow.Transition         "Created transition"
// @Failure      400         {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      401         {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500         {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions [post]
func (h *Handler) CreateTransition(w http.ResponseWriter, r *http.Request) {
	wfID, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return
	}
	var req createTransitionRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name is required")
		return
	}
	t := &workflow.Transition{
		WorkflowID:  wfID,
		FromStateID: req.FromStateID,
		ToStateID:   req.ToStateID,
		Name:        req.Name,
	}
	if err := h.repo.CreateTransition(r.Context(), t); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create transition")
		return
	}
	respond.JSON(w, http.StatusCreated, t)
}

// DeleteTransition removes a transition from a workflow.
//
// @Summary      Delete workflow transition
// @Description  Removes a transition from a workflow.
// @Tags         workflows
// @Security     BearerAuth
// @Param        orgID         path  string  true  "Organization ID"
// @Param        workflowID    path  string  true  "Workflow ID"
// @Param        transitionID  path  string  true  "Transition ID"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID} [delete]
func (h *Handler) DeleteTransition(w http.ResponseWriter, r *http.Request) {
	id, err := transitionIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid transition ID")
		return
	}
	if err := h.repo.DeleteTransition(r.Context(), id); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete transition")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Space-scoped handlers ────────────────────────────────────────────────────

// GetSpaceWorkflow returns the workflow assigned to a space.
//
// @Summary      Get space workflow
// @Description  Returns the workflow assigned to a space.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID"
// @Success      200      {object}  workflow.Workflow           "Workflow"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse   "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/workflow [get]
func (h *Handler) GetSpaceWorkflow(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	wf, err := h.q.GetSpaceWorkflow(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "no workflow assigned to space")
		return
	}
	respond.JSON(w, http.StatusOK, wf)
}

// GetSpaceWorkflowStates returns the workflow states for a space, in order.
//
// @Summary      Get space workflow states
// @Description  Returns the ordered list of workflow states for a space.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID"
// @Success      200      {array}   workflow.State             "States"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/workflow/states [get]
func (h *Handler) GetSpaceWorkflowStates(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	states, err := h.q.GetSpaceWorkflowStates(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get workflow states")
		return
	}
	// Convert generated rows to workflow.State for consistent response shape.
	out := make([]*workflow.State, len(states))
	for i, s := range states {
		out[i] = &workflow.State{
			ID:         s.ID,
			WorkflowID: s.WorkflowID,
			Name:       s.Name,
			Category:   s.Category,
			Color:      s.Color,
			Position:   s.Position,
			IsInitial:  s.IsInitial,
			CreatedAt:  s.CreatedAt.Time,
		}
	}
	respond.JSON(w, http.StatusOK, out)
}

// ApplyWorkflowTransitionToTicket transitions a ticket via the workflow engine.
//
// @Summary      Apply workflow transition (ticket)
// @Description  Validates and applies a workflow state transition to a ticket.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string                          true  "Space ID"
// @Param        ticketID  path      string                          true  "Ticket ID"
// @Param        body      body      workflowTransitionRequest       true  "Target state ID"
// @Success      200       {object}  generated.Ticket                "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse        "Invalid request"
// @Failure      401       {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse        "Not found"
// @Failure      409       {object}  api.SwaggerErrorResponse        "Invalid transition"
// @Failure      500       {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/workflow-state [post]
func (h *Handler) ApplyWorkflowTransitionToTicket(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // workflow state machine validation requires multiple guard branches
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	ticketID, err := uuid.Parse(chi.URLParam(r, "ticketID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	if !access.Can(r.Context(), access.CapTransitionAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req workflowTransitionRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// Fetch the space workflow.
	spaceWF, err := h.q.GetSpaceWorkflow(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "no workflow assigned to space")
		return
	}

	// Fetch the ticket's current state.
	ticket, err := h.q.GetTicketByID(r.Context(), ticketID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "ticket not found")
		return
	}

	var currentStateID uuid.UUID
	if ticket.WorkflowStateID.Valid {
		currentStateID = ticket.WorkflowStateID.Bytes
	} else {
		// Fall back to initial state if workflow_state_id is not yet set.
		initial, err := h.repo.GetInitialState(r.Context(), spaceWF.ID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get initial state")
			return
		}
		currentStateID = initial.ID
	}

	// Validate the transition.
	if err := h.eng.ValidateTransition(r.Context(), spaceWF.ID, currentStateID, req.StateID); err != nil {
		if errors.Is(err, workflow.ErrInvalidTransition) {
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "invalid workflow transition")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to validate transition")
		return
	}

	// Fetch the target state name to keep the status column in sync.
	targetState, err := h.repo.GetState(r.Context(), req.StateID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "target state not found")
		return
	}

	gated, ok := h.gate(w, r, spaceID, workflow.ApprovalEntityTicket, ticketID,
		ticket.Status, targetState.Name, workflow.EntitySnapshot{
			AssigneeID:  goUUIDPtr(ticket.AssigneeID),
			DueAt:       goTimePtr(ticket.DueAt),
			Description: ticket.Description,
			Labels:      ticket.Labels,
		})
	if !ok {
		return
	}

	if len(gated.Effects) > 0 {
		if !h.applyWithEffects(w, r, spaceID, workflow.ApprovalEntityTicket, ticketID, targetState.Name, gated) {
			return
		}
		refreshed, err := h.q.GetTicketByID(r.Context(), ticketID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to read the updated ticket")
			return
		}
		respond.JSON(w, http.StatusOK, refreshed)
		return
	}

	updated, err := h.q.UpdateTicketWorkflowState(r.Context(), generated.UpdateTicketWorkflowStateParams{
		ID:              ticketID,
		Status:          targetState.Name,
		WorkflowStateID: pgtype.UUID{Bytes: req.StateID, Valid: true},
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update ticket")
		return
	}
	respond.JSON(w, http.StatusOK, updated)
}

// ApplyWorkflowTransitionToItem transitions a project item via the workflow engine.
//
// @Summary      Apply workflow transition (project item)
// @Description  Validates and applies a workflow state transition to a project item.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID"
// @Param        itemID   path      string                          true  "Item ID"
// @Param        body     body      workflowTransitionRequest       true  "Target state ID"
// @Success      200      {object}  generated.ProjectItem           "Updated item"
// @Failure      400      {object}  api.SwaggerErrorResponse        "Invalid request"
// @Failure      401      {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse        "Not found"
// @Failure      409      {object}  api.SwaggerErrorResponse        "Invalid transition"
// @Failure      500      {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/workflow-state [post]
func (h *Handler) ApplyWorkflowTransitionToItem(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // workflow state machine validation requires multiple guard branches
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	if !access.Can(r.Context(), access.CapTransitionAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req workflowTransitionRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	spaceWF, err := h.q.GetSpaceWorkflow(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "no workflow assigned to space")
		return
	}

	item, err := h.q.GetProjectItemByID(r.Context(), itemID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "item not found")
		return
	}

	var currentStateID uuid.UUID
	if item.WorkflowStateID.Valid {
		currentStateID = item.WorkflowStateID.Bytes
	} else {
		initial, err := h.repo.GetInitialState(r.Context(), spaceWF.ID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get initial state")
			return
		}
		currentStateID = initial.ID
	}

	if err := h.eng.ValidateTransition(r.Context(), spaceWF.ID, currentStateID, req.StateID); err != nil {
		if errors.Is(err, workflow.ErrInvalidTransition) {
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "invalid workflow transition")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to validate transition")
		return
	}

	targetState, err := h.repo.GetState(r.Context(), req.StateID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "target state not found")
		return
	}

	gated, ok := h.gate(w, r, spaceID, workflow.ApprovalEntityItem, itemID,
		item.Status, targetState.Name, workflow.EntitySnapshot{
			AssigneeID:  goUUIDPtr(item.AssigneeID),
			DueAt:       goTimePtr(item.DueAt),
			Description: item.Description,
			Labels:      item.Labels,
		})
	if !ok {
		return
	}

	if len(gated.Effects) > 0 {
		if !h.applyWithEffects(w, r, spaceID, workflow.ApprovalEntityItem, itemID, targetState.Name, gated) {
			return
		}
		refreshed, err := h.q.GetProjectItemByID(r.Context(), itemID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to read the updated item")
			return
		}
		respond.JSON(w, http.StatusOK, refreshed)
		return
	}

	updated, err := h.q.UpdateProjectItemWorkflowState(r.Context(), generated.UpdateProjectItemWorkflowStateParams{
		ID:              itemID,
		Status:          targetState.Name,
		WorkflowStateID: pgtype.UUID{Bytes: req.StateID, Valid: true},
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update item")
		return
	}
	respond.JSON(w, http.StatusOK, updated)
}

type workflowTransitionRequest struct {
	StateID uuid.UUID `json:"state_id"`
}

// ─── URL param helpers ────────────────────────────────────────────────────────

func orgIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parsing orgID: %w", err)
	}
	return id, nil
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parsing spaceID: %w", err)
	}
	return id, nil
}

func workflowIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "workflowID"))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parsing workflowID: %w", err)
	}
	return id, nil
}

func stateIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "stateID"))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parsing stateID: %w", err)
	}
	return id, nil
}

func transitionIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "transitionID"))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parsing transitionID: %w", err)
	}
	return id, nil
}

// ─── Tier evaluation, shared by both engine-backed transition routes ──────────

// gate runs the ADR-0011 tiers for an engine-backed transition and reports
// whether it may proceed.
//
// These two routes have no frontend caller today, but they are gated for the
// same reason the /status routes are: an ungated route is a way around a
// configured guard, and "nothing calls it yet" is not a security boundary.
func (h *Handler) gate(
	w http.ResponseWriter, r *http.Request,
	spaceID uuid.UUID, entityType workflow.ApprovalEntityType, entityID uuid.UUID,
	currentStatus, targetStatus string, snapshot workflow.EntitySnapshot,
) (workflow.GateResult, bool) {
	if h.tiers == nil || h.applier == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return workflow.GateResult{}, false
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return workflow.GateResult{}, false
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return workflow.GateResult{}, false
	}

	gated, err := h.tiers.Evaluate(r.Context(), tiergate.Request{
		OrgID:         orgID,
		SpaceID:       spaceID,
		EntityType:    entityType,
		EntityID:      entityID,
		ActorID:       claims.UserID,
		CurrentStatus: currentStatus,
		TargetStatus:  targetStatus,
		Entity:        snapshot,
	})
	if err != nil {
		if errors.Is(err, workflow.ErrPostFunctionUnknown) || errors.Is(err, workflow.ErrPostFunctionMalformed) {
			respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation,
				"this transition is configured with an action this server cannot perform, so it was not applied")
			return workflow.GateResult{}, false
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to evaluate the transition")
		return workflow.GateResult{}, false
	}
	if tiergate.Refused(w, r, gated) || tiergate.Pending(w, gated) {
		return workflow.GateResult{}, false
	}
	return gated, true
}

// applyWithEffects writes the status and its post-function effects in one
// transaction, reporting whether it succeeded.
func (h *Handler) applyWithEffects(
	w http.ResponseWriter, r *http.Request,
	spaceID uuid.UUID, entityType workflow.ApprovalEntityType, entityID uuid.UUID,
	toStatus string, gated workflow.GateResult,
) bool {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return false
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return false
	}

	if err := h.applier.ApplyTransition(r.Context(), workflow.ApplyInput{
		EntityType:   entityType,
		EntityID:     entityID,
		OrgID:        orgID,
		SpaceID:      spaceID,
		ActorID:      claims.UserID,
		ToStatus:     toStatus,
		ToStateID:    gated.ToStateID,
		TransitionID: gated.TransitionID,
		Effects:      gated.Effects,
	}); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"the transition could not be completed and no change was made")
		return false
	}
	return true
}

// goUUIDPtr converts a pgtype.UUID to a *uuid.UUID; an invalid (NULL) value
// yields nil.
func goUUIDPtr(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	u := uuid.UUID(id.Bytes)
	return &u
}

// goTimePtr converts a pgtype.Timestamptz to a *time.Time; an invalid (NULL)
// value yields nil.
func goTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
