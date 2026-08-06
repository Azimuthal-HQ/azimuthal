// Package tickets provides HTTP handlers for service desk endpoints.
package tickets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/tiergate"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// NotificationEnqueuer enqueues in-app notification jobs.
type NotificationEnqueuer interface {
	EnqueueNotification(ctx context.Context, args jobs.NotificationArgs) error
}

// Handler holds the dependencies for ticket HTTP handlers.
type Handler struct {
	svc      *tickets.TicketService
	auditLog audit.Logger
	notifs   NotificationEnqueuer
	// suggestions backs the org-scoped ticket_ref typeahead. Held separately
	// from svc because it reads across spaces, which no space-scoped ticket
	// route does — see tickets.SuggestionStore.
	suggestions *tickets.SuggestionService
	// tiers evaluates ADR-0011 conditions, validators and approvals for a
	// status change; applier writes the change and its post-function effects in
	// one transaction.
	//
	// Both are pointer/interface kinds and therefore covered by
	// TestHarness_NoDarkDependencies, which fails by field name the moment the
	// harness stops mirroring cmd/server/main.go. A nil gate does NOT skip the
	// tiers — see TransitionStatus.
	tiers   *tiergate.Gate
	applier workflow.TransitionApplier
	// requesters resolves the external identity behind a portal-raised ticket
	// (migration 044's requester_id). Without it the agent surface has only a
	// null reporter_id to go on, and renders "Unknown".
	//
	// Interface kind, so TestHarness_NoDarkDependencies fails by field name the
	// moment the harness stops mirroring cmd/server/main.go. A nil lookup does
	// NOT quietly serialise "no requester" — see resolveRequesters.
	requesters RequesterLookup
	// customFields backs the per-ticket field-value routes (migration 053 made
	// values polymorphic; scopes attach fields to Beacon ticket forms). Pointer
	// kind, so TestHarness_NoDarkDependencies fails by field name if the
	// harness stops mirroring cmd/server/main.go. A nil service answers the
	// conventional feature-disabled 404 — see customFieldsEnabled.
	customFields *customfields.Service
}

// NewHandler creates a ticket Handler.
func NewHandler(svc *tickets.TicketService) *Handler {
	return &Handler{svc: svc, auditLog: audit.NewLogger()}
}

// WithWorkflowTiers attaches the ADR-0011 tier gate and the transactional
// applier. Both are required in any wiring that mounts the status route; see
// the field comments.
func (h *Handler) WithWorkflowTiers(g *tiergate.Gate, a workflow.TransitionApplier) *Handler {
	h.tiers = g
	h.applier = a
	return h
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

// WithRequesterLookup attaches the external-requester resolver. Required in
// any wiring that mounts the ticket read paths in an org that runs a customer
// portal; see the field comment.
func (h *Handler) WithRequesterLookup(l RequesterLookup) *Handler {
	h.requesters = l
	return h
}

// WithSuggestions attaches the ticket_ref suggestion service, enabling
// SuggestRefs. Optional in the builder because the route it backs is
// org-scoped and registered outside Routes().
func (h *Handler) WithSuggestions(s *tickets.SuggestionService) *Handler {
	h.suggestions = s
	return h
}

// WithCustomFields attaches the custom-fields service, enabling the per-ticket
// field-value routes. The same service instance the project handler holds —
// definitions and scopes are org-level, and two instances would still agree,
// but one is what production wires.
func (h *Handler) WithCustomFields(s *customfields.Service) *Handler {
	h.customFields = s
	return h
}

// Routes returns a chi.Router with all ticket endpoints mounted. It carries
// the space-scoped family only — the org-scoped ticket_ref typeahead
// (SuggestRefs) is registered directly on the org group, because it reads
// across every space the caller can see rather than one named in the URL.
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

	// Custom field values (per ticket) — the Beacon side of the polymorphic
	// value store, mirroring the item routes in internal/core/api/projects.
	r.Get("/{ticketID}/fields", h.GetTicketFields)
	r.Put("/{ticketID}/fields/{slug}", h.SetTicketField)
	return r
}

type createTicketRequest struct {
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Priority    tickets.Priority `json:"priority"`
	AssigneeID  *uuid.UUID       `json:"assignee_id,omitempty"`
	Labels      []string         `json:"labels,omitempty"`
	// DueAt lets a ticket be born with a due date. CreateTicketParams has
	// carried the field all along and TicketService.Create writes it to the
	// model; nothing ever populated it from a request, so it was dead.
	DueAt *time.Time `json:"due_at,omitempty"`
}

