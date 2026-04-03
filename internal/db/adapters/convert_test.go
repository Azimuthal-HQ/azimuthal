package adapters

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPgTimestamp(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	got := pgTimestamp(now)
	if !got.Valid {
		t.Fatal("expected valid timestamp")
	}
	if !got.Time.Equal(now) {
		t.Errorf("time mismatch: got %v, want %v", got.Time, now)
	}
}

func TestPgTimestampPtrNil(t *testing.T) {
	got := pgTimestampPtr(nil)
	if got.Valid {
		t.Fatal("expected invalid timestamp for nil input")
	}
}

func TestPgTimestampPtrValid(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	got := pgTimestampPtr(&now)
	if !got.Valid {
		t.Fatal("expected valid timestamp")
	}
	if !got.Time.Equal(now) {
		t.Errorf("time mismatch: got %v, want %v", got.Time, now)
	}
}

func TestGoTime(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	got := goTime(pgtype.Timestamptz{Time: now, Valid: true})
	if !got.Equal(now) {
		t.Errorf("time mismatch: got %v, want %v", got, now)
	}
}

func TestGoTimeInvalid(t *testing.T) {
	got := goTime(pgtype.Timestamptz{})
	if !got.IsZero() {
		t.Errorf("expected zero time for invalid timestamptz, got %v", got)
	}
}

func TestGoTimePtrValid(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	got := goTimePtr(pgtype.Timestamptz{Time: now, Valid: true})
	if got == nil {
		t.Fatal("expected non-nil pointer")
	}
	if !got.Equal(now) {
		t.Errorf("time mismatch: got %v, want %v", *got, now)
	}
}

func TestGoTimePtrNil(t *testing.T) {
	got := goTimePtr(pgtype.Timestamptz{})
	if got != nil {
		t.Errorf("expected nil for invalid timestamptz, got %v", *got)
	}
}

func TestPgUUID(t *testing.T) {
	id := uuid.New()
	got := pgUUID(&id)
	if !got.Valid {
		t.Fatal("expected valid UUID")
	}
	if uuid.UUID(got.Bytes) != id {
		t.Errorf("UUID mismatch: got %v, want %v", got.Bytes, id)
	}
}

func TestPgUUIDNil(t *testing.T) {
	got := pgUUID(nil)
	if got.Valid {
		t.Fatal("expected invalid UUID for nil input")
	}
}

func TestGoUUIDPtrValid(t *testing.T) {
	id := uuid.New()
	got := goUUIDPtr(pgtype.UUID{Bytes: id, Valid: true})
	if got == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *got != id {
		t.Errorf("UUID mismatch: got %v, want %v", *got, id)
	}
}

func TestGoUUIDPtrNil(t *testing.T) {
	got := goUUIDPtr(pgtype.UUID{})
	if got != nil {
		t.Errorf("expected nil for invalid pgtype.UUID")
	}
}

func TestStrPtr(t *testing.T) {
	s := "hello"
	got := strPtr(s)
	if got == nil || *got != s {
		t.Errorf("strPtr mismatch: got %v, want %v", got, s)
	}
}

func TestDerefStr(t *testing.T) {
	s := "hello"
	if got := derefStr(&s); got != s {
		t.Errorf("derefStr mismatch: got %v, want %v", got, s)
	}
	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr nil mismatch: got %v, want empty", got)
	}
}

func TestParseIP(t *testing.T) {
	got := parseIP("192.168.1.1")
	if got == nil {
		t.Fatal("expected non-nil addr")
	}
	want := netip.MustParseAddr("192.168.1.1")
	if *got != want {
		t.Errorf("IP mismatch: got %v, want %v", *got, want)
	}
}

func TestParseIPEmpty(t *testing.T) {
	if got := parseIP(""); got != nil {
		t.Errorf("expected nil for empty IP, got %v", got)
	}
}

func TestParseIPInvalid(t *testing.T) {
	if got := parseIP("not-an-ip"); got != nil {
		t.Errorf("expected nil for invalid IP, got %v", got)
	}
}

func TestIPString(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.1")
	if got := ipString(&addr); got != "10.0.0.1" {
		t.Errorf("ipString mismatch: got %v, want 10.0.0.1", got)
	}
	if got := ipString(nil); got != "" {
		t.Errorf("ipString nil mismatch: got %v, want empty", got)
	}
}
