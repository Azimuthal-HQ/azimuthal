package wiki

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
)

// pageSuggestion is one typeahead result for the page picker.
//
// page_id is what the picker submits — a relation target is (type, id) — and
// everything else is context for the row the operator is choosing between:
// two spaces can each have a page titled "Runbook", and the space key is how
// a human tells them apart.
type pageSuggestion struct {
	PageID    uuid.UUID `json:"page_id"`
	Title     string    `json:"title"`
	SpaceID   uuid.UUID `json:"space_id"`
	SpaceKey  string    `json:"space_key"`
	SpaceName string    `json:"space_name"`
}

// WithPageSuggestions attaches the page suggestion service, enabling
// SuggestPages. Optional in the builder because the route it backs is
// org-scoped and registered directly on the org group; the harness wires it,
// and TestHarness_NoDarkDependencies fails on a nil.
func (h *Handler) WithPageSuggestions(s *wiki.PageSuggestionService) *Handler {
	h.suggestions = s
	return h
}

// SuggestPages backs the page-picker typeahead: Codex pages the caller can
// read, most recently updated first.
//
// Org-member scoped and mounted beside /tickets/suggest, for the same reason
// that endpoint sits outside the admin guard: the picker it fills appears on
// panels operated by people who are not org admins, and the result set is cut
// to the caller's own resolved readable spaces, so this shows an operator
// exactly the pages they could already open by hand. It is also the
// cross-space page search pageSearch.ts once declared out of scope — the
// route-shape question ADR-0010 governs is answered the same way every other
// cross-space read answers it, with the per-viewer readable set in-handler.
//
// @Summary      Suggest pages
// @Description  Typeahead over Codex pages the caller can read, for the relation page picker. Most recently updated first. Bounded result set. Any org member.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true   "Organization ID (UUID)"
// @Param        q      query     string  false  "Match text: part of a page title (empty returns the default ordering)"
// @Success      200    {array}   wiki.pageSuggestion       "Matches"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Org not found or caller not a member"
// @Failure      500    {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/pages/suggest [get]
func (h *Handler) SuggestPages(w http.ResponseWriter, r *http.Request) {
	// Fail closed before anything else. No resolution on the context means
	// the resolution middleware did not run, so there is no readable set to
	// filter by — and the one thing this endpoint must never do is answer
	// that question from an unfiltered query. The empty answer is the safe
	// one; the route is mounted under RequireAuth, so reaching here at all
	// would be a wiring defect rather than a caller's doing.
	res := access.FromContext(r.Context())
	if res == nil {
		respond.JSON(w, http.StatusOK, []pageSuggestion{})
		return
	}
	if h.suggestions == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"page suggestions are not configured")
		return
	}

	// No client-supplied limit: the bound is in the query, so a typeahead
	// cannot be turned into a bulk export of every page the caller can read.
	results, err := h.suggestions.Suggest(r.Context(), res.ReadableSpaceIDs(),
		strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			"failed to suggest pages")
		return
	}

	out := make([]pageSuggestion, 0, len(results))
	for _, s := range results {
		out = append(out, pageSuggestion{
			PageID:    s.PageID,
			Title:     s.Title,
			SpaceID:   s.SpaceID,
			SpaceKey:  s.SpaceKey,
			SpaceName: s.SpaceName,
		})
	}
	respond.JSON(w, http.StatusOK, out)
}