// updateTicketRequest is a PATCH body: every field is optional and only the
// fields present in the JSON are applied.
//
// This shape is deliberately identical to updateItemRequest in
// internal/core/api/projects, because the two modules had drifted apart on it
// and only one of them was right.
//
// Title, Description and Priority are pointers because they were plain values
// assigned unconditionally onto the stored ticket. An omitted title decoded as
// "" and was written over the stored one, where TicketService.Update rejected
// it with ErrTitleRequired — so a request that meant "just change the due
// date" could not be expressed at all: every PATCH had to resend the whole
// ticket, and any field the client did not know about was silently blanked.
// Vector fixed exactly this on the item side; Beacon never did, and no
// frontend surface called this route, so nothing had yet noticed.
//
// DueAt needs three states, not two: absent (leave it alone), explicit null
// (clear it), and a value. respond.OptionalField keeps them apart. Sharing the
// type with the item PATCH is what stops the two modules disagreeing about
// what an absent due_at means — the disagreement that quietly destroyed item
// due dates before it was found.
type updateTicketRequest struct {
	Title       *string                          `json:"title"`
	Description *string                          `json:"description"`
	Priority    *tickets.Priority                `json:"priority"`
	Labels      []string                         `json:"labels,omitempty"`
	DueAt       respond.OptionalField[time.Time] `json:"due_at"`
}

// applyTicketPatch copies only the fields the request body actually carried
// onto the stored ticket. An absent field keeps its stored value — including
// DueAt, where only an explicit null means "clear it".
//
// The mirror of applyItemPatch in internal/core/api/projects. Kept as its own
// function for the same reason: the three-state rule is easy to state and easy
// to get wrong inline, and a reviewer can check this in isolation.
func applyTicketPatch(existing *tickets.Ticket, req updateTicketRequest) {
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	if req.Labels != nil {
		existing.Labels = req.Labels
	}
	// Only when the key was actually present. A nil Value with Set true is an
	// explicit null and does mean "clear it" — that is how the due-date control
	// on ticket detail clears a date.
	if req.DueAt.Set {
		existing.DueAt = req.DueAt.Value
	}
}

type transitionRequest struct {
	Status tickets.Status `json:"status"`
}

type assignRequest struct {
	AssigneeID *uuid.UUID `json:"assignee_id"`
}

// List returns all tickets in a space.
//
// @Summary      List tickets
// @Description  Returns all tickets in the specified space.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   api.SwaggerTicketResponse  "List of tickets"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets [get]
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
	h.respondTickets(w, r, http.StatusOK, result)
}

// Create creates a new ticket.
//
// @Summary      Create ticket
// @Description  Creates a new ticket in the specified space. Reporter is set from the JWT.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreateTicketRequest  true  "Ticket details"
// @Success      201      {object}  api.SwaggerTicketResponse       "Ticket created"
// @Failure      400      {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets [post]
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

	// Born INSIDE the space's state machine, both position columns written
	// together. The status text was already right for the seeded ticket
	// workflow, but only because that workflow happens to have a state named
	// "open" — a coincidence (D85), not a design, and one that fails for any
	// workflow an administrator starts elsewhere. workflow_state_id was not
	// written at all (D71).
	params := tickets.CreateTicketParams{
		SpaceID:     spaceID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		ReporterID:  claims.UserID,
		AssigneeID:  req.AssigneeID,
		Labels:      req.Labels,
		DueAt:       req.DueAt,
	}
	if h.tiers != nil {
		if status, stateID, ok := h.tiers.InitialPosition(r.Context(), spaceID); ok {
			params.Status, params.WorkflowStateID = tickets.Status(status), stateID
		}
	}

	ticket, err := h.svc.Create(r.Context(), params)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypeTicketCreated, ActorID: claims.UserID.String(),
		OrgID: claims.OrgID, ResourceType: "ticket", ResourceID: ticket.ID.String(),
	})
	h.respondTicket(w, r, http.StatusCreated, ticket)
}

