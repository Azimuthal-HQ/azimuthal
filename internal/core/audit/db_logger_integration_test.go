package audit_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The DB-backed audit logger against real PostgreSQL: every W7 event flows
// through Log, so its conversion paths (org/actor/entity parsing, payload
// marshalling) each need pinning. The Logger contract — failures never
// interrupt caller flow — means several of these assert "nil error AND no
// row", which only a real database can prove.

type auditFixture struct {
	db      *testutil.TestDB
	org     testutil.Org
	actor   testutil.User
	logger  audit.Logger
	queries *generated.Queries
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	actor := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)
	return &auditFixture{
		db: db, org: org, actor: actor,
		logger: audit.NewDBLogger(queries), queries: queries,
	}
}

func (f *auditFixture) rows(t *testing.T) []generated.AuditLog {
	t.Helper()
	rows, err := f.queries.ListAuditEventsByOrg(t.Context(), generated.ListAuditEventsByOrgParams{
		OrgID: f.org.ID, Limit: 100, Offset: 0,
	})
	require.NoError(t, err)
	return rows
}

func TestDBLogger_PersistsFullEvent(t *testing.T) {
	f := newAuditFixture(t)
	entityID := uuid.New()

	err := f.logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeTeamCreated,
		ActorID:      f.actor.ID.String(),
		OrgID:        f.org.ID.String(),
		ResourceType: "team",
		ResourceID:   entityID.String(),
		Metadata:     map[string]string{"slug": "audited"},
	})
	require.NoError(t, err)

	rows := f.rows(t)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, "team.created", row.Action)
	require.Equal(t, "team", row.EntityKind)
	require.Equal(t, entityID, row.EntityID)
	require.True(t, row.ActorID.Valid)
	require.Equal(t, f.actor.ID, uuid.UUID(row.ActorID.Bytes))

	var payload map[string]string
	require.NoError(t, json.Unmarshal(row.Payload, &payload))
	require.Equal(t, "audited", payload["slug"])
}

func TestDBLogger_InvalidOrgIDDroppedSilently(t *testing.T) {
	f := newAuditFixture(t)

	err := f.logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeTeamCreated,
		OrgID:        "not-a-uuid",
		ResourceType: "team",
		ResourceID:   uuid.NewString(),
	})
	require.NoError(t, err, "the Logger contract forbids surfacing errors")
	require.Empty(t, f.rows(t), "an event without a valid org must be dropped, not mis-attributed")
}

func TestDBLogger_AnonymousActorPersistsAsNull(t *testing.T) {
	f := newAuditFixture(t)

	err := f.logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeLoginFailed,
		ActorID:      "", // no authenticated actor (e.g. failed login)
		OrgID:        f.org.ID.String(),
		ResourceType: "user",
		ResourceID:   uuid.NewString(),
	})
	require.NoError(t, err)

	rows := f.rows(t)
	require.Len(t, rows, 1)
	require.False(t, rows[0].ActorID.Valid, "unknown actor must persist as NULL, not a zero uuid")
}

func TestDBLogger_UnparseableEntityIDPersistsAsNil(t *testing.T) {
	f := newAuditFixture(t)

	err := f.logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeSettingsChanged,
		ActorID:      f.actor.ID.String(),
		OrgID:        f.org.ID.String(),
		ResourceType: "settings",
		ResourceID:   "global", // non-uuid resource label
	})
	require.NoError(t, err)

	rows := f.rows(t)
	require.Len(t, rows, 1)
	require.Equal(t, uuid.Nil, rows[0].EntityID,
		"a non-uuid resource id degrades to the nil uuid — entity_id is NOT NULL by schema")
}

func TestDBLogger_NilMetadataPersistsEmptyObject(t *testing.T) {
	f := newAuditFixture(t)

	err := f.logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeGrantRevoked,
		ActorID:      f.actor.ID.String(),
		OrgID:        f.org.ID.String(),
		ResourceType: "grant",
		ResourceID:   uuid.NewString(),
	})
	require.NoError(t, err)

	rows := f.rows(t)
	require.Len(t, rows, 1)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(rows[0].Payload, &payload))
	require.Empty(t, payload)
}

func TestDBLogger_PersistFailureIsSwallowed(t *testing.T) {
	f := newAuditFixture(t)

	// A valid uuid that references no organization violates the FK — the
	// logger must swallow the failure per contract, and no row appears.
	err := f.logger.Log(context.Background(), audit.Event{
		Type:         audit.EventTypeTeamDeleted,
		OrgID:        uuid.NewString(),
		ResourceType: "team",
		ResourceID:   uuid.NewString(),
	})
	require.NoError(t, err, "persistence failures must never interrupt caller flow")
	require.Empty(t, f.rows(t))
}
