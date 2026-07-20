package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

type fixedStateCaller struct {
	method string
	params []any
	value  string
	err    error
}

func (caller *fixedStateCaller) Call(_ context.Context, method string, params []any, result any) error {
	caller.method = method
	caller.params = params
	if caller.err != nil {
		return caller.err
	}
	encoded, _ := json.Marshal(caller.value)
	return json.Unmarshal(encoded, result)
}

func fixedStateReader(t *testing.T, caller ethrpc.Caller, canonical bool) *Reader {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: caller,
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
	caller := &fixedStateCaller{value: "0x2a"}
	reader := fixedStateReader(t, caller, true)
	balance, err := reader.NativeBalance(t.Context(), "0x000000000000000000000000000000000000dEaD")
	if err != nil || balance != "42" {
		t.Fatalf("balance=%q err=%v", balance, err)
	}
	if caller.method != "eth_getBalance" || len(caller.params) != 2 || caller.params[0] != "0x000000000000000000000000000000000000dEaD" {
		t.Fatalf("method=%q params=%#v", caller.method, caller.params)
	}
	wantSelector := map[string]any{"blockHash": testStateHash(9).String(), "requireCanonical": true}
	if !reflect.DeepEqual(caller.params[1], wantSelector) {
		t.Fatalf("selector=%#v want=%#v", caller.params[1], wantSelector)
	}
}

func TestNativeBalancesBatchSharesCanonicalObservation(t *testing.T) {
	reader := fixedStateReader(t, testCaller{}, true)
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

	balanceCaller := &fixedStateCaller{value: result}
	balanceReader := fixedStateReader(t, balanceCaller, true)
	balance, err := balanceReader.ERC20Balance(t.Context(), contract, owner)
	if err != nil || balance != "123" {
		t.Fatalf("balance=%q err=%v", balance, err)
	}
	if balanceCaller.method != "eth_call" || len(balanceCaller.params) != 2 {
		t.Fatalf("method=%q params=%#v", balanceCaller.method, balanceCaller.params)
	}
	call, ok := balanceCaller.params[0].(map[string]any)
	if !ok {
		t.Fatalf("call=%#v", balanceCaller.params[0])
	}
	wantBalanceData := "0x70a08231" + strings.Repeat("0", 24) + strings.TrimPrefix(owner, "0x")
	if call["to"] != contract || call["data"] != wantBalanceData {
		t.Fatalf("call=%#v want data=%s", call, wantBalanceData)
	}

	supplyCaller := &fixedStateCaller{value: result}
	supplyReader := fixedStateReader(t, supplyCaller, true)
	supply, err := supplyReader.ERC20TotalSupply(t.Context(), contract)
	if err != nil || supply != "123" {
		t.Fatalf("supply=%q err=%v", supply, err)
	}
	supplyCall, ok := supplyCaller.params[0].(map[string]any)
	if !ok || supplyCall["data"] != "0x18160ddd" {
		t.Fatalf("supply call=%#v", supplyCaller.params)
	}
}

func TestFixedStateRejectsConcurrentReorgAndMalformedTokenResult(t *testing.T) {
	reorgReader := fixedStateReader(t, &fixedStateCaller{value: "0x1"}, false)
	if _, err := reorgReader.NativeBalance(t.Context(), "0x0000000000000000000000000000000000000001"); !errors.Is(err, httpapi.ErrNotReady) {
		t.Fatalf("reorg error=%v", err)
	}

	malformedReader := fixedStateReader(t, &fixedStateCaller{value: "0x01"}, true)
	if _, err := malformedReader.ERC20TotalSupply(t.Context(), "0x1111111111111111111111111111111111111111"); err == nil {
		t.Fatal("short ERC-20 uint256 result was accepted")
	}
}
