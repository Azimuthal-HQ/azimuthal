package wiki

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/attachments"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// maxImageUploadMemory caps in-memory multipart buffering for an image upload;
// larger parts spill to temp files. The hard ceiling is the attachment service's.
const maxImageUploadMemory = 8 << 20 // 8 MiB

type saveDraftRequest struct {
	Title       string          `json:"title"`
	Doc         json.RawMessage `json:"doc"`
	BaseVersion int32           `json:"base_version"`
}

type publishRequest struct {
	Title       string          `json:"title"`
	Doc         json.RawMessage `json:"doc"`
	BaseVersion int32           `json:"base_version"`
	// AcknowledgedLostIDs names the preserved blocks the author deliberately
	// deleted. Absent means "I deleted none", which is what makes an
	// unacknowledged disappearance a refusal rather than a silent write.
	AcknowledgedLostIDs []string `json:"acknowledged_lost_ids,omitempty"`
	// Overwrite is the explicit arm of the conflict dialogue.
	Overwrite bool `json:"overwrite,omitempty"`
}

// GetDocument returns a page's editable document plus the caller's own draft.
//
// @Summary      Get a page's editable document
// @Description  Returns the published ProseMirror document for a page, with every node type outside the editor's schema replaced by a preservation placeholder (ADR-0012), plus the calling user's own unpublished draft if they hold one. A page that has only ever held markdown is converted on the way out and not written back.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "Editable document"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/document [get]
func (h *Handler) GetDocument(w http.ResponseWriter, r *http.Request) {
	pageID, claims, ok := h.pageAndCaller(w, r)
	if !ok {
		return
	}
	document, err := h.docs.OpenDocument(r.Context(), pageID, claims.UserID)
	if err != nil {
		handleDocumentError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, document)
}

// SaveDraft autosaves the caller's unpublished edit of a page.
//
// @Summary      Save a page draft
// @Description  Stores the calling user's unpublished edit of a page, replacing any previous one. A draft is visible only to its author; readers continue to see the last published version. Requires the same permission as editing the page.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        body     body      map[string]interface{}  true  "Draft title, document and base version"
// @Success      200      {object}  map[string]interface{}    "Saved draft"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Malformed document"
// @Failure      403      {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/draft [put]
func (h *Handler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	pageID, claims, ok := h.editablePage(w, r)
	if !ok {
		return
	}
	var req saveDraftRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	draft, err := h.docs.SaveDraft(r.Context(), wiki.SaveDraftInput{
		PageID:      pageID,
		AuthorID:    claims.UserID,
		Title:       req.Title,
		Doc:         req.Doc,
		BaseVersion: req.BaseVersion,
	})
	if err != nil {
		handleDocumentError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, draft)
}

// DiscardDraft removes the caller's draft of a page.
//
// @Summary      Discard a page draft
// @Description  Removes the calling user's unpublished draft of a page. The published page is untouched. 404 when the caller holds no draft, so a confirmed destructive action never reports success for something it did not do.
// @Tags         wiki
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        pageID   path  string  true  "Page ID (UUID)"
// @Success      204  "Discarded"
// @Failure      403  {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No draft on this page"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/draft [delete]
func (h *Handler) DiscardDraft(w http.ResponseWriter, r *http.Request) {
	pageID, claims, ok := h.editablePage(w, r)
	if !ok {
		return
	}
	if err := h.docs.DiscardDraft(r.Context(), pageID, claims.UserID); err != nil {
		handleDocumentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListDrafts returns the pages in this space on which the caller holds a draft.
//
// @Summary      List the caller's page drafts
// @Description  Every page in the space on which the calling user holds an unpublished draft. Author-scoped: a draft is never visible to anybody else.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Drafts"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Space not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/drafts [get]
func (h *Handler) ListDrafts(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	drafts, err := h.docs.DraftsInSpace(r.Context(), spaceID, claims.UserID)
	if err != nil {
		handleDocumentError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, drafts)
}

// Publish replaces a page's published content with the caller's document.
//
// @Summary      Publish a page document
// @Description  Replaces the page's published content, bumps its version, records a revision and clears the caller's draft, all in one transaction. 409 with a conflict body when the page has been published past base_version — reload or overwrite. 409 with a lost-content body when the document no longer carries preserved content the page had, unless the removal is acknowledged (ADR-0012).
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        body     body      map[string]interface{}  true  "Title, document, base version"
// @Success      200      {object}  map[string]interface{}    "Published page"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Malformed document or empty title"
// @Failure      403      {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      409      {object}  map[string]interface{}    "Version conflict, or preserved content would be lost"
// @Failure      422      {object}  api.SwaggerErrorResponse  "Unresolvable preserved content, or a bad image reference"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/publish [post]
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	pageID, claims, ok := h.editablePage(w, r)
	if !ok {
		return
	}
	var req publishRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	result, err := h.docs.Publish(r.Context(), wiki.PublishInput{
		PageID:              pageID,
		AuthorID:            claims.UserID,
		Title:               req.Title,
		Doc:                 req.Doc,
		BaseVersion:         req.BaseVersion,
		AcknowledgedLostIDs: req.AcknowledgedLostIDs,
		Overwrite:           req.Overwrite,
	})
	if err != nil {
		handleDocumentError(w, r, err)
		return
	}
	// Both 409 bodies are prose written for a person: friendlyErrorMessage passes
	// CONFLICT messages straight through to the UI, so this text IS the dialogue
	// the author reads.
	if result.Conflict != nil {
		respond.JSON(w, http.StatusConflict, result.Conflict)
		return
	}
	if result.LostContent != nil {
		respond.JSON(w, http.StatusConflict, result.LostContent)
		return
	}

	// The existing page-update event, not a new one: publishing IS updating the
	// page, and the audit viewer already renders page.updated. The metadata says
	// how, so the trail distinguishes a document publish from a markdown save.
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypePageUpdated, ActorID: claims.UserID.String(),
		OrgID: claims.OrgID, ResourceType: "page", ResourceID: pageID.String(),
		Metadata: map[string]string{
			"via":     "document_publish",
			"version": strconv.FormatInt(int64(result.Page.Version), 10),
		},
	})
	respond.JSON(w, http.StatusOK, result.Page)
}

