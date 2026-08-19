// Package projects provides HTTP handlers for project tracking endpoints.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
)

// Handler holds the dependencies for project HTTP handlers.
//
// Relations are deliberately absent: they became an entity-generic satellite
// served by internal/core/api/relations, mounted per entity subtree in
// router.go the way comments are. The item relation URLs did not move — only
// their registration did.
type Handler struct {
	items   *projects.ItemService
	sprints *projects.SprintService
	backlog *projects.BacklogService
	roadmap *projects.RoadmapService
	// tags is the entity tag model (migration 055): project items carry the
	// same org-scoped tags pages do, and the tag routes in Routes() are
	// unconditional, so the service is a required constructor argument rather
	// than a With* option — a missing one does not compile.
	tags         *tags.Service
	itemTypes    *itemtypes.Service
	customFields *customfields.Service
	boardConfig  *projects.BoardConfigService
	auditLog     audit.Logger
	// tiers evaluates ADR-0011 conditions, validators and approvals for a
	// status change; applier writes the change and its post-function effects in
	// one transaction. Both are nil-able and therefore covered by
	// TestHarness_NoDarkDependencies. A nil gate does NOT skip the tiers.
	tiers   *tiergate.Gate
	applier workflow.TransitionApplier
}

// WithWorkflowTiers attaches the ADR-0011 tier gate and the transactional
// applier.
func (h *Handler) WithWorkflowTiers(g *tiergate.Gate, a workflow.TransitionApplier) *Handler {
	h.tiers = g
	h.applier = a
	return h
}

// NewHandler creates a project Handler.
func NewHandler(
	items *projects.ItemService,
	sprints *projects.SprintService,
	backlog *projects.BacklogService,
	roadmap *projects.RoadmapService,
	tagSvc *tags.Service,
) *Handler {
	return &Handler{
		items:    items,
		sprints:  sprints,
		backlog:  backlog,
		roadmap:  roadmap,
		tags:     tagSvc,
		auditLog: audit.NewLogger(),
	}
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithItemTypes attaches the item-types service, enabling the item-type
// endpoints and item-create type validation.
func (h *Handler) WithItemTypes(s *itemtypes.Service) *Handler {
	h.itemTypes = s
	return h
}

// WithCustomFields attaches the custom-fields service, enabling the custom-field
// definition endpoints and per-item field values.
func (h *Handler) WithCustomFields(s *customfields.Service) *Handler {
	h.customFields = s
	return h
}

// WithBoardConfig attaches the board-configuration service, enabling the
// per-space board configuration endpoints.
func (h *Handler) WithBoardConfig(s *projects.BoardConfigService) *Handler {
	h.boardConfig = s
	return h
}

// Routes returns a chi.Router with all project endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Items
	r.Get("/items", h.ListItems)
	r.Post("/items", h.CreateItem)
	r.Get("/items/search", h.SearchItems)
	r.Get("/items/resolve", h.ResolveItem)
	r.Get("/items/{itemID}", h.GetItem)
	r.Patch("/items/{itemID}", h.UpdateItem)
	r.Delete("/items/{itemID}", h.DeleteItem)
	r.Post("/items/{itemID}/status", h.UpdateItemStatus)
	r.Post("/items/{itemID}/sprint", h.AssignToSprint)
	r.Post("/items/{itemID}/rank", h.RankItem)

	// Custom field values (per item)
	r.Get("/items/{itemID}/fields", h.GetItemFields)
	r.Put("/items/{itemID}/fields/{slug}", h.SetItemField)

	// Entity tags (migration 055). Read is the space's, writing is the same
	// permission as editing the item.
	r.Get("/items/{itemID}/tags", h.ListItemTags)
	r.Put("/items/{itemID}/tags", h.SetItemTags)

	// Relations are mounted in router.go beside this subtree's comment routes,
	// not here: the satellite is entity-generic and every entity subtree
	// carries the same wrappers over one core (see api/relations).

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

	// Board configuration. Reading follows space read access; every write
	// follows space admin via CapManageSpace — no new capability.
	r.Get("/board/config", h.GetBoardConfig)
	r.Put("/board/config", h.SaveBoardConfig)
	r.Post("/board/config/reset", h.ResetBoardConfig)
	r.Delete("/board/config/columns/{columnID}", h.DeleteBoardColumn)

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
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// updateItemRequest is a PATCH body: every field is optional and only the
// fields present in the JSON are applied.
//
// Title, Description and Priority are pointers for exactly that reason. As
// plain strings, an omitted title decoded as "" and was assigned over the
// stored one, so a request that meant "just change the assignee" blanked the
// title and was then rejected by the service's own title-required rule. That
// made the assignee control on item detail — which sends assignee_id alone —
// fail with 400 every time.
//
// Kind is a pointer for the same reason and one more: it is validated against
// the org's active item types, so a plain string decoding as "" on an absent
// key would not merely blank the stored type — it would turn every PATCH that
// never mentioned kind into a 400, because "" is not an active type. Absent has
// to stay distinguishable from "the client asked for this type".
//
// AssigneeID and DueAt need three states, not two: absent (leave it alone),
// explicit null (clear it), and a value. A single pointer collapses the first
// two, and this used to resolve them as "clear" — which quietly destroyed
// data. Any PATCH that did not mention due_at wiped the stored due date, and
// since no frontend surface has ever sent due_at, *every* item edit cleared
// it: renaming an item removed it from the roadmap. The same shape meant a
// board drag sending only {"kind": …} unassigned the item as a side effect.
// respond.OptionalField keeps the three states apart, so absent now means
// absent. The Beacon ticket PATCH carries the same type for the same reason.
type updateItemRequest struct {
	Title       *string                          `json:"title"`
	Description *string                          `json:"description"`
	Kind        *string                          `json:"kind"`
	Priority    *string                          `json:"priority"`
	AssigneeID  respond.OptionalField[uuid.UUID] `json:"assignee_id"`
	DueAt       respond.OptionalField[time.Time] `json:"due_at"`
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

// completeSprintRequest carries the optional carry-over target for a sprint's
// incomplete items. Omitted or null next_sprint_id returns them to the backlog;
// a value moves them to that sprint. An empty request body is valid (backlog).
type completeSprintRequest struct {
	NextSprintID *uuid.UUID `json:"next_sprint_id,omitempty"`
}

type rankItemRequest struct {
	BeforeID *uuid.UUID `json:"before_id"`
	AfterID  *uuid.UUID `json:"after_id"`
}

type createLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// --- Item handlers ---

// ListItems returns all items in a space.
//
// @Summary      List project items
// @Description  Returns all items in a project space
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items [get]
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

// validateItemKind checks a requested type slug against the org's active item
// types, writing the error response itself; a false return means the caller must
// stop and write nothing further.
//
// This is the only integrity check standing behind project_items.kind. Migration
// 032 repurposed that column as the org-editable item-type slug and dropped the
// four-value CHECK that used to constrain it (D49), and referential integrity for
// types is deliberately a service-layer rule rather than a foreign key, so that
// an ordinary item insert is not coupled to per-org type seeding. The consequence
// is that the database will accept a typo, an archived slug or arbitrary junk in
// this column without complaint. Every write path that sets kind must come
// through here, and a second copy of this logic would be a defect.
//
// The org-types lookup lives in the handler rather than the item service because
// this is where org context is available; the item service is space-scoped.
//
// A nil itemTypes service means the item-type surface was never attached to this
// handler, and validation is skipped. That check is deliberately inside this
// method rather than at each call site: a caller that forgot the guard would
// otherwise dereference a nil service and panic, and the "no service means no
// validation" rule belongs in exactly one place.
func (h *Handler) validateItemKind(w http.ResponseWriter, r *http.Request, kind string) bool {
	if h.itemTypes == nil {
		return true
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return false
	}
	ok, err := h.itemTypes.IsActiveType(r.Context(), orgID, kind)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to validate item type")
		return false
	}
	if !ok {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "unknown or archived item type")
		return false
	}
	return true
}

