package workflows

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/tiergate"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// This file carries the ADR-0011 tier surface: the org-scoped configuration
// CRUD an administrator uses to attach guards, approvers and post-functions to
// a transition, and the space-scoped approval surface an approver uses to
// decide one.
//
// # Two scopes, two guard classes, and why
//
// CONFIGURATION is org-scoped and org-admin, because that is where workflows
// already live: a workflow is an org object shared by every space bound to it,
// so a space admin editing one would be editing other spaces' rules. The
// existing workflow mutations are org-admin for the same reason, and
// TestReadPathSweep_GuardClassMatchesMiddleware verifies that claim against the
// real middleware chain rather than taking the accounting row on trust.
//
// DECISIONS are space-scoped, because an approval is about one item in one
// space. Authority to decide is not a capability at all: it is being named,
// directly or through a team, on the transition. See workflow.CanDecide.
//
// # Every route here re-scopes {workflowID} to {orgID}
//
// The pre-existing workflow routes resolve {workflowID} without checking it
// belongs to {orgID}, which makes a workflow in another org reachable by id.
// That is reported as an inherited finding rather than changed here, because
// changing it alters existing behaviour. Every route in THIS file calls
// requireWorkflowInOrg first, so the new surface does not widen the exposure.

// registerTierOrgRoutes adds the per-transition configuration surface to the
// org-scoped workflow router.
//
// Registered as explicit routes rather than a chi Mount: the existing router
// already declares DELETE /{workflowID}/transitions/{transitionID}, and chi
// panics when a Mount collides with an existing path.
//
// Reads are open to org members — an ordinary user benefits from seeing why a
// transition is restricted — and every mutation carries adminGuard.
func (h *Handler) registerTierOrgRoutes(r chi.Router, adminGuard func(http.Handler) http.Handler) {
	const base = "/{workflowID}/transitions/{transitionID}"

	r.Get(base+"/guards", h.ListGuards)
	r.With(adminGuard).Post(base+"/guards", h.CreateGuard)
	r.With(adminGuard).Delete(base+"/guards/{guardID}", h.DeleteGuard)

	r.Get(base+"/post-functions", h.ListPostFunctions)
	r.With(adminGuard).Post(base+"/post-functions", h.CreatePostFunction)
	r.With(adminGuard).Delete(base+"/post-functions/{postFunctionID}", h.DeletePostFunction)

	r.Get(base+"/approvers", h.ListApprovers)
	r.With(adminGuard).Post(base+"/approvers", h.CreateApprover)
	r.With(adminGuard).Delete(base+"/approvers/{approverID}", h.DeleteApprover)
}

// registerTierSpaceRoutes adds the approval surface to the space-scoped
// workflow router.
//
// The two reads are not redundant. /approvals is the space's PENDING set — the
// board's blocked markers and the "awaiting a decision" list. The per-entity
// read is one item's whole history, pending and decided alike, and the detail
// page needs it for a reason the space list structurally cannot serve: the
// moment an approver declines, the request stops being pending and drops out of
// /approvals, taking the decline reason with it. A surface built only on the
// pending list would show the requester a blocked item, then show them nothing
// at all — the item silently not having moved, which is the exact failure the
// reason column was added to close.
func (h *Handler) registerTierSpaceRoutes(r chi.Router) {
	r.Get("/approvals", h.ListPendingApprovals)
	r.Post("/approvals/{approvalID}/decide", h.DecideApproval)
	r.Get("/entities/{entityType}/{entityID}/approvals", h.ListEntityApprovals)
	// The route that makes ADR-0011 conditions mean something. Until it
	// existed, "a condition determines whether a transition is OFFERED" had no
	// offerer: the filter was written, unit-tested and unreachable, so a
	// configured condition hid a transition from nobody. See ListAvailableTransitions.
	r.Get("/entities/{entityType}/{entityID}/transitions", h.ListAvailableTransitions)
}

// ─── Request bodies ───────────────────────────────────────────────────────────

