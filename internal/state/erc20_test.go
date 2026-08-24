package state

import (
	"database/sql/driver"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestERC20BalancesUseOneExactEndpointAndBlockHash(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 42, Hash: testStateHash(42)}
	result := make([]byte, 32)
	maximumUint256.FillBytes(result)
	service := &testStateRPC{callResult: result}
	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	reconciler := &NFTReconciler{
		db: stateTestDatabase(t,
			erc20CacheQueryExpectation(nil),
			erc20InsertExpectation(contract, owner, maximumUint256.String()),
		),
		pool:      newERC20TestPool(t, service, nil),
		canonical: testCanonical{reference: reference, canonical: true},
	}
	observations, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
		ChainID: "1", BlockNumber: "42", BlockHash: reference.Hash.Hex(),
	}, owner.Hex(), []catalog.ERC20BalanceCandidate{{TokenAddress: contract.Hex()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].Balance != maximumUint256.String() ||
		observations[0].Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("observations=%+v", observations)
	}
	call := service.params[0].(map[string]any)
	if data, ok := call["data"].(string); !ok || !strings.HasPrefix(data, "0x70a08231") {
		t.Fatalf("balanceOf call=%#v", call)
	}
	if !reflect.DeepEqual(service.params[1], canonicalSelector(reference)) {
		t.Fatalf("selector=%#v", service.params[1])
	}
}

func TestERC20BalancesUseExactCacheWithoutRPC(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 42, Hash: testStateHash(42)}
	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	service := &testStateRPC{err: errors.New("RPC must not be called for an exact cache hit")}
	reconciler := &NFTReconciler{
		db: stateTestDatabase(t, erc20CacheQueryExpectation([][]driver.Value{{
			contract.Bytes(), "0", catalog.NFTStateConfidenceRPCExact,
		}})),
		pool:      newERC20TestPool(t, service, nil),
		canonical: testCanonical{reference: reference, canonical: true},
	}
	observations, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
		ChainID: "1", BlockNumber: "42", BlockHash: reference.Hash.Hex(),
	}, owner.Hex(), []catalog.ERC20BalanceCandidate{{TokenAddress: contract.Hex()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].Balance != "0" ||
		observations[0].Confidence != catalog.NFTStateConfidenceRPCExact || service.method != "" {
		t.Fatalf("observations=%+v RPC method=%q", observations, service.method)
	}
}

func TestERC20BalancesBatchOnlyCacheMissesAndPreserveOrder(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 42, Hash: testStateHash(42)}
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	contracts := []common.Address{
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x3333333333333333333333333333333333333333"),
		common.HexToAddress("0x4444444444444444444444444444444444444444"),
	}
	calls := 0
	service := &testStateRPC{callFn: func(call map[string]any, _ rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
		calls++
		result := make([]byte, 32)
		switch call["to"] {
		case contracts[0].Hex():
			big.NewInt(1).FillBytes(result)
		case contracts[2].Hex():
			big.NewInt(3).FillBytes(result)
		default:
			return nil, errors.New("cached token was queried through RPC")
		}
		return hexutil.Bytes(result), nil
	}}
	observer := &nftBatchObserver{}
	reconciler := &NFTReconciler{
		db: stateTestDatabase(t,
			erc20CacheQueryExpectationWithCheck([][]driver.Value{{
				contracts[1].Bytes(), "2", catalog.NFTStateConfidenceRPCExact,
			}}, func(arguments []driver.NamedValue) error {
				addresses, ok := arguments[4].Value.([][]byte)
				if !ok || len(addresses) != len(contracts) {
					return errors.New("ERC-20 cache lookup was not one bounded address array")
				}
				return nil
			}),
			erc20InsertExpectation(contracts[0], owner, "1"),
			erc20InsertExpectation(contracts[2], owner, "3"),
		),
		pool:      newERC20TestPool(t, service, observer),
		canonical: testCanonical{reference: reference, canonical: true},
	}
	candidates := make([]catalog.ERC20BalanceCandidate, len(contracts))
	for index, contract := range contracts {
		candidates[index].TokenAddress = contract.Hex()
	}
	observations, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
		ChainID: "1", BlockNumber: "42", BlockHash: reference.Hash.Hex(),
	}, owner.Hex(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 3 || observations[0].Balance != "1" ||
		observations[1].Balance != "2" || observations[2].Balance != "3" || calls != 2 {
		t.Fatalf("observations=%+v calls=%d", observations, calls)
	}
	if len(observer.observations) != 1 || observer.observations[0].Method != "eth_call" ||
		observer.observations[0].BatchSize != 2 || observer.observations[0].SuccessCount != 2 {
		t.Fatalf("RPC observations=%+v", observer.observations)
	}
}

