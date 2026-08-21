package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"math/big"
	"slices"
	"strings"
)

func (catalog *Postgres) BlockStats(ctx context.Context, request BlockStatsRequest) ([]BlockStat, error) {
	if catalog == nil || catalog.db == nil {
		return nil, errors.New("catalog database is nil")
	}
	if err := validateChainID(request.ChainID); err != nil {
		return nil, err
	}
	expectedCount, err := decimalRangeSize(request.FromBlock, request.ToBlock, catalog.options.MaxChartPoints)
	if err != nil {
		return nil, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := readCanonicalSnapshot(ctx, tx, request.ChainID)
	if err != nil {
		return nil, err
	}
	if compareUnsignedDecimal(request.ToBlock, snapshot.BlockNumber) > 0 {
		return nil, fmt.Errorf("%w: statistics range exceeds the canonical snapshot", ErrInvalidInput)
	}
	if err := requireStageRange(ctx, tx, request.ChainID, request.FromBlock, request.ToBlock, StageStats); err != nil {
		return nil, err
	}
	if err := requireStageRange(ctx, tx, request.ChainID, request.FromBlock, request.ToBlock, StageToken); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogBlockStats, request.ChainID, request.FromBlock, request.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("query canonical block statistics: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	items := make([]BlockStat, 0, expectedCount)
	expectedHeight := new(big.Int)
	expectedHeight.SetString(request.FromBlock, 10)
	for rows.Next() {
		stat, scanErr := scanBlockStat(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan block statistic: %w", scanErr)
		}
		if stat.BlockNumber != expectedHeight.String() {
			return nil, fmt.Errorf("%w: statistics have a canonical-height gap", ErrCorruptData)
		}
		expectedHeight.Add(expectedHeight, big.NewInt(1))
		items = append(items, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate block statistics: %w", err)
	}
	if len(items) != expectedCount {
		return nil, fmt.Errorf("%w: completed stats stage has missing rows", ErrCorruptData)
	}
	if err := commitRead(tx); err != nil {
		return nil, err
	}
	return items, nil
}

func requireStageRange(ctx context.Context, tx *sql.Tx, chainID, fromBlock, toBlock string, stage Stage) error {
	var blockNumber string
	var blockHash []byte
	var state sql.NullString
	err := tx.QueryRowContext(ctx, dbgen.CatalogFirstIncompleteStageInRange, chainID, fromBlock, toBlock, string(stage), stage.Version()).Scan(&blockNumber, &blockHash, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check %s stage range: %w", stage, err)
	}
	if !canonicalUint256(blockNumber) {
		return ErrCorruptData
	}
	if len(blockHash) == 0 {
		return StageUnavailableError{Stage: StageCore, State: StageMissing, BlockNumber: blockNumber}
	}
	encodedHash, err := lowerHex(blockHash)
	if err != nil {
		return err
	}
	if !state.Valid {
		return StageUnavailableError{Stage: stage, State: StageMissing, BlockNumber: blockNumber, BlockHash: encodedHash}
	}
	switch StageState(state.String) {
	case StageUnavailable, StageFailed:
		return StageUnavailableError{Stage: stage, State: StageState(state.String), BlockNumber: blockNumber, BlockHash: encodedHash}
	default:
		return fmt.Errorf("%w: invalid incomplete stage state", ErrCorruptData)
	}
}

func scanBlockStat(row rowScanner) (BlockStat, error) {
	var (
		stat                                             BlockStat
		blockHash                                        []byte
		baseFee, blobGasUsed, excessBlobGas, blobBaseFee sql.NullString
		burnedWei, blobBurnedWei, blockInterval, tps     sql.NullString
	)
	if err := row.Scan(
		&stat.ChainID, &stat.BlockNumber, &blockHash, &stat.TransactionCount,
		&stat.GasUsed, &stat.GasLimit, &baseFee, &blobGasUsed, &excessBlobGas,
		&blobBaseFee, &burnedWei, &blobBurnedWei, &stat.BlockTimestamp,
		&blockInterval, &tps, &stat.TokenEventCount, &stat.TokenTransferCount,
		&stat.NFTTransferCount, &stat.ComputedAt,
	); err != nil {
		return BlockStat{}, err
	}
	if err := validateChainID(stat.ChainID); err != nil || !canonicalUint256(stat.BlockNumber) ||
		!canonicalInt64(stat.TransactionCount) || !canonicalUint256(stat.GasUsed) || !canonicalUint256(stat.GasLimit) ||
		!canonicalUint256(stat.BlockTimestamp) || !canonicalUint256(stat.TokenEventCount) ||
		!canonicalUint256(stat.TokenTransferCount) || !canonicalUint256(stat.NFTTransferCount) {
		return BlockStat{}, ErrCorruptData
	}
	var err error
	stat.BlockHash, err = lowerHex(blockHash)
	if err != nil {
		return BlockStat{}, err
	}
	for _, optional := range []struct {
		source      sql.NullString
		destination **string
	}{
		{baseFee, &stat.BaseFeePerGas},
		{blobGasUsed, &stat.BlobGasUsed},
		{excessBlobGas, &stat.ExcessBlobGas},
		{blobBaseFee, &stat.BlobBaseFeePerGas},
		{burnedWei, &stat.BurnedWei},
		{blobBurnedWei, &stat.BlobBurnedWei},
		{blockInterval, &stat.BlockIntervalSeconds},
	} {
		source, destination := optional.source, optional.destination
		if source.Valid {
			if !canonicalUint256(source.String) {
				return BlockStat{}, ErrCorruptData
			}
			value := source.String
			*destination = &value
		}
	}
	if tps.Valid {
		if !canonicalFixedDecimal(tps.String, 18) {
			return BlockStat{}, ErrCorruptData
		}
		stat.TransactionsPerSecond = &tps.String
	}
	return stat, nil
}

func (catalog *Postgres) AggregateStats(ctx context.Context, request AggregateStatsRequest) (AggregateStats, error) {
	if catalog == nil || catalog.db == nil {
		return AggregateStats{}, errors.New("catalog database is nil")
	}
	if err := validateChainID(request.ChainID); err != nil {
		return AggregateStats{}, err
	}
	if _, err := decimalRangeSize(request.FromBlock, request.ToBlock, catalog.options.MaxChartPoints); err != nil {
		return AggregateStats{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return AggregateStats{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := readCanonicalSnapshot(ctx, tx, request.ChainID)
	if err != nil {
		return AggregateStats{}, err
	}
	if compareUnsignedDecimal(request.ToBlock, snapshot.BlockNumber) > 0 {
		return AggregateStats{}, fmt.Errorf("%w: statistics range exceeds the canonical snapshot", ErrInvalidInput)
	}
	for _, stage := range []Stage{StageStats, StageToken} {
		if err := requireStageRange(ctx, tx, request.ChainID, request.FromBlock, request.ToBlock, stage); err != nil {
			return AggregateStats{}, err
		}
	}
	result := AggregateStats{
		ChainID: request.ChainID, FromBlock: request.FromBlock, ToBlock: request.ToBlock,
		Snapshot: snapshot, CoreComplete: true, StatsComplete: true, TokenComplete: true,
	}
	var weightedTPS sql.NullString
	if err := tx.QueryRowContext(ctx, dbgen.CatalogAggregateStats, request.ChainID, request.FromBlock, request.ToBlock).Scan(
		&result.BlockCount, &result.TransactionCount, &result.GasUsed,
		&result.BurnedWei, &result.BlobBurnedWei, &result.TokenEventCount,
		&result.TokenTransferCount, &result.NFTTransferCount, &weightedTPS,
	); err != nil {
		return AggregateStats{}, fmt.Errorf("query aggregate statistics: %w", err)
	}
	for _, value := range []string{
		result.BlockCount, result.TransactionCount, result.GasUsed, result.BurnedWei,
		result.BlobBurnedWei, result.TokenEventCount, result.TokenTransferCount,
		result.NFTTransferCount,
	} {
		if !canonicalUint256(value) {
			return AggregateStats{}, ErrCorruptData
		}
	}
	if weightedTPS.Valid {
		if !canonicalFixedDecimal(weightedTPS.String, 18) {
			return AggregateStats{}, ErrCorruptData
		}
		result.AverageTPS = &weightedTPS.String
	}
	if err := commitRead(tx); err != nil {
		return AggregateStats{}, err
	}
	return result, nil
}

func canonicalFixedDecimal(value string, maximumScale int) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || !canonicalUint256(parts[0]) {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	if parts[1] == "" || len(parts[1]) > maximumScale || parts[1][len(parts[1])-1] == '0' {
		return false
	}
	for _, character := range parts[1] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (catalog *Postgres) TransactionTrace(ctx context.Context, chainID, transactionHashText string) (TransactionTrace, error) {
	if err := validateChainID(chainID); err != nil {
		return TransactionTrace{}, err
	}
	transactionHash, err := decodeFixedHex(transactionHashText, 32)
	if err != nil {
		return TransactionTrace{}, err
	}
	if catalog.traceCache != nil {
		identity, err := catalog.readTraceIdentity(ctx, chainID, transactionHash)
		if err != nil {
			return TransactionTrace{}, err
		}
		if cached, found, cacheErr := catalog.traceCache.Get(ctx, identity.cacheKey()); cacheErr != nil {
			catalog.logTraceCacheBypass(ctx, "optional S3 trace cache read failed; using PostgreSQL", cacheErr)
		} else if found {
			var envelope cachedTransactionTrace
			if decodeErr := json.Unmarshal(cached, &envelope); decodeErr == nil &&
				envelope.Schema == 3 && envelope.JobID == identity.JobID && envelope.JobGeneration == identity.JobGeneration &&
				catalog.validateCachedTrace(identity, envelope.Trace) == nil {
				decorated, decorateErr := catalog.decorateCachedTrace(ctx, identity, envelope.Trace)
				if decorateErr == nil {
					return decorated, nil
				}
			} else {
				catalog.logTraceCacheBypass(ctx, "optional S3 trace cache object invalid; using PostgreSQL", errors.New("invalid trace cache object"))
			}
		}
	}
	result, identity, err := catalog.readTransactionTrace(ctx, chainID, transactionHash)
	if err != nil {
		return TransactionTrace{}, err
	}
	if catalog.traceCache != nil {
		cacheTrace := result
		cacheTrace.Frames = append([]TraceFrame(nil), result.Frames...)
		for index := range cacheTrace.Frames {
			cacheTrace.Frames[index].Decoding = nil
			cacheTrace.Frames[index].Execution = nil
		}
		encoded, encodeErr := json.Marshal(cachedTransactionTrace{
			Schema: 3, JobID: identity.JobID, JobGeneration: identity.JobGeneration, Trace: cacheTrace,
		})
		if encodeErr != nil {
			catalog.logTraceCacheBypass(ctx, "optional S3 trace cache encode failed", encodeErr)
		} else if cacheErr := catalog.traceCache.Put(ctx, identity.cacheKey(), encoded); cacheErr != nil {
			catalog.logTraceCacheBypass(ctx, "optional S3 trace cache write failed; PostgreSQL result served", cacheErr)
		}
	}
	return result, nil
}

type traceIdentity struct {
	ChainID          string
	BlockNumber      string
	BlockHash        string
	TransactionHash  string
	TransactionIndex string
	JobID            int64
	JobGeneration    int64
}

func (identity traceIdentity) cacheKey() string {
	return fmt.Sprintf("trace/v3/%s/%s/%d-%d/%s.json",
		identity.ChainID, strings.TrimPrefix(identity.BlockHash, "0x"), identity.JobID,
		identity.JobGeneration, strings.TrimPrefix(identity.TransactionHash, "0x"))
}

type cachedTransactionTrace struct {
	Schema        int              `json:"schema"`
	JobID         int64            `json:"job_id"`
	JobGeneration int64            `json:"job_generation"`
	Trace         TransactionTrace `json:"trace"`
}

func (catalog *Postgres) readTraceIdentity(ctx context.Context, chainID string, transactionHash []byte) (traceIdentity, error) {
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return traceIdentity{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	identity, _, err := catalog.resolveTraceIdentity(ctx, tx, chainID, transactionHash)
	if err != nil {
		return traceIdentity{}, err
	}
	if err := commitRead(tx); err != nil {
		return traceIdentity{}, err
	}
	return identity, nil
}

func (catalog *Postgres) resolveTraceIdentity(ctx context.Context, tx *sql.Tx, chainID string, transactionHash []byte) (traceIdentity, []byte, error) {
	var blockNumber, transactionIndex string
	var blockHash []byte
	err := tx.QueryRowContext(ctx, dbgen.CatalogCanonicalTransactionInclusion, chainID, transactionHash).Scan(&blockNumber, &blockHash, &transactionIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return traceIdentity{}, nil, ErrNotFound
	}
	if err != nil {
		return traceIdentity{}, nil, fmt.Errorf("resolve canonical transaction inclusion: %w", err)
	}
	if !canonicalUint256(blockNumber) || !canonicalInt64(transactionIndex) {
		return traceIdentity{}, nil, ErrCorruptData
	}
	encodedBlockHash, err := lowerHex(blockHash)
	if err != nil {
		return traceIdentity{}, nil, err
	}
	var state string
	var jobID, jobGeneration int64
	err = tx.QueryRowContext(ctx, dbgen.CatalogTraceStagePublication, chainID, blockNumber, blockHash, string(StageTrace), StageTrace.Version()).Scan(&state, &jobID, &jobGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return traceIdentity{}, nil, StageUnavailableError{
			Stage: StageTrace, State: StageMissing, BlockNumber: blockNumber, BlockHash: encodedBlockHash,
		}
	}
	if err != nil {
		return traceIdentity{}, nil, fmt.Errorf("read %s catalog stage publication: %w", StageTrace, err)
	}
	if StageState(state) != StageComplete {
		if StageState(state) == StageUnavailable || StageState(state) == StageFailed {
			return traceIdentity{}, nil, StageUnavailableError{
				Stage: StageTrace, State: StageState(state), BlockNumber: blockNumber, BlockHash: encodedBlockHash,
			}
		}
		return traceIdentity{}, nil, fmt.Errorf("%w: invalid %s stage state", ErrCorruptData, StageTrace)
	}
	if jobID <= 0 || jobGeneration <= 0 {
		return traceIdentity{}, nil, ErrCorruptData
	}
	return traceIdentity{
		ChainID: chainID, BlockNumber: blockNumber, BlockHash: encodedBlockHash,
		TransactionHash: "0x" + hex.EncodeToString(transactionHash), TransactionIndex: transactionIndex,
		JobID: jobID, JobGeneration: jobGeneration,
	}, append([]byte(nil), blockHash...), nil
}

func (catalog *Postgres) readTransactionTrace(ctx context.Context, chainID string, transactionHash []byte) (TransactionTrace, traceIdentity, error) {
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TransactionTrace{}, traceIdentity{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	identity, blockHash, err := catalog.resolveTraceIdentity(ctx, tx, chainID, transactionHash)
	if err != nil {
		return TransactionTrace{}, traceIdentity{}, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogTransactionTrace, chainID, identity.BlockNumber, blockHash, transactionHash, catalog.options.MaxTraceFrames+1)
	if err != nil {
		return TransactionTrace{}, traceIdentity{}, fmt.Errorf("query normalized transaction trace: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	persisted := make([]scannedTraceFrame, 0)
	traceDataBytes := 0
	for rows.Next() {
		frame, scanErr := catalog.scanTraceFrame(rows)
		if scanErr != nil {
			return TransactionTrace{}, traceIdentity{}, fmt.Errorf("scan normalized trace frame: %w", scanErr)
		}
		persisted = append(persisted, frame)
		if frame.dataBytes < 0 || traceDataBytes > catalog.options.MaxTraceDataBytes-frame.dataBytes {
			return TransactionTrace{}, traceIdentity{}, ErrLimitExceeded
		}
		traceDataBytes += frame.dataBytes
		if len(persisted) > catalog.options.MaxTraceFrames {
			return TransactionTrace{}, traceIdentity{}, ErrLimitExceeded
		}
	}
	if err := rows.Err(); err != nil {
		return TransactionTrace{}, traceIdentity{}, fmt.Errorf("iterate normalized transaction trace: %w", err)
	}
	slices.SortFunc(persisted, func(left, right scannedTraceFrame) int {
		return compareTracePaths(left.frame.Path, right.frame.Path)
	})
	frames := make([]TraceFrame, len(persisted))
	knownPaths := make(map[string]struct{}, len(persisted))
	rootCount := 0
	for index, item := range persisted {
		if _, exists := knownPaths[item.pathText]; exists || uint32(len(item.frame.Path)) != item.frame.Depth {
			return TransactionTrace{}, traceIdentity{}, ErrCorruptData
		}
		if item.frame.Depth == 0 {
			if item.pathText != "" || item.parentText.Valid {
				return TransactionTrace{}, traceIdentity{}, ErrCorruptData
			}
			rootCount++
		} else {
			expectedParent := tracePathText(item.frame.Path[:len(item.frame.Path)-1])
			if !item.parentText.Valid || item.parentText.String != expectedParent {
				return TransactionTrace{}, traceIdentity{}, ErrCorruptData
			}
			if _, exists := knownPaths[expectedParent]; !exists {
				return TransactionTrace{}, traceIdentity{}, ErrCorruptData
			}
		}
		knownPaths[item.pathText] = struct{}{}
		frames[index] = item.frame
	}
	if rootCount != 1 {
		return TransactionTrace{}, traceIdentity{}, ErrCorruptData
	}
	result := TransactionTrace{
		ChainID: identity.ChainID, BlockNumber: identity.BlockNumber, BlockHash: identity.BlockHash,
		TransactionHash: identity.TransactionHash, TransactionIndex: identity.TransactionIndex,
		State: StageComplete, Frames: frames,
	}
	if err := catalog.decorateTraceFrames(ctx, tx, identity, &result); err != nil {
		return TransactionTrace{}, traceIdentity{}, err
	}
	if err := commitRead(tx); err != nil {
		return TransactionTrace{}, traceIdentity{}, err
	}
	return result, identity, nil
}

func (catalog *Postgres) validateCachedTrace(identity traceIdentity, trace TransactionTrace) error {
	if trace.ChainID != identity.ChainID || trace.BlockNumber != identity.BlockNumber || trace.BlockHash != identity.BlockHash ||
		trace.TransactionHash != identity.TransactionHash || trace.TransactionIndex != identity.TransactionIndex || trace.State != StageComplete ||
		len(trace.Frames) == 0 || len(trace.Frames) > catalog.options.MaxTraceFrames {
		return ErrCorruptData
	}
	knownPaths := make(map[string]struct{}, len(trace.Frames))
	dataBytes := 0
	rootCount := 0
	for index, frame := range trace.Frames {
		if frame.Decoding != nil || frame.Execution != nil || frame.CallType == "" || len(frame.CallType) > 128 ||
			int(frame.Depth) != len(frame.Path) || len(frame.Path) > 128 || frame.DirectReverted && !frame.Reverted {
			return ErrCorruptData
		}
		if index > 0 && compareTracePaths(trace.Frames[index-1].Path, frame.Path) >= 0 {
			return ErrCorruptData
		}
		pathText := tracePathText(frame.Path)
		if _, exists := knownPaths[pathText]; exists {
			return ErrCorruptData
		}
		if frame.Depth == 0 {
			if len(frame.Path) != 0 || len(frame.ParentPath) != 0 {
				return ErrCorruptData
			}
			rootCount++
		} else {
			expectedParent := frame.Path[:len(frame.Path)-1]
			if !slices.Equal(frame.ParentPath, expectedParent) {
				return ErrCorruptData
			}
			if _, exists := knownPaths[tracePathText(expectedParent)]; !exists {
				return ErrCorruptData
			}
		}
		knownPaths[pathText] = struct{}{}
		for _, address := range []*string{frame.From, frame.To, frame.CreatedAddress} {
			if address == nil {
				continue
			}
			decoded, err := decodeFixedHex(*address, 20)
			if err != nil {
				return ErrCorruptData
			}
			canonical, err := checksumAddressBytes(decoded)
			if err != nil || canonical != *address {
				return ErrCorruptData
			}
		}
		for _, quantity := range []*string{frame.Value, frame.Gas, frame.GasUsed} {
			if quantity != nil && !canonicalUint256(*quantity) {
				return ErrCorruptData
			}
		}
		for _, data := range []*string{frame.Input, frame.Output} {
			if data == nil {
				continue
			}
			if len(*data) < 2 || !strings.HasPrefix(*data, "0x") || len(*data)%2 != 0 {
				return ErrCorruptData
			}
			decoded, err := hex.DecodeString((*data)[2:])
			if err != nil || dataBytes > catalog.options.MaxTraceDataBytes-len(decoded) {
				return ErrLimitExceeded
			}
			dataBytes += len(decoded)
		}
		if frame.Error != nil && len(*frame.Error) > catalog.options.MaxTextBytes {
			return ErrLimitExceeded
		}
	}
	if rootCount != 1 {
		return ErrCorruptData
	}
	return nil
}

func (catalog *Postgres) logTraceCacheBypass(ctx context.Context, message string, err error) {
	if catalog.logger != nil && err != nil {
		code, operation := "s3_trace_cache_read_failed", "read"
		switch {
		case strings.Contains(message, "object invalid"):
			code, operation = "s3_trace_cache_object_invalid", "validate"
		case strings.Contains(message, "encode"):
			code, operation = "s3_trace_cache_encode_failed", "encode"
		case strings.Contains(message, "write"):
			code, operation = "s3_trace_cache_write_failed", "write"
		}
		catalog.logger.WarnContext(ctx, message,
			"event", "optional_adapter_degraded", "component", "transaction-trace-catalog",
			"adapter", "s3", "operation", operation, "fallback", "postgresql",
			"error_code", code, "error_type", fmt.Sprintf("%T", err))
	}
}

type scannedTraceFrame struct {
	frame      TraceFrame
	pathText   string
	parentText sql.NullString
	dataBytes  int
}

func (catalog *Postgres) scanTraceFrame(row rowScanner) (scannedTraceFrame, error) {
	var (
		result                              scannedTraceFrame
		depth                               int64
		from, to, created, input, output    []byte
		executionAddress, executionCodeHash []byte
		value, gas, gasUsed, traceError     sql.NullString
		executionResolution                 string
		directReverted                      bool
	)
	if err := row.Scan(
		&result.pathText, &result.parentText, &depth, &result.frame.CallType,
		&from, &to, &created, &value, &gas, &gasUsed, &input, &output,
		&traceError, &directReverted, &result.frame.Reverted,
		&executionAddress, &executionCodeHash, &executionResolution,
	); err != nil {
		return scannedTraceFrame{}, err
	}
	if depth < 0 || depth > 128 || len(result.pathText) > catalog.options.MaxTextBytes ||
		result.frame.CallType == "" || len(result.frame.CallType) > 128 {
		return scannedTraceFrame{}, ErrCorruptData
	}
	if result.parentText.Valid && len(result.parentText.String) > catalog.options.MaxTextBytes {
		return scannedTraceFrame{}, ErrLimitExceeded
	}
	path, err := parseTracePath(result.pathText)
	if err != nil {
		return scannedTraceFrame{}, err
	}
	parentPath := []uint32{}
	if result.parentText.Valid {
		parentPath, err = parseTracePath(result.parentText.String)
		if err != nil {
			return scannedTraceFrame{}, err
		}
	}
	result.frame.Path, result.frame.ParentPath, result.frame.Depth = path, parentPath, uint32(depth)
	result.frame.DirectReverted = directReverted
	if directReverted && !result.frame.Reverted {
		return scannedTraceFrame{}, ErrCorruptData
	}
	if result.frame.From, err = optionalChecksumAddress(from); err != nil {
		return scannedTraceFrame{}, err
	}
	if result.frame.To, err = optionalChecksumAddress(to); err != nil {
		return scannedTraceFrame{}, err
	}
	if result.frame.CreatedAddress, err = optionalChecksumAddress(created); err != nil {
		return scannedTraceFrame{}, err
	}
	contextAddress := result.frame.To
	if contextAddress == nil {
		contextAddress = result.frame.CreatedAddress
	}
	if contextAddress == nil {
		contextAddress = result.frame.From
	}
	if contextAddress != nil {
		execution := &TraceExecution{ContextAddress: *contextAddress, Resolution: executionResolution}
		if len(executionAddress) != 0 {
			value, addressErr := optionalChecksumAddress(executionAddress)
			if addressErr != nil || value == nil {
				return scannedTraceFrame{}, ErrCorruptData
			}
			execution.Address = *value
		}
		if len(executionCodeHash) != 0 {
			value, hashErr := lowerHex(executionCodeHash)
			if hashErr != nil {
				return scannedTraceFrame{}, ErrCorruptData
			}
			execution.CodeHash = value
		}
		if !validTraceExecution(execution) {
			return scannedTraceFrame{}, ErrCorruptData
		}
		result.frame.Execution = execution
	}
	for _, optional := range []struct {
		source      sql.NullString
		destination **string
	}{
		{value, &result.frame.Value},
		{gas, &result.frame.Gas},
		{gasUsed, &result.frame.GasUsed},
	} {
		source, destination := optional.source, optional.destination
		if source.Valid {
			if !canonicalUint256(source.String) {
				return scannedTraceFrame{}, ErrCorruptData
			}
			copy := source.String
			*destination = &copy
		}
	}
	if input != nil {
		encoded := "0x" + hex.EncodeToString(input)
		result.frame.Input = &encoded
	}
	if output != nil {
		encoded := "0x" + hex.EncodeToString(output)
		result.frame.Output = &encoded
	}
	result.dataBytes = len(input) + len(output)
	if result.dataBytes < 0 || result.dataBytes > catalog.options.MaxTraceDataBytes {
		return scannedTraceFrame{}, ErrLimitExceeded
	}
	if traceError.Valid {
		if len(traceError.String) > catalog.options.MaxTextBytes {
			return scannedTraceFrame{}, ErrLimitExceeded
		}
		result.frame.Error = &traceError.String
	}
	return result, nil
}

func tracePathText(path []uint32) string {
	parts := make([]string, len(path))
	for index, component := range path {
		parts[index] = fmt.Sprintf("%d", component)
	}
	return strings.Join(parts, ".")
}

func compareUnsignedDecimal(left, right string) int {
	leftInteger, rightInteger := new(big.Int), new(big.Int)
	leftInteger.SetString(left, 10)
	rightInteger.SetString(right, 10)
	return leftInteger.Cmp(rightInteger)
}