type createGuardRequest struct {
	GuardClass string     `json:"guard_class"`
	Kind       string     `json:"kind"`
	Position   int32      `json:"position"`
	Capability *string    `json:"capability,omitempty"`
	TeamID     *uuid.UUID `json:"team_id,omitempty"`
	FieldKey   *string    `json:"field_key,omitempty"`
}

type createPostFunctionRequest struct {
	Kind           string     `json:"kind"`
	Position       int32      `json:"position"`
	AssigneeUserID *uuid.UUID `json:"assignee_user_id,omitempty"`
	FieldKey       *string    `json:"field_key,omitempty"`
	FieldValue     *string    `json:"field_value,omitempty"`
}

type createApproverRequest struct {
	SubjectType string    `json:"subject_type"`
	SubjectID   uuid.UUID `json:"subject_id"`
}

type decideApprovalRequest struct {
	Decision string `json:"decision"`
	// Reason is required when Decision is "declined" and optional otherwise.
	// TierService.Decide owns the rule; see workflow.ErrDeclineReasonRequired.
	Reason string `json:"reason"`
}

// ─── Guards ───────────────────────────────────────────────────────────────────

// ListGuards returns the conditions and validators on a transition.
//
// @Summary      List transition guards
// @Description  Returns the ADR-0011 conditions and validators attached to a workflow transition.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID         path      string  true  "Organization ID"
// @Param        workflowID    path      string  true  "Workflow ID"
// @Param        transitionID  path      string  true  "Transition ID"
// @Success      200  {array}   workflow.Guard            "Guards"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/guards [get]
func (h *Handler) ListGuards(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	guards, err := h.tierStore.GuardsForTransition(r.Context(), transitionID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list guards")
		return
	}
	// Go marshals a nil slice as null; the client's list fetchers coalesce, but
	// an empty array is the honest wire form and null-collections.spec.ts hunts
	// the other one.
	if guards == nil {
		guards = []workflow.Guard{}
	}
	respond.JSON(w, http.StatusOK, guards)
}

// CreateGuard attaches a condition or validator to a transition.
//
// @Summary      Create transition guard
// @Description  Attaches an ADR-0011 condition or validator to a workflow transition. The vocabulary is closed.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID         path      string              true  "Organization ID"
// @Param        workflowID    path      string              true  "Workflow ID"
// @Param        transitionID  path      string              true  "Transition ID"
// @Param        body          body      createGuardRequest  true  "Guard definition"
// @Success      201  {object}  workflow.Guard            "Created guard"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/guards [post]
func (h *Handler) CreateGuard(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	var req createGuardRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	g := workflow.Guard{
		TransitionID: transitionID,
		Class:        workflow.GuardClass(req.GuardClass),
		Kind:         workflow.GuardKind(req.Kind),
		Position:     req.Position,
		TeamID:       req.TeamID,
	}
	if req.Capability != nil {
		c := access.Capability(*req.Capability)
		g.Capability = &c
	}
	if req.FieldKey != nil {
		f := workflow.FieldKey(*req.FieldKey)
		g.FieldKey = &f
	}

	// The closed vocabulary is enforced here, before anything is written, so a
	// caller gets a sentence naming what is permitted rather than a constraint
	// violation. Migration 046's CHECKs refuse the same set one layer down.
	if err := workflow.ValidateGuard(g); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}

	created, err := h.tierStore.CreateGuard(r.Context(), g)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "transition not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create guard")
		return
	}
	h.logTierEvent(r, audit.EventTypeWorkflowGuardCreated, "workflow_guard", created.ID, map[string]string{
		"transition_id": transitionID.String(),
		"guard_class":   string(created.Class),
		"kind":          string(created.Kind),
	})
	respond.JSON(w, http.StatusCreated, created)
}

