// Package teams provides HTTP handlers for team management (v0.3 spec §6).
// Reads are open to every org member (the picker groups by team); creation,
// reparenting, deletion, and membership management are org-admin only in
// v0.3 (ADR-0007 administrative authority), enforced by the adminGuard the
// router passes in.
package teams

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/ticketref"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
)

// Handler holds the dependencies for team HTTP handlers.
type Handler struct {
	svc      *teams.Service
	auditLog audit.Logger

	// ticketRef is the boot-time ticket-reference policy. Every mutating
	// handler below resolves the reference *before* it performs its write:
	// under a required policy a missing reference has to mean nothing
	// happened, and a 400 raised after the service call would leave an
	// unreferenced change committed — precisely the outcome the requirement
	// exists to prevent. The zero value is the permissive default, so a
	// handler that is never given a policy behaves as it did before the flag
	// existed.
	ticketRef ticketref.Policy
}

// NewHandler creates a team Handler.
func NewHandler(svc *teams.Service) *Handler {
	return &Handler{svc: svc, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithTicketRefPolicy sets the ticket-reference requirement applied to team
// mutations.
func (h *Handler) WithTicketRefPolicy(p ticketref.Policy) *Handler {
	h.ticketRef = p
	return h
}

// Routes returns the team router. adminGuard wraps every administrative
// mutation (org admin only in v0.3).
func (h *Handler) Routes(adminGuard func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.With(adminGuard).Post("/", h.Create)
	r.Get("/{teamID}", h.Get)
	r.With(adminGuard).Patch("/{teamID}", h.Update)
	r.With(adminGuard).Delete("/{teamID}", h.Delete)
	r.Get("/{teamID}/members", h.ListMembers)
	r.With(adminGuard).Put("/{teamID}/members/{userID}", h.PutMember)
	r.With(adminGuard).Delete("/{teamID}/members/{userID}", h.RemoveMember)
	return r
}

type createTeamRequest struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	ParentID    *string `json:"parent_id,omitempty"`
}

// patchTeamRequest distinguishes "field absent" from "field null": a
// parent_id of JSON null moves the team to the root, an absent parent_id
// leaves the parent untouched.
type patchTeamRequest struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	ParentID    json.RawMessage `json:"parent_id,omitempty"`
}

type putMemberRequest struct {
	Role      string `json:"role"`
	IsPrimary bool   `json:"is_primary"`
}

// mapTeamError translates teams domain errors onto HTTP responses.
func mapTeamError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, teams.ErrNotFound), errors.Is(err, teams.ErrMemberNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, teams.ErrSlugTaken), errors.Is(err, teams.ErrHasChildren), errors.Is(err, teams.ErrOwnsSpaces):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, teams.ErrNameRequired), errors.Is(err, teams.ErrInvalidSlug),
		errors.Is(err, teams.ErrCycle), errors.Is(err, teams.ErrDepthExceeded),
		errors.Is(err, teams.ErrDefaultTeam), errors.Is(err, teams.ErrInvalidMemberRole),
		errors.Is(err, teams.ErrNotOrgMember), errors.Is(err, teams.ErrParentNotFound):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "team operation failed")
	}
}

// List returns every live team in the org.
//
// @Summary      List teams
// @Description  Returns all teams in the organization. Pass parent_id to filter to one parent's children; flat=true is the default representation (a flat list carrying parent_id and path for tree assembly).
// @Tags         teams
// @Produce      json
// @Security     BearerAuth
// @Param        orgID      path      string  true   "Organization ID (UUID)"
// @Param        parent_id  query     string  false  "Filter to children of this team"
// @Success      200        {array}   map[string]interface{}    "List of teams"
// @Failure      400        {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401        {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404        {object}  api.SwaggerErrorResponse  "Not an org member"
// @Router       /orgs/{orgID}/teams [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	list, err := h.svc.List(r.Context(), orgID)
	if err != nil {
		mapTeamError(w, r, err)
		return
	}
	if parentRaw := r.URL.Query().Get("parent_id"); parentRaw != "" {
		parentID, err := uuid.Parse(parentRaw)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid parent_id")
			return
		}
		filtered := make([]teams.Team, 0, len(list))
		for _, t := range list {
			if t.ParentID != nil && *t.ParentID == parentID {
				filtered = append(filtered, t)
			}
		}
		list = filtered
	}
	respond.JSON(w, http.StatusOK, list)
}

