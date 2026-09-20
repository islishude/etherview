// Package query adapts the PostgreSQL core schema to stable public API models.
package query

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/erc4337"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

var ErrInvalidCursor = publicquery.ErrInvalidCursor

type LatestBlockFunc func(context.Context) (uint64, error)

type RuntimeStatus struct {
	Latest              uint64
	Indexed             uint64
	HighestCovered      uint64
	LatestKnown         bool
	IndexedKnown        bool
	HighestCoveredKnown bool
	BackfillComplete    bool
	Ready               bool
}

type RuntimeStatusFunc func(context.Context) (RuntimeStatus, bool, error)

type NameResolver interface {
	ResolveForward(context.Context, string) (ensresolver.ForwardResolution, error)
}

type Options struct {
	ChainID               uint64
	StartBlock            uint64
	LatestBlock           LatestBlockFunc
	RuntimeStatus         RuntimeStatusFunc
	OptionalStages        gen.Completeness
	NameResolver          NameResolver
	UserOperationRegistry *erc4337.Registry
}

type PostgresReader struct {
	db                  dbaccess.Database
	chainID             string
	startBlock          uint64
	latestBlock         LatestBlockFunc
	runtimeStatus       RuntimeStatusFunc
	completeness        gen.Completeness
	nameResolver        NameResolver
	userOperationDigest []byte
	userOperationStart  uint64
	userOperations      bool
}

var _ publicquery.Reader = (*PostgresReader)(nil)

func NewPostgresReader(db dbaccess.Database, options Options) (*PostgresReader, error) {
	if db == nil {
		return nil, errors.New("query database is nil")
	}
	if options.ChainID == 0 {
		return nil, errors.New("query chain ID must be greater than zero")
	}
	completeness := options.OptionalStages
	var err error
	if completeness.Trace, err = normalizeOptionalStage(completeness.Trace); err != nil {
		return nil, fmt.Errorf("trace completeness: %w", err)
	}
	if completeness.Metadata, err = normalizeOptionalStage(completeness.Metadata); err != nil {
		return nil, fmt.Errorf("metadata completeness: %w", err)
	}
	if completeness.State, err = normalizeOptionalStage(completeness.State); err != nil {
		return nil, fmt.Errorf("state completeness: %w", err)
	}
	if completeness.UserOperations, err = normalizeOptionalStage(completeness.UserOperations); err != nil {
		return nil, fmt.Errorf("UserOperation completeness: %w", err)
	}
	completeness.Core = gen.StageStateComplete
	reader := &PostgresReader{
		db:            db,
		chainID:       strconv.FormatUint(options.ChainID, 10),
		startBlock:    options.StartBlock,
		latestBlock:   options.LatestBlock,
		runtimeStatus: options.RuntimeStatus,
		completeness:  completeness,
		nameResolver:  options.NameResolver,
	}
	if options.UserOperationRegistry != nil && len(options.UserOperationRegistry.Entries()) > 0 {
		digest := options.UserOperationRegistry.Digest()
		reader.userOperationDigest = append([]byte(nil), digest[:]...)
		reader.userOperationStart = options.UserOperationRegistry.StartBlock(options.StartBlock)
		reader.userOperations = true
	} else {
		reader.completeness.UserOperations = gen.StageStateUnavailable
	}
	return reader, nil
}

func (r *PostgresReader) Status(ctx context.Context) (publicquery.StatusSnapshot, error) {
	return r.status(ctx, r.db, r.runtimeStatus, r.latestBlock)
}

