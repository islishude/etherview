package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

// AddressOrigin resolves the first genesis- or transaction-backed origin for an
// account at one already-observed canonical state reference. Genesis
// allocations are authenticated independently by the canonical block-zero
// import; transaction-backed candidates still require genesis-through-
// reference Core and Trace proof before being called "first".
func (r *PostgresReader) AddressOrigin(
	ctx context.Context,
	rawAddress string,
	accountType gen.AddressSummaryType,
	referenceNumber uint64,
	referenceHash common.Hash,
) (gen.AddressOrigin, error) {
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("invalid origin address: %w", err)
	}
	kind := gen.Funding
	query := firstFundingOriginSQL
	if accountType == gen.AddressSummaryTypeContract {
		kind = gen.ContractCreation
		query = firstContractOriginSQL
	}
	result := gen.AddressOrigin{Kind: kind, State: gen.AddressOriginStateUnavailable}
	if accountType != gen.AddressSummaryTypeContract &&
		accountType != gen.AddressSummaryTypeEoa &&
		accountType != gen.AddressSummaryTypeDelegatedEoa {
		return result, nil
	}
	if r.startBlock != 0 {
		return result, nil
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("begin address origin snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var canonical bool
	if err := tx.QueryRowContext(ctx, addressOriginReferenceSQL,
		r.chainID, fmt.Sprint(referenceNumber), referenceHash.Bytes(),
	).Scan(&canonical); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("validate address origin reference: %w", err)
	}
	if !canonical {
		return gen.AddressOrigin{}, fmt.Errorf("%w: address state reference is no longer canonical", httpapi.ErrNotReady)
	}

	var genesis bool
	if err := tx.QueryRowContext(ctx, genesisAddressOriginSQL,
		r.chainID, address.Bytes(),
	).Scan(&genesis); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("check genesis address origin: %w", err)
	}
	if genesis {
		result.State = gen.AddressOriginStateGenesis
		if err := tx.Commit(); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit genesis address origin snapshot: %w", err)
		}
		return result, nil
	}

	coverageEnd := referenceNumber
	var candidateBlock string
	var sourceBytes, transactionHashBytes []byte
	err = tx.QueryRowContext(ctx, query,
		r.chainID, fmt.Sprint(referenceNumber), address.Bytes(),
	).Scan(&candidateBlock, &sourceBytes, &transactionHashBytes)
	notFound := errors.Is(err, sql.ErrNoRows)
	if !notFound && err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("query address origin: %w", err)
	}
	if !notFound {
		coverageEnd, err = strconv.ParseUint(candidateBlock, 10, 64)
		if err != nil || strconv.FormatUint(coverageEnd, 10) != candidateBlock ||
			coverageEnd > referenceNumber {
			return gen.AddressOrigin{}, errors.New("stored address origin block is malformed")
		}
	}

	var complete bool
	if err := tx.QueryRowContext(ctx, addressOriginCoverageSQL,
		r.chainID, fmt.Sprint(coverageEnd),
	).Scan(&complete); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("check address origin coverage: %w", err)
	}
	if !complete {
		if err := tx.Commit(); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit unavailable address origin snapshot: %w", err)
		}
		return result, nil
	}
	if notFound {
		result.State = gen.AddressOriginStateNotFound
		if err := tx.Commit(); err != nil {
			return gen.AddressOrigin{}, fmt.Errorf("commit empty address origin snapshot: %w", err)
		}
		return result, nil
	}
	if len(sourceBytes) != common.AddressLength || len(transactionHashBytes) != common.HashLength {
		return gen.AddressOrigin{}, errors.New("stored address origin identity is malformed")
	}
	source := common.BytesToAddress(sourceBytes).Hex()
	transactionHash := common.BytesToHash(transactionHashBytes).Hex()
	result.State = gen.AddressOriginStateFound
	result.SourceAddress = &source
	result.TransactionHash = &transactionHash
	if err := tx.Commit(); err != nil {
		return gen.AddressOrigin{}, fmt.Errorf("commit address origin snapshot: %w", err)
	}
	return result, nil
}

const addressOriginReferenceSQL = `
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
      AND number = $2::numeric
      AND block_hash = $3
)`

