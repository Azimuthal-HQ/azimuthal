package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// withPasswordCost pins the work factor for one test and restores it after.
//
// The tests that do not call it run at whatever a test binary boots with —
// bcrypt.MinCost — and that is the point of known-issues #18: their
// assertions (hash prefix, unique salts, match and mismatch semantics) hold
// at every work factor, so paying 3.2s per operation under -race to check
// them bought nothing. None of those assertions is weakened here; only the
// price of the fixture changed. The production work factor keeps its own
// test, below, which pays full price exactly once.
func withPasswordCost(t *testing.T, cost int) {
	t.Helper()
	previous := passwordCost.Swap(int64(cost))
	t.Cleanup(func() { passwordCost.Store(previous) })
}

// TestHashPassword_ProductionCostIsTwelve is the one test that pays the real
// work factor, and it reads the cost back out of the emitted hash rather than
// comparing a constant to itself — bcrypt.Cost parses what was actually used.
func TestHashPassword_ProductionCostIsTwelve(t *testing.T) {
	withPasswordCost(t, DefaultBcryptCost)

	hash, err := HashPassword("supersecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("parsing cost from hash: %v", err)
	}
	if cost != 12 {
		t.Errorf("production hashes must carry cost 12, got %d", cost)
	}
	// A production-shaped hash must also verify, at full price.
	if err := ComparePassword(hash, "supersecret"); err != nil {
		t.Errorf("a cost-12 hash must verify: %v", err)
	}
}

// TestSetPasswordCost_RefusesBelowProductionFloor is the floor's negative
// test. Delete the bounds check in SetPasswordCost and every refusal row
// fails three ways: the error goes nil, the stored cost moves, and the next
// hash carries the lowered cost.
func TestSetPasswordCost_RefusesBelowProductionFloor(t *testing.T) {
	// Deliberately NOT DefaultBcryptCost: each row hashes once to prove the
	// cost did not move, and six cost-12 hashes would cost ~19s under -race —
	// a fifth of the saving this change exists to make, spent asserting
	// something a cheap distinct baseline proves just as well. baseline is one
	// above bcrypt.MinCost so that a successfully-refused SetPasswordCost(4)
	// would still be visible as a change.
	baseline := bcrypt.MinCost + 1

	refused := []int{-1, 0, bcrypt.MinCost, 11, bcrypt.MaxCost + 1, 99}
	for _, cost := range refused {
		withPasswordCost(t, baseline)
		before := PasswordCost()

		if err := SetPasswordCost(cost); err == nil {
			t.Errorf("SetPasswordCost(%d) must be refused", cost)
		}
		if got := PasswordCost(); got != before {
			t.Errorf("SetPasswordCost(%d) was refused but moved the cost to %d", cost, got)
		}
		hash, err := HashPassword("x")
		if err != nil {
			t.Fatalf("hashing after a refused set: %v", err)
		}
		emitted, err := bcrypt.Cost([]byte(hash))
		if err != nil {
			t.Fatalf("parsing cost from hash: %v", err)
		}
		if emitted != before {
			t.Errorf("SetPasswordCost(%d) was refused but the next hash used cost %d", cost, emitted)
		}
	}

	for _, cost := range []int{MinBcryptCost, 13, bcrypt.MaxCost} {
		withPasswordCost(t, bcrypt.MinCost+1)
		if err := SetPasswordCost(cost); err != nil {
			t.Errorf("SetPasswordCost(%d) must be accepted, got %v", cost, err)
		}
		if got := PasswordCost(); got != cost {
			t.Errorf("SetPasswordCost(%d) accepted but the cost is %d", cost, got)
		}
	}
}

// TestPasswordCost_TestBinaryBootsAtMinCost locks in the saving itself.
// Without it, deleting the testing.Testing() branch would leave every test
// green and quietly hand the CI job back its three minutes.
//
// It asserts the OBSERVABLE property — the cost carried by a hash this binary
// actually produces — rather than the value of the package variable, because
// sibling tests in this file move that variable on purpose and an assertion
// about it would be about test ordering as much as about the code.
//
// Honest limitation, stated because the name could be read as more than it
// is: the `go build` branch cannot be reached from a test binary, since
// testing.Testing() is true here by definition. The second half swaps the
// predicate to prove the branch computes the production default. It does not,
// and cannot, prove what the linker does — and it is not the only thing
// standing between production and a weak hash either. That is loadConfig
// calling SetPasswordCost, which has its own test in cmd/server.
func TestPasswordCost_TestBinaryBootsAtMinCost(t *testing.T) {
	hash, err := HashPassword("boot-cost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("parsing cost from hash: %v", err)
	}
	if cost != bcrypt.MinCost {
		t.Errorf("a test binary must hash at bcrypt.MinCost (%d), got %d — known-issues #18 has regressed",
			bcrypt.MinCost, cost)
	}

	previous := isTestBinary
	isTestBinary = func() bool { return false }
	t.Cleanup(func() { isTestBinary = previous })

	if got := initialPasswordCost(); got != DefaultBcryptCost {
		t.Errorf("a non-test binary must boot at DefaultBcryptCost (%d), got %d", DefaultBcryptCost, got)
	}
	if DefaultBcryptCost < MinBcryptCost {
		t.Errorf("the default (%d) must never sit below the floor (%d)", DefaultBcryptCost, MinBcryptCost)
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("supersecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash == "supersecret" {
		t.Fatal("hash must not equal plaintext")
	}
	// bcrypt hashes begin with $2a$ or $2b$
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash prefix, got: %s", hash[:4])
	}
}

func TestHashPassword_DifferentEachTime(t *testing.T) {
	h1, err := HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("bcrypt should produce unique salts for identical inputs")
	}
}

func TestComparePassword_Match(t *testing.T) {
	hash, err := HashPassword("correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := ComparePassword(hash, "correct"); err != nil {
		t.Errorf("expected match, got: %v", err)
	}
}

func TestComparePassword_Mismatch(t *testing.T) {
	hash, err := HashPassword("correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := ComparePassword(hash, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got: %v", err)
	}
}

func TestComparePassword_InvalidHash(t *testing.T) {
	if err := ComparePassword("not-a-bcrypt-hash", "password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for invalid hash, got: %v", err)
	}
}
