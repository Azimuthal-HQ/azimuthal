package adapters

import (
	"context"
	"fmt"
	"html"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
)

// PortalLinkSender delivers customer-portal sign-in links over the email
// transport. It implements portal.Sender and mirrors InviteEmailSender.
type PortalLinkSender struct {
	sender email.Sender
}

// NewPortalLinkSender creates a PortalLinkSender over the given transport.
func NewPortalLinkSender(sender email.Sender) *PortalLinkSender {
	return &PortalLinkSender{sender: sender}
}

// SendMagicLink emails a sign-in link.
//
// BOTH INTERPOLATED VALUES ARE ESCAPED. The body is text/html
// (email.buildMIMEMessage sets the content type), the portal name is chosen by
// whoever configured the space, and the URL carries a base64 token — so an
// unescaped name is stored XSS delivered by the product itself, to an address
// outside the organisation. InviteEmailSender escapes both for the same
// reason; this is not defensive habit, it is the same known hazard.
func (s *PortalLinkSender) SendMagicLink(ctx context.Context, toEmail, portalName, linkURL string, expiresAt time.Time) error {
	safeName := html.EscapeString(portalName)
	safeURL := html.EscapeString(linkURL)
	body := fmt.Sprintf(
		`<p>Hello,</p>`+
			`<p>Use the link below to sign in to <strong>%s</strong> and track your requests. `+
			`It works once, and it expires on %s.</p>`+
			`<p><a href="%s">Sign in</a></p>`+
			`<p>If you did not ask for this, you can ignore this message — nothing has been created or changed.</p>`,
		safeName, expiresAt.UTC().Format("2 January 2006 at 15:04 UTC"), safeURL)

	if err := s.sender.Send(ctx, email.Message{
		To:      []string{toEmail},
		Subject: "Sign in to " + portalName,
		Body:    body,
	}); err != nil {
		return fmt.Errorf("sending portal sign-in link: %w", err)
	}
	return nil
}
