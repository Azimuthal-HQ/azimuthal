// Package wiki provides HTTP handlers for wiki/docs endpoints.
package wiki

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
)

// Handler holds the dependencies for wiki HTTP handlers.
type Handler struct {
	svc *wiki.Service
}

// NewHandler creates a wiki Handler.
func NewHandler(svc *wiki.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns a chi.Router with all wiki endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.ListPages)
	r.Post("/", h.CreatePage)
	r.Get("/tree", h.Tree)
	r.Get("/search", h.Search)
	r.Get("/{pageID}", h.GetPage)
	r.Put("/{pageID}", h.UpdatePage)
	r.Delete("/{pageID}", h.DeletePage)
	r.Post("/{pageID}/move", h.MovePage)
	r.Get("/{pageID}/revisions", h.ListRevisions)
	r.Get("/{pageID}/revisions/{version}", h.GetRevision)
	r.Get("/{pageID}/diff", h.DiffRevisions)
	r.Get("/{pageID}/render", h.RenderPage)
	return r
}

type createPageRequest struct {
	Title    string     `json:"title"`
	Content  string     `json:"content"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	Position int32      `json:"position"`
}

type updatePageRequest struct {
	Title           string `json:"title"`
	Content         string `json:"content"`
	ExpectedVersion int32  `json:"expected_version"`
}

type movePageRequest struct {
	ParentID *uuid.UUID `json:"parent_id"`
	Position int32      `json:"position"`
}

// ListPages returns all pages in a space.
//
// @Summary      List wiki pages
// @Description  Returns all pages in the specified space.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "List of pages"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki [get]
func (h *Handler) ListPages(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	pages, err := h.svc.ListPagesBySpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list pages")
		return
	}
	respond.JSON(w, http.StatusOK, pages)
}

// CreatePage creates a new wiki page.
//
// @Summary      Create wiki page
// @Description  Creates a new wiki page. Author is set from the JWT.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreatePageRequest  true  "Page details"
// @Success      201      {object}  map[string]interface{}         "Created page"
// @Failure      400      {object}  api.SwaggerErrorResponse       "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse       "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse       "Internal error"
// @Router       /spaces/{spaceID}/wiki [post]
func (h *Handler) CreatePage(w http.ResponseWriter, r *http.Request) {
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

	var req createPageRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	input := wiki.CreatePageInput{
		SpaceID:  spaceID,
		Title:    req.Title,
		Content:  req.Content,
		AuthorID: claims.UserID,
		Position: req.Position,
	}
	if req.ParentID != nil {
		input.ParentID = req.ParentID
	}

	page, err := h.svc.CreatePage(r.Context(), input)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusCreated, page)
}

// GetPage returns a single page by ID.
//
// @Summary      Get wiki page
// @Description  Returns a single wiki page by ID.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "Page details"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID} [get]
func (h *Handler) GetPage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	page, err := h.svc.GetPage(r.Context(), id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, page)
}

// UpdatePage updates a page with optimistic locking.
//
// @Summary      Update wiki page
// @Description  Updates a page with optimistic locking. Returns 409 with conflict details if version mismatch.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        pageID   path      string                        true  "Page ID (UUID)"
// @Param        body     body      api.SwaggerUpdatePageRequest  true  "Updated fields"
// @Success      200      {object}  map[string]interface{}         "Updated page"
// @Failure      400      {object}  api.SwaggerErrorResponse       "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse       "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse       "Not found"
// @Failure      409      {object}  map[string]interface{}          "Version conflict"
// @Failure      500      {object}  api.SwaggerErrorResponse       "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID} [put]
func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req updatePageRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	page, conflict, err := h.svc.UpdatePageOrConflict(r.Context(), wiki.UpdatePageInput{
		PageID:          id,
		ExpectedVersion: req.ExpectedVersion,
		Title:           req.Title,
		Content:         req.Content,
		AuthorID:        claims.UserID,
	})
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	if conflict != nil {
		respond.JSON(w, http.StatusConflict, conflict)
		return
	}
	respond.JSON(w, http.StatusOK, page)
}

// DeletePage soft-deletes a page.
//
// @Summary      Delete wiki page
// @Description  Soft-deletes a wiki page by ID.
// @Tags         wiki
// @Security     BearerAuth
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        pageID   path  string  true  "Page ID (UUID)"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID} [delete]
func (h *Handler) DeletePage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	if err := h.svc.DeletePage(r.Context(), id); err != nil {
		handleWikiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MovePage changes a page's parent or position in the tree.
//
// @Summary      Move wiki page
// @Description  Changes a page's parent or position in the tree hierarchy.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string                       true  "Space ID (UUID)"
// @Param        pageID   path      string                       true  "Page ID (UUID)"
// @Param        body     body      api.SwaggerMovePageRequest   true  "New position"
// @Success      200      {object}  api.SwaggerMessageResponse   "Page moved"
// @Failure      400      {object}  api.SwaggerErrorResponse     "Invalid request"
// @Failure      401      {object}  api.SwaggerErrorResponse     "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse     "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse     "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID}/move [post]
func (h *Handler) MovePage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	var req movePageRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	input := wiki.MovePageInput{
		PageID:   id,
		Position: req.Position,
	}
	if req.ParentID != nil {
		input.ParentID = req.ParentID
	}

	if err := h.svc.MovePage(r.Context(), input); err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"message": "page moved"})
}

// Tree returns the full page tree for a space.
//
// @Summary      Page tree
// @Description  Returns the full page tree hierarchy for a space.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Page tree"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/tree [get]
func (h *Handler) Tree(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	tree, err := h.svc.BuildTree(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to build page tree")
		return
	}
	respond.JSON(w, http.StatusOK, tree)
}

// ListRevisions returns the revision history for a page.
//
// @Summary      List page revisions
// @Description  Returns the revision history for a wiki page.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Revision history"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID}/revisions [get]
func (h *Handler) ListRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	revisions, err := h.svc.ListRevisions(r.Context(), id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, revisions)
}

// GetRevision returns a specific revision of a page.
//
// @Summary      Get page revision
// @Description  Returns a specific revision of a wiki page by version number.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        version  path      int     true  "Version number"
// @Success      200      {object}  map[string]interface{}    "Revision details"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID or version"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID}/revisions/{version} [get]
func (h *Handler) GetRevision(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	vStr := chi.URLParam(r, "version")
	v, err := strconv.ParseInt(vStr, 10, 32)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid version number")
		return
	}

	revision, err := h.svc.GetRevision(r.Context(), id, int32(v))
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, revision)
}

// DiffRevisions returns the diff between two page versions.
//
// @Summary      Diff page versions
// @Description  Returns the diff between two page versions. Requires 'from' and 'to' query parameters.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        from     query     int     true  "From version number"
// @Param        to       query     int     true  "To version number"
// @Success      200      {object}  map[string]interface{}    "Diff result"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Missing or invalid params"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID}/diff [get]
func (h *Handler) DiffRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" || toStr == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "'from' and 'to' version params are required")
		return
	}

	from, err := strconv.ParseInt(fromStr, 10, 32)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid 'from' version")
		return
	}
	to, err := strconv.ParseInt(toStr, 10, 32)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid 'to' version")
		return
	}

	diff, err := h.svc.DiffRevisions(r.Context(), id, int32(from), int32(to))
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, diff)
}

// RenderPage renders a page's markdown content as HTML.
//
// @Summary      Render page as HTML
// @Description  Renders a page's markdown content as HTML.
// @Tags         wiki
// @Produce      html
// @Security     BearerAuth
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        pageID   path  string  true  "Page ID (UUID)"
// @Success      200  {string}  string                    "Rendered HTML"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/{pageID}/render [get]
func (h *Handler) RenderPage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	page, err := h.svc.GetPage(r.Context(), id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}

	html, err := h.svc.RenderPage(page.Content)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to render page")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write([]byte(html)); writeErr != nil {
		slog.Error("writing rendered html response", "error", writeErr)
	}
}

// Search performs full-text search on wiki pages.
//
// @Summary      Search wiki pages
// @Description  Full-text search on wiki pages in a space. Requires query parameter 'q'.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        spaceID  path      string  true   "Space ID (UUID)"
// @Param        q        query     string  true   "Search query"
// @Param        limit    query     int     false  "Max results (1-200, default 50)"
// @Success      200      {array}   map[string]interface{}    "Search results"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Missing query"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /spaces/{spaceID}/wiki/search [get]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "query parameter 'q' is required")
		return
	}

	limit := wiki.DefaultSearchLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		n, parseErr := strconv.ParseInt(l, 10, 32)
		if parseErr == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}

	results, err := h.svc.SearchPages(r.Context(), wiki.SearchInput{
		SpaceID: spaceID,
		Query:   query,
		Limit:   limit,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "search failed")
		return
	}
	respond.JSON(w, http.StatusOK, results)
}

func pageIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "pageID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing page ID: %w", err)
	}
	return id, nil
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing space ID: %w", err)
	}
	return id, nil
}

func handleWikiError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, wiki.ErrPageNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, wiki.ErrVersionConflict):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, wiki.ErrEmptyTitle),
		errors.Is(err, wiki.ErrInvalidSpaceID),
		errors.Is(err, wiki.ErrInvalidAuthorID):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, wiki.ErrRevisionNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("wiki operation failed: %v", err))
	}
}
