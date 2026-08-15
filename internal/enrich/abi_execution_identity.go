package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	effectiveExecutionSourcePrestate = "prestate_tracer"
	effectiveExecutionSourceRoot     = "root_trace_code_observation"
	effectiveExecutionSourceMissing  = "unavailable"
)

type effectiveTransactionExecution struct {
	transactionHash   common.Hash
	transactionIndex  uint64
	contextAddress    common.Address
	executionAddress  *common.Address
	executionCodeHash *common.Hash
	resolution        string
	evidenceSource    string
	rootTracePath     *string
	input             []byte
}

type transactionRootWitness struct {
	executionAddress  *common.Address
	executionCodeHash *common.Hash
	resolution        string
}

type transactionStartCode struct {
	code     []byte
	codeHash common.Hash
}

type transactionCodeChange struct {
	index  uint64
	before []byte
	after  []byte
}

type effectiveTransactionExecutionInput struct {
	transactionHash  []byte
	transactionIndex int64
	raw              []byte
	storedContext    []byte
	storedExecution  []byte
	storedCodeHash   []byte
	storedResolution sql.NullString
	storedSource     sql.NullString
	rootContext      []byte
	rootExecution    []byte
	rootCodeHash     []byte
	rootResolution   sql.NullString
	rootInput        []byte
}

