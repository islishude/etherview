package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
		result.Decoding.Status = "unavailable"
		result.Decoding.Warning = "exact transaction-time execution code is unavailable"
	} else if err := catalog.decodeTransactionCalldata(ctx, tx, blockNumber, blockHash, transactionHash, wire.Data(), &result); err != nil {
		return TransactionCalldata{}, err
	}
	if err := commitRead(tx); err != nil {
		return TransactionCalldata{}, err
	}
	return result, nil
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
