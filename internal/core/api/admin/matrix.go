package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// matrixTeamResponse is one matrix row.
type matrixTeamResponse struct {
	ID          uuid.UUID   `json:"id"`
	ParentID    *uuid.UUID  `json:"parent_id,omitempty"`
	Path        []uuid.UUID `json:"path"`
	Name        string      `json:"name"`
	IsDefault   bool        `json:"is_default"`
	MemberCount int         `json:"member_count"`
}

// matrixSpaceResponse is one matrix column.
type matrixSpaceResponse struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Visibility string    `json:"visibility"`
}

// matrixGrantResponse is one direct (solid) cell. Inherited (ghosted) cells
// are derived client-side from team paths and correspond to no grant row.
type matrixGrantResponse struct {
	ID      uuid.UUID `json:"id"`
	TeamID  uuid.UUID `json:"team_id"`
	SpaceID uuid.UUID `json:"space_id"`
	Role    string    `json:"role"`
}

type matrixResponse struct {
	Teams  []matrixTeamResponse  `json:"teams"`
	Spaces []matrixSpaceResponse `json:"spaces"`
	Grants []matrixGrantResponse `json:"grants"`
}

// AccessMatrix returns the teams × spaces grant matrix.
//
// @Summary      Access matrix (admin)
// @Description  Teams (tree, member counts), spaces, and every direct team grant, in a constant number of queries. Inherited access is derived client-side from team paths. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  path      string  true  "Organization ID"
// @Success      200     {object}  admin.matrixResponse       "Matrix data"
// @Failure      401     {object}  api.SwaggerErrorResponse   "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse   "Not found (also returned to non-admins)"
// @Router       /orgs/{org_id}/access-matrix [get]
func (h *Handler) AccessMatrix(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	data, err := h.bulk.Matrix(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to load access matrix")
		return
	}
	out := matrixResponse{
		Teams:  make([]matrixTeamResponse, 0, len(data.Teams)),
		Spaces: make([]matrixSpaceResponse, 0, len(data.Spaces)),
		Grants: make([]matrixGrantResponse, 0, len(data.Grants)),
	}
	for _, t := range data.Teams {
		out.Teams = append(out.Teams, matrixTeamResponse{
			ID: t.ID, ParentID: t.ParentID, Path: t.Path, Name: t.Name,
			IsDefault: t.IsDefault, MemberCount: t.MemberCount,
		})
	}
	for _, s := range data.Spaces {
		out.Spaces = append(out.Spaces, matrixSpaceResponse{ID: s.ID, Name: s.Name, Type: s.Type, Visibility: s.Visibility})
	}
	for _, g := range data.Grants {
		out.Grants = append(out.Grants, matrixGrantResponse{ID: g.ID, TeamID: g.TeamID, SpaceID: g.SpaceID, Role: g.Role})
	}
	respond.JSON(w, http.StatusOK, out)
}

// bulkChangeRequest is one requested cell state. role null means revoke.
type bulkChangeRequest struct {
	TeamID  uuid.UUID `json:"team_id"`
	SpaceID uuid.UUID `json:"space_id"`
	Role    *string   `json:"role"`
}

// bulkPreviewRequest asks for the diff a set of changes would apply.
type bulkPreviewRequest struct {
	Changes []bulkChangeRequest `json:"changes"`
}

// bulkApplyRequest applies a set of changes atomically.
type bulkApplyRequest struct {
	Changes []bulkChangeRequest `json:"changes"`
	// TicketRef is the operator reference recorded on every audit event of the
	// batch. Free text, no foreign key. Optional by default; mandatory when
	// the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED.
	//
	// This is the one endpoint that carries the reference in the body rather
	// than the ticket_ref query parameter — a shipped contract with clients
	// already sending it. It shares the cap and the requirement rule with the
	// query-parameter transport via ticketref.Policy.
	TicketRef string `json:"ticket_ref"`
}

// bulkActionResponse is one itemised diff line.
type bulkActionResponse struct {
	TeamID   uuid.UUID `json:"team_id"`
	SpaceID  uuid.UUID `json:"space_id"`
	Action   string    `json:"action"`
	FromRole string    `json:"from_role,omitempty"`
	ToRole   string    `json:"to_role,omitempty"`
}

// bulkResultResponse reports a diff (preview) or an applied batch (apply).
type bulkResultResponse struct {
	BatchID *uuid.UUID `json:"batch_id,omitempty"`
	// TicketRef echoes the operator's free-text reference recorded on every
	// audit event of this batch. Present on apply, absent on preview.
	TicketRef string               `json:"ticket_ref,omitempty"`
	Creates   int                  `json:"creates"`
	Updates   int                  `json:"updates"`
	Revokes   int                  `json:"revokes"`
	Noops     int                  `json:"noops"`
	Actions   []bulkActionResponse `json:"actions"`
}

