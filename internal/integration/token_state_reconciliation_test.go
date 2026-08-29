//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
	"github.com/islishude/etherview/internal/state"
	"github.com/islishude/etherview/internal/store"
)

func TestEtherscanERC20HoldingsUseExactPostgresCache(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}

	genesis := testBundle(0, testHash(150_000), testHash(0), testHash(151_000), "etherscan-holding-genesis")
	tip := testBundle(1, testHash(150_001), testHash(150_000), testHash(151_001), "etherscan-holding-tip")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, tip}); err != nil {
		t.Fatal(err)
	}
	markTokenStageComplete(t, ctx, db, genesis)
	markTokenStageComplete(t, ctx, db, tip)

	contract, owner := testAddress(3), testAddress(150_100)
	insertTokenObservation(t, ctx, db, tip, contract, testHash(150_200), "Holding Token")
	reference := mustBlockRef(t, tip)
	transactionHash := tip.Block.Transactions()[0].Hash()
	execFixture(t, ctx, db, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, amount, canonical, confidence, raw
		) VALUES (1, 1, $1, 0, 0, $2, $3, 'erc20', 'mint',
			$4, $5, 7, TRUE, 'high', '{}'::jsonb)`,
		mustBytes(t, reference.Hash), mustBytes(t, transactionHash), mustBytes(t, contract),
		make([]byte, common.AddressLength), mustBytes(t, owner),
	)
	execFixture(t, ctx, db, `
		INSERT INTO token_balance_deltas (
			chain_id, block_number, block_hash, log_index, sub_index,
			token_address, owner_address, token_id, delta, canonical
		) VALUES (1, 1, $1, 0, 0, $2, $3, NULL, 7, TRUE)`,
		mustBytes(t, reference.Hash), mustBytes(t, contract), mustBytes(t, owner),
	)

	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	caller := &exactERC20Caller{balances: map[common.Address]string{contract: "7"}}
	reconciler, err := state.NewNFTReconciler(db, newERC20StatePool(t, caller), canonical)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
		ChainID: 1, ERC20State: reconciler,
	})
	if err != nil {
		t.Fatal(err)
	}
	transfers, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "tokentx",
		Values: url.Values{
			"contractaddress": {contract.String()}, "to": {owner.String()},
			"fromto_opr": {"and"}, "startblock": {"0"}, "endblock": {"latest"},
		},
	})
	if err != nil {
		t.Fatalf("query advanced ERC-20 transfers: %v", err)
	}
	transferRows := etherscanResultRows(t, transfers)
	if len(transferRows) != 1 || transferRows[0]["contractAddress"] != contract.String() ||
		transferRows[0]["to"] != owner.String() || transferRows[0]["value"] != "7" {
		t.Fatalf("advanced ERC-20 transfers=%#v", transferRows)
	}
	result, err := backend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "addresstokenbalance",
		Values: url.Values{"address": {owner.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := etherscanResultRows(t, result)
	if len(rows) != 1 || rows[0]["TokenAddress"] != contract.String() ||
		rows[0]["TokenQuantity"] != "7" || rows[0]["TokenName"] != "Holding Token" {
		t.Fatalf("exact compatibility holdings=%#v", rows)
	}
	if caller.calls != 1 {
		t.Fatalf("exact compatibility holding RPC calls=%d, want 1", caller.calls)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM erc20_balance_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND owner_address = $2 AND block_hash = $3`,
		1, mustBytes(t, contract), mustBytes(t, owner), mustBytes(t, reference.Hash),
	)

	failing := &exactERC20Caller{err: errors.New("cached compatibility holding must not call RPC")}
	cached, err := state.NewNFTReconciler(db, newERC20StatePool(t, failing), canonical)
	if err != nil {
		t.Fatal(err)
	}
	cachedBackend, err := etherscan.NewPostgresBackend(db, etherscan.PostgresOptions{
		ChainID: 1, ERC20State: cached,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cachedBackend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "addresstokenbalance",
		Values: url.Values{"address": {owner.String()}},
	}); err != nil {
		t.Fatalf("read cached compatibility holding: %v", err)
	}
	if failing.calls != 0 {
		t.Fatalf("cached compatibility holding made %d RPC calls", failing.calls)
	}

	next := testBundle(2, testHash(160_002), reference.Hash, testHash(161_002), "etherscan-holding-reclassified")
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{next}); err != nil {
		t.Fatal(err)
	}
	markTokenStageComplete(t, ctx, db, next)
	nextReference := mustBlockRef(t, next)
	execFixture(t, ctx, db, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence,
			name, symbol, decimals, total_supply, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (1, $1, $2, 'unknown', 'high',
			NULL, NULL, NULL, NULL, 'complete', 2, $3)`,
		mustBytes(t, contract), mustBytes(t, testHash(160_200)), mustBytes(t, nextReference.Hash),
	)
	if _, err := cachedBackend.Execute(ctx, etherscan.Request{
		Module: "account", Action: "addresstokenbalance",
		Values: url.Values{"address": {owner.String()}},
	}); !errors.Is(err, etherscan.ErrNotFound) {
		t.Fatalf("reclassified compatibility holding error=%v, want no records", err)
	}
	if failing.calls != 0 {
		t.Fatalf("reclassified token reused stale ERC-20 classification; RPC calls=%d", failing.calls)
	}
}

func TestExactNFTObservationsRejectConcurrentConflictsAndPreserveIdenticalWrites(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(110_000), testHash(0), testHash(111_000), "immutable-nft-genesis")
	tip := testBundle(1, testHash(110_001), testHash(110_000), testHash(111_001), "immutable-nft-tip")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, tip)
	reference := mustBlockRef(t, tip)
	snapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(reference.Number), BlockHash: reference.Hash.String(),
	}
	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	newReconciler := func(service any) *state.NFTReconciler {
		reconciler, reconcileErr := state.NewNFTReconciler(db, newNFTStatePool(t, service), canonical)
		if reconcileErr != nil {
			t.Fatal(reconcileErr)
		}
		return reconciler
	}

	contract721 := testAddress(1_721)
	firstOwner, conflictingOwner := testAddress(1_001), testAddress(1_002)
	blockedOwner := newGatedExactNFTCaller(&exactNFTCaller{owner: conflictingOwner})
	conflictingReconciler := newReconciler(blockedOwner)
	type ownerResult struct {
		observation catalog.NFTOwnerObservation
		err         error
	}
	conflictingResult := make(chan ownerResult, 1)
	go func() {
		observation, ownerErr := conflictingReconciler.Owner(ctx, snapshot, contract721.String(), "42")
		conflictingResult <- ownerResult{observation: observation, err: ownerErr}
	}()
	blockedOwner.waitUntilStarted(t, ctx)

	firstReconciler := newReconciler(&exactNFTCaller{owner: firstOwner})
	firstObservation, err := firstReconciler.Owner(ctx, snapshot, contract721.String(), "42")
	if err != nil || !firstObservation.Exists {
		t.Fatalf("persist first exact ERC-721 owner=%+v error=%v", firstObservation, err)
	}
	blockedOwner.releaseCall()
	second := <-conflictingResult
	if !errors.Is(second.err, state.ErrExactNFTObservationConflict) {
		t.Fatalf("conflicting ERC-721 observation=%+v error=%v", second.observation, second.err)
	}

	var storedOwner []byte
	if err := db.QueryRowContext(ctx, `
		SELECT owner_address FROM erc721_owner_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 42 AND block_hash = $2`,
		mustBytes(t, contract721), mustBytes(t, reference.Hash),
	).Scan(&storedOwner); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedOwner, mustBytes(t, firstOwner)) {
		t.Fatalf("stored ERC-721 owner=%x, want first immutable owner", storedOwner)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE erc721_owner_reconciliations SET owner_address = $1
		WHERE chain_id = 1 AND token_address = $2 AND token_id = 42 AND block_hash = $3`,
		mustBytes(t, conflictingOwner), mustBytes(t, contract721), mustBytes(t, reference.Hash),
	); err == nil {
		t.Fatal("direct mutation of an exact ERC-721 observation succeeded")
	}

	contract1155, balanceOwner := testAddress(2_115), testAddress(2_001)
	blockedBalance := newGatedExactNFTCaller(&exactNFTCaller{erc1155Balance: "9"})
	conflictingBalanceReconciler := newReconciler(blockedBalance)
	type balanceResult struct {
		observations []catalog.NFTBalanceObservation
		err          error
	}
	conflictingBalance := make(chan balanceResult, 1)
	candidate := []catalog.NFTBalanceCandidate{{
		Standard: "erc1155", TokenAddress: contract1155.String(), TokenID: "7",
	}}
	go func() {
		observations, balanceErr := conflictingBalanceReconciler.Balances(ctx, snapshot, balanceOwner.String(), candidate)
		conflictingBalance <- balanceResult{observations: observations, err: balanceErr}
	}()
	blockedBalance.waitUntilStarted(t, ctx)
	firstBalanceReconciler := newReconciler(&exactNFTCaller{erc1155Balance: "7"})
	firstBalances, err := firstBalanceReconciler.Balances(ctx, snapshot, balanceOwner.String(), candidate)
	if err != nil || len(firstBalances) != 1 || firstBalances[0].Balance != "7" {
		t.Fatalf("persist first exact ERC-1155 balance=%+v error=%v", firstBalances, err)
	}
	blockedBalance.releaseCall()
	secondBalance := <-conflictingBalance
	if !errors.Is(secondBalance.err, state.ErrExactNFTObservationConflict) {
		t.Fatalf("conflicting ERC-1155 observations=%+v error=%v", secondBalance.observations, secondBalance.err)
	}
	var storedBalance string
	if err := db.QueryRowContext(ctx, `
		SELECT balance::text FROM erc1155_balance_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 7
		  AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, contract1155), mustBytes(t, balanceOwner), mustBytes(t, reference.Hash),
	).Scan(&storedBalance); err != nil {
		t.Fatal(err)
	}
	if storedBalance != "7" {
		t.Fatalf("stored ERC-1155 balance=%s, want first immutable balance", storedBalance)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE erc1155_balance_reconciliations SET balance = 9
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 7
		  AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, contract1155), mustBytes(t, balanceOwner), mustBytes(t, reference.Hash),
	); err == nil {
		t.Fatal("direct mutation of an exact ERC-1155 observation succeeded")
	}

	// Force two identical observations to miss the cache before either write.
	// The second INSERT must take the conditional no-op path without changing
	// the first observation's audit timestamp.
	identicalTokenID := "43"
	blockedIdentical := newGatedExactNFTCaller(&exactNFTCaller{owner: firstOwner})
	identicalReconciler := newReconciler(blockedIdentical)
	identicalResult := make(chan ownerResult, 1)
	go func() {
		observation, ownerErr := identicalReconciler.Owner(ctx, snapshot, contract721.String(), identicalTokenID)
		identicalResult <- ownerResult{observation: observation, err: ownerErr}
	}()
	blockedIdentical.waitUntilStarted(t, ctx)
	if _, err := firstReconciler.Owner(ctx, snapshot, contract721.String(), identicalTokenID); err != nil {
		t.Fatalf("persist first identical ERC-721 observation: %v", err)
	}
	var firstObservedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT observed_at FROM erc721_owner_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 43 AND block_hash = $2`,
		mustBytes(t, contract721), mustBytes(t, reference.Hash),
	).Scan(&firstObservedAt); err != nil {
		t.Fatal(err)
	}
	blockedIdentical.releaseCall()
	identical := <-identicalResult
	if identical.err != nil || !identical.observation.Exists {
		t.Fatalf("second identical ERC-721 observation=%+v error=%v", identical.observation, identical.err)
	}
	var secondObservedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT observed_at FROM erc721_owner_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND token_id = 43 AND block_hash = $2`,
		mustBytes(t, contract721), mustBytes(t, reference.Hash),
	).Scan(&secondObservedAt); err != nil {
		t.Fatal(err)
	}
	if !secondObservedAt.Equal(firstObservedAt) {
		t.Fatalf("identical observation changed observed_at from %s to %s", firstObservedAt, secondObservedAt)
	}
}

func TestExactERC20BalanceObservationsRejectConcurrentConflictsAndPreserveIdenticalWrites(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(120_000), testHash(0), testHash(121_000), "immutable-erc20-genesis")
	tip := testBundle(1, testHash(120_001), testHash(120_000), testHash(121_001), "immutable-erc20-tip")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, tip)
	reference := mustBlockRef(t, tip)
	snapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(reference.Number), BlockHash: reference.Hash.String(),
	}
	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	newReconciler := func(service any) *state.NFTReconciler {
		reconciler, reconcileErr := state.NewNFTReconciler(db, newERC20StatePool(t, service), canonical)
		if reconcileErr != nil {
			t.Fatal(reconcileErr)
		}
		return reconciler
	}

	contract, owner := testAddress(20_001), testAddress(20_002)
	candidates := []catalog.ERC20BalanceCandidate{{TokenAddress: contract.String()}}
	blocked := newGatedExactERC20Caller(&exactERC20Caller{balance: "9"})
	type balanceResult struct {
		observations []catalog.ERC20BalanceObservation
		err          error
	}
	conflicting := make(chan balanceResult, 1)
	go func() {
		observations, balanceErr := newReconciler(blocked).ERC20Balances(ctx, snapshot, owner.String(), candidates)
		conflicting <- balanceResult{observations: observations, err: balanceErr}
	}()
	blocked.waitUntilStarted(t, ctx)
	first, err := newReconciler(&exactERC20Caller{balance: "7"}).ERC20Balances(ctx, snapshot, owner.String(), candidates)
	if err != nil || len(first) != 1 || first[0].Balance != "7" {
		t.Fatalf("persist first exact ERC-20 balance=%+v error=%v", first, err)
	}
	blocked.releaseCall()
	second := <-conflicting
	if !errors.Is(second.err, state.ErrExactERC20BalanceObservationConflict) {
		t.Fatalf("conflicting ERC-20 balance=%+v error=%v", second.observations, second.err)
	}
	var storedBalance string
	if err := db.QueryRowContext(ctx, `
		SELECT balance::text FROM erc20_balance_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, contract), mustBytes(t, owner), mustBytes(t, reference.Hash),
	).Scan(&storedBalance); err != nil {
		t.Fatal(err)
	}
	if storedBalance != "7" {
		t.Fatalf("stored ERC-20 balance=%s, want first immutable balance", storedBalance)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE erc20_balance_reconciliations SET balance = 9
		WHERE chain_id = 1 AND token_address = $1 AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, contract), mustBytes(t, owner), mustBytes(t, reference.Hash),
	); err == nil {
		t.Fatal("direct mutation of an exact ERC-20 balance observation succeeded")
	}

	identicalContract := testAddress(20_003)
	identicalCandidates := []catalog.ERC20BalanceCandidate{{TokenAddress: identicalContract.String()}}
	blockedIdentical := newGatedExactERC20Caller(&exactERC20Caller{balance: "11"})
	identicalResult := make(chan balanceResult, 1)
	go func() {
		observations, balanceErr := newReconciler(blockedIdentical).ERC20Balances(
			ctx, snapshot, owner.String(), identicalCandidates,
		)
		identicalResult <- balanceResult{observations: observations, err: balanceErr}
	}()
	blockedIdentical.waitUntilStarted(t, ctx)
	if _, err := newReconciler(&exactERC20Caller{balance: "11"}).ERC20Balances(
		ctx, snapshot, owner.String(), identicalCandidates,
	); err != nil {
		t.Fatalf("persist first identical ERC-20 balance: %v", err)
	}
	var firstObservedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT observed_at FROM erc20_balance_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, identicalContract), mustBytes(t, owner), mustBytes(t, reference.Hash),
	).Scan(&firstObservedAt); err != nil {
		t.Fatal(err)
	}
	blockedIdentical.releaseCall()
	identical := <-identicalResult
	if identical.err != nil || len(identical.observations) != 1 || identical.observations[0].Balance != "11" {
		t.Fatalf("second identical ERC-20 balance=%+v error=%v", identical.observations, identical.err)
	}
	var secondObservedAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT observed_at FROM erc20_balance_reconciliations
		WHERE chain_id = 1 AND token_address = $1 AND owner_address = $2 AND block_hash = $3`,
		mustBytes(t, identicalContract), mustBytes(t, owner), mustBytes(t, reference.Hash),
	).Scan(&secondObservedAt); err != nil {
		t.Fatal(err)
	}
	if !secondObservedAt.Equal(firstObservedAt) {
		t.Fatalf("identical ERC-20 observation changed observed_at from %s to %s", firstObservedAt, secondObservedAt)
	}
}

func TestExactERC20BalanceCacheSurvivesRestartAndRejectsOrphanedSnapshots(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(130_000), testHash(0), testHash(131_000), "erc20-cache-genesis")
	oldTip := testBundle(1, testHash(130_001), testHash(130_000), testHash(131_001), "erc20-cache-old")
	replacement := testBundle(1, testHash(140_001), testHash(130_000), testHash(141_001), "erc20-cache-new")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, oldTip)
	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	owner := testAddress(30_001)
	contracts := []common.Address{testAddress(30_002), testAddress(30_003)}
	candidates := []catalog.ERC20BalanceCandidate{
		{TokenAddress: contracts[0].String()}, {TokenAddress: contracts[1].String()},
	}
	oldReference := mustBlockRef(t, oldTip)
	oldSnapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(oldReference.Number), BlockHash: oldReference.Hash.String(),
	}
	firstCaller := &exactERC20Caller{balances: map[common.Address]string{
		contracts[0]: "7", contracts[1]: "0",
	}}
	observer := &exactNFTObserver{}
	first, err := state.NewNFTReconciler(db, newERC20StatePool(t, firstCaller, observer), canonical)
	if err != nil {
		t.Fatal(err)
	}
	balances, err := first.ERC20Balances(ctx, oldSnapshot, owner.String(), candidates)
	if err != nil || len(balances) != 2 || balances[0].Balance != "7" || balances[1].Balance != "0" {
		t.Fatalf("first exact ERC-20 balances=%+v error=%v", balances, err)
	}
	if len(observer.observations) != 1 || observer.observations[0].BatchSize != 2 || firstCaller.calls != 2 {
		t.Fatalf("first exact ERC-20 RPC observations=%+v calls=%d", observer.observations, firstCaller.calls)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc20_balance_reconciliations WHERE block_hash = $1`, 2, mustBytes(t, oldReference.Hash))

	compatibilityCaller := &exactERC20Caller{balance: "9"}
	compatibilityReader := &state.Reader{
		Pool: newERC20StatePool(t, compatibilityCaller), Canonical: canonical,
	}
	compatibilityBalance, err := compatibilityReader.ERC20Balance(ctx, contracts[0].String(), owner.String())
	if err != nil || compatibilityBalance != "9" || compatibilityCaller.calls != 1 {
		t.Fatalf("compatibility ERC-20 balance=%q calls=%d error=%v", compatibilityBalance, compatibilityCaller.calls, err)
	}

	failing := &exactERC20Caller{err: errors.New("RPC must not be called for cached exact ERC-20 state")}
	cached, err := state.NewNFTReconciler(db, newERC20StatePool(t, failing), canonical)
	if err != nil {
		t.Fatal(err)
	}
	cachedBalances, err := cached.ERC20Balances(ctx, oldSnapshot, owner.String(), candidates)
	if err != nil || len(cachedBalances) != 2 || cachedBalances[0].Balance != "7" || cachedBalances[1].Balance != "0" {
		t.Fatalf("cached exact ERC-20 balances=%+v error=%v", cachedBalances, err)
	}
	if failing.calls != 0 {
		t.Fatalf("cached ERC-20 reconciliation made %d RPC calls", failing.calls)
	}

	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{oldTip}, []chainbundle.Bundle{replacement}, "orphan ERC-20 balance cache")
	newReference := mustBlockRef(t, replacement)
	newSnapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(newReference.Number), BlockHash: newReference.Hash.String(),
	}
	if _, err := cached.ERC20Balances(ctx, newSnapshot, owner.String(), candidates); !errors.Is(err, httpapi.ErrUnavailable) {
		t.Fatalf("replacement exact ERC-20 balance error=%v, want unavailable", err)
	}
	if failing.calls != 2 {
		t.Fatalf("orphan ERC-20 observations were reused; RPC calls=%d", failing.calls)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc20_balance_reconciliations WHERE block_hash = $1`, 2, mustBytes(t, oldReference.Hash))

	replacementCaller := &exactERC20Caller{balances: map[common.Address]string{
		contracts[0]: "8", contracts[1]: "1",
	}}
	replacementReconciler, err := state.NewNFTReconciler(
		db, newERC20StatePool(t, replacementCaller), canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	replacementBalances, err := replacementReconciler.ERC20Balances(ctx, newSnapshot, owner.String(), candidates)
	if err != nil || len(replacementBalances) != 2 || replacementBalances[0].Balance != "8" || replacementBalances[1].Balance != "1" {
		t.Fatalf("replacement exact ERC-20 balances=%+v error=%v", replacementBalances, err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc20_balance_reconciliations`, 4)
}

