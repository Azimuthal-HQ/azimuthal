package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
)

// auditEntryResponse is one audit viewer row: a single event, or a batch
// collapsed to its representative row with batch_size > 1.
type auditEntryResponse struct {
	ID         uuid.UUID       `json:"id"`
	ActorID    *uuid.UUID      `json:"actor_id,omitempty"`
	ActorName  string          `json:"actor_name,omitempty"`
	Action     string          `json:"action"`
	EntityKind string          `json:"entity_kind"`
	EntityID   uuid.UUID       `json:"entity_id"`
	Payload    json.RawMessage `json:"payload"`
	BatchID    *uuid.UUID      `json:"batch_id,omitempty"`
	TicketRef  *string         `json:"ticket_ref,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	BatchSize  int             `json:"batch_size"`
}

// auditListResponse is one page plus the cursor for the next.
type auditListResponse struct {
	Entries []auditEntryResponse `json:"entries"`
	// NextCursor is "<rfc3339nano>,<uuid>" of the last row, empty when the
	// page was not full.
	NextCursor string `json:"next_cursor,omitempty"`
}

func toAuditEntryResponse(e audit.Entry) auditEntryResponse {
	payload := json.RawMessage(e.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return auditEntryResponse{
		ID:         e.ID,
		ActorID:    e.ActorID,
		ActorName:  e.ActorName,
		Action:     e.Action,
		EntityKind: e.EntityKind,
		EntityID:   e.EntityID,
		Payload:    payload,
		BatchID:    e.BatchID,
		TicketRef:  e.TicketRef,
		CreatedAt:  e.CreatedAt,
		BatchSize:  e.BatchSize,
	}
}

// parseAuditFilter builds the list filter from query parameters.
func parseAuditFilter(w http.ResponseWriter, r *http.Request) (audit.ListFilter, bool) {
	var f audit.ListFilter
	q := r.URL.Query()
	if v := q.Get("actor_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "invalid actor_id")
			return f, false
		}
		f.ActorID = &id
	}
	if v := q.Get("entity_kind"); v != "" {
		f.EntityKind = &v
	}
	if v := q.Get("action"); v != "" {
		f.Action = &v
	}
	if v := q.Get("limit"); v != "" {
		n, err := parsePositiveInt(v)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "limit must be a positive integer")
			return f, false
		}
		f.Limit = n
	}
	return parseAuditWindow(w, r, f)
}

// parseAuditWindow parses the date-range and cursor parameters.
func parseAuditWindow(w http.ResponseWriter, r *http.Request, f audit.ListFilter) (audit.ListFilter, bool) {
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "from must be RFC 3339")
			return f, false
		}
		f.CreatedFrom = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "to must be RFC 3339")
			return f, false
		}
		f.CreatedTo = &t
	}
	if v := q.Get("cursor"); v != "" {
		createdAt, id, err := parseCursor(v)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "invalid cursor")
			return f, false
		}
		f.CursorCreatedAt = &createdAt
		f.CursorID = &id
	}
	return f, true
}

// ListAuditLog returns one page of audit entries, batches collapsed.
//
// @Summary      Audit log (admin)
// @Description  Append-only audit events with filters for actor, entity kind, action, and date range. Events of one bulk batch collapse to a single row carrying batch_size; expand via the batches endpoint. Keyset cursor pagination. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        orgID       path      string  true   "Organization ID"
// @Param        actor_id     query     string  false  "Filter by actor"
// @Param        entity_kind  query     string  false  "Filter by entity kind"
// @Param        action       query     string  false  "Filter by action"
// @Param        from         query     string  false  "RFC 3339 lower bound"
// @Param        to           query     string  false  "RFC 3339 upper bound"
// @Param        cursor       query     string  false  "Keyset cursor from next_cursor"
// @Param        limit        query     int     false  "Page size (max 100, default 50)"
// @Success      200          {object}  admin.auditListResponse   "One page"
// @Failure      400          {object}  api.SwaggerErrorResponse  "Invalid filter"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not found (also returned to non-admins)"
// @Router       /orgs/{orgID}/audit-log [get]
func (h *Handler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	f, ok := parseAuditFilter(w, r)
	if !ok {
		return
	}
	requested := f.Limit
	if requested <= 0 || requested > 100 {
		requested = 50
	}
	entries, err := h.auditRd.List(r.Context(), orgID, f)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list audit log")
		return
	}
	out := auditListResponse{Entries: make([]auditEntryResponse, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, toAuditEntryResponse(e))
	}
	if len(entries) == requested {
		last := entries[len(entries)-1]
		out.NextCursor = formatCursor(last.CreatedAt, last.ID)
	}
	respond.JSON(w, http.StatusOK, out)
}

// AuditLogBatch expands one batch into its constituent events.
//
// @Summary      Audit log batch events (admin)
// @Description  The constituent events of one bulk batch, oldest first. Org admins only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID"
// @Param        batchID  path      string  true  "Batch ID"
// @Success      200       {array}   admin.auditEntryResponse  "Events"
// @Failure      404       {object}  api.SwaggerErrorResponse  "Not found (also returned to non-admins)"
// @Router       /orgs/{orgID}/audit-log/batches/{batchID} [get]
func (h *Handler) AuditLogBatch(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromRequest(w, r)
	if !ok {
		return
	}
	batchID, err := uuid.Parse(chi.URLParam(r, "batchID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid batch_id")
		return
	}
	entries, err := h.auditRd.BatchEvents(r.Context(), orgID, batchID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to load batch")
		return
	}
	if len(entries) == 0 {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "batch not found")
		return
	}
	out := make([]auditEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, toAuditEntryResponse(e))
	}
	respond.JSON(w, http.StatusOK, out)
}

// formatCursor encodes a keyset cursor.
func formatCursor(createdAt time.Time, id uuid.UUID) string {
	return createdAt.UTC().Format(time.RFC3339Nano) + "," + id.String()
}

// parseCursor decodes a keyset cursor.
func parseCursor(s string) (time.Time, uuid.UUID, error) {
	comma := strings.LastIndexByte(s, ',')
	if comma < 0 {
		return time.Time{}, uuid.Nil, errInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, s[:comma])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor timestamp: %w", err)
	}
	id, err := uuid.Parse(s[comma+1:])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor id: %w", err)
	}
	return t, id, nil
}

var errInvalidCursor = errors.New("invalid cursor")

// parsePositiveInt parses a positive base-10 integer.
func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parsing integer: %w", err)
	}
	if n <= 0 {
		return 0, errInvalidLimit
	}
	return n, nil
}

var errInvalidLimit = errors.New("must be positive")
