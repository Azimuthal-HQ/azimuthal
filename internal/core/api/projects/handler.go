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
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// Handler holds the dependencies for project HTTP handlers.
type Handler struct {
	items        *projects.ItemService
	sprints      *projects.SprintService
	backlog      *projects.BacklogService
	roadmap      *projects.RoadmapService
	relations    *projects.RelationService
	labels       *projects.LabelService
	itemTypes    *itemtypes.Service
	customFields *customfields.Service
	boardConfig  *projects.BoardConfigService
	auditLog     audit.Logger
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
		auditLog:  audit.NewLogger(),
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
	Labels      []string   `json:"labels,omitempty"`
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
// optionalField keeps the three states apart, so absent now means absent.
type updateItemRequest struct {
	Title       *string                  `json:"title"`
	Description *string                  `json:"description"`
	Kind        *string                  `json:"kind"`
	Priority    *string                  `json:"priority"`
	AssigneeID  optionalField[uuid.UUID] `json:"assignee_id"`
	Labels      []string                 `json:"labels,omitempty"`
	DueAt       optionalField[time.Time] `json:"due_at"`
}

// optionalField distinguishes "the client did not mention this field" from
// "the client explicitly sent null". encoding/json only calls UnmarshalJSON
// when the key is present, so Set is false for an absent key and true for
// both a null and a real value.
//
// Note the json tags above must NOT carry omitempty: it has no effect on
// decoding, but it would wrongly suggest these fields round-trip, and this
// type is decode-only.
type optionalField[T any] struct {
	// Set reports whether the key appeared in the request body at all.
	Set bool
	// Value is nil when the key appeared as null, or when it never appeared.
	Value *T
}

// UnmarshalJSON records that the key was present, then decodes null as an
// explicit clear and anything else as a value.
func (o *optionalField[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("decoding optional field: %w", err)
	}
	o.Value = &v
	return nil
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

type createRelationRequest struct {
	ToID   uuid.UUID `json:"to_id"`
	ToType string    `json:"to_type"`
	Kind   string    `json:"kind"`
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
		Labels:      req.Labels,
		DueAt:       req.DueAt,
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

	item, err := h.items.GetItem(r.Context(), id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, item)
}

// applyItemPatch copies only the fields the request body actually carried onto
// the stored item. An absent field keeps its stored value; AssigneeID and DueAt
// are the exceptions, where absent and null both mean "clear it".
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
	if req.Labels != nil {
		existing.Labels = req.Labels
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

	existing, err := h.items.GetItem(r.Context(), id)
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

	existing, err := h.items.GetItem(r.Context(), id)
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
	if err := h.items.DeleteItem(r.Context(), id, actorID); err != nil {
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

	item, err := h.items.UpdateItemStatus(r.Context(), id, req.Status)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypeItemStatusChange, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "item", ResourceID: id.String(),
			Metadata: map[string]string{"to": string(req.Status)},
		})
	}
	respond.JSON(w, http.StatusOK, item)
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

	if err := h.items.AssignToSprint(r.Context(), id, req.SprintID); err != nil {
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

// --- Relation handlers ---

// ListRelations returns all relations for an item.
//
// @Summary      List item relations
// @Description  Returns all relations for a project item
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200      {array}   map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations [get]
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
//
// @Summary      Create item relation
// @Description  Creates a new relation from a project item to another item
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                           true  "Space ID (UUID)"
// @Param        itemID   path      string                           true  "Item ID (UUID)"
// @Param        body     body      api.SwaggerCreateRelationRequest  true  "Relation details"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/relations [post]
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

	toType := req.ToType
	if toType == "" {
		toType = "project_item"
	}
	rel := &projects.Relation{
		FromID:    fromID,
		FromType:  "project_item",
		ToID:      req.ToID,
		ToType:    toType,
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
//
// @Summary      Delete a relation
// @Description  Removes a relation between project items
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
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

	if err := h.relations.DeleteRelation(r.Context(), id); err != nil {
		handleProjectError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

	sprint, err := h.sprints.GetSprint(r.Context(), id)
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

	sprint, err := h.sprints.StartSprint(r.Context(), id)
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

	sprint, err := h.sprints.CompleteSprint(r.Context(), id, projects.CompleteOptions{NextSprintID: req.NextSprintID})
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

	items, err := h.backlog.GetSprintBacklog(r.Context(), id)
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

	if err := h.backlog.MoveToSprint(r.Context(), req.ItemID, req.SprintID); err != nil {
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

	if err := h.backlog.MoveToBacklog(r.Context(), req.ItemID); err != nil {
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

// --- Label handlers ---

// ListLabels returns all labels for an organization.
//
// @Summary      List labels
// @Description  Returns all labels for an organization
// @Tags         labels
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {array}   map[string]interface{}
// @Failure      400    {object}  api.SwaggerErrorResponse
// @Failure      401    {object}  api.SwaggerErrorResponse
// @Failure      500    {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/labels [get]
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
//
// @Summary      Create a label
// @Description  Creates a new label for an organization
// @Tags         labels
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                         true  "Organization ID (UUID)"
// @Param        body   body      api.SwaggerCreateLabelRequest   true  "Label details"
// @Success      201    {object}  map[string]interface{}
// @Failure      400    {object}  api.SwaggerErrorResponse
// @Failure      401    {object}  api.SwaggerErrorResponse
// @Failure      409    {object}  api.SwaggerErrorResponse
// @Failure      500    {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/labels [post]
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
//
// @Summary      Delete a label
// @Description  Removes a label from an organization
// @Tags         labels
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        labelID  path      string  true  "Label ID (UUID)"
// @Success      204      "No Content"
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/labels/{labelID} [delete]
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
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("item type operation failed: %v", err))
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

// GetItemFields returns an item's custom fields: active definitions with their
// values, plus legacy read-only values whose definitions are gone.
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
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	itemID, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	fields, err := h.customFields.RenderForItem(r.Context(), orgID, itemID)
	if err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, fields)
}

// SetItemField writes an item's value for one active custom field. An empty
// value clears it. Legacy (undefined/archived) fields are read-only.
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
	existing, err := h.items.GetItem(r.Context(), itemID)
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
	if err := h.customFields.SetValue(r.Context(), orgID, itemID, slug, req.Value); err != nil {
		handleCustomFieldError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "field saved"})
}

func handleCustomFieldError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, customfields.ErrNotFound), errors.Is(err, customfields.ErrUndefinedField):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, customfields.ErrNameRequired),
		errors.Is(err, customfields.ErrInvalidName),
		errors.Is(err, customfields.ErrInvalidType),
		errors.Is(err, customfields.ErrOptionsRequired),
		errors.Is(err, customfields.ErrInvalidValue):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, customfields.ErrDuplicate):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("custom field operation failed: %v", err))
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
		errors.Is(err, projects.ErrSelfRelation):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, projects.ErrLabelDuplicate):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("project operation failed: %v", err))
	}
}