func (r *PostgresReader) status(
	ctx context.Context,
	queryer searchQueryer,
	runtimeStatus RuntimeStatusFunc,
	latestBlock LatestBlockFunc,
) (publicquery.StatusSnapshot, error) {
	snapshot := publicquery.StatusSnapshot{
		CoverageStart: r.startBlock,
		CoverageEnd:   r.startBlock,
		Completeness:  r.completeness,
	}
	var configuredStart, contiguousEnd, checkpointHeight, highestEnd pgtype.Text
	var contiguousHash, checkpointHash, highestHash []byte
	var safeHeight, finalizedHeight, traceState pgtype.Text
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(queryer).QueryStatusState(ctx, queryValue0)
		if err != nil {
			return err
		}
		resultValue0, err := dbaccess.NumericText(queryRow.ConfiguredStart)
		if err != nil {
			return err
		}
		configuredStart = resultValue0
		resultValue2, err := dbaccess.NumericText(queryRow.ContiguousRangeEnd)
		if err != nil {
			return err
		}
		contiguousEnd = resultValue2
		contiguousHash = queryRow.ContiguousBlockHash
		resultValue5, err := dbaccess.NumericText(queryRow.CheckpointNumber)
		if err != nil {
			return err
		}
		checkpointHeight = resultValue5
		checkpointHash = queryRow.CheckpointHash
		resultValue8, err := dbaccess.NumericText(queryRow.HighestRangeEnd)
		if err != nil {
			return err
		}
		highestEnd = resultValue8
		highestHash = queryRow.HighestBlockHash
		resultValue11, err := dbaccess.NumericText(queryRow.SafeNumber)
		if err != nil {
			return err
		}
		safeHeight = resultValue11
		resultValue13, err := dbaccess.NumericText(queryRow.FinalizedNumber)
		if err != nil {
			return err
		}
		finalizedHeight = resultValue13
		traceState = queryRow.State
		return nil
	}(); err != nil {
		return publicquery.StatusSnapshot{}, fmt.Errorf("query index status: %w", err)
	}
	configured := configuredStart.Valid
	if configured {
		persistedStart, err := parseDecimalUint64(configuredStart.String)
		if err != nil {
			return publicquery.StatusSnapshot{}, fmt.Errorf("decode configured index start: %w", err)
		}
		if persistedStart != r.startBlock {
			return publicquery.StatusSnapshot{}, fmt.Errorf("configured index start mismatch: persisted=%d requested=%d", persistedStart, r.startBlock)
		}
		snapshot.CoverageStart = persistedStart
	}
	if contiguousEnd.Valid != checkpointHeight.Valid {
		return publicquery.StatusSnapshot{}, errors.New("core coverage and checkpoint presence differ")
	}
	if contiguousEnd.Valid {
		if len(contiguousHash) != 32 || len(checkpointHash) != 32 || !equalBytes(contiguousHash, checkpointHash) {
			return publicquery.StatusSnapshot{}, errors.New("core coverage and checkpoint identities differ")
		}
		indexed, err := parseDecimalUint64(contiguousEnd.String)
		if err != nil {
			return publicquery.StatusSnapshot{}, fmt.Errorf("decode contiguous coverage end: %w", err)
		}
		checkpoint, err := parseDecimalUint64(checkpointHeight.String)
		if err != nil {
			return publicquery.StatusSnapshot{}, fmt.Errorf("decode core checkpoint: %w", err)
		}
		if indexed != checkpoint {
			return publicquery.StatusSnapshot{}, errors.New("core coverage and checkpoint heights differ")
		}
		snapshot.IndexedBlock = indexed
	}
	if highestEnd.Valid {
		if !configured || len(highestHash) != 32 {
			return publicquery.StatusSnapshot{}, errors.New("highest coverage identity is internally inconsistent")
		}
		highest, err := parseDecimalUint64(highestEnd.String)
		if err != nil {
			return publicquery.StatusSnapshot{}, fmt.Errorf("decode highest covered block: %w", err)
		}
		if contiguousEnd.Valid && snapshot.IndexedBlock > highest {
			return publicquery.StatusSnapshot{}, errors.New("contiguous coverage exceeds highest covered block")
		}
		snapshot.HighestCoveredBlock = highest
		snapshot.HighestCoveredKnown = true
		snapshot.CoverageEnd = highest
	} else if contiguousEnd.Valid {
		return publicquery.StatusSnapshot{}, errors.New("contiguous coverage exists without highest coverage")
	}
	var err error
	snapshot.SafeBlock, snapshot.FinalizedBlock, err = finalityNumbers(safeHeight, finalizedHeight)
	if err != nil {
		return publicquery.StatusSnapshot{}, err
	}
	if !snapshot.HighestCoveredKnown && (snapshot.SafeBlock != nil || snapshot.FinalizedBlock != nil) {
		return publicquery.StatusSnapshot{}, errors.New("finality markers exist without canonical blocks")
	}
	if snapshot.HighestCoveredKnown {
		if snapshot.SafeBlock != nil && *snapshot.SafeBlock > snapshot.HighestCoveredBlock {
			return publicquery.StatusSnapshot{}, errors.New("safe height exceeds canonical coverage")
		}
		if snapshot.FinalizedBlock != nil && *snapshot.FinalizedBlock > snapshot.HighestCoveredBlock {
			return publicquery.StatusSnapshot{}, errors.New("finalized height exceeds canonical coverage")
		}
	}
	if snapshot.Completeness.Trace == gen.StageStatePending {
		snapshot.Completeness.Trace, err = currentTraceCompleteness(contiguousEnd.Valid, traceState)
		if err != nil {
			return publicquery.StatusSnapshot{}, err
		}
	}
	if snapshot.Completeness.UserOperations == gen.StageStatePending && r.userOperations {
		var coveredNumber string
		var coveredHash []byte
		err = func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(strconv.FormatUint(r.userOperationStart, 10)); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(r.chainID); err != nil {
				return err
			}
			queryRow, err := dbgen.New(queryer).ERC4337CurrentSnapshot(ctx, queryValue0, queryValue1, r.userOperationDigest)
			if err != nil {
				return err
			}
			coveredNumber = queryRow.SnapshotNumber
			coveredHash = queryRow.SnapshotHash
			return nil
		}()
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No continuous configured-digest range is published yet.
		case err != nil:
			return publicquery.StatusSnapshot{}, fmt.Errorf("read UserOperation status coverage: %w", err)
		default:
			covered, parseErr := parseDecimalUint64(coveredNumber)
			if parseErr != nil || len(coveredHash) != common.HashLength {
				return publicquery.StatusSnapshot{}, errors.New("stored UserOperation status coverage is invalid")
			}
			if contiguousEnd.Valid && covered >= snapshot.IndexedBlock {
				snapshot.Completeness.UserOperations = gen.StageStateComplete
			}
		}
	}

	indexedKnown := contiguousEnd.Valid
	latestKnown := false
	runtimeConsistent := true
	if runtimeStatus != nil {
		runtime, exists, err := runtimeStatus(ctx)
		if err != nil {
			return publicquery.StatusSnapshot{}, fmt.Errorf("read durable sync runtime status: %w", err)
		}
		if exists && runtime.LatestKnown {
			snapshot.LatestBlock = runtime.Latest
			latestKnown = true
		}
		runtimeConsistent = exists && runtime.LatestKnown &&
			runtime.IndexedKnown == indexedKnown &&
			(!indexedKnown || runtime.Indexed == snapshot.IndexedBlock) &&
			runtime.HighestCoveredKnown == snapshot.HighestCoveredKnown &&
			(!snapshot.HighestCoveredKnown || runtime.HighestCovered == snapshot.HighestCoveredBlock)
		if !runtime.Ready || !runtime.BackfillComplete {
			runtimeConsistent = false
		}
	} else if latestBlock != nil {
		latest, err := latestBlock(ctx)
		if err != nil {
			return publicquery.StatusSnapshot{}, fmt.Errorf("read upstream latest block: %w", err)
		}
		snapshot.LatestBlock = latest
		latestKnown = true
	}
	snapshot.BackfillComplete = configured && latestKnown && snapshot.LatestBlock >= snapshot.CoverageStart &&
		indexedKnown && snapshot.IndexedBlock >= snapshot.LatestBlock
	snapshot.CoreReady = snapshot.BackfillComplete && runtimeConsistent
	if !snapshot.CoreReady {
		snapshot.Completeness.Core = gen.StageStatePending
	}
	return snapshot, nil
}

