package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoSigningKey is returned by a SigningKeyStore when no key is stored yet.
var ErrNoSigningKey = errors.New("no signing key stored")

// SigningKeyStore persists the RS256 JWT signing key. The database-backed
// implementation lives in internal/db/adapters; keys must survive process
// and container restarts, so environment variables and ephemeral files are
// not acceptable stores.
type SigningKeyStore interface {
	// GetPrivateKeyPEM returns the stored key, or ErrNoSigningKey when the
	// store is empty.
	GetPrivateKeyPEM(ctx context.Context) (string, error)
	// InsertPrivateKeyPEM stores a key if and only if none is stored yet;
	// losing a concurrent race is not an error.
	InsertPrivateKeyPEM(ctx context.Context, pemData string) error
}

// pemBlockTypeRSAPrivateKey is the PEM block type used when persisting the
// JWT signing key. PKCS#1 keeps the file readable by openssl and Go alike.
const pemBlockTypeRSAPrivateKey = "RSA PRIVATE KEY"

// LoadOrGenerateRSAKey returns an RSA key for signing JWTs. If path is empty
// the key is generated in memory and not persisted (useful in tests). If path
// points to an existing PEM file, it is read and parsed. If the file does not
// exist, a new key is generated, written to path with mode 0600, and returned.
// On any other error (unreadable file, bad PEM) the function returns the
// underlying error so the caller can fail loudly rather than silently
// regenerating and invalidating live tokens.
func LoadOrGenerateRSAKey(path string) (*rsa.PrivateKey, error) {
	if path == "" {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("generating RSA key: %w", err)
		}
		return key, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304 — path is operator-supplied configuration
	if err == nil {
		key, parseErr := parseRSAPrivateKeyPEM(data)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing RSA private key from %q: %w", path, parseErr)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading RSA private key %q: %w", path, err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating RSA key: %w", err)
	}
	if err := writeRSAPrivateKeyPEM(path, key); err != nil {
		return nil, err
	}
	return key, nil
}

// EnsureSigningKey returns the RS256 signing key from the store, creating it
// on first boot. When the store is empty and importPath names an existing PEM
// file (a deployment upgrading from the legacy file-based key), that key is
// imported so existing tokens stay valid. Concurrent first boots are safe:
// the insert is first-writer-wins and the winning key is re-read, so every
// instance ends up with the same key.
func EnsureSigningKey(ctx context.Context, store SigningKeyStore, importPath string) (*rsa.PrivateKey, error) {
	pemStr, err := store.GetPrivateKeyPEM(ctx)
	if err == nil {
		key, parseErr := parseRSAPrivateKeyPEM([]byte(pemStr))
		if parseErr != nil {
			return nil, fmt.Errorf("parsing stored signing key: %w", parseErr)
		}
		return key, nil
	}
	if !errors.Is(err, ErrNoSigningKey) {
		return nil, fmt.Errorf("loading signing key: %w", err)
	}

	key, err := importOrGenerateSigningKey(importPath)
	if err != nil {
		return nil, err
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  pemBlockTypeRSAPrivateKey,
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := store.InsertPrivateKeyPEM(ctx, string(pemBytes)); err != nil {
		return nil, fmt.Errorf("storing signing key: %w", err)
	}

	// Re-read: a concurrent instance may have won the insert race, and its
	// key — not ours — is the persisted truth.
	pemStr, err = store.GetPrivateKeyPEM(ctx)
	if err != nil {
		return nil, fmt.Errorf("re-reading signing key after store: %w", err)
	}
	winner, err := parseRSAPrivateKeyPEM([]byte(pemStr))
	if err != nil {
		return nil, fmt.Errorf("parsing stored signing key: %w", err)
	}
	return winner, nil
}

// importOrGenerateSigningKey reads the legacy PEM file at importPath when it
// exists (upgrade path from file-based keys), otherwise generates a new key.
func importOrGenerateSigningKey(importPath string) (*rsa.PrivateKey, error) {
	if importPath != "" {
		data, readErr := os.ReadFile(importPath) //nolint:gosec // G304 — path is operator-supplied configuration
		switch {
		case readErr == nil:
			key, err := parseRSAPrivateKeyPEM(data)
			if err != nil {
				return nil, fmt.Errorf("importing legacy signing key from %q: %w", importPath, err)
			}
			return key, nil
		case !errors.Is(readErr, os.ErrNotExist):
			return nil, fmt.Errorf("reading legacy signing key %q: %w", importPath, readErr)
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating signing key: %w", err)
	}
	return key, nil
}

// parseRSAPrivateKeyPEM decodes a PKCS#1 RSA private key from PEM bytes.
func parseRSAPrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if block.Type != pemBlockTypeRSAPrivateKey {
		return nil, fmt.Errorf("unexpected PEM block type %q", block.Type)
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKCS#1 private key: %w", err)
	}
	return key, nil
}

// writeRSAPrivateKeyPEM persists a private key to path with mode 0600,
// creating the parent directory tree if needed.
func writeRSAPrivateKeyPEM(path string, key *rsa.PrivateKey) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating directory for RSA key %q: %w", dir, err)
		}
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  pemBlockTypeRSAPrivateKey,
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return fmt.Errorf("writing RSA private key to %q: %w", path, err)
	}
	return nil
}
