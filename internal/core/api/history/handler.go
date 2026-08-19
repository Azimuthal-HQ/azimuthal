// Package history provides the per-entity audit History HTTP surface (D5): a
// space reader's view of what happened to one ticket or project item — status
// changes, assignment, field edits, creation — as a filtered, read-only slice of
// the append-only audit log.
//
// It is deliberately NOT the org-admin audit viewer. That surface
// (internal/core/api/admin, behind RequireOrgAdmin404) shows the raw log across
// the whole org, including membership, grant, role and security events. This one
// is space-read-guarded and serves only the entity-lifecycle vocabulary a
// contributor may see (see displayedActions), so an org-admin-only event that
// happens to carry the entity's id can never reach a space member through here.
//
// History is a SIBLING of Activity (comments), not a replacement — the JSM model
// keeps them separate. Comments carry entity_kind "comment" and so never appear
// in an entity's ticket/item history; the two surfaces do not interleave.
package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// displayedActions is the closed vocabulary the History surface serves: the
// entity-lifecycle events a space member is entitled to see, and nothing else.
//
// It is an ALLOW-LIST, not a deny-list, and the filter runs server-side in the
// query — the client is not trusted to omit what it must not see. The raw audit
// log carries org-admin events (grants, roles, invites, security) that share no
// entity with a ticket or item under normal operation, but a route that trusted
// the client, or filtered by exclusion, would leak one the moment an event was
// written against this entity's id with an unexpected action. Adding a new
// entity-history event type means adding it here on purpose.
//
// The two entity families are unioned deliberately: a ticket carries only
// ticket.* events and an item only item.*, so the union filters each correctly
// while keeping one list to reason about. Project items have no assign/unassign
// audit event of their own — assignment surfaces as item.updated — so those two
// entries are ticket-only in practice.
var displayedActions = []string{
	string(audit.EventTypeTicketCreated),
	string(audit.EventTypeTicketUpdated),
	string(audit.EventTypeTicketStatusChange),
	string(audit.EventTypeTicketAssigned),
	string(audit.EventTypeTicketUnassigned),
	string(audit.EventTypeItemCreated),
	string(audit.EventTypeItemUpdated),
	string(audit.EventTypeItemStatusChange),
}

// Handler holds the dependencies for the History HTTP surface. queries is
// required and set in the constructor — there are no optional With* builders, so
// the handler has no way to go dark on a mounted route.
type Handler struct {
	queries *generated.Queries
}

// NewHandler creates a History Handler.
func NewHandler(queries *generated.Queries) *Handler {
	return &Handler{queries: queries}
}

// historyEvent is one rendered audit event: who, what changed, when. The payload
// is passed through as a flat map — status changes carry {"from","to"}; other
// events carry whatever their writer recorded — so the client renders old -> new
// without a bespoke shape per event type.
type historyEvent struct {
	ID        uuid.UUID         `json:"id"`
	ActorID   *uuid.UUID        `json:"actor_id"`
	ActorName string            `json:"actor_name"`
	Action    string            `json:"action"`
	Payload   map[string]string `json:"payload"`
	CreatedAt string            `json:"created_at"`
}

// History routes are registered per resource subtree (tickets, project items)
// so the URL hangs off the resource's own path, exactly as comments and
// relations are. Each wrapper fixes the AUDIT entity kind and the URL parameter
// carrying the entity id.

// ListTicketHistory lists the audit history of one ticket.
//
// @Summary      List ticket history
// @Description  Returns the entity-lifecycle audit events (status changes, assignment, field edits, creation) for a ticket, newest first.
// @Tags         history
// @Produce      json
// @Security     BearerAuth
// @Param        orgID     path      string  true  "Organization ID (UUID)"
// @Param        spaceID   path      string  true  "Space ID (UUID)"
// @Param        ticketID  path      string  true  "Ticket ID (UUID)"
// @Success      200  {array}   api.SwaggerHistoryResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/tickets/{ticketID}/history [get]
func (h *Handler) ListTicketHistory(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "ticket", "ticketID")
}

