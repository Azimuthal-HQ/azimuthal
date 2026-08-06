package tickets

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// noopShareDeleter satisfies ShareRevokingDeleter for the unit tests, which
// exercise ticket logic rather than the delete-and-revoke-shares
// transaction (covered by integration tests against a real database).
type noopShareDeleter struct{}

func (noopShareDeleter) DeleteTicketAndRevokeShares(_ context.Context, _, _, _ uuid.UUID) error {
	return nil
}

// repoShareDeleter routes the delete-and-revoke seam back to the mock repo,
// so a unit test can assert the ticket is actually gone after Delete. The
// share revocation is a no-op here (no shares in the mock); the real
// same-transaction revocation is covered by integration tests.
type repoShareDeleter struct{ repo *mockRepo }

func (d repoShareDeleter) DeleteTicketAndRevokeShares(ctx context.Context, id, spaceID, _ uuid.UUID) error {
	return d.repo.DeleteInSpace(ctx, id, spaceID)
}

// --- Mock repository ---

type mockRepo struct {
	tickets map[uuid.UUID]*Ticket
	// outsiders are user ids UserIsMemberOfSpaceOrg answers false for.
	//
	// The default is membership, so every test written before the assignee check
	// existed still exercises the path it was written for. A test that wants the
	// refusal names its outsider explicitly, which is the only way this double
	// can report one — it cannot decide membership for itself, so the predicate
	// proper is proven in the integration suite and this only keeps the unit
	// tests from contradicting it.
	outsiders map[uuid.UUID]bool
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		tickets:   make(map[uuid.UUID]*Ticket),
		outsiders: make(map[uuid.UUID]bool),
	}
}

func (m *mockRepo) UserIsMemberOfSpaceOrg(_ context.Context, _, userID uuid.UUID) (bool, error) {
	return !m.outsiders[userID], nil
}

func (m *mockRepo) Create(_ context.Context, t *Ticket) error {
	m.tickets[t.ID] = t
	return nil
}

