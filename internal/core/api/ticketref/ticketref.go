// Package ticketref carries the operator-supplied ticket reference that
// administrative mutations record on their audit events.
//
// # One transport, every method
//
// The reference travels as the `ticket_ref` query parameter. A JSON body
// field would not reach the mutations that carry no body at all — team
// delete, space delete, member removal and person removal are all DELETE —
// and accepting the field in two places would be a second implementation of
// one surface, which docs/design/shared-surfaces.md forbids.
//
// The bulk-grants apply endpoint keeps its shipped body field. It is a
// published contract with clients already sending it, and it is a POST that
// always carries a body, so it never needed the query parameter. Both paths
// share the length cap and the required-mode check defined here — that is
// what stops them drifting apart.
//
// # No foreign key, ever
//
// The reference is free text and stays free text (locked decision, recorded
// against migration 025). The audit log is self-contained: a reference must
// survive the deletion of whatever it points at, and must accept a reference
// to a tracker Azimuthal has never heard of.
package ticketref

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

// MaxLen bounds the stored reference. The column is unconstrained TEXT; this
// is the API's own cap, so an accidental paste of a whole page cannot become
// an audit row.
const MaxLen = 200

// QueryParam is the wire name of the reference, lowercase snake_case per the
// project's wire-format rule.
const QueryParam = "ticket_ref"

// FromRequest returns the trimmed ticket_ref query parameter, or "" when the
// caller supplied none.
func FromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get(QueryParam))
}

// Policy is the boot-time ticket-reference requirement
// (AZIMUTHAL_TICKET_REF_REQUIRED). The zero value is the default posture:
// references are optional and behaviour is exactly as it was before the flag
// existed.
//
// Deliberately a value type, not a pointer: a handler that was never given a
// policy gets the permissive zero value rather than a nil dereference, and
// "no policy configured" cannot silently mean "requirement disabled" on a
// deployment that asked for it — the flag is read once at boot in
// cmd/server/main.go and threaded to every handler that accepts a reference.
type Policy struct {
	// Required makes a non-empty reference mandatory on every administrative
	// mutation that accepts one. Changing it requires a restart; that is the
	// design, for a deliberate production cutover.
	Required bool
}

// Check validates ref under the policy, writing the 400 response itself.
// It returns ok=false when the caller must stop and write nothing.
//
// Both failure modes are 400 rather than 403: neither is about authority.
// An over-long or missing reference is a malformed request, and the message
// says exactly what to do about it.
func (p Policy) Check(w http.ResponseWriter, r *http.Request, ref string) bool {
	if len(ref) > MaxLen {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"ticket_ref must be 200 characters or fewer")
		return false
	}
	// Rejected here or PostgreSQL rejects it later, and "later" is far worse.
	// A query parameter carries raw percent-decoded bytes, so `?ticket_ref=%FF`
	// or `%00` reaches this point as a perfectly ordinary non-empty Go string.
	// It only fails at the audit INSERT, where the column is `text` — and by
	// then the mutation has committed, the response has been decided, and
	// audit.Logger's contract is to swallow the error rather than interrupt
	// the request. The result would be a completed administrative change with
	// no audit row: one query parameter, and the record disappears. That
	// inverts the entire point of requiring a reference.
	if !utf8.ValidString(ref) || strings.ContainsRune(ref, 0) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"ticket_ref must be valid UTF-8 and contain no NUL bytes")
		return false
	}
	if p.Required && ref == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation,
			"ticket_ref is required: this organisation requires a ticket reference on every administrative change")
		return false
	}
	return true
}

// Resolve reads the reference from the request and validates it in one step —
// the shape every handler wants. Returns ok=false once the error response has
// been written.
func (p Policy) Resolve(w http.ResponseWriter, r *http.Request) (string, bool) {
	ref := FromRequest(r)
	if !p.Check(w, r, ref) {
		return "", false
	}
	return ref, true
}