// DeleteGuard removes a condition or validator.
//
// @Summary      Delete transition guard
// @Description  Removes an ADR-0011 condition or validator from a workflow transition.
// @Tags         workflows
// @Security     BearerAuth
// @Param        orgID         path  string  true  "Organization ID"
// @Param        workflowID    path  string  true  "Workflow ID"
// @Param        transitionID  path  string  true  "Transition ID"
// @Param        guardID       path  string  true  "Guard ID"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/guards/{guardID} [delete]
func (h *Handler) DeleteGuard(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "guardID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid guard ID")
		return
	}
	if err := h.tierStore.DeleteGuard(r.Context(), transitionID, id); err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "guard not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete guard")
		return
	}
	h.logTierEvent(r, audit.EventTypeWorkflowGuardDeleted, "workflow_guard", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Post-functions ───────────────────────────────────────────────────────────

// ListPostFunctions returns the actions a transition performs.
//
// @Summary      List transition post-functions
// @Description  Returns the ADR-0011 post-functions attached to a workflow transition. The set is fixed in code.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID         path      string  true  "Organization ID"
// @Param        workflowID    path      string  true  "Workflow ID"
// @Param        transitionID  path      string  true  "Transition ID"
// @Success      200  {array}   workflow.PostFunction     "Post-functions"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/post-functions [get]
func (h *Handler) ListPostFunctions(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	pfs, err := h.tierStore.PostFunctionsForTransition(r.Context(), transitionID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list post-functions")
		return
	}
	if pfs == nil {
		pfs = []workflow.PostFunction{}
	}
	respond.JSON(w, http.StatusOK, pfs)
}

// CreatePostFunction attaches an action to a transition.
//
// @Summary      Create transition post-function
// @Description  Attaches one of the fixed ADR-0011 post-functions to a transition. The set is defined in code and cannot be extended by configuration.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID         path      string                     true  "Organization ID"
// @Param        workflowID    path      string                     true  "Workflow ID"
// @Param        transitionID  path      string                     true  "Transition ID"
// @Param        body          body      createPostFunctionRequest  true  "Post-function definition"
// @Success      201  {object}  workflow.PostFunction     "Created post-function"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/post-functions [post]
func (h *Handler) CreatePostFunction(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	var req createPostFunctionRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	p := workflow.PostFunction{
		TransitionID:   transitionID,
		Kind:           workflow.PostFunctionKind(req.Kind),
		Position:       req.Position,
		AssigneeUserID: req.AssigneeUserID,
		FieldValue:     req.FieldValue,
	}
	if req.FieldKey != nil {
		f := workflow.PostFieldKey(*req.FieldKey)
		p.FieldKey = &f
	}
	if err := workflow.ValidatePostFunction(p); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}

	created, err := h.tierStore.CreatePostFunction(r.Context(), p)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "transition not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create post-function")
		return
	}
	h.logTierEvent(r, audit.EventTypeWorkflowPostFunctionCreated, "workflow_post_function", created.ID, map[string]string{
		"transition_id": transitionID.String(),
		"kind":          string(created.Kind),
	})
	respond.JSON(w, http.StatusCreated, created)
}

