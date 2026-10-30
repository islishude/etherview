package httpapi

import (
	"errors"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/enscontract"
	"net/http"
	"net/url"
)

func (h *Handler) nameHistory(w http.ResponseWriter, r *http.Request) {
	if !validateNameHistoryQuery(w, r, false) {
		return
	}
	reader, ok := h.addressNames.(enscontract.HistoryReader)
	if !ok || !h.cfg.Features.ENS {
		h.handleReaderError(w, r, NewCapabilityUnavailableError("name_history", "unavailable", "not_configured"))
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	page, next, err := reader.NameHistory(r.Context(), enscontract.HistoryRequest{Name: r.PathValue("name"), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		h.nameHistoryError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, struct {
		Data enscontract.HistoryPage `json:"data"`
		Meta gen.Meta                `json:"meta"`
	}{page, meta})
}
func (h *Handler) nameHistoryEvents(w http.ResponseWriter, r *http.Request) {
	if !validateNameHistoryQuery(w, r, true) {
		return
	}
	reader, ok := h.addressNames.(enscontract.HistoryReader)
	if !ok || !h.cfg.Features.ENS {
		h.handleReaderError(w, r, NewCapabilityUnavailableError("name_history", "unavailable", "not_configured"))
		return
	}
	limit, ok := parseLimit(w, r, 50)
	if !ok {
		return
	}
	page, next, err := reader.NameHistoryEvents(r.Context(), enscontract.HistoryEventsRequest{Name: r.PathValue("name"), TransactionHash: r.PathValue("transactionHash"), BlockHash: r.URL.Query().Get("block_hash"), Snapshot: r.URL.Query().Get("snapshot"), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		h.nameHistoryError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, struct {
		Data enscontract.HistoryEventsPage `json:"data"`
		Meta gen.Meta                      `json:"meta"`
	}{page, meta})
}
func (h *Handler) nameHistoryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, enscontract.ErrInvalidName) {
		writeError(w, r, http.StatusBadRequest, "invalid_name", "name is not a valid ENS name", nil)
		return
	}
	h.handleENSReaderError(w, r, err)
}

func validateNameHistoryQuery(w http.ResponseWriter, r *http.Request, events bool) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 4096 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid history parameters", nil)
		return false
	}
	for key, values := range query {
		if len(values) != 1 || (key != "limit" && key != "cursor" && (!events || key != "snapshot" && key != "block_hash")) {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid history parameters", nil)
			return false
		}
		if (key == "cursor" || key == "snapshot") && (len(values[0]) == 0 || len(values[0]) > maximumOpaqueCursorLength) {
			writeError(w, r, http.StatusBadRequest, "invalid_cursor", "invalid history cursor", nil)
			return false
		}
	}
	if events && (query.Get("snapshot") == "" || !hashPattern.MatchString(query.Get("block_hash")) || !hashPattern.MatchString(r.PathValue("transactionHash"))) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "history requires a snapshot and exact block and transaction hashes", nil)
		return false
	}
	return true
}
