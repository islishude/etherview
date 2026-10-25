package httpapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/watchlist"
)

type WatchlistService interface {
	List(context.Context, string) ([]gen.AddressWatch, error)
	Save(context.Context, string, string, gen.WatchInput) (gen.AddressWatch, error)
	Delete(context.Context, string, string) error
	PushSubscriptions(context.Context, string, string) (gen.PushSubscriptions, error)
	SavePushSubscription(context.Context, string, string, gen.PushSubscriptionInput) (gen.BrowserPushSubscription, error)
	DeletePushSubscription(context.Context, string, string, string) error
	Export(context.Context, string, gen.AddressExportRequest) (watchlist.Export, error)
}

func (h *Handler) registerWatchlistRoutes() {
	h.mux.HandleFunc("GET /api/v1/users/me/watchlist", h.listWatches)
	h.mux.HandleFunc("POST /api/v1/users/me/watchlist", h.saveWatch)
	h.mux.HandleFunc("PATCH /api/v1/users/me/watchlist/{id}", h.saveWatch)
	h.mux.HandleFunc("DELETE /api/v1/users/me/watchlist/{id}", h.deleteWatch)
	h.mux.HandleFunc("GET /api/v1/users/me/push-subscriptions", h.pushSubscriptions)
	h.mux.HandleFunc("POST /api/v1/users/me/push-subscriptions", h.pushSubscriptions)
	h.mux.HandleFunc("DELETE /api/v1/users/me/push-subscriptions/{id}", h.pushSubscriptions)
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
func (h *Handler) pushSubscriptions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	auth, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	session := auth.Session.ID
	switch r.Method {
	case "GET":
		result, err := h.watchlist.PushSubscriptions(r.Context(), user, session)
		if err != nil {
			h.watchError(w, r, err)
			return
		}
		writeJSON(w, 200, gen.PushSubscriptionsResponse{Data: result, Meta: h.meta(r)})
	case "POST":
		var input gen.PushSubscriptionInput
		if !decodeAuthJSON(w, r, &input) {
			return
		}
		result, err := h.watchlist.SavePushSubscription(r.Context(), user, session, input)
		if err != nil {
			h.watchError(w, r, err)
			return
		}
		writeJSON(w, 200, gen.PushSubscriptionResponse{Data: result, Meta: h.meta(r)})
	case "DELETE":
		if err := h.watchlist.DeletePushSubscription(r.Context(), user, session, r.PathValue("id")); err != nil {
			h.watchError(w, r, err)
			return
		}
		w.WriteHeader(204)
	}
}
func (h *Handler) exportAddressActivity(w http.ResponseWriter, r *http.Request) {
	user, ok := h.watchSession(w, r)
	if !ok {
		return
	}
	if !h.requireTurnstile(w, r, "csv_export") {
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
	const filenameTime = "2006-01-02T15-04-05Z"
	filename := strings.ToLower(input.Address) + "-" + input.From.UTC().Format(filenameTime) + "-" + input.To.UTC().Format(filenameTime) + "-" + string(input.Kind) + ".csv"
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
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
	case errors.Is(err, watchlist.ErrPushLimit):
		status, code = 409, "push_subscription_limit"
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
