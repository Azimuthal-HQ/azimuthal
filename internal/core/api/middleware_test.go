package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
)

func TestLoggingMiddleware(t *testing.T) {
	handler := api.Logging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// TestCORSMiddleware_EmptyAllowListEmitsNoHeaders pins the S5 default.
//
// This used to assert the opposite: it exercised a permissive api.CORS
// middleware and required Access-Control-Allow-Origin to equal "*". That
// middleware was the fail-open default for any router built without an
// allow-list, and it has been removed. An empty allow-list must now be
// silent, so the browser falls back to same-origin.
func TestCORSMiddleware_EmptyAllowListEmitsNoHeaders(t *testing.T) {
	handler := api.NewCORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no header at all", got)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d — the request itself must still be served", rr.Code, http.StatusOK)
	}
}

// TestCORSMiddleware_UnlistedOriginPreflightIsRefused covers the preflight
// half: an unlisted origin asking permission is told no.
func TestCORSMiddleware_UnlistedOriginPreflightIsRefused(t *testing.T) {
	handler := api.NewCORS([]string{"https://app.example.com"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("preflight status = %d, want %d for an unlisted origin", rr.Code, http.StatusForbidden)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no header for an unlisted origin", got)
	}
}

// TestCORSMiddleware_ListedOriginIsEchoed is the positive case. Without it the
// two tests above would pass against a middleware that never allows anything,
// which would assert nothing about the allow-list actually working.
func TestCORSMiddleware_ListedOriginIsEchoed(t *testing.T) {
	const allowed = "https://app.example.com"
	handler := api.NewCORS([]string{allowed})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", allowed)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != allowed {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, allowed)
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin — an allow-listed response is origin-dependent and must not be cached across origins", got)
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	handler := api.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	// Exactly one value — a handler that also sets it must replace, not append.
	if got := len(rr.Header().Values("X-Content-Type-Options")); got != 1 {
		t.Errorf("X-Content-Type-Options set %d times, want 1", got)
	}
}

// TestSecurityHeadersMiddleware_HandlerValueWins: the middleware runs first so
// a handler that sets its own value replaces rather than duplicates it. If it
// ever appended instead, a browser would see a malformed header list.
func TestSecurityHeadersMiddleware_HandlerValueWins(t *testing.T) {
	handler := api.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Values("X-Content-Type-Options"); len(got) != 1 || got[0] != "nosniff" {
		t.Errorf("X-Content-Type-Options = %v, want exactly [nosniff]", got)
	}
}

func TestRecovererMiddleware(t *testing.T) {
	handler := api.RequestID(api.Recoverer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}
