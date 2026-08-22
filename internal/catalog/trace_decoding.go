package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
)

const (
	maxReadTimeTraceABIIdentities           = 1024
	maxReadTimeTraceVerifiedSelectorLookups = 1024
)

type persistedTraceDecoding struct {
	objectKind                        string
	path                              string
	status, signature, source         sql.NullString
	confidence, warning, returnStatus sql.NullString
	arguments, candidates, returns    []byte
	targetAddress, targetCodeHash     []byte
	sourceAddress, sourceCodeHash     []byte
}

func (catalog *Postgres) decorateCachedTrace(
	ctx context.Context,
	identity traceIdentity,
	trace TransactionTrace,
) (TransactionTrace, error) {
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TransactionTrace{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	current, _, err := catalog.resolveTraceIdentity(ctx, tx, identity.ChainID, mustDecodeHash(identity.TransactionHash))
	if err != nil || current != identity {
		if err != nil {
			return TransactionTrace{}, err
		}
		return TransactionTrace{}, ErrCorruptData
	}
	if err := catalog.decorateTraceFrames(ctx, tx, identity, &trace); err != nil {
		return TransactionTrace{}, err
	}
	if err := commitRead(tx); err != nil {
		return TransactionTrace{}, err
	}
	return trace, nil
}

func mustDecodeHash(value string) []byte {
	decoded, _ := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	return decoded
}

func (catalog *Postgres) decorateTraceFrames(
	ctx context.Context,
	tx *sql.Tx,
	identity traceIdentity,
	trace *TransactionTrace,
) error {
	if err := catalog.attachTraceExecutions(ctx, tx, identity, trace); err != nil {
		return err
	}
	blockNumber, err := strconv.ParseUint(identity.BlockNumber, 10, 64)
	if err != nil {
		return ErrCorruptData
	}
	blockHash, err := decodeFixedHex(identity.BlockHash, common.HashLength)
	if err != nil {
		return ErrCorruptData
	}
	txHash, err := decodeFixedHex(identity.TransactionHash, common.HashLength)
	if err != nil {
		return ErrCorruptData
	}
	needsABI := false
	for index := range trace.Frames {
		frame := &trace.Frames[index]
		if callLikeTraceType(frame.CallType) && frame.Execution != nil && frame.Execution.Address != "" && frame.Input != nil {
			needsABI = true
			break
		}
		if (frame.CallType == "CREATE" || frame.CallType == "CREATE2") && frame.CreatedAddress != nil && frame.Input != nil {
			needsABI = true
			break
		}
	}
	persisted := map[string]*persistedTraceDecoding{}
	if needsABI {
		persisted, err = loadPersistedTraceDecodings(ctx, tx, identity.ChainID, blockHash, txHash)
		if err != nil {
			return err
		}
	}
	loaded := make(map[common.Address]traceRegistryResult)
	stateDiffChecked, stateDiffComplete := false, false
	verifiedSelectorLookups := 0
	for index := range trace.Frames {
		frame := &trace.Frames[index]
		if frame.CallType == "CREATE" || frame.CallType == "CREATE2" {
			if err := catalog.decorateConstructorFrame(ctx, tx, identity, frame, persisted); err != nil {
				return err
			}
			continue
		}
		if !callLikeTraceType(frame.CallType) {
			continue
		}
		if frame.Execution != nil && (frame.Execution.Resolution == "empty" || frame.Execution.Resolution == "not_applicable") {
			frame.Decoding = notApplicableTraceCallDecoding("call execution code is empty")
			continue
		}
		frame.Decoding = unavailableTraceCallDecoding(frame.DirectReverted, "no ABI is available for the call target at this block")
		if directVerifiedAddressTraceFallback(frame) {
			input, err := decodeTraceData(*frame.Input)
			if err != nil {
				return ErrCorruptData
			}
			if len(input) >= 4 {
				if !stateDiffChecked {
					state, _, stateErr := transactionStageState(
						ctx, tx, identity.ChainID, identity.BlockNumber, blockHash, true, StageStateDiff,
					)
					if stateErr != nil {
						return stateErr
					}
					stateDiffChecked, stateDiffComplete = true, state == StageComplete
				}
				if stateDiffComplete {
					if verifiedSelectorLookups >= maxReadTimeTraceVerifiedSelectorLookups {
						frame.Decoding.Status = "unknown"
						frame.Decoding.Warning = "read-time verified selector lookup limit exceeded"
						continue
					}
					verifiedSelectorLookups++
					contextBytes, decodeErr := decodeFixedHex(frame.Execution.ContextAddress, common.AddressLength)
					if decodeErr != nil {
						return ErrCorruptData
					}
					var output []byte
					if frame.Output != nil {
						output, decodeErr = decodeTraceData(*frame.Output)
						if decodeErr != nil {
							return ErrCorruptData
						}
					}
					decoding, found, decodeErr := decodeVerifiedAddressSelectorTraceCall(
						ctx, tx, identity.ChainID, blockNumber, blockHash,
						common.BytesToAddress(contextBytes), input, output, frame.DirectReverted,
					)
					if decodeErr != nil {
						return decodeErr
					}
					if found {
						frame.Decoding = decoding
						continue
					}
				}
			}
		}
		if frame.Execution == nil || frame.Execution.Address == "" || frame.Input == nil {
			frame.Decoding.Status = "unknown"
			frame.Decoding.Warning = "call frame has no exact execution code identity or calldata"
			continue
		}
		targetBytes, err := decodeFixedHex(frame.Execution.Address, common.AddressLength)
		if err != nil {
			return ErrCorruptData
		}
		target := common.BytesToAddress(targetBytes)
		path := tracePathText(frame.Path)
		storedCall := persisted[path+"\x00"+"trace_calldata"]
		storedRevert := persisted[path+"\x00"+"trace_revert"]
		if storedCall != nil && storedCall.strongFor(target) {
			decoding, err := storedCall.publicCall(frame.DirectReverted)
			if err != nil {
				return err
			}
			if frame.DirectReverted && storedRevert != nil && storedRevert.strongFor(target) {
				decoding.Revert, err = storedRevert.publicRevert()
				if err != nil {
					return err
				}
			}
			frame.Decoding = decoding
			continue
		}
		registryResult, ok := loaded[target]
		if !ok {
			if len(loaded) >= maxReadTimeTraceABIIdentities {
				frame.Decoding.Warning = "read-time ABI identity limit exceeded"
				continue
			}
			registryResult, err = loadTraceRegistry(ctx, tx, identity.ChainID, blockNumber, blockHash, target)
			if err != nil {
				return err
			}
			loaded[target] = registryResult
		}
		if registryResult.registry == nil {
			continue
		}
		input, err := decodeTraceData(*frame.Input)
		if err != nil {
			return ErrCorruptData
		}
		var output []byte
		if frame.Output != nil {
			output, err = decodeTraceData(*frame.Output)
			if err != nil {
				return ErrCorruptData
			}
		}
		decoded := registryResult.registry.DecodeCall(
			registryResult.identity, input, output, frame.DirectReverted,
		)
		frame.Decoding = publicTraceCall(decoded)
		if frame.DirectReverted {
			reverted := registryResult.registry.DecodeRevert(registryResult.identity, output)
			frame.Decoding.Revert = publicTraceRevert(reverted)
		}
	}
	return nil
}

func (catalog *Postgres) decorateConstructorFrame(
	ctx context.Context, tx *sql.Tx, identity traceIdentity, frame *TraceFrame,
	persisted map[string]*persistedTraceDecoding,
) error {
	frame.Decoding = &TraceCallDecoding{
		Kind: "constructor", Status: "unavailable", Inputs: []ABIValue{},
		OutputStatus: string(enrich.ReturnNotApplicable), Outputs: []ABIValue{},
		Candidates: []string{}, Warning: "no exact verified creation match is available",
	}
	if frame.DirectReverted && frame.Output != nil {
		output, err := decodeTraceData(*frame.Output)
		if err != nil {
			return ErrCorruptData
		}
		frame.Decoding.Revert = publicTraceRevert(enrich.NewABIRegistry().DecodeBuiltinRevert(output))
	}
	if frame.CreatedAddress == nil || frame.Input == nil {
		return nil
	}
	targetBytes, err := decodeFixedHex(*frame.CreatedAddress, common.AddressLength)
	if err != nil {
		return ErrCorruptData
	}
	target := common.BytesToAddress(targetBytes)
	path := tracePathText(frame.Path)
	if stored := persisted[path+"\x00"+"trace_constructor"]; stored != nil && stored.strongFor(target) {
		decoding, err := stored.publicConstructor()
		if err != nil {
			return err
		}
		frame.Decoding = decoding
		return nil
	}
	blockNumber, err := strconv.ParseUint(identity.BlockNumber, 10, 64)
	if err != nil {
		return ErrCorruptData
	}
	blockHash, err := decodeFixedHex(identity.BlockHash, common.HashLength)
	if err != nil {
		return ErrCorruptData
	}
	registry, abiIdentity, arguments, warning, err := loadExactConstructorRegistry(
		ctx, tx, identity.ChainID, blockNumber, blockHash, target, *frame.Input,
	)
	if err != nil {
		return err
	}
	if warning != "" {
		frame.Decoding.Status = "malformed"
		frame.Decoding.Warning = warning
		return nil
	}
	if registry == nil {
		return nil
	}
	if frame.DirectReverted && frame.Output != nil {
		output, err := decodeTraceData(*frame.Output)
		if err != nil {
			return ErrCorruptData
		}
		frame.Decoding.Revert = publicTraceRevert(registry.DecodeRevert(abiIdentity, output))
	}
	if frame.Reverted {
		return nil
	}
	decoded := registry.DecodeConstructor(abiIdentity, arguments)
	revert := frame.Decoding.Revert
	frame.Decoding = publicTraceConstructor(decoded)
	frame.Decoding.Revert = revert
	return nil
}

func loadExactConstructorRegistry(
	ctx context.Context, tx *sql.Tx, chainID string, blockNumber uint64,
	blockHash []byte, address common.Address, initcode string,
) (*enrich.ABIRegistry, enrich.ABIIdentity, []byte, string, error) {
	var codeHash, abiJSON, arguments []byte
	var validFromText string
	var validTo sql.NullString
	err := tx.QueryRowContext(ctx, dbgen.CatalogExactConstructorArtifact, chainID, strconv.FormatUint(blockNumber, 10), blockHash, address[:]).Scan(&codeHash, &abiJSON, &arguments, &validFromText, &validTo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, enrich.ABIIdentity{}, nil, "", nil
	}
	if err != nil {
		return nil, enrich.ABIIdentity{}, nil, "", fmt.Errorf("query exact constructor artifact: %w", err)
	}
	if len(codeHash) != common.HashLength {
		return nil, enrich.ABIIdentity{}, nil, "", ErrCorruptData
	}
	input, err := decodeTraceData(initcode)
	if err != nil {
		return nil, enrich.ABIIdentity{}, nil, "", ErrCorruptData
	}
	if !bytes.HasSuffix(input, arguments) {
		return nil, enrich.ABIIdentity{}, nil, "verified constructor arguments are not an exact initcode suffix", nil
	}
	if err := validateReadTimeConstructorArguments(abiJSON, arguments); err != nil {
		return nil, enrich.ABIIdentity{}, nil, "verified constructor arguments do not re-encode exactly", nil
	}
	validFrom, err := strconv.ParseUint(validFromText, 10, 64)
	if err != nil {
		return nil, enrich.ABIIdentity{}, nil, "", ErrCorruptData
	}
	var validToBlock *uint64
	if validTo.Valid {
		value, err := strconv.ParseUint(validTo.String, 10, 64)
		if err != nil {
			return nil, enrich.ABIIdentity{}, nil, "", ErrCorruptData
		}
		validToBlock = &value
	}
	abiIdentity := enrich.ABIIdentity{
		ChainID: chainID, Address: address, CodeHash: common.BytesToHash(codeHash),
		BlockNumber: blockNumber, BlockHash: common.BytesToHash(blockHash),
	}
	registry := enrich.NewABIRegistry()
	if err := registry.RegisterJSON(enrich.ABIBinding{
		Identity: abiIdentity, Source: enrich.ABISourceVerified,
		SourceAddress: address, SourceCodeHash: abiIdentity.CodeHash,
		ValidFromBlock: validFrom, ValidToBlock: validToBlock,
	}, abiJSON); err != nil {
		return nil, enrich.ABIIdentity{}, nil, "verified constructor ABI is malformed", nil
	}
	return registry, abiIdentity, arguments, "", nil
}

func validateReadTimeConstructorArguments(document, arguments []byte) error {
	parsed, err := gethabi.JSON(strings.NewReader(string(document)))
	if err != nil {
		return err
	}
	values, err := parsed.Constructor.Inputs.Unpack(arguments)
	if err != nil {
		return err
	}
	reencoded, err := parsed.Constructor.Inputs.Pack(values...)
	if err != nil {
		return err
	}
	if !bytes.Equal(reencoded, arguments) {
		return errors.New("constructor argument round trip mismatch")
	}
	return nil
}

func publicTraceConstructor(decoded enrich.DecodeResult) *TraceCallDecoding {
	result := &TraceCallDecoding{
		Kind: "constructor", Status: string(decoded.Status), FunctionName: decoded.Name,
		Signature: decoded.Signature, Inputs: publicABIValues(decoded.Arguments),
		OutputStatus: string(enrich.ReturnNotApplicable), Outputs: []ABIValue{},
		Candidates: append([]string(nil), decoded.Candidates...),
		ABISource:  publicDecodeSource(decoded), Confidence: string(decoded.Confidence),
		Warning: decoded.Warning,
	}
	if decoded.Status == enrich.DecodeAmbiguous {
		result.FunctionName, result.Signature = "", ""
		result.Inputs, result.ABISource = []ABIValue{}, nil
	}
	return result
}

func (catalog *Postgres) attachTraceExecutions(
	ctx context.Context, tx *sql.Tx, identity traceIdentity, trace *TransactionTrace,
) error {
	needsProjection := false
	for index := range trace.Frames {
		if trace.Frames[index].Execution == nil {
			needsProjection = true
			break
		}
	}
	if !needsProjection {
		return nil
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogTransactionTraceExecution, identity.ChainID, identity.BlockNumber, mustDecodeHash(identity.BlockHash),
		mustDecodeHash(identity.TransactionHash), catalog.options.MaxTraceFrames+1,
	)
	if err != nil {
		return fmt.Errorf("query trace execution projections: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	byPath := make(map[string]*TraceExecution)
	for rows.Next() {
		var path, resolution string
		var contextAddress, executionAddress, codeHash []byte
		if err := rows.Scan(&path, &contextAddress, &executionAddress, &codeHash, &resolution); err != nil {
			return fmt.Errorf("scan trace execution projection: %w", err)
		}
		context, err := optionalChecksumAddress(contextAddress)
		if err != nil || context == nil {
			return ErrCorruptData
		}
		item := &TraceExecution{ContextAddress: *context, Resolution: resolution}
		if len(executionAddress) != 0 {
			address, err := optionalChecksumAddress(executionAddress)
			if err != nil || address == nil {
				return ErrCorruptData
			}
			item.Address = *address
		}
		if len(codeHash) != 0 {
			item.CodeHash, err = lowerHex(codeHash)
			if err != nil {
				return ErrCorruptData
			}
		}
		if !validTraceExecution(item) {
			return ErrCorruptData
		}
		byPath[path] = item
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate trace execution projections: %w", err)
	}
	for index := range trace.Frames {
		path := tracePathText(trace.Frames[index].Path)
		item, exists := byPath[path]
		if !exists {
			return ErrCorruptData
		}
		trace.Frames[index].Execution = item
	}
	return nil
}

func validTraceExecution(value *TraceExecution) bool {
	if value == nil || value.ContextAddress == "" {
		return false
	}
	switch value.Resolution {
	case "direct", "eip7702_delegate":
		return value.Address != "" && value.CodeHash != ""
	case "empty", "not_applicable":
		return value.Address == "" && value.CodeHash == ""
	case "unavailable":
		return value.CodeHash == ""
	default:
		return false
	}
}

func callLikeTraceType(value string) bool {
	switch value {
	case "CALL", "STATICCALL", "DELEGATECALL", "CALLCODE":
		return true
	default:
		return false
	}
}

func directVerifiedAddressTraceFallback(frame *TraceFrame) bool {
	if frame == nil || frame.Execution == nil || frame.Input == nil ||
		frame.Execution.Resolution != "unavailable" || frame.Execution.Address != "" {
		return false
	}
	return frame.CallType == "CALL" || frame.CallType == "STATICCALL"
}

func decodeVerifiedAddressSelectorTraceCall(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	input, output []byte,
	directReverted bool,
) (*TraceCallDecoding, bool, error) {
	selection, err := loadVerifiedAddressSelectorSelection(
		ctx, tx, chainID, blockNumber, blockHash, address, input,
	)
	if err != nil {
		return nil, false, err
	}
	if selection.ambiguous {
		outputStatus := string(enrich.ReturnUnknown)
		if directReverted {
			outputStatus = string(enrich.ReturnNotApplicable)
		}
		decoding := &TraceCallDecoding{
			Kind: "function", Status: "ambiguous", Inputs: []ABIValue{},
			OutputStatus: outputStatus, Outputs: []ABIValue{},
			Candidates: selection.candidates,
			Warning:    "multiple verified address selector candidates decode this call frame",
		}
		if directReverted {
			decoding.Revert = publicTraceRevert(enrich.NewABIRegistry().DecodeBuiltinRevert(output))
		}
		return decoding, true, nil
	}
	if selection.match == nil {
		return nil, false, nil
	}
	decoded := selection.match.registry.DecodeCall(
		selection.match.identity, input, output, directReverted,
	)
	if decoded.Input.Status != enrich.DecodeDecoded ||
		decoded.Input.Signature != selection.match.input.Signature {
		return nil, false, ErrCorruptData
	}
	result := publicTraceCall(decoded)
	const warning = "decoded from the exact verified address range because execution identity is unavailable"
	if result.Warning == "" {
		result.Warning = warning
	} else {
		result.Warning += "; " + warning
	}
	if directReverted {
		result.Revert = publicTraceRevert(
			selection.match.registry.DecodeRevert(selection.match.identity, output),
		)
	}
	return result, true, nil
}

type traceRegistryResult struct {
	identity   enrich.ABIIdentity
	registry   *enrich.ABIRegistry
	candidates []logABICandidate
}

func loadTraceRegistry(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
) (traceRegistryResult, error) {
	identity, candidates, err := loadLogABICandidates(ctx, tx, chainID, blockNumber, blockHash, address)
	if err != nil {
		return traceRegistryResult{}, err
	}
	if len(candidates) == 0 {
		return traceRegistryResult{identity: identity}, nil
	}
	if len(candidates) > maxReadTimeLogABICandidates {
		return traceRegistryResult{identity: identity, candidates: candidates}, nil
	}
	registry, err := traceRegistryForCandidates(identity, candidates)
	if err != nil {
		if errors.Is(err, ErrCorruptData) {
			return traceRegistryResult{}, err
		}
		return traceRegistryResult{identity: identity, candidates: candidates}, nil
	}
	return traceRegistryResult{identity: identity, registry: registry, candidates: candidates}, nil
}

func loadTraceRegistryForCodeHash(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockNumber uint64,
	blockHash []byte,
	address common.Address,
	codeHash common.Hash,
) (traceRegistryResult, error) {
	identity, candidates, err := loadLogABICandidatesForCodeHash(
		ctx, tx, chainID, blockNumber, blockHash, address, &codeHash,
	)
	if err != nil {
		return traceRegistryResult{}, err
	}
	if identity.CodeHash != codeHash {
		return traceRegistryResult{}, ErrCorruptData
	}
	if len(candidates) == 0 {
		return traceRegistryResult{identity: identity}, nil
	}
	if len(candidates) > maxReadTimeLogABICandidates {
		return traceRegistryResult{identity: identity, candidates: candidates}, nil
	}
	registry, err := traceRegistryForCandidates(identity, candidates)
	if err != nil {
		if errors.Is(err, ErrCorruptData) {
			return traceRegistryResult{}, err
		}
		return traceRegistryResult{identity: identity, candidates: candidates}, nil
	}
	return traceRegistryResult{identity: identity, registry: registry, candidates: candidates}, nil
}

func traceRegistryForCandidates(
	identity enrich.ABIIdentity,
	candidates []logABICandidate,
) (*enrich.ABIRegistry, error) {
	registry := enrich.NewABIRegistry()
	for _, candidate := range candidates {
		var validTo *uint64
		if candidate.validTo.Valid {
			value, err := strconv.ParseUint(candidate.validTo.String, 10, 64)
			if err != nil {
				return nil, ErrCorruptData
			}
			validTo = &value
		}
		if err := registry.RegisterJSON(enrich.ABIBinding{
			Identity: identity, Source: candidate.source,
			SourceAddress: candidate.address, SourceCodeHash: candidate.codeHash,
			ValidFromBlock: candidate.validFrom, ValidToBlock: validTo,
		}, candidate.abi); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func loadPersistedTraceDecodings(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockHash, transactionHash []byte,
) (map[string]*persistedTraceDecoding, error) {
	rows, err := tx.QueryContext(ctx, dbgen.CatalogTransactionTraceDecodings, chainID, blockHash, transactionHash)
	if err != nil {
		return nil, fmt.Errorf("query transaction trace ABI decodings: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	result := make(map[string]*persistedTraceDecoding)
	for rows.Next() {
		item := &persistedTraceDecoding{}
		if err := rows.Scan(
			&item.objectKind, &item.path, &item.status, &item.signature,
			&item.source, &item.confidence, &item.arguments, &item.candidates,
			&item.warning, &item.targetAddress, &item.targetCodeHash,
			&item.sourceAddress, &item.sourceCodeHash, &item.returnStatus, &item.returns,
		); err != nil {
			return nil, fmt.Errorf("scan transaction trace ABI decoding: %w", err)
		}
		key := item.path + "\x00" + item.objectKind
		if _, duplicate := result[key]; duplicate {
			return nil, ErrCorruptData
		}
		result[key] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transaction trace ABI decodings: %w", err)
	}
	return result, nil
}

func (value *persistedTraceDecoding) strongFor(target common.Address) bool {
	return value != nil && value.status.Valid &&
		(value.confidence.String == "verified" || value.confidence.String == "high") &&
		len(value.targetAddress) == common.AddressLength && common.BytesToAddress(value.targetAddress) == target
}

func (value *persistedTraceDecoding) publicCall(directReverted bool) (*TraceCallDecoding, error) {
	inputs, err := decodeStoredABIValues(value.arguments)
	if err != nil {
		return nil, err
	}
	outputs, err := decodeStoredABIValues(value.returns)
	if err != nil {
		return nil, err
	}
	candidates, err := decodeStoredCandidates(value.candidates)
	if err != nil {
		return nil, err
	}
	result := &TraceCallDecoding{
		Kind:   "function",
		Status: value.status.String, Signature: value.signature.String,
		Inputs: inputs, Outputs: outputs, Candidates: candidates,
		OutputStatus: value.returnStatus.String, Confidence: value.confidence.String,
		Warning: value.warning.String,
	}
	result.FunctionName = signatureName(result.Signature)
	result.ABISource, err = value.publicSource()
	if err != nil {
		return nil, err
	}
	if directReverted {
		result.OutputStatus = string(enrich.ReturnNotApplicable)
		result.Outputs = []ABIValue{}
	}
	if result.Status == string(enrich.DecodeAmbiguous) {
		result.FunctionName, result.Signature = "", ""
		result.Inputs, result.Outputs = []ABIValue{}, []ABIValue{}
		if !directReverted {
			result.OutputStatus = string(enrich.ReturnUnknown)
		}
		result.ABISource = nil
	}
	return result, nil
}

func (value *persistedTraceDecoding) publicConstructor() (*TraceCallDecoding, error) {
	inputs, err := decodeStoredABIValues(value.arguments)
	if err != nil {
		return nil, err
	}
	candidates, err := decodeStoredCandidates(value.candidates)
	if err != nil {
		return nil, err
	}
	result := &TraceCallDecoding{
		Kind: "constructor", Status: value.status.String,
		FunctionName: "constructor", Signature: value.signature.String,
		Inputs: inputs, OutputStatus: string(enrich.ReturnNotApplicable), Outputs: []ABIValue{},
		Candidates: candidates, Confidence: value.confidence.String, Warning: value.warning.String,
	}
	result.ABISource, err = value.publicSource()
	if err != nil {
		return nil, err
	}
	if result.Status == string(enrich.DecodeAmbiguous) {
		result.FunctionName, result.Signature = "", ""
		result.Inputs, result.ABISource = []ABIValue{}, nil
	}
	return result, nil
}

func (value *persistedTraceDecoding) publicRevert() (*TraceRevertDecoding, error) {
	arguments, err := decodeStoredABIValues(value.arguments)
	if err != nil {
		return nil, err
	}
	candidates, err := decodeStoredCandidates(value.candidates)
	if err != nil {
		return nil, err
	}
	source, err := value.publicSource()
	if err != nil {
		return nil, err
	}
	result := &TraceRevertDecoding{
		Status: value.status.String, ErrorName: signatureName(value.signature.String),
		Signature: value.signature.String, Arguments: arguments, Candidates: candidates,
		ABISource: source, Confidence: value.confidence.String, Warning: value.warning.String,
	}
	if result.Status == string(enrich.DecodeAmbiguous) {
		result.ErrorName, result.Signature = "", ""
		result.Arguments, result.ABISource = []ABIValue{}, nil
	}
	return result, nil
}

func (value *persistedTraceDecoding) publicSource() (*ABISource, error) {
	if !value.source.Valid {
		return nil, nil
	}
	kind := mapStoredABISource(value.source.String)
	if kind == "" {
		return nil, ErrCorruptData
	}
	result := &ABISource{Kind: kind}
	if kind == "builtin" {
		return result, nil
	}
	address, codeHash := value.sourceAddress, value.sourceCodeHash
	if len(address) == 0 && kind != "proxy_implementation" {
		address, codeHash = value.targetAddress, value.targetCodeHash
	}
	if len(address) != common.AddressLength || len(codeHash) != common.HashLength {
		return nil, ErrCorruptData
	}
	result.Address = common.BytesToAddress(address).Hex()
	result.CodeHash = common.BytesToHash(codeHash).Hex()
	return result, nil
}

func decodeStoredABIValues(data []byte) ([]ABIValue, error) {
	if len(data) == 0 {
		return []ABIValue{}, nil
	}
	if !json.Valid(data) {
		return nil, ErrCorruptData
	}
	var stored []struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, ErrCorruptData
	}
	result := make([]ABIValue, len(stored))
	for index, item := range stored {
		result[index] = ABIValue{Name: item.Name, Type: item.Type, Value: item.Value}
	}
	return result, nil
}

func decodeStoredCandidates(data []byte) ([]string, error) {
	if len(data) == 0 {
		return []string{}, nil
	}
	if !json.Valid(data) {
		return nil, ErrCorruptData
	}
	var result []string
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, ErrCorruptData
	}
	return result, nil
}

func publicTraceCall(decoded enrich.CallDecodeResult) *TraceCallDecoding {
	warning := decoded.Input.Warning
	if decoded.Warning != "" {
		if warning == "" {
			warning = decoded.Warning
		} else {
			warning += "; " + decoded.Warning
		}
	}
	result := &TraceCallDecoding{
		Kind:   "function",
		Status: string(decoded.Input.Status), FunctionName: decoded.Input.Name,
		Signature: decoded.Input.Signature, Inputs: publicABIValues(decoded.Input.Arguments),
		OutputStatus: string(decoded.ReturnStatus), Outputs: publicABIValues(decoded.Returns),
		Candidates: append([]string(nil), decoded.Input.Candidates...),
		Confidence: string(decoded.Input.Confidence), Warning: warning,
	}
	result.ABISource = publicDecodeSource(decoded.Input)
	if decoded.Input.Status == enrich.DecodeAmbiguous {
		result.FunctionName, result.Signature = "", ""
		result.Inputs, result.Outputs = []ABIValue{}, []ABIValue{}
		if decoded.ReturnStatus != enrich.ReturnNotApplicable {
			result.OutputStatus = string(enrich.ReturnUnknown)
		}
		result.ABISource = nil
	}
	return result
}

func publicTraceRevert(decoded enrich.DecodeResult) *TraceRevertDecoding {
	result := &TraceRevertDecoding{
		Status: string(decoded.Status), ErrorName: decoded.Name, Signature: decoded.Signature,
		Arguments: publicABIValues(decoded.Arguments), Candidates: append([]string(nil), decoded.Candidates...),
		ABISource: publicDecodeSource(decoded), Confidence: string(decoded.Confidence), Warning: decoded.Warning,
	}
	if decoded.Status == enrich.DecodeAmbiguous {
		result.ErrorName, result.Signature = "", ""
		result.Arguments, result.ABISource = []ABIValue{}, nil
	}
	return result
}

func publicDecodeSource(decoded enrich.DecodeResult) *ABISource {
	kind := mapDecodeABISource(decoded.Source)
	if decoded.Source == enrich.ABISourceBuiltin {
		return &ABISource{Kind: "builtin"}
	}
	if kind == "" || decoded.SourceAddress == (common.Address{}) || decoded.SourceCodeHash == (common.Hash{}) {
		return nil
	}
	return &ABISource{Kind: kind, Address: decoded.SourceAddress.Hex(), CodeHash: decoded.SourceCodeHash.Hex()}
}

func publicABIValues(values []enrich.DecodedArgument) []ABIValue {
	result := make([]ABIValue, len(values))
	for index, value := range values {
		result[index] = ABIValue{Name: value.Name, Type: value.Type, Value: value.Value}
	}
	return result
}

func unavailableTraceCallDecoding(directReverted bool, warning string) *TraceCallDecoding {
	outputStatus := string(enrich.ReturnUnavailable)
	if directReverted {
		outputStatus = string(enrich.ReturnNotApplicable)
	}
	result := &TraceCallDecoding{
		Kind:   "function",
		Status: "unavailable", Inputs: []ABIValue{}, OutputStatus: outputStatus,
		Outputs: []ABIValue{}, Candidates: []string{}, Warning: warning,
	}
	if directReverted {
		result.Revert = &TraceRevertDecoding{
			Status: "unavailable", Arguments: []ABIValue{}, Candidates: []string{}, Warning: warning,
		}
	}
	return result
}

func notApplicableTraceCallDecoding(warning string) *TraceCallDecoding {
	return &TraceCallDecoding{
		Kind: "function", Status: "not_applicable", Inputs: []ABIValue{},
		OutputStatus: string(enrich.ReturnNotApplicable), Outputs: []ABIValue{},
		Candidates: []string{}, Warning: warning,
	}
}

func signatureName(signature string) string {
	if open := strings.IndexByte(signature, '('); open > 0 {
		return signature[:open]
	}
	return ""
}

func decodeTraceData(value string) ([]byte, error) {
	if len(value) < 2 || !strings.HasPrefix(value, "0x") || len(value)%2 != 0 {
		return nil, errors.New("invalid trace data")
	}
	return hex.DecodeString(value[2:])
}
