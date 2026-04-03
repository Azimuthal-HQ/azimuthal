package tickets

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestKanbanBoard(t *testing.T) {
	repo := newMockRepo()
	svc := NewTicketService(repo)
	ctx := context.Background()
	spaceID := uuid.New()
	reporterID := uuid.New()

	// Create tickets in different statuses
	t1 := createTestTicket(t, svc, spaceID, reporterID)
	t2 := createTestTicket(t, svc, spaceID, reporterID)
	t3 := createTestTicket(t, svc, spaceID, reporterID)

	// Move t2 to in_progress, t3 to resolved
	svc.TransitionStatus(ctx, t2.ID, StatusInProgress)
	svc.TransitionStatus(ctx, t3.ID, StatusInProgress)
	svc.TransitionStatus(ctx, t3.ID, StatusResolved)

	board, err := svc.KanbanBoard(ctx, spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(board) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(board))
	}

	// Verify columns
	expected := map[Status]int{
		StatusOpen:       1, // t1
		StatusInProgress: 1, // t2
		StatusResolved:   1, // t3
		StatusClosed:     0,
	}
	for _, col := range board {
		want, ok := expected[col.Status]
		if !ok {
			t.Errorf("unexpected column status %q", col.Status)
			continue
		}
		if len(col.Tickets) != want {
			t.Errorf("column %q: expected %d tickets, got %d", col.Status, want, len(col.Tickets))
		}
	}

	// Suppress unused variable warning
	_ = t1
}

func TestListByAssignee(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	ctx := context.Background()
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()

	ticket := createTestTicket(t, svc, spaceID, reporterID)
	svc.Assign(ctx, ticket.ID, assigneeID, nil)

	// Create another unassigned ticket
	createTestTicket(t, svc, spaceID, reporterID)

	results, err := svc.ListByAssignee(ctx, spaceID, assigneeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 assigned ticket, got %d", len(results))
	}
}

func TestListBySpace(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	ctx := context.Background()
	spaceID := uuid.New()
	otherSpace := uuid.New()
	reporterID := uuid.New()

	createTestTicket(t, svc, spaceID, reporterID)
	createTestTicket(t, svc, spaceID, reporterID)
	createTestTicket(t, svc, otherSpace, reporterID)

	results, err := svc.ListBySpace(ctx, spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 tickets in space, got %d", len(results))
	}
}
