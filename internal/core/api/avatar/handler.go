// Package avatar serves the user avatar surface: self and admin upload plus
// the org-member-readable serve endpoint. Avatars reuse the shared object
// store; the object key is always derived server-side from the ids, never
// accepted from the client.
package avatar

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
)

// Handler wires the avatar service to HTTP.
type Handler struct {
	svc      *people.AvatarService
	auditLog audit.Logger
}

// NewHandler creates an avatar Handler with a no-op audit logger by default.
func NewHandler(svc *people.AvatarService) *Handler {
	return &Handler{svc: svc, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

type avatarResponse struct {
	AvatarURL string `json:"avatar_url"`
}

// SelfUpload sets the caller's own avatar (PUT /auth/me/avatar).
//
// @Summary      Set my avatar
// @Description  Uploads the caller's own avatar. The image type is sniffed from the bytes server-side, never taken from the declared content type, and only PNG, JPEG, WebP and GIF are accepted. The object key is derived from the org and user ids.
// @Tags         auth
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file  formData  file  true  "Image file (PNG, JPEG, WebP or GIF; 5 MiB maximum)"
// @Success      200   {object}  avatar.avatarResponse     "The serve URL now recorded on the user"
// @Failure      400   {object}  api.SwaggerErrorResponse  "No file part, or an invalid organization on the token"
// @Failure      401   {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      413   {object}  api.SwaggerErrorResponse  "Image exceeds the maximum size"
// @Failure      415   {object}  api.SwaggerErrorResponse  "Not a supported image type"
// @Failure      500   {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /auth/me/avatar [put]
func (h *Handler) SelfUpload(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	orgID, err := uuid.Parse(claims.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid organization")
		return
	}
	h.upload(w, r, orgID, claims.UserID, claims.UserID)
}

// AdminUpload sets another member's avatar (PUT /orgs/{orgID}/users/{userID}/avatar).
// Mounted behind the org-admin-404 guard.
//
// @Summary      Set a member's avatar (admin)
// @Description  Uploads another member's avatar. Same server-side type sniffing and size ceiling as the self route. Org admins only; non-admins receive 404.
// @Tags         admin
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string  true  "Organization ID (UUID)"
// @Param        userID  path      string  true  "User ID (UUID)"
// @Param        file    formData  file    true  "Image file (PNG, JPEG, WebP or GIF; 5 MiB maximum)"
// @Success      200     {object}  avatar.avatarResponse     "The serve URL now recorded on the user"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Invalid ids, or no file part"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found (also returned to non-admins, and when the target is not a member)"
// @Failure      413     {object}  api.SwaggerErrorResponse  "Image exceeds the maximum size"
// @Failure      415     {object}  api.SwaggerErrorResponse  "Not a supported image type"
// @Router       /orgs/{orgID}/users/{userID}/avatar [put]
func (h *Handler) AdminUpload(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	orgID, ok := uuidParam(w, r, "orgID", "org_id")
	if !ok {
		return
	}
	userID, ok := uuidParam(w, r, "userID", "user_id")
	if !ok {
		return
	}
	h.upload(w, r, orgID, userID, claims.UserID)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request, orgID, userID, actorID uuid.UUID) {
	file, _, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "file is required")
		return
	}
	defer func() { _ = file.Close() }()

	url, err := h.svc.SetAvatar(r.Context(), orgID, userID, file)
	switch {
	case errors.Is(err, people.ErrAvatarTooLarge):
		respond.Error(w, r, http.StatusRequestEntityTooLarge, respond.CodeValidation, "avatar exceeds the maximum size")
		return
	case errors.Is(err, people.ErrAvatarType):
		respond.Error(w, r, http.StatusUnsupportedMediaType, respond.CodeValidation, "avatar must be a PNG, JPEG, WebP, or GIF image")
		return
	case errors.Is(err, people.ErrNotMember):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "member not found")
		return
	case err != nil:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to store avatar")
		return
	}

	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type:         audit.EventTypeUserAvatarChanged,
		ActorID:      actorID.String(),
		OrgID:        orgID.String(),
		ResourceType: "user",
		ResourceID:   userID.String(),
		Metadata:     map[string]string{},
	})
	respond.JSON(w, http.StatusOK, avatarResponse{AvatarURL: url})
}

// Serve streams a member's avatar (GET /orgs/{orgID}/users/{userID}/avatar).
// Any org member may read it; the object key is derived from the ids.
//
// @Summary      Get a member's avatar
// @Description  Streams a member's avatar image. Readable by any org member — avatars are shown org-wide — which is why this route sits outside the admin guard its sibling PUT carries. The response content type is sniffed from the stored bytes and served only if it is a supported image; anything else answers 404 rather than being rendered.
// @Tags         admin
// @Produce      png
// @Security     BearerAuth
// @Param        orgID   path  string  true  "Organization ID (UUID)"
// @Param        userID  path  string  true  "User ID (UUID)"
// @Success      200     {file}    binary                    "The image bytes"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Invalid ids"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "No avatar set, or the stored object is not a supported image"
// @Router       /orgs/{orgID}/users/{userID}/avatar [get]
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	orgID, ok := uuidParam(w, r, "orgID", "org_id")
	if !ok {
		return
	}
	userID, ok := uuidParam(w, r, "userID", "user_id")
	if !ok {
		return
	}
	data, contentType, err := h.svc.AvatarObject(r.Context(), orgID, userID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "avatar not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, bytes.NewReader(data))
}

func uuidParam(w http.ResponseWriter, r *http.Request, name, label string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid "+label)
		return uuid.Nil, false
	}
	return id, true
}
