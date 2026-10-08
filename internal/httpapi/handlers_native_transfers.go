package httpapi

import (
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/publicquery"
	"net/http"
	"strconv"
)

func (h *Handler) transactionNativeTransfers(w http.ResponseWriter, r *http.Request) {
	h.nativeTransfers(w, r, publicquery.NativeTransferRequest{TransactionHash: r.PathValue("hash")})
}
func (h *Handler) addressNativeTransfers(w http.ResponseWriter, r *http.Request) {
	h.nativeTransfers(w, r, publicquery.NativeTransferRequest{Address: r.PathValue("address")})
}
func (h *Handler) nativeTransfers(w http.ResponseWriter, r *http.Request, request publicquery.NativeTransferRequest) {
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	request.Limit = limit
	request.Cursor = cursor
	reader := h.nativeTransfersReader
	if reader == nil {
		h.handleReaderError(w, r, publicquery.NewCapabilityUnavailableError("native_transfers", "unavailable", "not_configured"))
		return
	}
	page, err := reader.NativeTransfers(r.Context(), request)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	if page.Applicable {
		start, end := strconv.FormatUint(page.CoverageStart, 10), strconv.FormatUint(page.CoverageEnd, 10)
		meta.CoverageStart = &start
		meta.CoverageEnd = &end
	}
	writeJSON(w, http.StatusOK, gen.NativeTransferListResponse{Data: page.Items, Meta: meta, Applicable: page.Applicable})
}
