package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/verify"
)

func (h *Handler) transactionTrace(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.catalog.TransactionTrace(r.Context(), h.chainID(), hash)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionTraceResponse{Data: transactionTraceModel(item), Meta: h.meta(r)})
}

func (h *Handler) transactionCalldata(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.catalog.TransactionCalldata(r.Context(), h.chainID(), hash)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionCalldataResponse{Data: transactionCalldataModel(item), Meta: h.meta(r)})
}

func (h *Handler) transactionFailure(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return
	}
	item, err := h.catalog.TransactionFailure(r.Context(), h.chainID(), hash)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrNotApplicable):
			writeError(w, r, http.StatusUnprocessableEntity, "failure_not_applicable", "transaction failure decoding is not applicable", nil)
		case errors.Is(err, catalog.ErrCorruptData):
			writeError(w, r, http.StatusServiceUnavailable, "failure_inconsistent", "transaction failure data is temporarily unavailable", nil)
		default:
			h.handleCatalogError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, gen.TransactionFailureResponse{Data: transactionFailureModel(item), Meta: h.meta(r)})
}

func (h *Handler) transactionTokenTransfers(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionTokenEvents(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TokenEvent, len(page.Items))
	for index := range page.Items {
		items[index] = tokenEventModel(page.Items[index])
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionTokenTransferResponse{
		Data: transactionTokenTransfersModel(page.Identity, items), Meta: meta,
	})
}

func (h *Handler) transactionInternalTransactions(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionInternalTransactions(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TransactionInternalTransaction, len(page.Items))
	for index, item := range page.Items {
		path := make([]int, len(item.Path))
		for pathIndex, value := range item.Path {
			path[pathIndex] = int(value)
		}
		items[index] = gen.TransactionInternalTransaction{
			Path: path, Depth: int(item.Depth), CallType: item.CallType,
			From: item.From, To: item.To, CreatedAddress: item.CreatedAddress, Value: item.Value,
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionInternalTransactionResponse{
		Data: transactionInternalTransactionsModel(page.Identity, items), Meta: meta,
	})
}

func (h *Handler) transactionLogs(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionLogs(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TransactionLog, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		topics := make([]gen.Hash, len(item.Topics))
		copy(topics, item.Topics)
		items[index] = gen.TransactionLog{
			Address: item.Address, LogIndex: item.LogIndex, Topics: topics, Data: item.Data,
			Decoding: transactionLogDecodingModel(item.Decoding),
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionLogResponse{
		Data: transactionLogsModel(page.Identity, items), Meta: meta,
	})
}

func transactionLogDecodingModel(value catalog.TransactionLogDecoding) gen.TransactionLogDecoding {
	if value.Status == "" {
		value.Status = "unavailable"
	}
	model := gen.TransactionLogDecoding{
		Status:     gen.TransactionLogDecodingStatus(value.Status),
		Arguments:  make([]gen.TransactionLogArgument, len(value.Arguments)),
		Candidates: make([]string, len(value.Candidates)),
		Attribution: gen.TransactionLogAttribution{
			Mode:      gen.TransactionLogAttributionMode(value.Attribution.Mode),
			TracePath: uint32PathModel(value.Attribution.TracePath),
		},
	}
	copy(model.Candidates, value.Candidates)
	for index, argument := range value.Arguments {
		model.Arguments[index] = gen.TransactionLogArgument{
			Name: argument.Name, Type: argument.Type, Indexed: argument.Indexed,
			Hashed: argument.Hashed, Value: argument.Value,
		}
	}
	if value.EventName != "" {
		model.EventName = &value.EventName
	}
	if value.Signature != "" {
		model.Signature = &value.Signature
	}
	if value.Confidence != "" {
		confidence := gen.TransactionLogDecodingConfidence(value.Confidence)
		model.Confidence = &confidence
	}
	if value.Warning != "" {
		model.Warning = &value.Warning
	}
	if value.ABISource != nil {
		model.AbiSource = abiSourceModel(value.ABISource)
	}
	if value.Attribution.ExecutionAddress != "" {
		address := gen.Address(value.Attribution.ExecutionAddress)
		model.Attribution.ExecutionAddress = &address
	}
	return model
}

func abiSourceModel(value *catalog.ABISource) *gen.ABISource {
	if value == nil {
		return nil
	}
	result := &gen.ABISource{Kind: gen.ABISourceKind(value.Kind)}
	if value.Address != "" {
		address := gen.Address(value.Address)
		result.Address = &address
	}
	if value.CodeHash != "" {
		codeHash := gen.Hash(value.CodeHash)
		result.CodeHash = &codeHash
	}
	return result
}

func uint32PathModel(path []uint32) []int {
	result := make([]int, len(path))
	for index, component := range path {
		result[index] = int(component)
	}
	return result
}

func (h *Handler) transactionStateChanges(w http.ResponseWriter, r *http.Request) {
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.catalog.TransactionStateChanges(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.TransactionStateChange, len(page.Items))
	for index := range page.Items {
		item := page.Items[index]
		items[index] = gen.TransactionStateChange{
			Address: item.Address, Kind: gen.TransactionStateChangeKind(item.Kind),
			StorageKey: item.StorageKey, Before: item.Before, After: item.After,
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionStateChangeResponse{
		Data: transactionStateChangesModel(page.Identity, items), Meta: meta,
	})
}

func (h *Handler) transactionAuthorizations(w http.ResponseWriter, r *http.Request) {
	if h.delegationHistory == nil {
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "authorization history is unavailable", nil)
		return
	}
	request, ok := h.transactionResourceRequest(w, r)
	if !ok {
		return
	}
	page, err := h.delegationHistory.TransactionAuthorizations(r.Context(), request)
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.EIP7702Authorization, len(page.Items))
	for index, item := range page.Items {
		items[index] = gen.EIP7702Authorization{
			Index: item.Index, ChainId: item.ChainID, Nonce: item.Nonce,
			Delegate: item.Delegate, YParity: item.YParity, R: item.R, S: item.S,
			SignatureStatus:   gen.EIP7702AuthorizationSignatureStatus(item.SignatureStatus),
			ApplicationStatus: gen.EIP7702AuthorizationApplicationStatus(item.ApplicationStatus),
		}
		items[index].Authority = item.Authority
		if item.SkipReason != nil {
			reason := gen.EIP7702AuthorizationSkipReason(*item.SkipReason)
			items[index].SkipReason = &reason
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.TransactionAuthorizationResponse{
		Data: gen.TransactionAuthorizations{
			ChainId: page.Identity.ChainID, BlockNumber: page.Identity.BlockNumber,
			BlockHash: page.Identity.BlockHash, TransactionHash: page.Identity.TransactionHash,
			TransactionIndex: page.Identity.TransactionIndex,
			State:            gen.TransactionAuthorizationsState(page.Identity.State), Items: items,
		},
		Meta: meta,
	})
}

func (h *Handler) addressDelegations(w http.ResponseWriter, r *http.Request) {
	if h.delegationHistory == nil {
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "delegation history is unavailable", nil)
		return
	}
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return
	}
	page, err := h.delegationHistory.AddressDelegations(r.Context(), catalog.AddressDelegationRequest{
		ChainID: h.chainID(), Address: address, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		h.handleCatalogError(w, r, err)
		return
	}
	items := make([]gen.DelegationHistoryItem, len(page.Items))
	for index, item := range page.Items {
		items[index] = gen.DelegationHistoryItem{
			Authority: item.Authority, Kind: gen.DelegationHistoryItemKind(item.Kind),
			Delegate: item.Delegate, PreviousDelegate: item.PreviousDelegate,
			BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
			TransactionHash: item.TransactionHash, TransactionIndex: item.TransactionIndex,
			AuthorizationIndex: item.AuthorizationIndex,
		}
	}
	meta := h.meta(r)
	if page.NextCursor != "" {
		meta.NextCursor = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, gen.DelegationHistoryResponse{Data: items, Meta: meta})
}

func (h *Handler) addressDelegation(w http.ResponseWriter, r *http.Request) {
	if h.delegationBindings == nil {
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "delegation state is unavailable", nil)
		return
	}
	address := r.PathValue("address")
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return
	}
	binding, err := h.delegationBindings.AddressDelegation(r.Context(), address)
	if err != nil {
		h.handleReaderError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gen.DelegationBindingResponse{Data: binding, Meta: h.meta(r)})
}

