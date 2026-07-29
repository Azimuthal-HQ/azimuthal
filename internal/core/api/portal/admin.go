package portal

import (
	"errors"
	"net/http"

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

type setEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// AdminRoutes returns the space-scoped portal configuration routes. The router
// mounts them inside the space subtree, so they inherit RequireSpaceInOrg and
// RequireSpaceReadable; the capability check is per-handler below.
func (h *Handler) AdminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.GetConfig)
	r.Post("/", h.CreateConfig)
	r.Patch("/", h.SetEnabled)
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

// SetEnabled turns a portal on or off WITHOUT discarding its key, so
// re-enabling does not invalidate every URL already handed out.
//
// @Summary      Enable or disable a space's customer portal
// @Tags         portal
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        request  body  portal.setEnabledRequest  true  "Desired state"
// @Success      200  {object}  portal.adminPortalView
// @Failure      403  {object}  api.SwaggerErrorResponse  "Missing manage_space"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No portal on this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/portal [patch]
func (h *Handler) SetEnabled(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.requireManageSpace(w, r)
	if !ok {
		return
	}
	var req setEnabledRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	p, err := h.svc.SetPortalEnabled(r.Context(), spaceID, req.Enabled)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "this space has no customer portal")
		return
	}

	if claims := auth.ClaimsFromContext(r.Context()); claims != nil {
		action := "disabled"
		if req.Enabled {
			action = "enabled"
		}
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypePortalConfigured, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "space", ResourceID: spaceID.String(),
			Metadata: map[string]string{"action": action},
		})
	}

	respond.JSON(w, http.StatusOK, toAdminView(p))
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
