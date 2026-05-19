package httpapi

import (
	"encoding/json"
	"net/http"
)

type statsHandler struct {
	tracker *LatencyTracker
}

func newStatsHandler(tracker *LatencyTracker) *statsHandler {
	return &statsHandler{tracker: tracker}
}

func (h *statsHandler) handleStats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := map[string]float64{
		"status": 1,
	}

	if h.tracker != nil {
		sP50, sP99, uP50, uP99 := h.tracker.Stats()
		resp["specialist_p50_ms"] = float64(sP50.Microseconds()) / 1000.0
		resp["specialist_p99_ms"] = float64(sP99.Microseconds()) / 1000.0
		resp["upstream_p50_ms"] = float64(uP50.Microseconds()) / 1000.0
		resp["upstream_p99_ms"] = float64(uP99.Microseconds()) / 1000.0
	}

	_ = json.NewEncoder(w).Encode(resp)
}
