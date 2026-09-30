package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/watchlist"
)

type WatchlistService interface {
	List(context.Context, string) ([]gen.AddressWatch, error)
	Save(context.Context, string, string, gen.WatchInput) (gen.AddressWatch, error)
	Delete(context.Context, string, string) error
	Notifications(context.Context, string, string, bool) (gen.WatchNotificationPage, error)
	Read(context.Context, string, string, bool) error
	Export(context.Context, string, gen.AddressExportRequest) (watchlist.Export, error)
}

func (h *Handler) registerWatchlistRoutes() {
	h.mux.HandleFunc("GET /api/v1/users/me/watchlist", h.listWatches)
	h.mux.HandleFunc("POST /api/v1/users/me/watchlist", h.saveWatch)
	h.mux.HandleFunc("PATCH /api/v1/users/me/watchlist/{id}", h.saveWatch)
	h.mux.HandleFunc("DELETE /api/v1/users/me/watchlist/{id}", h.deleteWatch)
	h.mux.HandleFunc("GET /api/v1/users/me/notifications", h.listNotifications)
	h.mux.HandleFunc("POST /api/v1/users/me/notifications/{id}/read", h.readNotification)
	h.mux.HandleFunc("POST /api/v1/users/me/notifications/read-through", h.readNotification)
	h.mux.HandleFunc("POST /api/v1/users/me/exports/address-activity", h.exportAddressActivity)
}
func (h *Handler) watchSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.userAuthAvailable(w, r) {
		return "", false
	}
	a, ok := h.requireUserSession(w, r)
	if !ok {
		return "", false
	}
	if r.Method != "GET" && (!h.requireAuthOrigin(w, r) || !h.requireCSRF(w, r, a)) {
		return "", false
	}
	if h.watchlist == nil {
		h.watchError(w, r, watchlist.ErrUnavailable)
		return "", false
	}
	return a.Session.User.ID, true
}
func (h *Handler) listWatches(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	items, err := h.watchlist.List(r.Context(), user)
	if err != nil {
		h.watchError(w, r, err)
		return
	}
	writeJSON(w, 200, gen.WatchListResponse{Data: items, Meta: h.meta(r)})
}
func (h *Handler) saveWatch(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	var input gen.WatchInput
	if !decodeAuthJSON(w, r, &input) {
		return
	}
	item, err := h.watchlist.Save(r.Context(), user, r.PathValue("id"), input)
	if err != nil {
		h.watchError(w, r, err)
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	writeJSON(w, status, gen.WatchResponse{Data: item, Meta: h.meta(r)})
}
func (h *Handler) deleteWatch(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	if err := h.watchlist.Delete(r.Context(), user, r.PathValue("id")); err != nil {
		h.watchError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	filter := r.URL.Query().Get("unread_only")
	if filter != "" && filter != "true" && filter != "false" {
		h.watchError(w, r, watchlist.ErrInvalid)
		return
	}
	page, err := h.watchlist.Notifications(r.Context(), user, r.URL.Query().Get("cursor"), filter == "true")
	if err != nil {
		h.watchError(w, r, err)
		return
	}
	writeJSON(w, 200, gen.WatchNotificationsResponse{Data: page, Meta: h.meta(r)})
}
func (h *Handler) readNotification(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	through := id == ""
	if through {
		var input gen.WatchReadThrough
		if !decodeAuthJSON(w, r, &input) {
			return
		}
		id = input.ThroughId
	}
	if err := h.watchlist.Read(r.Context(), user, id, through); err != nil {
		h.watchError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) exportAddressActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	var input gen.AddressExportRequest
	if !decodeAuthJSON(w, r, &input) {
		return
	}
	result, err := h.watchlist.Export(r.Context(), user, input)
	if err != nil {
		h.watchError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="address-activity.csv"`)
	w.Header().Set("X-Snapshot-Block-Number", result.BlockNumber)
	w.Header().Set("X-Snapshot-Block-Hash", result.BlockHash)
	w.WriteHeader(200)
	_, _ = w.Write(result.Bytes)
}
func (h *Handler) watchError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusServiceUnavailable, "activity_unavailable"
	switch {
	case errors.Is(err, watchlist.ErrInvalid):
		status, code = 400, "invalid_watchlist_request"
	case errors.Is(err, watchlist.ErrNotFound):
		status, code = 404, "watchlist_not_found"
	case errors.Is(err, watchlist.ErrDuplicate):
		status, code = 409, "address_already_watched"
	case errors.Is(err, watchlist.ErrLimit):
		status, code = 409, "watchlist_limit"
	case errors.Is(err, watchlist.ErrRateLimit):
		status, code = 429, "export_rate_limit"
		w.Header().Set("Retry-After", "60")
	case errors.Is(err, watchlist.ErrExportLimit), errors.Is(err, context.DeadlineExceeded):
		status, code = 422, "export_limit"
	}
	writeError(w, r, status, code, "account activity request could not be completed", nil)
}
