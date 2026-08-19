package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The per-entity History surface (D5), against the full production router and a
// real database. The maintainer's acceptance case is verbatim here: a ticket
// that was closed and then reopened shows BOTH actions. The surface is also a
// filter — the raw audit log is an org-admin thing, and an org-admin-only event
// carrying this entity's id must not reach a space reader through /history.

// seedAuditEvent inserts one audit_log row directly, so a test can place an
// event of any action on any entity — including an org-admin action the History
// filter must refuse to show.
func seedAuditEvent(t *testing.T, ts *testServer, action, entityKind string, entityID uuid.UUID) {
	t.Helper()
	_, err := ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO audit_log (id, org_id, actor_id, action, entity_kind, entity_id, payload)
		 VALUES ($1,$2,$3,$4,$5,$6,'{}')`,
		uuid.New(), ts.OrgID, ts.UserID, action, entityKind, entityID)
	require.NoError(t, err)
}

// findEvent returns the first history entry with the given action, or nil.
func findEvent(events []map[string]any, action string) map[string]any {
	for _, e := range events {
		if e["action"] == action {
			return e
		}
	}
	return nil
}

// TestTicketHistory_CloseAndReopen_ShowsBothActions is the maintainer's
// acceptance journey: close a ticket, reopen it, read the history, and see both
// the close and the reopen — each with its old -> new status.
func TestTicketHistory_CloseAndReopen_ShowsBothActions(t *testing.T) {
	ts := newTestServer(t)
	spaceBase, ticketID := createKanbanTicket(t, ts)
	statusURL := fmt.Sprintf("%s/tickets/%s/status", spaceBase, ticketID)

	// open -> in_progress -> closed (the close), then closed -> open (the reopen).
	for _, s := range []string{"in_progress", "closed", "open"} {
		r := ts.post(t, statusURL, map[string]any{"status": s}, true)
		require.Equal(t, http.StatusOK, r.StatusCode, "transition to %s: %s", s, r.Body)
	}

	r := ts.get(t, fmt.Sprintf("%s/tickets/%s/history", spaceBase, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "history: %s", r.Body)
	var events []map[string]any
	requireJSONList(t, r.Body, &events)

	// The close and the reopen are both status_changed events; find them by the
	// old -> new their payloads carry.
	var closed, reopened map[string]any
	for _, e := range events {
		if e["action"] != "ticket.status_changed" {
			continue
		}
		p, _ := e["payload"].(map[string]any)
		if p["to"] == "closed" {
			closed = e
		}
		if p["from"] == "closed" && p["to"] == "open" {
			reopened = e
		}
	}
	require.NotNil(t, closed, "the close must appear in history: %s", r.Body)
	require.NotNil(t, reopened, "the reopen must appear in history: %s", r.Body)

	// old -> new is legible on the close: in_progress -> closed.
	closedPayload, _ := closed["payload"].(map[string]any)
	require.Equal(t, "in_progress", closedPayload["from"], "close should record its old status")
	require.Equal(t, "closed", closedPayload["to"])

	// Actor is attributed.
	require.NotEmpty(t, reopened["actor_id"], "history entries name their actor")
}

// TestTicketHistory_FiltersOrgAdminEvents pins the allow-list: an org-admin-only
// event seeded against the ticket's own id is NOT disclosed to a space reader
// through /history, while the ticket's own lifecycle events are. The filter is
// server-side — this would fail if the handler returned the raw audit rows.
func TestTicketHistory_FiltersOrgAdminEvents(t *testing.T) {
	ts := newTestServer(t)
	spaceBase, ticketID := createKanbanTicket(t, ts)
	tid := uuid.MustParse(ticketID)

	// A genuine lifecycle event (allow-listed) ...
	r := ts.post(t, fmt.Sprintf("%s/tickets/%s/status", spaceBase, ticketID),
		map[string]any{"status": "in_progress"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "transition: %s", r.Body)

	// ... and an org-admin-only event carrying the SAME entity id, which must be
	// filtered out. user.org_role_changed is an admin-surface event; role changes
	// belong to the org-admin audit viewer, never a space member's history.
	seedAuditEvent(t, ts, "user.org_role_changed", "ticket", tid)
	// A grant change too, for good measure — the other classic admin event.
	seedAuditEvent(t, ts, "grant.created", "ticket", tid)

	r = ts.get(t, fmt.Sprintf("%s/tickets/%s/history", spaceBase, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "history: %s", r.Body)
	var events []map[string]any
	requireJSONList(t, r.Body, &events)

	require.NotNil(t, findEvent(events, "ticket.status_changed"),
		"the ticket's own status change must appear: %s", r.Body)
	require.Nil(t, findEvent(events, "user.org_role_changed"),
		"an org-admin event must NOT appear in a space reader's history: %s", r.Body)
	require.Nil(t, findEvent(events, "grant.created"),
		"an org-admin event must NOT appear in a space reader's history: %s", r.Body)
}

// TestItemHistory_StatusChangeAppears is the item-side twin: a project item's
// status change shows up on its /history route, entity_kind "item" and all.
func TestItemHistory_StatusChangeAppears(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "vector")
	spaceBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, space.ID)

	r := ts.post(t, spaceBase+"/projects/items", map[string]any{
		"title": "Item with history", "kind": "task", "priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	itemID := decodeJSONMap(t, r.Body)["id"].(string)

	r = ts.post(t, fmt.Sprintf("%s/projects/items/%s/status", spaceBase, itemID),
		map[string]any{"status": "in_progress"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "transition item: %s", r.Body)

	r = ts.get(t, fmt.Sprintf("%s/projects/items/%s/history", spaceBase, itemID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "item history: %s", r.Body)
	var events []map[string]any
	requireJSONList(t, r.Body, &events)
	require.NotNil(t, findEvent(events, "item.status_changed"),
		"the item's status change must appear on its history: %s", r.Body)
}
