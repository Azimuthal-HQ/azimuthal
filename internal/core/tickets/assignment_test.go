package tickets

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestAssign(t *testing.T) {
	repo := newMockRepo()
	svc := NewTicketService(repo, noopShareDeleter{})
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("assign to user", func(t *testing.T) {
		notifier := &mockNotifier{}
		updated, err := svc.Assign(context.Background(), ticket.ID, spaceID, assigneeID, notifier)
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
		_, err := svc.Assign(context.Background(), ticket.ID, spaceID, assigneeID, &mockNotifier{})
		if !errors.Is(err, ErrAlreadyAssigned) {
			t.Errorf("expected ErrAlreadyAssigned, got %v", err)
		}
	})

	t.Run("reassign to different user", func(t *testing.T) {
		newAssignee := uuid.New()
		updated, err := svc.Assign(context.Background(), ticket.ID, spaceID, newAssignee, &mockNotifier{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if *updated.AssigneeID != newAssignee {
			t.Error("expected ticket to be reassigned")
		}
	})

	t.Run("nil notifier", func(t *testing.T) {
		anotherTicket := createTestTicket(t, svc, spaceID, reporterID)
		_, err := svc.Assign(context.Background(), anotherTicket.ID, spaceID, assigneeID, nil)
		if err != nil {
			t.Fatalf("unexpected error with nil notifier: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Assign(context.Background(), uuid.New(), spaceID, assigneeID, &mockNotifier{})
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})

	// An assignee outside the ticket's organisation is refused, and nothing is
	// written or notified.
	//
	// assignee_id references the GLOBAL users table, so the foreign key is
	// satisfied by any user in the installation and the write used to land 200 —
	// naming somebody with no membership in the org and no access to the space,
	// and sending them the ticket's title. Delete the membership check in Assign
	// and this case assigns successfully. (known-issues #23c.)
	t.Run("an assignee outside the organisation is refused", func(t *testing.T) {
		outsider := uuid.New()
		repo.outsiders[outsider] = true
		fresh := createTestTicket(t, svc, spaceID, reporterID)
		notifier := &mockNotifier{}

		_, err := svc.Assign(context.Background(), fresh.ID, spaceID, outsider, notifier)
		if !errors.Is(err, ErrAssigneeNotOrgMember) {
			t.Fatalf("expected ErrAssigneeNotOrgMember, got %v", err)
		}
		if notifier.called {
			t.Error("a refused assignment must not notify: the message carries the ticket title")
		}
		if fresh.AssigneeID != nil {
			t.Error("a refused assignment must not have been written")
		}

		// And a member of the same org still assigns, so a check that refused
		// everybody could not pass this test.
		if _, err := svc.Assign(context.Background(), fresh.ID, spaceID, uuid.New(), &mockNotifier{}); err != nil {
			t.Fatalf("an org member must still be assignable: %v", err)
		}
	})

	// A real ticket named under the wrong space is refused, and refused as
	// ErrNotFound rather than as anything that would confirm it exists. Delete
	// the space predicate in Assign and this case assigns successfully.
	t.Run("ticket in another space is not found", func(t *testing.T) {
		otherSpace := uuid.New()
		before := ticket.AssigneeID
		_, err := svc.Assign(context.Background(), ticket.ID, otherSpace, uuid.New(), &mockNotifier{})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		if ticket.AssigneeID != before {
			t.Error("expected the assignee to be untouched by a cross-space assign")
		}
	})
}

func TestUnassign(t *testing.T) {
	svc := NewTicketService(newMockRepo(), noopShareDeleter{})
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	// First assign
	_, err := svc.Assign(context.Background(), ticket.ID, spaceID, assigneeID, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ordered before the successful unassign so the ticket still has an
	// assignee to lose: a cross-space unassign that got through would clear it,
	// and the assertion below would catch that rather than passing on a ticket
	// that was already unassigned.
	t.Run("ticket in another space is not found", func(t *testing.T) {
		_, err := svc.Unassign(context.Background(), ticket.ID, uuid.New())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		if ticket.AssigneeID == nil {
			t.Error("expected the assignee to survive a cross-space unassign")
		}
	})

	t.Run("unassign", func(t *testing.T) {
		updated, err := svc.Unassign(context.Background(), ticket.ID, spaceID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.AssigneeID != nil {
			t.Error("expected assignee to be nil after unassign")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Unassign(context.Background(), uuid.New(), spaceID)
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})
}
