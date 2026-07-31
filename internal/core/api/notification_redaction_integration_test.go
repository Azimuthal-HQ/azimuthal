package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The notification bell rendered a STORED entity title with no live read gate.
//
// migration 030 denormalised entity_space_id onto the row and argued in its own
// header that this "creates no permission oracle", because clicking navigates
// to the space-scoped detail page which enforces authz. That reasoning is sound
// about the LINK and was never true of the TITLE: the title is captured at
// enqueue time, when the actor could see the entity, and was rendered from the
// row unconditionally afterwards. Grep the tree before this change and
// entity_space_id appears in an INSERT and in a serializer — never in a WHERE
// clause and never in a comparison.
//
// So a grant revoked after delivery did not retract what had already been said.
//
// Persona note: the recipient is an org MEMBER holding a grant on one space
// only. testutil.CreateTestUser makes an org OWNER, whose readable set is every
// space in the org — this whole file would pass with the gate deleted if it ran
// as that user, because nothing would ever be unreadable.
type notifFixture struct {
	ts       *testServer
	readable testutil.Space
	hidden   testutil.Space
	token    string
	userID   uuid.UUID
}

func newNotifFixture(t *testing.T) *notifFixture {
	t.Helper()
	ts := newTestServer(t)
	ctx := context.Background()

	f := &notifFixture{ts: ts}
	f.readable = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")
	f.hidden = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "beacon")

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(ctx, ts.OrgID, f.readable.ID,
		access.SubjectUser, member.ID, access.RoleContributor, ts.UserID)
	require.NoError(t, err)

	f.userID = member.ID
	f.token = ts.tokenFor(t, member.ID, member.Email)
	return f
}

// enqueue writes a notification directly, standing in for the job the enqueuer
// runs. spaceID may be uuid.Nil to write the legacy shape with no space.
func (f *notifFixture) enqueue(t *testing.T, title string, spaceID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var space any
	if spaceID != uuid.Nil {
		space = spaceID
	}
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO notifications (id, user_id, kind, title, entity_kind, entity_id, entity_space_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, f.userID, "ticket.assigned", title, "ticket", uuid.New(), space)
	require.NoError(t, err)
	return id
}

type notifRow struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	EntityID      string `json:"entity_id"`
	EntitySpaceID string `json:"entity_space_id"`
	EntityKind    string `json:"entity_kind"`
	Redacted      bool   `json:"redacted"`
}

func (f *notifFixture) list(t *testing.T) map[string]notifRow {
	t.Helper()
	r := f.ts.getAs(t, f.token, "/api/v1/notifications")
	require.Equal(t, http.StatusOK, r.StatusCode, "list: %s", r.Body)

	var resp struct {
		Notifications []notifRow `json:"notifications"`
		UnreadCount   int64      `json:"unread_count"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resp))

	byID := make(map[string]notifRow, len(resp.Notifications))
	for _, n := range resp.Notifications {
		byID[n.ID] = n
	}
	return byID
}

const (
	visibleTitle = "Assigned: the quarterly pricing review"
	hiddenTitle  = "Assigned: azimuthal_notification_leak_marker"
	legacyTitle  = "Assigned: a notification from before migration 030"
)

// The central case, asserted in BOTH directions.
//
// Refusing everything would satisfy the redaction half on its own, so the same
// request must still render the notification whose space the caller CAN read.
func TestNotifications_RedactWhatTheViewerMayNoLongerRead(t *testing.T) {
	f := newNotifFixture(t)

	visibleID := f.enqueue(t, visibleTitle, f.readable.ID)
	hiddenID := f.enqueue(t, hiddenTitle, f.hidden.ID)

	rows := f.list(t)
	require.Len(t, rows, 2, "both rows must still be listed; redaction hides the subject, not the row")

	visible := rows[visibleID.String()]
	require.False(t, visible.Redacted, "a readable space must not be redacted")
	require.Equal(t, visibleTitle, visible.Title,
		"the positive direction: a grant the caller holds must still render its title")
	require.Equal(t, f.readable.ID.String(), visible.EntitySpaceID,
		"a readable row keeps the space the bell routes with")

	hidden := rows[hiddenID.String()]
	require.True(t, hidden.Redacted, "a space the caller holds no grant on must be redacted")
	require.NotContains(t, hidden.Title, "azimuthal_notification_leak_marker",
		"the stored title reached a viewer with no grant on its space")
	require.Empty(t, hidden.EntitySpaceID,
		"a redacted row must not name the space either — the container is identity too")
	require.Empty(t, hidden.EntityID, "a redacted row must not name the entity")
	require.Empty(t, hidden.EntityKind, "a redacted row must not name the entity's kind")
}

// Revocation is the case the entry was actually filed about: the title was
// captured when the actor COULD see the entity, so the only thing that can
// retract it is a gate consulted at read time.
//
// A cached or precomputed set would pass the test above and fail this one.
func TestNotifications_RevokingAGrantRetractsAnAlreadyDeliveredTitle(t *testing.T) {
	f := newNotifFixture(t)
	ctx := context.Background()

	id := f.enqueue(t, visibleTitle, f.readable.ID)

	before := f.list(t)[id.String()]
	require.False(t, before.Redacted)
	require.Equal(t, visibleTitle, before.Title, "precondition: the title is visible while the grant stands")

	// Revoke by removing the grant row directly: what matters is the state, not
	// the route that produced it.
	_, err := f.ts.DB.Pool.Exec(ctx,
		`DELETE FROM space_grants WHERE space_id = $1 AND subject_id = $2`,
		f.readable.ID, f.userID)
	require.NoError(t, err)

	after := f.list(t)[id.String()]
	require.True(t, after.Redacted,
		"a revoked grant must retract a title already delivered — the gate has to run per read")
	require.NotContains(t, after.Title, "pricing review")
}

// A row with no entity_space_id is NOT redacted.
//
// migration 030 added the column nullable and backfilled nothing, saying legacy
// rows "simply stay non-routable". Those rows, and any org-level notification
// that names no space, must keep rendering: blanking them would destroy the
// bell's history to close a gap they never had. This is the over-redaction
// guard, and it fails if the gate treats absent as unreadable.
func TestNotifications_ASpacelessRowIsNotRedacted(t *testing.T) {
	f := newNotifFixture(t)

	id := f.enqueue(t, legacyTitle, uuid.Nil)

	row := f.list(t)[id.String()]
	require.False(t, row.Redacted, "a notification that names no space has nothing to reconcile")
	require.Equal(t, legacyTitle, row.Title,
		"legacy and org-level notifications must survive the gate intact")
}

// The unread count still counts redacted rows.
//
// If redaction dropped rows instead of blanking them, the list and the count
// would disagree — and a count that exceeds what the list can show is itself
// the signal the redaction exists to remove.
func TestNotifications_RedactedRowsStillCountAsUnread(t *testing.T) {
	f := newNotifFixture(t)

	f.enqueue(t, visibleTitle, f.readable.ID)
	f.enqueue(t, hiddenTitle, f.hidden.ID)

	r := f.ts.getAs(t, f.token, "/api/v1/notifications")
	require.Equal(t, http.StatusOK, r.StatusCode)

	var resp struct {
		Notifications []notifRow `json:"notifications"`
		UnreadCount   int64      `json:"unread_count"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resp))

	require.Len(t, resp.Notifications, 2)
	require.Equal(t, int64(2), resp.UnreadCount,
		"the count must agree with the list, or the disagreement is the oracle")
}
