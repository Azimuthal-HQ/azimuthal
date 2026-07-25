package tickets

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
)

// ticketRefSuggestion is one typeahead result for the ticket_ref field.
//
// ref is the string the picker inserts. Everything else is context for the
// row the operator is choosing between — two spaces can each have a ticket
// numbered 42, and the title is how a human tells them apart.
//
// status is a plain string rather than tickets.Status so the generated
// OpenAPI schema shows it as one; the wire value is identical either way.
type ticketRefSuggestion struct {
	Ref          string    `json:"ref"`
	TicketID     uuid.UUID `json:"ticket_id"`
	Number       int32     `json:"number"`
	Title        string    `json:"title"`
	SpaceID      uuid.UUID `json:"space_id"`
	SpaceKey     string    `json:"space_key"`
	Status       string    `json:"status"`
	AssignedToMe bool      `json:"assigned_to_me"`
}

// SuggestRefs backs the ticket_ref typeahead: Beacon tickets the caller can
// read, their own assignments first, then most recently updated.
//
// Org-member scoped, mounted OUTSIDE the admin guard, for the same reason
// the person picker is (admin.SearchMembers): the field it fills appears on
// panels operated by space admins who are not necessarily org admins, and a
// picker that 404s for half the people who use the form is not a picker.
// That is not a widening — the result set is cut to the caller's own
// resolved readable spaces, so this endpoint shows an operator exactly the
// tickets they could already open by hand.
//
// Free text remains fully valid in ticket_ref everywhere. This endpoint only
// assists; nothing downstream requires that a reference came from it.
//
// @Summary      Suggest ticket references
// @Description  Typeahead over Beacon tickets the caller can read, for the ticket_ref field. Caller's own assignments first, then most recently updated. Bounded result set. Any org member.
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true   "Organization ID (UUID)"
// @Param        q      query     string  false  "Match text: part of a title, or a reference such as BEA-42 (empty returns the default ordering)"
// @Success      200    {array}   tickets.ticketRefSuggestion  "Matches"
// @Failure      401    {object}  api.SwaggerErrorResponse     "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse     "Org not found or caller not a member"
// @Failure      500    {object}  api.SwaggerErrorResponse     "Internal error"
// @Router       /orgs/{orgID}/tickets/suggest [get]
func (h *Handler) SuggestRefs(w http.ResponseWriter, r *http.Request) {
	// Fail closed before anything else. No resolution on the context means
	// the resolution middleware did not run, so there is no readable set to
	// filter by — and the one thing this endpoint must never do is answer
	// that question from an unfiltered query. The empty answer is the safe
	// one; the route is mounted under RequireAuth, so reaching here at all
	// would be a wiring defect rather than a caller's doing.
	res := access.FromContext(r.Context())
	if res == nil {
		respond.JSON(w, http.StatusOK, []ticketRefSuggestion{})
		return
	}
	if h.suggestions == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"ticket suggestions are not configured")
		return
	}

	// No client-supplied limit: the bound is in the query, so a typeahead
	// cannot be turned into a bulk export of every ticket the caller can read.
	results, err := h.suggestions.Suggest(r.Context(), tickets.SuggestParams{
		ReadableSpaceIDs: res.ReadableSpaceIDs(),
		CallerID:         res.UserID,
		Query:            strings.TrimSpace(r.URL.Query().Get("q")),
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"failed to suggest tickets")
		return
	}

	out := make([]ticketRefSuggestion, 0, len(results))
	for _, s := range results {
		out = append(out, ticketRefSuggestion{
			Ref:          s.Ref,
			TicketID:     s.TicketID,
			Number:       s.Number,
			Title:        s.Title,
			SpaceID:      s.SpaceID,
			SpaceKey:     s.SpaceKey,
			Status:       string(s.Status),
			AssignedToMe: s.AssignedToMe,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}
