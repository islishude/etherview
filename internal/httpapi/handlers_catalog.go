package httpapi

import (
	"net/http"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/catalog"
)

func (h *Handler) addressTransactions(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressActivities == nil {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	items, next, err := h.addressActivities.AddressTransactions(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.TransactionListResponse{Data: items, Meta: meta})
}

func (h *Handler) addressWithdrawals(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressActivities == nil {
		h.handleReaderError(w, r, ErrUnavailable)
		return
	}
	items, next, err := h.addressActivities.AddressWithdrawals(r.Context(), address, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.AddressWithdrawalListResponse{Data: items, Meta: meta})
}

func (h *Handler) addressInternalTransactions(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressEnrichment == nil {
		h.handleCatalogError(w, r, catalog.StageUnavailableError{
			Stage: catalog.StageTrace, State: catalog.StageUnavailable,
		})
		return
	}
	page, err := h.addressEnrichment.AddressInternalTransactions(r.Context(), catalog.AddressActivityRequest{
		ChainID: h.chainID(), Address: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.AddressInternalTransaction, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		path := make([]int, len(item.Path))
		for pathIndex := range item.Path {
			path[pathIndex] = int(item.Path[pathIndex])
		}
		items[index] = gen.AddressInternalTransaction{
			BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
			BlockTimestamp: item.BlockTimestamp.UTC(), TransactionHash: item.TransactionHash,
			TransactionIndex: item.TransactionIndex, Path: path, Depth: int(item.Depth),
			CallType: item.CallType, From: item.From, To: item.To,
			CreatedAddress: item.CreatedAddress, Value: item.Value, Gas: item.Gas,
			GasUsed: item.GasUsed, Input: item.Input, Error: item.Error, Reverted: item.Reverted,
		}
	}
	writeJSON(w, http.StatusOK, gen.AddressInternalTransactionListResponse{
		Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot),
	})
}

func (h *Handler) addressERC20Transfers(w http.ResponseWriter, r *http.Request) {
	h.addressTokenTransfers(w, r, false)
}

func (h *Handler) addressNFTTransfers(w http.ResponseWriter, r *http.Request) {
	h.addressTokenTransfers(w, r, true)
}

func (h *Handler) addressTokenTransfers(w http.ResponseWriter, r *http.Request, nft bool) {
	address, limit, cursor, ok := h.addressActivityPage(w, r)
	if !ok {
		return
	}
	if h.addressEnrichment == nil {
		h.handleCatalogError(w, r, catalog.StageUnavailableError{
			Stage: catalog.StageToken, State: catalog.StageUnavailable,
		})
		return
	}
	request := catalog.AddressActivityRequest{
		ChainID: h.chainID(), Address: address, Cursor: cursor, Limit: limit,
	}
	var page catalog.AddressTokenTransferPage
	var err error
	if nft {
		page, err = h.addressEnrichment.AddressNFTTransfers(r.Context(), request)
	} else {
		page, err = h.addressEnrichment.AddressERC20Transfers(r.Context(), request)
	}
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.AddressTokenTransfer, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		items[index] = gen.AddressTokenTransfer{
			BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
			BlockTimestamp: item.BlockTimestamp.UTC(), TransactionHash: item.TransactionHash,
			TransactionIndex: item.TransactionIndex, LogIndex: item.LogIndex,
			SubIndex: item.SubIndex, TokenAddress: item.TokenAddress,
			Standard: gen.AddressTokenTransferStandard(item.Standard),
			Kind:     gen.AddressTokenTransferKind(item.Kind),
			From:     item.From, To: item.To, TokenId: item.TokenID,
			Amount: item.Amount, Confidence: item.Confidence,
		}
		if item.Decimals != nil {
			value := int(*item.Decimals)
			items[index].Decimals = &value
		}
	}
	writeJSON(w, http.StatusOK, gen.AddressTokenTransferListResponse{
		Data: items, Meta: h.catalogPageMeta(r, page.NextCursor, page.Snapshot),
	})
}

func (h *Handler) addressActivityPage(
	w http.ResponseWriter,
	r *http.Request,
) (string, int, string, bool) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return "", 0, "", false
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return "", 0, "", false
	}
	return address, limit, cursor, true
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 256 {
		writeError(w, r, http.StatusBadRequest, "invalid_query", "q must contain 1 to 256 bytes", nil)
		return
	}
	limit, ok := parseLimit(w, r, 20)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.Search(r.Context(), query, cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.SearchResponse{Data: items, Meta: meta})
}

func (h *Handler) tokens(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TokenContracts(r.Context(), catalog.TokenListRequest{
		ChainID: h.chainID(), Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TokenContract, len(page.Items))
	for index := range page.Items {
		items[index] = tokenContractModel(page.Items[index])
	}
	meta := h.catalogPageMeta(r, page.NextCursor, page.Snapshot)
	writeJSON(w, http.StatusOK, gen.TokenListResponse{Data: items, Meta: meta})
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	item, err := h.catalog.TokenContract(r.Context(), h.chainID(), address)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TokenResponse{Data: tokenContractModel(item), Meta: h.meta(r)})
}

func (h *Handler) tokenTransfers(w http.ResponseWriter, r *http.Request) {
	address, limit, cursor, ok := parseCatalogAddressPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TokenEvents(r.Context(), catalog.TokenEventRequest{
		ChainID: h.chainID(), TokenAddress: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TokenEvent, len(page.Items))
	for index := range page.Items {
		items[index] = tokenEventModel(page.Items[index])
	}
	meta := h.catalogPageMeta(r, page.NextCursor, page.Snapshot)
	writeJSON(w, http.StatusOK, gen.TokenEventListResponse{Data: items, Meta: meta})
}

func (h *Handler) tokenHolders(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parseHolderPage(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TokenHolders(r.Context(), catalog.TokenHolderRequest{
		ChainID: h.chainID(), TokenAddress: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleHolderError(w, r, err)
		return
	}
	items := make([]gen.TokenHolder, len(page.Items))
	for index, item := range page.Items {
		items[index] = gen.TokenHolder{
			ChainId: item.ChainID, TokenAddress: item.TokenAddress,
			HolderAddress: item.HolderAddress, Balance: item.Balance,
			Confidence:          gen.StateConfidence(item.Confidence),
			ObservedBlockNumber: item.ObservedBlockNumber,
			ObservedBlockHash:   item.ObservedBlockHash,
		}
	}
	writeJSON(w, http.StatusOK, gen.TokenHolderListResponse{
		Data: items, Meta: h.tokenHolderMeta(r, page.NextCursor, page.Summary),
	})
}

func (h *Handler) tokenHolderCount(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	summary, err := h.catalog.TokenHolderCount(r.Context(), h.chainID(), address)
	if err != nil {
		h.handleHolderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TokenHolderCountResponse{
		Data: gen.TokenHolderCount{
			ChainId: summary.ChainID, TokenAddress: summary.TokenAddress,
			HolderCount: summary.HolderCount,
		},
		Meta: h.tokenHolderMeta(r, "", summary),
	})
}

func (h *Handler) nftOwner(w http.ResponseWriter, r *http.Request) {
	address, ok := parseAddressPath(w, r)
	if !ok {
		return
	}
	tokenID := r.PathValue("token_id")
	if !canonicalQuantity(tokenID) {
		writeError(w, r, http.StatusBadRequest, "invalid_token_id", "token_id must be a canonical decimal uint256", nil)
		return
	}
	item, err := h.catalog.NFTOwner(r.Context(), h.chainID(), address, tokenID)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.NFTOwnershipResponse{Data: nftOwnershipModel(item), Meta: h.meta(r)})
}
