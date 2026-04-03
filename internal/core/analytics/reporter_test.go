package analytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/analytics"
)

func TestDefaultReporter_IsAvailable(t *testing.T) {
	r := analytics.NewReporter()
	if r.IsAvailable() {
		t.Error("default reporter should report IsAvailable() == false")
	}
}

func TestDefaultReporter_OrgSummary_ReturnsNotImplemented(t *testing.T) {
	r := analytics.NewReporter()
	now := time.Now()
	summary, err := r.OrgSummary(context.Background(), "org-123", now.Add(-24*time.Hour), now)
	if err == nil {
		t.Fatal("expected ErrNotImplemented, got nil")
	}
	if !errors.Is(err, analytics.ErrNotImplemented) {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
	if summary != nil {
		t.Errorf("expected nil summary, got %+v", summary)
	}
}

func TestDefaultReporter_UserActivity_ReturnsNotImplemented(t *testing.T) {
	r := analytics.NewReporter()
	now := time.Now()
	activity, err := r.UserActivity(context.Background(), "org-123", now.Add(-24*time.Hour), now)
	if err == nil {
		t.Fatal("expected ErrNotImplemented, got nil")
	}
	if !errors.Is(err, analytics.ErrNotImplemented) {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
	if activity != nil {
		t.Errorf("expected nil activity slice, got %+v", activity)
	}
}

func TestDefaultReporter_ImplementsInterface(_ *testing.T) {
	var _ = analytics.NewReporter()
}
