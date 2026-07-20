package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
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
	defer tx.Rollback()
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
	rows, err := tx.QueryContext(ctx, blockStatsSQL, request.ChainID, request.FromBlock, request.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("query canonical block statistics: %w", err)
	}
	defer rows.Close()
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
	err := tx.QueryRowContext(ctx, firstIncompleteStageInRangeSQL,
		chainID, fromBlock, toBlock, string(stage), stage.Version(),
	).Scan(&blockNumber, &blockHash, &state)
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
	encodedHash, err := lowerHex(blockHash, 32)
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
	stat.BlockHash, err = lowerHex(blockHash, 32)
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
	defer tx.Rollback()
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
	if err := tx.QueryRowContext(ctx, aggregateStatsSQL,
		request.ChainID, request.FromBlock, request.ToBlock,
	).Scan(
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
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TransactionTrace{}, err
	}
	defer tx.Rollback()
	var blockNumber, transactionIndex string
	var blockHash []byte
	err = tx.QueryRowContext(ctx, canonicalTransactionInclusionSQL,
		chainID, transactionHash,
	).Scan(&blockNumber, &blockHash, &transactionIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return TransactionTrace{}, ErrNotFound
	}
	if err != nil {
		return TransactionTrace{}, fmt.Errorf("resolve canonical transaction inclusion: %w", err)
	}
	if !canonicalUint256(blockNumber) || !canonicalInt64(transactionIndex) {
		return TransactionTrace{}, ErrCorruptData
	}
	encodedBlockHash, err := lowerHex(blockHash, 32)
	if err != nil {
		return TransactionTrace{}, err
	}
	snapshot := Snapshot{ChainID: chainID, BlockNumber: blockNumber, BlockHash: encodedBlockHash}
	if err := requireStage(ctx, tx, snapshot, StageTrace); err != nil {
		return TransactionTrace{}, err
	}
	rows, err := tx.QueryContext(ctx, transactionTraceSQL,
		chainID, blockNumber, blockHash, transactionHash, catalog.options.MaxTraceFrames+1,
	)
	if err != nil {
		return TransactionTrace{}, fmt.Errorf("query normalized transaction trace: %w", err)
	}
	defer rows.Close()
	persisted := make([]scannedTraceFrame, 0)
	traceDataBytes := 0
	for rows.Next() {
		frame, scanErr := catalog.scanTraceFrame(rows)
		if scanErr != nil {
			return TransactionTrace{}, fmt.Errorf("scan normalized trace frame: %w", scanErr)
		}
		persisted = append(persisted, frame)
		if frame.dataBytes < 0 || traceDataBytes > catalog.options.MaxTraceDataBytes-frame.dataBytes {
			return TransactionTrace{}, ErrLimitExceeded
		}
		traceDataBytes += frame.dataBytes
		if len(persisted) > catalog.options.MaxTraceFrames {
			return TransactionTrace{}, ErrLimitExceeded
		}
	}
	if err := rows.Err(); err != nil {
		return TransactionTrace{}, fmt.Errorf("iterate normalized transaction trace: %w", err)
	}
	slices.SortFunc(persisted, func(left, right scannedTraceFrame) int {
		return compareTracePaths(left.frame.Path, right.frame.Path)
	})
	frames := make([]TraceFrame, len(persisted))
	knownPaths := make(map[string]struct{}, len(persisted))
	rootCount := 0
	for index, item := range persisted {
		if _, exists := knownPaths[item.pathText]; exists || uint32(len(item.frame.Path)) != item.frame.Depth {
			return TransactionTrace{}, ErrCorruptData
		}
		if item.frame.Depth == 0 {
			if item.pathText != "" || item.parentText.Valid {
				return TransactionTrace{}, ErrCorruptData
			}
			rootCount++
		} else {
			expectedParent := tracePathText(item.frame.Path[:len(item.frame.Path)-1])
			if !item.parentText.Valid || item.parentText.String != expectedParent {
				return TransactionTrace{}, ErrCorruptData
			}
			if _, exists := knownPaths[expectedParent]; !exists {
				return TransactionTrace{}, ErrCorruptData
			}
		}
		knownPaths[item.pathText] = struct{}{}
		frames[index] = item.frame
	}
	if rootCount != 1 {
		return TransactionTrace{}, ErrCorruptData
	}
	result := TransactionTrace{
		ChainID: chainID, BlockNumber: blockNumber, BlockHash: encodedBlockHash,
		TransactionHash: "0x" + hex.EncodeToString(transactionHash), TransactionIndex: transactionIndex,
		State: StageComplete, Frames: frames,
	}
	if err := commitRead(tx); err != nil {
		return TransactionTrace{}, err
	}
	return result, nil
}

type scannedTraceFrame struct {
	frame      TraceFrame
	pathText   string
	parentText sql.NullString
	dataBytes  int
}