func (h *Handler) transactionResourceRequest(
	w http.ResponseWriter,
	r *http.Request,
) (catalog.TransactionResourceRequest, bool) {
	hash := strings.ToLower(r.PathValue("hash"))
	if !hashPattern.MatchString(hash) {
		writeError(w, r, http.StatusBadRequest, "invalid_transaction_hash", "transaction hash must be 32 bytes", nil)
		return catalog.TransactionResourceRequest{}, false
	}
	limit, cursor, ok := parseCatalogPage(w, r)
	if !ok {
		return catalog.TransactionResourceRequest{}, false
	}
	return catalog.TransactionResourceRequest{
		ChainID: h.chainID(), TransactionHash: hash, Cursor: cursor, Limit: limit,
	}, true
}

type verifierSubmission struct {
	Language           verify.Language       `json:"language"`
	CompilerVersion    string                `json:"compiler_version"`
	InputKind          string                `json:"input_kind"`
	Input              json.RawMessage       `json:"input"`
	Sources            map[string]string     `json:"sources"`
	EVMVersion         string                `json:"evm_version"`
	OptimizationRuns   *int                  `json:"optimization_runs"`
	Libraries          map[string]string     `json:"libraries"`
	Bytecodes          *verify.BytecodePair  `json:"bytecodes"`
	Contracts          []verify.BytecodePair `json:"contracts"`
	ContractNameHint   string                `json:"contract_name_hint"`
	RuntimeEntrypoint  string                `json:"runtime_entrypoint"`
	CreationEntrypoint string                `json:"creation_entrypoint"`
}
