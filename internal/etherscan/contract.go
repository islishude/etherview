package etherscan

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/ethrpc"
)

type verifiedContractRecord struct {
	CodeHash        []byte
	ABI             []byte
	Sources         []byte
	Settings        []byte
	Language        string
	CompilerVersion string
	MatchKind       string
	ContractName    string
}

func (b *PostgresBackend) verifiedContract(ctx context.Context, values url.Values) (verifiedContractRecord, error) {
	_, addressBytes, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return verifiedContractRecord{}, err
	}
	var record verifiedContractRecord
	var matchedCodeHash []byte
	var language, compilerVersion, matchKind, contractName sql.NullString
	err = b.db.QueryRowContext(ctx, verifiedContractSQL, b.chain, addressBytes).Scan(
		&record.CodeHash, &matchedCodeHash, &record.ABI, &record.Sources, &record.Settings,
		&language, &compilerVersion, &matchKind, &contractName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return verifiedContractRecord{}, ErrStateUnavailable
	}
	if err != nil {
		return verifiedContractRecord{}, fmt.Errorf("query verified contract: %w", err)
	}
	if len(record.CodeHash) != 32 {
		return verifiedContractRecord{}, errors.New("stored canonical contract code hash is invalid")
	}
	if len(matchedCodeHash) == 0 {
		return verifiedContractRecord{}, ErrContractUnverified
	}
	if len(matchedCodeHash) != 32 || !bytes.Equal(record.CodeHash, matchedCodeHash) {
		return verifiedContractRecord{}, errors.New("stored verified contract code hash does not match canonical code")
	}
	if !language.Valid || !compilerVersion.Valid || !matchKind.Valid || !contractName.Valid {
		return verifiedContractRecord{}, errors.New("stored verified contract identity is incomplete")
	}
	record.Language = language.String
	record.CompilerVersion = compilerVersion.String
	record.MatchKind = matchKind.String
	record.ContractName = contractName.String
	if record.Language != "solidity" && record.Language != "yul" {
		return verifiedContractRecord{}, fmt.Errorf("stored verified contract has unsupported language %q", record.Language)
	}
	if record.MatchKind != "full" && record.MatchKind != "partial" {
		return verifiedContractRecord{}, fmt.Errorf("stored verified contract has unsupported match kind %q", record.MatchKind)
	}
	if strings.TrimSpace(record.CompilerVersion) == "" || strings.TrimSpace(record.ContractName) == "" {
		return verifiedContractRecord{}, errors.New("stored verified contract identity is incomplete")
	}
	if _, err := compactJSON(record.Sources); err != nil {
		return verifiedContractRecord{}, fmt.Errorf("decode verified contract sources: %w", err)
	}
	if _, err := compactJSON(record.Settings); err != nil {
		return verifiedContractRecord{}, fmt.Errorf("decode verified contract settings: %w", err)
	}
	return record, nil
}

func (b *PostgresBackend) contractABI(ctx context.Context, values url.Values) (string, error) {
	record, err := b.verifiedContract(ctx, values)
	if err != nil {
		return "", err
	}
	if len(record.ABI) == 0 || string(record.ABI) == "null" {
		return "", ErrContractUnverified
	}
	abi, err := compactJSON(record.ABI)
	if err != nil {
		return "", fmt.Errorf("decode verified contract ABI: %w", err)
	}
	return abi, nil
}

func (b *PostgresBackend) contractSource(ctx context.Context, values url.Values) ([]sourceCodeResult, error) {
	record, err := b.verifiedContract(ctx, values)
	if err != nil {
		return nil, err
	}
	sources, err := compactJSON(record.Sources)
	if err != nil {
		return nil, fmt.Errorf("decode verified contract sources: %w", err)
	}
	abi := ""
	if len(record.ABI) != 0 && string(record.ABI) != "null" {
		abi, err = compactJSON(record.ABI)
		if err != nil {
			return nil, fmt.Errorf("decode verified contract ABI: %w", err)
		}
	}
	settings, err := sourceSettings(record.Settings)
	if err != nil {
		return nil, err
	}
	proxy, implementation, err := b.currentVerifiedProxy(ctx, values.Get("address"), record.CodeHash)
	if err != nil {
		return nil, err
	}
	return []sourceCodeResult{{
		SourceCode: sources, ABI: abi, ContractName: record.ContractName,
		CompilerVersion: record.CompilerVersion, CompilerType: "solc",
		OptimizationUsed: settings.optimized,
		Runs:             settings.runs, ConstructorArguments: settings.constructorArguments,
		EVMVersion: settings.evmVersion, Library: settings.libraries,
		ContractFileName: "", LicenseType: settings.licenseType,
		Proxy: proxy, Implementation: implementation,
		SwarmSource: "", SimilarMatch: "", MatchKind: record.MatchKind,
	}}, nil
}

