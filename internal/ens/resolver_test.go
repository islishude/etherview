package ens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/metadata"
)

var (
	testUniversalResolver = common.HexToAddress(OfficialUniversalResolverAddress)
	testForwardResolver   = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testReverseResolver   = common.HexToAddress("0x2222222222222222222222222222222222222222")
	testResolvedAddress   = common.HexToAddress("0x3333333333333333333333333333333333333333")
	testBlockHash         = common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
)

type rpcStep struct {
	output []byte
	err    error
}

type scriptedRPCCaller struct {
	t      *testing.T
	steps  []rpcStep
	calls  int
	blocks []gethrpc.BlockNumberOrHash
}

func (caller *scriptedRPCCaller) CallContext(_ context.Context, result any, method string, args ...any) error {
	caller.t.Helper()
	if method != "eth_call" || len(args) != 2 || caller.calls >= len(caller.steps) {
		caller.t.Fatalf("unexpected RPC call method=%s args=%#v calls=%d", method, args, caller.calls)
	}
	block, ok := args[1].(gethrpc.BlockNumberOrHash)
	if !ok {
		caller.t.Fatalf("block selector = %T", args[1])
	}
	caller.blocks = append(caller.blocks, block)
	step := caller.steps[caller.calls]
	caller.calls++
	if step.err != nil {
		return step.err
	}
	target, ok := result.(*hexutil.Bytes)
	if !ok {
		caller.t.Fatalf("RPC result = %T", result)
	}
	*target = append((*target)[:0], step.output...)
	return nil
}

type dataError struct{ data string }

func (err dataError) Error() string  { return "execution reverted" }
func (err dataError) ErrorData() any { return err.data }

type gatewayStub struct {
	response []byte
	err      error
	calls    int
	url      string
	sender   common.Address
}

func (gateway *gatewayStub) Request(_ context.Context, url string, sender common.Address, _ []byte) ([]byte, error) {
	gateway.calls++
	gateway.url, gateway.sender = url, sender
	return append([]byte(nil), gateway.response...), gateway.err
}

func testResolver(t *testing.T, gateway GatewayRequester) *Resolver {
	t.Helper()
	normalizer, err := NewNormalizer()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(normalizer, gateway, 4)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func testProfile() Profile {
	return Profile{
		Source: SourceOfficial, UniversalResolver: testUniversalResolver,
		CoinType: big.NewInt(60), Gateways: []string{"https://ccip.example"},
		Block: BlockRef{Number: 12, Hash: testBlockHash},
	}
}

func forwardOutput(t *testing.T, address, resolver common.Address) []byte {
	t.Helper()
	inner, err := legacyAddressResolverABI.Methods["addr"].Outputs.Pack(address)
	if err != nil {
		t.Fatal(err)
	}
	output, err := universalResolverABI.Methods["resolve"].Outputs.Pack(inner, resolver)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func reverseOutput(t *testing.T, name string) []byte {
	t.Helper()
	output, err := universalResolverABI.Methods["reverse"].Outputs.Pack(
		name, testForwardResolver, testReverseResolver,
	)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func contractError(t *testing.T, name string, values ...any) error {
	t.Helper()
	definition := universalResolverABI.Errors[name]
	arguments, err := definition.Inputs.Pack(values...)
	if err != nil {
		t.Fatal(err)
	}
	encoded := append(append([]byte(nil), definition.ID[:4]...), arguments...)
	return dataError{data: hexutil.Encode(encoded)}
}

func TestUniversalResolverUsesENSIP23Entrypoints(t *testing.T) {
	if got := universalResolverABI.Methods["resolve"].Sig; got != "resolve(bytes,bytes)" {
		t.Fatalf("resolve signature = %q", got)
	}
	if got := universalResolverABI.Methods["reverse"].Sig; got != "reverse(bytes,uint256)" {
		t.Fatalf("reverse signature = %q", got)
	}
	if _, exists := universalResolverABI.Methods["resolveWithGateways"]; exists {
		t.Fatal("non-standard resolveWithGateways entrypoint must not be present")
	}
	if _, exists := universalResolverABI.Methods["reverseWithGateways"]; exists {
		t.Fatal("non-standard reverseWithGateways entrypoint must not be present")
	}
}

func TestForwardAndReverseUseOneExactBlockAndVerifyPrimaryName(t *testing.T) {
	caller := &scriptedRPCCaller{t: t, steps: []rpcStep{
		{output: forwardOutput(t, testResolvedAddress, testForwardResolver)},
		{output: reverseOutput(t, "name.eth")},
		{output: forwardOutput(t, testResolvedAddress, testForwardResolver)},
	}}
	resolver := testResolver(t, nil)
	forward, err := resolver.Forward(t.Context(), caller, testProfile(), "NaMe.EtH")
	if err != nil || forward.Outcome != OutcomeResolved || forward.Name != "name.eth" ||
		forward.Address != testResolvedAddress || forward.Resolver != testForwardResolver {
		t.Fatalf("forward = %+v, %v", forward, err)
	}
	primary, err := resolver.Reverse(t.Context(), caller, testProfile(), testResolvedAddress)
	if err != nil || primary.Outcome != OutcomeResolved || primary.Name != "name.eth" ||
		primary.Resolver != testForwardResolver || primary.ReverseResolver != testReverseResolver {
		t.Fatalf("primary = %+v, %v", primary, err)
	}
	if caller.calls != 3 {
		t.Fatalf("RPC calls = %d, want 3", caller.calls)
	}
	for _, block := range caller.blocks {
		if block.BlockHash == nil || *block.BlockHash != testBlockHash || !block.RequireCanonical {
			t.Fatalf("block selector = %+v", block)
		}
	}
}

func TestUniversalResolverNoRecordAndFailuresRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		wantNo   bool
		wantCode string
	}{
		{name: "not found", err: contractError(t, "ResolverNotFound", []byte{1}), wantNo: true},
		{name: "unsupported profile", err: contractError(t, "UnsupportedResolverProfile", [4]byte{1, 2, 3, 4}), wantNo: true},
		{name: "not contract", err: contractError(t, "ResolverNotContract", []byte{1}, testForwardResolver), wantCode: CodeResolverNotContract},
		{name: "reverse mismatch", err: contractError(t, "ReverseAddressMismatch", "name.eth", testResolvedAddress.Bytes()), wantCode: CodeForwardMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := &scriptedRPCCaller{t: t, steps: []rpcStep{{err: test.err}}}
			result, err := testResolver(t, nil).Forward(t.Context(), caller, testProfile(), "name.eth")
			if test.wantNo {
				if err != nil || result.Outcome != OutcomeNoRecord {
					t.Fatalf("result = %+v, %v", result, err)
				}
				return
			}
			var resolution *ResolutionError
			if !errors.As(err, &resolution) || resolution.Code != test.wantCode {
				t.Fatalf("error = %v, want %s", err, test.wantCode)
			}
		})
	}
}