// DeletePostFunction removes an action from a transition.
//
// @Summary      Delete transition post-function
// @Description  Removes an ADR-0011 post-function from a workflow transition.
// @Tags         workflows
// @Security     BearerAuth
// @Param        orgID           path  string  true  "Organization ID"
// @Param        workflowID      path  string  true  "Workflow ID"
// @Param        transitionID    path  string  true  "Transition ID"
// @Param        postFunctionID  path  string  true  "Post-function ID"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/post-functions/{postFunctionID} [delete]
func (h *Handler) DeletePostFunction(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "postFunctionID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid post-function ID")
		return
	}
	if err := h.tierStore.DeletePostFunction(r.Context(), transitionID, id); err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "post-function not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete post-function")
		return
	}
	h.logTierEvent(r, audit.EventTypeWorkflowPostFunctionDeleted, "workflow_post_function", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Approvers ────────────────────────────────────────────────────────────────

// ListApprovers returns who must approve a transition.
//
// @Summary      List transition approvers
// @Description  Returns the users and teams that may approve a workflow transition.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID         path      string  true  "Organization ID"
// @Param        workflowID    path      string  true  "Workflow ID"
// @Param        transitionID  path      string  true  "Transition ID"
// @Success      200  {array}   workflow.Approver         "Approvers"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/approvers [get]
func (h *Handler) ListApprovers(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	approvers, err := h.tierStore.ApproversForTransition(r.Context(), transitionID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list approvers")
		return
	}
	if approvers == nil {
		approvers = []workflow.Approver{}
	}
	respond.JSON(w, http.StatusOK, approvers)
}

// CreateApprover adds a user or team to a transition's approver set.
//
// @Summary      Create transition approver
// @Description  Adds a user or team that may approve a workflow transition. Any one approver may decide.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID         path      string                 true  "Organization ID"
// @Param        workflowID    path      string                 true  "Workflow ID"
// @Param        transitionID  path      string                 true  "Transition ID"
// @Param        body          body      createApproverRequest  true  "Approver subject"
// @Success      201  {object}  workflow.Approver         "Created approver"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409  {object}  api.SwaggerErrorResponse  "Already an approver"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/approvers [post]
func (h *Handler) CreateApprover(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	var req createApproverRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	ap := workflow.Approver{
		TransitionID: transitionID,
		SubjectType:  workflow.ApproverSubjectType(req.SubjectType),
		SubjectID:    req.SubjectID,
	}
	if err := workflow.ValidateApprover(ap); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}

	created, err := h.tierStore.CreateApprover(r.Context(), ap)
	if err != nil {
		switch {
		case errors.Is(err, workflow.ErrApproverExists):
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict,
				"that subject is already an approver for this transition")
		case errors.Is(err, workflow.ErrNotFound):
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "transition not found")
		default:
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create approver")
		}
		return
	}
	h.logTierEvent(r, audit.EventTypeWorkflowApproverCreated, "workflow_approver", created.ID, map[string]string{
		"transition_id": transitionID.String(),
		"subject_type":  string(created.SubjectType),
		"subject_id":    created.SubjectID.String(),
	})
	respond.JSON(w, http.StatusCreated, created)
}

// DeleteApprover removes a subject from a transition's approver set.
//
// @Summary      Delete transition approver
// @Description  Removes a user or team from a workflow transition's approver set.
// @Tags         workflows
// @Security     BearerAuth
// @Param        orgID         path  string  true  "Organization ID"
// @Param        workflowID    path  string  true  "Workflow ID"
// @Param        transitionID  path  string  true  "Transition ID"
// @Param        approverID    path  string  true  "Approver ID"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/workflows/{workflowID}/transitions/{transitionID}/approvers/{approverID} [delete]
func (h *Handler) DeleteApprover(w http.ResponseWriter, r *http.Request) {
	transitionID, ok := h.resolveTransition(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "approverID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid approver ID")
		return
	}
	if err := h.tierStore.DeleteApprover(r.Context(), transitionID, id); err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "approver not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete approver")
		return
	}
	h.logTierEvent(r, audit.EventTypeWorkflowApproverDeleted, "workflow_approver", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Approvals ────────────────────────────────────────────────────────────────

// ListPendingApprovals returns everything awaiting a decision in a space.
//
// Space-read: anyone who can see the space's items can see that one is waiting.
// The list is the surface the board reads to mark an item blocked, so hiding it
// from non-approvers would make the block invisible to the person it affects.
//
// @Summary      List pending approvals
// @Description  Returns the workflow transitions awaiting an approval decision in a space.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID"
// @Param        spaceID  path      string  true  "Space ID"
// @Success      200  {array}   workflow.Approval         "Pending approvals"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/workflow/approvals [get]
func (h *Handler) ListPendingApprovals(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	if h.tierStore == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return
	}
	approvals, err := h.tierStore.PendingApprovalsForSpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list approvals")
		return
	}
	if approvals == nil {
		approvals = []workflow.Approval{}
	}
	approvals, ok := h.markDecidable(w, r, approvals)
	if !ok {
		return
	}
	respond.JSON(w, http.StatusOK, approvals)
}

