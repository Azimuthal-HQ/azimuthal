package adapters_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// captureSender records the message instead of speaking SMTP.
type captureSender struct {
	sent []email.Message
	err  error
}

func (c *captureSender) Send(_ context.Context, msg email.Message) error {
	c.sent = append(c.sent, msg)
	return c.err
}

func TestInviteEmailSender_ResolvesOrgNameAndEscapes(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	// Give the org a name with markup to prove escaping.
	_, err := db.Pool.Exec(context.Background(),
		`UPDATE organizations SET name = '<b>Sharp & Co</b>' WHERE id = $1`, org.ID)
	require.NoError(t, err)

	capture := &captureSender{}
	sender := adapters.NewInviteEmailSender(capture, generated.New(db.Pool))

	expires := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, sender.SendInvite(context.Background(),
		"invitee@example.com", org.ID, "http://localhost:8080/invite/RAWTOKEN", expires))

	require.Len(t, capture.sent, 1)
	msg := capture.sent[0]
	require.Equal(t, []string{"invitee@example.com"}, msg.To)
	require.Contains(t, msg.Subject, "Sharp & Co", "the subject carries the org's display name")
	require.Contains(t, msg.Body, "http://localhost:8080/invite/RAWTOKEN")
	require.Contains(t, msg.Body, "1 August 2026", "the expiry date is spelled out")
	// The org name is HTML-escaped in the body — markup in a name cannot
	// inject into the email.
	require.Contains(t, msg.Body, "&lt;b&gt;Sharp &amp; Co&lt;/b&gt;")
	require.NotContains(t, msg.Body, "<b>Sharp & Co</b>")
}

func TestInviteEmailSender_UnknownOrgFallsBackToGenericName(t *testing.T) {
	db := testutil.NewTestDB(t)
	capture := &captureSender{}
	sender := adapters.NewInviteEmailSender(capture, generated.New(db.Pool))

	require.NoError(t, sender.SendInvite(context.Background(),
		"x@example.com", uuid.New(), "http://localhost/invite/T", time.Now().Add(time.Hour)))
	require.Len(t, capture.sent, 1)
	require.Contains(t, capture.sent[0].Body, "an Azimuthal organization")
}
