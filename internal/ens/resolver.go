package ens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/metadata"
)

const OfficialUniversalResolverAddress = "0xeEeEEEeE14D718C2B47D9923Deab1335E144EeEe"

type Source string

const (
	SourceOfficial Source = "ens"
	SourceCustom   Source = "custom_ens"
)

type Outcome string

const (
	OutcomeResolved Outcome = "resolved"
	OutcomeNoRecord Outcome = "not_found"
)

const (
	CodeRPCUnavailable      = "rpc_unavailable"
	CodeCCIPUnavailable     = "ccip_unavailable"
	CodeCCIPSenderMismatch  = "ccip_sender_mismatch"
	CodeCCIPDepthExceeded   = "ccip_depth_exceeded"
	CodeInvalidResponse     = "invalid_response"
	CodeResolverNotContract = "resolver_not_contract"
	CodeResolverFailure     = "resolver_failure"
	CodeForwardMismatch     = "forward_mismatch"
	CodeSourceIdentity      = "source_identity_mismatch"
	CodeCustomDeployment    = "custom_deployment_unavailable"
)

type ResolutionError struct {
	Code string
}

func (err *ResolutionError) Error() string { return "ENS resolution unavailable" }

func (err *ResolutionError) CapabilityDetails() (capability, state, code string) {
	state = "failed"
	if err != nil && (err.Code == CodeRPCUnavailable || err.Code == CodeCCIPUnavailable) {
		state = "unavailable"
	}
	if err == nil {
		return "name", "failed", CodeResolverFailure
	}
	return "name", state, err.Code
}

func resolutionError(code string) error { return &ResolutionError{Code: code} }

type BlockRef struct {
	Number uint64
	Hash   common.Hash
}

type Profile struct {
	Source            Source
	Registry          common.Address
	UniversalResolver common.Address
	CoinType          *big.Int
	Gateways          []string
	Block             BlockRef
}

type ForwardResult struct {
	Outcome  Outcome
	Name     string
	Address  common.Address
	Resolver common.Address
}

type PrimaryResult struct {
	Outcome         Outcome
	Name            string
	Address         common.Address
	Resolver        common.Address
	ReverseResolver common.Address
}

type RPCCaller interface {
	CallContext(context.Context, any, string, ...any) error
}

type GatewayRequester interface {
	Request(context.Context, string, common.Address, []byte) ([]byte, error)
}

type Resolver struct {
	normalizer   *Normalizer
	gateway      GatewayRequester
	maxCCIPDepth int
}

func NewResolver(normalizer *Normalizer, gateway GatewayRequester, maxCCIPDepth int) (*Resolver, error) {
	if normalizer == nil {
		return nil, errors.New("ENS resolver normalizer is nil")
	}
	if maxCCIPDepth <= 0 || maxCCIPDepth > 8 {
		return nil, errors.New("ENS resolver CCIP depth must be between 1 and 8")
	}
	return &Resolver{normalizer: normalizer, gateway: gateway, maxCCIPDepth: maxCCIPDepth}, nil
}

func (resolver *Resolver) Forward(
	ctx context.Context,
	caller RPCCaller,
	profile Profile,
	rawName string,
) (ForwardResult, error) {
	name, err := resolver.normalizer.Normalize(ctx, rawName)
	if err != nil {
		return ForwardResult{}, err
	}
	return resolver.forwardNormalized(ctx, caller, profile, name)
}