// ListEntityApprovals returns one item's approval history, pending and decided.
//
// Space-read, the same class as the pending list and for the same reason: the
// block is about an item anyone in the space can already see, and hiding the
// record from non-approvers would hide it from the requester the decision is
// addressed to.
//
// @Summary      List an item's approvals
// @Description  Returns every workflow approval ever requested for one ticket or project item, newest first.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID"
// @Param        spaceID     path      string  true  "Space ID"
// @Param        entityType  path      string  true  "ticket or item"
// @Param        entityID    path      string  true  "Entity ID"
// @Success      200  {array}   workflow.Approval         "Approvals, newest first"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID or entity type"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/workflow/entities/{entityType}/{entityID}/approvals [get]
func (h *Handler) ListEntityApprovals(w http.ResponseWriter, r *http.Request) {
	// The space is used, not merely validated. It used to be parsed into `_`
	// purely to reject a malformed URL, so the read that followed was keyed on
	// the entity id alone and returned another space's approval history.
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	if h.tierStore == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return
	}

	// The discriminator goes through the one permitted string→type boundary, so
	// an unrecognised word is a 400 naming the two that exist rather than a
	// silent empty list. tickets and project_items are separate tables
	// (ADR-0003) and their ids are not unique across the pair, so an entity_id
	// alone does not identify an item.
	entityType, err := workflow.ParseApprovalEntityType(chi.URLParam(r, "entityType"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}
	entityID, err := uuid.Parse(chi.URLParam(r, "entityID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}

	approvals, err := h.tierStore.ApprovalsForEntity(r.Context(), spaceID, entityType, entityID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list approvals")
		return
	}
	if approvals == nil {
		approvals = []workflow.Approval{}
	}
	approvals, ok := h.markDecidable(w, r, approvals)
	if !ok {
		return
	}
	respond.JSON(w, http.StatusOK, approvals)
}

// ListAvailableTransitions reports which status changes this entity may be offered.
//
// # Why this route exists
//
// ADR-0011 gives a condition one job — decide "whether a transition is offered"
// — and until this route there was nothing that offered. The filter existed on
// the service with no HTTP route and no production caller, so an administrator
// could configure a condition, have it schema-validated, see it in the admin UI
// with a badge reading "hides", and have it hide nothing from anyone. Both the
// ADR's own Correction and the reconciliation ledger record that the fix is
// two-part: offer only legal moves AND refuse illegal ones on the mutation
// route. This is the first part; TierService.Gate is the second, and neither is
// sufficient alone.
//
// # It is a read, and it stays a read
//
// It is served from TierService.OfferedTransitions rather than from the gate.
// Evaluate WRITES — it creates the pending approval row and notifies the
// approvers — so building a picker on it would file an approval request every
// time a page loaded.
//
// # It reports conditions, never validators
//
// A condition hides; a validator explains. Filtering by validator here would
// tell the caller which preconditions are currently unmet on an entity, one
// disappearing option at a time, which is the enumeration the split exists to
// prevent. A move whose validator fails is offered and then refused with a
// reason, which is ADR-0011's design rather than a compromise with it.
//
// @Summary      List available transitions
// @Description  The status changes this entity may be offered, with ADR-0011 conditions applied.
// @Tags         workflows
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID"
// @Param        spaceID     path      string  true  "Space ID"
// @Param        entityType  path      string  true  "ticket or item"
// @Param        entityID    path      string  true  "Entity ID"
// @Success      200  {object}  workflow.Offering         "The offered transitions"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/workflow/entities/{entityType}/{entityID}/transitions [get]
func (h *Handler) ListAvailableTransitions(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	if h.tiers == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	entityType, err := workflow.ParseApprovalEntityType(chi.URLParam(r, "entityType"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}
	entityID, err := uuid.Parse(chi.URLParam(r, "entityID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}

	// Reconciled against {spaceID}, exactly as the mutation route is: the URL
	// authorises a space and the entity id arrives unchecked, so reading an
	// entity by bare id here would report another space's workflow position.
	req, ok := h.entityGateRequest(w, r, orgID, spaceID, claims.UserID, entityType, entityID)
	if !ok {
		return
	}

	offering, err := h.tiers.Offer(r.Context(), req)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"failed to list the available transitions")
		return
	}
	respond.JSON(w, http.StatusOK, offering)
}

// entityGateRequest reads the entity in its space and describes it to the
// chokepoint in the same terms the mutation route uses.
//
// TargetStatus is deliberately empty: this is the read path, and there is no
// proposed target. OfferedTransitions never consults it.
func (h *Handler) entityGateRequest(
	w http.ResponseWriter, r *http.Request,
	orgID, spaceID, actorID uuid.UUID, entityType workflow.ApprovalEntityType, entityID uuid.UUID,
) (tiergate.Request, bool) {
	req := tiergate.Request{
		OrgID: orgID, SpaceID: spaceID,
		EntityType: entityType, EntityID: entityID, ActorID: actorID,
	}

	switch entityType {
	case workflow.ApprovalEntityTicket:
		t, err := h.q.GetTicketInSpace(r.Context(), generated.GetTicketInSpaceParams{
			TicketID: entityID, SpaceID: spaceID,
		})
		if err != nil {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "ticket not found")
			return tiergate.Request{}, false
		}
		req.CurrentStatus = t.Status
		req.CurrentStateID = goUUIDPtr(t.WorkflowStateID)
		req.Entity = workflow.EntitySnapshot{
			AssigneeID:  goUUIDPtr(t.AssigneeID),
			DueAt:       goTimePtr(t.DueAt),
			Description: t.Description,
			Labels:      t.Labels,
		}
	case workflow.ApprovalEntityItem:
		i, err := h.q.GetProjectItemInSpace(r.Context(), generated.GetProjectItemInSpaceParams{
			ItemID: entityID, SpaceID: spaceID,
		})
		if err != nil {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "item not found")
			return tiergate.Request{}, false
		}
		req.CurrentStatus = i.Status
		req.CurrentStateID = goUUIDPtr(i.WorkflowStateID)
		req.Entity = workflow.EntitySnapshot{
			AssigneeID:  goUUIDPtr(i.AssigneeID),
			DueAt:       goTimePtr(i.DueAt),
			Description: i.Description,
			Labels:      i.Labels,
		}
	default:
		// Unreachable while ParseApprovalEntityType is the only way in, and kept
		// so a third entity kind added to that vocabulary without a case here
		// fails loudly rather than reporting an empty offering for everything.
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"this entity kind has no workflow transitions")
		return tiergate.Request{}, false
	}
	return req, true
}