func (b *PostgresBackend) currentVerifiedProxy(
	ctx context.Context,
	rawAddress string,
	codeHash []byte,
) (string, string, error) {
	_, address, err := parseAddressParameter(rawAddress, "address")
	if err != nil {
		return "", "", err
	}
	var implementation []byte
	err = b.db.QueryRowContext(ctx, verifiedProxySQL, b.chain, address, codeHash).Scan(&implementation)
	if errors.Is(err, sql.ErrNoRows) {
		return "0", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("query verified proxy binding: %w", err)
	}
	if len(implementation) != common.AddressLength {
		return "", "", errors.New("stored verified proxy implementation is invalid")
	}
	return "1", common.BytesToAddress(implementation).Hex(), nil
}

type contractSettings struct {
	optimized            string
	runs                 string
	evmVersion           string
	libraries            string
	constructorArguments string
	licenseType          string
}

func sourceSettings(raw []byte) (contractSettings, error) {
	var object map[string]json.RawMessage
	if err := decodeRawObject(raw, &object); err != nil {
		return contractSettings{}, fmt.Errorf("decode verified contract settings: %w", err)
	}
	result := contractSettings{optimized: "0", runs: "0"}
	if optimizerRaw := object["optimizer"]; len(optimizerRaw) != 0 {
		var optimizer struct {
			Enabled bool            `json:"enabled"`
			Runs    json.RawMessage `json:"runs"`
		}
		if err := decodeRawObject(optimizerRaw, &optimizer); err != nil {
			return contractSettings{}, fmt.Errorf("decode optimizer settings: %w", err)
		}
		if optimizer.Enabled {
			result.optimized = "1"
		}
		if len(optimizer.Runs) != 0 {
			runs, err := jsonDecimal(optimizer.Runs)
			if err != nil {
				return contractSettings{}, fmt.Errorf("decode optimizer runs: %w", err)
			}
			result.runs = runs
		}
	}
	if value := object["evmVersion"]; len(value) != 0 {
		if err := json.Unmarshal(value, &result.evmVersion); err != nil {
			return contractSettings{}, fmt.Errorf("decode EVM version: %w", err)
		}
	}
	if value := object["libraries"]; len(value) != 0 {
		var err error
		result.libraries, err = compactJSON(value)
		if err != nil {
			return contractSettings{}, fmt.Errorf("decode libraries: %w", err)
		}
	}
	for key, destination := range map[string]*string{
		"constructorArguments": &result.constructorArguments,
		"licenseType":          &result.licenseType,
	} {
		if value := object[key]; len(value) != 0 {
			if err := json.Unmarshal(value, destination); err != nil {
				return contractSettings{}, fmt.Errorf("decode %s: %w", key, err)
			}
		}
	}
	return result, nil
}

func jsonDecimal(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
	}
	value, err := parseCanonicalDecimal(text)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func (b *PostgresBackend) contractCreation(ctx context.Context, values url.Values) ([]contractCreationResult, error) {
	rawAddresses := strings.Split(values.Get("contractaddresses"), ",")
	if len(rawAddresses) == 0 || len(rawAddresses) > 5 {
		return nil, invalidParameter("contractaddresses must contain between 1 and 5 addresses")
	}
	seen := make(map[string]struct{}, len(rawAddresses))
	addresses := make([]common.Address, 0, len(rawAddresses))
	for _, raw := range rawAddresses {
		address, _, err := parseAddressParameter(strings.TrimSpace(raw), "contractaddresses")
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address.Hex())
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidParameter("contractaddresses contains a duplicate address")
		}
		seen[key] = struct{}{}
		addresses = append(addresses, address)
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	result := make([]contractCreationResult, 0, len(addresses))
	for _, address := range addresses {
		item, err := b.oneContractCreation(ctx, tx, address)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit contract creation snapshot: %w", err)
	}
	return result, nil
}