// CreateItem creates a new project item.
//
// @Summary      Create a project item
// @Description  Creates a new item in a project space
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                      true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreateItemRequest  true  "Item details"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items [post]
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

	// A blank or unknown type is rejected here (the frontend picker defaults to
	// task, so real requests always carry one).
	if !h.validateItemKind(w, r, req.Kind) {
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
		DueAt:       req.DueAt,
	}
	// Born INSIDE the space's state machine, both position columns written
	// together. Without this an item started at the literal "open", which names
	// no state in the seeded project workflow, so its first transition resolved
	// no edge and nothing configured on the initial edge ever applied to it
	// (D72). A space with no workflow leaves both zero and the service's own
	// default stands, exactly as before.
	if h.tiers != nil {
		if status, stateID, ok := h.tiers.InitialPosition(r.Context(), spaceID); ok {
			item.Status, item.WorkflowStateID = status, stateID
		}
	}

	created, err := h.items.CreateItem(r.Context(), item)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypeItemCreated, ActorID: claims.UserID.String(),
		OrgID: claims.OrgID, ResourceType: "item", ResourceID: created.ID.String(),
	})
	respond.JSON(w, http.StatusCreated, created)
}

// GetItem returns a single item by ID.
//
// @Summary      Get a project item
// @Description  Returns a single project item by its ID
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID} [get]
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	item, err := h.items.GetItemInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, item)
}

// applyItemPatch copies only the fields the request body actually carried onto
// the stored item. An absent field keeps its stored value — including
// AssigneeID and DueAt, where only an explicit null means "clear it".
//
// This comment used to say the opposite of the code below it: that for
// AssigneeID and DueAt "absent and null both mean clear it". That described
// the defect the optionalField change fixed, not the behaviour it left, and it
// contradicted the updateItemRequest comment twenty lines above. Corrected
// here rather than copied onto the ticket handler alongside it.
func applyItemPatch(existing *projects.Item, req updateItemRequest) {
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	// Callers must have run validateItemKind on a non-nil Kind before reaching
	// here; this function only copies, it does not check.
	if req.Kind != nil {
		existing.Kind = *req.Kind
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	// Only when the key was actually present. A nil Value with Set true is an
	// explicit null and does mean "clear it" — that is how item detail
	// unassigns, and it still works.
	if req.AssigneeID.Set {
		existing.AssigneeID = req.AssigneeID.Value
	}
	if req.DueAt.Set {
		existing.DueAt = req.DueAt.Value
	}
}

// UpdateItem modifies an existing item.
//
// @Summary      Update a project item
// @Description  Modifies an existing project item
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        itemID   path      string                        true  "Item ID (UUID)"
// @Param        body     body      api.SwaggerUpdateItemRequest   true  "Updated item details"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID} [patch]
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	var req updateItemRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	existing, err := h.items.GetItemInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, existing.ReporterID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	// Only a kind the request actually carried is validated. An absent one must
	// not be checked at all: it means the client never mentioned the type, and a
	// stored slug that has since been archived has to stay editable in every
	// other respect. Validation runs before applyItemPatch so a rejected type
	// leaves nothing written — and after the permission check, so an unauthorised
	// caller cannot probe the org's type vocabulary through the error it gets.
	if req.Kind != nil && !h.validateItemKind(w, r, *req.Kind) {
		return
	}

	applyItemPatch(existing, req)

	updated, err := h.items.UpdateItem(r.Context(), existing)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeItemUpdated, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "item", ResourceID: id.String(),
		})
	}
	respond.JSON(w, http.StatusOK, updated)
}

