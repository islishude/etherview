package httpapi

import (
	"errors"
	"net/http"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/islishude/etherview/internal/defiread"
)

func (h *Handler) defiPositions(w http.ResponseWriter, r *http.Request) {
	owner, limit, cursor, ok := parseCatalogAddressPage(w, r)
	if !ok {
		return
	}
	protocol := deficontract.Protocol(r.URL.Query().Get("protocol"))
	if protocol != "" && !protocol.Valid() {
		writeError(w, r, http.StatusBadRequest, "invalid_protocol", "Unsupported DeFi protocol", nil)
		return
	}
	reader := h.defi
	if reader == nil {
		reader = &defiread.Reader{}
	}
	page, e := reader.Positions(r.Context(), deficontract.Request{Owner: owner, Limit: limit, Cursor: cursor, Protocol: protocol})
	if e != nil {
		switch {
		case errors.Is(e, deficontract.ErrCursor):
			writeError(w, r, http.StatusBadRequest, "invalid_cursor", "DeFi snapshot is no longer available; refresh the page", nil)
		case errors.Is(e, deficontract.ErrInput):
			writeError(w, r, http.StatusBadRequest, "invalid_request", "Invalid DeFi query", nil)
		default:
			writeError(w, r, http.StatusServiceUnavailable, "query_failed", "DeFi positions are unavailable", nil)
		}
		return
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, struct {
		Data deficontract.Page `json:"data"`
		Meta gen.Meta          `json:"meta"`
	}{Data: page, Meta: meta})
}
