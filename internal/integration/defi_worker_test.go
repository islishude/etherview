//go:build integration

package integration_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/defi"
	"github.com/islishude/etherview/internal/deficontract"
	"github.com/islishude/etherview/internal/defiread"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

type defiWorkerRPC struct {
	mu                           sync.Mutex
	hash                         common.Hash
	factory, pair, owner, t0, t1 common.Address
	calls                        int
	balance                      int64
	reserve                      int64
	failBalance                  bool
}

func (f *defiWorkerRPC) GetCode(_ common.Address, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if block.BlockHash == nil || *block.BlockHash != f.hash || !block.RequireCanonical {
		return nil, errors.New("wrong code snapshot")
	}
	return []byte{1}, nil
}
func (f *defiWorkerRPC) Call(request map[string]json.RawMessage, block rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if block.BlockHash == nil || *block.BlockHash != f.hash || !block.RequireCanonical {
		return nil, errors.New("wrong state snapshot")
	}
	var input hexutil.Bytes
	var to common.Address
	if json.Unmarshal(request["data"], &input) != nil || json.Unmarshal(request["to"], &to) != nil || len(input) < 4 {
		return nil, errors.New("invalid call")
	}
	selector := hexutil.Encode(input[:4])
	is := func(sig string) bool { return selector == hexutil.Encode(crypto.Keccak256([]byte(sig))[:4]) }
	word := func(n int64) []byte { return big.NewInt(n).FillBytes(make([]byte, 32)) }
	addr := func(a common.Address) []byte { return common.LeftPadBytes(a[:], 32) }
	switch {
	case is("allPairsLength()"), is("pool_count()"):
		return word(1), nil
	case is("allPairs(uint256)"), is("getPair(address,address)"), is("pool_list(uint256)"):
		return addr(f.pair), nil
	case is("get_lp_token(address)"):
		return addr(f.t0), nil
	case is("factory()"):
		return addr(f.factory), nil
	case is("token0()"):
		return addr(f.t0), nil
	case is("token1()"):
		return addr(f.t1), nil
	case is("totalSupply()"):
		return word(100), nil
	case is("getReserves()"):
		return append(append(word(f.reserve), word(2000)...), word(1)...), nil
	case is("balanceOf(address)"):
		if f.failBalance {
			return nil, errors.New("temporary state failure")
		}
		if common.BytesToAddress(input[4:]) != f.owner {
			return word(0), nil
		}
		return word(f.balance), nil
	case is("decimals()"):
		return word(18), nil
	}
	return nil, errors.New("unknown selector")
}
func TestDeFiWorkerPublishesBeforeHTTPAndRefreshesIdleHoldings(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, e := store.NewPostgresRepository(db)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.ConfigureIndex(ctx, "1", 0); e != nil {
		t.Fatal(e)
	}
	service := &defiWorkerRPC{factory: testAddress(91), pair: testAddress(92), owner: testAddress(93), t0: testAddress(94), t1: testAddress(95), balance: 10, reserve: 1000}
	block := derivedTokenBundle(t, 0, testHash(901), testHash(0), testHash(902), "defi-worker", service.pair, common.Address{}, service.owner, 10)
	if _, e = repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); e != nil {
		t.Fatal(e)
	}
	service.hash = block.Block.Hash()
	if _, e = db.Exec(ctx, `UPDATE chains SET genesis_hash=$1 WHERE chain_id=1`, service.hash.Bytes()); e != nil {
		t.Fatal(e)
	}
	manifest := defi.Manifest{Version: 1, ChainID: "1", Genesis: service.hash, Deployments: []defi.Deployment{{ID: "v2", Protocol: deficontract.UniswapV2, StartHash: service.hash, Contracts: []defi.Contract{{Role: "factory", Address: service.factory, CodeHash: crypto.Keccak256Hash([]byte{1})}}}}}
	server := rpc.NewServer()
	if e = server.RegisterName("eth", service); e != nil {
		t.Fatal(e)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	pool, e := ethrpc.NewPool([]ethrpc.Endpoint{{Name: "defi", Client: client, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true}}}, ethrpc.PoolOptions{})
	if e != nil {
		t.Fatal(e)
	}
	source := manifest.Digest() + ":v2"
	reader := &defiread.Reader{DB: db, Digest: manifest.Digest(), Sources: []defiread.Source{{ID: source, Protocol: deficontract.UniswapV2, Deployment: "v2"}}}
	run := func() {
		for range 12 {
			if _, e = db.Exec(ctx, `UPDATE defi_sources SET next_run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, source); e != nil {
				t.Fatal(e)
			}
			// Construct a fresh worker for every step to exercise durable restart progress.
			restarted := &defi.Worker{DB: db, Pool: pool, Manifest: manifest}
			if e = restarted.Step(ctx, manifest.Deployments[0]); e != nil {
				t.Fatal(e)
			}
		}
	}
	// A deployment in a future block must remain pending, even after many attempts.
	future := manifest
	future.Deployments = append([]defi.Deployment{}, manifest.Deployments...)
	future.Deployments[0].Start = 1
	future.Deployments[0].StartHash = testHash(999)
	pending := &defi.Worker{DB: db, Pool: pool, Manifest: future}
	pendingSource := future.Digest() + ":v2"
	for range 6 {
		if _, e = db.Exec(ctx, `UPDATE defi_sources SET next_run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, pendingSource); e != nil {
			t.Fatal(e)
		}
		if e = pending.Step(ctx, future.Deployments[0]); e != nil {
			t.Fatal(e)
		}
	}
	var failures int
	if e = db.QueryRow(ctx, `SELECT failures FROM defi_sources WHERE id=$1`, pendingSource).Scan(&failures); e != nil || failures != 0 {
		t.Fatalf("pending source exhausted retries: %d, %v", failures, e)
	}
	service.mu.Lock()
	service.failBalance = true
	service.mu.Unlock()
	run()
	failed, err := reader.Positions(ctx, deficontract.Request{Owner: service.owner.Hex()})
	if err != nil || len(failed.Positions) != 1 || failed.Positions[0].State != "unavailable" || len(failed.Positions[0].Assets) != 0 {
		t.Fatalf("partial publication=%+v error=%v", failed, err)
	}
	// Repair the same block without requiring new transfers, a new head or HTTP writes.
	service.mu.Lock()
	service.failBalance = false
	service.mu.Unlock()
	run()
	service.mu.Lock()
	calls := service.calls
	service.mu.Unlock()
	page, e := reader.Positions(ctx, deficontract.Request{Owner: service.owner.Hex()})
	if e != nil || len(page.Positions) != 1 || page.Positions[0].Assets[0].Amount != "100" {
		t.Fatalf("page=%+v error=%v", page, e)
	}
	service.mu.Lock()
	if service.calls != calls {
		t.Fatal("reader issued RPC")
	}
	service.mu.Unlock()
	// No LP transfer: changing pool reserves alone must still update underlying assets.
	next := derivedTokenBundle(t, 1, testHash(903), block.Block.Hash(), testHash(904), "defi-next", testAddress(99), common.Address{}, testAddress(98), 1)
	if _, e = repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{next}); e != nil {
		t.Fatal(e)
	}
	service.mu.Lock()
	service.hash = next.Block.Hash()
	service.reserve = 2000
	service.mu.Unlock()
	run()
	page, e = reader.Positions(ctx, deficontract.Request{Owner: service.owner.Hex()})
	if e != nil || len(page.Positions) != 1 || page.Positions[0].Assets[0].Amount != "200" || page.Positions[0].BlockNumber != "1" {
		t.Fatalf("refresh=%+v error=%v", page, e)
	}
	// Unknown pool accounting cannot establish an empty portfolio, even with no LP transfers.
	curveManifest := manifest
	curveManifest.Deployments = []defi.Deployment{{ID: "curve", Protocol: deficontract.Curve, StartHash: manifest.Genesis, Contracts: []defi.Contract{{Role: "metaregistry", Address: service.factory, CodeHash: crypto.Keccak256Hash([]byte{1})}}}}
	curveSource := curveManifest.Digest() + ":curve"
	for range 12 {
		if _, e = db.Exec(ctx, `UPDATE defi_sources SET next_run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, curveSource); e != nil {
			t.Fatal(e)
		}
		if e = (&defi.Worker{DB: db, Pool: pool, Manifest: curveManifest}).Step(ctx, curveManifest.Deployments[0]); e != nil {
			t.Fatal(e)
		}
	}
	curveReader := &defiread.Reader{DB: db, Digest: curveManifest.Digest(), Sources: []defiread.Source{{ID: curveSource, Protocol: deficontract.Curve, Deployment: "curve"}}}
	unknown, err := curveReader.Positions(ctx, deficontract.Request{Owner: service.owner.Hex(), Protocol: deficontract.Curve})
	if err != nil || len(unknown.Positions) != 0 || len(unknown.Deployments) != 1 || unknown.Deployments[0].State != "partial" {
		t.Fatal("unknown pool treated as complete empty coverage", unknown, err)
	}
	// A persistently unavailable position gets a bounded retry budget per attachment.
	stalled := derivedTokenBundle(t, 2, testHash(905), next.Block.Hash(), testHash(906), "defi-stalled", testAddress(99), common.Address{}, testAddress(98), 1)
	if _, e = repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{stalled}); e != nil {
		t.Fatal(e)
	}
	service.mu.Lock()
	service.hash = stalled.Block.Hash()
	service.failBalance = true
	service.mu.Unlock()
	for range 5 {
		run()
	}
	var publications int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM defi_runs WHERE source_id=$1 AND block_hash=$2 AND state='complete'`, source, stalled.Block.Hash().Bytes()).Scan(&publications); e != nil || publications != 5 {
		t.Fatalf("repair budget: publications=%d error=%v", publications, e)
	}
	service.mu.Lock()
	calls = service.calls
	service.mu.Unlock()
	run()
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.calls != calls {
		t.Fatal("exhausted repair budget kept issuing RPC")
	}

}
