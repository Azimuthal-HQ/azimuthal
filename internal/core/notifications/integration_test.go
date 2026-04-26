// Phase 1 (P1.3) integration coverage for the notifications service.
package notifications_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/notifications"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestService_CreateAndList writes a notification and then lists it back.
func TestService_CreateAndList(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	svc := notifications.NewService(generated.New(db.Pool))
	entity := uuid.New()

	created, err := svc.Create(context.Background(), notifications.CreateInput{
		UserID:     user.ID,
		Kind:       notifications.KindAssigned,
		Title:      "Assigned: a ticket",
		Body:       "by alice",
		EntityKind: notifications.EntityTicket,
		EntityID:   entity,
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, created.ID)
	require.False(t, created.IsRead)

	list, err := svc.List(context.Background(), user.ID, 50, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Assigned: a ticket", list[0].Title)
	require.Equal(t, notifications.KindAssigned, list[0].Kind)
	require.Equal(t, notifications.EntityTicket, list[0].EntityKind)
	require.Equal(t, entity, list[0].EntityID)

	// Unread count starts at 1.
	unread, err := svc.CountUnread(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), unread)
}

// TestService_AssignmentTrigger_PersistsRow simulates the producer pattern
// used by the API handlers: when an assignee is set, a notification is
// recorded for the new assignee. After MarkRead, unread_count returns to 0.
func TestService_AssignmentTrigger_PersistsRow(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	actor := testutil.CreateTestUser(t, db.Pool, org.ID)
	assignee := testutil.CreateTestUser(t, db.Pool, org.ID)

	svc := notifications.NewService(generated.New(db.Pool))
	entity := uuid.New()

	// Producer: assignee != actor, write notification.
	require.NotEqual(t, actor.ID, assignee.ID)
	_, err := svc.Create(context.Background(), notifications.CreateInput{
		UserID:     assignee.ID,
		Kind:       notifications.KindAssigned,
		Title:      "Assigned: bug fix",
		EntityKind: notifications.EntityItem,
		EntityID:   entity,
	})
	require.NoError(t, err)

	// Assignee sees 1 unread.
	count, err := svc.CountUnread(context.Background(), assignee.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	// Actor sees 0 — recipient is owner-scoped.
	count, err = svc.CountUnread(context.Background(), actor.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)

	// Mark the single notification read; unread count drops to 0.
	list, err := svc.List(context.Background(), assignee.ID, 50, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NoError(t, svc.MarkRead(context.Background(), assignee.ID, list[0].ID))

	count, err = svc.CountUnread(context.Background(), assignee.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
}

// TestService_MarkRead_OwnerScoped verifies that one user cannot mark
// another user's notification read.
func TestService_MarkRead_OwnerScoped(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, org.ID)
	intruder := testutil.CreateTestUser(t, db.Pool, org.ID)

	svc := notifications.NewService(generated.New(db.Pool))

	created, err := svc.Create(context.Background(), notifications.CreateInput{
		UserID: owner.ID,
		Kind:   notifications.KindAssigned,
		Title:  "Yours",
	})
	require.NoError(t, err)

	// Intruder attempts to mark read.
	require.NoError(t, svc.MarkRead(context.Background(), intruder.ID, created.ID))

	// Still unread for owner.
	count, err := svc.CountUnread(context.Background(), owner.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}

// TestService_MarkAllRead clears unread for the calling user only.
func TestService_MarkAllRead(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	other := testutil.CreateTestUser(t, db.Pool, org.ID)

	svc := notifications.NewService(generated.New(db.Pool))

	for i := 0; i < 3; i++ {
		_, err := svc.Create(context.Background(), notifications.CreateInput{
			UserID: user.ID, Kind: notifications.KindAssigned, Title: "n",
		})
		require.NoError(t, err)
	}
	_, err := svc.Create(context.Background(), notifications.CreateInput{
		UserID: other.ID, Kind: notifications.KindAssigned, Title: "other",
	})
	require.NoError(t, err)

	require.NoError(t, svc.MarkAllRead(context.Background(), user.ID))

	count, err := svc.CountUnread(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)

	// Other user untouched.
	count, err = svc.CountUnread(context.Background(), other.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}

// TestService_Create_RejectsNilUserID guards against a producer regression
// that would silently associate notifications with the zero UUID.
func TestService_Create_RejectsNilUserID(t *testing.T) {
	db := testutil.NewTestDB(t)
	svc := notifications.NewService(generated.New(db.Pool))
	_, err := svc.Create(context.Background(), notifications.CreateInput{
		Kind: notifications.KindAssigned, Title: "x",
	})
	require.ErrorIs(t, err, notifications.ErrInvalidUserID)
}
