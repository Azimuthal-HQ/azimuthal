package adapters_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
)

// recordingSender captures the message instead of sending it.
type recordingSender struct {
	msg email.Message
	err error
}

func (r *recordingSender) Send(_ context.Context, m email.Message) error {
	r.msg = m
	return r.err
}

// TestPortalLinkSender_EscapesBothInterpolatedValues is the one that matters.
//
// The body is text/html — email.buildMIMEMessage sets the content type — and
// the portal name is chosen by whoever configured the space. So an unescaped
// name is stored XSS delivered by the product itself, in an email, to an
// address OUTSIDE the organisation, where no CSP applies and the reader has no
// reason to be suspicious.
//
// FAILS-BEFORE: drop the html.EscapeString calls in SendMagicLink and this
// fails on the raw <script> assertion.
func TestPortalLinkSender_EscapesBothInterpolatedValues(t *testing.T) {
	rec := &recordingSender{}
	s := adapters.NewPortalLinkSender(rec)

	err := s.SendMagicLink(context.Background(),
		"customer@example.com",
		`Acme <script>alert(1)</script> Support`,
		`https://portal.example.com/portal/abc/signin/tok"onmouseover="x`,
		time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC))
	require.NoError(t, err)

	require.NotContains(t, rec.msg.Body, "<script>",
		"an unescaped portal name is stored XSS delivered by email")
	require.NotContains(t, rec.msg.Body, `"onmouseover="`,
		"an unescaped URL can break out of the href attribute")
	require.Contains(t, rec.msg.Body, "&lt;script&gt;")
	require.Contains(t, rec.msg.Body, "&#34;onmouseover=&#34;")

	// The recipient and the human-readable expiry still have to be right.
	require.Equal(t, []string{"customer@example.com"}, rec.msg.To)
	require.Contains(t, rec.msg.Body, "1 August 2026 at 09:30 UTC")
	require.Contains(t, rec.msg.Body, "It works once")

	// The SUBJECT is a header, not HTML, so it carries the name verbatim —
	// escaping it would put "&lt;" in the customer's inbox list.
	require.Equal(t, `Sign in to Acme <script>alert(1)</script> Support`, rec.msg.Subject)
}

// TestPortalLinkSender_WrapsTransportFailure keeps the error attributable.
// portal.Service.deliver turns any failure into Delivered=false rather than
// failing the request, so this wrapper is the only place the reason survives.
func TestPortalLinkSender_WrapsTransportFailure(t *testing.T) {
	boom := errors.New("smtp: connection refused")
	s := adapters.NewPortalLinkSender(&recordingSender{err: boom})

	err := s.SendMagicLink(context.Background(), "a@b.com", "Desk", "https://x/y", time.Now().UTC())
	require.Error(t, err)
	require.ErrorIs(t, err, boom)
	require.True(t, strings.Contains(err.Error(), "portal sign-in link"),
		"the wrap must say which send failed: %v", err)
}
