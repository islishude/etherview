package httpapi

import (
	"net/http"
	"strconv"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/publicquery"
)

func (h *Handler) userOperationList(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	reader, ok := h.userOperationReader(w, r)
	if !ok {
		return
	}
	page, err := reader.UserOperations(r.Context(), cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.UserOperationListResponse{Data: page.Items, Meta: userOperationMeta(h.meta(r), page)})
}

func (h *Handler) userOperationDetail(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_user_operation_hash", "userOpHash must be a 32-byte 0x-prefixed hash", nil)
		return
	}
	reader, ok := h.userOperationReader(w, r)
	if !ok {
		return
	}
	detail, err := reader.UserOperation(r.Context(), hash)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.UserOperationResponse{Data: detail, Meta: h.meta(r)})
}

func (h *Handler) transactionUserOperations(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be a 32-byte 0x-prefixed hash", nil)
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	reader, ok := h.userOperationReader(w, r)
	if !ok {
		return
	}
	page, err := reader.TransactionUserOperations(r.Context(), hash, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.UserOperationListResponse{Data: page.Items, Meta: userOperationMeta(h.meta(r), page)})
}

func (h *Handler) addressUserOperations(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be a 20-byte 0x-prefixed value", nil)
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	reader, ok := h.userOperationReader(w, r)
	if !ok {
		return
	}
	page, err := reader.AddressUserOperations(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.UserOperationListResponse{Data: page.Items, Meta: userOperationMeta(h.meta(r), page)})
}

func (h *Handler) userOperationReader(w http.ResponseWriter, r *http.Request) (UserOperationReader, bool) {
	if h.userOperations != nil {
		return h.userOperations, true
	}
	h.handleReaderError(w, r, publicquery.NewCapabilityUnavailableError(
		"user_operations", "unavailable", "not_configured",
	))
	return nil, false
}

func userOperationMeta(meta gen.Meta, page publicquery.UserOperationPage) gen.Meta {
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	start, end := gen.Quantity(strconv.FormatUint(page.CoverageStart, 10)), gen.Quantity(strconv.FormatUint(page.CoverageEnd, 10))
	meta.CoverageStart, meta.CoverageEnd = &start, &end
	return meta
}
