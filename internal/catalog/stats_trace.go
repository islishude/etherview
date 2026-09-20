package catalog

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
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
	defer dbaccess.Rollback(ctx, tx)
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
	rows, err := func() ([]dbgen.CatalogBlockStatsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(request.FromBlock); err != nil {
			return nil, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(request.ToBlock); err != nil {
			return nil, err
		}
		return dbgen.New(tx).CatalogBlockStats(ctx, queryValue0, queryValue1, queryValue2)
	}()
	if err != nil {
		return nil, fmt.Errorf("query canonical block statistics: %w", err)
	}

	items := make([]BlockStat, 0, expectedCount)
	expectedHeight := new(big.Int)
	expectedHeight.SetString(request.FromBlock, 10)
	for _, storedRow := range rows {
		stat, scanErr := scanBlockStat(dbgen.CatalogBlockStatsRow(storedRow))
		if scanErr != nil {
			return nil, fmt.Errorf("scan block statistic: %w", scanErr)
		}
		if stat.BlockNumber != expectedHeight.String() {
			return nil, fmt.Errorf("%w: statistics have a canonical-height gap", ErrCorruptData)
		}
		expectedHeight.Add(expectedHeight, big.NewInt(1))
		items = append(items, stat)
	}

	if len(items) != expectedCount {
		return nil, fmt.Errorf("%w: completed stats stage has missing rows", ErrCorruptData)
	}
	if err := commitRead(ctx, tx); err != nil {
		return nil, err
	}
	return items, nil
}

func requireStageRange(ctx context.Context, tx pgx.Tx, chainID, fromBlock, toBlock string, stage Stage) error {
	var blockNumber string
	var blockHash []byte
	var state pgtype.Text
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(fromBlock); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(toBlock); err != nil {
			return err
		}
		if stage.Version() < -2147483648 || stage.Version() > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).CatalogFirstIncompleteStageInRange(ctx, dbgen.CatalogFirstIncompleteStageInRangeParams{ChainID: queryValue0, FromBlock: queryValue1, ToBlock: queryValue2, Stage: string(stage), StageVersion: int32(stage.Version())})
		if err != nil {
			return err
		}
		blockNumber = queryRow.HeightsNumber
		blockHash = queryRow.BlockHash
		state = queryRow.State
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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

