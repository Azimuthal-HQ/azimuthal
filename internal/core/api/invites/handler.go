// Package invites provides the invite HTTP surface (P2.5 W2): the
// org-admin lifecycle (create, list, revoke, resend) and the public
// token-authenticated acceptance routes.
package invites

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/ticketref"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/invites"
)

// Handler holds the invite surface dependencies.
type Handler struct {
	invites   *invites.Service
	jwt       *auth.JWTService
	sessions  *auth.SessionService
	auditLog  audit.Logger
	ticketRef ticketref.Policy
}

// NewHandler creates an invite Handler. jwt mints the post-acceptance token
// pair for freshly created accounts, and sessions opens the row it is bound
// to — a post-acceptance auto-login is a login, so it needs a session like
// any other (B1: a token minted without one is refused on its first use).
func NewHandler(invitesSvc *invites.Service, jwt *auth.JWTService, sessions *auth.SessionService) *Handler {
	return &Handler{invites: invitesSvc, jwt: jwt, sessions: sessions, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithTicketRefPolicy attaches the boot-time ticket-reference requirement.
// The zero value leaves the reference optional, which is the default posture
// and exactly the behaviour that shipped before the flag existed.
func (h *Handler) WithTicketRefPolicy(p ticketref.Policy) *Handler {
	h.ticketRef = p
	return h
}

// AdminRoutes returns the org-admin lifecycle routes, mounted under
// /orgs/{orgID}/invites behind RequireOrgAdmin404.
func (h *Handler) AdminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Delete("/{inviteID}", h.Revoke)
	r.Post("/{inviteID}/resend", h.Resend)
	return r
}

// PublicRoutes returns the token-authenticated acceptance routes, mounted
// at /api/v1/invites with no auth middleware — possession of the raw token
// is the credential.
func (h *Handler) PublicRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{token}", h.Inspect)
	r.Post("/accept", h.Accept)
	return r
}