func (r *PostgresReader) Blocks(ctx context.Context, encodedCursor string, limit int) ([]gen.Block, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("block limit %d is outside 1..100", limit)
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable block query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)

	var snapshot blockCursor
	if encodedCursor == "" {
		snapshot, err = r.currentBlockCursor(ctx, tx)
		if err != nil {
			return nil, "", err
		}
	} else {
		if err := publicquery.DecodeCursor(encodedCursor, &snapshot); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if err := r.validateBlockCursor(ctx, tx, snapshot); err != nil {
			return nil, "", err
		}
	}

	chain, err := r.chainNumeric()
	if err != nil {
		return nil, "", err
	}
	queries := dbgen.New(r.db).WithTx(tx)
	var rows []dbgen.QueryListBlocksFirstRow
	if encodedCursor == "" {
		rows, err = queries.QueryListBlocksFirst(ctx, chain, numericUint64(snapshot.SnapshotNumber), int32(limit+1))
	} else {
		var page []dbgen.QueryListBlocksRow
		page, err = queries.QueryListBlocks(ctx, chain, numericUint64(snapshot.BeforeNumber), int32(limit+1))
		rows = make([]dbgen.QueryListBlocksFirstRow, len(page))
		for index, row := range page {
			rows[index] = dbgen.QueryListBlocksFirstRow(row)
		}
	}
	if err != nil {
		return nil, "", fmt.Errorf("query canonical block page: %w", err)
	}
	records := make([]blockRecord, 0, len(rows))
	for _, row := range rows {
		record, err := r.decodeBlock(row, true)
		if err != nil {
			return nil, "", err
		}
		records = append(records, record)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit stable block query: %w", err)
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	items := make([]gen.Block, len(records))
	for index := range records {
		items[index] = records[index].Model
	}
	if !hasMore || len(records) == 0 {
		return items, "", nil
	}
	last := records[len(records)-1]
	next, err := publicquery.EncodeCursor(blockCursor{
		ChainID:        r.chainID,
		SnapshotNumber: snapshot.SnapshotNumber,
		SnapshotHash:   snapshot.SnapshotHash,
		BeforeNumber:   last.Number,
		BeforeHash:     last.Hash.String(),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode block cursor: %w", err)
	}
	return items, next, nil
}

func (r *PostgresReader) Block(ctx context.Context, identifier string) (gen.Block, error) {
	chain, err := r.chainNumeric()
	if err != nil {
		return gen.Block{}, err
	}
	queries := dbgen.New(r.db)
	var stored dbgen.QueryListBlocksFirstRow
	forceCanonical := true
	if hash, isHash, err := parseHashIdentifier(identifier); err != nil {
		return gen.Block{}, err
	} else if isHash {
		row, err := queries.QueryBlockByHash(ctx, chain, hash.Bytes())
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.Block{}, publicquery.ErrNotFound
		}
		if err != nil {
			return gen.Block{}, fmt.Errorf("query block by hash: %w", err)
		}
		stored = dbgen.QueryListBlocksFirstRow(row)
		forceCanonical = false
	} else {
		height, err := parseBlockNumber(identifier)
		if err != nil {
			return gen.Block{}, err
		}
		row, err := queries.QueryBlockByNumber(ctx, chain, numericUint64(height))
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.Block{}, publicquery.ErrNotFound
		}
		if err != nil {
			return gen.Block{}, fmt.Errorf("query block by number: %w", err)
		}
		stored = dbgen.QueryListBlocksFirstRow(row)
	}
	record, err := r.decodeBlock(stored, forceCanonical)
	if err != nil {
		return gen.Block{}, err
	}
	return record.Model, nil
}

