// Package grants provides HTTP handlers for space grants and the
// effective-access explanation (v0.3 spec §6). Every route is capability
// guarded: manage_grants on the space (org admins pass via the bypass).
package grants

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// Handler holds the dependencies for grant HTTP handlers.
type Handler struct {
	grants    *access.GrantService
	explainer *access.Explainer
	auditLog  audit.Logger
}

// NewHandler creates a grants Handler.
func NewHandler(grants *access.GrantService, explainer *access.Explainer) *Handler {
	return &Handler{grants: grants, explainer: explainer, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// Routes returns the grant router, mounted at /spaces/{spaceID}/grants.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Patch("/{grantID}", h.Update)
	r.Delete("/{grantID}", h.Delete)
	return r
}

type createGrantRequest struct {
	SubjectType string    `json:"subject_type"`
	SubjectID   uuid.UUID `json:"subject_id"`
	Role        string    `json:"role"`
}

type updateGrantRequest struct {
	Role string `json:"role"`
}

// grantResponse is the wire form of a grant (lowercase snake_case).
type grantResponse struct {
	ID             uuid.UUID  `json:"id"`
	SpaceID        uuid.UUID  `json:"space_id"`
	SubjectType    string     `json:"subject_type"`
	SubjectID      uuid.UUID  `json:"subject_id"`
	SubjectName    string     `json:"subject_name,omitempty"`
	SubjectMissing bool       `json:"subject_missing,omitempty"`
	Role           string     `json:"role"`
	CreatedAt      string     `json:"created_at"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty"`
}

func toGrantResponse(g access.Grant) grantResponse {
	return grantResponse{
		ID:             g.ID,
		SpaceID:        g.SpaceID,
		SubjectType:    string(g.SubjectType),
		SubjectID:      g.SubjectID,
		SubjectName:    g.SubjectName,
		SubjectMissing: g.SubjectMissing,
		Role:           g.Role.String(),
		CreatedAt:      g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		CreatedBy:      g.CreatedBy,
	}
}

// requireManageGrants enforces the manage_grants capability on the URL
// space. The space is already known readable (router guards); lacking the
// capability is 403.
func requireManageGrants(w http.ResponseWriter, r *http.Request) (spaceID uuid.UUID, ok bool) {
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, false
	}
	if !access.Can(r.Context(), access.CapManageGrants, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_grants required")
		return uuid.Nil, false
	}
	return spaceID, true
}

// List returns every grant on the space.
//
// @Summary      List space grants
// @Description  Returns all grants on the space. Requires manage_grants.
// @Tags         grants
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Grants"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_grants required"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Space not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/grants [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := requireManageGrants(w, r)
	if !ok {
		return
	}
	list, err := h.grants.ListBySpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list grants")
		return
	}
	out := make([]grantResponse, 0, len(list))
	for _, g := range list {
		out = append(out, toGrantResponse(g))
	}
	respond.JSON(w, http.StatusOK, out)
}

// Create adds a grant to the space.
//
// @Summary      Create space grant
// @Description  Grants a user or team a role on the space. A user subject must be an org member; a team subject must be a live team in the org (400 otherwise). Requires manage_grants.
// @Tags         grants
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                        true  "Organization ID (UUID)"
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreateGrantRequest true  "Grant"
// @Success      201      {object}  map[string]interface{}        "Created grant"
// @Failure      400      {object}  api.SwaggerErrorResponse      "Subject not an org member / invalid role"
// @Failure      401      {object}  api.SwaggerErrorResponse      "Not authenticated"
// @Failure      403      {object}  api.SwaggerErrorResponse      "manage_grants required"
// @Failure      404      {object}  api.SwaggerErrorResponse      "Space not found"
// @Failure      409      {object}  api.SwaggerErrorResponse      "Duplicate grant"
// @Router       /orgs/{orgID}/spaces/{spaceID}/grants [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := requireManageGrants(w, r)
	if !ok {
		return
	}
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	var req createGrantRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	role, err := access.ParseRole(req.Role)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "role must be one of viewer, contributor, agent, space_admin")
		return
	}
	subjectType := access.SubjectType(req.SubjectType)
	if subjectType != access.SubjectUser && subjectType != access.SubjectTeam {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "subject_type must be 'user' or 'team'")
		return
	}
	if req.SubjectID == uuid.Nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "subject_id is required")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	grant, err := h.grants.Create(r.Context(), orgID, spaceID, subjectType, req.SubjectID, role, claims.UserID)
	switch {
	case errors.Is(err, access.ErrSubjectNotOrgMember), errors.Is(err, access.ErrSubjectTeamNotFound):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	case errors.Is(err, access.ErrDuplicateGrant):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
		return
	case err != nil:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create grant")
		return
	}
	h.logEvent(r, audit.EventTypeGrantCreated, grant, nil)
	respond.JSON(w, http.StatusCreated, toGrantResponse(grant))
}

