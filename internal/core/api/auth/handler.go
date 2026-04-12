// Package auth provides HTTP handlers for authentication endpoints.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// MembershipResolver looks up a user's primary organization after login.
type MembershipResolver interface {
	// PrimaryOrgForUser returns the org ID, slug, and name for the user's
	// primary organization (owner role preferred, then earliest membership).
	PrimaryOrgForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, string, string, error)
}

// OrgProvisioner creates a personal organization and membership for newly
// registered users. When nil, Register skips org provisioning (useful in tests).
type OrgProvisioner interface {
	// ProvisionOrg creates a personal org for a user and returns the org ID and slug.
	ProvisionOrg(ctx context.Context, displayName string) (uuid.UUID, string, error)
	// CreateMembership adds the user as owner of the given org.
	CreateMembership(ctx context.Context, orgID, userID uuid.UUID) error
}

// Handler holds the dependencies for auth HTTP handlers.
type Handler struct {
	users       *auth.UserService
	jwt         *auth.JWTService
	sessions    *auth.SessionService
	memberships MembershipResolver
	orgs        OrgProvisioner
}

// NewHandler creates an auth Handler.
func NewHandler(users *auth.UserService, jwt *auth.JWTService, sessions *auth.SessionService, memberships MembershipResolver, orgs OrgProvisioner) *Handler {
	return &Handler{users: users, jwt: jwt, sessions: sessions, memberships: memberships, orgs: orgs}
}

// Routes returns a chi.Router with all auth endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/login", h.Login)
	r.Post("/register", h.Register)
	r.Post("/refresh", h.Refresh)
	r.Post("/logout", h.Logout)
	return r
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	Token        string       `json:"token"`
	User         userResponse `json:"user"`
	Org          *orgResponse `json:"org,omitempty"`
}

