package auth

import "errors"

// Sentinel errors for the auth package.
var (
	// ErrNotFound is returned when a user or session cannot be located.
	ErrNotFound = errors.New("not found")

	// ErrEmailTaken is returned when attempting to create a user with an
	// email address that is already registered.
	ErrEmailTaken = errors.New("email address already in use")

	// ErrInvalidCredentials is returned when a login attempt fails due to
	// a wrong password or unknown email.
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrInvalidToken is returned when a JWT cannot be parsed or its
	// signature is invalid.
	ErrInvalidToken = errors.New("invalid or expired token")

	// ErrSessionExpired is returned when a valid session has passed its
	// expiry time.
	ErrSessionExpired = errors.New("session has expired")

	// ErrAccountInactive is returned when the user's account has been
	// deactivated.
	ErrAccountInactive = errors.New("account is inactive")
)