func (resolver *Resolver) forwardNormalized(
	ctx context.Context,
	caller RPCCaller,
	profile Profile,
	name string,
) (ForwardResult, error) {
	if err := validateProfile(caller, profile); err != nil {
		return ForwardResult{}, err
	}
	wire, err := DNSWireFormat(name)
	if err != nil {
		return ForwardResult{}, err
	}
	node, err := Namehash(name)
	if err != nil {
		return ForwardResult{}, err
	}
	resolverCall, legacy, err := packAddressResolverCall(node, profile.CoinType)
	if err != nil {
		return ForwardResult{}, resolutionError(CodeInvalidResponse)
	}
	input, err := universalResolverABI.Pack("resolve", wire, resolverCall)
	if err != nil {
		return ForwardResult{}, resolutionError(CodeInvalidResponse)
	}
	output, err := resolver.callWithCCIP(ctx, caller, profile, input, 0)
	if err != nil {
		if state, classified, ok := classifyUniversalRevert(err); ok {
			if classified != nil {
				return ForwardResult{}, classified
			}
			return ForwardResult{Outcome: state, Name: name}, nil
		}
		return ForwardResult{}, err
	}
	values, err := universalResolverABI.Unpack("resolve", output)
	if err != nil || len(values) != 2 {
		return ForwardResult{}, resolutionError(CodeInvalidResponse)
	}
	data, ok := values[0].([]byte)
	if !ok {
		return ForwardResult{}, resolutionError(CodeInvalidResponse)
	}
	resolverAddress, ok := values[1].(common.Address)
	if !ok || resolverAddress == (common.Address{}) {
		return ForwardResult{}, resolutionError(CodeInvalidResponse)
	}
	address, found, err := decodeResolvedAddress(data, legacy)
	if err != nil {
		return ForwardResult{}, resolutionError(CodeInvalidResponse)
	}
	if !found {
		return ForwardResult{Outcome: OutcomeNoRecord, Name: name}, nil
	}
	return ForwardResult{
		Outcome: OutcomeResolved, Name: name, Address: address, Resolver: resolverAddress,
	}, nil
}

func (resolver *Resolver) Reverse(
	ctx context.Context,
	caller RPCCaller,
	profile Profile,
	address common.Address,
) (PrimaryResult, error) {
	if address == (common.Address{}) {
		return PrimaryResult{}, errors.New("ENS reverse address is zero")
	}
	if err := validateProfile(caller, profile); err != nil {
		return PrimaryResult{}, err
	}
	input, err := universalResolverABI.Pack("reverse", address.Bytes(), profile.CoinType)
	if err != nil {
		return PrimaryResult{}, resolutionError(CodeInvalidResponse)
	}
	output, err := resolver.callWithCCIP(ctx, caller, profile, input, 0)
	if err != nil {
		if state, classified, ok := classifyUniversalRevert(err); ok {
			if classified != nil {
				return PrimaryResult{}, classified
			}
			return PrimaryResult{Outcome: state, Address: address}, nil
		}
		return PrimaryResult{}, err
	}
	values, err := universalResolverABI.Unpack("reverse", output)
	if err != nil || len(values) != 3 {
		return PrimaryResult{}, resolutionError(CodeInvalidResponse)
	}
	name, ok := values[0].(string)
	if !ok {
		return PrimaryResult{}, resolutionError(CodeInvalidResponse)
	}
	if name == "" {
		return PrimaryResult{Outcome: OutcomeNoRecord, Address: address}, nil
	}
	normalized, normalizeErr := resolver.normalizer.Normalize(ctx, name)
	if normalizeErr != nil || normalized != name {
		return PrimaryResult{}, resolutionError(CodeInvalidResponse)
	}
	forwardResolver, ok := values[1].(common.Address)
	if !ok || forwardResolver == (common.Address{}) {
		return PrimaryResult{}, resolutionError(CodeInvalidResponse)
	}
	reverseResolver, ok := values[2].(common.Address)
	if !ok || reverseResolver == (common.Address{}) {
		return PrimaryResult{}, resolutionError(CodeInvalidResponse)
	}
	forward, err := resolver.forwardNormalized(ctx, caller, profile, normalized)
	if err != nil {
		return PrimaryResult{}, err
	}
	if forward.Outcome != OutcomeResolved || forward.Address != address || forward.Resolver != forwardResolver {
		return PrimaryResult{}, resolutionError(CodeForwardMismatch)
	}
	return PrimaryResult{
		Outcome: OutcomeResolved, Name: normalized, Address: address,
		Resolver: forwardResolver, ReverseResolver: reverseResolver,
	}, nil
}

