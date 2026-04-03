//go:build !enterprise

package audit

import "context"

// stubLogger is the community-edition no-op audit logger.
// All events are silently discarded.
type stubLogger struct{}

// NewLogger returns the community no-op AuditLogger.
// In enterprise builds this function is replaced by the real append-only implementation.
func NewLogger() AuditLogger {
	return &stubLogger{}
}

// Log is a no-op in the community edition. Events are silently discarded.
func (s *stubLogger) Log(_ context.Context, _ AuditEvent) error {
	return nil
}

// IsAvailable always returns false in the community edition.
func (s *stubLogger) IsAvailable() bool {
	return false
}
