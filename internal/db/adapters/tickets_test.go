package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

func TestDbTicketToTicket(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	due := now.Add(72 * time.Hour)

	dbTicket := generated.Ticket{
		ID:          id,
		SpaceID:     spaceID,
		Number:      1,
		Title:       "Fix login bug",
		Description: "A test ticket",
		Status:      "open",
		Priority:    "high",
		ReporterID:  pgtype.UUID{Bytes: reporterID, Valid: true},
		AssigneeID:  pgtype.UUID{Bytes: assigneeID, Valid: true},
		DueAt:       pgtype.Timestamptz{Time: due, Valid: true},
		ResolvedAt:  pgtype.Timestamptz{},
		Rank:        "0|aaaaaa:",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
	}

	got := dbTicketToTicket(dbTicket)

	if got.ID != id {
		t.Errorf("ID mismatch: got %v, want %v", got.ID, id)
	}
	if got.SpaceID != spaceID {
		t.Errorf("SpaceID mismatch")
	}
	if got.Number != 1 {
		t.Errorf("Number mismatch: got %v", got.Number)
	}
	if got.Title != "Fix login bug" {
		t.Errorf("Title mismatch: got %v", got.Title)
	}
	if got.Description != "A test ticket" {
		t.Errorf("Description mismatch: got %v", got.Description)
	}
	if got.Status != tickets.StatusOpen {
		t.Errorf("Status mismatch: got %v, want %v", got.Status, tickets.StatusOpen)
	}
	if got.Priority != tickets.PriorityHigh {
		t.Errorf("Priority mismatch: got %v, want %v", got.Priority, tickets.PriorityHigh)
	}
	if got.ReporterID == nil || *got.ReporterID != reporterID {
		t.Errorf("ReporterID mismatch")
	}
	if got.AssigneeID == nil || *got.AssigneeID != assigneeID {
		t.Errorf("AssigneeID mismatch")
	}
	if got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Errorf("DueAt mismatch")
	}
	if got.ResolvedAt != nil {
		t.Errorf("expected nil ResolvedAt")
	}
	if got.Rank != "0|aaaaaa:" {
		t.Errorf("Rank mismatch: got %v", got.Rank)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt mismatch")
	}
}

