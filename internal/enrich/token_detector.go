package enrich

import (
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/islishude/etherview/internal/ethrpc"
	"golang.org/x/crypto/sha3"
)

type TokenLogEvidence struct {
	ERC20        bool
	ERC721       bool
	ERC1155      bool
	ERC721Or1155 bool
}

func (evidence TokenLogEvidence) empty() bool {
	return !evidence.ERC20 && !evidence.ERC721 && !evidence.ERC1155 && !evidence.ERC721Or1155
}

type TokenDetectionRequest struct {
	Job      Job
	Address  Address
	Evidence TokenLogEvidence
}

func (request TokenDetectionRequest) validate() error {
	if err := request.Job.Validate(); err != nil {
		return err
	}
	if request.Job.Stage != TokenStage {
		return errors.New("token detection requires the token stage")
	}
	if request.Address == (Address{}) {
		return errors.New("token detection address is zero")
	}
	if request.Evidence.empty() {
		return errors.New("token detection requires exact log-layout evidence")
	}
	return nil
}

type TokenMetadataState string

const (
	TokenMetadataComplete    TokenMetadataState = "complete"
	TokenMetadataUnavailable TokenMetadataState = "unavailable"
)

type TokenDetection struct {
	Standard      TokenStandard
	Confidence    Confidence
	CodeHash      Word
	Name          *string
	Symbol        *string
	Decimals      *uint8
	TotalSupply   *string
	MetadataState TokenMetadataState
}

func (detection TokenDetection) validate() error {
	switch detection.Standard {
	case TokenERC20, TokenERC721, TokenERC1155:
		if confidenceRank(detection.Confidence) < confidenceRank(ConfidenceInferred) {
			return errors.New("classified token detection has insufficient confidence")
		}
	case TokenStandardUnknown:
		if detection.Confidence != ConfidenceGuess {
			return errors.New("unknown token detection must use guess confidence")
		}
	default:
		return errors.New("token detection standard is invalid")
	}
	if detection.CodeHash.IsZero() {
		return errors.New("token detection code hash is zero")
	}
	if detection.Name != nil && len(*detection.Name) > 4096 || detection.Symbol != nil && len(*detection.Symbol) > 4096 {
		return errors.New("token metadata text exceeds the persistence limit")
	}
	if detection.TotalSupply != nil {
		value, ok := new(big.Int).SetString(*detection.TotalSupply, 10)
		if !ok || value.Sign() < 0 || value.BitLen() > 256 || value.String() != *detection.TotalSupply {
			return errors.New("token total supply is not a canonical uint256")
		}
	}
	switch detection.MetadataState {
	case TokenMetadataComplete, TokenMetadataUnavailable:
	default:
		return errors.New("token metadata state is invalid")
	}
	return nil
}

type TokenDetector interface {
	Detect(context.Context, TokenDetectionRequest) (TokenDetection, error)
}

type TokenDetectorFunc func(context.Context, TokenDetectionRequest) (TokenDetection, error)

func (function TokenDetectorFunc) Detect(ctx context.Context, request TokenDetectionRequest) (TokenDetection, error) {
	if function == nil {
		return TokenDetection{}, errors.New("token detector function is nil")
	}
	return function(ctx, request)
}

// TokenBlockDetector keeps every state read for one immutable block on a
// single endpoint. Processors prefer this boundary over calling Detect once
// per contract so the pool can rotate only between blocks.
type TokenBlockDetector interface {
	TokenDetector
	DetectBlock(context.Context, Job, map[Address]TokenLogEvidence) (map[Address]TokenDetection, error)
}

type PoolTokenDetector struct {
	pool   *ethrpc.Pool
	limits TokenProbeLimits
}

func NewPoolTokenDetector(pool *ethrpc.Pool, limits TokenProbeLimits) (*PoolTokenDetector, error) {
	if pool == nil {
		return nil, errors.New("pool token detector requires an RPC pool")
	}
	limits.defaults()
	if limits.MaxCodeBytes <= 0 || limits.MaxReturnBytes < 32 || limits.MaxStringBytes <= 0 ||
		limits.MaxStringBytes > limits.MaxReturnBytes {
		return nil, errors.New("pool token detector limits are invalid")
	}
	return &PoolTokenDetector{pool: pool, limits: limits}, nil
}

