// Phase 1 (P1.2) integration coverage for the audit Recorder. These tests
// run against the real test database and exercise the dbLogger by counting
// rows in the audit_log table per category.
package audit_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// countAuditByAction returns the number of audit_log rows in the test schema
// matching the given action string for the given org.
func countAuditByAction(t *testing.T, db *testutil.TestDB, orgID uuid.UUID, action string) int {
	t.Helper()
	var n int
	err := db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE org_id = $1 AND action = $2`,
		orgID, action,
	).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestDBLogger_PersistsAuthEvent confirms that an auth-category event lands
// in audit_log via the DB logger.
func TestDBLogger_PersistsAuthEvent(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	logger := audit.NewDBLogger(generated.New(db.Pool))
	require.True(t, logger.IsAvailable(), "DB logger should report available")

	require.NoError(t, logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeUserLogin,
		ActorID:      user.ID.String(),
		OrgID:        org.ID.String(),
		ResourceType: "user",
		ResourceID:   user.ID.String(),
	}))

	require.Equal(t, 1, countAuditByAction(t, db, org.ID, string(audit.EventTypeUserLogin)))
}

// TestDBLogger_PersistsTicketEvents covers ticket create/update/status/assign/delete.
func TestDBLogger_PersistsTicketEvents(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	logger := audit.NewDBLogger(generated.New(db.Pool))

	ticketID := uuid.New().String()
	cases := []audit.EventType{
		audit.EventTypeTicketCreated,
		audit.EventTypeTicketUpdated,
		audit.EventTypeTicketStatus,
		audit.EventTypeTicketAssigned,
		audit.EventTypeTicketUnassign,
		audit.EventTypeTicketDeleted,
	}
	for _, etype := range cases {
		require.NoError(t, logger.Log(context.Background(), audit.Event{
			Type:         etype,
			ActorID:      user.ID.String(),
			OrgID:        org.ID.String(),
			ResourceType: "ticket",
			ResourceID:   ticketID,
		}))
	}
	for _, etype := range cases {
		require.Equal(t, 1, countAuditByAction(t, db, org.ID, string(etype)),
			"expected 1 row for action %s", etype)
	}
}

// TestDBLogger_PersistsWikiEvents covers wiki page create/update/move/delete.
func TestDBLogger_PersistsWikiEvents(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	logger := audit.NewDBLogger(generated.New(db.Pool))

	pageID := uuid.New().String()
	cases := []audit.EventType{
		audit.EventTypePageCreated,
		audit.EventTypePageUpdated,
		audit.EventTypePageMoved,
		audit.EventTypePageDeleted,
	}
	for _, etype := range cases {
		require.NoError(t, logger.Log(context.Background(), audit.Event{
			Type:         etype,
			ActorID:      user.ID.String(),
			OrgID:        org.ID.String(),
			ResourceType: "page",
			ResourceID:   pageID,
		}))
	}
	for _, etype := range cases {
		require.Equal(t, 1, countAuditByAction(t, db, org.ID, string(etype)),
			"expected 1 row for action %s", etype)
	}
}

// TestDBLogger_PersistsProjectAndSprintAndComment covers the remaining categories
// in P1.2 — project items, sprint lifecycle, and comment creation.
func TestDBLogger_PersistsProjectAndSprintAndComment(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	logger := audit.NewDBLogger(generated.New(db.Pool))

	rid := uuid.New().String()
	cases := []struct {
		etype    audit.EventType
		resource string
	}{
		{audit.EventTypeProjectItemCreated, "project_item"},
		{audit.EventTypeProjectItemUpdated, "project_item"},
		{audit.EventTypeProjectItemStatus, "project_item"},
		{audit.EventTypeProjectItemSprintMoved, "project_item"},
		{audit.EventTypeProjectItemDeleted, "project_item"},
		{audit.EventTypeSprintCreated, "sprint"},
		{audit.EventTypeSprintStarted, "sprint"},
		{audit.EventTypeSprintCompleted, "sprint"},
		{audit.EventTypeCommentCreated, "comment"},
	}
	for _, c := range cases {
		require.NoError(t, logger.Log(context.Background(), audit.Event{
			Type:         c.etype,
			ActorID:      user.ID.String(),
			OrgID:        org.ID.String(),
			ResourceType: c.resource,
			ResourceID:   rid,
		}))
	}
	for _, c := range cases {
		require.Equal(t, 1, countAuditByAction(t, db, org.ID, string(c.etype)),
			"expected 1 row for action %s", c.etype)
	}
}

// TestDBLogger_DropsEventWithoutOrgID confirms the audit_log NOT NULL
// constraint on org_id is honoured by the logger swallowing the event
// rather than returning an error to the caller. Pre-auth events (e.g.
// failed login) hit this path.
func TestDBLogger_DropsEventWithoutOrgID(t *testing.T) {
	db := testutil.NewTestDB(t)
	logger := audit.NewDBLogger(generated.New(db.Pool))
	// No OrgID — must not return an error and must not insert a row.
	require.NoError(t, logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeUserLoginFailed,
		ResourceType: "user",
	}))
	var n int
	err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log`).Scan(&n)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}
