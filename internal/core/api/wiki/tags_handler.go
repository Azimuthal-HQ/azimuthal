package wiki

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
)

// setPageTagsRequest is the whole-set replacement of a page's tags.
//
// A list, not a patch. Tagging is a small closed set an author looks at while
// they edit it, so "these are the tags" is both the simpler contract and the
// one that can express removal — a patch shape would need a second field for
// deletions and would still be ambiguous about an empty one.
type setPageTagsRequest struct {
	Tags []string `json:"tags"`
}

// ListOrgTags returns every tag in the org.
//
// @Summary      List the organisation's Codex tags
// @Description  Every tag in the organisation, ordered by display name. Tags are org-scoped and are created by use — publishing a page whose body contains an inline #tag, or adding one to a page's tag list, creates it. There is no administration surface, so this list is the whole vocabulary. Any org member may read it: it backs the tag autocomplete, and a tag name is not itself sensitive — the pages carrying a tag are filtered separately.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {array}   map[string]interface{}    "Tags"
// @Failure      400    {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Router       /orgs/{orgID}/tags [get]
func (h *Handler) ListOrgTags(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	list, err := h.tags.List(r.Context(), orgID)
	if err != nil {
		handleTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// ListPagesWithTag returns the readable pages carrying a tag.
//
// @Summary      List the pages carrying a tag
// @Description  Every page in the organisation carrying this tag that the caller can read. Cross-space by nature — a tag is org-scoped — so the result is filtered against the caller's resolved readable space set (ADR-0010). A page in a space the caller cannot read is absent, not refused, so the response never reports whether such a page exists.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Param        slug   path      string  true  "Tag slug"
// @Success      200    {object}  map[string]interface{}    "The tag and its readable pages"
// @Failure      400    {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse  "No such tag"
// @Router       /orgs/{orgID}/tags/{slug}/pages [get]
func (h *Handler) ListPagesWithTag(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if slug == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "a tag slug is required")
		return
	}

	// The readable set, from the request's resolved access. A nil resolution
	// yields an empty set and therefore an empty result — fail closed, never
	// fail open (ADR-0010, and the same rule the saved-views surface follows).
	var readable []uuid.UUID
	if res := access.FromContext(r.Context()); res != nil {
		readable = res.ReadableSpaceIDs()
	}

	tag, pages, err := h.tags.PagesWithSlug(r.Context(), orgID, slug, readable)
	if err != nil {
		handleTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"tag": tag, "pages": pages})
}

// ListPageTags returns the tags a page carries.
//
// @Summary      List a page's tags
// @Description  The tags this page carries. Page-level tags are metadata rather than document content: they are stored alongside the page, not inside its body, so they survive a rewrite of the prose. Inline #tag tokens in the body are aggregated into this same set when the page is published.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Tags"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/tags [get]
func (h *Handler) ListPageTags(w http.ResponseWriter, r *http.Request) {
	pageID, _, ok := h.pageAndCaller(w, r)
	if !ok {
		return
	}
	list, err := h.tags.ForPage(r.Context(), pageID)
	if err != nil {
		handleTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// SetPageTags replaces a page's tags.
//
// @Summary      Set a page's tags
// @Description  Replaces the page's tag list with exactly the tags given. This is the authoritative path — a tag left out is removed from the page, which the aggregation of inline #tag tokens at publish deliberately cannot do. Tags that do not exist yet are created. Requires the same permission as editing the page.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        body     body      map[string]interface{}  true  "The page's tags"
// @Success      200      {array}   map[string]interface{}    "The page's tags"
// @Failure      400      {object}  api.SwaggerErrorResponse  "A tag name that cannot become a tag, or too many tags"
// @Failure      403      {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/tags [put]
func (h *Handler) SetPageTags(w http.ResponseWriter, r *http.Request) {
	pageID, _, ok := h.editablePage(w, r)
	if !ok {
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	var req setPageTagsRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	list, err := h.tags.SetPageTags(r.Context(), orgID, pageID, req.Tags)
	if err != nil {
		handleTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// handleTagError maps the tag model's errors to responses.
func handleTagError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tags.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "no tag by that name")
	case errors.Is(err, tags.ErrInvalidName):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"A tag needs at least one letter or digit.")
	case errors.Is(err, tags.ErrTooManyTags):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"That is more tags than one page can carry.")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "the tags could not be read")
	}
}
