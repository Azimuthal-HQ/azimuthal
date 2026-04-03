//go:build !enterprise

package analytics

import (
	"context"
	"time"
)

// stubReporter is the community-edition stub. Every method returns ErrEnterpriseRequired.
type stubReporter struct{}

// NewReporter returns the community stub Reporter.
// In enterprise builds this function is replaced by the real reporting engine.
func NewReporter() Reporter {
	return &stubReporter{}
}

// OrgSummary always returns ErrEnterpriseRequired in the community edition.
func (s *stubReporter) OrgSummary(_ context.Context, _ string, _, _ time.Time) (*OrgSummary, error) {
	return nil, ErrEnterpriseRequired
}

// UserActivity always returns ErrEnterpriseRequired in the community edition.
func (s *stubReporter) UserActivity(_ context.Context, _ string, _, _ time.Time) ([]UserActivitySummary, error) {
	return nil, ErrEnterpriseRequired
}

// IsAvailable always returns false in the community edition.
func (s *stubReporter) IsAvailable() bool {
	return false
}
