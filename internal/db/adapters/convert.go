package adapters

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// pgTimestamp converts a time.Time to a pgtype.Timestamptz.
func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// pgTimestampPtr converts a *time.Time to a pgtype.Timestamptz.
// A nil pointer yields an invalid (NULL) timestamptz.
func pgTimestampPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// goTime converts a pgtype.Timestamptz to a time.Time.
// An invalid (NULL) timestamptz yields the zero time.
func goTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// goTimePtr converts a pgtype.Timestamptz to a *time.Time.
// An invalid (NULL) timestamptz yields nil.
func goTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// pgUUID converts a *uuid.UUID to a pgtype.UUID.
// A nil pointer yields an invalid (NULL) pgtype.UUID.
func pgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// goUUIDPtr converts a pgtype.UUID to a *uuid.UUID.
// An invalid (NULL) pgtype.UUID yields nil.
func goUUIDPtr(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	u := uuid.UUID(id.Bytes)
	return &u
}

// strPtr returns a pointer to s.
func strPtr(s string) *string {
	return &s
}

// derefStr dereferences a *string, returning "" for nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// parseIP parses a string IP address into a *netip.Addr.
// Returns nil on parse failure or empty input.
func parseIP(s string) *netip.Addr {
	if s == "" {
		return nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return &addr
}

// ipString converts a *netip.Addr to its string representation.
// Returns "" for nil.
func ipString(a *netip.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}