func (b *PostgresBackend) oneContractCreation(
	ctx context.Context,
	queryer enrichmentQueryer,
	requested common.Address,
) (contractCreationResult, error) {
	requestedBytes := requested.Bytes()
	var sourceKind string
	var receiptJSON, transactionJSON, factoryBytes, traceInput []byte
	var transactionHashBytes, blockHashBytes []byte
	var blockNumberText, timestampText string
	var transactionIndex int64
	var tracePath, callType sql.NullString
	var traceDepth sql.NullInt64
	err := queryer.QueryRowContext(ctx, contractCreationSQL, b.chain, requestedBytes).Scan(
		&sourceKind, &receiptJSON, &transactionJSON, &transactionHashBytes, &blockHashBytes,
		&blockNumberText, &timestampText, &transactionIndex,
		&tracePath, &traceDepth, &callType, &factoryBytes, &traceInput,
	)
	if err == sql.ErrNoRows {
		return contractCreationResult{}, b.contractCreationAbsence(ctx, queryer)
	}
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("query contract creation: %w", err)
	}
	if transactionIndex < 0 {
		return contractCreationResult{}, errors.New("stored contract creation index is negative")
	}
	transactionHash, err := hashFromBytes(transactionHashBytes)
	if err != nil {
		return contractCreationResult{}, err
	}
	blockHash, err := hashFromBytes(blockHashBytes)
	if err != nil {
		return contractCreationResult{}, err
	}
	blockNumber, err := storedUint256(blockNumberText, "contract creation block number")
	if err != nil {
		return contractCreationResult{}, err
	}
	if _, err := storedUint256(timestampText, "contract creation timestamp"); err != nil {
		return contractCreationResult{}, err
	}
	transaction, sender, err := decodeStoredTransaction(transactionJSON, blockHash, blockNumber, transactionIndex)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("decode contract creation transaction: %w", err)
	}
	if transaction.Hash() != transactionHash {
		return contractCreationResult{}, errors.New("stored contract creation transaction identity is invalid")
	}
	creationBytecode := hexutil.Encode(transaction.Data())
	contractFactory := ""
	switch sourceKind {
	case "top_level":
		if transaction.To() != nil || tracePath.Valid || traceDepth.Valid || callType.Valid || factoryBytes != nil || traceInput != nil {
			return contractCreationResult{}, errors.New("stored top-level contract creation has trace-only fields")
		}
		receipt, err := decodeStoredReceipt(receiptJSON, transaction, blockHash, blockNumber, transactionIndex)
		if err != nil {
			return contractCreationResult{}, fmt.Errorf("decode contract creation receipt: %w", err)
		}
		if receipt.ContractAddress != requested {
			return contractCreationResult{}, errors.New("stored contract creation receipt identity does not match indexed row")
		}
	case "trace":
		if len(receiptJSON) != 0 || !tracePath.Valid || !traceDepth.Valid || !callType.Valid ||
			(callType.String != "CREATE" && callType.String != "CREATE2") || len(factoryBytes) != 20 {
			return contractCreationResult{}, errors.New("stored factory contract creation identity is invalid")
		}
		depth, pathErr := validateTracePath(tracePath.String)
		if pathErr != nil || depth == 0 || int64(depth) != traceDepth.Int64 {
			return contractCreationResult{}, errors.New("stored factory contract creation trace path is invalid")
		}
		contractFactory, err = optionalChecksumAddress(factoryBytes)
		if err != nil {
			return contractCreationResult{}, fmt.Errorf("checksum contract factory: %w", err)
		}
		creationBytecode = hexutil.Encode(traceInput)
	default:
		return contractCreationResult{}, errors.New("stored contract creation source kind is invalid")
	}
	if _, err := ethrpc.ParseData(creationBytecode); err != nil {
		return contractCreationResult{}, errors.New("stored contract creation bytecode is invalid")
	}
	if len(creationBytecode) > b.maxVerificationInputBytes*2+2 {
		return contractCreationResult{}, errors.New("stored contract creation bytecode exceeds the response limit")
	}
	creator, err := checksumAddress(sender)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("checksum contract creator: %w", err)
	}
	contract, err := checksumAddress(requested)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("checksum created contract: %w", err)
	}
	return contractCreationResult{
		ContractAddress: contract, ContractCreator: creator,
		TxHash: strings.ToLower(transactionHash.Hex()), BlockNumber: blockNumberText,
		Timestamp: timestampText, ContractFactory: contractFactory,
		CreationBytecode: strings.ToLower(creationBytecode),
	}, nil
}

