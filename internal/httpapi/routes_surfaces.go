package httpapi

func (h *Handler) registerExternalSurfaceRoutes() {
	if h.etherscan != nil {
		h.mux.Handle("/v2/api", h.etherscan)
	}
	if h.events != nil {
		h.mux.HandleFunc("GET /api/v1/events", h.eventStream)
	}
	h.mux.HandleFunc("GET /api/v1/home/stream", h.homeSnapshotStream)
	if h.web != nil {
		h.mux.Handle("/", h.web)
	}
}