// Get returns a single ticket by ID.
//
// @Summary      Get ticket
// @Description  Returns a single ticket by ID.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {object}  api.SwaggerTicketResponse  "Ticket details"
// @Failure      400       {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401       {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      500       {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	// The route's middleware proved the caller may read {spaceID}. It proved
	// nothing about {ticketID}, so the read is scoped to the space and a ticket
	// belonging to another one 404s exactly as a missing ticket does.
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	ticket, err := h.svc.GetInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	h.respondTicket(w, r, http.StatusOK, ticket)
}

// Update modifies an existing ticket.
//
// @Summary      Update ticket
// @Description  Updates an existing ticket. Every field is optional: a field the body omits keeps its stored value, and a null due_at clears the stored due date.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string                          true  "Space ID (UUID)"
// @Param        ticketID  path      string                          true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerUpdateTicketRequest  true  "Updated fields"
// @Success      200       {object}  api.SwaggerTicketResponse       "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401       {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse        "Not found"
// @Failure      500       {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	var req updateTicketRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// Scoped to {spaceID}: the ticket the permission check is about has to be
	// the ticket in the space the caller was authorised against, not whichever
	// ticket the id happens to name.
	existing, err := h.svc.GetInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, creatorOf(existing)) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	applyTicketPatch(existing, req)

	if err := h.svc.Update(r.Context(), existing); err != nil {
		handleTicketError(w, r, err)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeTicketUpdated, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "ticket", ResourceID: id.String(),
		})
	}
	h.respondTicket(w, r, http.StatusOK, existing)
}

// Delete soft-deletes a ticket.
//
// @Summary      Delete ticket
// @Description  Soft-deletes a ticket by ID.
// @Tags         tickets
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path  string  true  "Space ID (UUID)"
// @Param        ticketID  path  string  true  "Ticket ID (UUID)"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	// Scoped to {spaceID}. This read is also what keeps the delete below from
	// addressing another space: the transactional deleter takes a ticket id
	// alone, so a ticket outside {spaceID} has to be refused here or nowhere.
	existing, err := h.svc.GetInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, creatorOf(existing)) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	actorID := creatorOf(existing)
	if claims != nil {
		actorID = claims.UserID
	}
	// Delete revokes the ticket's shares in the same transaction (ADR-0008
	// rule 10); actorID attributes the share.revoked audit rows.
	if err := h.svc.Delete(r.Context(), id, spaceID, actorID); err != nil {
		handleTicketError(w, r, err)
		return
	}
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeTicketDeleted, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "ticket", ResourceID: id.String(),
		})
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
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string                          true  "Space ID (UUID)"
// @Param        ticketID  path      string                          true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerTransitionRequest    true  "New status"
// @Success      200       {object}  api.SwaggerTicketResponse       "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse        "Invalid status"
// @Failure      401       {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse        "Not found"
// @Failure      409       {object}  api.SwaggerErrorResponse        "Invalid transition"
// @Failure      500       {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/status [post]
func (h *Handler) TransitionStatus(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapTransitionAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req transitionRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// Scoped to {spaceID}, because the capability check above named that space
	// and the ticket id named nothing: the workflow, the state machine and the
	// audit row must all be about a ticket the caller was authorised for.
	current, getErr := h.svc.GetInSpace(r.Context(), spaceID, id)
	if getErr != nil {
		handleTicketError(w, r, getErr)
		return
	}

	// Resolved here rather than at the top of the handler so a missing ticket
	// still answers 404 before anything else, exactly as it did before this
	// phase. RequireAuth makes the nil case unreachable in the mounted router;
	// the handler unit tests reach it directly.
	claims, orgID, ok := actorFromRequest(w, r)
	if !ok {
		return
	}

	gated, ok := h.gateTicketTransition(w, r, orgID, spaceID, claims.UserID, current, req.Status)
	if !ok {
		return
	}

	// The hardcoded state machine decides legality ONLY where there is no
	// workflow to decide it.
	//
	// It used to run first and always, which made it the authority and left the
	// workflow adding restrictions on top of a rule it did not agree with. Now
	// a space with a workflow is adjudicated by that workflow, and this map is
	// what a space WITHOUT one falls back to — a codex-typed space, or one whose
	// best-effort assignment at create time failed, or one whose workflow was
	// deleted (spaces.workflow_id is ON DELETE SET NULL).
	//
	// The two agree by construction for the shipped workflow: migration 029 and
	// seedTicketWorkflow write the same eleven edges validTransitions holds,
	// reverse edges included. That is not a coincidence to rely on silently —
	// TestTicketStateMachine_MatchesTheSeededWorkflow asserts the two are equal
	// and fails if either side is edited alone.
	if gated.NoWorkflow {
		if err := tickets.ValidateTransition(current.Status, req.Status); err != nil {
			handleTicketError(w, r, err)
			return
		}
	}

	h.applyTicketTransition(w, r, ticketTransition{
		ticketID: id, spaceID: spaceID, orgID: orgID,
		actorID: claims.UserID, actorOrgID: claims.OrgID,
		status: req.Status, expectStatus: current.Status, gated: gated,
	})
}

