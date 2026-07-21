package admin

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

// personRefResponse is one picker search result.
type personRefResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	AvatarURL   *string   `json:"avatar_url,omitempty"`
}

// SearchMembers backs the person picker: name-or-email search over active
// org members. Org-member scoped (any member may pick people — grants and
// team membership panels are operated by space admins who are not
// necessarily org admins), mounted OUTSIDE the admin guard.
//
// @Summary      Search org members
// @Description  Name-or-email search over active members, for person pickers. Bounded result set. Any org member.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  path      string  true   "Organization ID"
// @Param        q       query     string  false  "Search text (empty returns the first page alphabetically)"
// @Success      200     {array}   admin.personRefResponse   "Matches"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Org not found or caller not a member"
// @Router       /orgs/{org_id}/members/search [get]
func (h *Handler) SearchMembers(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	refs, err := h.people.Search(r.Context(), orgID, query)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to search members")
		return
	}
	out := make([]personRefResponse, 0, len(refs))
	for _, ref := range refs {
		out = append(out, personRefResponse{ID: ref.ID, Email: ref.Email, DisplayName: ref.DisplayName, AvatarURL: ref.AvatarURL})
	}
	respond.JSON(w, http.StatusOK, out)
}