// Create creates a team.
//
// @Summary      Create team
// @Description  Creates a team, optionally under a parent (org admin only). Depth is capped at 5.
// @Tags         teams
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string                       true   "Organization ID (UUID)"
// @Param        ticket_ref  query     string                       false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Param        body        body      api.SwaggerCreateTeamRequest true   "Team details"
// @Success      201         {object}  map[string]interface{}       "Created team"
// @Failure      400         {object}  api.SwaggerErrorResponse     "Validation error"
// @Failure      401         {object}  api.SwaggerErrorResponse     "Not authenticated"
// @Failure      403         {object}  api.SwaggerErrorResponse     "Org admin required"
// @Failure      409         {object}  api.SwaggerErrorResponse     "Slug already in use"
// @Router       /orgs/{orgID}/teams [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	ref, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	var req createTeamRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	var parentID *uuid.UUID
	if req.ParentID != nil && *req.ParentID != "" {
		id, err := uuid.Parse(*req.ParentID)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "invalid parent_id")
			return
		}
		parentID = &id
	}
	team, err := h.svc.Create(r.Context(), orgID, parentID, req.Slug, req.Name, req.Description)
	if err != nil {
		mapTeamError(w, r, err)
		return
	}
	h.logEvent(r, audit.EventTypeTeamCreated, "team", team.ID, ref, map[string]string{"slug": team.Slug, "name": team.Name})
	respond.JSON(w, http.StatusCreated, team)
}

// Get returns one team.
//
// @Summary      Get team
// @Tags         teams
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string  true  "Organization ID (UUID)"
// @Param        teamID  path      string  true  "Team ID (UUID)"
// @Success      200     {object}  map[string]interface{}    "Team"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/teams/{teamID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamInOrg(w, r)
	if !ok {
		return
	}
	respond.JSON(w, http.StatusOK, team)
}

// Update renames and/or reparents a team (PATCH includes reparent, spec §6).
//
// @Summary      Update team
// @Description  Renames a team and/or moves it in the tree. parent_id: absent = unchanged, null = move to root, UUID = move under that parent. Org admin only.
// @Tags         teams
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string                      true   "Organization ID (UUID)"
// @Param        teamID      path      string                      true   "Team ID (UUID)"
// @Param        ticket_ref  query     string                      false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Param        body        body      api.SwaggerPatchTeamRequest true   "Fields to update"
// @Success      200         {object}  map[string]interface{}      "Updated team"
// @Failure      400         {object}  api.SwaggerErrorResponse    "Validation error (cycle, depth, default team)"
// @Failure      401         {object}  api.SwaggerErrorResponse    "Not authenticated"
// @Failure      403         {object}  api.SwaggerErrorResponse    "Org admin required"
// @Failure      404         {object}  api.SwaggerErrorResponse    "Not found"
// @Router       /orgs/{orgID}/teams/{teamID} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamInOrg(w, r)
	if !ok {
		return
	}
	// One PATCH can perform two writes and emit two events — team.updated
	// from the rename, team.reparented from the move. The reference is
	// resolved once, ahead of both, and both events carry it.
	ref, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	var req patchTeamRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	current, ok := h.applyRename(w, r, team, req, ref)
	if !ok {
		return
	}

	if len(req.ParentID) > 0 {
		newParent, err := parseReparentTarget(req.ParentID)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "invalid parent_id")
			return
		}
		moved, err := h.svc.Reparent(r.Context(), current.OrgID, current.ID, newParent)
		if err != nil {
			mapTeamError(w, r, err)
			return
		}
		current = moved
		meta := map[string]string{"new_parent_id": ""}
		if newParent != nil {
			meta["new_parent_id"] = newParent.String()
		}
		h.logEvent(r, audit.EventTypeTeamReparented, "team", current.ID, ref, meta)
	}

	respond.JSON(w, http.StatusOK, current)
}

// applyRename applies the name/description part of a team PATCH (absent
// fields keep their current values) and writes the team.updated audit
// event. ticketRef is the reference Update already resolved. Returns
// ok=false after writing an error response.
func (h *Handler) applyRename(w http.ResponseWriter, r *http.Request, current teams.Team, req patchTeamRequest, ticketRef string) (teams.Team, bool) {
	if req.Name == nil && req.Description == nil {
		return current, true
	}
	name := current.Name
	if req.Name != nil {
		name = *req.Name
	}
	description := current.Description
	if req.Description != nil {
		description = *req.Description
	}
	updated, err := h.svc.Rename(r.Context(), current.ID, name, description)
	if err != nil {
		mapTeamError(w, r, err)
		return current, false
	}
	h.logEvent(r, audit.EventTypeTeamUpdated, "team", updated.ID, ticketRef, map[string]string{"name": name})
	return updated, true
}

// parseReparentTarget decodes the tri-state parent_id: JSON null means the
// root (nil), anything else must be a UUID string.
func parseReparentTarget(raw json.RawMessage) (*uuid.UUID, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parent_id is not a string: %w", err)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parent_id is not a uuid: %w", err)
	}
	return &id, nil
}

// Delete deletes a team.
//
// @Summary      Delete team
// @Description  Soft-deletes a team (org admin only). Rejected while the team has children or owns spaces. Members move to the org default team.
// @Tags         teams
// @Security     BearerAuth
// @Param        orgID       path   string  true   "Organization ID (UUID)"
// @Param        teamID      path   string  true   "Team ID (UUID)"
// @Param        ticket_ref  query  string  false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Default team"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "Org admin required"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409  {object}  api.SwaggerErrorResponse  "Has children or owns spaces"
// @Router       /orgs/{orgID}/teams/{teamID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamInOrg(w, r)
	if !ok {
		return
	}
	ref, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), team.OrgID, team.ID); err != nil {
		mapTeamError(w, r, err)
		return
	}
	h.logEvent(r, audit.EventTypeTeamDeleted, "team", team.ID, ref, map[string]string{"slug": team.Slug})
	w.WriteHeader(http.StatusNoContent)
}

