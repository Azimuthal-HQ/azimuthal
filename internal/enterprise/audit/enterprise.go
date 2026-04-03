//go:build enterprise

// Package audit defines the AuditLogger interface and its community stub.
// This file is a compile-time placeholder; the azimuthal-ee private repo
// replaces NewLogger with the real append-only audit implementation.
package audit

import "context"

// enterpriseLogger is a compile-time placeholder that satisfies Logger
// when building with the enterprise tag in the community repository.
type enterpriseLogger struct{}

// NewLogger returns a placeholder AuditLogger for enterprise builds in the
// community repository. The azimuthal-ee private repo provides the real implementation.
func NewLogger() Logger {
	return &enterpriseLogger{}
}

// Log is a no-op placeholder. The real implementation is in azimuthal-ee.
func (e *enterpriseLogger) Log(_ context.Context, _ Event) error {
	return nil
}

// IsAvailable returns false — the real implementation is in azimuthal-ee.
func (e *enterpriseLogger) IsAvailable() bool {
	return false
}
