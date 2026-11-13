package ethrpc

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func testBALBlock(t *testing.T, raw json.RawMessage) (*types.Block, json.RawMessage) {
	t.Helper()
	encoded, err := encodeBALJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := crypto.Keccak256Hash(encoded)
	slot := uint64(1)
	block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), GasLimit: 30_000_000, BlockAccessListHash: &hash, SlotNumber: &slot})
	transport, _ := json.Marshal(hexutil.Bytes(encoded))
	return block, transport
}

func TestBALHeaderGateAndMethodFallback(t *testing.T) {
	body := json.RawMessage(`[{"address":"0x0000000000000000000000000000000000000001","balanceChanges":[{"index":"0x0","value":"0x2a"},{"index":"0x1","value":"0x0"}]}]`)
	block, _ := testBALBlock(t, body)
	var methods []string
	server := newSingleRPCServer(t, func(req testRequest) (json.RawMessage, *testRPCError) {
		methods = append(methods, req.Method)
		var hash common.Hash
		if len(req.Params) != 1 || json.Unmarshal(req.Params[0], &hash) != nil || hash != block.Hash() {
			t.Errorf("BAL not pinned: %s", req.Params)
		}
		if req.Method == BALRawMethod {
			return nil, &testRPCError{Code: -32601, Message: "unsupported"}
		}
		return body, nil
	})
	pool, err := NewPool([]Endpoint{{Name: "state", Client: mustClient(t, server.URL), Purposes: map[Purpose]bool{PurposeState: true}}}, PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := BALFetcher{Pool: pool}
	header := block.Header()
	header.BlockAccessListHash = nil
	header.SlotNumber = nil
	if _, err := fetcher.Fetch(t.Context(), types.NewBlockWithHeader(header)); !errors.Is(err, ErrBALNotApplicable) {
		t.Fatal(err)
	}
	if len(methods) != 0 || len(fetcher.unsupported) != 0 {
		t.Fatal("pre-BAL header caused a probe or capability downgrade")
	}
	header.BlockAccessListHash = block.Header().BlockAccessListHash
	if _, err := fetcher.Fetch(t.Context(), types.NewBlockWithHeader(header)); !errors.Is(err, ErrBALInvalid) {
		t.Fatal(err)
	}
	if len(methods) != 0 {
		t.Fatal("partial Amsterdam header caused RPC")
	}
	result, err := fetcher.Fetch(t.Context(), block)
	if err != nil {
		t.Fatal(err)
	}
	if result.Method != BALJSONMethod || len(result.List) != 1 || !result.List[0].BalanceChanges[1].PostBalance.IsZero() {
		t.Fatalf("result=%+v", result)
	}
	if _, err := fetcher.Fetch(t.Context(), block); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{BALRawMethod, BALJSONMethod, BALJSONMethod}) {
		t.Fatalf("methods=%v", methods)
	}
}