// DeleteItem soft-deletes an item.
//
// @Summary      Delete a project item
// @Description  Soft-deletes a project item
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      204      "No Content"
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID} [delete]
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	existing, err := h.items.GetItemInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, existing.ReporterID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	actorID := existing.ReporterID
	if claims != nil {
		actorID = claims.UserID
	}
	// Delete revokes the item's shares in the same transaction (ADR-0008
	// rule 10); actorID attributes the share.revoked audit rows.
	if err := h.items.DeleteItem(r.Context(), id, spaceID, actorID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeItemDeleted, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "item", ResourceID: id.String(),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateItemStatus changes the status of an item.
//
// @Summary      Update item status
// @Description  Changes the status of a project item
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                    true  "Space ID (UUID)"
// @Param        itemID   path      string                    true  "Item ID (UUID)"
// @Param        body     body      api.SwaggerStatusRequest   true  "New status"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      409      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/status [post]
func (h *Handler) UpdateItemStatus(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
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

	var req statusRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// Read before writing, and now the read decides as well as informs.
	//
	// This route wrote any string it was given. That is no longer true where a
	// workflow governs: the workflow names the legal statuses and the legal
	// moves between them, and one it does not name is refused. Where a space has
	// NO workflow the route keeps its original behaviour — no state machine,
	// any string — because that is the only rule it ever had and inventing one
	// for those spaces is not this change's business.
	current, getErr := h.items.GetItemInSpace(r.Context(), spaceID, id)
	if getErr != nil {
		handleProjectError(w, r, getErr)
		return
	}

	gated, ok := h.gateItemTransition(w, r, spaceID, current, req.Status)
	if !ok {
		return
	}

	h.applyItemTransition(w, r, itemTransition{
		itemID: id, spaceID: spaceID, status: req.Status,
		expectStatus: current.Status, gated: gated,
	})
}

// itemTransition carries the resolved inputs of one item status change.
type itemTransition struct {
	itemID  uuid.UUID
	spaceID uuid.UUID
	status  string
	// expectStatus is the status the gate read, and the compare-and-swap value
	// the write carries; see the ticket twin.
	expectStatus string
	gated        workflow.TransitionDecision
}

// gateItemTransition runs the ADR-0011 tiers and reports whether the transition
// may proceed, answering the request itself in every case that stops it.
func (h *Handler) gateItemTransition(
	w http.ResponseWriter, r *http.Request, spaceID uuid.UUID, current *projects.Item, target string,
) (workflow.TransitionDecision, bool) {
	if h.tiers == nil || h.applier == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"workflow tier evaluation is not configured on this server")
		return workflow.TransitionDecision{}, false
	}
	claims, orgID, ok := actorFromRequest(w, r)
	if !ok {
		return workflow.TransitionDecision{}, false
	}

	// See the ticket twin: the guard snapshot's tag slugs come from
	// entity_tags, and an unreadable tag set refuses the transition rather
	// than evaluating a tag guard against nothing.
	itemTags, err := h.tags.ForEntity(r.Context(), tags.EntityRef{
		Type: tags.EntityProjectItem, ID: current.ID, SpaceID: spaceID,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"the item's tags could not be read")
		return workflow.TransitionDecision{}, false
	}

	gated, err := h.tiers.Evaluate(r.Context(), ItemGateRequest(orgID, spaceID, claims.UserID, current, target, tags.SlugsOf(itemTags)))
	if err != nil {
		handleTierError(w, r, err)
		return workflow.TransitionDecision{}, false
	}
	if tiergate.Refused(w, r, gated) || tiergate.Pending(w, gated) {
		return workflow.TransitionDecision{}, false
	}
	return gated, true
}

// ItemGateRequest describes one project item to the chokepoint.
//
// Exported and shared with the read path for the same reason its ticket twin
// is: the transitions a client is OFFERED must be filtered against the same
// entity snapshot the mutation is checked against, or the picker and the server
// disagree about the same item.
func ItemGateRequest(
	orgID, spaceID, actorID uuid.UUID, current *projects.Item, target string, tagSlugs []string,
) tiergate.Request {
	return tiergate.Request{
		OrgID:          orgID,
		SpaceID:        spaceID,
		EntityType:     workflow.ApprovalEntityItem,
		EntityID:       current.ID,
		ActorID:        actorID,
		CurrentStatus:  current.Status,
		TargetStatus:   target,
		CurrentStateID: current.WorkflowStateID,
		Entity: workflow.EntitySnapshot{
			AssigneeID:  current.AssigneeID,
			DueAt:       current.DueAt,
			Description: current.Description,
			Tags:        tagSlugs,
		},
	}
}

