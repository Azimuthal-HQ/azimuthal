package api

import (
	"net/http"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

// Re-export error codes so existing code within the api package still compiles.
const (
	CodeNotFound          = respond.CodeNotFound
	CodeValidation        = respond.CodeValidation
	CodeUnauthorized      = respond.CodeUnauthorized
	CodeForbidden         = respond.CodeForbidden
	CodeConflict          = respond.CodeConflict
	CodeInternal          = respond.CodeInternal
	CodeBadRequest        = respond.CodeBadRequest
	CodeInvalidTransition = respond.CodeInvalidTransition
)

// WriteError writes a structured JSON error response.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code respond.ErrorCode, msg string) {
	respond.Error(w, r, status, code, msg)
}

// WriteJSON encodes v as JSON to w with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	respond.JSON(w, status, v)
}

// DecodeJSON reads the request body into dst.
func DecodeJSON(r *http.Request, dst any) error {
	return respond.DecodeJSON(r, dst)
}
