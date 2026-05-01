package tickets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// EmailSender is the interface for delivering outbound email messages.
type EmailSender interface {
	// SendTicketReply sends an email reply for a ticket update.
	SendTicketReply(ctx context.Context, to []string, subject string, body string) error
}

// ReplyParams holds the parameters for sending an email reply on a ticket.
type ReplyParams struct {
	TicketID   uuid.UUID `json:"ticket_id"`
	Recipients []string  `json:"recipients"`
	Body       string    `json:"body"`
}

// SendReply sends an outbound email reply for a ticket. It fetches the ticket
// to include the subject line, then delegates to the EmailSender.
func (s *TicketService) SendReply(ctx context.Context, sender EmailSender, params ReplyParams) error {
	if sender == nil {
		return fmt.Errorf("sending ticket reply: email sender is nil")
	}
	if len(params.Recipients) == 0 {
		return fmt.Errorf("sending ticket reply: no recipients specified")
	}

	t, err := s.repo.GetByID(ctx, params.TicketID)
	if err != nil {
		return fmt.Errorf("sending ticket reply: %w", err)
	}

	subject := fmt.Sprintf("Re: %s [#%s]", t.Title, t.ID.String()[:8])
	if err := sender.SendTicketReply(ctx, params.Recipients, subject, params.Body); err != nil {
		return fmt.Errorf("sending ticket reply: %w", err)
	}

	return nil
}