// applyItemTransition writes the status change the workflow permitted.
//
// The split is by whether a workflow governs, not by whether post-functions
// exist — see the ticket twin, applyTicketTransition, for why: `status` and
// `workflow_state_id` have to be written together (D71) and the write has to
// carry the compare-and-swap, and both of those live in the applier's
// statement. A space with no workflow keeps the single UPDATE and the
// Convention A audit row it has always had.
func (h *Handler) applyItemTransition(w http.ResponseWriter, r *http.Request, t itemTransition) {
	claims, orgID, ok := actorFromRequest(w, r)
	if !ok {
		return
	}

	if t.gated.NoWorkflow {
		item, err := h.items.UpdateItemStatus(r.Context(), t.itemID, t.status)
		if err != nil {
			handleProjectError(w, r, err)
			return
		}
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeItemStatusChange, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "item", ResourceID: t.itemID.String(),
			// from/to both, so History can render old -> new. expectStatus is the
			// status the gate read and the CAS matched — the state left behind.
			Metadata: map[string]string{"from": t.expectStatus, "to": t.status},
		})
		respond.JSON(w, http.StatusOK, item)
		return
	}

	if err := h.applier.ApplyTransition(r.Context(), workflow.ApplyInput{
		EntityType:       workflow.ApprovalEntityItem,
		EntityID:         t.itemID,
		OrgID:            orgID,
		SpaceID:          t.spaceID,
		ActorID:          claims.UserID,
		ToStatus:         t.status,
		ToStateID:        t.gated.ToStateID,
		ExpectFromStatus: t.expectStatus,
		TransitionID:     t.gated.TransitionID,
		Effects:          t.gated.Effects,
	}); err != nil {
		respondApplyError(w, r, err)
		return
	}

	// Scoped read-back, even though the gate already loaded this item within
	// the space: the itemTransition carries its space, so using it costs
	// nothing and leaves no unscoped read anywhere on a request path.
	item, err := h.items.GetItemInSpace(r.Context(), t.spaceID, t.itemID)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, item)
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

// respondApplyError renders a failed apply. A lost compare-and-swap is 409, not
// 500: the item is fine and the request was fine — somebody else moved it
// first. See the ticket twin for the full reasoning.
func respondApplyError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, workflow.ErrTransitionRaced) {
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
		return
	}
	respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
		"the transition could not be completed and no change was made")
}

// handleTierError maps a tier failure onto a response. A post-function this
// build cannot perform aborts the transition rather than committing without it.
func handleTierError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workflow.ErrPostFunctionUnknown), errors.Is(err, workflow.ErrPostFunctionMalformed):
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation,
			"this transition is configured with an action this server cannot perform, so it was not applied")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to evaluate the transition")
	}
}

// AssignToSprint assigns an item to a sprint.
//
// @Summary      Assign item to sprint
// @Description  Assigns a project item to a sprint
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        itemID   path      string                          true  "Item ID (UUID)"
// @Param        body     body      api.SwaggerSprintAssignRequest   true  "Sprint assignment"
// @Success      200      {object}  api.SwaggerMessageResponse
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/sprint [post]
func (h *Handler) AssignToSprint(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
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

	var req sprintAssignRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// The capability was asked about {spaceID}; neither {itemID} nor the
	// sprint id in the body has been reconciled with it yet.
	if err := h.items.AssignToSprint(r.Context(), id, spaceID, req.SprintID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item assigned to sprint"})
}

// RankItem repositions an item relative to its neighbours in the backlog.
//
// @Summary      Reorder a project item
// @Description  Repositions a project item by specifying the item that should come immediately before it (before_id) and/or after it (after_id). All items in the space are renumbered to reflect the new order.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string           true  "Space ID (UUID)"
// @Param        itemID   path      string           true  "Item ID (UUID)"
// @Param        body     body      rankItemRequest   true  "Neighbour IDs"
// @Success      200      {object}  api.SwaggerMessageResponse
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/rank [post]
func (h *Handler) RankItem(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}

	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req rankItemRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if err := h.backlog.RankItemRelative(r.Context(), spaceID, id, req.BeforeID, req.AfterID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item reordered"})
}

// SearchItems performs full-text search on items.
//
// @Summary      Search project items
// @Description  Performs full-text search on project items
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true   "Space ID (UUID)"
// @Param        q        query     string  true   "Search query"
// @Param        limit    query     int     false  "Maximum results (default 50, max 200)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/search [get]
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

// ResolveItem resolves a human-readable item key (e.g. VEC-123) to an item.
//
// Routing stays by UUID everywhere else; this is the one key → item lookup,
// scoped to the org that owns the space in the URL. It is the same resolution
// path the importer will call.
//
// @Summary      Resolve an item by key
// @Description  Resolves a human-readable item key (e.g. VEC-123) to a project item within the org
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        key      query     string  true  "Item key (e.g. VEC-123)"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/resolve [get]
func (h *Handler) ResolveItem(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "query parameter 'key' is required")
		return
	}

	item, err := h.items.ResolveKey(r.Context(), orgID, key)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}

	// Resolution is org-wide, but the space read-guard on this route only
	// covers the space in the URL. The resolved item may live in a different
	// space the caller cannot read — gate on that item's own space and return
	// 404 (not 403) so the endpoint never reveals the existence of items in
	// spaces the caller has no access to.
	if !access.Can(r.Context(), access.CapReadItems, item.SpaceID) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}

	respond.JSON(w, http.StatusOK, item)
}

// --- Sprint handlers ---

