//go:build !enterprise

package analytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/analytics"
)

func TestStubReporter_IsAvailable(t *testing.T) {
	r := analytics.NewReporter()
	if r.IsAvailable() {
		t.Error("community stub should report IsAvailable() == false")
	}
}

func TestStubReporter_OrgSummary_ReturnsEnterpriseRequired(t *testing.T) {
	r := analytics.NewReporter()
	now := time.Now()
	summary, err := r.OrgSummary(context.Background(), "org-123", now.Add(-24*time.Hour), now)
	if err == nil {
		t.Fatal("expected ErrEnterpriseRequired, got nil")
	}
	if !errors.Is(err, analytics.ErrEnterpriseRequired) {
		t.Errorf("expected ErrEnterpriseRequired, got %v", err)
	}
	if summary != nil {
		t.Errorf("expected nil summary, got %+v", summary)
	}
}

func TestStubReporter_UserActivity_ReturnsEnterpriseRequired(t *testing.T) {
	r := analytics.NewReporter()
	now := time.Now()
	activity, err := r.UserActivity(context.Background(), "org-123", now.Add(-24*time.Hour), now)
	if err == nil {
		t.Fatal("expected ErrEnterpriseRequired, got nil")
	}
	if !errors.Is(err, analytics.ErrEnterpriseRequired) {
		t.Errorf("expected ErrEnterpriseRequired, got %v", err)
	}
	if activity != nil {
		t.Errorf("expected nil activity slice, got %+v", activity)
	}
}

func TestStubReporter_ImplementsInterface(_ *testing.T) {
	var _ = analytics.NewReporter()
}