func scanBlockStat(row dbgen.CatalogBlockStatsRow) (BlockStat, error) {
	var (
		stat                                             BlockStat
		blockHash                                        []byte
		baseFee, blobGasUsed, excessBlobGas, blobBaseFee pgtype.Text
		burnedWei, blobBurnedWei, blockInterval, tps     pgtype.Text
	)
	if err := func() error {
		stat.ChainID = row.StatsChainID
		stat.BlockNumber = row.StatsBlockNumber
		blockHash = row.BlockHash
		stat.TransactionCount = row.StatsTransactionCount
		stat.GasUsed = row.StatsGasUsed
		stat.GasLimit = row.StatsGasLimit
		if value, err := dbaccess.NumericText(row.StatsBaseFeePerGas); err != nil {
			return err
		} else {
			baseFee = value
		}
		if value, err := dbaccess.NumericText(row.StatsBlobGasUsed); err != nil {
			return err
		} else {
			blobGasUsed = value
		}
		if value, err := dbaccess.NumericText(row.StatsExcessBlobGas); err != nil {
			return err
		} else {
			excessBlobGas = value
		}
		if value, err := dbaccess.NumericText(row.StatsBlobBaseFeePerGas); err != nil {
			return err
		} else {
			blobBaseFee = value
		}
		if value, err := dbaccess.NumericText(row.StatsBurnedWei); err != nil {
			return err
		} else {
			burnedWei = value
		}
		if value, err := dbaccess.NumericText(row.StatsBlobBurnedWei); err != nil {
			return err
		} else {
			blobBurnedWei = value
		}
		stat.BlockTimestamp = row.StatsBlockTimestamp
		if value, err := dbaccess.NumericText(row.StatsBlockIntervalSeconds); err != nil {
			return err
		} else {
			blockInterval = value
		}
		if value, err := normalizedNumericText(row.TransactionsPerSecond); err != nil {
			return err
		} else {
			tps = value
		}
		stat.TokenEventCount = row.TokenTokenEventCount
		stat.TokenTransferCount = row.TokenTokenTransferCount
		stat.NFTTransferCount = row.TokenNftTransferCount
		if !row.ComputedAt.Valid {
			return errors.New("invalid stored query value")
		}
		if row.ComputedAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		stat.ComputedAt = row.ComputedAt.Time
		return nil
	}(); err != nil {
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
		source      pgtype.Text
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
	defer dbaccess.Rollback(ctx, tx)
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
	var weightedTPS pgtype.Text
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(request.FromBlock); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(request.ToBlock); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).CatalogAggregateStats(ctx, queryValue0, queryValue1, queryValue2)
		if err != nil {
			return err
		}
		result.BlockCount = queryRow.BlockCount
		result.TransactionCount = queryRow.TransactionCount
		result.GasUsed = queryRow.GasUsed
		result.BurnedWei = queryRow.BurnedWei
		result.BlobBurnedWei = queryRow.BlobBurnedWei
		result.TokenEventCount = queryRow.TokenEventCount
		result.TokenTransferCount = queryRow.Erc20TransferCount
		result.NFTTransferCount = queryRow.NftTransferCount
		weightedTPS = pgtype.Text{String: queryRow.WeightedTps, Valid: queryRow.WeightedTpsPresent}
		return nil
	}(); err != nil {
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
	if err := commitRead(ctx, tx); err != nil {
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
	defer dbaccess.Rollback(ctx, tx)
	identity, _, err := catalog.resolveTraceIdentity(ctx, tx, chainID, transactionHash)
	if err != nil {
		return traceIdentity{}, err
	}
	if err := commitRead(ctx, tx); err != nil {
		return traceIdentity{}, err
	}
	return identity, nil
}

func (catalog *Postgres) resolveTraceIdentity(ctx context.Context, tx pgx.Tx, chainID string, transactionHash []byte) (traceIdentity, []byte, error) {
	var blockNumber, transactionIndex string
	var blockHash []byte
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).CatalogCanonicalTransactionInclusion(ctx, queryValue0, transactionHash)
		if err != nil {
			return err
		}
		blockNumber = queryRow.InclusionBlockNumber
		blockHash = queryRow.BlockHash
		transactionIndex = queryRow.InclusionTxIndex
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return err
		}
		if StageTrace.Version() < -2147483648 || StageTrace.Version() > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).CatalogTraceStagePublication(ctx, dbgen.CatalogTraceStagePublicationParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: blockHash, Stage: string(StageTrace), StageVersion: int32(StageTrace.Version())})
		if err != nil {
			return err
		}
		if !queryRow.State.Valid {
			return errors.New("invalid stored query value")
		}
		state = queryRow.State.String
		if queryRow.DurableJobID == nil {
			return errors.New("invalid stored query value")
		}
		jobID = *queryRow.DurableJobID
		if queryRow.JobGeneration == nil {
			return errors.New("invalid stored query value")
		}
		jobGeneration = *queryRow.JobGeneration
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	defer dbaccess.Rollback(ctx, tx)
	identity, blockHash, err := catalog.resolveTraceIdentity(ctx, tx, chainID, transactionHash)
	if err != nil {
		return TransactionTrace{}, traceIdentity{}, err
	}
	rows, err := func() ([]dbgen.CatalogTransactionTraceRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(identity.BlockNumber); err != nil {
			return nil, err
		}
		if catalog.options.MaxTraceFrames+1 < -2147483648 || catalog.options.MaxTraceFrames+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).CatalogTransactionTrace(ctx, dbgen.CatalogTransactionTraceParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: blockHash, TransactionHash: transactionHash, Limit: int32(catalog.options.MaxTraceFrames + 1)})
	}()
	if err != nil {
		return TransactionTrace{}, traceIdentity{}, fmt.Errorf("query normalized transaction trace: %w", err)
	}

	persisted := make([]scannedTraceFrame, 0)
	traceDataBytes := 0
	for _, storedRow := range rows {
		frame, scanErr := catalog.scanTraceFrame(dbgen.CatalogTransactionTraceRow(storedRow))
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
	if err := commitRead(ctx, tx); err != nil {
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
	parentText pgtype.Text
	dataBytes  int
}

func (catalog *Postgres) scanTraceFrame(row dbgen.CatalogTransactionTraceRow) (scannedTraceFrame, error) {
	var (
		result                              scannedTraceFrame
		depth                               int64
		from, to, created, input, output    []byte
		executionAddress, executionCodeHash []byte
		value, gas, gasUsed, traceError     pgtype.Text
		executionResolution                 string
		directReverted                      bool
	)
	if err := func() error {
		result.pathText = row.TracePath
		var queryValue1 pgtype.Text
		if row.ParentPath != nil {
			queryValue1 = pgtype.Text{String: *row.ParentPath, Valid: true}
		}
		result.parentText = queryValue1
		depth = int64(row.Depth)
		result.frame.CallType = row.CallType
		from = row.FromAddress
		to = row.ToAddress
		created = row.CreatedAddress
		if numericValue, err := dbaccess.NumericText(row.Value); err != nil {
			return err
		} else {
			value = numericValue
		}
		if value, err := dbaccess.NumericText(row.Gas); err != nil {
			return err
		} else {
			gas = value
		}
		if value, err := dbaccess.NumericText(row.GasUsed); err != nil {
			return err
		} else {
			gasUsed = value
		}
		input = row.Input
		output = row.Output
		var queryValue13 pgtype.Text
		if row.Error != nil {
			queryValue13 = pgtype.Text{String: *row.Error, Valid: true}
		}
		traceError = queryValue13
		directReverted = row.DirectReverted
		result.frame.Reverted = row.Reverted
		executionAddress = row.ExecutionAddress
		executionCodeHash = row.ExecutionCodeHash
		executionResolution = row.ExecutionResolution
		return nil
	}(); err != nil {
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
		source      pgtype.Text
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

// PostgreSQL stores exact scale; public statistics omit insignificant trailing
// fractional zeroes without ever converting to binary floating point.
func normalizedNumericText(value pgtype.Numeric) (pgtype.Text, error) {
	text, err := dbaccess.NumericText(value)
	if err != nil {
		return pgtype.Text{}, err
	}
	if text.Valid && strings.Contains(text.String, ".") {
		text.String = strings.TrimRight(strings.TrimRight(text.String, "0"), ".")
	}
	return text, nil
}
