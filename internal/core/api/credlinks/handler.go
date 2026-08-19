// Package credlinks is the internal-user credential-link HTTP surface: the
// public forgot-password / inspect / consume routes (possession of the raw token
// is the credential), the authenticated email-change request, and the org-admin
// issuance routes (create a member behind a sign-in link, mint a reset link).
package credlinks

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/credlink"
)

// Handler holds the credential-link surface dependencies. jwt and sessions mint
// the post-sign-in token pair for a consumed sign-in link (an auto-login is a
// login, so it needs a session like any other — B1); users reads the account
// back at its current generation and reauthenticates the email-change request.
type Handler struct {
	svc      *credlink.Service
	users    *auth.UserService
	jwt      *auth.JWTService
	sessions *auth.SessionService
	auditLog audit.Logger
}

// NewHandler creates a credential-link Handler.
func NewHandler(svc *credlink.Service, users *auth.UserService, jwt *auth.JWTService, sessions *auth.SessionService) *Handler {
	return &Handler{svc: svc, users: users, jwt: jwt, sessions: sessions, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// PublicRoutes returns the token-authenticated routes, mounted at
// /api/v1/credential-links with no auth middleware — possession of the raw token
// is the credential, exactly like the invite and portal public routes.
func (h *Handler) PublicRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/forgot-password", h.ForgotPassword)
	r.Post("/inspect", h.Inspect)
	r.Post("/consume", h.Consume)
	return r
}

// AdminRoutes returns the org-admin issuance routes, mounted under
// /orgs/{orgID}/credential-links behind RequireOrgAdmin404.
func (h *Handler) AdminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/users", h.CreateUser)
	r.Post("/reset", h.IssueReset)
	return r
}

// ── wire DTOs (snake_case per spec §6) ───────────────────────────────────────

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type statusResponse struct {
	Status string `json:"status"`
}

type inspectRequest struct {
	Token string `json:"token"`
}

type inspectResponse struct {
	Purpose string `json:"purpose"`
	// NewEmail is present only for the email_change purpose.
	NewEmail string `json:"new_email,omitempty"`
}

type consumeRequest struct {
	Token    string `json:"token"`
	Password string `json:"password,omitempty"`
}

