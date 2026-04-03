package tickets

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSearch(t *testing.T) {
	svc := NewTicketService(newMockRepo())
	ctx := context.Background()
	spaceID := uuid.New()
	reporterID := uuid.New()

	// Create tickets with different titles
	svc.Create(ctx, CreateTicketParams{
		SpaceID:     spaceID,
		Title:       "Login page not loading",
		Description: "The login page shows a blank screen",
		Priority:    PriorityHigh,
		ReporterID:  reporterID,
	})
	svc.Create(ctx, CreateTicketParams{
		SpaceID:     spaceID,
		Title:       "Password reset broken",
		Description: "Reset email never arrives",
		Priority:    PriorityMedium,
		ReporterID:  reporterID,
	})
	svc.Create(ctx, CreateTicketParams{
		SpaceID:     spaceID,
		Title:       "Dashboard performance",
		Description: "Dashboard is slow to load",
		Priority:    PriorityLow,
		ReporterID:  reporterID,
	})

	t.Run("finds matching tickets", func(t *testing.T) {
		results, err := svc.Search(ctx, spaceID, "login", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result for 'login', got %d", len(results))
		}
	})

	t.Run("searches description", func(t *testing.T) {
		results, err := svc.Search(ctx, spaceID, "email", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result for 'email', got %d", len(results))
		}
	})

	t.Run("no results", func(t *testing.T) {
		results, err := svc.Search(ctx, spaceID, "nonexistent", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("empty query", func(t *testing.T) {
		_, err := svc.Search(ctx, spaceID, "", 10)
		if err != ErrEmptySearchQuery {
			t.Errorf("expected ErrEmptySearchQuery, got %v", err)
		}
	})

	t.Run("default limit", func(t *testing.T) {
		results, err := svc.Search(ctx, spaceID, "load", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should use default limit (50), find matches for "load" in title/description
		if len(results) == 0 {
			t.Error("expected results with default limit")
		}
	})

	t.Run("different space", func(t *testing.T) {
		results, err := svc.Search(ctx, uuid.New(), "login", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for different space, got %d", len(results))
		}
	})
}
