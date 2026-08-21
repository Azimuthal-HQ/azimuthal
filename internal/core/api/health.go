package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// healthResponse is the JSON body returned by /health and /ready.
//
// The fields are status words only, by necessity: both endpoints are
// unauthenticated (a load balancer or orchestrator cannot present a token), so
// neither may disclose anything about the server's internals — a database error
// string, a queue's inner state — beyond whether it is alive/ready.
type healthResponse struct {
	Status string `json:"status"`
	Queue  string `json:"queue,omitempty"`
}

// readyProbeTimeout bounds the database ping behind /ready.
//
// Readiness is polled continuously by load balancers and orchestrators, and its
// whole job is to fail fast: a probe that hangs behind an unreachable database
// is itself a form of unreadiness — worse, one that ties up the prober — so the
// ping gets a short, fixed deadline rather than inheriting the request's (which
// a stalled client could stretch indefinitely). One second is comfortably
// longer than a healthy round-trip to any store worth serving from, and short
// enough that a dead database trips the probe promptly.
const readyProbeTimeout = 1 * time.Second

// writeJSON encodes v as JSON to w with a 200 status. Logs on failure (the
// client may have disconnected).
func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

// writeJSONStatus encodes v as JSON with an explicit status code. The header
// and code must be written before the body — the first Write locks the status
// at 200 — so a 503 readiness response cannot go through writeJSON.
func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing json response", "error", err)
	}
}

// HandleHealth responds to liveness probes with {"status":"ok"}.
//
// Liveness is not readiness. This answers "the process is up and its HTTP
// server is accepting connections" — the question an orchestrator asks before
// deciding whether to RESTART the container. It deliberately touches no
// dependency: a server whose database is momentarily unreachable is still
// alive and must not be killed for it (that is /ready's call, below). So it
// stays cheap and answers 200 for as long as the process runs.
//
// The OpenAPI entry for GET /health is documented on HandleHealthWithQueue,
// which is the variant the router actually mounts; annotating both would give
// swag two conflicting definitions of the same path.
func HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ok"})
}

// HandleHealthWithQueue returns a liveness handler that also reports the job
// queue's LIVE status, read at request time.
//
// queueStatus is called on every request, so the reported value reflects the
// queue's actual state then and there. The handler stays a liveness probe — it
// always answers 200 while the process runs and never pings a dependency — the
// queue word is informational only. A nil queueStatus (queue disabled) reports
// "disabled".
//
// This replaces a string captured once at boot and closed over forever:
// main.go set it to "ok" the moment the queue started and no code path ever
// changed it, so "error" was unreachable in production and a queue that died
// after boot still reported "ok".
//
// @Summary      Liveness probe with queue status
// @Description  Returns {"status":"ok","queue":"ok|error|disabled"} — queue status is read live, not captured at boot.
// @Tags         health
// @Produce      json
// @Success      200  {object}  healthResponse  "Server is alive"
// @Router       /health [get]
func HandleHealthWithQueue(queueStatus func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		status := "disabled"
		if queueStatus != nil {
			status = queueStatus()
		}
		writeJSON(w, healthResponse{Status: "ok", Queue: status})
	}
}

// DBPinger is the single datastore capability /ready needs: a bounded check
// that a connection can be obtained and a trivial query served. *pgxpool.Pool
// satisfies it via Ping. Kept to this one method so the readiness handler can
// be exercised with a real pool and with a closed one, without a whole server.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// HandleReady returns a readiness probe that answers 200 only when the store is
// reachable, and 503 otherwise.
//
// Readiness is not liveness. "Ready" means "this instance can serve a request
// that touches the store" — the question a load balancer asks before ROUTING
// traffic here. So, unlike /health, it actually pings the database, on a short
// deadline (readyProbeTimeout), and answers 503 when the store cannot be
// reached within it. The previous handler took no arguments and answered a
// hardcoded {"status":"ready"} unconditionally, so a load balancer would route
// traffic to an instance whose database was gone.
//
// The 503 body carries a status word only, never the ping error: the endpoint
// is unauthenticated, and a readiness probe fails routinely during rollout and
// shutdown, so the failure is not logged at error level either.
//
// @Summary      Readiness probe
// @Description  Returns {"status":"ready"} (200) when the datastore is reachable, or {"status":"unavailable"} (503) when it is not.
// @Tags         health
// @Produce      json
// @Success      200  {object}  healthResponse  "Server is ready"
// @Failure      503  {object}  healthResponse  "Server cannot reach its datastore"
// @Router       /ready [get]
func HandleReady(pinger DBPinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyProbeTimeout)
		defer cancel()

		if pinger == nil || pinger.Ping(ctx) != nil {
			writeJSONStatus(w, http.StatusServiceUnavailable, healthResponse{Status: "unavailable"})
			return
		}
		writeJSON(w, healthResponse{Status: "ready"})
	}
}
