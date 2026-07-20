// Package query adapts the PostgreSQL core schema to stable public API models.
package query

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/islishude/etherview/internal/api/gen"
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
	Resolve(context.Context, string) (string, error)
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
	snapshot := httpapi.StatusSnapshot{
		CoverageStart: r.startBlock,
		CoverageEnd:   r.startBlock,
		Completeness:  r.completeness,
	}
	var configuredStart, contiguousEnd, checkpointHeight, highestEnd sql.NullString
	var contiguousHash, checkpointHash, highestHash []byte
	var safeHeight, finalizedHeight sql.NullString
	if err := r.db.QueryRowContext(ctx, statusStateSQL, r.chainID).Scan(
		&configuredStart,
		&contiguousEnd, &contiguousHash,
		&checkpointHeight, &checkpointHash,
		&highestEnd, &highestHash,
		&safeHeight, &finalizedHeight,
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

	indexedKnown := contiguousEnd.Valid
	latestKnown := false
	runtimeConsistent := true
	if r.runtimeStatus != nil {
		runtime, exists, err := r.runtimeStatus(ctx)
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
	} else if r.latestBlock != nil {
		latest, err := r.latestBlock(ctx)
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
	defer tx.Rollback()

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

	pageSQL := listBlocksSQL
	boundary := snapshot.BeforeNumber
	if encodedCursor == "" {
		pageSQL = listBlocksFirstSQL
		boundary = snapshot.SnapshotNumber
	}
	rows, err := tx.QueryContext(ctx, pageSQL, r.chainID, strconv.FormatUint(boundary, 10), limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("query canonical block page: %w", err)
	}
	defer rows.Close()
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
		hashBytes, err := hash.Bytes()
		if err != nil {
			return gen.Block{}, err
		}
		rows, err := r.db.QueryContext(ctx, blockByHashSQL, r.chainID, hashBytes)
		if err != nil {
			return gen.Block{}, fmt.Errorf("query block by hash: %w", err)
		}
		defer rows.Close()
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
	rows, err := r.db.QueryContext(ctx, blockByNumberSQL, r.chainID, strconv.FormatUint(height, 10))
	if err != nil {
		return gen.Block{}, fmt.Errorf("query block by number: %w", err)
	}
	defer rows.Close()
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
	hashBytes, err := hash.Bytes()
	if err != nil {
		return gen.Transaction{}, err
	}
	rows, err := r.db.QueryContext(ctx, transactionByHashSQL, r.chainID, hashBytes)
	if err != nil {
		return gen.Transaction{}, fmt.Errorf("query transaction: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return gen.Transaction{}, fmt.Errorf("query transaction: %w", err)
		}
		return gen.Transaction{}, httpapi.ErrNotFound
	}
	record, err := r.scanTransaction(rows)
	return record.Model, err
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
	resolvedNameAddress := ""
	if encodedCursor == "" && externalNameQuery(value) {
		if r.nameResolver == nil {
			return nil, "", nameCapabilityUnavailable("unavailable", "not_configured")
		}
		resolved, resolveErr := r.nameResolver.Resolve(ctx, value)
		if resolveErr != nil {
			return nil, "", nameResolverError(resolveErr)
		}
		address, parseErr := ethrpc.ParseAddress(resolved)
		if parseErr != nil {
			return nil, "", nameCapabilityUnavailable("failed", "invalid_response")
		}
		resolvedNameAddress = strings.ToLower(address.String())
	}
	return r.search(ctx, value, encodedCursor, limit, resolvedNameAddress)
}

func (r *PostgresReader) search(ctx context.Context, value, encodedCursor string, limit int, resolvedNameAddress string) ([]gen.SearchResult, string, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable search: %w", err)
	}
	defer tx.Rollback()
	snapshot, err := r.currentBlockCursor(ctx, tx)
	if err != nil {
		return nil, "", err
	}
	var generation, minGeneration int64
	if err := tx.QueryRowContext(ctx, currentSearchGenerationSQL, r.chainID).Scan(&generation, &minGeneration); err != nil {
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
		resolvedNameAddress = decoded.ResolvedNameAddress
		boundary = &decoded
	}
	if resolvedNameAddress != "" {
		visible, visibilityErr := r.resolvedNameVisible(
			ctx, tx, value, resolvedNameAddress, snapshot.SnapshotNumber, generation,
		)
		if visibilityErr != nil {
			return nil, "", visibilityErr
		}
		if !visible {
			return nil, "", nameCapabilityUnavailable("unavailable", "stale_block")
		}
	}
	var results []gen.SearchResult
	hash, isHash, parseErr := parseHashIdentifier(value)
	if parseErr != nil {
		return nil, "", parseErr
	} else if isHash {
		results, err = r.searchHash(ctx, tx, hash, generation, limit+1)
	} else if height, blockParseErr := parseBlockNumber(value); blockParseErr == nil {
		results, err = r.searchBlockNumber(ctx, tx, height, generation)
	} else {
		results, err = r.searchText(
			ctx, tx, value, snapshot.SnapshotNumber, generation, resolvedNameAddress, boundary, limit+2,
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
		if boundary == nil || afterSearchBoundary(extra, *boundary) {
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
		Generation: generation, Query: strings.ToLower(value), ResolvedNameAddress: resolvedNameAddress,
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
		"transport_failure", "invalid_response", "stale_block", "identity_conflict":
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
	if !state.Valid() || state == gen.StageStateComplete {
		return "", fmt.Errorf("optional stage state %q must be pending, unavailable, or failed", state)
	}
	return state, nil
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

func parseHashIdentifier(value string) (ethrpc.Hash, bool, error) {
	if len(value) != 66 {
		return "", false, nil
	}
	hash, err := ethrpc.ParseHash(value)
	if err != nil {
		return "", false, fmt.Errorf("invalid hash identifier: %w", err)
	}
	return hash, true, nil
}

func decodeHashBytes(value []byte) (ethrpc.Hash, error) {
	if len(value) != 32 {
		return "", fmt.Errorf("database hash has %d bytes, expected 32", len(value))
	}
	return ethrpc.ParseHash("0x" + hex.EncodeToString(value))
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

const statusStateSQL = `
SELECT
	configuration.configured_start::text,
	contiguous.range_end::text,
	contiguous_block.block_hash,
    checkpoint.contiguous_through::text,
    checkpoint.block_hash,
	highest.range_end::text,
	highest_block.block_hash,
    finality.safe_number::text,
    finality.finalized_number::text
FROM (SELECT 1) AS singleton
LEFT JOIN core_index_configuration AS configuration
  ON configuration.chain_id = $1::numeric
LEFT JOIN core_coverage_ranges AS contiguous
  ON contiguous.chain_id = $1::numeric
 AND contiguous.range_start = configuration.configured_start
LEFT JOIN canonical_blocks AS contiguous_block
  ON contiguous_block.chain_id = $1::numeric
 AND contiguous_block.number = contiguous.range_end
LEFT JOIN index_checkpoints AS checkpoint
  ON checkpoint.chain_id = $1::numeric AND checkpoint.stage = 'core'
LEFT JOIN LATERAL (
	SELECT range_end
	FROM core_coverage_ranges
	WHERE chain_id = $1::numeric
	ORDER BY range_end DESC
	LIMIT 1
) AS highest ON TRUE
LEFT JOIN canonical_blocks AS highest_block
  ON highest_block.chain_id = $1::numeric
 AND highest_block.number = highest.range_end
LEFT JOIN chain_finality AS finality
  ON finality.chain_id = $1::numeric`

const listBlocksSQL = `
SELECT
    block.raw,
    canonical.number::text,
    canonical.block_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = canonical.chain_id
WHERE canonical.chain_id = $1::numeric
  AND canonical.number < $2::numeric
ORDER BY canonical.number DESC
LIMIT $3`

const listBlocksFirstSQL = `
SELECT
    block.raw,
    canonical.number::text,
    canonical.block_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = canonical.chain_id
WHERE canonical.chain_id = $1::numeric
  AND canonical.number <= $2::numeric
ORDER BY canonical.number DESC
LIMIT $3`

const blockByNumberSQL = `
SELECT
    block.raw,
    canonical.number::text,
    canonical.block_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = canonical.chain_id
WHERE canonical.chain_id = $1::numeric AND canonical.number = $2::numeric`

const blockByHashSQL = `
SELECT
    block.raw,
    block.number::text,
    block.hash,
    (canonical.block_hash IS NOT NULL),
    finality.safe_number::text,
    finality.finalized_number::text
FROM blocks AS block
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = block.chain_id
WHERE block.chain_id = $1::numeric AND block.hash = $2
LIMIT 1`

const transactionByHashSQL = `
SELECT
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    (canonical.block_hash IS NOT NULL),
    finality.safe_number::text,
    finality.finalized_number::text
FROM transaction_inclusions AS inclusion
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
ORDER BY (canonical.block_hash IS NOT NULL) DESC, inclusion.block_number DESC
LIMIT 1`

const searchHashSQL = `
WITH visible_labels AS (
    SELECT document.result_kind, document.result_key, document.result_label, document.id
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.source_kind = 'label'
      AND document.valid_from_generation <= $3
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $3)
)
SELECT kind, key, label, rank, canonical
FROM (
    SELECT
        'block'::text AS kind,
        '0x' || encode(block.hash, 'hex') AS key,
        COALESCE(operator_label.result_label, 'Block #' || block.number::text) AS label,
        CASE WHEN operator_label.result_label IS NULL THEN 100 ELSE 110 END::bigint AS rank,
        (canonical.block_hash IS NOT NULL) AS canonical
    FROM blocks AS block
    LEFT JOIN canonical_blocks AS canonical
      ON canonical.chain_id = block.chain_id
     AND canonical.number = block.number
     AND canonical.block_hash = block.hash
    LEFT JOIN LATERAL (
        SELECT visible.result_label
        FROM visible_labels AS visible
        WHERE visible.result_kind = 'block'
          AND lower(visible.result_key) = ('0x' || encode(block.hash, 'hex'))
        ORDER BY visible.id DESC
        LIMIT 1
    ) AS operator_label ON TRUE
    WHERE block.chain_id = $1::numeric AND block.hash = $2

    UNION ALL

    SELECT
        'transaction'::text,
        '0x' || encode(transaction.hash, 'hex'),
        COALESCE(operator_label.result_label, 'Transaction 0x' || encode(transaction.hash, 'hex')),
        CASE WHEN operator_label.result_label IS NULL THEN 90 ELSE 110 END::bigint,
        EXISTS (
            SELECT 1 FROM transaction_inclusions AS inclusion
            JOIN canonical_blocks AS canonical
              ON canonical.chain_id = inclusion.chain_id
             AND canonical.number = inclusion.block_number
             AND canonical.block_hash = inclusion.block_hash
            WHERE inclusion.chain_id = transaction.chain_id
              AND inclusion.tx_hash = transaction.hash
        )
    FROM transactions AS transaction
    LEFT JOIN LATERAL (
        SELECT visible.result_label
        FROM visible_labels AS visible
        WHERE visible.result_kind = 'transaction'
          AND lower(visible.result_key) = ('0x' || encode(transaction.hash, 'hex'))
        ORDER BY visible.id DESC
        LIMIT 1
    ) AS operator_label ON TRUE
    WHERE transaction.chain_id = $1::numeric AND transaction.hash = $2
) AS results
ORDER BY rank DESC, kind
LIMIT $4`

const searchBlockNumberSQL = `
WITH visible_labels AS (
    SELECT document.result_key, document.result_label, document.id
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.source_kind = 'label'
      AND document.result_kind = 'block'
      AND document.valid_from_generation <= $3
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $3)
)
SELECT canonical.number::text,
       canonical.block_hash,
       COALESCE(operator_label.result_label, 'Block #' || canonical.number::text),
       CASE WHEN operator_label.result_label IS NULL THEN 100 ELSE 110 END::bigint
FROM canonical_blocks AS canonical
LEFT JOIN LATERAL (
    SELECT visible.result_label
    FROM visible_labels AS visible
    WHERE lower(visible.result_key) IN (
        canonical.number::text,
        '0x' || encode(canonical.block_hash, 'hex')
    )
    ORDER BY CASE WHEN lower(visible.result_key) = canonical.number::text THEN 0 ELSE 1 END,
             visible.id DESC
    LIMIT 1
) AS operator_label ON TRUE
WHERE canonical.chain_id = $1::numeric AND canonical.number = $2::numeric`

const searchTextSQL = `
WITH visible_documents AS (
    SELECT document.*
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.valid_from_generation <= $4
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $4)
), candidates AS (
    SELECT document.result_kind AS kind,
           lower(document.result_key) AS key,
           document.result_label AS label,
           CASE document.source_kind
             WHEN 'label' THEN CASE WHEN $2 = ANY(document.exact_terms) THEN 110 ELSE 80 END
             WHEN 'name' THEN CASE WHEN $2 = ANY(document.exact_terms) THEN 100 ELSE 70 END
             WHEN 'token' THEN CASE
                 WHEN lower(document.result_key) = $2 THEN 105
                 WHEN $2 = ANY(document.exact_terms) THEN 95 ELSE 65 END
             WHEN 'verified_contract' THEN CASE
                 WHEN lower(document.result_key) = $2 THEN 104
                 WHEN $2 = ANY(document.exact_terms) THEN 94 ELSE 64 END
           END::bigint AS rank,
           CASE WHEN document.source_kind IN ('name', 'token') THEN TRUE ELSE NULL END::boolean AS canonical
    FROM visible_documents AS document
    LEFT JOIN canonical_blocks AS canonical
      ON document.source_kind IN ('name', 'token')
     AND canonical.chain_id = document.chain_id
     AND canonical.number = document.block_number
     AND canonical.block_hash = document.block_hash
    LEFT JOIN LATERAL (
        SELECT observation.code_hash, observation.block_number
        FROM visible_documents AS observation
        JOIN canonical_blocks AS observed_canonical
          ON observed_canonical.chain_id = observation.chain_id
         AND observed_canonical.number = observation.block_number
         AND observed_canonical.block_hash = observation.block_hash
        WHERE document.source_kind = 'verified_contract'
          AND observation.source_kind = 'code'
          AND observation.target_address = document.target_address
          AND observation.block_number <= $3::numeric
          AND observation.source_canonical = TRUE
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS current_code ON TRUE
    WHERE document.source_kind <> 'code'
      AND (
          document.source_kind <> 'name'
          OR $10 = ''
          OR lower(document.result_key) = $10
      )
      AND ($2 = ANY(document.exact_terms) OR EXISTS (
          SELECT 1 FROM unnest(document.partial_terms) AS term
          WHERE strpos(term, $2) > 0
      ))
      AND (
          document.source_kind NOT IN ('name', 'token')
          OR document.id = (
              SELECT latest.id
              FROM visible_documents AS latest
              JOIN canonical_blocks AS latest_canonical
                ON latest_canonical.chain_id = latest.chain_id
               AND latest_canonical.number = latest.block_number
               AND latest_canonical.block_hash = latest.block_hash
              WHERE latest.source_kind = document.source_kind
                AND latest.logical_identity = document.logical_identity
                AND latest.block_number <= $3::numeric
                AND latest.source_canonical = TRUE
              ORDER BY latest.block_number DESC, latest.valid_from_generation DESC, latest.id DESC
              LIMIT 1
          )
      )
      AND (
          document.source_kind = 'label'
          OR (
              document.source_kind IN ('name', 'token')
              AND document.block_number <= $3::numeric
              AND canonical.block_hash IS NOT NULL
              AND document.source_canonical = TRUE
          )
          OR (
              document.source_kind = 'verified_contract'
              AND current_code.code_hash = document.code_hash
              AND document.valid_from_block <= current_code.block_number
              AND (document.valid_to_block IS NULL OR document.valid_to_block >= current_code.block_number)
          )
      )
), deduplicated AS (
    SELECT DISTINCT ON (kind, key) kind, key, label, rank, canonical
    FROM candidates
    ORDER BY kind, key, rank DESC, label
)
SELECT kind, key, label, rank, canonical
FROM deduplicated
WHERE $5::boolean = false
   OR rank < $6
   OR (rank = $6 AND kind > $7)
   OR (rank = $6 AND kind = $7 AND key > $8)
ORDER BY rank DESC, kind, key
LIMIT $9`