// UploadImage stores an image on a page for the editor to reference.
//
// @Summary      Upload a page image
// @Description  Stores an image on the page through the shared attachments table and object store. The content type is sniffed from the bytes and checked against the document model's allow-list (PNG, JPEG, WebP, GIF) — the client's declared type is never trusted. The entity comes from the URL, not the form.
// @Tags         wiki
// @Accept       mpfd
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      201      {object}  map[string]interface{}    "Stored image"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Not a supported image"
// @Failure      403      {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      413      {object}  api.SwaggerErrorResponse  "Image too large"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/images [post]
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	pageID, claims, ok := h.editablePage(w, r)
	if !ok {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	if err := r.ParseMultipartForm(maxImageUploadMemory); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid multipart form")
		return
	}
	file, header, formErr := r.FormFile("file")
	if formErr != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "file is required")
		return
	}
	defer func() { _ = file.Close() }()

	image, err := h.docs.UploadImage(r.Context(), wiki.UploadImageInput{
		OrgID:      orgID,
		PageID:     pageID,
		Filename:   header.Filename,
		UploadedBy: claims.UserID,
		Content:    file,
		Size:       header.Size,
	})
	if err != nil {
		handleDocumentError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, image)
}

// pageAndCaller parses the page id and requires authentication. Read paths use
// it; the space-read guard on the subtree has already run.
func (h *Handler) pageAndCaller(w http.ResponseWriter, r *http.Request) (uuid.UUID, *auth.Claims, bool) {
	pageID, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return uuid.Nil, nil, false
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return uuid.Nil, nil, false
	}
	return pageID, claims, true
}

// editablePage is pageAndCaller plus the page-edit capability check.
//
// The subtree's write floor (create_items) has already refused viewers, so this
// is the check that actually decides anything: it refuses a contributor who holds
// edit_own_items on a page somebody else created. Drafting and publishing are the
// same permission as editing — this phase adds no capability — so it is the same
// access.CanEditEntity call the markdown save path makes, against the same
// ownership key (pages.author_id, the page's creator).
func (h *Handler) editablePage(w http.ResponseWriter, r *http.Request) (uuid.UUID, *auth.Claims, bool) {
	pageID, claims, ok := h.pageAndCaller(w, r)
	if !ok {
		return uuid.Nil, nil, false
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, nil, false
	}
	page, err := h.svc.GetPage(r.Context(), pageID)
	if err != nil {
		handleWikiError(w, r, err)
		return uuid.Nil, nil, false
	}
	if !access.CanEditEntity(r.Context(), spaceID, page.AuthorID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return uuid.Nil, nil, false
	}
	return pageID, claims, true
}

// handleDocumentError maps the document surface's errors to responses.
//
// The 422s are deliberately distinct from the 409s. A 409 is a state the author
// can resolve — reload, or confirm. A 422 means the request itself does not add
// up, which is a client defect or a stale editor session, and pretending it is a
// choice the author can make would send them round a loop.
func handleDocumentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, wiki.ErrPageNotFound), errors.Is(err, wiki.ErrDraftNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, wiki.ErrEmptyTitle):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a page needs a title")
	case errors.Is(err, wiki.ErrVersionConflict):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, wiki.ErrUnknownPreservedContent), errors.Is(err, wiki.ErrBaseVersionUnavailable):
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation,
			"This edit could not be matched to the version of the page it started from. Reload the page and re-apply your changes.")
	case errors.Is(err, wiki.ErrImageNotOnPage):
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation,
			"An image in this page refers to a file that is not attached to it.")
	case errors.Is(err, wiki.ErrImageNotAnImage), errors.Is(err, attachments.ErrUnsupportedImage):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"That file is not a PNG, JPEG, WebP or GIF image.")
	case errors.Is(err, attachments.ErrTooLarge):
		respond.Error(w, r, http.StatusRequestEntityTooLarge, respond.CodeValidation,
			"That image is larger than the 25 MB limit.")
	case errors.Is(err, doc.ErrTooDeep):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"This page is nested more deeply than the editor supports.")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "the page could not be saved")
	}
}