func TestTokenObservationsAndExactNFTStateSurviveRealPostgresReorg(t *testing.T) {
	db := newMigratedPostgres(t)
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	genesis := testBundle(0, testHash(80_000), testHash(0), testHash(81_000), "token-genesis")
	oldTip := testBundle(1, testHash(80_001), testHash(80_000), testHash(81_001), "token-old")
	replacement := testBundle(1, testHash(90_001), testHash(80_000), testHash(91_001), "token-new")
	thirdTip := testBundle(1, testHash(100_001), testHash(80_000), testHash(101_001), "token-third")
	commitCanonical(t, ctx, repository, genesis)
	commitCanonical(t, ctx, repository, oldTip)
	markTokenStageComplete(t, ctx, db, oldTip)

	contract, codeHash := testAddress(700), testHash(70_000)
	insertTokenObservation(t, ctx, db, genesis, contract, codeHash, "Genesis observation")
	insertTokenObservation(t, ctx, db, oldTip, contract, codeHash, "Old-tip observation")
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM token_contracts
		WHERE chain_id = 1 AND address = $1 AND code_hash = $2`,
		2, mustBytes(t, contract), mustBytes(t, codeHash),
	)

	reader, err := catalog.NewPostgres(db, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	current, err := reader.TokenContract(ctx, "1", contract.String())
	if err != nil || current.Name == nil || *current.Name != "Old-tip observation" || current.ObservedBlockNumber != "1" {
		t.Fatalf("old-tip token observation=%+v error=%v", current, err)
	}

	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{oldTip}, []chainbundle.Bundle{replacement}, "token observation reorg")
	markTokenStageComplete(t, ctx, db, replacement)
	current, err = reader.TokenContract(ctx, "1", contract.String())
	if err != nil || current.Name == nil || *current.Name != "Genesis observation" || current.ObservedBlockNumber != "0" {
		t.Fatalf("post-reorg token observation=%+v error=%v", current, err)
	}

	owner := testAddress(900)
	erc721, erc1155 := testAddress(721), testAddress(1155)
	rpc := &exactNFTCaller{owner: owner, erc1155Balance: "7"}
	observer := &exactNFTObserver{}
	pool := newNFTStatePool(t, rpc, observer)
	canonical := state.PostgresCanonicalSource{DB: db, ChainID: "1"}
	reconciler, err := state.NewNFTReconciler(db, pool, canonical)
	if err != nil {
		t.Fatal(err)
	}
	reference := mustBlockRef(t, replacement)
	snapshot := catalog.Snapshot{
		ChainID: "1", BlockNumber: fmt.Sprint(reference.Number), BlockHash: reference.Hash.String(),
	}
	balances, err := reconciler.Balances(ctx, snapshot, owner.String(), []catalog.NFTBalanceCandidate{
		{Standard: "erc721", TokenAddress: erc721.String(), TokenID: "42"},
		{Standard: "erc1155", TokenAddress: erc1155.String(), TokenID: "9"},
	})
	if err != nil || len(balances) != 2 || balances[0].Balance != "1" || balances[1].Balance != "7" ||
		balances[0].Confidence != catalog.NFTStateConfidenceRPCExact || balances[1].Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("exact balances=%+v error=%v", balances, err)
	}
	if rpc.calls != 2 {
		t.Fatalf("exact NFT RPC calls=%d, want ownerOf plus balanceOf", rpc.calls)
	}
	if len(observer.observations) != 1 || observer.observations[0].Method != "eth_call" ||
		observer.observations[0].BatchSize != 2 || observer.observations[0].SuccessCount != 2 ||
		observer.observations[0].ErrorCount != 0 {
		t.Fatalf("exact NFT RPC observations=%+v", observer.observations)
	}
	ownership, err := reconciler.Owner(ctx, snapshot, erc721.String(), "42")
	if err != nil || !ownership.Exists || ownership.Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("cached exact owner=%+v error=%v", ownership, err)
	}
	if rpc.calls != 2 || len(observer.observations) != 1 {
		t.Fatalf("cached exact owner made an RPC call: calls=%d observations=%+v", rpc.calls, observer.observations)
	}
	for _, selector := range rpc.selectors {
		if selector["blockHash"] != reference.Hash.String() || selector["requireCanonical"] != true {
			t.Fatalf("NFT selector=%#v", selector)
		}
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc721_owner_reconciliations WHERE block_hash = $1`, 1, mustBytes(t, reference.Hash))
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc1155_balance_reconciliations WHERE block_hash = $1`, 1, mustBytes(t, reference.Hash))

	// A fresh process can serve the exact canonical snapshot from PostgreSQL;
	// it does not need the RPC endpoint again.
	failing := &exactNFTCaller{err: errors.New("RPC must not be called for cached exact state")}
	cached, err := state.NewNFTReconciler(db, newNFTStatePool(t, failing), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Owner(ctx, snapshot, erc721.String(), "42"); err != nil {
		t.Fatalf("cached owner: %v", err)
	}
	if _, err := cached.Balances(ctx, snapshot, owner.String(), []catalog.NFTBalanceCandidate{
		{Standard: "erc721", TokenAddress: erc721.String(), TokenID: "42"},
		{Standard: "erc1155", TokenAddress: erc1155.String(), TokenID: "9"},
	}); err != nil {
		t.Fatalf("cached balances: %v", err)
	}
	if failing.calls != 0 {
		t.Fatalf("cached reconciliation made %d RPC calls", failing.calls)
	}

	applyDerivedReorg(t, ctx, repository, genesis, []chainbundle.Bundle{replacement}, []chainbundle.Bundle{thirdTip}, "orphan exact NFT observations")
	if _, err := cached.Owner(ctx, snapshot, erc721.String(), "42"); !errors.Is(err, httpapi.ErrUnavailable) {
		t.Fatalf("orphan cached owner error=%v, want unavailable after fresh RPC failure", err)
	}
	if failing.calls != 1 {
		t.Fatalf("orphan exact observation was reused; RPC calls=%d", failing.calls)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM erc721_owner_reconciliations WHERE block_hash = $1`, 1, mustBytes(t, reference.Hash))
}

