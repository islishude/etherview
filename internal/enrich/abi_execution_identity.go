package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	storedResolution pgtype.Text
	storedSource     pgtype.Text
	rootContext      []byte
	rootExecution    []byte
	rootCodeHash     []byte
	rootResolution   pgtype.Text
	rootInput        []byte
}

func loadEffectiveTransactionExecutions(
	ctx context.Context,
	tx pgx.Tx,
	job Job,
) ([]effectiveTransactionExecution, error) {
	rows, err := func() ([]dbgen.EnrichInlineLoadEffectiveTransactionExecutionsStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		if StateDiffStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		if TraceStage.Version > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EnrichInlineLoadEffectiveTransactionExecutionsStatement1(ctx, dbgen.EnrichInlineLoadEffectiveTransactionExecutionsStatement1Params{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], Stage: StateDiffStage.Name, StageVersion: int32(StateDiffStage.Version), Stage2: TraceStage.Name, StageVersion2: int32(TraceStage.Version)})
	}()
	if err != nil {
		return nil, fmt.Errorf("query effective transaction execution inputs: %w", err)
	}

	var inputs []effectiveTransactionExecutionInput
	for _, storedRow := range rows {
		var input effectiveTransactionExecutionInput
		{
			input.transactionHash = storedRow.TxHash
			input.transactionIndex = storedRow.TxIndex
			input.raw = storedRow.Raw
			input.storedContext = storedRow.ContextAddress
			input.storedExecution = storedRow.ExecutionAddress
			input.storedCodeHash = storedRow.ExecutionCodeHash
			input.storedResolution = storedRow.Resolution
			var queryValue7 pgtype.Text
			if storedRow.EvidenceSource != nil {
				queryValue7 = pgtype.Text{String: *storedRow.EvidenceSource, Valid: true}
			}
			input.storedSource = queryValue7
			input.rootContext = storedRow.ToAddress
			input.rootExecution = storedRow.ExecutionAddress_2
			input.rootCodeHash = storedRow.ExecutionCodeHash_2
			var queryValue12 pgtype.Text
			if storedRow.ExecutionResolution != nil {
				queryValue12 = pgtype.Text{String: *storedRow.ExecutionResolution, Valid: true}
			}
			input.rootResolution = queryValue12
			input.rootInput = storedRow.Input
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
	rootResolution pgtype.Text,
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
	tx pgx.Tx,
	job Job,
	address common.Address,
	transactionIndex uint64,
) (transactionStartCode, bool, error) {
	rows, err := func() ([]dbgen.EnrichInlineResolveTransactionStartCodeStatement1Row, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return nil, err
		}
		return dbgen.New(tx).EnrichInlineResolveTransactionStartCodeStatement1(ctx, dbgen.EnrichInlineResolveTransactionStartCodeStatement1Params{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], Address: address[:]})
	}()
	if err != nil {
		return transactionStartCode{}, false, fmt.Errorf("query transaction-position code changes: %w", err)
	}
	var changes []transactionCodeChange
	for _, storedRow := range rows {
		var index int64
		var beforeText, afterText pgtype.Text
		{
			index = storedRow.TransactionIndex
			var queryValue1 pgtype.Text
			if storedRow.BeforeValue != nil {
				queryValue1 = pgtype.Text{String: *storedRow.BeforeValue, Valid: true}
			}
			beforeText = queryValue1
			var queryValue3 pgtype.Text
			if storedRow.AfterValue != nil {
				queryValue3 = pgtype.Text{String: *storedRow.AfterValue, Valid: true}
			}
			afterText = queryValue3
		}
		if index < 0 {

			return transactionStartCode{}, false, Permanent(errors.New("transaction-position code change index is invalid"))
		}
		before, err := decodeHistoricalCode(beforeText)
		if err != nil {

			return transactionStartCode{}, false, Permanent(err)
		}
		after, err := decodeHistoricalCode(afterText)
		if err != nil {

			return transactionStartCode{}, false, Permanent(err)
		}
		changes = append(changes, transactionCodeChange{index: uint64(index), before: before, after: after})
	}

	if len(changes) != 0 {
		current, err := codeAtTransactionStart(changes, transactionIndex)
		if err != nil {
			return transactionStartCode{}, false, Permanent(err)
		}
		var priorHashBytes, priorCode []byte
		priorErr := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).EnrichInlineResolveTransactionStartCodeStatement2(ctx, queryValue0, address[:], queryValue1)
			if err != nil {
				return err
			}
			priorHashBytes = queryRow.CodeHash
			priorCode = queryRow.Code
			return nil
		}()
		if priorErr != nil && !errors.Is(priorErr, pgx.ErrNoRows) {
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).EnrichInlineResolveTransactionStartCodeStatement3(ctx, queryValue0, address[:], queryValue1)
		if err != nil {
			return err
		}
		codeHashBytes = queryRow.CodeHash
		code = queryRow.Code
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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

func decodeHistoricalCode(value pgtype.Text) ([]byte, error) {
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
	tx pgx.Tx,
	job Job,
	executions []effectiveTransactionExecution,
) error {
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(job.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
			return err
		}
		return dbgen.New(tx).EnrichInlinePersistEffectiveTransactionExecutionsStatement1(ctx, queryValue0, queryValue1, job.BlockHash[:])
	}(); err != nil {
		return fmt.Errorf("clear effective transaction execution identities: %w", err)
	}
	for _, execution := range executions {
		var executionAddress []byte
		var executionCodeHash []byte
		var rootTracePath *string
		if execution.executionAddress != nil {
			executionAddress = execution.executionAddress[:]
		}
		if execution.executionCodeHash != nil {
			executionCodeHash = execution.executionCodeHash[:]
		}
		if execution.rootTracePath != nil {
			rootTracePath = new(*execution.rootTracePath)
		}
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(job.ChainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(job.BlockNumber, 10)); err != nil {
				return err
			}
			if execution.transactionIndex > 9223372036854775807 {
				return errors.New("invalid stored query value")
			}
			return dbgen.New(tx).EnrichInlinePersistEffectiveTransactionExecutionsStatement2(ctx, dbgen.EnrichInlinePersistEffectiveTransactionExecutionsStatement2Params{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: job.BlockHash[:], TransactionHash: execution.transactionHash[:], TransactionIndex: int64(execution.transactionIndex), ContextAddress: execution.contextAddress[:], ExecutionAddress: executionAddress, ExecutionCodeHash: executionCodeHash, Resolution: pgtype.Text{String: execution.resolution, Valid: true}, EvidenceSource: execution.evidenceSource, RootTracePath: rootTracePath})
		}(); err != nil {
			return fmt.Errorf("persist effective transaction execution identity: %w", err)
		}
	}
	return nil
}