// Update changes a grant's role.
//
// @Summary      Update space grant
// @Description  Changes the role of an existing grant. Requires manage_grants.
// @Tags         grants
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                        true  "Organization ID (UUID)"
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        grantID  path      string                        true  "Grant ID (UUID)"
// @Param        body     body      api.SwaggerUpdateGrantRequest true  "New role"
// @Success      200      {object}  map[string]interface{}        "Updated grant"
// @Failure      400      {object}  api.SwaggerErrorResponse      "Invalid role"
// @Failure      401      {object}  api.SwaggerErrorResponse      "Not authenticated"
// @Failure      403      {object}  api.SwaggerErrorResponse      "manage_grants required"
// @Failure      404      {object}  api.SwaggerErrorResponse      "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/grants/{grantID} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := requireManageGrants(w, r)
	if !ok {
		return
	}
	grant, ok := h.grantInSpace(w, r, spaceID)
	if !ok {
		return
	}
	var req updateGrantRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	role, err := access.ParseRole(req.Role)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "role must be one of viewer, contributor, agent, space_admin")
		return
	}
	updated, err := h.grants.UpdateRole(r.Context(), grant.ID, role)
	if errors.Is(err, access.ErrGrantNotFound) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "grant not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update grant")
		return
	}
	h.logEvent(r, audit.EventTypeGrantUpdated, updated, map[string]string{"role": updated.Role.String()})
	respond.JSON(w, http.StatusOK, toGrantResponse(updated))
}

// Delete revokes a grant.
//
// @Summary      Revoke space grant
// @Description  Deletes a grant. Access recomputes per request, so revocation is immediate. Requires manage_grants.
// @Tags         grants
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        grantID  path  string  true  "Grant ID (UUID)"
// @Success      204  "Revoked"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "manage_grants required"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/grants/{grantID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := requireManageGrants(w, r)
	if !ok {
		return
	}
	grant, ok := h.grantInSpace(w, r, spaceID)
	if !ok {
		return
	}
	if err := h.grants.Revoke(r.Context(), grant.ID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to revoke grant")
		return
	}
	h.logEvent(r, audit.EventTypeGrantRevoked, grant, nil)
	w.WriteHeader(http.StatusNoContent)
}

// EffectiveAccess explains why a user can (or cannot) see the space.
//
// @Summary      Effective access
// @Description  Returns the grant chain producing a user's access to the space — which grant, which team matched, at what depth — not merely the resulting role. Callers may always ask about themselves; asking about another user requires manage_grants.
// @Tags         grants
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true   "Organization ID (UUID)"
// @Param        spaceID  path      string  true   "Space ID (UUID)"
// @Param        user_id  query     string  false  "Target user (defaults to the caller)"
// @Success      200      {object}  map[string]interface{}    "Explanation"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid user_id"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_grants required for other users"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Space not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/effective-access [get]
func (h *Handler) EffectiveAccess(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	target := claims.UserID
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid user_id")
			return
		}
		target = parsed
	}
	// Anyone may ask about themselves; asking about someone else is a grant
	// administration question.
	if target != claims.UserID && !access.Can(r.Context(), access.CapManageGrants, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_grants required to inspect other users")
		return
	}
	explanation, err := h.explainer.Explain(r.Context(), orgID, spaceID, target)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to explain access")
		return
	}
	respond.JSON(w, http.StatusOK, explanation)
}

// grantInSpace loads {grantID} and 404s when it does not belong to the URL
// space — grant ids from other spaces are indistinguishable from
// nonexistent ones.
func (h *Handler) grantInSpace(w http.ResponseWriter, r *http.Request, spaceID uuid.UUID) (access.Grant, bool) {
	grantID, err := uuid.Parse(chi.URLParam(r, "grantID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid grant_id")
		return access.Grant{}, false
	}
	grant, err := h.grants.Get(r.Context(), grantID)
	if err != nil || grant.SpaceID != spaceID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "grant not found")
		return access.Grant{}, false
	}
	return grant, true
}

// logEvent writes an audit event for a grant mutation.
func (h *Handler) logEvent(r *http.Request, t audit.EventType, g access.Grant, extra map[string]string) {
	claims := auth.ClaimsFromContext(r.Context())
	actor := ""
	if claims != nil {
		actor = claims.UserID.String()
	}
	meta := map[string]string{
		"space_id":     g.SpaceID.String(),
		"subject_type": string(g.SubjectType),
		"subject_id":   g.SubjectID.String(),
		"role":         g.Role.String(),
	}
	for k, v := range extra {
		meta[k] = v
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: t, ActorID: actor, OrgID: g.OrgID.String(),
		ResourceType: "grant", ResourceID: g.ID.String(), Metadata: meta,
	})
}
