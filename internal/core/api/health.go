package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// healthResponse is the JSON body returned by /health and /ready.
type healthResponse struct {
	Status string `json:"status"`
}

// writeJSON encodes v as JSON to w. Logs on failure (client may have disconnected).
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writing json response", "error", err)
	}
}

// HandleHealth responds to liveness probes with {"status":"ok"}.
func HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ok"})
}

// HandleReady responds to readiness probes with {"status":"ready"}.
func HandleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ready"})
}
