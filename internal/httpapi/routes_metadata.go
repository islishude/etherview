package httpapi

func (h *Handler) registerMetadataRoutes() {
	// Routes remain addressable while disabled so clients receive typed
	// capability responses instead of route-level 404s.
	h.handleBillable("getNFTMetadata", h.nftMetadata)
	h.mux.HandleFunc("GET /api/v1/nfts/{address}/{token_id}/media", h.nftMedia)
}