func TestReverseRejectsUnverifiedOrUnnormalizedName(t *testing.T) {
	for _, test := range []struct {
		name  string
		steps []rpcStep
		code  string
	}{
		{name: "forward mismatch", steps: []rpcStep{
			{output: reverseOutput(t, "name.eth")},
			{output: forwardOutput(t, common.HexToAddress("0x4444444444444444444444444444444444444444"), testForwardResolver)},
		}, code: CodeForwardMismatch},
		{name: "unnormalized", steps: []rpcStep{{output: reverseOutput(t, "NaMe.EtH")}}, code: CodeInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := &scriptedRPCCaller{t: t, steps: test.steps}
			_, err := testResolver(t, nil).Reverse(t.Context(), caller, testProfile(), testResolvedAddress)
			var resolution *ResolutionError
			if !errors.As(err, &resolution) || resolution.Code != test.code {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestCCIPReadValidatesSenderAllowlistAndCallbackDepth(t *testing.T) {
	callback := [4]byte{0xaa, 0xbb, 0xcc, 0xdd}
	offchain := contractError(
		t, "OffchainLookup", testUniversalResolver, []string{"https://ccip.example"},
		[]byte{1, 2}, callback, []byte{3, 4},
	)
	gateway := &gatewayStub{response: []byte{5, 6}}
	caller := &scriptedRPCCaller{t: t, steps: []rpcStep{
		{err: offchain}, {output: forwardOutput(t, testResolvedAddress, testForwardResolver)},
	}}
	result, err := testResolver(t, gateway).Forward(t.Context(), caller, testProfile(), "name.eth")
	if err != nil || result.Outcome != OutcomeResolved || gateway.calls != 1 ||
		gateway.url != "https://ccip.example" || gateway.sender != testUniversalResolver {
		t.Fatalf("result=%+v error=%v gateway=%+v", result, err, gateway)
	}

	badSender := contractError(
		t, "OffchainLookup", testForwardResolver, []string{"https://ccip.example"},
		[]byte{1}, callback, []byte{},
	)
	caller = &scriptedRPCCaller{t: t, steps: []rpcStep{{err: badSender}}}
	_, err = testResolver(t, gateway).Forward(t.Context(), caller, testProfile(), "name.eth")
	var resolution *ResolutionError
	if !errors.As(err, &resolution) || resolution.Code != CodeCCIPSenderMismatch {
		t.Fatalf("sender mismatch error = %v", err)
	}
}

func TestHTTPGatewayUsesBoundedPostAndRejectsRedirects(t *testing.T) {
	t.Parallel()
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"0x99"}`))
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("gateway request method=%s content-type=%q", request.Method, request.Header.Get("Content-Type"))
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["data"] != "0x0102" || body["sender"] != strings.ToLower(testUniversalResolver.Hex()) {
			t.Errorf("gateway request body=%v error=%v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"0x0506"}`))
	}))
	defer server.Close()
	client, err := metadata.New(metadata.Policy{
		AllowHTTP: true, UnsafeAllowPrivateNetworks: true, NoRedirects: true,
		Timeout: time.Second, MaxBytes: 1024,
	}, net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewHTTPGateway(client, []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := gateway.Request(t.Context(), server.URL, testUniversalResolver, []byte{1, 2})
	if err != nil || !bytes.Equal(response, []byte{5, 6}) {
		t.Fatalf("gateway response=%x error=%v", response, err)
	}
	if _, err := gateway.Request(t.Context(), target.URL, testUniversalResolver, []byte{1}); err == nil {
		t.Fatal("gateway accepted a URL outside its exact allowlist")
	}
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirect.Close()
	redirectGateway, err := NewHTTPGateway(client, []string{redirect.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := redirectGateway.Request(t.Context(), redirect.URL, testUniversalResolver, []byte{1}); err == nil {
		t.Fatal("gateway followed a redirect")
	}
	if redirected {
		t.Fatal("redirect target was contacted")
	}
}
