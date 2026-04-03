package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// generateToken creates a cryptographically random 32-byte hex token string.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
