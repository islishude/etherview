package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/mempool"
)

func (h *Handler) live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "live", "time": h.now().UTC()})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	if !h.runtimeReady() {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "runtime is not ready", nil)
		return
	}
	status, err := h.readinessStatus(r.Context())
	if err != nil || !status.CoreReady {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "core index is not ready", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.reader.Status(r.Context())
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	coverageStart, coverageEnd := quantity(snapshot.CoverageStart), quantity(snapshot.CoverageEnd)
	meta.CoverageStart, meta.CoverageEnd = &coverageStart, &coverageEnd
	writeJSON(w, http.StatusOK, gen.StatusResponse{
		Data: statusModel(h.cfg.Chain.ID, snapshot),
		Meta: meta,
	})
}

func (h *Handler) publicConfig(w http.ResponseWriter, r *http.Request) {
	features := map[string]bool{
		"trace":            h.cfg.Features.Trace,
		"mempool":          h.cfg.Features.Mempool,
		"historical_state": h.cfg.Features.HistoricalState,
		"verification":     h.verificationSubmitter != nil && h.verificationTargets != nil,
		"sourcify":         h.cfg.Features.Sourcify,
		"nft_metadata":     h.cfg.Features.NFTMetadata,
		"pricing":          h.cfg.Features.Pricing,
		"ens":              h.cfg.Features.ENS,
		"user_auth":        h.cfg.Features.UserAuth,
		"user_api_keys":    h.cfg.Features.UserAPIKeys,
		"x402_billing":     h.cfg.Features.X402Billing,
	}
	data := gen.PublicConfig{
		ChainId:        quantity(h.cfg.Chain.ID),
		ChainName:      h.cfg.Chain.Name,
		NativeSymbol:   h.cfg.Chain.NativeSymbol,
		NativeName:     h.cfg.Chain.NativeName,
		NativeDecimals: int(h.cfg.Chain.NativeDecimals),
		Features:       features,
	}
	if len(h.cfg.Wallet.AddChain.RPCURLs) > 0 {
		var blockExplorerURLs, iconURLs *[]string
		if len(h.cfg.Wallet.AddChain.BlockExplorerURLs) > 0 {
			values := slices.Clone(h.cfg.Wallet.AddChain.BlockExplorerURLs)
			blockExplorerURLs = &values
		}
		if len(h.cfg.Wallet.AddChain.IconURLs) > 0 {
			values := slices.Clone(h.cfg.Wallet.AddChain.IconURLs)
			iconURLs = &values
		}
		data.WalletAddChain = &gen.WalletAddChainConfig{
			ChainId: quantity(h.cfg.Chain.ID), ChainName: h.cfg.Chain.Name,
			NativeCurrency: gen.WalletNativeCurrency{
				Name: h.cfg.Chain.NativeName, Symbol: h.cfg.Chain.NativeSymbol,
				Decimals: int(h.cfg.Chain.NativeDecimals),
			},
			RpcUrls:           slices.Clone(h.cfg.Wallet.AddChain.RPCURLs),
			BlockExplorerUrls: blockExplorerURLs, IconUrls: iconURLs,
		}
	}
	writeJSON(w, http.StatusOK, gen.PublicConfigResponse{Data: data, Meta: h.meta(r)})
}

func (h *Handler) blocks(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.Blocks(r.Context(), cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.BlockListResponse{Data: items, Meta: meta})
}

func (h *Handler) block(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validBlockID(id) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_id", "block id must be a decimal/hex number or block hash", nil)
		return
	}
	item, err := h.reader.Block(r.Context(), id)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.BlockResponse{Data: item, Meta: h.meta(r)})
}

func (h *Handler) blockTransactions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validBlockID(id) {
		writeError(w, r, http.StatusBadRequest, "invalid_block_id", "block id must be a decimal/hex number or block hash", nil)
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.BlockTransactions(r.Context(), id, cursor, limit)
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

func (h *Handler) genesisAccounts(w http.ResponseWriter, r *http.Request) {
	if h.genesis == nil {
		h.handleReaderError(w, r, NewCapabilityUnavailableError(
			"genesis_state", "unavailable", "genesis_state_not_configured",
		))
		return
	}
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.genesis.GenesisAccounts(r.Context(), cursor, limit)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, gen.GenesisAccountListResponse{Data: items, Meta: meta})
}