func (r *PostgresReader) Transaction(ctx context.Context, value string) (gen.Transaction, error) {
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("invalid transaction hash: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("begin stable transaction query: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	snapshot, err := r.currentBlockCursor(ctx, tx)
	if err != nil {
		return gen.Transaction{}, err
	}
	chain, err := r.chainNumeric()
	if err != nil {
		return gen.Transaction{}, err
	}
	row, err := dbgen.New(r.db).WithTx(tx).QueryTransactionByHash(ctx, chain, hash.Bytes())
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.Transaction{}, publicquery.ErrNotFound
	}
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("query transaction: %w", err)
	}
	record, err := r.decodeTransaction(dbgen.ListBlockTransactionsRow(row), snapshot.SnapshotNumber)
	if err != nil {
		return gen.Transaction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gen.Transaction{}, fmt.Errorf("commit stable transaction query: %w", err)
	}
	return record.Model, nil
}

// Address state cannot be derived correctly from value transfers alone. Until
// a fixed-block state adapter or indexed state table is wired, returning an
// empty balance/nonce would be a correctness bug.
func (r *PostgresReader) Address(_ context.Context, value string) (gen.AddressSummary, error) {
	if _, err := ethrpc.ParseAddress(value); err != nil {
		return gen.AddressSummary{}, fmt.Errorf("invalid address: %w", err)
	}
	return gen.AddressSummary{}, fmt.Errorf("%w: address balance, nonce, and code state are not indexed", publicquery.ErrUnavailable)
}

