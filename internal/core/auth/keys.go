package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
