package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/enrich"
)

const maxReadTimeLogABICandidates = 16

type persistedLogDecoding struct {
	status         sql.NullString
	signature      sql.NullString
	source         sql.NullString
	confidence     sql.NullString
	arguments      []byte
	candidates     []byte
	warning        sql.NullString
	targetAddress  []byte
	targetCodeHash []byte
	sourceAddress  []byte
	sourceCodeHash []byte
}

type logABICandidate struct {
	abi        []byte
	source     enrich.ABISource
	sourceKind string
	address    common.Address
	codeHash   common.Hash
	validFrom  uint64
	validTo    sql.NullString
}

func resolveTransactionLogDecoding(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	topics []common.Hash,
	data []byte,
	persisted persistedLogDecoding,
) (TransactionLogDecoding, error) {
	stored, hasStored, err := publicPersistedLogDecoding(persisted)
	if err != nil {
		return TransactionLogDecoding{}, err
	}
	if hasStored && !bytes.Equal(persisted.targetAddress, address[:]) {
		hasStored = false
	}
	if hasStored && (stored.Confidence == "verified" || stored.Confidence == "high") {
		return stored, nil
	}

	identity, candidates, err := loadLogABICandidates(ctx, tx, chainID, blockNumber, blockHash, address)
	if err != nil {
		return TransactionLogDecoding{}, err
	}
	if len(candidates) == 0 {
		if hasStored {
			return stored, nil
		}
		return emptyLogDecoding("unavailable", "no ABI is available for the log address at this block"), nil
	}
	if len(candidates) > maxReadTimeLogABICandidates {
		return emptyLogDecoding("malformed", "ABI candidate limit exceeded"), nil
	}

	registry := enrich.NewABIRegistry()
	provenance := make(map[enrich.ABISource]ABISource, len(candidates))
	for _, candidate := range candidates {
		var validTo *uint64
		if candidate.validTo.Valid {
			value, err := strconv.ParseUint(candidate.validTo.String, 10, 64)
			if err != nil {
				return TransactionLogDecoding{}, ErrCorruptData
			}
			validTo = &value
		}
		binding := enrich.ABIBinding{
			Identity: identity, Source: candidate.source,
			SourceAddress: candidate.address, SourceCodeHash: candidate.codeHash,
			ValidFromBlock: candidate.validFrom, ValidToBlock: validTo,
		}
		if err := registry.RegisterJSON(binding, candidate.abi); err != nil {
			return emptyLogDecoding("malformed", "stored ABI document is invalid or exceeds decoding limits"), nil
		}
		provenance[candidate.source] = ABISource{
			Kind: candidate.sourceKind, Address: candidate.address.Hex(), CodeHash: candidate.codeHash.Hex(),
		}
	}
	result := registry.DecodeLog(identity, topics, data)
	if result.Status == enrich.DecodeUnknown && hasStored {
		return stored, nil
	}
	return publicDecodeResult(result, provenance), nil
}