// ticketTransition carries the resolved inputs of one status change from
// TransitionStatus to the write below, so neither function has to reread them.
type ticketTransition struct {
	ticketID uuid.UUID
	spaceID  uuid.UUID
	orgID    uuid.UUID
	actorID  uuid.UUID
	// actorOrgID is the wire-string org id the audit event carries.
	actorOrgID string
	status     tickets.Status
	// expectStatus is the status the gate read, and the compare-and-swap value
	// the write carries. A concurrent transition between the read and the write
	// matches no rows and answers 409 rather than silently winning.
	expectStatus tickets.Status
	gated        workflow.TransitionDecision
}

// applyTicketTransition writes the status change the workflow permitted.
//
// Two paths, chosen by whether a workflow governs rather than by whether
// post-functions exist:
//
//   - NO WORKFLOW — the single UPDATE it has always been, with the audit row
//     written the way it has always been written (Convention A). This is the
//     untouched-space guarantee, and it is a real statement-level guarantee
//     rather than an aspiration: the same query, the same columns, the same
//     order.
//
//   - A WORKFLOW — the transactional applier, always, even with no effects.
//     That is a change from the previous split, and the reason is D71: `status`
//     and `workflow_state_id` must be written together or the second column
//     goes on meaning nothing, and the write must carry the compare-and-swap.
//     Both live in the applier's statement. Routing only the post-function case
//     through it would leave the ordinary case writing one column, which is the
//     defect.
func (h *Handler) applyTicketTransition(w http.ResponseWriter, r *http.Request, t ticketTransition) {
	if t.gated.NoWorkflow {
		ticket, err := h.svc.TransitionStatus(r.Context(), t.ticketID, t.spaceID, t.status)
		if err != nil {
			handleTicketError(w, r, err)
			return
		}
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeTicketStatusChange, ActorID: t.actorID.String(),
			OrgID: t.actorOrgID, ResourceType: "ticket", ResourceID: t.ticketID.String(),
			Metadata: map[string]string{"to": string(t.status)},
		})
		h.respondTicket(w, r, http.StatusOK, ticket)
		return
	}

	if err := h.applier.ApplyTransition(r.Context(), workflow.ApplyInput{
		EntityType:       workflow.ApprovalEntityTicket,
		EntityID:         t.ticketID,
		OrgID:            t.orgID,
		SpaceID:          t.spaceID,
		ActorID:          t.actorID,
		ToStatus:         string(t.status),
		ToStateID:        t.gated.ToStateID,
		ExpectFromStatus: string(t.expectStatus),
		TransitionID:     t.gated.TransitionID,
		Effects:          t.gated.Effects,
	}); err != nil {
		respondApplyError(w, r, err)
		return
	}

	ticket, err := h.svc.GetInSpace(r.Context(), t.spaceID, t.ticketID)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	h.respondTicket(w, r, http.StatusOK, ticket)
}

// respondApplyError renders a failed apply.
//
// A lost compare-and-swap is 409, not 500: the entity is fine, the request is
// fine, and what went wrong is that somebody else moved it first. Telling the
// client 500 there would invite a retry of a request that is now about a status
// the entity has left. Everything else rolled back, and the message says so,
// because "failed" without "nothing happened" leaves a user wondering which
// half landed.
func respondApplyError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, workflow.ErrTransitionRaced) {
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
		return
	}
	respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
		"the transition could not be completed and no change was made")
}