// ListMembers returns the team's members.
//
// @Summary      List team members
// @Tags         teams
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string  true  "Organization ID (UUID)"
// @Param        teamID  path      string  true  "Team ID (UUID)"
// @Success      200     {array}   map[string]interface{}    "Members"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/teams/{teamID}/members [get]
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamInOrg(w, r)
	if !ok {
		return
	}
	members, err := h.svc.ListMembers(r.Context(), team.ID)
	if err != nil {
		mapTeamError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, members)
}

// PutMember adds or updates a team member (upsert per the PUT verb),
// optionally making the team the user's primary.
//
// @Summary      Add or update team member
// @Description  Enrols an org member into the team, or updates their metadata role. is_primary=true makes this the user's primary team. Org admin only.
// @Tags         teams
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string                        true   "Organization ID (UUID)"
// @Param        teamID      path      string                        true   "Team ID (UUID)"
// @Param        userID      path      string                        true   "User ID (UUID)"
// @Param        ticket_ref  query     string                        false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Param        body        body      api.SwaggerPutMemberRequest   true   "Membership"
// @Success      200         {object}  map[string]interface{}        "Membership"
// @Failure      400         {object}  api.SwaggerErrorResponse      "Not an org member / bad role"
// @Failure      401         {object}  api.SwaggerErrorResponse      "Not authenticated"
// @Failure      403         {object}  api.SwaggerErrorResponse      "Org admin required"
// @Failure      404         {object}  api.SwaggerErrorResponse      "Team not found"
// @Router       /orgs/{orgID}/teams/{teamID}/members/{userID} [put]
func (h *Handler) PutMember(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamInOrg(w, r)
	if !ok {
		return
	}
	// Resolved ahead of AddMember and SetPrimary alike — PutMember can write
	// twice, and neither write may outrun the reference check.
	ref, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid user_id")
		return
	}
	var req putMemberRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	member, err := h.svc.AddMember(r.Context(), team.ID, userID, team.OrgID, req.Role)
	if err != nil {
		mapTeamError(w, r, err)
		return
	}
	if req.IsPrimary {
		if err := h.svc.SetPrimary(r.Context(), team.ID, userID, team.OrgID); err != nil {
			mapTeamError(w, r, err)
			return
		}
		member.IsPrimary = true
	}
	h.logEvent(r, audit.EventTypeTeamMemberAdded, "team_member", team.ID, ref,
		map[string]string{"user_id": userID.String(), "role": member.Role})
	respond.JSON(w, http.StatusOK, member)
}

// RemoveMember removes a user from the team.
//
// @Summary      Remove team member
// @Description  Removes the user from the team. A user removed from their last team is re-added to the org default team — never teamless. Org admin only.
// @Tags         teams
// @Security     BearerAuth
// @Param        orgID       path   string  true   "Organization ID (UUID)"
// @Param        teamID      path   string  true   "Team ID (UUID)"
// @Param        userID      path   string  true   "User ID (UUID)"
// @Param        ticket_ref  query  string  false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Success      204  "Removed"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "Org admin required"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/teams/{teamID}/members/{userID} [delete]
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamInOrg(w, r)
	if !ok {
		return
	}
	ref, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid user_id")
		return
	}
	if err := h.svc.RemoveMember(r.Context(), team.ID, userID, team.OrgID); err != nil {
		mapTeamError(w, r, err)
		return
	}
	h.logEvent(r, audit.EventTypeTeamMemberRemoved, "team_member", team.ID, ref,
		map[string]string{"user_id": userID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// teamInOrg loads the {teamID} team and 404s when it does not live in the
// {orgID} org — team ids from other orgs must be indistinguishable from
// nonexistent ones.
func (h *Handler) teamInOrg(w http.ResponseWriter, r *http.Request) (teams.Team, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return teams.Team{}, false
	}
	teamID, err := uuid.Parse(chi.URLParam(r, "teamID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid team_id")
		return teams.Team{}, false
	}
	team, err := h.svc.Get(r.Context(), teamID)
	if err != nil || team.OrgID != orgID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "team not found")
		return teams.Team{}, false
	}
	return team, true
}

// logEvent writes an audit event for a team mutation; failures never
// interrupt the request (audit.Logger contract). ticketRef is the reference
// the caller resolved before its write — empty when none was supplied and
// the policy did not demand one, which the logger stores as SQL NULL.
func (h *Handler) logEvent(r *http.Request, t audit.EventType, kind string, entityID uuid.UUID, ticketRef string, meta map[string]string) {
	claims := auth.ClaimsFromContext(r.Context())
	actor := ""
	if claims != nil {
		actor = claims.UserID.String()
	}
	orgID := chi.URLParam(r, "orgID")
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: t, ActorID: actor, OrgID: orgID,
		ResourceType: kind, ResourceID: entityID.String(), Metadata: meta,
		TicketRef: ticketRef,
	})
}