// ListSprints returns all sprints in a space.
//
// @Summary      List sprints
// @Description  Returns all sprints in a project space
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints [get]
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
//
// @Summary      Create a sprint
// @Description  Creates a new sprint in a project space
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreateSprintRequest   true  "Sprint details"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints [post]
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
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
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
//
// @Summary      Get a sprint
// @Description  Returns a single sprint by its ID
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        sprintID  path      string  true  "Sprint ID (UUID)"
// @Success      200       {object}  map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      404       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID} [get]
func (h *Handler) GetSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	sprint, err := h.sprints.GetSprint(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// UpdateSprint modifies an existing sprint.
//
// @Summary      Update a sprint
// @Description  Modifies an existing sprint
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string                          true  "Space ID (UUID)"
// @Param        sprintID  path      string                          true  "Sprint ID (UUID)"
// @Param        body      body      api.SwaggerUpdateSprintRequest   true  "Updated sprint details"
// @Success      200       {object}  map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      404       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID} [put]
func (h *Handler) UpdateSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
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

	var req updateSprintRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	existing, err := h.sprints.GetSprint(r.Context(), spaceID, id)
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
//
// @Summary      Start a sprint
// @Description  Transitions a sprint to active status
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        sprintID  path      string  true  "Sprint ID (UUID)"
// @Success      200       {object}  map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      404       {object}  api.SwaggerErrorResponse
// @Failure      409       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/start [post]
func (h *Handler) StartSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
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

	sprint, err := h.sprints.StartSprint(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// CompleteSprint transitions a sprint to completed.
//
// @Summary      Complete a sprint
// @Description  Transitions a sprint to completed status
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        sprintID  path      string  true  "Sprint ID (UUID)"
// @Param        body      body      api.SwaggerCompleteSprintRequest  false  "Optional carry-over target for incomplete items"
// @Success      200       {object}  map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      404       {object}  api.SwaggerErrorResponse
// @Failure      409       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/complete [post]
func (h *Handler) CompleteSprint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
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

	// The disposition body is optional: no body (or null next_sprint_id) sends
	// incomplete items back to the backlog. A present body names a carry-over
	// sprint. Tolerate an empty body — EOF is the default-backlog case.
	var req completeSprintRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
			return
		}
	}

	sprint, err := h.sprints.CompleteSprint(r.Context(), spaceID, id, projects.CompleteOptions{NextSprintID: req.NextSprintID})
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, sprint)
}

// ListSprintItems returns items assigned to a sprint.
//
// @Summary      List sprint items
// @Description  Returns all items assigned to a sprint
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        sprintID  path      string  true  "Sprint ID (UUID)"
// @Success      200       {array}   map[string]interface{}
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints/{sprintID}/items [get]
func (h *Handler) ListSprintItems(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "sprintID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid sprint ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	items, err := h.backlog.GetSprintBacklog(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, items)
}

// GetActiveSprint returns the active sprint for a space.
//
// @Summary      Get active sprint
// @Description  Returns the currently active sprint for a space
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/sprints/active [get]
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
//
// @Summary      Get backlog
// @Description  Returns the unassigned backlog items for a space
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/backlog [get]
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
//
// @Summary      Move item to sprint
// @Description  Moves an item from the backlog to a sprint
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                         true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerMoveToSprintRequest  true  "Move details"
// @Success      200      {object}  api.SwaggerMessageResponse
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/backlog/move-to-sprint [post]
func (h *Handler) MoveToSprint(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req moveToSprintRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// Both ids come from the body and neither has been reconciled with
	// anything; spaceID is what the route actually proved.
	if err := h.backlog.MoveToSprint(r.Context(), req.ItemID, req.SprintID, spaceID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item moved to sprint"})
}

// MoveToBacklog moves an item from a sprint back to the backlog.
//
// @Summary      Move item to backlog
// @Description  Moves an item from a sprint back to the backlog
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerMoveToBacklogRequest  true  "Move details"
// @Success      200      {object}  api.SwaggerMessageResponse
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      404      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/backlog/move-to-backlog [post]
func (h *Handler) MoveToBacklog(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req moveToBacklogRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// req.ItemID is a body id the route proved nothing about; spaceID is.
	if err := h.backlog.MoveToBacklog(r.Context(), req.ItemID, spaceID); err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "item moved to backlog"})
}

// --- Roadmap handlers ---

// GetRoadmap returns items with due dates in a range.
//
// @Summary      Get roadmap
// @Description  Returns items with due dates in a date range
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        from     query     string  true  "Start date (YYYY-MM-DD)"
// @Param        to       query     string  true  "End date (YYYY-MM-DD)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/roadmap [get]
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
//
// @Summary      Get overdue items
// @Description  Returns items that are past their due date
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/roadmap/overdue [get]
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
//
// @Summary      Get sprint roadmap
// @Description  Returns sprints with their items for roadmap view
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/roadmap/sprints [get]
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

// --- Item type handlers ---

type createItemTypeRequest struct {
	Name string `json:"name"`
}