// contractCreationAbsence returns ErrNotFound only when genesis-to-tip core
// coverage and the trace stage are both complete. Without that proof, a
// missing row could be an unindexed factory CREATE/CREATE2 and must be exposed
// as an unavailable capability rather than a misleading empty result.
func (b *PostgresBackend) contractCreationAbsence(ctx context.Context, queryer enrichmentQueryer) error {
	if _, err := b.requireCanonicalStageRange(ctx, queryer, traceStage, "0", nil, ErrTraceUnavailable); err != nil {
		return err
	}
	return ErrNotFound
}

const verifiedContractSQL = `
WITH canonical_tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), current_code AS (
    SELECT observation.code_hash, tip.number AS context_number
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.address = $2
          AND observation.canonical = TRUE
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC,
                 observation.observed_at DESC,
                 observation.code_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
)
SELECT current_code.code_hash, verified.code_hash, verified.abi,
       verified.sources, verified.settings, verified.language,
       verified.compiler_version, verified.match_type, verified.contract_name
FROM current_code
LEFT JOIN LATERAL (
    SELECT verified.code_hash, verified.abi, verified.sources,
           verified.settings, verified.language, verified.compiler_version,
           verified.match_type, verified.contract_name
    FROM verified_contracts AS verified
    WHERE verified.chain_id = $1::numeric
      AND verified.address = $2
      AND verified.code_hash = current_code.code_hash
      AND verified.valid_from_block <= current_code.context_number
      AND (verified.valid_to_block IS NULL
           OR verified.valid_to_block >= current_code.context_number)
	    ORDER BY (verified.match_type = 'full') DESC,
	             verified.valid_from_block DESC,
	             verified.request_digest ASC NULLS LAST,
	             verified.verification_job_id ASC,
	             verified.created_at ASC
    LIMIT 1
) AS verified ON TRUE`