// markDecidable fills CanDecide for the calling user, answering the request
// itself on failure and reporting whether the caller may continue.
func (h *Handler) markDecidable(
	w http.ResponseWriter, r *http.Request, approvals []workflow.Approval,
) ([]workflow.Approval, bool) {
	if h.tierSvc == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return nil, false
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return nil, false
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return nil, false
	}
	marked, err := h.tierSvc.MarkDecidable(r.Context(), orgID, claims.UserID, approvals)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list approvals")
		return nil, false
	}
	return marked, true
}

// DecideApproval records an approver's verdict and, on approval, applies the
// transition the request captured.
//
// Authority is DATA, not a capability: the actor may decide because an
// administrator named them, or a team they are an ADR-0007 effective member of,
// on this transition. No Capability constant governs it, and none was added —
// "who approves change requests" is per-gate rather than per-role, and adding
// one would have changed the capability model, which CLAUDE.md §5 makes a
// stop-and-raise decision.
//
// On approval the captured transition is applied through the same transactional
// applier a direct transition uses, so its post-functions commit with the status
// or not at all. On a decline nothing is applied: the item never left its source
// status, which is what "decline returns the item to the source status" means
// when the gate blocks rather than moves.
//
// @Summary      Decide an approval
// @Description  Approves or declines a pending workflow transition. Only a configured approver may decide.
// @Tags         workflows
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string                 true  "Organization ID"
// @Param        spaceID     path      string                 true  "Space ID"
// @Param        approvalID  path      string                 true  "Approval ID"
// @Param        body        body      decideApprovalRequest  true  "approved or declined"
// @Success      200  {object}  workflow.Approval         "The recorded decision"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "Not an approver"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409  {object}  api.SwaggerErrorResponse  "Already decided"
// @Router       /orgs/{orgID}/spaces/{spaceID}/workflow/approvals/{approvalID}/decide [post]
func (h *Handler) DecideApproval(w http.ResponseWriter, r *http.Request) {
	spaceID, approvalID, claims, orgID, ok := h.decideInputs(w, r)
	if !ok {
		return
	}

	var req decideApprovalRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	decision, err := workflow.ParseDecision(req.Decision)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}

	// {spaceID} goes to the service, not just into ApplyTransition below: it is
	// the only thing that ties the approval to a space. The approver check
	// cannot, because approvers are configured on an org-wide workflow's
	// transition — see TierService.Decide.
	// One call, one transaction. The verdict and the transition it releases
	// commit together or not at all — see workflow.TierService.Decide.
	//
	// There is deliberately no "the approval was recorded but the transition
	// could not be applied" branch here any more. That message was an accurate
	// description of a state this route could produce and nobody could recover
	// from: approved, unmoved, and no longer pending. It is not a message worth
	// improving, because the state is no longer reachable.
	decided, err := h.tierSvc.Decide(r.Context(), workflow.DecideRequest{
		OrgID:      orgID,
		SpaceID:    spaceID,
		ApprovalID: approvalID,
		ActorID:    claims.UserID,
		Decision:   decision,
		Reason:     req.Reason,
	})
	if err != nil {
		respondDecideError(w, r, err)
		return
	}

	h.logApprovalDecision(r, decided)
	// A decline is notified too: the requester is holding an item that did not
	// move, and the reason is the only thing that tells them what would move it.
	h.notifyDecision(r, decided)
	respond.JSON(w, http.StatusOK, decided)
}

