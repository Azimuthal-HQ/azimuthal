package tickets

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestStatusIsValid(t *testing.T) {
	valid := []Status{StatusOpen, StatusInProgress, StatusResolved, StatusClosed}
	for _, s := range valid {
		if !s.IsValid() {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []Status{"pending", "cancelled", ""}
	for _, s := range invalid {
		if s.IsValid() {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestPriorityIsValid(t *testing.T) {
	valid := []Priority{PriorityUrgent, PriorityHigh, PriorityMedium, PriorityLow}
	for _, p := range valid {
		if !p.IsValid() {
			t.Errorf("expected %q to be valid", p)
		}
	}

	if Priority("critical").IsValid() {
		t.Error("expected 'critical' to be invalid")
	}
}

func TestCanTransitionTo(t *testing.T) {
	tests := []struct {
		from    Status
		to      Status
		allowed bool
	}{
		// Valid transitions from open
		{StatusOpen, StatusInProgress, true},
		{StatusOpen, StatusClosed, true},
		{StatusOpen, StatusResolved, false},

		// Valid transitions from in_progress
		{StatusInProgress, StatusResolved, true},
		{StatusInProgress, StatusOpen, true},
		{StatusInProgress, StatusClosed, true},

		// Valid transitions from resolved
		{StatusResolved, StatusClosed, true},
		{StatusResolved, StatusOpen, true},
		{StatusResolved, StatusInProgress, false},

		// Valid transitions from closed
		{StatusClosed, StatusOpen, true},
		{StatusClosed, StatusInProgress, false},
		{StatusClosed, StatusResolved, false},

		// Same status
		{StatusOpen, StatusOpen, false},
		{StatusClosed, StatusClosed, false},
	}

	for _, tt := range tests {
		result := tt.from.CanTransitionTo(tt.to)
		if result != tt.allowed {
			t.Errorf("%q -> %q: expected allowed=%v, got %v", tt.from, tt.to, tt.allowed, result)
		}
	}
}

func TestValidateTransition(t *testing.T) {
	t.Run("valid transition", func(t *testing.T) {
		err := ValidateTransition(StatusOpen, StatusInProgress)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid transition", func(t *testing.T) {
		err := ValidateTransition(StatusOpen, StatusResolved)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("invalid current status", func(t *testing.T) {
		err := ValidateTransition(Status("bogus"), StatusOpen)
		if !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("expected ErrInvalidStatus, got %v", err)
		}
	})

	t.Run("invalid target status", func(t *testing.T) {
		err := ValidateTransition(StatusOpen, Status("bogus"))
		if !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("expected ErrInvalidStatus, got %v", err)
		}
	})
}

func TestTransitionStatus(t *testing.T) {
	repo := newMockRepo()
	svc := NewTicketService(repo)
	spaceID := uuid.New()
	reporterID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("open to in_progress", func(t *testing.T) {
		updated, err := svc.TransitionStatus(context.Background(), ticket.ID, StatusInProgress)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Status != StatusInProgress {
			t.Errorf("expected status %q, got %q", StatusInProgress, updated.Status)
		}
	})

	t.Run("in_progress to resolved", func(t *testing.T) {
		updated, err := svc.TransitionStatus(context.Background(), ticket.ID, StatusResolved)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Status != StatusResolved {
			t.Errorf("expected status %q, got %q", StatusResolved, updated.Status)
		}
	})

	t.Run("resolved to closed", func(t *testing.T) {
		updated, err := svc.TransitionStatus(context.Background(), ticket.ID, StatusClosed)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Status != StatusClosed {
			t.Errorf("expected status %q, got %q", StatusClosed, updated.Status)
		}
	})

	t.Run("reopen from closed", func(t *testing.T) {
		updated, err := svc.TransitionStatus(context.Background(), ticket.ID, StatusOpen)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Status != StatusOpen {
			t.Errorf("expected status %q, got %q", StatusOpen, updated.Status)
		}
	})

	t.Run("invalid transition rejected", func(t *testing.T) {
		_, err := svc.TransitionStatus(context.Background(), ticket.ID, StatusResolved)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.TransitionStatus(context.Background(), uuid.New(), StatusInProgress)
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})
}

func TestFullLifecycle(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	ctx := context.Background()
	spaceID := uuid.New()
	reporterID := uuid.New()

	ticket := createTestTicket(t, svc, spaceID, reporterID)
	if ticket.Status != StatusOpen {
		t.Fatalf("new ticket should be open, got %q", ticket.Status)
	}

	// open -> in_progress -> resolved -> closed
	ticket, _ = svc.TransitionStatus(ctx, ticket.ID, StatusInProgress)
	ticket, _ = svc.TransitionStatus(ctx, ticket.ID, StatusResolved)
	ticket, _ = svc.TransitionStatus(ctx, ticket.ID, StatusClosed)

	if ticket.Status != StatusClosed {
		t.Errorf("expected closed, got %q", ticket.Status)
	}

	// reopen
	ticket, _ = svc.TransitionStatus(ctx, ticket.ID, StatusOpen)
	if ticket.Status != StatusOpen {
		t.Errorf("expected open after reopen, got %q", ticket.Status)
	}
}