const addressOriginCoverageSQL = `
WITH core_complete AS (
    SELECT EXISTS (
        SELECT 1
        FROM core_index_configuration AS configuration
        JOIN core_coverage_ranges AS coverage
          ON coverage.chain_id = configuration.chain_id
         AND coverage.range_start = 0
         AND coverage.range_end >= $2::numeric
        WHERE configuration.chain_id = $1::numeric
          AND configuration.configured_start = 0
    ) AS complete
), trace_complete AS (
    SELECT NOT EXISTS (
        SELECT 1
        FROM canonical_blocks AS canonical
        LEFT JOIN LATERAL (
            SELECT result.state
            FROM published_block_stage_results AS result
            WHERE result.chain_id = canonical.chain_id
              AND result.block_number = canonical.number
              AND result.block_hash = canonical.block_hash
              AND result.stage = 'trace'
              AND result.stage_version = 3
            LIMIT 1
        ) AS latest ON TRUE
        WHERE canonical.chain_id = $1::numeric
          AND canonical.number <= $2::numeric
          AND latest.state IS DISTINCT FROM 'complete'
    ) AS complete
)
SELECT core_complete.complete AND trace_complete.complete
FROM core_complete CROSS JOIN trace_complete`

const genesisAddressOriginSQL = `
SELECT EXISTS (
    SELECT 1
    FROM genesis_account_observations AS observation
    JOIN genesis_state_imports AS imported
      ON imported.chain_id = observation.chain_id
     AND imported.block_hash = observation.block_hash
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = 0
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND imported.state = 'complete'
)`

const firstContractOriginSQL = `
WITH candidates AS (
    SELECT receipt.block_number, receipt.tx_index,
           ARRAY[]::bigint[] AS trace_order, 0 AS source_rank,
           decode(substr(inclusion.raw->>'from', 3), 'hex') AS source_address,
           receipt.tx_hash AS transaction_hash
    FROM receipts AS receipt
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = receipt.chain_id
     AND canonical.number = receipt.block_number
     AND canonical.block_hash = receipt.block_hash
    JOIN transaction_inclusions AS inclusion
      ON inclusion.chain_id = receipt.chain_id
     AND inclusion.block_number = receipt.block_number
     AND inclusion.block_hash = receipt.block_hash
     AND inclusion.tx_index = receipt.tx_index
     AND inclusion.tx_hash = receipt.tx_hash
    WHERE receipt.chain_id = $1::numeric
      AND receipt.block_number <= $2::numeric
      AND lower(receipt.raw->>'contractAddress') =
          lower('0x' || encode($3, 'hex'))
      AND receipt.raw->>'status' = '0x1'

    UNION ALL

    SELECT trace.block_number, trace.transaction_index,
           string_to_array(trace.trace_path, '.')::bigint[] AS trace_order,
           1 AS source_rank, trace.from_address, trace.transaction_hash
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    WHERE trace.chain_id = $1::numeric
      AND trace.block_number <= $2::numeric
      AND trace.created_address = $3
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
      AND trace.call_type IN ('CREATE', 'CREATE2')
      AND trace.from_address IS NOT NULL
)
SELECT block_number::text, source_address, transaction_hash
FROM candidates
ORDER BY block_number, tx_index, source_rank, trace_order
LIMIT 1`

const firstFundingOriginSQL = `
WITH candidates AS (
    SELECT inclusion.block_number, inclusion.tx_index,
           ARRAY[]::bigint[] AS trace_order, 0 AS source_rank,
           decode(substr(inclusion.raw->>'from', 3), 'hex') AS source_address,
           inclusion.tx_hash AS transaction_hash
    FROM transaction_inclusions AS inclusion
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = inclusion.chain_id
     AND canonical.number = inclusion.block_number
     AND canonical.block_hash = inclusion.block_hash
    JOIN receipts AS receipt
      ON receipt.chain_id = inclusion.chain_id
     AND receipt.block_number = inclusion.block_number
     AND receipt.block_hash = inclusion.block_hash
     AND receipt.tx_index = inclusion.tx_index
     AND receipt.tx_hash = inclusion.tx_hash
    WHERE inclusion.chain_id = $1::numeric
      AND inclusion.block_number <= $2::numeric
      AND lower(inclusion.raw->>'to') = lower('0x' || encode($3, 'hex'))
      AND inclusion.raw->>'value' <> '0x0'
      AND receipt.raw->>'status' = '0x1'

    UNION ALL

    SELECT trace.block_number, trace.transaction_index,
           string_to_array(trace.trace_path, '.')::bigint[] AS trace_order,
           1 AS source_rank, trace.from_address, trace.transaction_hash
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    WHERE trace.chain_id = $1::numeric
      AND trace.block_number <= $2::numeric
      AND trace.to_address = $3
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
      AND trace.value > 0
      AND trace.from_address IS NOT NULL
)
SELECT block_number::text, source_address, transaction_hash
FROM candidates
ORDER BY block_number, tx_index, source_rank, trace_order
LIMIT 1`
