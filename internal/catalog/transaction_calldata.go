package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
)

func (catalog *Postgres) TransactionCalldata(
	ctx context.Context,
	chainID string,
	transactionHashText string,
) (TransactionCalldata, error) {
	if err := validateChainID(chainID); err != nil {
		return TransactionCalldata{}, err
	}
	transactionHash, err := decodeFixedHex(transactionHashText, common.HashLength)
	if err != nil {
		return TransactionCalldata{}, ErrInvalidInput
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TransactionCalldata{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var blockNumberText string
	var blockHash, raw []byte
	var transactionIndex int64
	err = tx.QueryRowContext(ctx, transactionCalldataIdentitySQL, chainID, transactionHash).Scan(
		&blockNumberText, &blockHash, &transactionIndex, &raw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TransactionCalldata{}, ErrNotFound
	}
	if err != nil {
		return TransactionCalldata{}, fmt.Errorf("resolve transaction calldata identity: %w", err)
	}
	blockNumber, err := strconv.ParseUint(blockNumberText, 10, 64)
	if err != nil || transactionIndex < 0 || len(blockHash) != common.HashLength {
		return TransactionCalldata{}, ErrCorruptData
	}
	wire, _, err := chainbundle.DecodeTransaction(
		json.RawMessage(raw), common.BytesToHash(blockHash), blockNumber, uint64(transactionIndex),
	)
	if err != nil || common.BytesToHash(transactionHash) != wire.Hash() {
		return TransactionCalldata{}, ErrCorruptData
	}
	if wire.To() == nil {
		return TransactionCalldata{}, ErrNotApplicable
	}
	blockHashText, err := lowerHex(blockHash)
	if err != nil {
		return TransactionCalldata{}, ErrCorruptData
	}
	state, _, err := transactionStageState(
		ctx, tx, chainID, blockNumberText, blockHash, true, StageStateDiff,
	)
	if err != nil {
		return TransactionCalldata{}, err
	}
	if state != StageComplete {
		return TransactionCalldata{}, StageUnavailableError{
			Stage: StageStateDiff, State: state, BlockNumber: blockNumberText, BlockHash: blockHashText,
		}
	}

	result := TransactionCalldata{
		Identity: TransactionResourceIdentity{
			ChainID: chainID, BlockNumber: blockNumberText, BlockHash: blockHashText,
			TransactionHash:  "0x" + hex.EncodeToString(transactionHash),
			TransactionIndex: strconv.FormatInt(transactionIndex, 10), State: StageComplete,
		},
		Input:    "0x" + hex.EncodeToString(wire.Data()),
		Decoding: TransactionCalldataDecoding{Inputs: []ABIValue{}, Candidates: []string{}},
	}
	if err := catalog.loadTransactionExecution(ctx, tx, chainID, blockNumberText, blockHash, transactionHash, *wire.To(), &result); err != nil {
		return TransactionCalldata{}, err
	}
	if result.Execution.Resolution == "empty" {
		result.Decoding.Status = "not_applicable"
		result.Decoding.Warning = "transaction-time execution code is empty"
	} else if result.Execution.Resolution == "unavailable" {
		decoded, found, err := decodeVerifiedAddressSelectorCalldata(
			ctx, tx, chainID, blockNumber, blockHash, *wire.To(), wire.Data(),
		)
		if err != nil {
			return TransactionCalldata{}, err
		}
		if found {
			result.Decoding = decoded
		} else {
			result.Decoding.Status = "unavailable"
			result.Decoding.Warning = "exact transaction-time execution code is unavailable"
		}
	} else if err := catalog.decodeTransactionCalldata(ctx, tx, blockNumber, blockHash, transactionHash, wire.Data(), &result); err != nil {
		return TransactionCalldata{}, err
	}
	if err := commitRead(tx); err != nil {
		return TransactionCalldata{}, err
	}
	return result, nil
}

const maxVerifiedAddressSelectorCandidates = 32

func decodeVerifiedAddressSelectorCalldata(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	input []byte,
) (TransactionCalldataDecoding, bool, error) {
	if len(input) < 4 {
		return TransactionCalldataDecoding{}, false, nil
	}
	rows, err := tx.QueryContext(ctx, transactionVerifiedAddressSelectorsSQL,
		chainID, address[:], strconv.FormatUint(blockNumber, 10), input[:4],
		maxVerifiedAddressSelectorCandidates+1,
	)
	if err != nil {
		return TransactionCalldataDecoding{}, false, fmt.Errorf("query verified address calldata selectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	identityBlockHash := common.BytesToHash(blockHash)
	matches := make(map[string]enrich.DecodeResult)
	signatures := make(map[string]struct{})
	overflow := false
	candidateCount := 0
	for rows.Next() {
		var codeHashBytes, abiEntry []byte
		var storedSignature string
		if err := rows.Scan(&codeHashBytes, &storedSignature, &abiEntry); err != nil {
			return TransactionCalldataDecoding{}, false, fmt.Errorf("scan verified address calldata selector: %w", err)
		}
		candidateCount++
		if candidateCount > maxVerifiedAddressSelectorCandidates {
			overflow = true
			continue
		}
		if len(codeHashBytes) != common.HashLength {
			return TransactionCalldataDecoding{}, false, ErrCorruptData
		}
		signature, exact := enrich.DecodeVerifiedFunctionCalldata(abiEntry, input)
		if !exact {
			continue
		}
		if signature != storedSignature {
			return TransactionCalldataDecoding{}, false, ErrCorruptData
		}
		codeHash := common.BytesToHash(codeHashBytes)
		identity := enrich.ABIIdentity{
			ChainID: chainID, Address: address, CodeHash: codeHash,
			BlockNumber: blockNumber, BlockHash: identityBlockHash,
		}
		validTo := blockNumber
		binding := enrich.ABIBinding{
			Identity: identity, Source: enrich.ABISourceVerified,
			SourceAddress: address, SourceCodeHash: codeHash,
			ValidFromBlock: blockNumber, ValidToBlock: &validTo,
		}
		document := make([]byte, 0, len(abiEntry)+2)
		document = append(document, '[')
		document = append(document, abiEntry...)
		document = append(document, ']')
		registry := enrich.NewABIRegistry()
		if err := registry.RegisterJSON(binding, document); err != nil {
			return TransactionCalldataDecoding{}, false, ErrCorruptData
		}
		decoded := registry.DecodeCalldata(identity, input)
		if decoded.Status != enrich.DecodeDecoded || decoded.Signature != storedSignature {
			return TransactionCalldataDecoding{}, false, ErrCorruptData
		}
		matches[storedSignature+"\x00"+codeHash.Hex()] = decoded
		signatures[storedSignature] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return TransactionCalldataDecoding{}, false, fmt.Errorf("iterate verified address calldata selectors: %w", err)
	}
	if overflow || len(matches) > 1 {
		candidates := make([]string, 0, len(signatures))
		for signature := range signatures {
			candidates = append(candidates, signature)
		}
		sort.Strings(candidates)
		return TransactionCalldataDecoding{
			Status: "ambiguous", Inputs: []ABIValue{}, Candidates: candidates,
			Warning: "multiple verified address selector candidates decode this calldata",
		}, true, nil
	}
	for _, decoded := range matches {
		call := publicTraceCall(enrich.CallDecodeResult{
			Input: decoded, ReturnStatus: enrich.ReturnNotApplicable,
		})
		result := transactionCalldataDecoding(call)
		result.Warning = "decoded from the exact verified address range because execution identity is unavailable"
		return result, true, nil
	}
	return TransactionCalldataDecoding{}, false, nil
}

func (catalog *Postgres) loadTransactionExecution(
	ctx context.Context,
	tx *sql.Tx,
	chainID, blockNumber string,
	blockHash, transactionHash []byte,
	contextAddress common.Address,
	result *TransactionCalldata,
) error {
	var storedContext, executionAddress, executionCodeHash []byte
	var resolution, evidenceSource string
	err := tx.QueryRowContext(ctx, transactionCalldataExecutionSQL,
		chainID, blockNumber, blockHash, transactionHash, contextAddress[:],
	).Scan(&storedContext, &executionAddress, &executionCodeHash, &resolution, &evidenceSource)
	if errors.Is(err, sql.ErrNoRows) {
		result.Execution = TraceExecution{
			ContextAddress: contextAddress.Hex(), Resolution: "unavailable",
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read transaction calldata execution identity: %w", err)
	}
	if len(storedContext) != common.AddressLength || common.BytesToAddress(storedContext) != contextAddress {
		return ErrCorruptData
	}
	result.Execution = TraceExecution{ContextAddress: contextAddress.Hex(), Resolution: resolution}
	if len(executionAddress) != 0 {
		if len(executionAddress) != common.AddressLength {
			return ErrCorruptData
		}
		result.Execution.Address = common.BytesToAddress(executionAddress).Hex()
	}
	if len(executionCodeHash) != 0 {
		if len(executionCodeHash) != common.HashLength {
			return ErrCorruptData
		}
		result.Execution.CodeHash = common.BytesToHash(executionCodeHash).Hex()
	}
	if !validTraceExecution(&result.Execution) ||
		resolution == "direct" && result.Execution.Address != result.Execution.ContextAddress ||
		resolution != "unavailable" && evidenceSource != "prestate_tracer" ||
		resolution == "unavailable" && evidenceSource != "unavailable" {
		return ErrCorruptData
	}
	return nil
}

func (catalog *Postgres) decodeTransactionCalldata(
	ctx context.Context,
	tx *sql.Tx,
	blockNumber uint64,
	blockHash, transactionHash, input []byte,
	result *TransactionCalldata,
) error {
	executionAddress := common.HexToAddress(result.Execution.Address)
	executionCodeHash := common.HexToHash(result.Execution.CodeHash)
	persisted, err := loadPersistedTransactionCalldata(
		ctx, tx, result.Identity.ChainID, blockHash, transactionHash, executionAddress, executionCodeHash,
	)
	if err != nil {
		return err
	}
	if persisted != nil && persisted.strongFor(executionAddress) {
		decoded, err := persisted.publicCall(false)
		if err != nil {
			return err
		}
		result.Decoding = transactionCalldataDecoding(decoded)
		return nil
	}

	registryResult, err := loadTraceRegistry(
		ctx, tx, result.Identity.ChainID, blockNumber, blockHash, executionAddress,
	)
	if err != nil {
		return err
	}
	if registryResult.registry == nil {
		if persisted != nil {
			decoded, err := persisted.publicCall(false)
			if err != nil {
				return err
			}
			result.Decoding = transactionCalldataDecoding(decoded)
			return nil
		}
		result.Decoding.Status = "unavailable"
		result.Decoding.Warning = "no ABI is available for the transaction-time execution code"
		return nil
	}
	if registryResult.identity.CodeHash != executionCodeHash {
		return ErrCorruptData
	}
	decoded := registryResult.registry.DecodeCalldata(registryResult.identity, input)
	call := publicTraceCall(enrich.CallDecodeResult{
		Input: decoded, ReturnStatus: enrich.ReturnNotApplicable, Returns: []enrich.DecodedArgument{},
	})
	result.Decoding = transactionCalldataDecoding(call)
	return nil
}

func loadPersistedTransactionCalldata(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockHash, transactionHash []byte,
	executionAddress common.Address,
	executionCodeHash common.Hash,
) (*persistedTraceDecoding, error) {
	value := &persistedTraceDecoding{}
	err := tx.QueryRowContext(ctx, transactionCalldataDecodingSQL,
		chainID, blockHash, transactionHash, executionAddress[:], executionCodeHash[:],
	).Scan(
		&value.status, &value.signature, &value.source, &value.confidence,
		&value.arguments, &value.candidates, &value.warning,
		&value.targetAddress, &value.targetCodeHash,
		&value.sourceAddress, &value.sourceCodeHash, &value.returnStatus, &value.returns,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query persisted transaction calldata decoding: %w", err)
	}
	return value, nil
}

func transactionCalldataDecoding(value *TraceCallDecoding) TransactionCalldataDecoding {
	return TransactionCalldataDecoding{
		Status: value.Status, FunctionName: value.FunctionName, Signature: value.Signature,
		Inputs: append([]ABIValue(nil), value.Inputs...), Candidates: append([]string(nil), value.Candidates...),
		ABISource: value.ABISource, Confidence: value.Confidence, Warning: value.Warning,
	}
}

const transactionCalldataIdentitySQL = `
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index, inclusion.raw
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
LIMIT 1`

const transactionCalldataExecutionSQL = `
SELECT context_address, execution_address, execution_code_hash, resolution, evidence_source
FROM transaction_execution_code_resolutions
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND transaction_hash = $4
  AND context_address = $5
  AND canonical`

const transactionCalldataDecodingSQL = `
SELECT decoding.status, decoding.signature, decoding.source, decoding.confidence,
       decoding.arguments, decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       decoding.return_status, decoding.return_arguments
FROM abi_decodings AS decoding
WHERE decoding.chain_id = $1::numeric
  AND decoding.block_hash = $2
  AND decoding.transaction_hash = $3
  AND decoding.object_kind = 'transaction_calldata'
  AND decoding.object_index = ''
  AND decoding.target_address = $4
  AND decoding.target_code_hash = $5
  AND decoding.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = decoding.chain_id
        AND published.block_number = decoding.block_number
        AND published.block_hash = decoding.block_hash
        AND published.stage = 'abi'
        AND published.stage_version = 3
        AND published.state = 'complete'
  )`

const transactionVerifiedAddressSelectorsSQL = `
SELECT indexed.code_hash, selector.signature, selector.abi_entry
FROM verified_function_selector_sets AS indexed
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
WHERE indexed.chain_id = $1::numeric
  AND indexed.address = $2
  AND indexed.status = 'complete'
  AND indexed.valid_from_block <= $3::numeric
  AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
  AND selector.selector = $4
ORDER BY selector.signature, indexed.code_hash, indexed.verification_job_id
LIMIT $5`