func (h *Handler) transaction(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.transactionReader.Transaction(r.Context(), strings.ToLower(hash))
	if err == nil {
		detail, modelErr := includedTransactionDetail(item)
		if modelErr != nil {
			h.logger.ErrorContext(r.Context(), "encode included transaction detail", "request_id", requestIDFrom(r.Context()))
			writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, gen.TransactionResponse{Data: detail, Meta: h.meta(r)})
		return
	}
	if !errors.Is(err, ErrNotFound) {
		h.handleReaderError(w, r, err)
		return
	}
	if !h.cfg.Features.Mempool {
		h.handleReaderError(w, r, ErrNotFound)
		return
	}
	if h.mempool == nil {
		h.handleMempoolError(w, r, mempool.CapabilityError{State: mempool.StateUnavailable, Code: "reader_unavailable"})
		return
	}
	mempoolDetail, err := h.mempool.Lookup(r.Context(), strings.ToLower(hash))
	if err != nil {
		if errors.Is(err, mempool.ErrNotFound) {
			h.handleReaderError(w, r, ErrNotFound)
			return
		}
		h.handleMempoolError(w, r, err)
		return
	}
	detail, err := mempoolTransactionDetail(mempoolDetail)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "encode mempool transaction detail", "request_id", requestIDFrom(r.Context()))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionResponse{Data: detail, Meta: h.meta(r)})
}

func includedTransactionDetail(transaction gen.Transaction) (gen.TransactionDetail, error) {
	var detail gen.TransactionDetail
	err := detail.FromIncludedTransactionDetail(gen.IncludedTransactionDetail{
		Kind: gen.IncludedTransactionDetailKindIncluded, Transaction: transaction,
	})
	return detail, err
}

func mempoolTransactionDetail(detail mempool.Detail) (gen.TransactionDetail, error) {
	var model gen.TransactionDetail
	switch detail.Kind {
	case mempool.DetailPending:
		err := model.FromPendingTransactionDetail(gen.PendingTransactionDetail{
			Kind: gen.PendingTransactionDetailKindPending, Transaction: pendingTransactionModel(detail.Transaction),
		})
		return model, err
	case mempool.DetailReplaced:
		err := model.FromReplacedTransactionDetail(gen.ReplacedTransactionDetail{
			Kind: gen.ReplacedTransactionDetailKindReplaced, Transaction: pendingTransactionModel(detail.Transaction),
			ReplacementHash: detail.ReplacementHash, ReplacedAt: detail.ReplacedAt.UTC(),
		})
		return model, err
	default:
		return gen.TransactionDetail{}, fmt.Errorf("unknown mempool transaction detail kind %q", detail.Kind)
	}
}

func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	items, next, err := h.reader.Transactions(r.Context(), cursor, limit)
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

func (h *Handler) pendingTransactions(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return
	}
	if h.mempool == nil {
		state, reason := mempool.StateUnavailable, "feature_disabled"
		if h.cfg.Features.Mempool {
			reason = "reader_unavailable"
		}
		writeError(w, r, http.StatusServiceUnavailable, "mempool_unavailable", "pending transaction capability is unavailable", map[string]any{
			"state": state, "reason": reason,
		})
		return
	}
	page, err := h.mempool.Pending(r.Context(), cursor, limit)
	if err != nil {
		h.handleMempoolError(w, r, err)
		return
	}
	items := make([]gen.PendingTransaction, len(page.Items))
	for index := range page.Items {
		items[index] = pendingTransactionModel(page.Items[index])
	}
	meta := gen.PendingMeta{
		Capability: gen.StageStateComplete,
		ChainId:    strconv.FormatUint(h.cfg.Chain.ID, 10), Endpoint: page.Snapshot.Endpoint,
		ExpiresAt: page.Snapshot.ExpiresAt.UTC(), RequestId: requestIDFrom(r.Context()),
		SnapshotAt: page.Snapshot.ObservedAt.UTC(), SnapshotId: strconv.FormatInt(page.Snapshot.ID, 10),
		TransactionCount: strconv.Itoa(page.Snapshot.TransactionCount),
	}
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.PendingTransactionListResponse{Data: items, Meta: meta})
}

func pendingTransactionModel(transaction mempool.Transaction) gen.PendingTransaction {
	model := gen.PendingTransaction{
		Endpoint: transaction.Endpoint, ExpiresAt: transaction.ExpiresAt.UTC(),
		FirstSeenAt: transaction.FirstSeenAt.UTC(), LastSeenAt: transaction.LastSeenAt.UTC(),
		Hash: transaction.Hash, From: transaction.From, Nonce: transaction.Nonce,
		Value: transaction.Value, Gas: transaction.Gas, Input: transaction.Input,
	}
	model.To = (*gen.Address)(transaction.To)
	model.GasPrice = (*gen.Quantity)(transaction.GasPrice)
	model.MaxFeePerGas = (*gen.Quantity)(transaction.MaxFeePerGas)
	model.MaxPriorityFeePerGas = (*gen.Quantity)(transaction.MaxPriorityFeePerGas)
	model.Type = (*gen.Quantity)(transaction.Type)
	model.ReplacesHash = (*gen.Hash)(transaction.ReplacesHash)
	return model
}

