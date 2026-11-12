package state

import (
	"context"
	"errors"
	"maps"
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/publicquery"
)

type memoryNativeBalances struct {
	values map[common.Address]string
	saves  int
	ref    CanonicalRef
}

func (m *memoryNativeBalances) Read(_ context.Context, ref CanonicalRef, _ []common.Address) (map[common.Address]string, error) {
	out := map[common.Address]string{}
	maps.Copy(out, m.values)
	return out, nil
}
func (m *memoryNativeBalances) Save(_ context.Context, ref CanonicalRef, values map[common.Address]string) error {
	m.saves++
	m.ref = ref
	if m.values == nil {
		m.values = map[common.Address]string{}
	}
	maps.Copy(m.values, values)
	return nil
}
func TestNativeBalanceCacheZeroAndBatchDuplicates(t *testing.T) {
	service := &testStateRPC{balance: big.NewInt(0)}
	reader := fixedStateReader(t, service, true)
	cache := &memoryNativeBalances{values: map[common.Address]string{common.HexToAddress("0x1"): "42"}}
	reader.Balances = cache
	addresses := []string{common.HexToAddress("0x1").Hex(), common.HexToAddress("0x2").Hex(), common.HexToAddress("0x2").Hex()}
	got, err := reader.NativeBalances(t.Context(), addresses)
	if err != nil || !reflect.DeepEqual(got, []string{"42", "0", "0"}) {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if cache.saves != 1 || cache.ref.Hash != testStateHash(9) {
		t.Fatalf("cache=%+v", cache)
	}
	service.err = errors.New("RPC must not run")
	reader.Pool = nil
	got, err = reader.NativeBalances(t.Context(), addresses)
	if err != nil || !reflect.DeepEqual(got, []string{"42", "0", "0"}) {
		t.Fatalf("cached=%v err=%v", got, err)
	}
	if cache.saves != 1 {
		t.Fatal("cache hit rewrote baseline")
	}
	reader.Canonical = testCanonical{reference: cache.ref, canonical: false}
	if _, err := reader.NativeBalances(t.Context(), addresses); !errors.Is(err, publicquery.ErrNotReady) {
		t.Fatalf("reorg accepted: %v", err)
	}
}
func TestNativeBalanceReorgDoesNotSaveBaseline(t *testing.T) {
	reader := fixedStateReader(t, &testStateRPC{balance: big.NewInt(1)}, false)
	cache := &memoryNativeBalances{}
	reader.Balances = cache
	if _, err := reader.NativeBalance(t.Context(), common.HexToAddress("0x1").Hex()); !errors.Is(err, publicquery.ErrNotReady) {
		t.Fatal(err)
	}
	if cache.saves != 0 {
		t.Fatal("orphan baseline saved")
	}
}

func TestAddressSummaryUsesPublishedBalance(t *testing.T) {
	reader := fixedStateReader(t, &testStateRPC{balance: big.NewInt(999)}, true)
	reader.Base = testBaseReader{}
	address := common.HexToAddress("0xdead")
	reader.Balances = &memoryNativeBalances{values: map[common.Address]string{address: "0"}}
	result, err := reader.Address(t.Context(), address.Hex())
	if err != nil || result.Balance != "0" || result.Nonce != "2" {
		t.Fatalf("summary=%+v error=%v", result, err)
	}
}
