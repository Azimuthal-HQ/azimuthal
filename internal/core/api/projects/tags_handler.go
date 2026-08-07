package projects

import (
	"errors"
	"net/http"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
)

// setItemTagsRequest is the whole-set replacement of a project item's tags —
// the same wire shape as the page and ticket tag editors, because it is the
// same surface on a different entity kind.
type setItemTagsRequest struct {
	Tags []string `json:"tags"`
}

// ListItemTags returns the tags a project item carries.
//
// @Summary      List a project item's tags
// @Description  The tags this item carries. Tags are org-scoped and entity-generic (migration 055): the same vocabulary a page is tagged with, filterable in search with tag: and browsable across modules.
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Tags"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/tags [get]
func (h *Handler) ListItemTags(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	// The route proved {spaceID} readable and proved nothing about {itemID};
	// the query reconciles the two. An item in another space carries no tags
	// here rather than being refused, so the answer never says whether such an
	// item exists.
	list, err := h.tags.ForEntity(r.Context(), tags.EntityRef{
		Type: tags.EntityProjectItem, ID: id, SpaceID: spaceID,
	})
	if err != nil {
		handleItemTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// SetItemTags replaces a project item's tags.
//
// @Summary      Set a project item's tags
// @Description  Replaces the item's tag list with exactly the tags given. Tags that do not exist yet are created. Requires the same permission as editing the item.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Param        body     body      map[string]interface{}  true  "The item's tags"
// @Success      200      {array}   map[string]interface{}    "The item's tags"
// @Failure      400      {object}  api.SwaggerErrorResponse  "A tag name that cannot become a tag, or too many tags"
// @Failure      403      {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/tags [put]
func (h *Handler) SetItemTags(w http.ResponseWriter, r *http.Request) {
	id, err := itemIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid item ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	var req setItemTagsRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// The same shape as UpdateItem: the item the permission check is about has
	// to be the item in the space the caller was authorised against, and
	// setting its tags is editing it.
	existing, err := h.items.GetItemInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, existing.ReporterID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	list, err := h.tags.SetEntityTags(r.Context(), orgID, tags.EntityRef{
		Type: tags.EntityProjectItem, ID: id, SpaceID: spaceID,
	}, req.Tags)
	if err != nil {
		handleItemTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// handleItemTagError maps the tag model's errors to responses, identically to
// the wiki and ticket tag handlers' mapping — the sentinels are the tag
// domain's, not a module's.
func handleItemTagError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tags.ErrEntityNotFound):
		// The same 404 whether the item never existed or sits in a space the
		// caller cannot reach — the two must not be distinguishable.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
	case errors.Is(err, tags.ErrInvalidName):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"A tag needs at least one letter or digit.")
	case errors.Is(err, tags.ErrTooManyTags):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"That is more tags than one entity can carry.")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "the tags could not be read")
	}
}
