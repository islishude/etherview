package defi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/islishude/etherview/internal/deficontract"
)

func TestAaveDiscoveryReportsUnreviewedReceiptsWithoutHolders(t *testing.T) {
	pool, provider, reserve, receipt := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4")
	hash := common.HexToHash("0xa")
	fixture := &callFixture{t: t, hash: hash, answers: map[string][]byte{
		key(pool, "getReservesList()"):                      append(words(32, 1), addrWord(reserve)...),
		key(provider, "getReserveTokensAddresses(address)"): append(addrWord(receipt), words(0, 0)...),
	}}
	d := Deployment{Protocol: deficontract.AaveV3, Contracts: []Contract{{Role: "pool", Address: pool}, {Role: "data_provider", Address: provider}}}
	for _, reviewed := range []bool{false, true} {
		if reviewed {
			d.ReceiptContracts = []Contract{{Role: "receipt", Address: receipt, CodeHash: crypto.Keccak256Hash([]byte{1})}}
		}
		items, next, done, err := Instruments(t.Context(), &State{RPC: fixture, Hash: hash}, d, 0)
		if err != nil || len(items) != 1 || next != 1 || !done {
			t.Fatalf("discovery failed: items=%v next=%d done=%v error=%v", items, next, done, err)
		}
		if (items[0].FailureCode == "") != reviewed {
			t.Fatalf("receipt review not reflected in discovery: reviewed=%v item=%+v", reviewed, items[0])
		}
	}
}

func TestDiscoveryReservesPublicationTime(t *testing.T) {
	factory := common.HexToAddress("0x1")
	hash := common.HexToHash("0xa")
	fixture := &callFixture{t: t, hash: hash, answers: map[string][]byte{key(factory, "allPairsLength()"): words(1)}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	items, next, done, err := Instruments(ctx, &State{RPC: fixture, Hash: hash}, Deployment{Protocol: deficontract.UniswapV2, Contracts: []Contract{{Role: "factory", Address: factory}}}, 0)
	if !errors.Is(err, deficontract.ErrUnavailable) || len(items) != 0 || next != 0 || done || fixture.seen != 1 {
		t.Fatalf("exhausted budget advanced discovery: items=%v next=%d done=%v calls=%d error=%v", items, next, done, fixture.seen, err)
	}
}