type updateItemTypeRequest struct {
	Name     *string `json:"name,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}

// ListItemTypes returns all item types for an org (active and archived),
// ordered. Members read this to populate the creation type picker and filters.
//
// @Summary      List item types
// @Description  Returns all item types for an organization (active and archived)
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {array}   map[string]interface{}
// @Failure      400    {object}  api.SwaggerErrorResponse
// @Failure      401    {object}  api.SwaggerErrorResponse
// @Failure      500    {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/item-types [get]
func (h *Handler) ListItemTypes(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	types, err := h.itemTypes.List(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list item types")
		return
	}
	respond.JSON(w, http.StatusOK, types)
}

// CreateItemType defines a new org item type.
//
// @Summary      Create an item type
// @Description  Defines a new item type for an organization (org admin only)
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      201    {object}  map[string]interface{}
// @Failure      400    {object}  api.SwaggerErrorResponse
// @Failure      401    {object}  api.SwaggerErrorResponse
// @Failure      409    {object}  api.SwaggerErrorResponse
// @Failure      500    {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/item-types [post]
func (h *Handler) CreateItemType(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	var req createItemTypeRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	created, err := h.itemTypes.Create(r.Context(), orgID, req.Name)
	if err != nil {
		handleItemTypeError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// UpdateItemType renames and/or archives an item type.
//
// @Summary      Update an item type
// @Description  Renames and/or archives an item type (org admin only)
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string  true  "Organization ID (UUID)"
// @Param        typeID  path      string  true  "Item type ID (UUID)"
// @Success      200     {object}  map[string]interface{}
// @Failure      400     {object}  api.SwaggerErrorResponse
// @Failure      401     {object}  api.SwaggerErrorResponse
// @Failure      404     {object}  api.SwaggerErrorResponse
// @Failure      500     {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/item-types/{typeID} [patch]
func (h *Handler) UpdateItemType(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	typeID, err := uuid.Parse(chi.URLParam(r, "typeID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item type ID")
		return
	}
	var req updateItemTypeRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Name == nil && req.Archived == nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "nothing to update")
		return
	}

	var result *itemtypes.ItemType
	if req.Name != nil {
		result, err = h.itemTypes.Rename(r.Context(), orgID, typeID, *req.Name)
		if err != nil {
			handleItemTypeError(w, r, err)
			return
		}
	}
	if req.Archived != nil {
		result, err = h.itemTypes.SetArchived(r.Context(), orgID, typeID, *req.Archived)
		if err != nil {
			handleItemTypeError(w, r, err)
			return
		}
	}
	respond.JSON(w, http.StatusOK, result)
}

// DeleteItemType hard-deletes an unreferenced item type. A type in use returns
// 409 — archive it instead.
//
// @Summary      Delete an item type
// @Description  Hard-deletes an unreferenced item type (org admin only); a referenced type returns 409
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string  true  "Organization ID (UUID)"
// @Param        typeID  path      string  true  "Item type ID (UUID)"
// @Success      204     "No Content"
// @Failure      400     {object}  api.SwaggerErrorResponse
// @Failure      401     {object}  api.SwaggerErrorResponse
// @Failure      404     {object}  api.SwaggerErrorResponse
// @Failure      409     {object}  api.SwaggerErrorResponse
// @Failure      500     {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/item-types/{typeID} [delete]
func (h *Handler) DeleteItemType(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	typeID, err := uuid.Parse(chi.URLParam(r, "typeID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item type ID")
		return
	}
	if err := h.itemTypes.Delete(r.Context(), orgID, typeID); err != nil {
		handleItemTypeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleItemTypeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, itemtypes.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, itemtypes.ErrNameRequired), errors.Is(err, itemtypes.ErrInvalidName):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, itemtypes.ErrDuplicate):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, itemtypes.ErrReferenced):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Unmapped(w, r, "item type", "", err)
	}
}

// --- Custom field handlers ---

type createCustomFieldRequest struct {
	Name    string   `json:"name"`
	Type    string   `json:"field_type"`
	Options []string `json:"options"`
}

type updateCustomFieldRequest struct {
	Name     *string  `json:"name,omitempty"`
	Options  []string `json:"options,omitempty"`
	Archived *bool    `json:"archived,omitempty"`
}

type setFieldValueRequest struct {
	Value string `json:"value"`
}

// ListCustomFields returns all custom field definitions for an org.
//
// @Summary      List custom fields
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {array}   map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields [get]
func (h *Handler) ListCustomFields(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	defs, err := h.customFields.ListDefs(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list custom fields")
		return
	}
	respond.JSON(w, http.StatusOK, defs)
}

// CreateCustomField defines a new custom field (org admin only).
//
// @Summary      Create a custom field
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      201    {object}  map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields [post]
func (h *Handler) CreateCustomField(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	var req createCustomFieldRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	created, err := h.customFields.CreateDef(r.Context(), orgID, req.Name, req.Type, req.Options)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, created)
}

// UpdateCustomField renames, re-options, and/or archives a custom field.
//
// @Summary      Update a custom field
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        fieldID  path      string  true  "Custom field ID (UUID)"
// @Success      200      {object}  map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields/{fieldID} [patch]
//
//nolint:cyclop // one PATCH updates name/options and/or archive state — each is a guarded branch
func (h *Handler) UpdateCustomField(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	fieldID, err := uuid.Parse(chi.URLParam(r, "fieldID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid custom field ID")
		return
	}
	var req updateCustomFieldRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Name == nil && req.Archived == nil && req.Options == nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "nothing to update")
		return
	}

	var result *customfields.FieldDef
	if req.Name != nil || req.Options != nil {
		name := ""
		if req.Name != nil {
			name = *req.Name
		}
		result, err = h.customFields.UpdateDef(r.Context(), orgID, fieldID, name, req.Options)
		if err != nil {
			handleCustomFieldError(w, r, err)
			return
		}
	}
	if req.Archived != nil {
		result, err = h.customFields.SetDefArchived(r.Context(), orgID, fieldID, *req.Archived)
		if err != nil {
			handleCustomFieldError(w, r, err)
			return
		}
	}
	respond.JSON(w, http.StatusOK, result)
}

// DeleteCustomField removes a custom field definition. Stored values remain as
// legacy read-only data (no silent data loss).
//
// @Summary      Delete a custom field
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        fieldID  path      string  true  "Custom field ID (UUID)"
// @Success      204      "No Content"
// @Router       /orgs/{orgID}/custom-fields/{fieldID} [delete]
func (h *Handler) DeleteCustomField(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	fieldID, err := uuid.Parse(chi.URLParam(r, "fieldID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid custom field ID")
		return
	}
	if err := h.customFields.DeleteDef(r.Context(), orgID, fieldID); err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListFieldScopes returns every attachment of one field: which spaces and
// entity types it appears on, and whether it is required there.
//
// @Summary      List a custom field's scopes
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        fieldID  path      string  true  "Custom field ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields/{fieldID}/scopes [get]
func (h *Handler) ListFieldScopes(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	fieldID, err := uuid.Parse(chi.URLParam(r, "fieldID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid custom field ID")
		return
	}
	scopes, err := h.customFields.ListScopes(r.Context(), orgID, fieldID)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, scopes)
}

type setFieldScopeRequest struct {
	Required bool `json:"required"`
}

// SetFieldScope attaches a field to a (space, entity type) form, or updates
// whether it is required there. Requiredness is a property of the attachment,
// never of the org-wide definition — see migration 053.
//
// @Summary      Attach a custom field to a space form
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID (UUID)"
// @Param        fieldID     path      string  true  "Custom field ID (UUID)"
// @Param        spaceID     path      string  true  "Space ID (UUID)"
// @Param        entityType  path      string  true  "Entity type (ticket or project_item)"
// @Success      200         {object}  map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields/{fieldID}/scopes/{spaceID}/{entityType} [put]
func (h *Handler) SetFieldScope(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, fieldID, spaceID, entityType, ok := scopeParamsFromURL(w, r)
	if !ok {
		return
	}
	var req setFieldScopeRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	scope, err := h.customFields.SetScope(r.Context(), orgID, fieldID, spaceID, entityType, req.Required)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, scope)
}

// RemoveFieldScope detaches a field from a (space, entity type) form. Stored
// values are untouched and surface read-only as legacy fields.
//
// @Summary      Detach a custom field from a space form
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID (UUID)"
// @Param        fieldID     path      string  true  "Custom field ID (UUID)"
// @Param        spaceID     path      string  true  "Space ID (UUID)"
// @Param        entityType  path      string  true  "Entity type (ticket or project_item)"
// @Success      204         "No Content"
// @Router       /orgs/{orgID}/custom-fields/{fieldID}/scopes/{spaceID}/{entityType} [delete]
func (h *Handler) RemoveFieldScope(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, fieldID, spaceID, entityType, ok := scopeParamsFromURL(w, r)
	if !ok {
		return
	}
	if err := h.customFields.RemoveScope(r.Context(), orgID, fieldID, spaceID, entityType); err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListFormFieldScopes returns one form's attachments — this space, this
// entity type — in form order: which fields the form carries, their required
// flags, and their positions. The read the ordering surface edits against.
//
// @Summary      List a form's attached custom fields in order
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID (UUID)"
// @Param        spaceID     path      string  true  "Space ID (UUID)"
// @Param        entityType  path      string  true  "Entity type (ticket or project_item)"
// @Success      200         {array}   map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields/forms/{spaceID}/{entityType} [get]
func (h *Handler) ListFormFieldScopes(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, spaceID, entityType, ok := formParamsFromURL(w, r)
	if !ok {
		return
	}
	scopes, err := h.customFields.ListFormScopes(r.Context(), orgID, spaceID, entityType)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, scopes)
}

type reorderFormFieldsRequest struct {
	FieldIDs []uuid.UUID `json:"field_ids"`
}

// ReorderFormFields rewrites one form's field order to the request's
// field_ids, first to last. The list must name every field attached to the
// form exactly once — a partial or stale order is refused whole rather than
// half-applied. Its own route and its own statement, deliberately not a
// widening of the scope upsert: the upsert's contract is that toggling
// required never touches position, and this route's is the converse.
//
// @Summary      Reorder a form's custom fields
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true  "Organization ID (UUID)"
// @Param        spaceID     path      string  true  "Space ID (UUID)"
// @Param        entityType  path      string  true  "Entity type (ticket or project_item)"
// @Success      200         {array}   map[string]interface{}
// @Router       /orgs/{orgID}/custom-fields/forms/{spaceID}/{entityType}/order [put]
func (h *Handler) ReorderFormFields(w http.ResponseWriter, r *http.Request) {
	if !h.customFieldsEnabled(w, r) {
		return
	}
	orgID, spaceID, entityType, ok := formParamsFromURL(w, r)
	if !ok {
		return
	}
	var req reorderFormFieldsRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	scopes, err := h.customFields.ReorderForm(r.Context(), orgID, spaceID, entityType, req.FieldIDs)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, scopes)
}

func formParamsFromURL(w http.ResponseWriter, r *http.Request) (orgID, spaceID uuid.UUID, entityType string, ok bool) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	spaceID, err = uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	entityType = chi.URLParam(r, "entityType")
	return orgID, spaceID, entityType, true
}

func scopeParamsFromURL(w http.ResponseWriter, r *http.Request) (orgID, fieldID, spaceID uuid.UUID, entityType string, ok bool) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	fieldID, err = uuid.Parse(chi.URLParam(r, "fieldID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid custom field ID")
		return
	}
	spaceID, err = uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	entityType = chi.URLParam(r, "entityType")
	return orgID, fieldID, spaceID, entityType, true
}

// GetItemFields returns an item's custom fields: definitions attached to this
// space's item form with their values and required flags, plus read-only
// values whose definitions are gone or unattached here.
//
// @Summary      Get item custom fields
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/fields [get]
func (h *Handler) GetItemFields(w http.ResponseWriter, r *http.Request) {
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
	itemID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	// The space goes down with the item id: the route proved {spaceID} readable
	// and proved nothing about {itemID}, so values were rendered for any item in
	// the installation under any space's URL.
	fields, err := h.customFields.RenderForEntity(r.Context(), orgID, spaceID, customfields.EntityTypeProjectItem, itemID)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, fields)
}

// SetItemField writes an item's value for one custom field attached to this
// space's item form. An empty value clears it — unless the attachment marks
// the field required, in which case the clear is refused with an error naming
// the field. Legacy (undefined/archived/unattached) fields are read-only.
//
// @Summary      Set an item custom field value
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Param        slug     path      string  true  "Field slug"
// @Success      200      {object}  api.SwaggerMessageResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/fields/{slug} [put]
func (h *Handler) SetItemField(w http.ResponseWriter, r *http.Request) {
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
	itemID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	slug := chi.URLParam(r, "slug")

	// Setting a field value is editing the item — gate exactly like UpdateItem
	// (edit_own for the reporter, edit_any otherwise).
	//
	// This read resolves the item through the space for the permission check,
	// and an item outside {spaceID} leaves through the item's own 404 before
	// anything is written. It is no longer the only reconciliation: the write
	// statement itself carries the space predicate (UpsertEntityFieldValue),
	// so the refusal holds for any caller, not just this one.
	existing, err := h.items.GetItemInSpace(r.Context(), spaceID, itemID)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, existing.ReporterID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	var req setFieldValueRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if err := h.customFields.SetValue(r.Context(), orgID, spaceID, customfields.EntityTypeProjectItem, itemID, slug, req.Value); err != nil {
		// The write statement matching no entity answers with the item's own
		// 404 wording — byte-identical to the resolve above, so the two paths
		// cannot drift into an oracle. Reachable only if the item vanishes
		// between the resolve and the write.
		if errors.Is(err, customfields.ErrEntityNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, projects.ErrNotFound.Error())
			return
		}
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "field saved"})
}

// customFieldsEnabled reports whether the custom-fields service is wired,
// answering the conventional feature-disabled 404 when it is not. The sibling
// itemTypes collaborator had this guard from the start; customFields was
// dereferenced bare on six handlers.
func (h *Handler) customFieldsEnabled(w http.ResponseWriter, r *http.Request) bool {
	if h.customFields == nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "custom fields are not enabled")
		return false
	}
	return true
}

func handleCustomFieldError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, customfields.ErrNotFound),
		errors.Is(err, customfields.ErrUndefinedField),
		// ErrFieldArchived rides the same 404 as the never-defined case: same
		// status, same envelope shape, only a more honest message. It names a
		// state (archived) any member can already list — field definitions are
		// member-readable on this org-internal surface — so the wording
		// discloses nothing the 404 was hiding.
		errors.Is(err, customfields.ErrFieldArchived),
		errors.Is(err, customfields.ErrFieldNotInScope),
		errors.Is(err, customfields.ErrScopeNotFound),
		errors.Is(err, customfields.ErrSpaceNotFound),
		errors.Is(err, customfields.ErrEntityNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, customfields.ErrNameRequired),
		errors.Is(err, customfields.ErrInvalidName),
		errors.Is(err, customfields.ErrInvalidType),
		errors.Is(err, customfields.ErrOptionsRequired),
		errors.Is(err, customfields.ErrInvalidValue),
		errors.Is(err, customfields.ErrInvalidEntityType),
		errors.Is(err, customfields.ErrUnscopableEntityType),
		errors.Is(err, customfields.ErrScopeSpaceMismatch),
		errors.Is(err, customfields.ErrOrderMismatch),
		errors.Is(err, customfields.ErrValueRequired):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, customfields.ErrDuplicate),
		errors.Is(err, customfields.ErrSlugHeldByLegacyValues):
		// 409: the name is well formed and the caller is entitled to use it —
		// something already occupies the slug it derives to. The message names
		// what, and how many items are involved.
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Unmapped(w, r, "custom field", "", err)
	}
}

// --- Helpers ---

func itemIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "itemID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing item ID: %w", err)
	}
	return id, nil
}

// readableSpaceIDs returns the caller's resolved readable set, or writes a 404
// and reports false when no resolution ran.
//
// A missing resolution denies rather than degrades. The alternative — carrying
// on with an empty set — is safe in the sense that every far side would redact,
// but it makes an unwired route answer 200 with plausible-looking rows instead
// of announcing that its authorization never executed. RequireSpaceReadable
// made the same call one layer up ("fail closed: a missing resolution denies,
// never allows"), and 404 rather than 403 for the same reason it does.
func readableSpaceIDs(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool) {
	res := access.FromContext(r.Context())
	if res == nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return nil, false
	}
	return res.ReadableSpaceIDs(), true
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
		errors.Is(err, projects.ErrKeyRequired),
		errors.Is(err, projects.ErrInvalidPriority),
		errors.Is(err, projects.ErrInvalidKind),
		errors.Is(err, projects.ErrInvalidRelationKind),
		errors.Is(err, projects.ErrInvalidNextSprint),
		errors.Is(err, projects.ErrInvalidEntityType),
		errors.Is(err, projects.ErrSelfRelation):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, projects.ErrRelationTargetNotFound):
		// Deliberately the same 404 body an absent target produces. The service
		// cannot tell the two apart, and neither can this.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, projects.ErrLabelDuplicate):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Unmapped(w, r, "project", "", err)
	}
}
