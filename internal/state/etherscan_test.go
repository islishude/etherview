package state

import (
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

func fixedStateReader(t *testing.T, service *testStateRPC, canonical bool) *Reader {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &Reader{
		Pool: pool,
		Canonical: testCanonical{
			reference: CanonicalRef{Number: 42, Hash: testStateHash(9)},
			canonical: canonical,
		},
	}
}

func TestNativeBalanceUsesFixedCanonicalHash(t *testing.T) {
	service := &testStateRPC{balance: big.NewInt(42)}
	reader := fixedStateReader(t, service, true)
	balance, err := reader.NativeBalance(t.Context(), "0x000000000000000000000000000000000000dEaD")
	if err != nil || balance != "42" {
		t.Fatalf("balance=%q err=%v", balance, err)
	}
	if service.method != "eth_getBalance" || len(service.params) != 2 || service.params[0] != common.HexToAddress("0xdead") {
		t.Fatalf("method=%q params=%#v", service.method, service.params)
	}
	wantSelector := rpc.BlockNumberOrHashWithHash(testStateHash(9), true)
	if !reflect.DeepEqual(service.params[1], wantSelector) {
		t.Fatalf("selector=%#v want=%#v", service.params[1], wantSelector)
	}
}

func TestAccountKindUsesExactCodeAndWriterCanonicality(t *testing.T) {
	delegation := append([]byte{0xef, 0x01, 0x00}, common.HexToAddress("0x1234").Bytes()...)
	for _, test := range []struct {
		name, want string
		code       []byte
	}{
		{name: "EOA", want: "eoa", code: []byte{}},
		{name: "delegated EOA", want: "delegated_eoa", code: delegation},
		{name: "contract", want: "contract", code: []byte{0x60, 0x00}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &testStateRPC{code: test.code}
			reader := fixedStateReader(t, service, true)
			kind, number, hash, err := reader.AccountKind(t.Context(), "0x000000000000000000000000000000000000dEaD")
			if err != nil || kind != test.want || number != "42" || hash != testStateHash(9).Hex() {
				t.Fatalf("kind=%q number=%q hash=%q error=%v", kind, number, hash, err)
			}
			if service.method != "eth_getCode" {
				t.Fatalf("method=%s", service.method)
			}
			canonical, err := reader.IsCanonical(t.Context(), number, hash)
			if err != nil || !canonical {
				t.Fatalf("canonical=%t error=%v", canonical, err)
			}
		})
	}
}

func TestNativeBalancesBatchSharesCanonicalObservation(t *testing.T) {
	reader := fixedStateReader(t, &testStateRPC{}, true)
	balances, err := reader.NativeBalances(t.Context(), []string{
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1000000000000000000", "1000000000000000000"}
	if !reflect.DeepEqual(balances, want) {
		t.Fatalf("balances=%v want=%v", balances, want)
	}
}

func TestERC20StateCallsUseABIAndFixedCanonicalHash(t *testing.T) {
	contract := "0x1111111111111111111111111111111111111111"
	owner := "0x2222222222222222222222222222222222222222"
	result := "0x" + strings.Repeat("0", 62) + "7b"

	balanceService := &testStateRPC{callResult: hexutil.MustDecode(result)}
	balanceReader := fixedStateReader(t, balanceService, true)
	balance, err := balanceReader.ERC20Balance(t.Context(), contract, owner)
	if err != nil || balance != "123" {
		t.Fatalf("balance=%q err=%v", balance, err)
	}
	if balanceService.method != "eth_call" || len(balanceService.params) != 2 {
		t.Fatalf("method=%q params=%#v", balanceService.method, balanceService.params)
	}
	call, ok := balanceService.params[0].(map[string]any)
	if !ok {
		t.Fatalf("call=%#v", balanceService.params[0])
	}
	wantBalanceData := "0x70a08231" + strings.Repeat("0", 24) + strings.TrimPrefix(owner, "0x")
	if call["to"] != contract || call["data"] != wantBalanceData {
		t.Fatalf("call=%#v want data=%s", call, wantBalanceData)
	}

	supplyService := &testStateRPC{callResult: hexutil.MustDecode(result)}
	supplyReader := fixedStateReader(t, supplyService, true)
	supply, err := supplyReader.ERC20TotalSupply(t.Context(), contract)
	if err != nil || supply != "123" {
		t.Fatalf("supply=%q err=%v", supply, err)
	}
	supplyCall, ok := supplyService.params[0].(map[string]any)
	if !ok || supplyCall["data"] != "0x18160ddd" {
		t.Fatalf("supply call=%#v", supplyService.params)
	}
}

func TestFixedStateRejectsConcurrentReorgAndMalformedTokenResult(t *testing.T) {
	reorgReader := fixedStateReader(t, &testStateRPC{balance: big.NewInt(1)}, false)
	if _, err := reorgReader.NativeBalance(t.Context(), "0x0000000000000000000000000000000000000001"); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("reorg error=%v", err)
	}

	malformedReader := fixedStateReader(t, &testStateRPC{callResult: []byte{1}}, true)
	if _, err := malformedReader.ERC20TotalSupply(t.Context(), "0x1111111111111111111111111111111111111111"); err == nil {
		t.Fatal("short ERC-20 uint256 result was accepted")
	}
}