func TestDbTicketToTicketNilOptionals(t *testing.T) {
	dbTicket := generated.Ticket{
		ID:         uuid.New(),
		SpaceID:    uuid.New(),
		Number:     1,
		Title:      "Minimal ticket",
		Status:     "open",
		Priority:   "medium",
		ReporterID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		AssigneeID: pgtype.UUID{},
		DueAt:      pgtype.Timestamptz{},
		ResolvedAt: pgtype.Timestamptz{},
		CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := dbTicketToTicket(dbTicket)
	if got.AssigneeID != nil {
		t.Errorf("expected nil AssigneeID, got %v", got.AssigneeID)
	}
	if got.DueAt != nil {
		t.Errorf("expected nil DueAt, got %v", got.DueAt)
	}
	if got.ResolvedAt != nil {
		t.Errorf("expected nil ResolvedAt, got %v", got.ResolvedAt)
	}
}

func TestDbTicketsToTickets(t *testing.T) {
	dbTickets := []generated.Ticket{
		{ID: uuid.New(), SpaceID: uuid.New(), Number: 1, Title: "First", Status: "open",
			Priority: "medium", ReporterID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), SpaceID: uuid.New(), Number: 2, Title: "Second", Status: "open",
			Priority: "high", ReporterID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	got := dbTicketsToTickets(dbTickets)
	if len(got) != 2 {
		t.Errorf("expected 2 tickets, got %d", len(got))
	}
}

func TestDbTicketsToTicketsEmpty(t *testing.T) {
	got := dbTicketsToTickets(nil)
	if len(got) != 0 {
		t.Errorf("expected 0 tickets for nil input, got %d", len(got))
	}
}

func TestTicketCreateParamsValidation(t *testing.T) {
	reporter := uuid.New()
	assignee := uuid.New()
	due := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	tk := &tickets.Ticket{
		ID:          uuid.New(),
		SpaceID:     uuid.New(),
		Title:       "Create test",
		Description: "Desc",
		Status:      tickets.StatusOpen,
		Priority:    tickets.PriorityHigh,
		ReporterID:  &reporter,
		AssigneeID:  &assignee,
		DueAt:       &due,
		Rank:        "0|aaa:",
	}

	// Verify the params struct we'd pass to CreateTicket.
	params := generated.CreateTicketParams{
		ID:          tk.ID,
		SpaceID:     tk.SpaceID,
		Number:      1,
		Title:       tk.Title,
		Description: tk.Description,
		Status:      string(tk.Status),
		Priority:    string(tk.Priority),
		ReporterID:  pgUUID(tk.ReporterID),
		AssigneeID:  pgUUID(tk.AssigneeID),
		DueAt:       pgTimestampPtr(tk.DueAt),
		Rank:        tk.Rank,
	}

	if params.ID != tk.ID {
		t.Errorf("ID mismatch")
	}
	if params.Title != "Create test" {
		t.Errorf("Title mismatch")
	}
	if params.Description != "Desc" {
		t.Errorf("Description mismatch")
	}
	if params.Status != "open" {
		t.Errorf("Status mismatch: got %v", params.Status)
	}
	if params.Priority != "high" {
		t.Errorf("Priority mismatch: got %v", params.Priority)
	}
	if !params.AssigneeID.Valid {
		t.Error("AssigneeID should be valid")
	}
	if !params.DueAt.Valid {
		t.Error("DueAt should be valid")
	}
}

func TestTicketCreateParamsNilOptionals(t *testing.T) {
	reporter := uuid.New()
	tk := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    uuid.New(),
		Title:      "Minimal",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: &reporter,
	}

	params := generated.CreateTicketParams{
		ID:          tk.ID,
		SpaceID:     tk.SpaceID,
		Number:      1,
		Title:       tk.Title,
		Description: tk.Description,
		Status:      string(tk.Status),
		Priority:    string(tk.Priority),
		ReporterID:  pgUUID(tk.ReporterID),
		AssigneeID:  pgUUID(tk.AssigneeID),
		DueAt:       pgTimestampPtr(tk.DueAt),
		Rank:        tk.Rank,
	}

	if params.AssigneeID.Valid {
		t.Error("AssigneeID should be invalid for nil")
	}
	if params.DueAt.Valid {
		t.Error("DueAt should be invalid for nil")
	}
}

func TestTicketUpdateParamsValidation(t *testing.T) {
	assignee := uuid.New()
	tk := &tickets.Ticket{
		ID:          uuid.New(),
		Title:       "Updated title",
		Description: "Updated desc",
		Status:      tickets.StatusInProgress,
		Priority:    tickets.PriorityUrgent,
		AssigneeID:  &assignee,
		Rank:        "0|bbb:",
	}

	params := generated.UpdateTicketParams{
		ID:          tk.ID,
		Title:       tk.Title,
		Description: tk.Description,
		Status:      string(tk.Status),
		Priority:    string(tk.Priority),
		AssigneeID:  pgUUID(tk.AssigneeID),
		DueAt:       pgTimestampPtr(tk.DueAt),
		Rank:        tk.Rank,
	}

	if params.ID != tk.ID {
		t.Errorf("ID mismatch")
	}
	if params.Title != "Updated title" {
		t.Errorf("Title mismatch")
	}
	if params.Status != "in_progress" {
		t.Errorf("Status mismatch: got %v", params.Status)
	}
	if params.Priority != "urgent" {
		t.Errorf("Priority mismatch: got %v", params.Priority)
	}
}

func TestNewTicketAdapter(t *testing.T) {
	adapter := NewTicketAdapter(nil)
	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}
}

// Verify interface compliance at compile time.
var _ tickets.TicketRepository = (*TicketAdapter)(nil)