const verifiedProxySQL = proxyCurrentStateSQL + `
, current_binding AS (
    SELECT binding.*, current_proxy.context_number,
           current_proxy.proxy_artifact_job_id,
           current_proxy.implementation_artifact_job_id
    FROM current_proxy
    JOIN verified_proxy_bindings AS binding
      ON binding.chain_id = $1::numeric
     AND binding.proxy_address = $2
     AND binding.observation_stage_version = 2
     AND binding.observation_block_number = current_proxy.block_number
     AND binding.observation_block_hash = current_proxy.block_hash
     AND binding.observation_generation_id = current_proxy.observation_generation_id
     AND binding.artifact_resolution_id IS NOT DISTINCT FROM
         current_proxy.artifact_resolution_id
     AND binding.beacon_generation_id IS NOT DISTINCT FROM
         current_proxy.beacon_generation_id
     AND binding.uups_generation_id IS NOT DISTINCT FROM
         current_proxy.uups_generation_id
     AND binding.proxy_code_hash = current_proxy.proxy_code_hash
     AND binding.proxy_kind = current_proxy.proxy_kind
     AND binding.proxy_pattern = current_proxy.proxy_pattern
     AND binding.standard_version IS NOT DISTINCT FROM current_proxy.standard_version
     AND binding.implementation_address = current_proxy.implementation_address
     AND binding.implementation_code_hash = current_proxy.implementation_code_hash
     AND binding.admin_address IS NOT DISTINCT FROM current_proxy.admin_address
     AND binding.admin_code_hash IS NOT DISTINCT FROM current_proxy.admin_code_hash
     AND binding.beacon_address IS NOT DISTINCT FROM current_proxy.beacon_address
     AND binding.beacon_code_hash IS NOT DISTINCT FROM current_proxy.beacon_code_hash
     AND binding.management_kind = current_proxy.management_kind
     AND binding.management_address IS NOT DISTINCT FROM
         current_proxy.management_address
     AND binding.management_code_hash IS NOT DISTINCT FROM
         current_proxy.management_code_hash
    JOIN canonical_blocks AS binding_context
      ON binding_context.chain_id = binding.chain_id
     AND binding_context.number = binding.context_block_number
     AND binding_context.block_hash = binding.context_block_hash
    WHERE current_proxy.proxy_code_hash = $3
      AND (
          (binding.proxy_pattern = 'transparent'
           AND binding.management_kind = 'proxy_admin'
           AND binding.management_address = binding.admin_address
           AND binding.management_code_hash = binding.admin_code_hash)
       OR (binding.proxy_pattern = 'beacon'
           AND binding.management_kind = 'upgradeable_beacon'
           AND binding.management_address = binding.beacon_address
           AND binding.management_code_hash = binding.beacon_code_hash)
       OR (binding.proxy_pattern IN ('clone', 'erc1967', 'uups')
           AND binding.management_kind = 'none'
           AND binding.management_address IS NULL
           AND binding.management_code_hash IS NULL)
      )
      AND proxy_interaction_coverage_contains(
              binding.chain_id,
              binding.observation_block_number,
              binding.observation_block_hash,
              current_proxy.context_number,
              current_proxy.context_hash
          )
      AND NOT EXISTS (
          SELECT 1
          FROM (VALUES
              (binding.proxy_address, binding.proxy_code_hash),
              (binding.implementation_address, binding.implementation_code_hash),
              (binding.admin_address, binding.admin_code_hash),
              (binding.beacon_address, binding.beacon_code_hash),
              (binding.management_address, binding.management_code_hash)
          ) AS identity(address, code_hash)
          JOIN contract_code_observations AS observation
            ON observation.chain_id = binding.chain_id
           AND observation.address = identity.address
           AND observation.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = observation.chain_id
           AND canonical.number = observation.block_number
           AND canonical.block_hash = observation.block_hash
          WHERE identity.address IS NOT NULL
            AND observation.block_number > binding.context_block_number
            AND observation.block_number <= current_proxy.context_number
            AND observation.code_hash IS DISTINCT FROM identity.code_hash
      )
      AND NOT EXISTS (
          SELECT 1
          FROM expected_identity AS identity
          JOIN transaction_state_changes AS change
            ON change.chain_id = binding.chain_id
           AND change.address = identity.address
           AND change.field_kind = 'code'
           AND change.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = change.chain_id
           AND canonical.number = change.block_number
           AND canonical.block_hash = change.block_hash
          WHERE change.block_number > binding.context_block_number
            AND change.block_number <= current_proxy.context_number
            AND lower(change.before_value) IS DISTINCT FROM
                lower(change.after_value)
      )
    ORDER BY binding.created_at DESC, binding.verification_job_id DESC
    LIMIT 1
), required_publication(address, code_hash, epoch_block) AS (
    SELECT publication.address, publication.code_hash, identity.epoch_block
    FROM current_binding AS binding
    CROSS JOIN LATERAL (VALUES
        (binding.proxy_address, binding.proxy_code_hash,
         binding.proxy_pattern <> 'clone'),
        (binding.implementation_address, binding.implementation_code_hash, TRUE),
        (binding.management_address, binding.management_code_hash,
         binding.management_kind <> 'none')
    ) AS publication(address, code_hash, required)
    JOIN expected_identity AS identity
      ON identity.address = publication.address
     AND identity.code_hash = publication.code_hash
    WHERE publication.required
)
SELECT binding.implementation_address
FROM current_binding AS binding
WHERE NOT EXISTS (
    SELECT 1
    FROM current_identity AS identity
    WHERE identity.current_code_hash IS DISTINCT FROM identity.code_hash
)
  AND NOT EXISTS (
      SELECT 1
      FROM required_publication AS publication
      WHERE NOT EXISTS (
          SELECT 1
          FROM verified_contracts AS verified
          WHERE verified.chain_id = $1::numeric
            AND verified.address = publication.address
            AND verified.code_hash = publication.code_hash
            AND verified.valid_from_block >= publication.epoch_block
            AND verified.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )
  AND (
      binding.management_kind = 'none' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.chain_id = $1::numeric
            AND artifact.address = binding.management_address
            AND artifact.code_hash = binding.management_code_hash
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = CASE binding.management_kind
                WHEN 'proxy_admin' THEN 'proxy_admin'
                WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
            END
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )
  AND (
      binding.proxy_pattern = 'clone' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.verification_job_id = binding.proxy_artifact_job_id
            AND artifact.chain_id = $1::numeric
            AND artifact.address = binding.proxy_address
            AND artifact.code_hash = binding.proxy_code_hash
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = CASE binding.proxy_pattern
                WHEN 'erc1967' THEN 'erc1967_proxy'
                WHEN 'transparent' THEN 'transparent_proxy'
                WHEN 'uups' THEN 'erc1967_proxy'
                WHEN 'beacon' THEN 'beacon_proxy'
            END
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )
  AND (
      binding.proxy_pattern <> 'uups' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.verification_job_id =
                binding.implementation_artifact_job_id
            AND artifact.chain_id = $1::numeric
            AND artifact.address = binding.implementation_address
            AND artifact.code_hash = binding.implementation_code_hash
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = 'uups_implementation'
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )`

