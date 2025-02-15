package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/metrics"
	"github.com/nickheyer/distroface/internal/models"
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

func (h *MetricsHandler) LogAccess(username string, action string, resource string, r *http.Request, status int) {
	entry := models.AccessLogEntry{
		Timestamp: time.Now(),
		Username:  username,
		Action:    action,
		Resource:  resource,
		Path:      r.URL.Path,
		Method:    r.Method,
		Status:    status,
	}
	h.metrics.AddAccessLog(entry)
}
