// Package adapters bridges the domain repository interfaces (auth, tickets,
// projects) to the sqlc-generated query layer. It handles type conversions
// between domain types (time.Time, *uuid.UUID) and database types
// (pgtype.Timestamptz, pgtype.UUID), and resolves design mismatches such as
// the OrgID field gap and session token hashing.
package adapters
