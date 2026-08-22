package httpapi

func (h *Handler) registerIdentityBillingRoutes() {
	h.mux.HandleFunc("POST /api/v1/auth/challenge", h.createAuthChallenge)
	h.mux.HandleFunc("POST /api/v1/auth/verify", h.verifyAuthChallenge)
	h.mux.HandleFunc("GET /api/v1/auth/session", h.authSession)
	h.mux.HandleFunc("POST /api/v1/auth/logout", h.logoutAuthSession)
	h.mux.HandleFunc("PATCH /api/v1/users/me", h.updateCurrentUser)
	h.mux.HandleFunc("GET /api/v1/users/me/api-keys", h.listCurrentUserAPIKeys)
	h.mux.HandleFunc("POST /api/v1/users/me/api-keys", h.createCurrentUserAPIKey)
	h.mux.HandleFunc("POST /api/v1/users/me/api-keys/{prefix}/rotate", h.rotateCurrentUserAPIKey)
	h.mux.HandleFunc("DELETE /api/v1/users/me/api-keys/{prefix}", h.revokeCurrentUserAPIKey)
	h.mux.HandleFunc("GET /api/v1/admin/users", h.listAdminUsers)
	h.mux.HandleFunc("PATCH /api/v1/admin/users/{id}", h.updateAdminUser)
	h.mux.HandleFunc("POST /api/v1/admin/users/{id}/sessions/revoke", h.revokeAdminUserSessions)
	h.mux.HandleFunc("GET /api/v1/billing/config", h.billingConfig)
	h.mux.HandleFunc("GET /api/v1/billing/payments", h.listCurrentUserBillingPayments)
	h.mux.HandleFunc("GET /api/v1/admin/billing/payments", h.listAdminBillingPayments)
	h.mux.HandleFunc("GET /api/v1/admin/billing/summary", h.adminBillingSummary)
}
