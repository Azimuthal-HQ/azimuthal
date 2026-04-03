// Package auth provides HTTP handlers for authentication endpoints.
package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// Handler holds the dependencies for auth HTTP handlers.
type Handler struct {
	users    *auth.UserService
	jwt      *auth.JWTService
	sessions *auth.SessionService
}

// NewHandler creates an auth Handler.
func NewHandler(users *auth.UserService, jwt *auth.JWTService, sessions *auth.SessionService) *Handler {
	return &Handler{users: users, jwt: jwt, sessions: sessions}
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
	User         userResponse `json:"user"`
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
	IsActive    bool      `json:"is_active"`
}

// Login authenticates a user and returns a JWT token pair.
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

	pair, err := h.jwt.IssueTokenPair(user.ID, user.Email)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to issue tokens")
		return
	}

	respond.JSON(w, http.StatusOK, loginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			IsActive:    user.IsActive,
		},
	})
}

// Register creates a new user account and returns a JWT token pair.
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

	user, err := h.users.CreateUser(r.Context(), req.Email, req.DisplayName, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "email address already in use")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create user")
		return
	}

	pair, err := h.jwt.IssueTokenPair(user.ID, user.Email)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to issue tokens")
		return
	}

	respond.JSON(w, http.StatusCreated, loginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			IsActive:    user.IsActive,
		},
	})
}

// Refresh exchanges a refresh token for a new token pair.
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
