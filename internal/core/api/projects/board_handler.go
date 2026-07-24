package projects

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// --- Request types ---

type boardColumnRequest struct {
	// ID is optional: omit it for a new column, supply it to keep an existing
	// column's identity across a save.
	ID       *uuid.UUID `json:"id"`
	Name     string     `json:"name"`
	WIPLimit *int       `json:"wip_limit"`
	Statuses []string   `json:"statuses"`
}

type saveBoardConfigRequest struct {
	// Columns in the order they appear on the board; position is the index.
	Columns []boardColumnRequest `json:"columns"`
}

type deleteBoardColumnRequest struct {
	// RemapTo names the column that adopts this one's statuses. Required —
	// every status must stay mapped somewhere.
	RemapTo uuid.UUID `json:"remap_to"`
}

// GetBoardConfig returns the space's board configuration.
//
// @Summary      Get board configuration
// @Description  Returns the space's board columns, their status mappings and WIP limits. A space that has never been customised gets a configuration derived from its workflow states, flagged with customized=false.
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {object}  api.SwaggerBoardConfig
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      403      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/board/config [get]
func (h *Handler) GetBoardConfig(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.boardSpace(w, r)
	if !ok {
		return
	}
	// Viewing the board's shape follows ordinary read access.
	if !access.Can(r.Context(), access.CapReadItems, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	cfg, err := h.boardConfig.GetConfig(r.Context(), spaceID)
	if err != nil {
		handleProjectError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}

// SaveBoardConfig replaces the space's board configuration.
//
// @Summary      Save board configuration
// @Description  Replaces the space's board columns. Column order is the array order. Every status in the space's vocabulary must be mapped to exactly one column; a configuration that would orphan a status is rejected with 400.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                  true  "Space ID (UUID)"
// @Param        body     body      saveBoardConfigRequest  true  "Board columns"
// @Success      200      {object}  api.SwaggerBoardConfig
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      403      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/board/config [put]
func (h *Handler) SaveBoardConfig(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.boardSpace(w, r)
	if !ok {
		return
	}
	if !h.canManageBoard(w, r, spaceID) {
		return
	}

	var req saveBoardConfigRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	columns := make([]projects.BoardColumn, 0, len(req.Columns))
	for i, c := range req.Columns {
		col := projects.BoardColumn{
			SpaceID:  spaceID,
			Name:     c.Name,
			Position: i,
			WIPLimit: c.WIPLimit,
			Statuses: c.Statuses,
		}
		if c.ID != nil {
			col.ID = *c.ID
		}
		columns = append(columns, col)
	}

	cfg, err := h.boardConfig.SaveConfig(r.Context(), spaceID, columns)
	if err != nil {
		h.respondBoardError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}

// ResetBoardConfig drops the stored configuration, returning the space to the
// derived default.
//
// @Summary      Reset board configuration
// @Description  Removes the space's stored board configuration so the board falls back to the default derived from its workflow states.
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {object}  api.SwaggerBoardConfig
// @Failure      400      {object}  api.SwaggerErrorResponse
// @Failure      401      {object}  api.SwaggerErrorResponse
// @Failure      403      {object}  api.SwaggerErrorResponse
// @Failure      500      {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/board/config/reset [post]
func (h *Handler) ResetBoardConfig(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.boardSpace(w, r)
	if !ok {
		return
	}
	if !h.canManageBoard(w, r, spaceID) {
		return
	}

	cfg, err := h.boardConfig.ResetConfig(r.Context(), spaceID)
	if err != nil {
		h.respondBoardError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}

// DeleteBoardColumn removes a column, re-homing its statuses.
//
// @Summary      Delete a board column
// @Description  Removes a board column after moving every status it owns onto the column named by remap_to. There is no variant that drops the statuses — every status must remain mapped.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string                    true  "Organization ID (UUID)"
// @Param        spaceID   path      string                    true  "Space ID (UUID)"
// @Param        columnID  path      string                    true  "Column ID (UUID)"
// @Param        body      body      deleteBoardColumnRequest  true  "Re-mapping target"
// @Success      200       {object}  api.SwaggerBoardConfig
// @Failure      400       {object}  api.SwaggerErrorResponse
// @Failure      401       {object}  api.SwaggerErrorResponse
// @Failure      403       {object}  api.SwaggerErrorResponse
// @Failure      404       {object}  api.SwaggerErrorResponse
// @Failure      500       {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/board/config/columns/{columnID} [delete]
func (h *Handler) DeleteBoardColumn(w http.ResponseWriter, r *http.Request) {
	spaceID, ok := h.boardSpace(w, r)
	if !ok {
		return
	}
	if !h.canManageBoard(w, r, spaceID) {
		return
	}

	columnID, err := uuid.Parse(chi.URLParam(r, "columnID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid column ID")
		return
	}

	var req deleteBoardColumnRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	if req.RemapTo == uuid.Nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"remap_to is required: a removed column's statuses must be re-homed")
		return
	}

	cfg, err := h.boardConfig.DeleteColumn(r.Context(), spaceID, columnID, req.RemapTo)
	if err != nil {
		h.respondBoardError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}

// boardSpace resolves the space id and confirms the board-config service is
// wired, answering the client itself when either fails.
func (h *Handler) boardSpace(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, false
	}
	if h.boardConfig == nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "board configuration is not enabled")
		return uuid.Nil, false
	}
	return spaceID, true
}

// canManageBoard gates board writes on space admin, reusing the existing
// space-management capability rather than inventing a board-specific one.
func (h *Handler) canManageBoard(w http.ResponseWriter, r *http.Request, spaceID uuid.UUID) bool {
	if !access.Can(r.Context(), access.CapManageSpace, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return false
	}
	return true
}

// respondBoardError maps board validation failures to 400 and leaves
// everything else to the shared project error mapping.
func (h *Handler) respondBoardError(w http.ResponseWriter, r *http.Request, err error) {
	if projects.ErrIsBoardValidation(err) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	}
	handleProjectError(w, r, err)
}