// ListItemHistory lists the audit history of one project item.
//
// The audit entity kind for a project item is "item" — NOT the "project_item"
// the comments and scoping code use — because that is what every item write site
// records. The wrapper passes the audit kind; entityInSpace maps it back to the
// project_items table for the space reconciliation.
//
// @Summary      List item history
// @Description  Returns the entity-lifecycle audit events (status changes, field edits, creation) for a project item, newest first.
// @Tags         history
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        itemID   path      string  true  "Item ID (UUID)"
// @Success      200  {array}   api.SwaggerHistoryResponse
// @Failure      400  {object}  api.SwaggerErrorResponse
// @Failure      401  {object}  api.SwaggerErrorResponse
// @Failure      500  {object}  api.SwaggerErrorResponse
// @Router       /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}/history [get]
func (h *Handler) ListItemHistory(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "item", "itemID")
}

// list returns the filtered audit history of the entity whose id is carried by
// idParam, reconciled against the space the URL names.
//
// THE SPACE IS RECONCILED FIRST. The middleware proved {spaceID} readable for
// this caller and proved nothing whatever about {entityID}: the audit log has no
// space column of its own, so a bare entity id would otherwise return the
// history of a ticket or item in any other space or org. An entity outside the
// space matches nothing here and answers the same empty list an entity that
// never existed produces — unreadable is nonexistent, never an existence oracle.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, auditKind, idParam string) {
	entityID, err := uuid.Parse(chi.URLParam(r, idParam))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid entity ID")
		return
	}
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	inSpace, err := h.entityInSpace(r.Context(), auditKind, entityID, spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list history")
		return
	}
	if !inSpace {
		respond.JSON(w, http.StatusOK, []historyEvent{})
		return
	}

	rows, err := h.queries.ListEntityHistory(r.Context(), generated.ListEntityHistoryParams{
		EntityKind: auditKind,
		EntityID:   entityID,
		Actions:    displayedActions,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list history")
		return
	}

	result := make([]historyEvent, 0, len(rows))
	for _, row := range rows {
		result = append(result, rowToHistoryEvent(row))
	}
	respond.JSON(w, http.StatusOK, result)
}

// entityInSpace reports whether the entity the history hangs off lives in the
// space the URL named.
//
// A miss is reported as absent, never as forbidden — the caller answers its
// ordinary empty list, because a distinguishable "this exists but is not yours"
// discloses the same fact in a different shape. This mirrors the reconciliation
// comments.Handler.entityInSpace performs; the audit kind ("item") is mapped
// back to the project_items table here.
func (h *Handler) entityInSpace(ctx context.Context, auditKind string, entityID, spaceID uuid.UUID) (bool, error) {
	var err error
	switch auditKind {
	case "ticket":
		_, err = h.queries.GetTicketInSpace(ctx, generated.GetTicketInSpaceParams{TicketID: entityID, SpaceID: spaceID})
	case "item":
		_, err = h.queries.GetProjectItemInSpace(ctx, generated.GetProjectItemInSpaceParams{ItemID: entityID, SpaceID: spaceID})
	default:
		// Unreachable through the router: auditKind is a literal fixed by the
		// wrapper the route is bound to. A new wrapper that forgot to add its arm
		// here would be reconciled against nothing, so this fails closed.
		return false, fmt.Errorf("history: unknown entity kind %q", auditKind)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reconcile %s %s against space %s: %w", auditKind, entityID, spaceID, err)
	}
	return true, nil
}

func rowToHistoryEvent(row generated.ListEntityHistoryRow) historyEvent {
	payload := map[string]string{}
	if len(row.Payload) > 0 {
		// A malformed payload degrades to an empty map rather than failing the
		// whole list — the audit row is still worth showing for its actor and
		// action even if its metadata cannot be parsed.
		_ = json.Unmarshal(row.Payload, &payload)
	}
	return historyEvent{
		ID:        row.ID,
		ActorID:   goUUIDPtr(row.ActorID),
		ActorName: row.ActorName,
		Action:    row.Action,
		Payload:   payload,
		CreatedAt: row.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
}

// goUUIDPtr converts a nullable pgtype.UUID into a *uuid.UUID, nil when unset —
// an audit actor is null for the rare event written with no actor.
func goUUIDPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}