func (catalog *Postgres) scanTraceFrame(row rowScanner) (scannedTraceFrame, error) {
	var (
		result                           scannedTraceFrame
		depth                            int64
		from, to, created, input, output []byte
		value, gas, gasUsed, traceError  sql.NullString
	)
	if err := row.Scan(
		&result.pathText, &result.parentText, &depth, &result.frame.CallType,
		&from, &to, &created, &value, &gas, &gasUsed, &input, &output,
		&traceError, &result.frame.Reverted,
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
	if result.frame.From, err = optionalChecksumAddress(from); err != nil {
		return scannedTraceFrame{}, err
	}
	if result.frame.To, err = optionalChecksumAddress(to); err != nil {
		return scannedTraceFrame{}, err
	}
	if result.frame.CreatedAddress, err = optionalChecksumAddress(created); err != nil {
		return scannedTraceFrame{}, err
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

const firstIncompleteStageInRangeSQL = `
WITH heights AS (
    SELECT generate_series($2::numeric, $3::numeric, 1::numeric) AS number
)
SELECT heights.number::text, cb.block_hash, latest.state
FROM heights
LEFT JOIN canonical_blocks AS cb
  ON cb.chain_id = $1::numeric AND cb.number = heights.number
LEFT JOIN LATERAL (
    SELECT result.state
    FROM published_block_stage_results AS result
    WHERE result.chain_id = cb.chain_id
      AND result.block_number = cb.number
      AND result.block_hash = cb.block_hash
      AND result.stage = $4
      AND result.stage_version = $5
) AS latest ON true
WHERE cb.block_hash IS NULL OR latest.state IS DISTINCT FROM 'complete'
ORDER BY heights.number
LIMIT 1`

const blockStatsSQL = `
SELECT stats.chain_id::text, stats.block_number::text, stats.block_hash,
       stats.transaction_count::text, stats.gas_used::text, stats.gas_limit::text,
       stats.base_fee_per_gas::text, stats.blob_gas_used::text,
       stats.excess_blob_gas::text, stats.blob_base_fee_per_gas::text,
       stats.burned_wei::text, stats.blob_burned_wei::text,
       stats.block_timestamp::text, stats.block_interval_seconds::text,
       trim(trailing '.' FROM trim(trailing '0' FROM stats.transactions_per_second::text)),
       token.token_event_count::text, token.token_transfer_count::text,
       token.nft_transfer_count::text, stats.computed_at
FROM block_statistics AS stats
JOIN canonical_blocks AS cb
  ON cb.chain_id = stats.chain_id
 AND cb.number = stats.block_number
 AND cb.block_hash = stats.block_hash
LEFT JOIN LATERAL (
    SELECT count(*) AS token_event_count,
           count(*) FILTER (
               WHERE event.standard = 'erc20'
                 AND event.event_kind IN ('transfer', 'mint', 'burn')
           ) AS token_transfer_count,
           count(*) FILTER (
               WHERE event.standard IN ('erc721', 'erc1155')
                 AND event.event_kind IN ('transfer', 'mint', 'burn')
           ) AS nft_transfer_count
    FROM token_events AS event
    WHERE event.chain_id = stats.chain_id
      AND event.block_number = stats.block_number
      AND event.block_hash = stats.block_hash
      AND event.canonical = true
) AS token ON true
WHERE stats.chain_id = $1::numeric
  AND stats.block_number BETWEEN $2::numeric AND $3::numeric
  AND stats.canonical = true
ORDER BY stats.block_number`

const aggregateStatsSQL = `
WITH selected_stats AS (
    SELECT stats.*
    FROM block_statistics AS stats
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = stats.chain_id
     AND canonical.number = stats.block_number
     AND canonical.block_hash = stats.block_hash
    WHERE stats.chain_id = $1::numeric
      AND stats.block_number BETWEEN $2::numeric AND $3::numeric
      AND stats.canonical = true
), selected_tokens AS (
    SELECT event.standard, event.event_kind
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = $1::numeric
      AND event.block_number BETWEEN $2::numeric AND $3::numeric
      AND event.canonical = true
)
SELECT count(*)::text,
       COALESCE(sum(transaction_count), 0)::text,
       COALESCE(sum(gas_used), 0)::text,
       COALESCE(sum(burned_wei), 0)::text,
       COALESCE(sum(blob_burned_wei), 0)::text,
       (SELECT count(*)::text FROM selected_tokens),
       (SELECT count(*)::text FROM selected_tokens
         WHERE standard = 'erc20' AND event_kind IN ('transfer', 'mint', 'burn')),
       (SELECT count(*)::text FROM selected_tokens
         WHERE standard IN ('erc721', 'erc1155') AND event_kind IN ('transfer', 'mint', 'burn')),
       CASE WHEN COALESCE(sum(block_interval_seconds) FILTER (
                     WHERE block_interval_seconds IS NOT NULL
                 ), 0) = 0 THEN NULL
            ELSE trim(trailing '.' FROM trim(trailing '0' FROM
                 round(
                     sum(transaction_count) FILTER (WHERE block_interval_seconds IS NOT NULL)
                     / sum(block_interval_seconds) FILTER (WHERE block_interval_seconds IS NOT NULL),
                     18
                 )::text))
       END
FROM selected_stats`

const canonicalTransactionInclusionSQL = `
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS cb
  ON cb.chain_id = inclusion.chain_id
 AND cb.number = inclusion.block_number
 AND cb.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
LIMIT 1`

const transactionTraceSQL = `
SELECT trace_path, parent_path, depth, call_type,
       from_address, to_address, created_address,
       value::text, gas::text, gas_used::text,
       input, output, error, reverted
FROM normalized_traces
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND transaction_hash = $4
  AND canonical = true
ORDER BY depth, trace_path
LIMIT $5`