func (h *Handler) handleMempoolError(w http.ResponseWriter, r *http.Request, err error) {
	var capability mempool.CapabilityError
	switch {
	case errors.As(err, &capability):
		details := map[string]any{"state": capability.State, "reason": capability.Code}
		if !capability.LastAttemptAt.IsZero() {
			details["last_attempt_at"] = capability.LastAttemptAt.UTC()
		}
		writeError(w, r, http.StatusServiceUnavailable, "mempool_unavailable", "pending transaction capability is unavailable", details)
	case errors.Is(err, mempool.ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or its pending snapshot expired", nil)
	default:
		h.logger.ErrorContext(r.Context(), "mempool query failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
	}
}

func (h *Handler) address(w http.ResponseWriter, r *http.Request) {
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	item, err := h.reader.Address(r.Context(), strings.ToLower(address))
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	if h.cfg.Features.ENS && h.addressNames != nil {
		parsed := common.HexToAddress(address)
		if primary, primaryErr := h.addressNames.ResolveCurrentPrimary(r.Context(), parsed); primaryErr == nil &&
			primary.Outcome == ensresolver.OutcomeResolved {
			item.PrimaryName = primaryNameModel(primary)
		}
	}
	writeJSON(w, http.StatusOK, gen.AddressResponse{Data: item, Meta: h.meta(r)})
}

func (h *Handler) addressNamesPage(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Features.ENS || h.addressNames == nil {
		h.handleReaderError(w, r, NewCapabilityUnavailableError("name", "unavailable", "not_configured"))
		return
	}
	raw := r.URL.Query().Get("addresses")
	if len(raw) < 42 || len(raw) > 4299 {
		writeError(w, r, http.StatusBadRequest, "invalid_addresses", "addresses must contain 1 to 100 comma-separated addresses", nil)
		return
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > 100 {
		writeError(w, r, http.StatusBadRequest, "invalid_addresses", "addresses must contain 1 to 100 comma-separated addresses", nil)
		return
	}
	addresses := make([]common.Address, len(parts))
	seen := make(map[common.Address]struct{}, len(parts))
	for index, part := range parts {
		if !addressPattern.MatchString(part) {
			writeError(w, r, http.StatusBadRequest, "invalid_addresses", "every address must contain exactly 20 bytes", nil)
			return
		}
		address := common.HexToAddress(part)
		if _, duplicate := seen[address]; duplicate {
			writeError(w, r, http.StatusBadRequest, "duplicate_address", "addresses must be unique", nil)
			return
		}
		seen[address] = struct{}{}
		addresses[index] = address
	}
	snapshot := r.URL.Query().Get("snapshot")
	if len(snapshot) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "snapshot is too long", nil)
		return
	}
	resolved, nextSnapshot, err := h.addressNames.ResolveAddressBatch(r.Context(), addresses, snapshot)
	if err != nil {
		h.handleENSReaderError(w, r, err)
		return
	}
	if len(resolved) != len(addresses) || nextSnapshot == "" || len(nextSnapshot) > maximumOpaqueCursorLength {
		h.handleReaderError(w, r, errors.New("ENS address-name service returned an invalid batch"))
		return
	}
	items := make([]gen.AddressNameLookup, len(resolved))
	for index, value := range resolved {
		if value.Address != addresses[index] {
			h.handleReaderError(w, r, errors.New("ENS address-name service reordered a batch"))
			return
		}
		address, checksumErr := checksumAddress(value.Address.Hex())
		if checksumErr != nil {
			h.handleReaderError(w, r, checksumErr)
			return
		}
		item := gen.AddressNameLookup{Address: address}
		switch {
		case value.Outcome == ensresolver.OutcomeResolved:
			item.State = gen.AddressNameLookupStateResolved
			item.PrimaryName = primaryNameModel(value)
			if item.PrimaryName == nil {
				h.handleReaderError(w, r, errors.New("ENS address-name service returned an invalid primary name"))
				return
			}
		case value.Outcome == ensresolver.OutcomeNoRecord:
			item.State = gen.AddressNameLookupStateNotFound
		case value.Code != "":
			item.State = gen.AddressNameLookupStateUnavailable
			code := value.Code
			item.Code = &code
		default:
			h.handleReaderError(w, r, errors.New("ENS address-name service returned an invalid outcome"))
			return
		}
		items[index] = item
	}
	writeJSON(w, http.StatusOK, gen.AddressNamePageResponse{
		Data: gen.AddressNamePage{Items: items, Snapshot: nextSnapshot}, Meta: h.meta(r),
	})
}

func primaryNameModel(value ensresolver.PrimaryResolution) *gen.PrimaryName {
	if value.Outcome != ensresolver.OutcomeResolved || value.Name == "" {
		return nil
	}
	source := gen.PrimaryNameSource(value.Source)
	if !source.Valid() {
		return nil
	}
	return &gen.PrimaryName{Name: value.Name, Source: source}
}

func (h *Handler) handleENSReaderError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ensresolver.ErrSnapshotInvalid) {
		h.handleReaderError(w, r, ErrInvalidCursor)
		return
	}
	if resolution, ok := errors.AsType[*ensresolver.ResolutionError](err); ok {
		capability, state, code := resolution.CapabilityDetails()
		h.handleReaderError(w, r, NewCapabilityUnavailableError(capability, state, code))
		return
	}
	h.handleReaderError(w, r, err)
}
