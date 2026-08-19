package credlink

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestHashToken_IsSHA256Hex pins the storage-hash construction: SHA-256 as
// lowercase hex, deterministic, and never the raw token. This is the property
// the whole "only the digest is stored" guarantee rests on.
func TestHashToken_IsSHA256Hex(t *testing.T) {
	raw := "some-raw-token-value"
	h := HashToken(raw)
	if len(h) != 64 {
		t.Fatalf("SHA-256 hex must be 64 chars, got %d (%q)", len(h), h)
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Fatalf("hash must be hex: %v", err)
	}
	if h != HashToken(raw) {
		t.Fatal("HashToken must be deterministic")
	}
	if strings.Contains(h, raw) {
		t.Fatal("the hash must not contain the raw token")
	}
	if HashToken("a") == HashToken("b") {
		t.Fatal("distinct inputs must hash differently")
	}
}

// TestGenerateToken_UniqueRawURLSafeAndHashed: 32 bytes of randomness as URL-safe
// base64 (43 chars), a matching hash, and no two calls alike.
func TestGenerateToken_UniqueRawURLSafeAndHashed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		raw, hash, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if len(raw) != 43 { // 32 bytes -> 43 base64url chars, unpadded
			t.Fatalf("raw token must be 43 chars, got %d (%q)", len(raw), raw)
		}
		if _, err := base64.RawURLEncoding.DecodeString(raw); err != nil {
			t.Fatalf("raw token must be URL-safe base64: %v", err)
		}
		if hash != HashToken(raw) {
			t.Fatal("the returned hash must be HashToken(raw)")
		}
		if seen[raw] {
			t.Fatal("generateToken must not repeat a token")
		}
		seen[raw] = true
	}
}

func TestPurpose_Valid(t *testing.T) {
	for _, p := range []Purpose{PurposeSignIn, PurposePasswordReset, PurposeEmailChange} {
		if !p.Valid() {
			t.Errorf("%q must be valid", p)
		}
	}
	for _, p := range []Purpose{"", "signout", "SIGNIN", "reset"} {
		if Purpose(p).Valid() {
			t.Errorf("%q must be invalid", p)
		}
	}
}

// TestNewService_DefaultsTTL: a non-positive TTL falls back to sixty minutes, so
// a caller that forgot to set it never mints a link that is born expired.
func TestNewService_DefaultsTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Hour} {
		s := NewService(nil, nil, nil, Config{TTL: ttl})
		if s.cfg.TTL != 60*time.Minute {
			t.Errorf("TTL %v must default to 60m, got %v", ttl, s.cfg.TTL)
		}
	}
	s := NewService(nil, nil, nil, Config{TTL: 30 * time.Minute})
	if s.cfg.TTL != 30*time.Minute {
		t.Errorf("an explicit TTL must be kept, got %v", s.cfg.TTL)
	}
}