func markTokenStageComplete(t *testing.T, ctx context.Context, db *sql.DB, block chainbundle.Bundle) {
	t.Helper()
	reference := mustBlockRef(t, block)
	if _, err := db.ExecContext(ctx, `
		UPDATE transactional_outbox
		SET published_at = clock_timestamp()
		WHERE chain_id = 1 AND topic = 'core.block.canonical' AND message_key = $1`,
		reference.Hash.String(),
	); err != nil {
		t.Fatalf("acknowledge token block outbox: %v", err)
	}
	word, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: reference.Number,
	}); err != nil {
		t.Fatal(err)
	}
	lease, found, err := queue.Claim(ctx, "token-state-fixture", []enrich.StageID{enrich.TokenStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim token state stage=%+v found=%t err=%v", lease, found, err)
	}
	processor, err := enrich.NewPostgresTokenProcessor(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessLease(ctx, lease, queue); err != nil {
		t.Fatalf("publish token state stage: %v", err)
	}
}

func insertTokenObservation(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	block chainbundle.Bundle,
	contract common.Address,
	codeHash common.Hash,
	name string,
) {
	t.Helper()
	reference := mustBlockRef(t, block)
	execFixture(t, ctx, db, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence,
			name, symbol, decimals, total_supply, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (
			1, $1, $2, 'erc20', 'high',
			$3, 'TOK', 18, 1000, 'complete',
			$4, $5
		)`,
		mustBytes(t, contract), mustBytes(t, codeHash), name,
		reference.Number, mustBytes(t, reference.Hash),
	)
}

type exactNFTCaller struct {
	owner          common.Address
	erc1155Balance string
	err            error
	calls          int
	selectors      []map[string]any
}

type exactERC20Caller struct {
	balance  string
	balances map[common.Address]string
	err      error
	calls    int
}

type gatedExactERC20Caller struct {
	delegate *exactERC20Caller
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newGatedExactERC20Caller(delegate *exactERC20Caller) *gatedExactERC20Caller {
	return &gatedExactERC20Caller{
		delegate: delegate,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (caller *gatedExactERC20Caller) Call(
	ctx context.Context,
	request map[string]any,
	selector rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	caller.once.Do(func() { close(caller.started) })
	select {
	case <-caller.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return caller.delegate.Call(ctx, request, selector)
}

func (caller *gatedExactERC20Caller) waitUntilStarted(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-caller.started:
	case <-ctx.Done():
		t.Fatalf("wait for gated exact ERC-20 call: %v", ctx.Err())
	}
}

func (caller *gatedExactERC20Caller) releaseCall() { close(caller.release) }

func (caller *exactERC20Caller) Call(
	_ context.Context,
	request map[string]any,
	selector rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	caller.calls++
	if caller.err != nil {
		return nil, caller.err
	}
	if selector.BlockHash == nil || !selector.RequireCanonical {
		return nil, fmt.Errorf("unexpected exact ERC-20 selector %#v", selector)
	}
	inputText, ok := request["data"].(string)
	if !ok {
		return nil, errors.New("exact ERC-20 calldata is not a hex string")
	}
	input, err := hexutil.Decode(inputText)
	if err != nil {
		return nil, err
	}
	if len(input) != 36 || fmt.Sprintf("%x", input[:4]) != "70a08231" {
		return nil, errors.New("exact ERC-20 calldata is not balanceOf(address)")
	}
	tokenText, ok := request["to"].(string)
	if !ok || !common.IsHexAddress(tokenText) {
		return nil, errors.New("exact ERC-20 call target is invalid")
	}
	balance := caller.balance
	if caller.balances != nil {
		var found bool
		balance, found = caller.balances[common.HexToAddress(tokenText)]
		if !found {
			return nil, errors.New("exact ERC-20 fixture has no token balance")
		}
	}
	value, ok := new(big.Int).SetString(balance, 10)
	if !ok || value.Sign() < 0 {
		return nil, errors.New("invalid fixture ERC-20 balance")
	}
	output := make([]byte, 32)
	value.FillBytes(output)
	return hexutil.Bytes(output), nil
}

type exactNFTObserver struct {
	observations []ethrpc.Observation
}

func (observer *exactNFTObserver) RecordRPC(observation ethrpc.Observation) {
	observer.observations = append(observer.observations, observation)
}

type gatedExactNFTCaller struct {
	delegate *exactNFTCaller
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newGatedExactNFTCaller(delegate *exactNFTCaller) *gatedExactNFTCaller {
	return &gatedExactNFTCaller{
		delegate: delegate,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (caller *gatedExactNFTCaller) Call(
	ctx context.Context,
	request map[string]any,
	selector rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	caller.once.Do(func() { close(caller.started) })
	select {
	case <-caller.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return caller.delegate.Call(ctx, request, selector)
}

func (caller *gatedExactNFTCaller) waitUntilStarted(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-caller.started:
	case <-ctx.Done():
		t.Fatalf("wait for gated exact NFT call: %v", ctx.Err())
	}
}

func (caller *gatedExactNFTCaller) releaseCall() { close(caller.release) }

func (caller *exactNFTCaller) Call(
	_ context.Context,
	request map[string]any,
	selector rpc.BlockNumberOrHash,
) (hexutil.Bytes, error) {
	caller.calls++
	if caller.err != nil {
		return nil, caller.err
	}
	if selector.BlockHash == nil || !selector.RequireCanonical {
		return nil, fmt.Errorf("unexpected exact NFT selector %#v", selector)
	}
	caller.selectors = append(caller.selectors, map[string]any{
		"blockHash":        selector.BlockHash.String(),
		"requireCanonical": selector.RequireCanonical,
	})
	inputText, ok := request["data"].(string)
	if !ok {
		return nil, errors.New("exact NFT calldata is not a hex string")
	}
	input, err := hexutil.Decode(inputText)
	if err != nil {
		return nil, err
	}
	if len(input) < 4 {
		return nil, errors.New("exact NFT calldata is too short")
	}
	output := make([]byte, 32)
	switch fmt.Sprintf("%x", input[:4]) {
	case "6352211e":
		copy(output[12:], caller.owner.Bytes())
	case "00fdd58e":
		balance, ok := new(big.Int).SetString(caller.erc1155Balance, 10)
		if !ok {
			return nil, errors.New("invalid fixture ERC-1155 balance")
		}
		balance.FillBytes(output)
	default:
		return nil, fmt.Errorf("unexpected exact NFT selector 0x%x", input[:4])
	}
	return hexutil.Bytes(output), nil
}

func newNFTStatePool(t *testing.T, service any, observers ...ethrpc.Observer) *ethrpc.Pool {
	t.Helper()
	var observer ethrpc.Observer
	if len(observers) > 0 {
		observer = observers[0]
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "exact-nft-state", Client: newIntegrationRPCClient(t, "eth", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func newERC20StatePool(t *testing.T, service any, observers ...ethrpc.Observer) *ethrpc.Pool {
	t.Helper()
	var observer ethrpc.Observer
	if len(observers) > 0 {
		observer = observers[0]
	}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "exact-erc20-state", Client: newIntegrationRPCClient(t, "eth", service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