type consumeResponse struct {
	Status  string `json:"status"`
	Purpose string `json:"purpose"`
	// The token pair is present only for the signin purpose (a redeemed sign-in
	// link signs the user in); a reset or an email change deliberately mints no
	// session — every existing one has just been revoked.
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type emailChangeRequest struct {
	NewEmail        string `json:"new_email"`
	CurrentPassword string `json:"current_password"`
}

// linkResponse carries a one-time link URL to an authorised caller (an admin
// minting a link, or the reauthenticated requester in the no-relay email-change
// case). Never returned on the unauthenticated forgot-password path.
type linkResponse struct {
	Status    string    `json:"status"`
	URL       string    `json:"url,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	UserID    string    `json:"user_id,omitempty"`
	Delivered bool      `json:"delivered"`
}

type createUserRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
}

type issueResetRequest struct {
	Email string `json:"email"`
}

// ── public routes ────────────────────────────────────────────────────────────

// ForgotPassword issues a password-reset link for the given address.
//
// @Summary      Request a password reset (public)
// @Description  Issues a password-reset link and, when a mail relay is configured, emails it. Answers 202 identically whether the address is known — it is not an account-existence oracle — and NEVER returns the link in the body (this is unauthenticated; the admin-issued link is the no-relay answer).
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      credlinks.forgotPasswordRequest  true  "Email address"
// @Success      202   {object}  credlinks.statusResponse
// @Router       /credential-links/forgot-password [post]
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Email == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "email is required")
		return
	}
	// RequestReset never differentiates known from unknown (or an infra failure)
	// in what it returns, so the response is fixed regardless.
	_ = h.svc.RequestReset(r.Context(), req.Email)
	respond.JSON(w, http.StatusAccepted, statusResponse{Status: "sent"})
}

// Inspect reports a link's purpose without consuming it.
//
// @Summary      Inspect a credential link (public)
// @Description  A non-consuming validity check so the redemption page can render the right form. Possession of the token is the credential. An invalid, consumed, superseded or expired link is 404, indistinguishably.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      credlinks.inspectRequest   true  "Raw token"
// @Success      200   {object}  credlinks.inspectResponse
// @Failure      404   {object}  api.SwaggerErrorResponse   "Invalid or expired"
// @Router       /credential-links/inspect [post]
func (h *Handler) Inspect(w http.ResponseWriter, r *http.Request) {
	var req inspectRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	insp, err := h.svc.Inspect(r.Context(), req.Token)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "this link is invalid or has expired")
		return
	}
	respond.JSON(w, http.StatusOK, inspectResponse{
		Purpose:  string(insp.Purpose),
		NewEmail: insp.NewEmail,
	})
}

// Consume redeems a credential link and applies its effect.
//
// @Summary      Consume a credential link (public)
// @Description  Redeems a link once. A sign-in link sets a password and signs in (token pair returned); a password reset sets a password and revokes every session; an email change binds the pending address and bumps the token generation. Possession of the token is the credential.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      credlinks.consumeRequest   true  "Token, plus password for signin/reset"
// @Success      200   {object}  credlinks.consumeResponse
// @Failure      400   {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      404   {object}  api.SwaggerErrorResponse   "Invalid or expired"
// @Failure      409   {object}  api.SwaggerErrorResponse   "Address now in use"
// @Router       /credential-links/consume [post]
func (h *Handler) Consume(w http.ResponseWriter, r *http.Request) {
	var req consumeRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	consumed, err := h.svc.Consume(r.Context(), req.Token, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, credlink.ErrPasswordTooShort):
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "password must be at least 8 characters")
		case errors.Is(err, credlink.ErrPasswordRequired):
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a password is required to redeem this link")
		case errors.Is(err, credlink.ErrEmailTaken):
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "that email address is already in use")
		case errors.Is(err, credlink.ErrInvalidLink):
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "this link is invalid or has expired")
		default:
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to redeem this link")
		}
		return
	}

	// Read the account back at its current (post-consume) generation. Required
	// for the sign-in mint; best-effort for the audit org id otherwise.
	user, userErr := h.users.GetUser(r.Context(), consumed.UserID)

	resp := consumeResponse{Purpose: string(consumed.Purpose)}
	switch consumed.Purpose {
	case credlink.PurposeSignIn:
		if userErr != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "password set, but sign-in failed — sign in normally")
			return
		}
		sess, err := h.sessions.CreateSession(r.Context(), user.ID, r.UserAgent(), "")
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "password set, but sign-in failed — sign in normally")
			return
		}
		pair, err := h.jwt.IssueTokenPair(user.ID, user.Email, user.OrgID.String(), user.Role, user.TokenGeneration, sess.ID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "password set, but sign-in failed — sign in normally")
			return
		}
		resp.Status = "signed_in"
		resp.AccessToken = pair.AccessToken
		resp.RefreshToken = pair.RefreshToken
	case credlink.PurposePasswordReset:
		resp.Status = "password_reset"
	case credlink.PurposeEmailChange:
		resp.Status = "email_changed"
	}

	h.logConsumed(r, consumed, user, userErr)
	respond.JSON(w, http.StatusOK, resp)
}

// ── authenticated email-change request ───────────────────────────────────────

// RequestEmailChange starts an authenticated email change. Mounted at
// /api/v1/auth/me/email-change behind RequireAuth.
//
// @Summary      Request an email change
// @Description  Requires the current password (reauth) — that alone closes the token-thief vector the old direct email-write left open (C.2-c). With a relay the confirmation link goes to the NEW address and 202 is returned; without a relay the link is returned to the reauthenticated requester (weaker — no proof of new-address control — but the reauth plus generation bump is the security content).
// @Tags         auth
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      credlinks.emailChangeRequest  true  "New email and current password"
// @Success      202   {object}  credlinks.statusResponse   "Confirmation link emailed to the new address"
// @Success      200   {object}  credlinks.linkResponse     "No relay: link returned to the requester"
// @Failure      400   {object}  api.SwaggerErrorResponse   "Validation error"
// @Failure      401   {object}  api.SwaggerErrorResponse   "Wrong current password"
// @Failure      409   {object}  api.SwaggerErrorResponse   "Address already in use"
// @Router       /auth/me/email-change [post]
func (h *Handler) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	var req emailChangeRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.NewEmail == "" || req.CurrentPassword == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "new_email and current_password are required")
		return
	}

	// Reauthenticate against the current password before issuing anything. This
	// is the whole security content of the request half: an XSS/token thief who
	// holds a bearer token does not hold the password.
	user, err := h.users.GetUser(r.Context(), claims.UserID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	if err := auth.ComparePassword(user.PasswordHash, req.CurrentPassword); err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "current password is incorrect")
		return
	}

	issued, err := h.svc.RequestEmailChange(r.Context(), user.ID, user.OrgID, user.Email, req.NewEmail)
	if err != nil {
		switch {
		case errors.Is(err, credlink.ErrInvalidEmail):
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a valid, different email address is required")
		case errors.Is(err, credlink.ErrEmailTaken):
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "that email address is already in use")
		default:
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to start the email change")
		}
		return
	}

	h.logIssued(r, credlink.PurposeEmailChange, user.OrgID, claims.UserID, user.ID)
	if issued.Delivered {
		respond.JSON(w, http.StatusAccepted, statusResponse{Status: "sent"})
		return
	}
	// No relay: hand the URL back to the reauthenticated requester.
	respond.JSON(w, http.StatusOK, linkResponse{
		Status:    "issued",
		URL:       issued.URL,
		ExpiresAt: issued.ExpiresAt,
		Delivered: false,
	})
}

// ── admin issuance ───────────────────────────────────────────────────────────

// CreateUser provisions a member and returns their one-time sign-in link.
//
// @Summary      Create a member with a sign-in link (admin)
// @Description  Creates an account with a default grant and no password, and returns a one-time sign-in link the admin hands over. The user sets their own password on redemption. Org admins only; non-admins receive 404.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                       true  "Organization ID"
// @Param        body   body      credlinks.createUserRequest  true  "Email, name, and optional org role"
// @Success      201    {object}  credlinks.linkResponse
// @Failure      400    {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Not an org admin"
// @Failure      409    {object}  api.SwaggerErrorResponse  "Email already a member"
// @Router       /orgs/{orgID}/credential-links/users [post]
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	var req createUserRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	issued, userID, err := h.svc.CreateUserWithSignInLink(r.Context(), credlink.NewUser{
		OrgID:       orgID,
		Email:       req.Email,
		DisplayName: req.Name,
		Role:        req.Role,
		CreatedBy:   claims.UserID,
	})
	if err != nil {
		switch {
		case errors.Is(err, credlink.ErrInvalidEmail):
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a valid email address and name are required")
		case errors.Is(err, credlink.ErrInvalidRole):
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "role must be owner, admin, or member")
		case errors.Is(err, credlink.ErrEmailTaken):
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "that email address already belongs to a member")
		default:
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create the user")
		}
		return
	}

	h.logIssued(r, credlink.PurposeSignIn, orgID, claims.UserID, userID)
	respond.JSON(w, http.StatusCreated, linkResponse{
		Status:    "created",
		URL:       issued.URL,
		ExpiresAt: issued.ExpiresAt,
		UserID:    userID.String(),
	})
}

// IssueReset mints a password-reset link for an existing member.
//
// @Summary      Issue a password-reset link (admin)
// @Description  Mints a one-time password-reset link for a member of this org, returned to the admin once. An address that is not a member here (including one that exists only in another org) is 404 — indistinguishable from never-existed. Org admins only; non-admins receive 404.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                       true  "Organization ID"
// @Param        body   body      credlinks.issueResetRequest  true  "Member email"
// @Success      200    {object}  credlinks.linkResponse
// @Failure      400    {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Not an org admin, or no such member"
// @Router       /orgs/{orgID}/credential-links/reset [post]
func (h *Handler) IssueReset(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	var req issueResetRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	issued, err := h.svc.IssueReset(r.Context(), orgID, req.Email, claims.UserID)
	if err != nil {
		switch {
		case errors.Is(err, credlink.ErrInvalidEmail):
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a valid email address is required")
		case errors.Is(err, credlink.ErrUserNotFound):
			// Indistinguishable from never-existed: a 404 that says nothing about
			// whether the address is a member of some other org.
			respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "no such member")
		default:
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to issue the reset link")
		}
		return
	}

	// The reset targets a user resolved from the email; re-resolve for the audit
	// row would double the lookup, so record the acting admin and org and leave
	// the resource id to the issued link's own trail.
	h.logIssuedEmail(r, credlink.PurposePasswordReset, orgID, claims.UserID, req.Email)
	respond.JSON(w, http.StatusOK, linkResponse{
		Status:    "issued",
		URL:       issued.URL,
		ExpiresAt: issued.ExpiresAt,
	})
}

// ── audit ────────────────────────────────────────────────────────────────────

func (h *Handler) logIssued(r *http.Request, purpose credlink.Purpose, orgID, actor, target uuid.UUID) {
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         audit.EventTypeCredentialLinkIssued,
		ActorID:      actor.String(),
		OrgID:        orgID.String(),
		ResourceType: "user",
		ResourceID:   target.String(),
		Metadata:     map[string]string{"purpose": string(purpose)},
	})
}

func (h *Handler) logIssuedEmail(r *http.Request, purpose credlink.Purpose, orgID, actor uuid.UUID, targetEmail string) {
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         audit.EventTypeCredentialLinkIssued,
		ActorID:      actor.String(),
		OrgID:        orgID.String(),
		ResourceType: "user",
		Metadata:     map[string]string{"purpose": string(purpose), "email": targetEmail},
	})
}

func (h *Handler) logConsumed(r *http.Request, consumed credlink.Consumed, user *auth.User, userErr error) {
	orgID := ""
	if userErr == nil && user != nil {
		orgID = user.OrgID.String()
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         audit.EventTypeCredentialLinkConsumed,
		ActorID:      consumed.UserID.String(),
		OrgID:        orgID,
		ResourceType: "user",
		ResourceID:   consumed.UserID.String(),
		Metadata:     map[string]string{"purpose": string(consumed.Purpose)},
	})
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
