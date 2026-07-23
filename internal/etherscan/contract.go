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
	if record.Language != "solidity" && record.Language != "vyper" {
		return verifiedContractRecord{}, fmt.Errorf("stored verified contract has unsupported language %q", record.Language)
	}
	if record.MatchKind != "exact" && record.MatchKind != "metadata_only" {
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
	compilerType := "solc"
	if record.Language == "vyper" {
		compilerType = "vyper"
	}
	return []sourceCodeResult{{
		SourceCode: sources, ABI: abi, ContractName: record.ContractName,
		CompilerVersion: record.CompilerVersion, CompilerType: compilerType,
		OptimizationUsed: settings.optimized,
		Runs:             settings.runs, ConstructorArguments: settings.constructorArguments,
		EVMVersion: settings.evmVersion, Library: settings.libraries,
		ContractFileName: "", LicenseType: settings.licenseType,
		Proxy: "0", Implementation: "",
		SwarmSource: "", SimilarMatch: "", MatchKind: record.MatchKind,
	}}, nil
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
	addresses := make([]ethrpc.Address, 0, len(rawAddresses))
	for _, raw := range rawAddresses {
		address, _, err := parseAddressParameter(strings.TrimSpace(raw), "contractaddresses")
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address.String())
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
	requested ethrpc.Address,
) (contractCreationResult, error) {
	requestedBytes, err := requested.Bytes()
	if err != nil {
		return contractCreationResult{}, err
	}
	var sourceKind string
	var receiptJSON, transactionJSON, factoryBytes, traceInput []byte
	var transactionHashBytes, blockHashBytes []byte
	var blockNumberText, timestampText string
	var transactionIndex int64
	var tracePath, callType sql.NullString
	var traceDepth sql.NullInt64
	err = queryer.QueryRowContext(ctx, contractCreationSQL, b.chain, requestedBytes).Scan(
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
	var transaction ethrpc.Transaction
	if err := decodeRawObject(transactionJSON, &transaction); err != nil {
		return contractCreationResult{}, fmt.Errorf("decode contract creation transaction: %w", err)
	}
	if !transaction.Hash.Equal(transactionHash) || transaction.BlockHash == nil || !transaction.BlockHash.Equal(blockHash) || transaction.BlockNumber == nil || transaction.TransactionIndex == nil {
		return contractCreationResult{}, errors.New("stored contract creation transaction identity is invalid")
	}
	transactionBlock, err := transaction.BlockNumber.Big()
	if err != nil || transactionBlock.Cmp(blockNumber) != 0 {
		return contractCreationResult{}, errors.New("stored contract creation transaction block is invalid")
	}
	transactionIndexValue, err := transaction.TransactionIndex.Uint64()
	if err != nil || transactionIndexValue != uint64(transactionIndex) {
		return contractCreationResult{}, errors.New("stored contract creation transaction index is invalid")
	}
	creationBytecode := transaction.Input.String()
	contractFactory := ""
	switch sourceKind {
	case "top_level":
		if transaction.To != nil || tracePath.Valid || traceDepth.Valid || callType.Valid || factoryBytes != nil || traceInput != nil {
			return contractCreationResult{}, errors.New("stored top-level contract creation has trace-only fields")
		}
		var receipt ethrpc.Receipt
		if err := decodeRawObject(receiptJSON, &receipt); err != nil {
			return contractCreationResult{}, fmt.Errorf("decode contract creation receipt: %w", err)
		}
		if receipt.ContractAddress == nil || !receipt.ContractAddress.Equal(requested) || !receipt.TransactionHash.Equal(transactionHash) || !receipt.BlockHash.Equal(blockHash) {
			return contractCreationResult{}, errors.New("stored contract creation receipt identity does not match indexed row")
		}
		wireBlock, receiptErr := receipt.BlockNumber.Big()
		if receiptErr != nil || wireBlock.Cmp(blockNumber) != 0 {
			return contractCreationResult{}, errors.New("stored contract creation receipt block does not match indexed row")
		}
		wireIndex, receiptErr := receipt.TransactionIndex.Uint64()
		if receiptErr != nil || wireIndex != uint64(transactionIndex) {
			return contractCreationResult{}, errors.New("stored contract creation receipt index does not match indexed row")
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
		creationBytecode = ethrpc.DataFromBytes(traceInput).String()
	default:
		return contractCreationResult{}, errors.New("stored contract creation source kind is invalid")
	}
	if _, err := ethrpc.ParseData(creationBytecode); err != nil {
		return contractCreationResult{}, errors.New("stored contract creation bytecode is invalid")
	}
	if len(creationBytecode) > b.maxVerificationInputBytes*2+2 {
		return contractCreationResult{}, errors.New("stored contract creation bytecode exceeds the response limit")
	}
	creator, err := checksumAddress(transaction.From)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("checksum contract creator: %w", err)
	}
	contract, err := checksumAddress(requested)
	if err != nil {
		return contractCreationResult{}, fmt.Errorf("checksum created contract: %w", err)
	}
	return contractCreationResult{
		ContractAddress: contract, ContractCreator: creator,
		TxHash: strings.ToLower(transactionHash.String()), BlockNumber: blockNumberText,
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
       verified.compiler_version, verified.match_kind, verified.contract_name
FROM current_code
LEFT JOIN LATERAL (
    SELECT verified.code_hash, verified.abi, verified.sources,
           verified.settings, verified.language, verified.compiler_version,
           verified.match_kind, verified.contract_name
    FROM verified_contracts AS verified
    WHERE verified.chain_id = $1::numeric
      AND verified.address = $2
      AND verified.code_hash = current_code.code_hash
      AND verified.valid_from_block <= current_code.context_number
      AND (verified.valid_to_block IS NULL
           OR verified.valid_to_block >= current_code.context_number)
	    ORDER BY (verified.match_kind = 'exact') DESC,
	             verified.valid_from_block DESC,
	             verified.request_digest ASC NULLS LAST,
	             verified.created_at ASC
    LIMIT 1
) AS verified ON TRUE`

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