type orgResponse struct {
	ID   uuid.UUID `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
}

type registerRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	OrgID       string    `json:"org_id"`
	Role        string    `json:"role"`
	IsActive    bool      `json:"is_active"`
}

// Login authenticates a user and returns a JWT token pair.
//
// @Summary      Authenticate user
// @Description  Validates email and password, returns JWT access/refresh tokens and user profile with primary org.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      api.SwaggerLoginRequest  true  "Login credentials"
// @Success      200   {object}  api.SwaggerLoginResponse       "Authenticated successfully"
// @Failure      400   {object}  api.SwaggerErrorResponse       "Missing email or password"
// @Failure      401   {object}  api.SwaggerErrorResponse       "Invalid credentials"
// @Failure      500   {object}  api.SwaggerErrorResponse       "Internal error"
// @Router       /auth/login [post]
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "email and password are required")
		return
	}

	user, err := h.users.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrAccountInactive) {
			respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "invalid email or password")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "authentication failed")
		return
	}

	// Resolve the user's primary org from memberships.
	// Falls back to the user's org_id if no memberships exist (e.g. registered
	// via the API but not yet added to an org through admin create-user).
	orgID, orgSlug, orgName, err := h.memberships.PrimaryOrgForUser(r.Context(), user.ID)
	if err != nil {
		orgID = user.OrgID
		orgSlug = ""
		orgName = ""
	}

	pair, err := h.jwt.IssueTokenPair(user.ID, user.Email, orgID.String(), user.Role)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to issue tokens")
		return
	}

	respond.JSON(w, http.StatusOK, loginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		Token:        pair.AccessToken,
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			OrgID:       orgID.String(),
			Role:        user.Role,
			IsActive:    user.IsActive,
		},
		Org: &orgResponse{
			ID:   orgID,
			Slug: orgSlug,
			Name: orgName,
		},
	})
}

// provisionOrgForUser creates a personal org and membership if an OrgProvisioner
// is configured. Returns the org ID and slug (both zero values when orgs is nil).
func (h *Handler) provisionOrgForUser(ctx context.Context, displayName, email string, userID uuid.UUID) (uuid.UUID, string, error) {
	if h.orgs == nil {
		return uuid.Nil, "", nil
	}
	name := displayName
	if name == "" {
		name = email
	}
	orgID, slug, err := h.orgs.ProvisionOrg(ctx, name)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("provisioning org: %w", err)
	}
	if userID != uuid.Nil {
		if err := h.orgs.CreateMembership(ctx, orgID, userID); err != nil {
			return uuid.Nil, "", fmt.Errorf("creating membership: %w", err)
		}
	}
	return orgID, slug, nil
}

// Register creates a new user account and returns a JWT token pair.
// When an OrgProvisioner is configured, each new user gets a personal
// organization and an owner membership in it.
//
// @Summary      Register new user
// @Description  Creates a new user account with a personal organization, returns JWT tokens.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      api.SwaggerRegisterRequest  true  "Registration details"
// @Success      201   {object}  api.SwaggerLoginResponse          "User created"
// @Failure      400   {object}  api.SwaggerErrorResponse          "Validation error"
// @Failure      409   {object}  api.SwaggerErrorResponse          "Email already in use"
// @Failure      500   {object}  api.SwaggerErrorResponse          "Internal error"
// @Router       /auth/register [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "email and password are required")
		return
	}

	// Provision a personal org before creating the user so the user row
	// has a valid org_id foreign key.
	orgID, orgSlug, err := h.provisionOrgForUser(r.Context(), req.DisplayName, req.Email, uuid.Nil)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create organization")
		return
	}

	user, err := h.users.CreateUserInOrg(r.Context(), req.Email, req.DisplayName, req.Password, orgID)
	if err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "email address already in use")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create user")
		return
	}

	// Create owner membership now that we have the user ID.
	if h.orgs != nil {
		if err := h.orgs.CreateMembership(r.Context(), orgID, user.ID); err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create membership")
			return
		}
	}

	pair, err := h.jwt.IssueTokenPair(user.ID, user.Email, user.OrgID.String(), user.Role)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to issue tokens")
		return
	}

	respond.JSON(w, http.StatusCreated, loginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		Token:        pair.AccessToken,
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			OrgID:       user.OrgID.String(),
			Role:        user.Role,
			IsActive:    user.IsActive,
		},
		Org: &orgResponse{
			ID:   orgID,
			Slug: orgSlug,
			Name: req.DisplayName,
		},
	})
}

// Refresh exchanges a refresh token for a new token pair.
//
// @Summary      Refresh tokens
// @Description  Exchanges a valid refresh token for a new access/refresh token pair.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      api.SwaggerRefreshRequest   true  "Refresh token"
// @Success      200   {object}  api.SwaggerRefreshResponse        "New token pair"
// @Failure      400   {object}  api.SwaggerErrorResponse          "Missing refresh_token"
// @Failure      401   {object}  api.SwaggerErrorResponse          "Invalid or expired refresh token"
// @Failure      500   {object}  api.SwaggerErrorResponse          "Internal error"
// @Router       /auth/refresh [post]
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "refresh_token is required")
		return
	}

	pair, err := h.jwt.RefreshTokens(req.RefreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "invalid or expired refresh token")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to refresh tokens")
		return
	}

	respond.JSON(w, http.StatusOK, refreshResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
	})
}

// Logout invalidates all sessions for the current user. Requires authentication.
//
// @Summary      Logout user
// @Description  Deletes all sessions for the current user.
// @Tags         auth
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  api.SwaggerLogoutResponse  "Logged out"
// @Failure      401  {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse   "Internal error"
// @Router       /auth/logout [post]
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	if err := h.sessions.DeleteAllSessions(r.Context(), claims.UserID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to logout")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// Me returns the current authenticated user's profile.
//
// @Summary      Get current user
// @Description  Returns the profile of the currently authenticated user.
// @Tags         auth
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  api.SwaggerUserResponse   "User profile"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /auth/me [get]
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	user, err := h.users.GetUser(r.Context(), claims.UserID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to get user")
		return
	}

	respond.JSON(w, http.StatusOK, userResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		OrgID:       user.OrgID.String(),
		Role:        user.Role,
		IsActive:    user.IsActive,
	})
}