func TestERC20BalancesRejectMalformedCacheWithoutRPC(t *testing.T) {
	t.Parallel()
	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	overflow := new(big.Int).Add(maximumUint256, big.NewInt(1)).String()
	for _, test := range []struct {
		name       string
		address    []byte
		balance    string
		confidence string
	}{
		{name: "overflow", address: contract.Bytes(), balance: overflow, confidence: catalog.NFTStateConfidenceRPCExact},
		{name: "confidence", address: contract.Bytes(), balance: "1", confidence: "inferred"},
		{name: "short address", address: []byte{1}, balance: "1", confidence: catalog.NFTStateConfidenceRPCExact},
		{name: "unexpected address", address: common.HexToAddress("0x3333333333333333333333333333333333333333").Bytes(), balance: "1", confidence: catalog.NFTStateConfidenceRPCExact},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &testStateRPC{err: errors.New("RPC must not run for corrupt cache data")}
			reconciler := &NFTReconciler{
				db: stateTestDatabase(t, erc20CacheQueryExpectation([][]driver.Value{{
					test.address, test.balance, test.confidence,
				}})),
				pool:      newERC20TestPool(t, service, nil),
				canonical: testCanonical{canonical: true},
			}
			_, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
				ChainID: "1", BlockNumber: "1", BlockHash: testStateHash(1).Hex(),
			}, owner.Hex(), []catalog.ERC20BalanceCandidate{{TokenAddress: contract.Hex()}})
			if err == nil || service.method != "" {
				t.Fatalf("error=%v RPC method=%q", err, service.method)
			}
		})
	}
}

func TestERC20BalancesRejectMalformedRPCAndCanonicalChange(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		result    []byte
		rpcErr    error
		canonical bool
		want      error
	}{
		{name: "malformed", result: []byte{1}, canonical: true, want: httpapi.ErrUnavailable},
		{name: "element error", rpcErr: errors.New("state unavailable"), canonical: true, want: httpapi.ErrUnavailable},
		{name: "reorg", result: make([]byte, 32), canonical: false, want: httpapi.ErrNotReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &testStateRPC{callResult: test.result, err: test.rpcErr}
			reconciler := &NFTReconciler{
				db:        stateTestDatabase(t, erc20CacheQueryExpectation(nil)),
				pool:      newERC20TestPool(t, service, nil),
				canonical: testCanonical{canonical: test.canonical},
			}
			_, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
				ChainID: "1", BlockNumber: "1", BlockHash: testStateHash(1).Hex(),
			}, "0x2222222222222222222222222222222222222222", []catalog.ERC20BalanceCandidate{{
				TokenAddress: "0x1111111111111111111111111111111111111111",
			}})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
}

func TestERC20BalancePersistenceRejectsConcurrentConflict(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 1, Hash: testStateHash(1)}
	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	result := make([]byte, 32)
	big.NewInt(9).FillBytes(result)
	reconciler := &NFTReconciler{
		db: stateTestDatabase(t,
			erc20CacheQueryExpectation(nil),
			stateSQLExpectation{
				kind: "exec", contains: "INSERT INTO erc20_balance_reconciliations", rowsAffected: 0,
			},
			stateSQLExpectation{
				kind: "query", contains: "EXISTS ( SELECT 1 FROM canonical_blocks",
				columns: []string{"canonical", "stored"}, rows: [][]driver.Value{{true, true}},
			},
		),
		pool: newERC20TestPool(t, &testStateRPC{callResult: result}, nil),
		canonical: testCanonical{
			reference: reference, canonical: true,
		},
	}
	_, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
		ChainID: "1", BlockNumber: "1", BlockHash: reference.Hash.Hex(),
	}, owner.Hex(), []catalog.ERC20BalanceCandidate{{TokenAddress: contract.Hex()}})
	if !errors.Is(err, ErrExactERC20BalanceObservationConflict) {
		t.Fatalf("error=%v", err)
	}
}

func erc20CacheQueryExpectation(rows [][]driver.Value) stateSQLExpectation {
	return erc20CacheQueryExpectationWithCheck(rows, nil)
}

func erc20CacheQueryExpectationWithCheck(
	rows [][]driver.Value,
	check func([]driver.NamedValue) error,
) stateSQLExpectation {
	return stateSQLExpectation{
		kind: "query", contains: "FROM erc20_balance_reconciliations AS observation",
		columns: []string{"token_address", "balance", "confidence"}, rows: rows, check: check,
	}
}

func erc20InsertExpectation(contract, owner common.Address, balance string) stateSQLExpectation {
	return stateSQLExpectation{
		kind: "exec", contains: "INSERT INTO erc20_balance_reconciliations", rowsAffected: 1,
		check: func(arguments []driver.NamedValue) error {
			if len(arguments) != 6 || !reflect.DeepEqual(arguments[1].Value, contract.Bytes()) ||
				!reflect.DeepEqual(arguments[2].Value, owner.Bytes()) || arguments[5].Value != balance {
				return errors.New("unexpected exact ERC-20 persistence arguments")
			}
			return nil
		},
	}
}

func newERC20TestPool(t *testing.T, service *testStateRPC, observer ethrpc.Observer) *ethrpc.Pool {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
