package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/enrich"
)

const maxReadTimeTraceABIIdentities = 1024

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
		if callLikeTraceType(frame.CallType) && frame.To != nil && frame.Input != nil &&
			(len(*frame.Input) >= 10 || frame.DirectReverted && frame.Output != nil && len(*frame.Output) >= 10) {
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
	for index := range trace.Frames {
		frame := &trace.Frames[index]
		if !callLikeTraceType(frame.CallType) {
			continue
		}
		frame.Decoding = unavailableTraceCallDecoding(frame.DirectReverted, "no ABI is available for the call target at this block")
		if frame.To == nil || frame.Input == nil {
			frame.Decoding.Status = "unknown"
			frame.Decoding.Warning = "call frame has no decodable target or calldata"
			continue
		}
		if len(*frame.Input) < 10 && (!frame.DirectReverted || frame.Output == nil || len(*frame.Output) < 10) {
			frame.Decoding.Status = "unknown"
			frame.Decoding.Warning = "calldata has no function selector"
			continue
		}
		targetBytes, err := decodeFixedHex(*frame.To, common.AddressLength)
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

func callLikeTraceType(value string) bool {
	switch value {
	case "CALL", "STATICCALL", "DELEGATECALL", "CALLCODE":
		return true
	default:
		return false
	}
}

type traceRegistryResult struct {
	identity enrich.ABIIdentity
	registry *enrich.ABIRegistry
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
		return traceRegistryResult{identity: identity}, nil
	}
	registry := enrich.NewABIRegistry()
	for _, candidate := range candidates {
		var validTo *uint64
		if candidate.validTo.Valid {
			value, err := strconv.ParseUint(candidate.validTo.String, 10, 64)
			if err != nil {
				return traceRegistryResult{}, ErrCorruptData
			}
			validTo = &value
		}
		if err := registry.RegisterJSON(enrich.ABIBinding{
			Identity: identity, Source: candidate.source,
			SourceAddress: candidate.address, SourceCodeHash: candidate.codeHash,
			ValidFromBlock: candidate.validFrom, ValidToBlock: validTo,
		}, candidate.abi); err != nil {
			return traceRegistryResult{identity: identity}, nil
		}
	}
	return traceRegistryResult{identity: identity, registry: registry}, nil
}

func loadPersistedTraceDecodings(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	blockHash, transactionHash []byte,
) (map[string]*persistedTraceDecoding, error) {
	rows, err := tx.QueryContext(ctx, transactionTraceDecodingsSQL, chainID, blockHash, transactionHash)
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

const transactionTraceDecodingsSQL = `
SELECT decoding.object_kind, decoding.object_index, decoding.status,
       decoding.signature, decoding.source, decoding.confidence,
       decoding.arguments, decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       decoding.return_status, decoding.return_arguments
FROM abi_decodings AS decoding
WHERE decoding.chain_id = $1::numeric
  AND decoding.block_hash = $2
  AND decoding.transaction_hash = $3
  AND decoding.object_kind IN ('trace_calldata', 'trace_revert')
  AND decoding.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = decoding.chain_id
        AND published.block_hash = decoding.block_hash
        AND published.stage = 'abi'
        AND published.stage_version = 2
        AND published.state = 'complete'
  )
ORDER BY decoding.object_index, decoding.object_kind`