const contractCreationSQL = `
WITH candidates AS (
    SELECT 'top_level'::text AS source_kind,
           receipt.raw AS receipt_raw, inclusion.raw AS transaction_raw,
           receipt.tx_hash AS transaction_hash, receipt.block_hash,
           receipt.block_number, block.timestamp, receipt.tx_index,
           NULL::text AS trace_path, NULL::integer AS trace_depth,
           NULL::text AS call_type, NULL::bytea AS factory_address,
           NULL::bytea AS trace_input, 0 AS source_rank
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
    JOIN blocks AS block
      ON block.chain_id = receipt.chain_id
     AND block.number = receipt.block_number
     AND block.hash = receipt.block_hash
    WHERE receipt.chain_id = $1::numeric
      AND lower(receipt.raw->>'contractAddress') = lower('0x' || encode($2, 'hex'))

    UNION ALL

    SELECT 'trace'::text AS source_kind,
           NULL::jsonb AS receipt_raw, inclusion.raw AS transaction_raw,
           trace.transaction_hash, trace.block_hash,
           trace.block_number, block.timestamp, trace.transaction_index,
           trace.trace_path, trace.depth, trace.call_type,
           trace.from_address AS factory_address, trace.input AS trace_input,
           1 AS source_rank
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    JOIN transaction_inclusions AS inclusion
      ON inclusion.chain_id = trace.chain_id
     AND inclusion.block_number = trace.block_number
     AND inclusion.block_hash = trace.block_hash
     AND inclusion.tx_index = trace.transaction_index
     AND inclusion.tx_hash = trace.transaction_hash
    JOIN blocks AS block
      ON block.chain_id = trace.chain_id
     AND block.number = trace.block_number
     AND block.hash = trace.block_hash
    WHERE trace.chain_id = $1::numeric
      AND trace.created_address = $2
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
      AND trace.call_type IN ('CREATE', 'CREATE2')
      AND trace.from_address IS NOT NULL
      AND trace.input IS NOT NULL
)
SELECT source_kind, receipt_raw, transaction_raw, transaction_hash,
       block_hash, block_number::text, timestamp::text, tx_index,
       trace_path, trace_depth, call_type, factory_address, trace_input
FROM candidates
ORDER BY block_number ASC, tx_index ASC, source_rank ASC, trace_path ASC
LIMIT 1`