// inviteResponse is one pending invite, snake_case per spec §6.
type inviteResponse struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	OrgRole       string     `json:"org_role"`
	TeamID        *uuid.UUID `json:"team_id,omitempty"`
	TeamName      string     `json:"team_name,omitempty"`
	InvitedBy     uuid.UUID  `json:"invited_by"`
	InvitedByName string     `json:"invited_by_name,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	Expired       bool       `json:"expired"`
}

func toInviteResponse(inv invites.Invite) inviteResponse {
	return inviteResponse{
		ID:            inv.ID,
		Email:         inv.Email,
		OrgRole:       inv.OrgRole,
		TeamID:        inv.TeamID,
		TeamName:      inv.TeamName,
		InvitedBy:     inv.InvitedBy,
		InvitedByName: inv.InvitedByName,
		ExpiresAt:     inv.ExpiresAt,
		CreatedAt:     inv.CreatedAt,
		Expired:       inv.IsExpired(),
	}
}

// createdInviteResponse carries the one-time raw token material.
type createdInviteResponse struct {
	inviteResponse
	// InviteURL embeds the raw token. Shown once; the token is never
	// persisted and cannot be retrieved again (resend rotates it).
	InviteURL string `json:"invite_url"`
	Delivered bool   `json:"delivered"`
}

// createInvitesRequest invites one or many email addresses at once.
type createInvitesRequest struct {
	Emails  []string   `json:"emails"`
	OrgRole string     `json:"org_role"`
	TeamID  *uuid.UUID `json:"team_id"`
}

// createInviteResult is the per-email outcome of a bulk invite request.
type createInviteResult struct {
	Email string `json:"email"`
	// Status is "created" or an error kind: "invalid_email",
	// "already_member", "already_invited", "error".
	Status string                 `json:"status"`
	Error  string                 `json:"error,omitempty"`
	Invite *createdInviteResponse `json:"invite,omitempty"`
}

func orgIDFromRequest(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, false
	}
	return orgID, true
}

// List returns the org's pending invites.
//
// @Summary      List pending invites (admin)
// @Description  Pending (not accepted, not revoked) invites, expired ones included so they can be resent or revoked. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID"
// @Success      200     {array}   invites.inviteResponse    "Pending invites"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found (also returned to non-admins)"
// @Router       /orgs/{orgID}/invites [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	list, err := h.invites.List(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list invites")
		return
	}
	out := make([]inviteResponse, 0, len(list))
	for _, inv := range list {
		out = append(out, toInviteResponse(inv))
	}
	respond.JSON(w, http.StatusOK, out)
}

// maxBulkInviteEmails bounds one create request.
const maxBulkInviteEmails = 200

// Create invites one or several email addresses.
//
// @Summary      Create invites (admin)
// @Description  Invites one or many emails with an org role and optional initial team. Returns a per-email outcome; each created invite carries its one-time invite_url — the raw token is never stored and cannot be retrieved again. Org admins only.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID      path      string                        true   "Organization ID"
// @Param        ticket_ref  query     string                        false  "Operator ticket reference recorded on the audit event. Free text, no foreign key. Required when AZIMUTHAL_TICKET_REF_REQUIRED is set."
// @Param        body        body      invites.createInvitesRequest  true   "Emails, org role, optional team"
// @Success      201     {array}   invites.createInviteResult    "Per-email outcomes"
// @Failure      400     {object}  api.SwaggerErrorResponse      "Validation error, or a missing/over-long ticket_ref"
// @Failure      404     {object}  api.SwaggerErrorResponse      "Not found (also returned to non-admins)"
// @Router       /orgs/{orgID}/invites [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, req, ticketRef, ok := h.createPreconditions(w, r)
	if !ok {
		return
	}

	results := make([]createInviteResult, 0, len(req.Emails))
	created := 0
	for _, email := range req.Emails {
		res, fatal := h.createOneInvite(r, orgID, email, req, ticketRef)
		if fatal != "" {
			// Request-level validation problems (bad org_role, dead team)
			// apply to the whole request, not one email.
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, fatal)
			return
		}
		if res.Status == "created" {
			created++
		}
		results = append(results, res)
	}

	status := http.StatusCreated
	if created == 0 {
		// Nothing was created — the outcome list still explains each email.
		status = http.StatusOK
	}
	respond.JSON(w, status, results)
}

// createPreconditions settles everything that must hold before the first
// invite is issued: the org, the caller, the ticket reference and the shape
// of the request. Grouped so the ordering is visible in one place — every
// rejection here happens before any invite exists, which is what makes a 400
// in required mode mean "nothing went out" rather than "the first few went
// out unreferenced". Writes the error response itself; ok=false means stop.
func (h *Handler) createPreconditions(w http.ResponseWriter, r *http.Request) (uuid.UUID, createInvitesRequest, string, bool) {
	var req createInvitesRequest

	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return uuid.Nil, req, "", false
	}
	if claims := auth.ClaimsFromContext(r.Context()); claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return uuid.Nil, req, "", false
	}
	ticketRef, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return uuid.Nil, req, "", false
	}
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return uuid.Nil, req, "", false
	}
	if len(req.Emails) == 0 {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "emails must not be empty")
		return uuid.Nil, req, "", false
	}
	if len(req.Emails) > maxBulkInviteEmails {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "too many emails in one request")
		return uuid.Nil, req, "", false
	}
	return orgID, req, ticketRef, true
}

// createOneInvite creates one invite and classifies the outcome. A non-empty
// fatal return is a request-level validation message that fails the whole
// call.
// One bulk request is one administrative action, so every invite it creates
// carries the same ticketRef.
func (h *Handler) createOneInvite(r *http.Request, orgID uuid.UUID, email string, req createInvitesRequest, ticketRef string) (createInviteResult, string) {
	claims := auth.ClaimsFromContext(r.Context())
	res := createInviteResult{Email: invites.NormalizeEmail(email)}
	c, err := h.invites.Create(r.Context(), orgID, email, req.OrgRole, req.TeamID, claims.UserID)
	switch {
	case err == nil:
		res.Status = "created"
		cr := createdInviteResponse{inviteResponse: toInviteResponse(c.Invite), InviteURL: c.URL, Delivered: c.Delivered}
		res.Invite = &cr
		h.logInviteEvent(r, audit.EventTypeInviteCreated, c.Invite, ticketRef, map[string]string{"email": c.Invite.Email, "org_role": c.Invite.OrgRole})
	case errors.Is(err, invites.ErrInvalidEmail):
		res.Status = "invalid_email"
		res.Error = "invalid email address"
	case errors.Is(err, invites.ErrAlreadyMember):
		res.Status = "already_member"
		res.Error = "already a member of this organization"
	case errors.Is(err, invites.ErrDuplicateInvite):
		res.Status = "already_invited"
		res.Error = "an active invite for this email already exists"
	case errors.Is(err, invites.ErrInvalidOrgRole):
		return res, "org_role must be member or admin"
	case errors.Is(err, invites.ErrTeamNotFound):
		return res, "team not found in this organization"
	default:
		res.Status = "error"
		res.Error = "failed to create invite"
	}
	return res, ""
}

// Revoke marks an active invite revoked; its link stops working.
//
// @Summary      Revoke invite (admin)
// @Description  Revokes a pending invite. The invite link stops working immediately. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        orgID      path      string  true   "Organization ID"
// @Param        inviteID   path      string  true   "Invite ID"
// @Param        ticket_ref  query     string  false  "Operator ticket reference recorded on the audit event. Free text, no foreign key. Required when AZIMUTHAL_TICKET_REF_REQUIRED is set."
// @Success      204        "Revoked"
// @Failure      400        {object}  api.SwaggerErrorResponse  "Missing or over-long ticket_ref"
// @Failure      404        {object}  api.SwaggerErrorResponse  "Not found or no longer active"
// @Router       /orgs/{orgID}/invites/{inviteID} [delete]
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid invite_id")
		return
	}
	ticketRef, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	inv, err := h.invites.GetByID(r.Context(), orgID, inviteID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "invite not found")
		return
	}
	if err := h.invites.Revoke(r.Context(), orgID, inviteID); err != nil {
		if errors.Is(err, invites.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "invite not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to revoke invite")
		return
	}
	h.logInviteEvent(r, audit.EventTypeInviteRevoked, inv, ticketRef, map[string]string{"email": inv.Email})
	w.WriteHeader(http.StatusNoContent)
}

// Resend rotates the token and expiry of a pending invite.
//
// @Summary      Resend invite (admin)
// @Description  Generates a fresh token and expiry for a pending invite. The previous link stops working. Returns the new one-time invite_url. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        orgID      path      string  true   "Organization ID"
// @Param        inviteID   path      string  true   "Invite ID"
// @Param        ticket_ref  query     string  false  "Operator ticket reference recorded on the audit event. Free text, no foreign key. Required when AZIMUTHAL_TICKET_REF_REQUIRED is set."
// @Success      200        {object}  invites.createdInviteResponse  "New invite URL"
// @Failure      400        {object}  api.SwaggerErrorResponse       "Missing or over-long ticket_ref"
// @Failure      404        {object}  api.SwaggerErrorResponse       "Not found or no longer active"
// @Router       /orgs/{orgID}/invites/{inviteID}/resend [post]
func (h *Handler) Resend(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid invite_id")
		return
	}
	ticketRef, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	c, err := h.invites.Resend(r.Context(), orgID, inviteID)
	if err != nil {
		if errors.Is(err, invites.ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "invite not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to resend invite")
		return
	}
	h.logInviteEvent(r, audit.EventTypeInviteResent, c.Invite, ticketRef, map[string]string{"email": c.Invite.Email})
	respond.JSON(w, http.StatusOK, createdInviteResponse{
		inviteResponse: toInviteResponse(c.Invite),
		InviteURL:      c.URL,
		Delivered:      c.Delivered,
	})
}

// inspectResponse is the acceptance page's pre-submit view.
type inspectResponse struct {
	Email    string `json:"email"`
	OrgName  string `json:"org_name"`
	State    string `json:"state"`
	Existing bool   `json:"existing_account"`
}

// Inspect answers the acceptance page's lookup. Public; the token is the
// credential.
//
// @Summary      Inspect invite (public)
// @Description  Returns whom the invite is for, the org, its state, and whether the email already has an account. Token-authenticated.
// @Tags         auth
// @Produce      json
// @Param        token  path      string  true  "Raw invite token"
// @Success      200    {object}  invites.inspectResponse   "Invite details"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Unknown token"
// @Router       /invites/{token} [get]
func (h *Handler) Inspect(w http.ResponseWriter, r *http.Request) {
	insp, err := h.invites.Inspect(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "invite not found")
		return
	}
	respond.JSON(w, http.StatusOK, inspectResponse{
		Email:    insp.Email,
		OrgName:  insp.OrgName,
		State:    insp.State,
		Existing: insp.ExistingAccount,
	})
}

// acceptRequest consumes an invite. display_name and password are required
// only when the invited email has no account yet.
type acceptRequest struct {
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// acceptResponse reports the acceptance outcome. Tokens are minted only for
// freshly created accounts; an existing account signs in with its own
// password.
type acceptResponse struct {
	Status          string     `json:"status"`
	ExistingAccount bool       `json:"existing_account"`
	OrgID           uuid.UUID  `json:"org_id"`
	OrgSlug         string     `json:"org_slug"`
	OrgName         string     `json:"org_name"`
	AccessToken     string     `json:"access_token,omitempty"`
	RefreshToken    string     `json:"refresh_token,omitempty"`
	UserID          *uuid.UUID `json:"user_id,omitempty"`
}

// Accept consumes an invite: creates the account when the email is new, or
// adds a membership to the existing account — never a second user, never a
// second org.
//
// @Summary      Accept invite (public)
// @Description  Consumes an invite transactionally. A new email gets an account (display_name and password required) and a token pair; an email with an existing account gets a membership added and signs in normally.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      invites.acceptRequest     true  "Token, plus display_name and password for new accounts"
// @Success      200   {object}  invites.acceptResponse    "Joined"
// @Failure      400   {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      404   {object}  api.SwaggerErrorResponse  "Unknown token"
// @Failure      410   {object}  api.SwaggerErrorResponse  "Invite expired, revoked, or already used"
// @Router       /invites/accept [post]
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	var req acceptRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Token == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "token is required")
		return
	}
	var newUser *invites.NewUser
	if req.DisplayName != "" || req.Password != "" {
		newUser = &invites.NewUser{DisplayName: req.DisplayName, Password: req.Password}
	}

	outcome, err := h.invites.Accept(r.Context(), req.Token, newUser)
	if err != nil {
		h.mapAcceptError(w, r, err)
		return
	}

	resp := acceptResponse{
		Status:          "joined",
		ExistingAccount: outcome.ExistingAccount,
		OrgID:           outcome.OrgID,
		OrgSlug:         outcome.OrgSlug,
		OrgName:         outcome.OrgName,
	}
	if outcome.User != nil {
		id := outcome.User.ID
		resp.UserID = &id
		if !outcome.ExistingAccount {
			// Auto-login for the account created moments ago; an existing
			// account proves its password at the login form instead. A session
			// is opened and the pair bound to it, exactly as login does.
			sess, err := h.sessions.CreateSession(r.Context(), outcome.User.ID, r.UserAgent(), "")
			if err != nil {
				respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "joined, but failed to start a session — sign in normally")
				return
			}
			pair, err := h.jwt.IssueTokenPair(outcome.User.ID, outcome.User.Email, outcome.OrgID.String(), outcome.User.Role, outcome.User.TokenGeneration, sess.ID)
			if err != nil {
				respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "joined, but failed to issue tokens — sign in normally")
				return
			}
			resp.AccessToken = pair.AccessToken
			resp.RefreshToken = pair.RefreshToken
		}
		h.logAcceptEvent(r, outcome)
	}
	respond.JSON(w, http.StatusOK, resp)
}

// mapAcceptError translates acceptance errors. Dead invites are 410 Gone —
// distinct from 404 so the acceptance page can say why.
func (h *Handler) mapAcceptError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, invites.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "invite not found")
	case errors.Is(err, invites.ErrRevoked):
		respond.Error(w, r, http.StatusGone, respond.CodeConflict, "this invite has been revoked")
	case errors.Is(err, invites.ErrAlreadyAccepted):
		respond.Error(w, r, http.StatusGone, respond.CodeConflict, "this invite has already been used")
	case errors.Is(err, invites.ErrExpired):
		respond.Error(w, r, http.StatusGone, respond.CodeConflict, "this invite has expired — ask your admin to resend it")
	case errors.Is(err, invites.ErrAccountInactive):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "the account for this email is deactivated — ask your admin to reactivate it")
	case errors.Is(err, invites.ErrDisplayNameAndPasswordRequired):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "display_name and password are required")
	case errors.Is(err, invites.ErrPasswordTooShort):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "password must be at least 8 characters")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to accept invite")
	}
}

// logInviteEvent writes one invite lifecycle audit event. ticketRef is the
// operator-supplied reference for the administrative action; it is empty
// unless the caller sent one, and rides its own column rather than the
// metadata payload.
func (h *Handler) logInviteEvent(r *http.Request, event audit.EventType, inv invites.Invite, ticketRef string, meta map[string]string) {
	actor := ""
	if claims := auth.ClaimsFromContext(r.Context()); claims != nil {
		actor = claims.UserID.String()
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         event,
		ActorID:      actor,
		OrgID:        inv.OrgID.String(),
		ResourceType: "invite",
		ResourceID:   inv.ID.String(),
		Metadata:     meta,
		TicketRef:    ticketRef,
	})
}

// logAcceptEvent writes the acceptance audit event; the actor is the
// accepting user themselves.
func (h *Handler) logAcceptEvent(r *http.Request, outcome invites.AcceptOutcome) {
	meta := map[string]string{"email": outcome.User.Email}
	if outcome.ExistingAccount {
		meta["existing_account"] = "true"
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         audit.EventTypeInviteAccepted,
		ActorID:      outcome.User.ID.String(),
		OrgID:        outcome.OrgID.String(),
		ResourceType: "user",
		ResourceID:   outcome.User.ID.String(),
		Metadata:     meta,
	})
}
