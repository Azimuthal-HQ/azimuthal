package portal

import "errors"

// Sentinel errors for the customer portal.
//
// A note on what is deliberately NOT distinguishable here. The public
// endpoints refuse in a way that reveals nothing about which requesters,
// portals or requests exist:
//
//   - Requesting a magic link succeeds identically for a known and an unknown
//     address. There is no ErrRequesterNotFound on that path because the
//     handler never asks the question. This matches Login, which maps both a
//     wrong password and a deactivated account to one 401 body.
//
//   - ErrRequestNotFound covers both "no such request" and "somebody else's
//     request", and the handler answers 404 for it. A 403 would confirm the
//     request exists, which is the §2.6 rule ("valid credentials, no access →
//     404 — never 403, do not leak existence") applied to an external reader.
var (
	// ErrInvalidSession is the single collapsed failure for every way a portal
	// session token can be unacceptable: malformed, wrong audience, wrong
	// issuer, wrong type, expired, or minted against a stale
	// session_generation. Collapsed on purpose — telling a caller WHICH check
	// failed tells an attacker which one to work on.
	ErrInvalidSession = errors.New("invalid or expired portal session")

	// ErrInvalidLink is returned when a magic link cannot be redeemed. It
	// covers unknown, already-consumed, superseded and expired links, for the
	// same reason ErrInvalidSession is collapsed.
	ErrInvalidLink = errors.New("invalid or expired sign-in link")

	// ErrPortalNotFound is returned when no enabled portal has the given key.
	// It does not distinguish "no such portal" from "portal disabled".
	ErrPortalNotFound = errors.New("portal not found")

	// ErrPortalExists is returned when a space already has a portal.
	ErrPortalExists = errors.New("space already has a portal")

	// ErrNotBeaconSpace is returned when a portal is requested for a space
	// that is not a Beacon service desk.
	ErrNotBeaconSpace = errors.New("portals are only available on Beacon spaces")

	// ErrRequesterInactive is returned when a deactivated requester attempts
	// to sign in. Never surfaced on the request-link path, which must not
	// distinguish it from success.
	ErrRequesterInactive = errors.New("requester is not active")

	// ErrRequestNotFound covers both "no such request" and "not this
	// requester's request". The handler answers 404 for it.
	ErrRequestNotFound = errors.New("request not found")

	// ErrInvalidEmail is returned when an address fails net/mail parsing.
	ErrInvalidEmail = errors.New("invalid email address")

	// ErrSummaryRequired is returned when a submitted request carries no
	// summary.
	ErrSummaryRequired = errors.New("summary is required")

	// ErrBodyRequired is returned when a reply carries no text.
	ErrBodyRequired = errors.New("reply text is required")

	// ErrPortalNameRequired is returned when a portal is created without a
	// display name.
	ErrPortalNameRequired = errors.New("portal name is required")
)