// gateTicketTransition runs the ADR-0011 tiers and reports whether the
// transition may proceed.
//
// It answers the request itself in every case that stops the transition — a
// validator refusal, a pending approval, or a misconfigured post-function — so
// the caller only has to distinguish "carry on" from "already handled".
func (h *Handler) gateTicketTransition(
	w http.ResponseWriter, r *http.Request,
	orgID, spaceID, actorID uuid.UUID, current *tickets.Ticket, target tickets.Status,
) (workflow.TransitionDecision, bool) {
	// A missing gate is a wiring fault, never a reason to skip the tiers. It
	// answers 500 rather than transitioning ungated: an approval an operator
	// configured must not be bypassable by a deployment that forgot to wire it.
	// TestHarness_NoDarkDependencies keeps this branch unreachable in tests, and
	// cmd/server/main.go keeps it unreachable in production.
	if h.tiers == nil || h.applier == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return workflow.TransitionDecision{}, false
	}

	gated, err := h.tiers.Evaluate(r.Context(), TicketGateRequest(orgID, spaceID, actorID, current, target))
	if err != nil {
		handleTierError(w, r, err)
		return workflow.TransitionDecision{}, false
	}
	if tiergate.Refused(w, r, gated) || tiergate.Pending(w, gated) {
		return workflow.TransitionDecision{}, false
	}
	return gated, true
}

// TicketGateRequest describes one ticket to the chokepoint.
//
// It is exported and shared rather than written inline, because the READ path —
// the transitions a client is offered, served from the workflows package — must
// describe the same ticket in the same terms as this write path. Two literals is
// exactly how a picker comes to be filtered against a different entity snapshot
// than the mutation it feeds, and the resulting disagreement is invisible: the
// picker offers a move and the server refuses it, with nothing to point at.
func TicketGateRequest(
	orgID, spaceID, actorID uuid.UUID, current *tickets.Ticket, target tickets.Status,
) tiergate.Request {
	return tiergate.Request{
		OrgID:          orgID,
		SpaceID:        spaceID,
		EntityType:     workflow.ApprovalEntityTicket,
		EntityID:       current.ID,
		ActorID:        actorID,
		CurrentStatus:  string(current.Status),
		TargetStatus:   string(target),
		CurrentStateID: current.WorkflowStateID,
		Entity: workflow.EntitySnapshot{
			AssigneeID:  current.AssigneeID,
			DueAt:       current.DueAt,
			Description: current.Description,
			Labels:      current.Labels,
		},
	}
}

// actorFromRequest resolves the caller's claims and org id, answering 401 and
// reporting false when either is absent.
func actorFromRequest(w http.ResponseWriter, r *http.Request) (*auth.Claims, uuid.UUID, bool) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return nil, uuid.Nil, false
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return nil, uuid.Nil, false
	}
	return claims, orgID, true
}

// handleTierError maps a tier failure onto a response.
//
// A post-function this build cannot perform aborts the transition with a named
// error rather than committing without it: a workflow that silently skipped a
// configured action would be worse than one that refused, because nothing would
// report the omission.
func handleTierError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workflow.ErrPostFunctionUnknown), errors.Is(err, workflow.ErrPostFunctionMalformed):
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation,
			"this transition is configured with an action this server cannot perform, so it was not applied")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to evaluate the transition")
	}
}

// Assign assigns a ticket to a user.
//
// @Summary      Assign ticket
// @Description  Assigns a ticket to a user by ID.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string                     true  "Space ID (UUID)"
// @Param        ticketID  path      string                     true  "Ticket ID (UUID)"
// @Param        body      body      api.SwaggerAssignRequest   true  "Assignee"
// @Success      200       {object}  api.SwaggerTicketResponse  "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse   "Invalid request"
// @Failure      401       {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      409       {object}  api.SwaggerErrorResponse   "Already assigned"
// @Failure      500       {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/assign [post]
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) { //nolint:cyclop // validation + capability check + assignment + notification fan-out
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req assignRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// null assignee_id means unassign
	if req.AssigneeID == nil {
		ticket, err := h.svc.Unassign(r.Context(), id, spaceID)
		if err != nil {
			handleTicketError(w, r, err)
			return
		}
		claims := auth.ClaimsFromContext(r.Context())
		if claims != nil {
			_ = h.auditLog.Log(r.Context(), audit.Event{
				Type: audit.EventTypeTicketUnassigned, ActorID: claims.UserID.String(),
				OrgID: claims.OrgID, ResourceType: "ticket", ResourceID: id.String(),
			})
		}
		h.respondTicket(w, r, http.StatusOK, ticket)
		return
	}

	var notifier tickets.AssignmentNotifier
	if h.notifs != nil {
		notifier = &queueAssignmentNotifier{enqueuer: h.notifs}
	}
	// {spaceID} goes to the service, not just to the capability check above:
	// this is the one pair of routes that writes without reading the ticket
	// first, so the scoped read inside Assign/Unassign is the only place the
	// two URL ids are reconciled.
	ticket, err := h.svc.Assign(r.Context(), id, spaceID, *req.AssigneeID, notifier)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeTicketAssigned, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "ticket", ResourceID: id.String(),
			Metadata: map[string]string{"assignee_id": req.AssigneeID.String()},
		})
	}
	h.respondTicket(w, r, http.StatusOK, ticket)
}

