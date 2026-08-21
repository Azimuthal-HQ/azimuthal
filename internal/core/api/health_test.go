package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
)

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	api.HandleHealth(rr, req)

	if got := rr.Code; got != http.StatusOK {
		t.Errorf("status = %d, want %d", got, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if got := body["status"]; got != "ok" {
		t.Errorf("status = %q, want %q", got, "ok")
	}
}

// TestHandleHealthWithQueue_ReadsStatusLivePerRequest is the fails-before test
// for the boot-time snapshot. The queue status is now a function the handler
// calls on every request, so a change between two requests is visible. Under
// the previous signature — a string captured once and closed over — the second
// request could not report anything different, which is exactly the defect: a
// queue that stopped after boot stayed "ok" forever.
func TestHandleHealthWithQueue_ReadsStatusLivePerRequest(t *testing.T) {
	live := "ok"
	handler := api.HandleHealthWithQueue(func() string { return live })

	first := doHealth(t, handler)
	require.Equal(t, "ok", first["status"])
	require.Equal(t, "ok", first["queue"], "queue status must be read from the live source")

	// Flip the underlying state the way a queue crashing after boot would.
	live = "error"

	second := doHealth(t, handler)
	require.Equal(t, "ok", second["status"], "liveness stays 200/ok — the process is up")
	require.Equal(t, "error", second["queue"],
		"the queue word must reflect the live change; a captured-at-boot string could not")
}

// TestHandleHealthWithQueue_NilIsDisabled covers the disabled queue: no live
// source, so /health reports "disabled" (main.go leaves the func nil then).
func TestHandleHealthWithQueue_NilIsDisabled(t *testing.T) {
	body := doHealth(t, api.HandleHealthWithQueue(nil))
	require.Equal(t, "ok", body["status"])
	require.Equal(t, "disabled", body["queue"])
}

// doHealth runs a /health handler and decodes its JSON body.
func doHealth(t *testing.T, h http.HandlerFunc) map[string]string {
	t.Helper()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, rr.Code, "liveness must always be 200 while the process runs")
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	return body
}

// pingerFunc adapts a function to api.DBPinger.
type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

// TestHandleReady_HealthyPingerIs200 is the positive control: a store that
// answers the ping is ready.
func TestHandleReady_HealthyPingerIs200(t *testing.T) {
	rr := doReady(t, api.HandleReady(pingerFunc(func(context.Context) error { return nil })))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	require.Equal(t, "ready", body["status"])
}

// TestHandleReady_UnreachableStoreIs503 is the whole point of the change: an
// instance whose store cannot be reached reports 503, so a load balancer stops
// routing to it. The previous handler took no arguments and answered a
// hardcoded {"status":"ready"} here, structurally unable to report unreadiness.
func TestHandleReady_UnreachableStoreIs503(t *testing.T) {
	sentinel := "connection refused: password=hunter2 host=internal-db.prod"
	rr := doReady(t, api.HandleReady(pingerFunc(func(context.Context) error {
		return errString(sentinel)
	})))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	require.Equal(t, "unavailable", body["status"])
	// Unauthenticated endpoint: the ping error must not leak into the body.
	require.NotContains(t, rr.Body.String(), "hunter2")
	require.NotContains(t, rr.Body.String(), "internal-db")
}

// TestHandleReady_NilPingerIs503 covers the unwired case: no pinger means no
// way to prove readiness, which is 503, not a false 200.
func TestHandleReady_NilPingerIs503(t *testing.T) {
	rr := doReady(t, api.HandleReady(nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	require.Equal(t, "unavailable", body["status"])
}

// TestHandleReady_BoundsASlowPingAndDoesNotHang proves the readiness probe
// bounds the ping with its own deadline rather than blocking on a store that
// never answers. The pinger here blocks until its context is cancelled; the
// handler must return a 503 promptly (well inside the test's own budget), not
// hang for the duration of a stalled connection.
func TestHandleReady_BoundsASlowPingAndDoesNotHang(t *testing.T) {
	handler := api.HandleReady(pingerFunc(func(ctx context.Context) error {
		<-ctx.Done() // never answers on its own — only the deadline unblocks it
		return ctx.Err()
	}))

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- doReady(t, handler) }()

	select {
	case rr := <-done:
		require.Equal(t, http.StatusServiceUnavailable, rr.Code)
		var body map[string]string
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
		require.Equal(t, "unavailable", body["status"])
	case <-time.After(10 * time.Second):
		t.Fatal("/ready hung on a stalled ping instead of timing out — the probe deadline is not bounding it")
	}
}

// doReady runs a /ready handler against a fresh request/recorder.
func doReady(t *testing.T, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/ready", nil))
	return rr
}

// errString is a minimal error carrying a fixed message, used to prove the
// ping error does not reach the response body.
type errString string

func (e errString) Error() string { return string(e) }
