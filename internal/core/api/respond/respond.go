// Package respond provides shared JSON response helpers for HTTP handlers.
// It lives in its own sub-package to avoid import cycles between the api
// package and its handler sub-packages.
package respond

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// ErrorCode represents a machine-readable error code returned in API responses.
type ErrorCode string

const (
	// CodeNotFound indicates the requested resource was not found.
	CodeNotFound ErrorCode = "NOT_FOUND"
	// CodeValidation indicates a request validation failure.
	CodeValidation ErrorCode = "VALIDATION_ERROR"
	// CodeUnauthorized indicates missing or invalid authentication.
	CodeUnauthorized ErrorCode = "UNAUTHORIZED"
	// CodeForbidden indicates insufficient permissions.
	CodeForbidden ErrorCode = "FORBIDDEN"
	// CodeConflict indicates a version or state conflict.
	CodeConflict ErrorCode = "CONFLICT"
	// CodeInternal indicates an unexpected server error.
	CodeInternal ErrorCode = "INTERNAL_ERROR"
	// CodeBadRequest indicates a malformed request.
	CodeBadRequest ErrorCode = "BAD_REQUEST"
	// CodeInvalidTransition indicates an invalid state transition.
	CodeInvalidTransition ErrorCode = "INVALID_TRANSITION"
)

type ctxKey int

const (
	// CtxKeyRequestID is the context key for the request ID.
	CtxKeyRequestID ctxKey = iota
)

// errorBody is the top-level JSON wrapper for all error responses.
type errorBody struct {
	Error errorDetail `json:"error"`
}

// errorDetail is the structured error object inside every error response.
type errorDetail struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id,omitempty"`
}

// RequestIDFromContext returns the request ID from the context, or empty string.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(CtxKeyRequestID).(string)
	return id
}

// Error writes a structured JSON error response. It pulls the request ID
// from the context (set by the RequestID middleware) if available.
func Error(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, msg string) {
	reqID := RequestIDFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := errorBody{
		Error: errorDetail{
			Code:      code,
			Message:   msg,
			RequestID: reqID,
		},
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writing error response", "error", err)
	}
}

// JSON encodes v as JSON to w with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing json response", "error", err)
	}
}

// DecodeJSON reads the request body into dst. Returns an error if the body is
// invalid JSON or empty.
func DecodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decoding request body: %w", err)
	}
	return nil
}

// OptionalField distinguishes "the client did not mention this field" from
// "the client explicitly sent null". encoding/json only calls UnmarshalJSON
// when the key is present, so Set is false for an absent key and true for
// both a null and a real value.
//
// A nullable PATCH field needs those three states, not two. A single pointer
// collapses absent and null, and this repository has already resolved that
// collision as "clear it" and destroyed data with it: every project-item edit
// wiped the stored due date, because no surface sent due_at and absent read as
// null. See updateItemRequest in internal/core/api/projects.
//
// It lives here, beside DecodeJSON, because it is decode-only and both the
// Beacon and Vector PATCH bodies need the same three states. A second copy is
// how the two modules drift apart on the semantics that matter most.
//
// The json tags on fields of this type must NOT carry omitempty: it has no
// effect on decoding, but it would wrongly suggest the field round-trips.
type OptionalField[T any] struct {
	// Set reports whether the key appeared in the request body at all.
	Set bool
	// Value is nil when the key appeared as null, or when it never appeared.
	Value *T
}

// UnmarshalJSON records that the key was present, then decodes null as an
// explicit clear and anything else as a value.
func (o *OptionalField[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("decoding optional field: %w", err)
	}
	o.Value = &v
	return nil
}

// RequestID is middleware that assigns a unique request ID to each request and
// sets it in the response header and context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = "req_" + uuid.New().String()[:8]
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), CtxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