func loadLogABICandidates(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
) (enrich.ABIIdentity, []logABICandidate, error) {
	identity := enrich.ABIIdentity{
		ChainID: chainID, Address: address, BlockNumber: blockNumber,
		BlockHash: common.BytesToHash(blockHash),
	}
	rows, err := tx.QueryContext(ctx, transactionLogABICandidatesSQL,
		chainID, address[:], fmt.Sprint(blockNumber), maxReadTimeLogABICandidates+1,
	)
	if err != nil {
		return identity, nil, fmt.Errorf("load log ABI candidates: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var candidates []logABICandidate
	for rows.Next() {
		var candidate logABICandidate
		var source, sourceKind string
		var sourceAddress, sourceCodeHash, targetCodeHash []byte
		var validFrom string
		if err := rows.Scan(
			&targetCodeHash, &candidate.abi, &source, &sourceKind,
			&sourceAddress, &sourceCodeHash, &validFrom, &candidate.validTo,
		); err != nil {
			return identity, nil, fmt.Errorf("scan log ABI candidate: %w", err)
		}
		if len(targetCodeHash) != common.HashLength || len(sourceAddress) != common.AddressLength ||
			len(sourceCodeHash) != common.HashLength {
			return identity, nil, ErrCorruptData
		}
		parsedValidFrom, parseErr := strconv.ParseUint(validFrom, 10, 64)
		if parseErr != nil {
			return identity, nil, ErrCorruptData
		}
		identity.CodeHash = common.BytesToHash(targetCodeHash)
		candidate.source = enrich.ABISource(source)
		candidate.sourceKind = sourceKind
		candidate.address = common.BytesToAddress(sourceAddress)
		candidate.codeHash = common.BytesToHash(sourceCodeHash)
		candidate.validFrom = parsedValidFrom
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return identity, nil, fmt.Errorf("iterate log ABI candidates: %w", err)
	}
	return identity, candidates, nil
}

func publicPersistedLogDecoding(value persistedLogDecoding) (TransactionLogDecoding, bool, error) {
	if !value.status.Valid {
		return TransactionLogDecoding{}, false, nil
	}
	result := emptyLogDecoding(value.status.String, value.warning.String)
	result.Signature = value.signature.String
	result.Confidence = value.confidence.String
	if result.Signature != "" {
		if open := len(result.Signature) - 1; open > 0 {
			for open >= 0 && result.Signature[open] != '(' {
				open--
			}
			if open > 0 {
				result.EventName = result.Signature[:open]
			}
		}
	}
	if len(value.arguments) > 0 && !json.Valid(value.arguments) {
		return TransactionLogDecoding{}, false, ErrCorruptData
	}
	if len(value.candidates) > 0 && !json.Valid(value.candidates) {
		return TransactionLogDecoding{}, false, ErrCorruptData
	}
	if len(value.arguments) > 0 {
		if err := json.Unmarshal(value.arguments, &result.Arguments); err != nil {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
	}
	if len(value.candidates) > 0 {
		if err := json.Unmarshal(value.candidates, &result.Candidates); err != nil {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
	}
	if value.source.Valid {
		kind := mapStoredABISource(value.source.String)
		if kind == "" {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
		result.ABISource = &ABISource{Kind: kind}
		sourceAddress, sourceCodeHash := value.sourceAddress, value.sourceCodeHash
		if len(sourceAddress) == 0 && kind != "proxy_implementation" {
			sourceAddress, sourceCodeHash = value.targetAddress, value.targetCodeHash
		}
		if len(sourceAddress) != common.AddressLength || len(sourceCodeHash) != common.HashLength {
			return TransactionLogDecoding{}, false, ErrCorruptData
		}
		result.ABISource.Address = common.BytesToAddress(sourceAddress).Hex()
		result.ABISource.CodeHash = common.BytesToHash(sourceCodeHash).Hex()
	}
	if result.Status == "ambiguous" {
		result.EventName, result.Signature = "", ""
		result.Arguments = []TransactionLogArgument{}
		result.ABISource = nil
	}
	return result, true, nil
}

func publicDecodeResult(
	result enrich.DecodeResult,
	provenance map[enrich.ABISource]ABISource,
) TransactionLogDecoding {
	decoded := emptyLogDecoding(string(result.Status), result.Warning)
	decoded.EventName = result.Name
	decoded.Signature = result.Signature
	decoded.Confidence = string(result.Confidence)
	decoded.Candidates = append(decoded.Candidates, result.Candidates...)
	decoded.Arguments = make([]TransactionLogArgument, len(result.Arguments))
	for index, argument := range result.Arguments {
		decoded.Arguments[index] = TransactionLogArgument{
			Name: argument.Name, Type: argument.Type, Indexed: argument.Indexed,
			Hashed: argument.Hashed, Value: argument.Value,
		}
	}
	if source, ok := provenance[result.Source]; ok {
		copy := source
		decoded.ABISource = &copy
	}
	if result.SourceAddress != (common.Address{}) && result.SourceCodeHash != (common.Hash{}) {
		decoded.ABISource = &ABISource{
			Kind: mapDecodeABISource(result.Source), Address: result.SourceAddress.Hex(), CodeHash: result.SourceCodeHash.Hex(),
		}
	}
	if result.Status == enrich.DecodeAmbiguous {
		decoded.EventName, decoded.Signature = "", ""
		decoded.Arguments = []TransactionLogArgument{}
		decoded.ABISource = nil
	}
	return decoded
}

func mapDecodeABISource(source enrich.ABISource) string {
	switch source {
	case enrich.ABISourceVerified:
		return "exact_address"
	case enrich.ABISourceCodeHash:
		return "code_hash"
	case enrich.ABISourceProxyImplementation:
		return "proxy_implementation"
	case enrich.ABISourceSignatureDatabase:
		return "signature_database"
	default:
		return ""
	}
}

func emptyLogDecoding(status, warning string) TransactionLogDecoding {
	return TransactionLogDecoding{
		Status: status, Arguments: []TransactionLogArgument{}, Candidates: []string{}, Warning: warning,
	}
}

func mapStoredABISource(source string) string {
	switch source {
	case "verified":
		return "exact_address"
	case "proxy_implementation":
		return "proxy_implementation"
	case "code_hash":
		return "code_hash"
	case "builtin":
		return "builtin"
	case "signature_database":
		return "signature_database"
	default:
		return ""
	}
}

const transactionLogABICandidatesSQL = `
WITH target_code AS (
    SELECT observation.code_hash
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND observation.block_number <= $3::numeric
      AND observation.canonical
    ORDER BY observation.block_number DESC, observation.observed_at DESC
    LIMIT 1
), historical_proxy AS (
    SELECT observation.implementation_address, observation.implementation_code_hash
    FROM proxy_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    CROSS JOIN target_code
    WHERE observation.chain_id = $1::numeric
      AND observation.proxy_address = $2
      AND observation.proxy_code_hash = target_code.code_hash
      AND observation.block_number <= $3::numeric
      AND observation.canonical
      AND observation.confidence IN ('verified', 'high')
      AND observation.implementation_address IS NOT NULL
      AND observation.implementation_code_hash IS NOT NULL
    ORDER BY observation.block_number DESC, observation.stage_version DESC,
             observation.block_hash DESC
    LIMIT 1
), candidates AS (
    SELECT target_code.code_hash AS target_code_hash, binding.abi,
           CASE binding.source
             WHEN 'verified' THEN 'verified'
             WHEN 'proxy_implementation' THEN 'proxy_implementation'
             ELSE 'signature_database'
           END AS registry_source,
           CASE binding.source
             WHEN 'verified' THEN 'exact_address'
             WHEN 'proxy_implementation' THEN 'proxy_implementation'
             ELSE 'signature_database'
           END AS source_kind,
           binding.source_address, binding.source_code_hash,
           binding.valid_from_block, binding.valid_to_block,
           CASE binding.source WHEN 'verified' THEN 0 WHEN 'proxy_implementation' THEN 2 ELSE 4 END AS priority,
           binding.created_at, NULL::bytea AS request_digest, NULL::uuid AS job_id
    FROM contract_abis AS binding, target_code
    WHERE binding.chain_id = $1::numeric
      AND binding.address = $2
      AND binding.code_hash = target_code.code_hash
      AND binding.valid_from_block <= $3::numeric
      AND (binding.valid_to_block IS NULL OR binding.valid_to_block >= $3::numeric)
      AND binding.canonical
    UNION ALL
    SELECT target_code.code_hash, verified.abi,
           CASE WHEN verified.address = $2 THEN 'verified' ELSE 'code_hash' END,
           CASE WHEN verified.address = $2 THEN 'exact_address' ELSE 'code_hash' END,
           verified.address, verified.code_hash,
           0::numeric, NULL::numeric,
           CASE WHEN verified.address = $2 THEN 1 ELSE 3 END,
           verified.created_at, verified.request_digest, verified.verification_job_id
    FROM verified_contracts AS verified, target_code
    WHERE verified.chain_id = $1::numeric
      AND verified.code_hash = target_code.code_hash
      AND verified.abi IS NOT NULL
      AND (verified.address <> $2 OR (
        verified.valid_from_block <= $3::numeric AND
        (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
      ))
    UNION ALL
    SELECT target_code.code_hash, verified.abi, 'proxy_implementation',
           'proxy_implementation', verified.address, verified.code_hash,
           0::numeric, NULL::numeric, 2,
           verified.created_at, verified.request_digest, verified.verification_job_id
    FROM verified_contracts AS verified, target_code, historical_proxy AS proxy
    WHERE verified.chain_id = $1::numeric
      AND verified.address = proxy.implementation_address
      AND verified.code_hash = proxy.implementation_code_hash
      AND verified.abi IS NOT NULL
)
SELECT target_code_hash, abi, registry_source, source_kind,
       source_address, source_code_hash,
       valid_from_block::text, valid_to_block::text
FROM candidates
ORDER BY priority, created_at DESC, request_digest ASC NULLS FIRST,
         job_id ASC NULLS FIRST, source_address, source_code_hash
LIMIT $4`