func (detector *PoolTokenDetector) Detect(ctx context.Context, request TokenDetectionRequest) (TokenDetection, error) {
	if err := request.validate(); err != nil {
		return TokenDetection{}, Permanent(err)
	}
	results, err := detector.DetectBlock(ctx, request.Job, map[Address]TokenLogEvidence{
		request.Address: request.Evidence,
	})
	if err != nil {
		return TokenDetection{}, err
	}
	result, ok := results[request.Address]
	if !ok {
		return TokenDetection{}, Permanent(errors.New("pool token detector omitted requested address"))
	}
	return result, nil
}

func (detector *PoolTokenDetector) DetectBlock(ctx context.Context, job Job, evidence map[Address]TokenLogEvidence) (map[Address]TokenDetection, error) {
	if detector == nil || detector.pool == nil {
		return nil, errors.New("pool token detector is not configured")
	}
	if err := job.Validate(); err != nil {
		return nil, Permanent(err)
	}
	if job.Stage != TokenStage {
		return nil, Permanent(errors.New("pool token detector requires the token stage"))
	}
	addresses := sortedTokenAddresses(evidence)
	if len(addresses) == 0 {
		return map[Address]TokenDetection{}, nil
	}
	for _, address := range addresses {
		if address == (Address{}) || evidence[address].empty() {
			return nil, Permanent(errors.New("pool token detector received invalid block evidence"))
		}
	}
	endpoint, err := detector.pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		return nil, Unavailable(errors.New("state RPC endpoint is unavailable"))
	}
	blockDetector, err := NewRPCTokenDetector(endpoint.Client, detector.limits)
	if err != nil {
		return nil, err
	}
	results := make(map[Address]TokenDetection, len(addresses))
	for _, address := range addresses {
		result, detectErr := blockDetector.Detect(ctx, TokenDetectionRequest{
			Job: job, Address: address, Evidence: evidence[address],
		})
		if detectErr != nil {
			detector.pool.ReportFailure(endpoint.Name)
			return nil, detectErr
		}
		results[address] = result
	}
	detector.pool.ReportSuccess(endpoint.Name)
	return results, nil
}

type TokenProbeLimits struct {
	MaxCodeBytes   int
	MaxReturnBytes int
	MaxStringBytes int
}

func (limits *TokenProbeLimits) defaults() {
	if limits.MaxCodeBytes <= 0 {
		limits.MaxCodeBytes = 1 << 20
	}
	if limits.MaxReturnBytes <= 0 {
		limits.MaxReturnBytes = 4096
	}
	if limits.MaxStringBytes <= 0 {
		limits.MaxStringBytes = 1024
	}
}

type RPCTokenDetector struct {
	caller ethrpc.Caller
	limits TokenProbeLimits
}

func NewRPCTokenDetector(caller ethrpc.Caller, limits TokenProbeLimits) (*RPCTokenDetector, error) {
	if caller == nil {
		return nil, errors.New("RPC token detector requires a caller")
	}
	limits.defaults()
	if limits.MaxCodeBytes <= 0 || limits.MaxReturnBytes < 32 || limits.MaxStringBytes <= 0 ||
		limits.MaxStringBytes > limits.MaxReturnBytes {
		return nil, errors.New("RPC token detector limits are invalid")
	}
	return &RPCTokenDetector{caller: caller, limits: limits}, nil
}

type tokenProbeResults struct {
	erc721, erc1155 bool
	name, symbol    *string
	decimals        *uint8
	totalSupply     *string
	nameOK          bool
	symbolOK        bool
	decimalsOK      bool
	totalSupplyOK   bool
}

