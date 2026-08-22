package httpapi

func (h *Handler) registerOperationalRoutes() {
	h.mux.HandleFunc("GET /health/live", h.live)
	h.mux.HandleFunc("GET /health/ready", h.ready)
	if h.metrics != nil {
		h.mux.Handle("GET /metrics", h.metrics)
	}
	h.mux.HandleFunc("GET /api/v1/status", h.status)
	h.mux.HandleFunc("GET /api/v1/config", h.publicConfig)
}
