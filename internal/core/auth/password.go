package auth

import (
	"fmt"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// MinBcryptCost is the work factor floor. Twelve, per project security
// policy, and it is a floor rather than a default: SetPasswordCost refuses
// anything below it, and internal/config refuses to load a configuration that
// asks for less. Raising it is an operator's decision; lowering it is nobody's.
const MinBcryptCost = 12

// DefaultBcryptCost is what a server hashes at when the operator configures
// nothing. Equal to the floor today, and deliberately a separate name: the
// day hardware makes 12 too cheap the default rises and the floor rises with
// it, but they are different statements.
const DefaultBcryptCost = 12

// passwordCost is the work factor HashPassword uses.
//
// # Why this is a variable at all
//
// It was `const bcryptCost = 12`, and that constant was the single largest
// item in the CI test job. Cost 12 costs ~0.16s per operation on a developer
// machine and about 3.2s under -race — a 20x multiplier — and the Go suite
// performs several dozen runtime hashes, so roughly 200s of every CI run went
// on proving bcrypt is slow. That is known-issues #18.
//
// # Why lowering it cannot reach production
//
// The initial value is chosen by testing.Testing(), which reports whether
// this binary was built by `go test`. It reads a variable the linker sets
// when it builds a test binary; no environment variable, build tag or flag is
// involved, so a binary produced by `go build` takes the production branch by
// construction. There is nothing to forget and nothing to misconfigure.
//
// The only other way to move it is SetPasswordCost, which enforces the floor.
// The reachable set for a shipped binary is therefore [12, 31], and the low
// cost exists solely inside test binaries.
//
// atomic.Int64 rather than a plain int: nothing sets the cost concurrently
// today, but a hash is a read and -race would flag the first test that ever
// ran one in parallel with a set. An atomic load next to a bcrypt round is
// free by any measure.
var passwordCost atomic.Int64

// isTestBinary reports whether this process was built by `go test`. It is a
// variable only so this package's own tests can make it lie and exercise the
// production branch of init — which could not otherwise be reached at all,
// because inside a test binary the real answer is always true. Nothing
// outside those tests writes it.
var isTestBinary = testing.Testing

func init() {
	passwordCost.Store(int64(initialPasswordCost()))
}

// initialPasswordCost is the boot value of the work factor: the cheapest cost
// bcrypt allows in a test binary, the production default in anything else.
// The tests that exist to prove the production work factor raise it
// explicitly for their own duration — see password_test.go.
func initialPasswordCost() int {
	if isTestBinary() {
		return bcrypt.MinCost
	}
	return DefaultBcryptCost
}

// SetPasswordCost sets the bcrypt work factor, refusing anything below
// MinBcryptCost or above bcrypt.MaxCost. cmd/server calls it once at boot
// with the configured value; there is no other production caller.
//
// The floor is enforced here as well as in internal/config on purpose. The
// config check is what an operator sees — a clear startup error naming the
// environment variable — and this one is what a future caller that never goes
// through config sees. Neither is redundant: a security floor with one
// enforcement point is a security floor with one place to forget it.
func SetPasswordCost(cost int) error {
	if cost < MinBcryptCost {
		return fmt.Errorf("bcrypt cost %d is below the minimum of %d", cost, MinBcryptCost)
	}
	if cost > bcrypt.MaxCost {
		return fmt.Errorf("bcrypt cost %d is above the maximum of %d", cost, bcrypt.MaxCost)
	}
	passwordCost.Store(int64(cost))
	return nil
}

// PasswordCost reports the work factor currently in use. Exported for the
// tests that assert on the configured posture; production has no reason to ask.
func PasswordCost() int {
	return int(passwordCost.Load())
}

// HashPassword hashes a plaintext password using bcrypt at the configured
// work factor — never below MinBcryptCost in any binary that was not built by
// `go test`.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), int(passwordCost.Load()))
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// ComparePassword checks whether a plaintext password matches a bcrypt hash.
// Returns nil on match, ErrInvalidCredentials on mismatch.
//
// The configured work factor plays no part here: bcrypt encodes the cost in
// the hash, so a stored hash keeps verifying at the cost it was written with
// even after the configured cost changes. Raising the cost does not invalidate
// anyone's password.
func ComparePassword(hashedPassword, plainPassword string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plainPassword))
	if err != nil {
		return ErrInvalidCredentials
	}
	return nil
}