func loadEffectiveTransactionExecutions(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
) ([]effectiveTransactionExecution, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT inclusion.tx_hash, inclusion.tx_index, inclusion.raw,
		       resolution.context_address, resolution.execution_address,
		       resolution.execution_code_hash, resolution.resolution,
		       resolution.evidence_source,
		       root.to_address, root.execution_address,
		       root.execution_code_hash, root.execution_resolution, root.input
		FROM transaction_inclusions AS inclusion
		LEFT JOIN transaction_execution_code_resolutions AS resolution
		  ON resolution.chain_id = inclusion.chain_id
		 AND resolution.block_number = inclusion.block_number
		 AND resolution.block_hash = inclusion.block_hash
		 AND resolution.transaction_hash = inclusion.tx_hash
		 AND resolution.transaction_index = inclusion.tx_index
		 AND resolution.context_address =
		     decode(substring(inclusion.raw->>'to' from 3), 'hex')
		 AND resolution.canonical
		 AND EXISTS (
		     SELECT 1 FROM published_block_stage_results AS published
		     WHERE published.chain_id = resolution.chain_id
		       AND published.block_number = resolution.block_number
		       AND published.block_hash = resolution.block_hash
		       AND published.stage = $4
		       AND published.stage_version = $5
		       AND published.state = 'complete'
		 )
		LEFT JOIN normalized_traces AS root
		  ON root.chain_id = inclusion.chain_id
		 AND root.block_number = inclusion.block_number
		 AND root.block_hash = inclusion.block_hash
		 AND root.transaction_hash = inclusion.tx_hash
		 AND root.transaction_index = inclusion.tx_index
		 AND root.trace_path = ''
		 AND root.canonical
		 AND EXISTS (
		     SELECT 1 FROM published_block_stage_results AS published
		     WHERE published.chain_id = root.chain_id
		       AND published.block_number = root.block_number
		       AND published.block_hash = root.block_hash
		       AND published.stage = $6
		       AND published.stage_version = $7
		       AND published.state = 'complete'
		 )
		WHERE inclusion.chain_id = $1::numeric
		  AND inclusion.block_number = $2::numeric
		  AND inclusion.block_hash = $3
		ORDER BY inclusion.tx_index`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
		StateDiffStage.Name, StateDiffStage.Version, TraceStage.Name, TraceStage.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("query effective transaction execution inputs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var inputs []effectiveTransactionExecutionInput
	for rows.Next() {
		var input effectiveTransactionExecutionInput
		if err := rows.Scan(
			&input.transactionHash, &input.transactionIndex, &input.raw,
			&input.storedContext, &input.storedExecution, &input.storedCodeHash,
			&input.storedResolution, &input.storedSource,
			&input.rootContext, &input.rootExecution, &input.rootCodeHash,
			&input.rootResolution, &input.rootInput,
		); err != nil {
			return nil, fmt.Errorf("scan effective transaction execution input: %w", err)
		}
		input.transactionHash = common.CopyBytes(input.transactionHash)
		input.raw = common.CopyBytes(input.raw)
		input.storedContext = common.CopyBytes(input.storedContext)
		input.storedExecution = common.CopyBytes(input.storedExecution)
		input.storedCodeHash = common.CopyBytes(input.storedCodeHash)
		input.rootContext = common.CopyBytes(input.rootContext)
		input.rootExecution = common.CopyBytes(input.rootExecution)
		input.rootCodeHash = common.CopyBytes(input.rootCodeHash)
		input.rootInput = common.CopyBytes(input.rootInput)
		inputs = append(inputs, input)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate effective transaction execution inputs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close effective transaction execution inputs: %w", err)
	}

	result := make([]effectiveTransactionExecution, 0, len(inputs))
	for _, input := range inputs {
		transactionHashBytes, transactionIndex, raw := input.transactionHash, input.transactionIndex, input.raw
		storedContext, storedExecution := input.storedContext, input.storedExecution
		storedCodeHash, storedResolution, storedSource := input.storedCodeHash, input.storedResolution, input.storedSource
		rootContext, rootExecution, rootCodeHash := input.rootContext, input.rootExecution, input.rootCodeHash
		rootResolution, rootInput := input.rootResolution, input.rootInput
		transactionHash, err := WordFromBytes(transactionHashBytes)
		if err != nil || transactionIndex < 0 {
			return nil, Permanent(errors.New("effective transaction execution identity is invalid"))
		}
		var wire types.Transaction
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, Permanent(fmt.Errorf("decode effective execution transaction: %w", err))
		}
		if err := validateABITransactionIdentity(&wire, raw, job, transactionHash); err != nil {
			return nil, Permanent(err)
		}
		if wire.To() == nil {
			continue
		}
		contextAddress := *wire.To()
		execution := effectiveTransactionExecution{
			transactionHash: transactionHash, transactionIndex: uint64(transactionIndex),
			contextAddress: contextAddress, resolution: "unavailable",
			evidenceSource: effectiveExecutionSourceMissing,
			input:          common.CopyBytes(wire.Data()),
		}
		if storedResolution.Valid {
			parsed, err := parseStoredEffectiveExecution(
				contextAddress, storedContext, storedExecution, storedCodeHash,
				storedResolution.String, storedSource.String,
			)
			if err != nil {
				return nil, Permanent(err)
			}
			execution.executionAddress = parsed.executionAddress
			execution.executionCodeHash = parsed.executionCodeHash
			execution.resolution = parsed.resolution
			execution.evidenceSource = parsed.evidenceSource
		}
		witness, witnessPresent, err := parseTransactionRootWitness(
			contextAddress, execution.input, rootContext, rootExecution,
			rootCodeHash, rootResolution, rootInput,
		)
		if err != nil {
			return nil, Permanent(err)
		}
		if witnessPresent && execution.resolution != "unavailable" {
			if err := validateEffectiveExecutionWitness(execution, witness); err != nil {
				return nil, Permanent(err)
			}
		}
		if execution.resolution == "unavailable" && execution.executionAddress != nil {
			if witnessPresent {
				if witness.executionAddress != nil &&
					*witness.executionAddress != *execution.executionAddress {
					return nil, Permanent(errors.New("transaction root execution address contradicts state-diff evidence"))
				}
				if witness.executionAddress == nil &&
					witness.resolution != "empty" && witness.resolution != "not_applicable" {
					return nil, Permanent(errors.New("transaction root execution address is missing"))
				}
				if _, precompile := vm.PrecompiledContractsPrague[*execution.executionAddress]; precompile {
					rootPath := ""
					execution.rootTracePath = &rootPath
					execution.evidenceSource = effectiveExecutionSourceRoot
					execution.executionAddress = nil
					execution.resolution = "empty"
					result = append(result, execution)
					continue
				}
				recovered, found, err := resolveTransactionStartCode(
					ctx, tx, job, *execution.executionAddress, execution.transactionIndex,
				)
				if err != nil {
					return nil, err
				}
				if found {
					if witness.executionCodeHash != nil &&
						*witness.executionCodeHash != recovered.codeHash {
						return nil, Permanent(errors.New("transaction root code hash contradicts canonical code history"))
					}
					rootPath := ""
					execution.rootTracePath = &rootPath
					execution.evidenceSource = effectiveExecutionSourceRoot
					if len(recovered.code) == 0 {
						execution.executionAddress = nil
						execution.resolution = "empty"
					} else if _, delegated := types.ParseDelegation(recovered.code); delegated {
						execution.executionAddress = nil
						execution.resolution = "empty"
					} else {
						execution.executionCodeHash = &recovered.codeHash
						execution.resolution = "eip7702_delegate"
					}
					if execution.resolution == "empty" &&
						witness.resolution != "empty" && witness.resolution != "not_applicable" && witness.resolution != "unavailable" {
						return nil, Permanent(errors.New("transaction root non-empty execution contradicts canonical empty code"))
					}
					if execution.resolution == "eip7702_delegate" && witness.resolution == "empty" {
						return nil, Permanent(errors.New("transaction root empty execution contradicts canonical delegate code"))
					}
				}
			}
		}
		result = append(result, execution)
	}
	return result, nil
}

func validateEffectiveExecutionWitness(
	execution effectiveTransactionExecution,
	witness transactionRootWitness,
) error {
	switch execution.resolution {
	case "direct", "eip7702_delegate":
		if witness.resolution != execution.resolution ||
			execution.executionAddress == nil || witness.executionAddress == nil ||
			*execution.executionAddress != *witness.executionAddress ||
			execution.executionCodeHash == nil || witness.executionCodeHash == nil ||
			*execution.executionCodeHash != *witness.executionCodeHash {
			return errors.New("transaction root execution identity contradicts state-diff evidence")
		}
	case "empty":
		if witness.resolution != "empty" && witness.resolution != "not_applicable" {
			return errors.New("transaction root execution identity contradicts empty state-diff evidence")
		}
	default:
		return errors.New("effective transaction execution resolution is invalid")
	}
	return nil
}

func parseStoredEffectiveExecution(
	contextAddress common.Address,
	storedContext, executionAddress, executionCodeHash []byte,
	resolution, evidenceSource string,
) (effectiveTransactionExecution, error) {
	if len(storedContext) != common.AddressLength ||
		common.BytesToAddress(storedContext) != contextAddress {
		return effectiveTransactionExecution{}, errors.New("stored transaction execution context is invalid")
	}
	result := effectiveTransactionExecution{
		contextAddress: contextAddress, resolution: resolution,
		evidenceSource: evidenceSource,
	}
	if len(executionAddress) != 0 {
		if len(executionAddress) != common.AddressLength {
			return effectiveTransactionExecution{}, errors.New("stored transaction execution address is invalid")
		}
		value := common.BytesToAddress(executionAddress)
		result.executionAddress = &value
	}
	if len(executionCodeHash) != 0 {
		if len(executionCodeHash) != common.HashLength {
			return effectiveTransactionExecution{}, errors.New("stored transaction execution code hash is invalid")
		}
		value := common.BytesToHash(executionCodeHash)
		result.executionCodeHash = &value
	}
	switch resolution {
	case "direct":
		if result.executionAddress == nil || *result.executionAddress != contextAddress ||
			result.executionCodeHash == nil || evidenceSource != effectiveExecutionSourcePrestate {
			return effectiveTransactionExecution{}, errors.New("stored direct transaction execution identity is invalid")
		}
	case "eip7702_delegate":
		if result.executionAddress == nil || result.executionCodeHash == nil ||
			evidenceSource != effectiveExecutionSourcePrestate {
			return effectiveTransactionExecution{}, errors.New("stored delegated transaction execution identity is invalid")
		}
	case "empty":
		if result.executionAddress != nil || result.executionCodeHash != nil ||
			evidenceSource != effectiveExecutionSourcePrestate {
			return effectiveTransactionExecution{}, errors.New("stored empty transaction execution identity is invalid")
		}
	case "unavailable":
		if result.executionCodeHash != nil || evidenceSource != effectiveExecutionSourceMissing {
			return effectiveTransactionExecution{}, errors.New("stored unavailable transaction execution identity is invalid")
		}
	default:
		return effectiveTransactionExecution{}, errors.New("stored transaction execution resolution is invalid")
	}
	return result, nil
}

func parseTransactionRootWitness(
	contextAddress common.Address,
	input, rootContext, rootExecution, rootCodeHash []byte,
	rootResolution sql.NullString,
	rootInput []byte,
) (transactionRootWitness, bool, error) {
	if !rootResolution.Valid {
		return transactionRootWitness{}, false, nil
	}
	if len(rootContext) != common.AddressLength ||
		common.BytesToAddress(rootContext) != contextAddress ||
		!bytes.Equal(rootInput, input) {
		return transactionRootWitness{}, false, errors.New("transaction root trace contradicts stored transaction input")
	}
	result := transactionRootWitness{resolution: rootResolution.String}
	if len(rootExecution) != 0 {
		if len(rootExecution) != common.AddressLength {
			return transactionRootWitness{}, false, errors.New("transaction root execution address is invalid")
		}
		value := common.BytesToAddress(rootExecution)
		result.executionAddress = &value
	}
	if len(rootCodeHash) != 0 {
		if len(rootCodeHash) != common.HashLength {
			return transactionRootWitness{}, false, errors.New("transaction root execution code hash is invalid")
		}
		value := common.BytesToHash(rootCodeHash)
		result.executionCodeHash = &value
	}
	switch result.resolution {
	case "direct", "eip7702_delegate":
		if result.executionAddress == nil || result.executionCodeHash == nil {
			return transactionRootWitness{}, false, errors.New("transaction root exact execution identity is incomplete")
		}
		if result.resolution == "direct" && *result.executionAddress != contextAddress {
			return transactionRootWitness{}, false, errors.New("transaction root direct execution address contradicts its context")
		}
	case "unavailable":
		if result.executionCodeHash != nil {
			return transactionRootWitness{}, false, errors.New("transaction root unavailable execution has a code hash")
		}
	case "empty", "not_applicable":
		if result.executionAddress != nil || result.executionCodeHash != nil {
			return transactionRootWitness{}, false, errors.New("transaction root empty execution identity is invalid")
		}
	default:
		return transactionRootWitness{}, false, errors.New("transaction root execution resolution is invalid")
	}
	return result, true, nil
}

func resolveTransactionStartCode(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	address common.Address,
	transactionIndex uint64,
) (transactionStartCode, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT transaction_index, before_value, after_value
		FROM transaction_state_changes
		WHERE chain_id = $1::numeric
		  AND block_number = $2::numeric
		  AND block_hash = $3
		  AND address = $4
		  AND field_kind = 'code'
		  AND canonical
		ORDER BY transaction_index`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:], address[:],
	)
	if err != nil {
		return transactionStartCode{}, false, fmt.Errorf("query transaction-position code changes: %w", err)
	}
	var changes []transactionCodeChange
	for rows.Next() {
		var index int64
		var beforeText, afterText sql.NullString
		if err := rows.Scan(&index, &beforeText, &afterText); err != nil {
			_ = rows.Close()
			return transactionStartCode{}, false, fmt.Errorf("scan transaction-position code change: %w", err)
		}
		if index < 0 {
			_ = rows.Close()
			return transactionStartCode{}, false, Permanent(errors.New("transaction-position code change index is invalid"))
		}
		before, err := decodeHistoricalCode(beforeText)
		if err != nil {
			_ = rows.Close()
			return transactionStartCode{}, false, Permanent(err)
		}
		after, err := decodeHistoricalCode(afterText)
		if err != nil {
			_ = rows.Close()
			return transactionStartCode{}, false, Permanent(err)
		}
		changes = append(changes, transactionCodeChange{index: uint64(index), before: before, after: after})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return transactionStartCode{}, false, fmt.Errorf("iterate transaction-position code changes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return transactionStartCode{}, false, fmt.Errorf("close transaction-position code changes: %w", err)
	}

	if len(changes) != 0 {
		current, err := codeAtTransactionStart(changes, transactionIndex)
		if err != nil {
			return transactionStartCode{}, false, Permanent(err)
		}
		var priorHashBytes, priorCode []byte
		priorErr := tx.QueryRowContext(ctx, `
			SELECT observation.code_hash, observation.code
			FROM contract_code_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = $1::numeric
			  AND observation.address = $2
			  AND observation.block_number < $3::numeric
			  AND observation.canonical
			ORDER BY observation.block_number DESC, observation.observed_at DESC,
			         observation.code_hash DESC
			LIMIT 1`,
			job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10),
		).Scan(&priorHashBytes, &priorCode)
		if priorErr != nil && !errors.Is(priorErr, sql.ErrNoRows) {
			return transactionStartCode{}, false, fmt.Errorf("query prior canonical code observation: %w", priorErr)
		}
		if priorErr == nil {
			if len(priorHashBytes) != common.HashLength ||
				common.BytesToHash(priorHashBytes) != crypto.Keccak256Hash(changes[0].before) ||
				priorCode != nil && !bytes.Equal(priorCode, changes[0].before) {
				return transactionStartCode{}, false, Permanent(errors.New("prior canonical code observation contradicts block code history"))
			}
		}
		return transactionStartCode{
			code: current, codeHash: crypto.Keccak256Hash(current),
		}, true, nil
	}

	var codeHashBytes, code []byte
	err = tx.QueryRowContext(ctx, `
		SELECT observation.code_hash, observation.code
		FROM contract_code_observations AS observation
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = observation.chain_id
		 AND canonical.number = observation.block_number
		 AND canonical.block_hash = observation.block_hash
		WHERE observation.chain_id = $1::numeric
		  AND observation.address = $2
		  AND observation.block_number < $3::numeric
		  AND observation.canonical
		ORDER BY observation.block_number DESC, observation.observed_at DESC,
		         observation.code_hash DESC
		LIMIT 1`,
		job.ChainID, address[:], strconv.FormatUint(job.BlockNumber, 10),
	).Scan(&codeHashBytes, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return transactionStartCode{}, false, nil
	}
	if err != nil {
		return transactionStartCode{}, false, fmt.Errorf("query canonical transaction-start code observation: %w", err)
	}
	if len(codeHashBytes) != common.HashLength || code == nil {
		return transactionStartCode{}, false, nil
	}
	codeHash := common.BytesToHash(codeHashBytes)
	if crypto.Keccak256Hash(code) != codeHash {
		return transactionStartCode{}, false, Permanent(errors.New("canonical code observation hash is inconsistent"))
	}
	return transactionStartCode{code: common.CopyBytes(code), codeHash: codeHash}, true, nil
}

