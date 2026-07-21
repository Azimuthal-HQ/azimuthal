// Package admin provides the org-administration HTTP surface (P2.5): the
// People directory and user lifecycle, the member picker search, the access
// matrix with atomic bulk editing, and the audit log viewer.
//
// Every route here except the picker search is org-admin only and mounted
// behind RequireOrgAdmin404 — non-admins receive 404, never 403, so the
// administrative surface does not exist as far as they can tell.
package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
)

// Handler holds the admin surface dependencies.
type Handler struct {
	people   *people.Service
	bulk     *access.BulkService
	auditRd  *audit.Reader
	auditLog audit.Logger
}

// NewHandler creates an admin Handler.
func NewHandler(peopleSvc *people.Service, bulkSvc *access.BulkService, auditRd *audit.Reader) *Handler {
	return &Handler{people: peopleSvc, bulk: bulkSvc, auditRd: auditRd, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// Routes are registered directly in the router (multiple URL prefixes under
// /orgs/{orgID} share the RequireOrgAdmin404 guard there); this handler only
// exposes the endpoint methods.

// personResponse is one People row, lowercase snake_case per spec §6.
type personResponse struct {
	UserID          uuid.UUID  `json:"user_id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	AvatarURL       *string    `json:"avatar_url,omitempty"`
	OrgRole         string     `json:"org_role"`
	Status          string     `json:"status"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	JoinedAt        time.Time  `json:"joined_at"`
	PrimaryTeamID   *uuid.UUID `json:"primary_team_id,omitempty"`
	PrimaryTeamName *string    `json:"primary_team_name,omitempty"`
}

func toPersonResponse(p people.Person) personResponse {
	status := "active"
	if !p.IsActive {
		status = "deactivated"
	}
	return personResponse{
		UserID:          p.UserID,
		Email:           p.Email,
		DisplayName:     p.DisplayName,
		AvatarURL:       p.AvatarURL,
		OrgRole:         p.OrgRole,
		Status:          status,
		LastLoginAt:     p.LastLoginAt,
		JoinedAt:        p.JoinedAt,
		PrimaryTeamID:   p.PrimaryTeamID,
		PrimaryTeamName: p.PrimaryTeamName,
	}
}

// orgIDFromRequest parses the {orgID} URL parameter.
func orgIDFromRequest(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, false
	}
	return orgID, true
}

// userIDFromRequest parses the {userID} URL parameter.
func userIDFromRequest(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid user_id")
		return uuid.Nil, false
	}
	return userID, true
}

// ListPeople returns every member of the org.
//
// @Summary      List org members (admin)
// @Description  Every member with org role, primary team, status, and last sign-in. Org admins only; non-admins receive 404.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  path      string  true  "Organization ID"
// @Success      200     {array}   admin.personResponse       "Members"
// @Failure      401     {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse   "Not found (also returned to non-admins)"
// @Router       /orgs/{org_id}/users [get]
func (h *Handler) ListPeople(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	list, err := h.people.List(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list members")
		return
	}
	out := make([]personResponse, 0, len(list))
	for _, p := range list {
		out = append(out, toPersonResponse(p))
	}
	respond.JSON(w, http.StatusOK, out)
}

// updatePersonRequest changes a member's org role and/or primary team.
type updatePersonRequest struct {
	OrgRole       *string    `json:"org_role"`
	PrimaryTeamID *uuid.UUID `json:"primary_team_id"`
}

