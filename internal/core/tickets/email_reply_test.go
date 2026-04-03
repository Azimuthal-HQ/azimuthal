package tickets

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSendReply(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	spaceID := uuid.New()
	reporterID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("success", func(t *testing.T) {
		sender := &mockEmailSender{}
		err := svc.SendReply(context.Background(), sender, ReplyParams{
			TicketID:   ticket.ID,
			Recipients: []string{"customer@example.com"},
			Body:       "We are looking into your issue.",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sender.sent) != 1 {
			t.Fatalf("expected 1 sent email, got %d", len(sender.sent))
		}
		sent := sender.sent[0]
		if !strings.HasPrefix(sent.subject, "Re: Test ticket") {
			t.Errorf("expected subject to start with 'Re: Test ticket', got %q", sent.subject)
		}
		if sent.body != "We are looking into your issue." {
			t.Errorf("unexpected body: %q", sent.body)
		}
	})

	t.Run("nil sender", func(t *testing.T) {
		err := svc.SendReply(context.Background(), nil, ReplyParams{
			TicketID:   ticket.ID,
			Recipients: []string{"customer@example.com"},
			Body:       "test",
		})
		if err == nil {
			t.Error("expected error for nil sender")
		}
	})

	t.Run("no recipients", func(t *testing.T) {
		err := svc.SendReply(context.Background(), &mockEmailSender{}, ReplyParams{
			TicketID: ticket.ID,
			Body:     "test",
		})
		if err == nil {
			t.Error("expected error for no recipients")
		}
	})

	t.Run("ticket not found", func(t *testing.T) {
		err := svc.SendReply(context.Background(), &mockEmailSender{}, ReplyParams{
			TicketID:   uuid.New(),
			Recipients: []string{"customer@example.com"},
			Body:       "test",
		})
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})
}
