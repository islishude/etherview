package httpapi

import (
	"net/http"
	"net/url"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/publicquery"
)

func (h *Handler) blockRequests(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !validBlockID(id) {
		h.handleReaderError(w, r, ErrInvalidInput)
		return
	}
	for key, items := range values {
		if (key != "limit" && key != "cursor" && key != "request_type") || len(items) != 1 || items[0] == "" {
			h.handleReaderError(w, r, ErrInvalidInput)
			return
		}
	}
	filter := values.Get("request_type")
	switch filter {
	case "", "0x00", "0x01", "0x02", "0x03", "0x04":
	default:
		h.handleReaderError(w, r, ErrInvalidInput)
		return
	}
	limit, ok := parseExactLimit(w, r, values, 50)
	if !ok {
		return
	}
	reader := h.requestsReader
	if reader == nil {
		reader, _ = h.reader.(publicquery.RequestsReader)
	}
	if reader == nil {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	data, next, err := reader.BlockRequests(r.Context(), id, filter, values.Get("cursor"), limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.BlockRequestsResponse{Data: data, Meta: meta})
}