func codeAtTransactionStart(
	changes []transactionCodeChange,
	transactionIndex uint64,
) ([]byte, error) {
	if len(changes) == 0 {
		return nil, errors.New("transaction-position code history is empty")
	}
	current := common.CopyBytes(changes[0].before)
	atTransactionStart := common.CopyBytes(current)
	for index, change := range changes {
		if index > 0 && changes[index-1].index >= change.index {
			return nil, errors.New("transaction-position code changes are not strictly ordered")
		}
		if !bytes.Equal(current, change.before) {
			return nil, errors.New("transaction-position code history is discontinuous")
		}
		if change.index < transactionIndex {
			atTransactionStart = common.CopyBytes(change.after)
		}
		current = common.CopyBytes(change.after)
	}
	return atTransactionStart, nil
}

func decodeHistoricalCode(value sql.NullString) ([]byte, error) {
	if !value.Valid {
		return []byte{}, nil
	}
	decoded, err := hexutil.Decode(value.String)
	if err != nil {
		return nil, errors.New("transaction-position code history contains invalid code")
	}
	return decoded, nil
}

func persistEffectiveTransactionExecutions(
	ctx context.Context,
	tx *sql.Tx,
	job Job,
	executions []effectiveTransactionExecution,
) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM transaction_effective_execution_identities
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3`,
		job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
	); err != nil {
		return fmt.Errorf("clear effective transaction execution identities: %w", err)
	}
	for _, execution := range executions {
		var executionAddress, executionCodeHash, rootTracePath any
		if execution.executionAddress != nil {
			executionAddress = execution.executionAddress[:]
		}
		if execution.executionCodeHash != nil {
			executionCodeHash = execution.executionCodeHash[:]
		}
		if execution.rootTracePath != nil {
			rootTracePath = *execution.rootTracePath
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transaction_effective_execution_identities (
			    chain_id, block_number, block_hash, transaction_hash,
			    transaction_index, context_address, execution_address,
			    execution_code_hash, resolution, evidence_source,
			    root_trace_path, canonical
			) VALUES (
			    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8,
			    $9, $10, $11, TRUE
			)`,
			job.ChainID, strconv.FormatUint(job.BlockNumber, 10), job.BlockHash[:],
			execution.transactionHash[:], execution.transactionIndex,
			execution.contextAddress[:], executionAddress, executionCodeHash,
			execution.resolution, execution.evidenceSource, rootTracePath,
		); err != nil {
			return fmt.Errorf("persist effective transaction execution identity: %w", err)
		}
	}
	return nil
}