func (r *PostgresReader) Search(ctx context.Context, value, encodedCursor string, limit int) ([]gen.SearchResult, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, "", errors.New("search query is empty")
	}
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("search limit %d is outside 1..100", limit)
	}
	gate := resolvedNameGate{}
	searchValue := value
	if encodedCursor == "" && externalNameQuery(value) {
		if r.nameResolver == nil {
			return nil, "", nameCapabilityUnavailable("unavailable", "not_configured")
		}
		resolved, resolveErr := r.nameResolver.ResolveForward(ctx, value)
		if resolveErr != nil {
			return nil, "", nameResolverError(resolveErr)
		}
		if resolved.ObservationID <= 0 || resolved.Name == "" ||
			(resolved.Outcome != ensresolver.OutcomeResolved && resolved.Outcome != ensresolver.OutcomeNoRecord) ||
			(resolved.Source != ensresolver.SourceOfficial && resolved.Source != ensresolver.SourceCustom) {
			return nil, "", nameCapabilityUnavailable("failed", "invalid_response")
		}
		gate = resolvedNameGate{
			Name: resolved.Name, ObservationID: resolved.ObservationID, Source: string(resolved.Source),
		}
		if resolved.Outcome == ensresolver.OutcomeResolved {
			if resolved.Address == (common.Address{}) {
				return nil, "", nameCapabilityUnavailable("failed", "invalid_response")
			}
			gate.Address = strings.ToLower(resolved.Address.String())
		}
		searchValue = resolved.Name
	}
	return r.search(ctx, value, searchValue, encodedCursor, limit, gate)
}

type resolvedNameGate struct {
	Name          string
	Address       string
	Source        string
	ObservationID int64
}

type userOperationSearchSnapshot struct {
	available bool
	end       uint64
	hash      string
}

