package adapters_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/credlink"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
)

// TestCredentialLinkSender_EscapesURLAndWordsPerPurpose: the body is text/html,
// and the URL carries a base64 token, so an unescaped URL is a stored-XSS vector
// delivered by the product itself — the same hazard PortalLinkSender escapes. The
// copy differs by purpose so a sign-in, a reset and an email change read right.
//
// FAILS-BEFORE: drop the html.EscapeString call in SendCredentialLink and this
// fails on the attribute-breakout assertion.
func TestCredentialLinkSender_EscapesURLAndWordsPerPurpose(t *testing.T) {
	cases := []struct {
		purpose credlink.Purpose
		subject string
		action  string
	}{
		{credlink.PurposeSignIn, "Set up your Azimuthal account", "Set your password"},
		{credlink.PurposePasswordReset, "Reset your Azimuthal password", "Reset your password"},
		{credlink.PurposeEmailChange, "Confirm your new Azimuthal email address", "Confirm this address"},
	}
	for _, tc := range cases {
		t.Run(string(tc.purpose), func(t *testing.T) {
			rec := &recordingSender{}
			s := adapters.NewCredentialLinkSender(rec)

			err := s.SendCredentialLink(context.Background(),
				"user@example.com",
				tc.purpose,
				`https://azimuthal.example.com/credential/tok"onmouseover="x`,
				time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC))
			require.NoError(t, err)

			require.Equal(t, []string{"user@example.com"}, rec.msg.To)
			require.Equal(t, tc.subject, rec.msg.Subject)
			require.NotContains(t, rec.msg.Body, `"onmouseover="`, "an unescaped URL breaks out of the href")
			require.Contains(t, rec.msg.Body, "&#34;onmouseover=&#34;")
			require.Contains(t, rec.msg.Body, tc.action)
			require.Contains(t, rec.msg.Body, "1 August 2026 at 09:30 UTC")
			require.Contains(t, rec.msg.Body, "It works once")
		})
	}
}

// TestCredentialLinkSender_WrapsTransportFailure keeps the error attributable —
// Service.deliver swallows it into Delivered=false, so this wrapper is the only
// place the reason survives.
func TestCredentialLinkSender_WrapsTransportFailure(t *testing.T) {
	boom := errors.New("smtp: connection refused")
	s := adapters.NewCredentialLinkSender(&recordingSender{err: boom})

	err := s.SendCredentialLink(context.Background(), "a@b.com", credlink.PurposeSignIn, "https://x/y", time.Now().UTC())
	require.Error(t, err)
	require.ErrorIs(t, err, boom)
	require.True(t, strings.Contains(err.Error(), "credential link"),
		"the wrap must say which send failed: %v", err)
}