// UpdatePerson changes a member's org role or primary team.
//
// @Summary      Update org member (admin)
// @Description  Change a member's org role (member|admin) or primary team. The last active admin cannot be demoted. Org admins only.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        org_id   path      string                     true  "Organization ID"
// @Param        user_id  path      string                     true  "User ID"
// @Param        body     body      admin.updatePersonRequest  true  "Fields to change"
// @Success      204      "Updated"
// @Failure      400      {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      404      {object}  api.SwaggerErrorResponse   "Not found"
// @Failure      409      {object}  api.SwaggerErrorResponse   "Last-admin protection"
// @Router       /orgs/{org_id}/users/{user_id} [patch]
func (h *Handler) UpdatePerson(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	userID, ok := userIDFromRequest(w, r)
	if !ok {
		return
	}
	var req updatePersonRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.OrgRole == nil && req.PrimaryTeamID == nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "provide org_role or primary_team_id")
		return
	}

	if req.OrgRole != nil {
		if err := h.people.ChangeOrgRole(r.Context(), orgID, userID, *req.OrgRole); err != nil {
			h.mapPeopleError(w, r, err)
			return
		}
		h.logEvent(r, audit.EventTypeUserOrgRoleChanged, orgID, userID, map[string]string{"org_role": *req.OrgRole})
	}
	if req.PrimaryTeamID != nil {
		if err := h.people.ChangePrimaryTeam(r.Context(), orgID, userID, *req.PrimaryTeamID); err != nil {
			h.mapPeopleError(w, r, err)
			return
		}
		h.logEvent(r, audit.EventTypeUserPrimaryTeamChanged, orgID, userID, map[string]string{"primary_team_id": req.PrimaryTeamID.String()})
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeactivatePerson blocks sign-in and always terminates the member's
// sessions. There is deliberately no option to keep them signed in.
//
// @Summary      Deactivate org member (admin)
// @Description  Blocks sign-in and invalidates every session and token the user holds, immediately. The last active admin cannot be deactivated. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id   path      string  true  "Organization ID"
// @Param        user_id  path      string  true  "User ID"
// @Success      204      "Deactivated; all sessions terminated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409      {object}  api.SwaggerErrorResponse  "Last-admin protection or already deactivated"
// @Router       /orgs/{org_id}/users/{user_id}/deactivate [post]
func (h *Handler) DeactivatePerson(w http.ResponseWriter, r *http.Request) {
	h.lifecycleAction(w, r, h.people.Deactivate, audit.EventTypeUserDeactivated, map[string]string{"sessions_terminated": "true"})
}

// ReactivatePerson re-enables sign-in.
//
// @Summary      Reactivate org member (admin)
// @Description  Re-enables sign-in for a deactivated account. Tokens invalidated at deactivation stay dead. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id   path      string  true  "Organization ID"
// @Param        user_id  path      string  true  "User ID"
// @Success      204      "Reactivated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409      {object}  api.SwaggerErrorResponse  "Already active"
// @Router       /orgs/{org_id}/users/{user_id}/reactivate [post]
func (h *Handler) ReactivatePerson(w http.ResponseWriter, r *http.Request) {
	h.lifecycleAction(w, r, h.people.Reactivate, audit.EventTypeUserReactivated, nil)
}

// ForceLogoutPerson signs the member out everywhere; they stay active.
//
// @Summary      Force logout org member (admin)
// @Description  Invalidates every session and token the user holds. The account stays active — they simply sign in again. For lost devices and suspected credential leaks. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id   path      string  true  "Organization ID"
// @Param        user_id  path      string  true  "User ID"
// @Success      204      "Signed out everywhere"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{org_id}/users/{user_id}/force-logout [post]
func (h *Handler) ForceLogoutPerson(w http.ResponseWriter, r *http.Request) {
	h.lifecycleAction(w, r, h.people.ForceLogout, audit.EventTypeUserForceLogout, nil)
}

// RemovePerson removes the member from the org: membership, team rows, and
// grants go; the account and their authored content survive.
//
// @Summary      Remove member from org (admin)
// @Description  Drops the membership, team memberships, and grants in one transaction. The user account and their authored content survive with attribution intact. The last active admin cannot be removed. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id   path      string  true  "Organization ID"
// @Param        user_id  path      string  true  "User ID"
// @Success      204      "Removed from org"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409      {object}  api.SwaggerErrorResponse  "Last-admin protection"
// @Router       /orgs/{org_id}/users/{user_id} [delete]
func (h *Handler) RemovePerson(w http.ResponseWriter, r *http.Request) {
	h.lifecycleAction(w, r, h.people.RemoveFromOrg, audit.EventTypeUserRemovedFromOrg, nil)
}

// lifecycleAction runs one (orgID, userID) lifecycle operation with shared
// parsing, error mapping, and audit.
func (h *Handler) lifecycleAction(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, orgID, userID uuid.UUID) error, event audit.EventType, meta map[string]string) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	userID, ok := userIDFromRequest(w, r)
	if !ok {
		return
	}
	if err := fn(r.Context(), orgID, userID); err != nil {
		h.mapPeopleError(w, r, err)
		return
	}
	h.logEvent(r, event, orgID, userID, meta)
	w.WriteHeader(http.StatusNoContent)
}

// mapPeopleError translates people domain errors to API responses. The
// last-admin message is specific — spec: surfaced clearly, not generically.
func (h *Handler) mapPeopleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, people.ErrNotMember):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "member not found")
	case errors.Is(err, people.ErrLastAdmin):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict,
			"this is the organization's last active admin — promote someone else first")
	case errors.Is(err, people.ErrNotActive):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "account is already deactivated")
	case errors.Is(err, people.ErrAlreadyActive):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "account is already active")
	case errors.Is(err, people.ErrInvalidOrgRole):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "org_role must be member or admin")
	case errors.Is(err, people.ErrCannotChangeOwner):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "the owner's role cannot be changed")
	case errors.Is(err, people.ErrTeamNotFound):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "team not found in this organization")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "operation failed")
	}
}

// logEvent writes one administrative audit event.
func (h *Handler) logEvent(r *http.Request, event audit.EventType, orgID, subjectID uuid.UUID, meta map[string]string) {
	actor := ""
	if claims := auth.ClaimsFromContext(r.Context()); claims != nil {
		actor = claims.UserID.String()
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         event,
		ActorID:      actor,
		OrgID:        orgID.String(),
		ResourceType: "user",
		ResourceID:   subjectID.String(),
		Metadata:     meta,
	})
}
