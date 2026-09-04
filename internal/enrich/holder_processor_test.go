package enrich

import (
	"context"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/ethrpc"
)

type holderRPCRequest struct {
	To   common.Address `json:"to"`
	Data hexutil.Bytes  `json:"data"`
}

type holderRPCFixture struct {
	mu       sync.Mutex
	supply   *big.Int
	balances map[common.Address]*big.Int
	calls    int
}

func (fixture *holderRPCFixture) Call(
	_ context.Context,
	request holderRPCRequest,
	block rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.calls++
	if block.BlockHash == nil || !block.RequireCanonical {
		return nil, rpc.ErrNoResult
	}
	var value *big.Int
	switch {
	case len(request.Data) == len(erc20TotalSupplySelector) && string(request.Data) == string(erc20TotalSupplySelector):
		value = fixture.supply
	case len(request.Data) == 36 && string(request.Data[:4]) == string(erc20BalanceOfSelector):
		value = fixture.balances[common.BytesToAddress(request.Data[16:])]
	}
	if value == nil {
		return nil, rpc.ErrNoResult
	}
	result := make([]byte, 32)
	value.FillBytes(result)
	return result, nil
}

func TestReconcileHolderTokenRequiresExactSupplyAgreement(t *testing.T) {
	t.Parallel()
	token := common.HexToAddress("0x1000000000000000000000000000000000000001")
	first := common.HexToAddress("0x2000000000000000000000000000000000000002")
	second := common.HexToAddress("0x3000000000000000000000000000000000000003")
	fixture := &holderRPCFixture{
		supply:   big.NewInt(10),
		balances: map[common.Address]*big.Int{first: big.NewInt(4), second: big.NewInt(6)},
	}
	server := rpc.NewServer()
	if err := server.RegisterName("eth", fixture); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	endpoint := &ethrpc.Endpoint{
		Name: "holder-test", Client: client,
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}
	job := Job{
		ID: "1", Stage: HolderStage, ChainID: "1", BlockNumber: 7,
		BlockHash: common.HexToHash("0x7000000000000000000000000000000000000000000000000000000000000007"),
	}

	complete, err := reconcileHolderToken(t.Context(), endpoint, job, holderTokenInput{
		token: token, holders: []common.Address{first, second}, full: true,
		eventSupply: big.NewInt(10), eventSupplyValid: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if complete.state != "complete" || complete.holderCount != 2 || complete.balanceSum.String() != "10" {
		t.Fatalf("complete reconciliation=%+v", complete)
	}

	unavailable, err := reconcileHolderToken(t.Context(), endpoint, job, holderTokenInput{
		token: token, holders: []common.Address{first, second}, full: true,
		eventSupply: big.NewInt(9), eventSupplyValid: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.state != "unavailable" || unavailable.totalSupply.String() != "10" {
		t.Fatalf("unavailable reconciliation=%+v", unavailable)
	}
	fixture.mu.Lock()
	fixture.supply = big.NewInt(9)
	fixture.balances[first] = big.NewInt(3)
	fixture.mu.Unlock()
	incremental, err := reconcileHolderToken(t.Context(), endpoint, job, holderTokenInput{
		token: token, holders: []common.Address{first}, previousBalances: []*big.Int{big.NewInt(4)},
		previousSum: big.NewInt(10), previousCount: 2, eventSupply: big.NewInt(9),
		eventSupplyValid: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if incremental.state != "complete" || incremental.holderCount != 2 || incremental.balanceSum.String() != "9" {
		t.Fatalf("incremental reconciliation=%+v", incremental)
	}
	fixture.mu.Lock()
	calls := fixture.calls
	fixture.mu.Unlock()
	if calls != 8 {
		t.Fatalf("RPC calls=%d, want 8", calls)
	}
}
