package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// QueueStatus is the operational state of the background job queue, as
// surfaced on /health.
type QueueStatus string

const (
	// QueueStatusOK indicates the queue is running and accepting jobs.
	QueueStatusOK QueueStatus = "ok"
	// QueueStatusDisabled indicates AZIMUTHAL_QUEUE_ENABLED=false at startup.
	QueueStatusDisabled QueueStatus = "disabled"
	// QueueStatusError indicates the queue failed to start or has a fatal
	// runtime error.
	QueueStatusError QueueStatus = "error"
)

// HealthProvider supplies the dynamic parts of the health response (queue
// state today, room for more later). Implementations must be safe for
// concurrent reads — callers may invoke them on every request.
type HealthProvider interface {
	QueueStatus() QueueStatus
}

// healthResponse is the JSON body returned by /health and /ready. The Queue
// field is omitted when no provider is configured so existing clients see
// the same shape they always have.
type healthResponse struct {
	Status string      `json:"status"`
	Queue  QueueStatus `json:"queue,omitempty"`
}

// writeJSON encodes v as JSON to w. Logs on failure (client may have disconnected).
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing json response", "error", err)
	}
}

// HandleHealth responds to liveness probes with {"status":"ok"}.
//
// @Summary      Liveness probe
// @Description  Returns {"status":"ok"} when the server is running.
// @Tags         health
// @Produce      json
// @Success      200  {object}  healthResponse  "Server is alive"
// @Router       /health [get]
func HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ok"})
}

// HandleHealthWith returns a /health handler that includes queue status from
// the supplied provider.
func HandleHealthWith(p HealthProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, healthResponse{Status: "ok", Queue: p.QueueStatus()})
	}
}

// HandleReady responds to readiness probes with {"status":"ready"}.
//
// @Summary      Readiness probe
// @Description  Returns {"status":"ready"} when the server is ready to accept traffic.
// @Tags         health
// @Produce      json
// @Success      200  {object}  healthResponse  "Server is ready"
// @Router       /ready [get]
func HandleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ready"})
}
