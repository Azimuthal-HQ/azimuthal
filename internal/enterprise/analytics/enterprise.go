//go:build enterprise

// Package analytics defines the AnalyticsReporter interface and its community stub.
// This file is a compile-time placeholder; the azimuthal-ee private repo
// replaces NewReporter with the real reporting engine.
package analytics

import (
	"context"
	"time"
)

// enterpriseReporter is a compile-time placeholder that satisfies AnalyticsReporter
// when building with the enterprise tag in the community repository.
type enterpriseReporter struct{}

// NewReporter returns a placeholder AnalyticsReporter for enterprise builds in the
// community repository. The azimuthal-ee private repo provides the real implementation.
func NewReporter() AnalyticsReporter {
	return &enterpriseReporter{}
}

// OrgSummary returns ErrEnterpriseRequired — the real implementation is in azimuthal-ee.
func (e *enterpriseReporter) OrgSummary(_ context.Context, _ string, _, _ time.Time) (*OrgSummary, error) {
	return nil, ErrEnterpriseRequired
}

// UserActivity returns ErrEnterpriseRequired — the real implementation is in azimuthal-ee.
func (e *enterpriseReporter) UserActivity(_ context.Context, _ string, _, _ time.Time) ([]UserActivitySummary, error) {
	return nil, ErrEnterpriseRequired
}

// IsAvailable returns false — the real implementation is in azimuthal-ee.
func (e *enterpriseReporter) IsAvailable() bool {
	return false
}
