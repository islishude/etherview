package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/db/gen"
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
	err = tx.QueryRowContext(ctx, dbgen.CatalogTransactionCalldataIdentity, chainID, transactionHash).Scan(
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
		Decoding: TransactionCalldataDecoding{Inputs: []TransactionCalldataInput{}, Candidates: []string{}},
	}
	if err := catalog.loadTransactionExecution(
		ctx, tx, chainID, blockNumberText, blockHash, transactionHash,
		transactionIndex, *wire.To(), &result,
	); err != nil {
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

type verifiedAddressSelectorMatch struct {
	identity enrich.ABIIdentity
	registry *enrich.ABIRegistry
	input    enrich.DecodeResult
}

type verifiedAddressSelectorSelection struct {
	match      *verifiedAddressSelectorMatch
	candidates []string
	ambiguous  bool
}

func decodeVerifiedAddressSelectorCalldata(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	input []byte,
) (TransactionCalldataDecoding, bool, error) {
	selection, err := loadVerifiedAddressSelectorSelection(
		ctx, tx, chainID, blockNumber, blockHash, address, input,
	)
	if err != nil {
		return TransactionCalldataDecoding{}, false, err
	}
	if selection.ambiguous {
		return TransactionCalldataDecoding{
			Status: "ambiguous", Inputs: []TransactionCalldataInput{}, Candidates: selection.candidates,
			Warning: "multiple verified address selector candidates decode this calldata",
		}, true, nil
	}
	if selection.match == nil {
		return TransactionCalldataDecoding{}, false, nil
	}
	result := transactionCalldataDecoding(selection.match.input)
	result.Warning = "decoded from the exact verified address range because execution identity is unavailable"
	return result, true, nil
}

func loadVerifiedAddressSelectorSelection(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	input []byte,
) (verifiedAddressSelectorSelection, error) {
	if len(input) < 4 {
		return verifiedAddressSelectorSelection{}, nil
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogTransactionVerifiedAddressSelectors, chainID, address[:], strconv.FormatUint(blockNumber, 10), input[:4],
		maxVerifiedAddressSelectorCandidates+1,
	)
	if err != nil {
		return verifiedAddressSelectorSelection{}, fmt.Errorf("query verified address function selectors: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	identityBlockHash := common.BytesToHash(blockHash)
	matches := make(map[string]verifiedAddressSelectorMatch)
	signatures := make(map[string]struct{})
	overflow := false
	candidateCount := 0
	for rows.Next() {
		var codeHashBytes, abiEntry []byte
		var storedSignature string
		if err := rows.Scan(&codeHashBytes, &storedSignature, &abiEntry); err != nil {
			return verifiedAddressSelectorSelection{}, fmt.Errorf("scan verified address function selector: %w", err)
		}
		candidateCount++
		if candidateCount > maxVerifiedAddressSelectorCandidates {
			overflow = true
			continue
		}
		if len(codeHashBytes) != common.HashLength {
			return verifiedAddressSelectorSelection{}, ErrCorruptData
		}
		signature, exact := enrich.DecodeVerifiedFunctionCalldata(abiEntry, input)
		if !exact {
			continue
		}
		if signature != storedSignature {
			return verifiedAddressSelectorSelection{}, ErrCorruptData
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
			return verifiedAddressSelectorSelection{}, ErrCorruptData
		}
		decoded := registry.DecodeCalldata(identity, input)
		if decoded.Status != enrich.DecodeDecoded || decoded.Signature != storedSignature {
			return verifiedAddressSelectorSelection{}, ErrCorruptData
		}
		matches[storedSignature+"\x00"+codeHash.Hex()] = verifiedAddressSelectorMatch{
			identity: identity, registry: registry, input: decoded,
		}
		signatures[storedSignature] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return verifiedAddressSelectorSelection{}, fmt.Errorf("iterate verified address function selectors: %w", err)
	}
	if overflow || len(matches) > 1 {
		candidates := make([]string, 0, len(signatures))
		for signature := range signatures {
			candidates = append(candidates, signature)
		}
		sort.Strings(candidates)
		return verifiedAddressSelectorSelection{candidates: candidates, ambiguous: true}, nil
	}
	for _, match := range matches {
		selected := match
		return verifiedAddressSelectorSelection{match: &selected}, nil
	}
	return verifiedAddressSelectorSelection{}, nil
}

func (catalog *Postgres) loadTransactionExecution(
	ctx context.Context,
	tx *sql.Tx,
	chainID, blockNumber string,
	blockHash, transactionHash []byte,
	transactionIndex int64,
	contextAddress common.Address,
	result *TransactionCalldata,
) error {
	var storedContext, executionAddress, executionCodeHash []byte
	var resolution, evidenceSource string
	err := tx.QueryRowContext(ctx, dbgen.CatalogTransactionCalldataExecution, chainID, blockNumber, blockHash, transactionHash, contextAddress[:], transactionIndex).Scan(&storedContext, &executionAddress, &executionCodeHash, &resolution, &evidenceSource)
	if errors.Is(err, sql.ErrNoRows) {
		result.Execution = TransactionExecution{
			ContextAddress: contextAddress.Hex(), Resolution: "unavailable",
			EvidenceSource: "unavailable",
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read transaction calldata execution identity: %w", err)
	}
	if len(storedContext) != common.AddressLength || common.BytesToAddress(storedContext) != contextAddress {
		return ErrCorruptData
	}
	result.Execution = TransactionExecution{
		ContextAddress: contextAddress.Hex(), Resolution: resolution,
		EvidenceSource: evidenceSource,
	}
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
	if !validTransactionExecution(&result.Execution) ||
		resolution == "direct" && result.Execution.Address != result.Execution.ContextAddress ||
		resolution == "direct" && evidenceSource != "prestate_tracer" ||
		resolution == "eip7702_delegate" &&
			evidenceSource != "prestate_tracer" && evidenceSource != "root_trace_code_observation" ||
		resolution == "empty" &&
			evidenceSource != "prestate_tracer" && evidenceSource != "root_trace_code_observation" ||
		resolution == "unavailable" && evidenceSource != "unavailable" {
		return ErrCorruptData
	}
	return nil
}

func validTransactionExecution(value *TransactionExecution) bool {
	if value == nil || !common.IsHexAddress(value.ContextAddress) {
		return false
	}
	switch value.Resolution {
	case "direct", "eip7702_delegate":
		return common.IsHexAddress(value.Address) && len(value.CodeHash) == 66
	case "empty":
		return value.Address == "" && value.CodeHash == ""
	case "unavailable":
		return value.CodeHash == "" &&
			(value.Address == "" || common.IsHexAddress(value.Address))
	default:
		return false
	}
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
	var persistedCall *TraceCallDecoding
	if persisted != nil {
		if len(persisted.targetAddress) != common.AddressLength ||
			common.BytesToAddress(persisted.targetAddress) != executionAddress ||
			len(persisted.targetCodeHash) != common.HashLength ||
			common.BytesToHash(persisted.targetCodeHash) != executionCodeHash {
			return ErrCorruptData
		}
		persistedCall, err = persisted.publicCall(false)
		if err != nil {
			return err
		}
	}

	registryResult, err := loadTraceRegistryForCodeHash(
		ctx, tx, result.Identity.ChainID, blockNumber, blockHash,
		executionAddress, executionCodeHash,
	)
	if err != nil {
		return err
	}
	if registryResult.registry == nil {
		if persistedCall != nil {
			if persistedCall.Status == string(enrich.DecodeDecoded) || len(persistedCall.Inputs) != 0 {
				return ErrCorruptData
			}
			result.Decoding = transactionCalldataStoredDecoding(persistedCall)
			return nil
		}
		result.Decoding.Status = "unavailable"
		result.Decoding.Warning = "no ABI is available for the transaction-time execution code"
		return nil
	}
	if registryResult.identity.CodeHash != executionCodeHash {
		return ErrCorruptData
	}
	if persistedCall != nil && persistedCall.Status == string(enrich.DecodeDecoded) {
		if err := validatePersistedTransactionCalldata(
			registryResult, input, persistedCall,
		); err != nil {
			return err
		}
	}
	decoded := registryResult.registry.DecodeCalldata(registryResult.identity, input)
	if persistedCall != nil && persistedCall.Status == string(enrich.DecodeDecoded) {
		if decoded.Status != enrich.DecodeDecoded && decoded.Status != enrich.DecodeAmbiguous {
			return ErrCorruptData
		}
	}
	result.Decoding = transactionCalldataDecoding(decoded)
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
	err := tx.QueryRowContext(ctx, dbgen.CatalogTransactionCalldataDecoding, chainID, blockHash, transactionHash, executionAddress[:], executionCodeHash[:]).Scan(
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

func sameABISource(left, right *ABISource) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validatePersistedTransactionCalldata(
	registryResult traceRegistryResult,
	input []byte,
	persisted *TraceCallDecoding,
) error {
	if persisted == nil || persisted.ABISource == nil {
		return ErrCorruptData
	}
	candidates := make([]logABICandidate, 0, len(registryResult.candidates))
	for _, candidate := range registryResult.candidates {
		if candidate.sourceKind == persisted.ABISource.Kind &&
			candidate.address.Hex() == persisted.ABISource.Address &&
			candidate.codeHash.Hex() == persisted.ABISource.CodeHash {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return ErrCorruptData
	}
	registry, err := traceRegistryForCandidates(registryResult.identity, candidates)
	if err != nil {
		return ErrCorruptData
	}
	decoded := registry.DecodeCalldata(registryResult.identity, input)
	if decoded.Status != enrich.DecodeDecoded {
		return ErrCorruptData
	}
	fresh := publicTraceCall(enrich.CallDecodeResult{
		Input: decoded, ReturnStatus: enrich.ReturnNotApplicable, Returns: []enrich.DecodedArgument{},
	})
	if !sameABISource(persisted.ABISource, fresh.ABISource) ||
		persisted.Signature != fresh.Signature ||
		persisted.FunctionName != fresh.FunctionName ||
		persisted.Confidence != fresh.Confidence ||
		!reflect.DeepEqual(persisted.Inputs, fresh.Inputs) {
		return ErrCorruptData
	}
	return nil
}

func transactionCalldataDecoding(decoded enrich.DecodeResult) TransactionCalldataDecoding {
	result := TransactionCalldataDecoding{
		Status: string(decoded.Status), FunctionName: decoded.Name, Signature: decoded.Signature,
		Inputs:     transactionCalldataInputs(decoded.Arguments),
		Candidates: append([]string(nil), decoded.Candidates...),
		ABISource:  publicDecodeSource(decoded), Confidence: string(decoded.Confidence), Warning: decoded.Warning,
	}
	if decoded.Status == enrich.DecodeAmbiguous {
		result.FunctionName, result.Signature = "", ""
		result.Inputs, result.ABISource = []TransactionCalldataInput{}, nil
	}
	return result
}

func transactionCalldataStoredDecoding(value *TraceCallDecoding) TransactionCalldataDecoding {
	return TransactionCalldataDecoding{
		Status: value.Status, FunctionName: value.FunctionName, Signature: value.Signature,
		Inputs: []TransactionCalldataInput{}, Candidates: append([]string(nil), value.Candidates...),
		ABISource: value.ABISource, Confidence: value.Confidence, Warning: value.Warning,
	}
}

func transactionCalldataInputs(values []enrich.DecodedArgument) []TransactionCalldataInput {
	result := make([]TransactionCalldataInput, len(values))
	for index, value := range values {
		result[index] = TransactionCalldataInput{
			Name: value.Name, Type: value.Type, Value: value.Value, InternalType: value.InternalType,
			Components: transactionCalldataParameters(value.Components),
		}
	}
	return result
}

func transactionCalldataParameters(values []enrich.DecodedParameter) []TransactionCalldataParameter {
	result := make([]TransactionCalldataParameter, len(values))
	for index, value := range values {
		result[index] = TransactionCalldataParameter{
			Name: value.Name, Type: value.Type, InternalType: value.InternalType,
			Components: transactionCalldataParameters(value.Components),
		}
	}
	return result
}
