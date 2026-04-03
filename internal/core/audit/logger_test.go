package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
)

func TestDefaultLogger_IsAvailable(t *testing.T) {
	l := audit.NewLogger()
	if l.IsAvailable() {
		t.Error("default logger should report IsAvailable() == false")
	}
}

func TestDefaultLogger_Log_IsNoOp(t *testing.T) {
	l := audit.NewLogger()
	event := audit.Event{
		Type:         audit.EventTypeUserLogin,
		ActorID:      "user-123",
		OrgID:        "org-456",
		ResourceType: "session",
		ResourceID:   "sess-789",
		Metadata:     map[string]string{"ip": "127.0.0.1"},
		OccurredAt:   time.Now(),
	}

	if err := l.Log(context.Background(), event); err != nil {
		t.Errorf("default Log() must be a no-op, got error: %v", err)
	}
}

func TestDefaultLogger_ImplementsInterface(_ *testing.T) {
	var _ = audit.NewLogger()
}
