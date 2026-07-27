// Package attachments provides HTTP handlers for entity attachments (P3,
// ADR-0008 rule 3). Two route families, mirroring shares:
//
//   - Space-scoped (/spaces/{spaceID}/attachments...) — upload, list,
//     stream, delete for a space member. The write floor (create_items)
//     already gates uploads; reads need only space-readability, both
//     enforced by the router guards. Every route re-checks that the
//     attachment's entity actually lives in the URL space.
//
//   - Shared (/shared/{entityType}/{entityID}/attachments...) — list and
//     stream for a viewer holding a share but no space access. Authorised
//     ONLY by share coverage, so a shared page's images render for its
//     audience. The object key is always taken from the row and the
//     attachment must belong to the covered entity — a guessed key or a
//     borrowed attachment id reads nothing (leak failure mode 4).
//
// Both families stream through one helper, [Handler.stream], and it decides
// the response Content-Type by SNIFFING the stored bytes — never from the
// attachment's content_type column. That column holds whatever the uploading
// client declared, and the shared family hands bytes to viewers outside the
// space, so an echoed type would let a space writer publish a same-origin
// document to a share's audience. The column remains in the wire response as
// display metadata; it is not a serving input. See
// [github.com/Azimuthal-HQ/azimuthal/internal/core/attachments.ServeTypeFor].
package attachments

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/attachments"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// maxUploadMemory caps in-memory multipart buffering; larger parts spill to
// temp files. The hard size ceiling lives in the service.
const maxUploadMemory = 8 << 20 // 8 MiB

// maxRequestBytes caps the whole multipart request body so a client cannot
// exhaust memory before the per-object ceiling is reached — the object
// ceiling plus 1 MiB of multipart framing headroom.
const maxRequestBytes = attachments.MaxSizeBytes + (1 << 20)

// Handler holds the dependencies for attachment HTTP handlers.
type Handler struct {
	svc    *attachments.Service
	shares *access.ShareService
}

// NewHandler creates an attachments Handler. shares supplies entity→space
// resolution (space path) and share coverage (shared path).
func NewHandler(svc *attachments.Service, shares *access.ShareService) *Handler {
	return &Handler{svc: svc, shares: shares}
}

// SpaceRoutes returns the space-scoped attachment router, mounted at
// /spaces/{spaceID}/attachments.
func (h *Handler) SpaceRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.ListInSpace)
	r.Post("/", h.Upload)
	r.Get("/{attachmentID}", h.DownloadInSpace)
	r.Delete("/{attachmentID}", h.DeleteInSpace)
	return r
}