// Unassign removes the assignee from a ticket.
//
// @Summary      Unassign ticket
// @Description  Removes the current assignee from a ticket.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {object}  api.SwaggerTicketResponse  "Updated ticket"
// @Failure      400       {object}  api.SwaggerErrorResponse   "Invalid ID"
// @Failure      401       {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      500       {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/assign [delete]
func (h *Handler) Unassign(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	ticket, err := h.svc.Unassign(r.Context(), id, spaceID)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeTicketUnassigned, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "ticket", ResourceID: id.String(),
		})
	}
	h.respondTicket(w, r, http.StatusOK, ticket)
}

// Search performs full-text search on tickets.
//
// @Summary      Search tickets
// @Description  Full-text search on tickets in a space. Requires query parameter 'q'.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true   "Space ID (UUID)"
// @Param        q        query     string  true   "Search query"
// @Param        limit    query     int     false  "Max results (1-200, default 50)"
// @Success      200      {array}   api.SwaggerTicketResponse  "Search results"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Missing query"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/search [get]
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
	h.respondTickets(w, r, http.StatusOK, result)
}

// Kanban returns the kanban board view grouped by status.
//
// @Summary      Kanban board
// @Description  Returns tickets grouped by status for kanban board display.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   api.SwaggerKanbanColumn    "Kanban columns"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/kanban [get]
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
	h.respondKanban(w, r, board)
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

func orgIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing org ID: %w", err)
	}
	return id, nil
}

// creatorOf reports the internal user who raised a ticket, for the
// edit_own_items half of access.CanEditEntity.
//
// A PORTAL-RAISED TICKET HAS NO INTERNAL CREATOR, and uuid.Nil is the correct
// answer rather than a placeholder. CanEditEntity grants on
// `createdBy == res.UserID || Can(CapEditAnyItem, ...)`, and a resolved
// caller's UserID is never uuid.Nil, so the ownership half simply never
// matches for a portal ticket and editing it requires edit_any_item — which
// is right, because nobody inside the organisation raised it and "their own"
// does not apply to anyone. Substituting the assignee or the space creator
// here would silently hand ownership to somebody who never asked for it.
type setTicketFieldValueRequest struct {
	Value string `json:"value"`
}

// GetTicketFields returns a ticket's custom fields: definitions attached to
// this space's ticket form with their values and required flags, plus
// read-only values whose definitions are gone or unattached here.
//
// @Summary      Get ticket custom fields
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {array}   map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401       {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/fields [get]
func (h *Handler) GetTicketFields(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	ticketID, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	// The space goes into the value read with the ticket id: the route proved
	// {spaceID} readable and proved nothing about {ticketID}, so a ticket in
	// another space contributes no values — exactly as an unknown id does.
	fields, err := h.customFields.RenderForEntity(r.Context(), orgID, spaceID, customfields.EntityTypeTicket, ticketID)
	if err != nil {
		handleTicketFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, fields)
}

// SetTicketField writes a ticket's value for one custom field attached to
// this space's ticket form. An empty value clears it — unless the attachment
// marks the field required, in which case the clear is refused with an error
// naming the field. Legacy (undefined/archived/unattached) fields are
// read-only.
//
// @Summary      Set a ticket custom field value
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Param        slug      path      string  true  "Field slug"
// @Success      200       {object}  api.SwaggerMessageResponse
// @Failure      400       {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      401       {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404       {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/fields/{slug} [put]
func (h *Handler) SetTicketField(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	ticketID, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	slug := chi.URLParam(r, "slug")

	// Setting a field value is editing the ticket — gate exactly like Update
	// (edit_own for the reporter, edit_any otherwise).
	//
	// This read resolves the ticket through the space for the permission
	// check, and a ticket outside {spaceID} leaves through the ticket's own
	// 404 before anything is written. It is no longer the only
	// reconciliation: the write statement itself carries the space predicate
	// (UpsertEntityFieldValue), so the refusal holds for any caller.
	existing, err := h.svc.GetInSpace(r.Context(), spaceID, ticketID)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, creatorOf(existing)) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req setTicketFieldValueRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if err := h.customFields.SetValue(r.Context(), orgID, spaceID, customfields.EntityTypeTicket, ticketID, slug, req.Value); err != nil {
		handleTicketFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "field saved"})
}

