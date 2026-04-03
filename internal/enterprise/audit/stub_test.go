//go:build !enterprise

package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/audit"
)

func TestStubLogger_IsAvailable(t *testing.T) {
	l := audit.NewLogger()
	if l.IsAvailable() {
		t.Error("community stub should report IsAvailable() == false")
	}
}

func TestStubLogger_Log_IsNoOp(t *testing.T) {
	l := audit.NewLogger()
	event := audit.AuditEvent{
		Type:         audit.EventTypeUserLogin,
		ActorID:      "user-123",
		OrgID:        "org-456",
		ResourceType: "session",
		ResourceID:   "sess-789",
		Metadata:     map[string]string{"ip": "127.0.0.1"},
		OccurredAt:   time.Now(),
	}

	// Must not return an error — no-op implementation.
	if err := l.Log(context.Background(), event); err != nil {
		t.Errorf("stub Log() must be a no-op, got error: %v", err)
	}
}

func TestStubLogger_ImplementsInterface(t *testing.T) {
	var _ audit.AuditLogger = audit.NewLogger()
}