func (r *PostgresReader) search(
	ctx context.Context,
	value, searchValue, encodedCursor string,
	limit int,
	gate resolvedNameGate,
) ([]gen.SearchResult, string, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable search: %w", err)
	}
	defer dbaccess.Rollback(ctx, tx)
	snapshot, err := r.currentBlockCursor(ctx, tx)
	if err != nil {
		return nil, "", err
	}
	var generation, minGeneration int64
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(r.chainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).GetCurrentSearchGeneration(ctx, queryValue0)
		if err != nil {
			return err
		}
		generation = queryRow.Generation
		minGeneration = queryRow.MinGeneration
		return nil
	}(); err != nil {
		return nil, "", fmt.Errorf("read search catalog generation: %w", err)
	}
	if generation < 0 || minGeneration < 0 || minGeneration > generation {
		return nil, "", errors.New("search catalog generation is invalid")
	}
	userOperations := userOperationSearchSnapshot{}
	if encodedCursor == "" {
		userOperations, err = r.currentSearchUserOperationSnapshot(ctx, tx)
		if err != nil {
			return nil, "", err
		}
	}
	var boundary *searchCursor
	if encodedCursor != "" {
		var decoded searchCursor
		if err := publicquery.DecodeCursor(encodedCursor, &decoded); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if err := r.validateSearchCursor(ctx, tx, decoded, value); err != nil {
			return nil, "", err
		}
		snapshot.SnapshotNumber, snapshot.SnapshotHash = decoded.SnapshotNumber, decoded.SnapshotHash
		generation = decoded.Generation
		gate = resolvedNameGate{
			Name: decoded.ResolvedName, Address: decoded.ResolvedNameAddress,
			Source: decoded.ResolvedNameSource, ObservationID: decoded.ResolvedNameObservationID,
		}
		if gate.Name != "" {
			searchValue = gate.Name
		}
		userOperations = userOperationSearchSnapshot{
			available: decoded.UserOperationSnapshot,
			end:       decoded.UserOperationSnapshotEnd,
			hash:      decoded.UserOperationSnapshotHash,
		}
		boundary = &decoded
	}
	if gate.ObservationID > 0 {
		visible, visibilityErr := r.resolvedNameVisible(
			ctx, tx, gate, generation,
		)
		if visibilityErr != nil {
			return nil, "", visibilityErr
		}
		if !visible {
			return nil, "", nameCapabilityUnavailable("unavailable", "stale_name_snapshot")
		}
	}
	var results []gen.SearchResult
	hash, isHash, parseErr := parseHashIdentifier(value)
	if parseErr != nil {
		return nil, "", parseErr
	} else if isHash {
		results, err = r.searchHash(ctx, tx, hash, generation, limit+1, userOperations)
	} else if _, addressErr := ethrpc.ParseAddress(value); addressErr == nil {
		results, err = r.searchText(
			ctx, tx, searchValue, snapshot.SnapshotNumber, generation, gate.ObservationID, boundary, limit+2,
		)
	} else if height, blockParseErr := parseBlockNumber(value); blockParseErr == nil {
		results, err = r.searchBlockNumber(ctx, tx, height, generation)
	} else {
		results, err = r.searchText(
			ctx, tx, searchValue, snapshot.SnapshotNumber, generation, gate.ObservationID, boundary, limit+2,
		)
	}
	if err != nil {
		return nil, "", err
	}
	if address, parseErr := ethrpc.ParseAddress(value); parseErr == nil {
		checksummed, err := ChecksumAddress(address.String())
		if err != nil {
			return nil, "", err
		}
		extra := gen.SearchResult{Kind: gen.SearchResultKindAddress, Key: checksummed, Label: checksummed, Rank: 50}
		catalogHasAddress := false
		for _, result := range results {
			if strings.EqualFold(result.Key, checksummed) {
				catalogHasAddress = true
				break
			}
		}
		if !catalogHasAddress && (boundary == nil || afterSearchBoundary(extra, *boundary)) {
			results = mergeSearchResults(results, extra, limit+2)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit stable search: %w", err)
	}
	if boundary != nil {
		filtered := results[:0]
		for _, result := range results {
			if afterSearchBoundary(result, *boundary) {
				filtered = append(filtered, result)
			}
		}
		results = filtered
	}
	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}
	if !hasMore || len(results) == 0 {
		return results, "", nil
	}
	last := results[len(results)-1]
	next, err := publicquery.EncodeCursor(searchCursor{
		ChainID: r.chainID, SnapshotNumber: snapshot.SnapshotNumber, SnapshotHash: snapshot.SnapshotHash,
		Generation: generation, Query: strings.ToLower(value),
		UserOperationDigest: func() string {
			if r.userOperations {
				return hex.EncodeToString(r.userOperationDigest)
			}
			return ""
		}(),
		UserOperationSnapshot:     userOperations.available,
		UserOperationSnapshotEnd:  userOperations.end,
		UserOperationSnapshotHash: userOperations.hash,
		ResolvedName:              gate.Name, ResolvedNameAddress: gate.Address,
		ResolvedNameObservationID: gate.ObservationID, ResolvedNameSource: gate.Source,
		AfterRank: last.Rank, AfterKind: string(last.Kind), AfterKey: canonicalSearchBoundaryKey(last.Key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode search cursor: %w", err)
	}
	return results, next, nil
}

func (r *PostgresReader) currentSearchUserOperationSnapshot(
	ctx context.Context,
	tx pgx.Tx,
) (userOperationSearchSnapshot, error) {
	if !r.userOperations {
		return userOperationSearchSnapshot{}, nil
	}
	snapshot, err := r.currentUserOperationSnapshot(ctx, tx)
	if errors.Is(err, publicquery.ErrNotReady) {
		return userOperationSearchSnapshot{}, nil
	}
	if err != nil {
		return userOperationSearchSnapshot{}, err
	}
	return userOperationSearchSnapshot{
		available: true,
		end:       snapshot.SnapshotNumber,
		hash:      snapshot.SnapshotHash,
	}, nil
}

type capabilityDetailer interface {
	CapabilityDetails() (capability, state, code string)
}

func nameResolverError(err error) error {
	if errors.Is(err, ensresolver.ErrInvalidName) {
		return publicquery.ErrInvalidInput
	}
	var detailer capabilityDetailer
	if errors.As(err, &detailer) {
		capability, state, code := detailer.CapabilityDetails()
		if capability == "name" && stableNameCapabilityCode(code) {
			if stable := publicquery.NewCapabilityUnavailableError(capability, state, code); stable != publicquery.ErrUnavailable {
				return stable
			}
		}
	}
	return nameCapabilityUnavailable("failed", "resolver_failure")
}

func stableNameCapabilityCode(code string) bool {
	switch code {
	case "unsafe_url", "unavailable", "temporary", "unsafe_content", "invalid_content", "too_large",
		"transport_failure", "invalid_response", "stale_block", "stale_name_snapshot", "identity_conflict",
		ensresolver.CodeRPCUnavailable, ensresolver.CodeCCIPUnavailable,
		ensresolver.CodeCCIPSenderMismatch, ensresolver.CodeCCIPDepthExceeded,
		ensresolver.CodeResolverNotContract, ensresolver.CodeResolverFailure,
		ensresolver.CodeForwardMismatch, ensresolver.CodeSourceIdentity,
		ensresolver.CodeCustomDeployment:
		return true
	default:
		return false
	}
}

func nameCapabilityUnavailable(state, code string) error {
	return publicquery.NewCapabilityUnavailableError("name", state, code)
}

func externalNameQuery(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= 255 && strings.Contains(value, ".") &&
		!strings.ContainsAny(value, "\x00\r\n\t /\\")
}

func normalizeOptionalStage(state gen.StageState) (gen.StageState, error) {
	if state == "" {
		return gen.StageStateUnavailable, nil
	}
	if !state.Valid() {
		return "", fmt.Errorf("optional stage state %q is invalid", state)
	}
	return state, nil
}

func currentTraceCompleteness(indexed bool, state pgtype.Text) (gen.StageState, error) {
	if !indexed || !state.Valid {
		return gen.StageStatePending, nil
	}
	result := gen.StageState(state.String)
	switch result {
	case gen.StageStateComplete, gen.StageStateUnavailable, gen.StageStateFailed:
		return result, nil
	default:
		return "", fmt.Errorf("published trace stage state %q is invalid", state.String)
	}
}

func parseDecimalUint64(value string) (uint64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, fmt.Errorf("invalid canonical decimal quantity %q", value)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("invalid canonical decimal quantity %q", value)
		}
	}
	return strconv.ParseUint(value, 10, 64)
}

func parseBlockNumber(value string) (uint64, error) {
	if strings.HasPrefix(value, "0x") {
		if len(value) == 2 {
			return 0, errors.New("hex block number has no digits")
		}
		return strconv.ParseUint(value[2:], 16, 64)
	}
	return strconv.ParseUint(value, 10, 64)
}

func parseHashIdentifier(value string) (common.Hash, bool, error) {
	if len(value) != 66 {
		return common.Hash{}, false, nil
	}
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return common.Hash{}, false, fmt.Errorf("invalid hash identifier: %w", err)
	}
	return hash, true, nil
}

func decodeHashBytes(value []byte) (common.Hash, error) {
	if len(value) != common.HashLength {
		return common.Hash{}, fmt.Errorf("database hash has %d bytes, expected 32", len(value))
	}
	return common.BytesToHash(value), nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
