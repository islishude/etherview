// Package query adapts the PostgreSQL core schema to stable public API models.
package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	ensresolver "github.com/islishude/etherview/internal/ens"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

var ErrInvalidCursor = httpapi.ErrInvalidCursor

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
	ChainID        uint64
	StartBlock     uint64
	LatestBlock    LatestBlockFunc
	RuntimeStatus  RuntimeStatusFunc
	OptionalStages gen.Completeness
	NameResolver   NameResolver
}

type PostgresReader struct {
	db            *sql.DB
	chainID       string
	startBlock    uint64
	latestBlock   LatestBlockFunc
	runtimeStatus RuntimeStatusFunc
	completeness  gen.Completeness
	nameResolver  NameResolver
}

var _ httpapi.Reader = (*PostgresReader)(nil)

func NewPostgresReader(db *sql.DB, options Options) (*PostgresReader, error) {
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
	completeness.Core = gen.StageStateComplete
	return &PostgresReader{
		db:            db,
		chainID:       strconv.FormatUint(options.ChainID, 10),
		startBlock:    options.StartBlock,
		latestBlock:   options.LatestBlock,
		runtimeStatus: options.RuntimeStatus,
		completeness:  completeness,
		nameResolver:  options.NameResolver,
	}, nil
}

func (r *PostgresReader) Status(ctx context.Context) (httpapi.StatusSnapshot, error) {
	return r.status(ctx, r.db, r.runtimeStatus, r.latestBlock)
}

