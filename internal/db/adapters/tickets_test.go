package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

func TestDbItemToTicket(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	spaceID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	due := now.Add(72 * time.Hour)
	desc := "A test ticket"

	dbItem := generated.Item{
		ID:          id,
		SpaceID:     spaceID,
		Kind:        "ticket",
		Title:       "Fix login bug",
		Description: &desc,
		Status:      "open",
		Priority:    "high",
		ReporterID:  reporterID,
		AssigneeID:  pgtype.UUID{Bytes: assigneeID, Valid: true},
		Labels:      []string{"bug", "auth"},
		DueAt:       pgtype.Timestamptz{Time: due, Valid: true},
		ResolvedAt:  pgtype.Timestamptz{},
		Rank:        "0|aaaaaa:",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
	}

	got := dbItemToTicket(dbItem)

	if got.ID != id {
		t.Errorf("ID mismatch: got %v, want %v", got.ID, id)
	}
	if got.SpaceID != spaceID {
		t.Errorf("SpaceID mismatch")
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
	if got.ReporterID != reporterID {
		t.Errorf("ReporterID mismatch")
	}
	if got.AssigneeID == nil || *got.AssigneeID != assigneeID {
		t.Errorf("AssigneeID mismatch")
	}
	if len(got.Labels) != 2 || got.Labels[0] != "bug" {
		t.Errorf("Labels mismatch: got %v", got.Labels)
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

func TestDbItemToTicketNilOptionals(t *testing.T) {
	dbItem := generated.Item{
		ID:         uuid.New(),
		SpaceID:    uuid.New(),
		Kind:       "ticket",
		Title:      "Minimal ticket",
		Status:     "open",
		Priority:   "medium",
		ReporterID: uuid.New(),
		AssigneeID: pgtype.UUID{},
		DueAt:      pgtype.Timestamptz{},
		ResolvedAt: pgtype.Timestamptz{},
		CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := dbItemToTicket(dbItem)
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

func TestFilterTickets(t *testing.T) {
	items := []generated.Item{
		{ID: uuid.New(), Kind: "ticket", SpaceID: uuid.New(), ReporterID: uuid.New(),
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), Kind: "task", SpaceID: uuid.New(), ReporterID: uuid.New(),
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), Kind: "ticket", SpaceID: uuid.New(), ReporterID: uuid.New(),
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), Kind: "bug", SpaceID: uuid.New(), ReporterID: uuid.New(),
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	got := filterTickets(items)
	if len(got) != 2 {
		t.Errorf("expected 2 tickets, got %d", len(got))
	}
	for _, tk := range got {
		if tk.Status != "" && string(tk.Status) == "task" {
			t.Error("non-ticket item leaked through filter")
		}
	}
}

func TestFilterTicketsEmpty(t *testing.T) {
	got := filterTickets(nil)
	if len(got) != 0 {
		t.Errorf("expected 0 tickets for nil input, got %d", len(got))
	}
}

func TestTicketToCreateParams(t *testing.T) {
	assignee := uuid.New()
	due := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	tk := &tickets.Ticket{
		ID:          uuid.New(),
		SpaceID:     uuid.New(),
		Title:       "Create test",
		Description: "Desc",
		Status:      tickets.StatusOpen,
		Priority:    tickets.PriorityHigh,
		ReporterID:  uuid.New(),
		AssigneeID:  &assignee,
		Labels:      []string{"bug"},
		DueAt:       &due,
		Rank:        "0|aaa:",
	}

	got := ticketToCreateParams(tk)

	if got.ID != tk.ID {
		t.Errorf("ID mismatch")
	}
	if got.Kind != "ticket" {
		t.Errorf("Kind should be 'ticket', got %v", got.Kind)
	}
	if got.Title != "Create test" {
		t.Errorf("Title mismatch")
	}
	if got.Description == nil || *got.Description != "Desc" {
		t.Errorf("Description mismatch")
	}
	if got.Status != "open" {
		t.Errorf("Status mismatch: got %v", got.Status)
	}
	if got.Priority != "high" {
		t.Errorf("Priority mismatch: got %v", got.Priority)
	}
	if !got.AssigneeID.Valid {
		t.Error("AssigneeID should be valid")
	}
	if len(got.Labels) != 1 || got.Labels[0] != "bug" {
		t.Errorf("Labels mismatch: got %v", got.Labels)
	}
	if !got.DueAt.Valid {
		t.Error("DueAt should be valid")
	}
}

func TestTicketToCreateParamsNilOptionals(t *testing.T) {
	tk := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    uuid.New(),
		Title:      "Minimal",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: uuid.New(),
	}

	got := ticketToCreateParams(tk)
	if got.AssigneeID.Valid {
		t.Error("AssigneeID should be invalid for nil")
	}
	if got.DueAt.Valid {
		t.Error("DueAt should be invalid for nil")
	}
}

func TestTicketToUpdateParams(t *testing.T) {
	assignee := uuid.New()
	tk := &tickets.Ticket{
		ID:          uuid.New(),
		Title:       "Updated title",
		Description: "Updated desc",
		Status:      tickets.StatusInProgress,
		Priority:    tickets.PriorityUrgent,
		AssigneeID:  &assignee,
		Labels:      []string{"feature", "urgent"},
		Rank:        "0|bbb:",
	}

	got := ticketToUpdateParams(tk)
	if got.ID != tk.ID {
		t.Errorf("ID mismatch")
	}
	if got.Title != "Updated title" {
		t.Errorf("Title mismatch")
	}
	if got.Status != "in_progress" {
		t.Errorf("Status mismatch: got %v", got.Status)
	}
	if got.Priority != "urgent" {
		t.Errorf("Priority mismatch: got %v", got.Priority)
	}
	if len(got.Labels) != 2 {
		t.Errorf("Labels mismatch: got %v", got.Labels)
	}
}

// Verify interface compliance at compile time.
var _ tickets.TicketRepository = (*TicketAdapter)(nil)