// attachmentResponse is the wire form of attachment metadata (no object
// key — that is internal and never leaves the process).
type attachmentResponse struct {
	ID          uuid.UUID `json:"id"`
	EntityType  string    `json:"entity_type"`
	EntityID    uuid.UUID `json:"entity_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
}

func toAttachmentResponse(a attachments.Attachment) attachmentResponse {
	return attachmentResponse{
		ID:          a.ID,
		EntityType:  a.EntityType,
		EntityID:    a.EntityID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		SizeBytes:   a.SizeBytes,
		CreatedBy:   a.CreatedBy,
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Upload stores a new attachment on an entity in the URL space.
//
// @Summary      Upload an attachment
// @Description  Uploads a file attachment to an entity (page, ticket, project_item) in the space. Multipart form with fields entity_type, entity_id, and file. Requires the create_items write floor on the space.
// @Tags         attachments
// @Accept       mpfd
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      201      {object}  map[string]interface{}    "Created attachment metadata"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Validation error"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Entity not in this space"
// @Failure      413      {object}  api.SwaggerErrorResponse  "Attachment too large"
// @Router       /orgs/{orgID}/spaces/{spaceID}/attachments [post]
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := h.orgSpace(w, r)
	if !ok {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	entityType, entityID, ok := h.uploadTarget(w, r, orgID, spaceID)
	if !ok {
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "file is required")
		return
	}
	defer func() { _ = file.Close() }()

	att, err := h.svc.Upload(r.Context(), attachments.UploadInput{
		OrgID:       orgID,
		EntityType:  entityType,
		EntityID:    entityID,
		Filename:    header.Filename,
		ContentType: contentType(header.Header.Get("Content-Type")),
		CreatedBy:   claims.UserID,
		Content:     file,
		Size:        header.Size,
	})
	if errors.Is(err, attachments.ErrTooLarge) {
		respond.Error(w, r, http.StatusRequestEntityTooLarge, respond.CodeValidation, "attachment exceeds the maximum size")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to store attachment")
		return
	}
	respond.JSON(w, http.StatusCreated, toAttachmentResponse(att))
}

// uploadTarget bounds the request body, parses the multipart form, and
// resolves the named entity to the URL space. It writes its own error
// response and returns ok=false on any failure.
func (h *Handler) uploadTarget(w http.ResponseWriter, r *http.Request, orgID, spaceID uuid.UUID) (string, uuid.UUID, bool) {
	// Bound the whole body before touching the multipart parser so a client
	// cannot exhaust memory (gosec G120).
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid multipart form")
		return "", uuid.Nil, false
	}
	entityType := r.FormValue("entity_type")
	entityID, err := uuid.Parse(r.FormValue("entity_id"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_id is required")
		return "", uuid.Nil, false
	}
	if !access.ValidShareEntityType(entityType) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_type must be one of page, ticket, project_item")
		return "", uuid.Nil, false
	}
	// The entity must live in the URL space, or it does not exist here.
	ref, err := h.shares.LookupEntity(r.Context(), orgID, entityType, entityID)
	if err != nil || ref.SpaceID != spaceID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "entity not found")
		return "", uuid.Nil, false
	}
	return entityType, entityID, true
}

// ListInSpace lists an entity's attachments for a space member.
//
// @Summary      List attachments (space)
// @Description  Lists the attachments on an entity in the space. Requires space read access.
// @Tags         attachments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string  true  "Organization ID (UUID)"
// @Param        spaceID      path      string  true  "Space ID (UUID)"
// @Param        entity_type  query     string  true  "Entity type"
// @Param        entity_id    query     string  true  "Entity ID (UUID)"
// @Success      200          {array}   map[string]interface{}    "Attachments"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Entity not in this space"
// @Router       /orgs/{orgID}/spaces/{spaceID}/attachments [get]
func (h *Handler) ListInSpace(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := h.orgSpace(w, r)
	if !ok {
		return
	}
	entityType := r.URL.Query().Get("entity_type")
	entityID, err := uuid.Parse(r.URL.Query().Get("entity_id"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_id is required")
		return
	}
	if !access.ValidShareEntityType(entityType) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_type must be one of page, ticket, project_item")
		return
	}
	ref, err := h.shares.LookupEntity(r.Context(), orgID, entityType, entityID)
	if err != nil || ref.SpaceID != spaceID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "entity not found")
		return
	}
	h.writeList(w, r, entityType, entityID)
}

// DownloadInSpace streams an attachment for a space member. The attachment
// is loaded by id, then its entity is re-verified to live in the URL space.
//
// @Summary      Download an attachment (space)
// @Description  Streams an attachment's bytes for a space member. The Content-Type is SNIFFED from the stored bytes at serve time and the stored content_type is never echoed. PNG, JPEG, GIF, WebP and PDF are served inline with their sniffed type; every other type — including SVG, HTML and XML — is served as application/octet-stream with Content-Disposition: attachment, under its original filename.
// @Tags         attachments
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        orgID          path      string  true  "Organization ID (UUID)"
// @Param        spaceID        path      string  true  "Space ID (UUID)"
// @Param        attachmentID   path      string  true  "Attachment ID (UUID)"
// @Success      200            {file}    file                      "Attachment bytes"
// @Header       200            {string}  Content-Type              "Sniffed from the bytes; application/octet-stream unless the object is a PNG, JPEG, GIF, WebP or PDF"
// @Header       200            {string}  Content-Disposition       "inline for the allow-listed types, attachment otherwise; the stored filename is preserved either way"
// @Header       200            {string}  X-Content-Type-Options    "nosniff"
// @Failure      404            {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/attachments/{attachmentID} [get]
func (h *Handler) DownloadInSpace(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := h.orgSpace(w, r)
	if !ok {
		return
	}
	attID, err := uuid.Parse(chi.URLParam(r, "attachmentID"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	att, err := h.svc.Get(r.Context(), attID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	// Re-derive the entity's space and confirm it is the URL space — an
	// attachment on an entity outside this space does not exist here.
	ref, err := h.shares.LookupEntity(r.Context(), orgID, att.EntityType, att.EntityID)
	if err != nil || ref.SpaceID != spaceID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	h.stream(w, r, att)
}

// DeleteInSpace soft-deletes an attachment for a space member (the write
// floor already gated the request).
//
// @Summary      Delete an attachment (space)
// @Description  Soft-deletes an attachment. Requires the write floor on the space.
// @Tags         attachments
// @Security     BearerAuth
// @Param        orgID          path  string  true  "Organization ID (UUID)"
// @Param        spaceID        path  string  true  "Space ID (UUID)"
// @Param        attachmentID   path  string  true  "Attachment ID (UUID)"
// @Success      204  "Deleted"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/attachments/{attachmentID} [delete]
func (h *Handler) DeleteInSpace(w http.ResponseWriter, r *http.Request) {
	orgID, spaceID, ok := h.orgSpace(w, r)
	if !ok {
		return
	}
	attID, err := uuid.Parse(chi.URLParam(r, "attachmentID"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	att, err := h.svc.Get(r.Context(), attID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	ref, err := h.shares.LookupEntity(r.Context(), orgID, att.EntityType, att.EntityID)
	if err != nil || ref.SpaceID != spaceID {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	if err := h.svc.Delete(r.Context(), att.EntityType, att.EntityID, att.ID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete attachment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListShared lists a shared entity's attachments for a share holder.
//
// @Summary      List attachments (shared)
// @Description  Lists the attachments on a shared entity. Authorised by an active share; needs no space access.
// @Tags         attachments
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string  true  "Organization ID (UUID)"
// @Param        entityType   path      string  true  "Entity type"
// @Param        entityID     path      string  true  "Entity ID (UUID)"
// @Success      200          {array}   map[string]interface{}    "Attachments"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not shared with you"
// @Router       /orgs/{orgID}/shared/{entityType}/{entityID}/attachments [get]
func (h *Handler) ListShared(w http.ResponseWriter, r *http.Request) {
	entityType, entityID, ok := h.coveredEntity(w, r)
	if !ok {
		return
	}
	h.writeList(w, r, entityType, entityID)
}

// DownloadShared streams a shared entity's attachment for a share holder.
//
// @Summary      Download an attachment (shared)
// @Description  Streams a shared entity's attachment. Authorised by an active share; needs no space access. The attachment must belong to the shared entity, and its object key comes from the stored row. The Content-Type is SNIFFED from the stored bytes at serve time and the stored content_type is never echoed — this route crosses the space boundary, so a stored type taken on trust would be stored XSS against the share's audience. PNG, JPEG, GIF, WebP and PDF are served inline with their sniffed type; every other type — including SVG, HTML and XML — is served as application/octet-stream with Content-Disposition: attachment, under its original filename.
// @Tags         attachments
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        orgID          path      string  true  "Organization ID (UUID)"
// @Param        entityType     path      string  true  "Entity type"
// @Param        entityID       path      string  true  "Entity ID (UUID)"
// @Param        attachmentID   path      string  true  "Attachment ID (UUID)"
// @Success      200            {file}    file                      "Attachment bytes"
// @Header       200            {string}  Content-Type              "Sniffed from the bytes; application/octet-stream unless the object is a PNG, JPEG, GIF, WebP or PDF"
// @Header       200            {string}  Content-Disposition       "inline for the allow-listed types, attachment otherwise; the stored filename is preserved either way"
// @Header       200            {string}  X-Content-Type-Options    "nosniff"
// @Failure      404            {object}  api.SwaggerErrorResponse  "Not found / not shared"
// @Router       /orgs/{orgID}/shared/{entityType}/{entityID}/attachments/{attachmentID} [get]
func (h *Handler) DownloadShared(w http.ResponseWriter, r *http.Request) {
	entityType, entityID, ok := h.coveredEntity(w, r)
	if !ok {
		return
	}
	attID, err := uuid.Parse(chi.URLParam(r, "attachmentID"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	// GetForEntity binds the attachment to the covered entity: a valid
	// attachment id from another entity is ErrEntityMismatch → 404.
	att, err := h.svc.GetForEntity(r.Context(), entityType, entityID, attID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	h.stream(w, r, att)
}

// coveredEntity parses and validates {entityType}/{entityID} and enforces
// share coverage. Both "unknown type/id" and "not shared with you" answer
// 404 — the shared attachment path leaks neither existence nor shared-ness.
func (h *Handler) coveredEntity(w http.ResponseWriter, r *http.Request) (string, uuid.UUID, bool) {
	entityType := chi.URLParam(r, "entityType")
	entityID, err := uuid.Parse(chi.URLParam(r, "entityID"))
	if err != nil || !access.ValidShareEntityType(entityType) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return "", uuid.Nil, false
	}
	if !h.shares.CoversForCaller(r.Context(), entityType, entityID) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return "", uuid.Nil, false
	}
	return entityType, entityID, true
}

// writeList lists an entity's attachments as metadata.
func (h *Handler) writeList(w http.ResponseWriter, r *http.Request, entityType string, entityID uuid.UUID) {
	list, err := h.svc.ListByEntity(r.Context(), entityType, entityID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list attachments")
		return
	}
	out := make([]attachmentResponse, 0, len(list))
	for _, a := range list {
		out = append(out, toAttachmentResponse(a))
	}
	respond.JSON(w, http.StatusOK, out)
}

// stream writes the attachment's bytes with a content type SNIFFED from the
// object at serve time. The object key is read from the row, never from the
// request.
//
// The stored content_type is not consulted for serving — see
// attachments.ServeTypeFor for why, and for what the inline allow-list is.
// The short version: the stored value is whatever the uploader declared, so
// echoing it on an `inline`, same-origin response turns any space writer's
// file upload into stored XSS against every share recipient, who by ADR-0008
// sits outside the space. The column survives as display metadata (the file
// list, icons); only serving changed.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request, att attachments.Attachment) {
	rc, serveType, err := h.svc.OpenForServing(r.Context(), att)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to read attachment")
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", serveType.ContentType)
	w.Header().Set("Content-Disposition", disposition(serveType, att.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// disposition formats the Content-Disposition header. The stored filename is
// preserved either way — a file that downloads still downloads under its own
// name. %q quotes and escapes it, so a filename cannot inject a header.
func disposition(serveType attachments.ServeType, filename string) string {
	if serveType.Inline {
		return fmt.Sprintf("inline; filename=%q", filename)
	}
	return fmt.Sprintf("attachment; filename=%q", filename)
}

// orgSpace parses {orgID} and {spaceID}.
func (h *Handler) orgSpace(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, uuid.Nil, false
	}
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, spaceID, true
}

// contentType defaults a blank content type to a safe generic value.
//
// What this returns is DISPLAY METADATA and nothing more. It is the client's
// own claim about the file, stored so the UI can pick an icon and show a type
// in the file list. It is not a security control and must never be used to
// decide how bytes are served — the serve path sniffs (see stream).
func contentType(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}
