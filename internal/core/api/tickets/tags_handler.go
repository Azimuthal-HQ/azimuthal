package tickets

import (
	"errors"
	"net/http"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
)

// setTicketTagsRequest is the whole-set replacement of a ticket's tags — the
// same wire shape as the page tag editor, because it is the same surface on a
// different entity kind.
type setTicketTagsRequest struct {
	Tags []string `json:"tags"`
}

// ListTicketTags returns the tags a ticket carries.
//
// @Summary      List a ticket's tags
// @Description  The tags this ticket carries. Tags are org-scoped and entity-generic (migration 055): the same vocabulary a page is tagged with, filterable in search with tag: and browsable across modules.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200       {array}   map[string]interface{}    "Tags"
// @Failure      400       {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401       {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/tags [get]
func (h *Handler) ListTicketTags(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	// The route proved {spaceID} readable and proved nothing about {ticketID};
	// the query reconciles the two. A ticket in another space carries no tags
	// here rather than being refused, so the answer never says whether such a
	// ticket exists.
	list, err := h.tags.ForEntity(r.Context(), tags.EntityRef{
		Type: tags.EntityTicket, ID: id, SpaceID: spaceID,
	})
	if err != nil {
		handleTicketTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// SetTicketTags replaces a ticket's tags.
//
// @Summary      Set a ticket's tags
// @Description  Replaces the ticket's tag list with exactly the tags given. Tags that do not exist yet are created. Requires the same permission as editing the ticket.
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Param        body      body      map[string]interface{}  true  "The ticket's tags"
// @Success      200       {array}   map[string]interface{}    "The ticket's tags"
// @Failure      400       {object}  api.SwaggerErrorResponse  "A tag name that cannot become a tag, or too many tags"
// @Failure      403       {object}  api.SwaggerErrorResponse  "Insufficient permissions"
// @Failure      404       {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/tags [put]
func (h *Handler) SetTicketTags(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid ticket ID")
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
	var req setTicketTagsRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// The same shape as Update: the ticket the permission check is about has
	// to be the ticket in the space the caller was authorised against, and
	// setting its tags is editing it.
	existing, err := h.svc.GetInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleTicketError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, creatorOf(existing)) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	list, err := h.tags.SetEntityTags(r.Context(), orgID, tags.EntityRef{
		Type: tags.EntityTicket, ID: id, SpaceID: spaceID,
	}, req.Tags)
	if err != nil {
		handleTicketTagError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, list)
}

// handleTicketTagError maps the tag model's errors to responses, identically
// to the wiki tag handler's mapping — the sentinels are the tag domain's, not
// a module's.
func handleTicketTagError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tags.ErrEntityNotFound):
		// The same 404 whether the ticket never existed or sits in a space the
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