func TestBALAuthenticationAndValidation(t *testing.T) {
	valid := json.RawMessage(`[{"address":"0x0000000000000000000000000000000000000001","balanceChanges":[{"index":"0x1","value":"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}]}]`)
	block, raw := testBALBlock(t, valid)
	for method, body := range map[string]json.RawMessage{BALRawMethod: raw, BALJSONMethod: valid} {
		if _, err := DecodeBAL(body, method, block); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DecodeBAL(json.RawMessage(`"0xc0"`), BALRawMethod, block); !errors.Is(err, ErrBALInvalid) {
		t.Fatalf("hash mismatch: %v", err)
	}
	for _, body := range []string{
		`[{"address":"0x0000000000000000000000000000000000000001","balanceChanges":[{"index":"0x2","value":"0x0"}]}]`,
		`[{"address":"0x0000000000000000000000000000000000000001","balanceChanges":[{"index":"0x1","value":"0x1"},{"index":"0x0","value":"0x0"}]}]`,
		`[{"address":"0x0000000000000000000000000000000000000001"},{"address":"0x0000000000000000000000000000000000000001"}]`,
	} {
		b, r := testBALBlock(t, json.RawMessage(body))
		if _, err := DecodeBAL(r, BALRawMethod, b); !errors.Is(err, ErrBALInvalid) {
			t.Fatalf("accepted invalid BAL %s: %v", body, err)
		}
	}
	for _, body := range []string{`null`, `[{}]`, `[{"address":"0x0000000000000000000000000000000000000001","balanceChanges":[{"index":"0x100000000","value":"0x0"}]}]`} {
		if _, err := encodeBALJSON(json.RawMessage(body)); !errors.Is(err, ErrBALInvalid) {
			t.Fatalf("accepted invalid transport %s", body)
		}
	}
	empty, emptyRaw := testBALBlock(t, json.RawMessage(`[]`))
	if _, err := DecodeBAL(emptyRaw, BALRawMethod, empty); err != nil {
		t.Fatalf("empty BAL: %v", err)
	}
}

func TestBALDoesNotFallbackOnMissingBody(t *testing.T) {
	block, _ := testBALBlock(t, json.RawMessage(`[]`))
	var calls int
	server := newSingleRPCServer(t, func(req testRequest) (json.RawMessage, *testRPCError) { calls++; return json.RawMessage(`null`), nil })
	pool, _ := NewPool([]Endpoint{{Name: "state", Client: mustClient(t, server.URL), Purposes: map[Purpose]bool{PurposeState: true}}}, PoolOptions{})
	f := BALFetcher{Pool: pool}
	if _, err := f.Fetch(t.Context(), block); !errors.Is(err, ErrBALUnavailable) {
		t.Fatal(err)
	}
	if calls != 1 || len(f.unsupported) != 0 {
		t.Fatalf("calls=%d unsupported=%v", calls, f.unsupported)
	}
}

func TestBALTransportFailureRemainsRetryable(t *testing.T) {
	block, _ := testBALBlock(t, json.RawMessage(`[]`))
	var calls int
	server := newSingleRPCServer(t, func(req testRequest) (json.RawMessage, *testRPCError) {
		calls++
		return nil, &testRPCError{Code: -32000, Message: "temporarily unavailable"}
	})
	pool, _ := NewPool([]Endpoint{{Name: "state", Client: mustClient(t, server.URL), Purposes: map[Purpose]bool{PurposeState: true}}}, PoolOptions{})
	f := BALFetcher{Pool: pool}
	if _, err := f.Fetch(t.Context(), block); !errors.Is(err, ErrTransport) {
		t.Fatalf("transport failure became terminal: %v", err)
	}
	if calls != 1 || len(f.unsupported) != 0 {
		t.Fatalf("calls=%d unsupported=%v", calls, f.unsupported)
	}
}

func TestBALUsesHealthyEndpointForExactHash(t *testing.T) {
	block, raw := testBALBlock(t, json.RawMessage(`[]`))
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "transport", true: "missing_body"}[missing], func(t *testing.T) {
			var failedCalls, healthyCalls int
			failed := newSingleRPCServer(t, func(req testRequest) (json.RawMessage, *testRPCError) {
				failedCalls++
				if req.Method != BALRawMethod {
					t.Errorf("unexpected JSON fallback after transport/missing body: %s", req.Method)
				}
				if missing {
					return json.RawMessage(`null`), nil
				}
				return nil, &testRPCError{Code: -32000, Message: "temporarily unavailable"}
			})
			healthy := newSingleRPCServer(t, func(req testRequest) (json.RawMessage, *testRPCError) {
				healthyCalls++
				var hash common.Hash
				if req.Method != BALRawMethod || len(req.Params) != 1 || json.Unmarshal(req.Params[0], &hash) != nil || hash != block.Hash() {
					t.Errorf("fallback changed exact BAL request: %+v", req)
				}
				return raw, nil
			})
			pool, err := NewPool([]Endpoint{
				{Name: "a", Client: mustClient(t, failed.URL), Purposes: map[Purpose]bool{PurposeState: true}},
				{Name: "b", Client: mustClient(t, healthy.URL), Purposes: map[Purpose]bool{PurposeState: true}},
			}, PoolOptions{})
			if err != nil {
				t.Fatal(err)
			}
			fetcher := BALFetcher{Pool: pool}
			for range 2 {
				if _, err := fetcher.Fetch(t.Context(), block); err != nil {
					t.Fatal(err)
				}
			}
			if failedCalls != 1 || healthyCalls != 2 || len(fetcher.unsupported) != 0 {
				t.Fatalf("failed=%d healthy=%d unsupported=%v", failedCalls, healthyCalls, fetcher.unsupported)
			}
		})
	}
}

// Captured from pinned Reth v2.9.0 on the isolated Preview payment genesis.
// Storage keys, values and reads use JSON quantities, not fixed-width hashes.
func TestRethBALTransportsAuthenticateIdentically(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile("testdata/reth-bal/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var header types.Header
	if err := json.Unmarshal(read("header"), &header); err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Hash common.Hash `json:"hash"`
	}
	if err := json.Unmarshal(read("header"), &identity); err != nil || identity.Hash != header.Hash() {
		t.Fatalf("header identity: %v", err)
	}
	block := types.NewBlockWithHeader(&header)
	raw, err := DecodeBAL(read("raw"), BALRawMethod, block)
	if err != nil {
		t.Fatal(err)
	}
	list, err := DecodeBAL(read("list"), BALJSONMethod, block)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.List, list.List) {
		t.Fatal("Reth transports disagree")
	}
}