// customFieldsEnabled reports whether the custom-fields service is wired,
// answering the conventional feature-disabled 404 when it is not.
func (h *Handler) customFieldsEnabled(w http.ResponseWriter, r *http.Request) bool {
	if h.customFields == nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "custom fields are not enabled")
		return false
	}
	return true
}

// handleTicketFieldError maps custom-field errors onto ticket responses. The
// not-found family answers with the TICKET's own 404 wording, byte-identical
// to GetInSpace's refusal, so the value routes cannot become an oracle the
// ticket routes are not.
func handleTicketFieldError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, customfields.ErrEntityNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, tickets.ErrNotFound.Error())
	case errors.Is(err, customfields.ErrUndefinedField),
		errors.Is(err, customfields.ErrFieldNotInScope):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, customfields.ErrInvalidValue),
		errors.Is(err, customfields.ErrInvalidEntityType),
		errors.Is(err, customfields.ErrValueRequired):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	default:
		respondUnmapped(w, r, err)
	}
}

func creatorOf(t *tickets.Ticket) uuid.UUID {
	if t == nil || t.ReporterID == nil {
		return uuid.Nil
	}
	return *t.ReporterID
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
	case errors.Is(err, tickets.ErrAssigneeNotOrgMember):
		// 400, not 404: the caller named a user, and the refusal is about that
		// user's membership rather than about the ticket's existence — which the
		// scoped read above has already settled. The grants surface answers the
		// same class the same way.
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, tickets.ErrAlreadyAssigned):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respondUnmapped(w, r, err)
	}
}

// respondUnmapped answers an error none of the arms above could classify.
//
// The error text does not reach the wire. Every arm above passes err.Error()
// having first established which sentinel it holds, and those strings are ours.
// The default arm has established nothing: what arrives here is whatever the
// layer below produced, and a Postgres error names the constraint it violated,
// the table and the SQLSTATE. known-issues #23 was filed against exactly that —
// a well-formed uuid naming no user reached the UPDATE, violated
// tickets_assignee_id_fkey, and the driver's sentence was handed to the caller.
//
// This is the change the hygiene pass (H5) made to the three project surfaces
// and explicitly left undone here; see respondUnmapped in
// internal/core/api/projects/handler.go for the longer note. The client gets a
// fixed message and the request id it already had — respond.Error puts it in the
// body and the RequestID middleware in the X-Request-ID header — while the full
// error goes to the server log under that same id. The detail moves rather than
// being discarded.
func respondUnmapped(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("unmapped handler error",
		"surface", "ticket",
		"error", err,
		"request_id", respond.RequestIDFromContext(r.Context()),
	)
	respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "ticket operation failed")
}

// queueAssignmentNotifier implements tickets.AssignmentNotifier via the job queue.
type queueAssignmentNotifier struct {
	enqueuer NotificationEnqueuer
}

func (n *queueAssignmentNotifier) NotifyAssignment(ctx context.Context, ticketID uuid.UUID, spaceID uuid.UUID, assigneeID uuid.UUID, title string) error {
	if err := n.enqueuer.EnqueueNotification(ctx, jobs.NotificationArgs{
		UserID:     assigneeID.String(),
		EventKind:  "ticket.assigned",
		Message:    "You have been assigned to: " + title,
		ResourceID: ticketID.String(),
		EntityKind: "ticket",
		SpaceID:    spaceID.String(),
	}); err != nil {
		return fmt.Errorf("enqueuing assignment notification: %w", err)
	}
	return nil
}
