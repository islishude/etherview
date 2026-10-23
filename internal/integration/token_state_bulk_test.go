//go:build integration

package integration_test

import (
	"errors"
	"testing"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/store"
)

func TestNFTBulkCacheMatchesAddressTokenIDPairsAndBatchesOnlyMisses(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(80100), testHash(0), testHash(81100), "nft-bulk-cache")
	commitCanonical(t, t.Context(), repository, genesis)
	snapshot := catalog.Snapshot{ChainID: "1", BlockNumber: "0", BlockHash: genesis.Block.Hash().String()}
	owner, other := testAddress(900), testAddress(901)
	a, b := testAddress(721), testAddress(1155)
	candidate := func(standard string, address string, id string) catalog.NFTBalanceCandidate {
		return catalog.NFTBalanceCandidate{Standard: standard, TokenAddress: address, TokenID: id}
	}
	candidates := []catalog.NFTBalanceCandidate{
		candidate("erc721", a.String(), "1"), candidate("erc721", b.String(), "2"),
		candidate("erc1155", a.String(), "1"),
		candidate("erc721", a.String(), "2"), candidate("erc721", b.String(), "1"),
		candidate("erc1155", a.String(), "2"),
		candidate("erc721", b.String(), "3"), candidate("erc1155", b.String(), "3"),
	}
	newReconciler := func(caller *exactNFTCaller, observers ...ethrpc.Observer) *state.TokenReconciler {
		pool := newExactStatePool(t, caller, observers...)
		reconciler, err := state.NewTokenReconciler(db, pool, state.PostgresCanonicalSource{DB: db, ChainID: "1"})
		if err != nil {
			t.Fatal(err)
		}
		return reconciler
	}
	// Distinct facts at paired keys would be conflated by independent IN lists,
	// positional row decoding, or a key that omitted the contract/standard.
	for _, seed := range []struct {
		caller *exactNFTCaller
		batch  []catalog.NFTBalanceCandidate
	}{
		{&exactNFTCaller{owner: owner, erc1155Balance: "7"}, candidates[:3]},
		{&exactNFTCaller{owner: other, erc1155Balance: "9"}, candidates[3:6]},
	} {
		if _, err := newReconciler(seed.caller).Balances(t.Context(), snapshot, owner.String(), seed.batch); err != nil {
			t.Fatal(err)
		}
	}
	caller, observer := &exactNFTCaller{owner: owner, erc1155Balance: "11"}, &exactNFTObserver{}
	got, err := newReconciler(caller, observer).Balances(t.Context(), snapshot, owner.String(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "1", "7", "0", "0", "9", "1", "11"}
	assertBalances := func(got []catalog.NFTBalanceObservation, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("balances=%+v", got)
		}
		for index, balance := range want {
			if got[index].Balance != balance || got[index].Confidence != catalog.NFTStateConfidenceRPCExact {
				t.Fatalf("balance[%d]=%+v, want %s", index, got[index], balance)
			}
		}
	}
	assertBalances(got, want)
	if caller.calls != 2 || len(observer.observations) != 1 || observer.observations[0].BatchSize != 2 {
		t.Fatalf("RPC calls=%d observations=%+v", caller.calls, observer.observations)
	}
	for left, right := 0, len(candidates)-1; left < right; left, right = left+1, right-1 {
		candidates[left], candidates[right] = candidates[right], candidates[left]
		want[left], want[right] = want[right], want[left]
	}
	offline := &exactNFTCaller{err: errors.New("cache must survive restart without RPC")}
	got, err = newReconciler(offline).Balances(t.Context(), snapshot, owner.String(), candidates)
	if err != nil {
		t.Fatal(err)
	}
	assertBalances(got, want)
	if offline.calls != 0 {
		t.Fatalf("cached batch made %d RPC calls", offline.calls)
	}
}