func validateProfile(caller RPCCaller, profile Profile) error {
	if caller == nil {
		return errors.New("ENS RPC caller is nil")
	}
	if profile.Source != SourceOfficial && profile.Source != SourceCustom {
		return errors.New("ENS profile source is invalid")
	}
	if profile.UniversalResolver == (common.Address{}) || profile.Block.Hash == (common.Hash{}) ||
		profile.CoinType == nil || profile.CoinType.Sign() <= 0 || profile.CoinType.BitLen() > 256 ||
		len(profile.Gateways) > 4 {
		return errors.New("ENS profile is invalid")
	}
	if profile.Source == SourceCustom && profile.Registry == (common.Address{}) {
		return errors.New("custom ENS registry is invalid")
	}
	return nil
}

func packAddressResolverCall(node common.Hash, coinType *big.Int) ([]byte, bool, error) {
	if coinType.Cmp(big.NewInt(60)) == 0 {
		input, err := legacyAddressResolverABI.Pack("addr", node)
		return input, true, err
	}
	input, err := multicoinAddressResolverABI.Pack("addr", node, coinType)
	return input, false, err
}

func decodeResolvedAddress(data []byte, legacy bool) (common.Address, bool, error) {
	if len(data) == 0 {
		return common.Address{}, false, nil
	}
	selected := multicoinAddressResolverABI
	if legacy {
		selected = legacyAddressResolverABI
	}
	values, err := selected.Unpack("addr", data)
	if err == nil && len(values) == 1 {
		if legacy {
			address, ok := values[0].(common.Address)
			if !ok {
				return common.Address{}, false, errors.New("invalid address result")
			}
			return address, address != (common.Address{}), nil
		}
		encoded, ok := values[0].([]byte)
		if !ok {
			return common.Address{}, false, errors.New("invalid multicoin result")
		}
		data = encoded
	}
	if !legacy && len(data) == common.AddressLength {
		address := common.BytesToAddress(data)
		return address, address != (common.Address{}), nil
	}
	return common.Address{}, false, errors.New("invalid resolved address")
}

type rpcRevert struct{ data []byte }

func (err *rpcRevert) Error() string { return "ENS contract reverted" }

func (resolver *Resolver) callWithCCIP(
	ctx context.Context,
	caller RPCCaller,
	profile Profile,
	input []byte,
	depth int,
) ([]byte, error) {
	var output hexutil.Bytes
	selector := gethrpc.BlockNumberOrHashWithHash(profile.Block.Hash, true)
	request := map[string]any{"to": profile.UniversalResolver, "data": hexutil.Bytes(input)}
	err := caller.CallContext(ctx, &output, "eth_call", request, selector)
	if err == nil {
		return append([]byte(nil), output...), nil
	}
	revert, ok := extractRevertData(err)
	if !ok {
		return nil, resolutionError(CodeRPCUnavailable)
	}
	offchain, ok := decodeOffchainLookup(revert)
	if !ok {
		return nil, &rpcRevert{data: revert}
	}
	if depth >= resolver.maxCCIPDepth {
		return nil, resolutionError(CodeCCIPDepthExceeded)
	}
	if offchain.sender != profile.UniversalResolver {
		return nil, resolutionError(CodeCCIPSenderMismatch)
	}
	if resolver.gateway == nil || len(offchain.urls) == 0 {
		return nil, resolutionError(CodeCCIPUnavailable)
	}
	allowed := make(map[string]struct{}, len(profile.Gateways))
	for _, gateway := range profile.Gateways {
		allowed[gateway] = struct{}{}
	}
	var response []byte
	for _, gateway := range offchain.urls {
		if _, exists := allowed[gateway]; !exists {
			continue
		}
		response, err = resolver.gateway.Request(ctx, gateway, offchain.sender, offchain.callData)
		if err == nil {
			break
		}
	}
	if err != nil || len(response) == 0 {
		return nil, resolutionError(CodeCCIPUnavailable)
	}
	arguments, err := callbackArguments.Pack(response, offchain.extraData)
	if err != nil {
		return nil, resolutionError(CodeInvalidResponse)
	}
	callback := append(append([]byte(nil), offchain.callback...), arguments...)
	return resolver.callWithCCIP(ctx, caller, profile, callback, depth+1)
}

