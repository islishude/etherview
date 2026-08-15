package query

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/enrich"
)

const maxSelectorCandidatesPerTransaction = 32

type transactionSelectorRequest struct {
	Ordinal          int    `json:"ordinal"`
	BlockNumber      string `json:"block_number"`
	BlockHash        string `json:"block_hash"`
	TransactionIndex int64  `json:"transaction_index"`
	Address          string `json:"address"`
	CodeHash         string `json:"code_hash"`
	Selector         string `json:"selector"`
	SelectorScope    string `json:"selector_scope"`
	ExactAddressOnly bool   `json:"exact_address_only"`
}

type transactionSelectorCandidate struct {
	sourceCodeHash common.Hash
	abiEntry       []byte
	signature      string
	priority       int
}

func (r *PostgresReader) projectTransactionMethods(
	ctx context.Context,
	tx *sql.Tx,
	records []transactionRecord,
) error {
	requests := make([]transactionSelectorRequest, 0, len(records))
	requestRecords := make(map[int]int, len(records))
	for index := range records {
		record := &records[index]
		if record.Model.To == nil || strongPublishedMethod(record.method) {
			projectTransactionMethod(
				&record.Model, record.method.executionResolution, record.method.decodedSignature,
			)
			continue
		}
		input, err := hexutil.Decode(record.Model.Input)
		if err != nil || len(input) < 4 {
			projectTransactionMethod(
				&record.Model, record.method.executionResolution, record.method.decodedSignature,
			)
			continue
		}
		address := record.method.executionAddress
		codeHash := record.method.executionCodeHash
		exactAddressOnly := false
		if len(address) != common.AddressLength || len(codeHash) != common.HashLength {
			if record.method.executionResolution.Valid ||
				!record.method.stateDiffComplete || record.Model.To == nil ||
				!common.IsHexAddress(string(*record.Model.To)) {
				projectTransactionMethod(
					&record.Model, record.method.executionResolution, record.method.decodedSignature,
				)
				continue
			}
			address = common.HexToAddress(string(*record.Model.To)).Bytes()
			codeHash = nil
			exactAddressOnly = true
		}
		ordinal := len(requests)
		requests = append(requests, transactionSelectorRequest{
			Ordinal: ordinal, BlockNumber: strconv.FormatUint(record.BlockNumber, 10),
			BlockHash: hex.EncodeToString(record.BlockHash[:]), TransactionIndex: int64(record.Index),
			Address:          hex.EncodeToString(address),
			CodeHash:         hex.EncodeToString(codeHash),
			Selector:         hex.EncodeToString(input[:4]),
			SelectorScope:    hex.EncodeToString(crypto.Keccak256(input[:4])),
			ExactAddressOnly: exactAddressOnly,
		})
		requestRecords[ordinal] = index
	}
	if len(requests) == 0 {
		return nil
	}

	encoded, err := json.Marshal(requests)
	if err != nil {
		return fmt.Errorf("encode transaction selector requests: %w", err)
	}
	rows, err := tx.QueryContext(ctx, transactionSelectorCandidatesSQL, r.chainID, encoded,
		maxSelectorCandidatesPerTransaction+1)
	if err != nil {
		return fmt.Errorf("query verified transaction selector candidates: %w", err)
	}
	candidates := make(map[int][]transactionSelectorCandidate, len(requests))
	overflowPriority := make(map[int]int)
	for rows.Next() {
		var ordinal int
		var source string
		var sourceAddress, sourceCodeHash, abiEntry []byte
		var validFromText string
		var validToText sql.NullString
		var selectorScoped bool
		var signature string
		if err := rows.Scan(
			&ordinal, &source, &sourceAddress, &sourceCodeHash, &abiEntry,
			&validFromText, &validToText, &selectorScoped, &signature,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan verified transaction selector candidate: %w", err)
		}
		if len(candidates[ordinal]) >= maxSelectorCandidatesPerTransaction {
			priority := transactionSelectorSourcePriority(enrich.ABISource(source))
			if current := overflowPriority[ordinal]; current == 0 || priority < current {
				overflowPriority[ordinal] = priority
			}
			continue
		}
		candidate, ok := parseTransactionSelectorCandidate(
			source, sourceAddress, sourceCodeHash, abiEntry, validFromText, validToText, selectorScoped,
			signature,
		)
		if ok {
			candidates[ordinal] = append(candidates[ordinal], candidate)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate verified transaction selector candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close verified transaction selector candidates: %w", err)
	}

	for ordinal, recordIndex := range requestRecords {
		record := &records[recordIndex]
		indexedSignature, ambiguous := decodeTransactionSelector(
			record, candidates[ordinal], overflowPriority[ordinal],
		)
		if indexedSignature.Valid {
			projectTransactionMethod(&record.Model, record.method.executionResolution, indexedSignature)
		} else if ambiguous {
			projectTransactionMethod(&record.Model, record.method.executionResolution, sql.NullString{})
		} else {
			projectTransactionMethod(
				&record.Model, record.method.executionResolution, record.method.decodedSignature,
			)
		}
	}
	return nil
}

func strongPublishedMethod(method transactionMethodContext) bool {
	if !method.decodedSignature.Valid || !method.decodedConfidence.Valid || !method.decodedSource.Valid {
		return false
	}
	return validTransactionMethodSignature(method.decodedSignature.String) &&
		(method.decodedConfidence.String == string(enrich.ConfidenceVerified) ||
			method.decodedConfidence.String == string(enrich.ConfidenceHigh))
}

func parseTransactionSelectorCandidate(
	source string,
	addressBytes, codeHashBytes, abiEntry []byte,
	validFromText string,
	validToText sql.NullString,
	_ bool,
	signature string,
) (transactionSelectorCandidate, bool) {
	if len(addressBytes) != common.AddressLength || len(codeHashBytes) != common.HashLength {
		return transactionSelectorCandidate{}, false
	}
	if _, err := strconv.ParseUint(validFromText, 10, 64); err != nil {
		return transactionSelectorCandidate{}, false
	}
	if validToText.Valid {
		if _, err := strconv.ParseUint(validToText.String, 10, 64); err != nil {
			return transactionSelectorCandidate{}, false
		}
	}
	parsedSource := enrich.ABISource(source)
	switch parsedSource {
	case enrich.ABISourceVerified, enrich.ABISourceCodeHash,
		enrich.ABISourceProxyImplementation, enrich.ABISourceDiamondFacet:
	default:
		return transactionSelectorCandidate{}, false
	}
	return transactionSelectorCandidate{
		sourceCodeHash: common.BytesToHash(codeHashBytes),
		abiEntry:       append([]byte(nil), abiEntry...),
		signature:      signature,
		priority:       transactionSelectorSourcePriority(parsedSource),
	}, true
}

func transactionSelectorSourcePriority(source enrich.ABISource) int {
	switch source {
	case enrich.ABISourceVerified:
		return 1
	case enrich.ABISourceCodeHash:
		return 2
	case enrich.ABISourceProxyImplementation, enrich.ABISourceDiamondFacet:
		return 3
	default:
		return 4
	}
}

func decodeTransactionSelector(
	record *transactionRecord,
	candidates []transactionSelectorCandidate,
	overflowPriority int,
) (sql.NullString, bool) {
	if record == nil || len(candidates) == 0 {
		return sql.NullString{}, overflowPriority > 0
	}
	input, err := hexutil.Decode(record.Model.Input)
	if err != nil || len(input) < 4 {
		return sql.NullString{}, false
	}
	for priority := 1; priority <= 3; priority++ {
		matches := make(map[string]string)
		for _, candidate := range candidates {
			if candidate.priority != priority {
				continue
			}
			signature, ok := enrich.DecodeVerifiedFunctionCalldata(candidate.abiEntry, input)
			if ok && signature == candidate.signature && validTransactionMethodSignature(signature) {
				matches[signature+"\x00"+candidate.sourceCodeHash.Hex()] = signature
			}
		}
		if overflowPriority == priority {
			return sql.NullString{}, true
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 {
			return sql.NullString{}, true
		}
		for _, signature := range matches {
			return sql.NullString{String: signature, Valid: true}, false
		}
	}
	return sql.NullString{}, false
}

// The input page is bounded to 101 rows. Candidate sets are joined by exact
// execution identity or a completed state-diff plus exact verified address
// range, and selector; no complete verified ABI document is read.
// Existing canonical route bindings preserve proxy/Diamond provenance, while
// published proxy observations make late implementation verification visible
// without replaying abi@3.
const transactionSelectorCandidatesSQL = `
WITH request AS (
    SELECT ordinal, block_number, block_hash, transaction_index,
           address, code_hash, selector, selector_scope, exact_address_only
    FROM jsonb_to_recordset($2::jsonb) AS input(
        ordinal integer,
        block_number text,
        block_hash text,
        transaction_index bigint,
        address text,
        code_hash text,
        selector text,
        selector_scope text,
        exact_address_only boolean
    )
), direct_candidates AS (
    SELECT request.ordinal,
           CASE WHEN indexed.address = decode(request.address, 'hex')
                  AND indexed.valid_from_block <= request.block_number::numeric
                  AND (verified.valid_to_block IS NULL OR
                       verified.valid_to_block >= request.block_number::numeric)
                THEN 'verified' ELSE 'code_hash' END AS source,
           indexed.address AS source_address,
           indexed.code_hash AS source_code_hash,
           selector.abi_entry,
           CASE WHEN indexed.address = decode(request.address, 'hex')
                  AND indexed.valid_from_block <= request.block_number::numeric
                  AND (verified.valid_to_block IS NULL OR
                       verified.valid_to_block >= request.block_number::numeric)
                THEN indexed.valid_from_block ELSE 0 END AS valid_from_block,
           CASE WHEN indexed.address = decode(request.address, 'hex')
                  AND indexed.valid_from_block <= request.block_number::numeric
                  AND (verified.valid_to_block IS NULL OR
                       verified.valid_to_block >= request.block_number::numeric)
                THEN verified.valid_to_block ELSE NULL END AS valid_to_block,
           FALSE AS selector_scoped,
           selector.signature
    FROM request
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = $1::numeric
     AND ((NOT request.exact_address_only AND
           indexed.code_hash = decode(request.code_hash, 'hex')) OR
          (request.exact_address_only AND
           indexed.address = decode(request.address, 'hex')))
     AND indexed.status = 'complete'
    JOIN verified_contracts AS verified
      ON verified.chain_id = indexed.chain_id
     AND verified.address = indexed.address
     AND verified.code_hash = indexed.code_hash
     AND verified.valid_from_block = indexed.valid_from_block
     AND verified.verification_job_id = indexed.verification_job_id
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
    WHERE NOT request.exact_address_only OR
          (indexed.valid_from_block <= request.block_number::numeric AND
           (verified.valid_to_block IS NULL OR
            verified.valid_to_block >= request.block_number::numeric))
), bound_routes AS (
    SELECT request.ordinal, binding.source,
           binding.source_address, binding.source_code_hash,
           selector.abi_entry,
           request.block_number::numeric AS valid_from_block,
           request.block_number::numeric AS valid_to_block,
           binding.source = 'diamond_facet' AS selector_scoped,
           selector.signature
    FROM request
    JOIN contract_abis AS binding
      ON NOT request.exact_address_only
     AND binding.chain_id = $1::numeric
     AND binding.address = decode(request.address, 'hex')
     AND binding.code_hash = decode(request.code_hash, 'hex')
     AND binding.canonical
     AND binding.source IN ('proxy_implementation', 'diamond_facet')
     AND binding.valid_from_block <= request.block_number::numeric
     AND (binding.valid_to_block IS NULL OR binding.valid_to_block >= request.block_number::numeric)
     AND (binding.source <> 'diamond_facet' OR
          binding.selector_scope = decode(request.selector_scope, 'hex'))
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = binding.chain_id
     AND indexed.address = binding.source_address
     AND indexed.code_hash = binding.source_code_hash
     AND indexed.status = 'complete'
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
), published_proxy_routes AS (
    SELECT request.ordinal, 'proxy_implementation'::text AS source,
           route.implementation_address AS source_address,
           route.implementation_code_hash AS source_code_hash,
           selector.abi_entry,
           request.block_number::numeric AS valid_from_block,
           request.block_number::numeric AS valid_to_block,
           FALSE AS selector_scoped,
           selector.signature
    FROM request
    JOIN LATERAL (
        SELECT observation.implementation_address,
               observation.implementation_code_hash
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        JOIN proxy_observation_generations AS generation
          ON generation.chain_id = observation.chain_id
         AND generation.proxy_address = observation.proxy_address
         AND generation.observation_block_hash = observation.block_hash
         AND generation.observation_stage_version = observation.stage_version
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = decode(request.address, 'hex')
          AND observation.proxy_code_hash = decode(request.code_hash, 'hex')
          AND observation.stage_version = 2
          AND observation.canonical
          AND observation.confidence IN ('verified', 'high')
          AND observation.implementation_address IS NOT NULL
          AND observation.implementation_code_hash IS NOT NULL
          AND observation.block_number <= request.block_number::numeric
        ORDER BY observation.block_number DESC, generation.id DESC
        LIMIT 1
    ) AS route ON NOT request.exact_address_only
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = $1::numeric
     AND indexed.address = route.implementation_address
     AND indexed.code_hash = route.implementation_code_hash
     AND indexed.status = 'complete'
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
), published_diamond_routes AS (
    SELECT request.ordinal, 'diamond_facet'::text AS source,
           route.facet_address AS source_address,
           route.facet_code_hash AS source_code_hash,
           selector.abi_entry,
           request.block_number::numeric AS valid_from_block,
           request.block_number::numeric AS valid_to_block,
           TRUE AS selector_scoped,
           selector.signature
    FROM request
    JOIN LATERAL (
        SELECT active.facet_address, facet.code_hash AS facet_code_hash
        FROM canonical_diamond_selector_intervals AS active
        JOIN LATERAL (
            SELECT candidate.code_hash
            FROM published_diamond_loupe_snapshots AS snapshot
            JOIN diamond_loupe_facets AS candidate
              ON candidate.snapshot_id = snapshot.id
            WHERE snapshot.chain_id = active.chain_id
              AND snapshot.diamond_address = active.diamond_address
              AND snapshot.block_number <= request.block_number::numeric
              AND snapshot.detection_state = 'confirmed'
              AND snapshot.canonical
              AND candidate.facet_address = active.facet_address
              AND candidate.facet_kind = 'facet'
              AND candidate.code_exists
              AND candidate.code_hash IS NOT NULL
            ORDER BY snapshot.block_number DESC, snapshot.id DESC
            LIMIT 1
        ) AS facet ON TRUE
        WHERE active.chain_id = $1::numeric
          AND active.diamond_address = decode(request.address, 'hex')
          AND active.selector = decode(request.selector, 'hex')
          AND (
              active.valid_from_block_number < request.block_number::numeric OR
              (active.valid_from_block_number = request.block_number::numeric AND
               active.valid_from_transaction_index < request.transaction_index)
          )
          AND (
              active.valid_to_block_number IS NULL OR
              active.valid_to_block_number > request.block_number::numeric OR
              (active.valid_to_block_number = request.block_number::numeric AND
               active.valid_to_transaction_index >= request.transaction_index)
          )
        ORDER BY active.valid_from_block_number DESC,
                 active.valid_from_transaction_index DESC,
                 active.valid_from_log_index DESC,
                 active.valid_from_cut_index DESC,
                 active.valid_from_selector_index DESC
        LIMIT 1
    ) AS route ON NOT request.exact_address_only
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = $1::numeric
     AND indexed.address = route.facet_address
     AND indexed.code_hash = route.facet_code_hash
     AND indexed.status = 'complete'
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
), combined AS (
    SELECT * FROM direct_candidates
    UNION
    SELECT * FROM bound_routes
    UNION
    SELECT * FROM published_proxy_routes
    UNION
    SELECT * FROM published_diamond_routes
), ranked AS (
    SELECT combined.*,
           row_number() OVER (
               PARTITION BY combined.ordinal
               ORDER BY CASE combined.source
                            WHEN 'verified' THEN 1
                            WHEN 'code_hash' THEN 2
                            ELSE 3
                        END,
                        combined.signature, combined.source_address,
                        combined.source_code_hash
           ) AS candidate_number
    FROM combined
)
SELECT ordinal, source, source_address, source_code_hash, abi_entry,
       valid_from_block::text, valid_to_block::text, selector_scoped, signature
FROM ranked
WHERE candidate_number <= $3
ORDER BY ordinal, candidate_number`
