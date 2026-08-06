package portal

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
)

// The agent-facing half: opting a Beacon space into the portal, and turning it
// off again.
//
// These routes are ORDINARY SPACE-SCOPED ROUTES and carry the ordinary guards
// — org membership, space readability, and manage_space in the handler. They
// are the opposite of the requester routes in every way that matters: an
// internal user with a real membership, resolved by ADR-0007, checked with
// access.Can. Nothing here touches portal.Session.
//
// manage_space rather than manage_shares, even though "expose this to
// outsiders" rhymes with sharing. A share widens one entity to people who are
// already in the organisation (ADR-0008); a portal exposes a submission
// surface to the public internet. That is a property of the space itself, and
// it belongs with the capability that owns the space's configuration.

// adminPortalView is the AGENT's view of the portal configuration. Unlike
// portalView it carries the key and the enabled flag, because the agent is the
// one who needs the URL to hand out and the state to toggle.
type adminPortalView struct {
	Key      string `json:"portal_key"`
	Name     string `json:"name"`
	Intro    string `json:"intro"`
	Enabled  bool   `json:"enabled"`
	PortalID string `json:"portal_id"`
}

type createPortalRequest struct {
	Name  string `json:"name"`
	Intro string `json:"intro,omitempty"`
}

// updatePortalRequest is the PATCH body: a partial update where only the
// fields present in the JSON are applied.
//
// EVERY FIELD IS THREE-STATE — absent (leave it alone), explicit null, and a
// value — via respond.OptionalField, and that is the whole risk of widening
// this route. The body used to be `{enabled}` with a plain bool; the moment
// the struct grew Name and Intro, a plain-typed Enabled would decode an
// absent key as false, and a body of {"name":"Support"} — a rename — would
// silently disable a live portal. That is the exact class that once wiped
// every project item's due date (see updateItemRequest in
// internal/core/api/projects), so this copies that shape rather than
// inventing a second nullability convention.
//
// What each field's explicit null means is resolved in UpdateConfig: null
// enabled and null name are validation errors (a portal cannot be "neither
// on nor off", and the name is required by migration 044's name-present
// CHECK); null intro clears it, intro being the one genuinely optional
// field here.
type updatePortalRequest struct {
	Enabled respond.OptionalField[bool]   `json:"enabled"`
	Name    respond.OptionalField[string] `json:"name"`
	Intro   respond.OptionalField[string] `json:"intro"`
}

// AdminRoutes returns the space-scoped portal configuration routes. The router
// mounts them inside the space subtree, so they inherit RequireSpaceInOrg and
// RequireSpaceReadable; the capability check is per-handler below.
func (h *Handler) AdminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.GetConfig)
	r.Post("/", h.CreateConfig)
	r.Patch("/", h.UpdateConfig)
	return r
}

// GetConfig returns a space's portal configuration.
//
// @Summary      Get a space's customer portal configuration
// @Tags         portal
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Success      200  {object}  portal.adminPortalView
// @Failure      403  {object}  api.SwaggerErrorResponse  "Missing manage_space"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No portal on this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/portal [get]
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.requireManageSpace(w, r)
	if !ok {
		return
	}
	p, err := h.svc.PortalForSpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "this space has no customer portal")
		return
	}
	respond.JSON(w, http.StatusOK, toAdminView(p))
}

// CreateConfig opts a Beacon space into the customer portal.
//
// @Summary      Enable the customer portal on a Beacon space
// @Tags         portal
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        request  body  portal.createPortalRequest  true  "Portal name and introduction"
// @Success      201  {object}  portal.adminPortalView
// @Failure      400  {object}  api.SwaggerErrorResponse  "Missing name, or not a Beacon space"
// @Failure      403  {object}  api.SwaggerErrorResponse  "Missing manage_space"
// @Failure      409  {object}  api.SwaggerErrorResponse  "This space already has a portal"
// @Router       /orgs/{orgID}/spaces/{spaceID}/portal [post]
func (h *Handler) CreateConfig(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.requireManageSpace(w, r)
	if !ok {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req createPortalRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	spaceType, err := h.spaceType(r, spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "space not found")
		return
	}

	p, err := h.svc.CreatePortal(r.Context(), spaceID, spaceType, req.Name, req.Intro, claims.UserID)
	switch {
	case errors.Is(err, portal.ErrNotBeaconSpace):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"customer portals are only available on Beacon service desks")
		return
	case errors.Is(err, portal.ErrPortalNameRequired):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a portal name is required")
		return
	case errors.Is(err, portal.ErrPortalExists):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "this space already has a customer portal")
		return
	case err != nil:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not enable the portal")
		return
	}

	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypePortalConfigured, ActorID: claims.UserID.String(),
		OrgID: claims.OrgID, ResourceType: "space", ResourceID: spaceID.String(),
		Metadata: map[string]string{"action": "created"},
	})

	respond.JSON(w, http.StatusCreated, toAdminView(p))
}

