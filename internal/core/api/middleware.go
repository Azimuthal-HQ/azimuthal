package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

// RequestID is middleware that assigns a unique request ID to each request.
var RequestID = respond.RequestID

// RequestIDFromContext returns the request ID from the context, or empty string.
var RequestIDFromContext = respond.RequestIDFromContext

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader captures the status code.
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wrote {
		rw.status = code
		rw.wrote = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures 200 as default status.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.status = http.StatusOK
		rw.wrote = true
	}
	n, err := rw.ResponseWriter.Write(b)
	if err != nil {
		return n, fmt.Errorf("writing response: %w", err)
	}
	return n, nil
}

// Unwrap supports http.ResponseController.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Logging is middleware that logs each request with its duration and status.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		// G706: values are from the request/context, not from untrusted user input.
		slog.Info("http request", //nolint:gosec // G706 — values originate from the HTTP server, not user-tainted data
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", respond.RequestIDFromContext(r.Context()),
		)
	})
}

// CORS is the legacy permissive middleware. It echoes Access-Control-Allow-Origin: *
// on every request and is preserved for tests and existing wiring that builds
// the router without an allow-list. Use NewCORS for production-safe behavior.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewCORS returns a CORS middleware that only echoes Access-Control-Allow-Origin
// when the request's Origin matches one of allowedOrigins. The wildcard "*"
// in allowedOrigins permits any origin (development default). An empty list
// rejects all cross-origin requests, which is the production default driven by
// AZIMUTHAL_ALLOWED_ORIGINS — audit ref: testing-audit.md §3.3.
func NewCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAny, allowSet := buildOriginAllowList(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := resolveAllowedOrigin(origin, allowAny, allowSet)
			if allowed != "" {
				writeCORSHeaders(w, allowed)
			}
			if r.Method == http.MethodOptions {
				if allowed == "" && origin != "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func buildOriginAllowList(origins []string) (bool, map[string]struct{}) {
	allowAny := false
	set := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAny = true
		}
		set[o] = struct{}{}
	}
	return allowAny, set
}

func resolveAllowedOrigin(origin string, allowAny bool, allowSet map[string]struct{}) string {
	if origin == "" {
		return ""
	}
	if allowAny {
		return origin
	}
	if _, ok := allowSet[origin]; ok {
		return origin
	}
	return ""
}

func writeCORSHeaders(w http.ResponseWriter, allowed string) {
	w.Header().Set("Access-Control-Allow-Origin", allowed)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Request-ID")
	w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// Recoverer is middleware that recovers from panics and returns a 500 error.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rvr := recover(); rvr != nil {
				slog.Error("panic recovered", //nolint:gosec // G706 — panic value and path are server-internal
					"error", rvr,
					"request_id", respond.RequestIDFromContext(r.Context()),
					"path", r.URL.Path,
				)
				respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
