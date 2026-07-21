package adapters

import (
	"context"
	"fmt"
	"html"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// InviteEmailSender implements invites.Sender over the email package,
// resolving the org's display name for the message body.
type InviteEmailSender struct {
	sender email.Sender
	q      *generated.Queries
}

// NewInviteEmailSender creates an InviteEmailSender.
func NewInviteEmailSender(sender email.Sender, q *generated.Queries) *InviteEmailSender {
	return &InviteEmailSender{sender: sender, q: q}
}

// SendInvite delivers the invite link by email.
func (s *InviteEmailSender) SendInvite(ctx context.Context, toEmail string, orgID uuid.UUID, inviteURL string, expiresAt time.Time) error {
	orgName := "an Azimuthal organization"
	if org, err := s.q.GetOrganizationByID(ctx, orgID); err == nil && org.Name != "" {
		orgName = org.Name
	}
	safeOrg := html.EscapeString(orgName)
	body := fmt.Sprintf(
		`<p>You have been invited to join <strong>%s</strong> on Azimuthal.</p>
<p><a href="%s">Accept the invitation</a></p>
<p>This invitation expires on %s. If you were not expecting it, you can ignore this email.</p>`,
		safeOrg, html.EscapeString(inviteURL), expiresAt.UTC().Format("2 January 2006"))
	if err := s.sender.Send(ctx, email.Message{
		To:      []string{toEmail},
		Subject: fmt.Sprintf("You're invited to join %s on Azimuthal", orgName),
		Body:    body,
	}); err != nil {
		return fmt.Errorf("sending invite email: %w", err)
	}
	return nil
}