// UpdateConfig applies a partial update to a portal's configuration: the
// enabled flag, the public name, and the introduction text. Only the fields
// the request actually carried are touched — see updatePortalRequest.
//
// Whatever it changes, it turns a portal on or off (or renames it) WITHOUT
// discarding its key, so re-enabling does not invalidate every URL already
// handed out. A rename holds the same contract for the same reason: the key
// is the URL, and the name is only a label on it.
//
// @Summary      Update a space's customer portal configuration
// @Description  Partial update. Absent fields are left alone; enabled, name and intro can change independently. The portal key never changes.
// @Tags         portal
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        request  body  api.SwaggerUpdatePortalRequest  true  "Fields to change"
// @Success      200  {object}  portal.adminPortalView
// @Failure      400  {object}  api.SwaggerErrorResponse  "Empty or null name, or null enabled"
// @Failure      403  {object}  api.SwaggerErrorResponse  "Missing manage_space"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No portal on this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/portal [patch]
func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.requireManageSpace(w, r)
	if !ok {
		return
	}
	var req updatePortalRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	var params portal.UpdatePortalParams
	if req.Enabled.Set {
		if req.Enabled.Value == nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
				"enabled must be true or false, not null")
			return
		}
		params.Enabled = req.Enabled.Value
	}
	if req.Name.Set {
		// A null name is the same defect as an empty one — the name is
		// required, and migration 044's name-present CHECK would otherwise
		// refuse it as a raw constraint error long after this handler could
		// answer 400. The empty and whitespace spellings take the same exit
		// through the service's ErrPortalNameRequired below, so the two ways
		// of saying "no name" cannot drift apart.
		if req.Name.Value == nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a portal name is required")
			return
		}
		params.Name = req.Name.Value
	}
	if req.Intro.Set {
		// Intro is the one genuinely optional field: explicit null clears it.
		intro := ""
		if req.Intro.Value != nil {
			intro = *req.Intro.Value
		}
		params.Intro = &intro
	}

	p, err := h.svc.UpdatePortal(r.Context(), spaceID, params)
	switch {
	case errors.Is(err, portal.ErrPortalNameRequired):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a portal name is required")
		return
	case errors.Is(err, portal.ErrPortalNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "this space has no customer portal")
		return
	case err != nil:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not update the portal")
		return
	}

	// A rename is auditable for the same reason a toggle always was: the name
	// and intro are the strings external customers see, and a change to the
	// org's public face with no record is what the audit log exists to catch.
	// The action keeps the existing enabled/disabled vocabulary when the flag
	// was in the request, and "fields" says exactly what the request carried.
	if claims := auth.ClaimsFromContext(r.Context()); claims != nil && len(auditedFields(req)) > 0 {
		action := "updated"
		if params.Enabled != nil {
			if *params.Enabled {
				action = "enabled"
			} else {
				action = "disabled"
			}
		}
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypePortalConfigured, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "space", ResourceID: spaceID.String(),
			Metadata: map[string]string{"action": action, "fields": strings.Join(auditedFields(req), ",")},
		})
	}

	respond.JSON(w, http.StatusOK, toAdminView(p))
}

// auditedFields names the fields a PATCH actually carried, in a fixed order,
// for the audit trail. An empty result means the request was a no-op and
// nothing is logged — nothing was configured.
func auditedFields(req updatePortalRequest) []string {
	fields := make([]string, 0, 3)
	if req.Enabled.Set {
		fields = append(fields, "enabled")
	}
	if req.Name.Set {
		fields = append(fields, "name")
	}
	if req.Intro.Set {
		fields = append(fields, "intro")
	}
	return fields
}

// requireManageSpace parses the space id and enforces the capability.
//
// manage_space is above the create_items write floor the space subtree already
// applies, so a CONTRIBUTOR clears the floor and is refused here — which is
// the persona a test of this gate has to use. A viewer proves nothing: the
// floor refuses them upstream and the gate below is never reached.
func (h *Handler) requireManageSpace(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, false
	}
	if !access.Can(r.Context(), access.CapManageSpace, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return uuid.Nil, false
	}
	return spaceID, true
}

// spaceType resolves the module of the space being configured, so the service
// can refuse a portal on a Codex or Vector space.
func (h *Handler) spaceType(r *http.Request, spaceID uuid.UUID) (string, error) {
	if h.spaceTypes == nil {
		return "", errSpaceTypeUnavailable
	}
	return h.spaceTypes(r.Context(), spaceID)
}

var errSpaceTypeUnavailable = errors.New("space type resolver not configured")

func toAdminView(p portal.Portal) adminPortalView {
	return adminPortalView{
		Key: p.Key, Name: p.Name, Intro: p.Intro, Enabled: p.Enabled,
		PortalID: p.ID.String(),
	}
}
