package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// SigningKeyAdapter implements auth.SigningKeyStore over the singleton
// auth_signing_keys row.
type SigningKeyAdapter struct {
	q *generated.Queries
}

// NewSigningKeyAdapter creates a SigningKeyAdapter.
func NewSigningKeyAdapter(q *generated.Queries) *SigningKeyAdapter {
	return &SigningKeyAdapter{q: q}
}

// GetPrivateKeyPEM returns the stored signing key, or auth.ErrNoSigningKey
// when none has been stored yet.
func (a *SigningKeyAdapter) GetPrivateKeyPEM(ctx context.Context) (string, error) {
	pemStr, err := a.q.GetSigningKey(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", auth.ErrNoSigningKey
	}
	if err != nil {
		return "", fmt.Errorf("signing key adapter get: %w", err)
	}
	return pemStr, nil
}

// InsertPrivateKeyPEM stores the key unless one already exists; losing the
// first-writer race is not an error (the caller re-reads the winner).
func (a *SigningKeyAdapter) InsertPrivateKeyPEM(ctx context.Context, pemData string) error {
	if _, err := a.q.InsertSigningKey(ctx, pemData); err != nil {
		return fmt.Errorf("signing key adapter insert: %w", err)
	}
	return nil
}