// notifyDecision tells the person whose transition was decided what happened.
//
// The recipient is the REQUESTER — the actor whose status change was gated —
// not the approver, who already knows: they just decided. On a decline the
// reason travels with the notification, because a decline the requester has to
// go and look up is barely better than one they are never told about.
//
// Best-effort, like every other enqueue in this codebase: the decision is
// already committed and, on an approval, the transition may already have been
// applied. Failing the request here would report a decision that did happen as
// one that did not.
func (h *Handler) notifyDecision(r *http.Request, a workflow.Approval) {
	if h.notifs == nil || a.Decision == nil {
		return
	}

	var message string
	if *a.Decision == workflow.DecisionApproved {
		message = fmt.Sprintf("Your move from %q to %q was approved.", a.FromStatus, a.ToStatus)
	} else {
		message = fmt.Sprintf("Your move from %q to %q was declined.", a.FromStatus, a.ToStatus)
		if a.Reason != nil && *a.Reason != "" {
			message = fmt.Sprintf("Your move from %q to %q was declined: %s",
				a.FromStatus, a.ToStatus, *a.Reason)
		}
	}

	_ = h.notifs.EnqueueNotification(r.Context(), jobs.NotificationArgs{
		UserID:     a.RequestedBy.String(),
		EventKind:  tiergate.KindApprovalDecided,
		Message:    message,
		ResourceID: a.EntityID.String(),
		EntityKind: string(a.EntityType),
		SpaceID:    a.SpaceID.String(),
	})
}

