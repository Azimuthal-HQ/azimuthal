package tickets

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestAssign(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("assign to user", func(t *testing.T) {
		notifier := &mockNotifier{}
		updated, err := svc.Assign(context.Background(), ticket.ID, assigneeID, notifier)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.AssigneeID == nil || *updated.AssigneeID != assigneeID {
			t.Error("expected ticket to be assigned")
		}
		if !notifier.called {
			t.Error("expected notifier to be called")
		}
		if notifier.assigneeID != assigneeID {
			t.Errorf("expected notifier assignee %s, got %s", assigneeID, notifier.assigneeID)
		}
	})

	t.Run("already assigned", func(t *testing.T) {
		_, err := svc.Assign(context.Background(), ticket.ID, assigneeID, &mockNotifier{})
		if err != ErrAlreadyAssigned {
			t.Errorf("expected ErrAlreadyAssigned, got %v", err)
		}
	})

	t.Run("reassign to different user", func(t *testing.T) {
		newAssignee := uuid.New()
		updated, err := svc.Assign(context.Background(), ticket.ID, newAssignee, &mockNotifier{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if *updated.AssigneeID != newAssignee {
			t.Error("expected ticket to be reassigned")
		}
	})

	t.Run("nil notifier", func(t *testing.T) {
		anotherTicket := createTestTicket(t, svc, spaceID, reporterID)
		_, err := svc.Assign(context.Background(), anotherTicket.ID, assigneeID, nil)
		if err != nil {
			t.Fatalf("unexpected error with nil notifier: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Assign(context.Background(), uuid.New(), assigneeID, &mockNotifier{})
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})
}

func TestUnassign(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	// First assign
	_, err := svc.Assign(context.Background(), ticket.ID, assigneeID, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("unassign", func(t *testing.T) {
		updated, err := svc.Unassign(context.Background(), ticket.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.AssigneeID != nil {
			t.Error("expected assignee to be nil after unassign")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Unassign(context.Background(), uuid.New())
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})
}
