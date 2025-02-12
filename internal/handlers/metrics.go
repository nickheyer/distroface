package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/metrics"
)

type MetricsHandler struct {
	metrics *metrics.MetricsService
	log     *logging.LogService
}

func NewMetricsHandler(metrics *metrics.MetricsService, log *logging.LogService) *MetricsHandler {
	return &MetricsHandler{
		metrics: metrics,
		log:     log,
	}
}

func (h *MetricsHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := h.metrics.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		h.log.Printf("Failed to encode metrics: %v", err)
		http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
		return
	}
}