// decideInputs resolves and validates everything DecideApproval needs before it
// can act, answering the request itself on any failure.
func (h *Handler) decideInputs(w http.ResponseWriter, r *http.Request) (
	spaceID, approvalID uuid.UUID, claims *auth.Claims, orgID uuid.UUID, ok bool,
) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	approvalID, err = uuid.Parse(chi.URLParam(r, "approvalID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid approval ID")
		return
	}
	if h.tiers == nil || h.applier == nil || h.tierSvc == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return
	}
	claims = auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	orgID, err = uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	return spaceID, approvalID, claims, orgID, true
}

func respondDecideError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workflow.ErrNotAnApprover):
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden,
			"you are not an approver for this transition")
	case errors.Is(err, workflow.ErrDeclineReasonRequired):
		// 400 VALIDATION_ERROR, so friendlyErrorMessage passes the sentence
		// through to the approver unchanged rather than collapsing it into a
		// generic fallback — the same reason tiergate.Refused uses that code.
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"a decline must say why: add a reason the requester can act on")
	case errors.Is(err, workflow.ErrApprovalAlreadyDecided):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict,
			"this approval has already been decided")
	case errors.Is(err, workflow.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "approval not found")
	case errors.Is(err, workflow.ErrInvalidTransition):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict,
			"the transition this approval was requested for no longer exists")
	case errors.Is(err, workflow.ErrTransitionRaced):
		// The item left the status this approval captured, so applying the
		// verdict would have written stale data over fresh. Nothing was
		// recorded — including the decision, which rolled back with it — so the
		// approval is still pending and can be decided against what the item
		// actually is now.
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict,
			"this item has changed since the approval was requested, so the decision was not recorded; review it again")
	case errors.Is(err, workflow.ErrPostFunctionUnknown), errors.Is(err, workflow.ErrPostFunctionMalformed):
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation,
			"this transition is configured with an action this server cannot perform, so it was not applied")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to record the decision")
	}
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// resolveTransition parses {transitionID} and proves the transition belongs to a
// workflow that belongs to {orgID}.
//
// Every tier route calls it. The pre-existing workflow routes resolve
// {workflowID} without an org check — reported as an inherited finding — and
// this is how the new surface avoids inheriting it.
func (h *Handler) resolveTransition(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if h.tierStore == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return uuid.Nil, false
	}

	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org ID")
		return uuid.Nil, false
	}
	workflowID, err := workflowIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid workflow ID")
		return uuid.Nil, false
	}
	transitionID, err := transitionIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid transition ID")
		return uuid.Nil, false
	}

	if _, err := h.q.GetWorkflowInOrg(r.Context(), generated.GetWorkflowInOrgParams{
		ID: workflowID, OrgID: orgID,
	}); err != nil {
		// 404 rather than 403: a workflow in another org must not be
		// distinguishable from one that does not exist.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "workflow not found")
		return uuid.Nil, false
	}

	transition, err := h.q.GetWorkflowTransition(r.Context(), transitionID)
	if err != nil || transition.WorkflowID != workflowID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "transition not found")
		return uuid.Nil, false
	}
	return transitionID, true
}

// logTierEvent records a configuration change. Convention A — the handler
// mutates, then audits — because tier configuration carries no atomicity
// contract: one row changes, and a lost audit row does not make a partially
// applied change.
func (h *Handler) logTierEvent(r *http.Request, t audit.EventType, kind string, id uuid.UUID, meta map[string]string) {
	if h.auditLog == nil {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		return
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: t, ActorID: claims.UserID.String(), OrgID: claims.OrgID,
		ResourceType: kind, ResourceID: id.String(), Metadata: meta,
	})
}

func (h *Handler) logApprovalDecision(r *http.Request, a workflow.Approval) {
	meta := map[string]string{
		"entity_type": string(a.EntityType),
		"entity_id":   a.EntityID.String(),
		"from_status": a.FromStatus,
		"to_status":   a.ToStatus,
	}
	if a.Decision != nil {
		meta["decision"] = string(*a.Decision)
	}
	h.logTierEvent(r, audit.EventTypeWorkflowApprovalDecided, "approval", a.ID, meta)
}