type offchainLookup struct {
	sender    common.Address
	urls      []string
	callData  []byte
	callback  []byte
	extraData []byte
}

func decodeOffchainLookup(data []byte) (offchainLookup, bool) {
	errorABI, exists := universalResolverABI.Errors["OffchainLookup"]
	if !exists || len(data) < 4 || !bytes.Equal(data[:4], errorABI.ID[:4]) {
		return offchainLookup{}, false
	}
	values, err := errorABI.Inputs.Unpack(data[4:])
	if err != nil || len(values) != 5 {
		return offchainLookup{}, false
	}
	sender, senderOK := values[0].(common.Address)
	urls, urlsOK := values[1].([]string)
	callData, callOK := values[2].([]byte)
	callback, callbackOK := values[3].([4]byte)
	extraData, extraOK := values[4].([]byte)
	if !senderOK || !urlsOK || !callOK || !callbackOK || !extraOK || len(urls) > 4 || len(callData) == 0 {
		return offchainLookup{}, false
	}
	return offchainLookup{
		sender: sender, urls: urls, callData: append([]byte(nil), callData...),
		callback: append([]byte(nil), callback[:]...), extraData: append([]byte(nil), extraData...),
	}, true
}

func extractRevertData(err error) ([]byte, bool) {
	var dataError gethrpc.DataError
	if !errors.As(err, &dataError) {
		return nil, false
	}
	switch value := dataError.ErrorData().(type) {
	case string:
		decoded, decodeErr := hexutil.Decode(value)
		return decoded, decodeErr == nil && len(decoded) >= 4
	case json.RawMessage:
		var encoded string
		if json.Unmarshal(value, &encoded) != nil {
			return nil, false
		}
		decoded, decodeErr := hexutil.Decode(encoded)
		return decoded, decodeErr == nil && len(decoded) >= 4
	default:
		return nil, false
	}
}

func classifyUniversalRevert(err error) (Outcome, error, bool) {
	var revert *rpcRevert
	if !errors.As(err, &revert) || len(revert.data) < 4 {
		return "", nil, false
	}
	for _, name := range []string{"ResolverNotFound", "UnsupportedResolverProfile"} {
		candidate := universalResolverABI.Errors[name]
		if bytes.Equal(revert.data[:4], candidate.ID[:4]) {
			return OutcomeNoRecord, nil, true
		}
	}
	for name, code := range map[string]string{
		"ResolverNotContract":    CodeResolverNotContract,
		"ResolverError":          CodeResolverFailure,
		"ReverseAddressMismatch": CodeForwardMismatch,
		"HttpError":              CodeCCIPUnavailable,
	} {
		candidate := universalResolverABI.Errors[name]
		if bytes.Equal(revert.data[:4], candidate.ID[:4]) {
			return "", resolutionError(code), true
		}
	}
	return "", resolutionError(CodeInvalidResponse), true
}

type HTTPGateway struct {
	client  *metadata.Client
	allowed map[string]struct{}
}

func NewHTTPGateway(client *metadata.Client, allowed []string) (*HTTPGateway, error) {
	if client == nil {
		return nil, errors.New("ENS gateway HTTP client is nil")
	}
	if len(allowed) == 0 || len(allowed) > 8 {
		return nil, errors.New("ENS gateway allowlist must contain 1 to 8 URLs")
	}
	values := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		if value == "" {
			return nil, errors.New("ENS gateway URL is empty")
		}
		values[value] = struct{}{}
	}
	return &HTTPGateway{client: client, allowed: values}, nil
}

