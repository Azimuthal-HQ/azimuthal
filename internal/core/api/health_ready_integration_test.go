package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestReady_RealPool_HealthyThenClosed exercises HandleReady against a real
// PostgreSQL pool in both states the probe exists to distinguish, using the
// exact type production wires — *pgxpool.Pool as an api.DBPinger.
//
// Healthy pool → 200 "ready". The pool is then closed to stand in for a
// database that has gone away, and the same handler must answer 503 — promptly,
// inside the probe's own deadline, not by hanging on a dead pool. This is the
// fails-before for a /ready that ignored its store: the previous handler took
// no arguments and answered 200 in both states.
func TestReady_RealPool_HealthyThenClosed(t *testing.T) {
	db := testutil.NewTestDB(t) // skips without DATABASE_URL; fatal in CI

	handler := api.HandleReady(db.Pool)

	// 1. Healthy pool: the store is reachable, so the instance is ready.
	rrHealthy := httptest.NewRecorder()
	handler(rrHealthy, httptest.NewRequest(http.MethodGet, "/ready", nil))
	require.Equal(t, http.StatusOK, rrHealthy.Code, "a reachable store must report ready")
	var healthy map[string]string
	require.NoError(t, json.NewDecoder(rrHealthy.Body).Decode(&healthy))
	require.Equal(t, "ready", healthy["status"])

	// 2. Store gone: close the pool. Pool.Close is idempotent, so the NewTestDB
	// cleanup that also closes it stays safe.
	db.Pool.Close()

	start := time.Now()
	rrClosed := httptest.NewRecorder()
	handler(rrClosed, httptest.NewRequest(http.MethodGet, "/ready", nil))
	elapsed := time.Since(start)

	require.Equal(t, http.StatusServiceUnavailable, rrClosed.Code, "an unreachable store must report 503")
	var closed map[string]string
	require.NoError(t, json.NewDecoder(rrClosed.Body).Decode(&closed))
	require.Equal(t, "unavailable", closed["status"])
	require.Less(t, elapsed, 5*time.Second,
		"the probe must fail fast within its deadline, not hang on a dead pool (took %s)", elapsed)
}
