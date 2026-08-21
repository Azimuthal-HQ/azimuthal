package adapters

import (
	"context"
	"fmt"
	"html"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/credlink"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
)

// CredentialLinkSender delivers internal-user credential links over the email
// transport. It implements credlink.Sender and mirrors PortalLinkSender.
type CredentialLinkSender struct {
	sender email.Sender
}

// NewCredentialLinkSender creates a CredentialLinkSender over the given transport.
func NewCredentialLinkSender(sender email.Sender) *CredentialLinkSender {
	return &CredentialLinkSender{sender: sender}
}

// SendCredentialLink emails a credential link, worded for its purpose.
//
// THE URL IS ESCAPED. The body is text/html (email.buildMIMEMessage sets the
// content type) and the URL carries a base64 token, so an unescaped URL is a
// stored-XSS vector delivered by the product itself — the same hazard
// PortalLinkSender and InviteEmailSender escape against. The purpose is a fixed
// enum, not user input, so its phrasing is chosen here rather than interpolated.
func (s *CredentialLinkSender) SendCredentialLink(ctx context.Context, toEmail string, purpose credlink.Purpose, linkURL string, expiresAt time.Time) error {
	safeURL := html.EscapeString(linkURL)
	subject, lead, action := credentialLinkCopy(purpose)
	body := fmt.Sprintf(
		`<p>Hello,</p>`+
			`<p>%s It works once, and it expires on %s.</p>`+
			`<p><a href="%s">%s</a></p>`+
			`<p>If you did not ask for this, you can ignore this message — nothing has been changed.</p>`,
		lead, expiresAt.UTC().Format("2 January 2006 at 15:04 UTC"), safeURL, action)

	if err := s.sender.Send(ctx, email.Message{
		To:      []string{toEmail},
		Subject: subject,
		Body:    body,
	}); err != nil {
		return fmt.Errorf("sending credential link: %w", err)
	}
	return nil
}

// credentialLinkCopy returns the subject, lead sentence and call-to-action label
// for a purpose. Kept in one place so the three flows read consistently.
func credentialLinkCopy(purpose credlink.Purpose) (subject, lead, action string) {
	switch purpose {
	case credlink.PurposeSignIn:
		return "Set up your Azimuthal account",
			"An account has been created for you. Use the link below to set a password and sign in.",
			"Set your password"
	case credlink.PurposePasswordReset:
		return "Reset your Azimuthal password",
			"Use the link below to set a new password. Setting it signs you out of every device.",
			"Reset your password"
	case credlink.PurposeEmailChange:
		return "Confirm your new Azimuthal email address",
			"Use the link below to confirm this address for your account.",
			"Confirm this address"
	default:
		return "Your Azimuthal sign-in link",
			"Use the link below to continue.",
			"Continue"
	}
}