func (detector *RPCTokenDetector) Detect(ctx context.Context, request TokenDetectionRequest) (TokenDetection, error) {
	if detector == nil || detector.caller == nil {
		return TokenDetection{}, errors.New("RPC token detector is not configured")
	}
	if err := request.validate(); err != nil {
		return TokenDetection{}, Permanent(err)
	}
	blockReference := map[string]any{
		"blockHash": request.Job.BlockHash.String(), "requireCanonical": true,
	}
	var encodedCode ethrpc.Data
	if err := detector.caller.Call(ctx, "eth_getCode", []any{request.Address.String(), blockReference}, &encodedCode); err != nil {
		return TokenDetection{}, exactStateRPCError(ctx, "eth_getCode", err)
	}
	code, err := encodedCode.Bytes()
	if err != nil {
		return TokenDetection{}, Permanent(errors.New("eth_getCode returned invalid bytecode"))
	}
	if len(code) > detector.limits.MaxCodeBytes {
		return TokenDetection{}, Permanent(errors.New("contract bytecode exceeds token detection limit"))
	}
	detection := TokenDetection{
		Standard: TokenStandardUnknown, Confidence: ConfidenceGuess,
		CodeHash: codeHash(code), MetadataState: TokenMetadataUnavailable,
	}
	if len(code) == 0 {
		return detection, nil
	}
	probes, err := detector.probe(ctx, request.Address, blockReference)
	if err != nil {
		return TokenDetection{}, err
	}
	detection.Name, detection.Symbol = probes.name, probes.symbol
	detection.Decimals, detection.TotalSupply = probes.decimals, probes.totalSupply
	if probes.nameOK || probes.symbolOK || probes.decimalsOK || probes.totalSupplyOK {
		detection.MetadataState = TokenMetadataComplete
	}
	detection.Standard, detection.Confidence = classifyToken(request.Evidence, probes)
	return detection, nil
}

func (detector *RPCTokenDetector) probe(ctx context.Context, address Address, blockReference map[string]any) (tokenProbeResults, error) {
	var probes tokenProbeResults
	var err error
	if probes.erc721, err = detector.supportsInterface(ctx, address, blockReference, [4]byte{0x80, 0xac, 0x58, 0xcd}); err != nil {
		return tokenProbeResults{}, err
	}
	if probes.erc1155, err = detector.supportsInterface(ctx, address, blockReference, [4]byte{0xd9, 0xb6, 0x7a, 0x26}); err != nil {
		return tokenProbeResults{}, err
	}
	name, supported, err := detector.call(ctx, address, blockReference, SignatureSelector("name()"))
	if err != nil {
		return tokenProbeResults{}, err
	}
	if supported {
		if value, ok := decodeTokenString(name, detector.limits.MaxStringBytes); ok {
			probes.name, probes.nameOK = &value, true
		}
	}
	symbol, supported, err := detector.call(ctx, address, blockReference, SignatureSelector("symbol()"))
	if err != nil {
		return tokenProbeResults{}, err
	}
	if supported {
		if value, ok := decodeTokenString(symbol, detector.limits.MaxStringBytes); ok {
			probes.symbol, probes.symbolOK = &value, true
		}
	}
	decimals, supported, err := detector.call(ctx, address, blockReference, SignatureSelector("decimals()"))
	if err != nil {
		return tokenProbeResults{}, err
	}
	if supported {
		if value, ok := decodeTokenUint(decimals); ok && value.IsUint64() && value.Uint64() <= 255 {
			parsed := uint8(value.Uint64())
			probes.decimals, probes.decimalsOK = &parsed, true
		}
	}
	totalSupply, supported, err := detector.call(ctx, address, blockReference, SignatureSelector("totalSupply()"))
	if err != nil {
		return tokenProbeResults{}, err
	}
	if supported {
		if value, ok := decodeTokenUint(totalSupply); ok {
			parsed := value.String()
			probes.totalSupply, probes.totalSupplyOK = &parsed, true
		}
	}
	return probes, nil
}

func (detector *RPCTokenDetector) supportsInterface(ctx context.Context, address Address, blockReference map[string]any, interfaceID [4]byte) (bool, error) {
	input := make([]byte, 4+32)
	selector := SignatureSelector("supportsInterface(bytes4)")
	copy(input[:4], selector[:])
	// ABI fixed bytes are left-aligned in their 32-byte slot.
	copy(input[4:8], interfaceID[:])
	output, supported, err := detector.callData(ctx, address, blockReference, input)
	if err != nil || !supported {
		return false, err
	}
	if len(output) != 32 {
		return false, nil
	}
	for _, value := range output[:31] {
		if value != 0 {
			return false, nil
		}
	}
	return output[31] == 1, nil
}

