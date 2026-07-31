package tickets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
)

// RequesterLookup resolves external requester identities in bulk.
//
// It is an interface here, and satisfied by adapters.PortalAdapter, so the
// ticket API layer depends on the shape it needs rather than on the portal's
// storage. The bulk signature is not a convenience: List, Search and Kanban
// each serialise a whole page, and a per-ticket lookup would put an N+1 on
// every agent read path in Beacon.
type RequesterLookup interface {
	RequestersByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]portal.RequesterIdentity, error)
}

// requesterIdentity is the agent-facing shape of an external requester.
//
// snake_case, like every other wire type here (CLAUDE.md §1).
type requesterIdentity struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
}

// ticketView is a ticket as the AGENT surface sees it: every field of the
// domain ticket, plus the resolved external requester.
//
// The embedded pointer promotes tickets.Ticket's fields, so this stays
// byte-identical to the previous response for every ticket raised inside the
// product — the only addition is `requester`, which is null for those.
//
// Resolution happens HERE, in the API layer, rather than on tickets.Ticket:
// ADR-0009's consequence ("the view layer fans out per module and merges in
// the API layer") is the governing pattern, and putting an identity from the
// portal's tables onto the core ticket struct would make every repository
// method that returns a Ticket responsible for a join it does not need.
type ticketView struct {
	*tickets.Ticket
	Requester *requesterIdentity `json:"requester"`
}

// kanbanColumnView mirrors tickets.KanbanColumn with resolved tickets.
type kanbanColumnView struct {
	Status  tickets.Status `json:"status"`
	Tickets []ticketView   `json:"tickets"`
}

// distinctRequesterIDs collects the external identities a page of tickets
// names, once each.
//
// The de-duplication is what makes the lookup one round trip rather than one
// per portal ticket: a busy queue is frequently several requests from the same
// customer, and asking for the same id four times is an N+1 wearing a bulk
// signature. TestTicketRequester_ListResolvesWithoutNPlusOne asserts the batch
// this returns, not just the number of calls.
func distinctRequesterIDs(ts []*tickets.Ticket) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(ts))
	seen := make(map[uuid.UUID]struct{}, len(ts))
	for _, t := range ts {
		if t == nil || t.RequesterID == nil {
			continue
		}
		if _, dup := seen[*t.RequesterID]; dup {
			continue
		}
		seen[*t.RequesterID] = struct{}{}
		ids = append(ids, *t.RequesterID)
	}
	return ids
}

// resolveRequesters turns domain tickets into agent views, resolving the
// external identity of every portal-raised one in a single round trip.
//
// A ticket whose requester row has vanished serialises with a null requester
// rather than failing the read: the ticket still exists and the agent still
// needs it. A lookup that ERRORS is a different thing and is returned, because
// answering "no requester" when the truth is "we could not tell" is precisely
// the silent degradation that leaves this surface reading "Unknown".
func (h *Handler) resolveRequesters(ctx context.Context, ts []*tickets.Ticket) ([]ticketView, error) {
	views := make([]ticketView, 0, len(ts))
	ids := distinctRequesterIDs(ts)

	// No portal-raised ticket on this page: no lookup, no dependency on the
	// portal being wired at all. This is the common case in a space that does
	// not run a service desk.
	var found map[uuid.UUID]portal.RequesterIdentity
	if len(ids) > 0 {
		if h.requesters == nil {
			// Reached only if the handler was built without the lookup while a
			// portal ticket exists. TestHarness_NoDarkDependencies fails by
			// field name before this can ship; erroring rather than returning
			// a null requester keeps it from degrading quietly if it ever does.
			return nil, fmt.Errorf("ticket requester lookup is not wired")
		}
		var err error
		found, err = h.requesters.RequestersByIDs(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("resolve ticket requesters: %w", err)
		}
	}

	for _, t := range ts {
		v := ticketView{Ticket: t}
		if t != nil && t.RequesterID != nil {
			if id, ok := found[*t.RequesterID]; ok {
				v.Requester = &requesterIdentity{
					ID:          id.ID,
					DisplayName: id.DisplayName,
					Email:       id.Email,
				}
			}
		}
		views = append(views, v)
	}
	return views, nil
}

// resolveRequester is the single-ticket form.
func (h *Handler) resolveRequester(ctx context.Context, t *tickets.Ticket) (ticketView, error) {
	views, err := h.resolveRequesters(ctx, []*tickets.Ticket{t})
	if err != nil {
		return ticketView{}, err
	}
	return views[0], nil
}

// respondTicket writes one ticket with its external requester resolved.
//
// Every agent-side ticket response goes through this or respondTickets, so
// `requester` is present on all of them. A read path that serialised a bare
// tickets.Ticket would answer without the field entirely, and a client cannot
// tell "no requester" from "this endpoint forgot".
func (h *Handler) respondTicket(w http.ResponseWriter, r *http.Request, status int, t *tickets.Ticket) {
	v, err := h.resolveRequester(r.Context(), t)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to resolve ticket requester")
		return
	}
	respond.JSON(w, status, v)
}

// respondTickets writes a page of tickets, resolving every external requester
// on it in one round trip.
func (h *Handler) respondTickets(w http.ResponseWriter, r *http.Request, status int, ts []*tickets.Ticket) {
	views, err := h.resolveRequesters(r.Context(), ts)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to resolve ticket requesters")
		return
	}
	respond.JSON(w, status, views)
}

// respondKanban writes the board, resolving every requester across all four
// columns in ONE round trip rather than one per column.
func (h *Handler) respondKanban(w http.ResponseWriter, r *http.Request, board []tickets.KanbanColumn) {
	flat := make([]*tickets.Ticket, 0)
	for _, col := range board {
		flat = append(flat, col.Tickets...)
	}
	views, err := h.resolveRequesters(r.Context(), flat)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to resolve ticket requesters")
		return
	}

	out := make([]kanbanColumnView, 0, len(board))
	at := 0
	for _, col := range board {
		n := len(col.Tickets)
		out = append(out, kanbanColumnView{
			Status:  col.Status,
			Tickets: views[at : at+n],
		})
		at += n
	}
	respond.JSON(w, http.StatusOK, out)
}