func (gateway *HTTPGateway) Request(
	ctx context.Context,
	rawURL string,
	sender common.Address,
	callData []byte,
) ([]byte, error) {
	if gateway == nil || gateway.client == nil {
		return nil, errors.New("ENS gateway is unavailable")
	}
	if _, ok := gateway.allowed[rawURL]; !ok || sender == (common.Address{}) || len(callData) == 0 {
		return nil, errors.New("ENS gateway request is invalid")
	}
	body, err := json.Marshal(struct {
		Data   string `json:"data"`
		Sender string `json:"sender"`
	}{Data: hexutil.Encode(callData), Sender: strings.ToLower(sender.Hex())})
	if err != nil {
		return nil, errors.New("encode ENS gateway request")
	}
	result, err := gateway.client.PostJSON(ctx, rawURL, body, metadata.KindCCIP)
	if err != nil || result.URL != rawURL {
		return nil, errors.New("ENS gateway request failed")
	}
	encoded := ""
	if result.ContentType == "application/json" || strings.HasSuffix(result.ContentType, "+json") {
		decoder := json.NewDecoder(bytes.NewReader(result.Body))
		decoder.DisallowUnknownFields()
		var response struct {
			Data string `json:"data"`
		}
		if err := decoder.Decode(&response); err != nil {
			return nil, errors.New("ENS gateway response is invalid")
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, errors.New("ENS gateway response has trailing data")
		}
		encoded = response.Data
	} else {
		encoded = strings.TrimSpace(string(result.Body))
	}
	decoded, err := hexutil.Decode(encoded)
	if err != nil || len(decoded) == 0 {
		return nil, errors.New("ENS gateway response data is invalid")
	}
	return decoded, nil
}

func mustABI(definition string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		panic(fmt.Sprintf("invalid trusted ENS ABI: %v", err))
	}
	return parsed
}

var universalResolverABI = mustABI(`[
  {"type":"function","name":"resolve","stateMutability":"view","inputs":[{"name":"name","type":"bytes"},{"name":"data","type":"bytes"}],"outputs":[{"name":"result","type":"bytes"},{"name":"resolver","type":"address"}]},
  {"type":"function","name":"reverse","stateMutability":"view","inputs":[{"name":"lookupAddress","type":"bytes"},{"name":"coinType","type":"uint256"}],"outputs":[{"name":"primary","type":"string"},{"name":"resolver","type":"address"},{"name":"reverseResolver","type":"address"}]},
  {"type":"error","name":"ResolverNotFound","inputs":[{"name":"name","type":"bytes"}]},
  {"type":"error","name":"ResolverNotContract","inputs":[{"name":"name","type":"bytes"},{"name":"resolver","type":"address"}]},
  {"type":"error","name":"UnsupportedResolverProfile","inputs":[{"name":"selector","type":"bytes4"}]},
  {"type":"error","name":"ResolverError","inputs":[{"name":"errorData","type":"bytes"}]},
  {"type":"error","name":"ReverseAddressMismatch","inputs":[{"name":"primary","type":"string"},{"name":"primaryAddress","type":"bytes"}]},
  {"type":"error","name":"HttpError","inputs":[{"name":"status","type":"uint16"},{"name":"message","type":"string"}]},
  {"type":"error","name":"OffchainLookup","inputs":[{"name":"sender","type":"address"},{"name":"urls","type":"string[]"},{"name":"callData","type":"bytes"},{"name":"callbackFunction","type":"bytes4"},{"name":"extraData","type":"bytes"}]}
]`)

var legacyAddressResolverABI = mustABI(`[
  {"type":"function","name":"addr","stateMutability":"view","inputs":[{"name":"node","type":"bytes32"}],"outputs":[{"name":"address","type":"address"}]}
]`)

var multicoinAddressResolverABI = mustABI(`[
  {"type":"function","name":"addr","stateMutability":"view","inputs":[{"name":"node","type":"bytes32"},{"name":"coinType","type":"uint256"}],"outputs":[{"name":"address","type":"bytes"}]}
]`)

var callbackArguments abi.Arguments

func init() {
	bytesType, err := abi.NewType("bytes", "", nil)
	if err != nil {
		panic(err)
	}
	callbackArguments = abi.Arguments{{Type: bytesType}, {Type: bytesType}}
}