func toBulkResultResponse(res access.BulkResult, includeBatch bool) bulkResultResponse {
	out := bulkResultResponse{
		Creates: res.Creates, Updates: res.Updates, Revokes: res.Revokes, Noops: res.Noops,
		Actions: make([]bulkActionResponse, 0, len(res.Actions)),
	}
	if includeBatch {
		id := res.BatchID
		out.BatchID = &id
	}
	for _, a := range res.Actions {
		out.Actions = append(out.Actions, bulkActionResponse{
			TeamID: a.TeamID, SpaceID: a.SpaceID, Action: a.Action,
			FromRole: a.FromRole, ToRole: a.ToRole,
		})
	}
	return out
}

// decodeBulkChanges validates and converts the wire changes.
func decodeBulkChanges(w http.ResponseWriter, r *http.Request, raw []bulkChangeRequest) ([]access.BulkChange, bool) {
	changes := make([]access.BulkChange, 0, len(raw))
	for _, c := range raw {
		if c.TeamID == uuid.Nil || c.SpaceID == uuid.Nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "each change needs team_id and space_id")
			return nil, false
		}
		change := access.BulkChange{TeamID: c.TeamID, SpaceID: c.SpaceID}
		if c.Role != nil {
			role, err := access.ParseRole(*c.Role)
			if err != nil {
				respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "role must be viewer, contributor, agent, or space_admin, or null to revoke")
				return nil, false
			}
			change.Role = &role
		}
		changes = append(changes, change)
	}
	return changes, true
}

// mapBulkError translates bulk domain errors.
func mapBulkError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, access.ErrBulkEmpty):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "changes must not be empty")
	case errors.Is(err, access.ErrBulkTooLarge):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "too many changes in one batch")
	case errors.Is(err, access.ErrBulkDuplicateCell):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "two changes target the same team and space")
	case errors.Is(err, access.ErrBulkUnknownTeam):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a change references a team that is not in this organization")
	case errors.Is(err, access.ErrBulkUnknownSpace):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a change references a space that is not in this organization")
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "bulk operation failed")
	}
}

// BulkPreview computes the diff a bulk change would apply, without applying.
//
// @Summary      Preview bulk grant changes (admin)
// @Description  Computes the itemised diff (creates, role changes, revocations) the given changes would apply, using the same computation apply uses. Nothing is written. Org admins only.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  path      string                    true  "Organization ID"
// @Param        body    body      admin.bulkPreviewRequest  true  "Requested cell states"
// @Success      200     {object}  admin.bulkResultResponse  "The diff"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Validation error — the whole batch is rejected"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found (also returned to non-admins)"
// @Router       /orgs/{org_id}/grants/bulk-preview [post]
func (h *Handler) BulkPreview(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	var req bulkPreviewRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	changes, ok := decodeBulkChanges(w, r, req.Changes)
	if !ok {
		return
	}
	res, err := h.bulk.Preview(r.Context(), orgID, changes)
	if err != nil {
		mapBulkError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, toBulkResultResponse(res, false))
}

// BulkApply applies a bulk grant change as one transaction with one batch_id.
//
// @Summary      Apply bulk grant changes (admin)
// @Description  Applies the changes atomically: one transaction, one batch_id, one audit batch. A failure anywhere rolls back everything. Org admins only.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  path      string                  true  "Organization ID"
// @Param        body    body      admin.bulkApplyRequest  true  "Changes and the ticket_ref body field (max 200 chars; may be mandatory — see AZIMUTHAL_TICKET_REF_REQUIRED). Unlike the other administrative mutations this endpoint takes the reference in the body, not the query string — a shipped contract."
// @Success      200     {object}  admin.bulkResultResponse  "Applied diff with batch_id"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Validation error, including a missing or over-long ticket_ref — nothing applied"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found (also returned to non-admins)"
// @Router       /orgs/{org_id}/grants/bulk-apply [post]
func (h *Handler) BulkApply(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	var req bulkApplyRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	// Same policy as the query-parameter transport, deliberately: this
	// endpoint keeps its shipped body field, but the cap and the required-mode
	// rule live in one place so the two transports cannot drift. Trimmed
	// first, because ticketref.FromRequest trims — otherwise a body of "   "
	// would satisfy a requirement the query string rejects.
	req.TicketRef = strings.TrimSpace(req.TicketRef)
	if !h.ticketRef.Check(w, r, req.TicketRef) {
		return
	}
	changes, ok := decodeBulkChanges(w, r, req.Changes)
	if !ok {
		return
	}
	res, err := h.bulk.Apply(r.Context(), orgID, claims.UserID, changes, req.TicketRef)
	if err != nil {
		mapBulkError(w, r, err)
		return
	}
	// Echo the operator's ticket_ref so the confirmation surface can show it
	// was recorded (the adapter writes it onto every audit event of the batch).
	out := toBulkResultResponse(res, true)
	out.TicketRef = req.TicketRef
	respond.JSON(w, http.StatusOK, out)
}