func (m *mockRepo) GetByID(_ context.Context, id uuid.UUID) (*Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

// GetByIDInSpace applies the same space predicate the query applies, and
// returns the same ErrNotFound for a ticket in another space as for one that
// is absent. A mock that ignored spaceID would make every scoped read look
// correct while asserting nothing about the scoping.
func (m *mockRepo) GetByIDInSpace(_ context.Context, spaceID, id uuid.UUID) (*Ticket, error) {
	t, ok := m.tickets[id]
	if !ok || t.SpaceID != spaceID {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *mockRepo) Update(_ context.Context, t *Ticket) error {
	if _, ok := m.tickets[t.ID]; !ok {
		return ErrNotFound
	}
	m.tickets[t.ID] = t
	return nil
}

func (m *mockRepo) UpdateStatus(_ context.Context, id uuid.UUID, status Status) (*Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	t.Status = status
	now := time.Now().UTC()
	t.UpdatedAt = now
	if status == StatusResolved {
		t.ResolvedAt = &now
	}
	return t, nil
}

func (m *mockRepo) DeleteInSpace(_ context.Context, id, spaceID uuid.UUID) error {
	t, ok := m.tickets[id]
	if !ok || t.SpaceID != spaceID {
		return ErrNotFound
	}
	delete(m.tickets, id)
	return nil
}

func (m *mockRepo) ListBySpace(_ context.Context, spaceID uuid.UUID) ([]*Ticket, error) {
	var result []*Ticket
	for _, t := range m.tickets {
		if t.SpaceID == spaceID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockRepo) ListByStatus(_ context.Context, spaceID uuid.UUID, status Status) ([]*Ticket, error) {
	var result []*Ticket
	for _, t := range m.tickets {
		if t.SpaceID == spaceID && t.Status == status {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockRepo) ListByAssignee(_ context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Ticket, error) {
	var result []*Ticket
	for _, t := range m.tickets {
		if t.SpaceID == spaceID && t.AssigneeID != nil && *t.AssigneeID == assigneeID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockRepo) Search(_ context.Context, spaceID uuid.UUID, query string, limit int32) ([]*Ticket, error) {
	var result []*Ticket
	lower := strings.ToLower(query)
	for _, t := range m.tickets {
		if t.SpaceID != spaceID {
			continue
		}
		if strings.Contains(strings.ToLower(t.Title), lower) ||
			strings.Contains(strings.ToLower(t.Description), lower) {
			result = append(result, t)
			if len(result) >= int(limit) {
				break
			}
		}
	}
	return result, nil
}

// --- Mock notifier ---

type mockNotifier struct {
	called     bool
	ticketID   uuid.UUID
	spaceID    uuid.UUID
	assigneeID uuid.UUID
}

func (n *mockNotifier) NotifyAssignment(_ context.Context, ticketID uuid.UUID, spaceID uuid.UUID, assigneeID uuid.UUID, _ string) error {
	n.called = true
	n.ticketID = ticketID
	n.spaceID = spaceID
	n.assigneeID = assigneeID
	return nil
}

// --- Mock email sender ---

type mockEmailSender struct {
	sent []sentEmail
}

type sentEmail struct {
	to      []string
	subject string
	body    string
}

func (s *mockEmailSender) SendTicketReply(_ context.Context, to []string, subject string, body string) error {
	s.sent = append(s.sent, sentEmail{to: to, subject: subject, body: body})
	return nil
}

// --- Helpers ---

func createTestTicket(t *testing.T, svc *TicketService, spaceID, reporterID uuid.UUID) *Ticket {
	t.Helper()
	ticket, err := svc.Create(context.Background(), CreateTicketParams{
		SpaceID:    spaceID,
		Title:      "Test ticket",
		Priority:   PriorityMedium,
		ReporterID: reporterID,
	})
	if err != nil {
		t.Fatalf("creating test ticket: %v", err)
	}
	return ticket
}

// --- Tests ---

func TestCreateTicket(t *testing.T) {
	svc := NewTicketService(newMockRepo(), noopShareDeleter{})
	spaceID := uuid.New()
	reporterID := uuid.New()

	t.Run("success", func(t *testing.T) {
		ticket, err := svc.Create(context.Background(), CreateTicketParams{
			SpaceID:     spaceID,
			Title:       "Login page broken",
			Description: "Cannot log in with valid credentials",
			Priority:    PriorityHigh,
			ReporterID:  reporterID,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ticket.ID == uuid.Nil {
			t.Error("expected non-nil ticket ID")
		}
		if ticket.Status != StatusOpen {
			t.Errorf("expected status %q, got %q", StatusOpen, ticket.Status)
		}
		if ticket.Priority != PriorityHigh {
			t.Errorf("expected priority %q, got %q", PriorityHigh, ticket.Priority)
		}
	})

	t.Run("missing title", func(t *testing.T) {
		_, err := svc.Create(context.Background(), CreateTicketParams{
			SpaceID:    spaceID,
			Priority:   PriorityMedium,
			ReporterID: reporterID,
		})
		if !errors.Is(err, ErrTitleRequired) {
			t.Errorf("expected ErrTitleRequired, got %v", err)
		}
	})

	t.Run("missing space", func(t *testing.T) {
		_, err := svc.Create(context.Background(), CreateTicketParams{
			Title:      "Test",
			Priority:   PriorityMedium,
			ReporterID: reporterID,
		})
		if !errors.Is(err, ErrSpaceRequired) {
			t.Errorf("expected ErrSpaceRequired, got %v", err)
		}
	})

	t.Run("missing reporter", func(t *testing.T) {
		_, err := svc.Create(context.Background(), CreateTicketParams{
			SpaceID:  spaceID,
			Title:    "Test",
			Priority: PriorityMedium,
		})
		if !errors.Is(err, ErrReporterRequired) {
			t.Errorf("expected ErrReporterRequired, got %v", err)
		}
	})

	t.Run("invalid priority", func(t *testing.T) {
		_, err := svc.Create(context.Background(), CreateTicketParams{
			SpaceID:    spaceID,
			Title:      "Test",
			Priority:   Priority("critical"),
			ReporterID: reporterID,
		})
		if err == nil {
			t.Error("expected error for invalid priority")
		}
	})
}

func TestGetTicket(t *testing.T) {
	svc := NewTicketService(newMockRepo(), noopShareDeleter{})
	spaceID := uuid.New()
	reporterID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("exists", func(t *testing.T) {
		got, err := svc.Get(context.Background(), ticket.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != ticket.ID {
			t.Errorf("expected ID %s, got %s", ticket.ID, got.ID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Get(context.Background(), uuid.New())
		if err == nil {
			t.Error("expected error for missing ticket")
		}
	})
}

// TestGetTicketInSpace covers the reconciliation the space-scoped routes rely
// on: a ticket read under its own space is returned, and the same ticket read
// under any other space is reported as ErrNotFound — the same error, in the
// same words, as a ticket that was never there.
func TestGetTicketInSpace(t *testing.T) {
	svc := NewTicketService(newMockRepo(), noopShareDeleter{})
	spaceID := uuid.New()
	reporterID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("in its own space", func(t *testing.T) {
		got, err := svc.GetInSpace(context.Background(), spaceID, ticket.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != ticket.ID {
			t.Errorf("expected ID %s, got %s", ticket.ID, got.ID)
		}
	})

	t.Run("in another space", func(t *testing.T) {
		_, err := svc.GetInSpace(context.Background(), uuid.New(), ticket.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("absent", func(t *testing.T) {
		_, err := svc.GetInSpace(context.Background(), spaceID, uuid.New())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestUpdateTicket(t *testing.T) {
	svc := NewTicketService(newMockRepo(), noopShareDeleter{})
	spaceID := uuid.New()
	reporterID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	t.Run("success", func(t *testing.T) {
		ticket.Title = "Updated title"
		ticket.Priority = PriorityUrgent
		err := svc.Update(context.Background(), ticket)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, _ := svc.Get(context.Background(), ticket.ID)
		if got.Title != "Updated title" {
			t.Errorf("expected updated title, got %q", got.Title)
		}
	})

	t.Run("empty title", func(t *testing.T) {
		ticket.Title = ""
		err := svc.Update(context.Background(), ticket)
		if !errors.Is(err, ErrTitleRequired) {
			t.Errorf("expected ErrTitleRequired, got %v", err)
		}
		ticket.Title = "Updated title" // restore
	})

	t.Run("invalid priority", func(t *testing.T) {
		ticket.Priority = Priority("invalid")
		err := svc.Update(context.Background(), ticket)
		if err == nil {
			t.Error("expected error for invalid priority")
		}
		ticket.Priority = PriorityMedium // restore
	})
}

func TestDeleteTicket(t *testing.T) {
	repo := newMockRepo()
	svc := NewTicketService(repo, repoShareDeleter{repo})
	spaceID := uuid.New()
	reporterID := uuid.New()
	ticket := createTestTicket(t, svc, spaceID, reporterID)

	err := svc.Delete(context.Background(), ticket.ID, spaceID, reporterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = svc.Get(context.Background(), ticket.ID)
	if err == nil {
		t.Error("expected error after deletion")
	}
}