func (r *PostgresReader) status(
	ctx context.Context,
	queryer searchQueryer,
	runtimeStatus RuntimeStatusFunc,
	latestBlock LatestBlockFunc,
) (httpapi.StatusSnapshot, error) {
	snapshot := httpapi.StatusSnapshot{
		CoverageStart: r.startBlock,
		CoverageEnd:   r.startBlock,
		Completeness:  r.completeness,
	}
	var configuredStart, contiguousEnd, checkpointHeight, highestEnd sql.NullString
	var contiguousHash, checkpointHash, highestHash []byte
	var safeHeight, finalizedHeight, traceState sql.NullString
	if err := queryer.QueryRowContext(ctx, dbgen.QueryStatusState, r.chainID).Scan(
		&configuredStart,
		&contiguousEnd, &contiguousHash,
		&checkpointHeight, &checkpointHash,
		&highestEnd, &highestHash,
		&safeHeight, &finalizedHeight,
		&traceState,
	); err != nil {
		return httpapi.StatusSnapshot{}, fmt.Errorf("query index status: %w", err)
	}
	configured := configuredStart.Valid
	if configured {
		persistedStart, err := parseDecimalUint64(configuredStart.String)
		if err != nil {
			return httpapi.StatusSnapshot{}, fmt.Errorf("decode configured index start: %w", err)
		}
		if persistedStart != r.startBlock {
			return httpapi.StatusSnapshot{}, fmt.Errorf("configured index start mismatch: persisted=%d requested=%d", persistedStart, r.startBlock)
		}
		snapshot.CoverageStart = persistedStart
	}
	if contiguousEnd.Valid != checkpointHeight.Valid {
		return httpapi.StatusSnapshot{}, errors.New("core coverage and checkpoint presence differ")
	}
	if contiguousEnd.Valid {
		if len(contiguousHash) != 32 || len(checkpointHash) != 32 || !equalBytes(contiguousHash, checkpointHash) {
			return httpapi.StatusSnapshot{}, errors.New("core coverage and checkpoint identities differ")
		}
		indexed, err := parseDecimalUint64(contiguousEnd.String)
		if err != nil {
			return httpapi.StatusSnapshot{}, fmt.Errorf("decode contiguous coverage end: %w", err)
		}
		checkpoint, err := parseDecimalUint64(checkpointHeight.String)
		if err != nil {
			return httpapi.StatusSnapshot{}, fmt.Errorf("decode core checkpoint: %w", err)
		}
		if indexed != checkpoint {
			return httpapi.StatusSnapshot{}, errors.New("core coverage and checkpoint heights differ")
		}
		snapshot.IndexedBlock = indexed
	}
	if highestEnd.Valid {
		if !configured || len(highestHash) != 32 {
			return httpapi.StatusSnapshot{}, errors.New("highest coverage identity is internally inconsistent")
		}
		highest, err := parseDecimalUint64(highestEnd.String)
		if err != nil {
			return httpapi.StatusSnapshot{}, fmt.Errorf("decode highest covered block: %w", err)
		}
		if contiguousEnd.Valid && snapshot.IndexedBlock > highest {
			return httpapi.StatusSnapshot{}, errors.New("contiguous coverage exceeds highest covered block")
		}
		snapshot.HighestCoveredBlock = highest
		snapshot.HighestCoveredKnown = true
		snapshot.CoverageEnd = highest
	} else if contiguousEnd.Valid {
		return httpapi.StatusSnapshot{}, errors.New("contiguous coverage exists without highest coverage")
	}
	var err error
	snapshot.SafeBlock, snapshot.FinalizedBlock, err = finalityNumbers(safeHeight, finalizedHeight)
	if err != nil {
		return httpapi.StatusSnapshot{}, err
	}
	if !snapshot.HighestCoveredKnown && (snapshot.SafeBlock != nil || snapshot.FinalizedBlock != nil) {
		return httpapi.StatusSnapshot{}, errors.New("finality markers exist without canonical blocks")
	}
	if snapshot.HighestCoveredKnown {
		if snapshot.SafeBlock != nil && *snapshot.SafeBlock > snapshot.HighestCoveredBlock {
			return httpapi.StatusSnapshot{}, errors.New("safe height exceeds canonical coverage")
		}
		if snapshot.FinalizedBlock != nil && *snapshot.FinalizedBlock > snapshot.HighestCoveredBlock {
			return httpapi.StatusSnapshot{}, errors.New("finalized height exceeds canonical coverage")
		}
	}
	if snapshot.Completeness.Trace == gen.StageStatePending {
		snapshot.Completeness.Trace, err = currentTraceCompleteness(contiguousEnd.Valid, traceState)
		if err != nil {
			return httpapi.StatusSnapshot{}, err
		}
	}

	indexedKnown := contiguousEnd.Valid
	latestKnown := false
	runtimeConsistent := true
	if runtimeStatus != nil {
		runtime, exists, err := runtimeStatus(ctx)
		if err != nil {
			return httpapi.StatusSnapshot{}, fmt.Errorf("read durable sync runtime status: %w", err)
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
			return httpapi.StatusSnapshot{}, fmt.Errorf("read upstream latest block: %w", err)
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
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable block query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var snapshot blockCursor
	if encodedCursor == "" {
		snapshot, err = r.currentBlockCursor(ctx, tx)
		if err != nil {
			return nil, "", err
		}
	} else {
		if err := httpapi.DecodeCursor(encodedCursor, &snapshot); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		if err := r.validateBlockCursor(ctx, tx, snapshot); err != nil {
			return nil, "", err
		}
	}

	pageSQL := dbgen.QueryListBlocks
	boundary := snapshot.BeforeNumber
	if encodedCursor == "" {
		pageSQL = dbgen.QueryListBlocksFirst
		boundary = snapshot.SnapshotNumber
	}
	rows, err := tx.QueryContext(ctx, pageSQL, r.chainID, strconv.FormatUint(boundary, 10), limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("query canonical block page: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	records := make([]blockRecord, 0, limit+1)
	for rows.Next() {
		record, err := r.scanBlock(rows, true)
		if err != nil {
			return nil, "", err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate canonical block page: %w", err)
	}
	if err := tx.Commit(); err != nil {
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
	next, err := httpapi.EncodeCursor(blockCursor{
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
	if hash, isHash, err := parseHashIdentifier(identifier); err != nil {
		return gen.Block{}, err
	} else if isHash {
		rows, err := r.db.QueryContext(ctx, dbgen.QueryBlockByHash, r.chainID, hash.Bytes())
		if err != nil {
			return gen.Block{}, fmt.Errorf("query block by hash: %w", err)
		}
		defer rows.Close() //nolint:errcheck
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return gen.Block{}, fmt.Errorf("query block by hash: %w", err)
			}
			return gen.Block{}, httpapi.ErrNotFound
		}
		record, err := r.scanBlock(rows, false)
		if err != nil {
			return gen.Block{}, err
		}
		return record.Model, nil
	}
	height, err := parseBlockNumber(identifier)
	if err != nil {
		return gen.Block{}, err
	}
	rows, err := r.db.QueryContext(ctx, dbgen.QueryBlockByNumber, r.chainID, strconv.FormatUint(height, 10))
	if err != nil {
		return gen.Block{}, fmt.Errorf("query block by number: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return gen.Block{}, fmt.Errorf("query block by number: %w", err)
		}
		return gen.Block{}, httpapi.ErrNotFound
	}
	record, err := r.scanBlock(rows, true)
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
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("begin stable transaction query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := r.currentBlockCursor(ctx, tx)
	if err != nil {
		return gen.Transaction{}, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.QueryTransactionByHash, r.chainID, hash.Bytes())
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("query transaction: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return gen.Transaction{}, fmt.Errorf("query transaction: %w", err)
		}
		return gen.Transaction{}, httpapi.ErrNotFound
	}
	record, err := r.scanTransaction(rows, snapshot.SnapshotNumber)
	if err != nil {
		return gen.Transaction{}, err
	}
	if err := tx.Commit(); err != nil {
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
	return gen.AddressSummary{}, fmt.Errorf("%w: address balance, nonce, and code state are not indexed", httpapi.ErrUnavailable)
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

func (r *PostgresReader) search(
	ctx context.Context,
	value, searchValue, encodedCursor string,
	limit int,
	gate resolvedNameGate,
) ([]gen.SearchResult, string, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable search: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, err := r.currentBlockCursor(ctx, tx)
	if err != nil {
		return nil, "", err
	}
	var generation, minGeneration int64
	if err := tx.QueryRowContext(ctx, dbgen.GetCurrentSearchGeneration, r.chainID).Scan(&generation, &minGeneration); err != nil {
		return nil, "", fmt.Errorf("read search catalog generation: %w", err)
	}
	if generation < 0 || minGeneration < 0 || minGeneration > generation {
		return nil, "", errors.New("search catalog generation is invalid")
	}
	var boundary *searchCursor
	if encodedCursor != "" {
		var decoded searchCursor
		if err := httpapi.DecodeCursor(encodedCursor, &decoded); err != nil {
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
		results, err = r.searchHash(ctx, tx, hash, generation, limit+1)
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
	if err := tx.Commit(); err != nil {
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
	next, err := httpapi.EncodeCursor(searchCursor{
		ChainID: r.chainID, SnapshotNumber: snapshot.SnapshotNumber, SnapshotHash: snapshot.SnapshotHash,
		Generation: generation, Query: strings.ToLower(value),
		ResolvedName: gate.Name, ResolvedNameAddress: gate.Address,
		ResolvedNameObservationID: gate.ObservationID, ResolvedNameSource: gate.Source,
		AfterRank: last.Rank, AfterKind: string(last.Kind), AfterKey: canonicalSearchBoundaryKey(last.Key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode search cursor: %w", err)
	}
	return results, next, nil
}

type capabilityDetailer interface {
	CapabilityDetails() (capability, state, code string)
}

func nameResolverError(err error) error {
	if errors.Is(err, ensresolver.ErrInvalidName) {
		return httpapi.ErrInvalidInput
	}
	var detailer capabilityDetailer
	if errors.As(err, &detailer) {
		capability, state, code := detailer.CapabilityDetails()
		if capability == "name" && stableNameCapabilityCode(code) {
			if stable := httpapi.NewCapabilityUnavailableError(capability, state, code); stable != httpapi.ErrUnavailable {
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
	return httpapi.NewCapabilityUnavailableError("name", state, code)
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

func currentTraceCompleteness(indexed bool, state sql.NullString) (gen.StageState, error) {
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
