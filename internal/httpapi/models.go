package httpapi

import (
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/analytics"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/metadata"
)

func (h *Handler) handleReaderError(w http.ResponseWriter, r *http.Request, err error) {
	var capability *CapabilityUnavailableError
	switch {
	case errors.As(err, &capability) && capability.valid():
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "required capability is unavailable", map[string]any{
			"capability": capability.Capability,
			"state":      capability.State,
			"code":       capability.Code,
		})
	case errors.Is(err, ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	case errors.Is(err, ErrUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "capability_unavailable", "required capability is unavailable", nil)
	case errors.Is(err, ErrNotReady):
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "indexed data is not ready", nil)
	case errors.Is(err, ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale after a canonical change", nil)
	case errors.Is(err, ErrInvalidInput):
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_query", "query input is invalid", nil)
	default:
		h.logger.ErrorContext(r.Context(), "query failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
	}
}

func (h *Handler) handleCatalogError(w http.ResponseWriter, r *http.Request, err error) {
	var stageError catalog.StageUnavailableError
	switch {
	case errors.As(err, &stageError):
		details := map[string]any{
			"stage": stageError.Stage,
			"state": stageError.State,
		}
		if stageError.BlockNumber != "" {
			details["block_number"] = stageError.BlockNumber
		}
		if stageError.BlockHash != "" {
			details["block_hash"] = stageError.BlockHash
		}
		writeError(w, r, http.StatusServiceUnavailable, "stage_unavailable", "required enrichment stage is unavailable", details)
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	case errors.Is(err, catalog.ErrInvalidCursor):
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or stale after a canonical change", nil)
	case errors.Is(err, catalog.ErrInvalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_query", "catalog query is invalid", nil)
	case errors.Is(err, catalog.ErrLimitExceeded):
		writeError(w, r, http.StatusUnprocessableEntity, "result_limit_exceeded", "catalog result exceeds the configured safety limit", nil)
	case errors.Is(err, catalog.ErrNotApplicable):
		writeError(w, r, http.StatusUnprocessableEntity, "calldata_not_applicable", "transaction calldata decoding is not applicable", nil)
	default:
		h.logger.ErrorContext(r.Context(), "catalog query failed", "request_id", requestIDFrom(r.Context()), "error_type", fmt.Sprintf("%T", err))
		writeError(w, r, http.StatusInternalServerError, "query_failed", "query failed", nil)
	}
}

func (h *Handler) chainID() string {
	return strconv.FormatUint(h.cfg.Chain.ID, 10)
}

func (h *Handler) catalogPageMeta(r *http.Request, next string, snapshot catalog.Snapshot) gen.Meta {
	meta := h.meta(r)
	if next != "" {
		meta.NextCursor = &next
	}
	if snapshot.BlockNumber != "" {
		meta.CoverageEnd = &snapshot.BlockNumber
	}
	return meta
}

func parseCatalogPage(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	limit, ok := parseLimit(w, r, 25)
	if !ok {
		return 0, "", false
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maximumOpaqueCursorLength {
		writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is too long", nil)
		return 0, "", false
	}
	return limit, cursor, true
}

func parseCatalogAddressPage(
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

func parseAddressPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	address := strings.ToLower(r.PathValue("address"))
	if !addressPattern.MatchString(address) {
		writeError(w, r, http.StatusBadRequest, "invalid_address", "address must be 20 bytes", nil)
		return "", false
	}
	return address, true
}

func canonicalQuantity(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	integer, ok := new(big.Int).SetString(value, 10)
	if !ok || integer.Sign() < 0 {
		return false
	}
	return integer.BitLen() <= 256
}

func tokenContractModel(item catalog.TokenContract) gen.TokenContract {
	model := gen.TokenContract{
		ChainId: item.ChainID, Address: item.Address, CodeHash: item.CodeHash,
		Standard: gen.TokenContractStandard(item.Standard), Confidence: gen.TokenContractConfidence(item.Confidence),
		Name: item.Name, Symbol: item.Symbol, TotalSupply: item.TotalSupply,
		MetadataState: item.MetadataState, ObservedBlockNumber: item.ObservedBlockNumber,
		ObservedBlockHash: item.ObservedBlockHash, UpdatedAt: item.UpdatedAt.UTC(),
	}
	if item.Decimals != nil {
		value := int(*item.Decimals)
		model.Decimals = &value
	}
	return model
}

func tokenEventModel(item catalog.TokenEvent) gen.TokenEvent {
	model := gen.TokenEvent{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		LogIndex: item.LogIndex, SubIndex: item.SubIndex, TransactionHash: item.TransactionHash,
		TokenAddress: item.TokenAddress, Standard: item.Standard, Kind: item.Kind,
		Operator: item.Operator, From: item.From, To: item.To, TokenId: item.TokenID,
		Amount: item.Amount, Confidence: item.Confidence,
	}
	if item.Decimals != nil {
		value := int(*item.Decimals)
		model.Decimals = &value
	}
	return model
}

func catalogSnapshotModel(snapshot catalog.Snapshot) gen.CatalogSnapshot {
	return gen.CatalogSnapshot{
		ChainId: snapshot.ChainID, BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
	}
}

func nftOwnershipModel(item catalog.NFTOwnership) gen.NFTOwnership {
	return gen.NFTOwnership{
		ChainId: item.ChainID, TokenAddress: item.TokenAddress, TokenId: item.TokenID,
		Owner: item.Owner, Balance: item.Balance, Confidence: gen.StateConfidence(item.Confidence),
		Snapshot: catalogSnapshotModel(item.Snapshot),
	}
}

func nftMetadataModel(chainID, address, tokenID string, item metadata.NFTMetadata) gen.NFTMetadata {
	attributes := make([]gen.NFTMetadataAttribute, len(item.Attributes))
	for index := range item.Attributes {
		attribute := item.Attributes[index]
		attributes[index] = gen.NFTMetadataAttribute{TraitType: attribute.TraitType, Value: attribute.Value}
		if attribute.DisplayType != "" {
			attributes[index].DisplayType = &attribute.DisplayType
		}
	}
	model := gen.NFTMetadata{
		ChainId: chainID, TokenAddress: address, TokenId: tokenID,
		State: gen.NFTMetadataState(item.State),
		Observation: gen.CatalogSnapshot{
			ChainId: chainID, BlockNumber: quantity(item.Observation.BlockNumber), BlockHash: item.Observation.BlockHash.Hex(),
		},
		NameTruncated: item.NameTruncated, DescriptionTruncated: item.DescriptionTruncated,
		Attributes: attributes, OmittedAttributeCount: item.OmittedAttributeCount,
		ContentStale: item.ContentStale,
		Image:        gen.NFTMetadataImage{State: gen.NFTMetadataImageState(item.Image.State)},
	}
	if item.ContentObservation != nil {
		model.ContentObservation = &gen.CatalogSnapshot{
			ChainId: chainID, BlockNumber: quantity(item.ContentObservation.BlockNumber),
			BlockHash: item.ContentObservation.BlockHash.Hex(),
		}
	}
	if item.Name != "" {
		model.Name = &item.Name
	}
	if item.Description != "" {
		model.Description = &item.Description
	}
	if item.Image.State == metadata.NFTMetadataImageAvailable {
		model.Image.Url = &item.Image.URL
		scheme := gen.NFTMetadataImageSourceScheme(item.Image.SourceScheme)
		model.Image.SourceScheme = &scheme
	}
	return model
}

func nftBalanceModel(item catalog.NFTBalance) gen.NFTBalance {
	return gen.NFTBalance{
		ChainId: item.ChainID, Owner: item.Owner, TokenAddress: item.TokenAddress,
		TokenId: item.TokenID, Balance: item.Balance, Confidence: gen.StateConfidence(item.Confidence),
	}
}

func blockStatModel(item catalog.BlockStat) gen.BlockStat {
	return gen.BlockStat{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		TransactionCount: item.TransactionCount, GasUsed: item.GasUsed, GasLimit: item.GasLimit,
		BaseFeePerGas: item.BaseFeePerGas, BlobGasUsed: item.BlobGasUsed,
		ExcessBlobGas: item.ExcessBlobGas, BlobBaseFeePerGas: item.BlobBaseFeePerGas,
		BurnedWei: item.BurnedWei, BlobBurnedWei: item.BlobBurnedWei,
		BlockTimestamp: item.BlockTimestamp, BlockIntervalSeconds: item.BlockIntervalSeconds,
		TransactionsPerSecond: item.TransactionsPerSecond,
		TokenEventCount:       item.TokenEventCount, TokenTransferCount: item.TokenTransferCount,
		NftTransferCount: item.NFTTransferCount, ComputedAt: item.ComputedAt.UTC(),
	}
}

func aggregateStatsModel(item catalog.AggregateStats) gen.AggregateStats {
	return gen.AggregateStats{
		ChainId: item.ChainID, FromBlock: item.FromBlock, ToBlock: item.ToBlock,
		Snapshot: gen.CatalogSnapshot{
			ChainId: item.Snapshot.ChainID, BlockNumber: item.Snapshot.BlockNumber,
			BlockHash: item.Snapshot.BlockHash,
		},
		BlockCount: item.BlockCount, TransactionCount: item.TransactionCount,
		GasUsed: item.GasUsed, BurnedWei: item.BurnedWei, BlobBurnedWei: item.BlobBurnedWei,
		TokenEventCount: item.TokenEventCount, TokenTransferCount: item.TokenTransferCount,
		NftTransferCount: item.NFTTransferCount, AverageTps: item.AverageTPS,
		Completeness: gen.AggregateStatsCompleteness{
			Core: item.CoreComplete, Stats: item.StatsComplete, Token: item.TokenComplete,
		},
	}
}

func chartOverviewModel(item analytics.Overview) gen.ChartOverview {
	metrics := make([]gen.ChartPreview, len(item.Metrics))
	for index := range item.Metrics {
		preview := item.Metrics[index]
		metrics[index] = gen.ChartPreview{
			Metric: gen.ChartMetric(preview.Metric), CurrentValue: preview.CurrentValue,
			PreviousValue: preview.PreviousValue, ChangePercent: preview.ChangePercent,
			Points: chartPointsModel(preview.Points),
		}
	}
	return gen.ChartOverview{
		GeneratedAt: item.GeneratedAt.UTC(), Snapshot: chartSnapshotModel(item.Snapshot),
		Coverage: chartCoverageModel(item.Coverage), Metrics: metrics, Pending: item.Pending,
	}
}

func chartSeriesModel(item analytics.Series) gen.ChartMetricSeries {
	return gen.ChartMetricSeries{
		Metric: gen.ChartMetric(item.Metric), Interval: gen.ChartInterval(item.Interval),
		FromTime: item.FromTime.UTC(), ToTime: item.ToTime.UTC(),
		Points: chartPointsModel(item.Points),
		Summary: gen.ChartSummary{
			Current: item.Summary.Current, Highest: item.Summary.Highest,
			Lowest: item.Summary.Lowest, Total: item.Summary.Total, Average: item.Summary.Average,
		},
		Snapshot: chartSnapshotModel(item.Snapshot), Coverage: chartCoverageModel(item.Coverage),
	}
}

func chartPointsModel(items []analytics.Point) []gen.ChartPoint {
	result := make([]gen.ChartPoint, len(items))
	for index := range items {
		item := items[index]
		result[index] = gen.ChartPoint{
			BucketStart: item.BucketStart.UTC(), BucketEnd: item.BucketEnd.UTC(),
			Value: item.Value, Partial: item.Partial,
			FromBlock: item.FromBlock, ToBlock: item.ToBlock,
		}
	}
	return result
}

func chartSnapshotModel(item analytics.Snapshot) gen.CatalogSnapshot {
	return gen.CatalogSnapshot{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
	}
}

func chartCoverageModel(item analytics.Coverage) gen.ChartCoverage {
	state := gen.ChartCoverageBackfillStatePartial
	if item.Complete {
		state = gen.ChartCoverageBackfillStateComplete
	} else if item.AvailableFrom == nil {
		state = gen.ChartCoverageBackfillStateEmpty
	}
	return gen.ChartCoverage{
		AvailableFrom: item.AvailableFrom, AvailableTo: item.AvailableTo,
		Complete: item.Complete, DirtyHours: item.DirtyHours,
		BackfillState: state, BackfillProgress: item.Progress,
	}
}

func transactionTraceModel(item catalog.TransactionTrace) gen.TransactionTrace {
	frames := make([]gen.TraceFrame, len(item.Frames))
	for index := range item.Frames {
		frame := item.Frames[index]
		path, parentPath := uint32PathModel(frame.Path), uint32PathModel(frame.ParentPath)
		frames[index] = gen.TraceFrame{
			Path: path, ParentPath: parentPath, Depth: int(frame.Depth), CallType: frame.CallType,
			From: frame.From, To: frame.To, CreatedAddress: frame.CreatedAddress,
			Value: frame.Value, Gas: frame.Gas, GasUsed: frame.GasUsed,
			Input: frame.Input, Output: frame.Output, Error: frame.Error,
			DirectReverted: frame.DirectReverted, Reverted: frame.Reverted,
		}
		if frame.Execution != nil {
			execution := gen.TraceExecution{
				ContextAddress: frame.Execution.ContextAddress,
				Resolution:     gen.TraceExecutionResolution(frame.Execution.Resolution),
			}
			if frame.Execution.Address != "" {
				execution.Address = &frame.Execution.Address
			}
			if frame.Execution.CodeHash != "" {
				execution.CodeHash = &frame.Execution.CodeHash
			}
			frames[index].Execution = execution
		}
		if frame.Decoding != nil {
			frames[index].Decoding = traceCallDecodingModel(frame.Decoding)
		}
	}
	return gen.TransactionTrace{
		ChainId: item.ChainID, BlockNumber: item.BlockNumber, BlockHash: item.BlockHash,
		TransactionHash: item.TransactionHash, TransactionIndex: item.TransactionIndex,
		State: gen.TransactionTraceState(item.State), Frames: frames,
	}
}

func transactionCalldataModel(item catalog.TransactionCalldata) gen.TransactionCalldata {
	execution := gen.TransactionExecution{
		ContextAddress: item.Execution.ContextAddress,
		Resolution:     gen.TransactionExecutionResolution(item.Execution.Resolution),
		EvidenceSource: gen.TransactionExecutionEvidenceSource(item.Execution.EvidenceSource),
	}
	if item.Execution.Address != "" {
		execution.Address = &item.Execution.Address
	}
	if item.Execution.CodeHash != "" {
		execution.CodeHash = &item.Execution.CodeHash
	}
	decoding := gen.TransactionCalldataDecoding{
		Status:     gen.TransactionCalldataDecodingStatus(item.Decoding.Status),
		Inputs:     transactionCalldataInputsModel(item.Decoding.Inputs),
		Candidates: append([]string{}, item.Decoding.Candidates...),
		AbiSource:  abiSourceModel(item.Decoding.ABISource),
	}
	if item.Decoding.FunctionName != "" {
		decoding.FunctionName = &item.Decoding.FunctionName
	}
	if item.Decoding.Signature != "" {
		decoding.Signature = &item.Decoding.Signature
	}
	if item.Decoding.Confidence != "" {
		confidence := gen.TransactionCalldataDecodingConfidence(item.Decoding.Confidence)
		decoding.Confidence = &confidence
	}
	if item.Decoding.Warning != "" {
		decoding.Warning = &item.Decoding.Warning
	}
	return gen.TransactionCalldata{
		ChainId: item.Identity.ChainID, BlockNumber: item.Identity.BlockNumber,
		BlockHash: item.Identity.BlockHash, TransactionHash: item.Identity.TransactionHash,
		TransactionIndex: item.Identity.TransactionIndex, State: gen.TransactionCalldataState(item.Identity.State),
		Input: item.Input, Execution: execution, Decoding: decoding,
	}
}

func transactionFailureModel(item catalog.TransactionFailure) gen.TransactionFailure {
	decoding := gen.TransactionFailureDecoding{
		Status:     gen.TransactionFailureDecodingStatus(item.Decoding.Status),
		Arguments:  transactionCalldataInputsModel(item.Decoding.Arguments),
		Candidates: append([]string{}, item.Decoding.Candidates...),
		AbiSource:  abiSourceModel(item.Decoding.ABISource), Reason: item.Decoding.Reason,
	}
	if item.Decoding.ErrorName != "" {
		decoding.ErrorName = &item.Decoding.ErrorName
	}
	if item.Decoding.Signature != "" {
		decoding.Signature = &item.Decoding.Signature
	}
	if item.Decoding.Confidence != "" {
		confidence := gen.TransactionFailureDecodingConfidence(item.Decoding.Confidence)
		decoding.Confidence = &confidence
	}
	if item.Decoding.Warning != "" {
		decoding.Warning = &item.Decoding.Warning
	}
	result := gen.TransactionFailure{
		ChainId: item.Identity.ChainID, BlockNumber: item.Identity.BlockNumber,
		BlockHash: item.Identity.BlockHash, TransactionHash: item.Identity.TransactionHash,
		TransactionIndex: item.Identity.TransactionIndex,
		State:            gen.TransactionFailureState(item.Identity.State), Error: item.Error,
		RevertData: item.RevertData, Decoding: decoding,
	}
	if item.Execution != nil {
		execution := gen.TraceExecution{
			ContextAddress: item.Execution.ContextAddress,
			Resolution:     gen.TraceExecutionResolution(item.Execution.Resolution),
		}
		if item.Execution.Address != "" {
			execution.Address = &item.Execution.Address
		}
		if item.Execution.CodeHash != "" {
			execution.CodeHash = &item.Execution.CodeHash
		}
		result.Execution = &execution
	}
	return result
}

func traceCallDecodingModel(value *catalog.TraceCallDecoding) *gen.TraceCallDecoding {
	if value == nil {
		return nil
	}
	result := &gen.TraceCallDecoding{
		Kind:   gen.TraceCallDecodingKind(value.Kind),
		Status: gen.TraceCallDecodingStatus(value.Status), Inputs: abiValuesModel(value.Inputs),
		OutputStatus: gen.TraceCallDecodingOutputStatus(value.OutputStatus), Outputs: abiValuesModel(value.Outputs),
		Candidates: append([]string{}, value.Candidates...), AbiSource: abiSourceModel(value.ABISource),
	}
	if value.FunctionName != "" {
		result.FunctionName = &value.FunctionName
	}
	if value.Signature != "" {
		result.Signature = &value.Signature
	}
	if value.Confidence != "" {
		confidence := gen.TraceCallDecodingConfidence(value.Confidence)
		result.Confidence = &confidence
	}
	if value.Warning != "" {
		result.Warning = &value.Warning
	}
	if value.Revert != nil {
		result.Revert = traceRevertDecodingModel(value.Revert)
	}
	return result
}

func traceRevertDecodingModel(value *catalog.TraceRevertDecoding) *gen.TraceRevertDecoding {
	if value == nil {
		return nil
	}
	result := &gen.TraceRevertDecoding{
		Status: gen.TraceRevertDecodingStatus(value.Status), Arguments: abiValuesModel(value.Arguments),
		Candidates: append([]string{}, value.Candidates...), AbiSource: abiSourceModel(value.ABISource),
	}
	if value.ErrorName != "" {
		result.ErrorName = &value.ErrorName
	}
	if value.Signature != "" {
		result.Signature = &value.Signature
	}
	if value.Confidence != "" {
		confidence := gen.TraceRevertDecodingConfidence(value.Confidence)
		result.Confidence = &confidence
	}
	if value.Warning != "" {
		result.Warning = &value.Warning
	}
	return result
}

func abiValuesModel(values []catalog.ABIValue) []gen.ABIValue {
	result := make([]gen.ABIValue, len(values))
	for index, value := range values {
		result[index] = gen.ABIValue{Name: value.Name, Type: value.Type, Value: value.Value}
	}
	return result
}

func transactionCalldataInputsModel(values []catalog.TransactionCalldataInput) []gen.TransactionCalldataInput {
	result := make([]gen.TransactionCalldataInput, len(values))
	for index, value := range values {
		result[index] = gen.TransactionCalldataInput{
			Name: value.Name, Type: value.Type, Value: value.Value,
			Components: transactionCalldataParametersModel(value.Components),
		}
		if value.InternalType != "" {
			result[index].InternalType = &value.InternalType
		}
	}
	return result
}

func transactionCalldataParametersModel(
	values []catalog.TransactionCalldataParameter,
) []gen.TransactionCalldataParameter {
	result := make([]gen.TransactionCalldataParameter, len(values))
	for index, value := range values {
		result[index] = gen.TransactionCalldataParameter{
			Name: value.Name, Type: value.Type,
			Components: transactionCalldataParametersModel(value.Components),
		}
		if value.InternalType != "" {
			result[index].InternalType = &value.InternalType
		}
	}
	return result
}

func transactionTokenTransfersModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TokenEvent,
) gen.TransactionTokenTransfers {
	return gen.TransactionTokenTransfers{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionTokenTransfersState(identity.State), Items: items,
	}
}

func transactionInternalTransactionsModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TransactionInternalTransaction,
) gen.TransactionInternalTransactions {
	return gen.TransactionInternalTransactions{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionInternalTransactionsState(identity.State), Items: items,
	}
}

func transactionLogsModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TransactionLog,
) gen.TransactionLogs {
	return gen.TransactionLogs{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionLogsState(identity.State), Items: items,
	}
}

func transactionStateChangesModel(
	identity catalog.TransactionResourceIdentity,
	items []gen.TransactionStateChange,
) gen.TransactionStateChanges {
	return gen.TransactionStateChanges{
		ChainId: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: gen.TransactionStateChangesState(identity.State), Items: items,
	}
}