func (detector *RPCTokenDetector) call(ctx context.Context, address Address, blockReference map[string]any, selector [4]byte) ([]byte, bool, error) {
	return detector.callData(ctx, address, blockReference, selector[:])
}

func (detector *RPCTokenDetector) callData(ctx context.Context, address Address, blockReference map[string]any, input []byte) ([]byte, bool, error) {
	request := map[string]any{"to": address.String(), "data": ethrpc.DataFromBytes(input).String()}
	var encoded ethrpc.Data
	err := detector.caller.Call(ctx, "eth_call", []any{request, blockReference}, &encoded)
	if err != nil {
		if executionReverted(err) {
			return nil, false, nil
		}
		return nil, false, exactStateRPCError(ctx, "eth_call", err)
	}
	output, err := encoded.Bytes()
	if err != nil || len(output) > detector.limits.MaxReturnBytes {
		return nil, false, nil
	}
	return output, true, nil
}

func executionReverted(err error) bool {
	var rpcError *ethrpc.RPCError
	if !errors.As(err, &rpcError) {
		return false
	}
	message := strings.ToLower(rpcError.Message)
	return rpcError.Code == 3 || strings.Contains(message, "execution reverted") || strings.Contains(message, "revert")
}

func classifyToken(evidence TokenLogEvidence, probes tokenProbeResults) (TokenStandard, Confidence) {
	if probes.erc721 && probes.erc1155 {
		return TokenStandardUnknown, ConfidenceGuess
	}
	if probes.erc721 {
		if evidence.ERC20 || evidence.ERC1155 {
			return TokenStandardUnknown, ConfidenceGuess
		}
		return TokenERC721, ConfidenceHigh
	}
	if probes.erc1155 {
		if evidence.ERC20 || evidence.ERC721 {
			return TokenStandardUnknown, ConfidenceGuess
		}
		return TokenERC1155, ConfidenceHigh
	}
	strongERC20 := probes.totalSupplyOK && (probes.decimalsOK || probes.nameOK || probes.symbolOK)
	if evidence.ERC20 && !evidence.ERC721 && !evidence.ERC1155 && strongERC20 {
		return TokenERC20, ConfidenceHigh
	}
	return TokenStandardUnknown, ConfidenceGuess
}

func decodeTokenUint(output []byte) (*big.Int, bool) {
	if len(output) != 32 {
		return nil, false
	}
	return new(big.Int).SetBytes(output), true
}

func decodeTokenString(output []byte, maximum int) (string, bool) {
	if len(output) == 32 {
		end := len(output)
		for end > 0 && output[end-1] == 0 {
			end--
		}
		value := output[:end]
		if len(value) > maximum || !utf8.Valid(value) {
			return "", false
		}
		return string(value), true
	}
	if len(output) < 64 || !zeroWordPrefix(output[:32]) || binary.BigEndian.Uint64(output[24:32]) != 32 || !zeroWordPrefix(output[32:64]) {
		return "", false
	}
	length := binary.BigEndian.Uint64(output[56:64])
	if length > uint64(maximum) || length > uint64(len(output)-64) {
		return "", false
	}
	padded := (length + 31) / 32 * 32
	if padded > uint64(len(output)-64) || len(output) != 64+int(padded) {
		return "", false
	}
	value := output[64 : 64+int(length)]
	for _, padding := range output[64+int(length):] {
		if padding != 0 {
			return "", false
		}
	}
	if !utf8.Valid(value) {
		return "", false
	}
	return string(value), true
}

func zeroWordPrefix(word []byte) bool {
	if len(word) != 32 {
		return false
	}
	for _, value := range word[:24] {
		if value != 0 {
			return false
		}
	}
	return true
}

func codeHash(code []byte) Word {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(code)
	var hash Word
	hasher.Sum(hash[:0])
	return hash
}
